package configstore

import (
	"context"
	"errors"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"gorm.io/gorm"
)

// IdentityStore is optional so deployments without relational persistence keep
// their current behavior. It is used by the Community OIDC/RBAC layer.
type IdentityStore interface {
	ListIdentityUsers(ctx context.Context) ([]tables.TableIdentityUser, error)
	GetIdentityUser(ctx context.Context, id string) (*tables.TableIdentityUser, error)
	UpsertIdentityUser(ctx context.Context, user *tables.TableIdentityUser) error
	DeleteIdentityUser(ctx context.Context, id string) error
	ListIdentityRoles(ctx context.Context) ([]tables.TableIdentityRole, error)
	CreateIdentityRole(ctx context.Context, role *tables.TableIdentityRole) error
	UpdateIdentityRole(ctx context.Context, role *tables.TableIdentityRole) error
	DeleteIdentityRole(ctx context.Context, id string) error
	ListIdentityRoleIDs(ctx context.Context, userID string) ([]string, error)
	ReplaceIdentityUserRoles(ctx context.Context, userID string, roleIDs []string) error
}

// IdentityVirtualKeyAssignmentStore is intentionally separate from IdentityStore
// so external identity-store implementations remain source-compatible when
// personal Virtual Key ownership is enabled in Community deployments.
type IdentityVirtualKeyAssignmentStore interface {
	GetIdentityVirtualKeyAssignment(ctx context.Context, userID string) (*tables.TableIdentityVirtualKeyAssignment, error)
	ReplaceIdentityVirtualKeyAssignment(ctx context.Context, userID string, virtualKeyID *string) error
	ResolveIdentityVirtualKeyAssignees(ctx context.Context, virtualKeyIDs []string) (map[string]*tables.AssignedUser, error)
}

func (s *RDBConfigStore) ListIdentityUsers(ctx context.Context) ([]tables.TableIdentityUser, error) {
	var users []tables.TableIdentityUser
	err := s.DB().WithContext(ctx).Order("name ASC, id ASC").Find(&users).Error
	return users, err
}

func (s *RDBConfigStore) GetIdentityUser(ctx context.Context, id string) (*tables.TableIdentityUser, error) {
	var user tables.TableIdentityUser
	if err := s.DB().WithContext(ctx).First(&user, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &user, nil
}

func (s *RDBConfigStore) UpsertIdentityUser(ctx context.Context, user *tables.TableIdentityUser) error {
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now().UTC()
	}
	user.UpdatedAt = time.Now().UTC()
	return s.DB().WithContext(ctx).Save(user).Error
}

func (s *RDBConfigStore) DeleteIdentityUser(ctx context.Context, id string) error {
	return s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", id).Delete(&tables.TableIdentityVirtualKeyAssignment{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&tables.TableIdentityUserRole{}).Error; err != nil {
			return err
		}
		result := tx.Delete(&tables.TableIdentityUser{}, "id = ?", id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (s *RDBConfigStore) GetIdentityVirtualKeyAssignment(ctx context.Context, userID string) (*tables.TableIdentityVirtualKeyAssignment, error) {
	var assignment tables.TableIdentityVirtualKeyAssignment
	if err := s.DB().WithContext(ctx).First(&assignment, "user_id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &assignment, nil
}

// ReplaceIdentityVirtualKeyAssignment assigns one existing virtual key to one
// user, or clears that user's assignment when virtualKeyID is nil. The unique
// virtual_key_id index is the concurrency-safe guard against sharing a personal
// key between users.
func (s *RDBConfigStore) ReplaceIdentityVirtualKeyAssignment(ctx context.Context, userID string, virtualKeyID *string) error {
	return s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user tables.TableIdentityUser
		if err := dbForUpdate(tx.WithContext(ctx)).First(&user, "id = ?", userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if virtualKeyID == nil || *virtualKeyID == "" {
			return tx.Where("user_id = ?", userID).Delete(&tables.TableIdentityVirtualKeyAssignment{}).Error
		}

		var key tables.TableVirtualKey
		if err := dbForUpdate(tx.WithContext(ctx)).First(&key, "id = ?", *virtualKeyID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		var existing tables.TableIdentityVirtualKeyAssignment
		err := dbForUpdate(tx.WithContext(ctx)).First(&existing, "virtual_key_id = ?", *virtualKeyID).Error
		if err == nil && existing.UserID != userID {
			return ErrAlreadyExists
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&tables.TableIdentityVirtualKeyAssignment{}).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		return tx.Save(&tables.TableIdentityVirtualKeyAssignment{UserID: userID, VirtualKeyID: *virtualKeyID, CreatedAt: now, UpdatedAt: now}).Error
	})
}

// ResolveIdentityVirtualKeyAssignees loads the small projection rendered in the
// virtual-key list in one query. It runs only on dashboard reads, never on the
// inference path.
func (s *RDBConfigStore) ResolveIdentityVirtualKeyAssignees(ctx context.Context, virtualKeyIDs []string) (map[string]*tables.AssignedUser, error) {
	result := make(map[string]*tables.AssignedUser, len(virtualKeyIDs))
	if len(virtualKeyIDs) == 0 {
		return result, nil
	}
	type row struct {
		VirtualKeyID string
		ID           string
		Name         string
		Email        string
	}
	var rows []row
	if err := s.DB().WithContext(ctx).
		Table(tables.TableIdentityVirtualKeyAssignment{}.TableName()+" AS assignment").
		Select("assignment.virtual_key_id, users.id, users.name, users.email").
		Joins("JOIN identity_users AS users ON users.id = assignment.user_id").
		Where("assignment.virtual_key_id IN ?", virtualKeyIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.VirtualKeyID] = &tables.AssignedUser{ID: row.ID, Name: row.Name, Email: row.Email}
	}
	return result, nil
}

func (s *RDBConfigStore) ListIdentityRoles(ctx context.Context) ([]tables.TableIdentityRole, error) {
	var roles []tables.TableIdentityRole
	err := s.DB().WithContext(ctx).Order("is_system DESC, name ASC").Find(&roles).Error
	return roles, err
}

func (s *RDBConfigStore) CreateIdentityRole(ctx context.Context, role *tables.TableIdentityRole) error {
	if err := s.DB().WithContext(ctx).Create(role).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || isUniqueConstraintError(err) {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (s *RDBConfigStore) UpdateIdentityRole(ctx context.Context, role *tables.TableIdentityRole) error {
	result := s.DB().WithContext(ctx).Model(&tables.TableIdentityRole{}).Where("id = ?", role.ID).Updates(map[string]any{
		"name": role.Name, "description": role.Description, "external_role": role.ExternalRole,
		"permissions_json": role.PermissionsJSON, "updated_at": time.Now().UTC(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *RDBConfigStore) DeleteIdentityRole(ctx context.Context, id string) error {
	return s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var role tables.TableIdentityRole
		if err := tx.First(&role, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if role.IsSystem {
			return ErrAlreadyExists
		}
		if err := tx.Where("role_id = ?", id).Delete(&tables.TableIdentityUserRole{}).Error; err != nil {
			return err
		}
		return tx.Delete(&role).Error
	})
}

func (s *RDBConfigStore) ListIdentityRoleIDs(ctx context.Context, userID string) ([]string, error) {
	var ids []string
	err := s.DB().WithContext(ctx).Model(&tables.TableIdentityUserRole{}).Where("user_id = ?", userID).Order("role_id ASC").Pluck("role_id", &ids).Error
	return ids, err
}

func (s *RDBConfigStore) ReplaceIdentityUserRoles(ctx context.Context, userID string, roleIDs []string) error {
	return s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user tables.TableIdentityUser
		if err := tx.First(&user, "id = ?", userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if len(roleIDs) > 0 {
			var count int64
			if err := tx.Model(&tables.TableIdentityRole{}).Where("id IN ?", roleIDs).Count(&count).Error; err != nil {
				return err
			}
			if count != int64(len(roleIDs)) {
				return ErrNotFound
			}
		}
		if err := tx.Where("user_id = ?", userID).Delete(&tables.TableIdentityUserRole{}).Error; err != nil {
			return err
		}
		if len(roleIDs) == 0 {
			return nil
		}
		links := make([]tables.TableIdentityUserRole, 0, len(roleIDs))
		for _, roleID := range roleIDs {
			links = append(links, tables.TableIdentityUserRole{UserID: userID, RoleID: roleID, CreatedAt: time.Now().UTC()})
		}
		return tx.Create(&links).Error
	})
}

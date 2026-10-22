package tables

import "time"

// TableIdentityUser is a locally cached identity from an external identity
// provider. Subject is immutable and is the OIDC `sub` claim when Source is
// keycloak; names and email are refreshed from the provider.
type TableIdentityUser struct {
	ID         string    `gorm:"primaryKey;type:varchar(64)" json:"id"`
	Subject    string    `gorm:"uniqueIndex;type:varchar(255);not null" json:"subject"`
	Username   string    `gorm:"index;type:varchar(255);not null" json:"username"`
	Email      string    `gorm:"uniqueIndex;type:varchar(320);not null" json:"email"`
	Name       string    `gorm:"type:varchar(255);not null" json:"name"`
	Source     string    `gorm:"index;type:varchar(32);not null" json:"source"`
	IsActive   bool      `gorm:"not null;default:true" json:"is_active"`
	ClaimsJSON string    `gorm:"type:text;not null;default:'{}'" json:"-"`
	CreatedAt  time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt  time.Time `gorm:"not null" json:"updated_at"`
}

func (TableIdentityUser) TableName() string { return "identity_users" }

// TableIdentityRole stores a Bifrost role and its resource:operation grants.
// PermissionsJSON is intentionally a compact JSON array so a role changes
// atomically without an N-row replacement on every edit.
type TableIdentityRole struct {
	ID              string    `gorm:"primaryKey;type:varchar(64)" json:"id"`
	Name            string    `gorm:"uniqueIndex;type:varchar(128);not null" json:"name"`
	Description     string    `gorm:"type:text;not null" json:"description"`
	ExternalRole    string    `gorm:"uniqueIndex;type:varchar(255);not null" json:"external_role"`
	PermissionsJSON string    `gorm:"type:text;not null;default:'[]'" json:"-"`
	IsSystem        bool      `gorm:"not null;default:false" json:"is_system"`
	CreatedAt       time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt       time.Time `gorm:"not null" json:"updated_at"`
}

func (TableIdentityRole) TableName() string { return "identity_roles" }

// TableIdentityUserRole connects identities to the roles they hold. The
// composite key makes repeated OIDC syncs naturally idempotent.
type TableIdentityUserRole struct {
	UserID    string    `gorm:"primaryKey;type:varchar(64)"`
	RoleID    string    `gorm:"primaryKey;type:varchar(64)"`
	CreatedAt time.Time `gorm:"not null"`
}

func (TableIdentityUserRole) TableName() string { return "identity_user_roles" }

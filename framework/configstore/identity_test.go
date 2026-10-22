package configstore

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdentityStoreCRUD(t *testing.T) {
	store := setupRDBTestStore(t)
	require.NoError(t, store.DB().AutoMigrate(
		&tables.TableIdentityUser{},
		&tables.TableIdentityRole{},
		&tables.TableIdentityUserRole{},
		&tables.TableIdentityVirtualKeyAssignment{},
		&tables.TableVirtualKey{},
	))
	ctx := context.Background()

	role := &tables.TableIdentityRole{
		ID: "role-developer", Name: "Developer", Description: "Can manage APIs", ExternalRole: "bifrost-developer", PermissionsJSON: `["api_gateway:write"]`,
	}
	require.NoError(t, store.CreateIdentityRole(ctx, role))
	require.ErrorIs(t, store.CreateIdentityRole(ctx, role), ErrAlreadyExists)

	user := &tables.TableIdentityUser{
		ID: "user-1", Subject: "keycloak-subject", Username: "dev", Email: "dev@example.test", Name: "Developer", Source: "keycloak", IsActive: true, ClaimsJSON: `{}`,
	}
	require.NoError(t, store.UpsertIdentityUser(ctx, user))
	require.NoError(t, store.ReplaceIdentityUserRoles(ctx, user.ID, []string{role.ID}))
	key := &tables.TableVirtualKey{ID: "vk-user-1", Name: "Personal key", Value: *schemas.NewSecretVar("sk-bf-user-1")}
	require.NoError(t, store.CreateVirtualKey(ctx, key))
	require.NoError(t, store.ReplaceIdentityVirtualKeyAssignment(ctx, user.ID, &key.ID))
	assignment, err := store.GetIdentityVirtualKeyAssignment(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, key.ID, assignment.VirtualKeyID)
	assignees, err := store.ResolveIdentityVirtualKeyAssignees(ctx, []string{key.ID})
	require.NoError(t, err)
	assert.Equal(t, user.Email, assignees[key.ID].Email)
	secondUser := &tables.TableIdentityUser{ID: "user-2", Subject: "keycloak-subject-2", Username: "other", Email: "other@example.test", Name: "Other User", Source: "keycloak", IsActive: true, ClaimsJSON: `{}`}
	require.NoError(t, store.UpsertIdentityUser(ctx, secondUser))
	require.ErrorIs(t, store.ReplaceIdentityVirtualKeyAssignment(ctx, secondUser.ID, &key.ID), ErrAlreadyExists)

	loaded, err := store.GetIdentityUser(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, user.Subject, loaded.Subject)
	roleIDs, err := store.ListIdentityRoleIDs(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{role.ID}, roleIDs)

	require.ErrorIs(t, store.ReplaceIdentityUserRoles(ctx, user.ID, []string{"missing"}), ErrNotFound)
	require.NoError(t, store.DeleteIdentityUser(ctx, user.ID))
	_, err = store.GetIdentityUser(ctx, user.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.GetIdentityVirtualKeyAssignment(ctx, user.ID)
	require.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, store.DeleteIdentityRole(ctx, role.ID))
	require.ErrorIs(t, store.DeleteIdentityRole(ctx, role.ID), ErrNotFound)
}

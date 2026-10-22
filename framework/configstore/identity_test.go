package configstore

import (
	"context"
	"testing"

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

	require.NoError(t, store.DeleteIdentityRole(ctx, role.ID))
	require.ErrorIs(t, store.DeleteIdentityRole(ctx, role.ID), ErrNotFound)
}

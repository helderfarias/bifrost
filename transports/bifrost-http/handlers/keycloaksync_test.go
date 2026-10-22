package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeycloakSyncerSyncsUsersAndMappedRoles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/bifrost-local/protocol/openid-connect/token":
			require.Equal(t, http.MethodPost, r.Method)
			username, password, ok := r.BasicAuth()
			require.True(t, ok)
			assert.Equal(t, "bifrost-oidc", username)
			assert.Equal(t, "secret", password)
			_, _ = w.Write([]byte(`{"access_token":"test-token"}`))
		case "/admin/realms/bifrost-local/users":
			require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`[{"id":"kc-user-1","username":"ada","email":"ada@example.test","firstName":"Ada","lastName":"Lovelace","enabled":true}]`))
		case "/admin/realms/bifrost-local/users/kc-user-1/role-mappings/realm":
			_, _ = w.Write([]byte(`[{"name":"bifrost-developer"},{"name":"offline_access"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	issuer, err := url.Parse(server.URL + "/realms/bifrost-local")
	require.NoError(t, err)
	syncer := &keycloakSyncer{issuer: issuer, realm: "bifrost-local", clientID: "bifrost-oidc", clientSecret: "secret", httpClient: server.Client()}
	store, ok := newRealOAuth2Store(t).(configstore.IdentityStore)
	require.True(t, ok)
	ctx := context.Background()

	created, updated, err := syncer.sync(ctx, store)
	require.NoError(t, err)
	assert.Equal(t, 1, created)
	assert.Zero(t, updated)

	users, err := store.ListIdentityUsers(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "kc-user-1", users[0].Subject)
	assert.Equal(t, "Ada Lovelace", users[0].Name)
	roleIDs, err := store.ListIdentityRoleIDs(ctx, users[0].ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"role-developer"}, roleIDs)

	created, updated, err = syncer.sync(ctx, store)
	require.NoError(t, err)
	assert.Zero(t, created)
	assert.Equal(t, 1, updated)
}

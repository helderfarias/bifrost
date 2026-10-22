package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
)

// keycloakSyncer is deliberately configured from operator-owned environment
// variables. Unlike request-supplied webhooks, its issuer is never attacker
// controlled, so the Admin API client cannot be used as an SSRF primitive.
//
// BIFROST_KEYCLOAK_ISSUER=http://localhost:8081/realms/bifrost-local
// BIFROST_KEYCLOAK_CLIENT_ID=bifrost-oidc
// BIFROST_KEYCLOAK_CLIENT_SECRET=...
type keycloakSyncer struct {
	issuer       *url.URL
	realm        string
	clientID     string
	clientSecret string
	httpClient   *http.Client
}

func newKeycloakSyncerFromEnvironment() *keycloakSyncer {
	rawIssuer := strings.TrimRight(strings.TrimSpace(os.Getenv("BIFROST_KEYCLOAK_ISSUER")), "/")
	clientID := strings.TrimSpace(os.Getenv("BIFROST_KEYCLOAK_CLIENT_ID"))
	clientSecret := os.Getenv("BIFROST_KEYCLOAK_CLIENT_SECRET")
	if rawIssuer == "" || clientID == "" || clientSecret == "" {
		return nil
	}
	issuer, err := url.Parse(rawIssuer)
	if err != nil || issuer.Scheme != "http" && issuer.Scheme != "https" || issuer.Host == "" {
		logger.Warn("Keycloak synchronization is disabled: BIFROST_KEYCLOAK_ISSUER must be an absolute http(s) URL")
		return nil
	}
	parts := strings.Split(strings.Trim(issuer.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "realms" || parts[1] == "" {
		logger.Warn("Keycloak synchronization is disabled: issuer must end in /realms/{realm}")
		return nil
	}
	return &keycloakSyncer{issuer: issuer, realm: parts[1], clientID: clientID, clientSecret: clientSecret, httpClient: &http.Client{Timeout: 10 * time.Second}}
}

// SyncKeycloakUsersFromEnvironment performs one idempotent Keycloak directory
// sync when the three BIFROST_KEYCLOAK_* variables are present. It is used at
// server startup and by the dashboard's explicit refresh action; a missing
// configuration is deliberately a no-op so Keycloak remains optional.
func SyncKeycloakUsersFromEnvironment(ctx context.Context, store configstore.IdentityStore) (created, updated int, configured bool, err error) {
	syncer := newKeycloakSyncerFromEnvironment()
	if syncer == nil || store == nil {
		return 0, 0, false, nil
	}
	created, updated, err = syncer.sync(ctx, store)
	return created, updated, true, err
}

type keycloakTokenResponse struct {
	AccessToken string `json:"access_token"`
}

type keycloakUser struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Enabled   bool   `json:"enabled"`
}

type keycloakRole struct {
	Name string `json:"name"`
}

func (s *keycloakSyncer) sync(ctx context.Context, store configstore.IdentityStore) (created, updated int, err error) {
	token, err := s.accessToken(ctx)
	if err != nil {
		return 0, 0, err
	}
	roles, err := store.ListIdentityRoles(ctx)
	if err != nil {
		return 0, 0, err
	}
	roleByExternalName := make(map[string]string, len(roles))
	for _, role := range roles {
		roleByExternalName[role.ExternalRole] = role.ID
	}
	existing, err := store.ListIdentityUsers(ctx)
	if err != nil {
		return 0, 0, err
	}
	bySubject := make(map[string]tables.TableIdentityUser, len(existing))
	for _, user := range existing {
		bySubject[user.Subject] = user
	}
	users, err := s.users(ctx, token)
	if err != nil {
		return 0, 0, err
	}
	for _, external := range users {
		if external.ID == "" || external.Username == "" || external.Email == "" {
			continue
		}
		roleNames, err := s.userRealmRoles(ctx, token, external.ID)
		if err != nil {
			return created, updated, err
		}
		roleIDs := make([]string, 0, len(roleNames))
		for _, roleName := range roleNames {
			if roleID, ok := roleByExternalName[roleName]; ok {
				roleIDs = append(roleIDs, roleID)
			}
		}
		name := strings.TrimSpace(strings.TrimSpace(external.FirstName) + " " + strings.TrimSpace(external.LastName))
		if name == "" {
			name = external.Username
		}
		claims, _ := json.Marshal(map[string]any{"realm_roles": roleNames})
		user, found := bySubject[external.ID]
		if !found {
			user.ID = uuid.NewString()
			user.Subject = external.ID
			created++
		} else {
			updated++
		}
		user.Username = external.Username
		user.Email = strings.ToLower(external.Email)
		user.Name = name
		user.Source = "keycloak"
		user.IsActive = external.Enabled
		user.ClaimsJSON = string(claims)
		if err := store.UpsertIdentityUser(ctx, &user); err != nil {
			return created, updated, err
		}
		if err := store.ReplaceIdentityUserRoles(ctx, user.ID, uniqueIdentityIDs(roleIDs)); err != nil {
			return created, updated, err
		}
	}
	return created, updated, nil
}

func (s *keycloakSyncer) accessToken(ctx context.Context) (string, error) {
	form := url.Values{"grant_type": {"client_credentials"}}
	endpoint := s.endpoint("realms", s.realm, "protocol/openid-connect/token")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(s.clientID, s.clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var response keycloakTokenResponse
	if err := s.doJSON(req, &response); err != nil {
		return "", err
	}
	if response.AccessToken == "" {
		return "", fmt.Errorf("Keycloak token response did not include access_token")
	}
	return response.AccessToken, nil
}

func (s *keycloakSyncer) users(ctx context.Context, token string) ([]keycloakUser, error) {
	var users []keycloakUser
	for first := 0; ; first += 100 {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint("admin/realms", s.realm, "users")+fmt.Sprintf("?first=%d&max=100", first), nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		var page []keycloakUser
		if err := s.doJSON(request, &page); err != nil {
			return nil, err
		}
		users = append(users, page...)
		if len(page) < 100 {
			return users, nil
		}
	}
}

func (s *keycloakSyncer) userRealmRoles(ctx context.Context, token, userID string) ([]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint("admin/realms", s.realm, "users", userID, "role-mappings/realm"), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	var roles []keycloakRole
	if err := s.doJSON(request, &roles); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(roles))
	for _, role := range roles {
		result = append(result, role.Name)
	}
	return result, nil
}

func (s *keycloakSyncer) endpoint(parts ...string) string {
	base := *s.issuer
	base.Path = "/" + path.Join(parts...)
	base.RawQuery = ""
	return base.String()
}

func (s *keycloakSyncer) doJSON(request *http.Request, dst any) error {
	response, err := s.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("Keycloak returned HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(dst)
}

package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/fasthttp/router"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/v2/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// IdentityHandler manages the Community user directory and RBAC catalogue.
// OIDC sync writes through this same handler/store path, so dashboard-created
// roles and externally synchronized identities have one source of truth.
type IdentityHandler struct {
	store    configstore.IdentityStore
	keycloak *keycloakSyncer
}

func NewIdentityHandler(store configstore.IdentityStore) *IdentityHandler {
	return &IdentityHandler{store: store, keycloak: newKeycloakSyncerFromEnvironment()}
}

func (h *IdentityHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/identity/users", lib.ChainMiddlewares(h.listUsers, middlewares...))
	r.POST("/api/identity/users", lib.ChainMiddlewares(h.createUser, middlewares...))
	r.PUT("/api/identity/users/{id}", lib.ChainMiddlewares(h.updateUser, middlewares...))
	r.DELETE("/api/identity/users/{id}", lib.ChainMiddlewares(h.deleteUser, middlewares...))
	r.PUT("/api/identity/users/{id}/roles", lib.ChainMiddlewares(h.replaceUserRoles, middlewares...))
	r.POST("/api/identity/users/sync/keycloak", lib.ChainMiddlewares(h.syncKeycloakUsers, middlewares...))
	r.GET("/api/identity/roles", lib.ChainMiddlewares(h.listRoles, middlewares...))
	r.POST("/api/identity/roles", lib.ChainMiddlewares(h.createRole, middlewares...))
	r.PUT("/api/identity/roles/{id}", lib.ChainMiddlewares(h.updateRole, middlewares...))
	r.DELETE("/api/identity/roles/{id}", lib.ChainMiddlewares(h.deleteRole, middlewares...))
}

func (h *IdentityHandler) syncKeycloakUsers(ctx *fasthttp.RequestCtx) {
	if !h.requireStore(ctx) {
		return
	}
	if h.keycloak == nil {
		SendError(ctx, fasthttp.StatusBadRequest, "Keycloak synchronization is not configured")
		return
	}
	created, updated, err := h.keycloak.sync(ctx, h.store)
	if err != nil {
		logger.Warn("keycloak user sync failed: %v", err)
		SendError(ctx, fasthttp.StatusBadGateway, "failed to synchronize users from Keycloak")
		return
	}
	SendJSON(ctx, map[string]int{"created": created, "updated": updated})
}

type identityUserPayload struct {
	ID       string   `json:"id"`
	Subject  string   `json:"subject"`
	Username string   `json:"username"`
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Source   string   `json:"source"`
	IsActive *bool    `json:"is_active"`
	RoleIDs  []string `json:"role_ids"`
}

type identityUserResponse struct {
	ID       string   `json:"id"`
	Subject  string   `json:"subject"`
	Username string   `json:"username"`
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Source   string   `json:"source"`
	IsActive bool     `json:"is_active"`
	RoleIDs  []string `json:"role_ids"`
}

type identityRolePayload struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	ExternalRole string   `json:"external_role"`
	Permissions  []string `json:"permissions"`
}

type identityRoleResponse struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	ExternalRole string   `json:"external_role"`
	Permissions  []string `json:"permissions"`
	IsSystem     bool     `json:"is_system"`
}

func (h *IdentityHandler) requireStore(ctx *fasthttp.RequestCtx) bool {
	if h.store != nil {
		return true
	}
	SendError(ctx, fasthttp.StatusServiceUnavailable, "identity storage is unavailable")
	return false
}

func (h *IdentityHandler) listUsers(ctx *fasthttp.RequestCtx) {
	if !h.requireStore(ctx) {
		return
	}
	users, err := h.store.ListIdentityUsers(ctx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to list users")
		return
	}
	SendJSON(ctx, map[string]any{"users": h.userResponses(ctx, users)})
}

func (h *IdentityHandler) createUser(ctx *fasthttp.RequestCtx) {
	if !h.requireStore(ctx) {
		return
	}
	user, roleIDs, err := decodeIdentityUser(ctx.PostBody(), "", false)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	user.ID = uuid.NewString()
	if user.Subject == "" {
		user.Subject = "local:" + user.ID
	}
	if err := h.store.UpsertIdentityUser(ctx, &user); err != nil {
		SendError(ctx, fasthttp.StatusConflict, "failed to create user")
		return
	}
	if err := h.store.ReplaceIdentityUserRoles(ctx, user.ID, roleIDs); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to assign user roles")
		return
	}
	SendJSONWithStatus(ctx, h.userResponses(ctx, []tables.TableIdentityUser{user})[0], fasthttp.StatusCreated)
}

func (h *IdentityHandler) updateUser(ctx *fasthttp.RequestCtx) {
	if !h.requireStore(ctx) {
		return
	}
	id := routeIdentityID(ctx)
	existing, err := h.store.GetIdentityUser(ctx, id)
	if err != nil {
		status := fasthttp.StatusInternalServerError
		if err == configstore.ErrNotFound {
			status = fasthttp.StatusNotFound
		}
		SendError(ctx, status, "failed to find user")
		return
	}
	user, roleIDs, err := decodeIdentityUser(ctx.PostBody(), id, true)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	// The OIDC subject, provenance, claims and original creation time belong to
	// the identity provider. Dashboard edits only affect display and activation
	// fields, so a local edit can never detach an existing Keycloak identity.
	user.Subject = existing.Subject
	user.Source = existing.Source
	user.ClaimsJSON = existing.ClaimsJSON
	user.CreatedAt = existing.CreatedAt
	if err := h.store.UpsertIdentityUser(ctx, &user); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to update user")
		return
	}
	if roleIDs != nil {
		if err := h.store.ReplaceIdentityUserRoles(ctx, id, roleIDs); err != nil {
			SendError(ctx, fasthttp.StatusInternalServerError, "failed to assign user roles")
			return
		}
	}
	SendJSON(ctx, h.userResponses(ctx, []tables.TableIdentityUser{user})[0])
}

func (h *IdentityHandler) deleteUser(ctx *fasthttp.RequestCtx) {
	if !h.requireStore(ctx) {
		return
	}
	if err := h.store.DeleteIdentityUser(ctx, routeIdentityID(ctx)); err != nil {
		status := fasthttp.StatusInternalServerError
		if err == configstore.ErrNotFound {
			status = fasthttp.StatusNotFound
		}
		SendError(ctx, status, "failed to delete user")
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

func (h *IdentityHandler) replaceUserRoles(ctx *fasthttp.RequestCtx) {
	if !h.requireStore(ctx) {
		return
	}
	var payload struct {
		RoleIDs []string `json:"role_ids"`
	}
	if err := sonic.Unmarshal(ctx.PostBody(), &payload); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid request payload")
		return
	}
	if err := h.store.ReplaceIdentityUserRoles(ctx, routeIdentityID(ctx), uniqueIdentityIDs(payload.RoleIDs)); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to assign user roles")
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

func (h *IdentityHandler) listRoles(ctx *fasthttp.RequestCtx) {
	if !h.requireStore(ctx) {
		return
	}
	roles, err := h.store.ListIdentityRoles(ctx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to list roles")
		return
	}
	SendJSON(ctx, map[string]any{"roles": identityRoleResponses(roles)})
}

func (h *IdentityHandler) createRole(ctx *fasthttp.RequestCtx) {
	if !h.requireStore(ctx) {
		return
	}
	role, err := decodeIdentityRole(ctx.PostBody(), "")
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	role.ID = uuid.NewString()
	if err := h.store.CreateIdentityRole(ctx, &role); err != nil {
		SendError(ctx, fasthttp.StatusConflict, "failed to create role")
		return
	}
	SendJSONWithStatus(ctx, identityRoleResponses([]tables.TableIdentityRole{role})[0], fasthttp.StatusCreated)
}

func (h *IdentityHandler) updateRole(ctx *fasthttp.RequestCtx) {
	if !h.requireStore(ctx) {
		return
	}
	role, err := decodeIdentityRole(ctx.PostBody(), routeIdentityID(ctx))
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.UpdateIdentityRole(ctx, &role); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to update role")
		return
	}
	SendJSON(ctx, identityRoleResponses([]tables.TableIdentityRole{role})[0])
}

func (h *IdentityHandler) deleteRole(ctx *fasthttp.RequestCtx) {
	if !h.requireStore(ctx) {
		return
	}
	if err := h.store.DeleteIdentityRole(ctx, routeIdentityID(ctx)); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "failed to delete role")
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

func decodeIdentityUser(body []byte, id string, update bool) (tables.TableIdentityUser, []string, error) {
	var payload identityUserPayload
	if err := sonic.Unmarshal(body, &payload); err != nil {
		return tables.TableIdentityUser{}, nil, fmt.Errorf("invalid request payload")
	}
	if strings.TrimSpace(payload.Username) == "" || strings.TrimSpace(payload.Email) == "" || strings.TrimSpace(payload.Name) == "" {
		return tables.TableIdentityUser{}, nil, fmt.Errorf("username, email and name are required")
	}
	if !strings.Contains(payload.Email, "@") {
		return tables.TableIdentityUser{}, nil, fmt.Errorf("email is invalid")
	}
	if payload.IsActive == nil {
		payload.IsActive = schemas.Ptr(true)
	}
	if payload.Source == "" {
		payload.Source = "local"
	}
	user := tables.TableIdentityUser{ID: id, Subject: strings.TrimSpace(payload.Subject), Username: strings.TrimSpace(payload.Username), Email: strings.ToLower(strings.TrimSpace(payload.Email)), Name: strings.TrimSpace(payload.Name), Source: strings.TrimSpace(payload.Source), IsActive: *payload.IsActive, ClaimsJSON: "{}"}
	if update && user.Subject == "" {
		return tables.TableIdentityUser{}, nil, fmt.Errorf("subject is required when updating a user")
	}
	roleIDs := uniqueIdentityIDs(payload.RoleIDs)
	if update && payload.RoleIDs == nil {
		roleIDs = nil
	}
	return user, roleIDs, nil
}

func decodeIdentityRole(body []byte, id string) (tables.TableIdentityRole, error) {
	var payload identityRolePayload
	if err := sonic.Unmarshal(body, &payload); err != nil {
		return tables.TableIdentityRole{}, fmt.Errorf("invalid request payload")
	}
	if strings.TrimSpace(payload.Name) == "" || strings.TrimSpace(payload.ExternalRole) == "" {
		return tables.TableIdentityRole{}, fmt.Errorf("name and external role are required")
	}
	permissions, err := sonic.MarshalString(uniqueIdentityIDs(payload.Permissions))
	if err != nil {
		return tables.TableIdentityRole{}, fmt.Errorf("invalid permissions")
	}
	return tables.TableIdentityRole{ID: id, Name: strings.TrimSpace(payload.Name), Description: strings.TrimSpace(payload.Description), ExternalRole: strings.TrimSpace(payload.ExternalRole), PermissionsJSON: permissions}, nil
}

func (h *IdentityHandler) userResponses(ctx context.Context, users []tables.TableIdentityUser) []identityUserResponse {
	result := make([]identityUserResponse, 0, len(users))
	for _, user := range users {
		roleIDs, _ := h.store.ListIdentityRoleIDs(ctx, user.ID)
		result = append(result, identityUserResponse{ID: user.ID, Subject: user.Subject, Username: user.Username, Email: user.Email, Name: user.Name, Source: user.Source, IsActive: user.IsActive, RoleIDs: roleIDs})
	}
	return result
}

func identityRoleResponses(roles []tables.TableIdentityRole) []identityRoleResponse {
	result := make([]identityRoleResponse, 0, len(roles))
	for _, role := range roles {
		var permissions []string
		_ = sonic.UnmarshalString(role.PermissionsJSON, &permissions)
		if permissions == nil {
			permissions = []string{}
		}
		result = append(result, identityRoleResponse{ID: role.ID, Name: role.Name, Description: role.Description, ExternalRole: role.ExternalRole, Permissions: permissions, IsSystem: role.IsSystem})
	}
	return result
}

func routeIdentityID(ctx *fasthttp.RequestCtx) string {
	id, _ := ctx.UserValue("id").(string)
	return id
}

func uniqueIdentityIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

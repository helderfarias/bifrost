package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"github.com/maximhq/bifrost/transports/v2/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// SessionHandler manages HTTP requests for session operations
type SessionHandler struct {
	configStore   configstore.ConfigStore
	wsTicketStore *WSTicketStore
	// setupLock is the OSS setup-lock gate (see AuthMiddleware.SetupLockMiddleware). Nil
	// when the gate is not installed (enterprise, or no config store), in which case
	// is-auth-enabled never reports setup_required.
	setupLock *AuthMiddleware
	oidc      *keycloakOIDCConfig
}

// NewSessionHandler creates a new session handler instance
func NewSessionHandler(configStore configstore.ConfigStore, wsTicketStore *WSTicketStore) *SessionHandler {
	return &SessionHandler{
		configStore:   configStore,
		wsTicketStore: wsTicketStore,
		oidc:          keycloakOIDCConfigFromEnvironment(),
	}
}

// SetSetupLock tells the handler the OSS setup-lock gate is installed, so is-auth-enabled
// reports setup_required / setup_token_configured and ws-ticket issues setup-token tickets.
func (h *SessionHandler) SetSetupLock(m *AuthMiddleware) {
	h.setupLock = m
}

// setSetupSessionCookie writes (or, with an empty value, expires) the HttpOnly setup session
// cookie. SameSite=Strict: it only ever needs to ride the dashboard's own requests.
func setSetupSessionCookie(ctx *fasthttp.RequestCtx, value string, expires time.Time) {
	cookie := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(cookie)
	cookie.SetKey(SetupSessionCookie)
	cookie.SetValue(value)
	cookie.SetExpire(expires)
	cookie.SetPath("/")
	cookie.SetHTTPOnly(true)
	cookie.SetSameSite(fasthttp.CookieSameSiteStrictMode)
	if ctx.IsTLS() || string(ctx.Request.Header.Peek("X-Forwarded-Proto")) == "https" {
		cookie.SetSecure(true)
	}
	ctx.Response.Header.SetCookie(cookie)
}

// startSetupSession handles POST /api/session/setup: while the OSS setup lock is active, it
// trades the setup token (X-Bifrost-Setup-Token, checked here rather than trusting a cookie
// so a session cannot extend itself) for an HttpOnly setup session cookie. The dashboard
// keeps no copy of the token; the cookie authenticates its calls until it expires or
// dashboard auth is enabled.
func (h *SessionHandler) startSetupSession(ctx *fasthttp.RequestCtx) {
	if h.setupLock == nil || h.setupLock.IsDashboardAuthActive() {
		SendError(ctx, fasthttp.StatusConflict, "the setup lock is not active")
		return
	}
	if !h.setupLock.CheckConfiguredSetupToken(string(ctx.Request.Header.Peek(SetupTokenHeader))) {
		SendError(ctx, fasthttp.StatusForbidden, "invalid setup token")
		return
	}
	value, expires, err := h.setupLock.IssueSetupSession(time.Now())
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to start setup session")
		return
	}
	setSetupSessionCookie(ctx, value, expires)
	SendJSON(ctx, map[string]any{"expires_at": expires.UTC().Format(time.RFC3339)})
}

// setupLockState reports whether the setup-lock gate is currently locking the API and
// whether the operator configured a setup token.
func (h *SessionHandler) setupLockState() (setupRequired bool, setupTokenConfigured bool) {
	if h.setupLock == nil {
		return false, false
	}
	return !h.setupLock.IsDashboardAuthActive(), h.setupLock.HasSetupToken()
}

// RegisterRoutes registers the session-related routes
func (h *SessionHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.POST("/api/session/login", lib.ChainMiddlewares(h.login, middlewares...))
	r.POST("/api/session/logout", lib.ChainMiddlewares(h.logout, middlewares...))
	r.GET("/api/session/is-auth-enabled", lib.ChainMiddlewares(h.isAuthEnabled, middlewares...))
	r.POST("/api/session/ws-ticket", lib.ChainMiddlewares(h.issueWSTicket, middlewares...))
	r.POST("/api/session/setup", lib.ChainMiddlewares(h.startSetupSession, middlewares...))
	r.GET("/api/session/oidc/login", lib.ChainMiddlewares(h.startOIDCLogin, middlewares...))
	r.GET("/api/session/oidc/callback", lib.ChainMiddlewares(h.finishOIDCLogin, middlewares...))
}

// isAuthEnabled handles GET /api/session/is-auth-enabled - Check if auth is enabled
func (h *SessionHandler) isAuthEnabled(ctx *fasthttp.RequestCtx) {
	setupRequired, setupTokenConfigured := h.setupLockState()
	if h.configStore == nil {
		SendJSON(ctx, map[string]any{
			"is_auth_enabled":         false,
			"has_valid_token":         false,
			"auth_type":               "none",
			"inference_auth_enforced": false,
			"setup_required":          setupRequired,
			"setup_token_configured":  setupTokenConfigured,
		})
		return
	}
	// inference_auth_enforced reports enforce_auth_on_inference - a separate toggle from
	// dashboard auth (this endpoint's main subject) that gates /v1/* instead of the
	// dashboard/admin API. Surfaced here so a locked dashboard doesn't look like the whole
	// gateway is secured when this second, easily-missed control is still off.
	inferenceAuthEnforced := false
	if clientConfig, err := h.configStore.GetClientConfig(ctx); err == nil && clientConfig != nil {
		inferenceAuthEnforced = clientConfig.EnforceAuthOnInference
	}
	authConfig, err := h.configStore.GetAuthConfig(ctx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to get auth config: %v", err))
		return
	}
	if authConfig == nil && h.oidc == nil {
		SendJSON(ctx, map[string]any{
			"is_auth_enabled":         false,
			"has_valid_token":         false,
			"auth_type":               "none",
			"inference_auth_enforced": inferenceAuthEnforced,
			"setup_required":          setupRequired,
			"setup_token_configured":  setupTokenConfigured,
		})
		return
	}
	// Check if the header has a token and is valid (Authorization header or cookie)
	token := ""
	if authHeader := string(ctx.Request.Header.Peek("Authorization")); strings.HasPrefix(authHeader, "Bearer ") {
		token = strings.TrimPrefix(authHeader, "Bearer ")
	}
	if token == "" {
		token = string(ctx.Request.Header.Cookie("token"))
	}
	hasValidToken := false
	if token != "" {
		session, err := h.configStore.GetSession(ctx, token)
		if err == nil && session != nil && session.ExpiresAt.After(time.Now()) {
			hasValidToken = true
		}
	}
	passwordAuthEnabled := authConfig != nil && authConfig.IsEnabled
	authEnabled := h.oidc != nil || passwordAuthEnabled
	authType := dashboardAuthType(passwordAuthEnabled)
	if h.oidc != nil {
		authType = "sso"
	}
	SendJSON(ctx, map[string]any{
		"is_auth_enabled":         authEnabled,
		"has_valid_token":         hasValidToken,
		"auth_type":               authType,
		"inference_auth_enforced": inferenceAuthEnforced,
		"setup_required":          setupRequired,
		"setup_token_configured":  setupTokenConfigured,
	})
}

func (h *SessionHandler) startOIDCLogin(ctx *fasthttp.RequestCtx) {
	if h.oidc == nil {
		SendError(ctx, fasthttp.StatusNotFound, "Keycloak OIDC login is not configured")
		return
	}
	state, err := newKeycloakOIDCState(string(ctx.QueryArgs().Peek("goto")))
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to start OIDC login")
		return
	}
	encoded, err := encodeKeycloakOIDCState(state)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to start OIDC login")
		return
	}
	setOIDCStateCookie(ctx, encoded, time.Now().Add(10*time.Minute))
	ctx.Redirect(h.oidc.authorizationURL(state.State, state.Verifier), fasthttp.StatusFound)
}

func (h *SessionHandler) finishOIDCLogin(ctx *fasthttp.RequestCtx) {
	if h.oidc == nil {
		SendError(ctx, fasthttp.StatusNotFound, "Keycloak OIDC login is not configured")
		return
	}
	state, err := decodeKeycloakOIDCState(string(ctx.Request.Header.Cookie(keycloakOIDCStateCookie)))
	setOIDCStateCookie(ctx, "", time.Unix(1, 0))
	if err != nil || subtle.ConstantTimeCompare([]byte(state.State), ctx.QueryArgs().Peek("state")) != 1 {
		SendError(ctx, fasthttp.StatusBadRequest, "invalid OIDC login state")
		return
	}
	code := string(ctx.QueryArgs().Peek("code"))
	if code == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "missing OIDC authorization code")
		return
	}
	token, err := h.oidc.exchangeCode(ctx, code, state.Verifier)
	if err != nil {
		logger.Warn("Keycloak OIDC code exchange failed: %v", err)
		SendError(ctx, fasthttp.StatusUnauthorized, "Keycloak login failed")
		return
	}
	if _, err := h.oidc.userInfo(ctx, token.AccessToken); err != nil {
		logger.Warn("Keycloak OIDC userinfo failed: %v", err)
		SendError(ctx, fasthttp.StatusUnauthorized, "Keycloak login failed")
		return
	}
	expiresIn := time.Duration(token.ExpiresIn) * time.Second
	if expiresIn <= 0 || expiresIn > 8*time.Hour {
		expiresIn = 8 * time.Hour
	}
	if err := h.createBrowserSession(ctx, time.Now().Add(expiresIn)); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to create login session")
		return
	}
	ctx.Redirect(state.Goto, fasthttp.StatusFound)
}

func (h *SessionHandler) createBrowserSession(ctx *fasthttp.RequestCtx, expiresAt time.Time) error {
	token := uuid.NewString()
	if err := h.configStore.CreateSession(ctx, &tables.SessionsTable{Token: token, ExpiresAt: expiresAt, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		return err
	}
	setSessionCookie(ctx, token, expiresAt)
	return nil
}

func setSessionCookie(ctx *fasthttp.RequestCtx, value string, expiresAt time.Time) {
	cookie := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(cookie)
	cookie.SetKey("token")
	cookie.SetValue(value)
	cookie.SetExpire(expiresAt)
	cookie.SetPath("/")
	cookie.SetHTTPOnly(true)
	cookie.SetSameSite(fasthttp.CookieSameSiteLaxMode)
	if ctx.IsTLS() || string(ctx.Request.Header.Peek("X-Forwarded-Proto")) == "https" {
		cookie.SetSecure(true)
	}
	ctx.Response.Header.SetCookie(cookie)
}

func setOIDCStateCookie(ctx *fasthttp.RequestCtx, value string, expiresAt time.Time) {
	cookie := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(cookie)
	cookie.SetKey(keycloakOIDCStateCookie)
	cookie.SetValue(value)
	cookie.SetExpire(expiresAt)
	cookie.SetPath("/api/session/oidc")
	cookie.SetHTTPOnly(true)
	cookie.SetSameSite(fasthttp.CookieSameSiteLaxMode)
	if ctx.IsTLS() || string(ctx.Request.Header.Peek("X-Forwarded-Proto")) == "https" {
		cookie.SetSecure(true)
	}
	ctx.Response.Header.SetCookie(cookie)
}

// dashboardAuthType reports the dashboard session auth mode for frontend flows.
func dashboardAuthType(isEnabled bool) string {
	if isEnabled {
		return "password"
	}
	return "none"
}

// login handles POST /api/session/login - Login a user
func (h *SessionHandler) login(ctx *fasthttp.RequestCtx) {
	if h.configStore == nil {
		SendError(ctx, fasthttp.StatusForbidden, "Authentication is not enabled")
		return
	}
	if h.oidc != nil {
		SendError(ctx, fasthttp.StatusForbidden, "Keycloak OIDC login is enabled")
		return
	}
	payload := struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{}
	if err := json.Unmarshal(ctx.PostBody(), &payload); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "Invalid request payload")
		return
	}

	// Get auth config
	authConfig, err := h.configStore.GetAuthConfig(ctx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to get auth config: %v", err))
		return
	}

	// Check if auth is enabled
	if authConfig == nil || !authConfig.IsEnabled {
		SendError(ctx, fasthttp.StatusForbidden, "Authentication is not enabled")
		return
	}

	// Verify credentials
	if payload.Username != authConfig.AdminUserName.GetValue() {
		SendError(ctx, fasthttp.StatusUnauthorized, "Invalid username or password")
		return
	}
	compare, err := encrypt.CompareHash(authConfig.AdminPassword.GetValue(), payload.Password)
	if err != nil {
		SendError(ctx, fasthttp.StatusUnauthorized, "Unauthorized")
		return
	}
	if !compare {
		SendError(ctx, fasthttp.StatusUnauthorized, "Invalid username or password")
		return
	}

	// Creating a new session
	token := uuid.New().String()
	session := &tables.SessionsTable{
		Token:     token,
		ExpiresAt: time.Now().Add(time.Hour * 24 * 30), // 30 days
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	err = h.configStore.CreateSession(ctx, session)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, fmt.Sprintf("Failed to create session: %v", err))
		return
	}

	// Setting cookies
	cookie := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(cookie)
	cookie.SetKey("token")
	cookie.SetValue(token)
	cookie.SetExpire(time.Now().Add(time.Hour * 24 * 30))
	cookie.SetPath("/")
	cookie.SetHTTPOnly(true)
	cookie.SetSameSite(fasthttp.CookieSameSiteLaxMode)
	// Check if source is https then set secure
	if string(ctx.Request.Header.Peek("X-Forwarded-Proto")) == "https" {
		cookie.SetSecure(true)
	}
	ctx.Response.Header.SetCookie(cookie)

	SendJSON(ctx, map[string]any{
		"message": "Login successful",
	})
}

// logout handles POST /api/session/logout - Logout a user
func (h *SessionHandler) logout(ctx *fasthttp.RequestCtx) {
	if h.configStore == nil {
		SendError(ctx, fasthttp.StatusForbidden, "Authentication is not enabled")
		return
	}
	// Get token from Authorization header
	token := string(ctx.Request.Header.Peek("Authorization"))
	token = strings.TrimPrefix(token, "Bearer ")

	// If no token in header, try to get from cookie
	if token == "" {
		token = string(ctx.Request.Header.Cookie("token"))
	}

	// clear token from cookies
	cookie := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(cookie)
	cookie.SetKey("token")
	cookie.SetValue("")
	cookie.SetExpire(time.Now().Add(-time.Hour * 24 * 30))
	cookie.SetPath("/")
	cookie.SetHTTPOnly(true)
	cookie.SetSameSite(fasthttp.CookieSameSiteLaxMode)
	// Check if source is https then set secure
	if string(ctx.Request.Header.Peek("X-Forwarded-Proto")) == "https" {
		cookie.SetSecure(true)
	}
	ctx.Response.Header.SetCookie(cookie)

	// Drop any setup session as well, so a later return to the setup lock (auth disabled
	// again) cannot be re-entered with a cookie from before.
	setSetupSessionCookie(ctx, "", time.Now().Add(-time.Hour))

	// delete session from database if token exists
	if token != "" {
		err := h.configStore.DeleteSession(ctx, token)
		if err != nil && !errors.Is(err, configstore.ErrNotFound) {
			logger.Error("failed to delete session during logout: %v", err)
			SendError(ctx, fasthttp.StatusInternalServerError, "Failed to invalidate session. Please try again.")
			return
		}
	}

	SendJSON(ctx, map[string]any{
		"message": "Logout successful",
	})
}

// issueWSTicket handles POST /api/session/ws-ticket - Issue a short-lived ticket for WebSocket auth.
// The caller must already be authenticated (via cookie or Authorization header).
// Returns a one-time-use ticket that the frontend passes as ?ticket= when opening the WebSocket.
func (h *SessionHandler) issueWSTicket(ctx *fasthttp.RequestCtx) {
	if h.wsTicketStore == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "WebSocket tickets are not available")
		return
	}
	sessionToken, ok := ctx.UserValue(schemas.BifrostContextKeySessionToken).(string)
	if !ok {
		SendError(ctx, fasthttp.StatusUnauthorized, "Unauthorized")
		return
	}
	if sessionToken == "" {
		// This is the case where auth is not configured or not enabled
		sessionToken = "dummy-session"
	}
	ticket, err := h.wsTicketStore.Issue(sessionToken)
	if err != nil {
		logger.Error("failed to issue WS ticket: %v", err)
		SendError(ctx, fasthttp.StatusInternalServerError, "Failed to issue WebSocket ticket")
		return
	}
	SendJSON(ctx, map[string]any{
		"ticket": ticket,
	})
}

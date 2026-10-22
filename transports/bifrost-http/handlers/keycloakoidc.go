package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	keycloakOIDCStateCookie = "bifrost_oidc_state"
	defaultKeycloakCallback = "http://localhost:3000/api/session/oidc/callback"
)

type keycloakOIDCConfig struct {
	issuer, clientID, clientSecret, redirectURI string
	httpClient                                  *http.Client
}
type keycloakOIDCState struct {
	State    string `json:"state"`
	Verifier string `json:"verifier"`
	Goto     string `json:"goto"`
}
type keycloakOIDCToken struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}
type keycloakUserInfo struct {
	Subject string `json:"sub"`
}

func keycloakOIDCConfigFromEnvironment() *keycloakOIDCConfig {
	issuer := strings.TrimRight(strings.TrimSpace(os.Getenv("BIFROST_KEYCLOAK_ISSUER")), "/")
	clientID, clientSecret := strings.TrimSpace(os.Getenv("BIFROST_KEYCLOAK_CLIENT_ID")), os.Getenv("BIFROST_KEYCLOAK_CLIENT_SECRET")
	redirectURI := strings.TrimSpace(os.Getenv("BIFROST_KEYCLOAK_REDIRECT_URI"))
	if redirectURI == "" {
		redirectURI = defaultKeycloakCallback
	}
	parsedIssuer, err := url.Parse(issuer)
	parsedRedirect, redirectErr := url.Parse(redirectURI)
	if issuer == "" || clientID == "" || clientSecret == "" || err != nil || redirectErr != nil || (parsedIssuer.Scheme != "http" && parsedIssuer.Scheme != "https") || parsedIssuer.Host == "" || (parsedRedirect.Scheme != "http" && parsedRedirect.Scheme != "https") || parsedRedirect.Host == "" {
		return nil
	}
	return &keycloakOIDCConfig{issuer: issuer, clientID: clientID, clientSecret: clientSecret, redirectURI: redirectURI, httpClient: &http.Client{Timeout: 10 * time.Second}}
}

func (c *keycloakOIDCConfig) authorizationURL(state, verifier string) string {
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{"client_id": {c.clientID}, "response_type": {"code"}, "scope": {"openid profile email"}, "redirect_uri": {c.redirectURI}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}}
	return c.issuer + "/protocol/openid-connect/auth?" + query.Encode()
}

func (c *keycloakOIDCConfig) exchangeCode(ctx context.Context, code, verifier string) (*keycloakOIDCToken, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {c.redirectURI}, "code_verifier": {verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.issuer+"/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.clientID, c.clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var token keycloakOIDCToken
	if err := c.doJSON(req, &token); err != nil {
		return nil, err
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("Keycloak token response did not include an access token")
	}
	return &token, nil
}

func (c *keycloakOIDCConfig) userInfo(ctx context.Context, accessToken string) (*keycloakUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.issuer+"/protocol/openid-connect/userinfo", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	var info keycloakUserInfo
	if err := c.doJSON(req, &info); err != nil {
		return nil, err
	}
	if info.Subject == "" {
		return nil, fmt.Errorf("Keycloak userinfo response did not include sub")
	}
	return &info, nil
}

func (c *keycloakOIDCConfig) doJSON(req *http.Request, destination any) error {
	response, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("Keycloak returned HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(destination)
}
func newKeycloakOIDCState(gotoPath string) (keycloakOIDCState, error) {
	state, err := randomOIDCValue()
	if err != nil {
		return keycloakOIDCState{}, err
	}
	verifier, err := randomOIDCValue()
	if err != nil {
		return keycloakOIDCState{}, err
	}
	return keycloakOIDCState{State: state, Verifier: verifier, Goto: safeOIDCGoto(gotoPath)}, nil
}
func randomOIDCValue() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}
func encodeKeycloakOIDCState(state keycloakOIDCState) (string, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}
func decodeKeycloakOIDCState(value string) (keycloakOIDCState, error) {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return keycloakOIDCState{}, err
	}
	var state keycloakOIDCState
	if err := json.Unmarshal(payload, &state); err != nil || state.State == "" || state.Verifier == "" {
		return keycloakOIDCState{}, fmt.Errorf("invalid OIDC state")
	}
	return state, nil
}
func safeOIDCGoto(value string) string {
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.Contains(value, "\\") {
		return value
	}
	return "/workspace"
}

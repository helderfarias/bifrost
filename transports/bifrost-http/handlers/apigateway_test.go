package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/v2/bifrost-http/apigateway"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

type apiRouteStore struct {
	configstore.ConfigStore
	value string
	fail  bool
}

func (s *apiRouteStore) GetConfig(context.Context, string) (*tables.TableGovernanceConfig, error) {
	if s.value == "" {
		return nil, configstore.ErrNotFound
	}
	return &tables.TableGovernanceConfig{Key: apiGatewayConfigKey, Value: s.value}, nil
}
func (s *apiRouteStore) UpdateConfig(_ context.Context, row *tables.TableGovernanceConfig, _ ...*gorm.DB) error {
	if s.fail {
		return errors.New("store unavailable")
	}
	s.value = row.Value
	return nil
}
func TestAPIGatewayPersistenceAndRevision(t *testing.T) {
	store := &apiRouteStore{}
	h, err := NewAPIGatewayHandler(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetBodyString(`{"routes":[],"revision":"stale"}`)
	h.replace(ctx)
	if ctx.Response.StatusCode() != 409 {
		t.Fatal(ctx.Response.StatusCode())
	}
	route := apigateway.Route{ID: "ocr", Name: "OCR", Path: "/api/v2/ocr", Upstream: "http://localhost:3000", Enabled: true, Capabilities: []string{"svc.ocr"}, Consumers: []apigateway.Consumer{{Name: "app", TokenHash: fmt.Sprintf("%x", sha256.Sum256([]byte("a-random-test-key-0123456789abcdef"))), Capabilities: []string{"svc.ocr"}}}, TimeoutSeconds: 30, FailureThreshold: 5, RecoverySeconds: 30}
	body, _ := json.Marshal(struct {
		Routes   []apigateway.Route `json:"routes"`
		Revision string             `json:"revision"`
	}{[]apigateway.Route{route}, apiRoutesRevision(h.routes)})
	ctx = &fasthttp.RequestCtx{}
	ctx.Request.SetBody(body)
	h.replace(ctx)
	if ctx.Response.StatusCode() != 200 || store.value == "" {
		t.Fatalf("save: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	restored, err := NewAPIGatewayHandler(context.Background(), store)
	if err != nil || len(restored.routes) != 1 || restored.routes[0].Path != route.Path {
		t.Fatalf("restore: %v", err)
	}
	store.fail = true
	body, _ = json.Marshal(struct {
		Routes   []apigateway.Route `json:"routes"`
		Revision string             `json:"revision"`
	}{[]apigateway.Route{}, apiRoutesRevision(h.routes)})
	ctx = &fasthttp.RequestCtx{}
	ctx.Request.SetBody(body)
	h.replace(ctx)
	if len(h.routes) != 1 {
		t.Fatal("failed save changed active routes")
	}
	if ctx.Response.StatusCode() != 500 {
		t.Fatal(ctx.Response.StatusCode())
	}
}
func TestAPIGatewayUnauthenticatedMutation(t *testing.T) {
	h, _ := NewAPIGatewayHandler(context.Background(), &apiRouteStore{})
	ctx := &fasthttp.RequestCtx{}
	ctx.SetUserValue(schemas.BifrostContextKeyAuthBypassed, true)
	ctx.Request.SetBodyString(`{"routes":[]}`)
	h.replace(ctx)
	if ctx.Response.StatusCode() != 403 {
		t.Fatal(ctx.Response.StatusCode())
	}
}
func TestAPIGatewayInvalidStoredRoutes(t *testing.T) {
	if _, err := NewAPIGatewayHandler(context.Background(), &apiRouteStore{value: `[{"id":"unsafe"}]`}); err == nil {
		t.Fatal("invalid persisted configuration was accepted")
	}
}

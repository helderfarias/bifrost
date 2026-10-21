package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/v2/bifrost-http/apigateway"
	"github.com/maximhq/bifrost/transports/v2/bifrost-http/lib"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"
)

const apiGatewayConfigKey = "application_api_gateway_routes"

// APIGatewayHandler persists route definitions in the existing config store.
// Management uses dashboard authentication; traffic uses its own consumer keys.
// Runtime edits, quotas and circuits are local to one gateway instance.
type APIGatewayHandler struct {
	store   configstore.ConfigStore
	gateway *apigateway.Gateway
	mu      sync.Mutex
	routes  []apigateway.Route
}

func NewAPIGatewayHandler(ctx context.Context, store configstore.ConfigStore) (*APIGatewayHandler, error) {
	h := &APIGatewayHandler{store: store, gateway: apigateway.New(), routes: []apigateway.Route{}}
	if store == nil {
		return h, nil
	}
	row, err := store.GetConfig(ctx, apiGatewayConfigKey)
	if err != nil && !errors.Is(err, configstore.ErrNotFound) {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(row.Value), &h.routes); err != nil {
			return nil, err
		}
	}
	if err = h.gateway.Replace(h.routes); err != nil {
		return nil, err
	}
	return h, nil
}
func (h *APIGatewayHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/api-gateway/routes", lib.ChainMiddlewares(h.list, middlewares...))
	r.PUT("/api/api-gateway/routes", lib.ChainMiddlewares(h.replace, middlewares...))
	proxy := fasthttpadaptor.NewFastHTTPHandler(h.gateway)
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		r.Handle(method, "/gateway/{path:*}", proxy)
		r.Handle(method, "/api/v2/{path:*}", proxy)
	}
}
func (h *APIGatewayHandler) list(ctx *fasthttp.RequestCtx) {
	h.mu.Lock()
	defer h.mu.Unlock()
	SendJSON(ctx, struct {
		Routes   []apigateway.Route `json:"routes"`
		Revision string             `json:"revision"`
	}{h.routes, apiRoutesRevision(h.routes)})
}
func (h *APIGatewayHandler) replace(ctx *fasthttp.RequestCtx) {
	// An unauthenticated dashboard must never be able to provision an arbitrary
	// private upstream proxy, even when other management endpoints are default-open.
	if bypassed, _ := ctx.UserValue(schemas.BifrostContextKeyAuthBypassed).(bool); bypassed {
		SendError(ctx, 403, "Dashboard authentication is required to configure API routes")
		return
	}
	if h.store == nil {
		SendError(ctx, 503, "API route storage is unavailable")
		return
	}
	var body struct {
		Revision string             `json:"revision"`
		Routes   []apigateway.Route `json:"routes"`
	}
	if len(ctx.PostBody()) > 1<<20 {
		SendError(ctx, 413, "API route configuration is too large")
		return
	}
	if err := json.Unmarshal(ctx.PostBody(), &body); err != nil || body.Routes == nil {
		SendError(ctx, 400, "Expected a routes array")
		return
	}
	if err := apigateway.Validate(body.Routes); err != nil {
		SendError(ctx, 400, err.Error())
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if body.Revision != apiRoutesRevision(h.routes) {
		SendError(ctx, 409, "API routes changed; reload before saving")
		return
	}
	payload, err := json.Marshal(body.Routes)
	if err != nil {
		SendError(ctx, 500, "Failed to encode API routes")
		return
	}
	if err = h.store.UpdateConfig(ctx, &tables.TableGovernanceConfig{Key: apiGatewayConfigKey, Value: string(payload)}); err != nil {
		SendError(ctx, 500, "Failed to save API routes")
		return
	}
	// Replace only fails validation, already performed before persistence.
	if err = h.gateway.Replace(body.Routes); err != nil {
		SendError(ctx, 500, "Failed to activate API routes")
		return
	}
	h.routes = body.Routes
	body.Revision = apiRoutesRevision(h.routes)
	SendJSON(ctx, body)
}

func apiRoutesRevision(routes []apigateway.Route) string {
	data, _ := json.Marshal(routes)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

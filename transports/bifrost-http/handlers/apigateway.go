package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/bytedance/sonic"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/network"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/v2/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

const apiGatewayPathPrefix = "/apis"

var apiGatewayPathPrefixBytes = []byte(apiGatewayPathPrefix + "/")

// APIGatewayHandler evaluates API routes registered through the management API
// before Bifrost's own HTTP router. The immutable compiled snapshot changes
// only after a CRUD write; request-time reads are a single atomic load.
type APIGatewayHandler struct {
	routes            atomic.Pointer[[]compiledAPIGatewayRoute]
	client            *fasthttp.Client
	resolveVirtualKey APIGatewayVirtualKeyResolver
}

type compiledAPIGatewayRoute struct {
	id            string
	target        *url.URL
	predicates    [][]*regexp.Regexp
	filters       []rewritePathFilter
	virtualKeyIDs map[string]struct{}
}

type rewritePathFilter struct {
	pattern     *regexp.Regexp
	replacement string
}

type apiGatewayRoutePayload struct {
	ID            string   `json:"id"`
	URI           string   `json:"uri"`
	Predicates    []string `json:"predicates"`
	Filters       []string `json:"filters,omitempty"`
	Enabled       *bool    `json:"enabled,omitempty"`
	VirtualKeyIDs []string `json:"virtual_key_ids,omitempty"`
}

type apiGatewayRouteResponse struct {
	ID            string   `json:"id"`
	URI           string   `json:"uri"`
	Predicates    []string `json:"predicates"`
	Filters       []string `json:"filters"`
	Enabled       bool     `json:"enabled"`
	VirtualKeyIDs []string `json:"virtual_key_ids"`
}

// APIGatewayVirtualKeyResolver resolves a presented virtual-key secret to its
// stable ID. The server supplies a governance in-memory resolver, keeping the
// proxy path free of database reads.
type APIGatewayVirtualKeyResolver func(value string) (id string, ok bool)

// NewAPIGatewayHandler creates an empty handler when persistence is disabled.
// This makes the feature opt-in while retaining a single nil check for normal
// requests; registered routes are loaded and compiled once during startup.
func NewAPIGatewayHandler(store configstore.APIGatewayRouteStore, factory *network.HTTPClientFactory, resolveVirtualKey APIGatewayVirtualKeyResolver) (*APIGatewayHandler, error) {
	handler := &APIGatewayHandler{resolveVirtualKey: resolveVirtualKey}
	if factory != nil {
		handler.client = factory.GetFasthttpClient(network.ClientPurposeAPI)
	} else {
		handler.client = &fasthttp.Client{}
	}
	if store == nil {
		return handler, nil
	}
	routes, err := store.ListAPIGatewayRoutes(context.Background())
	if err != nil {
		return nil, fmt.Errorf("load api gateway routes: %w", err)
	}
	if err := handler.ReplaceRoutes(routes); err != nil {
		return nil, err
	}
	return handler, nil
}

// ReplaceRoutes atomically installs a new compiled routing snapshot.
func (h *APIGatewayHandler) ReplaceRoutes(routes []tables.TableAPIGatewayRoute) error {
	compiled, err := compileAPIGatewayRoutes(routes)
	if err != nil {
		return err
	}
	h.routes.Store(&compiled)
	return nil
}

// Wrap gives managed API routes precedence over Bifrost's built-in and UI
// routes. A gateway with no routes performs only the atomic nil/length check.
func (h *APIGatewayHandler) Wrap(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		endpoint, isGatewayRequest := apiGatewayEndpoint(ctx.Path())
		if !isGatewayRequest {
			next(ctx)
			return
		}
		routes := h.routes.Load()
		if routes != nil {
			for i := range *routes {
				if (*routes)[i].matches(endpoint) {
					if !(*routes)[i].permitsVirtualKey(ctx, h.resolveVirtualKey) {
						SendError(ctx, fasthttp.StatusUnauthorized, "a permitted virtual key is required for this api gateway route")
						return
					}
					h.proxy(ctx, &(*routes)[i], endpoint)
					return
				}
			}
		}
		next(ctx)
	}
}

func compileAPIGatewayRoutes(routes []tables.TableAPIGatewayRoute) ([]compiledAPIGatewayRoute, error) {
	compiled := make([]compiledAPIGatewayRoute, 0, len(routes))
	seen := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if !route.Enabled {
			continue
		}
		if _, exists := seen[route.ID]; exists {
			return nil, fmt.Errorf("duplicate api gateway route id %q", route.ID)
		}
		seen[route.ID] = struct{}{}
		predicates, filters, virtualKeyIDs, target, err := parseAPIGatewayRoute(route.ID, route.URI, route.PredicatesJSON, route.FiltersJSON, route.VirtualKeyIDsJSON)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, compiledAPIGatewayRoute{id: route.ID, target: target, predicates: predicates, filters: filters, virtualKeyIDs: virtualKeyIDs})
	}
	return compiled, nil
}

func parseAPIGatewayRoute(id, rawURI, predicatesJSON, filtersJSON, virtualKeyIDsJSON string) ([][]*regexp.Regexp, []rewritePathFilter, map[string]struct{}, *url.URL, error) {
	if strings.TrimSpace(id) == "" {
		return nil, nil, nil, nil, fmt.Errorf("api gateway route id is required")
	}
	target, err := url.Parse(rawURI)
	if err != nil || target.Scheme == "" || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return nil, nil, nil, nil, fmt.Errorf("api gateway route %q uri must be an absolute http(s) URL without credentials, query parameters, or a fragment", id)
	}
	var rawPredicates, rawFilters, rawVirtualKeyIDs []string
	if err := sonic.UnmarshalString(predicatesJSON, &rawPredicates); err != nil || len(rawPredicates) == 0 {
		return nil, nil, nil, nil, fmt.Errorf("api gateway route %q requires Path predicates", id)
	}
	if filtersJSON != "" && sonic.UnmarshalString(filtersJSON, &rawFilters) != nil {
		return nil, nil, nil, nil, fmt.Errorf("api gateway route %q has invalid filters", id)
	}
	if virtualKeyIDsJSON != "" && sonic.UnmarshalString(virtualKeyIDsJSON, &rawVirtualKeyIDs) != nil {
		return nil, nil, nil, nil, fmt.Errorf("api gateway route %q has invalid virtual key ids", id)
	}
	predicates := make([][]*regexp.Regexp, 0, len(rawPredicates))
	for _, predicate := range rawPredicates {
		name, arguments, ok := strings.Cut(predicate, "=")
		if !ok || name != "Path" || strings.TrimSpace(arguments) == "" {
			return nil, nil, nil, nil, fmt.Errorf("api gateway route %q supports predicates only in the form Path=/path", id)
		}
		paths := strings.Split(arguments, ",")
		alternatives := make([]*regexp.Regexp, 0, len(paths))
		for _, path := range paths {
			path = strings.TrimSpace(path)
			if !strings.HasPrefix(path, "/") {
				return nil, nil, nil, nil, fmt.Errorf("api gateway route %q Path predicates must start with /", id)
			}
			alternatives = append(alternatives, mustCompileGatewayPath(path))
		}
		predicates = append(predicates, alternatives)
	}
	filters := make([]rewritePathFilter, 0, len(rawFilters))
	for _, filter := range rawFilters {
		name, arguments, ok := strings.Cut(filter, "=")
		if !ok || name != "RewritePath" {
			return nil, nil, nil, nil, fmt.Errorf("api gateway route %q supports filters only in the form RewritePath=regexp,replacement", id)
		}
		expression, replacement, ok := strings.Cut(arguments, ",")
		if !ok || expression == "" {
			return nil, nil, nil, nil, fmt.Errorf("api gateway route %q has an invalid RewritePath filter", id)
		}
		pattern, err := regexp.Compile(expression)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("api gateway route %q has an invalid RewritePath expression: %w", id, err)
		}
		filters = append(filters, rewritePathFilter{pattern: pattern, replacement: replacement})
	}
	virtualKeyIDs := make(map[string]struct{}, len(rawVirtualKeyIDs))
	for _, virtualKeyID := range rawVirtualKeyIDs {
		virtualKeyID = strings.TrimSpace(virtualKeyID)
		if virtualKeyID == "" {
			return nil, nil, nil, nil, fmt.Errorf("api gateway route %q has an empty virtual key id", id)
		}
		virtualKeyIDs[virtualKeyID] = struct{}{}
	}
	return predicates, filters, virtualKeyIDs, target, nil
}

func (r *compiledAPIGatewayRoute) matches(path string) bool {
	for _, alternatives := range r.predicates {
		matched := false
		for _, predicate := range alternatives {
			if predicate.MatchString(path) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func (r *compiledAPIGatewayRoute) permitsVirtualKey(ctx *fasthttp.RequestCtx, resolve APIGatewayVirtualKeyResolver) bool {
	if len(r.virtualKeyIDs) == 0 {
		return true
	}
	if resolve == nil {
		return false
	}
	value, _ := lib.ResolveVirtualKeyFromHeaders(ctx)
	id, ok := resolve(value)
	if !ok {
		return false
	}
	_, allowed := r.virtualKeyIDs[id]
	return allowed
}

func (h *APIGatewayHandler) proxy(ctx *fasthttp.RequestCtx, route *compiledAPIGatewayRoute, path string) {
	if string(ctx.Method()) == fasthttp.MethodConnect {
		SendError(ctx, fasthttp.StatusMethodNotAllowed, "CONNECT is not supported by api gateway routes")
		return
	}
	for _, filter := range route.filters {
		path = filter.pattern.ReplaceAllString(path, filter.replacement)
	}
	request := fasthttp.AcquireRequest()
	response := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(request)
	defer fasthttp.ReleaseResponse(response)
	ctx.Request.CopyTo(request)
	removeHopByHopHeaders(&request.Header)
	setForwardedHeaders(ctx, &request.Header)
	request.URI().SetScheme(route.target.Scheme)
	request.URI().SetHost(route.target.Host)
	request.URI().SetPath(joinGatewayPaths(route.target.Path, path))
	request.URI().SetQueryStringBytes(ctx.URI().QueryString())
	request.Header.SetHost(route.target.Host)
	if err := h.client.Do(request, response); err != nil {
		SendError(ctx, fasthttp.StatusBadGateway, fmt.Sprintf("api gateway route %q upstream request failed", route.id))
		return
	}
	removeHopByHopHeaders(&response.Header)
	response.CopyTo(&ctx.Response)
	ctx.SetUserValue(string(schemas.BifrostContextKeyHTTPRoute), "api_gateway:"+route.id)
}

// apiGatewayEndpoint keeps proxied routes in their own namespace. Route
// predicates and filters operate on the path after /apis, so Path=/health is
// exposed as /apis/health and RewritePath remains independent of that prefix.
func apiGatewayEndpoint(path []byte) (string, bool) {
	if !bytes.HasPrefix(path, apiGatewayPathPrefixBytes) {
		return "", false
	}
	return string(path[len(apiGatewayPathPrefixBytes)-1:]), true
}

// APIGatewayRoutesHandler exposes CRUD for upstream APIs, mirroring the
// provider-management pattern while keeping this generic HTTP proxy separate
// from the LLM provider registry.
type APIGatewayRoutesHandler struct {
	store   configstore.APIGatewayRouteStore
	runtime *APIGatewayHandler
}

func NewAPIGatewayRoutesHandler(store configstore.APIGatewayRouteStore, runtime *APIGatewayHandler) *APIGatewayRoutesHandler {
	return &APIGatewayRoutesHandler{store: store, runtime: runtime}
}

func (h *APIGatewayRoutesHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/api-gateway/routes", lib.ChainMiddlewares(h.list, middlewares...))
	r.POST("/api/api-gateway/routes", lib.ChainMiddlewares(h.create, middlewares...))
	r.PUT("/api/api-gateway/routes/{id}", lib.ChainMiddlewares(h.update, middlewares...))
	r.DELETE("/api/api-gateway/routes/{id}", lib.ChainMiddlewares(h.delete, middlewares...))
}

func (h *APIGatewayRoutesHandler) list(ctx *fasthttp.RequestCtx) {
	if h.store == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "api gateway route storage is unavailable")
		return
	}
	routes, err := h.store.ListAPIGatewayRoutes(ctx)
	if err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to list api gateway routes")
		return
	}
	SendJSON(ctx, map[string]any{"routes": apiGatewayRouteResponses(routes)})
}

func (h *APIGatewayRoutesHandler) create(ctx *fasthttp.RequestCtx) {
	if h.store == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "api gateway route storage is unavailable")
		return
	}
	route, err := decodeAPIGatewayRoute(ctx.PostBody(), "")
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.CreateAPIGatewayRoute(ctx, &route); err != nil {
		status := fasthttp.StatusInternalServerError
		if err == configstore.ErrAlreadyExists {
			status = fasthttp.StatusConflict
		}
		SendError(ctx, status, "failed to create api gateway route")
		return
	}
	if err := h.reload(ctx); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to activate api gateway route")
		return
	}
	SendJSONWithStatus(ctx, apiGatewayRouteResponses([]tables.TableAPIGatewayRoute{route})[0], fasthttp.StatusCreated)
}

func (h *APIGatewayRoutesHandler) update(ctx *fasthttp.RequestCtx) {
	if h.store == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "api gateway route storage is unavailable")
		return
	}
	id, _ := ctx.UserValue("id").(string)
	if id == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "route id is required")
		return
	}
	route, err := decodeAPIGatewayRoute(ctx.PostBody(), id)
	if err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.UpdateAPIGatewayRoute(ctx, &route); err != nil {
		status := fasthttp.StatusInternalServerError
		if err == configstore.ErrNotFound {
			status = fasthttp.StatusNotFound
		}
		SendError(ctx, status, "failed to update api gateway route")
		return
	}
	if err := h.reload(ctx); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to activate api gateway route")
		return
	}
	SendJSON(ctx, apiGatewayRouteResponses([]tables.TableAPIGatewayRoute{route})[0])
}

func (h *APIGatewayRoutesHandler) delete(ctx *fasthttp.RequestCtx) {
	if h.store == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "api gateway route storage is unavailable")
		return
	}
	id, _ := ctx.UserValue("id").(string)
	if id == "" {
		SendError(ctx, fasthttp.StatusBadRequest, "route id is required")
		return
	}
	if err := h.store.DeleteAPIGatewayRoute(ctx, id); err != nil {
		status := fasthttp.StatusInternalServerError
		if err == configstore.ErrNotFound {
			status = fasthttp.StatusNotFound
		}
		SendError(ctx, status, "failed to delete api gateway route")
		return
	}
	if err := h.reload(ctx); err != nil {
		SendError(ctx, fasthttp.StatusInternalServerError, "failed to activate api gateway route")
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

func (h *APIGatewayRoutesHandler) reload(ctx *fasthttp.RequestCtx) error {
	routes, err := h.store.ListAPIGatewayRoutes(ctx)
	if err != nil {
		return err
	}
	return h.runtime.ReplaceRoutes(routes)
}

func decodeAPIGatewayRoute(body []byte, pathID string) (tables.TableAPIGatewayRoute, error) {
	var payload apiGatewayRoutePayload
	if err := sonic.Unmarshal(body, &payload); err != nil {
		return tables.TableAPIGatewayRoute{}, fmt.Errorf("invalid request payload")
	}
	if pathID != "" {
		if payload.ID != "" && payload.ID != pathID {
			return tables.TableAPIGatewayRoute{}, fmt.Errorf("route id cannot be changed")
		}
		payload.ID = pathID
	}
	if payload.Enabled == nil {
		payload.Enabled = schemas.Ptr(true)
	}
	predicates, err := sonic.MarshalString(payload.Predicates)
	if err != nil {
		return tables.TableAPIGatewayRoute{}, fmt.Errorf("invalid predicates")
	}
	filters, err := sonic.MarshalString(payload.Filters)
	if err != nil {
		return tables.TableAPIGatewayRoute{}, fmt.Errorf("invalid filters")
	}
	virtualKeyIDs, err := sonic.MarshalString(payload.VirtualKeyIDs)
	if err != nil {
		return tables.TableAPIGatewayRoute{}, fmt.Errorf("invalid virtual key ids")
	}
	route := tables.TableAPIGatewayRoute{ID: payload.ID, URI: payload.URI, PredicatesJSON: predicates, FiltersJSON: filters, VirtualKeyIDsJSON: virtualKeyIDs, Enabled: *payload.Enabled}
	_, _, _, _, err = parseAPIGatewayRoute(route.ID, route.URI, route.PredicatesJSON, route.FiltersJSON, route.VirtualKeyIDsJSON)
	return route, err
}

func apiGatewayRouteResponses(routes []tables.TableAPIGatewayRoute) []apiGatewayRouteResponse {
	result := make([]apiGatewayRouteResponse, 0, len(routes))
	for _, route := range routes {
		var predicates, filters, virtualKeyIDs []string
		_ = sonic.UnmarshalString(route.PredicatesJSON, &predicates)
		_ = sonic.UnmarshalString(route.FiltersJSON, &filters)
		_ = sonic.UnmarshalString(route.VirtualKeyIDsJSON, &virtualKeyIDs)
		if filters == nil {
			filters = []string{}
		}
		if virtualKeyIDs == nil {
			virtualKeyIDs = []string{}
		}
		result = append(result, apiGatewayRouteResponse{ID: route.ID, URI: route.URI, Predicates: predicates, Filters: filters, VirtualKeyIDs: virtualKeyIDs, Enabled: route.Enabled})
	}
	return result
}

func mustCompileGatewayPath(pattern string) *regexp.Regexp {
	var expression strings.Builder
	expression.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				expression.WriteString(".*")
				i++
			} else {
				expression.WriteString("[^/]*")
			}
		case '?':
			expression.WriteString("[^/]")
		default:
			expression.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	expression.WriteString("$")
	return regexp.MustCompile(expression.String())
}

func joinGatewayPaths(base, requestPath string) string {
	if base == "" || base == "/" {
		return requestPath
	}
	if requestPath == "" || requestPath == "/" {
		return base
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(requestPath, "/")
}

func setForwardedHeaders(ctx *fasthttp.RequestCtx, header *fasthttp.RequestHeader) {
	clientIP := ctx.RemoteIP().String()
	if existing := header.Peek("X-Forwarded-For"); len(existing) > 0 {
		header.Set("X-Forwarded-For", string(existing)+", "+clientIP)
	} else {
		header.Set("X-Forwarded-For", clientIP)
	}
	if header.Peek("X-Forwarded-Proto") == nil {
		if ctx.IsTLS() {
			header.Set("X-Forwarded-Proto", "https")
		} else {
			header.Set("X-Forwarded-Proto", "http")
		}
	}
}

type hopByHopHeader interface {
	Peek(string) []byte
	Del(string)
}

func removeHopByHopHeaders(header hopByHopHeader) {
	connection := string(header.Peek("Connection"))
	for _, name := range strings.Split(connection, ",") {
		if name = strings.TrimSpace(name); name != "" {
			header.Del(name)
		}
	}
	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		header.Del(name)
	}
}

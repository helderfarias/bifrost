package handlers

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func TestAPIGatewayProxyRewritesPathAndPreservesRequest(t *testing.T) {
	var receivedPath, receivedQuery, receivedBody, receivedHeader string
	upstreamURL, upstreamDial := startGatewayUpstream(t, func(ctx *fasthttp.RequestCtx) {
		receivedPath = string(ctx.Path())
		receivedQuery = string(ctx.QueryArgs().QueryString())
		receivedBody = string(ctx.PostBody())
		receivedHeader = string(ctx.Request.Header.Peek("X-Original-Header"))
		ctx.Response.Header.Set("X-Upstream", "yes")
		ctx.SetStatusCode(fasthttp.StatusCreated)
		ctx.SetBodyString("proxied")
	})

	handler, err := NewAPIGatewayHandler(nil, nil, nil)
	require.NoError(t, err)
	handler.client = &fasthttp.Client{Dial: upstreamDial}
	require.NoError(t, handler.ReplaceRoutes([]tables.TableAPIGatewayRoute{gatewayRoute(t, "health-proxy", upstreamURL, []string{"Path=/health"}, []string{"RewritePath=/health,/actuator/health"})}))

	status, body, responseHeader := serveGatewayRequest(t, handler, fasthttp.MethodPost, "/apis/health?verbose=true", []byte("ping"), map[string]string{"X-Original-Header": "kept"})
	require.Equal(t, fasthttp.StatusCreated, status)
	require.Equal(t, "proxied", body)
	require.Equal(t, "yes", responseHeader)
	require.Equal(t, "/actuator/health", receivedPath)
	require.Equal(t, "verbose=true", receivedQuery)
	require.Equal(t, "ping", receivedBody)
	require.Equal(t, "kept", receivedHeader)
}

func TestAPIGatewayRoutesCRUDReloadsRuntime(t *testing.T) {
	store := &memoryAPIGatewayStore{}
	runtime, err := NewAPIGatewayHandler(store, nil, nil)
	require.NoError(t, err)
	h := NewAPIGatewayRoutesHandler(store, runtime)
	r := router.New()
	h.RegisterRoutes(r)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(fasthttp.MethodPost)
	ctx.Request.SetRequestURI("http://gateway.test/api/api-gateway/routes")
	ctx.Request.SetBodyString(`{"id":"health-proxy","uri":"http://example.test","predicates":["Path=/health"],"filters":["RewritePath=/health,/actuator/health"],"virtual_key_ids":["vk-health"]}`)
	r.Handler(ctx)
	require.Equal(t, fasthttp.StatusCreated, ctx.Response.StatusCode())
	require.NotNil(t, runtime.routes.Load())
	require.Len(t, *runtime.routes.Load(), 1)
	_, hasVirtualKey := (*runtime.routes.Load())[0].virtualKeyIDs["vk-health"]
	require.True(t, hasVirtualKey)

	ctx = &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(fasthttp.MethodDelete)
	ctx.Request.SetRequestURI("http://gateway.test/api/api-gateway/routes/health-proxy")
	r.Handler(ctx)
	require.Equal(t, fasthttp.StatusNoContent, ctx.Response.StatusCode())
	require.Empty(t, *runtime.routes.Load())
}

func TestAPIGatewayRoutesListReturnsEmptyFilters(t *testing.T) {
	store := &memoryAPIGatewayStore{routes: map[string]tables.TableAPIGatewayRoute{
		"passthrough": gatewayRoute(t, "passthrough", "http://example.test", []string{"Path=/passthrough"}, nil),
	}}
	runtime, err := NewAPIGatewayHandler(store, nil, nil)
	require.NoError(t, err)
	h := NewAPIGatewayRoutesHandler(store, runtime)
	r := router.New()
	h.RegisterRoutes(r)

	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI("http://gateway.test/api/api-gateway/routes")
	r.Handler(ctx)
	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
	require.JSONEq(t, `{"routes":[{"id":"passthrough","uri":"http://example.test","predicates":["Path=/passthrough"],"filters":[],"enabled":true,"virtual_key_ids":[]}]}`, string(ctx.Response.Body()))
}

func TestAPIGatewayRouteRequiresPermittedVirtualKey(t *testing.T) {
	handler, err := NewAPIGatewayHandler(nil, nil, func(value string) (string, bool) {
		if value == "sk-bf-permitted" {
			return "vk-permitted", true
		}
		return "", false
	})
	require.NoError(t, err)
	route := gatewayRoute(t, "private-api", "http://upstream.test", []string{"Path=/private"}, nil)
	route.VirtualKeyIDsJSON = `["vk-permitted"]`
	require.NoError(t, handler.ReplaceRoutes([]tables.TableAPIGatewayRoute{route}))

	status, _, _ := serveGatewayRequest(t, handler, fasthttp.MethodGet, "/apis/private", nil, nil)
	require.Equal(t, fasthttp.StatusUnauthorized, status)

	status, _, _ = serveGatewayRequest(t, handler, fasthttp.MethodGet, "/apis/private", nil, map[string]string{"x-bf-vk": "sk-bf-permitted"})
	require.Equal(t, fasthttp.StatusBadGateway, status)
}

func TestAPIGatewayProxyFallsThroughWhenNoRouteMatches(t *testing.T) {
	handler, err := NewAPIGatewayHandler(nil, nil, nil)
	require.NoError(t, err)
	require.NoError(t, handler.ReplaceRoutes([]tables.TableAPIGatewayRoute{gatewayRoute(t, "health-proxy", "http://upstream.test", []string{"Path=/health"}, nil)}))
	status, body, _ := serveGatewayRequest(t, handler, fasthttp.MethodGet, "/health", nil, nil)
	require.Equal(t, fasthttp.StatusTeapot, status)
	require.Equal(t, "bifrost", body)
}

func BenchmarkAPIGatewayNoMatch(b *testing.B) {
	handler, err := NewAPIGatewayHandler(nil, nil, nil)
	if err != nil {
		b.Fatal(err)
	}
	predicates, _ := sonic.MarshalString([]string{"Path=/health"})
	if err := handler.ReplaceRoutes([]tables.TableAPIGatewayRoute{{ID: "health-proxy", URI: "http://upstream.test", PredicatesJSON: predicates, FiltersJSON: "[]", Enabled: true}}); err != nil {
		b.Fatal(err)
	}
	wrapped := handler.Wrap(func(*fasthttp.RequestCtx) {})
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI("http://gateway.test/v1/chat/completions")
	b.ReportAllocs()
	for b.Loop() {
		wrapped(ctx)
	}
}

func gatewayRoute(t *testing.T, id, uri string, predicates, filters []string) tables.TableAPIGatewayRoute {
	t.Helper()
	predicatesJSON, err := sonic.MarshalString(predicates)
	require.NoError(t, err)
	filtersJSON, err := sonic.MarshalString(filters)
	require.NoError(t, err)
	return tables.TableAPIGatewayRoute{ID: id, URI: uri, PredicatesJSON: predicatesJSON, FiltersJSON: filtersJSON, Enabled: true}
}

func startGatewayUpstream(t *testing.T, handler fasthttp.RequestHandler) (string, fasthttp.DialFunc) {
	t.Helper()
	server := &fasthttp.Server{Handler: handler}
	listener := fasthttputil.NewInmemoryListener()
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = listener.Close(); _ = server.Shutdown() })
	return "http://upstream.test", func(string) (net.Conn, error) { return listener.Dial() }
}

func serveGatewayRequest(t *testing.T, handler *APIGatewayHandler, method, requestURI string, body []byte, headers map[string]string) (int, string, string) {
	t.Helper()
	server := &fasthttp.Server{Handler: handler.Wrap(func(ctx *fasthttp.RequestCtx) { ctx.SetStatusCode(fasthttp.StatusTeapot); ctx.SetBodyString("bifrost") })}
	listener := fasthttputil.NewInmemoryListener()
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = listener.Close(); _ = server.Shutdown() })
	client := &fasthttp.Client{Dial: func(string) (net.Conn, error) { return listener.Dial() }, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	request, response := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(request)
	defer fasthttp.ReleaseResponse(response)
	request.Header.SetMethod(method)
	request.SetRequestURI("http://gateway.test" + requestURI)
	request.SetBody(body)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	require.NoError(t, client.Do(request, response))
	return response.StatusCode(), string(response.Body()), string(response.Header.Peek("X-Upstream"))
}

type memoryAPIGatewayStore struct {
	mu     sync.Mutex
	routes map[string]tables.TableAPIGatewayRoute
}

func (s *memoryAPIGatewayStore) ListAPIGatewayRoutes(context.Context) ([]tables.TableAPIGatewayRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	routes := make([]tables.TableAPIGatewayRoute, 0, len(s.routes))
	for _, route := range s.routes {
		routes = append(routes, route)
	}
	return routes, nil
}
func (s *memoryAPIGatewayStore) CreateAPIGatewayRoute(_ context.Context, route *tables.TableAPIGatewayRoute) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.routes == nil {
		s.routes = map[string]tables.TableAPIGatewayRoute{}
	}
	if _, ok := s.routes[route.ID]; ok {
		return configstore.ErrAlreadyExists
	}
	s.routes[route.ID] = *route
	return nil
}
func (s *memoryAPIGatewayStore) UpdateAPIGatewayRoute(_ context.Context, route *tables.TableAPIGatewayRoute) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.routes[route.ID]; !ok {
		return configstore.ErrNotFound
	}
	s.routes[route.ID] = *route
	return nil
}
func (s *memoryAPIGatewayStore) DeleteAPIGatewayRoute(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.routes[id]; !ok {
		return configstore.ErrNotFound
	}
	delete(s.routes, id)
	return nil
}

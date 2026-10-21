package apigateway

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testKey = "0123456789abcdef0123456789abcdef"

func testRoute(upstream string) Route {
	return Route{ID: "ocr", Name: "OCR", Path: "/api/v2/ocr", Upstream: upstream, StripPrefix: true, RelayAuthorization: true, Enabled: true, Capabilities: []string{"svc.ocr", "svc.all"}, Consumers: []Consumer{{Name: "app", TokenHash: fmt.Sprintf("%x", sha256.Sum256([]byte(testKey))), Capabilities: []string{"svc.ocr"}}}, RequestsPerMinute: 60, TimeoutSeconds: 2, FailureThreshold: 2, RecoverySeconds: 1}
}
func call(g *Gateway, method, path, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader("document"))
	r.Header.Set("X-Bifrost-API-Key", key)
	r.Header.Set("Authorization", "Bearer upstream-token")
	r.Header.Set("Cookie", "dashboard=secret")
	r.Header.Set("X-Bifrost-Capabilities", "svc.all")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}
func TestOCRProxy(t *testing.T) {
	var seen int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		if r.URL.RequestURI() != "/backend/jobs?format=json" {
			t.Errorf("URI: %s", r.URL.RequestURI())
		}
		if r.Method != "POST" {
			t.Errorf("method: %s", r.Method)
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != "document" {
			t.Errorf("body: %s", b)
		}
		if r.Header.Get("Authorization") != "Bearer upstream-token" || r.Header.Get("X-Bifrost-API-Key") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Bifrost-Capabilities") != "" {
			t.Errorf("credential/header isolation failed")
		}
		w.Header().Set("X-OCR", "done")
		w.WriteHeader(201)
		w.Write([]byte(`{"text":"scanned"}`))
	}))
	defer upstream.Close()
	g := New()
	r := testRoute(upstream.URL + "/backend")
	if err := g.Replace([]Route{r}); err != nil {
		t.Fatal(err)
	}
	w := call(g, "POST", "/api/v2/ocr/jobs?format=json", testKey)
	if w.Code != 201 || w.Header().Get("X-OCR") != "done" || w.Body.String() != `{"text":"scanned"}` || seen != 1 {
		t.Fatalf("proxy response: %d %s", w.Code, w.Body.String())
	}
}
func TestAccessQuotaAndMatching(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer upstream.Close()
	cases := []struct {
		name, path, key string
		configure       func(*Route)
		want            int
	}{
		{name: "missing key", path: "/api/v2/ocr", want: 401},
		{name: "invalid key", path: "/api/v2/ocr", key: "bad", want: 401},
		{name: "capability denied", path: "/api/v2/ocr", key: testKey, configure: func(r *Route) { r.Consumers[0].Capabilities = []string{"svc.other"} }, want: 403},
		{name: "capability any match", path: "/api/v2/ocr", key: testKey, configure: func(r *Route) { r.Consumers[0].Capabilities = []string{"svc.all"} }, want: 200},
		{name: "boundary", path: "/api/v2/ocr-other", key: testKey, want: 404},
		{name: "disabled", path: "/api/v2/ocr", key: testKey, configure: func(r *Route) { r.Enabled = false }, want: 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := New()
			r := testRoute(upstream.URL)
			if tc.configure != nil {
				tc.configure(&r)
			}
			if err := g.Replace([]Route{r}); err != nil {
				t.Fatal(err)
			}
			if w := call(g, "GET", tc.path, tc.key); w.Code != tc.want {
				t.Fatalf("got %d, want %d", w.Code, tc.want)
			}
		})
	}
	g := New()
	r := testRoute(upstream.URL)
	r.RequestsPerMinute = 1
	_ = g.Replace([]Route{r})
	if w := call(g, "GET", r.Path, testKey); w.Code != 200 {
		t.Fatal(w.Code)
	}
	// Saving an unrelated edit cannot reset the quota.
	r.Name = "Edited"
	_ = g.Replace([]Route{r})
	if w := call(g, "GET", r.Path, testKey); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("quota: %d", w.Code)
	}
}
func TestCircuitAndRecovery(t *testing.T) {
	var mu sync.Mutex
	fail := true
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if fail {
			w.WriteHeader(500)
		} else {
			w.Write([]byte("recovered"))
		}
	}))
	defer upstream.Close()
	g := New()
	r := testRoute(upstream.URL)
	_ = g.Replace([]Route{r})
	for i := 0; i < 2; i++ {
		if w := call(g, "GET", r.Path, testKey); w.Code != 500 {
			t.Fatal(w.Code)
		}
	}
	if w := call(g, "GET", r.Path, testKey); w.Code != 503 {
		t.Fatal(w.Code)
	}
	mu.Lock()
	fail = false
	if calls != 2 {
		t.Fatal(calls)
	}
	mu.Unlock()
	// Advance only the test's circuit deadline; production uses the configured wait.
	g.routes[0].state.mu.Lock()
	g.routes[0].state.until = time.Now().Add(-time.Second)
	g.routes[0].state.mu.Unlock()
	if w := call(g, "GET", r.Path, testKey); w.Code != 200 || w.Body.String() != "recovered" {
		t.Fatalf("recovery: %d", w.Code)
	}
}
func TestLongestPrefixAndRelayDisabled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("authorization was relayed")
		}
		w.Write([]byte(r.URL.Path))
	}))
	defer upstream.Close()
	g := New()
	r := testRoute(upstream.URL)
	child := testRoute(upstream.URL)
	child.ID = "jobs"
	child.Path += "/jobs"
	child.RelayAuthorization = false
	if err := g.Replace([]Route{r, child}); err != nil {
		t.Fatal(err)
	}
	if w := call(g, "GET", child.Path+"/123", testKey); w.Code != 200 || w.Body.String() != "/123" {
		t.Fatalf("longest prefix: %d %s", w.Code, w.Body.String())
	}
}
func TestValidation(t *testing.T) {
	for _, p := range []string{"/api/config", "/health", "/api/v2/ocr/", "/gateway/a/../b", "/gateway/%2f", "/gateway/{path:*}"} {
		r := testRoute("http://localhost:3000")
		r.Path = p
		if Validate([]Route{r}) == nil {
			t.Errorf("accepted path %s", p)
		}
	}
	for _, u := range []string{"file:///etc/passwd", "http://user:pass@localhost", "http://169.254.169.254/", "http://0.0.0.0", "http://localhost?token=secret"} {
		r := testRoute(u)
		if Validate([]Route{r}) == nil {
			t.Errorf("accepted upstream %s", u)
		}
	}
	r := testRoute("http://localhost:3000")
	r.Consumers = nil
	if Validate([]Route{r}) == nil {
		t.Error("accepted unauthenticated route")
	}
}
func TestConcurrentReplacement(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer upstream.Close()
	g := New()
	r := testRoute(upstream.URL)
	r.RequestsPerMinute = 0
	_ = g.Replace([]Route{r})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := g.Replace([]Route{r}); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if w := call(g, "GET", r.Path, testKey); w.Code != 204 {
				t.Error(w.Code)
			}
		}()
	}
	wg.Wait()
}
func TestUpstreamFailure(t *testing.T) {
	g := New()
	r := testRoute("http://127.0.0.1:1")
	r.FailureThreshold = 1
	_ = g.Replace([]Route{r})
	if w := call(g, "GET", r.Path, testKey); w.Code != 502 {
		t.Fatal(w.Code)
	}
	if w := call(g, "GET", r.Path, testKey); w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestConsumerQuotasAreIndependent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer upstream.Close()
	g := New()
	r := testRoute(upstream.URL)
	r.RequestsPerMinute = 1
	other := "another-consumer-key-0123456789abcdef"
	r.Consumers = append(r.Consumers, Consumer{Name: "other", TokenHash: fmt.Sprintf("%x", sha256.Sum256([]byte(other))), Capabilities: []string{"svc.ocr"}})
	if err := g.Replace([]Route{r}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{testKey, other} {
		if w := call(g, "GET", r.Path, key); w.Code != 204 {
			t.Fatal(w.Code)
		}
	}
	if w := call(g, "GET", r.Path, testKey); w.Code != 429 {
		t.Fatal(w.Code)
	}
}
func TestTimeoutOpensCircuit(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer upstream.Close()
	defer close(release)
	g := New()
	r := testRoute(upstream.URL)
	r.TimeoutSeconds = 1
	r.FailureThreshold = 1
	if err := g.Replace([]Route{r}); err != nil {
		t.Fatal(err)
	}
	w := call(g, "GET", r.Path, testKey)
	if w.Code != 503 && w.Code != 502 {
		t.Fatalf("timeout: %d", w.Code)
	}
	deadline := time.Now().Add(time.Second)
	for {
		g.routes[0].state.mu.Lock()
		open := !g.routes[0].state.until.IsZero()
		g.routes[0].state.mu.Unlock()
		if open {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout did not open circuit")
		}
		time.Sleep(time.Millisecond)
	}
	if w := call(g, "GET", r.Path, testKey); w.Code != 503 {
		t.Fatalf("open circuit: %d", w.Code)
	}
}
func BenchmarkRejectUnauthorized(b *testing.B) {
	g := New()
	r := testRoute("http://localhost:3000")
	_ = g.Replace([]Route{r})
	request := httptest.NewRequest("GET", r.Path, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.ServeHTTP(httptest.NewRecorder(), request)
	}
}

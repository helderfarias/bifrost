// Package apigateway routes application HTTP APIs independently of LLM inference.
package apigateway

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Consumer struct {
	Name         string   `json:"name"`
	TokenHash    string   `json:"token_sha256"`
	Capabilities []string `json:"capabilities"`
}

type Route struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Upstream           string     `json:"upstream"`
	Path               string     `json:"path"`
	StripPrefix        bool       `json:"strip_prefix"`
	RelayAuthorization bool       `json:"relay_authorization"`
	Enabled            bool       `json:"enabled"`
	Capabilities       []string   `json:"capabilities"`
	Consumers          []Consumer `json:"consumers"`
	RequestsPerMinute  int        `json:"requests_per_minute"`
	TimeoutSeconds     int        `json:"timeout_seconds"`
	FailureThreshold   int        `json:"failure_threshold"`
	RecoverySeconds    int        `json:"recovery_seconds"`
}

// Validate reserves the control plane and inference namespaces. /api/v2 supports
// migrations such as /api/v2/ocr; /gateway is available for other application APIs.
func Validate(routes []Route) error {
	if len(routes) > 128 {
		return errors.New("at most 128 API routes are supported")
	}
	ids, paths := map[string]bool{}, map[string]bool{}
	for _, r := range routes {
		if r.ID == "" || len(r.ID) > 120 || r.Name == "" || len(r.Name) > 120 || ids[r.ID] {
			return errors.New("route IDs must be unique and names and IDs must contain 1–120 characters")
		}
		ids[r.ID] = true
		if (!strings.HasPrefix(r.Path, "/api/v2/") && !strings.HasPrefix(r.Path, "/gateway/")) || path.Clean(r.Path) != r.Path || strings.ContainsAny(r.Path, "?#%\\{}*") || paths[r.Path] {
			return errors.New("use a unique, clean path below /api/v2/ or /gateway/ without a trailing slash")
		}
		paths[r.Path] = true
		u, err := url.Parse(r.Upstream)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("upstream must be an HTTP(S) URL without credentials, query or fragment")
		}
		if ip := net.ParseIP(u.Hostname()); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()) {
			return errors.New("upstream cannot address metadata, link-local or unspecified networks")
		}
		if r.TimeoutSeconds < 1 || r.TimeoutSeconds > 300 || r.RequestsPerMinute < 0 || r.RequestsPerMinute > 1000000 || r.FailureThreshold < 1 || r.FailureThreshold > 1000 || r.RecoverySeconds < 1 || r.RecoverySeconds > 3600 {
			return errors.New("invalid timeout, rate limit or circuit breaker settings")
		}
		if len(r.Consumers) == 0 || len(r.Consumers) > 64 || len(r.Capabilities) == 0 || len(r.Capabilities) > 64 {
			return errors.New("configure 1–64 consumers and required capabilities")
		}
		names, hashes := map[string]bool{}, map[string]bool{}
		for _, c := range r.Consumers {
			hash, err := hex.DecodeString(c.TokenHash)
			if err != nil || len(hash) != sha256.Size || c.Name == "" || len(c.Name) > 120 || names[c.Name] || hashes[c.TokenHash] || len(c.Capabilities) == 0 || len(c.Capabilities) > 64 {
				return errors.New("consumers need unique names, unique SHA-256 token hashes and capabilities")
			}
			names[c.Name] = true
			hashes[c.TokenHash] = true
		}
		for _, caps := range append([][]string{r.Capabilities}, consumerCapabilities(r.Consumers)...) {
			for _, cap := range caps {
				if strings.TrimSpace(cap) == "" || len(cap) > 120 {
					return errors.New("capabilities must contain 1–120 characters")
				}
			}
		}
	}
	return nil
}
func consumerCapabilities(cs []Consumer) [][]string {
	out := make([][]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Capabilities)
	}
	return out
}

type quota struct {
	minute int64
	used   int
}
type circuit struct {
	mu       sync.Mutex
	failures int
	until    time.Time
	probing  bool
	quotas   map[string]quota
}
type compiledRoute struct {
	config Route
	proxy  *httputil.ReverseProxy
	state  *circuit
}
type Gateway struct {
	mu     sync.RWMutex
	routes []*compiledRoute
}

func New() *Gateway { return &Gateway{} }

// Replace publishes an immutable routing snapshot. Quotas and circuit state are
// bounded by configured consumers, retained across edits and local to this process.
func (g *Gateway) Replace(routes []Route) error {
	if err := Validate(routes); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	previous := map[string]*circuit{}
	for _, r := range g.routes {
		previous[r.config.ID] = r.state
	}
	next := make([]*compiledRoute, 0, len(routes))
	for _, r := range routes {
		target, _ := url.Parse(r.Upstream)
		state := previous[r.ID]
		if state == nil {
			state = &circuit{quotas: map[string]quota{}}
		}
		// Remove counters for deleted consumers so repeated edits cannot grow memory.
		state.mu.Lock()
		allowed := map[string]bool{}
		for _, c := range r.Consumers {
			allowed[c.TokenHash] = true
		}
		for key := range state.quotas {
			if !allowed[key] {
				delete(state.quotas, key)
			}
		}
		state.mu.Unlock()
		cfg := r
		proxy := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				if cfg.StripPrefix {
					p := strings.TrimPrefix(pr.Out.URL.Path, cfg.Path)
					if p == "" {
						p = "/"
					}
					pr.Out.URL.Path = p
					pr.Out.URL.RawPath = ""
				}
				pr.SetURL(target)
				pr.Out.Header.Del("X-Bifrost-API-Key")
				// Dashboard cookies and caller-controlled identity headers never reach upstreams.
				pr.Out.Header.Del("Cookie")
				pr.Out.Header.Del("X-Bifrost-Capabilities")
				if !cfg.RelayAuthorization {
					pr.Out.Header.Del("Authorization")
				}
			},
			Transport: &http.Transport{Proxy: nil, DialContext: safeDialer.DialContext, MaxIdleConns: 32, MaxIdleConnsPerHost: 8, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: time.Duration(r.TimeoutSeconds) * time.Second},
			ModifyResponse: func(resp *http.Response) error {
				body, err := io.ReadAll(io.LimitReader(resp.Body, (32<<20)+1))
				resp.Body.Close()
				if err != nil {
					return err
				}
				if len(body) > 32<<20 {
					return errors.New("upstream response exceeds 32 MiB")
				}
				resp.Body = io.NopCloser(bytes.NewReader(body))
				state.finish(resp.StatusCode >= 500, cfg)
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
				state.finish(true, cfg)
				http.Error(w, "API upstream unavailable", http.StatusBadGateway)
			},
		}
		next = append(next, &compiledRoute{config: r, proxy: proxy, state: state})
	}
	sort.Slice(next, func(i, j int) bool { return len(next[i].config.Path) > len(next[j].config.Path) })
	old := g.routes
	g.routes = next
	for _, r := range old {
		r.proxy.Transport.(*http.Transport).CloseIdleConnections()
	}
	return nil
}

// Upstreams may be private services, but metadata and non-unicast addresses are
// rejected after DNS resolution as well as at configuration time.
var safeDialer = &net.Dialer{Timeout: 10 * time.Second, Control: func(network, address string, c syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return errors.New("upstream address is not allowed")
	}
	return nil
}}

func (s *circuit) finish(failed bool, r Route) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probing = false
	if failed {
		s.failures++
		if s.failures >= r.FailureThreshold {
			s.until = time.Now().Add(time.Duration(r.RecoverySeconds) * time.Second)
		}
	} else {
		s.failures = 0
		s.until = time.Time{}
	}
}
func (s *circuit) admit(r Route, consumer string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if !s.until.IsZero() && (now.Before(s.until) || s.probing) {
		return http.StatusServiceUnavailable
	}
	if r.RequestsPerMinute > 0 {
		q := s.quotas[consumer]
		minute := now.Unix() / 60
		if q.minute != minute {
			q = quota{minute: minute}
		}
		if q.used >= r.RequestsPerMinute {
			return http.StatusTooManyRequests
		}
		q.used++
		s.quotas[consumer] = q
	}
	if !s.until.IsZero() {
		s.probing = true
	}
	return 0
}
func (g *Gateway) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	g.mu.RLock()
	var match *compiledRoute
	for _, r := range g.routes {
		if r.config.Enabled && (req.URL.Path == r.config.Path || strings.HasPrefix(req.URL.Path, r.config.Path+"/")) {
			match = r
			break
		}
	}
	g.mu.RUnlock()
	if match == nil {
		http.NotFound(w, req)
		return
	}
	hash := sha256.Sum256([]byte(req.Header.Get("X-Bifrost-API-Key")))
	var consumer *Consumer
	for _, c := range match.config.Consumers {
		expected, _ := hex.DecodeString(c.TokenHash)
		if subtle.ConstantTimeCompare(hash[:], expected) == 1 {
			copy := c
			consumer = &copy
			break
		}
	}
	if consumer == nil {
		http.Error(w, "valid X-Bifrost-API-Key required", http.StatusUnauthorized)
		return
	}
	authorized := false
	for _, required := range match.config.Capabilities {
		for _, cap := range consumer.Capabilities {
			if cap == required {
				authorized = true
			}
		}
	}
	if !authorized {
		http.Error(w, "consumer lacks a required capability", http.StatusForbidden)
		return
	}
	if status := match.state.admit(match.config, consumer.TokenHash); status != 0 {
		if status == 429 {
			w.Header().Set("Retry-After", fmt.Sprint(60-time.Now().Unix()%60))
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	// TimeoutHandler caps the full exchange, including response bodies. Responses
	// are buffered; long-lived streaming protocols are outside this API workflow.
	http.TimeoutHandler(match.proxy, time.Duration(match.config.TimeoutSeconds)*time.Second, "API upstream timeout").ServeHTTP(w, req)
}

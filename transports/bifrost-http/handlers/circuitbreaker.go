package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/v2/bifrost-http/circuitbreaker"
	"github.com/maximhq/bifrost/transports/v2/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// CircuitBreakerReloader is implemented by the HTTP server. Changes are saved
// before reload so an invalid runtime configuration never becomes active.
type CircuitBreakerReloader interface {
	ReloadPlugin(ctx context.Context, name string, path *string, pluginConfig any, placement *schemas.PluginPlacement, order *int) error
}

// CircuitBreakerHandler exposes the documented policy CRUD and runtime state.
type CircuitBreakerHandler struct {
	store    configstore.ConfigStore
	reloader CircuitBreakerReloader
	plugin   func() *circuitbreaker.Plugin
}

func NewCircuitBreakerHandler(store configstore.ConfigStore, reloader CircuitBreakerReloader, plugin func() *circuitbreaker.Plugin) *CircuitBreakerHandler {
	return &CircuitBreakerHandler{store: store, reloader: reloader, plugin: plugin}
}

func (h *CircuitBreakerHandler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	r.GET("/api/circuit-breaker/policies", lib.ChainMiddlewares(h.listPolicies, middlewares...))
	r.POST("/api/circuit-breaker/policies", lib.ChainMiddlewares(h.createPolicy, middlewares...))
	r.PUT("/api/circuit-breaker/policies/{name}", lib.ChainMiddlewares(h.updatePolicy, middlewares...))
	r.DELETE("/api/circuit-breaker/policies/{name}", lib.ChainMiddlewares(h.deletePolicy, middlewares...))
	r.GET("/api/circuit-breaker/state", lib.ChainMiddlewares(h.getState, middlewares...))
}

func (h *CircuitBreakerHandler) listPolicies(ctx *fasthttp.RequestCtx) {
	config, err := h.config(ctx)
	if err != nil {
		h.sendConfigError(ctx, err)
		return
	}
	policies := config.Policies
	if policies == nil {
		policies = []circuitbreaker.Policy{}
	}
	SendJSON(ctx, map[string]any{"policies": policies, "count": len(policies)})
}

func (h *CircuitBreakerHandler) createPolicy(ctx *fasthttp.RequestCtx) {
	var policy circuitbreaker.Policy
	if err := json.Unmarshal(ctx.PostBody(), &policy); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "Invalid circuit breaker policy")
		return
	}
	config, err := h.config(ctx)
	if err != nil {
		h.sendConfigError(ctx, err)
		return
	}
	if slices.ContainsFunc(config.Policies, func(existing circuitbreaker.Policy) bool { return existing.Name == policy.Name }) {
		SendError(ctx, fasthttp.StatusConflict, "A circuit breaker policy with this name already exists")
		return
	}
	config.Policies = append(config.Policies, policy)
	if err := h.save(ctx, config); err != nil {
		h.sendSaveError(ctx, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusCreated)
	SendJSON(ctx, policy)
}

func (h *CircuitBreakerHandler) updatePolicy(ctx *fasthttp.RequestCtx) {
	name := ctx.UserValue("name").(string)
	var policy circuitbreaker.Policy
	if err := json.Unmarshal(ctx.PostBody(), &policy); err != nil {
		SendError(ctx, fasthttp.StatusBadRequest, "Invalid circuit breaker policy")
		return
	}
	if policy.Name != "" && policy.Name != name {
		SendError(ctx, fasthttp.StatusBadRequest, "Policy name cannot be changed")
		return
	}
	policy.Name = name
	config, err := h.config(ctx)
	if err != nil {
		h.sendConfigError(ctx, err)
		return
	}
	index := slices.IndexFunc(config.Policies, func(existing circuitbreaker.Policy) bool { return existing.Name == name })
	if index < 0 {
		SendError(ctx, fasthttp.StatusNotFound, "Circuit breaker policy not found")
		return
	}
	config.Policies[index] = policy
	if err := h.save(ctx, config); err != nil {
		h.sendSaveError(ctx, err)
		return
	}
	SendJSON(ctx, policy)
}

func (h *CircuitBreakerHandler) deletePolicy(ctx *fasthttp.RequestCtx) {
	name := ctx.UserValue("name").(string)
	config, err := h.config(ctx)
	if err != nil {
		h.sendConfigError(ctx, err)
		return
	}
	index := slices.IndexFunc(config.Policies, func(existing circuitbreaker.Policy) bool { return existing.Name == name })
	if index < 0 {
		SendError(ctx, fasthttp.StatusNotFound, "Circuit breaker policy not found")
		return
	}
	config.Policies = append(config.Policies[:index], config.Policies[index+1:]...)
	if err := h.save(ctx, config); err != nil {
		h.sendSaveError(ctx, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

func (h *CircuitBreakerHandler) getState(ctx *fasthttp.RequestCtx) {
	if h.plugin == nil || h.plugin() == nil {
		SendJSON(ctx, map[string]any{"states": []circuitbreaker.PolicyState{}})
		return
	}
	SendJSON(ctx, map[string]any{"states": h.plugin().States()})
}

func (h *CircuitBreakerHandler) config(ctx context.Context) (*circuitbreaker.Config, error) {
	if h.store == nil {
		return nil, fmt.Errorf("config store not available")
	}
	row, err := h.store.GetPlugin(ctx, circuitbreaker.PluginName)
	if errors.Is(err, configstore.ErrNotFound) {
		return &circuitbreaker.Config{Policies: []circuitbreaker.Policy{}}, nil
	}
	if err != nil {
		return nil, err
	}
	config := &circuitbreaker.Config{}
	if row.Config == nil {
		return config, nil
	}
	bytes, err := json.Marshal(row.Config)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(bytes, config); err != nil {
		return nil, fmt.Errorf("stored circuit breaker configuration is invalid: %w", err)
	}
	return config, nil
}

func (h *CircuitBreakerHandler) save(ctx context.Context, config *circuitbreaker.Config) error {
	if _, err := circuitbreaker.Init(config); err != nil {
		return err
	}
	if h.store == nil || h.reloader == nil {
		return fmt.Errorf("circuit breaker configuration is unavailable")
	}
	row, err := h.store.GetPlugin(ctx, circuitbreaker.PluginName)
	if errors.Is(err, configstore.ErrNotFound) {
		if err := h.store.CreatePlugin(ctx, &configstoreTables.TablePlugin{Name: circuitbreaker.PluginName, Enabled: true, Config: config}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		row.Enabled, row.Config = true, config
		if err := h.store.UpdatePlugin(ctx, row); err != nil {
			return err
		}
	}
	return h.reloader.ReloadPlugin(ctx, circuitbreaker.PluginName, nil, config, nil, nil)
}

func (h *CircuitBreakerHandler) sendConfigError(ctx *fasthttp.RequestCtx, err error) {
	if h.store == nil {
		SendError(ctx, fasthttp.StatusServiceUnavailable, "Circuit breaker configuration requires a config store")
		return
	}
	logger.Error("failed to load circuit breaker configuration: %v", err)
	SendError(ctx, fasthttp.StatusInternalServerError, "Failed to load circuit breaker configuration")
}

func (h *CircuitBreakerHandler) sendSaveError(ctx *fasthttp.RequestCtx, err error) {
	logger.Warn("failed to save circuit breaker configuration: %v", err)
	SendError(ctx, fasthttp.StatusBadRequest, err.Error())
}

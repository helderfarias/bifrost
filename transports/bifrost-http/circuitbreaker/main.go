// Package circuitbreaker redirects requests away from an unhealthy provider/model
// pair when an upstream response matches an operator-defined signal.
package circuitbreaker

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

const PluginName = "circuit_breaker"

// Config is persisted as the circuit_breaker built-in plugin configuration.
type Config struct {
	Policies []Policy `json:"policies"`
}

type Policy struct {
	Name             string                `json:"name"`
	Enabled          bool                  `json:"enabled"`
	PrimaryProvider  schemas.ModelProvider `json:"primary_provider"`
	PrimaryModel     string                `json:"primary_model"`
	PrimaryKeyIDs    []string              `json:"primary_key_ids,omitempty"`
	FallbackProvider schemas.ModelProvider `json:"fallback_provider"`
	FallbackModel    string                `json:"fallback_model"`
	Condition        Condition             `json:"condition"`
	DefaultCooldown  string                `json:"default_cooldown,omitempty"`
	CooldownHeader   string                `json:"cooldown_header,omitempty"`
}

type Condition struct {
	Operator string   `json:"operator"`
	Signals  []Signal `json:"signals"`
}

type Signal struct {
	Source         string `json:"source"`
	HeaderName     string `json:"header_name"`
	HeaderValue    string `json:"header_value,omitempty"`
	HeaderContains string `json:"header_contains,omitempty"`
}

type circuitState struct {
	OpenedAt  time.Time
	OpenUntil time.Time
}

type PolicyState struct {
	Name      string    `json:"name"`
	Open      bool      `json:"open"`
	OpenedAt  time.Time `json:"opened_at,omitempty"`
	OpenUntil time.Time `json:"open_until,omitempty"`
}

// Plugin keeps only active circuits in memory. A restart deliberately resets
// them: the next upstream result determines availability again.
type Plugin struct {
	policies []Policy
	states   sync.Map // policy name -> circuitState
}

func Init(config *Config) (*Plugin, error) {
	if config == nil {
		config = &Config{}
	}
	policies := append([]Policy(nil), config.Policies...)
	seen := make(map[string]struct{}, len(policies))
	for i := range policies {
		if err := validatePolicy(&policies[i]); err != nil {
			return nil, err
		}
		if _, exists := seen[policies[i].Name]; exists {
			return nil, fmt.Errorf("duplicate circuit breaker policy %q", policies[i].Name)
		}
		seen[policies[i].Name] = struct{}{}
	}
	return &Plugin{policies: policies}, nil
}

func validatePolicy(policy *Policy) error {
	policy.Name = strings.TrimSpace(policy.Name)
	policy.PrimaryModel = strings.TrimSpace(policy.PrimaryModel)
	policy.FallbackModel = strings.TrimSpace(policy.FallbackModel)
	policy.DefaultCooldown = strings.TrimSpace(policy.DefaultCooldown)
	policy.CooldownHeader = strings.TrimSpace(policy.CooldownHeader)
	if policy.Name == "" || policy.PrimaryProvider == "" || policy.PrimaryModel == "" || policy.FallbackProvider == "" || policy.FallbackModel == "" {
		return fmt.Errorf("policy requires name, primary_provider, primary_model, fallback_provider and fallback_model")
	}
	if policy.PrimaryProvider == policy.FallbackProvider && policy.PrimaryModel == policy.FallbackModel {
		return fmt.Errorf("policy %q fallback must differ from its primary route", policy.Name)
	}
	if policy.DefaultCooldown == "" {
		policy.DefaultCooldown = "30s"
	}
	if _, err := time.ParseDuration(policy.DefaultCooldown); err != nil {
		return fmt.Errorf("policy %q has invalid default_cooldown: %w", policy.Name, err)
	}
	policy.Condition.Operator = strings.ToUpper(strings.TrimSpace(policy.Condition.Operator))
	if policy.Condition.Operator == "" {
		policy.Condition.Operator = "OR"
	}
	if policy.Condition.Operator != "OR" && policy.Condition.Operator != "AND" {
		return fmt.Errorf("policy %q condition.operator must be OR or AND", policy.Name)
	}
	if len(policy.Condition.Signals) == 0 {
		return fmt.Errorf("policy %q requires at least one condition signal", policy.Name)
	}
	for i := range policy.Condition.Signals {
		s := &policy.Condition.Signals[i]
		s.Source = strings.TrimSpace(s.Source)
		s.HeaderName = strings.TrimSpace(s.HeaderName)
		if s.Source != "response_header" || s.HeaderName == "" {
			return fmt.Errorf("policy %q signals must use response_header with header_name", policy.Name)
		}
		if s.HeaderValue != "" && s.HeaderContains != "" {
			return fmt.Errorf("policy %q signal %q cannot set both header_value and header_contains", policy.Name, s.HeaderName)
		}
	}
	return nil
}

func (p *Plugin) GetName() string { return PluginName }
func (p *Plugin) Cleanup() error  { return nil }

// PreRequestHook is intentionally a no-op. The pre-LLM hook runs for every
// attempt and is where a currently open primary is rewritten to its fallback.
func (p *Plugin) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}

func (p *Plugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	if req == nil {
		return req, nil, nil
	}
	provider, model, _ := req.GetRequestFields()
	policy := p.policyFor(provider, model)
	if policy == nil || !policy.Enabled || !p.isPolicyOpen(*policy, time.Now()) {
		return req, nil, nil
	}
	req.SetProvider(policy.FallbackProvider)
	req.SetModel(policy.FallbackModel)
	ctx.AppendRoutingEngineLog(schemas.RoutingEngineCircuitBreaker, schemas.LogLevelWarn, fmt.Sprintf("Circuit breaker %s is open; routing %s/%s to %s/%s", policy.Name, provider, model, policy.FallbackProvider, policy.FallbackModel))
	return req, nil, nil
}

func (p *Plugin) PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, err *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	// The request identity lives in the response/error metadata after dispatch,
	// and avoids evaluating a fallback response against a primary policy.
	provider, model := responseRoute(resp, err)
	policy := p.policyFor(provider, model)
	if policy == nil || !policy.Enabled {
		return resp, err, nil
	}
	headers, _ := ctx.Value(schemas.BifrostContextKeyProviderResponseHeaders).(map[string]string)
	if !matches(policy.Condition, headers) {
		return resp, err, nil
	}
	cooldown := cooldownFor(policy, headers)
	now := time.Now()
	key := ""
	if len(policy.PrimaryKeyIDs) > 0 {
		selectedKeyID, _ := ctx.Value(schemas.BifrostContextKeySelectedKeyID).(string)
		if !slices.Contains(policy.PrimaryKeyIDs, selectedKeyID) {
			return resp, err, nil
		}
		key = selectedKeyID
	}
	p.states.Store(stateKey(policy.Name, key), circuitState{OpenedAt: now, OpenUntil: now.Add(cooldown)})
	ctx.AppendRoutingEngineLog(schemas.RoutingEngineCircuitBreaker, schemas.LogLevelWarn, fmt.Sprintf("Circuit breaker %s opened for %s", policy.Name, cooldown))
	return resp, err, nil
}

func responseRoute(resp *schemas.BifrostResponse, err *schemas.BifrostError) (schemas.ModelProvider, string) {
	if resp != nil {
		if fields := resp.GetExtraFields(); fields != nil {
			return fields.Provider, fields.ResolvedModelUsed
		}
	}
	if err != nil {
		return err.ExtraFields.Provider, err.ExtraFields.ResolvedModelUsed
	}
	return "", ""
}

func (p *Plugin) policyFor(provider schemas.ModelProvider, model string) *Policy {
	for i := range p.policies {
		policy := &p.policies[i]
		if policy.PrimaryProvider == provider && policy.PrimaryModel == model {
			return policy
		}
	}
	return nil
}

func (p *Plugin) isOpen(name string, now time.Time) bool {
	value, ok := p.states.Load(name)
	if !ok {
		return false
	}
	state := value.(circuitState)
	if now.Before(state.OpenUntil) {
		return true
	}
	p.states.CompareAndDelete(name, value)
	return false
}

func (p *Plugin) isPolicyOpen(policy Policy, now time.Time) bool {
	if len(policy.PrimaryKeyIDs) == 0 {
		return p.isOpen(policy.Name, now)
	}
	for _, keyID := range policy.PrimaryKeyIDs {
		if !p.isOpen(stateKey(policy.Name, keyID), now) {
			return false
		}
	}
	return true
}

func stateKey(policy, keyID string) string {
	if keyID == "" {
		return policy
	}
	return policy + "\x00" + keyID
}

func (p *Plugin) States() []PolicyState {
	now := time.Now()
	states := make([]PolicyState, 0, len(p.policies))
	for _, policy := range p.policies {
		state := PolicyState{Name: policy.Name}
		if p.isPolicyOpen(policy, now) {
			state.Open = true
			// A per-key policy is logically open only when every configured key is
			// open, so expose the earliest recovery as the next probe opportunity.
			keys := policy.PrimaryKeyIDs
			if len(keys) == 0 {
				keys = []string{""}
			}
			for _, key := range keys {
				if value, ok := p.states.Load(stateKey(policy.Name, key)); ok {
					circuit := value.(circuitState)
					if state.OpenedAt.IsZero() || circuit.OpenedAt.Before(state.OpenedAt) {
						state.OpenedAt = circuit.OpenedAt
					}
					if state.OpenUntil.IsZero() || circuit.OpenUntil.Before(state.OpenUntil) {
						state.OpenUntil = circuit.OpenUntil
					}
				}
			}
		}
		states = append(states, state)
	}
	return states
}

func matches(condition Condition, headers map[string]string) bool {
	if headers == nil {
		return false
	}
	matched := 0
	for _, signal := range condition.Signals {
		value, exists := header(headers, signal.HeaderName)
		ok := exists
		if signal.HeaderValue != "" {
			ok = exists && strings.EqualFold(value, signal.HeaderValue)
		}
		if signal.HeaderContains != "" {
			ok = exists && strings.Contains(strings.ToLower(value), strings.ToLower(signal.HeaderContains))
		}
		if ok {
			matched++
		}
	}
	if condition.Operator == "AND" {
		return matched == len(condition.Signals)
	}
	return matched > 0
}

func header(headers map[string]string, name string) (string, bool) {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value, true
		}
	}
	return "", false
}

func cooldownFor(policy *Policy, headers map[string]string) time.Duration {
	fallback, _ := time.ParseDuration(policy.DefaultCooldown)
	if policy.CooldownHeader == "" {
		return fallback
	}
	value, ok := header(headers, policy.CooldownHeader)
	if !ok {
		return fallback
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

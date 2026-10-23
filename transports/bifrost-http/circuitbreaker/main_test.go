package circuitbreaker

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestCircuitOpensFromHeaderAndRoutesToFallback(t *testing.T) {
	p, err := Init(&Config{Policies: []Policy{{Name: "anthropic-overloaded", Enabled: true, PrimaryProvider: schemas.Anthropic, PrimaryModel: "claude", FallbackProvider: schemas.OpenAI, FallbackModel: "gpt", DefaultCooldown: "1m", Condition: Condition{Signals: []Signal{{Source: "response_header", HeaderName: "x-overloaded"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := schemas.NewBifrostContext(context.Background(), time.Now())
	ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, map[string]string{"X-Overloaded": "true"})
	errResp := &schemas.BifrostError{ExtraFields: schemas.BifrostErrorExtraFields{Provider: schemas.Anthropic, ResolvedModelUsed: "claude"}}
	_, _, err = p.PostLLMHook(ctx, nil, errResp)
	if err != nil {
		t.Fatal(err)
	}
	req := &schemas.BifrostRequest{ChatRequest: &schemas.BifrostChatRequest{Provider: schemas.Anthropic, Model: "claude"}}
	got, _, err := p.PreLLMHook(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	provider, model, _ := got.GetRequestFields()
	if provider != schemas.OpenAI || model != "gpt" {
		t.Fatalf("got %s/%s", provider, model)
	}
}

func TestSignalMatching(t *testing.T) {
	if !matches(Condition{Operator: "AND", Signals: []Signal{{HeaderName: "retry-after", HeaderValue: "60"}, {HeaderName: "message", HeaderContains: "OVERLOAD"}}}, map[string]string{"Retry-After": "60", "Message": "server overloaded"}) {
		t.Fatal("expected headers to match")
	}
}

func TestPerKeyPolicyOpensOnlyAfterEveryKeyIsOpen(t *testing.T) {
	plugin, err := Init(&Config{Policies: []Policy{{Name: "keyed", Enabled: true, PrimaryProvider: schemas.OpenAI, PrimaryModel: "gpt", PrimaryKeyIDs: []string{"a", "b"}, FallbackProvider: schemas.Anthropic, FallbackModel: "claude", Condition: Condition{Signals: []Signal{{Source: "response_header", HeaderName: "x-fail"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	openKey := func(id string) {
		ctx := schemas.NewBifrostContext(context.Background(), time.Now())
		ctx.SetValue(schemas.BifrostContextKeySelectedKeyID, id)
		ctx.SetValue(schemas.BifrostContextKeyProviderResponseHeaders, map[string]string{"x-fail": "1"})
		_, _, _ = plugin.PostLLMHook(ctx, nil, &schemas.BifrostError{ExtraFields: schemas.BifrostErrorExtraFields{Provider: schemas.OpenAI, ResolvedModelUsed: "gpt"}})
	}
	openKey("a")
	if plugin.States()[0].Open {
		t.Fatal("one unavailable key must not open the policy")
	}
	openKey("b")
	if !plugin.States()[0].Open {
		t.Fatal("all unavailable keys must open the policy")
	}
}

func BenchmarkPreLLMHookWithoutPolicies(b *testing.B) {
	plugin, err := Init(nil)
	if err != nil {
		b.Fatal(err)
	}
	ctx := schemas.NewBifrostContext(context.Background(), time.Now())
	req := &schemas.BifrostRequest{ChatRequest: &schemas.BifrostChatRequest{Provider: schemas.OpenAI, Model: "gpt-4.1"}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = plugin.PreLLMHook(ctx, req)
	}
}

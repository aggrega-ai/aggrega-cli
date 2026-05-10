// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func emptyPipe() types.InjectPipe {
	return types.InjectPipe{
		Env:  types.OrderedMap[string]{},
		File: types.OrderedMap[string]{},
		Arg:  types.OrderedMap[string]{},
		All:  types.OrderedMap[string]{},
	}
}

func TestExecutePass2a_LiteralValue(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env: map[string]any{"PORT": "8080"},
	}

	_, err := ExecutePass2a(&inject, selfInject, "api", types.OrderedMap[string]{}, SelfInjectContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	port, _ := inject.Env.Get("PORT")
	if port != "8080" {
		t.Errorf("PORT = %q, want '8080'", port)
	}
	portAll, _ := inject.All.Get("PORT")
	if portAll != "8080" {
		t.Errorf("all.PORT = %q", portAll)
	}
}

func TestExecutePass2a_FromOwnProvides(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env: map[string]any{
			"MY_HOST": map[string]any{"from": "host"},
		},
	}
	ownProvides := types.OrderedMap[string]{}
	ownProvides.Set("host", "api.svc.cluster.local")

	_, err := ExecutePass2a(&inject, selfInject, "api", ownProvides, SelfInjectContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	host, _ := inject.Env.Get("MY_HOST")
	if host != "api.svc.cluster.local" {
		t.Errorf("MY_HOST = %q", host)
	}
}

func TestExecutePass2a_ValueTemplate(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env: map[string]any{
			"APP_ID": map[string]any{"value": "{{ .context.instance_id }}-app"},
		},
	}
	siCtx := SelfInjectContext{
		Context: SelfInjectContextBlock{InstanceID: "my-api"},
	}

	_, err := ExecutePass2a(&inject, selfInject, "my-api", types.OrderedMap[string]{}, siCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	appID, _ := inject.Env.Get("APP_ID")
	if appID != "my-api-app" {
		t.Errorf("APP_ID = %q, want 'my-api-app'", appID)
	}
}

func TestExecutePass2a_ForbiddenReference(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env: map[string]any{
			"BAD": map[string]any{"value": "{{ .context.inject.env.SECRET }}"},
		},
	}

	_, err := ExecutePass2a(&inject, selfInject, "api", types.OrderedMap[string]{}, SelfInjectContext{})
	if err == nil {
		t.Fatal("expected error for forbidden reference")
	}
	if !errors.Is(err, ErrSelfInjectForbiddenReference) {
		t.Errorf("expected ErrSelfInjectForbiddenReference, got: %v", err)
	}
}

func TestExecutePass2a_NilSelfInject(t *testing.T) {
	t.Parallel()

	inject := types.InjectPipe{
		Env:  func() types.OrderedMap[string] { m := types.OrderedMap[string]{}; m.Set("EXISTING", "val"); return m }(),
		File: types.OrderedMap[string]{},
		Arg:  types.OrderedMap[string]{},
		All:  types.OrderedMap[string]{},
	}

	keys, err := ExecutePass2a(&inject, nil, "api", types.OrderedMap[string]{}, SelfInjectContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if keys != nil {
		t.Error("expected nil selfKeys for nil selfInject")
	}
	existing, _ := inject.Env.Get("EXISTING")
	if existing != "val" {
		t.Error("existing inject should be preserved")
	}
}

func TestExecutePass2a_FromMissingProvides(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env: map[string]any{
			"X": map[string]any{"from": "nonexistent"},
		},
	}

	_, err := ExecutePass2a(&inject, selfInject, "api", types.OrderedMap[string]{}, SelfInjectContext{})
	if err == nil {
		t.Fatal("expected error for missing provides key")
	}
}

// TestNormalizeSelfInjectEntries_OrderedMapShape_DecodesEntries pins the
// typed-arm fix at normalizeSelfInjectEntries. When the InjectBlock field
// arrives as *types.OrderedMap[any] (the round-trip-fidelity decode shape
// produced when the inject block flows through an OrderedMap[any]-routed
// authoring path), the function decodes via .All() rather than silently
// returning an empty map. Both the literal-string shorthand and the
// verbose {value, from} shape are exercised.
func TestNormalizeSelfInjectEntries_OrderedMapShape_DecodesEntries(t *testing.T) {
	t.Parallel()
	raw := types.NewOrderedMap[any]()
	raw.Set("zulu", "literal-1")
	inner := types.NewOrderedMap[any]()
	inner.Set("from", "alpha-key")
	raw.Set("alpha", inner)
	raw.Set("middle", "literal-2")

	entries, err := normalizeSelfInjectEntries(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if e, ok := entries["zulu"]; !ok || !e.isLiteral || e.value != "literal-1" {
		t.Errorf("zulu: %+v want isLiteral=true value=literal-1", e)
	}
	if e, ok := entries["alpha"]; !ok || e.from != "alpha-key" {
		t.Errorf("alpha: %+v want from=alpha-key", e)
	}
	if e, ok := entries["middle"]; !ok || !e.isLiteral || e.value != "literal-2" {
		t.Errorf("middle: %+v want isLiteral=true value=literal-2", e)
	}
}

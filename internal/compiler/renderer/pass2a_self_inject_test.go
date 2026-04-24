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
		Env: map[string]interface{}{"PORT": "8080"},
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
		Env: map[string]interface{}{
			"MY_HOST": map[string]interface{}{"from": "host"},
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
		Env: map[string]interface{}{
			"APP_ID": map[string]interface{}{"value": "{{ .context.instance_id }}-app"},
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
		Env: map[string]interface{}{
			"BAD": map[string]interface{}{"value": "{{ .context.inject.env.SECRET }}"},
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
		Env: map[string]interface{}{
			"X": map[string]interface{}{"from": "nonexistent"},
		},
	}

	_, err := ExecutePass2a(&inject, selfInject, "api", types.OrderedMap[string]{}, SelfInjectContext{})
	if err == nil {
		t.Fatal("expected error for missing provides key")
	}
}

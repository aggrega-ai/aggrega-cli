// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestExecutePass2a_LiteralValue(t *testing.T) {
	t.Parallel()

	inject := types.InjectPipe{
		Env: map[string]string{}, File: map[string]string{}, Arg: map[string]string{}, All: map[string]string{},
	}
	selfInject := &types.InjectBlock{
		Env: map[string]interface{}{"PORT": "8080"},
	}

	_, err := ExecutePass2a(&inject, selfInject, "api", nil, SelfInjectContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inject.Env["PORT"] != "8080" {
		t.Errorf("PORT = %q, want '8080'", inject.Env["PORT"])
	}
	if inject.All["PORT"] != "8080" {
		t.Errorf("all.PORT = %q", inject.All["PORT"])
	}
}

func TestExecutePass2a_FromOwnProvides(t *testing.T) {
	t.Parallel()

	inject := types.InjectPipe{
		Env: map[string]string{}, File: map[string]string{}, Arg: map[string]string{}, All: map[string]string{},
	}
	selfInject := &types.InjectBlock{
		Env: map[string]interface{}{
			"MY_HOST": map[string]interface{}{"from": "host"},
		},
	}
	ownProvides := map[string]string{"host": "api.svc.cluster.local"}

	_, err := ExecutePass2a(&inject, selfInject, "api", ownProvides, SelfInjectContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inject.Env["MY_HOST"] != "api.svc.cluster.local" {
		t.Errorf("MY_HOST = %q", inject.Env["MY_HOST"])
	}
}

func TestExecutePass2a_ValueTemplate(t *testing.T) {
	t.Parallel()

	inject := types.InjectPipe{
		Env: map[string]string{}, File: map[string]string{}, Arg: map[string]string{}, All: map[string]string{},
	}
	selfInject := &types.InjectBlock{
		Env: map[string]interface{}{
			"APP_ID": map[string]interface{}{"value": "{{ .context.instance_id }}-app"},
		},
	}
	siCtx := SelfInjectContext{
		Context: SelfInjectContextBlock{InstanceID: "my-api"},
	}

	_, err := ExecutePass2a(&inject, selfInject, "my-api", nil, siCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inject.Env["APP_ID"] != "my-api-app" {
		t.Errorf("APP_ID = %q, want 'my-api-app'", inject.Env["APP_ID"])
	}
}

func TestExecutePass2a_ForbiddenReference(t *testing.T) {
	t.Parallel()

	inject := types.InjectPipe{
		Env: map[string]string{}, File: map[string]string{}, Arg: map[string]string{}, All: map[string]string{},
	}
	selfInject := &types.InjectBlock{
		Env: map[string]interface{}{
			"BAD": map[string]interface{}{"value": "{{ .context.inject.env.SECRET }}"},
		},
	}

	_, err := ExecutePass2a(&inject, selfInject, "api", nil, SelfInjectContext{})
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
		Env: map[string]string{"EXISTING": "val"}, File: map[string]string{}, Arg: map[string]string{}, All: map[string]string{},
	}

	keys, err := ExecutePass2a(&inject, nil, "api", nil, SelfInjectContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if keys != nil {
		t.Error("expected nil selfKeys for nil selfInject")
	}
	if inject.Env["EXISTING"] != "val" {
		t.Error("existing inject should be preserved")
	}
}

func TestExecutePass2a_FromMissingProvides(t *testing.T) {
	t.Parallel()

	inject := types.InjectPipe{
		Env: map[string]string{}, File: map[string]string{}, Arg: map[string]string{}, All: map[string]string{},
	}
	selfInject := &types.InjectBlock{
		Env: map[string]interface{}{
			"X": map[string]interface{}{"from": "nonexistent"},
		},
	}

	_, err := ExecutePass2a(&inject, selfInject, "api", map[string]string{}, SelfInjectContext{})
	if err == nil {
		t.Fatal("expected error for missing provides key")
	}
}

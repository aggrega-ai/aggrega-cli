// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestValidateValuesAgainstBaseSchema_Success(t *testing.T) {
	t.Parallel()

	lookup := func(name string) (types.Provider, bool) {
		return types.Provider{
			Spec: types.ProviderSpec{
				BaseSchema: `image: string, replicas?: int & >=1`,
			},
		}, true
	}

	err := renderer.ValidateValuesAgainstBaseSchema(
		"test-provider",
		lookup,
		map[string]any{"image": "app:latest", "replicas": 3},
		"test-node",
	)
	if err != nil {
		t.Errorf("expected success, got: %v", err)
	}
}

// TestValidateValuesAgainstBaseSchema_StubSkipsCueAuthoring verifies the
// transitional stub-skip behavior: when a provider authors a non-empty
// BaseSchema (currently CUE source), the validator does NOT halt the
// build. The function logs a deprecation note and returns nil. The
// schema-authoring phase restores per-value validation once providers
// migrate BaseSchema to JSON Schema 2020-12.
func TestValidateValuesAgainstBaseSchema_StubSkipsCueAuthoring(t *testing.T) {
	t.Parallel()

	lookup := func(name string) (types.Provider, bool) {
		return types.Provider{
			Spec: types.ProviderSpec{
				BaseSchema: `image: string, replicas: int & >=1`,
			},
		}, true
	}

	err := renderer.ValidateValuesAgainstBaseSchema(
		"transition-provider",
		lookup,
		map[string]any{"image": "app:latest"}, // would have failed under legacy CUE validation
		"test-node",
	)
	if err != nil {
		t.Errorf("expected nil (stub-skips Spec.BaseSchema); got %v", err)
	}
}

func TestValidateValuesAgainstBaseSchema_NoSchemaNoValidation(t *testing.T) {
	t.Parallel()

	lookup := func(name string) (types.Provider, bool) {
		return types.Provider{Spec: types.ProviderSpec{}}, true // empty baseSchema
	}

	err := renderer.ValidateValuesAgainstBaseSchema(
		"test-provider",
		lookup,
		map[string]any{"anything": "goes"},
		"test-node",
	)
	if err != nil {
		t.Errorf("expected no validation when baseSchema empty, got: %v", err)
	}
}

func TestValidateValuesAgainstBaseSchema_ProviderNotFound(t *testing.T) {
	t.Parallel()

	lookup := func(name string) (types.Provider, bool) {
		return types.Provider{}, false
	}

	err := renderer.ValidateValuesAgainstBaseSchema(
		"missing-provider",
		lookup,
		map[string]any{},
		"test-node",
	)
	if err != nil {
		t.Errorf("expected no validation when provider not found, got: %v", err)
	}
}

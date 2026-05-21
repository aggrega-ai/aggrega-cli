// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestValidateValuesAgainstBaseSchema_Success covers the happy path —
// a JSON Schema 2020-12 baseSchema body parsed via compactform, a
// conforming values dict; no findings.
func TestValidateValuesAgainstBaseSchema_Success(t *testing.T) {
	t.Parallel()

	lookup := func(name string) (types.Provider, bool) {
		return types.Provider{
			Spec: types.ProviderSpec{
				BaseSchema: "" +
					"type: object\n" +
					"properties:\n" +
					"  image: { type: string }\n" +
					"  replicas: { type: integer, minimum: 1, default: 1 }\n" +
					"required: [image]\n" +
					"additionalProperties: true\n",
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

// TestValidateValuesAgainstBaseSchema_RejectsMalformedYAML covers the
// real-validator-rejects-legacy-CUE behaviour. A baseSchema body in CUE
// syntax (no longer accepted after the language migration) surfaces as
// a CATALOG_ERROR rather than silently passing.
func TestValidateValuesAgainstBaseSchema_RejectsMalformedYAML(t *testing.T) {
	t.Parallel()

	lookup := func(name string) (types.Provider, bool) {
		return types.Provider{
			Spec: types.ProviderSpec{
				BaseSchema: `image: string, replicas: int & >=1`,
			},
		}, true
	}

	err := renderer.ValidateValuesAgainstBaseSchema(
		"legacy-cue-provider",
		lookup,
		map[string]any{"image": "app:latest"},
		"test-node",
	)
	if err == nil {
		t.Fatal("expected rejection for CUE-syntax baseSchema; got nil")
	}
	if !strings.Contains(err.Error(), "legacy-cue-provider") {
		t.Errorf("error should name the offending provider; got: %v", err)
	}
}

// TestValidateValuesAgainstBaseSchema_RejectsValueViolations covers a
// values dict that fails the schema (missing required field). The
// validator's primary use case — confirms findings surface as a
// CATALOG_ERROR with the consumer label.
func TestValidateValuesAgainstBaseSchema_RejectsValueViolations(t *testing.T) {
	t.Parallel()

	lookup := func(name string) (types.Provider, bool) {
		return types.Provider{
			Spec: types.ProviderSpec{
				BaseSchema: "" +
					"type: object\n" +
					"properties:\n" +
					"  image: { type: string }\n" +
					"required: [image]\n" +
					"additionalProperties: true\n",
			},
		}, true
	}

	err := renderer.ValidateValuesAgainstBaseSchema(
		"strict-provider",
		lookup,
		map[string]any{"replicas": 3}, // missing required "image"
		"test-node",
	)
	if err == nil {
		t.Fatal("expected rejection for missing required field; got nil")
	}
	if !strings.Contains(err.Error(), "strict-provider") {
		t.Errorf("error should name the offending provider; got: %v", err)
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

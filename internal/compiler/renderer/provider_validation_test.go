// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"strings"
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
		map[string]interface{}{"image": "app:latest", "replicas": 3},
		"test-node",
	)
	if err != nil {
		t.Errorf("expected success, got: %v", err)
	}
}

func TestValidateValuesAgainstBaseSchema_Fail(t *testing.T) {
	t.Parallel()

	lookup := func(name string) (types.Provider, bool) {
		return types.Provider{
			Spec: types.ProviderSpec{
				BaseSchema: `image: string, replicas: int & >=1`, // replicas required
			},
		}, true
	}

	err := renderer.ValidateValuesAgainstBaseSchema(
		"test-provider",
		lookup,
		map[string]interface{}{"image": "app:latest"}, // missing replicas
		"test-node",
	)
	if err == nil {
		t.Fatal("expected validation failure for missing required field")
	}
	if !strings.Contains(err.Error(), "baseSchema validation failed") {
		t.Errorf("unexpected error message: %v", err)
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
		map[string]interface{}{"anything": "goes"},
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
		map[string]interface{}{},
		"test-node",
	)
	if err != nil {
		t.Errorf("expected no validation when provider not found, got: %v", err)
	}
}

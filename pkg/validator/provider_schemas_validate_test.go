// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// TestValidateProviderSchemas_NilSpecReturnsNil pins the early-return
// guard for a nil ProviderSpec — nothing to validate, no errors.
func TestValidateProviderSchemas_NilSpecReturnsNil(t *testing.T) {
	t.Parallel()
	if errs := validator.ValidateProviderSchemas("helm", "test.yaml", nil); errs != nil {
		t.Errorf("nil spec should return nil; got %v", errs)
	}
}

// TestValidateProviderSchemas_EmptyBodyPasses pins the whitespace-only
// branch: a Provider declaring an empty (or all-whitespace) workloadSchema
// passes silently. The workloadSchema is optional.
func TestValidateProviderSchemas_EmptyBodyPasses(t *testing.T) {
	t.Parallel()
	spec := &types.ProviderSpec{WorkloadSchema: "   "}
	if errs := validator.ValidateProviderSchemas("helm", "test.yaml", spec); len(errs) != 0 {
		t.Errorf("empty body should pass; got %v", errs)
	}
}

// TestValidateProviderSchemas_CleanSchemaPasses pins the happy path:
// a Provider with a syntactically valid JSON Schema body in
// workloadSchema compiles cleanly and returns no errors.
func TestValidateProviderSchemas_CleanSchemaPasses(t *testing.T) {
	t.Parallel()
	spec := &types.ProviderSpec{
		WorkloadSchema: `
release_name:
  type: string
namespace:
  type: string
`,
	}
	if errs := validator.ValidateProviderSchemas("helm", "test.yaml", spec); len(errs) != 0 {
		t.Errorf("valid schema should pass; got %v", errs)
	}
}

// TestValidateProviderSchemas_MalformedYAMLProducesError pins the
// YAML-parse error path: a body that is not valid YAML wraps into a
// CatalogError naming the provider, the field, and the source file.
func TestValidateProviderSchemas_MalformedYAMLProducesError(t *testing.T) {
	t.Parallel()
	spec := &types.ProviderSpec{
		WorkloadSchema: `: : : not yaml`,
	}
	errs := validator.ValidateProviderSchemas("helm", "test.yaml", spec)
	if len(errs) == 0 {
		t.Fatal("malformed YAML should produce error")
	}
	msg := errs[0].Error()
	for _, want := range []string{`Provider "helm"`, "workloadSchema", "test.yaml"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q: %s", want, msg)
		}
	}
}

// TestValidateProviderSchemas_CompileErrorWrapsCatalog pins the
// compactform-compile error path. A body that parses as YAML but
// fails to compile to JSON Schema 2020-12 (e.g., constraint applied to
// the wrong declared type) surfaces as a CatalogError naming the
// field.
func TestValidateProviderSchemas_CompileErrorWrapsCatalog(t *testing.T) {
	t.Parallel()
	spec := &types.ProviderSpec{
		WorkloadSchema: `port: { type: object, min: 5 }`,
	}
	errs := validator.ValidateProviderSchemas("helm", "test.yaml", spec)
	if len(errs) == 0 {
		t.Fatal("compile error should produce error")
	}
	msg := errs[0].Error()
	for _, want := range []string{`Provider "helm"`, "workloadSchema"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q: %s", want, msg)
		}
	}
}

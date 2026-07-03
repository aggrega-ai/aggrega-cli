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

// TestValidatePluginProviderSchemas_NilCasesPass pins the early-return
// guards: a nil PluginSpec, and a Plugin with no spec.provider block,
// both validate without error — the provider block is optional.
func TestValidatePluginProviderSchemas_NilCasesPass(t *testing.T) {
	t.Parallel()
	if errs := validator.ValidatePluginProviderSchemas("helm-renderer:1.0.0", "test.yaml", nil); errs != nil {
		t.Errorf("nil spec should return nil; got %v", errs)
	}
	spec := &types.PluginSpec{Plugin: "oci://x@sha256:abc", PluginType: "provider", AbiVersion: "v1alpha1"}
	if errs := validator.ValidatePluginProviderSchemas("helm-renderer:1.0.0", "test.yaml", spec); len(errs) != 0 {
		t.Errorf("absent spec.provider should pass; got %v", errs)
	}
}

// TestValidatePluginProviderSchemas_EmptyBodyPasses pins the
// whitespace-only branch: a spec.provider block whose workloadSchema is
// empty (a non-synthesizing provider plugin) validates silently.
func TestValidatePluginProviderSchemas_EmptyBodyPasses(t *testing.T) {
	t.Parallel()
	spec := &types.PluginSpec{
		Provider: &types.PluginProvider{WorkloadSchema: "   "},
	}
	if errs := validator.ValidatePluginProviderSchemas("helm-chart-renderer:1.0.0", "test.yaml", spec); len(errs) != 0 {
		t.Errorf("empty body should pass; got %v", errs)
	}
}

// TestValidatePluginProviderSchemas_CleanSchemaPasses pins the happy
// path: a spec.provider.workloadSchema carrying a syntactically valid
// compact-form JSON Schema body compiles cleanly and returns no errors.
func TestValidatePluginProviderSchemas_CleanSchemaPasses(t *testing.T) {
	t.Parallel()
	spec := &types.PluginSpec{
		Provider: &types.PluginProvider{
			WorkloadSchema: `
containers:
  type: object
  valuesType: object
replicas:
  type: integer
  min: 0
`,
		},
	}
	if errs := validator.ValidatePluginProviderSchemas("helm-renderer:1.0.0", "test.yaml", spec); len(errs) != 0 {
		t.Errorf("valid schema should pass; got %v", errs)
	}
}

// TestValidatePluginProviderSchemas_CompileErrorWrapsCatalog pins the
// reject path: a spec.provider.workloadSchema body that parses as YAML
// but fails to compile to JSON Schema 2020-12 (a constraint applied to
// the wrong declared type) surfaces as a CatalogError naming the Plugin
// and the spec.provider.workloadSchema field. This is the load-time
// acceptance gate a malformed provider-Plugin body is rejected by.
func TestValidatePluginProviderSchemas_CompileErrorWrapsCatalog(t *testing.T) {
	t.Parallel()
	spec := &types.PluginSpec{
		Provider: &types.PluginProvider{
			WorkloadSchema: `port: { type: object, min: 5 }`,
		},
	}
	errs := validator.ValidatePluginProviderSchemas("helm-renderer:1.0.0", "plugin.yaml", spec)
	if len(errs) == 0 {
		t.Fatal("compile error should produce error")
	}
	msg := errs[0].Error()
	for _, want := range []string{`Plugin "helm-renderer:1.0.0"`, "provider.workloadSchema", "plugin.yaml"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q: %s", want, msg)
		}
	}
}

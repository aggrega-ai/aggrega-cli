// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

const (
	validateCatalogDir = "testdata/validate/catalog"
	validateRecipePath = "testdata/validate/catalog/recipe/helm/service/1.0.0/recipe.yaml"
	validateCompPath   = "testdata/validate/catalog/component/example/compute/service/1.0.0/component.yaml"
	validateFRPath     = "testdata/validate/featurerecipe.yaml"
	validateBrokenPath = "testdata/validate/broken-recipe.yaml"

	validatePluginPath           = "testdata/validate/plugin.yaml"
	validatePluginBadTypePath    = "testdata/validate/plugin-bad-type.yaml"
	validatePluginMissingABIPath = "testdata/validate/plugin-missing-abi.yaml"
)

// TestValidateRecipeRendersInIsolation drives the full tiered path for a Recipe
// with no required credential or topology input: schema, shape, resolution, and
// a real isolated render through the embedded provider guest. It asserts the
// render emits the synthesized workload's artifacts. The wasm toolchain is
// present in this environment, so the render tier runs rather than skipping.
func TestValidateRecipeRendersInIsolation(t *testing.T) {
	var buf bytes.Buffer
	if err := dispatchValidate(resolveTempWorkspace(t, tempWorkspaceOptions{}), validateRecipePath, []string{validateCatalogDir}, false, &buf); err != nil {
		t.Fatalf("dispatchValidate: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"Kind:   Recipe",
		"Schema: valid",
		"Shape:  valid",
		"Resolution: valid",
		"Render: 2 artifact(s)",
		"kind: Service",
		"kind: Deployment",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, got)
		}
	}
}

// TestValidateRecipeDeterministic proves the report is byte-identical across
// two runs of the same input — the GitOps-diff guarantee the render tier rides
// on, surfaced through validate's own output.
func TestValidateRecipeDeterministic(t *testing.T) {
	var a, b bytes.Buffer
	if err := dispatchValidate(resolveTempWorkspace(t, tempWorkspaceOptions{}), validateRecipePath, []string{validateCatalogDir}, false, &a); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if err := dispatchValidate(resolveTempWorkspace(t, tempWorkspaceOptions{}), validateRecipePath, []string{validateCatalogDir}, false, &b); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Errorf("output differs between runs:\n--- run 1 ---\n%s\n--- run 2 ---\n%s", a.String(), b.String())
	}
}

// TestValidateComponentPillars checks a Component's schema and pillar structure
// (the render tiers do not apply: a Component alone has no workload to render).
func TestValidateComponentPillars(t *testing.T) {
	var buf bytes.Buffer
	if err := dispatchValidate(resolveTempWorkspace(t, tempWorkspaceOptions{}), validateCompPath, []string{validateCatalogDir}, false, &buf); err != nil {
		t.Fatalf("dispatchValidate: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"Kind:   Component", "Schema: valid", "Pillars: valid"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, got)
		}
	}
}

// TestValidateFeatureRecipeShape checks a standalone FeatureRecipe's schema and
// shape. A FeatureRecipe renders only when attached to a host component, so the
// render tiers are reported as skipped rather than attempted.
func TestValidateFeatureRecipeShape(t *testing.T) {
	var buf bytes.Buffer
	if err := dispatchValidate(resolveTempWorkspace(t, tempWorkspaceOptions{}), validateFRPath, []string{validateCatalogDir}, false, &buf); err != nil {
		t.Fatalf("dispatchValidate: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"Kind:   FeatureRecipe", "Schema: valid", "Shape:  valid", "Render: skipped"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, got)
		}
	}
}

// TestValidatePluginSchemaOnly checks that a Plugin is a recognized single-entity
// target and is schema-checked only: it reaches the envelope gate, reports a valid
// schema, and runs no pillar or render tier. It also proves the old refusal for
// an unrecognized single-entity kind no longer applies to a Plugin.
func TestValidatePluginSchemaOnly(t *testing.T) {
	var buf bytes.Buffer
	if err := dispatchValidate(resolveTempWorkspace(t, tempWorkspaceOptions{}), validatePluginPath, []string{validateCatalogDir}, false, &buf); err != nil {
		t.Fatalf("dispatchValidate: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"Kind:   Plugin", "Schema: valid"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, got)
		}
	}
	for _, absent := range []string{"Pillars:", "Shape:", "Resolution:", "Render:"} {
		if strings.Contains(got, absent) {
			t.Errorf("output unexpectedly contains %q (a Plugin has no such tier)\nfull output:\n%s", absent, got)
		}
	}
	if strings.Contains(got, "not a single-entity validation target") {
		t.Errorf("Plugin is still refused as a validation target\nfull output:\n%s", got)
	}
}

// TestValidatePluginBadTypeHaltsLoudly confirms an invalid pluginType enum value
// halts at the envelope gate with the recoverable sentinel.
func TestValidatePluginBadTypeHaltsLoudly(t *testing.T) {
	var buf bytes.Buffer
	err := dispatchValidate(resolveTempWorkspace(t, tempWorkspaceOptions{}), validatePluginBadTypePath, []string{validateCatalogDir}, false, &buf)
	if err == nil {
		t.Fatalf("expected a schema error, got nil; output:\n%s", buf.String())
	}
	if !errors.Is(err, envelope.ErrEntityEnvelope()) {
		t.Errorf("error does not wrap ErrEntityEnvelope: %v", err)
	}
	if !strings.Contains(err.Error(), "envelope schema") {
		t.Errorf("diagnostic does not name the envelope schema: %v", err)
	}
}

// TestValidatePluginMissingRequiredHaltsLoudly confirms a Plugin missing a
// required spec field halts at the envelope gate with the recoverable sentinel.
func TestValidatePluginMissingRequiredHaltsLoudly(t *testing.T) {
	var buf bytes.Buffer
	err := dispatchValidate(resolveTempWorkspace(t, tempWorkspaceOptions{}), validatePluginMissingABIPath, []string{validateCatalogDir}, false, &buf)
	if err == nil {
		t.Fatalf("expected a schema error, got nil; output:\n%s", buf.String())
	}
	if !errors.Is(err, envelope.ErrEntityEnvelope()) {
		t.Errorf("error does not wrap ErrEntityEnvelope: %v", err)
	}
}

// TestValidatePluginBinaryOnlyPass drives the binary-only plugin scope: a raw
// .wasm validated against a matching declared type. It asserts the deterministic
// report and a clean exit. The wasm toolchain is present in this environment, so
// the fixture builds rather than skipping.
func TestValidatePluginBinaryOnlyPass(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")
	var buf bytes.Buffer
	if err := validatePluginBinary(pluginPath, "parser", "", &buf); err != nil {
		t.Fatalf("validatePluginBinary: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"Declared type: parser", "Describe: valid", "Type: valid"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, got)
		}
	}
}

// TestValidatePluginBinaryOnlyTypeMismatchHaltsLoudly confirms a declared type
// that disagrees with the binary's describe() self-report halts with the
// plugin-type-mismatch sentinel.
func TestValidatePluginBinaryOnlyTypeMismatchHaltsLoudly(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")
	var buf bytes.Buffer
	err := validatePluginBinary(pluginPath, "provider", "", &buf)
	if err == nil {
		t.Fatalf("expected a type-mismatch error, got nil; output:\n%s", buf.String())
	}
	if !errors.Is(err, wasm.ErrWasmPluginTypeMismatch()) {
		t.Errorf("error does not wrap ErrWasmPluginTypeMismatch: %v", err)
	}
}

// TestValidatePluginBinaryOnlyRequiresType confirms binary-only mode refuses to
// run without a declared type, since there is no entity to source one from.
func TestValidatePluginBinaryOnlyRequiresType(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")
	var buf bytes.Buffer
	err := validatePluginBinary(pluginPath, "", "", &buf)
	if err == nil {
		t.Fatalf("expected a missing-type error, got nil; output:\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "--type") {
		t.Errorf("diagnostic does not name --type: %v", err)
	}
}

// TestValidatePluginFullModePass drives the full plugin scope: a Plugin entity
// cross-checked against its matching .wasm through the one publish validation
// door. It asserts the schema gate and the cross-check both report valid.
func TestValidatePluginFullModePass(t *testing.T) {
	entityPath, pluginPath, _ := writeAgreeingPlugin(t)
	var buf bytes.Buffer
	if err := validatePluginBinary(pluginPath, "", entityPath, &buf); err != nil {
		t.Fatalf("validatePluginBinary: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"Kind:   Plugin", "Schema: valid", "Plugin: valid"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, got)
		}
	}
}

// TestValidatePluginFullModeTypeDisagreementHaltsLoudly confirms that a --type
// contradicting the entity's metadata.pluginType halts: a Plugin carries one
// declared type, so a second source in disagreement is an authoring error.
func TestValidatePluginFullModeTypeDisagreementHaltsLoudly(t *testing.T) {
	entityPath, pluginPath, _ := writeAgreeingPlugin(t)
	var buf bytes.Buffer
	err := validatePluginBinary(pluginPath, "provider", entityPath, &buf)
	if err == nil {
		t.Fatalf("expected a type-disagreement error, got nil; output:\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "disagrees with the entity's metadata.pluginType") {
		t.Errorf("diagnostic does not name the pluginType disagreement: %v", err)
	}
}

// TestValidatePluginFullModeNonPluginEntityHaltsLoudly confirms --plugin against
// a non-Plugin entity is refused before any cross-check: the flag cross-checks a
// Plugin surface, and a Component (or any other kind) has none.
func TestValidatePluginFullModeNonPluginEntityHaltsLoudly(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")
	var buf bytes.Buffer
	err := validatePluginBinary(pluginPath, "", validateCompPath, &buf)
	if err == nil {
		t.Fatalf("expected a non-Plugin-entity error, got nil; output:\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "cross-checks a Plugin entity") {
		t.Errorf("diagnostic does not explain the Plugin-entity requirement: %v", err)
	}
}

// TestValidateTypeWithoutPluginGuard confirms --type on its own — with no
// --plugin — is refused at the command layer, so it never silently no-ops on a
// catalog-, release-, or entity-scoped run.
func TestValidateTypeWithoutPluginGuard(t *testing.T) {
	cmd := newValidateCmd()
	cmd.SetArgs([]string{"--type", "parser"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected a guard error for --type without --plugin, got nil")
	}
	if !strings.Contains(err.Error(), "--type applies only with --plugin") {
		t.Errorf("diagnostic does not explain the --type/--plugin coupling: %v", err)
	}
}

// TestValidatePluginReleaseMutuallyExclusiveGuard confirms --plugin and
// --release are refused together: they select different validation scopes.
func TestValidatePluginReleaseMutuallyExclusiveGuard(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")
	cmd := newValidateCmd()
	cmd.SetArgs([]string{"--plugin", pluginPath, "--type", "parser", "--release", "some-release.yaml"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected a guard error for --plugin with --release, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive with --release") {
		t.Errorf("diagnostic does not explain the --plugin/--release exclusivity: %v", err)
	}
}

// TestValidateBrokenEntityHaltsLoudly confirms a schema violation halts at the
// envelope gate with the recoverable sentinel, before any catalog or render
// work.
func TestValidateBrokenEntityHaltsLoudly(t *testing.T) {
	var buf bytes.Buffer
	err := dispatchValidate(resolveTempWorkspace(t, tempWorkspaceOptions{}), validateBrokenPath, []string{validateCatalogDir}, false, &buf)
	if err == nil {
		t.Fatalf("expected a schema error, got nil; output:\n%s", buf.String())
	}
	if !errors.Is(err, envelope.ErrEntityEnvelope()) {
		t.Errorf("error does not wrap ErrEntityEnvelope: %v", err)
	}
	if !strings.Contains(err.Error(), "envelope schema") {
		t.Errorf("diagnostic does not name the envelope schema: %v", err)
	}
}

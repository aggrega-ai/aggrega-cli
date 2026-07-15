// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

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
	if err := dispatchValidate(resolveTempWorkspace(t), validateRecipePath, []string{validateCatalogDir}, &buf); err != nil {
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
	if err := dispatchValidate(resolveTempWorkspace(t), validateRecipePath, []string{validateCatalogDir}, &a); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if err := dispatchValidate(resolveTempWorkspace(t), validateRecipePath, []string{validateCatalogDir}, &b); err != nil {
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
	if err := dispatchValidate(resolveTempWorkspace(t), validateCompPath, []string{validateCatalogDir}, &buf); err != nil {
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
	if err := dispatchValidate(resolveTempWorkspace(t), validateFRPath, []string{validateCatalogDir}, &buf); err != nil {
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
	if err := dispatchValidate(resolveTempWorkspace(t), validatePluginPath, []string{validateCatalogDir}, &buf); err != nil {
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
	err := dispatchValidate(resolveTempWorkspace(t), validatePluginBadTypePath, []string{validateCatalogDir}, &buf)
	if err == nil {
		t.Fatalf("expected a schema error, got nil; output:\n%s", buf.String())
	}
	if !errors.Is(err, envelope.ErrEntityEnvelope) {
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
	err := dispatchValidate(resolveTempWorkspace(t), validatePluginMissingABIPath, []string{validateCatalogDir}, &buf)
	if err == nil {
		t.Fatalf("expected a schema error, got nil; output:\n%s", buf.String())
	}
	if !errors.Is(err, envelope.ErrEntityEnvelope) {
		t.Errorf("error does not wrap ErrEntityEnvelope: %v", err)
	}
}

// TestValidateBrokenEntityHaltsLoudly confirms a schema violation halts at the
// envelope gate with the recoverable sentinel, before any catalog or render
// work.
func TestValidateBrokenEntityHaltsLoudly(t *testing.T) {
	var buf bytes.Buffer
	err := dispatchValidate(resolveTempWorkspace(t), validateBrokenPath, []string{validateCatalogDir}, &buf)
	if err == nil {
		t.Fatalf("expected a schema error, got nil; output:\n%s", buf.String())
	}
	if !errors.Is(err, envelope.ErrEntityEnvelope) {
		t.Errorf("error does not wrap ErrEntityEnvelope: %v", err)
	}
	if !strings.Contains(err.Error(), "envelope schema") {
		t.Errorf("diagnostic does not name the envelope schema: %v", err)
	}
}

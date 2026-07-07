// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the early lint-time input-kind capability detector
// (inputKindCapabilityErrors): a workload:-declaring Recipe/FeatureRecipe bound
// to a provider whose plugin cannot synthesize is a guaranteed render halt,
// surfaced at lint. The detector reads the provider's input-kind support from the
// authoring-time capabilities claim on its serving Plugin entity (via
// enginefactory.CapabilitiesFor) and reuses the gate's message clause, so the
// early and late diagnostics agree.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// End-to-end: a workload:+helm-chart Recipe in the catalog makes whole-catalog
// `aggrega lint` fail (the wiring from lintCatalogWide into the detector).
func TestLintCatalogWide_FlagsWorkloadOnHelmChart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: api-helmchart
  version: "1.0.0"
spec:
  provider: helm-chart
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
`)
	writeFixture(t, filepath.Join(dir, "plugin.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm-chart-renderer
  version: "1.0.0"
spec:
  plugin: oci://hub.aggrega.ai/plugins/helmchart-provider@sha256:01b1e8ed9b62b2f9f44e90163a233bbf38bc1bc3973fe88712bbf0950bff30f7
  pluginType: provider
  abiVersion: v1alpha1
  compatibleProviders:
    - helm-chart
  exports:
    - name: render_node
    - name: assemble_boundary
  capabilities:
    inputKinds:
      sourced: true
    boundaryAssembly: nest
`)
	err := lintCatalogWide("", "", []string{dir}, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("expected lint to flag workload:+helm-chart; got nil")
	}
	if !strings.Contains(err.Error(), "input-kind capability") {
		t.Fatalf("expected input-kind capability failure; got: %v", err)
	}
}

// End-to-end: a workload:+compose Recipe (synthesizing engine) lints clean —
// only a non-synthesizing engine trips the detector.
func TestLintCatalogWide_AllowsWorkloadOnCompose(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: api-compose
  version: "1.0.0"
spec:
  provider: compose
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
`)
	writeFixture(t, filepath.Join(dir, "plugin.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: compose-renderer
  version: "1.0.0"
spec:
  plugin: oci://hub.aggrega.ai/plugins/compose-provider@sha256:e58a3914ad4601e457571c41381856babfc7064387ce08ea13ac904b9cb70ca5
  pluginType: provider
  abiVersion: v1alpha1
  compatibleProviders:
    - compose
  exports:
    - name: render_node
    - name: assemble_boundary
  capabilities:
    inputKinds:
      sourced: true
      synthesized: true
    boundaryAssembly: merge
`)
	if err := lintCatalogWide("", "", []string{dir}, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("workload:+compose (synthesizing) must lint clean; got %v", err)
	}
}

// fakeProviderPlugins resolves the shipped provider names to a Plugin entity
// carrying the provider's authored capabilities.input_kinds claim — the same shape
// the render route resolves — so the detector's unit tests exercise the
// entity-claim path without a catalog. An unknown provider resolves nothing.
func fakeProviderPlugins(ref string) (types.Plugin, bool, error) {
	claim := func(ik types.PluginInputKinds) (types.Plugin, bool, error) {
		return types.Plugin{Spec: types.PluginSpec{Capabilities: &types.PluginCapabilities{InputKinds: ik}}}, true, nil
	}
	switch ref {
	case "helm":
		return claim(types.PluginInputKinds{Sourced: true, Synthesized: true, ArtifactsStandalone: true})
	case "compose":
		return claim(types.PluginInputKinds{Sourced: true, Synthesized: true})
	case "helm-chart":
		return claim(types.PluginInputKinds{Sourced: true})
	case "terraform":
		return claim(types.PluginInputKinds{Sourced: true, ArtifactsStandalone: true})
	default:
		return types.Plugin{}, false, nil
	}
}

func wlRecipe(name, provider string, workload bool) types.Recipe {
	var r types.Recipe
	r.Metadata.Name = name
	r.Metadata.Version = "1.0.0"
	r.Spec.Provider = provider
	if workload {
		r.Spec.Workload = types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1alpha1\nkind: Workload\n"}
	}
	return r
}

func wlFeatureRecipe(name, provider string, workload bool) types.FeatureRecipe {
	var fr types.FeatureRecipe
	fr.Metadata.Name = name
	fr.Metadata.Version = "1.0.0"
	fr.Spec.Provider = provider
	if workload {
		fr.Spec.Workload = types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1alpha1\nkind: Workload\n"}
	}
	return fr
}

// workload: on helm-chart (engine ships, Synthesized=false) → flagged, with a
// message at parity with the render gate's clause.
func TestInputKindCapabilityErrors_FlagsWorkloadOnNonSynthesizingEngine(t *testing.T) {
	errs := inputKindCapabilityErrors(fakeProviderPlugins, []types.Recipe{wlRecipe("api", "helm-chart", true)}, nil)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for workload:+helm-chart; got %d: %v", len(errs), errs)
	}
	msg := errs[0].Error()
	for _, want := range []string{`Recipe "api:1.0.0"`, `provider "helm-chart"`, "does not support synthesised recipes", "this provider supports: sourced"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q; got: %s", want, msg)
		}
	}
}

// workload: on a synthesizing engine (helm, compose, and the empty-name helm
// default) → clean.
func TestInputKindCapabilityErrors_AllowsSynthesizingEngines(t *testing.T) {
	errs := inputKindCapabilityErrors(fakeProviderPlugins, []types.Recipe{
		wlRecipe("a", "helm", true),
		wlRecipe("b", "compose", true),
		wlRecipe("c", "", true),
	}, nil)
	if len(errs) != 0 {
		t.Fatalf("workload on synthesizing engines must be clean; got: %v", errs)
	}
}

// A non-workload Recipe on helm-chart (sourced/artifacts mode) → clean: only
// workload: triggers the check.
func TestInputKindCapabilityErrors_IgnoresNonWorkload(t *testing.T) {
	errs := inputKindCapabilityErrors(fakeProviderPlugins, []types.Recipe{wlRecipe("api", "helm-chart", false)}, nil)
	if len(errs) != 0 {
		t.Fatalf("non-workload recipe must not trip the check; got: %v", errs)
	}
}

// Unknown / unimplemented / eval providers bind to no shipped engine
// (CapabilitiesFor ok=false) → skipped, no false positive against forward-looking
// catalog scaffolding.
func TestInputKindCapabilityErrors_SkipsUnknownProviders(t *testing.T) {
	errs := inputKindCapabilityErrors(fakeProviderPlugins, []types.Recipe{
		wlRecipe("b", "cloudformation", true),
		wlRecipe("c", "eval", true),
		wlRecipe("d", "nonsense-typo", true),
	}, nil)
	if len(errs) != 0 {
		t.Fatalf("unknown/unimplemented providers must be skipped (ok=false); got: %v", errs)
	}
}

// workload: on terraform (engine ships, Synthesized=false) → flagged, with a
// message naming the input kinds terraform does support (sourced, artifacts-only).
func TestInputKindCapabilityErrors_FlagsWorkloadOnTerraform(t *testing.T) {
	errs := inputKindCapabilityErrors(fakeProviderPlugins, []types.Recipe{wlRecipe("api", "terraform", true)}, nil)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for workload:+terraform; got %d: %v", len(errs), errs)
	}
	msg := errs[0].Error()
	for _, want := range []string{`Recipe "api:1.0.0"`, `provider "terraform"`, "does not support synthesised recipes", "this provider supports: sourced, artifacts-only"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q; got: %s", want, msg)
		}
	}
}

// FeatureRecipe is checked on the same footing as Recipe.
func TestInputKindCapabilityErrors_FeatureRecipe(t *testing.T) {
	errs := inputKindCapabilityErrors(fakeProviderPlugins, nil, []types.FeatureRecipe{wlFeatureRecipe("sidecar", "helm-chart", true)})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `FeatureRecipe "sidecar:1.0.0"`) {
		t.Fatalf("expected 1 FeatureRecipe error; got: %v", errs)
	}
}

// Output order is deterministic regardless of input order (whole-catalog mode
// ranges an unordered map).
func TestInputKindCapabilityErrors_Deterministic(t *testing.T) {
	errs := inputKindCapabilityErrors(fakeProviderPlugins, []types.Recipe{
		wlRecipe("zeta", "helm-chart", true),
		wlRecipe("alpha", "helm-chart", true),
	}, nil)
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors; got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), "alpha") || !strings.Contains(errs[1].Error(), "zeta") {
		t.Fatalf("expected deterministic alpha-before-zeta order; got: %v", errs)
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the early lint-time input-kind capability detector
// (inputKindCapabilityErrors): a workload:-declaring Recipe/FeatureRecipe bound
// to a known provider whose engine cannot synthesize is a guaranteed render
// halt, surfaced at lint. The detector reads capabilities through
// enginefactory.CapabilitiesFor (the same Dispatch the render gate uses) and
// reuses the gate's message clause, so the early and late diagnostics agree.

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
	err := lintCatalogWide("", "", []string{dir}, os.Stderr)
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
	if err := lintCatalogWide("", "", []string{dir}, os.Stderr); err != nil {
		t.Fatalf("workload:+compose (synthesizing) must lint clean; got %v", err)
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
	errs := inputKindCapabilityErrors([]types.Recipe{wlRecipe("api", "helm-chart", true)}, nil)
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
	errs := inputKindCapabilityErrors([]types.Recipe{
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
	errs := inputKindCapabilityErrors([]types.Recipe{wlRecipe("api", "helm-chart", false)}, nil)
	if len(errs) != 0 {
		t.Fatalf("non-workload recipe must not trip the check; got: %v", errs)
	}
}

// Unknown / unimplemented / eval providers bind to no shipped engine
// (CapabilitiesFor ok=false) → skipped, no false positive against forward-looking
// catalog scaffolding.
func TestInputKindCapabilityErrors_SkipsUnknownProviders(t *testing.T) {
	errs := inputKindCapabilityErrors([]types.Recipe{
		wlRecipe("a", "terraform", true),
		wlRecipe("b", "cloudformation", true),
		wlRecipe("c", "eval", true),
		wlRecipe("d", "nonsense-typo", true),
	}, nil)
	if len(errs) != 0 {
		t.Fatalf("unknown/unimplemented providers must be skipped (ok=false); got: %v", errs)
	}
}

// FeatureRecipe is checked on the same footing as Recipe.
func TestInputKindCapabilityErrors_FeatureRecipe(t *testing.T) {
	errs := inputKindCapabilityErrors(nil, []types.FeatureRecipe{wlFeatureRecipe("sidecar", "helm-chart", true)})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `FeatureRecipe "sidecar:1.0.0"`) {
		t.Fatalf("expected 1 FeatureRecipe error; got: %v", errs)
	}
}

// Output order is deterministic regardless of input order (whole-catalog mode
// ranges an unordered map).
func TestInputKindCapabilityErrors_Deterministic(t *testing.T) {
	errs := inputKindCapabilityErrors([]types.Recipe{
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

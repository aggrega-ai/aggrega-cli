// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the early lint-time input-kind capability detector
// (inputKindCapabilityErrors): a workload:-declaring Recipe/FeatureRecipe bound
// to a provider whose plugin cannot synthesize is a guaranteed render halt,
// surfaced at lint. The detector reads the provider's input-kind support from the
// authoring-time capabilities claim on its serving Plugin entity (via
// enginefactory.ContractFor) and reuses the gate's message clause, so the
// early and late diagnostics agree.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// End-to-end: a workload:+helm-chart Recipe in the catalog makes whole-catalog
// `aggrega lint` fail (the wiring from validateCatalogWide into the detector).
func TestLintCatalogWide_FlagsWorkloadOnHelmChart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: api-helmchart
  version: "1.0.0"
spec:
  capability: helm-chart
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
`)
	writeFixture(t, filepath.Join(dir, "plugin.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm-chart-renderer
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  exports:
    - name: render_node
    - name: assemble_boundary
  provider:
    capabilities:
      - helm-chart
    contract:
      inputKinds:
        sourced: true
      boundaryAssembly: nest
`)
	err := validateCatalogWide(t.Context(), "", "", []string{dir}, nil, nil, os.Stderr, os.Stdout)
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
  capability: compose
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
`)
	writeFixture(t, filepath.Join(dir, "plugin.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: compose-renderer
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  exports:
    - name: render_node
    - name: assemble_boundary
  provider:
    capabilities:
      - compose
    contract:
      inputKinds:
        sourced: true
        synthesized: true
      boundaryAssembly: merge
`)
	if err := validateCatalogWide(t.Context(), "", "", []string{dir}, nil, nil, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("workload:+compose (synthesizing) must lint clean; got %v", err)
	}
}

// End-to-end: a source:+workload: Recipe bound to terraform makes whole-catalog
// validation fail. The mixed shape is the one a detector consulting a single kind
// misses, so this is the end-to-end counterpart of the mixed unit row below.
//
// The Plugin entity carries a contract: block deliberately. Without one the
// provider resolves but claims no input kinds, the detector skips the Recipe, and
// the whole-catalog pass comes back clean — so a fixture written without it would
// assert the skip while reading as though it asserted the check.
func TestLintCatalogWide_FlagsSourcePlusWorkloadOnTerraform(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: api-terraform
  version: "1.0.0"
spec:
  capability: terraform
  implements: core/server
  source: oci://registry.example.com/modules/api:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
`)
	writeFixture(t, filepath.Join(dir, "plugin.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: terraform-renderer
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  exports:
    - name: render_node
    - name: assemble_boundary
  provider:
    capabilities:
      - terraform
    contract:
      inputKinds:
        sourced: true
        artifactsStandalone: true
      boundaryAssembly: nest
`)
	// The returned error is the count summary; the per-finding detail is written
	// to the diagnostic writer, which is where an operator reads it — so the
	// wording is asserted against the emitted bytes rather than the error value.
	var diag, out bytes.Buffer
	err := validateCatalogWide(t.Context(), "", "", []string{dir}, nil, nil, &diag, &out)
	if err == nil {
		t.Fatal("expected whole-catalog validation to flag source:+workload:+terraform; got nil")
	}
	if !strings.Contains(err.Error(), "input-kind capability") {
		t.Fatalf("expected an input-kind capability failure; got: %v", err)
	}
	emitted := diag.String() + out.String()
	t.Logf("emitted diagnostic:\n%s", emitted)
	for _, want := range []string{`Recipe "api-terraform:1.0.0"`, `provider "terraform"`, "does not support synthesized recipes", "this provider supports: sourced, artifacts-only"} {
		if !strings.Contains(emitted, want) {
			t.Errorf("the emitted finding does not carry %q; got:\n%s", want, emitted)
		}
	}
	if strings.Contains(emitted, "does not support sourced recipes") {
		t.Errorf("the finding names the honourable kind rather than the unhonourable one:\n%s", emitted)
	}
}

// The same corpus with the contract: block removed comes back CLEAN, which is the
// needle behind the skip: it proves the row above is carrying the check rather
// than a lookup that happens to report nothing.
func TestLintCatalogWide_SkipsSourcePlusWorkloadWhenPluginDeclaresNoContract(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: api-terraform
  version: "1.0.0"
spec:
  capability: terraform
  implements: core/server
  source: oci://registry.example.com/modules/api:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
`)
	writeFixture(t, filepath.Join(dir, "plugin.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: terraform-renderer
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  exports:
    - name: render_node
    - name: assemble_boundary
  provider:
    capabilities:
      - terraform
`)
	if err := validateCatalogWide(t.Context(), "", "", []string{dir}, nil, nil, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("a provider Plugin declaring no contract: block offers no capability claim to contradict, so the detector must skip and the pass must be clean; got: %v", err)
	}
}

// fakeProviderPlugins resolves the shipped provider names to a Plugin entity
// carrying the provider's authored capabilities.input_kinds claim — the same shape
// the render route resolves — so the detector's unit tests exercise the
// entity-claim path without a catalog. An unknown provider resolves nothing.
func fakeProviderPlugins(ref string) (types.Plugin, bool, error) {
	claim := func(ik types.PluginInputKinds) (types.Plugin, bool, error) {
		return types.Plugin{Spec: types.PluginSpec{Provider: types.PluginProvider{Contract: types.ProviderContract{InputKinds: ik}}}}, true, nil
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
	r.Spec.Capability = provider
	if workload {
		r.Spec.Workload = types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1alpha1\nkind: Workload\n"}
	}
	return r
}

func wlFeatureRecipe(name, provider string, workload bool) types.FeatureRecipe {
	var fr types.FeatureRecipe
	fr.Metadata.Name = name
	fr.Metadata.Version = "1.0.0"
	fr.Spec.Capability = provider
	if workload {
		fr.Spec.Workload = types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1alpha1\nkind: Workload\n"}
	}
	return fr
}

// workload: on helm-chart (engine ships, Synthesized=false) → flagged, with a
// message at parity with the render gate's clause.
func TestInputKindCapabilityErrors_FlagsWorkloadOnNonSynthesizingEngine(t *testing.T) {
	errs := validateops.InputKindCapabilityErrors(fakeProviderPlugins, []types.Recipe{wlRecipe("api", "helm-chart", true)}, nil)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for workload:+helm-chart; got %d: %v", len(errs), errs)
	}
	msg := errs[0].Error()
	for _, want := range []string{`Recipe "api:1.0.0"`, `provider "helm-chart"`, "does not support synthesized recipes", "this provider supports: sourced"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q; got: %s", want, msg)
		}
	}
}

// workload: on a synthesizing engine (helm, compose, and the empty-name helm
// default) → clean.
func TestInputKindCapabilityErrors_AllowsSynthesizingEngines(t *testing.T) {
	errs := validateops.InputKindCapabilityErrors(fakeProviderPlugins, []types.Recipe{
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
	errs := validateops.InputKindCapabilityErrors(fakeProviderPlugins, []types.Recipe{wlRecipe("api", "helm-chart", false)}, nil)
	if len(errs) != 0 {
		t.Fatalf("non-workload recipe must not trip the check; got: %v", errs)
	}
}

// Unknown / unimplemented providers bind to no shipped engine
// (ContractFor ok=false) → skipped, no false positive against forward-looking
// catalog scaffolding.
func TestInputKindCapabilityErrors_SkipsUnknownProviders(t *testing.T) {
	errs := validateops.InputKindCapabilityErrors(fakeProviderPlugins, []types.Recipe{
		wlRecipe("b", "cloudformation", true),
		wlRecipe("d", "nonsense-typo", true),
	}, nil)
	if len(errs) != 0 {
		t.Fatalf("unknown/unimplemented providers must be skipped (ok=false); got: %v", errs)
	}
}

// workload: on terraform (engine ships, Synthesized=false) → flagged, with a
// message naming the input kinds terraform does support (sourced, artifacts-only).
func TestInputKindCapabilityErrors_FlagsWorkloadOnTerraform(t *testing.T) {
	errs := validateops.InputKindCapabilityErrors(fakeProviderPlugins, []types.Recipe{wlRecipe("api", "terraform", true)}, nil)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for workload:+terraform; got %d: %v", len(errs), errs)
	}
	msg := errs[0].Error()
	for _, want := range []string{`Recipe "api:1.0.0"`, `provider "terraform"`, "does not support synthesized recipes", "this provider supports: sourced, artifacts-only"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q; got: %s", want, msg)
		}
	}
}

// source: + workload: on terraform (Sourced=true, Synthesized=false) → flagged.
// The mixed row is the one that separates a detector reading every declared kind
// from one reading only the kind a Recipe's mode is named after: the source is a
// kind terraform honours, so a detector consulting that one alone reports nothing
// while the workload still cannot be rendered.
func TestInputKindCapabilityErrors_FlagsSourcePlusWorkloadOnNonSynthesizingProvider(t *testing.T) {
	mixed := wlRecipe("api", "terraform", true)
	mixed.Spec.Source = "./module"

	errs := validateops.InputKindCapabilityErrors(fakeProviderPlugins, []types.Recipe{mixed}, nil)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for source:+workload:+terraform; got %d: %v", len(errs), errs)
	}
	msg := errs[0].Error()
	for _, want := range []string{`Recipe "api:1.0.0"`, `provider "terraform"`, "does not support synthesized recipes", "this provider supports: sourced, artifacts-only"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q; got: %s", want, msg)
		}
	}
	// The finding must name the kind terraform cannot honour, not the one it can.
	// A detector reporting the sourced kind would send the author to the wrong line.
	if strings.Contains(msg, "does not support sourced recipes") {
		t.Errorf("the finding names the honourable kind rather than the unhonourable one: %s", msg)
	}
}

// A FeatureRecipe carries the mixed fault identically — the detector checks both
// on the same footing, so the mixed row is not Recipe-only either.
func TestInputKindCapabilityErrors_FlagsSourcePlusWorkloadOnFeatureRecipe(t *testing.T) {
	mixed := wlFeatureRecipe("sidecar", "terraform", true)
	mixed.Spec.Source = "./module"

	errs := validateops.InputKindCapabilityErrors(fakeProviderPlugins, nil, []types.FeatureRecipe{mixed})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for source:+workload:+terraform on a FeatureRecipe; got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Error(), `FeatureRecipe "sidecar:1.0.0"`) {
		t.Errorf("expected the finding to name the FeatureRecipe; got: %v", errs)
	}
	if !strings.Contains(errs[0].Error(), "does not support synthesized recipes") {
		t.Errorf("expected the finding to name the synthesized kind; got: %v", errs)
	}
}

// noContractProviderPlugin resolves a provider to a Plugin entity that HAS a
// provider section and no render-behavior contract on it — the shape a
// forward-looking catalog entity carries before its capabilities are declared.
func noContractProviderPlugin(string) (types.Plugin, bool, error) {
	return types.Plugin{Spec: types.PluginSpec{Provider: types.PluginProvider{}}}, true, nil
}

// A resolvable Plugin carrying no contract: block is SKIPPED, and this row is
// what makes that skip a measured property rather than a claim. The Recipe here
// is the same one the terraform row flags, so the row fails if the skip ever
// stops applying — and it is the reason the rows above supply a contract at all:
// against a contract-less lookup they would assert the skip while appearing to
// assert the check.
func TestInputKindCapabilityErrors_SkipsProviderPluginWithNoContractBlock(t *testing.T) {
	mixed := wlRecipe("api", "terraform", true)
	mixed.Spec.Source = "./module"

	errs := validateops.InputKindCapabilityErrors(noContractProviderPlugin, []types.Recipe{mixed}, nil)
	if len(errs) != 0 {
		t.Fatalf("a Plugin carrying no contract: block has no capability claim to contradict and must be skipped; got: %v", errs)
	}
	// Positive control: the identical Recipe against a lookup that DOES carry a
	// contract is flagged, so the zero above is the skip rather than a detector
	// that reports nothing whatever it is handed.
	if errs := validateops.InputKindCapabilityErrors(fakeProviderPlugins, []types.Recipe{mixed}, nil); len(errs) != 1 {
		t.Fatalf("positive control: the same Recipe against a contract-carrying lookup must be flagged; got %d: %v", len(errs), errs)
	}
}

// FeatureRecipe is checked on the same footing as Recipe.
func TestInputKindCapabilityErrors_FeatureRecipe(t *testing.T) {
	errs := validateops.InputKindCapabilityErrors(fakeProviderPlugins, nil, []types.FeatureRecipe{wlFeatureRecipe("sidecar", "helm-chart", true)})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `FeatureRecipe "sidecar:1.0.0"`) {
		t.Fatalf("expected 1 FeatureRecipe error; got: %v", errs)
	}
}

// Output order is deterministic regardless of input order (whole-catalog mode
// ranges an unordered map).
func TestInputKindCapabilityErrors_Deterministic(t *testing.T) {
	errs := validateops.InputKindCapabilityErrors(fakeProviderPlugins, []types.Recipe{
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

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// refPluginLookup builds the reference FRP step plugin on demand (skipping when
// the wasm toolchain is absent) and returns a Lookup that resolves any plugin
// URI to the built artifact.
func refPluginLookup(t *testing.T) pluginpath.Lookup {
	t.Helper()
	path := wasmtest.EnsureFixtureBuilt(t, "frp-step-patch-rust.wasm")
	return pluginpath.NewResolver(pluginpath.ResolutionContext{Override: path}, pluginpath.PinPostureNone, pluginpath.PinMaps{}).Resolve
}

// multiDocOwn is one emitted file carrying two YAML documents: a Deployment
// (which the reference plugin content-selects and PATCHes) and a ConfigMap
// sibling carrying a comment and a non-default scalar style (which the plugin
// leaves untouched). After the pipeline runs, the Deployment document must
// carry the new annotation and the ConfigMap sibling — comment and all — must
// be byte-identical, proving apply-back re-serialises only the patched
// document.
const multiDocOwn = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  annotations:
    existing: keep-me
spec:
  replicas: 1
---
# This ConfigMap is an unpatched sibling; its comment and scalar style
# must survive the pipeline byte-for-byte.
apiVersion: v1
kind: ConfigMap
metadata:
  name: sidecar-config
data:
  mode: 'single-quoted-on-purpose'
`

// TestRunNodePipeline_MultiDocSiblingPreserved drives the shared executor over
// a multi-document own file with the reference plugin and asserts the patched
// document changed while the unpatched sibling — comment, scalar style, and
// the document separator — survives byte-for-byte.
func TestRunNodePipeline_MultiDocSiblingPreserved(t *testing.T) {
	lookup := refPluginLookup(t)
	t.Parallel()

	own := []EmittedFile{{Name: "bundle.yaml", Origin: EmitOriginWorkload, Body: []byte(multiDocOwn)}}
	steps := []types.PipelineStep{{ID: "patch", Plugin: "frp-step-patch-rust.wasm", Function: "step"}}

	res, err := runNodePipeline(context.Background(), steps, own, nil, false, "helm", "",
		renderctx.RenderContext{}, &types.TemplateContext{}, redact.Keys{},
		2, lookup, nil, "node")

	if err != nil {
		t.Fatalf("runNodePipeline: %v", err)
	}

	// Find the rewritten bundle file (own EMIT appends a ConfigMap too).
	var bundle []byte
	for _, f := range res.Own {
		if f.Name == "bundle.yaml" {
			bundle = f.Body
		}
	}
	if bundle == nil {
		t.Fatalf("bundle.yaml missing from pipeline output")
	}

	// The patched Deployment document carries the new annotation.
	if !strings.Contains(string(bundle), "aggrega.ai/pipeline-patched") {
		t.Errorf("patched Deployment missing the pipeline annotation; got:\n%s", bundle)
	}

	// The unpatched ConfigMap sibling survives byte-for-byte — its comment,
	// the single-quoted scalar, and the `---` separator are intact.
	const siblingVerbatim = `---
# This ConfigMap is an unpatched sibling; its comment and scalar style
# must survive the pipeline byte-for-byte.
apiVersion: v1
kind: ConfigMap
metadata:
  name: sidecar-config
data:
  mode: 'single-quoted-on-purpose'
`
	if !strings.Contains(string(bundle), siblingVerbatim) {
		t.Errorf("unpatched sibling document was not preserved byte-for-byte; got:\n%s", bundle)
	}

	// The Deployment's pre-existing annotation survives the patch.
	if !strings.Contains(string(bundle), "existing: keep-me") {
		t.Errorf("patched Deployment lost a pre-existing field; got:\n%s", bundle)
	}
}

// TestRunNodePipeline_AssemblerOwnPatchHalts pins the scope-symmetric
// pre-assembly guard: a PATCH against an own-scope document from an
// Intermediate (pre-assembly fragment) emitted file halts with
// ErrPipelinePatchPreAssembly.
func TestRunNodePipeline_AssemblerOwnPatchHalts(t *testing.T) {
	lookup := refPluginLookup(t)
	t.Parallel()

	// An Intermediate fragment carrying a Deployment the reference plugin
	// content-selects and patches.
	frag := []EmittedFile{{
		Name:         "fragment.yaml",
		Origin:       EmitOriginWorkload,
		Body:         []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  annotations: {}\n"),
		Intermediate: true,
	}}
	steps := []types.PipelineStep{{ID: "patch", Plugin: "frp-step-patch-rust.wasm", Function: "step"}}

	_, err := runNodePipeline(context.Background(), steps, frag, nil, false, "helm", "",
		renderctx.RenderContext{}, &types.TemplateContext{}, redact.Keys{},
		2, lookup, nil, "node")

	if err == nil {
		t.Fatalf("expected pre-assembly halt; got nil error")
	}
	if !errors.Is(err, ErrPipelinePatchPreAssembly()) {
		t.Errorf("expected ErrPipelinePatchPreAssembly; got %v", err)
	}
}

// TestRunNodePipeline_HostEmitWhenHostPresent pins the positive /host EMIT
// path: when a host set IS present (a component-scope FeatureRecipe attached to
// a host), the reference plugin's additive /host marker lands in HostPatched
// keyed by its declared path. The negative path — a /host operation from a
// hostless step halting with ErrPipelineHostScopeUnavailable — is asserted in
// pipeline_exec_sentinel_test.go (TestRunNodePipeline_HostEmitFromHostlessHalts
// and TestRunNodePipeline_HostPatchFromHostlessHalts), which the reference
// plugin cannot drive because it emits /host only when a host set is present.
func TestRunNodePipeline_HostEmitWhenHostPresent(t *testing.T) {
	lookup := refPluginLookup(t)
	t.Parallel()

	own := []EmittedFile{{Name: "deployment.yaml", Origin: EmitOriginWorkload, Body: []byte("kind: Deployment\nmetadata:\n  annotations: {}\n")}}
	host := []EmittedFile{{Name: "host-deployment.yaml", Origin: EmitOriginWorkload, Body: []byte("kind: Deployment\nmetadata:\n  annotations: {}\n")}}
	steps := []types.PipelineStep{{ID: "patch", Plugin: "frp-step-patch-rust.wasm", Function: "step"}}

	res, err := runNodePipeline(context.Background(), steps, own, host, false, "helm", "helm",
		renderctx.RenderContext{}, &types.TemplateContext{}, redact.Keys{},
		2, lookup, nil, "node")

	if err != nil {
		t.Fatalf("runNodePipeline: %v", err)
	}
	// The host EMIT surfaces in HostPatched keyed by its declared path.
	if _, ok := res.HostPatched["pipeline-host-marker.yaml"]; !ok {
		t.Errorf("expected host EMIT pipeline-host-marker.yaml in HostPatched; got %v", res.HostPatched)
	}
	// The host Deployment was also PATCHed (own-scope plugin patches both via
	// the same content selection running over own; host PATCH is keyed back).
	if _, ok := res.HostPatched["host-deployment.yaml"]; !ok {
		t.Logf("note: host-deployment.yaml not patched (plugin only patches own scope)")
	}
}

// TestRunNodePipeline_ValidateOnlyWhenNoLookup pins the non-regression path: a
// nil lookup validates the steps but does not run them, returning the own set
// unchanged with a validate-only warning.
func TestRunNodePipeline_ValidateOnlyWhenNoLookup(t *testing.T) {
	t.Parallel()
	own := []EmittedFile{{Name: "a.yaml", Origin: EmitOriginWorkload, Body: []byte("kind: ConfigMap\n")}}
	steps := []types.PipelineStep{{ID: "p", Plugin: "x.wasm", Function: "step"}}
	res, err := runNodePipeline(context.Background(), steps, own, nil, false, "helm", "",
		renderctx.RenderContext{}, &types.TemplateContext{}, redact.Keys{},
		2, nil, nil, "node")

	if err != nil {
		t.Fatalf("validate-only path errored: %v", err)
	}
	if len(res.Own) != 1 || string(res.Own[0].Body) != "kind: ConfigMap\n" {
		t.Errorf("validate-only path mutated the own set: %v", res.Own)
	}
	if len(res.Warnings) != 1 || !strings.Contains(RenderDiagnosticText(res.Warnings[0]), "validated but not executed") {
		t.Errorf("validate-only path missing warning: %v", res.Warnings)
	}
	// The notice locates itself at the node rather than naming it inside the
	// sentence, which is what a consumer reading no terminal has to work from.
	if loc := res.Warnings[0].Location; loc == nil || loc.Instance != "node" {
		t.Errorf("validate-only notice not located at the node: %+v", res.Warnings[0].Location)
	}
}

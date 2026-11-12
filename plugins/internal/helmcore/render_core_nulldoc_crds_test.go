// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmcore_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
)

// TestHelmEngine_Sourced_NullDocDroppedNotNamed pins the null-document skip: a
// template whose only content is a bare "---" left by a false conditional
// deserializes to a null document that carries no resource. It must be dropped —
// not halted, and not path-fallback-named — while an unguarded resource in
// another template of the same chart still emits.
func TestHelmEngine_Sourced_NullDocDroppedNotNamed(t *testing.T) {
	t.Parallel()
	const wantAlways = "nd-configmap-always-config.yaml"

	files, err := helmcore.RenderLoadedChart(context.Background(), loadChart(t, "testdata/null-doc-chart"),
		map[string]any{}, nodeCtx("nd", "testdata/null-doc-chart"), helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("a null document from a false conditional must be dropped, not halt: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected exactly 1 output file (the unguarded resource); got %d: %v", len(files), sortedFileKeys(files))
	}
	always, ok := files[wantAlways]
	if !ok {
		t.Fatalf("no unguarded resource output at %q; files: %v", wantAlways, sortedFileKeys(files))
	}
	if !strings.Contains(string(always), "key: always") {
		t.Errorf("unguarded resource content missing; got:\n%s", always)
	}
	// The dropped null document must not resurface as a path-fallback-named file.
	for _, k := range sortedFileKeys(files) {
		if strings.Contains(k, "optional") {
			t.Errorf("null document was path-fallback-named instead of dropped: %q", k)
		}
	}
}

// TestHelmEngine_Sourced_CrdsEmitted pins the crds/ emission: a chart shipping a
// CustomResourceDefinition under its Helm-native crds/ directory emits that CRD
// as its own output file, keyed by resource identity exactly like a templated
// resource and carrying injected provenance labels, alongside the chart's
// templated resources.
func TestHelmEngine_Sourced_CrdsEmitted(t *testing.T) {
	t.Parallel()
	const wantCRD = "crd-customresourcedefinition-widgets-example-com.yaml"
	const wantDeploy = "crd-deployment-widget-operator.yaml"

	files, err := helmcore.RenderLoadedChart(context.Background(), loadChart(t, "testdata/crd-chart"),
		map[string]any{}, nodeCtx("crd", "testdata/crd-chart"), helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 output files (CRD + templated Deployment); got %d: %v", len(files), sortedFileKeys(files))
	}

	crd, ok := files[wantCRD]
	if !ok {
		t.Fatalf("no CRD output at %q; files: %v", wantCRD, sortedFileKeys(files))
	}
	for _, want := range []string{"kind: CustomResourceDefinition", "name: widgets.example.com", "kind: Widget"} {
		if !strings.Contains(string(crd), want) {
			t.Errorf("CRD content missing %q; got:\n%s", want, crd)
		}
	}
	// The CRD is labeled through the same identity path as templated resources.
	if !strings.Contains(string(crd), "aggrega.ai/component-id: crd") {
		t.Errorf("CRD carries no injected provenance label; got:\n%s", crd)
	}

	if _, ok := files[wantDeploy]; !ok {
		t.Errorf("no templated Deployment output at %q; files: %v", wantDeploy, sortedFileKeys(files))
	}
}

// TestHelmEngine_Sourced_CrdsEmissionIsDeterministic pins byte-identity of the
// crds/ emission across two renders of the same chart — the determinism GitOps
// diffs rely on and the property that keeps the host engine and the provider
// guest byte-for-byte identical.
func TestHelmEngine_Sourced_CrdsEmissionIsDeterministic(t *testing.T) {
	t.Parallel()
	first, err := helmcore.RenderLoadedChart(context.Background(), loadChart(t, "testdata/crd-chart"),
		map[string]any{}, nodeCtx("crd", "testdata/crd-chart"), helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	second, err := helmcore.RenderLoadedChart(context.Background(), loadChart(t, "testdata/crd-chart"),
		map[string]any{}, nodeCtx("crd", "testdata/crd-chart"), helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("file count differs across runs: %v vs %v", sortedFileKeys(first), sortedFileKeys(second))
	}
	for k, v := range first {
		w, ok := second[k]
		if !ok {
			t.Errorf("file %q present in first run, absent in second", k)
			continue
		}
		if string(v) != string(w) {
			t.Errorf("file %q differs across runs:\nfirst:\n%s\nsecond:\n%s", k, v, w)
		}
	}
}

// TestHelmEngine_Sourced_CrdTemplateCollisionHalts pins that the Loud Guardrail
// spans the crds/ and templates/ surfaces: a crds/ CRD and a templates/ CRD that
// derive one output filename with differing content halt on the
// output-name-collision condition rather than silently drop one.
func TestHelmEngine_Sourced_CrdTemplateCollisionHalts(t *testing.T) {
	t.Parallel()
	_, err := helmcore.RenderLoadedChart(context.Background(), loadChart(t, "testdata/crd-collision-chart"),
		map[string]any{}, nodeCtx("xc", "testdata/crd-collision-chart"), helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err == nil {
		t.Fatal("expected a collision error when a crds/ and a templates/ resource derive one output filename")
	}
	if !errors.Is(err, plugincondition.ErrPluginOutputNameCollision()) {
		t.Fatalf("error does not carry the output-name-collision condition: %v", err)
	}
	if !strings.Contains(err.Error(), "xc-customresourcedefinition-things-example-com.yaml") {
		t.Errorf("collision message missing the shared output path; got: %s", err.Error())
	}
	// The condition names the kind; this sentence is what still says a rendered
	// file is what collided rather than some other name this core derives.
	if want := "two rendered resources derive the same output filename with differing content"; !strings.Contains(err.Error(), want) {
		t.Errorf("collision message missing %q; got: %s", want, err.Error())
	}
}

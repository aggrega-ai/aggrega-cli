// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/helm-provider/helmcore"
)

// TestHelmEngine_Sourced_NamelessDocsGetPathFallback pins the path-based fallback
// filename: a rendered document that carries no resource identity — a
// generateName resource, a List wrapper — no longer halts the render. Each such
// document is emitted under a deterministic filename derived from its rendered
// template path, and both survive as distinct, buildable output files carrying
// injected provenance labels.
func TestHelmEngine_Sourced_NamelessDocsGetPathFallback(t *testing.T) {
	t.Parallel()
	const wantGenerateName = "cfg-nameless-doc-templates-generatename.yaml"
	const wantList = "cfg-nameless-doc-templates-list.yaml"

	files, err := helmcore.RenderLoadedChart(context.Background(), loadChart(t, "testdata/nameless-doc-chart"),
		map[string]any{}, nodeCtx("cfg", "testdata/nameless-doc-chart"), helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("nameless documents should render, not halt: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 path-keyed output files, got %d: %v", len(files), sortedFileKeys(files))
	}

	gen, ok := files[wantGenerateName]
	if !ok {
		t.Fatalf("no generateName output at %q; files: %v", wantGenerateName, sortedFileKeys(files))
	}
	if !strings.Contains(string(gen), "generateName: gen-config-") {
		t.Errorf("generateName resource content missing; got:\n%s", gen)
	}
	// Provenance labels must be injected so the doc is buildable, not just named.
	if !strings.Contains(string(gen), "aggrega.ai/component-id: cfg") {
		t.Errorf("generateName resource carries no injected provenance label; got:\n%s", gen)
	}

	list, ok := files[wantList]
	if !ok {
		t.Fatalf("no List output at %q; files: %v", wantList, sortedFileKeys(files))
	}
	for _, want := range []string{"kind: List", "item-one", "item-two"} {
		if !strings.Contains(string(list), want) {
			t.Errorf("List wrapper content missing %q; got:\n%s", want, list)
		}
	}
	if !strings.Contains(string(list), "aggrega.ai/component-id: cfg") {
		t.Errorf("List wrapper carries no injected provenance label; got:\n%s", list)
	}
}

// TestHelmEngine_Sourced_NamelessDocFallbackIsDeterministic pins byte-identity of
// the path-based fallback: two renders of the same nameless-doc chart produce the
// same filenames with byte-identical content, the determinism the fallback exists
// to preserve.
func TestHelmEngine_Sourced_NamelessDocFallbackIsDeterministic(t *testing.T) {
	t.Parallel()
	first, err := helmcore.RenderLoadedChart(context.Background(), loadChart(t, "testdata/nameless-doc-chart"),
		map[string]any{}, nodeCtx("cfg", "testdata/nameless-doc-chart"), helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	second, err := helmcore.RenderLoadedChart(context.Background(), loadChart(t, "testdata/nameless-doc-chart"),
		map[string]any{}, nodeCtx("cfg", "testdata/nameless-doc-chart"), helmcore.NewHermeticValuesValidator(forbidFetch(t)))
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

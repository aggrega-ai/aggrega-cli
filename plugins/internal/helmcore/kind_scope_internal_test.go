// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// TestKindScopes_EveryBuiltInKindIsPlacedExactlyWhereItsNameCarriesTheNamespace
// renders one document of every kind the built-in list classifies, each writing no
// namespace, into a node declaring the namespace shop, and holds the two readers of
// the classification to one answer per kind: the placement writes shop into the
// document exactly where the names declaration keys its name by kind and shop, and
// leaves it without one exactly where the declaration keys its name by the kind
// alone — and both follow the list.
//
// The list is read as the oracle on purpose: what is under test is that the two
// readers cannot part over any kind in it, which a hand-picked sample would leave
// unexamined for every kind it did not pick.
func TestKindScopes_EveryBuiltInKindIsPlacedExactlyWhereItsNameCarriesTheNamespace(t *testing.T) {
	t.Parallel()

	kinds := make([]groupKind, 0, len(builtInKindScopes))
	for gk := range builtInKindScopes {
		kinds = append(kinds, gk)
	}
	sort.Slice(kinds, func(i, j int) bool {
		if kinds[i].group != kinds[j].group {
			return kinds[i].group < kinds[j].group
		}
		return kinds[i].kind < kinds[j].kind
	})

	// One name per document, so two groups serving one kind name never write one
	// resource identity.
	byName := make(map[string]groupKind, len(kinds))
	var docs strings.Builder
	for i, gk := range kinds {
		apiVersion := "v1"
		if gk.group != "" {
			apiVersion = gk.group + "/v1"
		}
		name := fmt.Sprintf("k%03d", i)
		byName[name] = gk
		fmt.Fprintf(&docs, "---\napiVersion: %s\nkind: %s\nmetadata:\n  name: %s\n", apiVersion, gk.kind, name)
	}
	ch := &chart.Chart{
		Metadata:  &chart.Metadata{APIVersion: "v2", Name: "kinds", Version: "0.1.0"},
		Templates: []*chart.File{{Name: "templates/kinds.yaml", Data: []byte(docs.String())}},
	}
	nodeCtx := rendercore.NodeRenderContext{
		NodeID:      "api",
		Annotations: map[string]string{rendercore.AnnotationNamespace: "shop"},
	}
	rendered, err := RenderChart(context.Background(), ch, map[string]any{}, nodeCtx,
		func(*chart.Chart, chartutil.Values) error { return nil })
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(rendered.Advisories) != 0 {
		t.Errorf("a render of built-in kinds alone raised advisories: %v", rendered.Advisories)
	}

	paths := make([]string, 0, len(rendered.Files))
	for p := range rendered.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	files := make([]rendercore.RenderedFile, 0, len(paths))
	for _, p := range paths {
		files = append(files, rendercore.RenderedFile{Path: p, Content: rendered.Files[p]})
	}
	declared, _, err := ReportEmitted(files, "shop")
	if err != nil {
		t.Fatalf("ReportEmitted: %v", err)
	}
	spaceOf := map[string]string{}
	for _, space := range declared.Keys() {
		names, _ := declared.Get(space)
		for _, n := range names {
			spaceOf[n] = space
		}
	}

	seen := 0
	for _, root := range emittedResources(files) {
		metadata := childOf(root, "metadata")
		name := scalarChildValue(metadata, "name")
		gk, known := byName[name]
		if !known {
			t.Errorf("an emitted document is named %q, which no document of this render was", name)
			continue
		}
		seen++
		placed := writtenNamespace(metadata) == "shop"
		space := spaceOf[name]
		wantNamespaced := builtInKindScopes[gk] == scopeNamespaced
		switch {
		case placed != wantNamespaced:
			t.Errorf("%s/%s: placed in shop = %v, but the list says namespaced = %v", gk.group, gk.kind, placed, wantNamespaced)
		case placed && space != resourceNameSpace(gk.kind, scopeNamespaced, "shop"):
			t.Errorf("%s/%s is placed in shop, and its name is declared in %q", gk.group, gk.kind, space)
		case !placed && space != resourceNameSpace(gk.kind, scopeCluster, ""):
			t.Errorf("%s/%s is left without a namespace, and its name is declared in %q", gk.group, gk.kind, space)
		}
	}
	if seen != len(kinds) {
		t.Fatalf("the render emitted %d of the %d documents written", seen, len(kinds))
	}
}

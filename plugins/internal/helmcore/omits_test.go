// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore_test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
	"helm.sh/helm/v3/pkg/chart"
)

// omitChart is a chart built in memory whose documents exercise every way an omit
// entry can or cannot match: two ConfigMaps of one name in two namespaces, a
// ConfigMap writing a namespace of its own, a ConfigMap writing none, a Secret, two
// ConfigMaps whose names differ but fold to one file identity, a Job carrying no
// name, and a CustomResourceDefinition shipped under crds/.
func omitChart() *chart.Chart {
	file := func(name, data string) *chart.File { return &chart.File{Name: name, Data: []byte(data)} }
	return &chart.Chart{
		Metadata: &chart.Metadata{APIVersion: "v2", Name: "omit", Version: "0.1.0"},
		Templates: []*chart.File{
			file("templates/settings.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\ndata:\n  level: info\n"),
			file("templates/extra.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: extra\n  namespace: other\ndata:\n  k: v\n"),
			file("templates/secret.yaml", "apiVersion: v1\nkind: Secret\nmetadata:\n  name: token\nstringData:\n  key: generated\n"),
			file("templates/shared.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: shared\n  namespace: ns1\n"+
				"---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: shared\n  namespace: ns2\n"),
			// "twin.a" and "twin-a" are two names that derive one file identity, so the
			// two documents take a tiebreaker each.
			file("templates/twins.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: twin.a\ndata:\n  which: dot\n"+
				"---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: twin-a\ndata:\n  which: dash\n"),
			file("templates/job.yaml", "apiVersion: batch/v1\nkind: Job\nmetadata:\n  generateName: migrate-\n"),
		},
		Files: []*chart.File{
			file("crds/widgets.yaml", "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\n"+
				"metadata:\n  name: widgets.example.com\nspec:\n  group: example.com\n  scope: Namespaced\n"+
				"  names:\n    kind: Widget\n    plural: widgets\n  versions: []\n"),
		},
	}
}

// omitEntries builds the resolved entries a render is handed, positioned in the
// order given.
func omitEntries(entries ...renderer.OmitEntry) []renderer.ResolvedOmit {
	out := make([]renderer.ResolvedOmit, len(entries))
	for i, e := range entries {
		out[i] = renderer.ResolvedOmit{Entry: e, Position: i}
	}
	return out
}

// renderOmitChart renders the in-memory chart with the given entries and,
// optionally, a declared namespace.
func renderOmitChart(t *testing.T, namespace string, entries []renderer.ResolvedOmit) helmcore.ChartRender {
	t.Helper()
	nc := nodeCtx("om", "in-memory")
	if namespace != "" {
		nc.Annotations = map[string]string{renderer.AnnotationNamespace: namespace}
	}
	nc.Omits = entries
	rendered, err := helmcore.RenderChart(context.Background(), omitChart(), map[string]any{}, nc,
		helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return rendered
}

// TestOmits_EntriesMatchTheDocumentsOwnWords is the matching contract, one row per
// shape: what an entry names, the counts the render reports, and the files it leaves
// out of what it returns. Every row is checked against the render handed no entries,
// so a row also proves that nothing it did not name moved.
func TestOmits_EntriesMatchTheDocumentsOwnWords(t *testing.T) {
	t.Parallel()

	baseline := map[string]helmcore.ChartRender{
		"":     renderOmitChart(t, "", nil),
		"apps": renderOmitChart(t, "apps", nil),
	}

	rows := []struct {
		name      string
		namespace string
		entries   []renderer.OmitEntry
		counts    []int
		leftOut   []string
	}{
		{
			name:    "kind and name select one document",
			entries: []renderer.OmitEntry{{Kind: "Secret", Name: "token"}},
			counts:  []int{1},
			leftOut: []string{"om-secret-token.yaml"},
		},
		{
			name:    "a kind the source renders under another name matches nothing",
			entries: []renderer.OmitEntry{{Kind: "Secret", Name: "settings"}},
			counts:  []int{0},
		},
		{
			name:    "a name the source renders under another kind matches nothing",
			entries: []renderer.OmitEntry{{Kind: "ConfigMap", Name: "token"}},
			counts:  []int{0},
		},
		{
			name:    "an entry naming the namespace a document writes matches it",
			entries: []renderer.OmitEntry{{Kind: "ConfigMap", Name: "extra", Namespace: "other"}},
			counts:  []int{1},
			leftOut: []string{"om-configmap-other-extra.yaml"},
		},
		{
			name:    "an entry naming another namespace does not",
			entries: []renderer.OmitEntry{{Kind: "ConfigMap", Name: "extra", Namespace: "elsewhere"}},
			counts:  []int{0},
		},
		{
			name:    "an entry naming no namespace matches whatever namespace the document is in",
			entries: []renderer.OmitEntry{{Kind: "ConfigMap", Name: "extra"}},
			counts:  []int{1},
			leftOut: []string{"om-configmap-other-extra.yaml"},
		},
		{
			name:    "a document left without a namespace is in none, never the cluster default",
			entries: []renderer.OmitEntry{{Kind: "ConfigMap", Name: "settings", Namespace: "default"}},
			counts:  []int{0},
		},
		{
			name:      "a document the render places in the declared namespace is in that namespace",
			namespace: "apps",
			entries:   []renderer.OmitEntry{{Kind: "ConfigMap", Name: "settings", Namespace: "apps"}},
			counts:    []int{1},
			leftOut:   []string{"om-configmap-apps-settings.yaml"},
		},
		{
			name:    "a crds/ document is matched like a templated one",
			entries: []renderer.OmitEntry{{Kind: "CustomResourceDefinition", Name: "widgets.example.com"}},
			counts:  []int{1},
			leftOut: []string{"om-customresourcedefinition-widgets-example-com.yaml"},
		},
		{
			name:    "one entry matching several documents counts each",
			entries: []renderer.OmitEntry{{Kind: "ConfigMap", Name: "shared"}},
			counts:  []int{2},
			leftOut: []string{"om-configmap-ns1-shared.yaml", "om-configmap-ns2-shared.yaml"},
		},
		{
			name:    "two entries matching one document each count it, and it is left out once",
			entries: []renderer.OmitEntry{{Kind: "Secret", Name: "token"}, {Kind: "Secret", Name: "token"}},
			counts:  []int{1, 1},
			leftOut: []string{"om-secret-token.yaml"},
		},
		{
			name:    "counts arrive in the order the entries were handed",
			entries: []renderer.OmitEntry{{Kind: "Secret", Name: "absent"}, {Kind: "Secret", Name: "token"}},
			counts:  []int{0, 1},
			leftOut: []string{"om-secret-token.yaml"},
		},
		{
			name:    "the same entries handed in the other order report the other order",
			entries: []renderer.OmitEntry{{Kind: "Secret", Name: "token"}, {Kind: "Secret", Name: "absent"}},
			counts:  []int{1, 0},
			leftOut: []string{"om-secret-token.yaml"},
		},
		{
			name:    "a document carrying no name matches no entry",
			entries: []renderer.OmitEntry{{Kind: "Job", Name: ""}},
			counts:  []int{0},
		},
		{
			// The two twins derive one file identity, so each takes a tiebreaker from
			// its place in the template. Leaving one out keeps the other at the name
			// it had, because the tiebreakers count the documents as rendered.
			name:    "two documents folding to one identity, one of them left out",
			entries: []renderer.OmitEntry{{Kind: "ConfigMap", Name: "twin.a"}},
			counts:  []int{1},
			leftOut: []string{"om-configmap-twin-a-0.yaml"},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got := renderOmitChart(t, row.namespace, omitEntries(row.entries...))
			if !slices.Equal(got.Omitted, row.counts) {
				t.Errorf("counts: got %v, want %v", got.Omitted, row.counts)
			}
			base := baseline[row.namespace]
			for _, name := range row.leftOut {
				if _, ok := base.Files[name]; !ok {
					t.Fatalf("fixture drift: the render handed no entries emits no %q; files: %v", name, sortedFileKeys(base.Files))
				}
				if _, ok := got.Files[name]; ok {
					t.Errorf("%q is still emitted; it should have been left out", name)
				}
			}
			// Everything not left out is emitted under the name and with the bytes it
			// has when no entry is handed at all.
			for name, body := range base.Files {
				if slices.Contains(row.leftOut, name) {
					continue
				}
				kept, ok := got.Files[name]
				if !ok {
					t.Errorf("%q was left out though no entry names it", name)
					continue
				}
				if !bytes.Equal(kept, body) {
					t.Errorf("%q changed when another document was left out:\nwith entries:\n%s\nwithout:\n%s", name, kept, body)
				}
			}
			if len(got.Files) != len(base.Files)-len(row.leftOut) {
				t.Errorf("file count: got %d, want %d (%v)", len(got.Files), len(base.Files)-len(row.leftOut), sortedFileKeys(got.Files))
			}
		})
	}
}

// TestOmits_RenderHandedNoEntriesReportsNoCounts pins that a render handed no
// entries reports no count list at all — not an empty one — so its output carries
// nothing it would not carry without the key.
func TestOmits_RenderHandedNoEntriesReportsNoCounts(t *testing.T) {
	t.Parallel()
	got := renderOmitChart(t, "", nil)
	if got.Omitted != nil {
		t.Errorf("a render handed no entries reported counts %v", got.Omitted)
	}
	if len(got.Files) == 0 {
		t.Fatal("the fixture chart rendered nothing, so this test proves nothing")
	}
}

// TestOmits_ALeftOutDocumentReachesNoDeclaredNameOrFact pins that a document left
// out is absent from what the provider reports alongside the files: the names it
// declares for the duplicate-name check and the facts it reports. Both are read from
// the files a render returns, so this holds the reading to the filtered set.
func TestOmits_ALeftOutDocumentReachesNoDeclaredNameOrFact(t *testing.T) {
	t.Parallel()
	got := renderOmitChart(t, "", omitEntries(renderer.OmitEntry{Kind: "Secret", Name: "token"}))
	declared, _, err := helmcore.ReportEmitted(filesOf(got), "default")
	if err != nil {
		t.Fatalf("report emitted: %v", err)
	}
	for space, names := range declared.All() {
		for _, name := range names {
			if strings.Contains(name, "token") {
				t.Errorf("key space %q still declares %q for a left-out document", space, name)
			}
		}
	}
	kept := renderOmitChart(t, "", nil)
	keptDeclared, _, err := helmcore.ReportEmitted(filesOf(kept), "default")
	if err != nil {
		t.Fatalf("report emitted: %v", err)
	}
	found := false
	for _, names := range keptDeclared.All() {
		for _, name := range names {
			if strings.Contains(name, "token") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("needle: the render handed no entries declares no name for the Secret, so the absence above proves nothing")
	}
}

// filesOf presents a chart render's files in the shape a provider returns them.
func filesOf(r helmcore.ChartRender) []renderer.RenderedFile {
	out := make([]renderer.RenderedFile, 0, len(r.Files))
	for _, name := range sortedFileKeys(r.Files) {
		out = append(out, renderer.RenderedFile{Path: name, Content: r.Files[name]})
	}
	return out
}

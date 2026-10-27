// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
	"helm.sh/helm/v4/pkg/chart/common"
	chart "helm.sh/helm/v4/pkg/chart/v2"
)

// omitChart is a chart built in memory whose documents exercise every way an omit
// entry can or cannot match: two ConfigMaps of one name in two namespaces, a
// ConfigMap writing a namespace of its own, a ConfigMap writing none, a Secret, two
// ConfigMaps whose names differ but fold to one file identity, a Job carrying no
// name, and a CustomResourceDefinition shipped under crds/.
func omitChart() *chart.Chart {
	file := func(name, data string) *common.File { return &common.File{Name: name, Data: []byte(data)} }
	return &chart.Chart{
		Metadata: &chart.Metadata{APIVersion: "v2", Name: "omit", Version: "0.1.0"},
		Templates: []*common.File{
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
		Files: []*common.File{
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

// subchartOmitChart is a chart built in memory whose subcharts ship one document at
// one name with differing bytes, the shape that halts a render with nothing left
// out: the subcharts first and second each ship the CustomResourceDefinition
// widgets.example.com under crds/, second's copy carrying a marker the other does
// not. first carries a nested subchart, inner, rendering the ConfigMap inner-config;
// the chart itself renders the ConfigMap settings; and a subchart third, rendering
// the ConfigMap third-config, is declared as a dependency its values disable.
func subchartOmitChart() *chart.Chart {
	file := func(name, data string) *common.File { return &common.File{Name: name, Data: []byte(data)} }
	crd := func(copy string) string {
		return "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\n" +
			"metadata:\n  name: widgets.example.com\n  annotations:\n    example.com/copy: " + copy + "\n" +
			"spec:\n  group: example.com\n  scope: Namespaced\n" +
			"  names:\n    kind: Widget\n    plural: widgets\n  versions: []\n"
	}
	inner := &chart.Chart{
		Metadata:  &chart.Metadata{APIVersion: "v2", Name: "inner", Version: "0.1.0"},
		Templates: []*common.File{file("templates/inner.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: inner-config\n")},
	}
	first := &chart.Chart{
		Metadata: &chart.Metadata{APIVersion: "v2", Name: "first", Version: "0.1.0"},
		Files:    []*common.File{file("crds/widgets.yaml", crd("first"))},
	}
	first.AddDependency(inner)
	second := &chart.Chart{
		Metadata: &chart.Metadata{APIVersion: "v2", Name: "second", Version: "0.1.0"},
		Files:    []*common.File{file("crds/widgets.yaml", crd("second"))},
	}
	third := &chart.Chart{
		Metadata:  &chart.Metadata{APIVersion: "v2", Name: "third", Version: "0.1.0"},
		Templates: []*common.File{file("templates/third.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: third-config\n")},
	}
	root := &chart.Chart{
		Metadata: &chart.Metadata{APIVersion: "v2", Name: "app", Version: "0.1.0",
			Dependencies: []*chart.Dependency{{Name: "third", Version: "0.1.0", Condition: "third.enabled"}}},
		Values:    map[string]any{"third": map[string]any{"enabled": false}},
		Templates: []*common.File{file("templates/settings.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n")},
	}
	root.AddDependency(first, second, third)
	return root
}

// renderSubchartOmitChart renders the subchart chart with the given entries.
func renderSubchartOmitChart(t *testing.T, entries []renderer.ResolvedOmit) (helmcore.ChartRender, error) {
	t.Helper()
	nc := nodeCtx("om", "in-memory")
	nc.Omits = entries
	return helmcore.RenderChart(context.Background(), subchartOmitChart(), map[string]any{}, nc,
		helmcore.NewHermeticValuesValidator(forbidFetch(t)))
}

// subchartCRDFile is the file the subcharts' CustomResourceDefinition is emitted as.
const subchartCRDFile = "om-customresourcedefinition-widgets-example-com.yaml"

// TestOmits_ASubchartNamesWhereTheDocumentCameFrom is the subchart half of the
// matching contract, one row per shape. It opens on the render the subchart field
// exists for: two subcharts shipping one document at one name with differing bytes
// halt the render when nothing is left out. An entry naming one subchart then leaves
// out that subchart's copy and keeps the other, byte for byte the copy that subchart
// ships; an entry naming none leaves out both; a document the chart renders itself
// came from no subchart; and a nested subchart is named by its path from the
// outermost, a document it renders matching neither its parent's name nor its own
// name alone.
func TestOmits_ASubchartNamesWhereTheDocumentCameFrom(t *testing.T) {
	t.Parallel()

	if _, err := renderSubchartOmitChart(t, nil); !errors.Is(err, wasm.ErrPluginOutputNameCollision()) {
		t.Fatalf("the chart rendered with nothing left out gave %v; want the halt on two documents at one name, "+
			"which is the render these rows leave one copy out of", err)
	}

	crd := renderer.OmitEntry{Kind: "CustomResourceDefinition", Name: "widgets.example.com"}
	from := func(e renderer.OmitEntry, subchart string) renderer.OmitEntry {
		e.Subchart = subchart
		return e
	}
	for _, tc := range []struct {
		name    string
		entries []renderer.OmitEntry
		counts  []int
		// copy is which subchart's copy of the definition is kept, "" for none.
		copy string
		// files are further files the render must emit (true) or not (false).
		files map[string]bool
	}{
		{
			name:    "naming second leaves second's copy out and keeps first's",
			entries: []renderer.OmitEntry{from(crd, "second")},
			counts:  []int{1},
			copy:    "first",
		},
		{
			name:    "naming first leaves first's copy out and keeps second's",
			entries: []renderer.OmitEntry{from(crd, "first")},
			counts:  []int{1},
			copy:    "second",
		},
		{
			name:    "an entry naming no subchart leaves out both copies",
			entries: []renderer.OmitEntry{crd},
			counts:  []int{2},
			copy:    "",
		},
		{
			name: "a document the chart renders itself came from no subchart",
			entries: []renderer.OmitEntry{from(crd, "second"),
				{Kind: "ConfigMap", Name: "settings", Subchart: "first"}},
			counts: []int{1, 0},
			copy:   "first",
			files:  map[string]bool{"om-configmap-settings.yaml": true},
		},
		{
			name: "a nested subchart's document does not match its parent's name",
			entries: []renderer.OmitEntry{from(crd, "second"),
				{Kind: "ConfigMap", Name: "inner-config", Subchart: "first"}},
			counts: []int{1, 0},
			copy:   "first",
			files:  map[string]bool{"om-configmap-inner-config.yaml": true},
		},
		{
			name: "a nested subchart is named by its path from the outermost",
			entries: []renderer.OmitEntry{from(crd, "second"),
				{Kind: "ConfigMap", Name: "inner-config", Subchart: "first/inner"}},
			counts: []int{1, 1},
			copy:   "first",
			files:  map[string]bool{"om-configmap-inner-config.yaml": false, "om-configmap-settings.yaml": true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := renderSubchartOmitChart(t, omitEntries(tc.entries...))
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !slices.Equal(got.Omitted, tc.counts) {
				t.Errorf("counts: got %v, want %v", got.Omitted, tc.counts)
			}
			kept, emitted := got.Files[subchartCRDFile]
			switch {
			case tc.copy == "" && emitted:
				t.Errorf("a copy of the definition is still emitted:\n%s", kept)
			case tc.copy != "" && !emitted:
				t.Errorf("no copy of the definition is emitted; want %s's; files: %v", tc.copy, sortedFileKeys(got.Files))
			case tc.copy != "" && !strings.Contains(string(kept), "example.com/copy: "+tc.copy+"\n"):
				t.Errorf("the definition kept is not %s's copy:\n%s", tc.copy, kept)
			}
			for name, want := range tc.files {
				if _, ok := got.Files[name]; ok != want {
					t.Errorf("%q emitted = %v, want %v; files: %v", name, ok, want, sortedFileKeys(got.Files))
				}
			}
			if _, ok := got.Files["om-configmap-third-config.yaml"]; ok {
				t.Error("the subchart the values disable rendered anyway, so the rows below read a render the fixture does not describe")
			}
		})
	}
}

// TestOmits_AnEntryNamingASubchartTheChartDoesNotRenderHalts holds the refusal of an
// entry naming a subchart the chart does not render — a name it does not have, and
// one its values disable — under the condition for something a plugin could not
// resolve to exactly one value, naming the entry, the subchart it names and the
// subcharts the chart renders. Every entry is checked before any document is
// matched, so the refusal comes though an earlier entry would have matched. A
// subchart produced from a value declared sensitive is shown as the placeholder.
func TestOmits_AnEntryNamingASubchartTheChartDoesNotRenderHalts(t *testing.T) {
	t.Parallel()

	const plaintext = "subchart-withheld-plaintext"
	keep := renderer.OmitEntry{Kind: "CustomResourceDefinition", Name: "widgets.example.com", Subchart: "second"}
	for _, tc := range []struct {
		name  string
		entry renderer.ResolvedOmit
		says  []string
	}{
		{
			name:  "a name the chart does not have",
			entry: renderer.ResolvedOmit{Entry: renderer.OmitEntry{Kind: "ConfigMap", Name: "settings", Subchart: "nosuch"}},
			says:  []string{`an omits entry (kind "ConfigMap", name "settings") names subchart "nosuch"`},
		},
		{
			name:  "a subchart the values disable",
			entry: renderer.ResolvedOmit{Entry: renderer.OmitEntry{Kind: "ConfigMap", Name: "third-config", Subchart: "third"}},
			says:  []string{`names subchart "third"`},
		},
		{
			name:  "a nested subchart named by its own name alone",
			entry: renderer.ResolvedOmit{Entry: renderer.OmitEntry{Kind: "ConfigMap", Name: "inner-config", Subchart: "inner"}},
			says:  []string{`names subchart "inner"`},
		},
		{
			name: "a subchart produced from a value declared sensitive",
			entry: renderer.ResolvedOmit{Entry: renderer.OmitEntry{Kind: "ConfigMap", Name: "settings", Subchart: plaintext},
				Withheld: renderer.OmitFieldsWithheld{Subchart: true}},
			says: []string{"names subchart " + redact.Placeholder()},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entries := []renderer.ResolvedOmit{{Entry: keep, Position: 0}, tc.entry}
			entries[1].Position = 1
			got, err := renderSubchartOmitChart(t, entries)
			if !errors.Is(err, wasm.ErrPluginRequiredInputUnresolved()) {
				t.Fatalf("got files %v and error %v; want the refusal of an entry naming a subchart the chart does not render",
					sortedFileKeys(got.Files), err)
			}
			for _, want := range append(tc.says,
				"the chart renders no subchart of that name",
				`the subcharts it renders are "first", "first/inner", "second"`) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not carry %q:\n%v", want, err)
				}
			}
			if strings.Contains(err.Error(), plaintext) {
				t.Errorf("the refusal quotes a subchart produced from a value declared sensitive:\n%v", err)
			}
		})
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

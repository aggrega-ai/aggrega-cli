// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchartcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegmenttest"
)

// writeNamedChart writes a chart whose manifest declares manifestName and whose
// one template carries three independent facts: a marker literal baked into the
// template body, the secret read from .Values, and metadata.name read from the
// chart's own name. Three rather than one, because a single fact cannot separate
// "the wrong chart rendered" from "the wrong values arrived".
//
// The vendored default for the secret is a string no real value would be, so a
// sub-chart whose values never reach it renders that string instead of failing.
// A chart with unaddressable values renders its own defaults and reports
// success, so an assertion that only checks the chart rendered would pass on
// values going nowhere.
func writeNamedChart(t *testing.T, dir, manifestName, marker string) {
	t.Helper()
	writeFileMkdir(t, filepath.Join(dir, chartMetadataFileName),
		"apiVersion: v2\nname: "+manifestName+"\nversion: 0.1.0\n")
	writeFileMkdir(t, filepath.Join(dir, "values.yaml"), "secret: \"NO-VALUES-REACHED-THIS-SUBCHART\"\n")
	writeFileMkdir(t, filepath.Join(dir, "templates", "cm.yaml"),
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: \"{{ .Chart.Name }}\"\ndata:\n"+
			"  source: \""+marker+"\"\n  secret: \"{{ .Values.secret }}\"\n")
}

// twoSubChartUmbrella assembles an umbrella over two sourced contributors and
// places each through the same render core the provider guest runs, returning the
// chart root plus the manifest name each placed sub-chart actually carries on
// disk. rawPlace skips the render core and copies the source bytes instead, which
// is the negative control: it reproduces what the tree emitted before the
// umbrella owned the name.
func twoSubChartUmbrella(t *testing.T, srcA, idA, srcB, idB string, rawPlace bool) (root, placedA, placedB string, umbrella map[string][]byte) {
	t.Helper()
	files, err := buildUmbrella("boundary", "app", []renderer.SubChartInfo{
		{InstanceID: idA, Version: "9.9.9", Values: map[string]any{"secret": idA + "-secret"}, Source: srcA},
		{InstanceID: idB, Version: "9.9.9", Values: map[string]any{"secret": idB + "-secret"}, Source: srcB},
	}, "", 2)
	if err != nil {
		t.Fatalf("assemble umbrella: %v", err)
	}
	root = t.TempDir()
	for name, body := range files {
		writeFileMkdir(t, filepath.Join(root, name), string(body))
	}
	place := func(src, id string) string {
		if rawPlace {
			copyTree(t, src, filepath.Join(root, "charts", id))
		} else {
			for path, body := range placeSourcedChart(t, src, id) {
				writeFileMkdir(t, filepath.Join(root, "charts", id, path), string(body))
			}
		}
		got, _, rerr := readChartMeta(filepath.Join(root, "charts", id))
		if rerr != nil {
			t.Fatalf("reading back the placed manifest for %q: %v", id, rerr)
		}
		return got
	}
	return root, place(srcA, idA), place(srcB, idB), files
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		data, derr := os.ReadFile(p)
		if derr != nil {
			return derr
		}
		writeFileMkdir(t, filepath.Join(dst, rel), string(data))
		return nil
	}); err != nil {
		t.Fatalf("copying %q: %v", src, err)
	}
}

// TestSourcedSubChartsSharingOneAuthoredName_BothRenderTheirOwnContent is the
// defect this ownership exists to remove, asserted at install time through the
// same dependency pipeline `helm template` runs.
//
// Two components vendor DIFFERENT charts whose manifests declare the SAME name.
// Because a chart's name is what Helm binds a dependency by, both of the
// umbrella's dependencies used to resolve to whichever of the two loaded first:
// one chart's templates rendered under both aliases and the other's under none,
// while both value blocks still arrived — so nothing downstream read as wrong.
//
// The fixture cannot degrade into a no-collision pass: it halts if the two
// SOURCE manifests do not share a name.
func TestSourcedSubChartsSharingOneAuthoredName_BothRenderTheirOwnContent(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dbSrc, cacheSrc := filepath.Join(base, "dbsrc"), filepath.Join(base, "cachesrc")
	writeNamedChart(t, dbSrc, "widget", "db-chart")
	writeNamedChart(t, cacheSrc, "widget", "cache-chart")
	assertSourcesShareAName(t, dbSrc, cacheSrc)

	root, placedDB, placedCache, _ := twoSubChartUmbrella(t, dbSrc, "db", cacheSrc, "cache", false)
	if placedDB == placedCache {
		t.Fatalf("the two placed manifests still share the name %q, so the umbrella does not own it", placedDB)
	}

	rendered := renderThroughDependencyPipeline(t, root)
	// Each contributor's own template body renders exactly once.
	if got := strings.Count(rendered, `source: "db-chart"`); got != 1 {
		t.Errorf("the db contributor's own template rendered %d times, want 1:\n%s", got, rendered)
	}
	if got := strings.Count(rendered, `source: "cache-chart"`); got != 1 {
		t.Errorf("the cache contributor's own template rendered %d times, want 1:\n%s", got, rendered)
	}
	// Each contributor's own mapped value arrives, at its own sub-chart.
	assertMappedValuesArrive(t, rendered, "db", "cache")
}

// TestSameSourceChartVendoredTwice_EachCopyKeepsItsOwnValues is the case a
// refusal keyed on the authored name would have broken, and the reason the
// collision is removed rather than refused: two instances of ONE source chart
// share that chart's name by construction, and the provider documents them as
// coexisting with their own values.
func TestSameSourceChartVendoredTwice_EachCopyKeepsItsOwnValues(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "shared")
	writeNamedChart(t, src, "widget", "one-shared-chart")

	root, placedDB, placedCache, _ := twoSubChartUmbrella(t, src, "db", src, "cache", false)
	if placedDB == placedCache {
		t.Fatalf("two instances of one source chart still share the name %q", placedDB)
	}

	rendered := renderThroughDependencyPipeline(t, root)
	// One shared chart body, rendered once per instance.
	if got := strings.Count(rendered, `source: "one-shared-chart"`); got != 2 {
		t.Errorf("the shared chart rendered %d times, want 2 (once per instance):\n%s", got, rendered)
	}
	assertMappedValuesArrive(t, rendered, "db", "cache")
}

// TestSourcedSubChartsWithDistinctAuthoredNames_StillRenderEachOwnContent is the
// control: two charts whose authored names already differ were never affected,
// and must not become affected.
func TestSourcedSubChartsWithDistinctAuthoredNames_StillRenderEachOwnContent(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dbSrc, cacheSrc := filepath.Join(base, "dbsrc"), filepath.Join(base, "cachesrc")
	writeNamedChart(t, dbSrc, "gadget", "db-chart")
	writeNamedChart(t, cacheSrc, "widget", "cache-chart")

	root, _, _, _ := twoSubChartUmbrella(t, dbSrc, "db", cacheSrc, "cache", false)
	rendered := renderThroughDependencyPipeline(t, root)
	if got := strings.Count(rendered, `source: "db-chart"`); got != 1 {
		t.Errorf("db contributor rendered %d times, want 1:\n%s", got, rendered)
	}
	if got := strings.Count(rendered, `source: "cache-chart"`); got != 1 {
		t.Errorf("cache contributor rendered %d times, want 1:\n%s", got, rendered)
	}
	assertMappedValuesArrive(t, rendered, "db", "cache")
}

// TestPlacingASourcedChartRawLeavesItsValuesUnaddressable is the negative
// control that keeps every assertion above from being vacuous.
//
// It places the sub-charts by copying the source bytes — skipping the name the
// umbrella owns — and asserts the opposite outcome: the dependency then names a
// chart that is not there, so no alias binds, no mapped value arrives, and each
// sub-chart renders its own vendored default. Without this row, a positive
// assertion could pass on an arrangement where values reach nothing, because a
// chart with unaddressable values renders defaults and reports success.
func TestPlacingASourcedChartRawLeavesItsValuesUnaddressable(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dbSrc, cacheSrc := filepath.Join(base, "dbsrc"), filepath.Join(base, "cachesrc")
	writeNamedChart(t, dbSrc, "widget", "db-chart")
	writeNamedChart(t, cacheSrc, "widget", "cache-chart")

	root, placedDB, placedCache, _ := twoSubChartUmbrella(t, dbSrc, "db", cacheSrc, "cache", true)
	if placedDB != placedCache {
		t.Fatalf("the raw control must leave the authored name in place on both (%q, %q)", placedDB, placedCache)
	}

	rendered := renderThroughDependencyPipeline(t, root)
	for _, id := range []string{"db", "cache"} {
		if strings.Contains(rendered, `secret: "`+id+`-secret"`) {
			t.Errorf("the raw control propagated %q's mapped value, so the positive rows prove nothing:\n%s", id, rendered)
		}
	}
	if !strings.Contains(rendered, "NO-VALUES-REACHED-THIS-SUBCHART") {
		t.Errorf("the raw control should render the vendored default, showing values reached nothing:\n%s", rendered)
	}
}

// TestSourcedSubChartDependencyEntryAgreesWithThePlacedManifest pins the two
// agreements the assembled umbrella needs, at install time and on the bytes.
//
// 🔴 They are TWO agreements rather than one three-way identity, and an instance
// id carrying a character a chart name refuses is what separates them: the
// dependency's `name` equals the PLACED MANIFEST's name, which is encoded; the
// dependency's `repository` equals the sub-chart's DIRECTORY, which carries the
// instance id as authored. The two are the same word only for an id the name
// alphabet already admits, so asserting a single identity across all three would
// be asserting something false for a punctuated id.
func TestSourcedSubChartDependencyEntryAgreesWithThePlacedManifest(t *testing.T) {
	t.Parallel()
	for _, instanceID := range []string{"plain", "data.main-db"} {
		t.Run(instanceID, func(t *testing.T) {
			t.Parallel()
			src := filepath.Join(t.TempDir(), "src")
			writeNamedChart(t, src, "postgres", "the-chart")

			files, err := buildUmbrella("boundary", "app", []renderer.SubChartInfo{{
				InstanceID: instanceID,
				Version:    "9.9.9",
				Values:     map[string]any{"secret": "s3cr3t"},
				Source:     src,
			}}, "", 2)
			if err != nil {
				t.Fatalf("assemble umbrella: %v", err)
			}

			root := t.TempDir()
			for name, body := range files {
				writeFileMkdir(t, filepath.Join(root, name), string(body))
			}
			dir := filepath.Join(root, "charts", instanceID)
			for path, body := range placeSourcedChart(t, src, instanceID) {
				writeFileMkdir(t, filepath.Join(dir, path), string(body))
			}

			placedName, _, err := readChartMeta(dir)
			if err != nil {
				t.Fatalf("reading the placed manifest: %v", err)
			}
			wantName := helmNameEncoding.Segment(instanceID)
			if placedName != wantName {
				t.Errorf("placed manifest name = %q; want the encoded identity %q", placedName, wantName)
			}
			if placedName == "postgres" {
				t.Errorf("the placed manifest still carries the name its author wrote")
			}

			umbrella := string(files[chartMetadataFileName])
			// Agreement 1 — the dependency's name IS the placed manifest's name.
			if !strings.Contains(umbrella, "- name: "+placedName+"\n") {
				t.Errorf("no dependency names the placed manifest %q:\n%s", placedName, umbrella)
			}
			// Agreement 2 — the dependency's repository IS the directory on disk.
			if !strings.Contains(umbrella, "repository: file://charts/"+instanceID+"\n") {
				t.Errorf("the repository must name the sub-chart's own directory %q:\n%s", instanceID, umbrella)
			}
			if _, serr := os.Stat(filepath.Join(root, "charts", instanceID, chartMetadataFileName)); serr != nil {
				t.Errorf("the directory the repository names does not hold a chart: %v", serr)
			}

			// And the whole thing loads with the sub-chart present and rendering.
			rendered := renderThroughDependencyPipeline(t, root)
			if !strings.Contains(rendered, `source: "the-chart"`) {
				t.Errorf("the sub-chart did not render:\n%s", rendered)
			}
			if !strings.Contains(rendered, `secret: "s3cr3t"`) {
				t.Errorf("the umbrella's mapped value did not reach the sub-chart, so the alias did not bind:\n%s", rendered)
			}
			if !strings.Contains(rendered, `name: "`+wantName+`"`) {
				t.Errorf("a template reading the chart's own name should read %q:\n%s", wantName, rendered)
			}
		})
	}
}

// TestPlacedManifestRewriteMovesOnlyTheName is the round-trip proof. A chart
// manifest may carry arbitrary other keys, comments, quoting styles and blank
// lines, and the promise the provider's page now makes is "verbatim except its
// own name" — so every byte outside that one scalar must survive.
//
// The assertion is a whole-document comparison against the source with only the
// name substituted, which fails on any incidental reformatting: a re-serialized
// document would drop the comments and normalize the quoting, and a span edit
// that over-reached would take a neighbouring byte with it.
func TestPlacedManifestRewriteMovesOnlyTheName(t *testing.T) {
	t.Parallel()
	const manifest = `# a chart an author wrote, with their own comments
apiVersion: v2
name: acquired-upstream-chart   # the name they chose
version: 2.4.6
description: >-
  a folded description
  over two lines
keywords:
  - one
  - two
maintainers:
  - name: someone            # a nested key that is also called name
    email: 'someone@example.test'
annotations:
  "quoted.key/here": "value"

# a trailing comment after a blank line
`
	proj := &renderer.SourceProjection{Files: []renderer.SourceFile{
		{Path: chartMetadataFileName, Content: []byte(manifest)},
	}}
	placed, err := RenderSourcedChartCore(proj, "web.api", 2)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	got := string(placed[chartMetadataFileName])

	want := strings.Replace(manifest,
		"name: acquired-upstream-chart   # the name they chose",
		"name: "+helmNameEncoding.Segment("web.api")+"   # the name they chose", 1)
	if got != want {
		t.Errorf("the rewrite moved something other than the top-level name.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// The nested maintainers[0].name must be untouched — a naive replacement of
	// the first "name:" line, or a document-wide substitution, would take it.
	if !strings.Contains(got, "- name: someone            # a nested key that is also called name") {
		t.Errorf("a nested key also called name was disturbed:\n%s", got)
	}
	if strings.Contains(got, "acquired-upstream-chart") {
		t.Errorf("the authored chart name still appears in the placed manifest:\n%s", got)
	}
}

// TestPlacedSubChartNameIsInjectiveOverVendoredManifests proves the property
// this ownership actually depends on, which the two committed injectivity tests
// do not cover: they run the encoder over the shared corpus directly and read no
// vendored manifest at all, so they say nothing about the name a PLACED chart
// ends up carrying.
//
// Here every identity in the corpus is placed over one and the same vendored
// chart — the worst case, since every source manifest declares one name — and
// the assertion is that no two identities reach one placed name. A collision is
// reported with both identities rather than only a count.
func TestPlacedSubChartNameIsInjectiveOverVendoredManifests(t *testing.T) {
	t.Parallel()
	const shared = "apiVersion: v2\nname: one-name-for-every-instance\nversion: 0.1.0\n"
	corpus := namesegmenttest.Corpus()
	seen := make(map[string]string, len(corpus))
	for _, identity := range corpus {
		proj := &renderer.SourceProjection{Files: []renderer.SourceFile{
			{Path: chartMetadataFileName, Content: []byte(shared)},
		}}
		placed, err := RenderSourcedChartCore(proj, identity, 2)
		if err != nil {
			t.Fatalf("placing identity %q: %v", identity, err)
		}
		name, _, err := ReadChartMetaBytes(placed[chartMetadataFileName], identity)
		if err != nil {
			t.Fatalf("reading back the placed manifest for %q: %v", identity, err)
		}
		if name == "one-name-for-every-instance" {
			t.Fatalf("identity %q left the vendored name in place", identity)
		}
		if prior, clash := seen[name]; clash {
			t.Fatalf("identities %q and %q both reach the placed chart name %q", prior, identity, name)
		}
		seen[name] = identity
	}
	if len(seen) != len(corpus) {
		t.Fatalf("placed %d names from %d identities; each must reach its own name", len(seen), len(corpus))
	}
}

// TestPlacedManifestRewriteIsDeterministic pins the Deterministic Output
// invariant over the rewrite: identical input produces identical bytes.
func TestPlacedManifestRewriteIsDeterministic(t *testing.T) {
	t.Parallel()
	const manifest = "apiVersion: v2\nname: upstream\nversion: 1.2.3\n# trailing\n"
	var first string
	for i := 0; i < 8; i++ {
		proj := &renderer.SourceProjection{Files: []renderer.SourceFile{
			{Path: chartMetadataFileName, Content: []byte(manifest)},
		}}
		placed, err := RenderSourcedChartCore(proj, "svc.one", 2)
		if err != nil {
			t.Fatalf("render on run %d: %v", i, err)
		}
		got := string(placed[chartMetadataFileName])
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("run %d produced different bytes:\n%q\nvs\n%q", i, got, first)
		}
	}
}

// TestSourcedRenderHaltsOnAManifestItCannotOwn pins that a vendored manifest the
// render core cannot take the name of halts loudly rather than placing a chart no
// dependency can bind. Each row carries one fault, asserted against the message
// that fault already had, so the rows cannot pass on each other's refusal.
func TestSourcedRenderHaltsOnAManifestItCannotOwn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, manifest, wantMessage string
	}{
		{"empty name", "apiVersion: v2\nversion: 0.1.0\n", "has empty name"},
		{"empty version", "apiVersion: v2\nname: upstream\n", "has empty version"},
		{"multiple documents", "apiVersion: v2\nname: a\nversion: 1\n---\nname: b\n", "must be a single document"},
		{"unparseable", "::: not yaml :::", "parse Chart.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proj := &renderer.SourceProjection{Files: []renderer.SourceFile{
				{Path: chartMetadataFileName, Content: []byte(tc.manifest)},
			}}
			_, err := RenderSourcedChartCore(proj, "web", 2)
			if err == nil {
				t.Fatalf("a manifest with %s should halt", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantMessage) {
				t.Errorf("the halt should name the %s fault (%q); got %v", tc.name, tc.wantMessage, err)
			}
		})
	}
}

// TestSourcedRenderWithoutAManifestPassesEverythingThrough pins that a
// projection carrying no chart manifest is returned unchanged. This core does not
// require one: the loud guardrail for a chart source genuinely missing its
// metadata is the sourced-listing validation that runs before it, and requiring
// the file here would turn a chart-shaped filename into a precondition of the
// render path.
func TestSourcedRenderWithoutAManifestPassesEverythingThrough(t *testing.T) {
	t.Parallel()
	proj := &renderer.SourceProjection{Files: []renderer.SourceFile{
		{Path: "templates/only.yaml", Content: []byte("kind: ConfigMap\n")},
	}}
	placed, err := RenderSourcedChartCore(proj, "web", 2)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(placed) != 1 || string(placed["templates/only.yaml"]) != "kind: ConfigMap\n" {
		t.Errorf("a projection with no manifest should pass through unchanged; got %v", placed)
	}
}

// assertSourcesShareAName halts when the two SOURCE manifests do not declare one
// name, so a collision fixture cannot silently degrade into a no-collision pass
// that reads exactly like a real one.
func assertSourcesShareAName(t *testing.T, dirs ...string) {
	t.Helper()
	var first string
	for i, d := range dirs {
		name, _, err := readChartMeta(d)
		if err != nil {
			t.Fatalf("reading source manifest %q: %v", d, err)
		}
		if i == 0 {
			first = name
			continue
		}
		if name != first {
			t.Fatalf("fixture is vacuous: source manifests declare %q and %q rather than one shared name", first, name)
		}
	}
}

// assertMappedValuesArrive asserts each instance's own mapped value reached its
// own sub-chart, and that no sub-chart fell back to the vendored default. The
// default check is what makes this non-vacuous: an unbound alias renders the
// vendored default and reports success.
func assertMappedValuesArrive(t *testing.T, rendered string, instanceIDs ...string) {
	t.Helper()
	for _, id := range instanceIDs {
		want := `secret: "` + id + `-secret"`
		if got := strings.Count(rendered, want); got != 1 {
			t.Errorf("%q's own mapped value arrived %d times, want 1:\n%s", id, got, rendered)
		}
	}
	if strings.Contains(rendered, "NO-VALUES-REACHED-THIS-SUBCHART") {
		t.Errorf("a sub-chart rendered its vendored default, so the umbrella's values did not reach it:\n%s", rendered)
	}
}

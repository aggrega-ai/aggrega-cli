// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// Where a resolution took each reference from, on the answer that reports it.
//
// A build that resolves its dependencies first answers with that resolution's
// report, and every entry of the report names where this run took the reference
// from: fetched from a registry, reused from the cache, found on the local
// filesystem, served from inside the binary, or the Release's own Aggregate and
// Manifest. The fact describes one run on one machine and the lockfile does not
// record it, so the answer is the only place a caller can read it.
//
// Two instruments, because the two halves fail differently.
//
//   - The PROJECTION is asserted over a resolution planted with members on every
//     axis, carrying every source the vocabulary names, with each expected word
//     spelled out as the wire token a client reads. That reaches the plugins axis,
//     which the workspace below does not fill, and it fails on a renamed word.
//   - The WIRING is asserted end to end: a build asked to fill, over a workspace
//     whose resolution reaches both an entity and a library, against a separate
//     resolution of a separate fresh copy read field by field. Its expectation is
//     never taken from the answer under test, nor through the projection that
//     built it.
//
// Both read the answer off its encoding by the key names a client binds to,
// rather than through the producer's own types, so a key respelled on the
// producer cannot carry the expectation along with it.

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/pullops"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// answeredEntry is one entry of an encoded resolution report, read by key name.
type answeredEntry struct {
	axis, uri, source string
	// carried is whether the entry has a source key at all, which a decoded empty
	// string cannot tell apart from an absent one.
	carried bool
}

// reportSourceEntries reads every entry of an encoded resolution report, in the
// collection it is answered in, with the source it names.
func reportSourceEntries(t *testing.T, report json.RawMessage) []answeredEntry {
	t.Helper()
	var collections struct {
		Entities  []map[string]json.RawMessage `json:"entities"`
		Libraries []map[string]json.RawMessage `json:"libraries"`
		Plugins   []map[string]json.RawMessage `json:"plugins"`
	}
	if err := json.Unmarshal(report, &collections); err != nil {
		t.Fatalf("decode the resolution report: %v\nreport: %s", err, report)
	}
	var out []answeredEntry
	for _, section := range []struct {
		axis    string
		entries []map[string]json.RawMessage
	}{
		{"entities", collections.Entities},
		{"libraries", collections.Libraries},
		{"plugins", collections.Plugins},
	} {
		for _, entry := range section.entries {
			e := answeredEntry{axis: section.axis}
			if err := json.Unmarshal(entry["uri"], &e.uri); err != nil {
				t.Fatalf("an entry of the report's %s carries no readable uri: %v\nreport: %s",
					section.axis, err, report)
			}
			if raw, carried := entry["source"]; carried {
				e.carried = true
				if err := json.Unmarshal(raw, &e.source); err != nil {
					t.Fatalf("the report's %s entry %s carries a source that is not a string: %v\nreport: %s",
						section.axis, e.uri, err, report)
				}
			}
			out = append(out, e)
		}
	}
	return out
}

// plantedSource is one entry planted on the resolution under test, with the word
// its answered entry must name.
type plantedSource struct {
	axis, uri string
	// word is empty where the answered entry must carry no source key at all.
	word string
}

// TestResolutionReportNamesTheSourceOfEveryEntry requires each entry the report
// projects, on every axis, to name the source its own lookup recorded, in the word
// the command line's pull detail prints for it.
//
// The words are spelled out rather than read from the vocabulary's constants, and
// that duplication is DELIBERATE: they are the wire tokens a client prints, so the
// assertion has to fail if one moves. Reading the constants would compare the
// implementation against itself and could not witness a rename.
//
// Each word is planted on an axis whose lookup can stamp it, and beside them an
// entry whose lookup recorded nothing: its answered entry carries no source key,
// which is what the wire contract says for that case, as distinct from a key
// present and empty.
func TestResolutionReportNamesTheSourceOfEveryEntry(t *testing.T) {
	t.Parallel()

	var cl closure.Closure
	var planted []plantedSource
	ref := func(axis, uri, resolutionPath, word string) closure.ResolvedRef {
		planted = append(planted, plantedSource{axis: axis, uri: uri, word: word})
		return closure.ResolvedRef{URI: uri, ResolvedVersion: "1.0.0", ResolutionPath: resolutionPath}
	}
	cl.Entities = []closure.ResolvedRef{
		ref("entities", "planted/fetched", closure.ResolutionPathRegistry, "fetched-fresh"),
		ref("entities", "planted/cached", closure.ResolutionPathCache, "cache-reuse"),
		ref("entities", "planted/local", closure.ResolutionPathCatalog, "local"),
		ref("entities", "planted/release-own", closure.ResolutionPathExternal, "external"),
		ref("entities", "planted/unrecorded", "", ""),
	}
	cl.Plugins = []closure.ResolvedRef{
		ref("plugins", "planted/plugin-in-binary", closure.ResolutionPathEmbedded, "in-binary"),
		ref("plugins", "planted/plugin-fetched", closure.ResolutionPathRegistry, "fetched-fresh"),
		ref("plugins", "planted/plugin-local", closure.ResolutionPathCatalog, "local"),
	}
	// One member on every declared library axis, so an axis the projection stopped
	// carrying is missed by name rather than hidden in an empty collection.
	libraryTags := []struct{ resolutionPath, word string }{
		{closure.ResolutionPathCatalog, "local"},
		{closure.ResolutionPathCache, "cache-reuse"},
		{closure.ResolutionPathRegistry, "fetched-fresh"},
	}
	axes := closure.LibraryAxes()
	if len(axes) == 0 {
		t.Fatal("the closure declares no library axis, so the libraries collection would be asserted empty " +
			"against empty")
	}
	for i, axis := range axes {
		tag := libraryTags[i%len(libraryTags)]
		uri := "planted/" + axis.Axis
		axis.SetLibraries(&cl, []closure.ResolvedLibrary{{
			URI: uri, ResolvedVersion: "1.0.0", ResolutionPath: tag.resolutionPath,
		}})
		planted = append(planted, plantedSource{axis: "libraries", uri: uri, word: tag.word})
	}

	body, err := json.Marshal(dependencyResolutionReportOf(pullops.Result{Closure: cl}, ""))
	if err != nil {
		t.Fatalf("encode the report: %v", err)
	}
	answered := map[string]answeredEntry{}
	for _, e := range reportSourceEntries(t, body) {
		answered[e.axis+" "+e.uri] = e
	}

	for _, p := range planted {
		got, listed := answered[p.axis+" "+p.uri]
		switch {
		case !listed:
			t.Errorf("the report lists no %s entry %s, so where the run took it from cannot be read at all\n"+
				"report: %s", p.axis, p.uri, body)
		case p.word == "" && got.carried:
			t.Errorf("the %s entry %s carries a source key (%q) although its lookup recorded none; the wire "+
				"contract omits the key for that case", p.axis, p.uri, got.source)
		case p.word != "" && !got.carried:
			t.Errorf("the %s entry %s names no source; its lookup recorded one, which pull --detail prints as %q",
				p.axis, p.uri, p.word)
		case got.source != p.word:
			t.Errorf("the %s entry %s names its source %q; pull --detail prints %q for the source its lookup "+
				"recorded", p.axis, p.uri, got.source, p.word)
		}
	}
	if len(answered) != len(planted) {
		t.Errorf("the report lists %d entries for %d planted\nreport: %s", len(answered), len(planted), body)
	}
}

// libraryReferencingFillWorkspace is the auto-pull fixture with a shared document
// its Component draws on, and with no lockfile yet.
//
// The library is what puts a second axis within reach end to end: a resolution of
// the plain fixture reaches entities alone, so an answer that dropped the library
// axis would pass over it. The lockfile is removed so the build's fill is the first
// resolution of the Release, which is the state a fill exists for.
func libraryReferencingFillWorkspace(t *testing.T) (ws, releaseRel string) {
	t.Helper()
	ws, releaseRel = autoPullWorkspace(t)
	catalogDir := filepath.Join(ws, "catalog")
	writeFixtureFile(t, filepath.Join(catalogDir, "libraries", "base-types.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BaseTypes
  version: "1.0.0"
spec:
  schemas:
    PortSpec:
      port: { type: integer, default: 8080 }
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "svc.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      ports:
        $ref: "BaseTypes:1.0.0#/schemas/PortSpec"
    provides:
      host: { type: string, required: true }
`)
	if err := os.Remove(lockfile.PathFor(filepath.Join(ws, releaseRel))); err != nil {
		t.Fatalf("remove the fixture's lockfile: %v", err)
	}
	return ws, releaseRel
}

// resolutionSourceRow is one reference a resolution reported, reduced to the
// collection it is answered in, the reference, and the word for where the run
// took it from.
type resolutionSourceRow struct {
	axis, uri, source string
}

// String renders a row the way a failure names it.
func (r resolutionSourceRow) String() string {
	source := r.source
	if source == "" {
		source = "(no source)"
	}
	return r.axis + " " + r.uri + " ← " + source
}

// fixtureRelativeURI rewrites a reference that spells a fixture's own root, so two
// fresh copies of one fixture name the same reference the same way.
func fixtureRelativeURI(uri, ws string) string {
	return strings.ReplaceAll(uri, ws, "<workspace>")
}

// independentResolutionSources runs the resolution a build's fill runs, over the
// workspace at ws, and reads where it took each reference from straight off the
// result.
//
// Each tag a lookup stamped is mapped to its word through the vocabulary's own
// mapper, never through the projection under test. A tag the vocabulary has no word
// for fails here: this side states only what the vocabulary names, so how a
// projection renders a tag without a word is not what is compared.
func independentResolutionSources(t *testing.T, ws, releaseRel string) []resolutionSourceRow {
	t.Helper()
	srv := newFillCapableServer(t, ws)
	res, err := pullops.Run(t.Context(), pullops.Config{
		Workspace:       srv.resolvedWorkspace,
		ReleasePath:     filepath.Join(ws, releaseRel),
		CompilerVersion: srv.compilerVersion,
		Toolchain:       srv.toolchain,
		DiagOut:         io.Discard,
	})
	if err != nil {
		t.Fatalf("the independent resolution failed, so this test has no expectation to compare against: %v", err)
	}
	row := func(axis, uri, resolutionPath string) resolutionSourceRow {
		source, named := closure.ResolutionSourceFor(resolutionPath)
		if !named {
			t.Fatalf("precondition: the resolution took %s %s from a source tagged %q, which the vocabulary "+
				"has no word for, so there is no word to expect", axis, uri, resolutionPath)
		}
		return resolutionSourceRow{axis: axis, uri: fixtureRelativeURI(uri, ws), source: string(source)}
	}
	var rows []resolutionSourceRow
	for _, e := range res.Closure.Entities {
		rows = append(rows, row("entities", e.URI, e.ResolutionPath))
	}
	for _, axis := range closure.LibraryAxes() {
		for _, l := range axis.Libraries(&res.Closure) {
			rows = append(rows, row("libraries", l.URI, l.ResolutionPath))
		}
	}
	for _, p := range res.Closure.Plugins {
		rows = append(rows, row("plugins", p.URI, p.ResolutionPath))
	}
	return rows
}

// buildAnswerReport returns the encoded resolution report of the build answer on
// the wire, failing where the answer carries no report key at all.
func buildAnswerReport(t *testing.T, pushed string) json.RawMessage {
	t.Helper()
	r := bufio.NewReader(strings.NewReader(pushed))
	for {
		frame, err := readMessage(r)
		if err != nil {
			t.Fatalf("the wire carries no answer to the build: %v\nwire:\n%s", err, pushed)
		}
		var answer struct {
			ID     *int                       `json:"id"`
			Result map[string]json.RawMessage `json:"result"`
		}
		if json.Unmarshal(frame, &answer) != nil || answer.ID == nil {
			continue
		}
		report, carried := answer.Result["dependencyResolution"]
		if !carried {
			t.Fatalf("the build resolved its dependencies first and its answer carries no dependencyResolution "+
				"key\nanswer: %s", frame)
		}
		return report
	}
}

// resolutionSourceMismatches compares two populations of rows as multisets and
// names every row one side has and the other does not.
//
// It is a set difference rather than a count: two sides short by the same row agree
// on a count, and the row that went missing is the only part of a failure anyone
// can act on.
func resolutionSourceMismatches(want, got []resolutionSourceRow) (missing, unexpected []string) {
	counts := map[resolutionSourceRow]int{}
	for _, r := range want {
		counts[r]++
	}
	for _, r := range got {
		counts[r]--
	}
	for r, n := range counts {
		for ; n > 0; n-- {
			missing = append(missing, r.String())
		}
		for ; n < 0; n++ {
			unexpected = append(unexpected, r.String())
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	return missing, unexpected
}

// TestBuildNamesWhereItsFillTookEachReference drives a build that resolves its
// dependencies first and requires every entry of its answer's report to name the
// source the resolution itself recorded for that reference.
//
// The expectation is a separate resolution over a separate fresh copy of the same
// workspace, so nothing one run cached can make the other report a different
// source, and nothing the build's answer says can bring the expectation into line
// with it.
//
// The plugins axis is not reached here: the one plugin this workspace uses is a
// provider the binary carries, which a resolution leaves unpinned. The projection
// test above holds that axis.
func TestBuildNamesWhereItsFillTookEachReference(t *testing.T) {
	// The harness declares the artifact cache as a RELATIVE path, so each run works
	// from inside its own copy and neither reads what the other stored.
	wantWS, wantRelease := libraryReferencingFillWorkspace(t)
	t.Chdir(wantWS)
	want := independentResolutionSources(t, wantWS, wantRelease)

	// The population preconditions, before anything is compared: both axes this
	// workspace can reach are reached, and the sources differ between references,
	// so an answer naming one word for everything cannot pass.
	axes, words := map[string]bool{}, map[string]bool{}
	for _, r := range want {
		axes[r.axis] = true
		words[r.source] = true
	}
	if !axes["entities"] || !axes["libraries"] {
		t.Fatalf("precondition: the resolution reached %v, and this test exists to hold both the entity and the "+
			"library axis to their sources", want)
	}
	if len(words) < 2 {
		t.Fatalf("precondition: every reference was taken from one source, so an answer naming that one word for "+
			"everything would pass: %v", want)
	}

	ws, releaseRel := libraryReferencingFillWorkspace(t)
	t.Chdir(ws)
	resp, pushed := driveBuildWithRawParams(t, newFillCapableServer(t, ws),
		`{"release":"`+releaseRel+`","autoPull":true}`)
	if resp.Error != nil {
		t.Fatalf("the filling build refused: code %d: %s\nwire:\n%s", resp.Error.Code, resp.Error.Message, pushed)
	}
	var got []resolutionSourceRow
	for _, e := range reportSourceEntries(t, buildAnswerReport(t, pushed)) {
		got = append(got, resolutionSourceRow{axis: e.axis, uri: fixtureRelativeURI(e.uri, ws), source: e.source})
	}
	t.Logf("the resolution recorded %v; the build answered %v", want, got)

	missing, unexpected := resolutionSourceMismatches(want, got)
	if len(missing) > 0 || len(unexpected) > 0 {
		t.Errorf("the build's answer names where its fill took its references from differently from the "+
			"resolution itself.\n  recorded by the resolution and not answered:\n    %s\n  answered and not "+
			"recorded by the resolution:\n    %s", rowsOrNone(missing), rowsOrNone(unexpected))
	}
}

// rowsOrNone keeps an empty side of a failure readable.
func rowsOrNone(rows []string) string {
	if len(rows) == 0 {
		return "(none)"
	}
	return strings.Join(rows, "\n    ")
}

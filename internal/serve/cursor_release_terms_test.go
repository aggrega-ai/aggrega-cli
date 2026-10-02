// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// cursorVariantComponent is a Component document the catalog loads, carrying the
// description a test reads back to tell one variant from another.
func cursorVariantComponent(name, description string) string {
	return "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: " + name +
		"\n  version: \"1.0.0\"\n  description: " + description +
		"\n  labels:\n    component.aggrega.ai/type: \"stateful-server\"\nspec:\n  schema:\n" +
		"    provides:\n      host: { type: string, required: true }\n"
}

// cursorVariantRecipe is a Recipe implementing component, which is all the
// implementations and references walks read of it.
func cursorVariantRecipe(name, component string) string {
	return "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: " + name +
		"\n  version: \"1.0.0\"\nspec:\n  capability: helm\n  implements: " + component + "\n"
}

// cursorVariantAggregate is the document the cursor requests are made from. Each
// component's type sits on its own line, at the line cursorRefLines names.
const cursorVariantAggregate = "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: demo-app\n" +
	"  version: \"1.0.0\"\nspec:\n  components:\n" +
	"    store:\n      type: demo/datastore/db\n" +
	"    edge:\n      type: demo/net/edge\n" +
	"    only:\n      type: demo/datastore/only-a\n" +
	"    ghost:\n      type: demo/none/declared\n"

// cursorRefLines is the line of each reference in cursorVariantAggregate; every
// value starts at character 12, so a cursor at 14 sits inside it.
var cursorRefLines = map[string]int{
	"demo/datastore/db":     8,
	"demo/net/edge":         10,
	"demo/datastore/only-a": 12,
	"demo/none/declared":    14,
}

// cursorVariants is the workspace the cursor-request proofs run in, and where its
// documents are.
type cursorVariants struct {
	srv                *Server
	root               string
	outside, outsideB  string
	aggregate          string
	componentA         string
	componentB         string
	onlyA              string
	recipeA, recipeB   string
	edgeOut, edgeOutB  string
	outsideDB          string
	releaseA, releaseB string
}

// cursorVariantWorkspace lays out one workspace keeping two variants of one
// Component side by side, one per Release, each with a Recipe implementing it,
// plus two catalog directories outside the workspace root, and opens it the way the
// daemon opens any workspace a request names.
//
//   - catalog-a/ and catalog-b/ each declare demo/datastore/db and a Recipe for it;
//     only catalog-a/ declares demo/datastore/only-a.
//   - outside/, outside the root, is the workspace's own list. It declares
//     demo/net/edge, which no workspace document does, and a demo/datastore/db of
//     its own, which the workspace documents already answer for.
//   - outside-b/, outside the root, declares another demo/net/edge.
//   - release-a.yaml reads catalog-a/ then outside/; release-b.yaml reads catalog-b/
//     then outside-b/.
//
// The two outside directories are what make the catalog lookup observable: the
// walk of the workspace cannot reach them, so anything answered from them was
// answered by the lookup.
func cursorVariantWorkspace(t *testing.T) cursorVariants {
	t.Helper()
	root := t.TempDir()
	v := cursorVariants{
		root:       root,
		outside:    t.TempDir(),
		outsideB:   t.TempDir(),
		aggregate:  filepath.Join(root, "aggregate.yaml"),
		componentA: filepath.Join(root, "catalog-a", "component", "db.yaml"),
		componentB: filepath.Join(root, "catalog-b", "component", "db.yaml"),
		onlyA:      filepath.Join(root, "catalog-a", "component", "only-a.yaml"),
		recipeA:    filepath.Join(root, "catalog-a", "recipe", "db-a.yaml"),
		recipeB:    filepath.Join(root, "catalog-b", "recipe", "db-b.yaml"),
		releaseA:   filepath.Join(root, "release-a.yaml"),
		releaseB:   filepath.Join(root, "release-b.yaml"),
	}
	v.edgeOut = filepath.Join(v.outside, "component", "edge.yaml")
	v.outsideDB = filepath.Join(v.outside, "component", "db.yaml")
	v.edgeOutB = filepath.Join(v.outsideB, "component", "edge.yaml")
	release := func(name string) string {
		return "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: " + name +
			"\n  version: \"1.0.0\"\nspec:\n  aggregate: ./aggregate.yaml\n  manifest: ./manifest.yaml\n"
	}
	files := map[string]string{
		v.aggregate:  cursorVariantAggregate,
		v.componentA: cursorVariantComponent("demo/datastore/db", "variant a"),
		v.componentB: cursorVariantComponent("demo/datastore/db", "variant b"),
		v.onlyA:      cursorVariantComponent("demo/datastore/only-a", "only variant a"),
		v.recipeA:    cursorVariantRecipe("db-a", "demo/datastore/db"),
		v.recipeB:    cursorVariantRecipe("db-b", "demo/datastore/db:1.0.0"),
		v.edgeOut:    cursorVariantComponent("demo/net/edge", "edge from the workspace list"),
		v.outsideDB:  cursorVariantComponent("demo/datastore/db", "db from the workspace list"),
		v.edgeOutB:   cursorVariantComponent("demo/net/edge", "edge from release-b"),
		v.releaseA:   release("variant-a"),
		v.releaseB:   release("variant-b"),
	}
	writeCursorFiles(t, files)
	config := fmt.Sprintf("workspace:\n  catalog_dirs:\n    - %s\n  releases:\n"+
		"    release-a.yaml:\n      catalog_dirs:\n        - catalog-a\n        - %s\n"+
		"    release-b.yaml:\n      catalog_dirs:\n        - catalog-b\n        - %s\n", v.outside, v.outside, v.outsideB)
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	sess := newTestSession(t, root, bytes.NewReader(nil), &bytes.Buffer{})
	srv, err := sess.workspaceFor(root)
	if err != nil {
		t.Fatalf("open the workspace at %s: %v", root, err)
	}
	v.srv = srv
	validateFile(t, srv, v.aggregate, cursorVariantAggregate)
	return v
}

// writeCursorFiles writes each document of a cursor-request workspace at its path,
// creating the directories it sits in.
func writeCursorFiles(t *testing.T, files map[string]string) {
	t.Helper()
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

// samePath reports whether two paths name one file, reading through symbolic
// links, so a temporary directory reached through a linked prefix still compares.
func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// cursorParams is a cursor request's params, with the Release where one is named.
func cursorParams(uri string, line int, release string) map[string]any {
	p := map[string]any{"uri": uri, "position": yamlpos.Position{Line: line, Character: 14}}
	if release != "" {
		p["release"] = release
	}
	return p
}

// TestCursorRequestsAnswerInTheTermsOfTheReleaseNamed is the invocation proof for
// go-to-definition and hover: one daemon, one cursor per reference, and the
// answer with no Release named and with each Release.
//
// Each row names the document the definition must be and the description the
// hover must render, or neither where nothing in those terms declares the name.
// The rows are chosen so every source an answer can come from is observable:
//
//   - demo/datastore/db is declared by both variants and by the workspace list's
//     outside directory. With no Release named, the workspace documents answer and
//     the outside directory's copy is never reached — the lookup is consulted on a
//     miss and not ahead of them. Each Release gets its own variant.
//   - demo/net/edge is declared by no workspace document, only by the two outside
//     directories, so every answer for it was read from a catalog lookup — the
//     workspace list's for no Release and release-a.yaml, release-b.yaml's own
//     for release-b.yaml.
//   - demo/datastore/only-a is declared only under catalog-a/, which release-b.yaml
//     does not read: release-b.yaml gets nothing, the negative row.
//   - demo/none/declared is declared nowhere: nothing, in every terms.
//
// Planted defects each row catches: drop the reach from the workspace passes and
// release-b.yaml's only-a row answers at all; drop the lookup and every edge row
// answers nothing; consult the lookup ahead of the workspace documents with no
// Release named and the no-Release db row answers the outside directory's copy.
// Every named-Release row holds whichever of a Release's lookup and the workspace
// documents is read first, because here the two find one and the same document;
// TestANamedReleaseIsAnsweredFromItsOwnLookupAheadOfTheWorkspaceDocuments is where
// that order is observable.
func TestCursorRequestsAnswerInTheTermsOfTheReleaseNamed(t *testing.T) {
	t.Parallel()
	v := cursorVariantWorkspace(t)
	cases := []struct {
		ref, release string
		// file is the document the definition must be, and about the description the
		// hover must render; both empty where the answer is null.
		file, about string
	}{
		{ref: "demo/datastore/db", file: v.componentA, about: "variant a"},
		{ref: "demo/datastore/db", release: "release-a.yaml", file: v.componentA, about: "variant a"},
		{ref: "demo/datastore/db", release: "release-b.yaml", file: v.componentB, about: "variant b"},
		{ref: "demo/net/edge", file: v.edgeOut, about: "edge from the workspace list"},
		{ref: "demo/net/edge", release: "release-a.yaml", file: v.edgeOut, about: "edge from the workspace list"},
		{ref: "demo/net/edge", release: "release-b.yaml", file: v.edgeOutB, about: "edge from release-b"},
		{ref: "demo/datastore/only-a", file: v.onlyA, about: "only variant a"},
		{ref: "demo/datastore/only-a", release: "release-a.yaml", file: v.onlyA, about: "only variant a"},
		{ref: "demo/datastore/only-a", release: "release-b.yaml"},
		{ref: "demo/none/declared"},
		{ref: "demo/none/declared", release: "release-a.yaml"},
		{ref: "demo/none/declared", release: "release-b.yaml"},
	}
	for _, tc := range cases {
		name := tc.ref + " naming " + tc.release
		if tc.release == "" {
			name = tc.ref + " naming no Release"
		}
		t.Run(name, func(t *testing.T) {
			params := cursorParams(v.aggregate, cursorRefLines[tc.ref], tc.release)

			var res resolveResult
			decodeDaemonResult(t, callDaemonMethod(t, v.srv, "aggrega/resolve", params), &res)
			switch {
			case tc.file == "" && res.Location != nil:
				t.Errorf("resolve answered %s; want null", res.Location.URI)
			case tc.file != "" && res.Location == nil:
				t.Errorf("resolve answered null; want %s", tc.file)
			case tc.file != "" && !samePath(res.Location.URI, tc.file):
				t.Errorf("resolve answered %s; want %s", res.Location.URI, tc.file)
			case tc.file != "" && res.Location.Range.Start.Line != 3:
				t.Errorf("resolve answered line %d; want 3, the metadata.name line", res.Location.Range.Start.Line)
			}

			var hov hoverResult
			decodeDaemonResult(t, callDaemonMethod(t, v.srv, "aggrega/hover", params), &hov)
			switch {
			case tc.about == "" && hov.Contents != nil:
				t.Errorf("hover rendered %q; want null contents", hov.Contents.Value)
			case tc.about != "" && hov.Contents == nil:
				t.Errorf("hover rendered nothing; want the entity described %q", tc.about)
			case tc.about != "" && !strings.Contains(hov.Contents.Value, "\n"+tc.about+"\n"):
				t.Errorf("hover rendered %q; want the entity described %q", hov.Contents.Value, tc.about)
			}
		})
	}
}

// cursorShadowAggregate is the document the precedence proofs make their cursor
// requests from; each component's type sits on the line cursorShadowLines names,
// its value starting at character 12.
const cursorShadowAggregate = "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: app\n" +
	"  version: \"1.0.0\"\nspec:\n  components:\n" +
	"    db:\n      type: demo/db\n" +
	"    svc:\n      type: demo/svc\n" +
	"    side:\n      type: demo/beside-only\n"

// cursorShadowLines is the line of each reference in cursorShadowAggregate.
var cursorShadowLines = map[string]int{
	"demo/db":          8,
	"demo/svc":         10,
	"demo/beside-only": 12,
}

// cursorShadows is the workspace where the order between a named Release's lookup
// and the workspace documents decides the answer, and where its documents are.
type cursorShadows struct {
	srv                       *Server
	root                      string
	aggregate                 string
	dbA, dbOverride           string
	svcA, svcBeside           string
	besideOnly                string
	releaseA, releaseOverride string
}

// cursorShadowWorkspace lays out a workspace in which a walk of the workspace and
// a named Release's stack find different documents for one name, and opens it.
//
//   - catalog-a/ is the workspace's own list, which release-a.yaml reads, having
//     no entry of its own. release-o.yaml reads overrides/ ahead of catalog-a/.
//   - demo/db is declared under overrides/ and under catalog-a/. release-o.yaml's
//     stack resolves it to overrides/, the one a walk of the workspace reaches
//     second.
//   - demo/svc is declared under catalog-a/ and by a-beside.yaml, which sits under
//     no catalog directory and which a walk reaches first.
//   - demo/beside-only is declared by beside-only.yaml alone, under no catalog
//     directory, so no Release's lookup resolves it.
func cursorShadowWorkspace(t *testing.T) cursorShadows {
	t.Helper()
	root := t.TempDir()
	v := cursorShadows{
		root:            root,
		aggregate:       filepath.Join(root, "aggregate.yaml"),
		dbA:             filepath.Join(root, "catalog-a", "component", "db.yaml"),
		dbOverride:      filepath.Join(root, "overrides", "component", "db.yaml"),
		svcA:            filepath.Join(root, "catalog-a", "component", "svc.yaml"),
		svcBeside:       filepath.Join(root, "a-beside.yaml"),
		besideOnly:      filepath.Join(root, "beside-only.yaml"),
		releaseA:        filepath.Join(root, "release-a.yaml"),
		releaseOverride: filepath.Join(root, "release-o.yaml"),
	}
	release := func(name string) string {
		return "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: " + name +
			"\n  version: \"1.0.0\"\nspec:\n  aggregate: ./aggregate.yaml\n  manifest: ./manifest.yaml\n"
	}
	writeCursorFiles(t, map[string]string{
		v.aggregate:       cursorShadowAggregate,
		v.dbA:             cursorVariantComponent("demo/db", "db in catalog-a"),
		v.dbOverride:      cursorVariantComponent("demo/db", "db in overrides"),
		v.svcA:            cursorVariantComponent("demo/svc", "svc in catalog-a"),
		v.svcBeside:       cursorVariantComponent("demo/svc", "svc beside the catalog"),
		v.besideOnly:      cursorVariantComponent("demo/beside-only", "declared beside the catalog only"),
		v.releaseA:        release("release-a"),
		v.releaseOverride: release("release-o"),
		filepath.Join(root, workspaceconfig.ConfigFilename): "workspace:\n  catalog_dirs:\n    - catalog-a\n" +
			"  releases:\n    release-o.yaml:\n      catalog_dirs:\n        - overrides\n        - catalog-a\n",
	})
	sess := newTestSession(t, root, bytes.NewReader(nil), &bytes.Buffer{})
	srv, err := sess.workspaceFor(root)
	if err != nil {
		t.Fatalf("open the workspace at %s: %v", root, err)
	}
	v.srv = srv
	validateFile(t, srv, v.aggregate, cursorShadowAggregate)
	return v
}

// TestANamedReleaseIsAnsweredFromItsOwnLookupAheadOfTheWorkspaceDocuments is the
// proof of the order go-to-definition and hover read their two sources in: a
// request naming a Release is answered through that Release's lookup first and
// from the workspace documents only where the lookup resolves nothing; a request
// naming none reads the workspace documents first and the workspace's own lookup
// only on a miss.
//
// Each named row also asks aggrega/entityDefinition for the same name and Release,
// and requires the two to name one document: go-to-definition for a named Release
// opens what a lookup by name in that Release's terms answers.
//
// Planted defects the rows catch: read the workspace documents first for a named
// Release too, and release-o.yaml's db row answers catalog-a/ while both svc rows
// naming a Release answer a-beside.yaml; read a named Release's lookup alone, with
// no fallback, and the beside-only row naming release-o.yaml answers nothing; read
// the lookup first with no Release named as well, and the svc row naming none
// answers catalog-a/.
func TestANamedReleaseIsAnsweredFromItsOwnLookupAheadOfTheWorkspaceDocuments(t *testing.T) {
	t.Parallel()
	v := cursorShadowWorkspace(t)
	cases := []struct {
		ref, release string
		// file is the document the definition must be, and about the description
		// the hover must render.
		file, about string
	}{
		{ref: "demo/db", file: v.dbA, about: "db in catalog-a"},
		{ref: "demo/db", release: "release-a.yaml", file: v.dbA, about: "db in catalog-a"},
		{ref: "demo/db", release: "release-o.yaml", file: v.dbOverride, about: "db in overrides"},
		{ref: "demo/svc", file: v.svcBeside, about: "svc beside the catalog"},
		{ref: "demo/svc", release: "release-a.yaml", file: v.svcA, about: "svc in catalog-a"},
		{ref: "demo/svc", release: "release-o.yaml", file: v.svcA, about: "svc in catalog-a"},
		{ref: "demo/beside-only", file: v.besideOnly, about: "declared beside the catalog only"},
		{ref: "demo/beside-only", release: "release-o.yaml", file: v.besideOnly, about: "declared beside the catalog only"},
	}
	for _, tc := range cases {
		name := tc.ref + " naming " + tc.release
		if tc.release == "" {
			name = tc.ref + " naming no Release"
		}
		t.Run(name, func(t *testing.T) {
			params := cursorParams(v.aggregate, cursorShadowLines[tc.ref], tc.release)

			var res resolveResult
			decodeDaemonResult(t, callDaemonMethod(t, v.srv, "aggrega/resolve", params), &res)
			switch {
			case res.Location == nil:
				t.Errorf("resolve answered null; want %s", tc.file)
			case !samePath(res.Location.URI, tc.file):
				t.Errorf("resolve answered %s; want %s", res.Location.URI, tc.file)
			case res.Location.Range.Start.Line != 3:
				t.Errorf("resolve answered line %d; want 3, the metadata.name line", res.Location.Range.Start.Line)
			}

			var hov hoverResult
			decodeDaemonResult(t, callDaemonMethod(t, v.srv, "aggrega/hover", params), &hov)
			switch {
			case hov.Contents == nil:
				t.Errorf("hover rendered nothing; want the entity described %q", tc.about)
			case !strings.Contains(hov.Contents.Value, "\n"+tc.about+"\n"):
				t.Errorf("hover rendered %q; want the entity described %q", hov.Contents.Value, tc.about)
			}

			if tc.release == "" || tc.file == v.besideOnly {
				return
			}
			var def entityDefinitionResult
			decodeDaemonResult(t, callDaemonMethod(t, v.srv, "aggrega/entityDefinition",
				map[string]any{"name": tc.ref, "release": tc.release}), &def)
			if !samePath(filepath.Join(v.root, def.File), tc.file) {
				t.Errorf("entityDefinition naming %s answered %s; want %s, the document resolve opened",
					tc.release, def.File, tc.file)
			}
		})
	}
}

// TestANamedReleasesLookupIsAnsweredFromTheCopyTheEditorHolds pins that where a
// named Release's lookup picks a document the editor holds with unsaved changes,
// the definition is positioned in that copy and the hover renders it, and that a
// held copy declaring some other name is not what is answered.
//
// The control comes first: before the editor sends overrides/'s demo/db, the
// definition is on line 3 of the bytes on disk. The edited copy adds two lines
// above the document and changes its description, so a definition read from disk
// stays on line 3 and a hover read from disk renders the old description — which is
// what a planted defect reading only the disk produces. The renamed copy then
// declares another name on the same line: a defect answering whatever copy is held
// renders "renamed in the editor".
func TestANamedReleasesLookupIsAnsweredFromTheCopyTheEditorHolds(t *testing.T) {
	t.Parallel()
	v := cursorShadowWorkspace(t)
	params := cursorParams(v.aggregate, cursorShadowLines["demo/db"], "release-o.yaml")
	answer := func(t *testing.T, step string, wantLine int, wantAbout string) {
		t.Helper()
		var res resolveResult
		decodeDaemonResult(t, callDaemonMethod(t, v.srv, "aggrega/resolve", params), &res)
		switch {
		case res.Location == nil:
			t.Errorf("%s: resolve answered null; want %s", step, v.dbOverride)
		case !samePath(res.Location.URI, v.dbOverride):
			t.Errorf("%s: resolve answered %s; want %s", step, res.Location.URI, v.dbOverride)
		case res.Location.Range.Start != (yamlpos.Position{Line: wantLine, Character: 8}):
			t.Errorf("%s: resolve answered %+v; want line %d character 8, the metadata.name value",
				step, res.Location.Range.Start, wantLine)
		}
		var hov hoverResult
		decodeDaemonResult(t, callDaemonMethod(t, v.srv, "aggrega/hover", params), &hov)
		if hov.Contents == nil || !strings.Contains(hov.Contents.Value, "\n"+wantAbout+"\n") {
			t.Errorf("%s: hover rendered %+v; want the entity described %q", step, hov.Contents, wantAbout)
		}
	}

	answer(t, "nothing held", 3, "db in overrides")

	validateFile(t, v.srv, v.dbOverride,
		"# held open in the editor,\n# two lines added above the document\n"+
			cursorVariantComponent("demo/db", "db in overrides, edited in the editor"))
	answer(t, "an edited copy held", 5, "db in overrides, edited in the editor")

	validateFile(t, v.srv, v.dbOverride, cursorVariantComponent("demo/renamed", "renamed in the editor"))
	answer(t, "a renamed copy held", 3, "db in overrides")
}

// TestCompletionOffersWhatTheReleaseNamedReaches holds the candidate list at a
// component's type, whole and in order, with no Release named and with each.
//
// The no-Release row is the control: every workspace document and the workspace
// list's directory. release-a.yaml's row equals it, since its stack reads that
// directory too and the reach holds both of its variant's documents; release-b.yaml's
// row loses demo/datastore/only-a, declared only under catalog-a/, which it does
// not read. A planted defect that dropped the reach from the workspace pass puts
// only-a back into release-b.yaml's row; one that kept the session lookup for a
// named Release changes nothing here, which is why the edge row of the test above
// is the lookup's proof and this one the reach's.
func TestCompletionOffersWhatTheReleaseNamedReaches(t *testing.T) {
	t.Parallel()
	v := cursorVariantWorkspace(t)
	cases := []struct {
		release string
		want    []string
	}{
		{want: []string{"demo/datastore/db", "demo/datastore/only-a", "demo/net/edge"}},
		{release: "release-a.yaml", want: []string{"demo/datastore/db", "demo/datastore/only-a", "demo/net/edge"}},
		{release: "release-b.yaml", want: []string{"demo/datastore/db", "demo/net/edge"}},
	}
	for _, tc := range cases {
		t.Run("naming "+tc.release, func(t *testing.T) {
			var res completionResult
			decodeDaemonResult(t, callDaemonMethod(t, v.srv, "aggrega/completion",
				cursorParams(v.aggregate, cursorRefLines["demo/datastore/db"], tc.release)), &res)
			if got := labelsList(res.Items); !slices.Equal(got, tc.want) {
				t.Errorf("completion naming %q = %q, want %q", tc.release, got, tc.want)
			}
		})
	}
}

// TestReferencesAndImplementationsHoldOnlyWhatTheReleaseNamedReaches drives both
// requests from variant a's metadata.name, with no Release and with each, and holds
// the whole set of locations each answers.
//
// The cursor stays in variant a's document on every row, so the release-b.yaml rows
// are the case a client meets when a document of one variant is open while another
// Release is selected: the name is the same, and the answer is release-b.yaml's.
// Every row fails on a location gained as well as one lost.
func TestReferencesAndImplementationsHoldOnlyWhatTheReleaseNamedReaches(t *testing.T) {
	t.Parallel()
	v := cursorVariantWorkspace(t)
	content, err := os.ReadFile(v.componentA)
	if err != nil {
		t.Fatalf("read %s: %v", v.componentA, err)
	}
	validateFile(t, v.srv, v.componentA, string(content))
	cases := []struct {
		method, release string
		want            []string
	}{
		{method: "aggrega/findReferences", want: []string{v.aggregate, v.recipeA, v.recipeB}},
		{method: "aggrega/findReferences", release: "release-a.yaml", want: []string{v.aggregate, v.recipeA}},
		{method: "aggrega/findReferences", release: "release-b.yaml", want: []string{v.aggregate, v.recipeB}},
		{method: "aggrega/findImplementations", want: []string{v.recipeA, v.recipeB}},
		{method: "aggrega/findImplementations", release: "release-a.yaml", want: []string{v.recipeA}},
		{method: "aggrega/findImplementations", release: "release-b.yaml", want: []string{v.recipeB}},
	}
	for _, tc := range cases {
		t.Run(tc.method+" naming "+tc.release, func(t *testing.T) {
			params := map[string]any{"uri": v.componentA, "position": yamlpos.Position{Line: 3, Character: 10}}
			if tc.release != "" {
				params["release"] = tc.release
			}
			var res findReferencesResult
			decodeDaemonResult(t, callDaemonMethod(t, v.srv, tc.method, params), &res)
			var got []string
			for _, loc := range res.Locations {
				got = append(got, loc.URI)
			}
			slices.Sort(got)
			want := append([]string(nil), tc.want...)
			slices.Sort(want)
			if !slices.EqualFunc(got, want, samePath) {
				t.Errorf("%s naming %q = %q, want %q", tc.method, tc.release, got, want)
			}
		})
	}
}

// TestACursorRequestNamingAReleaseIsRefusedWhereTheValueCannotNameOne pins the
// refusals of the Release parameter on all five cursor requests, one fault per row,
// beside the positive control on each method.
//
// The refusal is the one every Release parameter answers with, so a value that
// names no Release is never answered as if it named the workspace's own terms.
func TestACursorRequestNamingAReleaseIsRefusedWhereTheValueCannotNameOne(t *testing.T) {
	t.Parallel()
	v := cursorVariantWorkspace(t)
	faults := []struct {
		name, release, fault string
	}{
		{name: "a Release that is there", release: "release-a.yaml"},
		{name: "a Release that is not there", release: "release-gone.yaml", fault: "does not exist"},
		{name: "a directory", release: "catalog-a", fault: "is a directory"},
		{name: "an absolute Release", release: v.releaseA, fault: "is an absolute path"},
		{name: "a Release above the root", release: "../release-a.yaml", fault: "resolves outside the workspace root"},
	}
	for _, method := range []string{"aggrega/hover", "aggrega/resolve", "aggrega/completion",
		"aggrega/findReferences", "aggrega/findImplementations"} {
		for _, tc := range faults {
			t.Run(method+" "+tc.name, func(t *testing.T) {
				resp := callDaemonMethod(t, v.srv, method,
					cursorParams(v.aggregate, cursorRefLines["demo/datastore/db"], tc.release))
				if tc.fault == "" {
					if resp.Error != nil {
						t.Fatalf("%s refused the control: %d %s", method, resp.Error.Code, resp.Error.Message)
					}
					return
				}
				if resp.Error == nil {
					t.Fatalf("%s answered %s; want a refusal carrying %q", method, resp.Result, tc.fault)
				}
				if resp.Error.Code != jsonRPCInvalidParams {
					t.Errorf("code = %d; want %d, the request being at fault", resp.Error.Code, jsonRPCInvalidParams)
				}
				if !strings.Contains(resp.Error.Message, tc.fault) {
					t.Errorf("refusal %q does not carry %q", resp.Error.Message, tc.fault)
				}
			})
		}
	}
}

// TestAWithdrawnCursorRequestDoesNotGoOnToReadTheCatalog pins that a catalog lookup
// is not read for a request already withdrawn, for hover and for go-to-definition
// alike: neither the lookup a miss falls through to with no Release named, nor the
// one a named Release is answered through ahead of the workspace documents.
//
// demo/net/edge is declared only outside the workspace root, so a live request is
// answered from a lookup and nothing else — which is the control: without it, a
// withdrawn arm answering nothing would prove nothing. A planted defect removing
// either withdrawal check makes that pair of withdrawn arms answer the outside
// document.
func TestAWithdrawnCursorRequestDoesNotGoOnToReadTheCatalog(t *testing.T) {
	t.Parallel()
	v := cursorVariantWorkspace(t)
	withdrawn, stop := context.WithCancel(context.Background())
	stop()
	named, refusal := v.srv.termsFor("release-b.yaml")
	if refusal != nil {
		t.Fatalf("the terms of release-b.yaml were refused: %s", refusal.Message)
	}
	for _, tc := range []struct {
		name  string
		terms releaseTerms
		want  string
	}{
		{name: "naming no Release", terms: v.srv.workspaceTerms(), want: v.edgeOut},
		{name: "naming release-b.yaml", terms: named, want: v.edgeOutB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if doc, _ := v.srv.findEntityForHover(withdrawn, "demo/net/edge", tc.terms); doc != nil {
				t.Errorf("a withdrawn hover found the entity; it was supposed to read no catalog")
			}
			if loc, _ := v.srv.findDefinitionByURI(withdrawn, "demo/net/edge", tc.terms); loc != nil {
				t.Errorf("a withdrawn go-to-definition answered %s; it was supposed to read no catalog", loc.URI)
			}
			if doc, _ := v.srv.findEntityForHover(context.Background(), "demo/net/edge", tc.terms); doc == nil {
				t.Fatalf("the live hover control found nothing, so the withdrawn arm above proves nothing")
			}
			if loc, _ := v.srv.findDefinitionByURI(context.Background(), "demo/net/edge", tc.terms); loc == nil || !samePath(loc.URI, tc.want) {
				t.Fatalf("the live go-to-definition control answered %v; want %s", loc, tc.want)
			}
		})
	}
}

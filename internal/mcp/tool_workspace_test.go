// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The discovery fixture. Every file in it is chosen so that a surface deciding
// what a document is by its FILENAME would answer differently from one deciding
// by the document's own kind header, which is the property these tools inherit
// from the implementation they call and the one a second implementation would
// quietly get wrong.
//
//   - parts/blue.yaml           a Component whose path says nothing about either
//     its kind or its name
//   - parts/green.yaml          a Release named without any conventional prefix
//   - release-production.yaml   NOT a Release: a Component wearing the filename
//   - values.yaml               foreign YAML declaring no kind at all
//   - notes/unnamed.yaml        a kind with no name, so nothing to address it by
const (
	discoveryComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/compute/web-server
  version: "1.0.0"
  description: A component whose file name says nothing about it.
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    infra:
      port: { type: integer, default: 8080 }
`

	discoveryRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: unconventionally-named-release
  version: "1.0.0"
  environment: production
spec:
  manifest: ./manifest.yaml
`

	discoveryLookalike = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/compute/not-a-release
  version: "1.0.0"
spec:
  schema:
    infra:
      port: { type: integer, default: 9090 }
`

	discoveryForeignYAML = `replicaCount: 3
image:
  repository: nginx
  tag: "1.25"
`

	discoveryKindWithoutName = `apiVersion: aggrega.ai/v1alpha1
kind: Component
spec:
  schema: {}
`
)

// newDiscoveryWorkspace writes the fixture and returns its root.
func newDiscoveryWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range map[string]string{
		"parts/blue.yaml":         discoveryComponent,
		"parts/green.yaml":        discoveryRelease,
		"release-production.yaml": discoveryLookalike,
		"values.yaml":             discoveryForeignYAML,
		"notes/unnamed.yaml":      discoveryKindWithoutName,
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

// callDiscoveryTool invokes a tool through the real tools/call dispatch — by
// name, the way a client reaches it — rather than through its handler directly,
// so what these tests exercise is a registered tool and not a function that
// happens to exist.
func callDiscoveryTool(t *testing.T, s *server, name string, args map[string]any) *callToolResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	params, err := json.Marshal(callToolParams{Name: name, Arguments: raw})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	res, rpcErr := s.handleToolsCall(context.Background(), params)
	if rpcErr != nil {
		t.Fatalf("tools/call %s returned a protocol error: %s", name, rpcErr.Message)
	}
	out, ok := res.(*callToolResult)
	if !ok {
		t.Fatalf("tools/call %s returned %T, not a tool result", name, res)
	}
	return out
}

func decodeStructured(t *testing.T, res *callToolResult, into any) {
	t.Helper()
	if res.IsError {
		t.Fatalf("the tool reported a failure: %s", res.Content[0].Text)
	}
	if len(res.StructuredContent) == 0 {
		t.Fatal("the tool returned no structured content to read")
	}
	if err := json.Unmarshal(res.StructuredContent, into); err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
}

// TestBothDiscoveryToolsAreInTheDefaultListing pins that a caller arriving cold
// is offered them without having to ask what else exists.
//
// It matters more for these two than for any other tool in the set: every other
// curated tool takes a subject, so a caller that cannot enumerate has nothing to
// put in one. A discovery tool reachable only through a discovery call would
// leave that caller exactly where it started.
func TestBothDiscoveryToolsAreInTheDefaultListing(t *testing.T) {
	s := &server{out: io.Discard, logw: io.Discard, launchRoot: t.TempDir()}
	listed, rpcErr := s.handleToolsList(context.Background())
	if rpcErr != nil {
		t.Fatalf("tools/list: %s", rpcErr.Message)
	}
	result, ok := listed.(listToolsResult)
	if !ok {
		t.Fatalf("tools/list returned %T", listed)
	}

	advertised := map[string]bool{}
	for _, tool := range result.Tools {
		advertised[tool.Name] = true
	}
	for _, want := range []string{"list-entities", "list-releases"} {
		if !advertised[want] {
			t.Errorf("the default listing does not advertise %q, so a caller that cannot name a subject "+
				"is offered no way to find one", want)
		}
	}
}

// TestTheEntityIndexReportsWhatTheDocumentDeclaresNotWhatItIsCalled is the
// content-over-filename proof for the entity listing, and it is built so that a
// filename match cannot pass it.
//
// Two rows carry the weight. A Component in a file called blue.yaml is reported
// under its declared kind and name, neither of which appears anywhere in its
// path. And a file called release-production.yaml is reported as the Component
// it declares itself to be — an implementation reading the name would have
// called it a Release.
func TestTheEntityIndexReportsWhatTheDocumentDeclaresNotWhatItIsCalled(t *testing.T) {
	root := newDiscoveryWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, launchRoot: root}

	var got listEntitiesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-entities", map[string]any{
		"workspace_root": root, "response_format": "detailed",
	}), &got)

	byFile := map[string]string{}
	for _, e := range got.Entities {
		if e.File != "" {
			byFile[e.File] = e.Kind + " " + e.Name
		}
	}

	t.Run("a file whose name says nothing about its contents", func(t *testing.T) {
		const file = "parts/blue.yaml"
		want := "Component example/compute/web-server"
		if byFile[file] != want {
			t.Fatalf("%s is reported as %q; want %q. The name and the kind are both absent from the path, so "+
				"only a read of the document produces them.", file, byFile[file], want)
		}
	})

	t.Run("a file named like a Release that declares a Component", func(t *testing.T) {
		const file = "release-production.yaml"
		got := byFile[file]
		if !strings.HasPrefix(got, "Component ") {
			t.Fatalf("%s is reported as %q; want it reported as a Component. Its filename is the only thing "+
				"about it that says Release.", file, got)
		}
	})

	t.Run("foreign YAML declaring no kind is not an entity", func(t *testing.T) {
		if entry, reported := byFile["values.yaml"]; reported {
			t.Fatalf("a values file with no kind was reported as an entity: %q", entry)
		}
	})

	t.Run("a kind with no name is not addressable and is not reported", func(t *testing.T) {
		if entry, reported := byFile["notes/unnamed.yaml"]; reported {
			t.Fatalf("a document with no metadata.name was reported as %q, and nothing could address it", entry)
		}
	})
}

// newMixedProjectWorkspace writes a workspace holding Aggrega documents beside
// the rendered Kubernetes manifests a team that commits its build output keeps
// next to them. Every document in it declares a kind and a name, so what the
// listing does with each is decided by its apiVersion alone.
//
// It is deliberately separate from the discovery fixture above: that one is
// sized against the paging assertions built on it, and a fixture whose entity
// count moves would make those answer a different question.
func newMixedProjectWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range map[string]string{
		"parts/api.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/compute/api
  version: "1.0.0"
`,
		// No apiVersion: an unfinished Aggrega document, which the listing
		// reports so its author can be told what it is missing.
		"parts/draft.yaml": `kind: Component
metadata:
  name: example/compute/draft
`,
		"rendered/deployment.yaml": `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
`,
		"rendered/service.yaml": `apiVersion: v1
kind: Service
metadata:
  name: api
`,
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

// TestTheEntityListingOffersOnlyNamesTheOtherToolsCanTake drives the admission
// rule through the tool a client actually calls, and it is the half the daemon's
// own test cannot make: this listing tells a caller it is how the names for
// every other tool are obtained, so a name it offers that describe, validate and
// build all refuse is a listing that has misdirected its caller.
//
// The draft row is the one that keeps the rule honest in the other direction. A
// document declaring no apiVersion is still offered, because it is an Aggrega
// document with something missing rather than another project's file, and a
// listing that hid it would leave its author with no way to find out it is there.
func TestTheEntityListingOffersOnlyNamesTheOtherToolsCanTake(t *testing.T) {
	root := newMixedProjectWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, launchRoot: root}

	var got listEntitiesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-entities", map[string]any{
		"workspace_root": root, "response_format": "detailed",
	}), &got)

	byFile := map[string]string{}
	for _, e := range got.Entities {
		if e.File != "" {
			byFile[e.File] = e.Kind + " " + e.Name
		}
	}

	t.Run("an Aggrega document declaring the group is offered", func(t *testing.T) {
		const file = "parts/api.yaml"
		want := "Component example/compute/api"
		if byFile[file] != want {
			t.Fatalf("%s is reported as %q; want %q", file, byFile[file], want)
		}
	})

	t.Run("an Aggrega document declaring no apiVersion is still offered", func(t *testing.T) {
		const file = "parts/draft.yaml"
		want := "Component example/compute/draft"
		if byFile[file] != want {
			t.Fatalf("%s is reported as %q; want %q. A document with no apiVersion is an Aggrega document "+
				"missing a field, not another project's file, and hiding it leaves its author nothing to act on.",
				file, byFile[file], want)
		}
	})

	for _, rendered := range []string{"rendered/deployment.yaml", "rendered/service.yaml"} {
		t.Run("a rendered manifest is not offered: "+rendered, func(t *testing.T) {
			if entry, reported := byFile[rendered]; reported {
				t.Fatalf("%s was offered as %q. Every other tool refuses that name, so the listing that says it "+
					"is how names are obtained would have handed its caller one that resolves nowhere.",
					rendered, entry)
			}
		})
	}

	// The breakdown is what a caller reading the summary sees, so a foreign kind
	// surviving there is as misleading as one surviving in the records.
	for _, k := range got.Kinds {
		if k.Kind == "Deployment" || k.Kind == "Service" {
			t.Errorf("the per-kind breakdown still counts %d %s, so the summary reports a population the "+
				"records no longer carry", k.Count, k.Kind)
		}
	}
	if got.Count != len(byFile) {
		t.Errorf("count = %d but %d distinct files were reported; the total and the records disagree",
			got.Count, len(byFile))
	}
}

// TestTheReleaseListingIsDecidedByContentNotFilename is the same proof for the
// Release listing, and it is the half that a corpus of conventionally-named
// fixtures cannot make: the two assertions pull in opposite directions, so an
// implementation that matched on the filename fails both.
func TestTheReleaseListingIsDecidedByContentNotFilename(t *testing.T) {
	root := newDiscoveryWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, launchRoot: root}

	var got listReleasesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-releases", map[string]any{
		"workspace_root": root, "response_format": "detailed",
	}), &got)

	listed := map[string]bool{}
	for _, r := range got.Releases {
		listed[r] = true
	}

	t.Run("an unconventionally named Release is included", func(t *testing.T) {
		if !listed["parts/green.yaml"] {
			t.Fatalf("parts/green.yaml declares kind: Release and was left out; got %v", got.Releases)
		}
	})

	t.Run("a file merely named like a Release is excluded", func(t *testing.T) {
		if listed["release-production.yaml"] {
			t.Fatalf("release-production.yaml declares kind: Component and was listed as a Release; got %v",
				got.Releases)
		}
	})

	if got.Returned != len(got.Releases) {
		t.Errorf("the answer reports %d paths returned and carries %d", got.Returned, len(got.Releases))
	}
	// The fixture holds fewer Releases than one page, so this answer is the whole
	// listing — which is what makes the count and the page the same number here and
	// what makes a remainder reported over it wrong.
	if got.Count != len(got.Releases) || got.MoreRemaining {
		t.Errorf("the workspace holds %d Releases and one page carried %d with more_remaining=%v; the fixture "+
			"is smaller than a page, so the page is the whole listing", got.Count, len(got.Releases), got.MoreRemaining)
	}
}

// TestEnumerationProducesANameTheAddressingToolsAccept walks the join the two
// listings exist to make possible: enumerate, take a coordinate out of the
// answer, and address it.
//
// The negative leg is what gives the positive one its meaning. A name the
// enumeration did not produce is refused rather than answered with an empty
// result — which is why a caller holding no name cannot simply guess one, and
// therefore why enumeration had to be on this surface at all.
func TestEnumerationProducesANameTheAddressingToolsAccept(t *testing.T) {
	root := newDiscoveryWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, launchRoot: root}

	var index listEntitiesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-entities", map[string]any{
		"workspace_root": root, "response_format": "detailed",
	}), &index)

	var name string
	for _, e := range index.Entities {
		if e.Kind == "Component" && e.File == "parts/blue.yaml" {
			name = e.Name
			break
		}
	}
	if name == "" {
		t.Fatal("the enumeration named no Component to address, so there is no join to walk")
	}

	t.Run("the enumerated name resolves", func(t *testing.T) {
		res := callDiscoveryTool(t, s, "describe", map[string]any{
			"name":           name,
			"workspace_root": root,
			"catalog_dirs":   []string{root},
		})
		if res.IsError {
			t.Fatalf("describe refused a name the enumeration produced (%q): %s", name, res.Content[0].Text)
		}
		var described describeEntityPayload
		decodeStructured(t, res, &described)
		if described.Name != name || described.Kind != "Component" {
			t.Fatalf("describe answered about %s %q; the enumeration named Component %q",
				described.Kind, described.Name, name)
		}
	})

	t.Run("a name the enumeration did not produce is refused, not answered empty", func(t *testing.T) {
		res := callDiscoveryTool(t, s, "describe", map[string]any{
			"name":           "example/compute/no-such-component",
			"workspace_root": root,
			"catalog_dirs":   []string{root},
		})
		if !res.IsError {
			t.Fatal("describe answered a name no document declares; an empty answer there would report a " +
				"typo as a subject with nothing to offer, which is why enumeration is the way to get a name")
		}
	})
}

// TestTheDiscoveryToolsAnswerWithTheSummaryWhenNoShapeIsNamed pins the default
// both tools take, which is the one thing about them a caller arriving cold
// cannot choose: its first call is made before it knows what the workspace holds,
// so the shape that call returns is decided here rather than by the caller.
//
// The assertion is not merely that the records are absent. An answer that carried
// nothing would satisfy that and tell the caller nothing, so each leg checks that
// the summary IS there — the total, and a breakdown that adds up to it — and that
// the answer says records remain to be asked for.
func TestTheDiscoveryToolsAnswerWithTheSummaryWhenNoShapeIsNamed(t *testing.T) {
	root := newDiscoveryWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, launchRoot: root}

	t.Run("entities", func(t *testing.T) {
		var got listEntitiesResult
		decodeStructured(t, callDiscoveryTool(t, s, "list-entities", map[string]any{"workspace_root": root}), &got)

		if got.ResponseFormat != responseFormatConcise {
			t.Errorf("a call naming no response format was answered in the %q shape; a caller that has not "+
				"seen the workspace cannot have asked for its records", got.ResponseFormat)
		}
		if len(got.Entities) != 0 || got.Returned != 0 {
			t.Errorf("the summary shape carried %d record(s) and reported %d returned; a first page of records "+
				"answers a different question from the one this call asks", len(got.Entities), got.Returned)
		}
		if got.Count == 0 {
			t.Fatal("the summary reported no entities at all, so the fixture is not being read and the rest of " +
				"this measures nothing")
		}
		summed := 0
		for _, k := range got.Kinds {
			summed += k.Count
		}
		if summed != got.Count {
			t.Errorf("the per-kind breakdown totals %d and the workspace holds %d, so the summary does not "+
				"describe the population it reports: %+v", summed, got.Count, got.Kinds)
		}
		if !got.MoreRemaining {
			t.Error("the summary carries no records and the workspace holds some, so records remain to be " +
				"asked for and more_remaining says they do not")
		}
	})

	t.Run("releases", func(t *testing.T) {
		var got listReleasesResult
		decodeStructured(t, callDiscoveryTool(t, s, "list-releases", map[string]any{"workspace_root": root}), &got)

		if got.ResponseFormat != responseFormatConcise {
			t.Errorf("a call naming no response format was answered in the %q shape", got.ResponseFormat)
		}
		if len(got.Releases) != 0 || got.Returned != 0 {
			t.Errorf("the summary shape carried %d path(s) and reported %d returned", len(got.Releases), got.Returned)
		}
		if got.Count == 0 {
			t.Fatal("the summary reported no Releases, so the fixture is not being read")
		}
		summed := 0
		for _, d := range got.Directories {
			summed += d.Count
		}
		if summed != got.Count {
			t.Errorf("the per-directory breakdown totals %d and the workspace holds %d Releases: %+v",
				summed, got.Count, got.Directories)
		}
		if !got.MoreRemaining {
			t.Error("the summary carries no paths and the workspace holds Releases, so more_remaining is wrong")
		}
	})
}

// discoveryPlantedKind is a kind the rest of the fixture does not declare, so a
// breakdown that reports it can only have read it out of the document.
const discoveryPlantedKind = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: example/planted-manifest
  version: "1.0.0"
spec:
  mappings: []
`

// TestTheSummaryGrowsWithTheWorkspaceRatherThanBeingDeclared is the assertion a
// shrinking bug passes and a derived breakdown does not.
//
// Every other check here is satisfied by an answer that reports LESS than the
// workspace holds — a projection that dropped entries would still be small, still
// internally consistent, and still under any size bound. So this one adds a
// document of a kind nothing else in the fixture declares and requires the answer
// to GROW: the total up by exactly one, and a row for the new kind that no list
// written beside the code could have supplied.
func TestTheSummaryGrowsWithTheWorkspaceRatherThanBeingDeclared(t *testing.T) {
	root := newDiscoveryWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, launchRoot: root}

	read := func() listEntitiesResult {
		t.Helper()
		var got listEntitiesResult
		decodeStructured(t, callDiscoveryTool(t, s, "list-entities", map[string]any{"workspace_root": root}), &got)
		return got
	}
	countFor := func(r listEntitiesResult, kind string) (int, bool) {
		for _, k := range r.Kinds {
			if k.Kind == kind {
				return k.Count, true
			}
		}
		return 0, false
	}

	before := read()
	if _, present := countFor(before, "Manifest"); present {
		t.Fatal("the fixture already declares a Manifest, so planting one proves nothing about derivation")
	}

	planted := filepath.Join(root, "planted.yaml")
	if err := os.WriteFile(planted, []byte(discoveryPlantedKind), 0o644); err != nil {
		t.Fatalf("plant the document: %v", err)
	}

	after := read()
	if after.Count != before.Count+1 {
		t.Errorf("one document was added and the total went from %d to %d; a listing that does not grow with "+
			"the workspace is reporting something other than the workspace", before.Count, after.Count)
	}
	n, present := countFor(after, "Manifest")
	if !present {
		t.Fatalf("a Manifest was added and the breakdown reports no Manifest row, so the kinds are coming from "+
			"somewhere other than the documents: %+v", after.Kinds)
	}
	if n != 1 {
		t.Errorf("one Manifest was added and the breakdown reports %d", n)
	}

	if err := os.Remove(planted); err != nil {
		t.Fatalf("remove the planted document: %v", err)
	}
	restored := read()
	if restored.Count != before.Count {
		t.Errorf("the planted document was removed and the total is %d, not the %d it started at",
			restored.Count, before.Count)
	}
	if _, present := countFor(restored, "Manifest"); present {
		t.Errorf("the planted document was removed and the breakdown still reports a Manifest: %+v", restored.Kinds)
	}
}

// TestADetailedListingIsBoundedAndContinues walks a listing one record at a time
// and holds the walk to the contract the answer states: every record appears once,
// in order, and the remainder signal is what says whether to ask again.
//
// A page size of one is deliberate. It makes every boundary a boundary this walk
// actually crosses, where a page larger than the fixture would exercise the bound
// only in the case where it does nothing.
func TestADetailedListingIsBoundedAndContinues(t *testing.T) {
	root := newDiscoveryWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, launchRoot: root}

	var whole listEntitiesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-entities", map[string]any{
		"workspace_root": root, "response_format": "detailed",
	}), &whole)
	if whole.Count < 2 {
		t.Fatalf("the fixture holds %d entities; a paging walk over fewer than two crosses no boundary", whole.Count)
	}

	var walked []string
	offset, pages := 0, 0
	for {
		var page listEntitiesResult
		decodeStructured(t, callDiscoveryTool(t, s, "list-entities", map[string]any{
			"workspace_root": root, "response_format": "detailed", "limit": 1, "offset": offset,
		}), &page)
		pages++
		if page.Offset != offset {
			t.Fatalf("page %d was asked for at offset %d and reports offset %d", pages, offset, page.Offset)
		}
		if page.Returned != len(page.Entities) {
			t.Fatalf("page %d reports %d returned and carries %d", pages, page.Returned, len(page.Entities))
		}
		if page.Returned > 1 {
			t.Fatalf("page %d was bounded to one record and carries %d", pages, page.Returned)
		}
		for _, e := range page.Entities {
			walked = append(walked, e.Kind+" "+e.Name+" "+e.File)
		}
		if !page.MoreRemaining {
			break
		}
		offset += page.Returned
		if pages > whole.Count+1 {
			t.Fatal("the walk did not terminate; more_remaining stayed true past the end of the listing")
		}
	}

	if pages != whole.Count {
		t.Errorf("a listing of %d records walked one at a time took %d pages", whole.Count, pages)
	}
	if len(walked) != whole.Count {
		t.Fatalf("the walk collected %d records out of %d, so a bounded listing loses or repeats records",
			len(walked), whole.Count)
	}
	for i, e := range whole.Entities {
		want := e.Kind + " " + e.Name + " " + e.File
		if walked[i] != want {
			t.Errorf("record %d walked as %q and the unpaged listing reports %q, so the pages do not reassemble "+
				"into the listing", i, walked[i], want)
		}
	}

	t.Run("an offset past the end is an empty answer rather than a refusal", func(t *testing.T) {
		var page listEntitiesResult
		decodeStructured(t, callDiscoveryTool(t, s, "list-entities", map[string]any{
			"workspace_root": root, "response_format": "detailed", "offset": whole.Count + 10,
		}), &page)
		if page.Returned != 0 || page.MoreRemaining {
			t.Errorf("an offset past the end reported %d record(s) with more_remaining=%v",
				page.Returned, page.MoreRemaining)
		}
		if page.Count != whole.Count {
			t.Errorf("an offset past the end reports the workspace holding %d, not %d", page.Count, whole.Count)
		}
	})
}

// TestADetailedListingWithNoLimitIsStillBounded pins the property the bound
// exists for: a caller that asks for records and names no page size cannot be
// handed however many the workspace happens to hold.
//
// It reads the bound off the tool rather than restating it, so the assertion
// follows the default rather than pinning a number beside it.
func TestADetailedListingWithNoLimitIsStillBounded(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < listingPageDefault+7; i++ {
		doc := fmt.Sprintf("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: example/compute/c%03d\n  version: \"1.0.0\"\nspec:\n  schema: {}\n", i)
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("c%03d.yaml", i)), []byte(doc), 0o644); err != nil {
			t.Fatalf("write fixture %d: %v", i, err)
		}
	}
	s := &server{out: io.Discard, logw: io.Discard, launchRoot: root}

	var got listEntitiesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-entities", map[string]any{
		"workspace_root": root, "response_format": "detailed",
	}), &got)

	if got.Count != listingPageDefault+7 {
		t.Fatalf("the workspace holds %d entities and the answer reports %d", listingPageDefault+7, got.Count)
	}
	if got.Returned != listingPageDefault {
		t.Errorf("a detailed call naming no limit returned %d records; the page it is bounded to is %d",
			got.Returned, listingPageDefault)
	}
	if !got.MoreRemaining {
		t.Error("the answer carries fewer records than the workspace holds and says none remain")
	}
}

// discoveryCatalogFeature lives in a catalog directory OUTSIDE the workspace, so
// the only path it has is one on the machine the server runs on.
const discoveryCatalogFeature = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: example/telemetry
  version: "1.0.0"
  description: A Feature reachable only through the catalog search stack.
spec:
  appliesTo: "component.aggrega.ai/type=server"
`

// TestACatalogResolvedEntityReportsNoFileAndSaysWhy checks the contract sentence
// the reference page states about an entity that has no workspace document: it
// is reported, it carries no file, and it says where it came from — so an empty
// file reads as a disclosure rather than as a field nobody filled in.
//
// The Feature is placed outside the workspace root deliberately. Inside it, the
// same Feature would arrive as a workspace document with a file, and the leg
// would pass without ever exercising the path it is about.
func TestACatalogResolvedEntityReportsNoFileAndSaysWhy(t *testing.T) {
	root := newDiscoveryWorkspace(t)
	catalogDir := t.TempDir()
	featurePath := filepath.Join(catalogDir, "features", "telemetry.yaml")
	if err := os.MkdirAll(filepath.Dir(featurePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(featurePath, []byte(discoveryCatalogFeature), 0o644); err != nil {
		t.Fatalf("write feature: %v", err)
	}

	s := &server{out: io.Discard, logw: io.Discard, launchRoot: root}
	var got listEntitiesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-entities", map[string]any{
		"workspace_root":  root,
		"catalog_dirs":    []string{catalogDir},
		"response_format": "detailed",
	}), &got)

	var found bool
	for _, e := range got.Entities {
		if e.Kind != "Feature" || e.Name != "example/telemetry" {
			continue
		}
		found = true
		if e.File != "" {
			t.Errorf("a catalog-resolved Feature reported the file %q; it has no workspace document, and the "+
				"only path it has would disclose the server machine's layout", e.File)
		}
		if e.Origin == "" {
			t.Error("a catalog-resolved Feature reported no origin, so its empty file is indistinguishable " +
				"from a field the build failed to fill in")
		}
		if strings.Contains(e.Origin, catalogDir) {
			t.Errorf("the origin leaks a host path: %q", e.Origin)
		}
	}
	if !found {
		t.Fatalf("the Feature the catalog directory declares was not reported at all, so the page's claim "+
			"that catalog Features are listed is false; got %d entities", got.Count)
	}
}

// TestTheTwoListingsAgreeOnTheCoordinateAClientJoinsOn pins the property that
// makes the join mechanical rather than a matter of interpretation: a Release
// appears in both answers under one path, so a client lining the two up matches
// strings instead of reconstructing a path from one shape into the other.
func TestTheTwoListingsAgreeOnTheCoordinateAClientJoinsOn(t *testing.T) {
	root := newDiscoveryWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, launchRoot: root}

	var index listEntitiesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-entities", map[string]any{
		"workspace_root": root, "response_format": "detailed",
	}), &index)
	var releases listReleasesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-releases", map[string]any{
		"workspace_root": root, "response_format": "detailed",
	}), &releases)

	indexed := map[string]string{}
	for _, e := range index.Entities {
		if e.Kind == "Release" && e.File != "" {
			indexed[e.File] = e.Name
		}
	}

	for _, path := range releases.Releases {
		name, joined := indexed[path]
		if !joined {
			t.Errorf("the Release listing reports %q, and the entity listing reports no Release under that "+
				"path, so a client has nothing to join it against: %v", path, indexed)
			continue
		}
		if name == "" {
			t.Errorf("the entity listing joined %q to an empty name", path)
		}
	}
}

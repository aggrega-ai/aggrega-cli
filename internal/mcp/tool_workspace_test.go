// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
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
	res, rpcErr := s.handleToolsCall(params)
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
	s := &server{out: io.Discard, logw: io.Discard, startDir: t.TempDir()}
	listed, rpcErr := s.handleToolsList()
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
	s := &server{out: io.Discard, logw: io.Discard, startDir: root}

	var got listEntitiesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-entities", map[string]any{"workspace_root": root}), &got)

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

// TestTheReleaseListingIsDecidedByContentNotFilename is the same proof for the
// Release listing, and it is the half that a corpus of conventionally-named
// fixtures cannot make: the two assertions pull in opposite directions, so an
// implementation that matched on the filename fails both.
func TestTheReleaseListingIsDecidedByContentNotFilename(t *testing.T) {
	root := newDiscoveryWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, startDir: root}

	var got listReleasesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-releases", map[string]any{"workspace_root": root}), &got)

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

	if got.Count != len(got.Releases) {
		t.Errorf("the reported count %d does not match the %d paths returned", got.Count, len(got.Releases))
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
	s := &server{out: io.Discard, logw: io.Discard, startDir: root}

	var index listEntitiesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-entities", map[string]any{"workspace_root": root}), &index)

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

	s := &server{out: io.Discard, logw: io.Discard, startDir: root}
	var got listEntitiesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-entities", map[string]any{
		"workspace_root": root,
		"catalog_dirs":   []string{catalogDir},
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
	s := &server{out: io.Discard, logw: io.Discard, startDir: root}

	var index listEntitiesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-entities", map[string]any{"workspace_root": root}), &index)
	var releases listReleasesResult
	decodeStructured(t, callDiscoveryTool(t, s, "list-releases", map[string]any{"workspace_root": root}), &releases)

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

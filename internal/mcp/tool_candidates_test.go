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

	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The candidacy fixture. It is built so that a surface answering from anything
// other than the real label match would answer differently:
//
//   - example/compute/web      a Component labelled as a server, declaring one
//     dependency socket constrained to databases
//   - example/data/db          a Component labelled as a database — the only
//     legal target of that socket
//   - example/compute/plain    a Component carrying NO labels and declaring no
//     sockets: the node with nothing to offer on either half
//   - example/observability/metrics  a Feature applying to servers only
//   - example/data/backup      a Feature applying to databases only
//
// No Feature here declares an absent applicability, which is what makes the
// empty answer meaningful: an unconstrained Feature is a candidate for every
// node, so one in the fixture would make an empty candidate list unreachable.
const (
	candidacyWebComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/compute/web
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
    example.com/tier: "edge"
spec:
  usesSchema:
    database:
      selector: "component.aggrega.ai/type=database"
      required: true
      inject: ["env"]
  schema:
    infra:
      port: { type: integer, default: 8080 }
`

	candidacyDBComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/data/db
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "database"
spec:
  schema:
    provides:
      host: { type: string, required: true }
`

	candidacyPlainComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/compute/plain
  version: "1.0.0"
spec:
  schema:
    infra:
      port: { type: integer, default: 9090 }
`

	candidacyServerFeature = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: example/observability/metrics
  version: "1.0.0"
spec:
  appliesTo: "component.aggrega.ai/type=server"
  schema:
    infra:
      scrape_port: { type: integer, default: 9100 }
`

	candidacyDatabaseFeature = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: example/data/backup
  version: "1.0.0"
spec:
  appliesTo: "component.aggrega.ai/type=database"
  schema:
    infra:
      schedule: { type: string, default: "0 2 * * *" }
`
)

// newCandidacyWorkspace writes the fixture and returns its root.
func newCandidacyWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range map[string]string{
		"components/web.yaml":   candidacyWebComponent,
		"components/db.yaml":    candidacyDBComponent,
		"components/plain.yaml": candidacyPlainComponent,
		"features/metrics.yaml": candidacyServerFeature,
		"features/backup.yaml":  candidacyDatabaseFeature,
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

// candidateNames renders a candidate list as kind/name strings, so an assertion
// reads as a set rather than as a struct dump.
func candidateNames(candidates []serve.AttachCandidate) []string {
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.Kind+" "+c.Name)
	}
	return out
}

func targetNames(targets []serve.WorkspaceEntityRef) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.Kind+" "+t.Name)
	}
	return out
}

// TestAttachCandidatesAnswersEachOfItsThreeAddressingShapes drives all three
// coordinates through the real tools/call dispatch and asserts each reports a
// DIFFERENT label source.
//
// The three are asserted together rather than in three tests because the
// property under test spans them: a surface carrying two of the three passes any
// test written over those two while the third is unreachable, and the label
// source is what a caller reads to know which of the three it got. Each shape's
// candidate set is asserted too, so a shape that answered with the wrong label
// set would fail even though its provenance field was right.
func TestAttachCandidatesAnswersEachOfItsThreeAddressingShapes(t *testing.T) {
	root := newCandidacyWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: root}

	t.Run("a workspace document by name reports the labels it declares", func(t *testing.T) {
		var got attachCandidatesPayload
		decodeStructured(t, callDiscoveryTool(t, s, "attach-candidates", map[string]any{
			"workspace_root": root,
			"node":           "example/compute/web",
		}), &got)

		if got.LabelSource != serve.NodeLabelsDeclared {
			t.Errorf("labelSource = %q, want %q — a match over a document's declared labels must not be "+
				"reported as one over a build's finalised set", got.LabelSource, serve.NodeLabelsDeclared)
		}
		if got.Node != "example/compute/web" {
			t.Errorf("node = %q, want the coordinate the answer was computed for", got.Node)
		}
		if got.NodeLabels["component.aggrega.ai/type"] != "server" {
			t.Errorf("nodeLabels does not carry the labels the match ran over: %v", got.NodeLabels)
		}
		if want := []string{"Feature example/observability/metrics"}; !equalStrings(candidateNames(got.Candidates), want) {
			t.Errorf("candidates = %v, want %v — the database Feature does not apply to a server",
				candidateNames(got.Candidates), want)
		}
	})

	t.Run("a supplied label set names no node and reports itself as supplied", func(t *testing.T) {
		var got attachCandidatesPayload
		decodeStructured(t, callDiscoveryTool(t, s, "attach-candidates", map[string]any{
			"workspace_root": root,
			"labels":         map[string]string{"component.aggrega.ai/type": "database"},
		}), &got)

		if got.LabelSource != serve.NodeLabelsSupplied {
			t.Errorf("labelSource = %q, want %q — an answer computed over a caller's own label set must never "+
				"read as one this surface vouched for", got.LabelSource, serve.NodeLabelsSupplied)
		}
		if got.Node != "" {
			t.Errorf("node = %q, want empty: this call addressed no node", got.Node)
		}
		if want := []string{"Feature example/data/backup"}; !equalStrings(candidateNames(got.Candidates), want) {
			t.Errorf("candidates = %v, want %v", candidateNames(got.Candidates), want)
		}
	})

	t.Run("an empty supplied label set is a subject, not an absent argument", func(t *testing.T) {
		res := callDiscoveryTool(t, s, "attach-candidates", map[string]any{
			"workspace_root": root,
			"labels":         map[string]string{},
		})
		if res.IsError {
			t.Fatalf("an empty label set was refused as no coordinate at all: %s", res.Content[0].Text)
		}
		var got attachCandidatesPayload
		decodeStructured(t, res, &got)
		if got.LabelSource != serve.NodeLabelsSupplied {
			t.Errorf("labelSource = %q, want %q", got.LabelSource, serve.NodeLabelsSupplied)
		}
		if len(got.Candidates) != 0 {
			t.Errorf("candidates = %v, want none: no Feature here declares an applicability an unlabelled node "+
				"satisfies", candidateNames(got.Candidates))
		}
	})

	t.Run("a node on a compiled graph reports the finalised labels as resolved", func(t *testing.T) {
		fsRoot, catalogDir, releasePath := fullstackWorkspace(t)
		gs := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: fsRoot}

		var got attachCandidatesPayload
		decodeStructured(t, callDiscoveryTool(t, gs, "attach-candidates", map[string]any{
			"workspace_root": fsRoot,
			"catalog_dirs":   []string{catalogDir},
			"graph_node":     "data.main-db",
			"release":        releasePath,
		}), &got)

		if got.LabelSource != serve.NodeLabelsResolved {
			t.Fatalf("labelSource = %q, want %q — a graph node's labels are the finalised set a build "+
				"evaluates, and reporting them as anything weaker would let a caller read the stronger "+
				"answer as the weaker one", got.LabelSource, serve.NodeLabelsResolved)
		}
		// A qualified id: the node is contributed by a sub-aggregate, so this also
		// proves the ids accepted are the graph's own keys rather than anything
		// reconstructed from the authored documents.
		if got.Node != "data.main-db" {
			t.Errorf("node = %q, want the qualified graph id the request addressed", got.Node)
		}
		if len(got.NodeLabels) == 0 {
			t.Error("nodeLabels is empty, so the answer carries no basis a caller could audit it from")
		}

		// The join clause 4 asks for, and the strongest available proof that one
		// matcher serves both shapes: feed the labels this answer reported back as
		// a supplied set and require the identical candidate list. Two matchers
		// would have to agree here, and a second one written later would not.
		var viaLabels attachCandidatesPayload
		decodeStructured(t, callDiscoveryTool(t, gs, "attach-candidates", map[string]any{
			"workspace_root": fsRoot,
			"catalog_dirs":   []string{catalogDir},
			"labels":         got.NodeLabels,
		}), &viaLabels)

		if len(got.Candidates) == 0 {
			t.Fatal("the graph-node answer named no candidate, so comparing the two lists compares two empties")
		}
		if !equalStrings(candidateNames(got.Candidates), candidateNames(viaLabels.Candidates)) {
			t.Errorf("the graph-node shape and the supplied-label shape disagree over one label set:\n"+
				" by graph  = %v\n by labels = %v",
				candidateNames(got.Candidates), candidateNames(viaLabels.Candidates))
		}
		if viaLabels.LabelSource != serve.NodeLabelsSupplied {
			t.Errorf("the echoed label set was reported as %q rather than %q, so the weaker answer wears the "+
				"stronger one's provenance", viaLabels.LabelSource, serve.NodeLabelsSupplied)
		}
	})
}

// TestAttachCandidatesReportsANodeWithNoCandidates pins the case a caller most
// needs told apart from a refusal: the node resolved, the match ran, and nothing
// applies.
func TestAttachCandidatesReportsANodeWithNoCandidates(t *testing.T) {
	root := newCandidacyWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: root}

	res := callDiscoveryTool(t, s, "attach-candidates", map[string]any{
		"workspace_root": root,
		"node":           "example/compute/plain",
	})
	if res.IsError {
		t.Fatalf("a node that resolves and matches nothing was refused: %s", res.Content[0].Text)
	}
	var got attachCandidatesPayload
	decodeStructured(t, res, &got)

	if got.Node != "example/compute/plain" {
		t.Errorf("node = %q, want the node the answer is about", got.Node)
	}
	if len(got.Candidates) != 0 {
		t.Errorf("candidates = %v, want none", candidateNames(got.Candidates))
	}
	if got.LabelSource != serve.NodeLabelsDeclared {
		t.Errorf("labelSource = %q, want %q", got.LabelSource, serve.NodeLabelsDeclared)
	}
}

// TestCandidacyToolsRefuseAnUnknownSubjectRatherThanAnsweringEmpty is the
// distinction the whole surface rests on: an empty list and a refusal mean
// different things, and collapsing them would report a typo as a subject with
// nothing to offer.
//
// Both tools are driven, because the property is one decision carried onto two
// tools and a test over one would pass while the other answered empty.
func TestCandidacyToolsRefuseAnUnknownSubjectRatherThanAnsweringEmpty(t *testing.T) {
	root := newCandidacyWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: root}

	for _, tool := range []string{"attach-candidates", "connection-candidates"} {
		t.Run(tool, func(t *testing.T) {
			res := callDiscoveryTool(t, s, tool, map[string]any{
				"workspace_root": root,
				"node":           "example/compute/no-such-thing",
			})
			if !res.IsError {
				t.Fatalf("a name the workspace does not declare was answered rather than refused: %s",
					res.Content[0].Text)
			}
			text := res.Content[0].Text
			if !strings.Contains(text, "example/compute/no-such-thing") {
				t.Errorf("the refusal does not name what could not be resolved: %q", text)
			}
			if !strings.Contains(text, "list-entities") {
				t.Errorf("the refusal does not say how to obtain a name that would resolve: %q", text)
			}
		})
	}

	t.Run("an unknown graph node names the graph, not the workspace", func(t *testing.T) {
		fsRoot, catalogDir, releasePath := fullstackWorkspace(t)
		gs := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: fsRoot}
		res := callDiscoveryTool(t, gs, "attach-candidates", map[string]any{
			"workspace_root": fsRoot,
			"catalog_dirs":   []string{catalogDir},
			"graph_node":     "no-such-node",
			"release":        releasePath,
		})
		if !res.IsError {
			t.Fatalf("an id no graph node carries was answered rather than refused: %s", res.Content[0].Text)
		}
		text := res.Content[0].Text
		if !strings.Contains(text, "no-such-node") {
			t.Errorf("the refusal does not name the id it could not find: %q", text)
		}
		if strings.Contains(text, "workspace entity") {
			t.Errorf("the refusal sends the caller looking for a workspace document it never named: %q", text)
		}
	})
}

// TestConnectionCandidatesKeepsTheTwoHalvesDistinguishable is clause 2's proof:
// the target half is complete and the socket half is open, and the response lets
// a caller tell them apart.
//
// The ad-hoc row is the one that carries the requirement. A surface that refused
// a socket the entity does not declare, or reported it indistinguishably from a
// declared one, would teach an author that their freedom is narrower than it is.
func TestConnectionCandidatesKeepsTheTwoHalvesDistinguishable(t *testing.T) {
	root := newCandidacyWorkspace(t)
	s := &server{out: io.Discard, logw: io.Discard, serverVersion: "test", startDir: root}

	t.Run("a socket the entity declares carries its constraint and its complete targets", func(t *testing.T) {
		var got connectionCandidatesPayload
		decodeStructured(t, callDiscoveryTool(t, s, "connection-candidates", map[string]any{
			"workspace_root": root,
			"node":           "example/compute/web",
		}), &got)

		if len(got.Sockets) != 1 {
			t.Fatalf("sockets = %d, want the one this Component declares: %+v", len(got.Sockets), got.Sockets)
		}
		sock := got.Sockets[0]
		if sock.Socket != "database" {
			t.Errorf("socket = %q, want %q", sock.Socket, "database")
		}
		if sock.Declaration != types.SocketDeclarationDeclared {
			t.Errorf("declaration = %q, want %q", sock.Declaration, types.SocketDeclarationDeclared)
		}
		if !sock.Required {
			t.Error("required = false, and the declaration says the socket must end up bound")
		}
		if sock.Selector == "" {
			t.Error("selector is empty, so a caller cannot explain the targets from the answer alone")
		}
		if want := []string{"Component example/data/db"}; !equalStrings(targetNames(sock.Targets), want) {
			t.Errorf("targets = %v, want %v — the target half is the set the same constraint admits during "+
				"a build, not a wider one", targetNames(sock.Targets), want)
		}
		if !got.AdHocSocketsAccepted {
			t.Error("adHocSocketsAccepted = false on a Component, whose own topology its author owns; a caller " +
				"reading this would present the declared sockets as the only legal names")
		}
	})

	t.Run("a socket authored ad-hoc is reported AS ad-hoc rather than refused", func(t *testing.T) {
		var got connectionCandidatesPayload
		decodeStructured(t, callDiscoveryTool(t, s, "connection-candidates", map[string]any{
			"workspace_root": root,
			"node":           "example/compute/web",
			"socket":         "sidecar-cache",
		}), &got)

		if len(got.Sockets) != 1 {
			t.Fatalf("sockets = %d, want the one socket the request named: %+v", len(got.Sockets), got.Sockets)
		}
		sock := got.Sockets[0]
		if sock.Socket != "sidecar-cache" {
			t.Errorf("socket = %q, want the name the request asked about", sock.Socket)
		}
		if sock.Declaration != types.SocketDeclarationAdHoc {
			t.Fatalf("declaration = %q, want %q — a socket the entity does not declare that reads as declared "+
				"is the flattening this field exists to prevent", sock.Declaration, types.SocketDeclarationAdHoc)
		}
		if sock.Declaration == types.SocketDeclarationDeclared {
			t.Fatal("the ad-hoc socket is indistinguishable from a declared one")
		}
		// It declares no constraint, so every Component is a legal target: the
		// unconstrained reading is the one a build takes for the same socket.
		if len(sock.Targets) == 0 {
			t.Error("an accepted ad-hoc socket reports no target, which reads as a connection with nowhere to go")
		}
	})

	t.Run("a closed socket surface says so and offers nothing through an undeclared name", func(t *testing.T) {
		var got connectionCandidatesPayload
		decodeStructured(t, callDiscoveryTool(t, s, "connection-candidates", map[string]any{
			"workspace_root": root,
			"node":           "example/observability/metrics",
			"socket":         "anything",
		}), &got)

		if got.AdHocSocketsAccepted {
			t.Fatal("a Feature's attachment surface is reported as accepting an undeclared socket; an " +
				"attachment participates in the topology the Feature declares rather than authoring one")
		}
		if len(got.Sockets) != 1 {
			t.Fatalf("sockets = %d, want the one the request named", len(got.Sockets))
		}
		if n := len(got.Sockets[0].Targets); n != 0 {
			t.Errorf("targets = %d on a socket the surface will not accept, so the answer offers a connection "+
				"the build refuses outright", n)
		}
	})

	t.Run("an entity declaring no socket answers with an empty list rather than a refusal", func(t *testing.T) {
		res := callDiscoveryTool(t, s, "connection-candidates", map[string]any{
			"workspace_root": root,
			"node":           "example/compute/plain",
		})
		if res.IsError {
			t.Fatalf("an entity that resolves and declares no socket was refused: %s", res.Content[0].Text)
		}
		var got connectionCandidatesPayload
		decodeStructured(t, res, &got)
		if len(got.Sockets) != 0 {
			t.Errorf("sockets = %+v, want none", got.Sockets)
		}
		if got.Sockets == nil {
			t.Error("sockets is null on the wire rather than an empty array, so a client must nil-check it")
		}
	})
}

// TestCandidacyToolsStateThatTheyCreateNothing is clause 5 on the surface a
// caller actually reads. An assistant is exactly the caller that would otherwise
// expect a proposed edit, and the neighbouring capability that would have written
// source was retired on that ground.
//
// It asserts the tool DESCRIPTIONS, which is what a client loads and shows,
// rather than only the prose in this package.
func TestCandidacyToolsStateThatTheyCreateNothing(t *testing.T) {
	s := newTestServer(t)
	for _, tc := range []struct{ tool, creates string }{
		{"attach-candidates", "creates no attachment"},
		{"connection-candidates", "creates no edge"},
	} {
		spec, found := s.lookupTool(tc.tool)
		if !found {
			t.Fatalf("%s is not registered; the assertion measures nothing", tc.tool)
		}
		if !strings.Contains(spec.description, tc.creates) {
			t.Errorf("%s does not say it %s, so an assistant may expect a proposed edit: %q",
				tc.tool, tc.creates, spec.description)
		}
		if !strings.Contains(spec.description, "proposes no text") {
			t.Errorf("%s does not say it proposes no text", tc.tool)
		}
	}
}

// TestConnectionCandidatesSaysItHasNoCoordinateFreeForm pins the asymmetry a
// caller meets after learning the label-set shape on the other tool: it will look
// for one here, and there is none.
func TestConnectionCandidatesSaysItHasNoCoordinateFreeForm(t *testing.T) {
	s := newTestServer(t)
	spec, found := s.lookupTool("connection-candidates")
	if !found {
		t.Fatal("connection-candidates is not registered")
	}
	if !strings.Contains(spec.description, "no coordinate-free form") &&
		!strings.Contains(spec.description, "NO coordinate-free form") {
		t.Errorf("the description does not say the coordinate-free form the attach half offers is absent "+
			"here: %q", spec.description)
	}
	var declared struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(spec.inputSchema), &declared); err != nil {
		t.Fatalf("input schema does not parse: %v", err)
	}
	if _, ok := declared.Properties["labels"]; ok {
		t.Error("connection-candidates declares a labels argument, so the description's statement is false")
	}
}

// TestBothCandidacyToolsAreDiscoverableAndInvocable pins the deferred placement:
// each is surfaced by the capability tool for the kinds an author works in, and
// invocable by name through the same dispatch as a curated tool.
//
// The pair is what matters. Clause 0's ruling is that the two halves ship
// together, so a surface carrying one of them is a failure rather than a partial
// success, and this is the assertion that would catch it.
func TestBothCandidacyToolsAreDiscoverableAndInvocable(t *testing.T) {
	s := newTestServer(t)

	surfaced := map[string]bool{}
	for _, spec := range s.deferredTools() {
		surfaced[spec.name] = true
	}
	for _, want := range []string{"attach-candidates", "connection-candidates"} {
		if !surfaced[want] {
			t.Errorf("%s is in neither the default listing nor the deferred long tail, so no caller can "+
				"discover it", want)
		}
		if _, ok := s.lookupTool(want); !ok {
			t.Errorf("%s cannot be resolved by name, so discovering it would not make it callable", want)
		}
	}

	for _, kind := range []string{"component", "feature", "aggregate"} {
		named := map[string]bool{}
		for _, c := range s.capabilitiesForKind(kind) {
			named[c.Name] = true
		}
		for _, want := range []string{"attach-candidates", "connection-candidates"} {
			if !named[want] {
				t.Errorf("describe-capabilities(kind=%q) does not surface %q, so an author working on that "+
					"kind is not offered it", kind, want)
			}
		}
	}
}

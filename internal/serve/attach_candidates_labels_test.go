// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// These tests cover the coordinate that names nothing: a request supplying a
// label set outright, which the daemon matches without resolving anything.
//
// They run against the same fixture the graph-node tests use, because the
// property worth proving is a comparison between the three coordinates rather
// than anything about one of them alone.

// attachRaw drives one aggrega/attachCandidates request whose params are an
// arbitrary object, so a shape the typed request struct cannot express — a
// label value that is not a string — reaches the same decode production runs.
func attachRaw(t *testing.T, srv *Server, params map[string]any) jsonRPCResponse {
	t.Helper()
	in := bytes.NewBuffer(framedRequest(1, "aggrega/attachCandidates", params))
	out := &bytes.Buffer{}
	return runOneRequest(t, srv, in, out)
}

func equalLabels(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestASuppliedLabelSetAnswersAsTheGraphNodeDoesAndDiffersFromTheDeclaredAnswer
// is the reason the coordinate exists rather than a convenience test.
//
// The fixture's graph node "checkout" and the workspace document "checkout"
// are different subjects carrying different labels, so the daemon has two
// genuinely different label sets for one string. The finalised one is what a
// build evaluates applicability against; the declared one is what a read over
// source can see. A client holding a rendered graph holds the finalised set,
// and this asserts that handing it back reaches the stronger answer without
// naming anything and without rebuilding the graph.
//
// The supplied set is taken from the daemon's own graph-node response rather
// than hand-written here. That is deliberate on two counts: it is the exact
// client behaviour the method documents as the one that keeps a client and the
// daemon agreeing, and a hand-written map would let the test pass while the
// graph reported something else entirely.
func TestASuppliedLabelSetAnswersAsTheGraphNodeDoesAndDiffersFromTheDeclaredAnswer(t *testing.T) {
	ws, features, rel := graphNodeWorkspace(t)
	dirs := []string{filepath.Join(ws, "catalog"), features}

	byGraph := decodeAttachResult(t, attachRequest(t, graphNodeServer(t, ws, dirs...),
		attachCandidatesParams{GraphNode: "checkout", Release: rel}))
	byName := decodeAttachResult(t, attachRequest(t, graphNodeServer(t, ws, dirs...),
		attachCandidatesParams{Node: "checkout"}))

	// Guard the fixture's load-bearing property. If the two label sets ever
	// coincide, everything below still passes while proving nothing about the
	// difference between a finalised set and a declared one.
	if equalLabels(byGraph.NodeLabels, byName.NodeLabels) {
		t.Fatalf("the graph node and the document named checkout now carry the same labels (%+v), "+
			"so this test can no longer show the finalised set outranking the declared one",
			byGraph.NodeLabels)
	}

	supplied := decodeAttachResult(t, attachRequest(t, graphNodeServer(t, ws, dirs...),
		attachCandidatesParams{Labels: byGraph.NodeLabels}))

	if supplied.LabelSource != NodeLabelsSupplied {
		t.Errorf("labelSource = %q, want %q — a caller's own label set must never be "+
			"reported as one the daemon read", supplied.LabelSource, NodeLabelsSupplied)
	}
	if supplied.Node != "" {
		t.Errorf("node = %q, want empty — this request addressed no node", supplied.Node)
	}
	if !equalLabels(supplied.NodeLabels, byGraph.NodeLabels) {
		t.Errorf("nodeLabels = %+v, want the set that was sent %+v; the response must echo "+
			"what the match was evaluated against", supplied.NodeLabels, byGraph.NodeLabels)
	}

	suppliedCands := refNames(supplied.Candidates)
	if !equalStrings(suppliedCands, refNames(byGraph.Candidates)) {
		t.Errorf("supplied-labels candidates = %v, want the graph-node answer %v",
			suppliedCands, refNames(byGraph.Candidates))
	}
	if equalStrings(suppliedCands, refNames(byName.Candidates)) {
		t.Fatalf("the supplied-labels answer and the declared answer are both %v, so this "+
			"fixture no longer demonstrates that the finalised set reaches a different answer",
			suppliedCands)
	}

	// The other direction: the parameter carries whatever set it is handed
	// faithfully, so sending the declared set reaches the declared answer.
	// Without this, the assertions above are also satisfied by a path that
	// ignored the parameter and rebuilt the graph.
	suppliedDeclared := decodeAttachResult(t, attachRequest(t, graphNodeServer(t, ws, dirs...),
		attachCandidatesParams{Labels: byName.NodeLabels}))
	if got := refNames(suppliedDeclared.Candidates); !equalStrings(got, refNames(byName.Candidates)) {
		t.Errorf("supplying the declared label set answered %v, want the by-name answer %v; "+
			"the match must be a function of the label set that was sent",
			got, refNames(byName.Candidates))
	}
}

// TestASuppliedLabelSetDistinguishesAnEmptyAnswerFromARefusal is the
// prove-it-can-fail half.
//
// A label set nothing admits comes back as a successful response carrying an
// empty array. That is honest under this coordinate and under no other: the
// name-addressed paths refuse an unknown coordinate because an empty list
// would report a typo as a node with nothing to offer, and there is no name
// here to have typed wrongly. A malformed request still refuses, and the two
// outcomes are told apart by the envelope rather than by reading the message.
func TestASuppliedLabelSetDistinguishesAnEmptyAnswerFromARefusal(t *testing.T) {
	ws, features, _ := graphNodeWorkspace(t)
	dirs := []string{filepath.Join(ws, "catalog"), features}

	answered := []struct {
		name   string
		labels map[string]string
	}{
		// The empty label set is a subject, not an absent coordinate: a node
		// carrying no labels. Every Feature in the fixture declares a
		// requirement, so nothing admits it.
		{"an empty label set", map[string]string{}},
		{"a label set nothing declares", map[string]string{"tier": "nothing-matches-this"}},
	}
	for _, c := range answered {
		t.Run(c.name, func(t *testing.T) {
			resp := attachRequest(t, graphNodeServer(t, ws, dirs...),
				attachCandidatesParams{Labels: c.labels})
			if resp.Error != nil {
				t.Fatalf("refused rather than answered: %+v", resp.Error)
			}
			result := decodeAttachResult(t, resp)
			if len(result.Candidates) != 0 {
				t.Errorf("candidates = %v, want none", refNames(result.Candidates))
			}
			if result.LabelSource != NodeLabelsSupplied {
				t.Errorf("labelSource = %q, want %q", result.LabelSource, NodeLabelsSupplied)
			}
			// An empty array rather than null, so a client iterates without a
			// nil check — and so the empty answer is a value on the wire
			// rather than an absence a client has to interpret.
			var wire map[string]json.RawMessage
			if err := json.Unmarshal(resp.Result, &wire); err != nil {
				t.Fatalf("unmarshal result: %v (body: %s)", err, resp.Result)
			}
			if got := string(wire["candidates"]); got != "[]" {
				t.Errorf("candidates on the wire = %s, want []", got)
			}
		})
	}

	refused := []struct {
		name   string
		params map[string]any
	}{
		// Malformed: a label value that is not a string. The request never
		// reaches the match, so the answer is a refusal rather than an empty
		// list computed over a set the daemon could not read.
		{"a non-string label value", map[string]any{"labels": map[string]any{"tier": 5}}},
		// Malformed: a label set that is not an object at all.
		{"a label set that is not an object", map[string]any{"labels": "tier=edge"}},
		// No coordinate: absent and null are both "nothing was addressed",
		// which is what makes the empty OBJECT above a coordinate.
		{"no coordinate at all", map[string]any{}},
		{"an explicitly null label set", map[string]any{"labels": nil}},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			resp := attachRaw(t, graphNodeServer(t, ws, dirs...), c.params)
			if resp.Error == nil {
				t.Fatalf("accepted: %s", resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("error code = %d, want %d (%+v)",
					resp.Error.Code, jsonRPCInvalidParams, resp.Error)
			}
			if len(resp.Result) != 0 {
				t.Errorf("a refusal carried a result body %s; the two answers must not "+
					"be distinguishable only by reading the message", resp.Result)
			}
		})
	}
}

// TestASuppliedLabelSetIsRefusedAlongsideAnotherCoordinate pins that the
// coordinates stay mutually exclusive as a third one arrives. Ranking them
// silently would answer a question the client did not ask.
func TestASuppliedLabelSetIsRefusedAlongsideAnotherCoordinate(t *testing.T) {
	ws, features, rel := graphNodeWorkspace(t)
	dirs := []string{filepath.Join(ws, "catalog"), features}
	labels := map[string]string{"tier": "edge"}

	cases := []struct {
		name   string
		params attachCandidatesParams
	}{
		{"a label set and a workspace entity", attachCandidatesParams{Node: "checkout", Labels: labels}},
		{"a label set and a graph node", attachCandidatesParams{GraphNode: "checkout", Release: rel, Labels: labels}},
		{"all three", attachCandidatesParams{Node: "checkout", GraphNode: "checkout", Release: rel, Labels: labels}},
		{"a label set with a release", attachCandidatesParams{Release: rel, Labels: labels}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := attachRequest(t, graphNodeServer(t, ws, dirs...), c.params)
			if resp.Error == nil {
				t.Fatalf("accepted: %s", resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("error code = %d, want %d (%+v)",
					resp.Error.Code, jsonRPCInvalidParams, resp.Error)
			}
			if !bytes.Contains([]byte(resp.Error.Message), []byte("params.labels")) {
				t.Errorf("refusal %q does not name the label-set coordinate, so a client "+
					"that sent two cannot tell which two", resp.Error.Message)
			}
		})
	}

	// Positive control for the four rows above: the label set ALONE is
	// accepted by the same harness, so a green there reflects the arity check
	// rather than a coordinate nothing can reach.
	resp := attachRequest(t, graphNodeServer(t, ws, dirs...), attachCandidatesParams{Labels: labels})
	if resp.Error != nil {
		t.Fatalf("a label set on its own was refused, so the refusals above prove nothing: %+v", resp.Error)
	}
}

// TestASuppliedLabelSetBuildsNoGraphAndNeedsNoLockfile pins that the
// coordinate resolves nothing. The graph-node coordinate refuses when the
// lockfile is absent, because building a graph means resolving what a Release
// reaches; supplying a label set crosses no such precondition, and asserting
// it against a workspace with the lockfile removed is what makes "it looks
// nothing up" observable rather than merely stated.
func TestASuppliedLabelSetBuildsNoGraphAndNeedsNoLockfile(t *testing.T) {
	ws, features, rel := graphNodeWorkspace(t)
	dirs := []string{filepath.Join(ws, "catalog"), features}

	if err := os.Remove(lockfile.PathFor(filepath.Join(ws, rel))); err != nil {
		t.Fatalf("remove lockfile: %v", err)
	}

	// The control: the graph-node coordinate DOES refuse on this workspace, so
	// the acceptance below reflects the absence of a graph build rather than a
	// precondition that stopped applying.
	graphResp := attachRequest(t, graphNodeServer(t, ws, dirs...),
		attachCandidatesParams{GraphNode: "checkout", Release: rel})
	if graphResp.Error == nil || graphResp.Error.Code != errLockfileAbsent {
		t.Fatalf("the graph-node coordinate did not refuse with the lockfile removed, so this "+
			"test proves nothing about the label-set coordinate: %+v", graphResp.Error)
	}

	result := decodeAttachResult(t, attachRequest(t, graphNodeServer(t, ws, dirs...),
		attachCandidatesParams{Labels: map[string]string{"tier": "edge"}}))
	if got := refNames(result.Candidates); !equalStrings(got, []string{"example/edge-tls", "example/observability"}) {
		t.Errorf("candidates = %v, want [example/edge-tls example/observability]", got)
	}
}

// TestASuppliedLabelSetIsByteStableAcrossRepeatedRequests pins the
// determinism invariant across the one place map-iteration order could reach
// the answer: the label set arrives as a JSON object, which decodes into a Go
// map whose iteration order permutes per run.
func TestASuppliedLabelSetIsByteStableAcrossRepeatedRequests(t *testing.T) {
	ws, features, _ := graphNodeWorkspace(t)
	dirs := []string{filepath.Join(ws, "catalog"), features}
	labels := map[string]string{
		"tier":                      "edge",
		"component.aggrega.ai/type": "server",
		"zone":                      "eu",
		"team":                      "platform",
		"stage":                     "prod",
	}

	srv := graphNodeServer(t, ws, dirs...)
	first := attachRequest(t, srv, attachCandidatesParams{Labels: labels})
	if first.Error != nil {
		t.Fatalf("attachCandidates errored: %+v", first.Error)
	}
	for i := 2; i <= 12; i++ {
		again := attachRequest(t, graphNodeServer(t, ws, dirs...),
			attachCandidatesParams{Labels: labels})
		if again.Error != nil {
			t.Fatalf("request %d errored: %+v", i, again.Error)
		}
		if !bytes.Equal(first.Result, again.Result) {
			t.Fatalf("request %d returned different bytes\nfirst: %s\nagain: %s",
				i, first.Result, again.Result)
		}
	}
}

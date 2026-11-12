// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// twoWorkspaces lays out two workspaces whose contents are DISJOINT in all
// three ways a request could leak between them: the entities each declares, the
// Release documents each holds, and the file each has diagnostics about.
//
// Disjointness is what makes leakage detectable. Two workspaces sharing an
// entity name would answer alike whether the daemon kept them apart or not, so
// the assertions below would pass on the anchored implementation this change
// replaces.
func twoWorkspaces(t *testing.T) (rootA, rootB string) {
	t.Helper()
	write := func(root string, files map[string]string) {
		for rel, content := range files {
			abs := filepath.Join(root, rel)
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", rel, err)
			}
			if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", rel, err)
			}
		}
	}
	rootA, rootB = t.TempDir(), t.TempDir()
	write(rootA, map[string]string{
		"alpha-component.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: alpha-only
`,
		"releases/alpha.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: alpha-release
`,
	})
	write(rootB, map[string]string{
		"beta-component.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: beta-only
`,
		"releases/beta.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: beta-release
`,
	})
	return rootA, rootB
}

// twoWorkspacesWithABrokenRelease is twoWorkspaces with each workspace's single
// Release document made unloadable.
//
// It exists because the PUSH path needs a request that actually pushes, and a
// validation is not one: a validation returns its findings on its own response.
// What pushes without a lockfile or a render is the default-Release query, which
// publishes a diagnostic on any Release the cascade found and could not load —
// so one unloadable Release per workspace is the cheapest fixture that makes a
// real push observable.
func twoWorkspacesWithABrokenRelease(t *testing.T) (rootA, rootB, relA, relB string) {
	t.Helper()
	rootA, rootB = twoWorkspaces(t)
	relA, relB = filepath.Join("releases", "alpha.yaml"), filepath.Join("releases", "beta.yaml")
	// The envelope has to PARSE and the load has to FAIL, and the two halves are
	// why this document looks so nearly valid. The candidate walk selects by
	// reading each file's own `kind:`, so a document no YAML parser will read is
	// not found at all — it is not a broken Release, it is not a Release — and the
	// cascade then reports zero candidates rather than one that failed. So the
	// document is well-formed YAML declaring a Release, and it is the schema that
	// refuses it: `aggregate` names a document, and a number names none.
	const broken = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: broken
spec:
  aggregate: 42
`
	for root, rel := range map[string]string{rootA: relA, rootB: relB} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(broken), 0o644); err != nil {
			t.Fatalf("write the broken Release under %s: %v", root, err)
		}
	}
	return rootA, rootB, relA, relB
}

// scriptedSession drives one daemon through its real dispatch loop over the
// framed request script, and returns every frame it wrote in wire order.
//
// It is the whole loop rather than a per-request harness on purpose: the
// interleaving proof is about one process answering successive requests, so a
// helper that ran each request in isolation would not be able to observe the
// thing under test.
func scriptedSession(t *testing.T, launchRoot string, script [][]byte) []wireFrame {
	t.Helper()
	var stdin, stdout bytes.Buffer
	for _, frame := range script {
		stdin.Write(frame)
	}
	stdin.Write(framedNotification("aggrega/shutdown", nil))

	srv := newPoolTestServerAt(t, launchRoot, &stdin, &stdout, 1, nil)
	if err := srv.dispatchLoop(context.Background(), 5*time.Second); err != nil {
		t.Fatalf("dispatchLoop: %v", err)
	}

	var frames []wireFrame
	r := bufio.NewReader(&stdout)
	for {
		body, err := readMessage(r)
		if err != nil {
			break
		}
		var f wireFrame
		if err := json.Unmarshal(body, &f); err != nil {
			t.Fatalf("unmarshal frame: %v (body %s)", err, body)
		}
		frames = append(frames, f)
	}
	return frames
}

// resultOf returns the decoded result of the response frame for id, failing when
// that frame carried an error envelope or never arrived.
func resultOf(t *testing.T, frames []wireFrame, id string, into any) {
	t.Helper()
	for _, f := range frames {
		if f.Method != "" || string(f.ID) != id {
			continue
		}
		if f.Error != nil {
			t.Fatalf("id %s carried an error envelope: %d %s", id, f.Error.Code, f.Error.Message)
		}
		if err := json.Unmarshal(f.Result, into); err != nil {
			t.Fatalf("id %s: decode result: %v (%s)", id, err, f.Result)
		}
		return
	}
	t.Fatalf("no response frame for id %s among %d frames", id, len(frames))
}

// errorOf returns the error envelope of the response frame for id, failing when
// that frame carried a result instead.
func errorOf(t *testing.T, frames []wireFrame, id string) (int, string) {
	t.Helper()
	for _, f := range frames {
		if f.Method != "" || string(f.ID) != id {
			continue
		}
		if f.Error == nil {
			t.Fatalf("id %s was answered rather than refused: %s", id, f.Result)
		}
		return f.Error.Code, f.Error.Message
	}
	t.Fatalf("no response frame for id %s among %d frames", id, len(frames))
	return 0, ""
}

// entityNames returns the sorted entity names of a catalog-index result, which
// is the exact set an assertion here compares against — not a count, and not a
// substring of a rendered payload.
func entityNames(result catalogIndexResult) []string {
	out := make([]string, 0, len(result.Entities))
	for _, e := range result.Entities {
		out = append(out, e.Name)
	}
	sort.Strings(out)
	return out
}

// TestOneDaemonAnswersTwoWorkspacesInterleaved is the load-bearing proof that
// the workspace is a parameter rather than a property of the process.
//
// 🔴 The requests are INTERLEAVED, not sequential, and that is the whole design
// of the test. A sequential script — everything about A, then everything about B
// — passes on an implementation that silently re-anchors itself on each call,
// which is the session model wearing a parameter. Interleaving means an
// implementation holding one workspace has to answer at least one request from
// the wrong one.
//
// The final request repeats the first, which is what separates "keyed" from
// "cleared": an implementation that replaced its state whenever a different
// workspace was named would answer the repeat correctly only by rebuilding, and
// the warm-cache assertion below is what detects that. Here the repeat's job is
// narrower and still necessary — it proves B's arrival did not corrupt A's
// answer.
//
// Every expectation is the fixture's own literal input. Nothing is computed by
// calling the code under test, so an assertion cannot agree with a defect on
// both sides.
func TestOneDaemonAnswersTwoWorkspacesInterleaved(t *testing.T) {
	t.Parallel()
	rootA, rootB := twoWorkspaces(t)

	frames := scriptedSession(t, rootA, [][]byte{
		framedRequestAt(t, rootA, 1, "aggrega/catalogIndex", map[string]any{}),
		framedRequestAt(t, rootB, 2, "aggrega/catalogIndex", map[string]any{}),
		framedRequestAt(t, rootA, 3, "aggrega/releaseCandidates", map[string]any{}),
		framedRequestAt(t, rootB, 4, "aggrega/releaseCandidates", map[string]any{}),
		framedRequestAt(t, rootA, 5, "aggrega/catalogIndex", map[string]any{}),
	})

	// Entities: each workspace reports its own and only its own. The index spans
	// every addressable document, so each workspace's Release is named here
	// alongside its Component — which is what makes the two sets disjoint at two
	// entries each rather than one.
	for _, tc := range []struct {
		id   string
		want []string
	}{
		{id: "1", want: []string{"alpha-only", "alpha-release"}},
		{id: "2", want: []string{"beta-only", "beta-release"}},
		{id: "5", want: []string{"alpha-only", "alpha-release"}},
	} {
		var got catalogIndexResult
		resultOf(t, frames, tc.id, &got)
		if names := entityNames(got); !equalStrings(names, tc.want) {
			t.Errorf("id %s: entities = %v; want exactly %v", tc.id, names, tc.want)
		}
	}

	// Releases: each workspace reports its own document, by absolute path under
	// its own root. Comparing the whole set rather than checking for a member is
	// what catches a leak — a union of both workspaces' Releases contains the
	// expected member and is still wrong.
	for _, tc := range []struct {
		id   string
		root string
		rel  string
	}{
		{id: "3", root: rootA, rel: "releases/alpha.yaml"},
		{id: "4", root: rootB, rel: "releases/beta.yaml"},
	} {
		var got releaseCandidatesResult
		resultOf(t, frames, tc.id, &got)
		want := []string{filepath.Join(tc.root, tc.rel)}
		if !equalStrings(got.Releases, want) {
			t.Errorf("id %s: releases = %v; want exactly %v", tc.id, got.Releases, want)
		}
	}
}

// TestASecondWorkspaceDoesNotCoolTheFirstsCache is the keyed-not-cleared proof.
//
// The parse cache is what makes it observable. A validation is the only method
// that seeds it, and the cached-diagnostics request reads it and refuses with
// the file-not-validated code when the entry is absent — so "still warm" is an
// exact, falsifiable property of the wire rather than a timing measurement.
//
// The script validates a file in A, then names B, then asks A for the cached
// answer. An implementation that replaced its state on the workspace change
// refuses the third request; a keyed one answers it.
//
// The fourth request is the other half, and it is the leakage assertion: the
// same file asked about under B must be refused, because B was never told
// anything about it. Without that, a daemon that ignored the parameter entirely
// and kept one shared cache would pass the first three.
func TestASecondWorkspaceDoesNotCoolTheFirstsCache(t *testing.T) {
	t.Parallel()
	rootA, rootB := twoWorkspaces(t)
	const relA = "alpha-component.yaml"
	contentA, err := os.ReadFile(filepath.Join(rootA, relA))
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}

	frames := scriptedSession(t, rootA, [][]byte{
		framedRequestAt(t, rootA, 1, "aggrega/validate", map[string]any{
			"file": relA, "content": string(contentA),
		}),
		framedRequestAt(t, rootB, 2, "aggrega/catalogIndex", map[string]any{}),
		framedRequestAt(t, rootA, 3, "aggrega/diagnostics", map[string]any{"file": relA}),
		framedRequestAt(t, rootB, 4, "aggrega/diagnostics", map[string]any{"file": relA}),
	})

	// The seeding request answered as a validation, and as the narrower of the two
	// validations: it named no Release, so the reply says which answer it is. That
	// is asserted rather than assumed, because a seeding call that was refused
	// would leave the two reads below proving nothing about keying.
	var seeded validateResult
	resultOf(t, frames, "1", &seeded)
	if seeded.Checked != checkedDocument {
		t.Fatalf("the seeding validation reported checked = %q; want %q, so the parse cache was "+
			"seeded by the answer this test rests on", seeded.Checked, checkedDocument)
	}

	// A's entry survived B being named: the cached read is ANSWERED rather than
	// refused, which is the whole of the keyed-not-cleared property. resultOf
	// fails the test on an error envelope, so reaching this line is the assertion.
	var cached diagnosticsResult
	resultOf(t, frames, "3", &cached)

	// B was never told about the file, so it has nothing cached for it. This is
	// the direction that fails on a daemon sharing one cache across workspaces.
	code, message := errorOf(t, frames, "4")
	if code != errFileNotValidated {
		t.Errorf("the same file under a workspace that never validated it drew code %d (%s); "+
			"want %d, since a cache entry belongs to the workspace that seeded it",
			code, message, errFileNotValidated)
	}
}

// TestPushesAreConfinedToTheWorkspaceDeclaredAtConnect is the push-model proof,
// and it asserts BOTH directions in one session because they are one rule.
//
// The client declares A at the handshake. A validation in A therefore pushes,
// and a validation in B answers in full and pushes nothing — so an assistant
// asking about a second workspace cannot fill this reader's problem list with
// findings about files they never opened.
//
// 🔴 With a single connected client this is indistinguishable from pushing
// unconditionally, which is the point rather than a weakness: that equivalence
// is what makes the change invisible to the one existing consumer. What makes
// the assertion discriminating anyway is the SECOND workspace, which no
// single-client session would ever name.
//
// The assertion is an exact frame count and an exact file identity, not a
// presence check. A push-everything implementation emits two notifications and
// still contains the expected one, so "the notification naming A's file is
// present" would pass on the behaviour this rule forbids.
func TestPushesAreConfinedToTheWorkspaceDeclaredAtConnect(t *testing.T) {
	t.Parallel()
	rootA, rootB, relA, relB := twoWorkspacesWithABrokenRelease(t)

	frames := scriptedSession(t, rootA, [][]byte{
		framedRequest(1, "aggrega/initialize", map[string]any{
			"capabilities":  map[string]any{},
			"workspaceRoot": rootA,
		}),
		framedRequestAt(t, rootA, 2, "aggrega/activeRelease", map[string]any{}),
		framedRequestAt(t, rootB, 3, "aggrega/activeRelease", map[string]any{}),
	})

	want := []string{filepath.Join(rootA, relA)}
	if got := pushedFiles(t, frames); !equalStrings(got, want) {
		t.Errorf("pushed notifications named %v; want exactly %v — the declared workspace pushes "+
			"and no other one does", got, want)
	}

	// The other half, and the one a suppression rule would get wrong: the request
	// about the workspace nothing was declared for is answered in FULL. Its
	// account of the exhausted cascade names its OWN broken Release, so the
	// findings reached the caller on its own response rather than being withheld
	// along with the push.
	var answeredB activeReleaseResult
	resultOf(t, frames, "3", &answeredB)
	if answeredB.Release != "" {
		t.Errorf("the undeclared workspace resolved a Release (%q); the fixture's only Release "+
			"cannot be loaded, so the answer must be an empty path with an account",
			answeredB.Release)
	}
	if !strings.Contains(answeredB.Trace, relB) {
		t.Errorf("the undeclared workspace's answer does not account for its own Release %q; "+
			"trace was %q — the rule confines the PUSH, never the findings", relB, answeredB.Trace)
	}
	if strings.Contains(answeredB.Trace, relA) {
		t.Errorf("the undeclared workspace's answer names the OTHER workspace's Release %q: %q",
			relA, answeredB.Trace)
	}
}

// pushedFiles returns the file each publishDiagnostics notification in frames
// named, in wire order.
func pushedFiles(t *testing.T, frames []wireFrame) []string {
	t.Helper()
	var out []string
	for _, f := range frames {
		if f.Method != "aggrega/publishDiagnostics" {
			continue
		}
		var params publishDiagnosticsParams
		if err := json.Unmarshal(f.Params, &params); err != nil {
			t.Fatalf("decode a pushed notification: %v (%s)", err, f.Params)
		}
		out = append(out, params.File)
	}
	return out
}

// TestARequestNamingNoWorkspaceIsRefused pins the refusal, in each of the four
// shapes a bad declaration takes.
//
// 🔴 Every case goes through the VERBATIM helper, which puts the test's bytes on
// the wire as written. The ordinary helpers supply the parameter for a method
// that needs one, so a refusal driven through them could not fail: the thing
// being refused would have been supplied on the way in.
func TestARequestNamingNoWorkspaceIsRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	notADirectory := filepath.Join(root, "a-file.yaml")
	if err := os.WriteFile(notADirectory, []byte("x: 1\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, tc := range []struct {
		name   string
		params map[string]any
		names  string
	}{
		{
			name:   "the parameter is absent",
			params: map[string]any{},
			names:  "`workspaceRoot` parameter is required",
		},
		{
			name:   "the parameter is the empty string",
			params: map[string]any{"workspaceRoot": ""},
			names:  "`workspaceRoot` parameter is required",
		},
		{
			name:   "the parameter is relative",
			params: map[string]any{"workspaceRoot": "some/where"},
			names:  "must be an absolute path",
		},
		{
			name:   "the parameter names something that is not a directory",
			params: map[string]any{"workspaceRoot": notADirectory},
			names:  "is not a directory",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := newTestSession(t, root, bytes.NewReader(nil), &bytes.Buffer{})
			in := bytes.NewBuffer(framedRequest(1, "aggrega/catalogIndex", tc.params))
			out := &bytes.Buffer{}
			resp := runOneRequestVerbatim(t, sess, in, out)
			if resp.Error == nil {
				t.Fatalf("the request was answered rather than refused: %s", resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("code = %d; want %d, the request being at fault rather than the workspace",
					resp.Error.Code, jsonRPCInvalidParams)
			}
			if !strings.Contains(resp.Error.Message, tc.names) {
				t.Errorf("the refusal does not say what to send: got %q, want it to contain %q",
					resp.Error.Message, tc.names)
			}
		})
	}
}

// TestTheWorkspaceFreeMethodsAreExactlyTheThreeDeclared holds the
// workspace-free set to the three methods whose subject is genuinely not a
// workspace.
//
// The set matters more than its size, so the assertion is a set difference that
// NAMES what moved in either direction. A count would agree with itself when one
// method was added and another dropped, and the direction that costs most is the
// silent one: a workspace-acting method marked free answers from an empty
// workspace and reports an empty result, which reads exactly like a workspace
// with nothing in it.
//
// The expectation is spelled out here rather than derived from the table, so the
// two can disagree. An expectation read from the same rows the routing reads
// would move with them and could never fail.
func TestTheWorkspaceFreeMethodsAreExactlyTheThreeDeclared(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"aggrega/initialize":     "it declares a workspace rather than acting on one",
		"aggrega/getSchema":      "its answer is derived from the compiled-in schema set",
		"aggrega/scaffoldEntity": "its answer is derived from the compiled-in schema set",
	}

	got := map[string]bool{}
	for method, route := range methodIndex {
		if route.workspaceFree {
			got[method] = true
		}
	}
	for method := range got {
		if _, expected := want[method]; !expected {
			t.Errorf("%q is routed without a workspace, and its subject is not one of the three "+
				"that genuinely is not a workspace; such a method answers from an empty workspace "+
				"and reports an empty result", method)
		}
	}
	for method, why := range want {
		if !got[method] {
			t.Errorf("%q now requires a workspace, but %s — so a client is asked for a value the "+
				"answer does not read", method, why)
		}
	}
}

// TestTheDaemonHoldsNoWorkspaceUntilOneIsNamed pins the structural half of the
// shape: the long-lived object carries no workspace, and each named one is
// installed on first use and reused afterwards.
//
// It reads the registry directly, which is what makes it a statement about what
// the daemon HOLDS rather than about what it answers. The answers are the
// interleaving proof's job; this is the property that makes those answers
// impossible to get wrong by accident.
func TestTheDaemonHoldsNoWorkspaceUntilOneIsNamed(t *testing.T) {
	t.Parallel()
	rootA, rootB := twoWorkspaces(t)

	// A session built with no launch resolution holds nothing at all — the shape
	// the process itself has before its own launch workspace is installed.
	bare := &session{workspaces: newWorkspaceRegistry()}
	if roots := bare.workspaces.roots(); len(roots) != 0 {
		t.Fatalf("a session holds %v before any workspace is named; want none", roots)
	}

	sess := newTestSession(t, rootA, bytes.NewReader(nil), &bytes.Buffer{})
	if roots := sess.workspaces.roots(); len(roots) != 0 {
		t.Fatalf("the registry holds %v before any request; want none", roots)
	}

	first, err := sess.workspaceFor(rootA)
	if err != nil {
		t.Fatalf("resolve %s: %v", rootA, err)
	}
	if roots := sess.workspaces.roots(); !equalStrings(roots, []string{filepath.Clean(rootA)}) {
		t.Errorf("after naming one workspace the registry holds %v; want exactly the one named", roots)
	}

	// The same root, named again, is the SAME entry — which is what "keyed"
	// means. A fresh entry here would be a cold cache on every request.
	again, err := sess.workspaceFor(rootA)
	if err != nil {
		t.Fatalf("resolve %s again: %v", rootA, err)
	}
	if again != first {
		t.Error("naming one workspace twice produced two entries; each request would then start cold")
	}

	// A second root ADDS an entry rather than replacing the first's.
	if _, err := sess.workspaceFor(rootB); err != nil {
		t.Fatalf("resolve %s: %v", rootB, err)
	}
	wantRoots := []string{filepath.Clean(rootA), filepath.Clean(rootB)}
	sort.Strings(wantRoots)
	if roots := sess.workspaces.roots(); !equalStrings(roots, wantRoots) {
		t.Errorf("the registry holds %v; want exactly %v — a second workspace adds an entry and "+
			"evicts nothing", roots, wantRoots)
	}

	// Two spellings of one root are one entry, which is what the key being the
	// cleaned absolute path buys. Without it a client would be answered from a
	// cold cache for a workspace the daemon already had warm.
	doubled, err := sess.workspaceFor(rootA + string(filepath.Separator) + "." + string(filepath.Separator))
	if err != nil {
		t.Fatalf("resolve a second spelling of %s: %v", rootA, err)
	}
	if doubled != first {
		t.Error("two spellings of one workspace root produced two entries; the key is the cleaned " +
			"absolute path precisely so they do not")
	}
}

// TestAClientDeclaringNoWorkspaceStillReceivesPushes is the compatibility
// proof, and it is the case the change could most easily have broken in silence.
//
// A client that sends the handshake it sent before this field existed keeps
// receiving the diagnostics it received before — the workspace the process was
// launched over. An editor that connected without the field and stopped getting
// diagnostics would be a regression nothing in its own behaviour could reveal.
//
// The empty declaration is asserted beside it, because the two together are what
// make the field's three cases distinguishable rather than two-with-a-default.
func TestAClientDeclaringNoWorkspaceStillReceivesPushes(t *testing.T) {
	t.Parallel()
	rootA, _, relA, _ := twoWorkspacesWithABrokenRelease(t)

	for _, tc := range []struct {
		name        string
		handshake   map[string]any
		wantPushes  []string
		wantReports string
	}{
		{
			name:        "the field is absent, as a client predating it sends",
			handshake:   map[string]any{"capabilities": map[string]any{}},
			wantPushes:  []string{filepath.Join(rootA, relA)},
			wantReports: rootA,
		},
		{
			name: "the field is the empty string, asking for no pushes at all",
			handshake: map[string]any{
				"capabilities": map[string]any{}, "workspaceRoot": "",
			},
			wantPushes:  nil,
			wantReports: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := scriptedSession(t, rootA, [][]byte{
				framedRequest(1, "aggrega/initialize", tc.handshake),
				framedRequestAt(t, rootA, 2, "aggrega/activeRelease", map[string]any{}),
			})

			var handshake initializeResult
			resultOf(t, frames, "1", &handshake)
			if handshake.WorkspaceRoot != tc.wantReports {
				t.Errorf("the handshake reported workspaceRoot %q; want %q",
					handshake.WorkspaceRoot, tc.wantReports)
			}
			if got := pushedFiles(t, frames); !equalStrings(got, tc.wantPushes) {
				t.Errorf("pushed notifications named %v; want exactly %v", got, tc.wantPushes)
			}

			// Whatever the declaration, the request is answered. A client that
			// declared nothing must still WORK, which is a different claim from
			// whether it is pushed to.
			var answered activeReleaseResult
			resultOf(t, frames, "2", &answered)
			if !strings.Contains(answered.Trace, relA) {
				t.Errorf("the answer does not account for the workspace's own Release %q: %q",
					relA, answered.Trace)
			}
		})
	}
}

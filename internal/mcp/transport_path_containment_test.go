// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// How far a path a call names may reach is a property of how the caller reached
// the server, so it is proved on both transports against one fixture: a caller
// over a listener names its subject inside the one workspace that process serves,
// and a client that launched the process names a path anywhere on the machine it
// is sitting at.
//
// The refusal is asserted on the failing result a caller actually reads — the tool
// result flagged as a failure, and the sentence inside it — rather than on the
// helper that composes it. A path silently resolved somewhere else and a path
// refused are the same call from the outside and differ only there.

// containmentRefusal is the fragment every refusal carries, whichever shape of
// out-of-workspace path produced it. A row asserting it is asserting that this
// surface refused for THIS reason, rather than that the call failed for some
// reason or other — which every one of these calls would do anyway, since none of
// the fixture paths names a file that exists.
const containmentRefusal = "must name a path inside the workspace this server serves"

// theRelativeClimb and theInsidePath are the two values every row sends. The
// climb spells its escape in the relative form a caller would write; the inside
// value names a path under the served root and is what keeps a row from passing
// against a server that refused everything.
//
// The inside value names a document the fixture does NOT hold, deliberately. What
// these rows measure is whether a path was allowed to reach the operation behind
// the argument, and a path that exists would then have that operation do its real
// work — publishing, generating, rendering — which is neither what is being
// measured nor something a row here should set in motion. Every one of these calls
// therefore fails, and the assertion is on WHY: for a reason that is not the bound.
// That a contained path is answered from is proved separately, over a document the
// fixture does hold.
const (
	theRelativeClimb = "../elsewhere/taken.yaml"
	theInsidePath    = "parts/not-authored-yet.yaml"
)

// TestEveryRemotePathArgumentStaysInsideTheWorkspace drives each argument that
// names a path over the transport that bounds it.
//
// Three values per argument, and the third is the one that makes the other two
// mean anything: a relative path climbing out of the workspace, an absolute path
// naming a file elsewhere on the machine, and a path inside the workspace, which
// must NOT be refused. Without that row every assertion here would pass against a
// build that refused every path it was given.
func TestEveryRemotePathArgumentStaysInsideTheWorkspace(t *testing.T) {
	served := rootedWorkspace(t)
	elsewhere := filepath.Join(rootedWorkspace(t), theLaunchedWorkspacesRelease)

	// A registry is named wherever a publication tool takes one, because that tool
	// asks for a destination before it reads its paths and a row without one would
	// be answered about the destination instead. The publication never happens:
	// every one of these calls is refused, and the one row that is not runs dry.
	const registry = "registry.example/containment"

	// argumentUnder composes a tool's arguments around the one path value the row
	// is about, so the three values per argument differ in nothing else.
	for _, tc := range []struct {
		name        string
		tool        string
		argumentsIn func(path string) map[string]any
	}{
		{
			name: "the Release a build renders",
			tool: "build",
			argumentsIn: func(path string) map[string]any {
				return map[string]any{"release": path}
			},
		},
		{
			name: "the entity document validate checks",
			tool: "validate",
			argumentsIn: func(path string) map[string]any {
				return map[string]any{"entity": path}
			},
		},
		{
			name: "the entity document a publication reads",
			tool: "publish-entity",
			argumentsIn: func(path string) map[string]any {
				return map[string]any{"entity": path, "dry_run": true, "registry": registry}
			},
		},
		{
			name: "the module an entity is generated for",
			tool: "gen-catalog",
			argumentsIn: func(path string) map[string]any {
				return map[string]any{"plugin": path, "output_dir": "generated", "overlay": "overlays"}
			},
		},
		{
			name: "the batch file a withdrawal reads",
			tool: "deprecate-artifact",
			argumentsIn: func(path string) map[string]any {
				return map[string]any{"from": path, "registry": registry}
			},
		},
		{
			name: "the document an insertion point is located in",
			tool: "insertion-point",
			argumentsIn: func(path string) map[string]any {
				return map[string]any{"file": path, "container": []string{"spec"}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("a relative path climbing out is refused", func(t *testing.T) {
				res := callOverHTTP(t, served, tc.tool, tc.argumentsIn(theRelativeClimb))
				assertRefusedNamingTheBound(t, res, served, theRelativeClimb)
			})

			t.Run("an absolute path elsewhere on the machine is refused", func(t *testing.T) {
				res := callOverHTTP(t, served, tc.tool, tc.argumentsIn(elsewhere))
				assertRefusedNamingTheBound(t, res, served, elsewhere)
			})

			t.Run("a path inside the workspace resolves", func(t *testing.T) {
				res := callOverHTTP(t, served, tc.tool, tc.argumentsIn(theInsidePath))
				text := res.Content[0].Text
				if strings.Contains(text, containmentRefusal) {
					t.Fatalf("a path inside the served workspace was refused as outside it, so this surface "+
						"refuses every path rather than the ones that leave: %q", text)
				}
				if strings.Contains(text, "names a location on the machine this server runs on") {
					t.Fatalf("a workspace-relative path was read as a location on the machine: %q", text)
				}
			})
		})
	}
}

// assertRefusedNamingTheBound reads the answer a caller gets: a result flagged as
// a failure, naming the bound in words — the workspace this server serves — so
// the caller learns what the bound IS rather than only that it was crossed.
//
// It names no place on the host while it does so. The served root is a location
// a caller reaching a listener is not told by any answer, a refusal included, and
// an absolute value the caller sent is not echoed back either. A relative value is
// the caller's own spelling and names nothing on the host, so it is quoted back as
// sent, which is what tells the caller which value to correct.
func assertRefusedNamingTheBound(t *testing.T, res *callToolResult, served, sent string) {
	t.Helper()
	text := res.Content[0].Text
	if !res.IsError {
		t.Fatalf("the path was not refused; the result reads %q", text)
	}
	if !strings.Contains(text, containmentRefusal) {
		t.Fatalf("the call failed, but not because the path left the workspace, so this row proves nothing "+
			"about containment: %q", text)
	}
	if strings.Contains(text, served) {
		t.Errorf("the refusal names the served root %q, a location on the host: %q", served, text)
	}
	switch {
	case filepath.IsAbs(sent) && strings.Contains(text, sent):
		t.Errorf("the refusal echoes the absolute location %q back to the caller: %q", sent, text)
	case !filepath.IsAbs(sent) && !strings.Contains(text, sent):
		t.Errorf("the refusal does not quote the relative value %q the caller sent, so the caller cannot tell "+
			"which value to correct: %q", sent, text)
	}
}

// TestAContainedPathIsAnsweredOverHTTP is the half the rows above cannot make.
// They assert a path inside the workspace is not refused; this asserts one is
// actually read and answered from.
//
// insertion-point is the tool driven here because its whole answer is derived from
// the document's own bytes, so an answer at all proves the file was opened.
func TestAContainedPathIsAnsweredOverHTTP(t *testing.T) {
	served := rootedWorkspace(t)
	document := "spec:\n  components:\n    api: {}\n"
	if err := os.WriteFile(filepath.Join(served, "authored.yaml"), []byte(document), 0o644); err != nil {
		t.Fatalf("write the authored document: %v", err)
	}

	res := callOverHTTP(t, served, "insertion-point", map[string]any{
		"file":      "authored.yaml",
		"container": []string{"spec", "components"},
	})
	if res.IsError {
		t.Fatalf("a document inside the served workspace was not answered: %s", res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, "authored.yaml") {
		t.Errorf("the answer does not name the document it was computed over: %q", res.Content[0].Text)
	}
}

// TestAnAbsolutePathOutsideTheRootIsAcceptedOverStdio is the control on the other
// transport, and it is half of what the rows above assert.
//
// A client that launched this process is at the machine's keyboard, so it names a
// file wherever that file is — which is what the reference page promises and what
// every one of those refusals must not have broken. Without this row, a build that
// simply refused every out-of-root path on both transports would pass everything
// above.
//
// The discriminator is the payload's own target: validate reports the path it
// checked, so a target naming the file outside the root proves that path was
// resolved and used rather than merely tolerated.
func TestAnAbsolutePathOutsideTheRootIsAcceptedOverStdio(t *testing.T) {
	served := rootedWorkspace(t)
	outside := filepath.Join(rootedWorkspace(t), theLaunchedWorkspacesRelease)

	s := &server{out: io.Discard, logw: io.Discard, launchRoot: served, transport: transportStdio}

	var got validateResultPayload
	decodeStructured(t, callDiscoveryTool(t, s, "validate", map[string]any{"entity": outside}), &got)
	if told(got.Target) != outside {
		t.Errorf("validate checked %q, want the file outside the root it was pointed at, %q.\n"+
			"A caller that launched this process names a path anywhere on its own machine, and this surface "+
			"still answers from wherever it was pointed.", told(got.Target), outside)
	}
}

// TestTheSamePathIsRefusedOverHTTPAndAcceptedOverStdio drives ONE value through
// both transports, so the difference between the two answers cannot be an
// accident of two differently-composed calls.
func TestTheSamePathIsRefusedOverHTTPAndAcceptedOverStdio(t *testing.T) {
	served := rootedWorkspace(t)
	outside := filepath.Join(rootedWorkspace(t), theLaunchedWorkspacesRelease)
	arguments := map[string]any{"entity": outside}

	overHTTP := callOverHTTP(t, served, "validate", arguments)
	if !overHTTP.IsError || !strings.Contains(overHTTP.Content[0].Text, containmentRefusal) {
		t.Fatalf("over a listener the path outside the served workspace was not refused: %q",
			overHTTP.Content[0].Text)
	}

	s := &server{out: io.Discard, logw: io.Discard, launchRoot: served, transport: transportStdio}
	var got validateResultPayload
	decodeStructured(t, callDiscoveryTool(t, s, "validate", arguments), &got)
	if told(got.Target) != outside {
		t.Errorf("over this process's own streams the same path was not honoured: target = %q, want %q",
			told(got.Target), outside)
	}
}

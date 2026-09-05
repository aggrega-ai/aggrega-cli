// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// What this file can and cannot reach, said once so no assertion below is read
// as stronger than it is.
//
// aggrega/build renders under the posture of a surface that emits an artifact,
// so every gate is on and a drifted lockfile halts it. No fixture in this
// package carries a lockfile that matches its own closure — each writes a
// minimal one and relies on the graph inspection tolerating the divergence — and
// producing one means running the dependency-resolution pass, which lives above
// this package. So the SUCCEEDING write is not reachable from here and is proved
// end to end against the real binary instead.
//
// What is reachable, and is what this file asserts, is the half that a
// succeeding run cannot show: that the write sits downstream of every gate, so a
// build that refuses writes nothing at all — and that the destination is derived
// from the workspace this session anchored to rather than from anything a
// request carries.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
)

// buildResponse is the decoded shape an aggrega/build request answers with: the
// result on success, the envelope on a refusal. Both are read from one decode so
// a test can assert on whichever arrived without guessing first.
type buildResponse struct {
	Result *buildResult `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// driveBuildOn runs ONE aggrega/build request to completion at pool=1 against
// the supplied server, and returns the decoded response together with everything
// the handler put on the wire — the diagnostic notifications included, which is
// where its positioned findings travel.
func driveBuildOn(t *testing.T, srv *Server, releaseRel string) (buildResponse, string) {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/build", buildParams{Release: releaseRel}))
	srv.in = bufio.NewReader(&in)
	srv.out = &out
	body, err := readMessage(srv.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	driveHandle(t, srv, &req, &out)

	pushed := out.String()
	r := bufio.NewReader(strings.NewReader(pushed))
	for {
		frame, rerr := readMessage(r)
		if rerr != nil {
			t.Fatalf("no build response was written; the wire carried:\n%s", pushed)
		}
		var probe struct {
			ID *int `json:"id"`
		}
		if json.Unmarshal(frame, &probe) != nil || probe.ID == nil {
			// A server-push notification, not the answer to this request.
			continue
		}
		var resp buildResponse
		if uerr := json.Unmarshal(frame, &resp); uerr != nil {
			t.Fatalf("decode response: %v (body %s)", uerr, frame)
		}
		return resp, pushed
	}
}

// driveBuild is driveBuildOn over a server built for ws.
func driveBuild(t *testing.T, ws, releaseRel string) (buildResponse, string) {
	t.Helper()
	var in, out bytes.Buffer
	return driveBuildOn(t, newPinIsolationServer(t, ws, &in, &out, 1), releaseRel)
}

// TestBuildRefusesWhereTheGraphInspectionDraws is the discriminating assertion
// that this method took the artifact posture rather than the inspection one, and
// it is the same pair the preview beside it rests on: one workspace, two
// requests, two different answers.
//
// The graph inspection draws over a workspace whose lockfile no longer matches
// its closure, because a reader left with no graph is worse off than one left
// with a graph and a diagnostic. A build over that same workspace halts, naming
// the command that refreshes the lockfile. A handler that had reached for the
// inspection posture would have written a file set here, which is what makes the
// pair evidence rather than a restatement of the code.
func TestBuildRefusesWhereTheGraphInspectionDraws(t *testing.T) {
	ws, releaseRel := renderDialWorkspace(t)

	if graph := irGraphWithDial(t, ws, releaseRel, true); len(graph) == 0 {
		t.Fatal("precondition: the graph inspection produced nothing over this workspace, " +
			"so the two answers cannot be compared")
	}

	resp, pushed := driveBuild(t, ws, releaseRel)
	if resp.Error == nil {
		t.Fatalf("the build answered a workspace the command line would refuse; it renders under the "+
			"posture of a surface that emits an artifact, so a divergence halts\nwire:\n%s", pushed)
	}
	if !strings.Contains(resp.Error.Message, "lockfile") {
		t.Errorf("the refusal does not name what diverged: %s", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "aggrega pull") {
		t.Errorf("the refusal does not name the command that resolves it: %s", resp.Error.Message)
	}
}

// TestBuildWritesNothingWhenItRefuses is the assertion that separates this
// method from a writer that happens to run some gates first: the write sits
// downstream of every gate, so a run that refuses leaves the workspace exactly
// as it found it and creates no output directory at all.
//
// It is the one write-side property a package that cannot drive a succeeding
// render can still establish, and it is the property that matters most — a build
// that wrote a partial set and then reported a refusal would hand a reader an
// artifact tree the compiler never finished producing.
func TestBuildWritesNothingWhenItRefuses(t *testing.T) {
	ws, releaseRel := renderDialWorkspace(t)

	before := treeSnapshot(t, ws)
	// An empty snapshot would make every comparison below vacuous, so the
	// instrument is checked before its reading is trusted.
	if len(before) == 0 {
		t.Fatal("precondition: the workspace snapshot is empty, so a write could not be observed")
	}

	resp, pushed := driveBuild(t, ws, releaseRel)
	if resp.Error == nil {
		t.Fatalf("precondition: this workspace was expected to refuse, so the run proves nothing "+
			"about a refused build's writes\nwire:\n%s", pushed)
	}

	if _, err := os.Stat(filepath.Join(ws, artifactout.DefaultOutputDir)); !os.IsNotExist(err) {
		t.Errorf("the refused build created the output directory; stat err = %v", err)
	}
	after := treeSnapshot(t, ws)
	for path, sum := range after {
		prev, ok := before[path]
		switch {
		case !ok:
			t.Errorf("the refused build created %s", path)
		case prev != sum:
			t.Errorf("the refused build rewrote %s (%s -> %s)", path, prev, sum)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			t.Errorf("the refused build removed %s", path)
		}
	}
}

// TestBuildOutputRootIsAnchoredToTheWorkspace pins where this method may write.
// The destination is derived from the workspace root the session anchored to and
// from the one output-directory name both surfaces share, so no request can
// point the daemon at a directory of its own choosing — there is no parameter to
// carry one, and this is what makes that absence checkable rather than merely
// stated.
//
// The rootless row is the honest fallback: a daemon with no anchor resolves to
// the same relative default the command line uses, because inventing an absolute
// path from the process's working directory would write somewhere neither
// surface named.
func TestBuildOutputRootIsAnchoredToTheWorkspace(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	if got, want := (&Server{workspaceRoot: ws}).buildOutputRoot(), filepath.Join(ws, "dist"); got != want {
		t.Errorf("buildOutputRoot() = %q, want %q", got, want)
	}
	if got, want := (&Server{}).buildOutputRoot(), "dist"; got != want {
		t.Errorf("a daemon with no anchored workspace resolves to %q, want the relative default %q", got, want)
	}
}

// TestBuildRefusesAReleaseOutsideTheWorkspace pins that this method honours the
// Release parameter's contract on its own path rather than only on the
// inspection's. The method writes, so a value that escaped the workspace root
// would decide where those writes land — which is why the refusal is asserted
// here and not left to the shared helper's own coverage.
func TestBuildRefusesAReleaseOutsideTheWorkspace(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		release string
		fault   string
	}{
		{"a parent traversal", "../elsewhere/release.yaml", "outside the workspace root"},
		{"an absolute path", "/etc/release.yaml", "absolute path"},
		{"no Release at all", "", "named no Release"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			resp, pushed := driveBuild(t, ws, tc.release)
			if resp.Error == nil {
				t.Fatalf("the build accepted %q instead of refusing it\nwire:\n%s", tc.release, pushed)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("the refusal carries code %d, not invalid-params %d: %s",
					resp.Error.Code, jsonRPCInvalidParams, resp.Error.Message)
			}
			if !strings.Contains(resp.Error.Message, tc.fault) {
				t.Errorf("the refusal does not name the fault %q: %s", tc.fault, resp.Error.Message)
			}
			if _, err := os.Stat(filepath.Join(ws, artifactout.DefaultOutputDir)); !os.IsNotExist(err) {
				t.Errorf("a refused Release parameter still produced an output directory; stat err = %v", err)
			}
		})
	}
}

// TestBuildAndPreviewAnswerOneComputation pins that the two methods are
// transports over one render rather than two implementations that agree today.
//
// Over one workspace, both refuse and both refuse with the same message. The
// assertion is on the message rather than on the mere fact of a refusal, because
// two independently-written gate sequences would both refuse a drifted lockfile
// while naming different things — and a reader choosing between the two methods
// has no way to see that divergence.
func TestBuildAndPreviewAnswerOneComputation(t *testing.T) {
	ws, releaseRel := renderDialWorkspace(t)

	build, buildWire := driveBuild(t, ws, releaseRel)
	preview, previewWire := driveBuildPreview(t, ws, releaseRel)

	if build.Error == nil || preview.Error == nil {
		t.Fatalf("precondition: both methods were expected to refuse this workspace\nbuild:\n%s\npreview:\n%s",
			buildWire, previewWire)
	}
	if build.Error.Code != preview.Error.Code {
		t.Errorf("the build refuses with code %d and the preview with %d over one workspace",
			build.Error.Code, preview.Error.Code)
	}
	if build.Error.Message != preview.Error.Message {
		t.Errorf("the two methods refuse one workspace differently:\n build: %s\npreview: %s",
			build.Error.Message, preview.Error.Message)
	}
}

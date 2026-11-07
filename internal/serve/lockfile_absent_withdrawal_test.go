// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// driveRequestOn runs one request against an already-configured server, writing
// its framed response and every notification it pushed into out.
//
// The property these tests assert spans several requests on ONE server — what a
// client holds about a file is the sum of every frame it was sent — so a second
// server per request would start each one with nothing ever published.
func driveRequestOn(t *testing.T, srv *Server, out *bytes.Buffer, id int, method string, params any) {
	t.Helper()
	var in bytes.Buffer
	in.Write(framedRequest(id, method, params))
	srv.in = bufio.NewReader(&in)
	srv.out = out
	body, err := readMessage(srv.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	driveHandle(t, srv, &req, out)
}

// writeDriftedLockfile puts a lockfile beside the Release that the graph
// request's drift verify reports as drifted: every axis the Release reaches is
// pinned as the inspection computes it, plus one entity the Release does not
// reach. It is the shape an author is left with when a catalog entry the
// Release used to reach is dropped and nobody has pulled since.
//
// The honest pins are learned by running the daemon's own preview rather than
// being written by hand, so the one extra pin is the only thing the verify can
// object to.
func writeDriftedLockfile(t *testing.T, srv *Server, rel, lockfilePath string) {
	t.Helper()
	writeFixtureLockfile(t, lockfilePath, &lockfile.Lockfile{Version: 1, Release: "./" + rel, GeneratedAt: "2026-06-03T10:00:00Z"})
	drifted := lockfileFromClosure("./"+rel, serveInspectionClosure(t, srv, rel))
	drifted.Entities = append(drifted.Entities, lockfile.Entity{
		Kind: "component", URI: "demo/withdrawn", ResolvedVersion: "1.0.0", Digest: "sha256:" + strings.Repeat("0", 64),
	})
	writeFixtureLockfile(t, lockfilePath, drifted)
}

// TestAbsentLockfileWithdrawsItsDriftFinding drives the daemon through its own
// protocol, the way the editor drives it when its watcher reports a lockfile
// change: the drift finding is raised and reaches the client, the lockfile is
// deleted or left alone, the editor's invalidation is sent, and then a request
// about the Release.
//
// What is asserted is what the client is LEFT HOLDING, replayed from the frames
// it was sent, and beside it what the daemon holds. The two can disagree, and
// that disagreement is the defect this pins: the invalidation drops the daemon's
// own entry and tells the client nothing, so a server that only dropped its entry
// would satisfy an assertion over its store while the reader still sees the
// warning.
//
// The deleted rows cover both gates a Release request crosses — the one the
// inspections share and the one the build previews use — so a withdrawal written
// into one of them and not the other fails a row. The last row is the other half:
// a lockfile still present and still drifted keeps its finding, with no moment in
// between at which the client was told it was gone. A gate that withdrew whatever
// it found would pass every deleted row and fail that one.
func TestAbsentLockfileWithdrawsItsDriftFinding(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// deleteLockfile removes the lockfile once the finding is raised; false
		// leaves it in place, still drifted.
		deleteLockfile bool
		method         string
		params         func(release string) any
		wantWithdrawn  bool
	}{
		{
			name:           "a graph request after the lockfile is deleted",
			deleteLockfile: true,
			method:         "aggrega/irGraph",
			params:         func(r string) any { return irGraphParams{Release: r} },
			wantWithdrawn:  true,
		},
		{
			name:           "a dependency listing after the lockfile is deleted",
			deleteLockfile: true,
			method:         "aggrega/releaseClosure",
			params:         func(r string) any { return releaseClosureParams{Release: r} },
			wantWithdrawn:  true,
		},
		{
			name:           "a build preview after the lockfile is deleted",
			deleteLockfile: true,
			method:         "aggrega/buildPreview",
			params:         func(r string) any { return buildPreviewParams{Release: r} },
			wantWithdrawn:  true,
		},
		{
			name:           "a graph request while the lockfile is still drifted",
			deleteLockfile: false,
			method:         "aggrega/irGraph",
			params:         func(r string) any { return irGraphParams{Release: r} },
			wantWithdrawn:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ws, rel := libraryRefWorkspace(t)
			lockfilePath := lockfile.PathFor(filepath.Join(ws, rel))
			var in, out bytes.Buffer
			srv := newPinIsolationServer(t, ws, &in, &out, 1)
			writeDriftedLockfile(t, srv, rel, lockfilePath)

			// The precondition every row stands on: the finding was raised and the
			// client holds it.
			driveRequestOn(t, srv, &out, 1, "aggrega/irGraph", irGraphParams{Release: rel})
			raised := publishedFrames(t, &out)
			if view, told := clientView(srv, raised, lockfilePath); !told || len(view) != 1 {
				t.Fatalf("precondition: the client holds %d finding(s) on the lockfile (told: %v), want the one drift "+
					"finding, so nothing below is about a withdrawal", len(view), told)
			}

			if tc.deleteLockfile {
				if err := os.Remove(lockfilePath); err != nil {
					t.Fatalf("delete the lockfile: %v", err)
				}
			}
			driveRequestOn(t, srv, &out, 2, "aggrega/invalidate", invalidateParams{File: lockfilePath})
			driveRequestOn(t, srv, &out, 3, tc.method, tc.params(rel))
			after := publishedFrames(t, &out)

			view, _ := clientView(srv, append(append([]publishDiagnosticsParams(nil), raised...), after...), lockfilePath)
			held := srv.snapshotDiagnostics(lockfilePath)
			if tc.wantWithdrawn {
				if len(view) != 0 {
					t.Errorf("the client still shows %d finding(s) on a lockfile that is gone: %+v", len(view), view)
				}
				if len(held) != 0 {
					t.Errorf("the daemon still holds %d finding(s) on a lockfile that is gone: %+v", len(held), held)
				}
				return
			}
			if len(view) != 1 {
				t.Errorf("the client shows %d finding(s) on a lockfile still present and still drifted, want 1: %+v", len(view), view)
			}
			if len(held) != 1 {
				t.Errorf("the daemon holds %d finding(s) on a lockfile still present and still drifted, want 1: %+v", len(held), held)
			}
			for _, f := range after {
				if srv.canonicalFile(f.File) == srv.canonicalFile(lockfilePath) && len(f.Diagnostics) == 0 {
					t.Errorf("the client was told the lockfile is clear while it was still present and still drifted")
				}
			}
		})
	}
}

// TestVerifyDriftAndPublishWithNoClosureWithdrawsWithoutReading pins the
// absent-lockfile half of the one drift publisher: handed no closure, it
// withdraws the finding and publishes that, reading nothing and logging nothing.
//
// The lockfile is deliberately ON DISK and drifted here, which is the moment the
// nil closure exists for: a pull writing the file after the request found it
// missing and before the withdrawal ran. Verified against an empty closure
// instead, that new file would be reported as drifted from a resolution that never
// ran, and this test would see a finding where it asserts none.
func TestVerifyDriftAndPublishWithNoClosureWithdrawsWithoutReading(t *testing.T) {
	t.Parallel()
	ws, rel := libraryRefWorkspace(t)
	lockfilePath := lockfile.PathFor(filepath.Join(ws, rel))
	var in, out, stderr bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	srv.stderr = &stderr
	writeDriftedLockfile(t, srv, rel, lockfilePath)
	srv.setDiagnostics(lockfilePath, []Diagnostic{
		{Line: 1, Column: 1, Severity: editorSeverityWarning, Message: "previous drift"},
	})

	ctx, flush := serializerContext(t, &out)
	if lf := srv.verifyDriftAndPublish(ctx, lockfilePath, rel, nil); lf != nil {
		t.Errorf("a withdrawal returned a lockfile, so it read one: %+v", lf)
	}
	flush()

	if held := srv.snapshotDiagnostics(lockfilePath); len(held) != 0 {
		t.Errorf("the daemon holds %d finding(s) after a withdrawal, want none: %+v", len(held), held)
	}
	frames := publishedFrames(t, &out)
	if view, told := clientView(srv, frames, lockfilePath); !told || len(view) != 0 {
		t.Errorf("the client was not told the lockfile is clear (told: %v, holds %d)", told, len(view))
	}
	if stderr.Len() != 0 {
		t.Errorf("a withdrawal logged %q; an absent lockfile is an ordinary state the refusal already reports", stderr.String())
	}
}

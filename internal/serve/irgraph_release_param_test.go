// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestIrGraphRefusesReleaseOutsideTheContract drives aggrega/irGraph
// with each shape the `release` parameter may not take and asserts the
// daemon refuses rather than joining.
//
// The refusal is what makes the contract real. filepath.Join
// concatenates an absolute second operand onto the first instead of
// failing, so before this guard an absolute value produced a path that
// named no file the workspace had, and every diagnostic downstream
// pointed at it. The traversal cases are the same defect one step
// along: a value that is relative in spelling but lands outside the
// root the daemon validates against.
func TestIrGraphRefusesReleaseOutsideTheContract(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	cases := []struct {
		name        string
		release     string
		wantInFault string
	}{
		{
			name: "absolute path, which Join would concatenate rather than reject",
			// The shape the editor sent before it normalised: the
			// daemon's own releaseCandidates answer, used verbatim.
			release:     filepath.Join(tmp, "envs", "prod.yaml"),
			wantInFault: "is an absolute path",
		},
		{
			name:        "traversal escaping the workspace root",
			release:     filepath.Join("..", "..", "elsewhere", "release.yaml"),
			wantInFault: "resolves outside the workspace root",
		},
		{
			name:        "traversal that escapes only after a leading segment",
			release:     filepath.Join("envs", "..", "..", "release.yaml"),
			wantInFault: "resolves outside the workspace root",
		},
		{
			name:        "empty, which would resolve to the workspace root itself",
			release:     "",
			wantInFault: "named no Release",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			srv := newTestServer(t, tmp, nil, &out)

			var in bytes.Buffer
			in.Write(framedRequest(1, "aggrega/irGraph", irGraphParams{Release: tc.release}))
			resp := runOneRequest(t, srv, &in, &out)

			if resp.Error == nil {
				t.Fatalf("expected refusal for release=%q; got result: %s", tc.release, resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("Code: got %d, want %d (invalid params)", resp.Error.Code, jsonRPCInvalidParams)
			}
			// The message teaches the contract first, then the fault.
			// Both halves are asserted: a refusal naming only the
			// fault leaves the sender guessing what to send instead.
			if !strings.Contains(resp.Error.Message, "must be a path relative to the workspace root") {
				t.Errorf("Message should state the contract; got %q", resp.Error.Message)
			}
			if !strings.Contains(resp.Error.Message, tc.wantInFault) {
				t.Errorf("Message should name the fault %q; got %q", tc.wantInFault, resp.Error.Message)
			}
		})
	}
}

// TestIrGraphAcceptsWorkspaceRelativeRelease is the other direction of
// the refusal, and it is not optional: a guard proven only by what it
// rejects is indistinguishable from one that rejects everything.
//
// A legitimate workspace-relative value — including one whose interior
// traversal cancels out and lands back inside the root — passes the
// parameter check and reaches the lockfile gate, which is the next
// stage and the proof the request was admitted. The lockfile-absent
// envelope naming the resolved path is the observable that the join
// happened against the workspace root as documented.
func TestIrGraphAcceptsWorkspaceRelativeRelease(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	cases := []struct {
		name    string
		release string
		wantAt  string
	}{
		{
			name:    "a plain workspace-relative path",
			release: filepath.Join("envs", "prod.yaml"),
			wantAt:  filepath.Join(tmp, "envs", "prod.lock"),
		},
		{
			name:    "interior traversal that lands back inside the root",
			release: filepath.Join("envs", "..", "release.yaml"),
			wantAt:  filepath.Join(tmp, "release.lock"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			srv := newTestServer(t, tmp, nil, &out)

			var in bytes.Buffer
			in.Write(framedRequest(1, "aggrega/irGraph", irGraphParams{Release: tc.release}))
			resp := runOneRequest(t, srv, &in, &out)

			if resp.Error == nil {
				t.Fatalf("expected the lockfile gate to answer; got result: %s", resp.Result)
			}
			if resp.Error.Code == jsonRPCInvalidParams {
				t.Fatalf("release=%q was refused by the parameter check; it honours the contract: %s", tc.release, resp.Error.Message)
			}
			if resp.Error.Code != errLockfileAbsent {
				t.Fatalf("Code: got %d, want %d (lockfile-absent — the stage after the parameter check)", resp.Error.Code, errLockfileAbsent)
			}
			if resp.Error.Data == nil {
				t.Fatalf("expected Data populated; got nil")
			}
			if resp.Error.Data.Lockfile != tc.wantAt {
				t.Errorf("Data.Lockfile: got %q, want %q — the request resolved against the workspace root", resp.Error.Data.Lockfile, tc.wantAt)
			}
		})
	}
}

// TestReleaseParamRefusalNamesTheContractOnTheWire pins the refusal's
// wire shape rather than its Go shape: an editor branching on the
// JSON-RPC code sees the standard invalid-params value, so a malformed
// request is distinguishable from the daemon's own 1xxx conditions,
// which name states the user acts on (seed the cache, generate a
// lockfile, pick a Release). A malformed request has no such action —
// the sender corrects the request — so it must not arrive in that band.
func TestReleaseParamRefusalNamesTheContractOnTheWire(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	_, envelope := resolveReleaseParam(tmp, filepath.Join(tmp, "release.yaml"))
	if envelope == nil {
		t.Fatalf("expected an absolute release to be refused")
	}
	if envelope.Code < -32768 || envelope.Code > -32000 {
		t.Errorf("Code %d sits outside the JSON-RPC reserved range; a malformed request must not land in the daemon's 1xxx band", envelope.Code)
	}
	if envelope.Data != nil {
		t.Errorf("Data: got %+v, want nil — the refusal is about the request, so it points at no artifact", envelope.Data)
	}
}

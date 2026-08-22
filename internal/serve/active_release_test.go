// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestDispatchActiveReleaseTriggersTheCascadeAsTheFirstRequest is the
// invocation proof for the new method: it drives aggrega/activeRelease as the
// FIRST request of a session, over a server built WITHOUT the harness's
// cascade suppression, and asserts the reply carries the resolved Release.
//
// The pre-condition assertion is what gives the test its force. A server whose
// active-Release fields were injected by the test would answer correctly while
// proving nothing about the handler triggering the cascade, so this asserts the
// fields are still zero-valued before the request and populated after it. If
// the ensureCascade call were dropped from the handler, the reply would carry
// an empty path and this test would fail — which is the failure mode the
// assertion exists to demonstrate.
func TestDispatchActiveReleaseTriggersTheCascadeAsTheFirstRequest(t *testing.T) {
	ws := t.TempDir()
	want := writeRelease(t, ws, "release.yaml", "release/only")

	srv := newLazyCascadeServer(t, ws, nil, nil)

	// Pre-condition: nothing has run the cascade yet.
	if srv.activeRelease != nil || srv.activeReleasePath != "" {
		t.Fatalf("cascade already ran before the first request: path=%q", srv.activeReleasePath)
	}

	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/activeRelease", map[string]any{}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("activeRelease returned an error envelope: %+v", resp.Error)
	}

	var result activeReleaseResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v (raw: %s)", err, resp.Result)
	}
	if result.Release != want {
		t.Errorf("release = %q; want %q", result.Release, want)
	}

	// Post-condition: the handler, not a prior aggrega/validate, ran the cascade.
	if srv.activeRelease == nil {
		t.Errorf("activeRelease request did not trigger the cascade; activeRelease is still nil")
	}
	if srv.cascadeAttempt.ResolvedStep != 3 {
		t.Errorf("cascade ResolvedStep = %d; want 3 (the single-Release heuristic)", srv.cascadeAttempt.ResolvedStep)
	}
}

// TestDispatchActiveReleaseUnresolvedIsSuccessWithAnEmptyPath pins the
// not-resolved contract: a workspace the cascade cannot resolve gets a SUCCESS
// response whose release field is present and empty — never the
// release-not-resolved error envelope. The distinction is load-bearing for the
// client: it refreshes this indicator on daemon restarts, anchor changes and
// file-watcher events, and an error reply would raise a remediation prompt on
// every one of them in a workspace that legitimately holds no Release.
//
// The raw-key assertion is separate from the decoded-value one on purpose.
// Decoding into the result struct yields the empty string whether the field was
// present-and-empty or absent altogether, so only reading the raw object proves
// the field carries no omitempty.
func TestDispatchActiveReleaseUnresolvedIsSuccessWithAnEmptyPath(t *testing.T) {
	ws := t.TempDir() // no Release anywhere: the cascade exhausts into the fallback

	srv := newLazyCascadeServer(t, ws, nil, nil)

	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/activeRelease", map[string]any{}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("unresolved workspace answered with an error envelope: %+v", resp.Error)
	}

	// The field is present on the wire even though it is empty.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(resp.Result, &raw); err != nil {
		t.Fatalf("unmarshal raw result: %v (raw: %s)", err, resp.Result)
	}
	if _, ok := raw["release"]; !ok {
		t.Errorf("release key absent from the result; the field must carry no omitempty (raw: %s)", resp.Result)
	}

	var result activeReleaseResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.Release != "" {
		t.Errorf("release = %q; want the empty string (nothing resolved)", result.Release)
	}
	// The cascade ran and exhausted, rather than not having run at all.
	if srv.activeRelease != nil {
		t.Errorf("activeRelease = %+v; want nil for a workspace with no Release", srv.activeRelease)
	}
}

// TestDispatchActiveReleaseReportsTheCascadeWinnerNotTheMarker is the defect
// this method exists to close. The workspace declares an authoring Release in
// config AND carries an active-release marker naming a different file, both
// loadable. The cascade prefers the config, so the daemon acts on that Release
// — while a client re-deriving the answer by reading the marker file would
// display the other one. The reply must carry the Release the cascade actually
// selected.
func TestDispatchActiveReleaseReportsTheCascadeWinnerNotTheMarker(t *testing.T) {
	ws := t.TempDir()
	configured := writeRelease(t, ws, "configured.yaml", "release/configured")
	marked := writeRelease(t, ws, "marked.yaml", "release/marked")

	markerDir := filepath.Join(ws, ".aggrega")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatalf("mkdir marker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "active-release"), []byte("marked.yaml\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	srv := newLazyCascadeServer(t, ws, nil, nil)
	srv.wsConfig.Build.AuthoringRelease = "configured.yaml"

	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/activeRelease", map[string]any{}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("activeRelease returned an error envelope: %+v", resp.Error)
	}

	var result activeReleaseResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.Release != configured {
		t.Errorf("release = %q; want %q (the configured authoring Release, which outranks the marker)", result.Release, configured)
	}
	if result.Release == marked {
		t.Errorf("release = %q; the reply followed the marker file instead of the cascade", result.Release)
	}
	if srv.cascadeAttempt.ResolvedStep != 1 {
		t.Errorf("cascade ResolvedStep = %d; want 1 (the configured authoring Release)", srv.cascadeAttempt.ResolvedStep)
	}
}

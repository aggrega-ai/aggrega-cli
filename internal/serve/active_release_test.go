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

// activeReleaseAnswer drives aggrega/activeRelease against srv over the real
// framing and write path and returns the decoded result. Each call is an
// independent request on the same server, which is what lets a test ask twice.
func activeReleaseAnswer(t *testing.T, srv *Server) activeReleaseResult {
	t.Helper()
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
	return result
}

// TestDispatchActiveReleaseRunsTheCascadeAsTheFirstRequest is the invocation
// proof: it drives aggrega/activeRelease as the FIRST request of a session and
// asserts the reply carries the Release the cascade selects.
//
// The assertion is on the wire answer and on nothing else, which is what gives
// it force. The daemon retains no Release, so there is no field a test could
// inspect instead — and none it could pre-populate to make the handler look
// like it worked. Were the cascade call dropped, the reply would carry an empty
// path and this test would fail.
func TestDispatchActiveReleaseRunsTheCascadeAsTheFirstRequest(t *testing.T) {
	ws := t.TempDir()
	want := writeRelease(t, ws, "release.yaml", "release/only")

	srv := newLazyCascadeServer(t, ws, nil, nil)

	result := activeReleaseAnswer(t, srv)
	if result.Release != want {
		t.Errorf("release = %q; want %q", result.Release, want)
	}
	if result.Reason != "" || result.Trace != "" {
		t.Errorf("a resolved answer carried an explanation: reason=%q trace=%q", result.Reason, result.Trace)
	}
}

// TestDispatchActiveReleaseAnswersAgainAfterTheWorkspaceChanges is the
// re-answerability proof, and it is the property a once-per-process answer
// cannot have. One server, no restart: the first request is answered against a
// workspace holding one Release, then an operator marker is written naming a
// different one, and the second request must report the new winner.
//
// A daemon that resolved the Release once and kept it would pass the first
// assertion and fail the second with the first answer, which is exactly the
// failure this shape exists to make impossible.
func TestDispatchActiveReleaseAnswersAgainAfterTheWorkspaceChanges(t *testing.T) {
	ws := t.TempDir()
	first := writeRelease(t, ws, "release.yaml", "release/only")

	srv := newLazyCascadeServer(t, ws, nil, nil)

	if got := activeReleaseAnswer(t, srv).Release; got != first {
		t.Fatalf("first answer = %q; want %q", got, first)
	}

	// The operator picks a different Release, the way they would from the
	// editor: a second Release file plus a marker naming it. The marker
	// outranks the single-Release inference, and with two Releases present
	// that inference no longer applies either.
	second := writeRelease(t, ws, "other.yaml", "release/other")
	markerDir := filepath.Join(ws, ".aggrega")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatalf("mkdir marker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "active-release"), []byte("other.yaml\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	if got := activeReleaseAnswer(t, srv).Release; got != second {
		t.Errorf("second answer = %q; want %q — the daemon answered from a frozen resolution", got, second)
	}
}

// TestDispatchActiveReleaseUnresolvedIsSuccessWithAnEmptyPath pins the
// not-resolved contract: a workspace the cascade cannot resolve gets a SUCCESS
// response whose release field is present and empty — never an error envelope.
// The distinction is load-bearing for the client: it refreshes this indicator
// on daemon restarts, anchor changes and file-watcher events, and an error
// reply would raise a remediation prompt on every one of them in a workspace
// that legitimately holds no Release.
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
}

// TestDispatchActiveReleaseUnresolvedCarriesTheReasonAndTrace pins the half of
// the answer a client needs in order to explain itself. An empty path alone
// tells a user nothing they can act on, so the exhausted cascade's account
// travels with it: a one-line summary for a compact surface and the per-step
// trace for a log.
//
// Both are asserted for content rather than for presence — a field carrying an
// empty string would satisfy a presence check while explaining nothing.
func TestDispatchActiveReleaseUnresolvedCarriesTheReasonAndTrace(t *testing.T) {
	ws := t.TempDir()
	// Two Releases and no marker: the cascade attempts every step and
	// exhausts, which is the state the account describes.
	writeRelease(t, ws, "a.yaml", "release/a")
	writeRelease(t, ws, "b.yaml", "release/b")

	srv := newLazyCascadeServer(t, ws, nil, nil)

	result := activeReleaseAnswer(t, srv)
	if result.Release != "" {
		t.Fatalf("release = %q; want the empty string (two Releases, no marker)", result.Release)
	}
	if result.Reason == "" {
		t.Errorf("reason is empty; an unresolved answer must say why")
	}
	if result.Trace == "" {
		t.Errorf("trace is empty; an unresolved answer must carry the per-step account")
	}
}

// TestDispatchActiveReleaseReportsTheCascadeWinnerNotTheMarker is the defect
// this method exists to close. The workspace declares an authoring Release in
// config AND carries an active-release marker naming a different file, both
// loadable. The cascade prefers the config — while a client re-deriving the
// answer by reading the marker file would display the other one. The reply must
// carry the Release the cascade actually selected.
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

	result := activeReleaseAnswer(t, srv)
	if result.Release != configured {
		t.Errorf("release = %q; want %q (the configured authoring Release, which outranks the marker)", result.Release, configured)
	}
	if result.Release == marked {
		t.Errorf("release = %q; the reply followed the marker file instead of the cascade", result.Release)
	}
}

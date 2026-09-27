// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// handshakeVersionProbe is the build stamp the test below installs. It is shaped
// like no release, so neither surface can report it by coincidence: a version a
// surface worked out for itself — a hard-coded string, or a second reading of the
// build information — would have to guess this exact string to pass.
const handshakeVersionProbe = "v0.0.0-handshake-version-probe"

// TestServeHandshakeReportsTheVersionCommandsVersion pins that the language
// server's handshake names the version `aggrega version -o json` reports for the
// same binary.
//
// Both answers are taken through the real command tree: the version command's
// own output, and the initialize answer of a daemon started by the serve command
// with its own wiring, speaking over its standard streams. So what is compared is
// the value each surface actually emits, and the one derivation both read is the
// only thing that can make them agree. A handshake answering with a version the
// daemon derived itself, or with the field left empty, disagrees with the version
// command here.
//
// The stamp is installed through the build-stamp seam and asserted on the version
// command's side first, which is what makes the comparison discriminating: an
// ambient value the two surfaces happened to share would pass a bare equality on
// a build where a second derivation agrees by accident.
func TestServeHandshakeReportsTheVersionCommandsVersion(t *testing.T) {
	// Not parallel: mutates the process-global version seam other tests read.
	orig := version
	version = handshakeVersionProbe
	t.Cleanup(func() { version = orig })

	var versionOut bytes.Buffer
	versionRoot := newRootCmd()
	versionRoot.SetOut(&versionOut)
	versionRoot.SetErr(&versionOut)
	versionRoot.SetArgs([]string{"version", "-o", "json"})
	if err := versionRoot.Execute(); err != nil {
		t.Fatalf("aggrega version -o json: %v\n%s", err, versionOut.String())
	}
	var reported struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(versionOut.Bytes(), &reported); err != nil {
		t.Fatalf("decode the version command's output: %v\n%s", err, versionOut.String())
	}
	if want := normalizeVersion(handshakeVersionProbe); reported.Version != want {
		t.Fatalf("the version command reported %q; want the installed stamp in its reported form %q",
			reported.Version, want)
	}

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	// Closing both ends is what lets a daemon still running when the test fails
	// return: a read of standard input ends, and a write of standard output fails.
	t.Cleanup(func() {
		_ = stdinW.Close()
		_ = stdoutR.Close()
	})
	var serveErr bytes.Buffer
	serveRoot := newRootCmd()
	serveRoot.SetIn(stdinR)
	serveRoot.SetOut(stdoutW)
	serveRoot.SetErr(&serveErr)
	serveRoot.SetArgs([]string{"serve", "--workspace-root", t.TempDir()})
	done := make(chan error, 1)
	go func() {
		err := serveRoot.Execute()
		_ = stdoutW.Close()
		done <- err
	}()

	ceiling := responseLivenessCeiling(t)
	if _, err := stdinW.Write(framed(1, "aggrega/initialize", map[string]any{
		"capabilities": map[string]any{},
	})); err != nil {
		t.Fatalf("write the initialize request: %v", err)
	}
	resp := readOneResponse(t, stdoutR, ceiling)

	// The end of standard input is the end of the session, and the daemon exits
	// cleanly on it. Anything else it writes is drained so that no write blocks on
	// a pipe nobody reads.
	_ = stdinW.Close()
	go func() { _, _ = io.Copy(io.Discard, stdoutR) }()
	// The ceiling is a LIVENESS bound, never a performance budget: it separates a
	// daemon that exits from one that never will.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("aggrega serve: %v\n%s", err, serveErr.String())
		}
	case <-time.After(ceiling):
		t.Fatalf("aggrega serve did not exit within the liveness ceiling of %s after its standard input "+
			"closed; this bound is not a performance budget", ceiling)
	}

	if errEnv, ok := resp["error"]; ok {
		t.Fatalf("aggrega/initialize answered with an error envelope: %+v", errEnv)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("the initialize answer carries no result object: %+v", resp)
	}
	got, present := result["version"]
	if !present {
		t.Fatalf("the initialize answer carries no version key: %+v", result)
	}
	if got != reported.Version {
		t.Errorf("the handshake reported version %#v; `aggrega version -o json` reported %q for the same binary",
			got, reported.Version)
	}
}

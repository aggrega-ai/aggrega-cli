// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/pkg/browser"
)

// TestAuthTokenPrintsOnlyTheToken proves `auth token` writes exactly the token
// (plus a trailing newline) to stdout, so command substitution captures the
// token alone.
func TestAuthTokenPrintsOnlyTheToken(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	err := authTokenRun(authTokenConfig{
		TokenGetter: func() (string, error) { return "raw-token-xyz", nil },
		Out:         &out,
	})
	if err != nil {
		t.Fatalf("authTokenRun: %v", err)
	}
	if got := out.String(); got != "raw-token-xyz\n" {
		t.Errorf("stdout = %q, want only the token %q", got, "raw-token-xyz\n")
	}
}

// TestAuthTokenErrorLeavesStdoutEmpty proves an acquisition failure is returned
// and nothing is written to stdout, so a failed substitution never yields a
// partial or stray value.
func TestAuthTokenErrorLeavesStdoutEmpty(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	want := errors.New("acquisition failed")
	err := authTokenRun(authTokenConfig{
		TokenGetter: func() (string, error) { return "", want },
		Out:         &out,
	})
	if err == nil {
		t.Fatal("authTokenRun should return the acquisition error")
	}
	if !errors.Is(err, want) {
		t.Errorf("error = %v, want it to wrap %v", err, want)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty on error", out.String())
	}
}

// TestRootCmdDiscardsBrowserOpenerStdout proves the process discards the
// browser-opener subprocess's stdout, so a browser that prints to its stdout
// (e.g. "Opening in existing browser session.") cannot prepend junk to a
// captured token under `export AGGREGA_IDENTITY_TOKEN=$(aggrega auth token)`.
// The redirect is a one-time process-startup setting (package init), and
// building the root command exercises the same package whose init applies it.
//
// Not parallel: browser.Stdout is a process-global. The test reads it and
// saves/restores it around the assertion so it cannot disturb other tests.
func TestRootCmdDiscardsBrowserOpenerStdout(t *testing.T) {
	// newRootCmd below also assigns the process-global globalDisplay seam other
	// tests read, which is a second reason this test cannot run in parallel.
	saved := browser.Stdout
	defer func() { browser.Stdout = saved }()

	_ = newRootCmd()
	if browser.Stdout != io.Discard {
		t.Errorf("browser.Stdout = %v, want io.Discard so the opener subprocess's stdout never reaches a command's stdout", browser.Stdout)
	}
}

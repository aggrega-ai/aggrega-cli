// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"testing"
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

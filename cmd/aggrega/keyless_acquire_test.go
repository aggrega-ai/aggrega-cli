// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"strings"
	"testing"
)

// failAcquire is an acquisition seam that fails the test if it is ever
// invoked, proving a code path never reaches the interactive browser flow.
func failAcquire(t *testing.T) func() (string, error) {
	t.Helper()
	return func() (string, error) {
		t.Fatal("acquire must not be called on this path")
		return "", nil
	}
}

// TestResolveOrAcquireExplicitFlagShortCircuits proves an explicit flag token
// wins without consulting the TTY or the acquisition seam.
func TestResolveOrAcquireExplicitFlagShortCircuits(t *testing.T) {
	t.Parallel()
	ttyCalls := 0
	tok, err := resolveOrAcquireIDToken(
		"flag-tok",
		func(string) string { return "" },
		func() bool { ttyCalls++; return true },
		failAcquire(t),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "flag-tok" {
		t.Errorf("token = %q, want %q", tok, "flag-tok")
	}
	if ttyCalls != 0 {
		t.Errorf("tty consulted %d times on the explicit path, want 0", ttyCalls)
	}
}

// TestResolveOrAcquireExplicitEnvShortCircuits proves an env-supplied token
// (via the injected lookup) wins without consulting the TTY or acquisition.
func TestResolveOrAcquireExplicitEnvShortCircuits(t *testing.T) {
	t.Parallel()
	ttyCalls := 0
	look := func(k string) string {
		if k == envIdentityToken {
			return "env-tok"
		}
		return ""
	}
	tok, err := resolveOrAcquireIDToken(
		"",
		look,
		func() bool { ttyCalls++; return true },
		failAcquire(t),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "env-tok" {
		t.Errorf("token = %q, want %q", tok, "env-tok")
	}
	if ttyCalls != 0 {
		t.Errorf("tty consulted %d times on the explicit path, want 0", ttyCalls)
	}
}

// TestResolveOrAcquireNoTokenNoTTYRefuses proves the loud refuse fires when no
// token is supplied and stdin is not a terminal, naming the env-export path,
// and that acquisition is never attempted.
func TestResolveOrAcquireNoTokenNoTTYRefuses(t *testing.T) {
	t.Parallel()
	tok, err := resolveOrAcquireIDToken(
		"",
		func(string) string { return "" },
		func() bool { return false },
		failAcquire(t),
	)
	if err == nil {
		t.Fatalf("want a refuse error, got token %q", tok)
	}
	for _, want := range []string{envIdentityToken, envSigstoreIDToken} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refuse message %q does not mention %q", err.Error(), want)
		}
	}
}

// TestResolveOrAcquireNoTokenTTYAcquiresOnce proves the interactive path is
// taken exactly once when no token is supplied and stdin is a terminal, and
// the acquired token is propagated.
func TestResolveOrAcquireNoTokenTTYAcquiresOnce(t *testing.T) {
	t.Parallel()
	acquireCalls := 0
	tok, err := resolveOrAcquireIDToken(
		"",
		func(string) string { return "" },
		func() bool { return true },
		func() (string, error) { acquireCalls++; return "fake-tok", nil },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "fake-tok" {
		t.Errorf("token = %q, want %q", tok, "fake-tok")
	}
	if acquireCalls != 1 {
		t.Errorf("acquire called %d times, want 1", acquireCalls)
	}
}

// TestResolveOrAcquirePrecedenceExplicitOverTTY proves an explicit token wins
// even when a terminal is present — explicit > interactive.
func TestResolveOrAcquirePrecedenceExplicitOverTTY(t *testing.T) {
	t.Parallel()
	tok, err := resolveOrAcquireIDToken(
		"flag-tok",
		func(string) string { return "" },
		func() bool { return true },
		failAcquire(t),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "flag-tok" {
		t.Errorf("token = %q, want %q", tok, "flag-tok")
	}
}

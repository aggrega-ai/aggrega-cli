// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

// fakeEnv builds a lookup over a fixed map so tests inject the environment
// without os.Setenv and stay parallel-safe.
func fakeEnv(vals map[string]string) func(string) string {
	return func(k string) string { return vals[k] }
}

// TestLoginPasswordPrecedence: --password-stdin wins over the environment;
// the environment fills in when stdin is absent; the interactive prompt is the
// last resort (asserted indirectly — when neither stdin nor env supplies a
// password and no interactive terminal is available, login errors rather than
// silently succeeding).
func TestLoginPasswordPrecedence(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	const reg = "registry.example.com"

	// stdin wins even when the env var is set.
	if err := loginRun(loginConfig{
		Registry: reg, Username: "alice", PasswordStdin: true, SkipVerify: true,
		In: strings.NewReader("stdin-secret\n"), Out: io.Discard,
		EnvLookup: fakeEnv(map[string]string{envRegistryPassword: "env-secret"}),
	}); err != nil {
		t.Fatalf("loginRun (stdin): %v", err)
	}
	if got, _ := freshDockerStore(t).Get(context.Background(), reg); got.Password != "stdin-secret" {
		t.Errorf("stdin must win: stored password = %q; want stdin-secret", got.Password)
	}

	// No stdin: the env var supplies the password.
	if err := loginRun(loginConfig{
		Registry: reg, Username: "alice", SkipVerify: true,
		In: strings.NewReader(""), Out: io.Discard,
		EnvLookup: fakeEnv(map[string]string{envRegistryPassword: "env-secret"}),
	}); err != nil {
		t.Fatalf("loginRun (env): %v", err)
	}
	if got, _ := freshDockerStore(t).Get(context.Background(), reg); got.Password != "env-secret" {
		t.Errorf("env must fill in when stdin absent: stored password = %q; want env-secret", got.Password)
	}
}

// TestBoundEnvVarsCombined: the combined enumeration includes the config-backed
// table keys and the command-layer secrets/workspace-root, and excludes the
// sigstore interop alias (which is intentionally not an AGGREGA_ binding).
func TestBoundEnvVarsCombined(t *testing.T) {
	t.Parallel()
	bound := make(map[string]bool)
	for _, n := range allBoundEnvVars() {
		bound[n] = true
	}
	for _, want := range []string{
		"AGGREGA_DEFAULT_REGISTRY", "AGGREGA_SIGNING_FULCIO_URL",
		envIdentityToken, envRegistryPassword, envWorkspaceRoot,
	} {
		if !bound[want] {
			t.Errorf("allBoundEnvVars missing %q", want)
		}
	}
	if bound[envSigstoreIDToken] {
		t.Errorf("SIGSTORE_ID_TOKEN must not appear in allBoundEnvVars (foreign interop alias)")
	}
}

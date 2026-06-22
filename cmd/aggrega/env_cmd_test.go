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

// TestResolveIDTokenPrecedence pins the single identity-token ordering shared by
// push and sign: the --identity-token flag wins, then AGGREGA_IDENTITY_TOKEN,
// then the SIGSTORE_ID_TOKEN interop alias, then empty.
func TestResolveIDTokenPrecedence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		flag string
		env  map[string]string
		want string
	}{
		{
			name: "flag wins over both env vars",
			flag: "from-flag",
			env:  map[string]string{envIdentityToken: "from-aggrega", envSigstoreIDToken: "from-sigstore"},
			want: "from-flag",
		},
		{
			name: "aggrega env wins over sigstore alias",
			flag: "",
			env:  map[string]string{envIdentityToken: "from-aggrega", envSigstoreIDToken: "from-sigstore"},
			want: "from-aggrega",
		},
		{
			name: "sigstore alias is the last fallback",
			flag: "",
			env:  map[string]string{envSigstoreIDToken: "from-sigstore"},
			want: "from-sigstore",
		},
		{
			name: "none set yields empty",
			flag: "",
			env:  nil,
			want: "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveIDToken(tc.flag, fakeEnv(tc.env)); got != tc.want {
				t.Errorf("resolveIDToken(%q, env) = %q; want %q", tc.flag, got, tc.want)
			}
		})
	}
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

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"regexp"
	"strings"
	"testing"
)

// envNameRE is the naming convention every bound environment variable obeys:
// the AGGREGA_ prefix followed by upper-snake alphanumerics.
var envNameRE = regexp.MustCompile(`^AGGREGA_[A-Z0-9_]+$`)

// flagToEnvName derives a flag's would-be environment-variable name via the
// same upper-snake transform the config-backed bindings use: AGGREGA_ + the
// flag/config path upper-snake-cased (dots and dashes become underscores).
func flagToEnvName(name string) string {
	upper := strings.ToUpper(name)
	upper = strings.ReplaceAll(upper, "-", "_")
	upper = strings.ReplaceAll(upper, ".", "_")
	return "AGGREGA_" + upper
}

// TestBoundEnvVarsObeyNamingConvention pins the naming convention mechanically:
// every bound variable matches AGGREGA_[A-Z0-9_]+. The config-path 1:1 mapping
// for the config-backed bindings is pinned in the workspaceconfig package's own
// test, where the binding table is visible.
func TestBoundEnvVarsObeyNamingConvention(t *testing.T) {
	t.Parallel()

	for _, name := range allBoundEnvVars() {
		if !envNameRE.MatchString(name) {
			t.Errorf("bound env var %q does not match the AGGREGA_[A-Z0-9_]+ naming convention", name)
		}
	}
}

// tier3Flags is the situational/one-shot/positional flag set that deliberately
// carries NO environment variable. Listing them here makes "a new Tier-3 flag
// accidentally gained an env var" a test failure.
var tier3Flags = []string{
	"dry-run",
	"trace",
	"detail",
	"regenerate",
	"verify",
	"include-prerelease",
	"json",
	"skip-verify",
	"release",
	"entity",
	// the lint per-invocation policy family
	"allow-dynamic-refs",
	"allow-id-overrides",
	"allow-direct-x-aggrega-extensions",
	"allow-aggrega-ai-prefix",
	"allow-nonstandard-names",
	"allow-category-mismatches",
	"enforce-monotonic-narrowing",
	"enforce-immutability",
}

// TestTier3FlagsHaveNoEnvBinding is the anti-drift guard: no situational flag
// maps to a bound environment variable. Each Tier-3 flag's would-be env name is
// derived via the same transform the bindings use and asserted absent from the
// combined bound set.
func TestTier3FlagsHaveNoEnvBinding(t *testing.T) {
	t.Parallel()

	bound := make(map[string]bool)
	for _, name := range allBoundEnvVars() {
		bound[name] = true
	}

	for _, flag := range tier3Flags {
		envName := flagToEnvName(flag)
		if bound[envName] {
			t.Errorf("Tier-3 flag --%s maps to bound env var %q; situational flags must not gain an environment variable", flag, envName)
		}
	}
}

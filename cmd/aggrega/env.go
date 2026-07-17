// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// workspaceRootFromFlagOrEnv resolves the workspace-root override: the
// --workspace-root flag wins when the operator changed it (even to empty),
// otherwise AGGREGA_WORKSPACE_ROOT fills in, otherwise the empty string lets
// discovery proceed. Reading Changed distinguishes an explicit empty flag from
// an unset one. Both the root command and serve resolve the override through
// this one helper, so the environment fallback is single-sourced.
func workspaceRootFromFlagOrEnv(cmd *cobra.Command, flagValue string, look func(string) string) string {
	if cmd.Flags().Changed("workspace-root") {
		return flagValue
	}
	return envLookupOrGetenv(look)(envWorkspaceRoot)
}

// globalConfigLookup builds the environment lookup that resolves the global
// config path from the --config flag first, then the process environment. When
// configPath is set it wins: a request for the config-override variable returns
// configPath, and every other variable falls through to base. When configPath is
// empty the flag is unset and the returned lookup IS base, so $AGGREGA_CONFIG
// (else the XDG default) governs — the flag overrides the environment only when
// the operator sets it. This is the single construction site for the flag-aware
// lookup; command bodies read the result from the context rather than rebuilding
// it. base is the underlying reader; pass nil for the live process environment.
func globalConfigLookup(configPath string, base func(string) string) func(string) string {
	base = envLookupOrGetenv(base)
	if configPath == "" {
		return base
	}
	return func(name string) string {
		if name == workspaceconfig.EnvConfigOverride {
			return configPath
		}
		return base(name)
	}
}

// envLookupOrGetenv returns look when non-nil, else os.Getenv. The single
// nil-to-production seam every command-layer environment read goes through, so
// the production lookup is named in exactly one place.
func envLookupOrGetenv(look func(string) string) func(string) string {
	if look != nil {
		return look
	}
	return os.Getenv
}

// Command-layer environment variables. The config-backed variables live in the
// workspaceconfig binding table; the names below are the ones resolved at a
// command rather than through the config overlay: the two secrets (never a
// config field) and the workspace-root variable (feeds root discovery, not a
// config field).
const (
	// envIdentityToken supplies the keyless OIDC identity token. A secret —
	// never a config field — resolved at the signer-construction boundary.
	envIdentityToken = "AGGREGA_IDENTITY_TOKEN"
	// envRegistryPassword supplies a registry password for non-interactive
	// login. A secret — never a config field — resolved at the login command.
	envRegistryPassword = "AGGREGA_REGISTRY_PASSWORD"
	// envWorkspaceRoot supplies the workspace root, mirroring --workspace-root.
	// Not a config field; it feeds root discovery, so it is applied at the
	// root-flag fallback rather than through the config overlay.
	envWorkspaceRoot = workspaceconfig.EnvWorkspaceRoot
	// envSigstoreIDToken is the sigstore-ecosystem interop alias for the
	// identity token. Read only by the identity-token resolver, after the
	// AGGREGA_ token; deliberately not an AGGREGA_-prefixed binding so it is
	// not treated as an Aggrega setting.
	envSigstoreIDToken = "SIGSTORE_ID_TOKEN"
)

// allBoundEnvVars returns every AGGREGA_-prefixed environment variable the CLI
// binds — the config-overlay table keys plus the command-resolved secrets and
// the workspace-root variable — as the single combined enumeration the naming
// and anti-drift guards check against. The sigstore interop alias is excluded
// by design: it is a foreign alias, not an Aggrega setting. Order is
// deterministic: table order, then the fixed command-layer trailer.
func allBoundEnvVars() []string {
	tableKeys := workspaceconfig.ConfigBackedEnvVars()
	out := make([]string, 0, len(tableKeys)+3)
	out = append(out, tableKeys...)
	out = append(out, envIdentityToken, envRegistryPassword, envWorkspaceRoot)
	return out
}

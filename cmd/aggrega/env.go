// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
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

// helpCatalogDir is the --catalog flag's help text, held once because four
// commands carry the same flag and a reader comparing two of them must not be
// told two different things about where entities are looked for. It states the
// whole stack, since the part a reader most often needs is the part they did not
// have to type.
const helpCatalogDir = "Catalog directory; repeatable. Each --catalog is prepended to the " +
	"workspace catalog_dirs search stack (first match wins), which ends with the `catalog` " +
	"directory at the workspace root when there is one. A workspace whose catalog_dirs is " +
	"written as an empty list reads no local catalog at all."

// helpOffline is the offline flag's help text, held beside the composition that
// reads the flag so the promise a reader is given and the layer that keeps it
// sit in one place.
//
// It names both directions because the flag is the top of a ladder rather than
// a switch: the same setting is reachable from the configuration file and from
// the environment, so turning it OFF for one run is as much the flag's job as
// turning it on, and a reader meeting a refusal they did not ask for needs the
// off direction first.
const helpOffline = "Refuse every network reach for this one run: the offline posture, turned on " +
	"without editing configuration. Pass --offline=false to turn it off for one run where " +
	"the configuration file or AGGREGA_OFFLINE already turns it on. What refuses is what cannot " +
	"be true without a registry: --upgrade, whose whole job is to float each dependency to the " +
	"highest version a registry currently offers, and the commands whose own work is a network " +
	"round trip — push, push-artifact, and registry update. What still runs is whatever the " +
	"machine can already satisfy: a build against a filled lockfile, --auto-pull over references " +
	"already cached or present in a local catalog, and a dry run, which reports what it would " +
	"publish and calls nothing"

// flagOverlayFromCobra reads the command-line layer of the precedence ladder off
// the invocation: the top layer, above the environment overlay and the
// configuration cascade.
//
// The predicate is whether the reader CHANGED the flag rather than what value it
// holds, and the distinction is the whole reason the layer exists. A boolean's
// zero value and an unset flag are the same bits, so reading the value alone
// would make every invocation look as though it had asked for the flag's default
// — which would silently overwrite a posture the configuration or the
// environment set, and would leave no way at all to turn one off for a single
// run.
//
// It is the single construction site for that layer. The root command calls it
// once and stashes the result on the workspace resolver, so every command body
// reads one composed configuration rather than each re-reading the flag.
func flagOverlayFromCobra(cmd *cobra.Command, offline bool) workspaceconfig.FlagOverlay {
	var out workspaceconfig.FlagOverlay
	if cmd.Flags().Changed(workspaceconfig.OfflineSettingKey) {
		out.Offline = &offline
	}
	return out
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
	// never a config field — resolved at the signer-construction boundary. The
	// name's home is the signing package, beside the ladder that reads it.
	envIdentityToken = signing.EnvIdentityToken
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
	// not treated as an Aggrega setting. The name's home is the signing
	// package, beside the ladder that reads it.
	envSigstoreIDToken = signing.EnvSigstoreIDToken
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

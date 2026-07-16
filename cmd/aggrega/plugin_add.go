// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// providerSocketKind is the socket argument that binds a capability to a
// provider-rendering plugin (the `plugins.providers` list), as opposed to the
// config sockets that take a bare plugin attachment.
const providerSocketKind = "provider"

// configSocketKinds is the set of workspace-level config sockets a plugin
// attaches to by its bare entity identity. The provider socket is handled
// separately because it binds a capability rather than attaching a plugin to
// a socket list.
var configSocketKinds = map[string]bool{
	"mutator":  true,
	"policy":   true,
	"linter":   true,
	"importer": true,
}

// newPluginAddCmd builds the `plugin add` command: it searches the configured
// registry for a plugin serving the named socket, resolves the match to one
// plugin identity, and writes the attachment into the workspace config file —
// either a `plugins.<socket>` list entry (mutator / policy / linter / importer)
// or a `plugins.providers` capability binding (provider).
func newPluginAddCmd() *cobra.Command {
	var target string
	var global bool
	var workspace bool
	c := &cobra.Command{
		Use:   "add <socket> <query>",
		Short: "Search the registry and attach a plugin to a workspace socket",
		Long: `Search the configured registry for a plugin serving the named socket and write
the attachment into the workspace config file.

The socket is one of the config sockets — mutator, policy, linter, importer — or
provider. The query is a case-insensitive substring match on the plugin
repository path, scoped to the chosen socket; it must resolve to exactly one
plugin (an exact plugin-name match wins when several share a substring), else the
command halts and lists the candidates so you can narrow it.

For a config socket the latest published version is pinned and the bare plugin
entity identity (<socket>/<name>:<version>) is added to the plugins.<socket> list.
For the provider socket, --target names the capability the binding serves and
the plugin's oci:// identity is written as a plugins.providers entry.

The entry is written to the workspace config file, preserving existing settings.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return pluginAddRun(cmd.Context(), pluginAddConfig{
				Socket:    args[0],
				Query:     args[1],
				Target:    target,
				Global:    global,
				Workspace: workspace,
				ws:        rw,
				Out:       cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().StringVar(&target, "target", "", "Capability the provider binding serves (required for, and only valid with, the provider socket)")
	c.Flags().BoolVar(&global, "global", false, "Write the attachment to the global config shared across every workspace.")
	c.Flags().BoolVar(&workspace, "workspace", false, "Write the attachment to this project's workspace config. A plugin attachment defaults here.")
	return c
}

// pluginAddConfig is the input contract for `aggrega plugin add`, populated by
// the cobra command from its arguments and flags.
type pluginAddConfig struct {
	Socket string
	Query  string
	Target string

	// Global and Workspace are the operator's write-target overrides. A plugin
	// attachment is build-scoped, so it defaults to this project's workspace
	// config; --global forces the global file and --workspace forces the
	// workspace file. Setting both is a usage error.
	Global    bool
	Workspace bool

	ws  *workspaceconfig.ResolvedWorkspace
	Out interface{ Write([]byte) (int, error) }
	// lookup reads the environment for global-write-path resolution. Nil selects
	// the live process environment; tests inject a frozen lookup so resolution
	// is deterministic and parallel-safe.
	lookup func(string) string
}

// pluginAddRun searches the workspace registry for a plugin serving cfg.Socket,
// resolves the query to a single plugin repository, and writes the attachment
// into the workspace config file. A config socket attaches the bare plugin
// entity identity at its latest published version; the provider socket binds
// cfg.Target to the plugin's oci:// identity.
func pluginAddRun(ctx context.Context, cfg pluginAddConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}

	socket := cfg.Socket
	isProvider := socket == providerSocketKind
	if !isProvider && !configSocketKinds[socket] {
		return fmt.Errorf("aggrega plugin add: unknown socket %q; expected one of mutator, policy, linter, importer, provider", socket)
	}
	if isProvider && cfg.Target == "" {
		return fmt.Errorf("aggrega plugin add: the provider socket requires --target naming the capability the binding serves")
	}
	if !isProvider && cfg.Target != "" {
		return fmt.Errorf("aggrega plugin add: --target applies only to the provider socket")
	}

	// Resolve the write destination up front — it does not depend on the search,
	// so a write-target flag misuse (both --global and --workspace) or an
	// unresolvable global path fails fast, before any registry search.
	op := workspaceconfig.OpConfigSocket
	if isProvider {
		op = workspaceconfig.OpProviderBinding
	}
	configPath, err := workspaceconfig.WriteDestination(op, cfg.Global, cfg.Workspace, rw.Root(), cfg.lookup)
	if err != nil {
		return err
	}

	reg, host, err := registrySearchTarget(rw.Config())
	if err != nil {
		return fmt.Errorf("aggrega plugin add: %w", err)
	}
	matches, err := searchRegistry(ctx, reg, cfg.Query, socketSearchScope(socket))
	if err != nil {
		return err
	}
	repoPath, err := resolveSinglePluginRepo(matches, cfg.Query, socket)
	if err != nil {
		return err
	}

	if isProvider {
		// The provider binding carries the oci:// artifact identity built from the
		// repository path; a config-socket attachment instead carries the bare
		// entity identity, which an oci:// reference is refused for.
		uri := "oci://" + host + "/" + repoPath
		if err := workspaceconfig.UpsertProviderBinding(configPath, cfg.Target, uri); err != nil {
			return fmt.Errorf("aggrega plugin add: write %s: %w", configPath, err)
		}
		fmt.Fprintf(cfg.Out, "Bound capability %q to %s in %s\n", cfg.Target, uri, configPath)
		return nil
	}

	// Config socket: strip the plugin/ path prefix to the bare entity identity and
	// pin the latest published version so the attachment is version-pinned.
	entity := strings.TrimPrefix(repoPath, catalog.KindPathSegment("Plugin")+"/")
	version, err := resolveLatestPluginVersion(ctx, host, repoPath)
	if err != nil {
		return fmt.Errorf("aggrega plugin add: %w", err)
	}
	if version == "" {
		return fmt.Errorf("aggrega plugin add: %s has no published version tag to attach; publish a version first", repoPath)
	}
	identity := entity + ":" + version
	if err := workspaceconfig.UpsertPluginSocketRef(configPath, socket, identity); err != nil {
		return fmt.Errorf("aggrega plugin add: write %s: %w", configPath, err)
	}
	fmt.Fprintf(cfg.Out, "Attached %s to the %s socket in %s\n", identity, socket, configPath)
	return nil
}

// socketSearchScope admits only the repository paths a plugin serving socket
// publishes under — the plugin/<socket>/ path prefix — so a search for one
// socket never returns a plugin bound for another.
func socketSearchScope(socket string) searchScope {
	prefix := catalog.KindPathSegment("Plugin") + "/" + socket + "/"
	return func(repoPath string) bool {
		return strings.HasPrefix(repoPath, prefix)
	}
}

// resolveSinglePluginRepo reduces the scoped search matches to exactly one
// plugin repository. A single match is used directly; when several match the
// substring an exact plugin-name (final path segment) hit is preferred; a query
// that still resolves to zero or more than one plugin halts loudly with the
// candidates rather than guessing which the operator meant.
func resolveSinglePluginRepo(matches []string, query, socket string) (string, error) {
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("aggrega plugin add: no %s plugin in the registry matches %q", socket, query)
	case 1:
		return matches[0], nil
	}
	var exact []string
	for _, m := range matches {
		if path.Base(m) == query {
			exact = append(exact, m)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	return "", fmt.Errorf("aggrega plugin add: %q matches %d %s plugins (%s); narrow the query to one",
		query, len(matches), socket, strings.Join(matches, ", "))
}

// pluginRepoTagLister lists the tags a registry serves for a plugin repository.
// It is a package-level seam so config-socket version resolution runs against a
// fake in tests; production lists live tags through the shared repository client
// so the loopback plain-HTTP and credential wiring match every other pull.
var pluginRepoTagLister = func(ctx context.Context, host, repoPath string) ([]string, error) {
	repo, err := oci.NewRemoteRepository(host + "/" + repoPath)
	if err != nil {
		return nil, err
	}
	return oci.ListTags(ctx, repo)
}

// resolveLatestPluginVersion returns the highest strict-SemVer tag published for
// the plugin repository, or the empty string when the repository carries no
// conforming version tag. Pre-release tags are excluded from the selection, the
// same rule the publishing and deprecation paths apply.
func resolveLatestPluginVersion(ctx context.Context, host, repoPath string) (string, error) {
	tags, err := pluginRepoTagLister(ctx, host, repoPath)
	if err != nil {
		return "", fmt.Errorf("list versions for %s: %w", repoPath, err)
	}
	return oci.SelectLatestVersion(tags, false /* includePreRelease */), nil
}

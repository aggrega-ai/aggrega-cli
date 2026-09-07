// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// newPluginAddCmd builds the `plugin add` command: it searches the configured
// registry for a plugin of the named type, resolves the match to one plugin
// identity, and either writes the attachment into the workspace config file — a
// `plugins.<type>` list entry (mutator / policy / linter / importer) or a
// `plugins.providers` capability binding (provider) — or, for the cli type,
// installs the plugin as a top-level aggrega command.
func newPluginAddCmd() *cobra.Command {
	var capability string
	var role string
	var global bool
	var workspace bool
	var acceptCaps bool
	var assumeYes bool
	c := &cobra.Command{
		Use:   "add <type> <query>",
		Short: "Search the registry and add a plugin of the named type",
		Long: `Search the configured registry for a plugin of the named type, resolve the
query to one plugin, and add it to your configuration.

Arguments:
  <type>   The plugin socket to add for. One of: provider, governance,
           linter, importer, or cli. The governance type requires --role.
  <query>  A case-insensitive substring matched against the plugin names of the
           chosen type. It must resolve to exactly one plugin — an exact
           plugin-name match wins when several share the substring; otherwise the
           command lists the candidates so you can narrow it.

What add writes depends on the type:

  - A socket type (governance, linter, importer) pins the latest published
    version and adds the bare plugin identity (<type>/<name>:<version>) to the
    plugins.<type> list. A governance entry also records its --role
    (label-transform, ir-verdict, or render-verdict).
  - The provider type writes the plugin's oci:// identity as a plugins.providers
    entry; --capability names the capability the binding serves, preserving
    existing settings.
  - The cli type fetches the plugin from the registry, verifies it, and caches
    its bytes, then pins it into the plugins.cli list as a top-level aggrega
    command — no separate pull step. Its resolved reference and the content
    digest of the verified bytes are recorded so the command runs those exact
    bytes, resolving from the local cache on later runs with no further network
    access. A cli plugin defaults to the global config, so its command is
    available in every workspace; --workspace pins it to this project instead.
    Its declared capabilities are confirmed before installing, and
    --accept-capabilities or --yes skips that prompt for non-interactive use.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return pluginAddRun(cmd.Context(), pluginAddConfig{
				Socket:     args[0],
				Query:      args[1],
				Capability: capability,
				Role:       role,
				Global:     global,
				Workspace:  workspace,
				AcceptCaps: acceptCaps,
				AssumeYes:  assumeYes,
				ws:         rw,
				In:         cmd.InOrStdin(),
				Out:        cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().StringVar(&capability, "capability", "", "Capability the provider binding serves (required for, and only valid with, the provider type)")
	c.Flags().StringVar(&role, "role", "", "Governance attachment role: label-transform, ir-verdict, or render-verdict (required for, and only valid with, the governance type)")
	c.Flags().BoolVar(&global, "global", false, "Write the attachment to the global config shared across every workspace.")
	c.Flags().BoolVar(&workspace, "workspace", false, "Write the attachment to this project's workspace config. A provider or socket attachment defaults here; a cli plugin defaults to the global config.")
	c.Flags().BoolVar(&acceptCaps, "accept-capabilities", false, "Accept a cli plugin's declared capabilities without the interactive prompt (cli type only)")
	c.Flags().BoolVar(&assumeYes, "yes", false, "Skip the cli capability-confirmation prompt for non-interactive use (cli type only)")
	return c
}

// pluginAddConfig is the input contract for `aggrega plugin add`, populated by
// the cobra command from its arguments and flags.
type pluginAddConfig struct {
	Socket     string
	Query      string
	Capability string
	Role       string

	// Global and Workspace are the operator's write-target overrides. A plugin
	// attachment is build-scoped, so it defaults to this project's workspace
	// config; --global forces the global file and --workspace forces the
	// workspace file. Setting both is a usage error.
	Global    bool
	Workspace bool

	// AcceptCaps and AssumeYes gate the cli type's capability-confirmation
	// prompt. They are consulted only when the type is cli; on any other type a
	// set flag is a usage error, mirroring the provider-only --capability guard.
	AcceptCaps bool
	AssumeYes  bool

	ws  *workspaceconfig.ResolvedWorkspace
	In  io.Reader
	Out interface{ Write([]byte) (int, error) }
}

// pluginAddRun runs the addition and renders the command line's own account of
// what it did.
//
// The operation itself lives in the plugin operations layer, which every surface
// reaches; this translates the command's flags into that layer's options and turns
// the result into the sentence a terminal expects. Which field the result filled
// is what says what landed — a bare version-pinned identity for a config socket, an
// oci:// artifact identity for a provider binding — so the sentence is chosen from
// the answer rather than re-derived from the arguments.
//
// The cli type prints nothing here: installing a command extension is performed by
// the installer this command supplies, which reports the commands it registered as
// part of doing so, and a second sentence would announce the same install twice.
func pluginAddRun(ctx context.Context, cfg pluginAddConfig) error {
	res, err := pluginops.Add(ctx, cfg.ws, pluginops.AddOptions{
		Socket:             cfg.Socket,
		Query:              cfg.Query,
		Capability:         cfg.Capability,
		Role:               cfg.Role,
		Global:             cfg.Global,
		Workspace:          cfg.Workspace,
		AcceptCapabilities: cfg.AcceptCaps,
		AssumeYes:          cfg.AssumeYes,
		InstallCLIPlugin:   installCLIPlugin,
		In:                 cfg.In,
		Notices:            cfg.Out,
	})
	if err != nil {
		return err
	}
	switch {
	case res.URI != "":
		fmt.Fprintf(cfg.Out, "Bound capability %q to %s in %s\n", res.Capability, res.URI, res.ConfigPath)
	case res.Identity != "":
		fmt.Fprintf(cfg.Out, "Added %s as a %s plugin in %s\n", res.Identity, res.Socket, res.ConfigPath)
	}
	return nil
}

// installCLIPlugin is the command-extension installer the operations layer is
// handed.
//
// It exists as a named adapter rather than a closure at the call site because it
// is supplied from two places — this command, and the daemon's wiring — and a
// closure written twice is the same function written twice. The install itself
// stays in this package because its collision guard reads the freshly built
// command tree, which is what authoritatively decides the built-in names and
// cannot be enumerated from outside package main without becoming a second copy
// that drifts from the tree.
func installCLIPlugin(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, req pluginops.CLIInstallRequest) error {
	return pluginInstallRun(ctx, rw, req.Reference, req.Global, req.Workspace, req.AcceptCapabilities, req.AssumeYes, req.In, req.Out)
}

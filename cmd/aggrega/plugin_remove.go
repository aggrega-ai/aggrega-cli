// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// newPluginRemoveCmd builds the `plugin remove` command: the inverse of
// `plugin add`. It removes a plugin from a config file so it no longer
// participates — a `plugins.<type>` list entry (mutator / policy / linter /
// importer), a `plugins.providers` capability binding (provider), or a
// `plugins.cli` command (cli). No registry search is involved: remove operates
// on the config text alone.
func newPluginRemoveCmd() *cobra.Command {
	var global bool
	var workspace bool
	c := &cobra.Command{
		Use:   "remove <type> <query>",
		Short: "Remove a configured plugin of the named type",
		Long: `Remove a configured plugin of the named type from a config file so it no longer
participates.

The type is one of provider, governance, linter, importer, or cli — the same
set 'plugin add' accepts. The query identifies the entry to remove: the plugin's
short name (matched against 'add's short name so remove is symmetric with add),
its full pinned reference, or, for the provider type, the capability the binding
serves. A query that matches nothing halts and leaves the config untouched, so a
typo never silently removes the wrong plugin.

A provider or socket attachment defaults to this project's workspace config; a cli
command defaults to the global config shared across every workspace. --global and
--workspace force the file the removal targets, overriding the default.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return pluginRemoveRun(pluginRemoveConfig{
				Socket:    args[0],
				Query:     args[1],
				Global:    global,
				Workspace: workspace,
				ws:        rw,
				Out:       cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().BoolVar(&global, "global", false, "Remove from the global config shared across every workspace.")
	c.Flags().BoolVar(&workspace, "workspace", false, "Remove from this project's workspace config. A provider or socket attachment defaults here; a cli command defaults to the global config.")
	return c
}

// pluginRemoveConfig is the input contract for `aggrega plugin remove`, populated
// by the cobra command from its arguments and flags.
type pluginRemoveConfig struct {
	Socket string
	Query  string

	// Global and Workspace are the operator's write-target overrides, mirroring
	// `plugin add`. A provider or socket removal defaults to the workspace config
	// and a cli removal defaults to the global config; --global forces the global
	// file and --workspace forces the workspace file. Setting both is a usage
	// error, surfaced by WriteDestination.
	Global    bool
	Workspace bool

	ws  *workspaceconfig.ResolvedWorkspace
	Out interface{ Write([]byte) (int, error) }
}

// pluginRemoveRun runs the removal and renders the command line's own account of
// what it did.
//
// The operation itself lives in the plugin operations layer, which every surface
// reaches; this translates the command's flags into that layer's options and turns
// the result into the sentence a terminal expects. The operation halts loudly when
// the type is unknown or when the query matches no entry, so a removal reported
// here is never a silent no-op.
func pluginRemoveRun(cfg pluginRemoveConfig) error {
	res, err := pluginops.Remove(cfg.ws, pluginops.RemoveOptions{
		Socket:    cfg.Socket,
		Query:     cfg.Query,
		Global:    cfg.Global,
		Workspace: cfg.Workspace,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(cfg.Out, "Removed %s plugin %q from %s\n", res.Socket, res.Query, res.ConfigPath)
	return nil
}

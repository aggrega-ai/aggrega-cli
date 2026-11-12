// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// newPluginSyncCmd builds the `plugin sync` command: it aligns a plugin's
// machine-owned records in its entity file with the binary — the host-function
// set derived from the .wasm import section, and the CLI command mirror read from
// the plugin's describe() self-report.
func newPluginSyncCmd() *cobra.Command {
	var pluginPath string
	c := &cobra.Command{
		Use:   "sync <entity-path>",
		Short: "Sync a plugin's machine-owned records (host functions, CLI commands) in its entity file with its .wasm",
		Long: `Sync a plugin's machine-owned records in a Plugin entity file with its .wasm.

Two records are not authored by hand: the set of host functions a plugin may
call, derived from the plugin's .wasm import section, and the top-level commands a
CLI plugin contributes, read from the plugin's describe() self-report. Both are
ground truth in the binary. This command reads the .wasm, derives the
host-function set in canonical form and the command mirror in describe()-declared
order, and aligns the entity's spec.runtime.hostFunctions and spec.commands with
them — creating the runtime block when the file has none. Only those blocks
change; every comment, blank line, and other field is preserved byte-for-byte. A
plugin that imports no host functions or contributes no commands records an
explicit empty list, so each field is always present and reviewable.

The write is idempotent: re-running when the records already match the .wasm
leaves the file untouched. Commit the result; publish (aggrega push) then only
verifies the committed records against the .wasm and refuses to publish a file
whose records are out of sync with its binary.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if pluginPath == "" {
				return fmt.Errorf("aggrega plugin sync: --plugin <path-to-.wasm> is required (the recorded host functions and commands are derived from the .wasm)")
			}
			return stampPluginFile(cmd.Context(), cmd.OutOrStdout(), args[0], pluginPath)
		},
	}
	c.Flags().StringVar(&pluginPath, "plugin", "", "Path to the plugin's .wasm artifact whose imports and describe() determine the recorded host functions and commands")
	return c
}

// stampPluginFile runs the record alignment and renders the command line's own
// account of what it did.
//
// The operation itself lives in the plugin operations layer, which every surface
// reaches; this translates the command's arguments into that layer's options and
// turns the result into the sentence a terminal expects. The two sentences are the
// two outcomes the operation distinguishes: a write, and a file whose records were
// already what the binary implies — which is left byte-identical.
//
// The command mirror is formatted here rather than carried on the result as text,
// because the result carries the described commands themselves and each surface
// renders what it needs from that one value.
//
// ctx is the caller's and reaches the guest the alignment describes, so a reader
// who withdraws the run ends the describe rather than waiting for it.
func stampPluginFile(ctx context.Context, out io.Writer, entityPath, pluginPath string) error {
	res, err := pluginops.Sync(ctx, pluginops.SyncOptions{
		EntityPath: entityPath,
		PluginPath: pluginPath,
	})
	if err != nil {
		return err
	}
	if !res.Changed {
		fmt.Fprintf(out, "Already current: %s records the host functions %v and commands %s the .wasm implies\n",
			res.EntityPath, res.HostFunctions, wasm.FormatCommandMirror(res.Commands))
		return nil
	}
	fmt.Fprintf(out, "Recorded host functions %v and commands %s into %s\n",
		res.HostFunctions, wasm.FormatCommandMirror(res.Commands), res.EntityPath)
	return nil
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = newRootCmd()

// newRootCmd builds the aggrega command tree. Each invocation returns a
// fresh, independent tree: a new root with its subcommands freshly
// constructed, so command-level state (such as the per-run SilenceUsage
// flag set by silenceUsageOnRun) cannot leak between trees.
//
// SilenceErrors is set on the root so cobra does not print its own
// "Error:" line when a command fails — Execute renders the single
// formatted diagnostic instead. silenceUsageOnRun then wraps every
// command's RunE so that, once a command's own work begins, the Usage
// block is suppressed on failure; errors cobra detects before RunE runs
// (unknown flag, bad argument, missing required flag) still surface the
// Usage block, because that is genuinely a misuse of the command.
func newRootCmd() *cobra.Command {
	var workspaceRootFlag string
	root := &cobra.Command{
		Use:           "aggrega",
		Short:         "Aggrega compiler CLI",
		Long:          "Aggrega compiles declarative aggregates into intermediate representation and deployment artifacts.",
		SilenceErrors: true,
		// Attach the lazy workspace resolver to every command's context.
		// Resolution itself is deferred until a command first asks for it
		// (workspaceFromContext), so version/login/logout run without
		// discovering a workspace.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cmd.SetContext(withWorkspaceResolver(cmd.Context(), workspaceRootFlag))
			return nil
		},
	}
	root.PersistentFlags().StringVar(&workspaceRootFlag, "workspace-root", "",
		"absolute path to use as the workspace root (skips discovery)")
	root.AddCommand(
		newBuildCmd(),
		newPullCmd(),
		newInspectCmd(),
		newServeCmd(),
		newLintCmd(),
		newPushCmd(),
		newSignCmd(),
		newRegistryCmd(),
		newLoginCmd(),
		newLogoutCmd(),
		newVersionCmd(),
	)
	silenceUsageOnRun(root)
	return root
}

// silenceUsageOnRun wraps the RunE of every command in the subtree
// rooted at c so a failure originating in the command's own work does
// not print the Usage block. The wrapper sets SilenceUsage as its first
// action, then delegates to the original RunE. Because the wrapper IS
// the RunE, it runs only after cobra has parsed flags, validated
// arguments, and checked required flags — so those genuine usage errors
// keep their Usage block, while errors from the command body do not.
//
// Centralizing the wrap (rather than setting SilenceUsage inside each
// command) keeps the behavior uniform and drift-proof: a command added
// to the tree inherits it without a per-command opt-in to forget.
func silenceUsageOnRun(c *cobra.Command) {
	for _, sub := range c.Commands() {
		silenceUsageOnRun(sub)
	}
	if c.RunE != nil {
		orig := c.RunE
		c.RunE = func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			return orig(cmd, args)
		}
	}
}

// Execute runs the root command. On cobra dispatch error — unknown
// subcommand, flag-parse failure, RunE error from any subcommand —
// the error is printed to stderr and the process exits with status 1.
// Successful dispatch returns normally; the caller (main) then
// terminates with exit status 0. The function intentionally handles
// the error+exit boundary itself rather than returning the error to
// the caller, so main remains a thin shim. Cobra's own error and usage
// printing is suppressed (see newRootCmd), so this is the single point
// at which a failure reaches the user.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, formatCompilerError(err))
		os.Exit(1)
	}
}

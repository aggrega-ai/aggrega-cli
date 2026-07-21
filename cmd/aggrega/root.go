// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/pkg/browser"
	"github.com/spf13/cobra"
)

// init discards the browser-opener's stdout process-wide. The interactive
// OIDC flow opens a browser via a subprocess (xdg-open / open / start), and
// some browsers print a line such as "Opening in existing browser session."
// to their stdout. Under `export AGGREGA_IDENTITY_TOKEN=$(aggrega auth token)`
// that capture pipe is the process's stdout, so the opener's stdout would
// prepend junk to the token and yield a malformed JWT. browser.Stdout is the
// io.Writer the opener assigns to the subprocess's stdout; pointing it at
// io.Discard makes os/exec wire the child a pipe (copied into the discard
// writer) rather than letting the child inherit fd 1, so the opener's stdout
// never reaches any command's stdout. This is set once at process startup and
// never toggled — before any goroutine exists, so there is no data race on the
// global. It applies to every command (auth, sign, push) because the only
// thing that opens a browser is the OIDC flow, whose own human-facing prompts
// go to stderr; discarding the opener subprocess's stdout is harmless
// everywhere. browser.Stderr is left at its default (os.Stderr) so those
// prompts still reach the terminal.
func init() {
	browser.Stdout = io.Discard
}

//go:generate make -C ../.. docs

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
	var configPathFlag string
	var noColorFlag, plainFlag bool
	root := &cobra.Command{
		Use:           "aggrega",
		Short:         "Aggrega compiler CLI",
		Long:          "Aggrega compiles declarative aggregates into intermediate representation and deployment artifacts.",
		SilenceErrors: true,
		// Attach the lazy workspace resolver to every command's context, and
		// resolve the process-global display decision once. Workspace
		// resolution itself is deferred until a command first asks for it
		// (workspaceFromContext), so version/login/logout run without
		// discovering a workspace. The display decision is set here — before
		// any command's RunE — so the color and progress surfaces read one
		// single-sourced value.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			globalDisplay = resolveGlobalDisplay(noColorFlag, plainFlag, os.LookupEnv)
			root := workspaceRootFromFlagOrEnv(cmd, workspaceRootFlag, nil)
			cmd.SetContext(withWorkspaceResolver(cmd.Context(), root, globalConfigLookup(configPathFlag, nil)))
			return nil
		},
	}
	root.PersistentFlags().StringVar(&workspaceRootFlag, "workspace-root", "",
		"absolute path to use as the workspace root (skips discovery)")
	root.PersistentFlags().StringVar(&configPathFlag, "config", "",
		"path to the global config file, overriding $AGGREGA_CONFIG and the default location. Workspace-tree config files still layer on top")
	root.PersistentFlags().BoolVar(&noColorFlag, "no-color", false,
		"Disable ANSI color in all output. Also honored via the NO_COLOR environment variable")
	root.PersistentFlags().BoolVar(&plainFlag, "plain", false,
		"Accessible mode: disable color and draw plain, static progress lines with no spinner animation or cursor repaint, even on a terminal. Intended for screen readers")
	root.AddCommand(
		newBuildCmd(),
		newAcquireCmd(),
		newPullCmd(),
		newInspectCmd(),
		newImportCmd(),
		newGetSchemaCmd(),
		newServeCmd(),
		newMCPCmd(),
		newLintCmd(),
		newValidateCmd(),
		newPushCmd(),
		newPushArtifactCmd(),
		newSignCmd(),
		newDeprecateCmd(),
		newAuthCmd(),
		newRegistryCmd(),
		newPluginCmd(),
		newSearchCmd(),
		newConfigCmd(),
		newCacheCmd(),
		newLoginCmd(),
		newLogoutCmd(),
		newVersionCmd(),
	)
	// Register a command per installed CLI plugin AFTER the static commands, so
	// the live tree is the authoritative built-in set that always wins a name
	// collision. This runs before silenceUsageOnRun so the dynamic commands
	// inherit the same usage-suppression wrap as the built-ins.
	registerInstalledCLIPlugins(root)
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

// Execute runs the root command. Successful dispatch returns normally; the
// caller (main) then terminates with exit status 0. A non-zero outcome exits the
// process with the code executeRoot computes. The function intentionally handles
// the error+exit boundary itself rather than returning the error to the caller,
// so main remains a thin shim. Cobra's own error and usage printing is suppressed
// (see newRootCmd), so this is the single point at which a failure reaches the
// user.
func Execute() {
	if code := executeRoot(rootCmd); code != 0 {
		os.Exit(code)
	}
}

// executeRoot runs root and maps its outcome to a process exit code without
// exiting itself, so the exit boundary is testable. A nil error is exit 0. An
// installed plugin that ran and exited non-zero surfaces as an exitCodeError,
// whose code is propagated verbatim with no formatted diagnostic — a non-zero
// command result is the plugin's status, not a host error. Any other error is the
// single formatted diagnostic to stderr and exit 1; when it is cobra's
// unknown-command error for a discoverable plugin name, an install hint is
// appended.
func executeRoot(root *cobra.Command) int {
	err := root.Execute()
	if err == nil {
		return 0
	}
	var ece exitCodeError
	if errors.As(err, &ece) {
		return ece.code
	}
	fmt.Fprintln(os.Stderr, unknownCommandSuggestion(formatCompilerError(err), err))
	return 1
}

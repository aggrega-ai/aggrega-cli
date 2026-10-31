// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/pkg/browser"
	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
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
	var noColorFlag, plainFlag, offlineFlag bool
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
		//
		// The compiled-module cache's size cap is armed here for the same
		// reason: this runs before any command body, so it is before any plugin
		// could be loaded, and the cap is applied when the cache is first
		// opened. It is armed with a function rather than a value so the
		// deferral above survives — a command that loads no plugin resolves no
		// workspace and reads no configuration.
		//
		// Whether this invocation may show the first-run configuration notice is
		// decided here too, because this is the only point that holds both the
		// resolver that writes the notice and the command being run. The deferral
		// is what keeps the decision honest: a command that never reads
		// configuration never reaches the notice either.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			globalDisplay = resolveGlobalDisplay(noColorFlag, plainFlag, os.LookupEnv)
			// The environment is the process's own on every production invocation;
			// a test running the tree as a stated machine supplies another
			// (withMachine), and the resolver picks the platform up from the same
			// context.
			_, env := machineFromContext(cmd.Context())
			root, rootSource := workspaceRootFromFlagOrEnv(cmd, workspaceRootFlag, env)
			ctx := withWorkspaceResolver(cmd.Context(), root,
				globalConfigLookup(configPathFlag, env),
				flagOverlayFromCobra(cmd, offlineFlag, configPathFlag))
			nameWorkspaceRootSource(ctx, rootSource)
			if cmd.CommandPath() == configInitInvocation {
				withholdFirstRunNotice(ctx)
			}
			cmd.SetContext(ctx)
			armCompiledModuleCacheCap(ctx)
			return nil
		},
	}
	root.PersistentFlags().StringVar(&workspaceRootFlag, workspaceRootFlagName, "",
		"absolute path to use as the workspace root (skips discovery)")
	root.PersistentFlags().StringVar(&configPathFlag, workspaceconfig.ConfigFlagName, "",
		"path to the global config file, overriding the AGGREGA_CONFIG environment variable and the default location. Workspace-tree config files still layer on top")
	root.PersistentFlags().BoolVar(&noColorFlag, "no-color", false,
		"Disable ANSI color in all output. Also honored via the NO_COLOR environment variable")
	root.PersistentFlags().BoolVar(&plainFlag, "plain", false,
		"Accessible mode: disable color and draw plain, static progress lines with no spinner animation or cursor repaint, even on a terminal. Intended for screen readers")
	root.PersistentFlags().BoolVar(&offlineFlag, workspaceconfig.OfflineSettingKey, false, helpOffline)
	// Every capability is attached by naming the operation it carries, so the
	// command line registers nothing the operation table does not account for and
	// nothing under a path the table does not give. The grouping commands run
	// nothing and carry no capability, so they are attached as the groups they
	// are; their children name their own operations where they are built.
	mustAttach(root, newBuildCmd(), "build")
	mustAttach(root, newPullCmd(), "pull")
	mustAttach(root, newPinCmd(), "pin")
	mustAttach(root, newInspectCmd(), "inspect")
	mustAttach(root, newImportCmd(), "import")
	mustAttach(root, newEditDocumentCmd(), "document-edit")
	mustAttach(root, newGetSchemaCmd(), "get-schema")
	mustAttach(root, newScaffoldCmd(), "scaffold-entity")
	mustAttach(root, newExplainCmd(), "explain-error")
	mustAttach(root, newServeCmd(), "serve")
	mustAttach(root, newMCPCmd(), "mcp")
	mustAttach(root, newLintCmd(), "lint")
	mustAttach(root, newValidateCmd(), "validate")
	mustAttach(root, newPushCmd(), "push")
	mustAttach(root, newPushArtifactCmd(), "push-artifact")
	mustAttach(root, newSignCmd(), "sign")
	mustAttach(root, newDeprecateCmd(), "deprecate")
	attachAuthCommands(root)
	attachRegistryCommands(root)
	attachPluginCommands(root)
	mustAttach(root, newSearchCmd(), "search")
	attachConfigCommands(root)
	attachCacheCommands(root)
	attachSkillsCommands(root)
	mustAttach(root, newLoginCmd(), "login")
	mustAttach(root, newLogoutCmd(), "logout")
	mustAttach(root, newVersionCmd(), "version")
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

// Execute builds the command tree and runs it. Successful dispatch returns
// normally; the caller (main) then terminates with exit status 0. A non-zero
// outcome exits the process with the code executeRoot computes. The function
// intentionally handles the error+exit boundary itself rather than returning the
// error to the caller, so main remains a thin shim. Cobra's own error and usage
// printing is suppressed (see newRootCmd), so this is the single point at which
// a failure reaches the user.
//
// The tree is constructed here rather than held in a package variable, and that
// placement is load-bearing. Construction registers the configured extension
// commands, which resolves the workspace and its configuration cascade — real
// work against the file system. A package variable would run that work at
// process start, before this function is entered, so any program that links this
// package would pay it whether or not it ever dispatches a command.
//
// This is also where the run's context is created, because it is the only place
// that owns the whole process: a stop request arrives for the process, not for a
// command, so the context every command inherits has to start here for any of
// them to be able to observe one.
//
// The consoles behind standard output and standard error are prepared here too,
// before any command writes to them, for the same reason: a console's mode
// belongs to the process rather than to a command. Where a console has to be
// asked to interpret escape sequences it is asked once, and put back the way it
// was found when the command returns; where it refuses, color and progress
// write plain text to it.
func Execute() {
	restoreTerminals := prepareTerminals(os.Stdout, os.Stderr)
	ctx, stop := withStopSignals(context.Background())
	defer stop()
	code := executeRoot(ctx, newRootCmd())
	restoreTerminals()
	if code != 0 {
		os.Exit(code)
	}
}

// executeRoot runs root and maps its outcome to a process exit code without
// exiting itself, so the exit boundary is testable. A status that is not an error
// — an installed plugin that ran and exited non-zero, or the drift import --sync
// --dry-run found — surfaces as an exitCodeError, whose code is propagated
// verbatim with no formatted diagnostic. Any other error is the single formatted
// diagnostic to stderr and exit 1; when it is cobra's unknown-command error, a
// plugin hint read from the local registry index is appended.
//
// A reported success is checked once more before it becomes exit 0, which is why
// this uses ExecuteContextC rather than ExecuteContext: only the C-suffixed form
// hands back the command the arguments actually resolved to, and that command is
// the only place the leftover positionals can still be read. See
// strayPositionalOnParentCommand for the case this catches and why the error
// alone cannot express it.
//
// ctx becomes every command's starting context, which the root's
// PersistentPreRunE then wraps rather than replaces, so a command body reading
// cmd.Context() reads a descendant of this one and observes its withdrawal.
func executeRoot(ctx context.Context, root *cobra.Command) int {
	cmd, err := root.ExecuteContextC(ctx)
	if err == nil {
		if strayPositionalOnParentCommand(cmd) {
			return 1
		}
		return 0
	}
	var ece exitCodeError
	if errors.As(err, &ece) {
		return ece.code
	}
	fmt.Fprintln(os.Stderr, unknownCommandSuggestion(ctx, formatCompilerError(err), err))
	return 1
}

// strayPositionalOnParentCommand reports whether cmd is a command that exists
// only to group its subcommands, yet was handed positional arguments it has no
// use for.
//
// Such a command does no work of its own: "aggrega cache" is there to route to
// "cache status", "cache prune" and the rest. Cobra answers an invocation of one
// by printing its help, and it signals that outcome exactly as it signals an
// explicit --help — a nil error. So a mistyped subcommand name arrives here
// indistinguishable from a help request that succeeded, and "aggrega cache prunee"
// prints help and reports success. The same typo written directly under the root
// is rejected, because the root alone gets cobra's unknown-subcommand check.
// Re-reading the resolved command's leftover positionals restores the missing
// half: the help still prints, byte for byte as before, but the invocation now
// reports failure.
//
// Each of the three conditions carries weight. A command with a run body consumes
// its own positional arguments and polices them with its own argument validator,
// so it is none of this check's business. A command with no subcommands has no
// subcommand name to mistype. And an invocation with no positional arguments left
// over is the ordinary, correct "aggrega cache", which must keep succeeding.
//
// The leftover positionals are readable here because cobra parses flags before it
// decides the command cannot run, so the resolved command's flag set already holds
// whatever the flags did not claim.
func strayPositionalOnParentCommand(cmd *cobra.Command) bool {
	if cmd == nil || cmd.Runnable() || !cmd.HasSubCommands() {
		return false
	}
	return len(cmd.Flags().Args()) > 0
}

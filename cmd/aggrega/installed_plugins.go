// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/cliplugin"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// cliPluginDispatcher runs an installed CLI plugin identified by its artifact
// URI, streaming the guest's output and returning its exit code and the content
// digest of the bytes it resolved and ran. command is the resolved subcommand
// name the operator invoked, handed to the guest so a single guest backing
// several commands can route on which command was called. It is the seam a
// plugin command's RunE calls; the production value is a cliplugin.Runner, and a
// test substitutes a fake that records the command and argument vector and
// returns a chosen exit code and digest without a real guest.
type cliPluginDispatcher interface {
	Run(ctx context.Context, uri, command string, argv []string) (int, string, error)
}

// installedPluginRunnerFor builds the dispatcher that runs an installed CLI
// plugin command for a resolved workspace. The production value resolves the
// plugin URI through the same workspace-derived cascade the install path uses —
// the sidecar directory, the catalog entity view, and the shared compilation
// cache — so any URI an install accepts is resolvable at run time. It is a
// package-level seam so a test can substitute a fake dispatcher that records the
// argument vector without a real guest or workspace.
var installedPluginRunnerFor = func(rw *workspaceconfig.ResolvedWorkspace) cliPluginDispatcher {
	return cliplugin.Runner{Resolve: workspacePluginResolver(rw), Digest: describeCacheDigest}
}

// installedPluginIndexSource yields the installed-CLI-plugin index used to
// register plugin commands. Production reads the on-disk index; a test
// substitutes a fixture without touching the real cache root.
var installedPluginIndexSource = readInstalledPluginIndex

// pluginRegistrationWarnWriter receives the diagnostics emitted when an installed
// plugin cannot be registered as a command. It defaults to standard error and is
// redirected by tests to observe the diagnostics.
var pluginRegistrationWarnWriter io.Writer = os.Stderr

// pluginDiscoverable reports whether an unrecognized command name corresponds to
// an installable plugin, so the unknown-command path can suggest installing it.
// The default is a non-network stub that reports nothing discoverable; the
// registry-backed lookup is wired separately.
var pluginDiscoverable = func(name string) bool { return false }

// reservedCommandNames are command names the CLI reserves for anticipated
// built-ins, so an installed plugin can never squat a name the CLI intends to
// claim later. The names present in a freshly built tree are the authoritative
// gate and are checked separately against root.Commands() — built-ins always
// win — so this set deliberately lists reserved-but-not-yet-built-in names
// rather than re-enumerating the live built-ins (which would be a second copy
// that could drift from the tree).
var reservedCommandNames = map[string]bool{
	"build":    true,
	"deploy":   true,
	"render":   true,
	"apply":    true,
	"validate": true,
	"plugin":   true,
	"cache":    true,
}

// registerInstalledCLIPlugins adds one top-level command per installed CLI plugin
// from the on-disk index to root, so a freshly built tree exposes the installed
// plugins as commands. It runs after the static commands are added, so the live
// tree is the authoritative built-in set. A name that collides with a built-in
// already in the tree, or that is on the reserved denylist, is skipped with a
// diagnostic — built-ins always win, and reserved names stay reserved. Each
// registered command's help comes from the index's cached describe metadata; no
// guest is loaded to list or help. A malformed index is reported and treated as
// no installed plugins rather than failing the tree.
func registerInstalledCLIPlugins(root *cobra.Command) {
	idx, err := installedPluginIndexSource()
	if err != nil {
		fmt.Fprintf(pluginRegistrationWarnWriter, "warning: ignoring installed CLI plugins: %v\n", err)
		return
	}
	if len(idx.Plugins) == 0 {
		return
	}

	builtin := map[string]bool{}
	for _, c := range root.Commands() {
		builtin[c.Name()] = true
	}

	for _, name := range idx.names() {
		switch {
		case builtin[name]:
			fmt.Fprintf(pluginRegistrationWarnWriter,
				"warning: installed CLI plugin %q is not registered: a built-in command with that name already exists\n", name)
			continue
		case reservedCommandNames[name]:
			fmt.Fprintf(pluginRegistrationWarnWriter,
				"warning: installed CLI plugin %q is not registered: %q is a reserved command name\n", name, name)
			continue
		}
		root.AddCommand(newInstalledPluginCmd(name, idx.Plugins[name]))
	}
}

// newInstalledPluginCmd builds the cobra command that dispatches an installed CLI
// plugin. Its help comes entirely from the index's cached describe metadata, so
// building the tree never loads a guest. Flag parsing is disabled so every
// argument after the command name is forwarded verbatim to the guest, which
// interprets its own flags. Running the command resolves and executes the plugin
// through the shared runner, threading the command's own name to the guest as
// the invoked-command discriminator so a single guest backing several commands
// routes on it; a non-zero exit is returned as an exitCodeError so the process
// mirrors the plugin's status without treating it as a host error.
func newInstalledPluginCmd(name string, entry installedPluginEntry) *cobra.Command {
	return &cobra.Command{
		Use:                name,
		Short:              entry.Usage,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			code, digest, err := installedPluginRunnerFor(rw).Run(cmd.Context(), entry.URI, name, args)
			if err != nil {
				return err
			}
			reconcileInstalledPluginDigest(name, digest, cmd.ErrOrStderr())
			if code != 0 {
				return exitCodeError{code: code}
			}
			return nil
		},
	}
}

// reconcileInstalledPluginDigest pins or drift-checks the digest of the bytes a
// command just resolved and ran (trust on first use). The first run of a command
// records the digest into its index entry; a later run compares the freshly
// resolved digest against the pinned one and, on a mismatch, writes an advisory
// warning to warn — the resolved bytes changed since they were pinned — without
// halting, since the command has already run and this is drift detection, not a
// guardrail. An empty digest (no bytes resolved) or an unreadable index is a
// silent no-op: a corrupt index is surfaced loudly at command registration, and
// reconciliation must never turn a successful run into a failure. The read,
// entry update, and write-back reuse the shared index helpers, so the pinned
// digest and the describe-cache key are one value.
func reconcileInstalledPluginDigest(cmdName, digest string, warn io.Writer) {
	if digest == "" {
		return
	}
	idx, err := readInstalledPluginIndex()
	if err != nil {
		return
	}
	entry, ok := idx.Plugins[cmdName]
	if !ok {
		return
	}
	if entry.Digest == "" {
		entry.Digest = digest
		idx.Plugins[cmdName] = entry
		_ = writeInstalledPluginIndex(idx)
		return
	}
	if entry.Digest != digest {
		fmt.Fprintf(warn,
			"warning: the %q plugin's resolved bytes changed since they were pinned "+
				"(pinned %s, now %s); the installed command may have been modified\n",
			cmdName, shortDigest(entry.Digest), shortDigest(digest))
	}
}

// shortDigest renders a content digest in an abbreviated form for a diagnostic,
// keeping the leading hex that distinguishes it while sparing the full 64-char
// string. A digest shorter than the abbreviation is returned unchanged.
func shortDigest(d string) string {
	const keep = 12
	if len(d) <= keep {
		return d
	}
	return d[:keep] + "..."
}

// exitCodeError carries an installed plugin's non-zero exit status up to the
// single process-exit boundary. It lets a plugin command's RunE report an
// arbitrary exit code without calling os.Exit itself, so Execute stays the one
// place the process exits and the plugin's status still reaches the shell
// verbatim.
type exitCodeError struct {
	code int
}

func (e exitCodeError) Error() string {
	return fmt.Sprintf("plugin command exited with status %d", e.code)
}

// installSuggestion is the single home for the wording that points a user at
// installing a plugin for a name the CLI does not recognize as a command, so the
// unknown-command hint and the plugin-install command stay in lockstep.
func installSuggestion(name string) string {
	return fmt.Sprintf("%q is not a command — install the %q plugin with 'aggrega plugin install %s'", name, name, name)
}

// unknownCommandSuggestion returns formatted with an install hint appended when
// err is cobra's unknown-command error for a name that is discoverable as an
// installable plugin. For any other error, or an unknown command that is not
// discoverable, it returns formatted unchanged so cobra's normal message stands.
func unknownCommandSuggestion(formatted string, err error) string {
	name, ok := unknownCommandName(err)
	if !ok || !pluginDiscoverable(name) {
		return formatted
	}
	return formatted + "\n" + installSuggestion(name)
}

// unknownCommandName extracts the offending command name from cobra's
// unknown-command error and reports whether err is that error. Cobra formats the
// error as: unknown command "NAME" for "aggrega" (optionally followed by
// suggestions). The name is the first double-quoted token, matched precisely so
// only the unknown-command case is enriched.
func unknownCommandName(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	const prefix = `unknown command "`
	msg := err.Error()
	if !strings.HasPrefix(msg, prefix) {
		return "", false
	}
	rest := msg[len(prefix):]
	end := strings.IndexByte(rest, '"')
	if end <= 0 {
		return "", false
	}
	return rest[:end], true
}

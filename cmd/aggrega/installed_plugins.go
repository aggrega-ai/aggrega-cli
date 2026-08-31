// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/cliplugin"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// cliPluginDispatcher runs a configured CLI plugin identified by its artifact
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

// installedPluginRunnerFor builds the dispatcher that runs a configured CLI
// plugin command for a resolved workspace. The production value resolves the
// plugin URI through the same workspace-derived cascade the install path uses —
// the catalog entity view and the shared compilation cache — so any URI an
// install accepts is resolvable at run time. It is a
// package-level seam so a test can substitute a fake dispatcher that records the
// argument vector without a real guest or workspace.
var installedPluginRunnerFor = func(rw *workspaceconfig.ResolvedWorkspace) cliPluginDispatcher {
	return cliplugin.Runner{Resolve: workspacePluginResolver(rw), Digest: digest.ContentDigest}
}

// resolveCLIPluginDigest resolves the bytes uri names through the workspace
// cascade and returns their content digest, so the run gate can compare them to
// the pinned digest before executing the guest. It is a package-level seam so a
// test can force a drift outcome without a real guest.
var resolveCLIPluginDigest = func(rw *workspaceconfig.ResolvedWorkspace, uri string) (string, error) {
	resolved, err := workspacePluginResolver(rw)(uri)
	if err != nil {
		return "", err
	}
	return digest.ContentDigest(resolved.Bytes), nil
}

// cliPluginRegistrations yields the CLI-extension commands to register for this
// invocation, derived from the resolved config cascade and the describe cache.
//
// It defaults to contributing nothing, and the process entry point wires the
// live implementation — the same shape the unknown-command install hint uses.
// The default matters because command-tree construction happens before any
// command runs, so the live implementation has no resolved workspace to be
// handed and resolves one from the working directory. Defaulting to the live
// implementation would make every caller of the tree constructor read whatever
// configuration sits above its working directory: a test would take the
// developer's own configuration as input, and the generated command reference
// would vary with whichever CLI plugins the machine building it happens to have
// configured. A test that wants registrations substitutes a fixture, so tree
// construction touches neither disk nor a guest.
var cliPluginRegistrations = func() []cliCommandReg { return nil }

// pluginRegistrationWarnWriter receives the diagnostics emitted when a configured
// plugin cannot be registered as a command. It defaults to standard error and is
// redirected by tests to observe the diagnostics.
var pluginRegistrationWarnWriter io.Writer = os.Stderr

// pluginDiscoverable reports whether an unrecognized command name corresponds to
// an installable plugin, so the unknown-command path can suggest installing it.
// The default is a non-network stub that reports nothing discoverable; the
// registry-backed lookup is wired separately.
var pluginDiscoverable = func(name string) bool { return false }

// reservedCommandNames are command names the CLI reserves for anticipated
// built-ins, so a configured CLI plugin can never squat a name the CLI intends
// to claim later. The names present in a freshly built tree are the
// authoritative gate and are checked separately against root.Commands() —
// built-ins always win — so this set deliberately lists reserved-but-not-yet-
// built-in names rather than re-enumerating the live built-ins (which would be a
// second copy that could drift from the tree).
var reservedCommandNames = map[string]bool{
	"build":    true,
	"deploy":   true,
	"render":   true,
	"apply":    true,
	"validate": true,
	"plugin":   true,
	"cache":    true,
}

// cliCommandReg is one top-level command a configured CLI plugin contributes:
// the command verb and its short-help line (from the plugin's describe
// metadata), the flags the command declares (also from describe, in declared
// order), the plugin artifact URI the runner resolves and runs, and the digest
// the config pinned that URI to. Registration builds one per command; the flags
// are declared on the cobra command so help renders them like a core command's,
// while the guest remains the parser; the run gate compares the freshly resolved
// bytes' digest to that pin before running.
type cliCommandReg struct {
	name   string
	short  string
	flags  []wasm.PluginCommandFlag
	uri    string
	digest string
}

// productionCLIPluginRegistrations resolves the workspace and projects its
// plugins.cli list into the commands to register. A resolution failure is
// reported as a diagnostic and treated as no configured CLI plugins rather than
// failing the tree, mirroring how a malformed config surfaces elsewhere.
func productionCLIPluginRegistrations() []cliCommandReg {
	rw, err := workspaceconfig.Resolve("", "")
	if err != nil {
		fmt.Fprintf(pluginRegistrationWarnWriter, "warning: ignoring configured CLI plugins: %v\n", err)
		return nil
	}
	return cliRegistrationsFromWorkspace(context.Background(), rw)
}

// cliRegistrationsFromWorkspace projects a resolved workspace's plugins.cli list
// into the commands to register, in the list's author order (the deterministic
// registration order). Each entry's command names and help come from the
// describe cache keyed by the pinned digest; a cold cache falls back to resolving
// and describing the guest and warms the cache, so the cache is an optimization
// and never a requirement. An entry whose bytes cannot be resolved or described
// on a cold cache is skipped with a diagnostic rather than failing the tree.
func cliRegistrationsFromWorkspace(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace) []cliCommandReg {
	resolve := workspacePluginResolver(rw)
	var regs []cliCommandReg
	for _, ref := range rw.Config().Plugins.CLI {
		desc, ok := readDescribeCache(ref.Digest)
		if !ok {
			d, derr := describeConfiguredCLI(ctx, ref, resolve)
			if derr != nil {
				fmt.Fprintf(pluginRegistrationWarnWriter,
					"warning: CLI plugin %q was not added as a command: %v\n", ref.URI, derr)
				continue
			}
			desc = d
		}
		for _, command := range desc.Commands {
			regs = append(regs, cliCommandReg{name: command.Name, short: command.Description, flags: command.Flags, uri: ref.URI, digest: ref.Digest})
		}
	}
	return regs
}

// describeConfiguredCLI is the cold-cache fallback for cliRegistrationsFromWorkspace:
// it resolves ref's bytes and describes the guest under the describe-only inert
// profile, caching the descriptor under the resolved bytes' content digest so a
// later run reads it without loading the guest again.
func describeConfiguredCLI(ctx context.Context, ref workspaceconfig.CLIPluginRef, resolve pluginpath.Lookup) (wasm.PluginDescriptor, error) {
	resolved, err := resolve(ref.URI)
	if err != nil {
		return wasm.PluginDescriptor{}, err
	}
	spec := resolver.PluginSpec(pluginDiagName(ref.URI), resolved)
	if spec.MaxMemoryPages == 0 {
		spec.MaxMemoryPages = wasm.DescribeMaxMemoryPages
	}
	loader := wasm.NewCachingLoaderFromContext(ctx, nil)
	plugin, desc, err := loader.LoadForDescribe(ctx, spec)
	if err != nil {
		return wasm.PluginDescriptor{}, err
	}
	_ = plugin.Close(ctx)
	writeDescribeCache(digest.ContentDigest(resolved.Bytes), desc)
	return desc, nil
}

// registerInstalledCLIPlugins adds one top-level command per configured CLI
// plugin command to root, so a freshly built tree exposes the configured plugins
// as commands. It runs after the static commands are added, so the live tree is
// the authoritative built-in set. A name that collides with a built-in already
// in the tree, that is on the reserved denylist, or that another CLI plugin
// already contributed this invocation is skipped with a diagnostic — built-ins
// always win, reserved names stay reserved, and one command name registers once.
// Each registered command's help comes from the describe cache; no guest is
// loaded to list or help while that cache is warm.
func registerInstalledCLIPlugins(root *cobra.Command) {
	regs := cliPluginRegistrations()
	if len(regs) == 0 {
		return
	}

	builtin := map[string]bool{}
	for _, c := range root.Commands() {
		builtin[c.Name()] = true
	}

	added := map[string]bool{}
	for _, reg := range regs {
		switch {
		case builtin[reg.name]:
			fmt.Fprintf(pluginRegistrationWarnWriter,
				"warning: CLI plugin %q was not added as a command: a built-in command with that name already exists\n", reg.name)
			continue
		case reservedCommandNames[reg.name]:
			fmt.Fprintf(pluginRegistrationWarnWriter,
				"warning: CLI plugin %q was not added as a command: %q is a reserved command name\n", reg.name, reg.name)
			continue
		case added[reg.name]:
			fmt.Fprintf(pluginRegistrationWarnWriter,
				"warning: CLI plugin %q was not added as a command: the command %q is already contributed by another CLI plugin\n", reg.name, reg.name)
			continue
		}
		added[reg.name] = true
		root.AddCommand(newCLIPluginCmd(reg))
	}
}

// newCLIPluginCmd builds the cobra command that dispatches a configured CLI
// plugin. Its help comes from the describe cache, so building the tree never
// loads a guest while that cache is warm. The command's declared flags are
// registered on it so that 'aggrega help <command>' renders them in a Flags
// section like a core command's flags; this is a help-rendering surface only.
// Flag parsing stays disabled so the whole argument vector after the command
// name — flag tokens included — is forwarded verbatim to the guest, which is the
// sole parser of its own flags. Before running, the command re-resolves the
// plugin's bytes and compares their content digest to the digest pinned in the
// config: a mismatch halts the command loudly with a re-pin instruction, so bytes
// that drifted from the pin never execute. On a match it resolves and executes
// the plugin through the shared runner, threading the command's own name to the
// guest as the invoked-command discriminator; a non-zero exit is returned as an
// exitCodeError so the process mirrors the plugin's status without treating it as
// a host error.
func newCLIPluginCmd(reg cliCommandReg) *cobra.Command {
	cmd := &cobra.Command{
		Use:                reg.name,
		Short:              reg.short,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			current, err := resolveCLIPluginDigest(rw, reg.uri)
			if err != nil {
				return err
			}
			if current != reg.digest {
				return fmt.Errorf(
					"the %q plugin's resolved bytes no longer match the pinned digest "+
						"(pinned %s, resolved %s); re-pin it with 'aggrega plugin add cli %s' before running it",
					reg.name, shortDigest(reg.digest), shortDigest(current), reg.name)
			}
			code, _, err := installedPluginRunnerFor(rw).Run(cmd.Context(), reg.uri, reg.name, args)
			if err != nil {
				return err
			}
			if code != 0 {
				return exitCodeError{code: code}
			}
			return nil
		},
	}
	declarePluginCommandFlags(cmd, reg.flags)
	return cmd
}

// declarePluginCommandFlags registers a configured CLI plugin command's declared
// flags on cmd so help renders them like a core command's flags. The declaration
// is a help-rendering surface only: because the command keeps flag parsing
// disabled, cobra never parses these flags and the guest remains the sole parser
// of the forwarded argument vector. A flag whose type is bool declares a bool
// flag (its default parsed from the declared default string, defaulting to
// false); every other type declares a string flag carrying the declared default
// verbatim, so the flag kind and default shown in help follow the guest's own
// declaration while the host performs no flag interpretation.
func declarePluginCommandFlags(cmd *cobra.Command, flags []wasm.PluginCommandFlag) {
	for _, f := range flags {
		if f.Type == "bool" {
			def, _ := strconv.ParseBool(f.Default)
			cmd.Flags().BoolP(f.Name, f.Shorthand, def, f.Description)
			continue
		}
		cmd.Flags().StringP(f.Name, f.Shorthand, f.Default, f.Description)
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

// exitCodeError carries a configured CLI plugin's non-zero exit status up to the
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
	return fmt.Sprintf("%q is not a command — install the %q plugin with 'aggrega plugin add cli %s'", name, name, name)
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

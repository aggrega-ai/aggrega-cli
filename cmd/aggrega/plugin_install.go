// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// resolvedInstallable is a verified, resolved plugin ready to describe and
// register as a command: its canonical artifact URI and the .wasm bytes the
// describe gate loads.
type resolvedInstallable struct {
	URI   string
	Bytes []byte
}

// installPluginFetch resolves a plugin reference to its verified .wasm bytes.
// Production resolves the reference through the shared plugin cascade and
// verifies any registry-originated signature through the same pull-time verify
// primitive the closure walk uses; a test substitutes a fetcher that returns
// local guest bytes so install runs without a registry.
var installPluginFetch = productionInstallPluginFetch

// describeInstallable loads resolved plugin bytes under the runtime CLI profile
// and returns its validated describe() descriptor. It is the real describe gate
// (LoadCLI + DescribeAndValidate, the same sequence the runtime runner uses); a
// test substitutes a crafted descriptor to exercise the install branches without
// building a guest.
var describeInstallable = func(ctx context.Context, src resolvedInstallable) (wasm.PluginDescriptor, error) {
	spec := resolver.PluginSpec(pluginDiagName(src.URI), pluginpath.ResolvedPlugin{Bytes: src.Bytes})
	if spec.MaxMemoryPages == 0 {
		spec.MaxMemoryPages = describeMaxMemoryPages
	}
	loader := wasm.NewCachingLoaderFromContext(ctx, nil)
	plugin, err := loader.LoadCLI(ctx, spec, io.Discard, io.Discard)
	if err != nil {
		return wasm.PluginDescriptor{}, err
	}
	desc, err := loader.DescribeAndValidate(ctx, plugin, spec)
	if err != nil {
		return wasm.PluginDescriptor{}, err
	}
	_ = plugin.Close(ctx)
	return desc, nil
}

// newPluginInstallCmd builds the `plugin install` command: it resolves and
// verifies a CLI plugin reference, confirms the plugin's capabilities with the
// operator, and registers the plugin as a top-level aggrega command.
func newPluginInstallCmd() *cobra.Command {
	var acceptCaps, assumeYes bool
	c := &cobra.Command{
		Use:   "install <plugin-ref>",
		Short: "Install a CLI plugin as a top-level aggrega command",
		Long: `Resolve, verify, and install a CLI plugin so it runs as a top-level
aggrega <command>.

The reference is resolved through the plugin cascade and, when it names a
registry artifact, verified against the registry's trust policy — the same
signature check the dependency resolver applies. The resolved plugin must be a
CLI plugin and must name at least one command it contributes; a single plugin
may contribute several commands, each installed as its own top-level command.

A plugin any of whose command names shadows a built-in command or a reserved
name is refused. Before installing, the command prints the plugin's declared
capabilities and asks for confirmation; pass --accept-capabilities or --yes to
skip the prompt for non-interactive use. Without a skip flag on a
non-interactive stdin it refuses rather than blocking on a prompt no one can
answer.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return pluginInstallRun(cmd.Context(), rw, args[0], acceptCaps, assumeYes, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	c.Flags().BoolVar(&acceptCaps, "accept-capabilities", false, "Accept the plugin's declared capabilities without the interactive prompt")
	c.Flags().BoolVar(&assumeYes, "yes", false, "Skip the capability-confirmation prompt (for non-interactive/CI use)")
	return c
}

// pluginInstallRun resolves and verifies ref, requires it to be a CLI plugin
// that names at least one command, refuses when any command name collides with a
// built-in or reserved name, confirms the plugin's capabilities with the operator
// once, and records one installed-plugin index entry per command plus the cached
// describe metadata. The collision guard runs across every command before the
// prompt so a plugin any of whose commands can never install is rejected without
// asking, and the index is written only after confirmation.
func pluginInstallRun(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, ref string, acceptCaps, assumeYes bool, in io.Reader, out io.Writer) error {
	src, err := installPluginFetch(ctx, rw, ref)
	if err != nil {
		return fmt.Errorf("plugin install: %w", err)
	}

	desc, err := describeInstallable(ctx, src)
	if err != nil {
		return fmt.Errorf("plugin install: describe %q: %w", ref, err)
	}
	if desc.PluginType != wasm.PluginTypeCLI {
		return fmt.Errorf("plugin install: %q is not a CLI plugin (declares type %q); only CLI plugins install as commands", ref, desc.PluginType)
	}
	if len(desc.Commands) == 0 {
		return fmt.Errorf("plugin install: %q declares no commands; a CLI plugin must name at least one command it contributes", ref)
	}

	// The collision guard runs per command before the prompt so a plugin any of
	// whose commands can never install is refused without asking. builtin names
	// are read once — they do not change across the commands of one install.
	builtins := builtinCommandNames()
	for _, command := range desc.Commands {
		if builtins[command.Name] {
			return fmt.Errorf("plugin install: %q contributes the command %q, which is a built-in command; refusing to install", ref, command.Name)
		}
		if reservedCommandNames[command.Name] {
			return fmt.Errorf("plugin install: %q contributes the command %q, which is a reserved command name; refusing to install", ref, command.Name)
		}
	}

	// Capabilities are a plugin-level property, so they are confirmed once per
	// install regardless of how many commands the plugin contributes.
	if err := confirmInstallCapabilities(out, in, installCommandNames(desc), beyondBaselineCapabilities(desc), acceptCaps, assumeYes); err != nil {
		return err
	}

	if err := recordInstalledPlugin(src, desc); err != nil {
		return fmt.Errorf("plugin install: %w", err)
	}
	fmt.Fprintf(out, "Installed %q as command(s) %s.\n", ref, quoteCommandNames(desc))
	return nil
}

// installCommandNames joins a CLI plugin's declared command names for the
// capability-confirmation prompt, which addresses the plugin as a whole.
func installCommandNames(desc wasm.PluginDescriptor) string {
	names := make([]string, len(desc.Commands))
	for i, command := range desc.Commands {
		names[i] = command.Name
	}
	return strings.Join(names, ", ")
}

// quoteCommandNames renders a CLI plugin's declared command names as a
// comma-separated list of quoted verbs for the install confirmation line.
func quoteCommandNames(desc wasm.PluginDescriptor) string {
	quoted := make([]string, len(desc.Commands))
	for i, command := range desc.Commands {
		quoted[i] = fmt.Sprintf("%q", command.Name)
	}
	return strings.Join(quoted, ", ")
}

// beyondBaselineCapabilities returns the plugin's declared capabilities beyond
// the CLI baseline — its argument vector in, streamed stdout/stderr, and an exit
// code out — which need no confirmation. The CLI socket models no
// beyond-baseline capability class yet, so this reports none; it grows as the
// socket wires new capability classes, and the confirmation mechanism is already
// in place to surface them.
func beyondBaselineCapabilities(desc wasm.PluginDescriptor) []string {
	_ = desc
	return nil
}

// confirmInstallCapabilities prints the plugin's declared capabilities and gates
// the install on the operator's confirmation. With acceptCaps or assumeYes it
// consents immediately. On a non-interactive stdin without a skip flag it
// refuses — returning an error rather than blocking on a prompt no one can
// answer — mirroring the cache-clear confirmation discipline. Otherwise it
// prompts [y/N] and consents only on an explicit yes.
func confirmInstallCapabilities(w io.Writer, r io.Reader, commandLabel string, caps []string, acceptCaps, assumeYes bool) error {
	if len(caps) > 0 {
		fmt.Fprintf(w, "The %q plugin requests these capabilities beyond the baseline:\n", commandLabel)
		for _, capName := range caps {
			fmt.Fprintf(w, "  - %s\n", capName)
		}
	} else {
		fmt.Fprintf(w, "The %q plugin requests no additional capabilities beyond the baseline (arguments in; streamed stdout/stderr; exit code).\n", commandLabel)
	}
	if acceptCaps || assumeYes {
		return nil
	}
	if !isTerminal() {
		return errors.New("plugin install: refusing to install without capability confirmation on a non-interactive stdin; pass --accept-capabilities or --yes")
	}
	fmt.Fprint(w, "Install this plugin? [y/N] ")
	line, _ := bufio.NewReader(r).ReadString('\n')
	switch strings.TrimSpace(line) {
	case "y", "Y", "yes", "Yes", "YES":
		return nil
	default:
		return errors.New("plugin install: aborted")
	}
}

// builtinCommandNames returns the static built-in command names a newly-built
// tree exposes, so an installed plugin can never shadow one. It reads the live
// command tree — the same authoritative built-in surface the registration guard
// uses — and removes the dynamically-registered installed-plugin commands, since
// an already-installed plugin's own name is not a built-in a new install must
// avoid.
func builtinCommandNames() map[string]bool {
	names := map[string]bool{}
	for _, c := range newRootCmd().Commands() {
		names[c.Name()] = true
	}
	if idx, err := installedPluginIndexSource(); err == nil {
		for name := range idx.Plugins {
			delete(names, name)
		}
	}
	return names
}

// recordInstalledPlugin writes one installed-plugin index entry per command the
// plugin contributes and caches the plugin's describe metadata keyed by the
// content digest, so a freshly built tree registers each command and renders its
// help without loading the guest. Every command's entry shares the plugin's
// resolved URI and describe cache; each carries its own command's short help as
// the entry usage. The index is read from disk, the entries are set, and the
// whole index is written back, so an install adds to — rather than replaces —
// the installed set. The describe cache is written once, keyed by the guest's
// content digest.
func recordInstalledPlugin(src resolvedInstallable, desc wasm.PluginDescriptor) error {
	idx, err := readInstalledPluginIndex()
	if err != nil {
		return err
	}
	if idx.Plugins == nil {
		idx.Plugins = map[string]installedPluginEntry{}
	}
	caps := beyondBaselineCapabilities(desc)
	for _, command := range desc.Commands {
		idx.Plugins[command.Name] = installedPluginEntry{
			URI:          src.URI,
			Usage:        command.Description,
			Capabilities: caps,
		}
	}
	if err := writeInstalledPluginIndex(idx); err != nil {
		return err
	}
	writeDescribeCache(describeCacheDigest(src.Bytes), desc)
	return nil
}

// productionInstallPluginFetch resolves ref to its .wasm bytes through the shared
// plugin cascade and verifies any registry-originated signature through the same
// pull-time primitive the closure walk uses. A local, sidecar, or catalog
// reference resolves with no network and carries no registry signature to verify;
// a registry reference resolves through the cascade's registry tier and is
// verified against the registry's trust policy.
func productionInstallPluginFetch(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, ref string) (resolvedInstallable, error) {
	var wsCfg workspaceconfig.Config
	if rw != nil {
		wsCfg = rw.Config()
	}
	resolved, err := workspacePluginResolver(rw)(ref)
	if err != nil {
		return resolvedInstallable{}, err
	}
	if err := verifyInstallSignature(ctx, ref, wsCfg); err != nil {
		return resolvedInstallable{}, err
	}
	return resolvedInstallable{URI: ref, Bytes: resolved.Bytes}, nil
}

// verifyInstallSignature verifies the signature of a registry-resolved plugin
// artifact through the same pull-time primitive the closure walk uses
// (pullverify.Verify, targeting via remoteTargetFor). A local, sidecar, or
// otherwise host-less reference carries no registry signature, so — exactly as
// the closure verify pass skips non-registry items — verification is a no-op for
// it. Signature verification against a live registry is operator-gated: the
// sandbox exercises the local no-op path; the registry path runs against a real
// hub with credentials.
func verifyInstallSignature(ctx context.Context, ref string, wsCfg workspaceconfig.Config) error {
	if wsCfg.Offline || wsCfg.SkipSignatureVerification {
		return nil
	}
	bare := strings.TrimPrefix(ref, "oci://")
	host := registryhost.Of(bare)
	if !isRegistryHost(host) {
		// Local / sidecar / catalog reference: no registry signature to verify,
		// mirroring the closure verify pass's skip of non-registry items.
		return nil
	}
	repoRef := bare
	if at := strings.LastIndex(repoRef, "@"); at >= 0 {
		repoRef = repoRef[:at]
	}
	repo, err := oci.NewRemoteRepository(repoRef)
	if err != nil {
		return err
	}
	desc, err := repo.Resolve(ctx, bare)
	if err != nil {
		return fmt.Errorf("resolve %q for signature verification: %w", ref, err)
	}
	_, err = pullverify.Verify(ctx, []pullverify.Entity{{
		URI:            ref,
		ManifestDigest: desc.Digest.String(),
		Host:           host,
	}}, pullverify.Options{
		Config: wsCfg,
		Target: remoteTargetFor,
		Warn:   func(notice string) { fmt.Fprintf(os.Stderr, "  verify: %s\n", notice) },
	})
	return err
}

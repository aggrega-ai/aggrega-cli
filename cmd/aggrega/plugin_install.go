// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginfetchverify"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/ociref"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
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

// describeInstallable loads resolved plugin bytes under the describe-only profile
// and returns their validated describe() descriptor. The load reads the guest's
// self-report to warm the describe cache without granting the guest any real host
// capability — its imports are satisfied by inert stubs — and admits any plugin
// type through the union import allowlist. It never runs a command; command
// execution happens at runtime in the CLI runner, not here. A test substitutes a
// crafted descriptor to exercise the install branches without building a guest.
var describeInstallable = func(ctx context.Context, src resolvedInstallable) (wasm.PluginDescriptor, error) {
	spec := resolver.PluginSpec(pluginDiagName(src.URI), pluginpath.ResolvedPlugin{Bytes: src.Bytes})
	if spec.MaxMemoryPages == 0 {
		spec.MaxMemoryPages = wasm.DescribeMaxMemoryPages
	}
	loader := wasm.NewCachingLoaderFromContext(ctx, nil)
	plugin, desc, err := loader.LoadForDescribe(ctx, spec)
	if err != nil {
		return wasm.PluginDescriptor{}, err
	}
	_ = plugin.Close(ctx)
	return desc, nil
}

// pluginInstallRun resolves and verifies ref, requires it to be a CLI plugin
// that names at least one command, refuses when any command name collides with a
// built-in or reserved name, confirms the plugin's capabilities with the operator
// once, and pins the plugin into the plugins.cli config list plus warms the
// describe cache. The write destination follows the CLI operation's nature (a
// machine-wide tool defaults to the global config) with the --global / --workspace
// overrides, resolved off rw so the pin lands in the config file this run read;
// it is resolved up front so a write-target flag misuse fails before any fetch.
// The collision guard runs across every command before the prompt so a plugin any
// of whose commands can never install is rejected without asking, and the config
// is written only after confirmation.
func pluginInstallRun(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, ref string, global, workspace, acceptCaps, assumeYes bool, in io.Reader, out io.Writer) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return err
	}
	configPath, err := rw.WriteDestination(workspaceconfig.OpCLI, global, workspace)
	if err != nil {
		return err
	}

	src, err := installPluginFetch(ctx, rw, ref)
	if err != nil {
		return fmt.Errorf("aggrega plugin add: %w", err)
	}

	desc, err := describeInstallable(ctx, src)
	if err != nil {
		return fmt.Errorf("aggrega plugin add: describe %q: %w", ref, err)
	}
	// Installability precondition, deliberately distinct from the shared
	// load-gate type-match: `plugin add` installs only a CLI plugin as a
	// command, so it refuses any other reported type here. This is a
	// reported-vs-installable check, not the gate's declared-vs-reported
	// verdict — do not fold it into that gate.
	if desc.PluginType != string(wasm.PluginTypeCLI) {
		return fmt.Errorf("aggrega plugin add: %q is not a CLI plugin (declares type %q); only CLI plugins install as commands", ref, desc.PluginType)
	}
	if len(desc.Commands) == 0 {
		return fmt.Errorf("aggrega plugin add: %q declares no commands; a CLI plugin must name at least one command it contributes", ref)
	}

	// The collision guard runs per command before the prompt so a plugin any of
	// whose commands can never install is refused without asking. builtin names
	// are read once — they do not change across the commands of one install.
	builtins := builtinCommandNames()
	for _, command := range desc.Commands {
		if builtins[command.Name] {
			return fmt.Errorf("aggrega plugin add: %q contributes the command %q, which is a built-in command; refusing to install", ref, command.Name)
		}
		if reservedCommandNames[command.Name] {
			return fmt.Errorf("aggrega plugin add: %q contributes the command %q, which is a reserved command name; refusing to install", ref, command.Name)
		}
	}

	// Capabilities are a plugin-level property, so they are confirmed once per
	// install regardless of how many commands the plugin contributes.
	if err := confirmInstallCapabilities(out, in, installCommandNames(desc), beyondBaselineCapabilities(desc), acceptCaps, assumeYes); err != nil {
		return err
	}

	if err := recordInstalledCLIPlugin(configPath, src, desc); err != nil {
		return fmt.Errorf("aggrega plugin add: %w", err)
	}
	fmt.Fprintf(out, "Added %q as command(s) %s in %s.\n", ref, quoteCommandNames(desc), configPath)
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
	return confirmation{
		question:   "Install this plugin? [y/N] ",
		noTerminal: "aggrega plugin add: refusing to install without capability confirmation on a non-interactive stdin; pass --accept-capabilities or --yes",
		declined:   "aggrega plugin add: aborted",
	}.ask(w, r, acceptCaps || assumeYes)
}

// builtinCommandNames returns the static built-in command names a newly-built
// tree exposes, so a configured CLI plugin can never shadow one. It reads the
// live command tree — the same authoritative built-in surface the registration
// guard uses — and removes the dynamically-registered CLI-plugin commands, since
// an already-configured plugin's own name is not a built-in a new install must
// avoid.
func builtinCommandNames() map[string]bool {
	names := map[string]bool{}
	for _, c := range newRootCmd().Commands() {
		names[c.Name()] = true
	}
	for _, reg := range cliPluginRegistrations() {
		delete(names, reg.name)
	}
	return names
}

// recordInstalledCLIPlugin pins the resolved plugin into the plugins.cli list at
// configPath — one {uri, digest} entry — and warms the describe cache under that
// pin, so a freshly built tree registers each of the plugin's commands and
// renders their help without loading the guest. The pin is the canonical content
// digest of the signature-verified bytes computed at install, which closes the
// pull-to-first-run window: the running bytes are fixed the moment they are
// fetched, not trusted on first use. The write is idempotent — re-adding the same
// plugin re-pins the one entry rather than duplicating it. The describe cache is
// written once under that same content pin (keyed by its colon-free tag form),
// so a later run reads the descriptor by the digest the config records.
func recordInstalledCLIPlugin(configPath string, src resolvedInstallable, desc wasm.PluginDescriptor) error {
	pin := digest.ContentDigest(src.Bytes)
	if err := workspaceconfig.UpsertCLIPluginRef(configPath, src.URI, pin); err != nil {
		return err
	}
	writeDescribeCache(pin, desc)
	return nil
}

// productionInstallPluginFetch resolves ref to its verified .wasm bytes. A
// registry oci:// reference is fetched over the wire through the same OCI puller
// the closure walk uses, its signature verified against the registry's trust
// policy, pinned by content digest, and cached under the workspace artifact cache
// as two self-verifying blobs (content + manifest) so a later offline run
// resolves the pinned bytes from cache with no network. A local or embedded
// reference resolves through the shared plugin cascade with no network
// and no cache write, since it carries no registry signature and needs none —
// its bytes are already on disk or in the binary.
func productionInstallPluginFetch(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, ref string) (resolvedInstallable, error) {
	var (
		wsCfg workspaceconfig.Config
		root  string
	)
	if rw != nil {
		wsCfg = rw.Config()
		root = rw.Root()
	}

	bare := strings.TrimPrefix(ref, "oci://")
	if !pluginfetchverify.IsRegistryHost(registryhost.Of(bare)) {
		// Local / embedded reference: resolve through the shared cascade
		// with no network and no cache write, mirroring the closure verify pass's
		// skip of non-registry items.
		resolved, err := workspacePluginResolver(rw)(ref)
		if err != nil {
			return resolvedInstallable{}, err
		}
		return resolvedInstallable{URI: ref, Bytes: resolved.Bytes}, nil
	}

	// Registry reference: fetch the .wasm bytes, verify the signature, pin by
	// content digest, and cache both blobs under the same (URI, pin) slot the
	// runtime resolver's cache tier re-reads. The URI written here is ref verbatim,
	// so it is byte-identical to the plugins.cli entry the config records and the
	// runtime cache read addresses.
	repoRef, tag, err := splitPluginRefTag(ref)
	if err != nil {
		return resolvedInstallable{}, err
	}
	puller := catalog.NewDefaultOCIPuller(wsCfg)
	if puller == nil {
		return resolvedInstallable{}, fmt.Errorf("cannot fetch %q while the workspace is offline; installing a registry plugin requires network access", ref)
	}
	result, err := puller.Pull(ctx, repoRef, tag)
	if err != nil {
		return resolvedInstallable{}, fmt.Errorf("fetch %q: %w", ref, err)
	}
	// Route the fetched plugin through the single verification home — the same
	// signature verification every other plugin fetch runs. A CLI plugin is fetched
	// by its host-carrying oci:// reference without loading a catalog Plugin entity,
	// so no host-function stamp is supplied and that check is a no-op here (exactly
	// as it is for any plugin resolved without an entity body); the registry
	// signature is verified. result.Digest is the fetched manifest digest the
	// signature commits to.
	installVerifyClient, _ := workspaceEgressClient(wsCfg)
	if _, err := pluginfetchverify.Verify(ctx, pluginfetchverify.Fetched{
		URI:            ref,
		ManifestDigest: result.Digest,
		Host:           registryhost.Of(bare),
		Kind:           catalog.EntityKindForHint("plugin"),
	}, wsCfg, pluginfetchverify.RemoteTargetFor(installVerifyClient), os.Stderr); err != nil {
		return resolvedInstallable{}, err
	}
	pin := digest.ContentDigest(result.Bytes)
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	if err := oci.WriteCached(root, cacheDir, ref, pin, result.Bytes); err != nil {
		return resolvedInstallable{}, err
	}
	if len(result.ManifestBytes) > 0 {
		if err := oci.WriteCachedManifest(root, cacheDir, ref, pin, result.ManifestBytes); err != nil {
			return resolvedInstallable{}, err
		}
	}
	return resolvedInstallable{URI: ref, Bytes: result.Bytes}, nil
}

// splitPluginRefTag splits a registry plugin reference "oci://host/path:tag" into
// its bare "host/path" repository reference (the OCI puller's fetch coordinate)
// and its version "tag". The version tag is the final ":"-delimited segment; a
// registry host:port colon is earlier and is always followed by the "/path"
// segment, so the tag is the substring after the last colon precisely when that
// substring carries no "/". A reference with no such tag is refused loudly rather
// than pulled against a repository with no version to select.
func splitPluginRefTag(ref string) (repoRef, tag string, err error) {
	bare := strings.TrimPrefix(ref, "oci://")
	repoRef, tag, hasVersion := ociref.SplitVersionTag(bare)
	if !hasVersion {
		return "", "", fmt.Errorf("aggrega plugin add: reference %q carries no version tag to fetch", ref)
	}
	return repoRef, tag, nil
}

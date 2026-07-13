// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// describeMaxMemoryPages is the memory cap applied when describing a plugin that
// carries no explicit limit. The platform default cap is generous for a running
// render but too small to instantiate the largest built-in provider guests for a
// describe() call, so the list command raises the ceiling to the value the
// provider catalog entities themselves declare. It is a describe-time convenience
// only — the render path applies each plugin's own authored limits.
const describeMaxMemoryPages uint32 = 4096

// newPluginCmd builds the `plugin` parent command grouping the plugin-inspection
// subcommands.
func newPluginCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "plugin",
		Short: "Inspect the Wasm plugins the CLI can resolve",
	}
	c.AddCommand(newPluginListCmd())
	c.AddCommand(newPluginGenCatalogCmd())
	c.AddCommand(newPluginInstallCmd())
	c.AddCommand(newPluginSearchCmd())
	return c
}

// newPluginSearchCmd builds the `plugin search` command: it searches the
// configured registry for installable CLI plugins through the shared
// registry-search primitive, scoped to the plugin repositories.
func newPluginSearchCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "search [query]",
		Short: "Search the registry for installable CLI plugins",
		Long: `Search the configured registry for installable plugins.

The optional query is a case-insensitive substring match on the repository
path; an empty query lists every plugin repository. The scope is the plugin
repositories only — use 'aggrega catalog search' to search the whole catalog.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			query := ""
			if len(args) == 1 {
				query = args[0]
			}
			return runRegistrySearch(cmd.Context(), rw.Config(), query, pluginSearchScope, true, cmd.OutOrStdout())
		},
	}
	return c
}

func newPluginListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "List the embedded built-in and workspace-configured plugins with their describe() metadata",
		Long: `List every Wasm plugin the CLI can see and, for each, its self-reported
describe() metadata.

Three groups are printed. Embedded built-ins are the provider and linter guests
compiled into the binary; they resolve with no filesystem, cache, or network
access. Configured plugins are the workspace bindings — provider_bindings and
the linter socket — resolved through the plugin cascade. Installed CLI plugins
are the commands installed with 'aggrega plugin install', each carrying a
readiness label: ready, unresolved, describe-failed, or digest-drift when its
bytes changed since the first run pinned them.

For each plugin the command loads it, invokes its describe() export, and prints
its identity, plugin type, version and commit, targets, and key capabilities.
The output is read-only and deterministic. A plugin that fails to resolve or
describe is listed with its error rather than halting the whole listing.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return pluginListRun(cmd.Context(), rw, cmd.OutOrStdout())
		},
	}
	return c
}

// pluginListEntry is one plugin's enumeration result: its source group and URI,
// where a configured plugin was bound, and either its validated describe()
// descriptor or the error that describing it produced.
type pluginListEntry struct {
	source string
	uri    string
	origin string
	desc   wasm.PluginDescriptor
	err    error

	// resolvedDigest is the content digest of the bytes the URI resolved to,
	// captured once resolution succeeds and empty when it did not. It lets an
	// installed-plugin entry compare the freshly resolved bytes against the
	// digest pinned at first run.
	resolvedDigest string

	// readiness is the installed CLI plugin's one-word readiness label; empty for
	// the embedded and configured groups, which carry no readiness signal.
	readiness string
}

// pluginListRun enumerates the embedded built-ins and the workspace-configured
// plugins, describing each, and writes the deterministic listing to w. It never
// mutates state. A missing or minimal workspace config is not an error: the
// configured group is simply empty and the embedded built-ins still list.
func pluginListRun(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, w io.Writer) error {
	var wsCfg workspaceconfig.Config
	if rw != nil {
		wsCfg = rw.Config()
	}
	sidecarDir, pluginByURI := workspacePluginResolutionInputs(rw)

	embeddedSet := map[string]bool{}
	var embeddedEntries []pluginListEntry
	for _, uri := range embedded.URIs() {
		embeddedSet[uri] = true
		embeddedEntries = append(embeddedEntries, describePluginURI(ctx, "embedded", uri, "", sidecarDir, nil))
	}

	var configuredEntries []pluginListEntry
	for _, ref := range collectConfiguredRefs(wsCfg) {
		configuredEntries = append(configuredEntries, describePluginURI(ctx, "configured", ref.uri, ref.origin, sidecarDir, pluginByURI))
	}

	var installedEntries []pluginListEntry
	if idx, err := installedPluginIndexSource(); err == nil {
		for _, name := range idx.names() {
			installedEntries = append(installedEntries, describeInstalledPlugin(ctx, name, idx.Plugins[name], sidecarDir, pluginByURI))
		}
	}

	formatPluginList(w, embeddedEntries, configuredEntries, installedEntries, embeddedSet)
	return nil
}

// configuredRef is a workspace-configured plugin URI and the config location(s)
// that bound it.
type configuredRef struct {
	uri    string
	origin string
}

// collectConfiguredRefs gathers the workspace-configured plugin URIs — the
// provider bindings and the linter socket (base and append) — deduplicated by
// URI with their origins merged, sorted by URI. Every collection walk is over a
// sorted or index-ordered surface so the result is deterministic regardless of
// the config map's iteration order.
func collectConfiguredRefs(cfg workspaceconfig.Config) []configuredRef {
	origins := map[string][]string{}
	add := func(uri, origin string) {
		if uri == "" {
			return
		}
		origins[uri] = append(origins[uri], origin)
	}

	bindingKinds := make([]string, 0, len(cfg.ProviderBindings))
	for k := range cfg.ProviderBindings {
		bindingKinds = append(bindingKinds, k)
	}
	sort.Strings(bindingKinds)
	for _, k := range bindingKinds {
		add(cfg.ProviderBindings[k], "provider_bindings["+k+"]")
	}
	for i, r := range cfg.Plugins.Linter {
		add(r.URI, fmt.Sprintf("plugins.linter[%d]", i))
	}
	for i, r := range cfg.Plugins.LinterAppend {
		add(r.URI, fmt.Sprintf("plugins.linter_append[%d]", i))
	}

	refs := make([]configuredRef, 0, len(origins))
	for uri, o := range origins {
		sort.Strings(o)
		refs = append(refs, configuredRef{uri: uri, origin: strings.Join(o, ", ")})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].uri < refs[j].uri })
	return refs
}

// describePluginURI returns uri's describe() descriptor as a listing entry
// without instantiating the guest wherever a static descriptor is available.
//
// An embedded built-in reads the descriptor extracted from its guest at build
// time, so listing the built-ins never compiles a guest. A configured plugin is
// resolved to its bytes and served from the digest-keyed describe cache; only a
// cache miss loads and describes the guest, and the result is cached for later
// runs. Either path falls back to loading and describing the guest — loudly, not
// with stale data — when no static descriptor is present, for example a tree
// built before the descriptor assets were generated. Any resolve or describe
// failure is captured on the entry rather than propagated, so one unresolvable
// plugin does not halt the listing.
func describePluginURI(ctx context.Context, source, uri, origin, sidecarDir string, pluginByURI func(string) (types.Plugin, bool)) pluginListEntry {
	entry := pluginListEntry{source: source, uri: uri, origin: origin}

	if source == "embedded" {
		if raw, ok := embedded.LookupDescriptor(uri); ok {
			var desc wasm.PluginDescriptor
			if err := json.Unmarshal(raw, &desc); err == nil {
				entry.desc = desc
				return entry
			}
			// A malformed embedded descriptor falls through to load-and-describe
			// rather than reporting a corrupt static self-report.
		}
	}

	resolved, err := resolvePluginURI(uri, sidecarDir, pluginByURI)
	if err != nil {
		entry.err = err
		return entry
	}

	digest := describeCacheDigest(resolved.Bytes)
	entry.resolvedDigest = digest
	if desc, ok := readDescribeCache(digest); ok {
		entry.desc = desc
		return entry
	}

	spec := resolver.PluginSpec(pluginDiagName(uri), resolved)
	if spec.MaxMemoryPages == 0 {
		spec.MaxMemoryPages = describeMaxMemoryPages
	}
	loader := wasm.NewCachingLoaderFromContext(ctx, nil)
	plugin, desc, err := loader.LoadAndDescribe(ctx, spec)
	if err != nil {
		entry.err = err
		return entry
	}
	_ = plugin.Close(ctx)
	writeDescribeCache(digest, desc)
	entry.desc = desc
	return entry
}

// describeInstalledPlugin enumerates one installed CLI plugin for the listing:
// it resolves and describes the plugin's URI through the same cascade the run
// path uses, then derives a readiness label from whether the plugin resolves,
// instantiates, and matches the digest pinned at its first run. Reusing
// describePluginURI keeps the resolve-describe-digest path single-sourced; only
// the readiness label is layered on top.
func describeInstalledPlugin(ctx context.Context, cmdName string, plugin installedPluginEntry, sidecarDir string, pluginByURI func(string) (types.Plugin, bool)) pluginListEntry {
	entry := describePluginURI(ctx, "installed", plugin.URI, "", sidecarDir, pluginByURI)
	entry.origin = fmt.Sprintf("command %q", cmdName)
	entry.readiness = installedReadiness(plugin.Digest, entry.resolvedDigest, entry.err)
	return entry
}

// installedReadiness maps an installed CLI plugin's resolve-and-describe outcome
// and pinned digest to a one-word readiness label. A resolve failure (no fresh
// digest was computed) reads unresolved; a describe or instantiation failure
// reads describe-failed; a pinned digest that no longer matches the freshly
// resolved bytes reads digest-drift; anything else — including a plugin not yet
// pinned by a first run — reads ready.
func installedReadiness(pinned, fresh string, err error) string {
	switch {
	case err != nil && fresh == "":
		return "unresolved"
	case err != nil:
		return "describe-failed"
	case pinned != "" && pinned != fresh:
		return "digest-drift"
	default:
		return "ready"
	}
}

// resolvePluginURI resolves uri to its .wasm bytes through the plugin cascade.
// When a catalog Plugin accessor is supplied, an entity-fronted resolution is
// tried first so a Plugin entity URI carries the entity's authored capability
// claim and runtime limits. A URI that is not a Plugin entity — a direct
// artifact or an embedded built-in reference — falls back to the bare cascade,
// whose embed tier serves the built-ins.
func resolvePluginURI(uri, sidecarDir string, pluginByURI func(string) (types.Plugin, bool)) (pluginpath.ResolvedPlugin, error) {
	cascade := pluginpath.Resolver{SidecarDir: sidecarDir}
	if pluginByURI != nil {
		front := pluginpath.EntityLookup(func(entityURI string) (string, *pluginpath.PluginCapabilityClaim, *pluginpath.PluginRuntimeLimits, bool) {
			entity, ok := pluginByURI(entityURI)
			if !ok {
				return "", nil, nil, false
			}
			return entity.Spec.Plugin, resolver.PluginCapabilityClaim(entity), resolver.PluginRuntimeLimits(entity), true
		}, cascade.Resolve)
		resolved, err := front(uri)
		if err == nil {
			return resolved, nil
		}
		if !errors.Is(err, pluginpath.ErrPluginEntityNotFound) {
			return pluginpath.ResolvedPlugin{}, err
		}
	}
	return cascade.Resolve(uri)
}

// workspacePluginResolutionInputs derives the plugin-resolution inputs from a
// resolved workspace: the sidecar plugin directory and the catalog Plugin-by-URI
// entity view. A nil workspace yields empty inputs — the bare cascade that serves
// only embedded built-ins and direct local or absolute artifact paths. The
// install path, the listing, and the runtime plugin runner all build their
// resolver from these same inputs, so a URI one path accepts every other path
// resolves identically.
func workspacePluginResolutionInputs(rw *workspaceconfig.ResolvedWorkspace) (sidecarDir string, pluginByURI func(string) (types.Plugin, bool)) {
	if rw == nil {
		return "", nil
	}
	wsCfg := rw.Config()
	dirs := resolveCatalogDirs(nil, wsCfg, rw.Root())
	lazyView := catalog.NewLazyLookup(rw.Root(), wsCfg.ResolvedArtifactCacheDir(), dirs, nil)
	return wsCfg.PluginSidecarDir, lazyView.PluginByURI
}

// workspacePluginResolver binds a resolved workspace's plugin-resolution inputs
// into a single Lookup, so the runtime runner and the install path turn a plugin
// URI into bytes through the identical cascade. A URI that install accepts is
// therefore resolvable at run time; the two never diverge because they share this
// one construction.
func workspacePluginResolver(rw *workspaceconfig.ResolvedWorkspace) pluginpath.Lookup {
	sidecarDir, pluginByURI := workspacePluginResolutionInputs(rw)
	return func(uri string) (pluginpath.ResolvedPlugin, error) {
		return resolvePluginURI(uri, sidecarDir, pluginByURI)
	}
}

// pluginDiagName derives a short diagnostic name for a plugin URI — the final
// path segment — used only in load-error messages.
func pluginDiagName(uri string) string {
	s := uri
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return uri
	}
	return s
}

// formatPluginList writes the three-group listing to w. Each group's entries are
// already in a deterministic order; the group headers and empty-group placeholders
// keep the output readable when a group is empty.
func formatPluginList(w io.Writer, embeddedEntries, configuredEntries, installedEntries []pluginListEntry, embeddedSet map[string]bool) {
	fmt.Fprintln(w, "Embedded built-ins:")
	fmt.Fprintln(w)
	if len(embeddedEntries) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, e := range embeddedEntries {
		renderPluginEntry(w, e, embeddedSet)
	}

	fmt.Fprintln(w, "Configured plugins:")
	fmt.Fprintln(w)
	if len(configuredEntries) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, e := range configuredEntries {
		renderPluginEntry(w, e, embeddedSet)
	}

	fmt.Fprintln(w, "Installed CLI plugins:")
	fmt.Fprintln(w)
	if len(installedEntries) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, e := range installedEntries {
		renderPluginEntry(w, e, embeddedSet)
	}
}

// renderPluginEntry writes one plugin's block. A describe failure prints the
// error in place of the metadata. Optional fields (provenance, targets, input
// kinds, boundary assembly) are omitted when the descriptor carries none.
func renderPluginEntry(w io.Writer, e pluginListEntry, embeddedSet map[string]bool) {
	fmt.Fprintf(w, "  %s\n", e.uri)
	if e.origin != "" {
		fmt.Fprintf(w, "    origin:       %s\n", e.origin)
	}
	if e.readiness != "" {
		fmt.Fprintf(w, "    readiness:    %s\n", e.readiness)
	}
	if e.err != nil {
		fmt.Fprintf(w, "    error:        describe failed: %v\n", e.err)
		fmt.Fprintln(w)
		return
	}
	if e.source == "configured" && embeddedSet[e.uri] {
		fmt.Fprintln(w, "    note:         also an embedded built-in")
	}
	fmt.Fprintf(w, "    type:         %s\n", e.desc.PluginType)
	if prov := formatProvenance(e.desc.Metadata); prov != "" {
		fmt.Fprintf(w, "    version:      %s\n", prov)
	}
	if len(e.desc.CompatibleProviders) > 0 {
		fmt.Fprintf(w, "    targets:      %s\n", strings.Join(e.desc.CompatibleProviders, ", "))
	}
	if kinds := formatInputKinds(e.desc.Capabilities.InputKinds); kinds != "" {
		fmt.Fprintf(w, "    input kinds:  %s\n", kinds)
	}
	if e.desc.Capabilities.BoundaryAssembly != "" {
		fmt.Fprintf(w, "    boundary:     %s\n", e.desc.Capabilities.BoundaryAssembly)
	}
	if names := exportNames(e.desc.Exports); len(names) > 0 {
		fmt.Fprintf(w, "    exports:      %s\n", strings.Join(names, ", "))
	}
	fmt.Fprintln(w)
}

// formatProvenance renders a plugin's build-provenance line from its describe()
// metadata: version with commit when both are present, either alone when only
// one is, or the empty string when the guest carries no provenance (an unstamped
// build), in which case the caller omits the line.
func formatProvenance(m wasm.PluginDescriptorMeta) string {
	switch {
	case m.Version != "" && m.Commit != "":
		return m.Version + " (commit " + m.Commit + ")"
	case m.Version != "":
		return m.Version
	case m.Commit != "":
		return "(commit " + m.Commit + ")"
	default:
		return ""
	}
}

// formatInputKinds renders the plugin's input-kind claim in a fixed order, or
// the empty string when it claims none.
func formatInputKinds(k wasm.InputKinds) string {
	var parts []string
	if k.Sourced {
		parts = append(parts, "sourced")
	}
	if k.Synthesized {
		parts = append(parts, "synthesized")
	}
	if k.ArtifactsStandalone {
		parts = append(parts, "artifacts-standalone")
	}
	return strings.Join(parts, ", ")
}

// exportNames returns the plugin's export names in their declared order.
func exportNames(exports []wasm.PluginExport) []string {
	names := make([]string, 0, len(exports))
	for _, e := range exports {
		names = append(names, e.Name)
	}
	return names
}

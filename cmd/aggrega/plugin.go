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
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
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
	c.AddCommand(newPluginSyncCmd())
	c.AddCommand(newPluginAddCmd())
	c.AddCommand(newPluginRemoveCmd())
	return c
}

func newPluginListCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "list",
		Short: "List the plugins the CLI can resolve with their describe() metadata",
		Long: `List every Wasm plugin the CLI can see and, for each, its self-reported
describe() metadata.

Two groups are printed. Embedded built-ins are the provider and linter guests
compiled into the binary; they resolve with no filesystem, cache, or network
access. Configured plugins are the workspace bindings — the provider and linter
bindings and the CLI-extension commands — resolved through the plugin cascade.
Each plugin's type appears in the TYPE column, so a CLI-extension command
(type cli) is legible alongside the socket plugins; a CLI plugin also carries a
readiness label — ready, unresolved, describe-failed, or digest-drift when its
bytes changed since it was pinned at install.

The default output is a scannable table with one row per plugin, showing its
type, name, served capabilities, and version. The output is read-only and
deterministic. A plugin that fails to resolve or describe is listed with an
abnormal-state label rather than halting the whole listing.

With --json, the output is a structured object carrying the full per-plugin
detail — type, name, version, commit, capabilities, input kinds, boundary
assembly, exports, origin, readiness, and any resolve or describe error —
grouped as embedded and configured. It is suitable for programmatic
consumption and documentation generation.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return pluginListRun(cmd.Context(), rw, cmd.OutOrStdout(), asJSON)
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "emit a structured JSON object instead of the human-readable table")
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
// configured group is simply empty and the embedded built-ins still list. When
// asJSON is true it writes the full-detail structured form; otherwise it writes
// the human-readable table.
//
// The configured group carries both the socket bindings (provider and linter,
// gathered and sorted by URI) and the CLI-extension commands (plugins.cli, in
// author order), rendered as one section: the socket bindings first, then the
// CLI commands. Each subsequence is deterministic, so the concatenation is too.
// A CLI command is a configured plugin — its type shows as cli in the table —
// and it keeps the digest-pin readiness label its installed pin defines.
func pluginListRun(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, w io.Writer, asJSON bool) error {
	var wsCfg workspaceconfig.Config
	if rw != nil {
		wsCfg = rw.Config()
	}
	in := workspacePluginResolutionInputs(rw)

	var embeddedEntries []pluginListEntry
	for _, uri := range embedded.URIs() {
		// An embedded built-in resolves at the embed tier with no entity view; pass
		// only the sidecar directory so the listing matches its runtime resolution.
		embeddedEntries = append(embeddedEntries, describePluginURI(ctx, "embedded", uri, "", pluginResolutionInputs{sidecarDir: in.sidecarDir}))
	}

	var configuredEntries []pluginListEntry
	for _, ref := range collectConfiguredRefs(wsCfg) {
		configuredEntries = append(configuredEntries, describePluginURI(ctx, "configured", ref.uri, ref.origin, in))
	}
	for _, ref := range wsCfg.Plugins.CLI {
		configuredEntries = append(configuredEntries, describeInstalledPlugin(ctx, ref, in))
	}

	if asJSON {
		return writePluginListJSON(w, embeddedEntries, configuredEntries)
	}
	formatPluginList(w, embeddedEntries, configuredEntries)
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
// URI with their origins merged, sorted by URI. Every collection walk is over
// an index-ordered list and the result sorts by URI, so it is deterministic.
func collectConfiguredRefs(cfg workspaceconfig.Config) []configuredRef {
	origins := map[string][]string{}
	add := func(uri, origin string) {
		if uri == "" {
			return
		}
		origins[uri] = append(origins[uri], origin)
	}

	for i, r := range cfg.Plugins.Providers {
		add(r.URI, fmt.Sprintf("plugins.providers[%d]", i))
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
func describePluginURI(ctx context.Context, source, uri, origin string, in pluginResolutionInputs) pluginListEntry {
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

	resolved, err := resolvePluginURI(uri, in)
	if err != nil {
		entry.err = err
		return entry
	}

	pin := digest.ContentDigest(resolved.Bytes)
	entry.resolvedDigest = pin
	if desc, ok := readDescribeCache(pin); ok {
		entry.desc = desc
		return entry
	}

	spec := resolver.PluginSpec(pluginDiagName(uri), resolved)
	if spec.MaxMemoryPages == 0 {
		spec.MaxMemoryPages = describeMaxMemoryPages
	}
	loader := wasm.NewCachingLoaderFromContext(ctx, nil)
	plugin, desc, err := loader.LoadForDescribe(ctx, spec)
	if err != nil {
		entry.err = err
		return entry
	}
	_ = plugin.Close(ctx)
	writeDescribeCache(pin, desc)
	entry.desc = desc
	return entry
}

// describeInstalledPlugin enumerates one configured CLI-extension plugin for the
// listing: it resolves and describes the plugin's URI through the same cascade
// the run path uses, then derives a readiness label from whether the plugin
// resolves, instantiates, and matches the digest pinned in the config. Reusing
// describePluginURI keeps the resolve-describe-digest path single-sourced; only
// the readiness label is layered on top. The resulting entry joins the configured
// group — a CLI command is a configured plugin, distinguished by its cli type.
func describeInstalledPlugin(ctx context.Context, ref workspaceconfig.CLIPluginRef, in pluginResolutionInputs) pluginListEntry {
	entry := describePluginURI(ctx, "installed", ref.URI, "", in)
	entry.origin = fmt.Sprintf("pinned %s", shortDigest(ref.Digest))
	entry.readiness = installedReadiness(ref.Digest, entry.resolvedDigest, entry.err)
	return entry
}

// installedReadiness maps a configured CLI plugin's resolve-and-describe outcome
// and pinned digest to a one-word readiness label. A resolve failure (no fresh
// digest was computed) reads unresolved; a describe or instantiation failure
// reads describe-failed; a pinned digest that no longer matches the freshly
// resolved bytes reads digest-drift; anything else reads ready.
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

// pluginResolutionInputs is the workspace-derived context the plugin cascade
// consults: the sidecar directory and catalog Plugin-by-URI entity view every
// plugin type shares, plus the content-cache location and the per-URI content
// pins the installed CLI plugins carry so their pinned bytes resolve from the
// workspace cache offline. Only plugins.cli entries populate the pin map; a
// plugin type with no pin probes the cache tier with an empty version (a miss
// that falls through to the existing tiers), so enriching the shared resolver
// does not change how the socket plugin types resolve.
type pluginResolutionInputs struct {
	sidecarDir    string
	pluginByURI   func(string) (types.Plugin, bool)
	workspaceRoot string
	cacheDir      string
	pins          map[string]string
}

// resolvePluginURI resolves uri to its .wasm bytes through the plugin cascade.
// When a catalog Plugin accessor is supplied, an entity-fronted resolution is
// tried first so a Plugin entity URI carries the entity's authored capability
// claim and runtime limits. A URI that is not a Plugin entity — a direct
// artifact or an embedded built-in reference — falls back to the bare cascade,
// whose embed tier serves the built-ins. When the inputs carry a content-cache
// location and a pin for the URI, the cache tier reads the pinned bytes and
// verifies them against the pin, so an installed CLI plugin resolves offline from
// the bytes cached at install; the runtime supplies no puller, so a cache miss is
// a loud offline halt rather than a network fetch.
func resolvePluginURI(uri string, in pluginResolutionInputs) (pluginpath.ResolvedPlugin, error) {
	cascade := pluginpath.Resolver{
		SidecarDir:    in.sidecarDir,
		WorkspaceRoot: in.workspaceRoot,
		CacheDir:      in.cacheDir,
		Pins:          in.pins,
		PinVersions:   in.pins,
	}
	if in.pluginByURI != nil {
		front := pluginpath.EntityLookup(func(entityURI string) (string, *pluginpath.PluginContractClaim, *pluginpath.PluginRuntimeLimits, *[]pluginpath.PluginCommandMirror, bool) {
			entity, ok := in.pluginByURI(entityURI)
			if !ok {
				return "", nil, nil, nil, false
			}
			return entity.ArtifactReference(), resolver.PluginContractClaim(entity), resolver.PluginRuntimeLimits(entity), resolver.PluginCommandsMirror(entity), true
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
// resolved workspace: the sidecar plugin directory, the catalog Plugin-by-URI
// entity view, the content-cache location, and the per-URI content pins the
// installed CLI plugins carry (their plugins.cli {uri, digest} entries). Only the
// CLI plugins populate the pin map — the same prefixed content digest keys both
// the pin check and the cache-slot version — so the socket plugin types are
// unaffected. A nil workspace yields empty inputs — the bare cascade that serves
// only embedded built-ins and direct local or absolute artifact paths. The
// install path, the listing, and the runtime plugin runner all build their
// resolver from these same inputs, so a URI one path accepts every other path
// resolves identically.
func workspacePluginResolutionInputs(rw *workspaceconfig.ResolvedWorkspace) pluginResolutionInputs {
	if rw == nil {
		return pluginResolutionInputs{}
	}
	wsCfg := rw.Config()
	dirs := resolveCatalogDirs(nil, wsCfg, rw.Root())
	lazyView := catalog.NewLazyLookup(rw.Root(), wsCfg.ResolvedArtifactCacheDir(), dirs, nil)
	var pins map[string]string
	for _, ref := range wsCfg.Plugins.CLI {
		if ref.URI == "" || ref.Digest == "" {
			continue
		}
		if pins == nil {
			pins = map[string]string{}
		}
		pins[ref.URI] = ref.Digest
	}
	return pluginResolutionInputs{
		sidecarDir:    wsCfg.PluginSidecarDir,
		pluginByURI:   lazyView.PluginByURI,
		workspaceRoot: rw.Root(),
		cacheDir:      wsCfg.ResolvedArtifactCacheDir(),
		pins:          pins,
	}
}

// workspacePluginResolver binds a resolved workspace's plugin-resolution inputs
// into a single Lookup, so the runtime runner and the install path turn a plugin
// URI into bytes through the identical cascade. A URI that install accepts is
// therefore resolvable at run time; the two never diverge because they share this
// one construction.
func workspacePluginResolver(rw *workspaceconfig.ResolvedWorkspace) pluginpath.Lookup {
	in := workspacePluginResolutionInputs(rw)
	return func(uri string) (pluginpath.ResolvedPlugin, error) {
		return resolvePluginURI(uri, in)
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

// formatPluginList writes the two-group table listing to w. Each group's entries
// are already in a deterministic order; the group headers and empty-group
// placeholders keep the output readable when a group is empty. Each group renders
// as a four-column table — type, name, capabilities, version — so the whole set
// is scannable at a glance; the full per-plugin detail is available through the
// --json form. The CLI-extension commands are part of the configured group,
// legible there by their cli type.
func formatPluginList(w io.Writer, embeddedEntries, configuredEntries []pluginListEntry) {
	renderPluginGroup(w, "Embedded built-ins:", embeddedEntries)
	renderPluginGroup(w, "Configured plugins:", configuredEntries)
}

// renderPluginGroup writes one titled group as a four-column table, or an
// empty-group placeholder when it has no entries.
func renderPluginGroup(w io.Writer, header string, entries []pluginListEntry) {
	fmt.Fprintln(w, header)
	fmt.Fprintln(w)
	if len(entries) == 0 {
		fmt.Fprintln(w, "  (none)")
		fmt.Fprintln(w)
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  TYPE\tNAME\tCAPABILITIES\tVERSION")
	for _, e := range entries {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n",
			pluginRowType(e), e.uri, pluginRowCapabilities(e), pluginRowVersion(e))
	}
	_ = tw.Flush()
	fmt.Fprintln(w)
}

// pluginRowType is the table's type cell — the plugin's self-reported type, or
// empty when the plugin failed to describe.
func pluginRowType(e pluginListEntry) string {
	if e.err != nil {
		return ""
	}
	return string(e.desc.PluginType)
}

// pluginRowCapabilities is the table's capabilities cell — the served-capability
// list, empty for a cross-cutting plugin (a linter) and for a plugin that failed
// to describe.
func pluginRowCapabilities(e pluginListEntry) string {
	if e.err != nil {
		return ""
	}
	return strings.Join(e.desc.Capabilities, ", ")
}

// pluginRowVersion is the table's version cell. An abnormal state takes
// precedence over the provenance string: a resolve or describe failure shows the
// installed-plugin readiness label when one exists (unresolved, describe-failed)
// or a bare "describe failed" otherwise, and a digest drift shows its label. The
// full error text and the drifted digests are available through the --json form.
// A healthy plugin shows its version and commit, or nothing when the guest
// carries no provenance.
func pluginRowVersion(e pluginListEntry) string {
	switch {
	case e.err != nil && e.readiness != "":
		return e.readiness
	case e.err != nil:
		return "describe failed"
	case e.readiness != "" && e.readiness != "ready":
		return e.readiness
	default:
		if e.desc.Metadata == nil {
			return ""
		}
		return formatProvenance(*e.desc.Metadata)
	}
}

// pluginListDoc is the structured --json listing: the two plugin groups, each a
// list of full per-plugin detail entries. The group order and the entries within
// each group are deterministic, so the emitted JSON is byte-identical across runs
// of the same input. The configured group carries the CLI-extension commands
// alongside the socket bindings; each command's cli type distinguishes it.
type pluginListDoc struct {
	Embedded   []pluginEntryDoc `json:"embedded"`
	Configured []pluginEntryDoc `json:"configured"`
}

// pluginEntryDoc is one plugin's full detail on the --json surface: its identity
// and origin, its self-reported type, provenance, served capabilities, rendered
// input kinds, boundary assembly, and exports, plus the installed-plugin
// readiness label and any resolve or describe error. Optional fields are omitted
// when empty.
type pluginEntryDoc struct {
	URI          string   `json:"uri"`
	Type         string   `json:"type,omitempty"`
	Version      string   `json:"version,omitempty"`
	Commit       string   `json:"commit,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	InputKinds   []string `json:"inputKinds,omitempty"`
	Boundary     string   `json:"boundary,omitempty"`
	Exports      []string `json:"exports,omitempty"`
	Origin       string   `json:"origin,omitempty"`
	Readiness    string   `json:"readiness,omitempty"`
	Error        string   `json:"error,omitempty"`
}

// writePluginListJSON writes the two groups as an indented JSON object carrying
// the full per-plugin detail.
func writePluginListJSON(w io.Writer, embeddedEntries, configuredEntries []pluginListEntry) error {
	doc := pluginListDoc{
		Embedded:   pluginEntriesToDocs(embeddedEntries),
		Configured: pluginEntriesToDocs(configuredEntries),
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

// pluginEntriesToDocs maps a group's entries to their JSON detail form,
// preserving order. It returns a non-nil empty slice for an empty group so the
// JSON carries an empty array rather than null.
func pluginEntriesToDocs(entries []pluginListEntry) []pluginEntryDoc {
	docs := make([]pluginEntryDoc, 0, len(entries))
	for _, e := range entries {
		docs = append(docs, pluginEntryToDoc(e))
	}
	return docs
}

// pluginEntryToDoc maps one listing entry to its JSON detail form. A describe
// failure carries the error and omits the descriptor-derived fields.
func pluginEntryToDoc(e pluginListEntry) pluginEntryDoc {
	d := pluginEntryDoc{
		URI:       e.uri,
		Origin:    e.origin,
		Readiness: e.readiness,
	}
	if e.err != nil {
		d.Error = e.err.Error()
		return d
	}
	d.Type = e.desc.PluginType
	if e.desc.Metadata != nil {
		d.Version = e.desc.Metadata.Version
		d.Commit = e.desc.Metadata.Commit
	}
	d.Capabilities = e.desc.Capabilities
	if e.desc.Contract != nil {
		d.InputKinds = inputKindsList(e.desc.Contract.InputKinds)
		d.Boundary = e.desc.Contract.BoundaryAssembly
	}
	d.Exports = exportNames(e.desc.Exports)
	return d
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

// inputKindsList renders the plugin's input-kind claim in a fixed order, or nil
// when it claims none.
func inputKindsList(k wasm.InputKinds) []string {
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
	return parts
}

// exportNames returns the plugin's export names in their declared order.
func exportNames(exports []wasm.PluginExport) []string {
	names := make([]string, 0, len(exports))
	for _, e := range exports {
		names = append(names, e.Name)
	}
	return names
}

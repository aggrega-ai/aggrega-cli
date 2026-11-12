// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// attachPluginCommands builds the `plugin` command family under parent: the
// grouping command, then each subcommand named by the operation it carries.
//
// The group is attached before its children so that a child's invocation path is
// the one it will be invoked by when the registrar checks it against the
// operation table.
func attachPluginCommands(parent *cobra.Command) {
	c := &cobra.Command{
		Use:   "plugin",
		Short: "Inspect the Wasm plugins the CLI can resolve",
		Long: `Work with the WebAssembly plugins this CLI can resolve.

All of Aggrega's provider-specific and policy-specific behaviour lives in Wasm
plugins rather than in the binary, so this is where you see which ones are
reachable, add or remove a workspace binding, and check a guest before you
publish it.

Plugins reach the CLI two ways. Some are compiled into the binary and resolve
with no filesystem, cache or network access. The rest are workspace bindings in
.aggrega-config.yaml, resolved through the plugin cascade. "aggrega plugin list"
shows both groups together.

The subcommands below cover the whole life of a binding: list what is reachable,
add and remove bindings, run a local guest export directly, sync the
machine-owned records in a Plugin entity with its .wasm, and generate Plugin
catalog entities from a guest's own metadata.`,
		Example: `  # Everything this CLI can resolve, embedded and configured
  aggrega plugin list

  # The same, as structured output
  aggrega plugin list -o json

  # Add a provider plugin from a registry, bound to the capability it serves
  aggrega plugin add provider helm --capability helm

  # Call one export of a local guest, without publishing it
  aggrega plugin run-local --plugin ./build/my-provider.wasm --export render_node`,
	}
	mustAttachGroup(parent, c)
	mustAttach(c, newPluginListCmd(), "plugin-list")
	mustAttach(c, newPluginGenCatalogCmd(), "plugin-gen-catalog")
	mustAttach(c, newPluginSyncCmd(), "plugin-sync")
	mustAttach(c, newPluginAddCmd(), "plugin-add")
	mustAttach(c, newPluginRemoveCmd(), "plugin-remove")
	mustAttach(c, newPluginRunLocalCmd(), "plugin-run-local")
}

// pluginWriteReach names how far a plugin mutation's write reached, for the
// trailing parenthetical of a confirmation line.
//
// It is stated on every mutation rather than only on a machine-wide one, and the
// command-extension default is the reason: that type writes the file every
// workspace on this machine resolves through, so a person who passed no
// write-target flag is exactly the person most likely to read a machine-wide
// change as being about the project they are standing in. Naming the file alone
// does not settle it — that would ask the reader to recognise a path — which is
// why the reach is computed where the write target is resolved and reported
// rather than left to be inferred.
//
// Both mutations render it through here so the two cannot come to describe one
// reach in two wordings.
func pluginWriteReach(machineWide bool) string {
	if machineWide {
		return "machine-wide — every project on this machine resolves through it"
	}
	return "this project only"
}

func newPluginListCmd() *cobra.Command {
	var output string
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
status of its own — ready, unresolved, describe-failed, or digest-drift when
its bytes changed since it was pinned at install.

The default output is a scannable table with one row per plugin, showing its
type, name, served capabilities, and version. The output is read-only and
deterministic. A plugin that fails to resolve or describe is listed with its
failure status rather than halting the whole listing.

With -o json, the output is a structured object carrying the full per-plugin
detail — type, name, version, commit, capabilities, input kinds, boundary
assembly, exports, origin, readiness, and any resolve or describe error —
grouped as embedded and configured. It is suitable for programmatic
consumption and documentation generation.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := parseFormat("plugin list", output, "table", "json")
			if err != nil {
				return err
			}
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return pluginListRun(cmd.Context(), rw, cmd.OutOrStdout(), format == "json")
		},
	}
	c.Flags().StringVarP(&output, "output", "o", "table", "Output format: table|json.")
	return c
}

// pluginListing enumerates the embedded built-ins and the workspace-configured
// plugins, describing each, and returns the two groups. It never mutates state.
// A missing or minimal workspace config is not an error: the configured group is
// simply empty and the embedded built-ins still list.
//
// The configured group carries both the socket bindings (provider and linter,
// gathered and sorted by URI) and the CLI-extension commands (plugins.cli, in
// author order), in that order: the socket bindings first, then the CLI
// commands. Each subsequence is deterministic, so the concatenation is too. A
// CLI command is a configured plugin — its type shows as cli — and it keeps the
// digest-pin readiness label its installed pin defines.
//
// It is the one enumeration, and the surfaces that report plugins all call it.
// It lives here because the plugin resolution cascade's inputs are derived in
// this package, from the workspace and the command tree together, so a surface
// that cannot assemble that cascade takes this enumeration as a seam rather than
// rebuilding it — the same shape the import path's plugin resolution takes.
func pluginListing(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace) pluginops.PluginListing {
	var wsCfg workspaceconfig.Config
	if rw != nil {
		wsCfg = rw.Config()
	}
	in := workspacePluginResolutionInputs(rw)

	var listing pluginops.PluginListing
	for _, uri := range embedded.URIs() {
		// An embedded built-in resolves at the embed tier with no entity view, so
		// the listing matches its runtime resolution with an empty input set.
		listing.Embedded = append(listing.Embedded,
			describePluginURI(ctx, "embedded", uri, "", pluginResolutionInputs{}))
	}

	for _, ref := range collectConfiguredRefs(wsCfg) {
		listing.Configured = append(listing.Configured,
			describePluginURI(ctx, "configured", ref.uri, ref.origin, in))
	}
	for _, ref := range wsCfg.Plugins.CLI {
		listing.Configured = append(listing.Configured, describeInstalledPlugin(ctx, ref, in))
	}
	return listing
}

// pluginListRun writes the deterministic listing to w. When asJSON is true it
// writes the full-detail structured form; otherwise it writes the
// human-readable table.
func pluginListRun(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, w io.Writer, asJSON bool) error {
	listing := pluginListing(ctx, rw)
	if asJSON {
		return writePluginListJSON(w, listing.Embedded, listing.Configured)
	}
	formatPluginList(w, listing.Embedded, listing.Configured)
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
func describePluginURI(ctx context.Context, source, uri, origin string,
	in pluginResolutionInputs) pluginops.ListedPlugin {

	entry := pluginops.ListedPlugin{Source: source, URI: uri, Origin: origin}

	if source == "embedded" {
		if raw, ok := embedded.LookupDescriptor(uri); ok {
			var desc wasm.PluginDescriptor
			if err := json.Unmarshal(raw, &desc); err == nil {
				entry.Descriptor = desc
				return entry
			}
			// A malformed embedded descriptor falls through to load-and-describe
			// rather than reporting a corrupt static self-report.
		}
	}

	resolved, err := resolvePluginURI(uri, in)
	if err != nil {
		entry.Err = err
		return entry
	}

	pin := digest.ContentDigest(resolved.Bytes)
	entry.ResolvedDigest = pin
	if desc, ok := readDescribeCache(pin); ok {
		entry.Descriptor = desc
		return entry
	}

	spec := resolver.PluginSpec(pluginDiagName(uri), resolved)
	if spec.MaxMemoryPages == 0 {
		spec.MaxMemoryPages = wasm.DescribeMaxMemoryPages
	}
	loader := wasm.NewCachingLoaderFromContext(ctx, nil)
	plugin, desc, err := loader.LoadForDescribe(ctx, spec)
	if err != nil {
		entry.Err = err
		return entry
	}
	_ = plugin.Close(ctx)
	writeDescribeCache(pin, desc)
	entry.Descriptor = desc
	return entry
}

// describeInstalledPlugin enumerates one configured CLI-extension plugin for the
// listing: it resolves and describes the plugin's URI through the same cascade
// the run path uses, then derives a readiness label from whether the plugin
// resolves, instantiates, and matches the digest pinned in the config. Reusing
// describePluginURI keeps the resolve-describe-digest path single-sourced; only
// the readiness label is layered on top. The resulting entry joins the configured
// group — a CLI command is a configured plugin, distinguished by its cli type.
func describeInstalledPlugin(ctx context.Context, ref workspaceconfig.CLIPluginRef,
	in pluginResolutionInputs) pluginops.ListedPlugin {

	entry := describePluginURI(ctx, "installed", ref.URI, "", in)
	entry.Origin = fmt.Sprintf("pinned %s", shortDigest(ref.Digest))
	entry.Readiness = installedReadiness(ref.Digest, entry.ResolvedDigest, entry.Err)
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
// consults: the catalog Plugin-by-URI entity view every plugin type shares,
// plus the content-cache location and the per-URI content pins the installed
// CLI plugins carry so their pinned bytes resolve from the workspace cache
// offline. Only plugins.cli entries populate the pin map; a plugin type with no
// pin probes the cache tier with an empty version (a miss that falls through to
// the existing tiers), so enriching the shared resolver does not change how the
// socket plugin types resolve.
type pluginResolutionInputs struct {
	pluginByURI   func(string) (types.Plugin, bool)
	workspaceRoot string
	cacheDir      string
	pins          map[string]string

	// machineRoot and machineCacheDir address the machine-wide artifact cache where
	// it is a different cache from the one workspaceRoot and cacheDir address, and
	// are empty otherwise. A pinned CLI plugin the first cache does not hold is read
	// from this one (resolvePluginURI); nothing else is.
	machineRoot     string
	machineCacheDir string

	// colocatedDirs is where the cascade reads a module laid down beside its
	// Plugin entity in a catalog directory, keyed by the entity's artifact
	// reference. Only an import fills it, from the catalog view that selected
	// the importer (importPluginBytes); workspacePluginResolutionInputs leaves
	// it nil, which disables that tier, so the install path, the listing and the
	// runtime plugin runner resolve exactly as they would without it.
	colocatedDirs map[string]pluginpath.ColocatedDir
}

// reportUnrecordedPlugin tells the reader of this command, on its error stream,
// about a plugin the command resolved with no recorded digest to check it
// against — once per reference for the life of the command, however many of its
// commands and listings resolve the plugin. A build or a validation reports the
// plugins its own run resolves through the run instead, which records them on
// the run's advisory channel.
var reportUnrecordedPlugin = pluginpath.ReportUnrecordedTo(processStderr{})

// processStderr writes to the process's error stream as it stands at the moment
// of each write, so a writer held for the life of the command follows the stream
// rather than the file it named when the command started.
type processStderr struct{}

func (processStderr) Write(p []byte) (int, error) { return os.Stderr.Write(p) }

// resolvePluginURI resolves uri to its .wasm bytes through the plugin cascade.
// When a catalog Plugin accessor is supplied, an entity-fronted resolution is
// tried first so a Plugin entity URI carries the entity's authored capability
// claim and runtime limits. A URI that is not a Plugin entity — a direct
// artifact or an embedded built-in reference — falls back to the bare cascade,
// whose embed tier serves the built-ins. When the inputs carry a content-cache
// location and a pin for the URI, the cache tier reads the pinned bytes and
// verifies them against the pin, so an installed CLI plugin resolves offline from
// the bytes cached at install; the cascade fetches nothing, so a cache miss is
// a loud offline halt rather than a network fetch. When the inputs carry the
// directories beside the catalog's Plugin entities, the one cascade both the
// entity-fronted resolution and the bare fallback read through also reads a
// module laid down beside its entity.
//
// A pinned CLI plugin whose bytes no source holds is looked for once more in the
// machine-wide artifact cache, where the inputs name one apart from the cache
// already read: that is where a plugin added outside any workspace was cached. Its
// bytes are held to the same pin there. Where that cache does not hold them either,
// the first refusal is returned, since it names the places this resolution reads.
func resolvePluginURI(uri string, in pluginResolutionInputs) (pluginpath.ResolvedPlugin, error) {
	resolved, err := resolvePluginURIIn(uri, in)
	if err == nil || in.machineRoot == "" || in.pins[uri] == "" || !errors.Is(err, pluginpath.ErrPluginArtifactNotFound()) {
		return resolved, err
	}
	machine := in
	machine.workspaceRoot, machine.cacheDir = in.machineRoot, in.machineCacheDir
	machine.machineRoot, machine.machineCacheDir = "", ""
	if fromMachine, merr := resolvePluginURIIn(uri, machine); merr == nil {
		return fromMachine, nil
	}
	return resolved, err
}

// resolvePluginURIIn is resolvePluginURI over the one content cache in names.
func resolvePluginURIIn(uri string, in pluginResolutionInputs) (pluginpath.ResolvedPlugin, error) {
	cascade := pluginpath.NewResolver(pluginpath.ResolutionContext{
		ColocatedDirs: in.colocatedDirs,
		WorkspaceRoot: in.workspaceRoot,
		CacheDir:      in.cacheDir,
		Unrecorded:    reportUnrecordedPlugin,
	}, pluginpath.PinPostureLockfile, pluginpath.PinMaps{
		Pins:        in.pins,
		PinVersions: in.pins,
	})
	if in.pluginByURI != nil {
		front := pluginpath.EntityLookup(func(entityURI string) (string, *pluginpath.PluginContractClaim, *pluginpath.PluginRuntimeLimits, *[]pluginpath.PluginCommandMirror, bool) {
			entity, ok := in.pluginByURI(entityURI)
			if !ok {
				return "", nil, nil, nil, false
			}
			return entity.ArtifactReference(), resolver.PluginContractClaim(entity), resolver.PluginRuntimeLimits(entity), resolver.PluginCommandsMirror(entity), true
		}, cascade.Resolve, pluginpath.SurfacePermissive)
		resolved, err := front(uri)
		if err == nil {
			return resolved, nil
		}
		if !errors.Is(err, pluginpath.ErrPluginEntityNotFound()) {
			return pluginpath.ResolvedPlugin{}, err
		}
	}
	return cascade.Resolve(uri)
}

// workspacePluginResolutionInputs derives the plugin-resolution inputs from a
// resolved workspace: the catalog Plugin-by-URI entity view, the content-cache
// location, and the per-URI content pins the
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
	// CLI plugins belong to the workspace rather than to a Release, so their
	// entities are read from the workspace's own stack. These inputs are built for
	// every command, including the many that read no catalog, so a stack the
	// resolver refuses is not refused here: the stack is taken as listed, and the
	// lookup treats the refused directory as it treats any directory it cannot
	// walk. A command that reads the catalog refuses it where it builds the stack.
	dirs, _ := rw.ResolveCatalogDirs("", nil)
	named, _ := rw.NamedCatalogDirs("", nil)
	lazyView := catalog.NewLazyLookup(rw.Root(), wsCfg.ResolvedArtifactCacheDir(), dirs, nil, named...)
	lazyView.WriteNoticesTo(processStderr{})
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
	in := pluginResolutionInputs{
		pluginByURI:   lazyView.PluginByURI,
		workspaceRoot: rw.Root(),
		cacheDir:      wsCfg.ResolvedArtifactCacheDir(),
		pins:          pins,
	}
	// An installed CLI plugin's bytes are read from where this resolution caches a
	// command extension, and then from the machine-wide cache where that is somewhere
	// else: a plugin added in a folder that is no workspace is cached for the machine,
	// and runs from inside any workspace as it does outside one. Both reads hold the
	// bytes to the pin, so which cache served them changes nothing about what runs.
	if root, dir, err := rw.CommandExtensionCache(); err == nil {
		in.workspaceRoot, in.cacheDir = root, dir
	}
	if root, dir, err := rw.MachineArtifactCache(); err == nil && (root != in.workspaceRoot || dir != in.cacheDir) {
		in.machineRoot, in.machineCacheDir = root, dir
	}
	return in
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
// -o json form. The CLI-extension commands are part of the configured group,
// legible there by their cli type.
func formatPluginList(w io.Writer, embeddedEntries, configuredEntries []pluginops.ListedPlugin) {
	renderPluginGroup(w, "Embedded built-ins:", embeddedEntries)
	renderPluginGroup(w, "Configured plugins:", configuredEntries)
}

// renderPluginGroup writes one titled group as a four-column table, or an
// empty-group placeholder when it has no entries.
func renderPluginGroup(w io.Writer, header string, entries []pluginops.ListedPlugin) {
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
			pluginRowType(e), e.URI, pluginRowCapabilities(e), pluginRowVersion(e))
	}
	_ = tw.Flush()
	fmt.Fprintln(w)
}

// pluginRowType is the table's type cell — the plugin's self-reported type, or
// empty when the plugin failed to describe.
func pluginRowType(e pluginops.ListedPlugin) string {
	if e.Err != nil {
		return ""
	}
	return string(e.Descriptor.PluginType)
}

// pluginRowCapabilities is the table's capabilities cell — the served-capability
// list, empty for a cross-cutting plugin (a linter) and for a plugin that failed
// to describe.
func pluginRowCapabilities(e pluginops.ListedPlugin) string {
	if e.Err != nil {
		return ""
	}
	return strings.Join(e.Descriptor.Capabilities, ", ")
}

// pluginRowVersion is the table's version cell. An abnormal state takes
// precedence over the provenance string: a resolve or describe failure shows the
// installed-plugin readiness label when one exists (unresolved, describe-failed)
// or a bare "describe failed" otherwise, and a digest drift shows its label. The
// full error text and the drifted digests are available through the -o json form.
// A healthy plugin shows its version and commit, or nothing when the guest
// carries no provenance.
func pluginRowVersion(e pluginops.ListedPlugin) string {
	switch {
	case e.Err != nil && e.Readiness != "":
		return e.Readiness
	case e.Err != nil:
		return "describe failed"
	case e.Readiness != "" && e.Readiness != "ready":
		return e.Readiness
	default:
		if e.Descriptor.Metadata == nil {
			return ""
		}
		return formatProvenance(*e.Descriptor.Metadata)
	}
}

// pluginListDoc is the structured -o json listing: the two plugin groups, each a
// list of full per-plugin detail entries. The group order and the entries within
// each group are deterministic, so the emitted JSON is byte-identical across runs
// of the same input. The configured group carries the CLI-extension commands
// alongside the socket bindings; each command's cli type distinguishes it.
type pluginListDoc struct {
	Embedded   []pluginEntryDoc `json:"embedded"`
	Configured []pluginEntryDoc `json:"configured"`
}

// pluginEntryDoc is one plugin's full detail on the -o json surface: its identity
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
func writePluginListJSON(w io.Writer, embeddedEntries, configuredEntries []pluginops.ListedPlugin) error {
	doc := pluginListDoc{
		Embedded:   pluginEntriesToDocs(embeddedEntries),
		Configured: pluginEntriesToDocs(configuredEntries),
	}
	return writeStructured(w, "plugin list", doc)
}

// pluginEntriesToDocs maps a group's entries to their JSON detail form,
// preserving order. It returns a non-nil empty slice for an empty group so the
// JSON carries an empty array rather than null.
func pluginEntriesToDocs(entries []pluginops.ListedPlugin) []pluginEntryDoc {
	docs := make([]pluginEntryDoc, 0, len(entries))
	for _, e := range entries {
		docs = append(docs, pluginEntryToDoc(e))
	}
	return docs
}

// pluginEntryToDoc maps one listing entry to its JSON detail form. A describe
// failure carries the error and omits the descriptor-derived fields.
func pluginEntryToDoc(e pluginops.ListedPlugin) pluginEntryDoc {
	d := pluginEntryDoc{
		URI:       e.URI,
		Origin:    e.Origin,
		Readiness: e.Readiness,
	}
	if e.Err != nil {
		d.Error = e.Err.Error()
		return d
	}
	d.Type = e.Descriptor.PluginType
	if e.Descriptor.Metadata != nil {
		d.Version = e.Descriptor.Metadata.Version
		d.Commit = e.Descriptor.Metadata.Commit
	}
	d.Capabilities = e.Descriptor.Capabilities
	if e.Descriptor.Contract != nil {
		d.InputKinds = inputKindsList(e.Descriptor.Contract.InputKinds)
		d.Boundary = e.Descriptor.Contract.BoundaryAssembly
	}
	d.Exports = exportNames(e.Descriptor.Exports)
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

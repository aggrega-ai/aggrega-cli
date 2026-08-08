// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

func newInspectCmd() *cobra.Command {
	// Flag storage is per-command-instance, not package-global, so constructing
	// the command concurrently (parallel tests calling newInspectCmd) never
	// races on a shared binding target. The RunE closure captures these locals,
	// resolves them into a single inspectOptions value, and threads that struct
	// onward; the locals never reach a per-kind renderer.
	var (
		catalogDirs  []string
		listFlag     bool
		detailFlag   bool
		rawFlag      bool
		resolvedFlag bool
		colorFlag    string
		noColorFlag  bool
		outputFlag   string
		pluginFlag   string

		showImplementsFlag bool
		flatFlag           bool
	)
	c := &cobra.Command{
		Use:   "inspect [identifier]",
		Short: "Inspect a catalog entity by name or an entity file (resolves by entity kind)",
		// Arg-count contract enforced before RunE (so genuine misuse surfaces the
		// Usage block): exactly one identifier in the normal path; zero when
		// --plugin names a raw .wasm to derive host functions from.
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("plugin") {
				if len(args) > 0 {
					return fmt.Errorf("inspect --plugin <path-to-.wasm> takes no identifier argument")
				}
				return nil
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			// --plugin <path> is the pre-publish authoring loop: derive the host
			// functions from a raw .wasm not yet published or stamped, via the same
			// derivation primitive publish stamps and the loader registers from. It
			// takes no identifier argument.
			if pluginFlag != "" {
				return inspectRawPlugin(out, pluginFlag)
			}
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}

			color := colorFlag
			if noColorFlag {
				color = "never"
			}
			colorMode, err := parseColorMode(color)
			if err != nil {
				return err
			}
			output, err := parseOutputFormat(outputFlag)
			if err != nil {
				return err
			}

			// Color precedence: an explicit per-command --color=always|never (or
			// the --no-color alias mapped to "never" above) is authoritative and
			// wins over the process-global decision. --color=auto (the default)
			// defers to the process-global color-off signal — set by the global
			// --no-color / --plain flags or NO_COLOR — and, failing that, to
			// whether the sink is a terminal. The global signal is the single
			// source for the NO_COLOR read; inspect does not read it separately.
			noColorSet := globalDisplay.colorOff
			opts := inspectOptions{
				level:          resolveLevel(listFlag, detailFlag),
				mode:           resolveMode(rawFlag, resolvedFlag),
				output:         output,
				color:          colorMode,
				out:            out,
				colorize:       newColorizer(colorMode, out, noColorSet),
				showImplements: showImplementsFlag,
				flat:           flatFlag,
			}
			return dispatchInspect(rw, args[0], catalogDirs, opts)
		},
	}
	c.Flags().StringArrayVar(&catalogDirs, "catalog", nil, "Catalog directory; repeatable. Each --catalog is prepended to the workspace catalog_dirs search stack (first match wins). Defaults to the current directory.")
	c.Flags().BoolVar(&listFlag, "list", false, "Render identifiers only.")
	c.Flags().BoolVar(&detailFlag, "detail", false, "Render full field bodies.")
	c.Flags().BoolVar(&rawFlag, "raw", false, "Render the entity as authored, without resolution.")
	c.Flags().BoolVar(&resolvedFlag, "resolved", false, "Render the entity fully resolved.")
	c.Flags().StringVar(&colorFlag, "color", "auto", "Colorize output: auto|always|never.")
	c.Flags().BoolVar(&noColorFlag, "no-color", false, "Disable colorized output (alias for --color=never).")
	c.Flags().StringVarP(&outputFlag, "output", "o", "text", "Output format: text|yaml.")
	c.Flags().BoolVar(&showImplementsFlag, "show-implements", false, "For a Recipe or FeatureRecipe, resolve spec.implements against the catalog and render the bound Component or Feature.")
	c.Flags().BoolVar(&flatFlag, "flat", false, "Render a tree-shaped entity (Aggregate, Release) as a flat list, each node prefixed with its full dotted path. No effect on tabular kinds.")
	c.Flags().StringVar(&pluginFlag, "plugin", "", "Filesystem path to a raw .wasm artifact; derives and prints the host functions it imports (the pre-publish, not-yet-stamped view). Takes no identifier argument.")
	c.MarkFlagsMutuallyExclusive("list", "detail")
	c.MarkFlagsMutuallyExclusive("raw", "resolved")
	c.MarkFlagsMutuallyExclusive("color", "no-color")
	return c
}

// dispatchInspect routes the inspect command by the input form:
//
//   - Path-shaped argument (starts with ./, ../, /, or ends with .yaml /
//     .yml) → load the file, peek at kind:, dispatch to the appropriate
//     inspector branch (Component → schema; Release → per-component
//     Feature listings with provenance brackets; other kinds → forward-
//     compatible "not yet implemented" error).
//   - OCI URI (starts with oci://) → "not yet implemented" error; the
//     OCI resolver path is a separate work item.
//   - Anything else → treated as a catalog entity name, resolved by identity
//     across every entity kind (Component, Plugin, Feature, Recipe,
//     FeatureRecipe, Aggregate, SchemaLibrary, MappingLibrary, WorkloadLibrary,
//     ArtifactsLibrary) and rendered by the matched kind's inspector.
//
// catalogDirs supplies the repeatable --catalog flag directories for both the
// catalog-name branch and the Release branch's pipeline; each load site resolves
// them against the workspace catalog_dirs into the first-win search stack.
func dispatchInspect(rw *workspaceconfig.ResolvedWorkspace, identifier string, catalogDirs []string, opts inspectOptions) error {
	if strings.HasPrefix(identifier, "oci://") {
		return fmt.Errorf("inspect for OCI URIs not yet implemented; resolve to a local path or use a catalog name")
	}

	if isPathInput(identifier) {
		return inspectPath(rw, identifier, catalogDirs, opts)
	}

	return inspectCatalogEntity(rw, identifier, catalogDirs, opts)
}

// isPathInput returns true when the identifier names a filesystem path
// rather than a catalog component name. Path heuristics: a leading
// directory marker (./, ../, /) or a YAML extension on the trailing
// segment.
func isPathInput(identifier string) bool {
	if strings.HasPrefix(identifier, "./") || strings.HasPrefix(identifier, "../") || strings.HasPrefix(identifier, "/") {
		return true
	}
	if strings.HasSuffix(identifier, ".yaml") || strings.HasSuffix(identifier, ".yml") {
		return true
	}
	return false
}

// resolveSiblingYAML returns the absolute path of a sibling YAML file
// (the Aggregate or Manifest referenced by a Release) under dir. Three
// shapes are handled:
//
//   - name carries an explicit .yaml or .yml extension → returned
//     verbatim joined with dir (avoids the double-extension trap when
//     the Release author wrote `aggregate: foo.yaml` instead of `foo`).
//   - name has no extension → try `<name>.yaml` first; if absent on
//     disk, try `<name>.yml`; return whichever exists.
//   - neither exists → fall back to `<name>.yaml` so the downstream
//     loader produces a clear path-not-found error.
//
// Non-NotExist Stat errors (e.g. permission-denied) on a candidate
// short-circuit fallback evaluation and return that candidate directly,
// so the downstream loader surfaces the underlying error rather than
// having it masked by a fallback attempt on a sibling extension.
func resolveSiblingYAML(dir, name string) string {
	if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
		return filepath.Join(dir, name)
	}
	candidate := filepath.Join(dir, name+".yaml")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	} else if !errors.Is(err, fs.ErrNotExist) {
		return candidate
	}
	alt := filepath.Join(dir, name+".yml")
	if _, err := os.Stat(alt); err == nil {
		return alt
	} else if !errors.Is(err, fs.ErrNotExist) {
		return alt
	}
	return candidate
}

// kindRenderer renders one entity kind authored as a standalone file. Each
// per-kind inspector is a kindRenderer registered in the kindRenderers table;
// adding a kind is a single table entry. The renderer reads the already-loaded
// bytes, the catalog search stack, and the resolved inspectOptions (which
// carry the level, mode, output format, color decision, and the output sink),
// and writes to opts.out.
type kindRenderer func(rw *workspaceconfig.ResolvedWorkspace, path string, data []byte, catalogDirs []string, opts inspectOptions) error

// kindRenderers maps an entity kind to its file-path inspector. inspectPath
// dispatches through this table; kinds absent from it fall through to the
// not-yet-implemented / non-standalone handling in inspectPath.
var kindRenderers = map[string]kindRenderer{
	"Component":        renderComponentFile,
	"Feature":          renderFeatureFile,
	"Recipe":           renderRecipeFile,
	"FeatureRecipe":    renderFeatureRecipeFile,
	"Plugin":           renderPluginFile,
	"MappingLibrary":   renderMappingLibraryFile,
	"WorkloadLibrary":  renderWorkloadLibraryFile,
	"ArtifactsLibrary": renderArtifactsLibraryFile,
	"SchemaLibrary":    renderSchemaLibraryFile,
	"Manifest":         renderManifestFile,
	"Aggregate":        renderAggregateFile,
	"Release":          renderReleaseFile,
}

// nonStandalone names the kinds that exist only embedded under another entity
// and so cannot be inspected as standalone files (A8 hard error).
var nonStandalone = map[string]bool{
	"Workload": true,
}

// inspectPath reads a YAML file, peeks at apiVersion / kind, and dispatches to
// the per-kind renderer registered for that kind. A kind that is structurally
// embedded-only (Workload) is a hard error naming the entity it embeds under;
// an unrecognized kind reports the unknown kind.
func inspectPath(rw *workspaceconfig.ResolvedWorkspace, path string, catalogDirs []string, opts inspectOptions) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	var preamble struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &preamble); err != nil {
		return fmt.Errorf("parse YAML preamble in %q: %w", path, err)
	}
	if preamble.Kind == "" {
		return fmt.Errorf("couldn't determine entity kind from %q; expected apiVersion/kind preamble", path)
	}

	if render, ok := kindRenderers[preamble.Kind]; ok {
		return render(rw, path, data, catalogDirs, opts)
	}
	if nonStandalone[preamble.Kind] {
		return fmt.Errorf("kind %s is not a standalone inspectable entity; %s is embedded in Recipes — inspect a Recipe to see its %s", preamble.Kind, preamble.Kind, preamble.Kind)
	}
	return fmt.Errorf("unknown entity kind %q in %q", preamble.Kind, path)
}

// inspectCatalogEntity resolves a bare identifier against the catalog across
// every entity kind and renders the first match. It loads the catalog over the
// first-win search stack, resolves extends chains, then tries kinds in a fixed
// precedence — Component, Plugin, Feature, Recipe, FeatureRecipe, Aggregate,
// SchemaLibrary, MappingLibrary, WorkloadLibrary, ArtifactsLibrary — returning the first identity match.
//
// A Component match keeps its dedicated render path: the merged schema +
// usesSchema view plus a best-effort deprecation advisory. Every other kind is
// handed to the same per-kind inspector the file-path branch uses (via the
// kindRenderers table), so a bare identifier and a standalone file of the same
// entity render identically. For the pure-unmarshal kinds the matched in-memory
// entity is re-marshaled to YAML for that dispatch; a bare non-Component
// identifier therefore shows the entity's canonical re-marshaled form under
// --raw / --output yaml, since catalog entities retain no single authored-file
// byte source uniformly across kinds. Aggregate is the exception: its inspector
// runs the topology pass over the entity read from its source file, so it
// receives the retained source path and authored bytes rather than a re-marshal.
//
// No match in any kind is a clear not-found naming the identifier and stating it
// matched no catalog entity of any kind — the bare identifier is not assumed to
// be a Component.
func inspectCatalogEntity(rw *workspaceconfig.ResolvedWorkspace, identifier string, catalogDirs []string, opts inspectOptions) error {
	// Catalog entity details print without rendering any per-node IR, so no
	// workspace-relative path normalization is needed. The resolved workspace root
	// is still supplied, because it is the anchor the configured cache directory is
	// written against: with it, the walk steps over cached library.yaml files rather
	// than showing them as authored Tier-1 catalog no matter which directory
	// inspect is invoked from.
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	// Strict load (inspect must fail on a broken catalog, unlike build's tolerant
	// Pass-1) over the first-win stack: --catalog dirs prepended to catalog_dirs.
	dirs := workspaceconfig.ResolveCatalogDirs(catalogDirs, wsCfg, ".")
	cat, err := catalog.LoadAllWithCacheDir(wsRoot, cacheDir, dirs...)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		return fmt.Errorf("resolve extends: %s", extErrs[0].Error())
	}
	lookup := cat.CatalogLookup()

	// Component is first and keeps its render path exactly: the structured
	// schema view plus the best-effort deprecation probe. A probe failure
	// degrades to showing nothing — inspect never fails on the deprecation probe.
	if comp, found := lookup.Component(identifier); found {
		if err := renderComponentStructured(comp, identifier, opts); err != nil {
			return err
		}
		if v, ok := lookupComponentDeprecation(rw, identifier); ok {
			renderDeprecationBlock(opts.out, v)
		}
		return nil
	}

	// Remaining kinds resolve through the version-aware catalog accessors; the
	// first match wins in the fixed precedence below.
	if p, ok := lookup.Plugin(identifier); ok {
		return renderCatalogMatch(rw, "Plugin", identifier, p, catalogDirs, opts)
	}
	if f, ok := lookup.Feature(identifier); ok {
		return renderCatalogMatch(rw, "Feature", identifier, f, catalogDirs, opts)
	}
	if r, ok := cat.RecipeByURI(identifier); ok {
		return renderCatalogMatch(rw, "Recipe", identifier, r, catalogDirs, opts)
	}
	if fr, ok := cat.FeatureRecipeByURI(identifier); ok {
		return renderCatalogMatch(rw, "FeatureRecipe", identifier, fr, catalogDirs, opts)
	}
	// Aggregate is a resolved-instance kind: its inspector reads the entity from
	// a filesystem path to run the topology pass, unlike the pure-unmarshal
	// renderers that read the passed bytes. The catalog-resolved match carries
	// the retained source path and authored bytes, so it is dispatched directly
	// rather than through a re-marshal.
	if ra, ok := cat.AggregateLookup()(identifier); ok {
		return kindRenderers["Aggregate"](rw, ra.Path, ra.Bytes, catalogDirs, opts)
	}
	if lib, ok := cat.SchemaLibraries[identifier]; ok {
		return renderCatalogMatch(rw, "SchemaLibrary", identifier, lib, catalogDirs, opts)
	}
	if lib, ok := cat.MappingLibraries[identifier]; ok {
		return renderCatalogMatch(rw, "MappingLibrary", identifier, lib, catalogDirs, opts)
	}
	if lib, ok := cat.WorkloadLibraries[identifier]; ok {
		return renderCatalogMatch(rw, "WorkloadLibrary", identifier, lib, catalogDirs, opts)
	}
	if lib, ok := cat.ArtifactsLibraries[identifier]; ok {
		return renderCatalogMatch(rw, "ArtifactsLibrary", identifier, lib, catalogDirs, opts)
	}
	return fmt.Errorf("%q matched no catalog entity of any kind", identifier)
}

// renderCatalogMatch re-marshals a catalog-resolved entity to YAML and
// dispatches it through the kind's file-path inspector, which re-unmarshals into
// the same typed struct. It is the bare-name analogue of inspectPath for the
// pure-unmarshal kinds: the render logic is shared with the file branch, so a
// bare identifier and a standalone file of the same entity render identically.
// The identifier stands in for the file path (used only in the inspectors'
// error strings and YAML round-trip diagnostics, never read from disk).
func renderCatalogMatch(rw *workspaceconfig.ResolvedWorkspace, kind, identifier string, entity any, catalogDirs []string, opts inspectOptions) error {
	data, err := yaml.Marshal(entity)
	if err != nil {
		return fmt.Errorf("marshal %s %q: %w", kind, identifier, err)
	}
	return kindRenderers[kind](rw, identifier, data, catalogDirs, opts)
}

// inspectReleaseFile loads a Release plus its referenced Aggregate and
// Manifest, runs the build pipeline far enough to produce the IR
// (Resolve → ExpandAnnotations → RunGuards → Topology → IR), and
// prints per-node Feature attachments with provenance brackets:
//
//	Component: api  (node: aws-prod/eks-main/payments/api)
//	  Features:
//	    - core/rewrite-host:1.0.0    [explicit]
//	    - core/metrics-exporter:1.0.0    [annotation: feature.myco/enable-metrics from release.yaml:boundaries.prod.annotations.feature.myco/enable-metrics]
//
// The catalog directory comes from the --catalog flag (default `.`),
// mirroring `aggrega build`'s catalog discovery.
//
// Release is a resolved instance kind: its render subject is the pipeline
// result.
//
//   - --raw: the Release as authored (no pipeline), via the YAML round-trip.
//   - --output yaml: the resolved IR graph re-emitted as YAML (the pipeline
//     result is the resolved subject).
//   - default / --resolved: a boundary-organised text layout — each boundary
//     carries its output directory, sync-wave, and a two-way annotation split
//     (aggrega.ai/* provider-consumed vs. other pass-through); each component
//     under a boundary carries its Aggregate component type, its uses-edges
//     (cross-boundary edges marked), and its Feature attachments with the
//     preserved provenance brackets. --flat collapses the boundary tree to one
//     section per boundary keyed by its full path.
//
// This function threads opts.out as the output sink and resolves through the
// same shared funnel build/lint/pull use, so its resolve path cannot drift from
// the compiler's.
func renderReleaseFile(rw *workspaceconfig.ResolvedWorkspace, releasePath string, data []byte, catalogDirs []string, opts inspectOptions) error {
	// --raw is the as-authored bypass: emit the Release file verbatim, no
	// pipeline, regardless of level.
	if opts.mode == modeForcedRaw {
		return emitYAMLRoundTrip(opts, releasePath, data)
	}
	// wsRoot is the resolved workspace root; the catalog renders its entity
	// source-file accessors relative to it so catalog-sourced IR records read
	// consistently across machines. The root Aggregate node's own source path
	// comes from the AggregatePath label passed to the resolve funnel below,
	// not from wsRoot.
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	// Strict load over the first-win stack (--catalog dirs prepended to catalog_dirs).
	dirs := workspaceconfig.ResolveCatalogDirs(catalogDirs, wsCfg, ".")
	cat, err := catalog.LoadAllWithCacheDir(wsRoot, cacheDir, dirs...)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		return fmt.Errorf("resolve extends: %s", extErrs[0].Error())
	}

	releaseDir := filepath.Dir(releasePath)
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		return fmt.Errorf("load release: %w", err)
	}
	aggPath := resolveSiblingYAML(releaseDir, rel.Spec.Aggregate)
	manifestPath := resolveSiblingYAML(releaseDir, rel.Spec.Manifest)

	manifest, err := resolver.LoadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		return fmt.Errorf("read aggregate %q: %w", aggPath, err)
	}
	catalogLookup := cat.CatalogLookup()

	// Resolve through the same shared funnel build/lint/pull use
	// (pipeline.ResolveAndFlatten → resolver.ResolveAndUse), so inspect's resolve
	// path cannot drift from the compiler's and the parameter purge is covered by
	// the producer-owned defer — inspect holds no purge defer of its own. The
	// scoped callback builds the IR and prints while parameters are live. The
	// AggregatePath / ReleasePath labels carry the source locations the
	// annotation-expansion brackets read; they are the same strings inspect
	// previously passed, so the bracket output is unchanged.
	return pipeline.ResolveAndFlatten(
		context.Background(),
		pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggBytes,
			// AggregatePath is the sibling-resolved path (drives the annotation
			// source-location brackets — kept identical to the pre-convergence
			// value); RootAggregateFile is the workspace-relative path stamped
			// onto the root-tier node (machine-stable), the same split inspect
			// made before converging onto the shared funnel.
			AggregatePath:     aggPath,
			ReleasePath:       releasePath,
			RootAggregateFile: catalog.RelativeToWorkspace(wsRoot, aggPath),
		},
		catalogLookup,
		cat.SubAggregateLoader(),
		func(resolveResult *resolver.ResolveResult, topoResult *topology.TopologyResult) error {
			graph, _, err := ir.BuildIR(ir.BuildIRConfig{
				Topology:        topoResult,
				ResolveResult:   resolveResult,
				CatalogLookup:   catalogLookup,
				CompilerVersion: provenanceCompilerVersion(),
			})
			if err != nil {
				return fmt.Errorf("IR graph construction: %w", err)
			}
			return renderReleaseResolved(opts, rel, releasePath, graph, resolveResult, catalogLookup)
		},
	)
}

// renderFeatureAttachment writes one Feature attachment to stdout.
// When the attachment's per-leaf provenance is uniform across both
// pillars, the function emits the per-attachment marker (the format
// established before per-leaf provenance shipped) so callers reading
// the common case see one compact line per attachment. When provenance
// diverges across leaves, the function expands into per-leaf lines —
// one bracket per leaf the attachment populates — so the rendered
// output surfaces the divergence that consumers would otherwise miss.
// declared names which of the attached Feature's leaves its schema declares
// sensitive; each such leaf prints the redaction placeholder in place of its
// value while keeping its key, its pillar prefix and its provenance bracket.
// Only a declared leaf is hidden: the per-attachment sensitivity roll-up says
// the attachment handles secret material, not which of its leaves do, and
// hiding all of them would print the placeholder over a referenceable
// credential's name and key — public locators an author left unmarked.
//
// Both layouts carry the attachment-level facts clause on the attachment's own
// line, so which slot an attachment fills and whether it handles declared
// secret material read the same whichever layout the provenance selects.
func renderFeatureAttachment(out io.Writer, instanceName string, entry types.IRFeatureEntry, declared resolver.SensitiveLeaves) {
	// Formatted once, ahead of the layout branch: the clause describes the
	// attachment rather than a leaf, so both layouts print it in the same
	// position and neither can be edited into dropping it alone.
	facts := attachmentFactsBracket(entry)
	if facts != "" {
		facts = "    " + facts
	}
	if featureProvenanceLayout(entry) {
		fmt.Fprintf(out, "    - %s%s    %s\n", featureLabel(instanceName, entry), facts, perAttachmentBracket(entry))
		return
	}
	fmt.Fprintf(out, "    - %s%s\n", featureLabel(instanceName, entry), facts)
	for _, leaf := range walkLeavesInOrder(entry) {
		if leafDeclaredSensitive(declared, leaf.path) {
			fmt.Fprintf(out, "        %s: %s    %s\n", leaf.path, redact.Placeholder(), perLeafBracket(leaf.provenance))
			continue
		}
		fmt.Fprintf(out, "        %s: %v    %s\n", leaf.path, leaf.value, perLeafBracket(leaf.provenance))
	}
}

// leafDeclaredSensitive answers the pillar-prefixed question walkLeavesInOrder
// asks. Its input path carries the `values.` / `infra.` prefix the renderer
// prints; the declared sets are keyed from each pillar's own root, so the
// prefix is what selects the set and the remainder is the key.
func leafDeclaredSensitive(declared resolver.SensitiveLeaves, prefixedPath string) bool {
	if rest, ok := strings.CutPrefix(prefixedPath, "values."); ok {
		return declared.Values[rest]
	}
	if rest, ok := strings.CutPrefix(prefixedPath, "infra."); ok {
		return declared.Infra[rest]
	}
	return false
}

// declaredFeatureLeaves reads which leaves an attached Feature's schema
// declares sensitive, through the same compile-and-read pair the IR's
// sensitivity derivation uses, so what inspect hides and what the graph marks
// come from one declaration. An absent Feature, or one declaring no schema,
// reports nothing sensitive rather than guessing.
func declaredFeatureLeaves(lookup resolver.CatalogLookup, featureType string) resolver.SensitiveLeaves {
	compiled, ok := ir.CompileFeatureSchema(lookup, featureType)
	if !ok {
		return resolver.SensitiveLeaves{}
	}
	return resolver.DeclaredSensitiveLeaves(compiled)
}

// featureLabel formats the Feature URI line. The IR's Type field
// carries the typed URI (type:version) when resolved; the instance
// name supplements it when the two differ.
func featureLabel(instanceName string, entry types.IRFeatureEntry) string {
	label := entry.Type
	if entry.ResolvedVersion != "" && !strings.Contains(entry.Type, ":") {
		label = entry.Type + ":" + entry.ResolvedVersion
	}
	if instanceName != "" && instanceName != entry.Type {
		label = instanceName + " (" + label + ")"
	}
	return label
}

// attachmentFactsBracket formats the clause carrying the two per-attachment
// facts the IR builder derives about an attachment itself rather than about one
// of its leaves: the attachment slot it fills, and whether it handles material
// an author declared sensitive. Both are read from the entry; neither is
// recomputed here, so what inspect prints and what the IR carries cannot
// disagree.
//
// The slot half distinguishes three states, not two:
//
//   - a slot name prints as "slot: <name>";
//   - [types.AttachmentBindingFree] prints as that bare word — the positive
//     finding that the resolved Component declares no slot of this name;
//   - an empty binding prints no clause at all. Empty is the absence of a
//     finding, because the host Component's declared slot set did not resolve,
//     and printing the free sentinel for it would report a finding the
//     compiler never made.
//
// The sensitivity half appears only for a marked attachment. Its absence
// records that no declaration marked the attachment, which is a narrower
// statement than the attachment being free of secret material.
//
// Returns the empty string when the entry carries neither fact, so an
// attachment on a node whose Component does not resolve and whose declarations
// mark nothing prints the line it printed before either fact existed.
func attachmentFactsBracket(entry types.IRFeatureEntry) string {
	var parts []string
	switch entry.Binding {
	case "":
	case types.AttachmentBindingFree:
		parts = append(parts, types.AttachmentBindingFree)
	default:
		parts = append(parts, "slot: "+entry.Binding)
	}
	if entry.Sensitive {
		parts = append(parts, "sensitive")
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// perAttachmentBracket formats the trailing [explicit] /
// [annotation: ...] suffix appended to each Feature listing when every
// populated leaf shares the same FieldProvenance shape. The function
// reads the top-level Origin / AnnotationKey / AnnotationSource fields
// the IR builder populates from the per-leaf provenance derivation.
func perAttachmentBracket(entry types.IRFeatureEntry) string {
	if entry.Origin == "annotation" {
		return fmt.Sprintf("[annotation: %s from %s]", entry.AnnotationKey, entry.AnnotationSource)
	}
	return "[explicit]"
}

// perLeafBracket formats the bracket suffix for one leaf when the
// attachment's per-leaf provenance is heterogeneous. The shape mirrors
// perAttachmentBracket but emits an additional "overrides annotation"
// clause when the leaf's explicit contribution suppressed an annotation
// contribution at the same leaf.
func perLeafBracket(prov types.FieldProvenance) string {
	if prov.Origin == "annotation" {
		return fmt.Sprintf("[annotation: %s from %s]", prov.AnnotationKey, prov.AnnotationSource)
	}
	if prov.OverriddenAnnotation != nil {
		return fmt.Sprintf("[explicit; overrides annotation: %s from %s]",
			prov.OverriddenAnnotation.AnnotationKey,
			prov.OverriddenAnnotation.AnnotationSource)
	}
	return "[explicit]"
}

// featureProvenanceLayout reports whether an IRFeatureEntry's per-leaf
// provenance is uniform across both pillars. The decision rule:
//
//   - Empty per-leaf maps → uniform (the attachment carries no leaves;
//     the top-level provenance is the only signal).
//   - Every populated leaf shares the same FieldProvenance shape
//     (same Origin, Side, AnnotationKey, AnnotationSource, no
//     OverriddenAnnotation at any leaf) → uniform.
//   - Otherwise → heterogeneous.
//
// Uniform attachments fall back to the per-attachment marker for
// compact output; heterogeneous attachments expand into per-leaf
// lines so consumers see each leaf's origin distinctly.
func featureProvenanceLayout(entry types.IRFeatureEntry) bool {
	var first types.FieldProvenance
	hasFirst := false
	check := func(m types.OrderedMap[types.FieldProvenance]) bool {
		for _, fp := range m.All() {
			if fp.OverriddenAnnotation != nil {
				return false
			}
			if !hasFirst {
				first = fp
				hasFirst = true
				continue
			}
			if fp.Origin != first.Origin ||
				fp.Side != first.Side ||
				fp.AnnotationKey != first.AnnotationKey ||
				fp.AnnotationSource != first.AnnotationSource {
				return false
			}
		}
		return true
	}
	if !check(entry.ValuesProvenance) {
		return false
	}
	return check(entry.InfraProvenance)
}

// leafRecord pairs a dotted-path leaf identifier with the leaf's scalar
// value and the FieldProvenance carried for that leaf. walkLeavesInOrder
// emits these in the order the inspect renderer prints them.
type leafRecord struct {
	path       string
	value      any
	provenance types.FieldProvenance
}

// walkLeavesInOrder returns the populated leaves of an IRFeatureEntry
// in render order — values pillar first (author order, flattened to
// dotted-path scalars from nested OrderedMap intermediates), then
// infra pillar (same discipline). Each leaf's path carries the pillar
// prefix (`values.` / `infra.`) followed by the dotted segment chain
// produced by types.FlattenLeaves walking the nested OrderedMap tree;
// the leaf's value is the primitive scalar at the terminal segment;
// the FieldProvenance lookup uses the flat-dotted-leaf path (the
// same key the IR builder records during the per-leaf merge). The
// IR builder and the inspect renderer share types.FlattenLeaves so
// the two sequences align by construction.
func walkLeavesInOrder(entry types.IRFeatureEntry) []leafRecord {
	out := make([]leafRecord, 0, entry.ValuesProvenance.Len()+entry.InfraProvenance.Len())
	for _, p := range types.FlattenLeaves(entry.Values) {
		prov, _ := entry.ValuesProvenance.Get(p.Path)
		out = append(out, leafRecord{path: "values." + p.Path, value: p.Value, provenance: prov})
	}
	for _, p := range types.FlattenLeaves(entry.Infra) {
		prov, _ := entry.InfraProvenance.Get(p.Path)
		out = append(out, leafRecord{path: "infra." + p.Path, value: p.Value, provenance: prov})
	}
	return out
}

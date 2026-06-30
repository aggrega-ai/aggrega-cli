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

		showImplementsFlag bool
		flatFlag           bool
	)
	c := &cobra.Command{
		Use:   "inspect <identifier>",
		Short: "Inspect a catalog Component or a Release file (resolves by entity kind)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			out := cmd.OutOrStdout()
			_, noColorSet := os.LookupEnv("NO_COLOR")
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
	c.Flags().StringVar(&outputFlag, "output", "text", "Output format: text|yaml.")
	c.Flags().BoolVar(&showImplementsFlag, "show-implements", false, "For a Recipe or FeatureRecipe, resolve spec.implements against the catalog and render the bound Component or Feature.")
	c.Flags().BoolVar(&flatFlag, "flat", false, "Render a tree-shaped entity (Aggregate, Release) as a flat list, each node prefixed with its full dotted path. No effect on tabular kinds.")
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
//   - Anything else → treated as a catalog Component name, dispatched to
//     the legacy schema-rendering branch.
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

	return inspectCatalogComponent(rw, identifier, catalogDirs, opts)
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
	"Component":      renderComponentFile,
	"Feature":        renderFeatureFile,
	"Recipe":         renderRecipeFile,
	"FeatureRecipe":  renderFeatureRecipeFile,
	"Provider":       renderProviderFile,
	"WasmPlugin":     renderWasmPluginFile,
	"MappingLibrary": renderMappingLibraryFile,
	"SchemaLibrary":  renderSchemaLibraryFile,
	"Manifest":       renderManifestFile,
	"Aggregate":      renderAggregateFile,
	"Release":        renderReleaseFile,
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

// inspectCatalogComponent runs the legacy catalog-component-name path.
// Loads the catalog at catalogDir, resolves extends chains, looks up
// the named Component, and prints its merged schema + usesSchema.
func inspectCatalogComponent(rw *workspaceconfig.ResolvedWorkspace, componentType string, catalogDirs []string, opts inspectOptions) error {
	// Workspace-agnostic inspection: this path prints catalog Component
	// details without rendering any per-node IR, so no workspace-relative
	// path normalization is needed. The walk still skips the configured
	// cache directory so cached library.yaml files are not shown as authored
	// Tier-1 catalog.
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	// Strict load (inspect must fail on a broken catalog, unlike build's tolerant
	// Pass-1) over the first-win stack: --catalog dirs prepended to catalog_dirs.
	dirs := resolveCatalogDirs(catalogDirs, wsCfg, ".")
	cat, err := catalog.LoadAllWithCacheDir("", cacheDir, dirs...)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		return fmt.Errorf("resolve extends: %s", extErrs[0].Error())
	}
	comp, found := cat.CatalogLookup().Component(componentType)
	if !found {
		return fmt.Errorf("component %q not found in catalog", componentType)
	}
	return renderComponentStructured(comp, componentType, opts)
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
	dirs := resolveCatalogDirs(catalogDirs, wsCfg, ".")
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
	catalogLookup.ProviderInfraSchema = cat.ProviderInfraSchemaResolver(manifest)

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
				return fmt.Errorf("Step 3 (IR): %w", err)
			}
			return renderReleaseResolved(opts, rel, releasePath, graph, resolveResult)
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
func renderFeatureAttachment(out io.Writer, instanceName string, entry types.IRFeatureEntry) {
	if featureProvenanceLayout(entry) {
		fmt.Fprintf(out, "    - %s    %s\n", featureLabel(instanceName, entry), perAttachmentBracket(entry))
		return
	}
	fmt.Fprintf(out, "    - %s\n", featureLabel(instanceName, entry))
	for _, leaf := range walkLeavesInOrder(entry) {
		fmt.Fprintf(out, "        %s: %v    %s\n", leaf.path, leaf.value, perLeafBracket(leaf.provenance))
	}
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
	if prov.OverridesAnnotation != nil {
		return fmt.Sprintf("[explicit; overrides annotation: %s from %s]",
			prov.OverridesAnnotation.AnnotationKey,
			prov.OverridesAnnotation.AnnotationSource)
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
//     OverridesAnnotation at any leaf) → uniform.
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
			if fp.OverridesAnnotation != nil {
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

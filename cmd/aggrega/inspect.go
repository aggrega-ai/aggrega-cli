// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cognitio/aggrega-cli/internal/compiler/ir"
	"github.com/cognitio/aggrega-cli/internal/compiler/topology"
	"github.com/cognitio/aggrega-cli/pkg/catalog"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var inspectCatalogDir string

var inspectCmd = &cobra.Command{
	Use:   "inspect <identifier>",
	Short: "Inspect a catalog Component or a Release file (resolves by entity kind)",
	Args:  cobra.ExactArgs(1),
	RunE:  runInspect,
}

func init() {
	inspectCmd.Flags().StringVar(&inspectCatalogDir, "catalog", ".", "Directory containing catalog entities")
	rootCmd.AddCommand(inspectCmd)
}

// runInspect dispatches by the input form:
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
// The --catalog flag supplies the catalog directory for both the
// catalog-name branch and the Release branch's pipeline. Default `.`.
func runInspect(cmd *cobra.Command, args []string) error {
	identifier := args[0]

	if strings.HasPrefix(identifier, "oci://") {
		return fmt.Errorf("inspect for OCI URIs not yet implemented; resolve to a local path or use a catalog name")
	}

	if isPathInput(identifier) {
		return inspectPath(identifier, inspectCatalogDir)
	}

	return inspectCatalogComponent(identifier, inspectCatalogDir)
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

// inspectPath reads a YAML file, peeks at apiVersion / kind, and
// dispatches to the per-kind inspector branch.
func inspectPath(path, catalogDir string) error {
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

	switch preamble.Kind {
	case "Component":
		return inspectComponentFile(path, data)
	case "Release":
		return inspectReleaseFile(path, catalogDir)
	case "Manifest", "Aggregate", "Feature", "Recipe", "FeatureRecipe", "Provider", "Workload":
		return fmt.Errorf("inspect not yet implemented for kind %s", preamble.Kind)
	default:
		return fmt.Errorf("unknown entity kind %q in %q", preamble.Kind, path)
	}
}

// inspectCatalogComponent runs the legacy catalog-component-name path.
// Loads the catalog at catalogDir, resolves extends chains, looks up
// the named Component, and prints its merged schema + usesSchema.
func inspectCatalogComponent(componentType, catalogDir string) error {
	cat, err := catalog.Load(catalogDir)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		return fmt.Errorf("resolve extends: %s", extErrs[0].Error())
	}
	comp, found := cat.Components[componentType]
	if !found {
		return fmt.Errorf("component %q not found in catalog", componentType)
	}

	header := fmt.Sprintf("// Component: %s", componentType)
	if comp.Spec.Extends != "" {
		header += " (resolved from inheritance chain)"
	}
	fmt.Println(header)

	if comp.Spec.Schema.Kind != 0 {
		out, err := yaml.Marshal(&comp.Spec.Schema)
		if err != nil {
			return fmt.Errorf("render schema: %w", err)
		}
		fmt.Print(string(out))
	} else {
		fmt.Println("// (no schema defined)")
	}

	if comp.Spec.UsesSchema.Len() > 0 {
		fmt.Println("\n// usesSchema:")
		for socket, entry := range comp.Spec.UsesSchema.All() {
			fmt.Printf("//   %s: selector=%q\n", socket, entry.Selector)
		}
	}
	return nil
}

// inspectComponentFile renders the schema for a Component authored as a
// standalone file. Output shape matches the catalog-name path's schema
// rendering. usesSchema and extends headers are surfaced when present;
// schema-less Components produce the `(no schema defined)` placeholder.
func inspectComponentFile(path string, data []byte) error {
	// The minimal Component shape needed for inspect output is parsed
	// by reusing the catalog's loader logic indirectly: we marshal the
	// already-read bytes into a temporary Component struct via the
	// catalog's package-level types. To avoid a unneeded coupling the
	// implementation here narrows to the fields inspect renders.
	var comp struct {
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Spec struct {
			Extends string    `yaml:"extends"`
			Schema  yaml.Node `yaml:"schema"`
			// UsesSchema rendering parallels the catalog branch but
			// without resolving extends inheritance — the file is
			// inspected as authored.
			UsesSchema map[string]struct {
				Selector string `yaml:"selector"`
			} `yaml:"usesSchema"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &comp); err != nil {
		return fmt.Errorf("parse Component at %q: %w", path, err)
	}
	name := comp.Metadata.Name
	if name == "" {
		name = path
	}
	header := fmt.Sprintf("// Component: %s (from %s)", name, path)
	if comp.Spec.Extends != "" {
		header += " (declares extends; inheritance NOT resolved for file-path inspection)"
	}
	fmt.Println(header)
	if comp.Spec.Schema.Kind != 0 {
		out, err := yaml.Marshal(&comp.Spec.Schema)
		if err != nil {
			return fmt.Errorf("render schema: %w", err)
		}
		fmt.Print(string(out))
	} else {
		fmt.Println("// (no schema defined)")
	}
	if len(comp.Spec.UsesSchema) > 0 {
		fmt.Println("\n// usesSchema:")
		for socket, entry := range comp.Spec.UsesSchema {
			fmt.Printf("//   %s: selector=%q\n", socket, entry.Selector)
		}
	}
	return nil
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
func inspectReleaseFile(releasePath, catalogDir string) error {
	cat, err := catalog.Load(catalogDir)
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

	resolveResult, err := resolver.Resolve(resolver.ResolveConfig{
		AggregatePath: aggPath,
		ReleasePath:   releasePath,
		ManifestPath:  manifestPath,
	})
	if err != nil {
		return fmt.Errorf("Step 1 (resolve): %w", err)
	}
	defer resolver.PurgeParameters(&resolveResult.Parameters)

	catalogLookup := cat.CatalogLookup()
	if err := resolver.ExpandAnnotations(resolveResult, catalogLookup); err != nil {
		return fmt.Errorf("Step 1 (annotation expansion): %w", err)
	}
	if guardErrs := resolver.RunGuards(resolveResult.Aggregate, resolveResult.Release, catalogLookup, resolveResult.BoundaryInfra); len(guardErrs) > 0 {
		for _, e := range guardErrs {
			fmt.Fprintf(os.Stderr, "  guard: %s\n", e)
		}
		return fmt.Errorf("Step 1: %d guard violation(s)", len(guardErrs))
	}

	topoResult, err := topology.Build(topology.BuildConfig{
		ResolveResult:    resolveResult,
		CatalogLookup:    catalogLookup,
		LoadSubAggregate: cat.SubAggregateLoader(),
	})
	if err != nil {
		return fmt.Errorf("Step 2 (topology): %w", err)
	}

	graph, _, err := ir.BuildIR(ir.BuildIRConfig{
		Topology:        topoResult,
		ResolveResult:   resolveResult,
		CatalogLookup:   catalogLookup,
		CompilerVersion: "0.1.0",
	})
	if err != nil {
		return fmt.Errorf("Step 3 (IR): %w", err)
	}

	fmt.Printf("// Release: %s (from %s)\n", rel.Metadata.Name, releasePath)
	for nodeID, featureMap := range graph.Spec.FeatureValuesMapping.All() {
		fmt.Printf("\nComponent: %s\n", nodeID)
		if featureMap.Len() == 0 {
			fmt.Println("  (no Features attached)")
			continue
		}
		fmt.Println("  Features:")
		for instanceName, entry := range featureMap.All() {
			renderFeatureAttachment(instanceName, entry)
		}
	}
	return nil
}

// renderFeatureAttachment writes one Feature attachment to stdout.
// When the attachment's per-leaf provenance is uniform across both
// pillars, the function emits the per-attachment marker (the format
// established before per-leaf provenance shipped) so callers reading
// the common case see one compact line per attachment. When provenance
// diverges across leaves, the function expands into per-leaf lines —
// one bracket per leaf the attachment populates — so the rendered
// output surfaces the divergence that consumers would otherwise miss.
func renderFeatureAttachment(instanceName string, entry types.IRFeatureEntry) {
	if featureProvenanceLayout(entry) {
		fmt.Printf("    - %s    %s\n", featureLabel(instanceName, entry), perAttachmentBracket(entry))
		return
	}
	fmt.Printf("    - %s\n", featureLabel(instanceName, entry))
	for _, leaf := range walkLeavesInOrder(entry) {
		fmt.Printf("        %s: %v    %s\n", leaf.path, leaf.value, perLeafBracket(leaf.provenance))
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

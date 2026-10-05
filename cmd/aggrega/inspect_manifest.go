// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// renderManifestFile renders a Manifest authored as a standalone file. A
// Manifest is a resolved instance kind: its default render applies the extends
// chain (the same composition the build path uses, via
// resolver.ResolveManifestExtends) and lays the resolved mappings out as three
// tables — Component Mappings, Feature Mappings, Annotation Bindings.
//
// The flag matrix carries two distinct useful resolutions:
//
//   - --raw: the file as authored (no extends resolution), via the YAML
//     round-trip that preserves authoring bytes.
//   - --output yaml: the resolved Manifest re-marshalled as YAML (extends
//     applied, then serialized through the typed marshaler).
//   - default / --resolved: the tabular text layout over the resolved Manifest.
//
// --flat is a no-op (the layout is already tabular).
func renderManifestFile(_ context.Context, rw *workspaceconfig.ResolvedWorkspace, path string, data []byte, _ []string, opts inspectOptions) error {
	// --raw is the as-authored bypass: emit the file verbatim, no extends
	// resolution, regardless of output format.
	if opts.mode == modeForcedRaw {
		return emitYAMLRoundTrip(opts, path, data)
	}

	m, err := resolver.LoadManifest(path)
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}
	// Resolve the extends chain through the shared build-path funnel so the
	// inspector's composed mappings cannot drift from the compiler's.
	child := resolver.ResolvedManifest{Manifest: m, Path: path, Bytes: data}
	workspaceRoot := ""
	if rw != nil {
		workspaceRoot = rw.Root()
	}
	resolved, err := resolver.ResolveManifestExtends(workspaceRoot, filepath.Dir(path), child)
	if err != nil {
		return fmt.Errorf("resolve extends: %w", err)
	}

	if opts.output == outputYAML {
		out, err := yaml.Marshal(resolved)
		if err != nil {
			return fmt.Errorf("render resolved Manifest as YAML at %q: %w", path, err)
		}
		fmt.Fprint(opts.out, string(out))
		return nil
	}

	identifier := identifierVersion(resolved.Metadata.Name, resolved.Metadata.Version)
	renderHeader(opts, "Manifest", identifier)

	if chain := manifestExtendsChain(workspaceRoot, filepath.Dir(path), child); chain != "" {
		fmt.Fprintf(opts.out, "Extends: %s\n", chain)
	}

	if opts.level == levelList {
		renderManifestTierKeys(opts, "Component Mappings:", resolved.Spec.Mappings.Components)
		renderManifestTierKeys(opts, "Feature Mappings:", resolved.Spec.Mappings.Features)
		renderManifestAnnotationKeys(opts, resolved.Spec.Mappings.Features)
		return nil
	}

	renderManifestMappingTable(opts, "Component Mappings", "Recipe", resolved.Spec.Mappings.Components)
	renderManifestMappingTable(opts, "Feature Mappings", "FeatureRecipe", resolved.Spec.Mappings.Features)
	renderManifestAnnotationTable(opts, resolved.Spec.Mappings.Features)

	if len(resolved.Defs) > 0 {
		fmt.Fprintln(opts.out, "\nDefs:")
		for _, d := range resolved.Defs {
			fmt.Fprintf(opts.out, "  - %s\n", d)
		}
	}

	return nil
}

// manifestExtendsChain names the Manifests an extends chain folds, child first,
// as the flat display string `A which extends B which extends C`. A child with
// no extends reference yields "".
//
// The chain comes from the resolver's own walk rather than from a second one
// here, so the names displayed are the links that were folded. A walk of its
// own would need its own answer to what makes two links the same — and keying
// that on metadata.name, the only identity a bare Manifest carries, reports a
// loop wherever two files in one chain share a name, which is legal input the
// composition accepts.
//
// A chain the walk refuses yields "" rather than a partial rendering: this is
// decoration on a resolved Manifest, and every path that reaches it has already
// composed the same chain, so a fault here has already been reported as the
// command's own failure.
func manifestExtendsChain(workspaceRoot, releaseDir string, child resolver.ResolvedManifest) string {
	if child.Manifest == nil || child.Manifest.Spec.Extends == "" {
		return ""
	}
	chain, err := resolver.ManifestExtendsChain(workspaceRoot, releaseDir, child)
	if err != nil {
		return ""
	}
	parts := make([]string, len(chain))
	for i, link := range chain {
		parts[i] = link.Manifest.Metadata.Name
	}
	return strings.Join(parts, " which extends ")
}

// renderManifestMappingTable prints one mapping tier as a two-column table:
// the abstract type on the left, the bound Recipe / FeatureRecipe on the right.
// An empty tier prints a `(none)` placeholder under the section header.
func renderManifestMappingTable(opts inspectOptions, section, rhsLabel string, tier *types.MappingTier) {
	fmt.Fprintf(opts.out, "\n%s\n", opts.colorize.header(section+":"))
	if tier == nil || (tier.Type.Len() == 0 && tier.Name.Len() == 0) {
		fmt.Fprintln(opts.out, "  (none)")
		return
	}
	fmt.Fprintf(opts.out, "  %-40s → %s\n", "Type", rhsLabel)
	for k, v := range tier.Type.All() {
		fmt.Fprintf(opts.out, "  %-40s %s\n", k, opts.colorize.uri(v))
	}
	// Name-tier overrides win over type-tier at resolution; show them distinctly
	// so the operator sees the granular bindings the cascade applies last.
	for k, v := range tier.Name.All() {
		fmt.Fprintf(opts.out, "  %-40s %s  (name override)\n", k, opts.colorize.uri(v))
	}
}

// renderManifestAnnotationTable prints the annotation-binding section: each
// annotation key mapped to the Feature type it routes to. The bindings live on
// the Features tier's Annotations sub-map.
func renderManifestAnnotationTable(opts inspectOptions, features *types.MappingTier) {
	fmt.Fprintf(opts.out, "\n%s\n", opts.colorize.header("Annotation Bindings:"))
	if features == nil || features.Annotations == nil || features.Annotations.Len() == 0 {
		fmt.Fprintln(opts.out, "  (none)")
		return
	}
	fmt.Fprintf(opts.out, "  %-40s → %s\n", "Annotation Key", "Feature Type")
	for key, binding := range features.Annotations.All() {
		fmt.Fprintf(opts.out, "  %-40s %s\n", key, opts.colorize.uri(binding.Feature))
	}
}

// renderManifestTierKeys prints the keys of a mapping tier under a label
// (the --list view: identifiers only, no bound targets).
func renderManifestTierKeys(opts inspectOptions, label string, tier *types.MappingTier) {
	fmt.Fprintln(opts.out, label)
	if tier == nil || (tier.Type.Len() == 0 && tier.Name.Len() == 0) {
		fmt.Fprintln(opts.out, "  (none)")
		return
	}
	for k := range tier.Type.All() {
		fmt.Fprintf(opts.out, "  - %s\n", k)
	}
	for k := range tier.Name.All() {
		fmt.Fprintf(opts.out, "  - %s\n", k)
	}
}

// renderManifestAnnotationKeys prints the annotation-binding keys (the --list
// view of the Annotation Bindings section).
func renderManifestAnnotationKeys(opts inspectOptions, features *types.MappingTier) {
	fmt.Fprintln(opts.out, "Annotation Bindings:")
	if features == nil || features.Annotations == nil || features.Annotations.Len() == 0 {
		fmt.Fprintln(opts.out, "  (none)")
		return
	}
	for key := range features.Annotations.All() {
		fmt.Fprintf(opts.out, "  - %s\n", key)
	}
}

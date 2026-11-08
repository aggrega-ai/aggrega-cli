// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// emitYAMLRoundTrip writes the entity as authored — a verbatim YAML node
// round-trip of the loaded bytes. The as-authored kinds (Component, Feature,
// Recipe, FeatureRecipe, Plugin, MappingLibrary, SchemaLibrary) share this
// --output yaml path: the file IS the inspection subject, so re-emitting the
// parsed node preserves authoring bytes rather than normalizing through a typed
// re-marshal (which would reorder keys and drop comments).
func emitYAMLRoundTrip(opts inspectOptions, path string, data []byte) error {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return fmt.Errorf("parse YAML at %q: %w", path, err)
	}
	out, err := yaml.Marshal(&node)
	if err != nil {
		return fmt.Errorf("render YAML at %q: %w", path, err)
	}
	fmt.Fprint(opts.out, string(out))
	return nil
}

// renderHeader writes a styled `Kind: identifier` header line. The colorizer
// styles the whole line as a section header; suffix carries an optional
// parenthetical (source path, mode note) appended uncolored-in-line.
func renderHeader(opts inspectOptions, kind, identifier string) {
	fmt.Fprintln(opts.out, opts.colorize.header(kind+": "+identifier))
}

// renderIdentityBlock writes the header followed by the entity's own
// description, which is omitted when the entity declares none.
//
// It is the one home for the rule that an entity's description is reported
// immediately under its header. Written out at each renderer instead, the rule
// held for some kinds and not others: a reader who saw a description for one
// kind had no way to tell whether another kind carried none or merely reported
// none, and a kind added later inherited whichever of the two its author
// happened to copy.
//
// An entity declaring no description prints no line rather than an empty one,
// because a description that is absent and one that is empty are different
// statements and only the first is ever true here.
func renderIdentityBlock(opts inspectOptions, kind, identifier, description string) {
	renderHeader(opts, kind, identifier)
	if strings.TrimSpace(description) != "" {
		fmt.Fprintf(opts.out, "Description: %s\n", description)
	}
}

// identifierVersion formats a `name:version` identifier, falling back to the
// name alone when no version is authored.
func identifierVersion(name, version string) string {
	if version == "" {
		return name
	}
	return name + ":" + version
}

// renderSchemaBody writes a JSON-Schema body (the Component / Feature schema
// pillar) to opts.out, or a `(no schema defined)` placeholder when the node is
// empty. Single-sourced so the Component and Feature renderers cannot drift on
// how the shared schema surface is printed.
func renderSchemaBody(opts inspectOptions, label string, schema yaml.Node) error {
	if schema.Kind == 0 {
		fmt.Fprintf(opts.out, "%s (none defined)\n", label)
		return nil
	}
	fmt.Fprintf(opts.out, "%s\n", label)
	out, err := yaml.Marshal(&schema)
	if err != nil {
		return fmt.Errorf("render schema: %w", err)
	}
	for _, line := range splitTrimmedLines(string(out)) {
		fmt.Fprintf(opts.out, "  %s\n", line)
	}
	return nil
}

// renderYAMLNodeIndented writes an arbitrary yaml.Node body indented two spaces
// under an already-printed label, used for SchemaLibrary def bodies and template
// bodies at --detail. Empty nodes are skipped silently (the caller prints the
// label).
func renderYAMLNodeIndented(opts inspectOptions, node yaml.Node) error {
	if node.Kind == 0 {
		return nil
	}
	out, err := yaml.Marshal(&node)
	if err != nil {
		return fmt.Errorf("render node: %w", err)
	}
	for _, line := range splitTrimmedLines(string(out)) {
		fmt.Fprintf(opts.out, "    %s\n", line)
	}
	return nil
}

// renderTemplateBodyIndented writes a multi-line template / mapping body
// indented under a label, preserving authored line breaks.
func renderTemplateBodyIndented(opts inspectOptions, body string) {
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		fmt.Fprintf(opts.out, "    %s\n", line)
	}
}

// splitTrimmedLines splits s on newlines after trimming trailing newlines, so an
// indented body does not carry a dangling blank line from the marshaler's
// trailing newline.
func splitTrimmedLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// valuesMappingSummary describes a ValuesMapping in one as-authored line: the
// inline mode, a file `$import`, or a library `$import` (bare-name identity).
func valuesMappingSummary(vm types.ValuesMapping) string {
	switch {
	case vm.IsEmpty():
		return "(none)"
	case vm.Inline != "":
		return "inline template"
	case vm.IsLibraryImport():
		return "$import " + vm.Import + " (library)"
	default:
		return "$import " + vm.Import
	}
}

// importableSummary describes a WorkloadTemplate / ArtifactEntry mode in one
// line: inline body or a file `$import`.
func importableSummary(inline, importTarget string) string {
	switch {
	case inline != "":
		return "inline"
	case importTarget != "":
		return "$import " + importTarget
	default:
		return "(none)"
	}
}

// resolveImplementsComponent loads the catalog and resolves a Recipe's
// spec.implements reference to its bound Component via the shared catalog
// lookup (the three-form version matching used by the build path — no parallel
// resolver). It aborts with a clear error when implements is empty, the catalog
// is unavailable, or the target Component is absent.
func resolveImplementsComponent(rw *workspaceconfig.ResolvedWorkspace, catalogDirs []string, implements string) (types.Component, error) {
	if implements == "" {
		return types.Component{}, fmt.Errorf("--show-implements: recipe declares no implements reference")
	}
	lookup, err := loadCatalogLookup(rw, catalogDirs)
	if err != nil {
		return types.Component{}, fmt.Errorf("--show-implements: %w", err)
	}
	if lookup.Component == nil {
		return types.Component{}, fmt.Errorf("--show-implements: catalog has no Component index")
	}
	comp, found := lookup.Component(implements)
	if !found {
		if pe, malformed := types.ParseVersionPin(implements).Malformed(); malformed {
			return types.Component{}, fmt.Errorf("--show-implements: %w", pe.At("in the recipe's spec.implements"))
		}
		return types.Component{}, fmt.Errorf("--show-implements: implements target %q not found in catalog", implements)
	}
	return comp, nil
}

// resolveImplementsFeature mirrors resolveImplementsComponent for a
// FeatureRecipe's spec.implements reference, resolving the bound Feature via the
// shared catalog lookup.
func resolveImplementsFeature(rw *workspaceconfig.ResolvedWorkspace, catalogDirs []string, implements string) (types.Feature, error) {
	if implements == "" {
		return types.Feature{}, fmt.Errorf("--show-implements: feature recipe declares no implements reference")
	}
	lookup, err := loadCatalogLookup(rw, catalogDirs)
	if err != nil {
		return types.Feature{}, fmt.Errorf("--show-implements: %w", err)
	}
	if lookup.Feature == nil {
		return types.Feature{}, fmt.Errorf("--show-implements: catalog has no Feature index")
	}
	feat, found := lookup.Feature(implements)
	if !found {
		if pe, malformed := types.ParseVersionPin(implements).Malformed(); malformed {
			return types.Feature{}, fmt.Errorf("--show-implements: %w", pe.At("in the feature recipe's spec.implements"))
		}
		return types.Feature{}, fmt.Errorf("--show-implements: implements target %q not found in catalog", implements)
	}
	return feat, nil
}

// loadCatalogLookup loads the catalog over the first-win search stack and
// returns its CatalogLookup. It is the single catalog entry point for the
// --show-implements path; the as-authored file renderers do not load a catalog
// for their default render. Like the catalog-name entity path
// (inspectCatalogEntity), it does no per-node IR rendering, so no
// workspace-relative source-path stamping is needed to resolve an implements
// reference — but it still supplies the resolved workspace root, which anchors
// the configured cache directory so the walk steps over cached entities from
// whichever directory the command is invoked.
func loadCatalogLookup(rw *workspaceconfig.ResolvedWorkspace, catalogDirs []string) (resolver.CatalogLookup, error) {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return resolver.CatalogLookup{}, err
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	// An implements target is looked up for an authored file, not for a Release,
	// so the workspace's own stack is the one read.
	dirs, err := rw.ResolveCatalogDirs("", catalogDirs)
	if err != nil {
		return resolver.CatalogLookup{}, err
	}
	named, err := rw.NamedCatalogDirs("", catalogDirs)
	if err != nil {
		return resolver.CatalogLookup{}, err
	}
	cat, err := catalog.LoadAllWithNamedDirs(wsRoot, cacheDir, named, dirs...)
	if err != nil {
		return resolver.CatalogLookup{}, fmt.Errorf("catalog load: %w", err)
	}
	extErrs, err := resolveWorkspaceExtends(context.Background(), rw, cat, "", dirs, named)
	if err != nil {
		return resolver.CatalogLookup{}, err
	}
	if len(extErrs) > 0 {
		return resolver.CatalogLookup{}, fmt.Errorf("resolve extends: %s", extErrs[0].Error())
	}
	return cat.CatalogLookup(), nil
}

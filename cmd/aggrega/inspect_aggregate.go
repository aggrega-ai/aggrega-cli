// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// renderAggregateFile renders an Aggregate authored as a standalone file. An
// Aggregate is a resolved instance kind: its default render runs the same
// topology pass the build path uses (topology.Build over a minimal ResolveResult
// carrying just the loaded Aggregate), then lays out the flattened nodes, their
// developer values, their uses-edges, and their catalog-declared required
// Features. The resolved uses-graph is also presented target-first as a
// Dependency Graph (which component is used by whom), read straight off the
// topology edge slice.
//
// The compiler deliberately does not topologically sort the uses-graph — cycles
// are legitimate (multi-agent loops) — so this renderer reports the real edges
// and the real node order topology.Build emits; it never synthesizes a linear
// deployment order the system itself rejects.
//
// The flag matrix:
//
//   - --raw: the file as authored (no topology), via the YAML round-trip.
//   - --output yaml: the Aggregate re-emitted as YAML (as-authored; topology is
//     a derived graph, not a serializable Aggregate shape).
//   - default / --resolved: the topology-resolved text layout.
//   - --flat: the subsystem tree is flattened to one line per node, each
//     prefixed with its full dotted InstanceID.
func renderAggregateFile(rw *workspaceconfig.ResolvedWorkspace, path string, data []byte, catalogDirs []string, opts inspectOptions) error {
	if opts.mode == modeForcedRaw || opts.output == outputYAML {
		return emitYAMLRoundTrip(opts, path, data)
	}

	agg, err := resolver.LoadAggregate(path)
	if err != nil {
		return fmt.Errorf("load aggregate: %w", err)
	}

	lookup, subLoader, wsRoot, err := aggregateTopologyInputs(rw, catalogDirs)
	if err != nil {
		return err
	}

	// Compose the existing build-path topology funnel by handing it a minimal
	// ResolveResult carrying just the loaded Aggregate. topology.Build reads only
	// ResolveResult.Aggregate (+ optional BoundaryInfra, absent here) so no
	// Release or Manifest is required; this is composition of the shared funnel,
	// not a reimplementation of flattening or edge normalisation.
	topoResult, err := topology.Build(topology.BuildConfig{
		ResolveResult:     &resolver.ResolveResult{Aggregate: agg},
		CatalogLookup:     lookup,
		LoadSubAggregate:  subLoader,
		RootAggregateFile: catalog.RelativeToWorkspace(wsRoot, path),
	})
	if err != nil {
		return fmt.Errorf("topology: %w", err)
	}

	identifier := identifierVersion(agg.Metadata.Name, agg.Metadata.Version)
	renderHeader(opts, "Aggregate", identifier)

	renderAggregateParameters(opts, agg.Spec.Parameters)

	if opts.level == levelList {
		renderAggregateNodeList(opts, topoResult)
		return nil
	}

	renderAggregateNodes(opts, topoResult, lookup)
	renderAggregateDependencyGraph(opts, topoResult)
	return nil
}

// aggregateTopologyInputs loads the catalog over the first-win search stack and
// returns the CatalogLookup, the sub-aggregate loader callback, and the resolved
// workspace root the topology pass needs. The catalog is loaded strictly
// (inspect fails on a broken catalog) so the resolved node labels and
// required-features lookups are accurate; an unavailable catalog aborts the
// inspection (A5).
func aggregateTopologyInputs(rw *workspaceconfig.ResolvedWorkspace, catalogDirs []string) (resolver.CatalogLookup, topology.SubAggregateLoader, string, error) {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return resolver.CatalogLookup{}, nil, "", fmt.Errorf("workspaceconfig: %w", err)
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	dirs := resolveCatalogDirs(catalogDirs, wsCfg, ".")
	cat, err := catalog.LoadAllWithCacheDir(wsRoot, cacheDir, dirs...)
	if err != nil {
		return resolver.CatalogLookup{}, nil, "", fmt.Errorf("catalog load: %w", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		return resolver.CatalogLookup{}, nil, "", fmt.Errorf("resolve extends: %s", extErrs[0].Error())
	}
	return cat.CatalogLookup(), cat.SubAggregateLoader(), wsRoot, nil
}

// renderAggregateParameters prints the typed parameter-contract declarations the
// Aggregate exposes for environment variation. Each entry is a typed-contract
// object (type, default, description, …); the renderer surfaces the declared
// shape so an operator sees which variables a Release may fulfill. The
// Aggregate's parameters site carries declarations, not fulfillment — the
// $import fulfillment form lives on the Release, not here.
func renderAggregateParameters(opts inspectOptions, params types.OrderedMap[any]) {
	if params.Len() == 0 {
		return
	}
	fmt.Fprintf(opts.out, "\n%s\n", opts.colorize.header("Parameters:"))
	for name, decl := range params.All() {
		fmt.Fprintf(opts.out, "  %s%s\n", name, parameterDeclSummary(decl))
	}
}

// parameterDeclSummary formats the one-line summary of a typed parameter
// declaration: its type and, when declared, its default. A non-object
// declaration (a bare scalar at the parameter site) is summarized as-is.
func parameterDeclSummary(decl any) string {
	m, ok := decl.(*types.OrderedMap[any])
	if !ok {
		return ""
	}
	var parts []string
	if t, ok := m.Get("type"); ok {
		parts = append(parts, fmt.Sprintf("type=%v", t))
	}
	if d, ok := m.Get("default"); ok {
		parts = append(parts, fmt.Sprintf("default=%v", d))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  (" + strings.Join(parts, ", ") + ")"
}

// renderAggregateNodeList prints the --list view: one full dotted InstanceID per
// node, in topology (author) order.
func renderAggregateNodeList(opts inspectOptions, topo *topology.TopologyResult) {
	fmt.Fprintf(opts.out, "\n%s\n", opts.colorize.header("Components:"))
	for id, node := range topo.Nodes.All() {
		fmt.Fprintf(opts.out, "  %s  (%s)\n", id, opts.colorize.uri(node.ComponentType))
	}
}

// renderAggregateNodes prints the per-node block at default / detail level. The
// subsystem tree is rendered as an indented tree by default; --flat collapses it
// to one block per node keyed by the full dotted InstanceID. Each block carries
// the component type, the developer-authored values, the uses-edges grouped by
// socket, and the catalog-declared required Features.
func renderAggregateNodes(opts inspectOptions, topo *topology.TopologyResult, lookup resolver.CatalogLookup) {
	fmt.Fprintf(opts.out, "\n%s\n", opts.colorize.header("Components:"))
	for id, node := range topo.Nodes.All() {
		label := id
		indent := "  "
		if !opts.flat {
			// Tree shape: indent by subsystem depth (dot count in the dotted ID),
			// printing the trailing segment as the local name.
			label, indent = aggregateTreeLabel(id)
		}
		fmt.Fprintf(opts.out, "%s%s  (type: %s)\n", indent, label, opts.colorize.uri(node.ComponentType))
		body := indent + "  "
		renderAggregateNodeValues(opts, body, node.Values)
		renderAggregateNodeUses(opts, body, node.Uses)
		renderAggregateNodeRequiredFeatures(opts, body, node.ComponentType, lookup)
	}
}

// aggregateTreeLabel derives the tree-view local label and indent for a dotted
// InstanceID. The indent grows two spaces per dotted segment so a node nested
// under a subsystem prints beneath it; the label is the trailing segment.
func aggregateTreeLabel(id string) (label, indent string) {
	depth := 0
	last := 0
	for i := 0; i < len(id); i++ {
		if id[i] == '.' {
			depth++
			last = i + 1
		}
	}
	indent = "  "
	for i := 0; i < depth; i++ {
		indent += "  "
	}
	return id[last:], indent
}

// renderAggregateNodeValues prints the developer-authored values block for a
// node, or a `(none)` placeholder. Values are rendered as flat dotted leaves in
// author order.
func renderAggregateNodeValues(opts inspectOptions, indent string, values types.OrderedMap[any]) {
	if values.Len() == 0 {
		fmt.Fprintf(opts.out, "%sValues: (none)\n", indent)
		return
	}
	fmt.Fprintf(opts.out, "%sValues:\n", indent)
	for _, leaf := range types.FlattenLeaves(values) {
		fmt.Fprintf(opts.out, "%s  %s: %v\n", indent, leaf.Path, leaf.Value)
	}
}

// renderAggregateNodeUses prints the node's uses-edges grouped by socket. Both
// authoring forms (string-array, verbose alias-map) are surfaced as alias →
// target lines under each socket; a node with no uses prints `Uses: (none)`.
func renderAggregateNodeUses(opts inspectOptions, indent string, uses types.OrderedMap[types.UsesSocketValue]) {
	if uses.Len() == 0 {
		fmt.Fprintf(opts.out, "%sUses: (none)\n", indent)
		return
	}
	fmt.Fprintf(opts.out, "%sUses:\n", indent)
	for socket, sv := range uses.All() {
		fmt.Fprintf(opts.out, "%s  %s:\n", indent, socket)
		for _, target := range sv.Compact {
			fmt.Fprintf(opts.out, "%s    - %s\n", indent, opts.colorize.uri(target))
		}
		for alias, entry := range sv.Verbose.All() {
			target := entry.Target
			if target == "" {
				target = alias
			}
			fmt.Fprintf(opts.out, "%s    - %s → %s\n", indent, alias, opts.colorize.uri(target))
		}
	}
}

// renderAggregateNodeRequiredFeatures prints the Features the node's Component
// catalog entity declares as required. The list comes from the shared catalog
// lookup (the same index the build path resolves against), so it cannot drift
// from the compiler's view. A Component absent from the catalog, or one
// declaring none, prints nothing.
func renderAggregateNodeRequiredFeatures(opts inspectOptions, indent, componentType string, lookup resolver.CatalogLookup) {
	if lookup.Component == nil {
		return
	}
	comp, found := lookup.Component(componentType)
	if !found || len(comp.Spec.RequiredFeatures) == 0 {
		return
	}
	fmt.Fprintf(opts.out, "%sRequired Features:\n", indent)
	for _, rf := range comp.Spec.RequiredFeatures {
		fmt.Fprintf(opts.out, "%s  - %s\n", indent, rf)
	}
}

// renderAggregateDependencyGraph prints the reverse view of the uses-graph:
// each target node followed by the nodes that use it. It reads the topology
// edge slice directly (no parallel graph walk). The edge slice carries
// compositional author order (Build's contract); targets appear in first-seen
// edge order and per-target consumers in first-seen edge order, both
// de-duplicated — author order is preserved rather than alphabetized, so the
// rendered graph stays byte-identical across runs without faking determinism.
// A graph with no edges prints `(no dependencies)`.
func renderAggregateDependencyGraph(opts inspectOptions, topo *topology.TopologyResult) {
	fmt.Fprintf(opts.out, "\n%s\n", opts.colorize.header("Dependency Graph:"))
	if len(topo.Edges) == 0 {
		fmt.Fprintln(opts.out, "  (no dependencies)")
		return
	}
	order := []string{}
	consumers := map[string][]string{}
	seen := map[string]map[string]bool{}
	for _, e := range topo.Edges {
		if _, ok := seen[e.Target]; !ok {
			seen[e.Target] = map[string]bool{}
			order = append(order, e.Target)
		}
		if !seen[e.Target][e.Source] {
			seen[e.Target][e.Source] = true
			consumers[e.Target] = append(consumers[e.Target], e.Source)
		}
	}
	for _, target := range order {
		fmt.Fprintf(opts.out, "  %s\n", target)
		for _, src := range consumers[target] {
			fmt.Fprintf(opts.out, "    ↑ %s\n", src)
		}
	}
}

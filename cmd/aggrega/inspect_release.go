// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// renderReleaseResolved renders the resolved Release: the IR graph organised by
// boundary. It runs inside the resolve funnel's callback scope (parameters live)
// and is the single layout entry point the funnel's continuation calls. The
// preserved provenance-bracket machinery (renderFeatureAttachment and its
// helpers) is reused verbatim for the per-component Feature block; this function
// adds the boundary organisation, the directory / sync-wave / annotation
// surfaces, the component-type column, and cross-boundary edge marking around it.
//
// It writes the readable layout and nothing else. The format flag selects a
// format for the command's own document and never a different subject, so the
// serialized graph is reached by its own flag and emitted by the one writer
// every command that offers it shares.
//
// The layout withholds the values an author declared sensitive. The subject here
// is the resolved graph — computed from the authored entities rather than
// reproduced from them — so it is a view this command produced for someone to
// read, and the sensitivity declarations that govern it are read off the graph's
// own per-leaf markings rather than derived a second way.
func renderReleaseResolved(opts inspectOptions, rel *types.Release, releasePath string, graph *types.IRGraph, rr *resolver.ResolveResult, lookup resolver.CatalogLookup) error {
	identifier := identifierVersion(rel.Metadata.Name, rel.Metadata.Version)
	renderHeader(opts, "Release", identifier)

	// Group nodes by boundary path in IR node (author) order so the boundary
	// sections and their components both stay author-ordered and deterministic.
	order := []string{}
	byBoundary := map[string][]string{}
	seen := map[string]bool{}
	for nodeID, node := range graph.Spec.Nodes.All() {
		bp := node.BoundaryPath
		if !seen[bp] {
			seen[bp] = true
			order = append(order, bp)
		}
		byBoundary[bp] = append(byBoundary[bp], nodeID)
	}

	// Edge index: source node → its outgoing edges, read straight off the IR
	// edge slice (no parallel graph walk) so cross-boundary marking uses the
	// same edges the compiler emitted.
	outgoing := map[string][]types.IREdge{}
	for _, e := range graph.Spec.Edges {
		outgoing[e.Source] = append(outgoing[e.Source], e)
	}
	nodeBoundary := map[string]string{}
	for nodeID, node := range graph.Spec.Nodes.All() {
		nodeBoundary[nodeID] = node.BoundaryPath
	}

	for _, bp := range order {
		renderReleaseBoundaryHeader(opts, bp, rel, rr)
		if opts.level == levelList {
			for _, nodeID := range byBoundary[bp] {
				fmt.Fprintf(opts.out, "  %s\n", nodeID)
			}
			continue
		}
		for _, nodeID := range byBoundary[bp] {
			renderReleaseComponent(opts, nodeID, bp, graph, outgoing, nodeBoundary, lookup)
		}
	}
	return nil
}

// renderReleaseBoundaryHeader prints one boundary's header: its path, output
// directory, sync-wave, and the two-way annotation split (aggrega.ai/* provider-
// consumed vs. other pass-through). directory / sync-wave / annotations come
// from the authored Release boundary the resolved node belongs to; the
// post-cascade scalar annotation projection comes from the resolver result.
func renderReleaseBoundaryHeader(opts inspectOptions, boundaryPath string, rel *types.Release, rr *resolver.ResolveResult) {
	label := boundaryPath
	if label == "" {
		label = "(default)"
	}
	fmt.Fprintf(opts.out, "\n%s\n", opts.colorize.header("Boundary: "+label))

	b, found := lookupBoundary(rel, boundaryPath)
	if found {
		if b.Directory != "" {
			fmt.Fprintf(opts.out, "  Directory: %s\n", b.Directory)
		}
		if b.SyncWave != 0 {
			fmt.Fprintf(opts.out, "  Sync Wave: %d\n", b.SyncWave)
		}
	}

	ann, ok := rr.BoundaryAnnotations[boundaryPath]
	if !ok || ann.Len() == 0 {
		return
	}
	provider := []string{}
	passthrough := []string{}
	for k, v := range ann.All() {
		line := fmt.Sprintf("%s: %s", k, v)
		if strings.HasPrefix(k, "aggrega.ai/") {
			provider = append(provider, line)
		} else {
			passthrough = append(passthrough, line)
		}
	}
	if len(provider) > 0 {
		fmt.Fprintln(opts.out, "  Annotations (provider-consumed):")
		for _, l := range provider {
			fmt.Fprintf(opts.out, "    %s\n", l)
		}
	}
	if len(passthrough) > 0 {
		fmt.Fprintln(opts.out, "  Annotations (pass-through):")
		for _, l := range passthrough {
			fmt.Fprintf(opts.out, "    %s\n", l)
		}
	}
}

// lookupBoundary walks the authored Release boundary tree to the boundary named
// by a dotted boundary path, returning the Boundary and whether it was found.
// The path segments index nested Boundaries maps; an empty path or a synthesized
// default boundary that has no authored counterpart returns found=false.
func lookupBoundary(rel *types.Release, boundaryPath string) (types.Boundary, bool) {
	if rel == nil || boundaryPath == "" {
		return types.Boundary{}, false
	}
	segments := strings.Split(boundaryPath, ".")
	current := rel.Spec.Boundaries
	var b types.Boundary
	for i, seg := range segments {
		got, ok := current.Get(seg)
		if !ok {
			return types.Boundary{}, false
		}
		b = got
		if i < len(segments)-1 {
			current = got.Boundaries
		}
	}
	return b, true
}

// renderReleaseComponent prints one resolved component under its boundary: its
// node ID, its Aggregate component type (with resolved version), its uses-edges
// (cross-boundary edges marked), and its Feature attachments via the preserved
// provenance-bracket machinery.
func renderReleaseComponent(opts inspectOptions, nodeID, boundaryPath string, graph *types.IRGraph, outgoing map[string][]types.IREdge, nodeBoundary map[string]string, lookup resolver.CatalogLookup) {
	node, _ := graph.Spec.Nodes.Get(nodeID)
	typeLabel := node.Component
	if node.ResolvedVersion != "" && !strings.Contains(typeLabel, ":") {
		typeLabel = typeLabel + ":" + node.ResolvedVersion
	}
	fmt.Fprintf(opts.out, "  Component: %s  (type: %s)\n", nodeID, opts.colorize.uri(typeLabel))

	if edges := outgoing[nodeID]; len(edges) > 0 {
		fmt.Fprintln(opts.out, "    Uses:")
		for _, e := range edges {
			marker := ""
			if nodeBoundary[e.Target] != boundaryPath {
				marker = "  [cross-boundary]"
			}
			fmt.Fprintf(opts.out, "      %s → %s%s\n", e.Alias, opts.colorize.uri(e.Target), marker)
		}
	}

	featureMap, ok := graph.Spec.FeatureValuesMapping.Get(nodeID)
	if !ok || featureMap.Len() == 0 {
		fmt.Fprintln(opts.out, "    Features: (none)")
		return
	}
	fmt.Fprintln(opts.out, "    Features:")
	for instanceName, entry := range featureMap.All() {
		renderFeatureAttachment(opts.out, instanceName, entry, declaredFeatureLeaves(opts, lookup, entry.Type))
	}
}

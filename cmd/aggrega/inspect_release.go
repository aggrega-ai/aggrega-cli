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
//
// realizationOf is the Manifest's FeatureRecipe lookup the graph build rendered
// against — by attachment name, then by Feature type — so the realization the
// layout names for an attachment is the one a build of the same documents uses.
func renderReleaseResolved(opts inspectOptions, rel *types.Release, releasePath string, graph *types.IRGraph, rr *resolver.ResolveResult, lookup resolver.CatalogLookup, realizationOf featureRealization) error {
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
			renderReleaseComponent(opts, nodeID, bp, graph, outgoing, nodeBoundary, lookup, realizationOf)
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

// featureRealization resolves the FeatureRecipe a Manifest binds for one
// attachment, by the attachment's name and the Feature type it names, and
// reports whether it binds one.
type featureRealization func(instanceName, featureType string) (*types.FeatureRecipe, bool)

// connectionKey names one connection of a node by the socket it is wired
// through and the alias it is written under — the pair an edge carries and the
// pair an attachment's connection record carries.
type connectionKey struct {
	socket string
	alias  string
}

// renderReleaseComponent prints one resolved component under its boundary: its
// node ID, its Aggregate component type (with resolved version), its uses-edges
// (cross-boundary edges marked), each edge followed by the attachments that
// connection carries, and the component's own Feature attachments via the
// preserved provenance-bracket machinery.
//
// An attachment a connection carries is printed beneath that connection's edge
// and not among the component's own, joined to an edge the component itself drew
// by the socket and the alias its connection record names — never by its composed
// name, which carries the alias and not the socket. One whose connection matches
// no such edge is printed among the component's own, where its facts clause names
// the connection, so no attachment the graph carries leaves the view.
func renderReleaseComponent(opts inspectOptions, nodeID, boundaryPath string, graph *types.IRGraph, outgoing map[string][]types.IREdge, nodeBoundary map[string]string, lookup resolver.CatalogLookup, realizationOf featureRealization) {
	node, _ := graph.Spec.Nodes.Get(nodeID)
	typeLabel := node.Component
	if node.ResolvedVersion != "" && !strings.Contains(typeLabel, ":") {
		typeLabel = typeLabel + ":" + node.ResolvedVersion
	}
	fmt.Fprintf(opts.out, "  Component: %s  (type: %s)\n", nodeID, opts.colorize.uri(typeLabel))

	featureMap, _ := graph.Spec.FeatureValuesMapping.Get(nodeID)
	carried := map[connectionKey][]string{}
	for instanceName, entry := range featureMap.All() {
		if c := entry.Connection; c != nil {
			k := connectionKey{socket: c.Socket, alias: c.Alias}
			carried[k] = append(carried[k], instanceName)
		}
	}

	shown := map[connectionKey]bool{}
	if edges := outgoing[nodeID]; len(edges) > 0 {
		fmt.Fprintln(opts.out, "    Uses:")
		for _, e := range edges {
			marker := ""
			if nodeBoundary[e.Target] != boundaryPath {
				marker = "  [cross-boundary]"
			}
			fmt.Fprintf(opts.out, "      %s → %s%s\n", e.Alias, opts.colorize.uri(e.Target), marker)
			// An edge an attachment drew is wired through a socket the attached
			// Feature declares, and a connection attachment is written on one of
			// the component's own connections, so only those edges carry one.
			if e.OriginFeature != "" {
				continue
			}
			k := connectionKey{socket: e.Socket, alias: e.Alias}
			if shown[k] {
				continue
			}
			shown[k] = true
			for _, instanceName := range carried[k] {
				entry, _ := featureMap.Get(instanceName)
				renderConnectionAttachment(opts.out, instanceName, entry,
					realizationName(realizationOf, instanceName, entry.Type),
					declaredFeatureLeaves(opts, lookup, entry.Type))
			}
		}
	}

	var own []string
	for instanceName, entry := range featureMap.All() {
		if c := entry.Connection; c != nil && shown[connectionKey{socket: c.Socket, alias: c.Alias}] {
			continue
		}
		own = append(own, instanceName)
	}
	if len(own) == 0 {
		fmt.Fprintln(opts.out, "    Features: (none)")
		return
	}
	fmt.Fprintln(opts.out, "    Features:")
	for _, instanceName := range own {
		entry, _ := featureMap.Get(instanceName)
		renderFeatureAttachment(opts.out, instanceName, entry, declaredFeatureLeaves(opts, lookup, entry.Type))
	}
}

// realizationName names the FeatureRecipe the Manifest binds for one
// attachment as `name:version`, or returns the empty string where the Manifest
// binds none or no lookup was supplied — the realization is then not known, and
// the view says nothing about it rather than naming one.
func realizationName(realizationOf featureRealization, instanceName, featureType string) string {
	if realizationOf == nil {
		return ""
	}
	fr, found := realizationOf(instanceName, featureType)
	if !found || fr == nil {
		return ""
	}
	return identifierVersion(fr.Metadata.Name, fr.Metadata.Version)
}

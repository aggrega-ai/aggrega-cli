// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// assembledGroup is one dispatch group's retained assembled output: the provider
// that rendered it, the disk-write prefix reattached at the final flush, and the
// group-relative files (keyed without the prefix, by the name each provider
// emitted). The post-assembly link pass edits files in place before the flush.
type assembledGroup struct {
	provider string
	prefix   string
	files    map[string][]byte
}

// wireCrossBoundaryLinks wires the graph's cross-boundary uses edges — those
// whose consuming and consumed ends assemble into different dispatch groups —
// into provider-native cross-reference wiring, editing the retained assembled
// artifacts in place. It is the only stage that sees every dispatch group's
// assembled output at once.
//
// Edges are walked in graph (author) order so the pass is deterministic. For
// each edge it resolves both ends' dispatch groups; an edge whose ends land in
// the same group is intra-boundary and skipped. A cross-boundary edge engages
// only when at least one end's provider advertises cross-boundary linking:
//
//   - Both ends the same provider (a shared provider reports one describe(), so
//     both ends advertise linking together): the pass publishes on the consumed
//     end's guest and resolves on the consuming end's guest, applying each side's
//     returned structural patches to that side's assembled files.
//   - Ends rendered by different providers, at least one advertising linking:
//     native wiring between two providers' artifacts is not supported, so the
//     edge keeps its existing scalar value-pipe and the pass surfaces a loud
//     warning (see [ErrCrossProviderLinkUnsupported]).
//   - Neither end's provider advertises linking: inert — no file change, no
//     warning.
//
// The host assembles the domain-blind per-edge payload from the edge, the
// endpoints' dispatch identity, the build's deployment identity, and both ends'
// resolved annotations; it interprets no annotation value
// (Invariants 1/3) and resolves no runtime state (Invariant 2) — the
// deterministic pointer strings a guest returns are piped, not resolved. The
// deployment identity is copied onto both endpoints as an opaque scalar; the
// host infers no meaning from it.
func wireCrossBoundaryLinks(
	ctx context.Context,
	graph *types.IRGraph,
	allProvides NodeProvides,
	nodeDispatch map[string]dispatchKey,
	nodeEngine map[string]Engine,
	nodeAnnotations map[string]map[string]string,
	assembledByDispatch map[dispatchKey]*assembledGroup,
	engineFactory EngineFactory,
	yamlIndent int,
	deploymentID string,
) ([]string, error) {
	var warnings []string
	for _, edge := range graph.Spec.Edges {
		srcKey, srcOK := nodeDispatch[edge.Source]
		tgtKey, tgtOK := nodeDispatch[edge.Target]
		if !srcOK || !tgtOK {
			// An endpoint that did not render has no assembled artifact to wire.
			continue
		}

		srcLinks := linksCrossBoundary(nodeEngine[edge.Source])
		tgtLinks := linksCrossBoundary(nodeEngine[edge.Target])

		switch classifyCrossBoundaryLink(srcKey, tgtKey, srcLinks, tgtLinks) {
		case crossBoundaryLinkInert:
			// Intra-boundary, neither provider links, or the same-provider guard:
			// no file change, no warning — the edge's scalar value-pipe stands.
			continue
		case crossBoundaryLinkCrossProvider:
			// Cross-provider crossing with at least one linking provider: native
			// wiring between two providers' artifacts is unsupported. Leave the
			// scalar value-pipe in place and warn loudly. Switching this to a
			// build halt is a one-line change: return the error rather than
			// appending its message.
			warnings = append(warnings, crossProviderLinkUnsupported(edge, srcKey.Provider, tgtKey.Provider).Error())
			continue
		}
		// crossBoundaryLinkFires: same provider, both ends link.

		srcGrp := assembledByDispatch[srcKey]
		tgtGrp := assembledByDispatch[tgtKey]
		if srcGrp == nil || tgtGrp == nil {
			continue
		}

		linkEdge := buildLinkEdge(edge, srcKey, tgtKey, allProvides, nodeAnnotations[edge.Target], nodeAnnotations[edge.Source], deploymentID)

		// Producer-side: publish on the consumed (target) end's guest, then apply
		// its wiring to the target group's assembled files.
		pubOut, err := nodeEngine[edge.Target].(CrossBoundaryLinker).LinkPublish(ctx, LinkPublishInput{Edge: linkEdge})
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: tgtKey.Path},
				fmt.Sprintf("cross-boundary link publish for uses edge %q -> %q", edge.Source, edge.Target))
		}

		// Consumer-side: resolve on the consuming (source) end's guest against the
		// peer's published handle plus the consuming group's assembled files, then
		// apply its wiring to the source group. The consumer files let the guest
		// locate the host-stamped placeholder and author its replacement at the
		// placeholder's real path.
		resOut, err := nodeEngine[edge.Source].(CrossBoundaryLinker).LinkResolve(ctx, LinkResolveInput{
			Edge:          linkEdge,
			Handle:        pubOut.Handle,
			ConsumerFiles: assembledFilesToRendered(srcGrp),
		})
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: srcKey.Path},
				fmt.Sprintf("cross-boundary link resolve for uses edge %q -> %q", edge.Source, edge.Target))
		}

		tgtWarns, err := applyLinkPatches(tgtGrp, pubOut.Patches, tgtKey.Provider, engineFactory, yamlIndent, edge.Target)
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, tgtWarns...)
		srcWarns, err := applyLinkPatches(srcGrp, resOut.Patches, srcKey.Provider, engineFactory, yamlIndent, edge.Source)
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, srcWarns...)
	}
	return warnings, nil
}

// crossBoundaryLinkClass is the single classification of a uses edge for
// cross-boundary linking, shared by the post-assembly link pass and the
// per-node placeholder stamp so both stages read one decision. Splitting the
// decision across two call sites would let the stamp and the wiring disagree
// about which edges cross a boundary — the placeholder would be planted where
// no resolution follows, or resolution would run where nothing was planted.
type crossBoundaryLinkClass int

const (
	// crossBoundaryLinkInert is an edge that engages no link wiring: its ends
	// share a dispatch group (intra-boundary), neither end's provider advertises
	// linking, or only one end of a same-provider crossing claims linking (a
	// half-claim a shared provider cannot actually produce). The edge keeps its
	// scalar value-pipe unchanged.
	crossBoundaryLinkInert crossBoundaryLinkClass = iota
	// crossBoundaryLinkFires is a cross-boundary edge whose ends are rendered by
	// the same linking provider: the producer publishes and the consumer resolves
	// native cross-reference wiring.
	crossBoundaryLinkFires
	// crossBoundaryLinkCrossProvider is a cross-boundary edge whose ends are
	// rendered by different providers where at least one advertises linking:
	// native wiring between two providers' artifacts is unsupported, so the edge
	// keeps its scalar value-pipe and the pass warns loudly.
	crossBoundaryLinkCrossProvider
)

// classifyCrossBoundaryLink decides how a uses edge participates in
// cross-boundary linking from its two ends' dispatch keys and their providers'
// linking claims. srcLinks and tgtLinks report whether each end's provider
// advertises the cross-boundary-linking capability. Because a shared provider
// reports one capability self-report for both ends, a same-provider crossing
// has srcLinks == tgtLinks; the caller that knows only one end's claim may pass
// it for both, since the cross-provider branch is decided before the guard reads
// the second claim.
func classifyCrossBoundaryLink(srcKey, tgtKey dispatchKey, srcLinks, tgtLinks bool) crossBoundaryLinkClass {
	if srcKey == tgtKey {
		return crossBoundaryLinkInert
	}
	if !srcLinks && !tgtLinks {
		return crossBoundaryLinkInert
	}
	if srcKey.Provider != tgtKey.Provider {
		return crossBoundaryLinkCrossProvider
	}
	if !srcLinks || !tgtLinks {
		return crossBoundaryLinkInert
	}
	return crossBoundaryLinkFires
}

// assembledFilesToRendered projects a dispatch group's assembled files into the
// wire file shape, sorted by path so the projection is byte-identical across
// runs of identical input.
func assembledFilesToRendered(grp *assembledGroup) []RenderedFile {
	names := make([]string, 0, len(grp.files))
	for name := range grp.files {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]RenderedFile, 0, len(names))
	for _, name := range names {
		out = append(out, RenderedFile{Path: name, Content: grp.files[name]})
	}
	return out
}

// scanOrphanCrossBoundaryLinkSentinels scans every assembled file for a
// surviving cross-boundary link placeholder. A placeholder is a build-internal
// marker the host stamps into a consuming node's injected input and a render
// target replaces during link resolution; one that reaches assembled output
// means resolution did not reach it — the injected value was consumed at a
// position the resolver could not address, or the edge was stamped without a
// matching resolve. Shipping a literal placeholder would be a silent defect, so
// the build halts loudly, naming the file. Groups and files are walked in sorted
// order so the halt names the same file across runs of identical input.
func scanOrphanCrossBoundaryLinkSentinels(assembledByDispatch map[dispatchKey]*assembledGroup) error {
	keys := make([]dispatchKey, 0, len(assembledByDispatch))
	for k := range assembledByDispatch {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Path != keys[j].Path {
			return keys[i].Path < keys[j].Path
		}
		return keys[i].Provider < keys[j].Provider
	})
	prefix := []byte(rendercore.CrossBoundaryLinkSentinelPrefix)
	for _, k := range keys {
		grp := assembledByDispatch[k]
		if grp == nil {
			continue
		}
		names := make([]string, 0, len(grp.files))
		for name := range grp.files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if bytes.Contains(grp.files[name], prefix) {
				return cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: name},
					fmt.Sprintf("assembled file %q at dispatch path %q retains an unresolved cross-boundary link placeholder",
						name, k.Path), nil, ErrOrphanCrossBoundaryLinkSentinel)
			}
		}
	}
	return nil
}

// buildLinkEdge assembles the domain-blind per-edge payload for one
// cross-boundary uses edge: both endpoints' identity (node, boundary dispatch
// path, provider), each end's resolved annotations as an opaque scalar map
// (the consumed end's and the consuming end's), the socket, and the consumed
// end's provides plus the consuming end's inject mapping. Edge enrichment is
// single-sourced through [EnrichedEdgeViewFromIREdge]; the host reads the
// projected view, it does not build a second edge-enrichment path.
func buildLinkEdge(edge types.IREdge, srcKey, tgtKey dispatchKey, allProvides NodeProvides, targetAnnotations, sourceAnnotations map[string]string, deploymentID string) LinkEdge {
	view := EnrichedEdgeViewFromIREdge(edge, allProvides)
	return LinkEdge{
		Source:            LinkEndpoint{Node: edge.Source, Boundary: srcKey.Path, Provider: srcKey.Provider, Deployment: deploymentID},
		Target:            LinkEndpoint{Node: edge.Target, Boundary: tgtKey.Path, Provider: tgtKey.Provider, Deployment: deploymentID},
		TargetAnnotations: targetAnnotations,
		SourceAnnotations: sourceAnnotations,
		Socket:            view.Socket,
		Provides:          view.Provides,
		Inject:            view.Inject,
	}
}

// applyLinkPatches applies each returned link patch to the named assembled file
// in grp, through the endpoint provider's codec. A patch naming a file the
// dispatch group did not emit halts loudly rather than silently dropping.
func applyLinkPatches(grp *assembledGroup, patches []LinkPatch, provider string, engineFactory EngineFactory, yamlIndent int, nodeID string) ([]string, error) {
	if len(patches) == 0 {
		return nil, nil
	}
	codec := selectCodec(engineFactory, provider, yamlIndent)
	var warnings []string
	for _, patch := range patches {
		body, ok := grp.files[patch.File]
		if !ok {
			return nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: patch.File},
				fmt.Sprintf("cross-boundary link patch targets file %q the dispatch group did not emit", patch.File), nil, nil)
		}
		patched, warns, err := applyLinkPatch(codec, patch.File, body, patch.Ops, nodeID)
		if err != nil {
			return nil, err
		}
		grp.files[patch.File] = patched
		warnings = append(warnings, warns...)
	}
	return warnings, nil
}

// applyLinkPatch applies a link patch's pre-resolved RFC-6902 ops to one
// assembled file through the codec, returning the byte-faithful reassembled
// file. It carves the file into addressable units, applies the ops through the
// shared (scope, key)-addressed applier — the single home of the opaque/absent
// target checks, so a patch against an opaque (non-structured) unit halts with
// [frp.ErrPipelineTargetOpaque] — and folds the ops back surgically so every
// byte the ops did not address survives verbatim. It does not thread through the
// step-pipeline wrapper: a link patch is a set of pre-resolved ops, not a
// pipeline step. The ops address the file's own root (scope own) by the file's
// segmentation token.
func applyLinkPatch(codec frp.Codec, name string, body []byte, ops []frp.JSONPatchOp, nodeID string) ([]byte, []string, error) {
	pfs, docs, err := buildScopeDocs(codec, []EmittedFile{{Name: name, Body: body}})
	if err != nil {
		return nil, nil, wrapPipelineErr(err, nodeID)
	}
	ds := frp.DocumentSet{Own: docs}
	if err := frp.ApplyPatches(&ds, ops); err != nil {
		// A patch against an opaque unit surfaces frp.ErrPipelineTargetOpaque; an
		// absent target surfaces frp.ErrPipelineTargetAbsent. Both stay matchable
		// via errors.Is through the wrap.
		return nil, nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("cross-boundary link patch of %q", name))
	}
	appliedByToken := map[string][]frp.JSONPatchOp{}
	for _, op := range ops {
		appliedByToken[op.Key] = append(appliedByToken[op.Key], op)
	}
	out, warnings, err := foldPatchesBack(codec, pfs, appliedByToken, nodeID)
	if err != nil {
		return nil, nil, err
	}
	for _, f := range out {
		if f.Name == name {
			return f.Body, warnings, nil
		}
	}
	return body, warnings, nil
}

// crossProviderLinkUnsupported builds the loud, specific diagnostic for a
// cross-boundary uses edge whose ends are rendered by different providers where
// native link wiring is unsupported. It wraps [ErrCrossProviderLinkUnsupported]
// and names both ends, the socket, and both providers, so the operator sees
// exactly which edge fell back to its scalar value-pipe and why. The link pass
// surfaces it as a warning; wrapping it as an error keeps a build-halt one line
// away.
func crossProviderLinkUnsupported(edge types.IREdge, srcProvider, tgtProvider string) error {
	return fmt.Errorf("%w: uses edge %q -> %q on socket %q spans providers %q (source) and %q (target); the edge keeps its scalar value-pipe",
		ErrCrossProviderLinkUnsupported, edge.Source, edge.Target, edge.Socket, srcProvider, tgtProvider)
}

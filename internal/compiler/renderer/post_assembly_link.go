// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"fmt"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// assembledGroup is one dispatch group's retained assembled output: the provider
// that rendered it, the disk-write prefix reattached at the final flush, and the
// group-relative files (keyed without the prefix, by the name each render target
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
// endpoints' dispatch identity, and the consumed end's resolved annotations; it
// interprets no annotation value (Invariants 1/3) and resolves no runtime state
// (Invariant 2) — the deterministic pointer strings a guest returns are piped,
// not resolved.
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
) ([]string, error) {
	var warnings []string
	for _, edge := range graph.Spec.Edges {
		srcKey, srcOK := nodeDispatch[edge.Source]
		tgtKey, tgtOK := nodeDispatch[edge.Target]
		if !srcOK || !tgtOK {
			// An endpoint that did not render has no assembled artifact to wire.
			continue
		}
		if srcKey == tgtKey {
			// Intra-boundary: both ends in one dispatch group, no crossing.
			continue
		}

		srcLinks := linksCrossBoundary(nodeEngine[edge.Source])
		tgtLinks := linksCrossBoundary(nodeEngine[edge.Target])
		if !srcLinks && !tgtLinks {
			// Neither provider advertises linking: inert. No file change, no
			// warning — the edge's scalar value-pipe stands unchanged.
			continue
		}

		if srcKey.Provider != tgtKey.Provider {
			// Cross-provider crossing with at least one linking provider: native
			// wiring between two providers' artifacts is unsupported. Leave the
			// scalar value-pipe in place and warn loudly. Switching this to a
			// build halt is a one-line change: return the error rather than
			// appending its message.
			warnings = append(warnings, crossProviderLinkUnsupported(edge, srcKey.Provider, tgtKey.Provider).Error())
			continue
		}

		if !srcLinks || !tgtLinks {
			// Same provider, so both ends advertise linking together (one
			// describe()); this cannot arise, but guard so a partial claim never
			// produces half-wired output.
			continue
		}

		srcGrp := assembledByDispatch[srcKey]
		tgtGrp := assembledByDispatch[tgtKey]
		if srcGrp == nil || tgtGrp == nil {
			continue
		}

		linkEdge := buildLinkEdge(edge, srcKey, tgtKey, allProvides, nodeAnnotations[edge.Target])

		// Producer-side: publish on the consumed (target) end's guest, then apply
		// its wiring to the target group's assembled files.
		pubOut, err := nodeEngine[edge.Target].(CrossBoundaryLinker).LinkPublish(ctx, LinkPublishInput{Edge: linkEdge})
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: tgtKey.Path},
				fmt.Sprintf("cross-boundary link publish for uses edge %q -> %q", edge.Source, edge.Target))
		}

		// Consumer-side: resolve on the consuming (source) end's guest against the
		// peer's published handle, then apply its wiring to the source group.
		resOut, err := nodeEngine[edge.Source].(CrossBoundaryLinker).LinkResolve(ctx, LinkResolveInput{Edge: linkEdge, Handle: pubOut.Handle})
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: srcKey.Path},
				fmt.Sprintf("cross-boundary link resolve for uses edge %q -> %q", edge.Source, edge.Target))
		}

		if err := applyLinkPatches(tgtGrp, pubOut.Patches, tgtKey.Provider, engineFactory, yamlIndent, edge.Target); err != nil {
			return nil, err
		}
		if err := applyLinkPatches(srcGrp, resOut.Patches, srcKey.Provider, engineFactory, yamlIndent, edge.Source); err != nil {
			return nil, err
		}
	}
	return warnings, nil
}

// buildLinkEdge assembles the domain-blind per-edge payload for one
// cross-boundary uses edge: both endpoints' identity (node, boundary dispatch
// path, render target), the consumed end's resolved annotations as an opaque
// scalar map, the socket, and the consumed end's provides plus the consuming
// end's inject mapping. Edge enrichment is single-sourced through
// [EnrichedEdgeViewFromIREdge]; the host reads the projected view, it does not
// build a second edge-enrichment path.
func buildLinkEdge(edge types.IREdge, srcKey, tgtKey dispatchKey, allProvides NodeProvides, targetAnnotations map[string]string) LinkEdge {
	view := EnrichedEdgeViewFromIREdge(edge, allProvides)
	return LinkEdge{
		Source:            LinkEndpoint{Node: edge.Source, Boundary: srcKey.Path, Provider: srcKey.Provider},
		Target:            LinkEndpoint{Node: edge.Target, Boundary: tgtKey.Path, Provider: tgtKey.Provider},
		TargetAnnotations: targetAnnotations,
		Socket:            view.Socket,
		Provides:          view.Provides,
		Inject:            view.Inject,
	}
}

// applyLinkPatches applies each returned link patch to the named assembled file
// in grp, through the endpoint provider's codec. A patch naming a file the
// dispatch group did not emit halts loudly rather than silently dropping.
func applyLinkPatches(grp *assembledGroup, patches []LinkPatch, provider string, engineFactory EngineFactory, yamlIndent int, nodeID string) error {
	if len(patches) == 0 {
		return nil
	}
	codec := selectCodec(engineFactory, provider, yamlIndent)
	for _, patch := range patches {
		body, ok := grp.files[patch.File]
		if !ok {
			return cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: patch.File},
				fmt.Sprintf("cross-boundary link patch targets file %q the dispatch group did not emit", patch.File), nil, nil)
		}
		patched, err := applyLinkPatch(codec, patch.File, body, patch.Ops, nodeID)
		if err != nil {
			return err
		}
		grp.files[patch.File] = patched
	}
	return nil
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
func applyLinkPatch(codec frp.Codec, name string, body []byte, ops []frp.JSONPatchOp, nodeID string) ([]byte, error) {
	pfs, docs, err := buildScopeDocs(codec, []EmittedFile{{Name: name, Body: body}})
	if err != nil {
		return nil, wrapPipelineErr(err, nodeID)
	}
	ds := frp.DocumentSet{Own: docs}
	if err := frp.ApplyPatches(&ds, ops); err != nil {
		// A patch against an opaque unit surfaces frp.ErrPipelineTargetOpaque; an
		// absent target surfaces frp.ErrPipelineTargetAbsent. Both stay matchable
		// via errors.Is through the wrap.
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("cross-boundary link patch of %q", name))
	}
	appliedByToken := map[string][]frp.JSONPatchOp{}
	for _, op := range ops {
		appliedByToken[op.Key] = append(appliedByToken[op.Key], op)
	}
	out, err := foldPatchesBack(codec, pfs, appliedByToken, nodeID)
	if err != nil {
		return nil, err
	}
	for _, f := range out {
		if f.Name == name {
			return f.Body, nil
		}
	}
	return body, nil
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

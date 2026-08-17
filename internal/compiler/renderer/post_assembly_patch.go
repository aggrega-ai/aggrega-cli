// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"fmt"
	"sort"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// replayDeferredPatches re-runs the pipelines that an assembler node's per-node
// pass deferred, now that the assembler has produced its merged documents. It
// mutates boundaryFiles in place: each deferred record's steps are replayed
// against the assembled documents and the rewritten bytes overwrite the
// corresponding entry, so the final write picks up the patched output.
//
// The replay is scope-symmetric. For an assembler node the merged document is
// both the node's own output AND (for an attached component-scope FeatureRecipe)
// the host surface, so the merged documents are mounted as BOTH the own root and
// the host root and each step's ops are routed by its declared scope inside the
// shared executor. A record carrying own-scope steps harvests the rewritten own
// documents (pres.Own); a record carrying host-scope steps harvests the host
// modifications (pres.HostPatched); a mixed record harvests both. Because the
// documents are discrete now, hostIsAssembler is false and the executor's
// fold-back applies the PATCH byte-faithfully through the assembler provider's
// codec (the stock YAML codec for compose, which has no codec override). There
// is no parallel apply/segment path — re-segmentation, content-selection, apply,
// and byte-faithful reassembly are exactly the machinery the per-node path uses.
//
// Ordering is the A1 deterministic cascade: node author order, then per-node
// FeatureOrder index. Own-scope work sequences before host-scope work on the
// merged document — the component renders, then the FeatureRecipes apply. Within
// one record the executor folds own back before host, so a record's own-scope
// ops land before any host-scope ops it carries; each record's output then
// threads into the next record's input (the running merged documents) so a later
// FeatureRecipe patches on top of an earlier one's changes.
//
// provider is the assembler provider rendering the merged documents (the node's
// own provider — for compose, also the host provider of every attached
// component-scope FeatureRecipe in this dispatch group). It drives codec
// selection and the per-scope compatibility check on replay.
func replayDeferredPatches(
	ctx context.Context,
	boundaryFiles map[string]AssembledFile,
	provider string,
	records []DeferredPatch,
	engineFactory EngineFactory,
	input RendererInput,
) ([]string, error) {
	if len(records) == 0 {
		return nil, nil
	}

	// A1 replay ordering: node author order, then FeatureOrder index. A stable
	// sort over a deterministic input keeps the order byte-identical across runs;
	// iteration over the (already sorted) dispatch keys upstream plus this sort
	// is the only ordering the post-assembly path introduces.
	ordered := append([]DeferredPatch(nil), records...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].HostNodeOrder != ordered[j].HostNodeOrder {
			return ordered[i].HostNodeOrder < ordered[j].HostNodeOrder
		}
		return ordered[i].FeatureOrderIndex < ordered[j].FeatureOrderIndex
	})

	lookup := input.PluginPathLookup
	if lookup == nil {
		// No resolver configured: the deferred steps were validated by their
		// presence but cannot run. Surface the same validate-only notice the
		// per-node path emits rather than silently skipping.
		warnings := make([]string, 0, len(ordered))
		for _, rec := range ordered {
			warnings = append(warnings, fmt.Sprintf("node %q: deferred pipeline (%d steps) validated but not executed (no plugin path resolver configured)", rec.NodeID, len(rec.Steps)))
		}
		return warnings, nil
	}

	codec := selectCodec(engineFactory, provider, input.YAMLIndent)
	invoke := newStepInvoker(lookup)
	// The replay reads the same retained self-reports the per-node passes filled,
	// so a plugin already described during the per-node work is not described
	// again here.
	describe := stepDescriberOver(input.stepDescriptors, lookup)

	var warnings []string
	for _, rec := range ordered {
		// Mount the merged documents as the root(s) this record's pipeline
		// actually targets: the own root for an own pipeline (a component Recipe —
		// OwnProvider set), the host root for a /host pipeline (a component-scope
		// FeatureRecipe — HostProvider set). A record carries exactly one scope's
		// pipeline today (a Recipe own pipeline has no host component; a /host FR
		// pipeline's own output is unassembled and never defers here), so mounting
		// only the targeted root keeps the empty scope genuinely empty — a step
		// that addressed the unmounted scope halts loudly rather than silently
		// patching the wrong root. For an assembler node the own and host roots
		// are the same merged document set, so each scope's write-back keys into
		// boundaryFiles by the same document name. The per-scope provider drives
		// codec selection and the compatibility check; hostIsAssembler is false
		// because assembly produced discrete documents.
		var own, host []EmittedFile
		ownProvider, hostProvider := "", ""
		if rec.OwnProvider != "" {
			own = mergedDocsAsFiles(boundaryFiles)
			ownProvider = rec.OwnProvider
		}
		if rec.HostProvider != "" {
			host = mergedDocsAsFiles(boundaryFiles)
			hostProvider = rec.HostProvider
		}
		pres, err := runNodePipelineWithInvoker(
			ctx, rec.Steps,
			own, host,
			false, // hostIsAssembler: false — assembly produced discrete documents
			ownProvider, hostProvider,
			rec.RenderCtx, rec.WithCtx,
			codec, codec,
			invoke, describe,
			rec.NodeID,
		)
		if err != nil {
			label := rec.FRInstance
			if label == "" {
				label = "recipe"
			}
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: rec.NodeID},
				fmt.Sprintf("post-assembly PATCH replay for %q on node %q", label, rec.NodeID))
		}
		warnings = append(warnings, pres.Warnings...)
		// Thread the patched (or EMITted) documents back so the next record in
		// the cascade sees them, and so the final write picks up the result.
		// Own-scope rewrites surface in pres.Own (keyed by emitted name);
		// host-scope rewrites/EMITs surface in pres.HostPatched (keyed by path).
		// Own folds back before host (own-before-host on the merged document).
		// A byte patch rewrites content but does not change the file's syntax, so
		// the existing declared format is preserved across the write-back. A newly
		// EMITted path absent from boundaryFiles carries the zero-value empty
		// format and falls back to extension selection.
		if rec.OwnProvider != "" {
			for _, f := range pres.Own {
				patched := boundaryFiles[f.Name]
				patched.Content = f.Body
				boundaryFiles[f.Name] = patched
			}
		}
		for path, content := range pres.HostPatched {
			patched := boundaryFiles[path]
			patched.Content = content
			boundaryFiles[path] = patched
		}
	}
	return warnings, nil
}

// mergedDocsAsFiles projects the assembler's merged documents into the
// EmittedFile slice the executor consumes as a scope root. The bytes are the
// reassembly source the codec carves into addressable units; the name is the
// merged document's key (the assembler's output path), which becomes the
// PATCH write-back address. Iterated in sorted key order so the root is
// byte-stable across runs. The same projection serves both the own and host
// roots — for an assembler node they are the same merged document set.
func mergedDocsAsFiles(boundaryFiles map[string]AssembledFile) []EmittedFile {
	names := make([]string, 0, len(boundaryFiles))
	for name := range boundaryFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]EmittedFile, 0, len(names))
	for _, name := range names {
		af := boundaryFiles[name]
		out = append(out, EmittedFile{Name: name, Origin: EmitOriginSource, Body: af.Content, Format: af.Format})
	}
	return out
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// replayDeferredHostPatches re-runs the host-PATCH steps that an assembler
// host's per-node pass deferred, now that the assembler has produced its merged
// documents. It mutates boundaryFiles in place: each deferred record's steps are
// replayed host-scope-only against the assembled documents and the rewritten
// bytes overwrite the corresponding entry, so the final write picks up the
// patched output.
//
// The replay reuses the shared pipeline executor (runNodePipelineWithInvoker)
// with an empty own root and the merged documents as the host root: the merged
// documents ARE discrete now, so hostIsAssembler is false and the executor's
// host fold-back applies the PATCH byte-faithfully through the assembler
// provider's codec (the stock YAML codec for compose, which has no codec
// override). There is no parallel apply/segment path — re-segmentation,
// content-selection, apply, and byte-faithful reassembly are exactly the
// machinery the per-node path uses.
//
// Records replay in the A1 deterministic cascade: host-node author order, then
// per-node FeatureOrder index, so several deferred PATCHes against the same
// assembled documents layer in the same order the per-node host cascade uses.
// Each record's output threads into the next record's input (the running merged
// documents), so a later FeatureRecipe patches on top of an earlier one's
// changes.
//
// provider is the assembler provider rendering the merged documents (the host
// provider for every deferred record in this dispatch group). It drives codec
// selection and the host-scope compatibility check on replay.
func replayDeferredHostPatches(
	ctx context.Context,
	boundaryFiles map[string][]byte,
	provider string,
	records []DeferredHostPatch,
	engineFactory EngineFactory,
	input RendererInput,
) ([]string, error) {
	if len(records) == 0 {
		return nil, nil
	}

	// A1 replay ordering: host-node author order, then FeatureOrder index. A
	// stable sort over a deterministic input keeps the order byte-identical
	// across runs; iteration over the (already sorted) dispatch keys upstream
	// plus this sort is the only ordering the post-assembly path introduces.
	ordered := append([]DeferredHostPatch(nil), records...)
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
			warnings = append(warnings, fmt.Sprintf("node %q: deferred host pipeline (%d steps) validated but not executed (no plugin path resolver configured)", rec.NodeID, len(rec.Steps)))
		}
		return warnings, nil
	}

	codec := selectCodec(engineFactory, provider, input.YAMLIndent)
	invoke := newStepInvoker(lookup)
	describe := newStepDescriber(lookup)

	var warnings []string
	for _, rec := range ordered {
		// C2 guard: a deferred step's export must declare the host scope. The
		// deferral fired because the step PATCHed the host root, and the
		// post-assembly replay re-invokes it host-scope-only (empty own root).
		// If a future descriptor declared a non-host scope on a deferred step,
		// the empty-own replay would mis-route or the own-compat check would
		// fire against the empty own provider — so assert the contract holds
		// (one host-scope export per deferred step) and fail loudly otherwise.
		if err := assertDeferredStepsHostScope(ctx, rec, describe); err != nil {
			return nil, err
		}

		// The merged documents as the host root; empty own root so the replay
		// is host-scope-only — a per-node /own op or EMIT does not re-run here.
		host := mergedDocsAsFiles(boundaryFiles)
		pres, err := runNodePipelineWithInvoker(
			ctx, rec.Steps,
			nil,   // own: empty — host-scope-only replay
			host,  // host: the assembled documents (now discrete)
			false, // hostIsAssembler: false — assembly produced discrete documents
			"",    // ownProvider: no own scope in this replay
			provider,
			rec.RenderCtx, rec.WithCtx,
			codec, codec,
			invoke, describe,
			rec.NodeID,
		)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: rec.NodeID},
				fmt.Sprintf("post-assembly host PATCH replay for feature %q on node %q", rec.FRInstance, rec.NodeID))
		}
		warnings = append(warnings, pres.Warnings...)
		// Thread the patched (or EMITted) documents back so the next record in
		// the cascade sees them, and so the final write picks up the result.
		for path, content := range pres.HostPatched {
			boundaryFiles[path] = content
		}
	}
	return warnings, nil
}

// assertDeferredStepsHostScope enforces the one-host-scope-per-deferred-step
// contract the post-assembly replay relies on. Each step's plugin descriptor
// is read and its named export's declared scope checked: a deferred step that
// does not declare the host scope cannot be faithfully replayed host-scope-only
// and halts loudly rather than being silently mis-routed. This makes a future
// descriptor change that loosens the one-scope-per-export contract trip a build
// error here instead of corrupting the assembled output.
func assertDeferredStepsHostScope(ctx context.Context, rec DeferredHostPatch, describe stepDescriber) error {
	for _, step := range rec.Steps {
		desc, err := describe(ctx, step)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: rec.NodeID},
				fmt.Sprintf("post-assembly host PATCH replay: describe plugin %q for node %q", step.Plugin, rec.NodeID))
		}
		// A step that produced no host op was never deferred; a deferred step's
		// PATCH-bearing export is what tripped the deferral. Check the export
		// the step names: it must declare host scope. An export the descriptor
		// does not declare yields the own-scope default (exportScope), which is
		// not host and is correctly rejected here.
		if scope := exportScope(desc, step.Function); scope != wasm.ScopeHost {
			return cerrors.Wrap(ErrPipelineDeferredScopeNotHost, cerrors.CatalogError, cerrors.SourceLocation{File: rec.NodeID},
				fmt.Sprintf("node %q: %s: plugin %q export %q declares scope %q, not %q (a deferred /host PATCH must be replayed host-scope-only)",
					rec.NodeID, ErrPipelineDeferredScopeNotHost.Error(), step.Plugin, step.Function, scopeOrOwn(scope), wasm.ScopeHost))
		}
	}
	return nil
}

// scopeOrOwn renders an empty declared scope as the own-scope default so the
// diagnostic names a concrete scope rather than an empty string.
func scopeOrOwn(scope string) string {
	if scope == "" {
		return wasm.ScopeOwn
	}
	return scope
}

// mergedDocsAsFiles projects the assembler's merged documents into the
// EmittedFile slice the executor consumes as its host root. The bytes are the
// reassembly source the codec carves into addressable units; the name is the
// merged document's key (the assembler's output path), which becomes the
// host-PATCH write-back address. Iterated in sorted key order so the host root
// is byte-stable across runs.
func mergedDocsAsFiles(boundaryFiles map[string][]byte) []EmittedFile {
	names := make([]string, 0, len(boundaryFiles))
	for name := range boundaryFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]EmittedFile, 0, len(names))
	for _, name := range names {
		out = append(out, EmittedFile{Name: name, Origin: EmitOriginSource, Body: boundaryFiles[name]})
	}
	return out
}

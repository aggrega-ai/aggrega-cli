// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// resolveReleaseClosure loads the Manifest + Aggregate siblings to
// the active Release and computes the catalog closure via the shared
// compiler-operations layer, adapting its stage-tagged failure into the
// daemon's wire envelope. Used by handleIrGraph (existing pipeline path)
// AND by the at-startup + per-irGraph drift checks.
//
// view is the request-scoped catalog lookup the closure walk resolves
// against — seeded with this request's pins, so a cache-resident entity
// resolves against the right pin set and another concurrent request's
// pins can never interleave into this walk. The same view is threaded
// onward into the render pipeline, so closure compute and render see one
// consistent pin snapshot. skipContext reads the view's skip record for
// the same reason.
//
// On error returns a populated *jsonRPCError envelope (1004
// errEntityNotResolvable) so handleIrGraph can forward it via
// writeErrorEnvelope. The drift-check caller treats a non-nil
// envelope as a stderr-logged warning and skips drift verification;
// the next handleIrGraph retries.
//
// The load-stage failures populate Data.File with the Release path being
// resolved; the closure-compute failure stays Data-less (the catalog spans
// multiple source dirs) and is annotated with skipContext so an in-closure
// entity skipped at the tolerant lazy enumeration is explained rather than a
// bare "not found".
//
// When lf is non-nil, the resolved closure is brought onto the same hermetic
// footing the CLI build uses: the cross-document library axes are resolved and
// the sub-aggregate + pipeline-plugin axes ComputeClosure leaves empty are
// reconstructed from the pinned cache, so a drift verify against the lockfile
// compares like-for-like rather than reporting spurious "no longer used"
// entries. Reconstruction is hermetic (the closure Lookup carries a nil
// OCIPuller, so no registry fetch fires) and provider bindings resolve from the
// LOCKFILE, so a plugins.providers change after the pull does not move the
// reconstructed closure. Callers that do not verify drift (the quiet
// validation load) pass nil to skip the reconstruction.
func (s *Server) resolveReleaseClosure(view *catalog.LazyLookup, releasePath string, lf *lockfile.Lockfile) (*compilerops.ResolvedClosure, *jsonRPCError) {
	// Augment stays off: serve folds the library axes and reconstructs the
	// sub-aggregate + plugin axes in reconstructClosureAxes below (only on the
	// drift path, when lf is non-nil), so folding here as well would run the
	// augment twice. The Lockfile gates the external Aggregate seed on a registry
	// pin, matching the CLI build.
	resolved, cerr := compilerops.ResolveClosure(compilerops.ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   s.workspaceRoot,
		WorkspaceConfig: s.wsConfig,
		Lockfile:        lf,
		Augment:         false,
	})
	if cerr != nil {
		if cerr.Stage == compilerops.ClosureStageComputeClosure {
			return nil, &jsonRPCError{
				Code:    errEntityNotResolvable,
				Message: s.skipContext(view, cerr.Error()),
			}
		}
		return nil, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: cerr.Error(),
			Data:    newErrorData(releasePath, 0, 0),
		}
	}
	if lf != nil {
		if rerr := reconstructClosureAxes(view, resolved, lf, s.workspaceRoot, s.wsConfig.ResolvedArtifactCacheDir()); rerr != nil {
			return nil, &jsonRPCError{
				Code:    errEntityNotResolvable,
				Message: s.skipContext(view, rerr.Error()),
			}
		}
	}
	return resolved, nil
}

// reconstructClosureAxes augments the resolved closure's cross-document library
// axes and reconstructs the sub-aggregate + pipeline-plugin axes from the
// lockfile-pinned cache, mirroring the CLI build's hermetic closure so the drift
// verify compares an honest expected set. It runs against the closure Lookup the
// resolution walked (reused, not rebuilt), reading provider bindings from the
// lockfile. Phase B re-folds the view's pin set to the augmented closure so the
// just-pinned plugin entities are visible; the pins are restored to the lockfile
// view afterward so the render path resolves exactly as before.
func reconstructClosureAxes(view *catalog.LazyLookup, resolved *compilerops.ResolvedClosure, lf *lockfile.Lockfile, workspaceRoot, cacheDir string) error {
	lookup := resolved.Lookup
	if err := closure.AugmentClosure(&resolved.Closure, view, lookup); err != nil {
		return err
	}
	aggLoader, _ := lookup.(closure.AggregateLoader)
	bindings := lockfile.ProviderBindings(lf)
	reconResolver := func(ref string) (types.Plugin, bool, error) {
		return view.ProviderPluginForTarget(ref, bindings)
	}
	reconRefold := func() (closure.RecipeLoader, error) {
		view.SetPins(catalog.PinsFromClosure(resolved.Closure))
		return view.ClosureRecipeLoader(), nil
	}
	if err := closure.ReconstructClosureAxes(&resolved.Closure, aggLoader, view.ClosureRecipeLoader(), lookup, reconResolver, reconRefold); err != nil {
		view.SetPins(catalog.PinsFromLockfile(lf))
		return err
	}
	view.SetPins(catalog.PinsFromLockfile(lf))

	// Reconstruct the output-shaping workspace-socket plugin entities and their
	// .wasm from the LOCKFILE (provider bindings + mutator attachments), config-
	// invariant, mirroring the CLI build: these sockets are pull-time choices
	// recorded only in the lockfile, so ComputeClosure cannot re-derive them
	// hermetically. The socket lookup honors the lockfile pins so a pinned socket
	// .wasm resolves from the content-addressed cache offline.
	socketEntityURIs := lockfile.WorkspaceSocketEntityURIs(lf)
	if len(socketEntityURIs) > 0 {
		socketLookup := view.ClosureLookupWithPins(nil, workspaceRoot, cacheDir, nil, catalog.PinsFromLockfile(lf))
		return closure.ReconstructWorkspaceSocketEntities(&resolved.Closure, socketEntityURIs, view.ClosureRecipeLoader(), socketLookup)
	}
	return nil
}

// skipContext annotates a closure-level message with the catalog files the lazy
// enumeration skipped (tolerantly) — so an in-closure entity that went missing
// because it was skipped is explained, not silently absent. Deterministic
// (sorted by path); no-op when nothing was skipped. Mirrors the CLI build's
// withSkipContext, sourced from the supplied request view's skip record — the
// same view whose enumeration produced the unresolved-URI failure, so the skips
// reported are exactly the ones that could explain it (not a session catalog's
// possibly-stale skip set).
//
// Each reason is rendered as its whole cause chain: the outermost frame names
// only which step refused the file, and the frame beneath it is the one naming
// the constraint the file violated.
func (s *Server) skipContext(view *catalog.LazyLookup, msg string) string {
	skips := view.Skips()
	if len(skips) == 0 {
		return msg
	}
	paths := make([]string, 0, len(skips))
	for p := range skips {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	b.WriteString(msg)
	b.WriteString("; note: catalog files skipped at load (one may be the unresolved in-closure entity):")
	for _, p := range paths {
		fmt.Fprintf(&b, " %s: %s;", p, cerrors.CauseChain(skips[p]))
	}
	return b.String()
}

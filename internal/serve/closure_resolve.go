// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
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
func (s *Server) resolveReleaseClosure(view *catalog.LazyLookup, releasePath string) (*compilerops.ResolvedClosure, *jsonRPCError) {
	resolved, cerr := compilerops.ResolveClosure(compilerops.ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   s.workspaceRoot,
		WorkspaceConfig: s.wsConfig,
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
	return resolved, nil
}

// skipContext annotates a closure-level message with the catalog files the lazy
// enumeration skipped (tolerantly) — so an in-closure entity that went missing
// because it was skipped is explained, not silently absent. Deterministic
// (sorted by path); no-op when nothing was skipped. Mirrors the CLI build's
// withSkipContext, sourced from the supplied request view's skip record — the
// same view whose enumeration produced the unresolved-URI failure, so the skips
// reported are exactly the ones that could explain it (not a session catalog's
// possibly-stale skip set).
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
		fmt.Fprintf(&b, " %s: %v;", p, skips[p])
	}
	return b.String()
}

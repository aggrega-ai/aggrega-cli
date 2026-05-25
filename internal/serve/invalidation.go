// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"encoding/json"
)

// invalidateParams is the request shape for aggrega/invalidate.
//
// File names the file the LSP frontend's file-watcher event was
// triggered by. Per-file parse + diagnostic cache entries for this
// file drop; an empty File is acceptable (per-file drops become
// no-ops; the workspace-level cache drop still fires).
//
// Granularity is a forward-compatible field reserved for future
// per-file or per-entity invalidation modes. Recognized values
// (lowercase canonical):
//
//	"" (empty)   → treated as "workspace" (default)
//	"workspace"  → drop the workspace-level resolved-catalog cache
//	               (the only mode implemented at this WI)
//	"file"       → reserved for future per-file granularity; falls
//	               through to workspace at this WI
//	"entity"     → reserved for future per-entity granularity; falls
//	               through to workspace at this WI
//
// Unknown values are accepted without error and fall through to
// workspace-level behavior. The forward-compatibility hatch lets
// future granularity-aware extensions land without breaking IDE
// clients authored against the current shape.
type invalidateParams struct {
	File        string `json:"file"`
	Granularity string `json:"granularity,omitempty"`
}

// invalidateResult is the response shape for aggrega/invalidate.
type invalidateResult struct {
	Invalidated bool `json:"invalidated"`
}

func (s *Server) dispatchInvalidate(req *jsonRPCRequest) error {
	var params invalidateParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	result := s.handleInvalidate(params)
	return s.writeResult(req.ID, result)
}

// handleInvalidate drops cache entries in response to an LSP
// file-watcher event. Behavior is coarse-default workspace-level
// invalidation:
//
//   - The named file's parse-cache + diagnostic-cache entries drop
//     (per-file scope).
//   - The workspace-level resolved-catalog cache drops:
//     recipeCatalog is cleared (re-loads on next aggrega/irGraph);
//     the lazy lookup's hot caches + negative caches + per-kind
//     indexes reset (re-walk on next Component / Feature lookup).
//
// activeRelease + cascadeAttempt are NOT invalidated. The
// disambiguation cascade is session-immutable; workspace-shape
// changes (Release files added or removed) require a daemon
// restart, not an aggrega/invalidate call.
//
// The Granularity field is accepted but ignored; all non-default
// values fall through to workspace-level behavior. See
// invalidateParams.Granularity for the documented reservation.
//
// Returns invalidated: true unconditionally. Back-to-back calls are
// idempotent — sequential dispatch plus per-primitive idempotent
// drops mean a second invalidate finds caches already cleared and
// produces no work-thrashing at the call boundary. Downstream
// operations (next aggrega/irGraph, next Component / Feature lookup)
// pay the re-resolution cost once after the first invalidate; the
// second invalidate has no incremental downstream cost because
// caches stay empty.
//
// Thread-safety: relies on the daemon's sequential dispatch loop.
// Concurrent callers would race on the recipeCatalog assignment +
// the lazy lookup's map re-allocations; the dispatch loop is the
// only intended caller.
func (s *Server) handleInvalidate(params invalidateParams) invalidateResult {
	// Per-file drops (no-op when File is empty or the entry is absent).
	s.parseCache.Drop(params.File)
	delete(s.diagnostics, params.File)

	// Workspace-level drop — applies regardless of params.File and
	// params.Granularity per the coarse-default contract. The IDE is
	// expected to filter event sources before issuing
	// aggrega/invalidate; the daemon does not classify params.File by
	// extension or content type.
	//
	// lazyCatalog is non-nil by construction: Run populates it via
	// catalog.NewLazyLookup before the dispatch loop starts, and the
	// test helper newTestServer mirrors that invariant. A nil-guard
	// here would silently skip the workspace-level reset on a
	// misconfigured Server — a panic surfaces the bug at the right
	// place (test setup) instead of masking it.
	s.recipeCatalog = nil
	s.lazyCatalog.Reset()

	return invalidateResult{Invalidated: true}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
)

// invalidateParams is the request shape for aggrega/invalidate.
//
// File names the file the LSP frontend's file-watcher or close
// event was triggered by. Per-file parse + diagnostic cache entries
// for this file drop on every granularity; an empty File is
// acceptable (the per-file drops become no-ops).
//
// Granularity selects how much state the call evicts. Recognized
// values (lowercase canonical):
//
//	"" (empty)   → treated as "workspace" (default; backward-
//	               compatible with callers that omit the field).
//	"workspace"  → drop the workspace-level resolved-catalog cache
//	               alongside the per-file parse + diagnostics caches.
//	"file"       → drop ONLY the per-file parse + diagnostics caches
//	               for the named file; the workspace-level resolved-
//	               catalog cache is preserved.
//	"entity"     → reserved for future per-entity granularity; falls
//	               through to "workspace" behavior.
//
// Unknown / unrecognized values fall through to "workspace"
// behavior without error. The forward-compatibility hatch lets
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

func (s *Server) dispatchInvalidate(ctx context.Context, req *jsonRPCRequest) error {
	var params invalidateParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
	}
	result := s.handleInvalidate(params)
	return s.writeResult(ctx, req.ID, result)
}

// handleInvalidate drops cache entries in response to an LSP
// file-watcher event or a close handler. The scope is gated by
// params.Granularity per invalidateParams's documented enumeration:
//
//   - Per-file drops fire on every call (regardless of granularity).
//     The named file's parse-cache + diagnostic-cache entries drop.
//     An empty File makes these drops no-ops.
//   - Workspace-level drops fire on every granularity EXCEPT "file".
//     The lazy lookup's hot caches + negative caches + per-kind indexes +
//     enumerated recipe shell + retained entity bytes reset (re-walk on the
//     next Component / Feature / recipe / closure resolution).
//
// activeRelease + cascadeAttempt are NOT invalidated. The
// disambiguation cascade is session-immutable; workspace-shape
// changes (Release files added or removed) require a daemon
// restart, not an aggrega/invalidate call.
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
// Concurrent callers would race on the lazy lookup's map
// re-allocations; the dispatch loop is the only intended caller.
func (s *Server) handleInvalidate(params invalidateParams) invalidateResult {
	// Per-file drops fire for every granularity — a file-scoped
	// invalidate legitimately clears the named file's per-file
	// entries, and a workspace-scoped invalidate is a superset that
	// also clears them. No-op when File is empty or the entry is
	// absent.
	s.parseCache.Drop(params.File)
	s.dropDiagnostics(params.File)

	// Workspace-level drops gated by granularity. Only "file" opts
	// out; "workspace", empty (default), "entity", and any
	// unrecognized value all flow through to preserve the documented
	// forward-compatibility hatch.
	//
	// lazyCatalog is non-nil by construction on this branch: Run
	// populates it via catalog.NewLazyLookup before the dispatch
	// loop starts, and the test helper newTestServer mirrors that
	// invariant. A nil-guard here would silently skip the workspace-
	// level reset on a misconfigured Server — a panic surfaces the
	// bug at the right place (test setup) instead of masking it.
	if params.Granularity != "file" {
		s.lazyCatalog.Reset()
	}

	return invalidateResult{Invalidated: true}
}

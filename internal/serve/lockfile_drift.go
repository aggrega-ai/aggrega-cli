// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"fmt"
	"os"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// loadAndCheckLockfileAtStartup runs the daemon's startup-time
// lockfile + drift flow. Best-effort in two senses:
//
//  1. Catalog-author workflows (no active Release) skip entirely —
//     s.activeRelease == nil short-circuits before any work.
//  2. Workspaces where the lazy closure resolution cannot complete at
//     startup (catalog dirs unavailable, or the hermetic lookup cannot
//     resolve an entity) stderr-log-and-skip; the next handleIrGraph retries.
//
// On success populates s.lockfile and writes any drift findings into
// s.diagnostics[lockfilePath]. This runs from ensureCascade on first
// use, not at construction; it updates the diagnostics cache without
// firing the wire-level aggrega/publishDiagnostics notification (that is
// left to the per-handleIrGraph drift check, which publishes as part of
// serving the IR graph).
func (s *Server) loadAndCheckLockfileAtStartup() {
	if s.activeRelease == nil || s.activeReleasePath == "" {
		return
	}
	lockfilePath := lockfile.PathFor(s.activeReleasePath)
	if _, err := os.Stat(lockfilePath); err != nil {
		// No lockfile present, or stat failure. Either way, leave
		// s.lockfile nil; the per-handleIrGraph absent-gate
		// surfaces the 1003 envelope when the operator invokes
		// irGraph.
		return
	}
	lf, diags, err := lockfile.LoadLockfile(lockfilePath)
	if err != nil {
		fmt.Fprintf(s.stderr, "  lockfile load %s: %v\n", lockfilePath, err)
		return
	}
	for _, d := range diags {
		fmt.Fprintf(s.stderr, "  lockfile: %s\n", d.Message)
	}
	s.lockfile = lf

	// Compute the closure and verify against a request-scoped view seeded with
	// this lockfile's pins (the same per-request resolution shape the handlers
	// use), so the startup closure walk resolves cache-resident entities against
	// the pin set rather than the session catalog's empty one. Best-effort —
	// failure here is logged to stderr and skipped; per-handleIrGraph drift is
	// the reliable path.
	view := s.lazyCatalog.ForRequest(catalog.PinsFromLockfile(lf))
	resolved, envelope := s.resolveReleaseClosure(view, s.activeReleasePath, lf)
	if envelope != nil {
		fmt.Fprintf(s.stderr, "  startup drift check: closure compute failed: %s\n", envelope.Message)
		return
	}
	s.verifyDriftAndUpdateCache(lockfilePath, resolved.Closure)
}

// verifyDriftAndPublish runs the lockfile verify against the
// supplied closure, updates s.diagnostics[lockfilePath]
// accordingly, and fires the aggrega/publishDiagnostics
// notification so the extension's DiagnosticCollection stays in
// sync with the daemon's cache state — including the
// drift-cleared transition (empty diagnostics list).
//
// The lockfile is re-loaded fresh from disk on every call (catches
// the case where the operator re-pulled between handleIrGraph
// calls). Load failures stderr-log, clear any prior drift entry,
// and still fire the publishDiagnostics so the extension drops
// its DiagnosticCollection entry.
//
// Used by handleIrGraph. The at-startup path uses
// verifyDriftAndUpdateCache (no notification) because the
// notification flushes from dispatchInitialize's post-response
// loop per the LSP-canonical ordering.
// Returns the loaded lockfile (nil on load failure) so the caller can thread
// it into the render pipeline for build-time source-artifact resolution.
func (s *Server) verifyDriftAndPublish(ctx context.Context, lockfilePath string, cl closure.Closure) *lockfile.Lockfile {
	lf := s.verifyDriftAndUpdateCache(lockfilePath, cl)
	_ = s.publishDiagnostics(ctx, lockfilePath)
	return lf
}

// verifyDriftAndUpdateCache runs the lockfile verify against the
// supplied closure and updates s.diagnostics[lockfilePath]
// without firing the wire-level publishDiagnostics notification.
// Used by the first-use startup check, which updates the diagnostics
// cache silently; the per-handleIrGraph callers use
// verifyDriftAndPublish, which wraps this and fires the
// notification afterwards.
// Returns the freshly-loaded lockfile (nil on load failure). Drift is
// non-blocking: on drift the lockfile is still returned (it parsed fine), so the
// render pipeline can resolve sources against its pins regardless.
func (s *Server) verifyDriftAndUpdateCache(lockfilePath string, cl closure.Closure) *lockfile.Lockfile {
	lf, _, err := lockfile.LoadLockfile(lockfilePath)
	if err != nil {
		fmt.Fprintf(s.stderr, "  lockfile load %s: %v\n", lockfilePath, err)
		s.dropDiagnostics(lockfilePath)
		return nil
	}
	if err := lockfile.Verify(lf, cl); err != nil {
		message := fmt.Sprintf("Lockfile drifted from current closure. Run `aggrega pull --upgrade --release <release>` to update. Details: %v", err)
		s.setDiagnostics(lockfilePath, []Diagnostic{
			{Line: 1, Column: 1, Severity: "warning", Message: message},
		})
		return lf
	}
	// No drift — clear any prior drift diagnostic.
	s.dropDiagnostics(lockfilePath)
	return lf
}

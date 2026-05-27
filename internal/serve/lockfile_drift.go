// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"os"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// loadAndCheckLockfileAtStartup runs the daemon's startup-time
// lockfile + drift flow. Best-effort in two senses:
//
//  1. Catalog-author workflows (no active Release) skip entirely —
//     s.activeRelease == nil short-circuits before any work.
//  2. Workspaces where s.recipeCatalog cannot be primed at startup
//     (catalog dirs unavailable, hermetic lookup cannot resolve)
//     stderr-log-and-skip; the next handleIrGraph retries.
//
// On success populates s.lockfile and writes any drift findings
// into s.diagnostics[lockfilePath]. The wire-level
// aggrega/publishDiagnostics notification fires later, from
// dispatchInitialize's post-response flush, in LSP-canonical order.
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

	// Compute the closure and verify. Best-effort — failure here
	// is logged to stderr and skipped; per-handleIrGraph drift is
	// the reliable path.
	resolved, envelope := s.resolveReleaseClosure(s.activeReleasePath)
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
func (s *Server) verifyDriftAndPublish(lockfilePath string, cl closure.Closure) {
	s.verifyDriftAndUpdateCache(lockfilePath, cl)
	_ = s.publishDiagnostics(lockfilePath)
}

// verifyDriftAndUpdateCache runs the lockfile verify against the
// supplied closure and updates s.diagnostics[lockfilePath]
// without firing the wire-level publishDiagnostics notification.
// Used by the at-startup path, where the notification is deferred
// to dispatchInitialize's post-response flush per the
// LSP-canonical ordering. Per-handleIrGraph callers use
// verifyDriftAndPublish, which wraps this and fires the
// notification afterwards.
func (s *Server) verifyDriftAndUpdateCache(lockfilePath string, cl closure.Closure) {
	lf, _, err := lockfile.LoadLockfile(lockfilePath)
	if err != nil {
		fmt.Fprintf(s.stderr, "  lockfile load %s: %v\n", lockfilePath, err)
		delete(s.diagnostics, lockfilePath)
		return
	}
	if err := lockfile.Verify(lf, cl); err != nil {
		message := fmt.Sprintf("Lockfile drifted from current closure. Run `aggrega pull --regenerate --release <path>` to update. Details: %v", err)
		s.diagnostics[lockfilePath] = []Diagnostic{
			{Line: 1, Column: 1, Severity: "warning", Message: message},
		}
		return
	}
	// No drift — clear any prior drift diagnostic.
	delete(s.diagnostics, lockfilePath)
}

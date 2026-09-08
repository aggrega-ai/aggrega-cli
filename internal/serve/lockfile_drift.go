// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// The drift check runs per request, against the Release that request
// named, and there is no startup check beside it.
//
// A startup check needs a Release before any client has asked about one,
// and the daemon holds none: a request that acts on a Release carries it.
// Deferring the check to the first Release-bearing request would not
// restore it either — to stay a once-per-session check it would have to
// remember which Releases it had already examined, which is the stored
// selection under another name, and without that memory it simply is the
// per-request check.
//
// Nothing is checked less often as a result. The per-request verify runs
// on every graph request, updates the same diagnostic entry the startup
// pass wrote, and additionally publishes it — and it covers every Release
// a client asks about rather than only the one a workspace-wide walk
// would have picked.

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
// Used by handleIrGraph. The cache update is kept separable from the
// notification so a caller that must not push — a request whose answer
// carries its own findings — can still record what it verified.
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
// verifyDriftAndPublish wraps this and fires the notification afterwards;
// the silent form stays separately reachable so the cache update and the
// push remain independent choices for a caller.
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
			{Line: 1, Column: 1, Severity: editorSeverityWarning, Message: message, ConditionCodes: conditionCodesOf(err)},
		})
		return lf
	}
	// No drift — clear any prior drift diagnostic.
	s.dropDiagnostics(lockfilePath)
	return lf
}

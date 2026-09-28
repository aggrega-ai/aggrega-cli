// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"fmt"
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
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

// requireLockfile is the lockfile-absent gate every request that renders or
// inspects a Release crosses before anything is resolved. It returns nil when a
// lockfile sits at lockfilePath, and otherwise the refusal to answer with:
// errLockfileAbsent naming what to run when there is no file there, and
// errEntityNotResolvable when the path cannot even be examined.
//
// # A request that finds the lockfile gone withdraws the drift finding on it
//
// A drift finding is raised against a lockfile, and it outlives nothing it
// describes: once the file is gone there is no pin set left to have drifted, so
// the finding names a state the reader cannot reach and a command that would
// write a new file rather than repair the old one. It is withdrawn here, at the
// moment a request learns the file is absent, and the withdrawal is published.
//
// Here and not at each surface, because this is the one place absence is
// learned. The request that follows a deletion is refused before it computes
// anything, so nothing downstream of the gate runs and nothing downstream could
// withdraw the finding however it was written. A surface that owes the Problems
// panel nothing still withdraws it: the file is gone whoever asked, and a
// withdrawal cannot contradict a finding another request raised, because that
// finding stood on the file this request has just found missing.
//
// A path that cannot be examined withdraws nothing. The file may be there, and
// a finding raised against it may still be true.
func (s *Server) requireLockfile(ctx context.Context, lockfilePath, release string) *jsonRPCError {
	_, err := os.Stat(lockfilePath)
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) {
		s.verifyDriftAndPublish(ctx, lockfilePath, nil)
		return &jsonRPCError{
			Code:    errLockfileAbsent,
			Message: fmt.Sprintf("lockfile %s not found; run `aggrega pull --release %s` to generate", lockfilePath, release),
			Data:    newLockfileErrorData(lockfilePath, release),
		}
	}
	return &jsonRPCError{
		Code:    errEntityNotResolvable,
		Message: fmt.Sprintf("lockfile stat %s: %v", lockfilePath, err),
		Data:    newLockfileErrorData(lockfilePath, release),
	}
}

// verifyDriftAndPublish settles the drift finding on lockfilePath, updates
// s.diagnostics[lockfilePath] accordingly, and fires the
// aggrega/publishDiagnostics notification so the extension's
// DiagnosticCollection stays in sync with the daemon's cache state — including
// the drift-cleared transition (empty diagnostics list).
//
// It is the one place a drift finding reaches the client, and it settles the
// finding in one of two ways, told apart by cl.
//
// A request that found the lockfile and computed a closure passes that
// closure. The lockfile is re-loaded fresh from disk (catches the case where
// the operator re-pulled between calls) and verified against it. Load failures
// stderr-log, clear any prior drift entry, and still fire the
// publishDiagnostics so the extension drops its DiagnosticCollection entry.
//
// A request that found NO lockfile passes a nil closure, and the finding is
// withdrawn without anything being loaded or verified. Nothing is logged: an
// absent lockfile is an ordinary state the refusal beside it already reports,
// not a fault. A nil closure rather than an empty one is what keeps this path
// honest when a pull writes the file between the request's look and this call:
// verified against an empty closure, the new file would be reported as drifted
// from a resolution that never ran.
//
// Used by handleIrGraph and by the lockfile-absent gate. The cache update is
// kept separable from the notification so a caller that must not push — a
// request whose answer carries its own findings — can still record what it
// verified. Returns the loaded lockfile (nil on load failure, and nil when cl is
// nil) so the caller can thread it into the render pipeline for build-time
// source-artifact resolution.
func (s *Server) verifyDriftAndPublish(ctx context.Context, lockfilePath string, cl *closure.Closure) *lockfile.Lockfile {
	var lf *lockfile.Lockfile
	if cl == nil {
		s.dropDiagnostics(lockfilePath)
	} else {
		lf = s.verifyDriftAndUpdateCache(lockfilePath, *cl)
	}
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
		message := fmt.Sprintf("Lockfile drifted; %s. Details: %v", compilerops.LockfileDriftRemedy("<release>"), err)
		s.setDiagnostics(lockfilePath, []Diagnostic{
			{Line: 1, Column: 1, Severity: editorSeverityWarning, Message: message, ConditionCodes: conditionCodesOf(err)},
		})
		return lf
	}
	// No drift — clear any prior drift diagnostic.
	s.dropDiagnostics(lockfilePath)
	return lf
}

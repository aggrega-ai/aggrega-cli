// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// The daemon's leg of the withdrawal proof, for the two requests that resolve
// dependencies: the resolution request itself, and the build that fills before it
// renders.
//
// A withheld reply looks identical whether the work stopped or ran to completion,
// so every assertion that carries this file is on an OBSERVABLE EFFECT — the
// lockfile's own bytes — and the error envelope is only ever a secondary check,
// raised with t.Error rather than t.Fatal so the byte comparison is always
// reached.
//
// The bytes rather than the file's presence, because this fixture ships a minimal
// lockfile of its own: what a withdrawal has to leave behind is the pin set that
// was already there, unchanged, which is precisely what a byte comparison says and
// what a presence check cannot.
//
// No assertion here carries a timing bound. The context is withdrawn BEFORE the
// request is handled, so where the stop lands is fixed by the code rather than by
// how fast this machine resolves.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// withdrawnResolutionFixture builds the filling fixture, moves into it and returns
// the Release as the requests name it, the lockfile path, and the bytes that file
// carries before anything runs.
func withdrawnResolutionFixture(t *testing.T) (srv *Server, releaseRel, lockPath string, before []byte) {
	t.Helper()
	ws, releaseRel := autoPullWorkspace(t)
	// The harness declares the artifact cache as a RELATIVE path, so the store a
	// resolution writes lands beside the working directory rather than in a
	// machine-wide location shared with everything else on this machine.
	t.Chdir(ws)
	lockPath = lockfile.PathFor(filepath.Join(ws, releaseRel))
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("precondition: the fixture carries no lockfile at %s, so the comparison below has no "+
			"left side: %v", lockPath, err)
	}
	return newFillCapableServer(t, ws), releaseRel, lockPath, before
}

// assertRefusedByTheWithdrawal checks that a request refused, and that what
// refused it is the caller's withdrawal rather than anything else.
//
// It is t.Error throughout and never t.Fatal: the byte comparison at each call
// site is the load-bearing half of its proof, and a fatal here would leave it
// unrun. Attribution is the point — an unchanged lockfile is also what a request
// refused on its params produces, so without this the proof would read green on a
// refusal that never reached the resolution at all.
func assertRefusedByTheWithdrawal(t *testing.T, errEnvelope *jsonRPCError, what string) {
	t.Helper()
	if errEnvelope == nil {
		t.Errorf("a withdrawn %s answered as a completed one, so nothing distinguishes it from one the "+
			"workspace actually ran", what)
		return
	}
	if !errors.Is(errEnvelope.cause, context.Canceled) {
		t.Errorf("the withdrawn %s refused with %q (code %d), which does not carry context.Canceled — so "+
			"it stopped for some reason other than the caller giving up, and the unchanged lockfile below "+
			"says nothing about the withdrawal", what, errEnvelope.Message, errEnvelope.Code)
	}
}

// cancelledContext is a context withdrawn before it is handed over, so the stop is
// observed at the first interruption point the resolution reaches rather than
// wherever a race happens to put it.
func cancelledContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}

// TestAWithdrawnDependencyResolutionLeavesTheLockfileAsItWas is the resolution
// request's own proof: a client that withdraws stops the resolution rather than
// only the answer to it, and the pins already on disk are left standing.
func TestAWithdrawnDependencyResolutionLeavesTheLockfileAsItWas(t *testing.T) {
	t.Run("a withdrawn resolution rewrites nothing", func(t *testing.T) {
		srv, releaseRel, lockPath, before := withdrawnResolutionFixture(t)
		ctx := cancelledContext(t)

		_, errEnvelope := srv.handleResolveDependencies(ctx,
			resolveDependenciesParams{Release: releaseRel}, srv.progressFor(ctx))

		// Non-fatal on purpose: a t.Fatal here would short-circuit the byte
		// comparison below, which is the load-bearing half of this proof.
		//
		// The refusal is checked against the WITHDRAWAL rather than against "any
		// error", because an unchanged lockfile is exactly what a request refused
		// for some unrelated reason also produces — and this file would then read
		// green while nothing it names had happened.
		assertRefusedByTheWithdrawal(t, errEnvelope, "resolution")
		after, err := os.ReadFile(lockPath)
		if err != nil {
			t.Fatalf("read the lockfile after the withdrawn resolution: %v", err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("the withdrawn resolution rewrote %s.\n"+
				"The cancellation did not reach the work, so pins landed on disk after the caller gave up.\n"+
				"before:\n%s\nafter:\n%s", lockPath, before, after)
		}
	})

	t.Run("positive control: a resolution nobody withdrew rewrites it", func(t *testing.T) {
		srv, releaseRel, lockPath, before := withdrawnResolutionFixture(t)

		_, errEnvelope := srv.handleResolveDependencies(t.Context(),
			resolveDependenciesParams{Release: releaseRel}, srv.progressFor(t.Context()))
		if errEnvelope != nil {
			t.Fatalf("the control resolution refused, so the unchanged bytes asserted above prove nothing: "+
				"code %d: %s", errEnvelope.Code, errEnvelope.Message)
		}
		after, err := os.ReadFile(lockPath)
		if err != nil {
			t.Fatalf("read the lockfile after the control resolution: %v", err)
		}
		if bytes.Equal(before, after) {
			t.Fatalf("the control resolution left %s byte-identical, so the comparison above would read "+
				"unchanged under every behaviour", lockPath)
		}
	})
}

// TestAWithdrawnFillingBuildLeavesTheLockfileAsItWas is the build's leg of the
// same property. It is a separate request with its own path into the resolution,
// so a context threaded into one and not the other would pass the test above and
// fail here.
func TestAWithdrawnFillingBuildLeavesTheLockfileAsItWas(t *testing.T) {
	t.Run("a withdrawn filling build fills nothing", func(t *testing.T) {
		srv, releaseRel, lockPath, before := withdrawnResolutionFixture(t)

		_, errEnvelope := srv.handleBuild(cancelledContext(t),
			buildParams{Release: releaseRel, AutoPull: true})

		assertRefusedByTheWithdrawal(t, errEnvelope, "filling build")
		after, err := os.ReadFile(lockPath)
		if err != nil {
			t.Fatalf("read the lockfile after the withdrawn build: %v", err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("the withdrawn build filled %s anyway.\n"+
				"The request's cancellation did not reach the fill, so pins landed on disk after the "+
				"caller gave up.\nbefore:\n%s\nafter:\n%s", lockPath, before, after)
		}
	})

	t.Run("positive control: a filling build nobody withdrew fills it", func(t *testing.T) {
		srv, releaseRel, lockPath, before := withdrawnResolutionFixture(t)

		_, errEnvelope := srv.handleBuild(t.Context(),
			buildParams{Release: releaseRel, AutoPull: true})
		if errEnvelope != nil {
			t.Fatalf("the control build refused, so the unchanged bytes asserted above prove nothing: "+
				"code %d: %s", errEnvelope.Code, errEnvelope.Message)
		}
		after, err := os.ReadFile(lockPath)
		if err != nil {
			t.Fatalf("read the lockfile after the control build: %v", err)
		}
		if bytes.Equal(before, after) {
			t.Fatalf("the control build left %s byte-identical, so the comparison above would read "+
				"unchanged under every behaviour", lockPath)
		}
	})
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// The pre-publish inspect view of a raw artifact loads and runs a guest, and it
// ran that load on a background context. Nothing leaves the process on this path,
// which is why it read as harmless — but a guest load is exactly what an ended
// context unwinds, and the identical operation was already interruptible on every
// other surface that drives it. The command that reaches it holds the run's
// context.
//
// # What this proof covers, and what it does not
//
// It establishes that THE RUN'S CONTEXT IS THE ONE THE GUEST LOAD RECEIVES: a run
// withdrawn before the call describes nothing, while a live control describes the
// same artifact and prints its export contract.
//
// That an ENDED CONTEXT UNWINDS AN ALREADY-RUNNING GUEST is a separate fact,
// proved elsewhere in this tree against a real module whose exported function
// never returns, and cited rather than re-asserted here.

// TestAnInspectWhoseRunHasAlreadyEndedDescribesNoGuest is the load-bearing proof.
//
// The control is what discriminates: the live arm must print the export contract,
// and the withdrawn arm must print the unavailable line instead. A run where both
// printed the same thing would prove nothing.
//
// A planted defect makes this fail: mint a background context at the describe
// instead of taking the caller's, and the withdrawn arm prints the same export
// contract the control does.
func TestAnInspectWhoseRunHasAlreadyEndedDescribesNoGuest(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	var live bytes.Buffer
	if err := inspectRawPlugin(context.Background(), &live, pluginPath); err != nil {
		t.Fatalf("the live control failed (%v), so the withdrawn arm below would pass for the "+
			"wrong reason", err)
	}
	if strings.Contains(live.String(), "Exports: (unavailable") {
		t.Fatalf("the live control could not describe the fixture either, so this proof is not "+
			"measuring cancellation at all:\n%s", live.String())
	}

	withdrawn, stop := context.WithCancel(context.Background())
	stop()

	var stopped bytes.Buffer
	if err := inspectRawPlugin(withdrawn, &stopped, pluginPath); err != nil {
		t.Fatalf("the withdrawn inspect returned an error rather than reporting the contract "+
			"unavailable: %v", err)
	}
	if !strings.Contains(stopped.String(), "Exports: (unavailable") {
		t.Errorf("an inspect whose run had already been withdrawn still described the guest:\n%s\n"+
			"the withdrawal never reached the load", stopped.String())
	}

	// The host-function derivation reads the binary rather than running it, so it
	// is unaffected either way. Asserting it keeps the withdrawn arm from passing
	// because inspect produced nothing at all.
	if !strings.Contains(stopped.String(), "hostFunctions") {
		t.Errorf("the withdrawn inspect printed no host-function section:\n%s\nit was supposed to "+
			"lose only the part that runs the guest", stopped.String())
	}
}

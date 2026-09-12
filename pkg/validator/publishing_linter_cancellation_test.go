// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// What a stop reaches on this path, and what it does not
//
// The linter dispatchers run a Wasm guest rather than a network transfer, so the
// work a stop has to reach is guest execution. Two mechanisms carry it, and they
// are separable:
//
// The host loads every guest with the runtime configured to close a module whose
// call context is done, so a cancelled call context unwinds a guest that is
// ALREADY RUNNING rather than being noticed once it next returns. That is not
// taken on trust here: the loader's own per-call execution ceiling is built on
// exactly this mechanism, and the loader package proves it end to end by driving
// a real wasm module whose exported function is an unconditional infinite loop
// and showing the cancelled call returns. Nothing in this package re-proves it —
// doing so would need a second conformant guest that never returns, and the proof
// would say the same thing the loader's already says.
//
// What IS this package's to prove is that the caller's context is the one that
// machinery receives: that the dispatchers stopped minting a context of their
// own. That is what the tests below drive, against the real publishing-linter
// guest.

// TestALintWhoseContextHasAlreadyEndedLoadsNoGuest proves the caller's context
// governs the dispatch rather than a fresh one minted inside it.
//
// A withdrawn caller must be answered with the withdrawal and no findings, while
// the live-context control over the same guest and the same input must succeed —
// so a run where both rows report the same thing cannot pass, and a dispatcher
// that ignored its context would return the control's findings to both.
//
// Two layers can each report the withdrawal — the entry check, and the load that
// is handed the same context — so neither defect alone falsifies this test, and
// knowing which is which took planting all three combinations:
//
//   - Removing the entry check alone does NOT fail it. The load is handed the
//     caller's context and refuses on it, so the withdrawal is still reported,
//     one step later. That outcome is the measurement that the context reaches
//     the guest machinery rather than stopping at this function's own guard.
//   - Minting a fresh context for the load alone does NOT fail it either, for the
//     mirror-image reason: the entry check answers first and the substitution is
//     never reached.
//   - Doing BOTH fails it: the withdrawn caller loads, runs and receives findings.
//     Only the pair shows that the threading, and not the guard, is what carries
//     the stop to the guest.
//
// The entry check is therefore what spares a caller who has already given up the
// cost of a compilation; it is not what makes the cancellation work.
func TestALintWhoseContextHasAlreadyEndedLoadsNoGuest(t *testing.T) {
	t.Parallel()
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	input := validator.LinterInput{
		RuleID:     "schema-evolution",
		EntityKind: "Component",
		Proposed:   loadFixture(t, "Component", "patch", "proposed.yaml"),
		Previous:   loadFixture(t, "Component", "patch", "previous.yaml"),
	}

	withdrawn, cancel := context.WithCancel(context.Background())
	cancel()

	findings, err := validator.DispatchPublishingLinter(withdrawn, linterSpec(pluginBytes), "", input)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a withdrawn caller got (%d findings, %v); want an error wrapping context.Canceled",
			len(findings), err)
	}
	if findings != nil {
		t.Errorf("a withdrawn caller received %d findings; a lint nobody is waiting for must "+
			"produce none", len(findings))
	}

	// The control is what makes the assertions above mean something: without a row
	// that SUCCEEDS over the same guest and the same input, a dispatcher failing
	// for an unrelated reason would pass this test.
	live, liveErr := validator.DispatchPublishingLinter(context.Background(), linterSpec(pluginBytes), "", input)
	if liveErr != nil {
		t.Fatalf("the live-context control failed (%v); without a control that succeeds, the "+
			"assertions above prove nothing about cancellation", liveErr)
	}
	if len(live) == 0 {
		t.Fatalf("the live-context control produced no findings; it must reach the guest and get " +
			"an answer for the comparison above to mean anything")
	}
}

// TestALintBatchWhoseContextHasAlreadyEndedLoadsNoGuest is the same proof for the
// batch form, which is the one the lint command actually dispatches through and
// the one worth cancelling: it loads the guest once and then evaluates every rule
// for every entity against it, so it is where a caller spends real time.
func TestALintBatchWhoseContextHasAlreadyEndedLoadsNoGuest(t *testing.T) {
	t.Parallel()
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	inputs := []validator.LinterInput{{
		RuleID:     "schema-evolution",
		EntityKind: "Component",
		Proposed:   loadFixture(t, "Component", "patch", "proposed.yaml"),
		Previous:   loadFixture(t, "Component", "patch", "previous.yaml"),
	}}

	withdrawn, cancel := context.WithCancel(context.Background())
	cancel()

	results, err := validator.DispatchPublishingLinterBatch(withdrawn, linterSpec(pluginBytes), "", inputs)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a withdrawn caller got (%d result rows, %v); want an error wrapping context.Canceled",
			len(results), err)
	}
	if results != nil {
		t.Errorf("a withdrawn caller received %d result rows; a batch nobody is waiting for must "+
			"produce none", len(results))
	}

	live, liveErr := validator.DispatchPublishingLinterBatch(context.Background(), linterSpec(pluginBytes), "", inputs)
	if liveErr != nil {
		t.Fatalf("the live-context control failed (%v); without a control that succeeds, the "+
			"assertions above prove nothing about cancellation", liveErr)
	}
	if len(live) != len(inputs) {
		t.Fatalf("the live-context control returned %d result rows for %d inputs; the control must "+
			"complete the batch for the comparison above to mean anything", len(live), len(inputs))
	}
}

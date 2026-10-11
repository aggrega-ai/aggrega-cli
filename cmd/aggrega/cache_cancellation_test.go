// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
)

// The cache reclamation proofs assert an ON-DISK observable, never the returned
// error alone. A withheld reply looks identical whether the operation stopped or
// ran to completion, so an assertion on the error proves only that an error was
// produced — not that the removal it was supposed to prevent was prevented.
// Every row below therefore names a file that must still be there, and pairs it
// with a positive control showing the same operation removes that exact file
// when nobody withdraws. Without the control a "still present" assertion would
// read as a pass over a fixture the operation never reclaims anyway.
//
// Every fixture root is a temporary directory assembled by newCacheFixture, and
// no test here calls cacheops.ResolveLocations. That is load-bearing rather than
// incidental: ResolveLocations fills the machine-wide roots shared by every
// project on the machine, so a prune or a clear driven through it would reclaim
// another workspace's cache. The
// hand-built Locations value confines all four operations to this test's own
// temporary tree.
//
// None of these tests carries a timing bound. The context is cancelled BEFORE
// the call, so where the stop lands is fixed by the code's own interruption
// points rather than by how fast the machine ran.

// stoppedContext returns a context already carrying a withdrawal, so the first
// interruption point an operation reaches observes it. Cancelling ahead of the
// call is what removes the race a mid-flight cancel would introduce.
func stoppedContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}

// TestPruneRemovesNothingWhenTheCallerStops proves the stop reaches the work
// rather than only the reply: the unreferenced elements a completed prune
// reclaims are all still on disk after a withdrawn one.
func TestPruneRemovesNothingWhenTheCallerStops(t *testing.T) {
	t.Run("a withdrawn prune leaves every unreferenced element on disk", func(t *testing.T) {
		fx := newCacheFixture(t)

		res, err := cacheops.Prune(stoppedContext(t), fx.loc)
		// Deliberately t.Error rather than t.Fatal. The on-disk assertions below are
		// the load-bearing half of this proof, and a Fatal here would skip them on
		// exactly the failure they exist to characterise — leaving "the reply was
		// withheld" as the only thing ever measured.
		if err == nil {
			t.Error("a withdrawn prune returned no error, so nothing distinguishes it from one that ran")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the prune refused with %v; want an error carrying context.Canceled, so a caller can tell a withdrawal from a verdict", err)
		}
		if len(res.Removed) != 0 {
			t.Errorf("a withdrawn prune reported %d removal(s): %+v", len(res.Removed), res.Removed)
		}
		for name, p := range map[string]string{
			"unreferenced entity slot":          fx.unrefEntitySlot,
			"unreferenced source-artifact slot": fx.unrefSourceSlot,
			"unreferenced external-input blob":  fx.unrefInputBlob,
			"unreferenced describe entry":       fx.unrefDescribe,
		} {
			if !exists(p) {
				t.Errorf("the withdrawn prune removed the %s anyway: %s\n"+
					"The cancellation did not reach the removal loop, so elements were reclaimed after the caller gave up.", name, p)
			}
		}
	})

	// The control. Without it the assertions above would pass over a fixture
	// prune never reclaims, and the test would prove nothing at all.
	t.Run("control: an uncancelled prune does remove them", func(t *testing.T) {
		fx := newCacheFixture(t)

		res, err := cacheops.Prune(t.Context(), fx.loc)
		if err != nil {
			t.Fatalf("the control prune refused, so it establishes no baseline: %v", err)
		}
		if len(res.Removed) == 0 {
			t.Fatal("the control prune removed nothing, so the fixture holds no reclaimable element and the negative above is vacuous")
		}
		for name, p := range map[string]string{
			"unreferenced entity slot":          fx.unrefEntitySlot,
			"unreferenced source-artifact slot": fx.unrefSourceSlot,
			"unreferenced external-input blob":  fx.unrefInputBlob,
			"unreferenced describe entry":       fx.unrefDescribe,
		} {
			if exists(p) {
				t.Errorf("the control prune left the %s in place, so its survival above is not evidence of a stop: %s", name, p)
			}
		}
		// And the referenced side is retained either way, which is what makes the
		// removals above attributable to the reference set rather than to a wipe.
		if !exists(fx.refEntitySlot) {
			t.Errorf("the control prune removed a REFERENCED entity slot: %s", fx.refEntitySlot)
		}
	})
}

// TestClearRemovesNoSubtreeWhenTheCallerStops proves a withdrawn wipe leaves
// every cache subtree standing. Clear is the widest of these operations, so it
// is the one where a stop that reached only the reply would cost the most.
func TestClearRemovesNoSubtreeWhenTheCallerStops(t *testing.T) {
	t.Run("a withdrawn clear wipes nothing", func(t *testing.T) {
		fx := newCacheFixture(t)

		res, err := cacheops.Clear(stoppedContext(t), fx.loc)
		// t.Error rather than t.Fatal, so the on-disk assertions below are reached.
		if err == nil {
			t.Error("a withdrawn clear returned no error, so nothing distinguishes it from a completed wipe")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the clear refused with %v; want an error carrying context.Canceled", err)
		}
		if len(res.Cleared) != 0 {
			t.Errorf("a withdrawn clear reported %d cleared subtree(s): %+v", len(res.Cleared), res.Cleared)
		}
		// The referenced side is the discriminator: a clear wipes whole subtrees, so
		// an element a prune would have RETAINED is the one whose survival separates
		// "the clear did not run" from "the clear ran and reclaimed only orphans".
		if !exists(fx.refEntitySlot) {
			t.Errorf("the withdrawn clear wiped the referenced entity slot: %s", fx.refEntitySlot)
		}
		if !exists(fx.compileModule) {
			t.Errorf("the withdrawn clear wiped the compiled module: %s", fx.compileModule)
		}
	})

	t.Run("control: an uncancelled clear does wipe them", func(t *testing.T) {
		fx := newCacheFixture(t)

		res, err := cacheops.Clear(t.Context(), fx.loc)
		if err != nil {
			t.Fatalf("the control clear refused, so it establishes no baseline: %v", err)
		}
		if len(res.Cleared) == 0 {
			t.Fatal("the control clear wiped nothing, so the negative above is vacuous")
		}
		if exists(fx.refEntitySlot) {
			t.Errorf("the control clear left the referenced entity slot in place, so its survival above is not evidence of a stop: %s", fx.refEntitySlot)
		}
	})
}

// TestEvictRemovesNoLeafWhenTheCallerStops proves the interruption point sits
// ahead of the reclamation rather than after it. The reclamation reports no
// progress and cannot be interrupted part-way, so before it starts is the only
// place a stop can be observed — and the compiled-module cache it trims is
// shared by every project on the machine, which is what makes that placement
// matter.
func TestEvictRemovesNoLeafWhenTheCallerStops(t *testing.T) {
	// A cap of one byte is below anything the fixture holds, so an eviction that
	// runs must remove rather than report a no-op. A no-op would leave this
	// assertion holding over an empty removal set.
	const capBytes = int64(1)

	t.Run("a withdrawn eviction leaves the compiled module on disk", func(t *testing.T) {
		fx := newCacheFixture(t)

		res, err := cacheops.Evict(stoppedContext(t), fx.loc, capBytes)
		// t.Error rather than t.Fatal, so the on-disk assertion below is reached.
		if err == nil {
			t.Error("a withdrawn eviction returned no error, so nothing distinguishes it from a completed trim")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the eviction refused with %v; want an error carrying context.Canceled", err)
		}
		if len(res.Removed) != 0 {
			t.Errorf("a withdrawn eviction reported %d removal(s): %+v", len(res.Removed), res.Removed)
		}
		if !exists(fx.compileModule) {
			t.Errorf("the withdrawn eviction removed the compiled module anyway: %s\n"+
				"The interruption point is not ahead of the reclamation, so a machine-wide store was trimmed after the caller gave up.",
				fx.compileModule)
		}
	})

	t.Run("control: an uncancelled eviction does remove it", func(t *testing.T) {
		fx := newCacheFixture(t)

		res, err := cacheops.Evict(t.Context(), fx.loc, capBytes)
		if err != nil {
			t.Fatalf("the control eviction refused, so it establishes no baseline: %v", err)
		}
		if res.WithinCapBefore {
			t.Fatalf("a cap of %d byte(s) reported the cache already within it, so the negative above is vacuous", capBytes)
		}
		if len(res.Removed) == 0 {
			t.Fatal("the control eviction removed nothing, so the compiled module's survival above is not evidence of a stop")
		}
		if exists(fx.compileModule) {
			t.Errorf("the control eviction left the compiled module in place: %s", fx.compileModule)
		}
	})
}

// TestStatusEnumeratesNothingWhenTheCallerStops proves the stop reaches the
// enumeration. Status removes nothing, so its observable is the work not done:
// the shared class assembly gathers no class at all, where a completed run
// gathers every one the fixture seeded.
//
// Collect is driven directly alongside Status because it is the function that
// carries the observable — Status folds the classes into totals, and a count of
// zero is also what an empty cache produces. The class set separates the two.
func TestStatusEnumeratesNothingWhenTheCallerStops(t *testing.T) {
	t.Run("a withdrawn enumeration gathers no class", func(t *testing.T) {
		fx := newCacheFixture(t)

		classes, err := cacheops.Collect(stoppedContext(t), fx.loc)
		// t.Error rather than t.Fatal, so the class-set assertion below is reached.
		if err == nil {
			t.Error("a withdrawn enumeration returned no error")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the enumeration refused with %v; want an error carrying context.Canceled", err)
		}
		if len(classes) != 0 {
			t.Errorf("the withdrawn enumeration gathered %d class(es); want 0, since the stop is observed before the first subtree is walked", len(classes))
		}

		// Status, which is what the surfaces call, withholds the report entirely
		// rather than answering with partial totals that would read as a small cache.
		report, statusErr := cacheops.Status(stoppedContext(t), fx.loc)
		// t.Error rather than t.Fatal, so the partial-report assertion below is reached.
		if statusErr == nil {
			t.Error("a withdrawn status returned no error")
		}
		if report.TotalCount != 0 || len(report.Classes) != 0 {
			t.Errorf("a withdrawn status answered with a partial report (%d class(es), %d element(s)); a half-walked inventory understates the cache while reading exactly like a small one",
				len(report.Classes), report.TotalCount)
		}
	})

	t.Run("control: an uncancelled enumeration gathers the fixture's classes", func(t *testing.T) {
		fx := newCacheFixture(t)

		classes, err := cacheops.Collect(t.Context(), fx.loc)
		if err != nil {
			t.Fatalf("the control enumeration refused, so it establishes no baseline: %v", err)
		}
		if len(classes) == 0 {
			t.Fatal("the control enumeration gathered no class, so the zero above is not evidence of a stop")
		}
		report, statusErr := cacheops.Status(t.Context(), fx.loc)
		if statusErr != nil {
			t.Fatalf("the control status refused: %v", statusErr)
		}
		if report.TotalCount == 0 {
			t.Fatal("the control status counted no element, so the withheld report above proves nothing")
		}
	})
}

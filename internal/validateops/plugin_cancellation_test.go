// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Both validation entry points took no context and minted their own, so a
// withdrawal reached neither the guest the plugin scope loads nor the two
// compilation pipeline runs the entity scope drives. Both are invoked from a
// terminal command and from the tool protocol, and both of those hold a
// cancellable context.
//
// # What this proof covers, and what it does not
//
// It establishes that THE CALLER'S CONTEXT IS THE ONE THE MACHINERY RECEIVES:
// a run withdrawn before the call loads no guest and reports the withdrawal,
// while a live control reaches the guest and reports on the plugin instead.
//
// That an ENDED CONTEXT UNWINDS AN ALREADY-RUNNING GUEST is a separate fact and
// it is proved elsewhere in this tree rather than re-asserted here: the loader
// builds every runtime with close-on-context-done, and a test in that package
// drives a real wasm32 module whose exported function is an unconditional
// infinite loop and asserts the cancelled call returns. Re-proving it here would
// need a second conformant guest that never returns, and would say what the
// loader's own proof already says.

// TestAPluginValidationWhoseContextHasAlreadyEndedLoadsNoGuest is the
// load-bearing proof for the plugin scope.
//
// The control is what discriminates, and it has to: a withdrawn run and a live
// run both return an error for a binary-only fixture would prove nothing. So the
// live arm must succeed — it validates a real conformant guest — and the
// withdrawn arm must fail, naming the withdrawal.
//
// A planted defect makes this fail: mint a background context inside the entry
// point instead of taking the caller's, and the withdrawn arm validates exactly
// as the control does.
func TestAPluginValidationWhoseContextHasAlreadyEndedLoadsNoGuest(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	// The declared type is the one the fixture's own describe() reports. Naming a
	// different one would fail both arms at the type cross-check, before the load,
	// and the comparison would measure nothing.
	const declaredType = "pipeline-step"

	// The live control first, so a fixture that does not validate at all is
	// reported as that rather than as a passing cancellation proof.
	if _, err := ValidatePluginBinary(context.Background(), pluginPath, declaredType,
		"", wasm.DescribeMaxMemoryPages); err != nil {
		t.Fatalf("the live control did not validate the fixture (%v), so the withdrawn arm below "+
			"would pass for the wrong reason", err)
	}

	withdrawn, stop := context.WithCancel(context.Background())
	stop()

	_, err := ValidatePluginBinary(withdrawn, pluginPath, declaredType, "", wasm.DescribeMaxMemoryPages)
	if err == nil {
		t.Fatal("a plugin validation whose caller had already withdrawn reported the binary valid; " +
			"the withdrawal never reached the guest load")
	}
	if !mentionsWithdrawal(err) {
		t.Errorf("the withdrawn validation failed with %v; it was supposed to fail ON the "+
			"withdrawal, and failing for some other reason means this proof is not measuring "+
			"cancellation at all", err)
	}
}

// TestAnEntityValidationWhoseContextHasAlreadyEndedRunsNoPipeline is the proof
// for the entity scope's render tier, which drives the real compilation pipeline
// twice.
//
// It drives runRenderTiers directly rather than the exported entry point, and
// that is a deliberate narrowing with a stated reason: the exported form reaches
// this tier only for a Recipe whose implements target resolves out of a REAL
// catalog on disk, and standing one up here would put the proof's weight on
// catalog fixture plumbing rather than on the thing under test. This is the
// function that held the minted context; the hop above it is a single parameter
// the compiler enforces, and `go vet` over every test file is what checks it.
//
// The control is what discriminates. Both arms fail — the synthesized build
// has no provider behind it — so the question is WHICH failure each reports: the
// withdrawn arm must name the withdrawal, and the live arm must not.
//
// A planted defect makes this fail: mint a background context in the render tier
// instead of taking the caller's, and the withdrawn arm reports exactly the
// resolution failure the live arm does.
func TestAnEntityValidationWhoseContextHasAlreadyEndedRunsNoPipeline(t *testing.T) {
	const implementsType = "example/compute/probe"

	lookup := resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == implementsType {
				return types.Component{Metadata: types.ComponentMetadata{Name: typeURI}}, true
			}
			return types.Component{}, false
		},
	}
	comp, _ := lookup.Component(implementsType)
	harness, err := buildEntityHarness(implementsType, "probe-recipe:1.0.0", &comp, lookup)
	if err != nil {
		t.Fatalf("build the harness the render tier runs against: %v", err)
	}
	recipeLookup := renderer.RecipeLookup{
		Recipe: func(string, string) (*types.Recipe, bool) { return &types.Recipe{}, true },
	}

	var withdrawnRes, liveRes EntityResult

	withdrawn, stop := context.WithCancel(context.Background())
	stop()

	// The probe Recipe carries no source, so the render tier is reached rather
	// than declined — which is what makes the withdrawal observable here.
	werr := runRenderTiers(withdrawn, &withdrawnRes, harness, lookup, recipeLookup, nil, false, false)
	lerr := runRenderTiers(context.Background(), &liveRes, harness, lookup, recipeLookup, nil, false, false)

	if werr == nil {
		t.Fatal("the withdrawn render tier succeeded; a run whose caller had already withdrawn " +
			"cannot have completed two pipeline passes")
	}
	if lerr != nil && mentionsWithdrawal(lerr) {
		t.Fatalf("the live control ALSO reported a withdrawal (%v), so the comparison below is "+
			"meaningless: something other than this test's cancellation is ending the run", lerr)
	}
	if !mentionsWithdrawal(werr) {
		t.Errorf("the withdrawn render tier failed with %v; it was supposed to fail ON the "+
			"withdrawal, and a failure for any other reason means the withdrawal reached nothing "+
			"(the live control reported: %v)", werr, lerr)
	}
}

// mentionsWithdrawal reports whether err is, or wraps, a caller's withdrawal —
// matched through the standard sentinels rather than by message text, and with a
// text fallback for the guest runtime, which reports a closed module in its own
// words rather than wrapping the context error.
func mentionsWithdrawal(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "context canceled") || strings.Contains(msg, "module closed")
}

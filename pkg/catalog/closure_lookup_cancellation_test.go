// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// A resolution that accepts a context and never hands it to the fetch underneath
// looks identical, from the outside, to one that does: the caller gets no reply
// either way, and the difference is only whether the fetch is still running. So
// these tests are written against the stack and against the fetch itself rather
// than against the reply.
//
// Two different questions are asked, because neither answers the other:
//
//   - Does stopping the resolution end the WORK? That is the frame comparison
//     below: with one goroutine stopped and one not, a single stack dump shows
//     the stopped one's fetch frame gone and the control's still there.
//   - Does EVERY registry-reaching path carry the caller's context, or only the
//     one the frame comparison happens to drive? That is the per-path table,
//     which reaches each fetch capability in turn and fails a path that was
//     handed a context the caller did not supply.

// parkedControlFetch and parkedStoppedFetch are the same function written twice,
// deliberately. The comparison needs ONE stack dump to distinguish two
// goroutines, and a stack frame is identified by the function it is in, so the
// two callers need two names. This is the one duplicate in this file that must
// not be unified: sharing an implementation would erase the distinction the
// proof rests on.
func parkedControlFetch(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error {
	close(entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
		return errFetchReleased
	}
}

func parkedStoppedFetch(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error {
	close(entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
		return errFetchReleased
	}
}

var errFetchReleased = errors.New("fetch released by the test")

// parkingPuller stands in for a registry transport that stays inside the fetch
// until either the caller's context ends or the test lets it go — the behaviour
// a real transport has, and the reason a stop has to reach the fetch rather than
// the step after it.
type parkingPuller struct {
	park    func(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error
	entered chan struct{}
	release <-chan struct{}
}

func (p *parkingPuller) Pull(ctx context.Context, _, _ string) (*oci.PullResult, error) {
	if err := p.park(ctx, p.entered, p.release); err != nil {
		return nil, err
	}
	return nil, oci.ErrFetchNotFound()
}

func (p *parkingPuller) RegistryHost() string { return "hub.aggrega.ai" }

// goroutineFrames renders every goroutine's stack, growing the buffer until the
// dump fits. A truncated dump would make an absent frame unreadable from a frame
// that simply did not fit, which is the one way this proof could quietly stop
// proving anything.
func goroutineFrames() string {
	for size := 1 << 20; ; size *= 2 {
		buf := make([]byte, size)
		n := runtime.Stack(buf, true)
		if n < size {
			return string(buf[:n])
		}
	}
}

// fetchReturnCeiling bounds "returns" against "hangs forever" and is NOT a
// performance budget: nothing here measures how long a stop takes, only that a
// stopped fetch stops at all. A machine slow enough to exceed this is a machine
// on which the whole suite has already failed.
const fetchReturnCeiling = 30 * time.Second

// TestStoppingTheResolutionEndsTheFetch is the load-bearing proof that a stop
// reaches the work rather than only the reply.
//
// Two resolutions are driven to the same point — inside a registry fetch — and
// one of them is then stopped. A single stack dump, taken while both goroutines
// exist, must show the stopped resolution's fetch frame GONE and the control's
// fetch frame STILL PRESENT. The control is what makes the absence mean
// something: without it, a dump missing both frames would read as success.
//
// A planted defect makes this fail: hand the puller a fresh background context
// at any of the tier-3 call sites instead of the caller's, and the stopped
// goroutine parks exactly as the control does, leaving parkedStoppedFetch on the
// stack and the fetch still running.
func TestStoppingTheResolutionEndsTheFetch(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	// A host-qualified, versioned reference: it misses an empty catalog at
	// Tier-1 and Tier-2 and splits cleanly into an OCI reference plus tag, so
	// the resolution reaches the registry tier.
	const uri = "hub.aggrega.ai/component/core/parked:1.0.0"

	cat, err := catalog.LoadAll("", t.TempDir())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	release := make(chan struct{})
	defer close(release)

	controlPuller := &parkingPuller{park: parkedControlFetch, entered: make(chan struct{}), release: release}
	stoppedPuller := &parkingPuller{park: parkedStoppedFetch, entered: make(chan struct{}), release: release}

	// Temp dirs are taken on the test's own goroutine, not inside the workers.
	controlCache, stoppedCache := t.TempDir(), t.TempDir()
	controlLookup := cat.ClosureLookup(controlPuller, controlCache, cacheDir)
	stoppedLookup := cat.ClosureLookup(stoppedPuller, stoppedCache, cacheDir)

	controlDone := make(chan error, 1)
	go func() {
		_, cerr := controlLookup.ResolveCatalogEntity(context.Background(), uri, "")
		controlDone <- cerr
	}()

	stopCtx, stop := context.WithCancel(context.Background())
	stoppedDone := make(chan error, 1)
	go func() {
		_, serr := stoppedLookup.ResolveCatalogEntity(stopCtx, uri, "")
		stoppedDone <- serr
	}()

	// Both resolutions are now inside the fetch. Until this point there is
	// nothing to compare: a frame that has not been entered is absent for a
	// reason that has nothing to do with cancellation.
	<-controlPuller.entered
	<-stoppedPuller.entered

	stop()

	// Recorded rather than fatal, so the frame comparison below is still reached
	// and a failing run reports BOTH what the caller saw and what the stack says.
	// A fatal here would hide the more informative half.
	select {
	case serr := <-stoppedDone:
		if !errors.Is(serr, context.Canceled) {
			t.Errorf("stopped resolution returned %v; want an error wrapping context.Canceled — "+
				"the fetch returned for some reason other than the withdrawal", serr)
		}
	case <-time.After(fetchReturnCeiling):
		t.Errorf("the stopped resolution never returned within the liveness ceiling: " +
			"the withdrawal did not reach the fetch")
	}

	frames := goroutineFrames()

	// The control assertion is this comparison's needle. It runs the same matcher
	// over the same dump and must find a parked fetch frame, so a failure to find
	// the stopped one below is a fact about the stack rather than about the
	// instrument reading it.
	if !strings.Contains(frames, "parkedControlFetch") {
		t.Fatalf("the control's fetch frame is ABSENT from the dump, so the comparison below " +
			"would pass for the wrong reason; the control must still be inside its fetch")
	}
	if strings.Contains(frames, "parkedStoppedFetch") {
		t.Errorf("the stopped resolution's fetch frame is STILL on the stack while the control's " +
			"is there too: the stop reached the reply but not the work")
	}

	select {
	case cerr := <-controlDone:
		t.Fatalf("the control resolution returned early (%v); it was supposed to stay inside "+
			"its fetch for the whole comparison", cerr)
	default:
	}
}

// stopObservingPuller records which fetch capability a resolution reached and
// reports what the context it was handed says. A path that was given a context
// the caller never supplied reports a nil error here, which is what the table
// below fails on.
type stopObservingPuller struct {
	reached string
	err     error
}

func (p *stopObservingPuller) observe(ctx context.Context, capability string) error {
	p.reached = capability
	p.err = ctx.Err()
	if p.err != nil {
		return p.err
	}
	return oci.ErrFetchNotFound()
}

func (p *stopObservingPuller) Pull(ctx context.Context, _, _ string) (*oci.PullResult, error) {
	return nil, p.observe(ctx, "Pull")
}

func (p *stopObservingPuller) RegistryHost() string { return "hub.aggrega.ai" }

// stopObservingCapablePuller adds the three optional capabilities, so the
// bare-identity tiers — which a Pull-only puller cannot reach — are exercised
// too. The two types exist because a Go value either has a capability or does
// not: one type cannot present both shapes.
type stopObservingCapablePuller struct{ stopObservingPuller }

func (p *stopObservingCapablePuller) PullEntity(ctx context.Context, _, _, _ string) (*oci.PullResult, string, error) {
	return nil, "", p.observe(ctx, "PullEntity")
}

func (p *stopObservingCapablePuller) PullLibrary(ctx context.Context, _, _, _ string) (*oci.PullResult, string, error) {
	return nil, "", p.observe(ctx, "PullLibrary")
}

func (p *stopObservingCapablePuller) PullAggregate(ctx context.Context, _, _ string) (*oci.PullResult, string, error) {
	return nil, "", p.observe(ctx, "PullAggregate")
}

// TestEveryRegistryTierCarriesTheCallersStop reaches each registry-touching path
// of the resolution surface in turn and asserts two things per row: that the
// intended fetch capability was the one reached, and that the context it was
// handed is the caller's. Naming the capability is what keeps a row honest — a
// path that quietly resolved through a different tier would otherwise pass on
// the strength of some other path's plumbing.
//
// A planted defect fails a row: restore a background context at that row's call
// site and the capability reports a nil context error, because a fresh
// background context is never done.
func TestEveryRegistryTierCarriesTheCallersStop(t *testing.T) {
	const cacheDir = ".aggrega/cache"

	type puller interface {
		catalog.OCIPuller
		reachedCapability() (string, error)
	}

	cases := []struct {
		name       string
		capability string
		newPuller  func() puller
		resolve    func(ctx context.Context, lookup closure.Lookup) error
	}{
		{
			name:       "catalog entity through the bare-identity tier",
			capability: "PullEntity",
			newPuller:  func() puller { return &capableProbe{} },
			resolve: func(ctx context.Context, lookup closure.Lookup) error {
				_, err := lookup.ResolveCatalogEntity(ctx, "core/api-server", "component")
				return err
			},
		},
		{
			name:       "catalog entity through the located-reference tier",
			capability: "Pull",
			newPuller:  func() puller { return &plainProbe{} },
			resolve: func(ctx context.Context, lookup closure.Lookup) error {
				_, err := lookup.ResolveCatalogEntity(ctx, "hub.aggrega.ai/component/core/api-server:1.0.0", "")
				return err
			},
		},
		{
			name:       "library through the bare-identity tier",
			capability: "PullLibrary",
			newPuller:  func() puller { return &capableProbe{} },
			resolve: func(ctx context.Context, lookup closure.Lookup) error {
				_, err := lookup.ResolveLibrary(ctx, closure.LibraryAxisNamed("SchemaLibraries"), "core/Probes")
				return err
			},
		},
		{
			name:       "library through the located-reference tier",
			capability: "Pull",
			newPuller:  func() puller { return &plainProbe{} },
			resolve: func(ctx context.Context, lookup closure.Lookup) error {
				_, err := lookup.ResolveLibrary(ctx, closure.LibraryAxisNamed("SchemaLibraries"), "hub.aggrega.ai/schemalibrary/core/Probes:1.0.0")
				return err
			},
		},
		{
			name:       "sub-aggregate through the bare-identity tier",
			capability: "PullAggregate",
			newPuller:  func() puller { return &capableProbe{} },
			resolve: func(ctx context.Context, lookup closure.Lookup) error {
				_, err := lookup.ResolveAggregate(ctx, "core/checkout")
				return err
			},
		},
		{
			name:       "plugin artifact through the versioned manifest",
			capability: "Pull",
			newPuller:  func() puller { return &plainProbe{} },
			resolve: func(ctx context.Context, lookup closure.Lookup) error {
				_, err := lookup.ResolvePlugin(ctx, "render/parked-provider", "1.0.0")
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat, err := catalog.LoadAll("", t.TempDir())
			if err != nil {
				t.Fatalf("LoadAll: %v", err)
			}
			p := tc.newPuller()
			lookup := cat.ClosureLookup(p, t.TempDir(), cacheDir)

			// The caller withdrew before the resolution began. Nothing on these
			// paths checks the context before the fetch, so the fetch itself is
			// where the withdrawal has to be observed — which is the property
			// under test rather than an artefact of the fixture.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			_ = tc.resolve(ctx, lookup)

			reached, ctxErr := p.reachedCapability()
			if reached == "" {
				t.Fatalf("no fetch capability was reached, so this row asserts nothing about %s", tc.capability)
			}
			if reached != tc.capability {
				t.Fatalf("reached %s; want %s — this row is exercising a different tier than it names", reached, tc.capability)
			}
			if !errors.Is(ctxErr, context.Canceled) {
				t.Errorf("%s was handed a context reporting %v; want context.Canceled — "+
					"this tier is fetching on a context the caller never supplied", tc.capability, ctxErr)
			}
		})
	}
}

type plainProbe struct{ stopObservingPuller }

func (p *plainProbe) reachedCapability() (string, error) { return p.reached, p.err }

type capableProbe struct{ stopObservingCapablePuller }

func (p *capableProbe) reachedCapability() (string, error) { return p.reached, p.err }

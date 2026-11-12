// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
)

// The OCI adapter pulls through a registry target whose every method already
// takes a context, so the question here is narrower than for the HTTPS adapter:
// does the context the target is handed belong to the CALLER, or did the adapter
// mint one of its own on the way past? Parking inside the target and watching the
// stack answers it, because a target parked on a context nobody can end is
// indistinguishable from a working one until you look.
//
// parkedControlRegistryPull and parkedStoppedRegistryPull are the same function
// written twice, deliberately: ONE stack dump distinguishes two goroutines only
// if the two frames carry two names. Unifying them would erase the distinction
// the proof rests on.
func parkedControlRegistryPull(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error {
	close(entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
		return errRegistryReleased
	}
}

func parkedStoppedRegistryPull(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error {
	close(entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-release:
		return errRegistryReleased
	}
}

var errRegistryReleased = errors.New("registry target released by the test")

// parkingTarget stands in for a registry that stays inside the pull until either
// the context it was handed ends or the test lets it go — the behaviour a real
// registry transport has over a slow link.
type parkingTarget struct {
	park    func(ctx context.Context, entered chan<- struct{}, release <-chan struct{}) error
	entered chan struct{}
	release <-chan struct{}
}

// Resolve is where the pull parks, because it is the first registry-reaching call
// the pull makes: a target whose Resolve never returns never reaches Fetch.
func (p *parkingTarget) Resolve(ctx context.Context, _ string) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, p.park(ctx, p.entered, p.release)
}

func (p *parkingTarget) Fetch(ctx context.Context, _ ocispec.Descriptor) (io.ReadCloser, error) {
	return nil, p.park(ctx, p.entered, p.release)
}

func (p *parkingTarget) Exists(_ context.Context, _ ocispec.Descriptor) (bool, error) {
	return false, nil
}

var _ oras.ReadOnlyTarget = (*parkingTarget)(nil)

// TestStoppingAnOCIResolutionEndsTheRegistryPull is the load-bearing proof for
// this adapter.
//
// Two pulls are driven to the same point — inside the registry target — and one
// caller is then withdrawn. A single stack dump must show the stopped pull's
// frame GONE and the control's STILL PRESENT. The control is what makes the
// absence mean something: without it a dump missing both frames would read as
// success.
//
// A planted defect makes this fail: mint a fresh background context in Resolve
// instead of using the caller's, and the stopped goroutine parks exactly as the
// control does, leaving parkedStoppedRegistryPull on the stack.
func TestStoppingAnOCIResolutionEndsTheRegistryPull(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	controlTarget := &parkingTarget{
		park: parkedControlRegistryPull, entered: make(chan struct{}), release: release,
	}
	stoppedTarget := &parkingTarget{
		park: parkedStoppedRegistryPull, entered: make(chan struct{}), release: release,
	}

	// Temp dirs are taken on the test's own goroutine, not inside the workers.
	controlRoot, stoppedRoot := t.TempDir(), t.TempDir()

	resolve := func(ctx context.Context, target oras.ReadOnlyTarget, root string) error {
		a := &ociAdapter{
			newTarget: func(string, *http.Client) (oras.ReadOnlyTarget, error) { return target, nil },
		}
		_, err := a.Resolve(ctx, sourceresolver.SourceSpec{
			Kind:          KindOCI,
			URL:           "oci://registry.example.com/charts/mariadb",
			Version:       "1.0.0",
			WorkspaceRoot: root,
			CacheDir:      ".aggrega/cache",
		})
		return err
	}

	controlDone := make(chan error, 1)
	go func() { controlDone <- resolve(context.Background(), controlTarget, controlRoot) }()

	stopCtx, stop := context.WithCancel(context.Background())
	stoppedDone := make(chan error, 1)
	go func() { stoppedDone <- resolve(stopCtx, stoppedTarget, stoppedRoot) }()

	// Both pulls are now inside the registry target. Until this point there is
	// nothing to compare: a frame that has not been entered is absent for a reason
	// that has nothing to do with cancellation.
	<-controlTarget.entered
	<-stoppedTarget.entered

	stop()

	// Recorded rather than fatal, so the frame comparison below is still reached
	// and a failing run reports BOTH what the caller saw and what the stack says.
	select {
	case err := <-stoppedDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the stopped resolution returned %v; want an error wrapping context.Canceled — "+
				"the pull ended for some reason other than the withdrawal", err)
		}
	case <-time.After(fetchReturnCeiling):
		t.Errorf("the stopped resolution never returned within the liveness ceiling: " +
			"the withdrawal did not reach the pull")
	}

	frames := goroutineFrames()

	// This assertion is the comparison's needle. It runs the same matcher over the
	// same dump and must find the control's frame, so a failure to find the
	// stopped one is a fact about the stack rather than about the matcher.
	if !strings.Contains(frames, "parkedControlRegistryPull") {
		t.Fatalf("the control's pull frame is ABSENT from the dump, so the comparison below " +
			"would pass for the wrong reason; the control must still be inside its pull")
	}
	if strings.Contains(frames, "parkedStoppedRegistryPull") {
		t.Errorf("the stopped resolution's pull frame is STILL on the stack while the control's " +
			"is there too: the stop reached the reply but not the work")
	}

	select {
	case err := <-controlDone:
		t.Fatalf("the control resolution returned early (%v); it was supposed to stay inside "+
			"its pull for the whole comparison", err)
	default:
	}
}

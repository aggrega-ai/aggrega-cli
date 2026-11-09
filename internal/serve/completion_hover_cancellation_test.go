// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// The proofs below park a traversal inside a read of a named pipe, so they are
// built only where the standard library can create one.

//go:build linux || darwin || dragonfly || freebsd || netbsd || openbsd

package serve

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The completion and hover projections fall through to a workspace traversal
// that reads every YAML file under the root. Before this change they ran that
// traversal on a background context, and the comment at each site said a
// background context sufficed because the dispatcher's entry check honored
// cancellation. That check is real, and it answers exactly one case: a request
// already withdrawn when it arrived. A withdrawal that lands WHILE the traversal
// is reading a large workspace reached nothing — the traversal ran to the end and
// the reply was thrown away, which is a withheld reply rather than a stop.
//
// These proofs ask the question a timing assertion cannot: is the traversal still
// walking? The discriminator is the FRAME of the goroutine driving it, compared
// against a control driven to the same point and left alone.
//
// parkedControlTraversal and parkedStoppedTraversal are the same function written
// twice, deliberately: ONE stack dump distinguishes two goroutines only if the
// two frames carry two names. Unifying them would erase the distinction the proof
// rests on.

// traversalReturnCeiling bounds "returns" against "hangs forever" and is NOT a
// performance budget: nothing here measures how long a stop takes, only that a
// stopped traversal stops at all. A machine slow enough to exceed this is a
// machine on which the whole suite has already failed.
const traversalReturnCeiling = 30 * time.Second

func parkedControlTraversal(srv *Server, ctx context.Context, done chan<- int) {
	done <- len(srv.enumerateEntityNames(ctx, "Component", srv.workspaceTerms()))
}

func parkedStoppedTraversal(srv *Server, ctx context.Context, done chan<- int) {
	done <- len(srv.enumerateEntityNames(ctx, "Component", srv.workspaceTerms()))
}

func parkedControlHoverTraversal(srv *Server, ctx context.Context, done chan<- bool) {
	doc, _ := srv.findEntityForHover(ctx, "never-present", srv.workspaceTerms())
	done <- doc != nil
}

func parkedStoppedHoverTraversal(srv *Server, ctx context.Context, done chan<- bool) {
	doc, _ := srv.findEntityForHover(ctx, "never-present", srv.workspaceTerms())
	done <- doc != nil
}

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

// parkingWorkspace builds a workspace whose first two YAML files are named pipes.
// A read of a named pipe blocks until a writer opens it, so the traversal parks
// inside a genuine filesystem read rather than inside anything this test
// controls — which is what makes the walk's own cancellation the thing under
// test. Lexical order is what puts the pipes first: the traversal visits
// lexically, so `a-` and `b-` are reached before the ordinary entity below them.
//
// Two pipes rather than one, because the walk observes a withdrawal BETWEEN
// files. One pipe would let a stopped walk and a live walk both return, and the
// comparison would have nothing to separate.
func parkingWorkspace(t *testing.T) (root, firstPipe, secondPipe string) {
	t.Helper()
	root = t.TempDir()
	firstPipe = filepath.Join(root, "a-parks.yaml")
	secondPipe = filepath.Join(root, "b-parks.yaml")
	for _, p := range []string{firstPipe, secondPipe} {
		// Fatal rather than skipped, deliberately. A named pipe is the only thing
		// here that parks a read inside the production walk, so a run without one
		// proves nothing — and a test that reports itself skipped is indistinguishable
		// from one that quietly stopped covering its subject. If this ever fails, the
		// environment is the finding.
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Fatalf("could not create the named pipe this proof parks on (%v); without a read "+
				"that genuinely blocks there is nothing to compare", err)
		}
	}
	body := "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: c-ordinary\n"
	if err := os.WriteFile(filepath.Join(root, "c-ordinary.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write ordinary entity: %v", err)
	}
	return root, firstPipe, secondPipe
}

// awaitParkedRead blocks until the traversal is genuinely inside the read of the
// named pipe, and returns the write end that will release it.
//
// Opening a named pipe for writing blocks until a reader has opened it, so this
// call returning IS the arrival signal. Splitting arrival from release is what
// makes the order below correct rather than racy: a withdrawal issued before the
// walk reached the pipe would end the walk at its first directory entry, the read
// end would never be opened, and this open would block for ever.
func awaitParkedRead(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s for writing: %v", path, err)
	}
	return f
}

// releaseParkedRead completes the parked read with a valid entity and closes the
// write end, so the traversal continues to its next directory entry — which is
// where a withdrawal is observed.
func releaseParkedRead(t *testing.T, f *os.File, name string) {
	t.Helper()
	body := "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: " + name + "\n"
	if _, werr := f.Write([]byte(body)); werr != nil {
		t.Fatalf("write to the parked pipe: %v", werr)
	}
	if cerr := f.Close(); cerr != nil {
		t.Fatalf("close the parked pipe: %v", cerr)
	}
}

// releasePipe is arrival-then-release in one step, for a read this test knows has
// already been reached or is certain to be.
func releasePipe(t *testing.T, path, name string) {
	t.Helper()
	releaseParkedRead(t, awaitParkedRead(t, path), name)
}

// waitForFrameToClear polls the stack until the named frame is gone or the
// liveness ceiling expires. It does NOT assert: the comparison below it does
// that, on one dump, with the control alongside. This only removes the race
// between a walk returning and this goroutine looking, so a failure reported
// there is a real one rather than a dump taken too early.
func waitForFrameToClear(t *testing.T, frame string) {
	t.Helper()
	deadline := time.Now().Add(traversalReturnCeiling)
	for time.Now().Before(deadline) {
		if !strings.Contains(goroutineFrames(), frame) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestStoppingACompletionEndsTheWorkspaceTraversal is the load-bearing proof for
// the completion projections.
//
// Two traversals are driven to the same point — each parked inside a read of its
// workspace's first named pipe — and one caller is then withdrawn. Both pipes are
// then released, so both walks are free to continue. A single stack dump must
// show the stopped traversal's frame GONE, because the walk observed the
// withdrawal at its next directory entry and returned, and the control's frame
// STILL PRESENT, because the control walked on to its second pipe and parked
// there. The control is what makes the absence mean something: without it a dump
// missing both frames would read as success.
//
// A planted defect makes this fail: hand the traversal context.Background()
// instead of the request's context, and the withdrawal reaches nothing — the
// stopped walk continues to its second pipe exactly as the control does, and
// parkedStoppedTraversal is still on the stack.
//
// The same failure catches a design that watches for the withdrawal beside the
// traversal and returns early: the caller is released on time, so a timing
// assertion would pass, while the walk runs on and the second pipe is still
// parked.
func TestStoppingACompletionEndsTheWorkspaceTraversal(t *testing.T) {
	controlRoot, controlFirst, controlSecond := parkingWorkspace(t)
	stoppedRoot, stoppedFirst, _ := parkingWorkspace(t)

	controlSrv := newTestServer(t, controlRoot, nil, nil)
	stoppedSrv := newTestServer(t, stoppedRoot, nil, nil)

	controlDone := make(chan int, 1)
	go parkedControlTraversal(controlSrv, context.Background(), controlDone)

	stopCtx, stop := context.WithCancel(context.Background())
	stoppedDone := make(chan int, 1)
	go parkedStoppedTraversal(stoppedSrv, stopCtx, stoppedDone)

	// Both walks are now driven to the same point: inside the read of their first
	// pipe. Until this point there is nothing to compare — a walk that has not
	// reached the pipe is parked nowhere, and withdrawing it would end it at its
	// first directory entry without ever having read anything.
	stoppedWriter := awaitParkedRead(t, stoppedFirst)
	controlWriter := awaitParkedRead(t, controlFirst)

	// Withdrawn while it is inside the read, so the withdrawal is in force when the
	// walk reaches its next directory entry — which is where it is observed.
	stop()
	releaseParkedRead(t, stoppedWriter, "b-stopped")
	releaseParkedRead(t, controlWriter, "b-control")

	// Recorded rather than fatal, so the frame comparison below is still reached
	// and a failing run reports BOTH what the caller saw and what the stack says.
	select {
	case <-stoppedDone:
	case <-time.After(traversalReturnCeiling):
		t.Errorf("the stopped completion never returned within the liveness ceiling: " +
			"the withdrawal did not reach the traversal")
	}

	waitForFrameToClear(t, "parkedStoppedTraversal")

	frames := goroutineFrames()

	// This assertion is the comparison's needle. It runs the same matcher over the
	// same dump and must find the control's frame, so a failure to find the
	// stopped one is a fact about the stack rather than about the matcher.
	if !strings.Contains(frames, "parkedControlTraversal") {
		t.Fatalf("the control's traversal frame is ABSENT from the dump, so the comparison below " +
			"would pass for the wrong reason; the control must still be walking")
	}
	if strings.Contains(frames, "parkedStoppedTraversal") {
		t.Errorf("the stopped completion's traversal frame is STILL on the stack while the " +
			"control's is there too: the caller was withdrawn but the walk kept reading the " +
			"workspace")
	}

	select {
	case <-controlDone:
		t.Fatalf("the control traversal returned early; it was supposed to stay inside its walk " +
			"for the whole comparison")
	default:
	}

	// Released last so the control's goroutine can finish rather than outliving
	// the test parked on a pipe nobody will open.
	releasePipe(t, controlSecond, "c-control")
	<-controlDone
}

// TestStoppingAHoverEndsTheWorkspaceTraversal is the same proof for the hover
// projection, which reaches the traversal through a different entry point: the
// parse-cache pass misses and the workspace walk is the fallback.
//
// It is a separate test rather than a subtest of the one above because the two
// entry points are separately reachable and a single dump cannot tell which of
// two identically-named frames belongs to which. The planted defect is the same:
// hand findEntityForHover a background context and the stopped walk keeps reading.
func TestStoppingAHoverEndsTheWorkspaceTraversal(t *testing.T) {
	controlRoot, controlFirst, controlSecond := parkingWorkspace(t)
	stoppedRoot, stoppedFirst, _ := parkingWorkspace(t)

	controlSrv := newTestServer(t, controlRoot, nil, nil)
	stoppedSrv := newTestServer(t, stoppedRoot, nil, nil)

	controlDone := make(chan bool, 1)
	go parkedControlHoverTraversal(controlSrv, context.Background(), controlDone)

	stopCtx, stop := context.WithCancel(context.Background())
	stoppedDone := make(chan bool, 1)
	go parkedStoppedHoverTraversal(stoppedSrv, stopCtx, stoppedDone)

	stoppedWriter := awaitParkedRead(t, stoppedFirst)
	controlWriter := awaitParkedRead(t, controlFirst)

	stop()
	releaseParkedRead(t, stoppedWriter, "b-stopped")
	releaseParkedRead(t, controlWriter, "b-control")

	select {
	case <-stoppedDone:
	case <-time.After(traversalReturnCeiling):
		t.Errorf("the stopped hover never returned within the liveness ceiling: " +
			"the withdrawal did not reach the traversal")
	}

	waitForFrameToClear(t, "parkedStoppedHoverTraversal")

	frames := goroutineFrames()
	if !strings.Contains(frames, "parkedControlHoverTraversal") {
		t.Fatalf("the control's hover traversal frame is ABSENT from the dump, so the comparison " +
			"below would pass for the wrong reason; the control must still be walking")
	}
	if strings.Contains(frames, "parkedStoppedHoverTraversal") {
		t.Errorf("the stopped hover's traversal frame is STILL on the stack while the control's " +
			"is there too: the caller was withdrawn but the walk kept reading the workspace")
	}

	releasePipe(t, controlSecond, "c-control")
	<-controlDone
}

// TestACompletionWhoseRequestHasAlreadyEndedWalksNothing pins the other half of
// the contract, and it is the half the dispatcher's entry check alone used to
// carry: a request withdrawn before the traversal starts must not read the
// workspace at all.
//
// The control is what discriminates. Both arms run against a workspace whose
// first file is a named pipe nobody will ever open for writing; the live arm must
// therefore park, and the withdrawn arm must not. A run where both returned would
// prove nothing, and a run where both parked would hang.
func TestACompletionWhoseRequestHasAlreadyEndedWalksNothing(t *testing.T) {
	root, firstPipe, secondPipe := parkingWorkspace(t)
	srv := newTestServer(t, root, nil, nil)

	withdrawn, stop := context.WithCancel(context.Background())
	stop()

	done := make(chan int, 1)
	go func() { done <- len(srv.enumerateEntityNames(withdrawn, "Component", srv.workspaceTerms())) }()

	select {
	case got := <-done:
		if got != 0 {
			t.Errorf("a completion whose request had already been withdrawn enumerated %d URIs; "+
				"it was supposed to read nothing", got)
		}
	case <-time.After(traversalReturnCeiling):
		t.Fatal("a completion whose request had already been withdrawn parked inside the " +
			"workspace read: the withdrawal was not observed before the walk started")
	}

	// The live control must behave differently, or the assertion above is
	// satisfied by a traversal that never reads anything under any circumstances.
	liveDone := make(chan int, 1)
	go func() {
		liveDone <- len(srv.enumerateEntityNames(context.Background(), "Component", srv.workspaceTerms()))
	}()
	select {
	case <-liveDone:
		t.Fatal("the live control returned without parking, so the withdrawn arm above proves " +
			"nothing: the workspace read is not blocking as this proof requires")
	case <-time.After(2 * time.Second):
		// Parked, as required. Both pipes are released below so the goroutine can
		// finish rather than outliving the test parked on one nobody will open.
	}
	releasePipe(t, firstPipe, "b-live")
	releasePipe(t, secondPipe, "c-live")
	<-liveDone
}

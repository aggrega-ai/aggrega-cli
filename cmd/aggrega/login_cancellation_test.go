// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"oras.land/oras-go/v2/registry/remote/auth"
)

// A login that accepts a context and mints a fresh one for its two outbound steps
// looks identical from the outside to one that forwards the caller's: the caller
// gets no reply either way, and the only difference is whether the exchange
// underneath is still running. So the credential-check proof is written against
// the stack, on the SERVER side — a login that returns proves only that the
// caller was released, while a registry still inside its handler is a check still
// in flight.

// parkedControlHandler and parkedStoppedHandler are the same function written
// twice, deliberately. The comparison needs ONE stack dump to tell two goroutines
// apart, and a frame is identified by the function it is in, so the two sides
// need two names. Unifying them would erase the distinction the proof rests on.
// The arrival is announced through a sync.Once because the registry client may
// open more than one request against the same server — a challenge and then an
// authenticated retry — and closing an already-closed channel panics. Announcing
// once and parking every request is what the proof needs: the frame is present
// for as long as ANY request is being served.
func parkedControlHandler(w http.ResponseWriter, r *http.Request, announce *sync.Once, entered chan struct{}, release <-chan struct{}) {
	announce.Do(func() { close(entered) })
	select {
	case <-r.Context().Done():
	case <-release:
		w.WriteHeader(http.StatusOK)
	}
}

func parkedStoppedHandler(w http.ResponseWriter, r *http.Request, announce *sync.Once, entered chan struct{}, release <-chan struct{}) {
	announce.Do(func() { close(entered) })
	select {
	case <-r.Context().Done():
	case <-release:
		w.WriteHeader(http.StatusOK)
	}
}

// loginGoroutineFrames renders every goroutine's stack, growing the buffer until
// the dump fits. A truncated dump would make an absent frame unreadable from a
// frame that simply did not fit, which is the one way this proof could quietly
// stop proving anything.
func loginGoroutineFrames() string {
	for size := 1 << 20; ; size *= 2 {
		buf := make([]byte, size)
		n := runtime.Stack(buf, true)
		if n < size {
			return string(buf[:n])
		}
	}
}

// loginReturnCeiling bounds "returns" against "hangs forever" and is NOT a
// performance budget: nothing here measures how long a stop takes, only that a
// stopped login stops at all. A machine slow enough to exceed this is a machine
// on which the whole suite has already failed.
const loginReturnCeiling = 30 * time.Second

// TestStoppingALoginEndsTheCredentialCheckOnTheWire proves the credential check
// runs under the caller's context rather than one the command minted.
//
// Two logins are driven to the same point — inside the registry's handler — and
// one is then stopped. A single stack dump, taken while both server goroutines
// exist, must show the stopped one's handler frame GONE and the control's STILL
// PRESENT. The control is what makes the absence mean something: without it a
// dump missing both frames would read as success.
//
// Parking on the SERVER is the strengthening. A login that returns on withdrawal
// proves only that the caller stopped waiting; a registry still inside its
// handler is a credential check still running. Asking whether the far end noticed
// is the only question that separates an abandoned exchange from a live one.
//
// A planted defect makes this fail: hand VerifyCredential context.Background()
// instead of the caller's, and the stopped login's handler parks exactly as the
// control does, leaving parkedStoppedHandler on the stack.
func TestStoppingALoginEndsTheCredentialCheckOnTheWire(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())

	release := make(chan struct{})
	var controlOnce, stoppedOnce sync.Once
	controlEntered := make(chan struct{})
	stoppedEntered := make(chan struct{})

	controlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parkedControlHandler(w, r, &controlOnce, controlEntered, release)
	}))
	defer controlSrv.Close()
	stoppedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parkedStoppedHandler(w, r, &stoppedOnce, stoppedEntered, release)
	}))
	defer stoppedSrv.Close()

	// Registered AFTER the servers so it runs BEFORE their Close: a server's Close
	// waits for its outstanding requests, and those requests are parked until this
	// channel closes. Releasing second would deadlock the teardown, which is a
	// failure mode this test has actually produced.
	defer close(release)

	controlHost := strings.TrimPrefix(controlSrv.URL, "http://")
	stoppedHost := strings.TrimPrefix(stoppedSrv.URL, "http://")

	// Resolved on the test's own goroutine: the workspace helpers are the test
	// harness's, and driving them from a spawned goroutine mixes harness calls
	// into a goroutine the harness does not own.
	controlWS := resolveTempWorkspace(t, tempWorkspaceOptions{})
	stoppedWS := resolveTempWorkspace(t, tempWorkspaceOptions{})

	controlDone := make(chan error, 1)
	go func() {
		controlDone <- loginRun(context.Background(), loginConfig{
			ws:       controlWS,
			Registry: controlHost, Username: "hello", PasswordStdin: true,
			In: strings.NewReader("world\n"), Out: io.Discard,
		})
	}()

	stopCtx, stop := context.WithCancel(context.Background())
	stoppedDone := make(chan error, 1)
	go func() {
		stoppedDone <- loginRun(stopCtx, loginConfig{
			ws:       stoppedWS,
			Registry: stoppedHost, Username: "hello", PasswordStdin: true,
			In: strings.NewReader("world\n"), Out: io.Discard,
		})
	}()

	// Both credential checks are now inside a handler. Until this point there is
	// nothing to compare: a frame that has not been entered is absent for a reason
	// that has nothing to do with cancellation.
	<-controlEntered
	<-stoppedEntered

	stop()

	// Recorded rather than fatal, so the frame comparison below is still reached
	// and a failing run reports BOTH what the caller saw and what the stack says.
	select {
	case err := <-stoppedDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the stopped login returned %v; want an error wrapping context.Canceled — "+
				"the credential check returned for some reason other than the withdrawal", err)
		}
	case <-time.After(loginReturnCeiling):
		t.Errorf("the stopped login never returned within the liveness ceiling: " +
			"the withdrawal did not reach the credential check")
	}

	frames := loginGoroutineFrames()

	// This assertion is the comparison's needle. It runs the same matcher over the
	// same dump and must find the control's frame, so a failure to find the stopped
	// one below is a fact about the stack rather than about the matcher.
	if !strings.Contains(frames, "parkedControlHandler") {
		t.Fatalf("the control registry's handler frame is ABSENT from the dump, so the comparison " +
			"below would pass for the wrong reason; the control must still be serving its request")
	}
	if strings.Contains(frames, "parkedStoppedHandler") {
		t.Errorf("the stopped login's registry handler is STILL serving while the control's is too: " +
			"the stop reached the caller but not the exchange on the wire")
	}

	select {
	case err := <-controlDone:
		t.Fatalf("the control login returned early (%v); it was supposed to stay inside its "+
			"credential check for the whole comparison", err)
	default:
	}
}

// parkingStore is a credential store whose write stays inside Put until either
// the context it was handed ends or the test lets it go — the behaviour a real
// credential helper has, and the reason a stop has to reach the write rather than
// the step after it.
type parkingStore struct {
	entered chan struct{}
	release <-chan struct{}
	put     chan auth.Credential
}

func (s *parkingStore) Get(context.Context, string) (auth.Credential, error) {
	return auth.EmptyCredential, nil
}

func (s *parkingStore) Put(ctx context.Context, _ string, cred auth.Credential) error {
	close(s.entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.release:
		s.put <- cred
		return nil
	}
}

func (s *parkingStore) Delete(context.Context, string) error { return nil }

// TestStoppingALoginEndsTheCredentialStoreWrite proves the second outbound step —
// the write to the credential store — also runs under the caller's context, and
// that stopping it stores nothing.
//
// The store records every credential it was actually given, so the assertion is
// about what the store HOLDS rather than about what the caller was told. That is
// the question the No-Ghost-Infrastructure concern actually asks here: a stopped
// login must not leave a credential behind looking like a good one.
//
// A planted defect makes this fail: hand store.Put context.Background() instead
// of the caller's, and the write completes after the withdrawal.
func TestStoppingALoginEndsTheCredentialStoreWrite(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())

	release := make(chan struct{})
	defer close(release)
	store := &parkingStore{
		entered: make(chan struct{}),
		release: release,
		put:     make(chan auth.Credential, 1),
	}
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{})

	stopCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- loginRun(stopCtx, loginConfig{
			ws:       ws,
			Registry: "registry.example.com", Username: "alice", PasswordStdin: true,
			SkipVerify: true, Store: store,
			In: strings.NewReader("s3cret\n"), Out: io.Discard,
		})
	}()

	// The write is now in flight. Until this point there is nothing to stop.
	<-store.entered
	stop()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the stopped login returned %v; want an error wrapping context.Canceled — "+
				"the store write returned for some reason other than the withdrawal", err)
		}
	case <-time.After(loginReturnCeiling):
		t.Fatalf("the stopped login never returned within the liveness ceiling: " +
			"the withdrawal did not reach the credential-store write")
	}

	select {
	case cred := <-store.put:
		t.Errorf("the store was handed a credential for %q despite the withdrawal; a stopped "+
			"login must leave nothing behind that reads as a good credential", cred.Username)
	default:
	}
}

// TestAnInterruptedLoginLeavesTheStoreAsItFoundIt is the end-to-end statement of
// the same property against the REAL credential store rather than an injected
// one: a login stopped before its write completes leaves the store holding
// exactly what it held before, and a reader afterwards finds no credential.
//
// It is the half the injected store cannot show, because the question is about
// the file the next command will read.
func TestAnInterruptedLoginLeavesTheStoreAsItFoundIt(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	const reg = "registry.example.com"

	withdrawn, cancel := context.WithCancel(context.Background())
	cancel()

	err := loginRun(withdrawn, loginConfig{
		ws:       resolveTempWorkspace(t, tempWorkspaceOptions{}),
		Registry: reg, Username: "alice", PasswordStdin: true, SkipVerify: true,
		In: strings.NewReader("s3cret\n"), Out: io.Discard,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a withdrawn login returned %v; want an error wrapping context.Canceled", err)
	}

	got, getErr := freshDockerStore(t).Get(context.Background(), reg)
	if getErr != nil {
		t.Fatalf("Get after the interrupted login: %v", getErr)
	}
	if got.Username != "" || got.Password != "" {
		t.Errorf("an interrupted login left a credential behind: {user:%q pass-set:%v}; the store "+
			"must hold exactly what it held before", got.Username, got.Password != "")
	}

	// The control: the same call with a live context DOES store, so the assertion
	// above is about the withdrawal rather than about a login that never works.
	if liveErr := loginRun(context.Background(), loginConfig{
		ws:       resolveTempWorkspace(t, tempWorkspaceOptions{}),
		Registry: reg, Username: "alice", PasswordStdin: true, SkipVerify: true,
		In: strings.NewReader("s3cret\n"), Out: io.Discard,
	}); liveErr != nil {
		t.Fatalf("the live-context control failed (%v); without a control that stores, the "+
			"assertion above proves nothing", liveErr)
	}
	stored, err := freshDockerStore(t).Get(context.Background(), reg)
	if err != nil {
		t.Fatalf("Get after the control login: %v", err)
	}
	if stored.Username != "alice" {
		t.Fatalf("the live-context control stored {user:%q}; want alice — the control must store "+
			"for the comparison above to mean anything", stored.Username)
	}
}

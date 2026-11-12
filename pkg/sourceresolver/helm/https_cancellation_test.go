// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helm

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
)

// This adapter is the hard case, and these tests are shaped by exactly what makes
// it hard: Helm's getter takes no context. It builds its own request and hands it
// to whatever transport it was given, so there is no argument to thread and no
// call to wrap — the stop has to arrive underneath, at the connection.
//
// That is also why these park on the SERVER side. A resolution that returns when
// its caller withdraws proves only that the caller was released; a server still
// sitting inside its handler is a fetch still in flight. Asking whether the other
// end noticed is the only question whose answer distinguishes a transfer that was
// abandoned from one that is still running behind a caller who stopped waiting.
//
// parkedControlChartFetch and parkedStoppedChartFetch are the same function
// written twice, deliberately: ONE stack dump distinguishes two goroutines only
// if the two frames carry two names. Unifying them would erase the distinction
// the proof rests on.
func parkedControlChartFetch(reqCtx context.Context, entered chan<- struct{}, release <-chan struct{}) {
	close(entered)
	select {
	case <-reqCtx.Done():
	case <-release:
	}
}

func parkedStoppedChartFetch(reqCtx context.Context, entered chan<- struct{}, release <-chan struct{}) {
	close(entered)
	select {
	case <-reqCtx.Done():
	case <-release:
	}
}

// parkingChartServer serves any path without ever answering: the handler stays
// put until the client goes away or the test lets it go. It stands in for a slow
// upstream, which is the case a stop has to be able to interrupt.
func parkingChartServer(
	t *testing.T,
	park func(reqCtx context.Context, entered chan<- struct{}, release <-chan struct{}),
	release <-chan struct{},
) (baseURL string, entered chan struct{}) {
	t.Helper()
	entered = make(chan struct{})
	var once bool
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// Only the first request parks the shared channel; a retry would otherwise
		// close an already-closed channel. Helm makes one request per fetch, so
		// this guard costs nothing and removes a way the fixture could panic
		// instead of failing.
		if once {
			<-release
			return
		}
		once = true
		park(r.Context(), entered, release)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, entered
}

// goroutineFrames renders every goroutine's stack, growing the buffer until the
// dump fits. A truncated dump would make an absent frame unreadable from a frame
// that simply did not fit, which is the one way this proof could quietly stop
// proving anything.
//
// It is declared here and used by the OCI cancellation proof in the same package
// too; the three comparable proofs elsewhere in this module each carry their own
// copy, because a test helper cannot cross a package boundary without being
// exported from somewhere that has no other reason to exist.
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

// waitForFrameToClear polls the stack until the named frame is gone or the
// liveness ceiling expires. It does NOT assert: the comparison that follows does
// that, on one dump, with the control alongside. This only removes the race
// between a client abandoning a connection and the server noticing, so a failure
// reported there is a real one rather than a dump taken too early.
func waitForFrameToClear(t *testing.T, frame string) {
	t.Helper()
	deadline := time.Now().Add(fetchReturnCeiling)
	for time.Now().Before(deadline) {
		if !strings.Contains(goroutineFrames(), frame) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestStoppingAnHTTPSChartResolutionEndsTheFetchOnTheWire is the load-bearing
// proof for the adapter whose transport accepts no context.
//
// Both of this adapter's fetches are covered, because they are two different
// network calls and threading one would not thread the other: a direct .tgz URL
// downloads the chart, and a repo-root URL first downloads index.yaml to find out
// where the chart is. A repair aimed only at the chart download would leave the
// index lookup running past a withdrawal, and nothing else in the suite would
// notice.
//
// Per row: two resolutions are driven to the same point — the server inside its
// handler with nothing written — and one caller is withdrawn. A single stack dump
// must show the stopped fetch's SERVER frame GONE, because the client abandoned
// the connection and the server observed it, and the control's STILL PRESENT.
//
// A planted defect makes this fail: hand the getter the egress gate's transport
// directly instead of the per-resolution copy from transportEndingWith, and the
// withdrawal reaches nothing — the connection stays up, the server stays in its
// handler, and parkedStoppedChartFetch is still on the stack.
//
// The same failure catches the design that watches for the withdrawal beside the
// fetch and returns early. That one releases the caller promptly, so a timing
// assertion passes it, while the download continues and the server never moves —
// which is the whole reason this is a frame comparison and not a stopwatch.
func TestStoppingAnHTTPSChartResolutionEndsTheFetchOnTheWire(t *testing.T) {
	cases := []struct {
		name string
		// path is appended to the parking server's base URL to form spec.URL.
		path string
		// chart is set only for the repo-root form, where the adapter must look
		// the chart up in index.yaml before it can fetch anything.
		chart string
	}{
		{
			name: "the chart download, from a direct .tgz URL",
			path: "/charts/mariadb-1.0.0.tgz",
		},
		{
			name:  "the index.yaml lookup, from a repo-root URL",
			path:  "/charts",
			chart: "mariadb",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})

			// Each resolution runs on its own goroutine and writes under the temp
			// workspace taken for it below, which the framework removes once this
			// body returns. The control one is still inside its fetch when the
			// comparison ends, which is exactly what the comparison asserts, so it
			// resumes the instant the servers are released and writes its chart
			// into a directory the removal may already be walking. The removal then
			// fails on a non-empty directory and reports as a failure of this test,
			// having measured nothing this test is about.
			//
			// So the body waits for both resolutions to return before it hands
			// back. The two defers below are registered in this order so that they
			// run in the opposite one — the release first, the wait second — and
			// both run ahead of every registered cleanup. Registering them before
			// the goroutines start is what keeps the pair on every exit path, a
			// fatal assertion included.
			var inFlight sync.WaitGroup
			defer func() {
				settled := make(chan struct{})
				go func() { inFlight.Wait(); close(settled) }()
				select {
				case <-settled:
				case <-time.After(fetchReturnCeiling):
					t.Errorf("a resolution was still running after both servers were released, so " +
						"the workspace it writes into is about to be removed underneath it")
				}
			}()
			defer close(release)

			controlBase, controlEntered := parkingChartServer(t, parkedControlChartFetch, release)
			stoppedBase, stoppedEntered := parkingChartServer(t, parkedStoppedChartFetch, release)

			// Temp dirs are taken on the test's own goroutine, not inside the workers.
			controlRoot, stoppedRoot := t.TempDir(), t.TempDir()

			// NewHTTPSAdapter wires the PRODUCTION getter, which is the point: a
			// test-injected getter carries its own body and never reaches a
			// transport, so it could not tell a threaded stop from an unthreaded one.
			resolve := func(ctx context.Context, base, root string) error {
				_, err := NewHTTPSAdapter().(*httpsAdapter).Resolve(ctx, sourceresolver.SourceSpec{
					Kind:          KindHTTPS,
					URL:           base + tc.path,
					Chart:         tc.chart,
					Version:       "1.0.0",
					WorkspaceRoot: root,
					CacheDir:      ".aggrega/cache",
					Client:        &http.Client{Transport: &http.Transport{}},
				})
				return err
			}

			inFlight.Add(2)

			controlDone := make(chan error, 1)
			go func() {
				defer inFlight.Done()
				controlDone <- resolve(context.Background(), controlBase, controlRoot)
			}()

			stopCtx, stop := context.WithCancel(context.Background())
			stoppedDone := make(chan error, 1)
			go func() {
				defer inFlight.Done()
				stoppedDone <- resolve(stopCtx, stoppedBase, stoppedRoot)
			}()

			// Both fetches are now in flight, with both servers inside their
			// handlers. Until this point there is nothing to compare: a frame that
			// has not been entered is absent for a reason that has nothing to do
			// with cancellation.
			<-controlEntered
			<-stoppedEntered

			stop()

			// Recorded rather than fatal, so the frame comparison below is still
			// reached and a failing run reports BOTH what the caller saw and what
			// the stack says. The error is NOT asserted to wrap context.Canceled:
			// the getter reports a broken connection in its own words, and pinning
			// that wording would assert Helm's error text rather than this
			// adapter's behaviour. That the fetch stopped is the stack's to say.
			select {
			case err := <-stoppedDone:
				if err == nil {
					t.Errorf("the stopped resolution succeeded; a fetch that was abandoned " +
						"cannot have produced a chart")
				}
			case <-time.After(fetchReturnCeiling):
				t.Errorf("the stopped resolution never returned within the liveness ceiling: " +
					"the withdrawal did not reach the fetch")
			}

			waitForFrameToClear(t, "parkedStoppedChartFetch")

			frames := goroutineFrames()

			// This assertion is the comparison's needle. It runs the same matcher
			// over the same dump and must find the control's frame, so a failure to
			// find the stopped one is a fact about the stack rather than about the
			// matcher.
			if !strings.Contains(frames, "parkedControlChartFetch") {
				t.Fatalf("the control's server frame is ABSENT from the dump, so the comparison " +
					"below would pass for the wrong reason; the control's fetch must still be in flight")
			}
			if strings.Contains(frames, "parkedStoppedChartFetch") {
				t.Errorf("the stopped fetch's SERVER frame is STILL on the stack while the " +
					"control's is there too: the caller was released but the download was never abandoned")
			}

			select {
			case err := <-controlDone:
				t.Fatalf("the control resolution returned early (%v); it was supposed to stay "+
					"inside its fetch for the whole comparison", err)
			default:
			}
		})
	}
}

// TestTheHTTPSAdapterLeavesTheSharedEgressTransportAlone guards the reason the
// per-resolution copy exists. The gate's transport is process-wide, so giving it
// a context would hand one resolution's lifetime to every other caller of it —
// a failure that would show up far away from here and look like nothing to do
// with source resolution.
//
// A planted defect makes this fail: drop the Clone in transportEndingWith and
// wrap the caller's transport in place.
func TestTheHTTPSAdapterLeavesTheSharedEgressTransportAlone(t *testing.T) {
	t.Parallel()

	shared := &http.Transport{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	derived := transportEndingWith(ctx, shared)

	if shared.DialContext != nil {
		t.Errorf("the shared transport's dialer was replaced; a copy was supposed to carry the " +
			"context so the gate's own transport stays free of it")
	}
	if derived == shared {
		t.Fatal("transportEndingWith returned the transport it was given, so every other caller " +
			"of the shared transport now shares this resolution's lifetime")
	}
	if derived.DialContext == nil {
		t.Error("the copy has no context-carrying dialer, so a withdrawal would reach no connection")
	}
}

// TestAWithdrawnHTTPSResolutionNeverDials is the same contract seen from the
// other end: a caller who had already withdrawn gets no connection at all, rather
// than one opened and then torn down.
//
// A planted defect makes this fail: dial under the request's own background
// context instead of the caller's, and the dial succeeds.
func TestAWithdrawnHTTPSResolutionNeverDials(t *testing.T) {
	t.Parallel()

	var dialed bool
	base := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialed = true
			return nil, ctx.Err()
		},
	}

	withdrawn, cancel := context.WithCancel(context.Background())
	cancel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("never reached"))
	}))
	defer srv.Close()

	a := NewHTTPSAdapter().(*httpsAdapter)
	_, err := a.Resolve(withdrawn, sourceresolver.SourceSpec{
		Kind:          KindHTTPS,
		URL:           srv.URL + "/charts/mariadb-1.0.0.tgz",
		Version:       "1.0.0",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
		Client:        &http.Client{Transport: base},
	})
	if err == nil {
		t.Fatal("a withdrawn caller got a chart back")
	}
	if !dialed {
		t.Fatal("the dialer was never reached, so this test asserts nothing about the dial")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the dial reported %v; want an error wrapping context.Canceled — the dial ran "+
			"under a context the caller never supplied", err)
	}
}

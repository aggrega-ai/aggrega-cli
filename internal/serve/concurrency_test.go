// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// concurrencyComponentYAML is a minimal Component the lazy catalog walk
// indexes and resolves, so the high-pool fan-out exercises the lazy
// lookup's Tier-1 index build + hot-cache populate races, not just the
// parse cache and diagnostics map.
const concurrencyComponentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/x
  version: "1.0.0"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`

// newConcurrencyServer builds a dispatch-loop-ready Server at the
// requested pool size whose lazy catalog walks a tmp workspace holding
// one Component file. The shared workspace files are what the
// concurrent handlers contend on.
func newConcurrencyServer(t *testing.T, in io.Reader, out io.Writer, poolSize int) (*Server, string) {
	t.Helper()
	tmp := t.TempDir()
	compPath := filepath.Join(tmp, "comp.yaml")
	if err := os.WriteFile(compPath, []byte(concurrencyComponentYAML), 0o644); err != nil {
		t.Fatalf("write component fixture: %v", err)
	}
	srv := newPoolTestServer(in, out, poolSize, nil)
	srv.workspaceRoot = tmp
	srv.lazyCatalog = catalog.NewLazyLookup(tmp, "", []string{tmp}, nil)
	return srv, compPath
}

// drainResponses reads framed responses from r until it hits EOF or the
// reader errors, returning how many it read. It runs concurrently with
// the request feed so a full bounded queue never deadlocks the test.
func drainResponses(r *bufio.Reader) int {
	n := 0
	for {
		if _, err := readMessage(r); err != nil {
			return n
		}
		n++
	}
}

// TestConcurrentSharedFileDispatchRaceClean is the load-bearing proof
// for the parse cache, diagnostics map, lazyCatalog, positionEncoding,
// and shuttingDown surfaces: it drives many concurrent
// validate/invalidate/diagnostics requests (interleaved with an
// initialize that writes positionEncoding and a terminating shutdown)
// against the SAME workspace files through the real dispatch loop at a
// range of pool sizes. With the race detector on, any unsynchronized
// access to a shared surface fails the test. Run at the new default
// (4) and a high pool (16, 64) so the worker pool actually parallelizes
// the contention.
func TestConcurrentSharedFileDispatchRaceClean(t *testing.T) {
	t.Parallel()
	for _, pool := range []int{4, 16, 64} {
		pool := pool
		t.Run(fmt.Sprintf("pool=%d", pool), func(t *testing.T) {
			t.Parallel()
			stdinR, stdinW := io.Pipe()
			stdoutR, stdoutW := io.Pipe()
			srv, compPath := newConcurrencyServer(t, stdinR, stdoutW, pool)

			loopDone := make(chan error, 1)
			go func() { loopDone <- srv.dispatchLoop(500 * time.Millisecond) }()

			// Read responses concurrently so a saturated bounded queue
			// back-pressures the writer rather than deadlocking the test.
			readDone := make(chan int, 1)
			go func() { readDone <- drainResponses(bufio.NewReader(stdoutR)) }()

			content := concurrencyComponentYAML
			file := compPath

			// The script interleaves the surfaces: initialize (writes
			// positionEncoding), then a burst hitting the same file's parse
			// cache (validate/diagnostics), the diagnostics map (validate
			// writes, diagnostics reads), and the lazyCatalog (invalidate ->
			// Reset). All target the SAME file/workspace so the handlers
			// genuinely contend.
			var feed [][]byte
			feed = append(feed, framedRequest(1, "aggrega/initialize", initializeParams{}))
			for i := 0; i < 60; i++ {
				feed = append(feed,
					framedRequest(i+2, "aggrega/validate", validateParams{File: file, Content: content}),
					framedRequest(i+2, "aggrega/diagnostics", diagnosticsParams{File: file}),
					framedRequest(i+2, "aggrega/invalidate", invalidateParams{File: file, Granularity: "workspace"}),
				)
			}
			feed = append(feed, framedNotification("aggrega/shutdown", nil))

			for _, msg := range feed {
				if _, err := stdinW.Write(msg); err != nil {
					t.Fatalf("write request: %v", err)
				}
			}
			stdinW.Close()

			select {
			case err := <-loopDone:
				if err != nil {
					t.Fatalf("dispatchLoop: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("dispatch loop did not exit")
			}
			// The serializer has flushed and returned by the time the
			// dispatch loop exits, so no further writes touch the pipe.
			// Closing the writer now hands the drain goroutine its EOF.
			stdoutW.Close()
			<-readDone
		})
	}
}

// TestConcurrentValidateAndReadHandlersRaceClean drives the resolve /
// hover / completion read paths (which range the parse cache via
// cache.Range and read positionEncoding) concurrently with validate
// (which Puts/Drops the cache and writes the diagnostics map) against
// the same file. These reader handlers reach into the cache from
// outside cache.go, so this exercises the Range-under-lock path that
// replaced the raw map iteration.
func TestConcurrentValidateAndReadHandlersRaceClean(t *testing.T) {
	t.Parallel()
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	srv, compPath := newConcurrencyServer(t, stdinR, stdoutW, 16)

	loopDone := make(chan error, 1)
	go func() { loopDone <- srv.dispatchLoop(500 * time.Millisecond) }()
	readDone := make(chan int, 1)
	go func() { readDone <- drainResponses(bufio.NewReader(stdoutR)) }()

	content := concurrencyComponentYAML
	file := compPath
	pos := func() json.RawMessage { return json.RawMessage(`{"line":0,"character":0}`) }

	var feed [][]byte
	for i := 0; i < 50; i++ {
		feed = append(feed,
			framedRequest(i, "aggrega/validate", validateParams{File: file, Content: content}),
			framedRequest(i, "aggrega/resolve", map[string]any{"uri": file, "position": pos()}),
			framedRequest(i, "aggrega/hover", map[string]any{"uri": file, "position": pos()}),
			framedRequest(i, "aggrega/completion", map[string]any{"uri": file, "position": pos()}),
			framedRequest(i, "aggrega/findReferences", map[string]any{"uri": file, "position": pos()}),
		)
	}
	feed = append(feed, framedNotification("aggrega/shutdown", nil))
	for _, msg := range feed {
		if _, err := stdinW.Write(msg); err != nil {
			t.Fatalf("write request: %v", err)
		}
	}
	stdinW.Close()

	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("dispatchLoop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch loop did not exit")
	}
	// The serializer has flushed and returned by the time the dispatch
	// loop exits, so no further writes touch the pipe. Closing the writer
	// now hands the drain goroutine its EOF.
	stdoutW.Close()
	<-readDone
}

// TestConcurrentIrGraphValidateInvalidateRaceClean drives the four pin-dependent
// daemon surfaces — irGraph, validate (Tier-4), invalidate (Reset), and
// diagnostics — concurrently against a real Release-bearing workspace through the
// dispatch loop at high pool sizes. Each handler resolves through its own
// per-request catalog view, and invalidate resets the session catalog under it;
// with the race detector on, any unsynchronized access between a request view's
// resolution and a concurrent reset (or another request's view) fails the test.
// Distinct from the pin-isolation test (which asserts logical correctness), this
// asserts the data-race floor across all four surfaces at once.
func TestConcurrentIrGraphValidateInvalidateRaceClean(t *testing.T) {
	t.Parallel()
	ws, releaseA, releaseB := pinIsolationWorkspace(t)
	relA := filepath.Base(releaseA)
	relB := filepath.Base(releaseB)

	for _, pool := range []int{4, 32} {
		pool := pool
		t.Run(fmt.Sprintf("pool=%d", pool), func(t *testing.T) {
			t.Parallel()
			stdinR, stdinW := io.Pipe()
			stdoutR, stdoutW := io.Pipe()
			srv := newPinIsolationServer(t, ws, stdinR, stdoutW, pool)
			srv.activeReleasePath = releaseA

			loopDone := make(chan error, 1)
			go func() { loopDone <- srv.dispatchLoop(500 * time.Millisecond) }()
			readDone := make(chan int, 1)
			go func() { readDone <- drainResponses(bufio.NewReader(stdoutR)) }()

			var feed [][]byte
			feed = append(feed, framedRequest(1, "aggrega/initialize", initializeParams{}))
			aggA := filepath.Join(ws, "release-a-agg.yaml")
			for i := 0; i < 40; i++ {
				feed = append(feed,
					framedRequest(i+2, "aggrega/irGraph", irGraphParams{Release: relA}),
					framedRequest(i+2, "aggrega/irGraph", irGraphParams{Release: relB}),
					framedRequest(i+2, "aggrega/validate", validateParams{File: aggA, Content: ""}),
					framedRequest(i+2, "aggrega/diagnostics", diagnosticsParams{File: aggA}),
					framedRequest(i+2, "aggrega/invalidate", invalidateParams{Granularity: "workspace"}),
				)
			}
			feed = append(feed, framedNotification("aggrega/shutdown", nil))
			for _, msg := range feed {
				if _, err := stdinW.Write(msg); err != nil {
					t.Fatalf("write request: %v", err)
				}
			}
			stdinW.Close()

			select {
			case err := <-loopDone:
				if err != nil {
					t.Fatalf("dispatchLoop: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("dispatch loop did not exit")
			}
			// The serializer has flushed and returned by the time the
			// dispatch loop exits, so no further writes touch the pipe.
			// Closing the writer now hands the drain goroutine its EOF.
			stdoutW.Close()
			<-readDone
		})
	}
}

// TestLazyLookupConcurrentAccessRaceClean is the direct pkg-level proof
// that LazyLookup is concurrency-safe: it fans out concurrent
// Component/Feature/AsCatalogLookup/MappingLibraries/Skips/Reset/SetPins
// calls — including invoking the closures AsCatalogLookup hands out
// after a concurrent SetPins/Reset — on one *LazyLookup over a shared
// catalog directory. Under -race this fails if any exported method or
// escaping closure touches the lookup's maps without the lock. It also
// guards against the re-entrant-deadlock failure mode (a hang) the
// coarse lock would introduce if a locked exported method called
// another locked one.
func TestLazyLookupConcurrentAccessRaceClean(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "comp.yaml"), []byte(concurrencyComponentYAML), 0o644); err != nil {
		t.Fatalf("write component fixture: %v", err)
	}
	l := catalog.NewLazyLookup(tmp, "", []string{tmp}, nil)

	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		w := w
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				switch (w + i) % 7 {
				case 0:
					l.Component("core/x")
				case 1:
					l.Feature("core/y")
				case 2:
					// Invoke the escaping closures the accessor hands out —
					// they re-enter the exported Component/Feature, which must
					// take the lock fresh (no lock held here).
					cl := l.AsCatalogLookup()
					cl.Component("core/x")
					cl.Feature("core/x")
				case 3:
					l.MappingLibraries()
				case 4:
					l.Skips()
				case 5:
					l.SetPins(nil)
				case 6:
					l.Reset()
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("LazyLookup fan-out did not complete; likely a re-entrant-lock deadlock")
	}
}

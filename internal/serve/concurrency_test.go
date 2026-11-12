// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bufio"
	"context"
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
const concurrencyComponentYAML = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/x
  version: "1.0.0"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`

// newConcurrencyServer builds a dispatch-loop-ready Server at the requested pool
// size whose lazy catalog walks a tmp workspace holding two documents: the
// resolvable Component above, and a second document the envelope gate rejects.
// The shared workspace files are what the concurrent handlers contend on.
//
// The second document is what makes an ANSWER assertion possible. The Component
// is clean, so its correct diagnostics answer is the empty one — byte-identical
// to the false clean answer a reader gets when it reads a file mid-validation —
// and no assertion over it can separate the two. A document that HAS findings
// can only be answered correctly one way.
func newConcurrencyServer(t *testing.T, in io.Reader, out io.Writer, poolSize int) (srv *Server, compPath, brokenPath string) {
	t.Helper()
	tmp := t.TempDir()
	compPath = filepath.Join(tmp, "comp.yaml")
	if err := os.WriteFile(compPath, []byte(concurrencyComponentYAML), 0o644); err != nil {
		t.Fatalf("write component fixture: %v", err)
	}
	brokenPath = filepath.Join(tmp, "concurrency-broken.yaml")
	if err := os.WriteFile(brokenPath, []byte(brokenComponentYAML), 0o644); err != nil {
		t.Fatalf("write rejected-document fixture: %v", err)
	}
	srv = newPoolTestServer(t, in, out, poolSize, nil)
	srv.workspaceRoot = tmp
	srv.lazyCatalog = catalog.NewLazyLookup(tmp, "", []string{tmp}, nil)
	reanchorTestWorkspace(t, srv)
	return srv, compPath, brokenPath
}

// drainResponses reads framed messages from r until it hits EOF or the reader
// errors, returning every id-bearing response it read, keyed by id. It runs
// concurrently with the request feed so a full bounded queue never deadlocks the
// test.
//
// It returns the BODIES rather than a count, and that is the whole point of its
// shape. A drain that returns a count discards every answer it read, so a
// harness built on one asserts that the daemon replied and nothing at all about
// WHAT it replied — which is how concurrent traffic can drive a wrong-answer
// defect on every run and pass. Attribution needs distinct request ids, so the
// feeds below assign one per request instead of reusing an id across an
// iteration's several requests.
//
// A notification carries no id and is counted in total but held in no entry,
// because nothing correlates it to a request.
func drainResponses(r *bufio.Reader) (byID map[int]jsonRPCResponse, total int) {
	byID = map[int]jsonRPCResponse{}
	for {
		body, err := readMessage(r)
		if err != nil {
			return byID, total
		}
		total++
		var resp jsonRPCResponse
		if json.Unmarshal(body, &resp) != nil || len(resp.ID) == 0 {
			continue
		}
		var id int
		if json.Unmarshal(resp.ID, &id) != nil {
			continue
		}
		byID[id] = resp
	}
}

// drainedDiagnostics returns the raw `diagnostics` array a drained response
// carried, and whether it carried a result at all.
func drainedDiagnostics(resp jsonRPCResponse) (json.RawMessage, bool) {
	if resp.Error != nil || len(resp.Result) == 0 {
		return nil, false
	}
	var body struct {
		Diagnostics json.RawMessage `json:"diagnostics"`
	}
	if json.Unmarshal(resp.Result, &body) != nil {
		return nil, false
	}
	return body.Diagnostics, true
}

// TestConcurrentSharedFileDispatchRaceClean is the load-bearing proof for the
// parse cache, diagnostics map, lazyCatalog, positionEncoding, and shuttingDown
// surfaces: it drives many concurrent validate/invalidate/diagnostics requests
// (interleaved with an initialize that writes positionEncoding and a terminating
// shutdown) against the SAME workspace files through the real dispatch loop at a
// range of pool sizes. With the race detector on, any unsynchronized access to a
// shared surface fails the test. Run at the default (4) and a high pool (16, 64)
// so the worker pool actually parallelizes the contention.
//
// # It asserts the answers as well as the data-race floor
//
// Race-cleanliness on its own is a weak verdict here, because a reader can be
// perfectly synchronized and still answer out of state a validation is halfway
// through writing — every access properly locked, every answer wrong. So the
// feed carries a second stream against a document the envelope gate REJECTS, and
// every answer on that stream is checked.
//
// Two properties, and the second is the one a count could never see. Every
// validation of the rejected document must report the identical, non-empty
// finding set — the first is taken as the expectation, off the wire, so nothing
// here re-derives it through the code under test. And every diagnostics answer
// for it must be either that same finding set or the honest not-validated
// refusal: never an empty set, which would be that document reported clean.
//
// Nothing invalidates the rejected document, which is what makes its answer
// interleaving-independent and equality the right assertion. The Component's own
// stream keeps the per-file invalidate contention it always had.
func TestConcurrentSharedFileDispatchRaceClean(t *testing.T) {
	t.Parallel()
	for _, pool := range []int{4, 16, 64} {
		pool := pool
		t.Run(fmt.Sprintf("pool=%d", pool), func(t *testing.T) {
			t.Parallel()
			stdinR, stdinW := io.Pipe()
			stdoutR, stdoutW := io.Pipe()
			srv, compPath, brokenPath := newConcurrencyServer(t, stdinR, stdoutW, pool)

			loopDone := make(chan error, 1)
			go func() { loopDone <- srv.dispatchLoop(context.Background(), 500*time.Millisecond) }()

			// Read responses concurrently so a saturated bounded queue
			// back-pressures the writer rather than deadlocking the test.
			type drained struct {
				byID  map[int]jsonRPCResponse
				total int
			}
			readDone := make(chan drained, 1)
			go func() {
				byID, total := drainResponses(bufio.NewReader(stdoutR))
				readDone <- drained{byID: byID, total: total}
			}()

			content := concurrencyComponentYAML
			file := compPath

			// The script interleaves the surfaces: initialize (writes
			// positionEncoding), then a burst hitting the same file's parse
			// cache (validate/diagnostics), the diagnostics map (validate
			// writes, diagnostics reads), and the lazyCatalog (invalidate ->
			// Reset). All target the SAME file/workspace so the handlers
			// genuinely contend.
			//
			// Ids are distinct per request rather than shared across an
			// iteration, because an answer that cannot be attributed to its
			// request cannot be asserted on.
			nextID := 1
			id := func() int { nextID++; return nextID }
			var feed [][]byte
			var brokenValidates, brokenReads []int
			feed = append(feed, framedRequestIn(t, srv, id(), "aggrega/initialize", initializeParams{}))
			for i := 0; i < 60; i++ {
				bv, br := id(), id()
				brokenValidates = append(brokenValidates, bv)
				brokenReads = append(brokenReads, br)
				feed = append(feed,
					framedRequestIn(t, srv, id(), "aggrega/validate", validateParams{File: file, Content: content}),
					framedRequestIn(t, srv, id(), "aggrega/diagnostics", diagnosticsParams{File: file}),
					framedRequestIn(t, srv, id(), "aggrega/invalidate", invalidateParams{File: file, Granularity: "workspace"}),
					framedRequestIn(t, srv, bv, "aggrega/validate", validateParams{File: brokenPath, Content: brokenComponentYAML}),
					framedRequestIn(t, srv, br, "aggrega/diagnostics", diagnosticsParams{File: brokenPath}),
				)
			}
			feed = append(feed, framedNotification("aggrega/shutdown", nil))

			for _, msg := range feed {
				if _, err := stdinW.Write(msg); err != nil {
					t.Fatalf("write request: %v", err)
				}
			}
			stdinW.Close()

			// A liveness ceiling, not a throughput budget: it separates a loop
			// that exits from one that is never going to. The cleanest-case
			// measurement for a whole sub-test here, run on its own, is under two
			// tenths of a second; the ceiling is three orders of magnitude above
			// that, because the figure spikes several-fold when the rest of this
			// package's parallel tests are competing for the same cores, and
			// because a reader of a file under validation now WAITS for it — so at
			// a wide pool this feed's sixty concurrent validations of one document
			// serialize sixty readers behind them. That is a shape no editor
			// produces, since one debounces to a single buffer, but one this feed
			// does.
			select {
			case err := <-loopDone:
				if err != nil {
					t.Fatalf("dispatchLoop: %v", err)
				}
			case <-time.After(60 * time.Second):
				t.Fatal("dispatch loop did not exit; the daemon is wedged")
			}
			// The serializer has flushed and returned by the time the
			// dispatch loop exits, so no further writes touch the pipe.
			// Closing the writer now hands the drain goroutine its EOF.
			stdoutW.Close()
			got := <-readDone

			// The expectation, taken off the wire from the validations
			// themselves rather than recomputed here.
			var want json.RawMessage
			for _, vid := range brokenValidates {
				resp, ok := got.byID[vid]
				if !ok {
					t.Fatalf("no answer for the validation sent under id %d", vid)
				}
				diags, isResult := drainedDiagnostics(resp)
				if !isResult {
					t.Fatalf("id %d: a validation of the rejected document was refused: %+v", vid, resp.Error)
				}
				if len(diags) == 0 || string(diags) == "null" || string(diags) == "[]" {
					t.Fatalf("id %d: a validation of the rejected document reported no findings, "+
						"so nothing in this test could tell a clean answer from a wrong one; got %s",
						vid, diags)
				}
				if want == nil {
					want = diags
					continue
				}
				if string(diags) != string(want) {
					t.Fatalf("id %d: two validations of one unchanged document disagreed:\n "+
						"first %s\n later %s", vid, want, diags)
				}
			}

			for _, rid := range brokenReads {
				resp, ok := got.byID[rid]
				if !ok {
					t.Fatalf("no answer for the diagnostics request sent under id %d", rid)
				}
				if resp.Error != nil {
					if resp.Error.Code == errFileNotValidated {
						// Honest: this reader ran before anything had validated
						// the document, or after its entry went stale.
						continue
					}
					t.Fatalf("id %d: diagnostics refused with %d: %s",
						rid, resp.Error.Code, resp.Error.Message)
				}
				diags, _ := drainedDiagnostics(resp)
				if string(diags) != string(want) {
					t.Fatalf("id %d: a diagnostics request answered a document under validation "+
						"with something other than its findings:\n want %s\n got  %s",
						rid, want, diags)
				}
			}
		})
	}
}

// TestConcurrentValidateAndReadHandlersRaceClean drives the resolve / hover /
// completion read paths (which range the parse cache via cache.Range and read
// positionEncoding) concurrently with validate (which Puts/Drops the cache and
// writes the diagnostics map) against the same file. These reader handlers reach
// into the cache from outside cache.go, so this exercises the Range-under-lock
// path that replaced the raw map iteration.
//
// It reads the bodies rather than counting frames, and asserts what those five
// readers can be held to: every request is answered exactly once, and every
// answer is either a result or the honest not-validated refusal — never a
// refusal from some other cause and never a malformed body. These readers decide
// and answer out of the parse cache alone, one store that is complete when it is
// written, so there is no gate-versus-payload window here to assert about; the
// equality oracle for the reader that DOES span two stores lives with the
// diagnostics wait.
func TestConcurrentValidateAndReadHandlersRaceClean(t *testing.T) {
	t.Parallel()
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	srv, compPath, _ := newConcurrencyServer(t, stdinR, stdoutW, 16)

	loopDone := make(chan error, 1)
	go func() { loopDone <- srv.dispatchLoop(context.Background(), 500*time.Millisecond) }()
	type drained struct {
		byID  map[int]jsonRPCResponse
		total int
	}
	readDone := make(chan drained, 1)
	go func() {
		byID, total := drainResponses(bufio.NewReader(stdoutR))
		readDone <- drained{byID: byID, total: total}
	}()

	content := concurrencyComponentYAML
	file := compPath
	pos := func() json.RawMessage { return json.RawMessage(`{"line":0,"character":0}`) }

	nextID := 0
	id := func() int { nextID++; return nextID }
	var feed [][]byte
	var sent []int
	send := func(method string, params any) {
		i := id()
		sent = append(sent, i)
		feed = append(feed, framedRequestIn(t, srv, i, method, params))
	}
	for i := 0; i < 50; i++ {
		send("aggrega/validate", validateParams{File: file, Content: content})
		send("aggrega/resolve", map[string]any{"uri": file, "position": pos()})
		send("aggrega/hover", map[string]any{"uri": file, "position": pos()})
		send("aggrega/completion", map[string]any{"uri": file, "position": pos()})
		send("aggrega/findReferences", map[string]any{"uri": file, "position": pos()})
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
	got := <-readDone

	for _, i := range sent {
		resp, ok := got.byID[i]
		if !ok {
			t.Fatalf("id %d was never answered", i)
		}
		if resp.Error != nil {
			if resp.Error.Code == errFileNotValidated {
				continue
			}
			t.Fatalf("id %d refused with %d: %s", i, resp.Error.Code, resp.Error.Message)
		}
		if len(resp.Result) == 0 {
			t.Fatalf("id %d carried neither a result nor an error", i)
		}
	}
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

			loopDone := make(chan error, 1)
			go func() { loopDone <- srv.dispatchLoop(context.Background(), 500*time.Millisecond) }()
			type drained struct {
				byID  map[int]jsonRPCResponse
				total int
			}
			readDone := make(chan drained, 1)
			go func() {
				byID, total := drainResponses(bufio.NewReader(stdoutR))
				readDone <- drained{byID: byID, total: total}
			}()

			var feed [][]byte
			feed = append(feed, framedRequestIn(t, srv, 1, "aggrega/initialize", initializeParams{}))
			aggA := filepath.Join(ws, "release-a-agg.yaml")
			// Both validations name the same file and different Releases, so
			// the two run the whole Tier-4 path against two Releases at once.
			// Nothing is shared between them to synchronize, which is the
			// property under test here: the Release each one acts on arrives
			// on its own request.
			// Ids are distinct per request, so an answer can be attributed to
			// the request that produced it: an id shared across an iteration's
			// six requests leaves the drain unable to tell whose answer it read.
			nextID := 1
			id := func() int { nextID++; return nextID }
			var sent []int
			send := func(method string, params any) {
				i := id()
				sent = append(sent, i)
				feed = append(feed, framedRequestIn(t, srv, i, method, params))
			}
			for i := 0; i < 40; i++ {
				send("aggrega/irGraph", irGraphParams{Release: relA})
				send("aggrega/irGraph", irGraphParams{Release: relB})
				send("aggrega/validate", validateParams{File: aggA, Content: "", Release: relA})
				send("aggrega/validate", validateParams{File: aggA, Content: "", Release: relB})
				send("aggrega/diagnostics", diagnosticsParams{File: aggA})
				send("aggrega/invalidate", invalidateParams{Granularity: "workspace"})
			}
			feed = append(feed, framedNotification("aggrega/shutdown", nil))
			for _, msg := range feed {
				if _, err := stdinW.Write(msg); err != nil {
					t.Fatalf("write request: %v", err)
				}
			}
			stdinW.Close()

			// A liveness ceiling, not a throughput budget. This feed drives four
			// hundred and eighty real semantic runs, so the cleanest-case
			// measurement for a sub-test is a little over nine seconds — which is
			// why the previous ten-second ceiling had almost no headroom and would
			// fail on a loaded runner rather than on a wedge. The figure and the
			// ceiling are recorded together so a later tightening argues from a
			// baseline instead of from a bare number.
			select {
			case err := <-loopDone:
				if err != nil {
					t.Fatalf("dispatchLoop: %v", err)
				}
			case <-time.After(120 * time.Second):
				t.Fatal("dispatch loop did not exit; the daemon is wedged")
			}
			// The serializer has flushed and returned by the time the
			// dispatch loop exits, so no further writes touch the pipe.
			// Closing the writer now hands the drain goroutine its EOF.
			stdoutW.Close()
			got := <-readDone

			// Every request answered exactly once, and every answer either a
			// result or one of the refusals this workspace can honestly produce.
			// Reading the bodies is what makes the response count mean something:
			// counting frames alone cannot tell an answer from an internal error.
			for _, i := range sent {
				resp, ok := got.byID[i]
				if !ok {
					t.Fatalf("id %d was never answered", i)
				}
				if resp.Error != nil {
					switch resp.Error.Code {
					case errFileNotValidated, errValidationInFlight, errLockfileAbsent:
						continue
					}
					t.Fatalf("id %d refused with %d: %s", i, resp.Error.Code, resp.Error.Message)
				}
				if len(resp.Result) == 0 {
					t.Fatalf("id %d carried neither a result nor an error", i)
				}
			}
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

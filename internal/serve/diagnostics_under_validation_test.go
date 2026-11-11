// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// brokenComponentYAML is a Component the document-envelope gate rejects, so a
// validation of it produces findings a reader can be wrong about. A CLEAN
// document is useless as a fixture here: its correct diagnostics answer is the
// empty one, which is byte-identical to the false clean answer this file exists
// to rule out, so no assertion over it could tell the two apart.
const brokenComponentYAML = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/broken
  version: "1.0.0"
spec:
  notAComponentField: true
  alsoNotAComponentField: 7
`

// validationWaitLivenessCeiling separates "the reader reached the wait" from "it
// is never going to". It is a liveness ceiling and NOT a performance budget: no
// amount of contention on a loaded runner turns a reader that blocked into one
// that did not, so nothing here asserts how FAST anything happens.
const validationWaitLivenessCeiling = 2 * time.Second

// validateGateSeam holds a validation open at the one moment its parse cache
// entry exists and its diagnostic layers do not — the window a concurrent reader
// of the same file is answered from when nothing makes it wait. Wired to one of
// the diagnostics hooks instead, the same seam holds a reader at that hook's
// point: between its first wait and its first lookup, or between that lookup and
// its second wait.
//
// It exists because the window is milliseconds wide in production, so a test
// that merely fires concurrent traffic at it covers the window only on the runs
// that happen to land in it. Holding the validation there makes the race
// deterministic, which is what lets a planted defect redden every run instead of
// most of them.
type validateGateSeam struct {
	mu      sync.Mutex
	entered chan struct{}
	release chan struct{}
}

// arm makes the next validation to reach the window stop there.
func (g *validateGateSeam) arm() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.entered = make(chan struct{}, 1)
	g.release = make(chan struct{})
}

// hook is what handleValidate calls. Unarmed it is a no-op, so one session
// serves both the sequential exchanges and the held ones.
func (g *validateGateSeam) hook(string) {
	g.mu.Lock()
	entered, release := g.entered, g.release
	g.mu.Unlock()
	if release == nil {
		return
	}
	select {
	case entered <- struct{}{}:
	default:
	}
	<-release
}

// waitEntered blocks until a validation has reached the window.
func (g *validateGateSeam) waitEntered(t *testing.T) {
	t.Helper()
	g.mu.Lock()
	entered := g.entered
	g.mu.Unlock()
	select {
	case <-entered:
	case <-time.After(validationWaitLivenessCeiling):
		t.Fatal("nothing reached the point the seam holds; the seam is not wired")
	}
}

// open releases the held validation and disarms the seam.
func (g *validateGateSeam) open() {
	g.mu.Lock()
	release := g.release
	g.release, g.entered = nil, nil
	g.mu.Unlock()
	if release != nil {
		close(release)
	}
}

// diagWire drives one in-memory dispatch loop over pipes and reads framed
// responses back by id.
//
// Every request it sends carries a DISTINCT id, which the older concurrency
// harnesses did not: they reused one id across the validate, diagnostics and
// invalidate of each iteration, so no response could be attributed to the
// request that produced it — which is the mechanical reason those harnesses
// could only ever count frames.
//
// Reads are synchronous and in arrival order, which is what the wire gives: the
// serializer emits ordered response slots in arrival-sequence order, so a later
// request's answer cannot be read before an earlier request's. Every assertion
// about a request whose answer is queued behind a held validation therefore
// observes the WAITER COUNT while the validation is held, and reads the answers
// afterwards.
type diagWire struct {
	t        *testing.T
	srv      *Server
	root     string
	stdinW   *io.PipeWriter
	stdoutW  *io.PipeWriter
	out      *bufio.Reader
	loopDone chan error
	seam     *validateGateSeam
	nextID   int
	pending  map[int]jsonRPCResponse
	closed   bool
}

// newDiagWire starts a dispatch loop at the requested pool size over a temp
// workspace, with the validate-window seam wired and the wait budget optionally
// shortened. A zero budget leaves the package default in force.
func newDiagWire(t *testing.T, poolSize int, waitBudget time.Duration) *diagWire {
	t.Helper()
	root := t.TempDir()
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	srv := newPoolTestServerAt(t, root, stdinR, stdoutW, poolSize, nil)
	srv.lazyCatalog = catalog.NewLazyLookup(root, "", []string{root}, nil)
	reanchorTestWorkspace(t, srv)
	seam := &validateGateSeam{}
	srv.testValidateGateHook = seam.hook
	srv.validationWaitBudget = waitBudget

	w := &diagWire{
		t:        t,
		srv:      srv,
		root:     root,
		stdinW:   stdinW,
		stdoutW:  stdoutW,
		out:      bufio.NewReader(stdoutR),
		loopDone: make(chan error, 1),
		seam:     seam,
		nextID:   1,
		pending:  map[int]jsonRPCResponse{},
	}
	go func() { w.loopDone <- srv.dispatchLoop(context.Background(), 500*time.Millisecond) }()
	return w
}

// writeFile drops a document into the workspace under name and returns its
// absolute path. A fresh name per iteration is what keeps each iteration COLD:
// the false clean answer needs a diagnostic store with no entry for the file
// yet, which a reused path only has once.
func (w *diagWire) writeFile(name, content string) string {
	w.t.Helper()
	path := filepath.Join(w.root, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		w.t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// id hands out the next request id.
func (w *diagWire) id() int {
	w.nextID++
	return w.nextID
}

// send frames one request onto the daemon's stdin.
func (w *diagWire) send(id int, method string, params any) {
	w.t.Helper()
	if _, err := w.stdinW.Write(framedRequestIn(w.t, w.srv, id, method, params)); err != nil {
		w.t.Fatalf("write %s: %v", method, err)
	}
}

// notify frames one notification onto the daemon's stdin.
func (w *diagWire) notify(method string, params any) {
	w.t.Helper()
	if _, err := w.stdinW.Write(framedNotification(method, params)); err != nil {
		w.t.Fatalf("write notification %s: %v", method, err)
	}
}

// await returns the response for id, retaining any earlier-sequence response it
// had to read past. Notifications carry no id and are consumed.
func (w *diagWire) await(id int) jsonRPCResponse {
	w.t.Helper()
	if resp, ok := w.pending[id]; ok {
		delete(w.pending, id)
		return resp
	}
	for {
		body, err := readMessage(w.out)
		if err != nil {
			w.t.Fatalf("read response for id %d: %v", id, err)
		}
		var resp jsonRPCResponse
		if uerr := json.Unmarshal(body, &resp); uerr != nil {
			w.t.Fatalf("unmarshal response: %v (body %s)", uerr, body)
		}
		if len(resp.ID) == 0 {
			continue
		}
		var got int
		if json.Unmarshal(resp.ID, &got) != nil {
			continue
		}
		if got == id {
			return resp
		}
		w.pending[got] = resp
	}
}

// close drains the daemon and asserts the loop exited cleanly.
//
// The drain runs on its own goroutine because the serializer writes into a pipe
// with a single reader: an unread response blocks the serializer, which blocks
// the pool drain, which is a hang rather than a failure. Reading concurrently
// until EOF is what keeps a teardown from deadlocking on frames the test did not
// ask for.
func (w *diagWire) close() {
	w.t.Helper()
	if w.closed {
		return
	}
	w.closed = true
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			if _, err := readMessage(w.out); err != nil {
				return
			}
		}
	}()
	w.notify("aggrega/shutdown", nil)
	w.stdinW.Close()
	select {
	case err := <-w.loopDone:
		if err != nil {
			w.t.Errorf("dispatchLoop: %v", err)
		}
	case <-time.After(20 * time.Second):
		w.t.Error("dispatch loop did not exit; the daemon is wedged")
	}
	w.stdoutW.Close()
	<-drained
}

// diagnosticsOf returns the raw `diagnostics` array out of a response's result.
// It reads the WIRE bytes rather than re-deriving the value through the
// handler's own composition helper: an expectation computed by the code under
// test agrees with it by construction and can no longer catch it being wrong.
func diagnosticsOf(t *testing.T, resp jsonRPCResponse) json.RawMessage {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("expected a result, got error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	var body struct {
		Diagnostics json.RawMessage `json:"diagnostics"`
	}
	if err := json.Unmarshal(resp.Result, &body); err != nil {
		t.Fatalf("unmarshal result: %v (result %s)", err, resp.Result)
	}
	return body.Diagnostics
}

// sequentialAnswer performs a strictly alternating exchange — write the
// validation, read its reply, then write the diagnostics request and read its
// reply — and returns the diagnostics answer off the wire. Nothing overlaps, so
// this is the answer every interleaving has to reproduce.
func sequentialAnswer(t *testing.T, w *diagWire, path string) json.RawMessage {
	t.Helper()
	vid := w.id()
	w.send(vid, "aggrega/validate", validateParams{File: path, Content: brokenComponentYAML})
	validated := diagnosticsOf(t, w.await(vid))

	did := w.id()
	w.send(did, "aggrega/diagnostics", diagnosticsParams{File: path})
	answer := diagnosticsOf(t, w.await(did))

	if string(answer) != string(validated) {
		t.Fatalf("the sequential diagnostics answer diverges from the validation that "+
			"produced it:\n validate:    %s\n diagnostics: %s", validated, answer)
	}
	return answer
}

// settleWaiters waits, up to the liveness ceiling, for n readers of file to be
// blocked on its in-flight validation, and reports how many got there.
//
// It never fails the test. A design that does not wait produces zero waiters and
// has already answered wrongly by the time this returns, so letting the caller
// carry on is what puts the ANSWER assertion in the verdict seat rather than a
// timeout — a timeout would tell a later reader that the harness broke, when what
// broke is the behaviour.
func settleWaiters(w *diagWire, file string, n int) int {
	deadline := time.Now().Add(validationWaitLivenessCeiling)
	for {
		got := w.srv.validations.waiting(file)
		if got >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(time.Millisecond)
	}
}

// awaitNoWaiters waits for file's waiter count to reach zero and reports whether
// it did. Reaching zero while the validation is still HELD is the observable for
// a wait that ended on something other than the validation finishing.
func awaitNoWaiters(w *diagWire, file string) bool {
	deadline := time.Now().Add(validationWaitLivenessCeiling)
	for time.Now().Before(deadline) {
		if w.srv.validations.waiting(file) == 0 {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return w.srv.validations.waiting(file) == 0
}

// TestDiagnosticsSequentialAnswerIsTheOracle pins the answer every other
// assertion in this file compares against, and pins the fixture along with it: a
// fixture whose findings were empty would make every equality assertion here
// vacuous, so the non-empty check is a precondition rather than a nicety.
func TestDiagnosticsSequentialAnswerIsTheOracle(t *testing.T) {
	t.Parallel()
	w := newDiagWire(t, 1, 0)
	defer w.close()

	answer := sequentialAnswer(t, w, w.writeFile("broken.yaml", brokenComponentYAML))

	var entries []Diagnostic
	if err := json.Unmarshal(answer, &entries); err != nil {
		t.Fatalf("unmarshal the oracle: %v (answer %s)", err, answer)
	}
	if len(entries) == 0 {
		t.Fatalf("the fixture produced no findings, so every equality assertion over it would "+
			"pass on an empty answer; the answer was %s", answer)
	}
	for i, e := range entries {
		if e.Severity != "error" {
			t.Errorf("finding %d: severity = %q; want error", i, e.Severity)
		}
		if e.Message == "" {
			t.Errorf("finding %d carries no message", i)
		}
	}
}

// TestDiagnosticsAnswersTheRealFindingsUnderConcurrency is the reproduction and
// the fix's proof at once: for each pool size a validation is held at the moment
// its parse cache entry exists and its diagnostics do not, more readers than the
// pool has workers are sent at that same file, and every one of their answers
// must EQUAL the sequential answer.
//
// Equality is the assertion, not presence and not containment. A presence test
// passes on a partial answer and a containment test passes on the first finding
// of three, and both of those are wrong answers a reader would act on.
//
// The reader count exceeding the pool width is the deadlock proof as well. A
// validation registers itself from inside its own handler, so it is already
// running on a worker before any reader can see it — a reader therefore never
// waits on work that is itself waiting for a worker, and the readers beyond the
// pool's width queue behind the blocked ones and are served as they finish.
func TestDiagnosticsAnswersTheRealFindingsUnderConcurrency(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pool       int
		readers    int
		iterations int
	}{
		{pool: 1, readers: 3, iterations: 12},
		{pool: 2, readers: 6, iterations: 12},
		{pool: 4, readers: 12, iterations: 76},
		{pool: 16, readers: 20, iterations: 12},
		{pool: 64, readers: 68, iterations: 12},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("pool=%d/readers=%d", tc.pool, tc.readers), func(t *testing.T) {
			t.Parallel()
			w := newDiagWire(t, tc.pool, 0)
			defer w.close()

			oracle := sequentialAnswer(t, w, w.writeFile("oracle.yaml", brokenComponentYAML))

			for i := 0; i < tc.iterations; i++ {
				path := w.writeFile(fmt.Sprintf("race-%d.yaml", i), brokenComponentYAML)

				w.seam.arm()
				vid := w.id()
				w.send(vid, "aggrega/validate", validateParams{File: path, Content: brokenComponentYAML})
				w.seam.waitEntered(t)

				ids := make([]int, tc.readers)
				for r := range ids {
					ids[r] = w.id()
					w.send(ids[r], "aggrega/diagnostics", diagnosticsParams{File: path})
				}
				// At pool size one the single worker IS the held validation, so
				// no reader can be dequeued and none is expected to be waiting.
				// Above that, every reader the pool can dequeue should be.
				want := tc.readers
				switch {
				case tc.pool == 1:
					want = 0
				case want > tc.pool-1:
					want = tc.pool - 1
				}
				settled := settleWaiters(w, path, want)
				w.seam.open()

				if got := diagnosticsOf(t, w.await(vid)); string(got) != string(oracle) {
					t.Fatalf("iteration %d: the validation's own answer moved:\n want %s\n got  %s",
						i, oracle, got)
				}
				for r, id := range ids {
					got := diagnosticsOf(t, w.await(id))
					if string(got) != string(oracle) {
						t.Fatalf("iteration %d reader %d: answered a file under validation with "+
							"something other than its findings (%d of %d readers were waiting "+
							"when the validation was released):\n want %s\n got  %s",
							i, r, settled, want, oracle, got)
					}
				}
			}
		})
	}
}

// TestDiagnosticsWaitBlocksUntilTheValidationReturns asserts the STRUCTURE the
// answer assertions rest on: that a reader for a file under validation is
// actually blocked, rather than answering early and happening to be right.
//
// It is separate from the equality tests on purpose. Those release the validation
// whether or not a reader got there, so on their own they cannot distinguish
// "waited, then answered correctly" from "answered late and correctly by luck".
func TestDiagnosticsWaitBlocksUntilTheValidationReturns(t *testing.T) {
	t.Parallel()
	w := newDiagWire(t, 4, 0)
	defer w.close()

	path := w.writeFile("held.yaml", brokenComponentYAML)
	w.seam.arm()
	vid := w.id()
	w.send(vid, "aggrega/validate", validateParams{File: path, Content: brokenComponentYAML})
	w.seam.waitEntered(t)

	did := w.id()
	w.send(did, "aggrega/diagnostics", diagnosticsParams{File: path})
	if got := settleWaiters(w, path, 1); got < 1 {
		t.Errorf("no reader was blocked on the in-flight validation of %s, so a diagnostics "+
			"request read a file mid-validation instead of waiting for it", path)
	}
	w.seam.open()

	oracle := diagnosticsOf(t, w.await(vid))
	if got := diagnosticsOf(t, w.await(did)); string(got) != string(oracle) {
		t.Fatalf("the reader did not get the findings:\n want %s\n got  %s", oracle, got)
	}
	if got := w.srv.validations.waiting(path); got != 0 {
		t.Errorf("waiter count after the exchange = %d; want 0 — a waiter leaked", got)
	}
}

// TestDiagnosticsWaitsForAValidationThatSeededTheCacheAfterTheReaderArrived
// forces the one interleaving a wait for the validations running at a reader's
// ARRIVAL leaves open: the reader arrives while nothing is validating its file,
// and before it consults the parse cache a validation registers and seeds that
// cache. The reader is held between its first wait and its lookup, the
// validation is held at the moment its parse-cache entry exists and its findings
// do not, and only then is the reader let go — so its lookup hits on an entry
// the validation has not yet answered for, on every run rather than on the runs
// a loaded scheduler happens to produce.
//
// The answer must EQUAL the sequential one. A reader that answers at once finds
// no finding written yet, and is either refused as not validated or, where the
// answer is not gated on the semantic entry, reports a rejected document clean;
// a reader that waits again after its lookup is blocked on the held validation
// and is answered with its findings once it returns.
func TestDiagnosticsWaitsForAValidationThatSeededTheCacheAfterTheReaderArrived(t *testing.T) {
	t.Parallel()
	w := newDiagWire(t, 4, 0)
	defer w.close()
	reader := &validateGateSeam{}
	w.srv.testDiagnosticsDecideHook = reader.hook

	oracle := sequentialAnswer(t, w, w.writeFile("oracle.yaml", brokenComponentYAML))
	path := w.writeFile("seeded-after-arrival.yaml", brokenComponentYAML)

	// The reader first. Nothing is validating the file, so its first wait passes
	// straight through, and it stops before the lookup that decides whether it
	// can answer.
	reader.arm()
	did := w.id()
	w.send(did, "aggrega/diagnostics", diagnosticsParams{File: path})
	reader.waitEntered(t)

	// Then a validation, held once it has seeded the parse cache and before it
	// has written a finding.
	w.seam.arm()
	vid := w.id()
	w.send(vid, "aggrega/validate", validateParams{File: path, Content: brokenComponentYAML})
	w.seam.waitEntered(t)

	// Let the reader look. Its lookup hits on the held validation's entry.
	reader.open()
	settled := settleWaiters(w, path, 1)
	w.seam.open()

	resp := w.await(did)
	if resp.Error != nil {
		t.Fatalf("the reader was refused (%d: %s) although the validation it should have waited for "+
			"had seeded the parse cache before the reader looked", resp.Error.Code, resp.Error.Message)
	}
	if got := diagnosticsOf(t, resp); string(got) != string(oracle) {
		t.Fatalf("a reader that decided after a validation seeded the parse cache answered with "+
			"something other than its findings (%d reader(s) were waiting on the validation when it "+
			"was released; want 1):\n want %s\n got  %s", settled, oracle, got)
	}
	if got := diagnosticsOf(t, w.await(vid)); string(got) != string(oracle) {
		t.Fatalf("the validation's own answer moved:\n want %s\n got  %s", oracle, got)
	}
	if got := w.srv.validations.waiting(path); got != 0 {
		t.Errorf("waiter count after the exchange = %d; want 0 — a waiter leaked", got)
	}
}

// cancellableDocument is the document a cancelled validation validates. It is
// the Component the entity-kind acceptance harness holds, which that harness
// checks against its kind's schema on every run, and it owes no placeholder — so
// the envelope leg of a validation finds nothing in it and the validation goes on
// to the semantic pass a cancellation interrupts. A document the envelope
// rejects would be answered at the envelope instead, and a cancellation landing
// on it would exercise nothing.
var cancellableDocument = acceptanceFixtures["Component"].document

// cancelledReleaseValidation sends a validation of path naming a Release, has
// the client cancel it before its handler starts, and returns once that
// validation has seeded the parse cache and is held there by the gate seam — so
// the caller decides when it reaches its cancellation exit. It returns the
// validation's id for cancelledWithoutAnswer.
//
// The request is parked inside the dispatcher until its own context is cancelled
// (parkUntilCancelled), and the cancellation is the client's real
// $/cancelRequest. So the handler starts with a context already cancelled, and
// whether it takes the cancellation exit depends on nothing a scheduler decides.
//
// The cancellation exit is the handler's only way out once three things hold,
// and each is checked here rather than assumed, so a fixture that answers early
// fails as a fixture: the buffer parsed and was cached (the seam is entered only
// after that), a Release is named (so the Release-less answer is not taken), and
// the envelope leg finds nothing in content — checked through the same method the
// handler calls, before the request is sent.
func cancelledReleaseValidation(t *testing.T, w *diagWire, path, content string) int {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("fixture defect: the cancelled validation's document does not parse: %v", err)
	}
	release := filepath.Join(w.root, "release.yaml")
	if diags, answers := w.srv.envelopeDiagnosticsForBuffer(path, release, []byte(content), &doc); answers {
		t.Fatalf("fixture defect: the envelope leg answers this document, so its validation stops there "+
			"and never reaches the cancellation it is sent to exercise: %+v", diags)
	}
	vid := w.id()
	entered := parkUntilCancelled(t, w.srv, vid)
	w.seam.arm()
	w.send(vid, "aggrega/validate", validateParams{File: path, Content: content, Release: "release.yaml"})
	select {
	case <-entered:
	case <-time.After(validationWaitLivenessCeiling):
		t.Fatal("the validation never reached the dispatcher's park; the context hook is not wired")
	}
	w.notify("$/cancelRequest", map[string]any{"id": vid})
	w.seam.waitEntered(t)
	return vid
}

// cancelledWithoutAnswer asserts, once the cancelled validation vid has been
// released, that it wrote no response: a validation whose context was cancelled
// by the time its handler returned drops its answer, and one that answered would
// have written it. A request sent afterwards is read to get past vid's slot, which
// the serializer closes before any later answer, so an answer for vid would have
// been read and held on the way.
func cancelledWithoutAnswer(t *testing.T, w *diagWire, vid int) {
	t.Helper()
	after := w.id()
	w.send(after, "aggrega/diagnostics", diagnosticsParams{File: filepath.Join(w.root, "never-validated.yaml")})
	w.await(after)
	if resp, answered := w.pending[vid]; answered {
		t.Fatalf("fixture defect: the validation meant to be cancelled answered (%+v), so the cancellation "+
			"never reached it and the row asserts nothing about a cancelled validation", resp)
	}
}

// TestDiagnosticsAnswersOnlyForTheTextAFinishedValidationChecked pins what a
// diagnostics read answers for a file whose validation was cancelled partway. A
// cancelled validation has already seeded the parse cache — hover and completion
// read the buffer from there — and writes no finding, so a read that took the
// cached buffer as proof of a finished validation would answer a file nobody
// finished checking as clean, and a read that took any answer standing for the
// file as the answer for that buffer would report an earlier buffer's findings
// for it.
//
// Each row cancels a Release-named validation at the same point and names the
// defect it catches:
//
//   - a first validation, cancelled: the read is refused as not validated. It
//     catches a read that answers from the parse-cache entry alone, which
//     answers null.
//   - a later validation of CHANGED text, cancelled after a first one answered:
//     the read is refused as not validated. It catches a read that takes any
//     answer standing for the file as the answer for the buffer it holds, which
//     answers the first buffer's findings for a buffer nobody finished checking.
//   - a later validation of the SAME text, cancelled after a first one answered:
//     the read still answers the first answer. It catches a fix that discards the
//     answer on every cancellation, and one that ties the answer to the cached
//     entry or the buffer's identity rather than to its text; each refuses a
//     text a validation did finish checking.
//   - a reader that found an answered text, held after that lookup while a
//     validation of CHANGED text seeds the cache, waited for and cancelled:
//     refused as not validated. It catches a read that decides on the text its
//     first lookup found rather than on the parse held once its waits are over,
//     which answers the earlier text's findings for a buffer nobody finished
//     checking.
//   - a reader that looked the buffer up while the cancelled validation was
//     still running, and waited for it: refused as not validated. It catches a
//     fix that only removes the cancelled validation's parse-cache entry, which
//     comes too late for a reader that already found it and answers null.
func TestDiagnosticsAnswersOnlyForTheTextAFinishedValidationChecked(t *testing.T) {
	t.Parallel()
	refusedAsNotValidated := func(t *testing.T, resp jsonRPCResponse) {
		t.Helper()
		if resp.Error == nil {
			t.Fatalf("a file whose current text no validation finished checking was answered rather "+
				"than refused, which reports findings nobody produced for that text: %s", resp.Result)
		}
		if resp.Error.Code != errFileNotValidated {
			t.Fatalf("refusal code = %d (%s); want %d, file not validated",
				resp.Error.Code, resp.Error.Message, errFileNotValidated)
		}
	}
	rows := []struct {
		name  string
		drive func(t *testing.T, w *diagWire)
	}{
		{
			name: "a first validation cancelled",
			drive: func(t *testing.T, w *diagWire) {
				path := w.writeFile("cancelled-first.yaml", cancellableDocument)
				vid := cancelledReleaseValidation(t, w, path, cancellableDocument)
				w.seam.open()
				cancelledWithoutAnswer(t, w, vid)

				did := w.id()
				w.send(did, "aggrega/diagnostics", diagnosticsParams{File: path})
				refusedAsNotValidated(t, w.await(did))
			},
		},
		{
			name: "a later validation of changed text cancelled after a first one answered",
			drive: func(t *testing.T, w *diagWire) {
				if brokenComponentYAML == cancellableDocument {
					t.Fatal("fixture defect: the two texts are the same, so this row would drive the same-text row")
				}
				// The first validation answers with findings, so the earlier answer,
				// read back for the changed text, cannot pass for a refusal.
				path := w.writeFile("cancelled-changed.yaml", brokenComponentYAML)
				sequentialAnswer(t, w, path)
				vid := cancelledReleaseValidation(t, w, path, cancellableDocument)
				w.seam.open()
				cancelledWithoutAnswer(t, w, vid)

				did := w.id()
				w.send(did, "aggrega/diagnostics", diagnosticsParams{File: path})
				refusedAsNotValidated(t, w.await(did))
			},
		},
		{
			name: "a later validation of the same text cancelled after a first one answered",
			drive: func(t *testing.T, w *diagWire) {
				// The first validation runs to its answer in the terms the cancelled
				// one is sent in — the same text, naming the same Release — so the
				// two differ only in that one was cancelled.
				path := w.writeFile("cancelled-same.yaml", cancellableDocument)
				fid := w.id()
				w.send(fid, "aggrega/validate",
					validateParams{File: path, Content: cancellableDocument, Release: "release.yaml"})
				first := diagnosticsOf(t, w.await(fid))
				vid := cancelledReleaseValidation(t, w, path, cancellableDocument)
				w.seam.open()
				cancelledWithoutAnswer(t, w, vid)

				did := w.id()
				w.send(did, "aggrega/diagnostics", diagnosticsParams{File: path})
				resp := w.await(did)
				if resp.Error != nil {
					t.Fatalf("a cancelled validation of unchanged text displaced the answer an earlier validation "+
						"of that text gave: refused %d (%s)", resp.Error.Code, resp.Error.Message)
				}
				if got := diagnosticsOf(t, resp); string(got) != string(first) {
					t.Fatalf("a cancelled validation of unchanged text displaced the answer an earlier validation "+
						"of that text gave:\n want %s\n got  %s", first, got)
				}
			},
		},
		{
			name: "a reader that found an answered text while a validation of changed text was cancelled",
			drive: func(t *testing.T, w *diagWire) {
				if brokenComponentYAML == cancellableDocument {
					t.Fatal("fixture defect: the two texts are the same, so the reader's lookups would read one text")
				}
				found := &validateGateSeam{}
				w.srv.testDiagnosticsFoundHook = found.hook
				path := w.writeFile("cancelled-after-lookup.yaml", brokenComponentYAML)

				// The first text runs to its answer, naming the Release the cancelled
				// validation names, so the entry it writes is a finished answer for it.
				fid := w.id()
				w.send(fid, "aggrega/validate",
					validateParams{File: path, Content: brokenComponentYAML, Release: "release.yaml"})
				diagnosticsOf(t, w.await(fid))

				// The reader: nothing is validating the file, so its first wait passes
				// straight through, its first lookup finds the answered text, and it
				// stops there, before its second wait.
				found.arm()
				did := w.id()
				w.send(did, "aggrega/diagnostics", diagnosticsParams{File: path})
				found.waitEntered(t)

				// Then a validation of the changed text, cancelled and held once it has
				// seeded the cache over the text the reader found.
				vid := cancelledReleaseValidation(t, w, path, cancellableDocument)

				// The reader's second wait catches the held validation; only then is
				// it let go, to exit cancelled.
				found.open()
				settled := settleWaiters(w, path, 1)
				w.seam.open()

				resp := w.await(did)
				cancelledWithoutAnswer(t, w, vid)
				if settled < 1 {
					t.Fatalf("fixture defect: the reader was not waiting on the cancelled validation when it was "+
						"released, so this row did not drive the interleaving it names (answer: %+v)", resp)
				}
				refusedAsNotValidated(t, resp)
			},
		},
		{
			name: "a reader that found the buffer while the cancelled validation ran",
			drive: func(t *testing.T, w *diagWire) {
				reader := &validateGateSeam{}
				w.srv.testDiagnosticsDecideHook = reader.hook
				path := w.writeFile("cancelled-under-reader.yaml", cancellableDocument)

				// The reader first: nothing is validating the file, so its first
				// wait passes straight through and it stops before its lookup.
				reader.arm()
				did := w.id()
				w.send(did, "aggrega/diagnostics", diagnosticsParams{File: path})
				reader.waitEntered(t)

				// Then the cancelled validation, held once it has seeded the cache.
				vid := cancelledReleaseValidation(t, w, path, cancellableDocument)

				// The reader's lookup finds the held validation's buffer, and its
				// second wait catches that validation; only then is it let go.
				reader.open()
				settled := settleWaiters(w, path, 1)
				w.seam.open()

				resp := w.await(did)
				cancelledWithoutAnswer(t, w, vid)
				if settled < 1 {
					t.Fatalf("fixture defect: the reader was not waiting on the cancelled validation when it was "+
						"released, so this row did not drive the interleaving it names (answer: %+v)", resp)
				}
				refusedAsNotValidated(t, resp)
			},
		},
	}
	for _, row := range rows {
		row := row
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			w := newDiagWire(t, 4, 0)
			defer w.close()
			writeRelease(t, w.root, "release.yaml", "release/only")
			row.drive(t, w)
		})
	}
}

// TestDiagnosticsWaitEndsOnCancelRequest proves the wait needs no cancellation
// mechanism of its own: it hangs off the request's per-request context, which
// $/cancelRequest already cancels.
//
// Two observables, and both are needed. The wait ends while the validation is
// still HELD, which is what makes it a mid-wait cancellation rather than a
// cancellation of nothing. And the cancelled request's ordered slot closes with
// NO body, which is what every cancelled request on this wire does — read by
// asking for a LATER request's answer, which cannot be emitted until the
// cancelled request's slot has closed.
func TestDiagnosticsWaitEndsOnCancelRequest(t *testing.T) {
	t.Parallel()
	w := newDiagWire(t, 4, 0)
	defer w.close()

	path := w.writeFile("cancelled.yaml", brokenComponentYAML)
	w.seam.arm()
	vid := w.id()
	w.send(vid, "aggrega/validate", validateParams{File: path, Content: brokenComponentYAML})
	w.seam.waitEntered(t)

	did := w.id()
	w.send(did, "aggrega/diagnostics", diagnosticsParams{File: path})
	if got := settleWaiters(w, path, 1); got < 1 {
		t.Fatalf("the reader never blocked, so there was no wait for a cancellation to end")
	}
	// The real wire path: the notification carries the id of the request to stop.
	w.notify("$/cancelRequest", map[string]any{"id": did})
	if !awaitNoWaiters(w, path) {
		t.Fatalf("the reader was still waiting %s after $/cancelRequest, while the validation "+
			"was still held; the cancellation did not reach the wait",
			validationWaitLivenessCeiling)
	}

	w.seam.open()
	if got := diagnosticsOf(t, w.await(vid)); len(got) == 0 {
		t.Fatal("the validation itself did not answer after its reader was cancelled")
	}
	// A request sent after the cancelled one: its answer sits behind the
	// cancelled request's slot in arrival order, so receiving it proves that slot
	// closed — and the cancelled id is absent from everything read on the way.
	after := w.id()
	w.send(after, "aggrega/diagnostics", diagnosticsParams{File: path})
	w.await(after)
	if resp, ok := w.pending[did]; ok {
		t.Fatalf("a cancelled diagnostics request answered anyway: %+v", resp)
	}
}

// TestDiagnosticsRefusesWhenTheWaitRunsOutOfBudget proves the deadline fires and
// returns something a client can act on, and that what it returns is a REFUSAL
// rather than an empty answer: an empty answer is indistinguishable from a file
// with nothing wrong with it, which is the outcome the wait exists to prevent.
//
// The deadline firing is observed as the waiter count reaching zero while the
// validation is still held — the answer itself cannot be read until then,
// because responses are emitted in arrival order and the validation's own slot
// comes first.
func TestDiagnosticsRefusesWhenTheWaitRunsOutOfBudget(t *testing.T) {
	t.Parallel()
	w := newDiagWire(t, 4, 40*time.Millisecond)
	defer w.close()

	path := w.writeFile("wedged.yaml", brokenComponentYAML)
	w.seam.arm()
	vid := w.id()
	w.send(vid, "aggrega/validate", validateParams{File: path, Content: brokenComponentYAML})
	w.seam.waitEntered(t)

	did := w.id()
	w.send(did, "aggrega/diagnostics", diagnosticsParams{File: path})
	settleWaiters(w, path, 1)
	if !awaitNoWaiters(w, path) {
		t.Fatalf("the reader was still waiting %s after a %s budget; the deadline did not fire",
			validationWaitLivenessCeiling, 40*time.Millisecond)
	}

	w.seam.open()
	w.await(vid)
	resp := w.await(did)
	if resp.Error == nil {
		t.Fatalf("the wait expired and the request was answered rather than refused: %s", resp.Result)
	}
	if resp.Error.Code != errValidationInFlight {
		t.Errorf("refusal code = %d; want %d (a validation is running)",
			resp.Error.Code, errValidationInFlight)
	}
	if resp.Error.Code == errFileNotValidated {
		t.Error("the refusal reported not-validated for a file being validated right now")
	}
	for _, want := range []string{path, "still running"} {
		if !strings.Contains(resp.Error.Message, want) {
			t.Errorf("refusal message %q does not name %q", resp.Error.Message, want)
		}
	}
}

// TestDiagnosticsStillRefusesAnUnvalidatedFile is the negative control the
// narrowed refusal rests on. With no validation run and none in flight there is
// nothing to wait for, so the wait must pass straight through to the
// not-validated refusal rather than absorbing it.
func TestDiagnosticsStillRefusesAnUnvalidatedFile(t *testing.T) {
	t.Parallel()
	w := newDiagWire(t, 4, 0)
	defer w.close()

	for i := 0; i < 12; i++ {
		path := w.writeFile(fmt.Sprintf("never-validated-%d.yaml", i), brokenComponentYAML)
		did := w.id()
		started := time.Now()
		w.send(did, "aggrega/diagnostics", diagnosticsParams{File: path})
		resp := w.await(did)
		if resp.Error == nil {
			t.Fatalf("iteration %d: an unvalidated file was answered rather than refused: %s",
				i, resp.Result)
		}
		if resp.Error.Code != errFileNotValidated {
			t.Fatalf("iteration %d: refusal code = %d; want %d (nothing has validated it)",
				i, resp.Error.Code, errFileNotValidated)
		}
		// Not a performance budget: it separates "passed straight through" from
		// "waited out a budget it had no business waiting" — a difference of two
		// orders of magnitude, which contention cannot blur.
		if elapsed := time.Since(started); elapsed > validationWaitLivenessCeiling {
			t.Fatalf("iteration %d: the refusal took %s, so the request waited for a "+
				"validation that was never in flight", i, elapsed)
		}
	}
}

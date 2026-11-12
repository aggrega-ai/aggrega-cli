// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

// The proofs here drive a real listener over a real TCP connection and read the
// response as it arrives, one frame at a time, rather than after it is over.
// That is the whole subject: a response that arrives all at once is exactly what
// this is meant to stop happening to a call that takes seconds, and a helper
// that read the body to completion first would make every proof here pass on a
// server that streamed nothing.
//
// # The resolution is a seam, and why
//
// The real dependency resolution reports its stages, but it reports them as
// fast as the fixture workspace resolves — which is to say all at once — and it
// cannot be asked to stall. Both properties under test here are about time: a
// report reaching the caller BEFORE the work is over, and an idle stream being
// kept alive. So the resolution is replaced by a seam the proof drives, and what
// stays real is everything either side of it: the listener, the admission path,
// the tool, the reporter, the stream and the record.

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/pullops"
)

// idleKeepAlive is the interval the keep-alive proof runs at. It is short
// because that proof is about a stream that sat idle, and nothing else here is
// about the interval at all: every other proof runs at the production value, so
// a stray comment line cannot be mistaken for the event it was asserting on.
const idleKeepAlive = 20 * time.Millisecond

// stepwiseResolution is a dependency resolution that reports the stages a proof
// asks for, and waits where the proof asks it to wait.
//
// It records what it observed rather than asserting on it: it runs on the
// listener's goroutine, and the testing package's failure calls belong to the
// test's own.
type stepwiseResolution struct {
	// stepsBeforeRelease and stepsAfterRelease are how many stages are reported
	// either side of the wait.
	stepsBeforeRelease int
	stepsAfterRelease  int

	// stepsAfterContextEnds is how many stages are reported once the context has
	// ended, which is the plant the "nothing afterwards" assertion needs: a
	// reporter that carried on would have something to carry.
	stepsAfterContextEnds int

	// waitForRelease holds the run between the two groups above. A nil channel
	// is a run that never waits.
	waitForRelease chan struct{}

	// entered is closed once the first group has been reported, so a proof knows
	// the stream has had something put on it.
	entered chan struct{}

	// ended carries what the run observed on its way out. It travels on a channel
	// rather than in a field because the run is on the listener's goroutine and
	// the proof reads it from its own; a field would be read across no
	// synchronisation at all.
	ended chan resolutionOutcome
}

// resolutionOutcome is what the seam observed: the cause its context held when
// it left, and whether the tool supplied the reporting callback at all.
//
// A nil cause is a reading rather than an absence of one, which is what lets a
// proof assert that nothing interrupted a call. The callback observation is what
// separates "the reporter wrote nothing" from "there was no reporter".
type resolutionOutcome struct {
	cause       error
	sawReporter bool
}

// run is the resolution itself, in the shared operation's own signature.
func (r *stepwiseResolution) run(ctx context.Context, cfg pullops.Config) (pullops.Result, error) {
	sawReporter := cfg.Report.Phase != nil
	report := func() {
		if cfg.Report.Phase != nil {
			cfg.Report.Phase(pullops.PhaseLoadCatalog)
		}
	}
	for range r.stepsBeforeRelease {
		report()
	}
	close(r.entered)
	if r.waitForRelease != nil {
		select {
		case <-r.waitForRelease:
		case <-ctx.Done():
		}
	}
	for range r.stepsAfterContextEnds {
		if ctx.Err() != nil {
			report()
		}
	}
	for range r.stepsAfterRelease {
		report()
	}
	r.ended <- resolutionOutcome{cause: ctx.Err(), sawReporter: sawReporter}
	return pullops.Result{}, ctx.Err()
}

// newStepwiseResolution prepares a seam with its channels made.
func newStepwiseResolution(before, after int) *stepwiseResolution {
	return &stepwiseResolution{
		stepsBeforeRelease: before,
		stepsAfterRelease:  after,
		entered:            make(chan struct{}),
		ended:              make(chan resolutionOutcome, 1),
	}
}

// awaitOutcome reads back what the seam observed, within the liveness ceiling.
func (r *stepwiseResolution) awaitOutcome(t *testing.T) resolutionOutcome {
	t.Helper()
	select {
	case got := <-r.ended:
		return got
	case <-time.After(runReturnCeiling):
		t.Fatal("the resolution never left within the liveness ceiling")
		return resolutionOutcome{}
	}
}

// countingWriter counts what reached the connection, so a proof can ask whether
// anything was written after a mark it took.
//
// It unwraps to the response writer beneath it, which is what keeps the
// stream's own flush working: the controller that pushes an event past the
// buffering follows Unwrap to find the writer that can do it.
type countingWriter struct {
	http.ResponseWriter
	mu      sync.Mutex
	written int
}

func (c *countingWriter) Write(b []byte) (int, error) {
	n, err := c.ResponseWriter.Write(b)
	c.mu.Lock()
	c.written += n
	c.mu.Unlock()
	return n, err
}

func (c *countingWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (c *countingWriter) bytesWritten() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.written
}

// progressFixture is one running listener over a workspace the resolution tool
// can reach, with the resolution replaced by the seam the proof drives and
// every byte that reaches the connection counted.
//
// # Why the workspace is the one the process was started over
//
// This transport serves a single workspace, fixed when the process starts, and
// refuses a call that names another: the workspace-selecting arguments are
// refused outright, and a path argument is resolved relative to that root and
// nowhere else. So the fixture points the server AT the workspace and every call
// names its Release by a relative path — which is the shape a caller of a
// listener actually sends, and the reason the arguments a proof over the tool's
// own invoke would pass are absent here.
type progressFixture struct {
	ts  *httptest.Server
	log *lockedBuffer

	// mu guards counted, which the listener's goroutine writes when a request
	// arrives and a proof reads from its own once that request is over.
	mu      sync.Mutex
	counted *countingWriter
}

// newProgressFixture starts that listener. keepAlive is the interval the stream
// runs at, which every proof but the idle one leaves at the production value.
func newProgressFixture(t *testing.T, keepAlive time.Duration, resolve dependencyResolver) *progressFixture {
	t.Helper()
	s, root, _, _ := pullWorkspace(t)
	s.launchRoot = root
	log := &lockedBuffer{}
	s.logw = log
	s.resolveDependencies = resolve
	h, err := newHTTPHandler(context.Background(), s, defaultTransportConfig())
	if err != nil {
		t.Fatalf("newHTTPHandler: %v", err)
	}
	h.auth = acceptAllAuthenticator{}
	h.progressKeepAlive = keepAlive
	f := &progressFixture{log: log}
	f.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counted := &countingWriter{ResponseWriter: w}
		f.mu.Lock()
		f.counted = counted
		f.mu.Unlock()
		h.ServeHTTP(counted, r)
	}))
	t.Cleanup(f.ts.Close)
	return f
}

// bytesWritten is how much has reached the connection of the request this
// fixture last answered.
func (f *progressFixture) bytesWritten() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.counted == nil {
		return 0
	}
	return f.counted.bytesWritten()
}

// callBody composes one tools/call in the handshake revision, carrying the
// progress token when one is given. The handshake revision asks for no mirror
// headers, and no proof here is about the headers.
func (f *progressFixture) callBody(t *testing.T, tool string, arguments map[string]any, token any) string {
	t.Helper()
	params := map[string]any{"name": tool, "arguments": arguments}
	if token != nil {
		params["_meta"] = map[string]any{metaKeyProgressToken: token}
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params,
	})
	if err != nil {
		t.Fatalf("compose tools/call: %v", err)
	}
	return string(body)
}

// fixtureRelease is the Release the fixture workspace carries, named the way a
// caller of a listener names it: relative to the one root the process serves.
const fixtureRelease = "release.yaml"

// pullArguments is the resolution tool's arguments against this fixture's
// workspace.
func (f *progressFixture) pullArguments() map[string]any {
	return map[string]any{"release": fixtureRelease}
}

// sseFrame is one line of substance off the stream: either an event's payload
// or a comment. The blank separators are consumed and never reported, because
// nothing here asserts on them beyond their framing role.
type sseFrame struct {
	data    string
	comment bool
}

// openStream posts one body and hands back the response with a reader parked on
// its body, having read no further than the headers.
//
// The frames are pumped onto a channel by a goroutine of its own, so a proof
// reads what has arrived so far without committing to the body being over — and
// so every read carries the liveness ceiling rather than hanging the suite.
type openStream struct {
	resp   *http.Response
	frames chan sseFrame
	closed chan struct{}
}

func (f *progressFixture) open(t *testing.T, body string) *openStream {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.ts.URL+endpointPath, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	s := &openStream{resp: resp, frames: make(chan sseFrame, 64), closed: make(chan struct{})}
	go s.pump()
	t.Cleanup(s.close)
	return s
}

// pump reads frames until the body ends, closing the channel so a reader can
// tell "nothing yet" from "nothing ever".
func (s *openStream) pump() {
	defer close(s.frames)
	reader := bufio.NewReader(s.resp.Body)
	for {
		line, err := reader.ReadString('\n')
		trimmed := strings.TrimRight(line, "\n")
		switch {
		case trimmed == "":
		case strings.HasPrefix(trimmed, ":"):
			s.frames <- sseFrame{comment: true, data: trimmed}
		case strings.HasPrefix(trimmed, "data: "):
			s.frames <- sseFrame{data: strings.TrimPrefix(trimmed, "data: ")}
		default:
			s.frames <- sseFrame{data: trimmed}
		}
		if err != nil {
			return
		}
	}
}

// next returns the next frame within the liveness ceiling, reporting whether
// the stream ended instead.
func (s *openStream) next(t *testing.T, what string) (sseFrame, bool) {
	t.Helper()
	select {
	case frame, more := <-s.frames:
		return frame, more
	case <-time.After(runReturnCeiling):
		t.Fatalf("%s never arrived within the liveness ceiling", what)
		return sseFrame{}, false
	}
}

// close ends the exchange from the client's side, which is what a caller
// walking away looks like to the listener.
func (s *openStream) close() {
	select {
	case <-s.closed:
		return
	default:
	}
	close(s.closed)
	_ = s.resp.Body.Close()
}

// drain reads the rest of the stream and returns every frame on it.
func (s *openStream) drain(t *testing.T) []sseFrame {
	t.Helper()
	var out []sseFrame
	for {
		frame, more := s.next(t, "the rest of the stream")
		if !more {
			return out
		}
		out = append(out, frame)
	}
}

// awaitRecord waits for the request's record to reach the collected stream,
// within the liveness ceiling.
//
// The record is written as the handler returns, which is after the client has
// seen the last event, so a proof reading the stream straight away can arrive
// first. The wait separates "the handler finished" from "it never will" and is
// the only thing a clock decides in these proofs beyond the keep-alive.
func awaitRecord(t *testing.T, log *lockedBuffer) {
	t.Helper()
	deadline := time.Now().Add(runReturnCeiling)
	for time.Now().Before(deadline) {
		if strings.Contains(log.String(), `"outcome"`) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the request was never recorded within the liveness ceiling:\n%s", log.String())
}

// progressValues reads the progress counts off the notification frames, failing
// on any frame that names a different method or a different token.
//
// The token is compared against what the request sent rather than merely being
// required to be present: a notification correlates to the call by the caller's
// own value, and one carrying anything else would be routed to a call the
// caller never made.
func progressValues(t *testing.T, frames []sseFrame, wantToken string) []uint64 {
	t.Helper()
	var out []uint64
	for _, frame := range frames {
		if frame.comment {
			continue
		}
		var note struct {
			Method string `json:"method"`
			Params struct {
				ProgressToken json.RawMessage `json:"progressToken"`
				Progress      uint64          `json:"progress"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(frame.data), &note); err != nil {
			t.Fatalf("a frame on the stream is not a JSON object: %q: %v", frame.data, err)
		}
		if note.Method != progressMethod {
			continue
		}
		if got := string(note.Params.ProgressToken); got != wantToken {
			t.Errorf("a progress notification names token %s; the request minted %s", got, wantToken)
		}
		out = append(out, note.Params.Progress)
	}
	return out
}

// finalResponse reads the last frame as the JSON-RPC response, failing when the
// stream's last event is anything else.
func finalResponse(t *testing.T, frames []sseFrame) rpcResponse {
	t.Helper()
	if len(frames) == 0 {
		t.Fatal("the stream carried no frames at all")
	}
	last := frames[len(frames)-1]
	if last.comment {
		t.Fatalf("the stream's last frame is a comment, not the response: %q", last.data)
	}
	var resp rpcResponse
	if err := json.Unmarshal([]byte(last.data), &resp); err != nil {
		t.Fatalf("the stream's last frame is not a JSON-RPC frame: %q: %v", last.data, err)
	}
	if len(resp.ID) == 0 {
		t.Fatalf("the stream's last frame carries no id, so it is not the response: %q", last.data)
	}
	return resp
}

// TestATokenBearingCallIsAnsweredWithAnEventStream proves the headers, the
// notifications, the strictly increasing count, the response as the last event,
// and the close.
//
// The count is asserted as STRICTLY increasing rather than as any particular
// sequence. The protocol requires only that it rise, and pinning the values
// would tie this proof to how many stages a resolution happens to report.
func TestATokenBearingCallIsAnsweredWithAnEventStream(t *testing.T) {
	seam := newStepwiseResolution(3, 0)
	f := newProgressFixture(t, progressKeepAlive, seam.run)

	stream := f.open(t, f.callBody(t, "pull", f.pullArguments(), "token-alpha"))

	if got := stream.resp.Header.Get(headerContentType); got != contentTypeEventStream {
		t.Fatalf("the response carries content type %q; want %q — a caller reading it as a stream "+
			"would not know to", got, contentTypeEventStream)
	}
	if got := stream.resp.Header.Get(headerAccelBuffering); got != valueBufferingDisable {
		t.Errorf("the response carries %s: %q; want %q — a proxy in front of this deployment would "+
			"buffer every event until the work was over, which is the whole thing the stream avoids",
			headerAccelBuffering, got, valueBufferingDisable)
	}

	frames := stream.drain(t)
	values := progressValues(t, frames, `"token-alpha"`)
	if len(values) == 0 {
		t.Fatalf("the stream carried no progress notification at all: %v", frames)
	}
	for i := 1; i < len(values); i++ {
		if values[i] <= values[i-1] {
			t.Fatalf("the progress count did not rise: %v. The protocol requires it to increase with "+
				"every notification, so a client tracking it reads the work going backwards.", values)
		}
	}

	resp := finalResponse(t, frames)
	if len(resp.Result) == 0 && resp.Error == nil {
		t.Errorf("the stream's last event carries neither a result nor an error: %+v", resp)
	}
	if !seam.awaitOutcome(t).sawReporter {
		t.Error("the tool supplied no progress callback to the resolution, so what the stream carried " +
			"did not come from the work reporting")
	}
}

// TestACallWithNoTokenIsAnsweredWithOneObject proves a tokenless call is not streamed.
//
// The content type is the discriminator rather than the absence of
// notifications: a stream that opened and carried nothing would satisfy
// "no notifications" while having changed the shape of every answer.
func TestACallWithNoTokenIsAnsweredWithOneObject(t *testing.T) {
	seam := newStepwiseResolution(3, 0)
	f := newProgressFixture(t, progressKeepAlive, seam.run)

	stream := f.open(t, f.callBody(t, "pull", f.pullArguments(), nil))

	if got := stream.resp.Header.Get(headerContentType); got != contentTypeJSON {
		t.Fatalf("a call that minted no token was answered with content type %q; want %q", got, contentTypeJSON)
	}
	frames := stream.drain(t)
	if values := progressValues(t, frames, ""); len(values) != 0 {
		t.Errorf("a call that minted no token was sent %d progress notifications", len(values))
	}
}

// TestTheFillingBuildStreamsAndTheRenderingOneDoesNot proves a token streams a build only where it fills a lockfile.
//
// The two rows are one proof because what separates them is the whole point: a
// design that streamed whenever a token was present would pass the first row and
// fail the second, and a reader seeing only the first would have no way to tell.
func TestTheFillingBuildStreamsAndTheRenderingOneDoesNot(t *testing.T) {
	t.Run("a build asked to fill its lockfile streams the fill", func(t *testing.T) {
		seam := newStepwiseResolution(2, 0)
		f := newProgressFixture(t, progressKeepAlive, seam.run)

		stream := f.open(t, f.callBody(t, "build", map[string]any{
			"release": fixtureRelease, "auto_pull": true,
		}, "token-build"))

		if got := stream.resp.Header.Get(headerContentType); got != contentTypeEventStream {
			t.Fatalf("a filling build carrying a token was answered with content type %q; want %q",
				got, contentTypeEventStream)
		}
		frames := stream.drain(t)
		if values := progressValues(t, frames, `"token-build"`); len(values) == 0 {
			t.Errorf("a filling build carrying a token reported no progress: %v", frames)
		}
		finalResponse(t, frames)
	})

	t.Run("a build that fills nothing is answered with one object", func(t *testing.T) {
		seam := newStepwiseResolution(2, 0)
		f := newProgressFixture(t, progressKeepAlive, seam.run)

		stream := f.open(t, f.callBody(t, "build", map[string]any{
			"release": fixtureRelease,
		}, "token-build"))

		if got := stream.resp.Header.Get(headerContentType); got != contentTypeJSON {
			t.Fatalf("a build that fills nothing was answered with content type %q; want %q — nothing "+
				"reports progress on a render, so a token alone must not change the answer's shape",
				got, contentTypeJSON)
		}
		select {
		case <-seam.entered:
			t.Error("a build that was not asked to fill its lockfile reached the resolution anyway")
		default:
		}
	})
}

// TestAnIdleStreamIsKeptAlive proves an idle stream writes a comment line before its next event.
//
// The only clock here is the liveness ceiling. The proof does not measure how
// long the comment took to arrive; it reads frames until one arrives, and fails
// if the next EVENT arrives first — which is the property, stated without a
// duration.
func TestAnIdleStreamIsKeptAlive(t *testing.T) {
	seam := newStepwiseResolution(1, 1)
	seam.waitForRelease = make(chan struct{})
	f := newProgressFixture(t, idleKeepAlive, seam.run)

	stream := f.open(t, f.callBody(t, "pull", f.pullArguments(), "token-idle"))

	first, more := stream.next(t, "the first progress notification")
	if !more || first.comment {
		t.Fatalf("the stream opened with %+v rather than with a progress notification", first)
	}

	next, more := stream.next(t, "the frame after the stream went idle")
	if !more {
		t.Fatal("the stream ended while the work was still running")
	}
	if !next.comment {
		t.Fatalf("the stream sat idle and the next frame was an event rather than a comment line: %q.\n"+
			"An idle stream writes nothing, and an intermediary between this server and its caller "+
			"drops a connection it sees nothing on.", next.data)
	}

	close(seam.waitForRelease)
	stream.drain(t)
}

// TestAMidStreamDisconnectStopsTheWorkAndTheWriting proves a disconnect ends the work and every write after it.
//
// Two things are asserted and they fail independently. The seam reports what its
// context held when it left, which is where "the resolution's context ended"
// is read. The bytes that reached the connection are counted, which is where
// "nothing afterwards" is read — and the seam deliberately reports three more
// stages once its context has ended, so a reporter that carried on would have
// something to write. Without that plant the absence would be a property of
// there being nothing left to say.
func TestAMidStreamDisconnectStopsTheWorkAndTheWriting(t *testing.T) {
	seam := newStepwiseResolution(1, 0)
	seam.waitForRelease = make(chan struct{})
	seam.stepsAfterContextEnds = 3
	f := newProgressFixture(t, progressKeepAlive, seam.run)

	stream := f.open(t, f.callBody(t, "pull", f.pullArguments(), "token-gone"))

	first, more := stream.next(t, "the first progress notification")
	if !more || first.comment {
		t.Fatalf("the stream opened with %+v rather than with a progress notification", first)
	}
	mark := f.bytesWritten()
	stream.close()

	if seam.awaitOutcome(t).cause == nil {
		t.Error("the caller disconnected and the resolution's context had not ended, so the work " +
			"carried on past the point the caller gave up")
	}

	// The handler is still returning when the seam reports, so the record is
	// waited for rather than read straight away. The ceiling separates "the
	// handler finished" from "it never will" and measures nothing else.
	awaitRecord(t, f.log)
	if after := f.bytesWritten() - mark; after != 0 {
		t.Errorf("%d bytes reached the connection after the caller disconnected; want 0", after)
	}
	line := oneAuditLine(t, f.log.String())
	if got := auditString(t, line, "outcome"); got != outcomeCancelled {
		t.Errorf("the record reports outcome %q for a caller that disconnected mid-stream; want %q",
			got, outcomeCancelled)
	}
}

// TestTheRecordOfAStreamedCallIsWrittenWhenTheStreamCloses is the audit seam
// this item moves: turning a response into a stream moves what "after the
// response" means, and a record written when the stream OPENED would report a
// duration that ended before the work did.
//
// It is asserted without a clock. The seam is held open, the proof reads the
// first event off the wire, and the record is asserted ABSENT at that moment;
// the seam is then released and the record asserted present. A duration taken
// at the wrong end would have produced the line during the hold.
func TestTheRecordOfAStreamedCallIsWrittenWhenTheStreamCloses(t *testing.T) {
	seam := newStepwiseResolution(1, 1)
	seam.waitForRelease = make(chan struct{})
	f := newProgressFixture(t, progressKeepAlive, seam.run)

	stream := f.open(t, f.callBody(t, "pull", f.pullArguments(), "token-record"))

	if first, more := stream.next(t, "the first progress notification"); !more || first.comment {
		t.Fatalf("the stream opened with %+v rather than with a progress notification", first)
	}
	if written := f.log.String(); strings.Contains(written, `"outcome"`) {
		t.Fatalf("the request was recorded while its stream was still open:\n%s\n"+
			"The duration on that line ends before the work does.", written)
	}

	close(seam.waitForRelease)
	stream.drain(t)

	awaitRecord(t, f.log)
	line := oneAuditLine(t, f.log.String())
	if got := auditString(t, line, "outcome"); got != outcomeOK {
		t.Errorf("the record reports outcome %q for a streamed call that completed; want %q", got, outcomeOK)
	}
}

// TestAMalformedProgressTokenIsRefused pins what a token of the wrong shape
// earns, in both directions: a refusal for a shape the protocol does not admit,
// and an answer for each shape it does.
//
// The positive rows are what makes the refusal a measurement rather than a
// property of the reader refusing everything.
func TestAMalformedProgressTokenIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name    string
		token   any
		refused bool
	}{
		{name: "a string token is admitted", token: "token-string"},
		{name: "a number token is admitted", token: 42},
		{name: "a boolean token is refused", token: true, refused: true},
		{name: "an object token is refused", token: map[string]any{"id": 1}, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seam := newStepwiseResolution(1, 0)
			f := newProgressFixture(t, progressKeepAlive, seam.run)

			stream := f.open(t, f.callBody(t, "pull", f.pullArguments(), tc.token))
			frames := stream.drain(t)
			resp := finalResponse(t, frames)

			if tc.refused {
				if resp.Error == nil {
					t.Fatalf("a token of this shape was accepted; the caller is left watching for a " +
						"stream that never arrives")
				}
				if !strings.Contains(resp.Error.Message, metaKeyProgressToken) {
					t.Errorf("the refusal does not name the field it is about: %q", resp.Error.Message)
				}
				return
			}
			if resp.Error != nil {
				t.Fatalf("a token of a shape the protocol admits was refused: %+v", resp.Error)
			}
		})
	}
}

// TestNothingIsReportedOnceTheRequestIsOver is the reporter's own half of the
// "nothing afterwards" rule, measured where it can be counted exactly.
//
// The positive control is the needle: the same reporter, with a live context,
// puts a notification on the sink. Without it a zero would be a claim about the
// sink rather than about the reporter.
func TestNothingIsReportedOnceTheRequestIsOver(t *testing.T) {
	var sent []progressNotification
	sink := func(note progressNotification) { sent = append(sent, note) }

	ctx, cancel := context.WithCancel(t.Context())
	reporter := &progressReporter{ctx: ctx, token: json.RawMessage(`"token"`), sink: sink}

	reporter.step()
	if len(sent) != 1 {
		t.Fatalf("a live reporter put %d notifications on the sink; want 1. Nothing below measures "+
			"the reporter until this one does.", len(sent))
	}

	cancel()
	reporter.step()
	reporter.step()
	if len(sent) != 1 {
		t.Errorf("a reporter whose request had ended wrote %d notifications; want none after the first",
			len(sent)-1)
	}
}

// TestTheProgressCountRisesAcrossEveryReport is the unit-level statement of the
// property TestATokenBearingCallIsAnsweredWithAnEventStream reads off the wire:
// it fails on a count that repeats as well as on one that falls.
func TestTheProgressCountRisesAcrossEveryReport(t *testing.T) {
	var sent []uint64
	reporter := &progressReporter{
		ctx:   t.Context(),
		token: json.RawMessage(`"token"`),
		sink:  func(note progressNotification) { sent = append(sent, note.Progress) },
	}
	for range 5 {
		reporter.step()
	}
	if len(sent) != 5 {
		t.Fatalf("five reports produced %d notifications", len(sent))
	}
	for i := 1; i < len(sent); i++ {
		if sent[i] <= sent[i-1] {
			t.Fatalf("the count did not rise across every report: %v", sent)
		}
	}
}

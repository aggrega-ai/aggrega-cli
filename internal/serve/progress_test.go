// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The progress proofs are driven through dispatchLoop over real
// Content-Length-framed stdin and stdout rather than by calling a handler, and
// that is the point rather than thoroughness: a token installed on a context and
// never pushed satisfies any assertion over server state and changes nothing a
// client sees. So every claim here is asserted over the frames that left the
// daemon.

// wireFrame is whichever of the two shapes a frame off the daemon's stdout
// carries: a response to a request, or a server-pushed notification. Both are
// decoded into one struct so a test can classify a stream of frames without
// knowing in advance which it is holding.
type wireFrame struct {
	Method string          `json:"method"`
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *jsonRPCError   `json:"error"`
	Params json.RawMessage `json:"params"`
}

// progressFramePayload is the params of one progress notification, decoded far
// enough to assert on.
type progressFramePayload struct {
	Token json.RawMessage `json:"token"`
	Value struct {
		Kind    string `json:"kind"`
		Title   string `json:"title"`
		Message string `json:"message"`
	} `json:"value"`
}

// readAllFrames reads every Content-Length-framed message off out until the
// stream is exhausted, returning the bodies verbatim.
func readAllFrames(t *testing.T, out *bytes.Buffer) [][]byte {
	t.Helper()
	r := bufio.NewReader(out)
	var frames [][]byte
	for {
		body, err := readMessage(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return frames
			}
			t.Fatalf("read frame %d: %v", len(frames)+1, err)
		}
		frames = append(frames, body)
	}
}

// sessionRequest is one request in a driven session, paired with the id its
// response will carry.
type sessionRequest struct {
	id    int
	frame []byte
}

// send pairs a framed request with its id, for driveSession.
func send(id int, frame []byte) sessionRequest {
	return sessionRequest{id: id, frame: frame}
}

// driveSession runs the production dispatch loop over real pipes and exchanges
// with it the way a client does: one request at a time, each written only after
// the previous one's response has been read. It returns every frame the daemon
// wrote, in order.
//
// 🔴 The one-at-a-time exchange is load-bearing rather than tidy, and writing
// the whole script up front instead measured the wrong thing. The worker pool is
// four wide by default, so a request written before the handshake's response has
// been read can be dispatched CONCURRENTLY with the handshake that negotiates
// the capability it depends on — and a session that races its own handshake is
// not a session a client runs. The protocol already forbids it: a client sends
// no request until the handshake has answered, which is what makes a capability
// read after the handshake well-defined. The same ordering governs the position
// encoding, and has all along.
func driveSession(t *testing.T, srv *Server, requests ...sessionRequest) []wireFrame {
	t.Helper()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv.in = bufio.NewReader(inR)
	srv.out = outW

	loopDone := make(chan error, 1)
	go func() { loopDone <- srv.dispatchLoop(500 * time.Millisecond) }()

	framesCh := make(chan wireFrame, 256)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		r := bufio.NewReader(outR)
		for {
			body, err := readMessage(r)
			if err != nil {
				return
			}
			var f wireFrame
			if err := json.Unmarshal(body, &f); err != nil {
				return
			}
			framesCh <- f
		}
	}()

	var collected []wireFrame
	awaitResponse := func(id string) {
		timeout := time.After(30 * time.Second)
		for {
			select {
			case f := <-framesCh:
				collected = append(collected, f)
				if f.Method == "" && string(f.ID) == id {
					return
				}
			case <-timeout:
				t.Fatalf("timed out waiting for the response to id %s; collected %d frames", id, len(collected))
			}
		}
	}

	for _, req := range requests {
		if _, err := inW.Write(req.frame); err != nil {
			t.Fatalf("write request %d: %v", req.id, err)
		}
		awaitResponse(strconv.Itoa(req.id))
	}
	if _, err := inW.Write(framedNotification("aggrega/shutdown", nil)); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}
	_ = inW.Close()
	if err := <-loopDone; err != nil {
		t.Fatalf("dispatchLoop: %v", err)
	}
	_ = outW.Close()
	<-readerDone
	for {
		select {
		case f := <-framesCh:
			collected = append(collected, f)
		default:
			return collected
		}
	}
}

// progressPayloads returns the decoded payload of every progress notification
// among frames, in the order they were written.
func progressPayloads(t *testing.T, frames []wireFrame) []progressFramePayload {
	t.Helper()
	var out []progressFramePayload
	for _, f := range frames {
		if f.Method != progressMethod {
			continue
		}
		var p progressFramePayload
		if err := json.Unmarshal(f.Params, &p); err != nil {
			t.Fatalf("decode progress params: %v (raw %s)", err, f.Params)
		}
		out = append(out, p)
	}
	return out
}

// responseTo returns the response frame carrying the given id, failing when
// none did.
func responseTo(t *testing.T, frames []wireFrame, id string) wireFrame {
	t.Helper()
	for _, f := range frames {
		if f.Method == "" && string(f.ID) == id {
			return f
		}
	}
	t.Fatalf("no response frame for id %s among %d frames", id, len(frames))
	return wireFrame{}
}

// negotiatingClient is the initialize request a client that can handle
// work-in-progress notifications sends.
func negotiatingClient(id int) sessionRequest {
	return send(id, framedRequest(id, "aggrega/initialize", map[string]any{
		"capabilities": map[string]any{
			"window": map[string]any{"workDoneProgress": true},
		},
	}))
}

// silentClient is the initialize request a client that declares only the
// position-encoding negotiation sends — the one shape the reference client sends
// today.
func silentClient(id int) sessionRequest {
	return send(id, framedRequest(id, "aggrega/initialize", map[string]any{
		"capabilities": map[string]any{
			"general": map[string]any{"positionEncodings": []string{"utf-8", "utf-16"}},
		},
	}))
}

// dryRunArtifact is the request every proof here reports on: a data-artifact
// publication in dry-run, which is the one lit operation that can be driven to
// completion with no destination and no network — it computes the bundle,
// writes its own three status lines, and returns before a remote target is
// constructed.
func dryRunArtifact(id int, extra map[string]any) sessionRequest {
	params := map[string]any{
		"file":   "bundle.txt",
		"name":   "handbook",
		"tag":    "2.0.0",
		"dryRun": true,
	}
	for k, v := range extra {
		params[k] = v
	}
	return send(id, framedRequest(id, "aggrega/publishArtifact", params))
}

// The three lines the dry-run publication writes, spelled out here rather than
// derived from the operation that produces them.
//
// 🔴 That is deliberate and it is the whole value of the assertion: an
// expectation computed by calling the same code the daemon calls would agree
// with a defect on both sides and prove nothing. The registry and the artifact
// name are this test's own inputs, and the byte count is the length of the
// fixture's own file content — so every field is known from the test side.
const (
	dryRunHeadLine = "aggrega push-artifact (dry-run): " + publishTestRegistry + "/docs/handbook:2.0.0"
	dryRunSizeLine = "size: 27 bytes"
)

// TestProgressReachesTheWireCorrelatedByTheCallersToken is the transport proof,
// asserted over the frames the daemon wrote rather than over anything it holds.
//
// It pins four things a client author depends on: the notification is the
// protocol-standard method; the token it carries is the caller's own value
// rather than the request's id; the payloads arrive as one begin, then the
// operation's own lines, then one end; and the response still arrives after
// them.
func TestProgressReachesTheWireCorrelatedByTheCallersToken(t *testing.T) {
	t.Parallel()
	srv, _ := publishFixture(t, defaultRegistryWorkspace)

	frames := driveSession(t, srv,
		negotiatingClient(1),
		dryRunArtifact(2, map[string]any{"workDoneToken": "handbook-publish"}),
	)

	payloads := progressPayloads(t, frames)
	if len(payloads) != 5 {
		t.Fatalf("want one begin, three reports and one end; got %d progress frames: %+v", len(payloads), payloads)
	}
	for i, p := range payloads {
		if got := string(p.Token); got != `"handbook-publish"` {
			t.Errorf("progress frame %d must carry the caller's own token; got %s", i+1, got)
		}
	}
	if payloads[0].Value.Kind != progressKindBegin {
		t.Errorf("the first payload must open the report; got kind %q", payloads[0].Value.Kind)
	}
	if payloads[0].Value.Title == "" {
		t.Error("a begin must say what is happening")
	}
	if last := payloads[len(payloads)-1]; last.Value.Kind != progressKindEnd {
		t.Errorf("the last payload must close the report; got kind %q", last.Value.Kind)
	}

	// The reported text is the operation's own, split at its newlines with the
	// terminal indentation trimmed. Compared against literals the test owns.
	var reported []string
	for _, p := range payloads[1 : len(payloads)-1] {
		if p.Value.Kind != progressKindReport {
			t.Fatalf("a payload between begin and end must be a report; got kind %q", p.Value.Kind)
		}
		reported = append(reported, p.Value.Message)
	}
	if len(reported) != 3 || reported[0] != dryRunHeadLine || reported[2] != dryRunSizeLine {
		t.Errorf("the reports must carry the operation's own lines verbatim; got %q", reported)
	}
	if !strings.HasPrefix(reported[1], "mediatype: ") || reported[1] == "mediatype: " {
		t.Errorf("the middle report must carry the media-type line with its indentation trimmed; got %q", reported[1])
	}

	// The response arrives too, and the progress frames precede it: an ordered
	// response slot is emitted after the handler returns, and a notification on
	// receipt.
	resp := responseTo(t, frames, "2")
	if resp.Error != nil {
		t.Fatalf("the publication must still answer; got error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	var res publishArtifactResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("unmarshal result: %v (raw %s)", err, resp.Result)
	}
	if !res.DryRun || res.Reach.BeyondThisMachine {
		t.Errorf("reporting progress must not change what the operation did; got dryRun=%v reach=%+v", res.DryRun, res.Reach)
	}
	lastProgress, response := -1, -1
	for i, f := range frames {
		if f.Method == progressMethod {
			lastProgress = i
		}
		if f.Method == "" && string(f.ID) == "2" {
			response = i
		}
	}
	if lastProgress > response {
		t.Errorf("every progress frame belongs before the response it reports on; last progress at %d, response at %d", lastProgress, response)
	}
}

// TestProgressTokenMayBeANumber pins the other half of the token's admissible
// shape, and pins it as bytes: a caller that mints a numeric token gets that
// number back, not a string spelling of it.
func TestProgressTokenMayBeANumber(t *testing.T) {
	t.Parallel()
	srv, _ := publishFixture(t, defaultRegistryWorkspace)

	frames := driveSession(t, srv,
		negotiatingClient(1),
		dryRunArtifact(2, map[string]any{"workDoneToken": 7}),
	)

	payloads := progressPayloads(t, frames)
	if len(payloads) == 0 {
		t.Fatal("a numeric token must be honoured")
	}
	for i, p := range payloads {
		if got := string(p.Token); got != "7" {
			t.Errorf("progress frame %d must echo the numeric token unquoted; got %s", i+1, got)
		}
	}
}

// TestAClientThatNegotiatesNoTokenIsUnaffected is the first of the two
// unchanged-behaviour proofs, and it is asserted the only way it can be: over
// the frames.
//
// Two sessions publish the same artifact — one declaring nothing, one declaring
// the capability but sending no token — and both must produce no notification at
// all and the same answer. A capability nobody asked for must not become a
// requirement, and a client that never sends a token must not be able to tell
// the mechanism exists.
func TestAClientThatNegotiatesNoTokenIsUnaffected(t *testing.T) {
	t.Parallel()

	silentSrv, _ := publishFixture(t, defaultRegistryWorkspace)
	silent := driveSession(t, silentSrv, silentClient(1), dryRunArtifact(2, nil))

	declaredSrv, _ := publishFixture(t, defaultRegistryWorkspace)
	declared := driveSession(t, declaredSrv, negotiatingClient(1), dryRunArtifact(2, nil))

	for label, frames := range map[string][]wireFrame{"silent": silent, "declared": declared} {
		if got := progressPayloads(t, frames); len(got) != 0 {
			t.Errorf("%s session: a request carrying no token must produce no notification; got %d: %+v", label, len(got), got)
		}
	}

	silentAnswer := string(responseTo(t, silent, "2").Result)
	declaredAnswer := string(responseTo(t, declared, "2").Result)
	if silentAnswer != declaredAnswer {
		t.Errorf("declaring the capability must not change an answer:\n silent:   %s\n declared: %s", silentAnswer, declaredAnswer)
	}
	if silentAnswer == "" {
		t.Error("the publication must answer in both sessions")
	}

	// The handshake a non-negotiating client reads must not grow a field
	// either, so its reply is byte-for-byte the reply it read before the
	// capability existed.
	silentHandshake := string(responseTo(t, silent, "1").Result)
	if strings.Contains(silentHandshake, "workDoneProgress") {
		t.Errorf("a client that declared nothing must read a handshake with no progress field; got %s", silentHandshake)
	}
	declaredHandshake := string(responseTo(t, declared, "1").Result)
	if !strings.Contains(declaredHandshake, `"workDoneProgress":true`) {
		t.Errorf("a client that declared the capability must be told the daemon will honour it; got %s", declaredHandshake)
	}
}

// TestAnOperationThatReportsNothingIsUnaffected is the second unchanged-behaviour
// proof: the transport is accepted on every method, and a method that reports
// nothing answers exactly as it does without a token.
//
// The method is chosen for being deliberately unreported — it walks the
// workspace once and answers — so a frame appearing here would mean the
// transport had leaked into an operation nobody lit up.
func TestAnOperationThatReportsNothingIsUnaffected(t *testing.T) {
	t.Parallel()

	withToken, _ := publishFixture(t, defaultRegistryWorkspace)
	tokened := driveSession(t, withToken,
		negotiatingClient(1),
		send(2, framedRequest(2, "aggrega/releaseCandidates", map[string]any{"workDoneToken": "unreported"})),
	)
	if got := progressPayloads(t, tokened); len(got) != 0 {
		t.Errorf("an unreported operation must push nothing; got %d frames: %+v", len(got), got)
	}
	tokenedAnswer := responseTo(t, tokened, "2")
	if tokenedAnswer.Error != nil {
		t.Fatalf("a token must not make an unreported operation fail; got error %d: %s",
			tokenedAnswer.Error.Code, tokenedAnswer.Error.Message)
	}

	withoutToken, _ := publishFixture(t, defaultRegistryWorkspace)
	plain := driveSession(t, withoutToken,
		negotiatingClient(1),
		send(2, framedRequest(2, "aggrega/releaseCandidates", map[string]any{})),
	)
	if got, want := string(tokenedAnswer.Result), string(responseTo(t, plain, "2").Result); got != want {
		t.Errorf("a token must not change an unreported operation's answer:\n with:    %s\n without: %s", got, want)
	}
}

// TestATokenWithoutTheCapabilityIsRefusedLoudly pins the negotiation as
// operative rather than decorative: the declared capability decides whether a
// token is admissible at all, and a mismatch is named rather than ignored.
//
// The two silent alternatives are what this rules out. Reporting anyway would
// leave the capability deciding nothing; reporting nothing would leave a caller
// watching a stream that never arrives with no way to find out why.
func TestATokenWithoutTheCapabilityIsRefusedLoudly(t *testing.T) {
	t.Parallel()
	srv, _ := publishFixture(t, defaultRegistryWorkspace)

	frames := driveSession(t, srv,
		silentClient(1),
		dryRunArtifact(2, map[string]any{"workDoneToken": "unnegotiated"}),
	)

	resp := responseTo(t, frames, "2")
	if resp.Error == nil {
		t.Fatalf("a token from a session that declared nothing must be refused; got result %s", resp.Result)
	}
	if resp.Error.Code != jsonRPCInvalidParams {
		t.Errorf("the refusal must be an invalid-params one; got code %d", resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "window.workDoneProgress") {
		t.Errorf("the refusal must name what to declare; got %q", resp.Error.Message)
	}
	if len(resp.Result) != 0 {
		t.Errorf("a refused request answers with no result; got %s", resp.Result)
	}
	if got := progressPayloads(t, frames); len(got) != 0 {
		t.Errorf("a refused token must push nothing; got %d frames: %+v", len(got), got)
	}
}

// TestATokenThatIsNotOneIsRefusedByShape pins the admissible token shape at the
// wire. The protocol admits a string or a number and nothing else, so a
// structured value is refused naming what to send rather than carried through to
// a notification a client cannot match.
func TestATokenThatIsNotOneIsRefusedByShape(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		token any
	}{
		{"an object", map[string]any{}},
		{"an array", []any{1}},
		{"a boolean", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := publishFixture(t, defaultRegistryWorkspace)
			frames := driveSession(t, srv,
				negotiatingClient(1),
				dryRunArtifact(2, map[string]any{"workDoneToken": tc.token}),
			)
			resp := responseTo(t, frames, "2")
			if resp.Error == nil {
				t.Fatalf("a token that is not a string or a number must be refused; got result %s", resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("the refusal must be an invalid-params one; got code %d", resp.Error.Code)
			}
			if !strings.Contains(resp.Error.Message, "must be a string or a number") {
				t.Errorf("the refusal must name the admissible shape; got %q", resp.Error.Message)
			}
			if got := progressPayloads(t, frames); len(got) != 0 {
				t.Errorf("a refused token must push nothing; got %d frames: %+v", len(got), got)
			}
		})
	}
}

// TestARequestRefusedOnItsParamsReportsNothing pins the ordering that makes a
// report honest: a caller is told a thing started only once it started, so a
// request refused before any work began produces no begin and therefore no end.
func TestARequestRefusedOnItsParamsReportsNothing(t *testing.T) {
	t.Parallel()
	srv, root := publishFixture(t, defaultRegistryWorkspace)

	frames := driveSession(t, srv,
		negotiatingClient(1),
		dryRunArtifact(2, map[string]any{
			"workDoneToken": "refused",
			"file":          filepath.Join(root, "bundle.txt"),
		}),
	)

	resp := responseTo(t, frames, "2")
	if resp.Error == nil {
		t.Fatalf("an absolute path must be refused; got result %s", resp.Result)
	}
	if got := progressPayloads(t, frames); len(got) != 0 {
		t.Errorf("a request refused on its params must report nothing; got %d frames: %+v", len(got), got)
	}
}

// TestWorkDoneTokenFrom covers the shapes a params document arrives in, and the
// third answer is the load-bearing one: params this reader cannot decode yield
// no token and no error, because whether those params are admissible belongs to
// the method rather than to this reader.
func TestWorkDoneTokenFrom(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		params    string
		wantToken string
		wantErr   bool
	}{
		{name: "no params at all", params: ""},
		{name: "params without the field", params: `{"file":"a.txt"}`},
		{name: "an explicit null reads as absent", params: `{"workDoneToken":null}`},
		{name: "a string token", params: `{"workDoneToken":"abc"}`, wantToken: `"abc"`},
		{name: "a numeric token", params: `{"workDoneToken":41}`, wantToken: `41`},
		{name: "whitespace around a token is preserved verbatim", params: `{"workDoneToken" : "abc" }`, wantToken: `"abc"`},
		{name: "an object is not a token", params: `{"workDoneToken":{"a":1}}`, wantErr: true},
		{name: "an array is not a token", params: `{"workDoneToken":[1]}`, wantErr: true},
		{name: "a boolean is not a token", params: `{"workDoneToken":true}`, wantErr: true},
		{name: "positional params carry no token and are not judged here", params: `[1,2]`},
		{name: "a scalar params value is not judged here", params: `"just a string"`},
		{name: "malformed params are not judged here", params: `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			token, err := workDoneTokenFrom(json.RawMessage(tc.params))
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("want a refusal; got token %s", token)
			case !tc.wantErr && err != nil:
				t.Fatalf("want no refusal; got %v", err)
			}
			if tc.wantErr {
				if !errors.Is(err, errProgressTokenShape) {
					t.Errorf("the refusal must be the shape one; got %v", err)
				}
				return
			}
			if got := string(token); got != tc.wantToken {
				t.Errorf("token: got %q, want %q", got, tc.wantToken)
			}
		})
	}
}

// TestProgressLinesSplitAndTrimTheOperationsOwnOutput drives the line writer
// through the production notification path — a real serializer, real framing —
// and pins the three behaviours a shared operation's writer needs: a line
// arriving in pieces is reported once, indentation and blank lines are dropped,
// and a final line written without a newline is not lost.
func TestProgressLinesSplitAndTrimTheOperationsOwnOutput(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	ctx, flush := serializerContext(t, &out)
	srv := &Server{stderr: io.Discard}
	run := srv.progressFor(withProgressToken(ctx, json.RawMessage(`"tok"`)))
	run.begin("Doing the thing")
	// One line arriving in three writes, then two lines in one write with an
	// indented second, then a blank line, then a trailing line with no newline.
	for _, chunk := range []string{"first ", "line", "\n", "second\n  indented\n", "\n", "  trailing"} {
		if _, err := run.writer().Write([]byte(chunk)); err != nil {
			t.Fatalf("write %q: %v", chunk, err)
		}
	}
	run.done()
	flush()

	var got []string
	for _, body := range readAllFrames(t, &out) {
		var f wireFrame
		if err := json.Unmarshal(body, &f); err != nil {
			t.Fatalf("decode frame: %v (body %s)", err, body)
		}
		if f.Method != progressMethod {
			t.Fatalf("every frame here is a progress notification; got method %q", f.Method)
		}
		var p progressFramePayload
		if err := json.Unmarshal(f.Params, &p); err != nil {
			t.Fatalf("decode progress params: %v", err)
		}
		switch p.Value.Kind {
		case progressKindReport:
			got = append(got, p.Value.Message)
		case progressKindBegin, progressKindEnd:
		default:
			t.Fatalf("unknown payload kind %q", p.Value.Kind)
		}
	}

	want := []string{"first line", "second", "indented", "trailing"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("reported lines: got %q, want %q", got, want)
	}
}

// TestProgressWriterDiscardsWhenNothingAskedForAReport pins the property that
// lets every lit call site pass the writer unconditionally: with no token on the
// context the writer is a discard, so a session that negotiated nothing pays no
// notification and the call sites need no branch.
func TestProgressWriterDiscardsWhenNothingAskedForAReport(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	ctx, flush := serializerContext(t, &out)
	srv := &Server{stderr: io.Discard}
	run := srv.progressFor(ctx)
	run.begin("Doing the thing")
	if _, err := run.writer().Write([]byte("a line that must not travel\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	run.done()
	flush()

	if frames := readAllFrames(t, &out); len(frames) != 0 {
		t.Errorf("a report with no token must write nothing; got %d frames: %s", len(frames), bytes.Join(frames, []byte("|")))
	}
}

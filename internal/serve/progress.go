// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/internal/opprogress"
)

// progressMethod is the notification method the daemon reports work in progress
// on.
//
// It is the protocol-standard spelling rather than an aggrega/-namespaced one,
// for the same reason $/cancelRequest is: a client already speaking the protocol
// routes it with nothing added, and correlating progress to the call that asked
// for it is a transport concern rather than an Aggrega one.
const progressMethod = "$/progress"

// The payload kinds one progress notification carries, in the order a report
// arrives in: one begin, then any number of reports, then one end.
const (
	progressKindBegin  = "begin"
	progressKindReport = "report"
	progressKindEnd    = "end"
)

// workDoneProgressParams is the slice of a request's params the dispatch path
// reads before the method's own handler decodes them: the optional token a
// caller mints so it can match the progress it asked for to the call it asked
// on.
//
// The token is captured as raw JSON rather than decoded to a concrete string or
// number, so it travels back on every notification exactly as the caller spelled
// it. The protocol admits either form, and a decode-then-re-encode step could
// perturb the bytes a caller matches on — the same reason the cancellation
// notification's id is carried raw.
type workDoneProgressParams struct {
	WorkDoneToken json.RawMessage `json:"workDoneToken,omitempty"`
}

// errProgressTokenShape refuses a token that is present and is not one. The
// message names what to send rather than what was sent, because the caller is
// composing a params document and needs the admissible shape.
var errProgressTokenShape = errors.New("invalid params: workDoneToken must be a string or a number")

// errProgressNotNegotiated refuses a token from a session that never said it
// could handle the notifications the token would produce.
//
// It refuses rather than reporting anyway or silently reporting nothing, and
// both alternatives were considered against the surrounding code. Reporting
// anyway would leave the declared capability deciding nothing, which is a
// capability in name only; reporting nothing would leave a caller watching a
// stream that never arrives with no way to find out why. A refusal naming what
// to declare is the only one of the three a caller can act on.
var errProgressNotNegotiated = errors.New("invalid params: workDoneToken was sent, but this session " +
	"declared no window.workDoneProgress capability; declare it on aggrega/initialize, or omit the token")

// workDoneTokenFrom reads the progress token off a request's params.
//
// Three answers, and the third is what keeps this from becoming a second params
// validator sitting ahead of every handler: a token, no token, or a token that
// is present and is not one. A params document that does not decode into this
// shape at all — a positional array, a bare scalar — yields no token and no
// error, because whether those params are admissible is the method's own
// question, and answering it here would refuse requests on a field they never
// mentioned.
//
// An explicit null reads as absent, the same way a null id reads as a
// notification: both spell "there is nothing here" on a wire where a missing
// field and a null one are written differently and mean the same.
func workDoneTokenFrom(params json.RawMessage) (json.RawMessage, error) {
	if len(params) == 0 {
		return nil, nil
	}
	var p workDoneProgressParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, nil
	}
	if len(p.WorkDoneToken) == 0 || string(p.WorkDoneToken) == "null" {
		return nil, nil
	}
	var decoded any
	if err := json.Unmarshal(p.WorkDoneToken, &decoded); err != nil {
		return nil, errProgressTokenShape
	}
	switch decoded.(type) {
	case string, float64:
		return p.WorkDoneToken, nil
	}
	return nil, errProgressTokenShape
}

// progressKey keys the per-request progress token on the request context.
type progressKey struct{}

// withProgressToken returns a context carrying the progress token its request
// asked to be reported on. The dispatch path installs it beside the per-request
// sink, so a handler reaches the token by the same route it reaches every other
// per-request handle.
//
// It installs the request's step reports with it (stepReports), so the steps an
// operation reports it waits on reach the report the request asked for, and a
// request that asked for none reports none.
func withProgressToken(ctx context.Context, token json.RawMessage) context.Context {
	ctx = context.WithValue(ctx, progressKey{}, token)
	steps := &stepReports{}
	return context.WithValue(opprogress.With(ctx, steps), stepReportsKey{}, steps)
}

// progressTokenFromContext returns the progress token installed by
// withProgressToken, or nil when the request carried none — which is the
// ordinary case, since a token is opt-in per request.
func progressTokenFromContext(ctx context.Context) json.RawMessage {
	token, _ := ctx.Value(progressKey{}).(json.RawMessage)
	return token
}

// progressParams is the params shape of one progress notification: the token
// the request carried, and one payload.
//
// The token is what correlates the notification to the call, and it is the
// caller's own value rather than the request's id. That is not a preference: a
// client's request id belongs to its transport, so a caller that issued the call
// may never see it, whereas a token the caller minted is a value it already
// holds.
type progressParams struct {
	Token json.RawMessage `json:"token"`
	Value any             `json:"value"`
}

// progressBegin opens a report. Title says what is happening, in the terms the
// caller asked for it.
type progressBegin struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
}

// progressReport carries one line of the operation's own account of itself.
//
// It carries no fraction, and the omission is deliberate: the operations that
// report here work through a set whose size is not known when the work starts —
// how many references a registry resolution walks, how many layers a
// publication uploads — so a fraction would have to be invented. A field nothing
// can populate honestly is worse than an absent one, because a client would
// build a bar out of it.
type progressReport struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// progressEnd closes a report. It carries nothing beyond the kind: the outcome
// travels on the response, which the caller is already waiting for, so repeating
// it here would give a client two places to read one answer from.
type progressEnd struct {
	Kind string `json:"kind"`
}

// progressReporter reports one request's work in progress on the token that
// request carried.
//
// A reporter holding no token is the ordinary case — a token is opt-in per
// request — and every method on it is then a no-op. That is what lets a handler
// report unconditionally: there is no capability to test at the call site, and a
// session that negotiated nothing sees no frame.
type progressReporter struct {
	s     *Server
	ctx   context.Context
	token json.RawMessage
}

// reporting reports whether anything this reporter is told will reach the wire.
func (r *progressReporter) reporting() bool {
	return r != nil && len(r.token) > 0
}

// emit pushes one payload as a progress notification. A write failure is
// dropped: progress is an account of work the response will report the outcome
// of, so failing the operation because its narration could not be written would
// turn a cosmetic loss into a functional one.
func (r *progressReporter) emit(value any) {
	if !r.reporting() {
		return
	}
	_ = r.s.writeNotification(r.ctx, progressMethod, progressParams{Token: r.token, Value: value})
}

// progressRun is one operation's report: the reporter, and the writer the shared
// layer writes its own account into.
//
// It exists so a handler adds reporting in two lines and holds no state of its
// own. Nothing is emitted until begin is called, so a request refused on its
// params produces no frames at all — a caller is told a thing started only when
// it started.
type progressRun struct {
	rep   *progressReporter
	lines *progressLines
	began bool
	// steps is the request's step reports, where the request carried a token.
	steps *stepReports
}

// progressFor returns a report for this request, whether or not the request
// asked to be reported on.
//
// The steps the request's operation reports it waits on (opprogress) are lines
// of this report too, the step's own name as the line, as the command line shows
// them to a person: the dispatch path installed the request's step reports where
// a token was carried (withProgressToken), and the report is bound to them here,
// so a step is reported only once the report has begun and until it is done.
func (s *Server) progressFor(ctx context.Context) *progressRun {
	rep := &progressReporter{s: s, ctx: ctx, token: progressTokenFromContext(ctx)}
	run := &progressRun{rep: rep, lines: &progressLines{rep: rep}}
	if steps, ok := ctx.Value(stepReportsKey{}).(*stepReports); ok {
		run.steps = steps
	}
	return run
}

// begin opens the report under the given title.
func (p *progressRun) begin(title string) {
	p.began = true
	p.rep.emit(progressBegin{Kind: progressKindBegin, Title: title})
	p.steps.open(p.lines)
}

// stepReportsKey keys a request's step reports on its context.
type stepReportsKey struct{}

// stepReports turns the steps an operation reports it waits on into lines of
// the request's progress report. It is installed on the request's context before
// any handler runs and is bound to the report when the report begins, so a step
// reported before the report began, or after it ended, reports nothing — a
// caller is never sent a line outside the begin and end that frame it.
type stepReports struct {
	mu    sync.Mutex
	lines *progressLines
}

// open binds the report's lines, from which steps are reported.
func (s *stepReports) open(lines *progressLines) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = lines
}

// close unbinds them, so nothing is reported once the report has ended.
func (s *stepReports) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = nil
}

func (s *stepReports) Begin(step string) opprogress.End {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lines != nil {
		s.lines.reportLine(step)
	}
	return func(error) {}
}

func (*stepReports) Yield() {}

func (*stepReports) Beside(w io.Writer) io.Writer { return w }

// writer is the io.Writer to hand the shared operation for its own
// human-readable output. It is never nil, and it discards when the request asked
// for no report, so a call site passes it unconditionally in place of the
// discard it passed before.
func (p *progressRun) writer() io.Writer {
	return p.lines
}

// done closes the report, reporting any trailing text the operation wrote
// without a final newline first. A report that never began ends nothing.
func (p *progressRun) done() {
	if !p.began {
		return
	}
	p.steps.close()
	p.lines.flush()
	p.rep.emit(progressEnd{Kind: progressKindEnd})
}

// progressLines turns a shared operation's own human-readable output into
// progress reports, one per line.
//
// The operation already writes an account of itself as it works — that is what
// the surfaces with a terminal show a person watching — and this surface
// discarded it for want of anywhere to put it. So what a report carries is the
// operation's own text rather than wording composed here, which is the same
// property the response's diagnostics carry: one condition keeps one wording
// however many surfaces report it.
//
// Line-buffered because a writer receives whatever chunks its producer happens
// to hand it, and a notification per chunk would split one sentence across two
// frames. The leading indentation the shared streams use to nest a line under
// the step that raised it is trimmed, and blank lines are dropped, for the same
// reason the response's notices are: indentation is a terminal layout, and a
// client showing a message has whitespace to strip rather than text to show.
type progressLines struct {
	rep *progressReporter
	buf bytes.Buffer
}

// Write accumulates output and reports each complete line. It never fails: a
// writer handed to a shared operation must not be able to fail that operation,
// and there is nothing here that can.
func (w *progressLines) Write(p []byte) (int, error) {
	if !w.rep.reporting() {
		return len(p), nil
	}
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// No newline yet: the remainder is an incomplete line, so put it
			// back and wait for the rest of it.
			w.buf.Reset()
			w.buf.WriteString(line)
			break
		}
		w.reportLine(line)
	}
	return len(p), nil
}

// flush reports whatever is left in the buffer as a final line. Called when the
// operation is over, so a last line written without a trailing newline is not
// lost.
func (w *progressLines) flush() {
	if !w.rep.reporting() {
		return
	}
	remainder := w.buf.String()
	w.buf.Reset()
	w.reportLine(remainder)
}

// reportLine reports one line, dropping it when it carries no text.
func (w *progressLines) reportLine(line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}
	w.rep.emit(progressReport{Kind: progressKindReport, Message: trimmed})
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"sync"
)

// progressMethod is the notification a request's progress is reported on. It is
// the protocol's own spelling rather than an aggrega/-namespaced one: a client
// already speaking the protocol routes it with nothing added.
const progressMethod = "notifications/progress"

// metaKeyProgressToken is the per-request metadata name a client mints a token
// under to ask for progress on the call carrying it.
const metaKeyProgressToken = "progressToken"

// progressNotification is the params shape of one progress notification: the
// token the request minted, and how far the work has got.
//
// The token travels as raw JSON rather than decoded to a concrete string or
// number, so it returns exactly as the caller spelled it. The protocol admits
// either form, and a decode-then-re-encode step could perturb the bytes a
// caller matches its own call on.
//
// It carries neither a total nor a message, and neither is an omission this
// shape chose. The operation reporting here says which stage it has entered and
// nothing else: it supplies no count of the stages a particular run will pass
// through and no sentence of its own, so both fields could only be composed out
// of something the work never said. A client builds a spinner out of a rising
// number; it would build a progress bar out of an invented total.
type progressNotification struct {
	ProgressToken json.RawMessage `json:"progressToken"`
	Progress      uint64          `json:"progress"`
}

// progressTokenFrom reads the progress token a request minted, out of the
// metadata a request carries its per-request names in.
//
// Three answers, and the third is what keeps a mistyped token from becoming
// silence: a token, no token, or a token that is present and is not one. A
// request naming no token is the ordinary case and is answered exactly as it
// was before this existed. An explicit null reads as absent, the same way a
// missing name does: both spell "there is nothing here" on a wire that writes
// them differently and means the same.
//
// The value is taken raw rather than through the string reader beside it,
// because the protocol admits a string OR a number here and that reader reports
// a number as absent — which would leave a client that minted a numeric token
// watching for a stream that never arrives.
//
// A token of any other shape is refused rather than ignored, for the same
// reason the shape is read at all: a caller that minted one is waiting to be
// reported to, and silence is the one answer it cannot act on. The sentence
// names the admissible shapes rather than what was sent, because the caller is
// composing a params document and needs to know what may go there.
func progressTokenFrom(params json.RawMessage) (json.RawMessage, *rpcError) {
	raw, present := readRequestMeta(params)[metaKeyProgressToken]
	if !present || len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, progressTokenShapeRefusal()
	}
	switch decoded.(type) {
	case string, float64:
		return raw, nil
	}
	return nil, progressTokenShapeRefusal()
}

// progressTokenShapeRefusal is the one home of the refusal a token of the wrong
// shape earns, so a caller that sends a boolean and one that sends an object
// are told the same thing.
func progressTokenShapeRefusal() *rpcError {
	return &rpcError{
		Code:    codeInvalidParams,
		Message: "_meta.progressToken must be a string or a number",
	}
}

// progressSink is where one request's progress notifications go. A transport
// installs its own — an event on the response stream over HTTP, a framed line
// through the serialised writer on stdio — and nothing below the transport
// knows which of the two it has.
type progressSink func(note progressNotification)

// progressReporter reports one request's work in progress, on the token that
// request minted.
//
// A reporter holding no token is the ordinary case, and every call on it is
// then a no-op. That is what lets an operation report unconditionally: there is
// nothing to test for at the call site, and a request that asked for nothing
// produces nothing on the wire.
//
// The count is the reporter's own rather than the operation's. The protocol
// requires it to rise with every notification, and the operations reporting
// here report the stage they entered rather than a number — so the one place
// that property can be held is the one place the notifications are composed.
type progressReporter struct {
	ctx   context.Context
	token json.RawMessage
	sink  progressSink

	mu   sync.Mutex
	sent uint64
}

// reporting reports whether anything this reporter is told will reach the
// caller. A nil reporter reports nothing, which is what lets a call site hold
// one without testing for it.
func (r *progressReporter) reporting() bool {
	return r != nil && len(r.token) > 0 && r.sink != nil
}

// step reports that the work has moved on.
//
// Nothing is written once the request's context has ended. A caller that closed
// its connection is not there to read it, and the protocol asks a server to
// stop reporting on a request that is over — so the question is asked here, at
// the one point every notification passes through, rather than at each site
// that reports.
func (r *progressReporter) step() {
	if !r.reporting() || r.ctx.Err() != nil {
		return
	}
	// The send happens under the lock so the order the notifications reach the
	// wire in is the order their counts were taken in. Releasing the lock first
	// would leave two reports free to overtake each other, and a caller would
	// read a count that fell — which is the one thing the protocol says this
	// number may not do.
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent++
	r.sink(progressNotification{ProgressToken: r.token, Progress: r.sent})
}

// progressSinkKey keys the per-request progress sink on the request context.
type progressSinkKey struct{}

// progressReporterKey keys the per-request reporter on the request context.
type progressReporterKey struct{}

// withProgressSink returns a context carrying the sink a transport reports one
// request's progress through.
//
// It is installed before the request is routed, which is what makes the stream
// a report opens the response stream of the request it belongs to and of no
// other: there is one sink per request, built by the goroutine answering it.
func withProgressSink(ctx context.Context, sink progressSink) context.Context {
	return context.WithValue(ctx, progressSinkKey{}, sink)
}

// progressSinkFrom returns the sink the transport installed, or nil where the
// transport installs none.
func progressSinkFrom(ctx context.Context) progressSink {
	sink, _ := ctx.Value(progressSinkKey{}).(progressSink)
	return sink
}

// withProgressReporter returns a context carrying the reporter this request's
// work reports through, built from the token the request minted and the sink
// its transport installed.
//
// It sits on the route both transports arrive at rather than in either of them,
// so the token is read once. What differs between the transports is the sink,
// which each installs for itself before it routes anything.
//
// The reporter watches the context it was built from rather than the one
// returned, which is the same cancellation either way: the value added here
// carries no deadline of its own.
func withProgressReporter(ctx context.Context, req *rpcRequest) (context.Context, *rpcError) {
	token, rpcErr := progressTokenFrom(req.Params)
	if rpcErr != nil {
		return ctx, rpcErr
	}
	reporter := &progressReporter{ctx: ctx, token: token, sink: progressSinkFrom(ctx)}
	return context.WithValue(ctx, progressReporterKey{}, reporter), nil
}

// progressReporterFrom returns the reporter installed for this request, or nil
// where nothing installed one — which every operation treats the same way,
// because every call on a nil reporter is a no-op.
func progressReporterFrom(ctx context.Context) *progressReporter {
	reporter, _ := ctx.Value(progressReporterKey{}).(*progressReporter)
	return reporter
}

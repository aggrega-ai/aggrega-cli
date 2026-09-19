// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// contentTypeEventStream is the media type an answer carrying progress is
// served with. A request that asked for no progress, and one whose work reports
// none, is still answered with one JSON object.
const contentTypeEventStream = "text/event-stream"

// The headers an event stream carries beyond its media type.
//
// The buffering one is not a standard header and is named for the intermediary
// that reads it: a reverse proxy that buffers a response holds every event
// until the work is over, which turns a stream into a slow single answer and
// defeats the whole exchange. Sending it costs nothing where nothing reads it.
const (
	headerConnection      = "Connection"
	headerAccelBuffering  = "X-Accel-Buffering"
	valueNoCache          = "no-cache"
	valueKeepAlive        = "keep-alive"
	valueBufferingDisable = "no"
)

// progressKeepAlive is how long an open stream may sit idle before a comment
// line is written on it.
//
// Fifteen seconds is the production value, and it is stated here and nowhere
// else. It is chosen against what sits between this server and its caller
// rather than against the work: idle timeouts on proxies and load balancers
// commonly start at a minute, so a line every fifteen seconds keeps a stream
// alive across the usual ones with room for a missed tick, while costing a few
// bytes an hour on a connection that is by definition already waiting.
const progressKeepAlive = 15 * time.Second

// sseComment is the keep-alive line itself. A line beginning with a colon is a
// comment in the event-stream format: it reaches the intermediary as bytes on a
// live connection and reaches the client as nothing at all.
const sseComment = ": keep-alive\n\n"

// progressStream turns one request's response into an event stream — but only
// once there is something to put on it.
//
// Nothing is written until the first report arrives, and that is the whole of
// how a token-bearing call reaching work that reports nothing is still answered
// with the single JSON object it was answered with before. The decision is
// taken by the work rather than by the presence of a token: this transport
// cannot know which tools report, and a table here naming the ones that do
// would be a second statement of something each tool already makes by
// reporting — the two would agree until a tool changed, and nothing would say
// when they stopped.
//
// Every write goes through one mutex. The keep-alive runs on a goroutine of its
// own, so a comment line and an event could otherwise interleave their bytes
// and neither would parse.
type progressStream struct {
	w         http.ResponseWriter
	ctrl      *http.ResponseController
	keepAlive time.Duration

	mu      sync.Mutex
	started bool
	closed  bool
	ticker  *time.Ticker
	done    chan struct{}
}

// newProgressStream prepares a stream over one response, without writing
// anything: a request whose work never reports must leave this having touched
// neither the status nor a header.
//
// An interval that is not positive falls back to the declared one rather than
// being taken at face value, because a ticker cannot be built from it and the
// failure would be a crash inside a request rather than a stream without a
// keep-alive. The fallback names the constant rather than restating its value,
// so the interval still has one home.
func newProgressStream(w http.ResponseWriter, keepAlive time.Duration) *progressStream {
	if keepAlive <= 0 {
		keepAlive = progressKeepAlive
	}
	return &progressStream{
		w:         w,
		ctrl:      http.NewResponseController(w),
		keepAlive: keepAlive,
		done:      make(chan struct{}),
	}
}

// emit writes one notification as an event, opening the stream if this is the
// first one.
//
// A write failure is dropped: progress is an account of work whose outcome the
// response reports, so failing the operation because its narration could not be
// written would turn a cosmetic loss into a functional one. A caller that has
// gone is met at the reporter, which stops on the request's context.
func (p *progressStream) emit(note progressNotification) {
	frame, err := json.Marshal(&rpcNotification{
		JSONRPC: "2.0",
		Method:  progressMethod,
		Params:  note,
	})
	if err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.openLocked()
	p.writeLocked(frame)
}

// opened reports whether anything ever reported on this request, which is the
// same question as whether the caller is reading a stream.
func (p *progressStream) opened() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.started
}

// finish writes resp as the stream's last event and closes the stream. It
// reports whether the answer was written here, which is false when nothing ever
// reported — in which case the caller answers with the single JSON object
// instead, exactly as it does for a request that asked for no progress at all.
//
// The response travels as an event rather than as a second body because the
// status and the headers are already sent by the time the work is done: a
// stream that opened has committed this exchange to one shape, and the result
// is the last thing on it.
//
// A response that will not marshal closes the stream carrying nothing further,
// which is the same answer the single-object path gives: the status is already
// sent, so there is no envelope left to compose, and the exchange terminates
// with the caller having read every event that did arrive.
func (p *progressStream) finish(resp *rpcResponse) bool {
	frame, err := json.Marshal(resp)
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started || p.closed {
		return p.started
	}
	if err == nil {
		p.writeLocked(frame)
	}
	p.closeLocked()
	return true
}

// close releases the keep-alive and refuses every later write. It is safe to
// call on a stream that never opened, and on one finish already closed, so a
// caller defers it and leaves the ordering to whichever path the request took.
func (p *progressStream) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeLocked()
}

// openLocked writes the stream's headers and starts the keep-alive, once.
func (p *progressStream) openLocked() {
	if p.started {
		return
	}
	p.started = true
	header := p.w.Header()
	header.Set(headerContentType, contentTypeEventStream)
	header.Set(headerCacheControl, valueNoCache)
	header.Set(headerConnection, valueKeepAlive)
	header.Set(headerAccelBuffering, valueBufferingDisable)
	p.w.WriteHeader(http.StatusOK)
	p.ticker = time.NewTicker(p.keepAlive)
	go p.keepAliveUntilClosed(p.ticker.C)
}

// writeLocked frames one payload as an event and pushes it past whatever
// buffering sits between here and the connection. The caller holds the mutex.
//
// The keep-alive interval is restarted after every event, so the comment line
// marks time the stream was genuinely idle rather than time since it opened.
func (p *progressStream) writeLocked(payload []byte) {
	if _, err := p.w.Write([]byte("data: ")); err != nil {
		return
	}
	if _, err := p.w.Write(payload); err != nil {
		return
	}
	if _, err := p.w.Write([]byte("\n\n")); err != nil {
		return
	}
	_ = p.ctrl.Flush()
	if p.ticker != nil {
		p.ticker.Reset(p.keepAlive)
	}
}

// closeLocked stops the keep-alive and marks the stream shut. The caller holds
// the mutex.
func (p *progressStream) closeLocked() {
	if p.closed {
		return
	}
	p.closed = true
	if p.ticker != nil {
		p.ticker.Stop()
	}
	close(p.done)
}

// keepAliveUntilClosed writes a comment line whenever the stream has sat idle
// for the interval, and stops when the stream closes.
//
// It runs on its own goroutine because the thing it measures is the absence of
// activity: a request whose work is deep in a resolution is not passing through
// here to be asked whether a tick is due.
func (p *progressStream) keepAliveUntilClosed(tick <-chan time.Time) {
	for {
		select {
		case <-p.done:
			return
		case <-tick:
			p.mu.Lock()
			if p.closed {
				p.mu.Unlock()
				return
			}
			if _, err := p.w.Write([]byte(sseComment)); err == nil {
				_ = p.ctrl.Flush()
			}
			p.mu.Unlock()
		}
	}
}

// stopOnContext closes the stream when ctx ends, so a caller that disconnected
// mid-stream stops the keep-alive along with the work.
//
// It returns the stop for the watch itself, which the caller defers: leaving it
// running would hold the stream alive for as long as the run does, one
// registration per request answered.
func (p *progressStream) stopOnContext(ctx context.Context) (stopWatching func() bool) {
	return context.AfterFunc(ctx, p.close)
}

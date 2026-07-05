// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"io"
	"sync"
)

// queueDepthFactor multiplies the worker count to size the bounded
// request queue. A small multiple keeps a few requests buffered ahead
// of the workers without letting the queue grow unbounded; when the
// queue fills, submission blocks and back-pressures the stdin reader.
const queueDepthFactor = 4

// queuedRequest pairs a parsed request with the arrival sequence the
// dispatch loop assigned it at submission time. seq is dense,
// monotonic, and gap-free across the single stdin stream, so it is the
// canonical request order the serializer emits responses in.
type queuedRequest struct {
	seq int64
	req *jsonRPCRequest
}

// workerPool runs queued requests on a fixed set of worker goroutines
// draining a bounded queue. The queue's bound is the back-pressure
// mechanism: a full queue blocks the submitting goroutine (the dispatch
// loop), which stops draining the stdin reader, which back-pressures
// the frontend. There is no reject or drop path.
//
// At a pool size of one the pool runs requests serially in arrival
// order, behaving identically to a direct synchronous dispatch call.
type workerPool struct {
	queue chan queuedRequest
	wg    sync.WaitGroup
}

// newWorkerPool starts size worker goroutines, each draining the
// bounded queue and running run for every dequeued request. size is
// clamped to a minimum of one by the caller. The queue capacity is
// size*queueDepthFactor.
func newWorkerPool(size int, run func(queuedRequest)) *workerPool {
	if size < 1 {
		size = 1
	}
	p := &workerPool{
		queue: make(chan queuedRequest, size*queueDepthFactor),
	}
	p.wg.Add(size)
	for i := 0; i < size; i++ {
		go func() {
			defer p.wg.Done()
			for q := range p.queue {
				run(q)
			}
		}()
	}
	return p
}

// submit hands a request to the pool. When the bounded queue is full
// this blocks until a worker frees a slot — the natural back-pressure
// path. There is deliberately no non-blocking variant.
func (p *workerPool) submit(q queuedRequest) {
	p.queue <- q
}

// drain stops accepting new work and waits for in-flight workers to
// finish. Closing the queue lets each worker's range loop exit once the
// remaining buffered requests are processed; the wait then returns once
// every worker goroutine has returned. In-flight handlers are bounded
// by serverCtx cancellation (the reader cancels it at shutdown
// arrival), so this wait completes within the drain budget rather than
// running an arbitrarily long handler to completion.
func (p *workerPool) drain() {
	close(p.queue)
	p.wg.Wait()
}

// outFrame is one unit of work for the stdout serializer. A frame
// either carries a body to write or, when hasSeq is true and body is
// nil, signals that an ordered slot completed without producing a
// response (a notification-only request, or a request cancelled before
// it wrote) so the serializer can advance past it.
//
// Two framing modes:
//
//   - hasSeq=true: an ordered response slot keyed by seq. The
//     serializer emits these strictly in seq order, buffering any that
//     arrive early. A nil body advances the slot without writing bytes.
//   - hasSeq=false: an immediate frame (a server-push notification with
//     no id-correlation contract). The serializer writes it as soon as
//     it dequeues it, interleaved between whatever ordered frames
//     surround it.
type outFrame struct {
	seq    int64
	body   []byte
	hasSeq bool
}

// serializer is the sole owner of the daemon's stdout once started. All
// write surfaces route their bodies to it through a channel; it is the
// only goroutine that touches the underlying writer, so concurrent
// workers never race the single writer.
//
// Ordered frames (responses) are emitted in arrival-sequence order
// regardless of which worker completes first: the serializer writes the
// frame whose seq equals next, then drains any consecutive frames it
// had buffered early. Immediate frames (notifications) bypass ordering
// and write on dequeue. A fatal write error is reported once on errCh
// so the dispatch loop can terminate the daemon, preserving the
// "a broken stdout ends the session" contract.
type serializer struct {
	out     io.Writer
	frames  chan outFrame
	errCh   chan error
	next    int64
	pending map[int64]outFrame
	done    chan struct{}
}

// newSerializer constructs a serializer writing to out. The frames
// channel is buffered so workers rarely block handing off a completed
// frame; the error channel is buffered to one so a write failure never
// blocks the serializer goroutine on a loop that may not be reading
// yet.
func newSerializer(out io.Writer) *serializer {
	return &serializer{
		out:     out,
		frames:  make(chan outFrame, 64),
		errCh:   make(chan error, 1),
		next:    1,
		pending: map[int64]outFrame{},
		done:    make(chan struct{}),
	}
}

// run drains the frame channel until it is closed, emitting frames in
// the order described on serializer. It closes done when finished so
// the dispatch loop can wait for the final flush. The first write
// failure is reported on errCh and suppresses further writes (the loop
// keeps draining so workers handing off frames never block, but emits
// no more bytes).
func (z *serializer) run() {
	defer close(z.done)
	failed := false
	emit := func(body []byte) {
		if failed || body == nil {
			return
		}
		if err := writeMessage(z.out, body); err != nil {
			failed = true
			select {
			case z.errCh <- err:
			default:
			}
		}
	}
	for f := range z.frames {
		if !f.hasSeq {
			emit(f.body)
			continue
		}
		if f.seq != z.next {
			z.pending[f.seq] = f
			continue
		}
		emit(f.body)
		z.next++
		for {
			nf, ok := z.pending[z.next]
			if !ok {
				break
			}
			delete(z.pending, z.next)
			emit(nf.body)
			z.next++
		}
	}
}

// emit hands an ordered response frame to the serializer. body may be
// nil to close an ordered slot that produced no response, advancing the
// serializer past it without writing bytes.
func (z *serializer) emit(seq int64, body []byte) {
	z.frames <- outFrame{seq: seq, body: body, hasSeq: true}
}

// emitNow hands an immediate (unordered) frame to the serializer, used
// for server-push notifications that carry no id-correlation contract.
func (z *serializer) emitNow(body []byte) {
	z.frames <- outFrame{body: body, hasSeq: false}
}

// closeAndWait stops the serializer and blocks until it has flushed
// every buffered frame and returned. Called during dispatch-loop
// teardown after the worker pool has drained, so no further frames can
// arrive once this runs.
func (z *serializer) closeAndWait() {
	close(z.frames)
	<-z.done
}

// requestSink is the per-request handle the dispatch path threads
// through context so the write surfaces route their output to the
// serializer under the request's arrival sequence. Carrying it on the
// context keeps a single write path: the write helpers read the sink
// and never touch the underlying stdout writer directly.
//
// A request produces at most one response body. The response write
// surfaces record it here; the dispatch path then closes the request's
// ordered slot exactly once after the handler returns — emitting the
// recorded body, or a body-less done-frame when the handler produced no
// response (a notification or a request dropped during drain). This
// keeps the serializer's arrival-sequence cursor advancing
// past response-less slots without stalling. Notifications bypass the
// slot entirely and emit immediately.
type requestSink struct {
	ser      *serializer
	seq      int64
	response []byte
}

type sinkKey struct{}

// withRequestSink returns a context carrying the per-request sink. The
// dispatch loop installs it before running a handler so every write
// surface the handler reaches routes through the same serializer slot.
func withRequestSink(ctx context.Context, sink *requestSink) context.Context {
	return context.WithValue(ctx, sinkKey{}, sink)
}

// sinkFromContext returns the per-request sink installed by
// withRequestSink. The dispatch path always installs one before running
// a handler, and the in-memory test harness does the same, so every
// write surface has a single route to stdout — the serializer. A nil
// return means a write surface ran outside a request context, which is
// a wiring bug rather than a supported mode.
func sinkFromContext(ctx context.Context) *requestSink {
	sink, _ := ctx.Value(sinkKey{}).(*requestSink)
	return sink
}

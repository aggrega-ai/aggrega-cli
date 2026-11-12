// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// AnswerWait is how long a request through the egress transport waits on the
// server it addressed, at either of the two points a server can leave a request
// hanging: for its answer to begin once the request has been sent in full, and,
// while the answer is being read, for the next part of it to arrive. A server
// that keeps the reader waiting longer than this at either point is refused
// (ErrNoAnswerInTime), so no request waits on a registry without end.
//
// It bounds waiting, never transferring: a large download that keeps arriving,
// and a large upload, take as long as they take, because the clock starts only
// once the request is sent and runs only while the reader is waiting on the
// next part of the answer. It is generous enough for a registry slow to start
// its answer and short enough that a server that will never answer costs a
// pause rather than a session. A request whose server answers only after long
// work of its own is given a longer wait by its caller (WithAnswerWait).
const AnswerWait = 30 * time.Second

// ErrNoAnswerInTime is returned when the server a request addressed kept it
// waiting longer than AnswerWait, or the wait its caller stated
// (WithAnswerWait): its answer did not begin within that long of the request
// being sent, or an answer under way stopped arriving for that long. The error
// naming it names the server and the wait.
//
// It also reports a timeout, as an expired network deadline does, so a caller
// asking whether a failed request is worth asking again treats it as the
// timeout it is.
func ErrNoAnswerInTime() error { return errNoAnswerInTime }

var errNoAnswerInTime = cerrors.Register("no-answer-in-time", cerrors.Meta{
	ShortCode: "E0376",
	Message:   "egress: the server did not answer in time",
	RulePlace: cerrors.NotARule("A server a request addressed kept it waiting past the time a request waits for an " +
		"answer. It reports a network operation that did not complete rather than a rule over anything an author wrote."),
})

// noAnswerError is the refusal of one request a server kept waiting: which
// server, how long it was waited on, and whether it never began its answer or
// stopped part way through one.
type noAnswerError struct {
	host    string
	wait    time.Duration
	partway bool
}

func (e *noAnswerError) Error() string {
	if e.partway {
		return fmt.Sprintf("%s stopped answering: nothing more of its answer arrived for %s: %s",
			e.host, e.wait, errNoAnswerInTime.Error())
	}
	return fmt.Sprintf("%s did not answer within %s: %s", e.host, e.wait, errNoAnswerInTime.Error())
}

func (e *noAnswerError) Unwrap() error { return errNoAnswerInTime }

// Timeout reports that the request timed out, as net.Error asks.
func (e *noAnswerError) Timeout() bool { return true }

// Temporary reports that asking again may succeed, as net.Error asks.
func (e *noAnswerError) Temporary() bool { return true }

// answerWaitKey keys a wait stated for the requests made under a context.
type answerWaitKey struct{}

// WithAnswerWait returns a context under which a request through the egress
// transport waits on its server for up to wait, in place of AnswerWait, at both
// points AnswerWait bounds.
//
// It is for a request a server legitimately answers only after long work of its
// own: a registry asked for its whole repository listing may gather every
// repository it holds before it answers at all. Such a request is still bounded
// — by the wait stated here, which the caller sets to the bound of the work as a
// whole. A wait that is not positive states nothing.
func WithAnswerWait(ctx context.Context, wait time.Duration) context.Context {
	return context.WithValue(ctx, answerWaitKey{}, wait)
}

// AnswerWaitFor returns how long a request made under ctx waits on its server:
// the wait ctx states (WithAnswerWait), or else AnswerWait.
func AnswerWaitFor(ctx context.Context) time.Duration {
	if stated, ok := ctx.Value(answerWaitKey{}).(time.Duration); ok && stated > 0 {
		return stated
	}
	return AnswerWait
}

// StandardTransport returns a standard-library transport for client's egress, for
// a library that takes a concrete *http.Transport rather than any round tripper
// and so cannot be handed the egress transport itself — Helm's chart getter is
// one. A nil client, which is how the egress gate reports an offline workspace,
// and a client whose transport has no standard transport beneath it report not
// ok, so the caller refuses rather than let the library build an ungated
// transport of its own.
//
// Beneath the egress transport it returns a copy of the standard transport with
// the wait for an answer to begin bounded by AnswerWait, as every egress request
// is. The wait on the rest of an answer is the one bound such a library is not
// held to, because only a round tripper sees the reads of an answer, and the
// expiry is reported in the standard library's own words rather than as
// ErrNoAnswerInTime. A client carrying a standard transport directly — one a
// caller built for itself — is returned as it is.
//
// The copy is made once per standard transport and shared, so every library
// handed one reads the same connection pool; a caller that has to change it — to
// dial under a context of its own, say — clones it first rather than modifying
// what every other caller holds.
func StandardTransport(client *http.Client) (*http.Transport, bool) {
	if client == nil {
		return nil, false
	}
	switch rt := client.Transport.(type) {
	case answerWaitTransport:
		std, ok := rt.next.(*http.Transport)
		if !ok {
			return nil, false
		}
		if held, ok := standardCopies.Load(std); ok {
			return held.(*http.Transport), true
		}
		t := std.Clone()
		t.ResponseHeaderTimeout = AnswerWait
		held, _ := standardCopies.LoadOrStore(std, t)
		return held.(*http.Transport), true
	case *http.Transport:
		return rt, true
	}
	return nil, false
}

// standardCopies holds, keyed by the standard transport beneath the egress
// transport, the one bounded copy StandardTransport hands out for it.
var standardCopies sync.Map

// answerWaitTransport is the egress transport: next, with every request held to
// the wait on its server's answer its context gives it (AnswerWaitFor).
//
// A policy added to RoundTrip owes its counterpart in StandardTransport. A
// library that takes a concrete standard transport is handed that copy instead of
// this wrapper, so a policy enforced only here would not reach it; the test
// holding the egress transport's policies to the copy names each one.
type answerWaitTransport struct {
	next http.RoundTripper
}

func (t answerWaitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	wait := AnswerWaitFor(req.Context())
	ctx, cancel := context.WithCancel(req.Context())
	clock := &answerClock{wait: wait, cancel: cancel}
	out := req.Clone(ctx)
	if req.Body != nil && req.Body != http.NoBody {
		// The answer is waited on from the moment the request has been sent in
		// full, which is when its body has been read to its end or closed.
		out.Body = &sentBody{ReadCloser: req.Body, sent: clock.requestSent}
		if req.GetBody != nil {
			out.GetBody = func() (io.ReadCloser, error) {
				body, err := req.GetBody()
				if err != nil {
					return nil, err
				}
				return &sentBody{ReadCloser: body, sent: clock.requestSent}, nil
			}
		}
	} else {
		clock.requestSent()
	}
	resp, err := t.next.RoundTrip(out)
	clock.answerBegan()
	if err != nil {
		cancel()
		if clock.expired() {
			return nil, &noAnswerError{host: req.URL.Host, wait: wait}
		}
		return nil, err
	}
	resp.Body = &awaitedBody{ReadCloser: resp.Body, clock: clock, host: req.URL.Host}
	return resp, nil
}

// answerClock is the one request's wait on its server: armed while the request
// waits on the server, disarmed while it does not, and, once a wait outlasts its
// bound, expired for good with the request withdrawn.
type answerClock struct {
	wait   time.Duration
	cancel context.CancelFunc

	mu       sync.Mutex
	timer    *time.Timer
	fired    bool
	started  bool
	answered bool
}

// requestSent arms the clock for the wait on the answer to begin, unless the
// answer has already begun: a server may answer before it has read the whole
// request, and the transport then closes the body after the fact.
func (c *answerClock) requestSent() {
	c.mu.Lock()
	answered := c.answered
	c.mu.Unlock()
	if !answered {
		c.startWaiting()
	}
}

// answerBegan disarms the clock once the server's answer has begun, or the
// request has failed, after which only reads of the answer arm it again.
func (c *answerClock) answerBegan() {
	c.stopWaiting()
	c.mu.Lock()
	c.answered = true
	c.mu.Unlock()
}

// startWaiting arms the clock where it is not already armed.
func (c *answerClock) startWaiting() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fired || c.started {
		return
	}
	c.started = true
	c.timer = time.AfterFunc(c.wait, c.expire)
}

// stopWaiting disarms the clock.
func (c *answerClock) stopWaiting() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.started = false
}

func (c *answerClock) expire() {
	c.mu.Lock()
	c.fired = true
	c.mu.Unlock()
	c.cancel()
}

func (c *answerClock) expired() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fired
}

// sentBody is a request body that reports when it has been sent in full: read to
// its end, or closed by the transport once it was written.
type sentBody struct {
	io.ReadCloser
	once sync.Once
	sent func()
}

func (b *sentBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if errors.Is(err, io.EOF) {
		b.once.Do(b.sent)
	}
	return n, err
}

func (b *sentBody) Close() error {
	b.once.Do(b.sent)
	return b.ReadCloser.Close()
}

// awaitedBody is an answer whose every read waits on the server for at most the
// clock's bound. The clock runs only during a read, so a reader that pauses
// between reads — to write what it read, say — is never refused for its own
// pause.
type awaitedBody struct {
	io.ReadCloser
	clock *answerClock
	host  string
}

func (b *awaitedBody) Read(p []byte) (int, error) {
	b.clock.startWaiting()
	n, err := b.ReadCloser.Read(p)
	b.clock.stopWaiting()
	if err != nil && !errors.Is(err, io.EOF) && b.clock.expired() {
		return n, &noAnswerError{host: b.host, wait: b.clock.wait, partway: true}
	}
	return n, err
}

func (b *awaitedBody) Close() error {
	err := b.ReadCloser.Close()
	b.clock.cancel()
	return err
}

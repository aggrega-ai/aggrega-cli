// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/opprogress"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
)

// This file is the one place that decides how a request to a registry reacts to a
// registry that says it is too busy. Every registry client this module builds sends
// its requests through RegistryClient — every remote repository and registry target
// (remoteClient), and the one registry request that is not made through a target,
// the read of the trust policy a registry advertises — so a request reaches a
// registry through this handling or not at all.
//
// A registry is too busy when it answers 429 Too Many Requests, or 503 Service
// Unavailable with a Retry-After header (IsBusyAnswer). What follows such an answer:
//
//   - The registry is left alone for as long as it asked — its Retry-After, or
//     BusyDefaultWait where it did not say — plus a small random spread, so several
//     machines told to wait at once do not all come back at the same instant. Every
//     request to that registry waits it out, not only the one that was refused,
//     because a rate limit counts the client rather than the request.
//   - The number of requests allowed in flight to that registry at once is halved,
//     down to one, and raised by one again after each run of as many successful
//     answers as the allowance, up to the command's own ceiling
//     (WithConcurrencyCeiling). The allowance is shared by every request this
//     process makes to the registry, so commands running side by side in one process
//     slow down together.
//   - A request with no body — a read, or the request that opens an upload — is
//     sent again here, with no fixed attempt ceiling, and so is a token request to a
//     sign-in service, whose form can be read again (sentAgainInPlace). A write of
//     the registry protocol is never sent again here: the registry's upload state
//     may have moved on with the body, and only a new upload session recovers it.
//     Such a refusal is handed back to the write that made it, which opens a new
//     session (resendWhileBusy); the wait it owes happens here, before that
//     session's first request, so no refusal is waited out twice.
//   - A registry that has answered nothing but "slow down" for BusyWaitLimit is
//     refused, by name, for every request waiting on it (ErrRegistryTooBusy), and so
//     is a request whose own context deadline ends its wait. Each request still makes
//     at least one attempt, so a later command tries the registry again rather than
//     inheriting the refusal.
//
// The random spread is timing alone: it decides when a request is sent and never
// reaches anything a command writes or prints.

// BusyWaitLimit is how long a registry may answer nothing but "slow down" before
// every request waiting on it is refused (ErrRegistryTooBusy). It is measured per
// registry, from the first such answer since the registry last answered anything
// else.
//
// A rate-limited registry blocks a client for a short, fixed spell — ten seconds is
// common — and a client already down to one request in flight is through within a
// spell or two. Thirty consecutive ten-second spells is no longer a busy registry
// but one that is refusing, and five minutes outlasts that while staying short
// enough that a command against such a registry ends instead of appearing to hang.
const BusyWaitLimit = 5 * time.Minute

// BusyDefaultWait is how long a registry that asked to slow down is left alone
// where its answer did not say how long (no usable Retry-After header): the spell a
// common rate limit blocks a client for.
const BusyDefaultWait = 10 * time.Second

// busySpread is the most the random spread adds to a wait.
const busySpread = time.Second

// busyTiming is the bound, the default wait and the spread a request is held to.
type busyTiming struct {
	limit, wait, spread time.Duration
}

// busyTimingKey keys a busyTiming a test states for the requests made under a
// context (WithBusyTiming).
type busyTimingKey struct{}

// WithBusyTiming returns a context under which requests to a registry are held to
// limit in place of BusyWaitLimit, wait in place of BusyDefaultWait, and a random
// spread of at most spread. It exists for tests, so a test of a registry that never
// stops refusing ends in milliseconds rather than minutes, and so tests running at
// once each state their own; production never calls it. A negative spread is taken
// as zero.
func WithBusyTiming(ctx context.Context, limit, wait, spread time.Duration) context.Context {
	return context.WithValue(ctx, busyTimingKey{}, busyTiming{limit: limit, wait: wait, spread: max(spread, 0)})
}

// busyTimingFor returns the timing the requests made under ctx are held to.
func busyTimingFor(ctx context.Context) busyTiming {
	if stated, ok := ctx.Value(busyTimingKey{}).(busyTiming); ok {
		return stated
	}
	return busyTiming{limit: BusyWaitLimit, wait: BusyDefaultWait, spread: busySpread}
}

// ErrRegistryTooBusy is returned when a registry kept answering that it is too
// busy until the request gave up: it answered nothing else for BusyWaitLimit, or
// the request's own deadline ended its wait. The error naming it names the
// registry, its last answer, and how long it had answered nothing else.
func ErrRegistryTooBusy() error { return errRegistryTooBusy }

var errRegistryTooBusy = cerrors.Register("registry-too-busy", cerrors.Meta{
	ShortCode: "E0378",
	Message:   "oci: the registry kept asking to slow down",
	RulePlace: cerrors.NotARule("A registry answered every request that it was too busy for longer than a request " +
		"waits on it. It reports a network operation that did not complete rather than a rule over anything an author wrote."),
})

// busyRefusal is the refusal of a request a registry kept asking to slow down:
// the registry, the status it last answered, and how long it had answered nothing
// but "slow down" — the wait the request gave up on. cause is the request's own
// deadline where that is what ended the wait.
type busyRefusal struct {
	host   string
	status int
	waited time.Duration
	cause  error
}

func (e *busyRefusal) Error() string {
	msg := fmt.Sprintf("%s kept asking to slow down: its last answer was %s, and it had answered nothing else for %s",
		e.host, statusWords(e.status), e.waited.Round(time.Second))
	if e.cause != nil {
		msg += " when the request's own time ran out"
	}
	return msg + ": " + errRegistryTooBusy.Error()
}

func (e *busyRefusal) Unwrap() []error {
	if e.cause != nil {
		return []error{errRegistryTooBusy, e.cause}
	}
	return []error{errRegistryTooBusy}
}

// busyAnswer is a "slow down" answer to a write of the registry protocol, handed
// back to the write that made it rather than sent again here (sentAgainInPlace). It names the registry and the status it answered.
type busyAnswer struct {
	host   string
	status int
}

func (e *busyAnswer) Error() string {
	return fmt.Sprintf("%s answered %s: it asked to slow down", e.host, statusWords(e.status))
}

// statusWords renders a status as a reader meets it — "429 Too Many Requests", or
// the number alone for a status with no name — and is the one rendering every
// message of this package naming a registry's status uses (ReadFailureKind,
// busyRefusal, busyAnswer).
func statusWords(status int) string {
	if text := http.StatusText(status); text != "" {
		return fmt.Sprintf("%d %s", status, text)
	}
	return strconv.Itoa(status)
}

// IsBusyAnswer reports whether resp is a registry saying it is too busy: a 429 Too
// Many Requests, or a 503 Service Unavailable carrying a Retry-After header. A 503
// without one says the registry failed rather than that it asked to be left
// alone, and is an ordinary transient failure.
func IsBusyAnswer(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return true
	case http.StatusServiceUnavailable:
		return resp.Header.Get("Retry-After") != ""
	}
	return false
}

// RegistryClient returns a client sending every request of httpClient through the
// busy-registry handling this file describes, and is how every registry client in
// this module is built. httpClient is the client the network-egress gate handed
// out; a client RegistryClient already returned is returned as it is, so wrapping
// twice never waits twice. A nil client — the egress gate's offline answer — is
// returned as nil.
func RegistryClient(httpClient *http.Client) *http.Client {
	if httpClient == nil || IsRegistryClient(httpClient) {
		return httpClient
	}
	wrapped := *httpClient
	next := wrapped.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	wrapped.Transport = busyTransport{next: next}
	return &wrapped
}

// IsRegistryClient reports whether c sends its requests through the busy-registry
// handling (RegistryClient).
func IsRegistryClient(c *http.Client) bool {
	if c == nil {
		return false
	}
	_, ok := c.Transport.(busyTransport)
	return ok
}

// ceilingKey keys the concurrency ceiling a command states for its requests.
type ceilingKey struct{}

// WithConcurrencyCeiling returns a context under which requests to a registry are
// let back up to n in flight at once once the registry's allowance recovers: the
// most the command itself sends at once. A command that states none recovers to
// the number it had in flight when the allowance was first lowered.
func WithConcurrencyCeiling(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, ceilingKey{}, n)
}

func ceilingFrom(ctx context.Context) int {
	n, _ := ctx.Value(ceilingKey{}).(int)
	return max(n, 0)
}

// askedAgainCountKey keys the count of requests asked again (CountAskedAgain);
// askedAgainOnceKey keys the flag that counts one request once.
type (
	askedAgainCountKey struct{}
	askedAgainOnceKey  struct{}
)

// CountAskedAgain returns a context under which every registry request made under
// it that is asked again — sent again here because the registry asked it to slow
// down, or asked again by a transport beneath a registry client after a failure
// in passing (NoteAskedAgain) — is counted in the returned counter, once per
// request as its caller made it, however many times and by however many layers it
// is asked again.
func CountAskedAgain(ctx context.Context) (context.Context, *atomic.Int64) {
	var asked atomic.Int64
	return context.WithValue(ctx, askedAgainCountKey{}, &asked), &asked
}

// NoteAskedAgain counts the request made under ctx as asked again, in the count
// ctx carries (CountAskedAgain), unless that request was already counted. Every
// layer that asks a registry request again calls it, and the request's one flag —
// placed on its context by the registry client as it first sends the request —
// is what makes the count once per request rather than once per layer. Where ctx
// carries no count it does nothing.
func NoteAskedAgain(ctx context.Context) {
	asked, ok := ctx.Value(askedAgainCountKey{}).(*atomic.Int64)
	if !ok {
		return
	}
	if once, ok := ctx.Value(askedAgainOnceKey{}).(*atomic.Bool); ok && !once.CompareAndSwap(false, true) {
		return
	}
	asked.Add(1)
}

// busyTransport is the transport every registry client's requests go through
// (RegistryClient).
type busyTransport struct {
	next http.RoundTripper
}

func (t busyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := registryhost.Normalize(req.URL.Host)
	h := busyHostFor(host)
	ctx := req.Context()
	if _, counting := ctx.Value(askedAgainCountKey{}).(*atomic.Int64); counting {
		// The one flag that counts this request once as asked again, whichever layer
		// asks it again first.
		ctx = context.WithValue(ctx, askedAgainOnceKey{}, new(atomic.Bool))
		req = req.WithContext(ctx)
	}
	resendable := sentAgainInPlace(req)
	ceiling := ceilingFrom(ctx)
	for resend := 0; ; resend++ {
		if err := h.enter(ctx, host, resend > 0, ceiling); err != nil {
			return nil, err
		}
		out := req
		if resend > 0 {
			NoteAskedAgain(ctx)
			if req.Body != nil && req.Body != http.NoBody {
				body, berr := req.GetBody()
				if berr != nil {
					return nil, berr
				}
				out = req.Clone(ctx)
				out.Body = body
			}
		}
		resp, err := t.next.RoundTrip(out)
		if !h.answered(ctx, host, resp, err) {
			if err != nil {
				// A deadline that ran out while the request was on its way to a
				// registry that had been refusing it ends the wait as one that runs
				// out while the request is held does.
				if refusal := h.deadlineRefusal(ctx, host); refusal != nil {
					return nil, refusal
				}
			}
			return resp, err
		}
		status := resp.StatusCode
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		if !resendable {
			return nil, &busyAnswer{host: host, status: status}
		}
	}
}

// sentAgainInPlace reports whether a request refused with "slow down" is sent
// again here, as it is: a request with no body — a read, or the request that
// opens an upload — and a request in the form a registry client asks a sign-in
// service for a token in: a POST carrying a form of credentials
// (application/x-www-form-urlencoded) whose body can be read again from the start
// (GetBody). Every other request with a body is handed back to the write that made
// it (busyAnswer) and never sent again here, because the registry's upload state
// may have moved on with the body.
//
// The request's form decides, not its path. A registry may hand out an upload
// location outside the protocol's /v2/ paths, and a write redirected to another
// address carries its body there too, so a path says nothing about whether a body
// is an upload; and a sign-in service may sit under /v2/ itself. An upload is never
// a form of credentials, and a token request is never a PUT or a PATCH.
func sentAgainInPlace(req *http.Request) bool {
	if req.Body == nil || req.Body == http.NoBody {
		return true
	}
	if req.Method != http.MethodPost || req.GetBody == nil {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/x-www-form-urlencoded"
}

// resendWhileBusy runs attempt — one whole write, opening its own upload session —
// again each time it fails because the registry asked to slow down on a request
// carrying the write's body (busyAnswer), which this file never sends again itself. It waits for
// nothing of its own: the wait the registry asked for is taken by the next
// attempt's first request, on its way through busyTransport. A write whose every
// attempt is refused this way for BusyWaitLimit is refused (ErrRegistryTooBusy),
// which bounds a write even against a registry that keeps accepting the request
// opening a session and refusing the one carrying its body.
func resendWhileBusy[T any](ctx context.Context, attempt func() (T, error)) (T, error) {
	var first time.Time
	for {
		v, err := attempt()
		var answer *busyAnswer
		if err == nil || !errors.As(err, &answer) {
			return v, err
		}
		now := time.Now()
		if first.IsZero() {
			first = now
		}
		if now.Sub(first) >= busyTimingFor(ctx).limit {
			return v, &busyRefusal{host: answer.host, status: answer.status, waited: now.Sub(first)}
		}
		if cerr := ctx.Err(); cerr != nil {
			return v, cerr
		}
	}
}

// busyHosts holds, keyed by registry host as registryhost.Normalize spells it,
// the state every request to that registry shares.
var busyHosts sync.Map

func busyHostFor(host string) *busyHost {
	if h, ok := busyHosts.Load(host); ok {
		return h.(*busyHost)
	}
	h, _ := busyHosts.LoadOrStore(host, &busyHost{changed: make(chan struct{})})
	return h.(*busyHost)
}

// busyHost is what every request to one registry shares: how many are in flight,
// how many may be (limit, zero when no limit is in force), when the registry may be
// asked again, and since when it has answered nothing but "slow down".
type busyHost struct {
	mu sync.Mutex
	// changed is closed, and replaced, whenever a request waiting to be sent may
	// now go: a slot was freed, the allowance changed, a wait ended.
	changed chan struct{}

	inFlight int
	limit    int
	// ceiling is the highest concurrency ceiling a command stated for this host;
	// loweredFrom is how many requests were in flight when the allowance was first
	// lowered, which a command stating no ceiling recovers to.
	ceiling     int
	loweredFrom int
	// successes counts the answers since the allowance last moved.
	successes int

	streakSince time.Time
	resumeAt    time.Time
	lastStatus  int
	// reportDue is set by the answer that opens a wait, and taken by the first
	// request that waits it out, which reports the wait to whoever is watching.
	reportDue bool
}

// broadcast wakes every request waiting to be sent. The caller holds h.mu.
func (h *busyHost) broadcast() {
	close(h.changed)
	h.changed = make(chan struct{})
}

// enter holds a request until it may be sent to host: until the registry may be
// asked again, and until the allowance has room for it. It returns the refusal
// where the request must not be sent: the registry has answered nothing but "slow
// down" for BusyWaitLimit and this request has already been sent once
// (attempted), or the request's own deadline ended its wait while the registry was
// refusing. A request entering for the first time is always let through once the
// registry may be asked again, so every request makes at least one attempt.
func (h *busyHost) enter(ctx context.Context, host string, attempted bool, ceiling int) (err error) {
	var end opprogress.End
	defer func() {
		if end != nil {
			end(err)
		}
	}()
	limit := busyTimingFor(ctx).limit
	for {
		h.mu.Lock()
		if ceiling > h.ceiling {
			h.ceiling = ceiling
		}
		now := time.Now()
		streaking := !h.streakSince.IsZero()
		if attempted && streaking && now.Sub(h.streakSince) >= limit {
			refusal := &busyRefusal{host: host, status: h.lastStatus, waited: now.Sub(h.streakSince)}
			h.mu.Unlock()
			return refusal
		}
		var wakeAt time.Time
		report := ""
		switch {
		case now.Before(h.resumeAt):
			wakeAt = h.resumeAt
			if attempted && streaking {
				if bound := h.streakSince.Add(limit); bound.Before(wakeAt) {
					wakeAt = bound
				}
			}
			if h.reportDue {
				h.reportDue = false
				report = fmt.Sprintf("%s asked to slow down; waiting %d s", host, int(math.Ceil(h.resumeAt.Sub(now).Seconds())))
			}
		case h.limit > 0 && h.inFlight >= h.limit:
			// Wait for a slot: a request in flight answering frees one.
		default:
			h.inFlight++
			h.mu.Unlock()
			return nil
		}
		changed := h.changed
		h.mu.Unlock()

		if report != "" && end == nil {
			end = opprogress.Wait(ctx, report)
		}
		if werr := waitFor(ctx, changed, wakeAt); werr != nil {
			if refusal := h.deadlineRefusal(ctx, host); refusal != nil {
				return refusal
			}
			return werr
		}
	}
}

// deadlineRefusal returns the refusal of a request whose own deadline ran out
// while host was answering nothing but "slow down", carrying that deadline; nil
// where the deadline has not run out, or the registry was not refusing. A caller
// that withdrew is not refused: its cancellation is its own answer.
func (h *busyHost) deadlineRefusal(ctx context.Context, host string) error {
	cerr := ctx.Err()
	if !errors.Is(cerr, context.DeadlineExceeded) {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.streakSince.IsZero() {
		return nil
	}
	return &busyRefusal{host: host, status: h.lastStatus, waited: time.Since(h.streakSince), cause: cerr}
}

// waitFor waits until changed is closed, wakeAt is reached (where it is set), or
// ctx ends, and returns ctx's error in the last case.
func waitFor(ctx context.Context, changed <-chan struct{}, wakeAt time.Time) error {
	var timer <-chan time.Time
	if !wakeAt.IsZero() {
		t := time.NewTimer(time.Until(wakeAt))
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-changed:
	case <-timer:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// answered records the registry's answer to a request enter let through, frees
// its slot, and reports whether the answer was "slow down" (IsBusyAnswer). A
// request that got no answer at all — a connection that failed — moves neither the
// allowance nor the run of "slow down" answers: the registry said nothing.
func (h *busyHost) answered(ctx context.Context, host string, resp *http.Response, sendErr error) (busy bool) {
	now := time.Now()
	timing := busyTimingFor(ctx)
	wait, spread := timing.wait, timing.spread
	h.mu.Lock()
	defer h.mu.Unlock()
	inFlight := h.inFlight
	h.inFlight--
	defer h.broadcast()
	if sendErr != nil || resp == nil {
		return false
	}
	if !IsBusyAnswer(resp) {
		// Any other answer ends the run of "slow down" answers; one the registry
		// served without failing also counts towards raising the allowance again.
		h.streakSince = time.Time{}
		if resp.StatusCode < http.StatusInternalServerError {
			h.raise(host)
		}
		return false
	}
	h.lastStatus = resp.StatusCode
	if h.streakSince.IsZero() {
		h.streakSince = now
	}
	// One wait and one halving per spell the registry asked for: the requests that
	// were in flight with this one are answered the same, and lowering the allowance
	// for each of them would take it to one on a single refusal.
	if !now.Before(h.resumeAt) {
		asked := retryAfter(resp.Header, now)
		if asked <= 0 {
			asked = wait
		}
		var jitter time.Duration
		if spread > 0 {
			jitter = rand.N(spread)
		}
		h.resumeAt = now.Add(asked + jitter)
		h.reportDue = true
		h.lower(host, inFlight)
	}
	return true
}

// lower halves the allowance, down to one. inFlight is how many requests were in
// flight, the refused one included. The caller holds h.mu.
func (h *busyHost) lower(host string, inFlight int) {
	current := h.limit
	if current == 0 {
		current = max(inFlight, 1)
		h.loweredFrom = current
	}
	h.limit = max(current/2, 1)
	h.successes = 0
	observeAllowance(host, h.limit)
}

// raise counts one answer that was not "slow down" and, after as many of them as
// the allowance, raises it by one; at the command's ceiling the limit is lifted.
// The caller holds h.mu.
func (h *busyHost) raise(host string) {
	if h.limit == 0 {
		return
	}
	h.successes++
	if h.successes < h.limit {
		return
	}
	h.successes = 0
	h.limit++
	top := h.ceiling
	if top == 0 {
		top = h.loweredFrom
	}
	if h.limit >= top {
		h.limit, h.loweredFrom = 0, 0
		observeAllowance(host, top)
		return
	}
	observeAllowance(host, h.limit)
}

// retryAfter reads a Retry-After header, in either of its two forms: a number of
// seconds, or the time to ask again at. A header that is absent, malformed, or
// names a time already past asks for no particular wait.
func retryAfter(h http.Header, now time.Time) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := at.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// allowanceObservers holds the functions ObserveRegistryAllowance installed, in
// the order they were installed.
var allowanceObservers struct {
	sync.Mutex
	entries []allowanceObserver
	n       int
}

type allowanceObserver struct {
	id int
	fn func(host string, allowed int)
}

// ObserveRegistryAllowance calls fn each time the number of requests allowed in
// flight to a registry changes, with the registry's host and the new allowance —
// the ceiling it recovered to where the limit was lifted — and returns the function
// that stops it. It is test instrumentation: it lets a test show the allowance
// falling and recovering without timing anything. fn is called while the
// registry's state is held, so it must not make a registry request itself.
func ObserveRegistryAllowance(fn func(host string, allowed int)) (stop func()) {
	allowanceObservers.Lock()
	defer allowanceObservers.Unlock()
	id := allowanceObservers.n
	allowanceObservers.n++
	allowanceObservers.entries = append(allowanceObservers.entries, allowanceObserver{id: id, fn: fn})
	return func() {
		allowanceObservers.Lock()
		defer allowanceObservers.Unlock()
		kept := allowanceObservers.entries[:0]
		for _, e := range allowanceObservers.entries {
			if e.id != id {
				kept = append(kept, e)
			}
		}
		allowanceObservers.entries = kept
	}
}

func observeAllowance(host string, allowed int) {
	allowanceObservers.Lock()
	defer allowanceObservers.Unlock()
	for _, e := range allowanceObservers.entries {
		e.fn(host, allowed)
	}
}

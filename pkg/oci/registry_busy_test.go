// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/aggrega-ai/aggrega-cli/internal/opprogress"
	"github.com/aggrega-ai/aggrega-cli/internal/retry"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// busyTestTiming holds a test's requests to a short wait and no random spread,
// under the production bound, so a registry asking to slow down costs
// milliseconds rather than ten seconds a time.
func busyTestTiming(ctx context.Context) context.Context {
	return oci.WithBusyTiming(ctx, oci.BusyWaitLimit, 5*time.Millisecond, 0)
}

// refusingServer answers the first `times` requests matching match with status
// (and a Retry-After header where retryAfter is set), every one of them when times
// is negative, and everything else 200 with a fixed body. It counts every request
// that matched.
type refusingServer struct {
	status     int
	retryAfter string
	times      int

	mu      sync.Mutex
	matched int
}

func (s *refusingServer) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.matched++
	refuse := s.times < 0 || s.matched <= s.times
	s.mu.Unlock()
	if refuse {
		if s.retryAfter != "" {
			w.Header().Set("Retry-After", s.retryAfter)
		}
		http.Error(w, "slow down", s.status)
		return
	}
	_, _ = io.WriteString(w, "served")
}

func (s *refusingServer) asked() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.matched
}

// TestARegistryAskingToSlowDownIsAskedAgainOnlyWhereItAskedSo drives one read
// through a registry client against a server that refuses it a few times, one
// refusal per row, and holds the client to which refusals are a registry asking to
// slow down: a 429, and a 503 carrying Retry-After are waited out and asked again
// until served, five times over — more than the retry budget a failing read is
// given (retry.MaxAttempts), so a client that capped them at that budget fails the
// row. A 503 with no Retry-After says the registry failed, not that it asked for
// time, and is handed straight back after one attempt, for the ordinary retry to
// judge.
func TestARegistryAskingToSlowDownIsAskedAgainOnlyWhereItAskedSo(t *testing.T) {
	t.Parallel()
	const refusals = 5
	rows := []struct {
		name       string
		status     int
		retryAfter string
		wantServed bool
		wantAsked  int
	}{
		{"a 429 naming no wait", http.StatusTooManyRequests, "", true, refusals + 1},
		{"a 429 naming a wait of zero seconds", http.StatusTooManyRequests, "0", true, refusals + 1},
		{"a 503 carrying Retry-After", http.StatusServiceUnavailable, "0", true, refusals + 1},
		{"a 503 carrying no Retry-After", http.StatusServiceUnavailable, "", false, 1},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if refusals <= retry.MaxAttempts {
				t.Fatalf("the row refuses %d times, no more than the retry budget of %d, so it cannot tell the two apart", refusals, retry.MaxAttempts)
			}
			srv := &refusingServer{status: row.status, retryAfter: row.retryAfter, times: refusals}
			ts := httptest.NewServer(srv)
			t.Cleanup(ts.Close)
			client := oci.RegistryClient(&http.Client{})
			req, err := http.NewRequestWithContext(busyTestTiming(t.Context()), http.MethodGet, ts.URL+"/v2/", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("the read failed: %v", err)
			}
			_ = resp.Body.Close()
			if served := resp.StatusCode == http.StatusOK; served != row.wantServed {
				t.Errorf("answered %d; served = %v, want %v", resp.StatusCode, served, row.wantServed)
			}
			if got := srv.asked(); got != row.wantAsked {
				t.Errorf("the server was asked %d time(s); want %d", got, row.wantAsked)
			}
		})
	}
}

// TestARegistryThatNeverStopsAskingToSlowDownIsRefusedByName holds the two ways a
// wait on a registry that answers nothing but 429 ends: the bound on such a run of
// answers, stated here as a tenth of a second (oci.WithBusyTiming), and the
// request's own deadline, with the bound left at an hour. Either way the request
// ends with the refusal (oci.ErrRegistryTooBusy) naming the registry and its last
// answer, and the deadline row also carries the deadline. A client with no bound
// on its waiting never returns from the first row, and the test is then stopped by
// its run's own time limit rather than passing.
func TestARegistryThatNeverStopsAskingToSlowDownIsRefusedByName(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name         string
		ctx          func(context.Context) (context.Context, context.CancelFunc)
		wantDeadline bool
	}{
		{"the bound on a run of slow-down answers", func(ctx context.Context) (context.Context, context.CancelFunc) {
			return oci.WithBusyTiming(ctx, 100*time.Millisecond, 5*time.Millisecond, 0), func() {}
		}, false},
		{"the request's own deadline", func(ctx context.Context) (context.Context, context.CancelFunc) {
			return context.WithTimeout(oci.WithBusyTiming(ctx, time.Hour, 5*time.Millisecond, 0), 100*time.Millisecond)
		}, true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			srv := &refusingServer{status: http.StatusTooManyRequests, times: -1}
			ts := httptest.NewServer(srv)
			t.Cleanup(ts.Close)
			host := strings.TrimPrefix(ts.URL, "http://")
			ctx, cancel := row.ctx(t.Context())
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v2/", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := oci.RegistryClient(&http.Client{}).Do(req)
			if err == nil {
				_ = resp.Body.Close()
				t.Fatalf("the read was answered %d; want the refusal of a registry that kept asking to slow down", resp.StatusCode)
			}
			if !errors.Is(err, oci.ErrRegistryTooBusy()) {
				t.Fatalf("error = %v; want oci.ErrRegistryTooBusy", err)
			}
			if got := errors.Is(err, context.DeadlineExceeded); got != row.wantDeadline {
				t.Errorf("the refusal carries the deadline = %v; want %v (%v)", got, row.wantDeadline, err)
			}
			for _, want := range []string{host + " kept asking to slow down", "429 Too Many Requests"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q does not name %q", err, want)
				}
			}
			if oci.IsTransientRegistryError(err) {
				t.Errorf("the refusal reads as a transient failure, so a retry would ask again past the bound: %v", err)
			}
			if srv.asked() < 2 {
				t.Errorf("the registry was asked %d time(s); a request is asked again while the registry asks for time", srv.asked())
			}
		})
	}
}

// bodyRefusingServer answers its first request 429, naming no wait, and every
// later one 200, recording the body each request carried.
type bodyRefusingServer struct {
	mu     sync.Mutex
	bodies []string
}

func (s *bodyRefusingServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	s.mu.Lock()
	s.bodies = append(s.bodies, string(body))
	first := len(s.bodies) == 1
	s.mu.Unlock()
	if first {
		http.Error(w, "slow down", http.StatusTooManyRequests)
		return
	}
	_, _ = io.WriteString(w, `{"access_token":"t"}`)
}

// TestARequestWithABodyIsSentAgainOnlyInTheFormOfATokenRequest sends requests
// carrying a body to a server that asks each to slow down once, one row per form
// and path, and holds the registry client to which of them it sends again itself.
// A token request — a POST carrying a form of credentials whose body can be read
// again — is sent again, with the same body, and answered, wherever its path is,
// under /v2/ included. Every other request with a body — an upload's PUT, under
// /v2/ or at a location outside it, or a POST carrying content — is never sent
// again here: it is handed back as the registry's answer, for the write to open a
// new upload, and the server is asked once.
//
// A rule deciding by path fails the two rows that cross it: the token request
// under /v2/ is not sent again, and the PUT outside /v2/ is.
func TestARequestWithABodyIsSentAgainOnlyInTheFormOfATokenRequest(t *testing.T) {
	t.Parallel()
	const (
		form        = "grant_type=refresh_token&service=registry.example"
		formType    = "application/x-www-form-urlencoded"
		contentType = "application/octet-stream"
	)
	rows := []struct {
		name        string
		method      string
		path        string
		contentType string
		wantAsked   int
		wantErr     bool
	}{
		{"a token request to a sign-in service", http.MethodPost, "/oauth2/token", formType, 2, false},
		{"a token request to a sign-in service under /v2/", http.MethodPost, "/v2/auth", formType, 2, false},
		{"a finalizing upload of the registry protocol", http.MethodPut, "/v2/component/core/busy/blobs/uploads/session?digest=sha256:0", contentType, 1, true},
		{"a finalizing upload at a location outside /v2/", http.MethodPut, "/uploads/session?digest=sha256:0", contentType, 1, true},
		{"a POST carrying content rather than a form", http.MethodPost, "/v2/component/core/busy/blobs/uploads/?digest=sha256:0", contentType, 1, true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			srv := &bodyRefusingServer{}
			ts := httptest.NewServer(srv)
			t.Cleanup(ts.Close)
			req, err := http.NewRequestWithContext(busyTestTiming(t.Context()), row.method, ts.URL+row.path, strings.NewReader(form))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", row.contentType)
			resp, err := oci.RegistryClient(&http.Client{}).Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
			if gotErr := err != nil; gotErr != row.wantErr {
				t.Fatalf("error = %v; want an error = %v", err, row.wantErr)
			}
			srv.mu.Lock()
			defer srv.mu.Unlock()
			if len(srv.bodies) != row.wantAsked {
				t.Fatalf("the server was asked %d time(s); want %d", len(srv.bodies), row.wantAsked)
			}
			for i, body := range srv.bodies {
				if body != form {
					t.Errorf("request %d carried %q; want the form %q each time", i+1, body, form)
				}
			}
		})
	}
}

// stepRecorder is an opprogress observer that records every step it is told an
// operation waits on, and whether each has ended.
type stepRecorder struct {
	mu    sync.Mutex
	steps []string
	ended int
}

func (r *stepRecorder) Begin(step string) opprogress.End {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, step)
	return func(error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.ended++
	}
}

func (*stepRecorder) Yield() {}

func (*stepRecorder) Beside(w io.Writer) io.Writer { return w }

// TestAWaitOnABusyRegistryIsReportedOnceThroughTheProgressReporter asks a read of
// a registry that answers 429 once, under a context carrying a progress observer,
// and requires exactly one step reported — the registry's host, that it asked to
// slow down, and the wait — and that step ended once the read was served. A client
// that waited without reporting reports nothing; one that reported per request
// rather than per wait, or never ended its report, fails the counts.
func TestAWaitOnABusyRegistryIsReportedOnceThroughTheProgressReporter(t *testing.T) {
	t.Parallel()
	srv := &refusingServer{status: http.StatusTooManyRequests, times: 1}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	host := strings.TrimPrefix(ts.URL, "http://")
	rec := &stepRecorder{}
	ctx := opprogress.With(busyTestTiming(t.Context()), rec)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v2/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := oci.RegistryClient(&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("the read failed: %v", err)
	}
	_ = resp.Body.Close()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.steps) != 1 || !strings.HasPrefix(rec.steps[0], host+" asked to slow down; waiting ") || !strings.HasSuffix(rec.steps[0], " s") {
		t.Fatalf("steps reported = %q; want one naming %s, that it asked to slow down, and the wait", rec.steps, host)
	}
	if rec.ended != 1 {
		t.Errorf("the reported wait ended %d time(s); want once, when the read went out again", rec.ended)
	}
}

// uploadRefusingRegistry serves the distribution protocol from an in-memory
// registry and, once armed, answers the first finalizing PUT of a blob upload 429
// — a registry asking to slow down on the request that carries the body. It
// answers a second PUT on an upload location that already received one 416, as a
// registry does that rewrites an upload's state as it takes in the body, so a
// client that sent the refused PUT again, body and all, fails here rather than
// passing because this registry happened to be forgiving. It records every request
// after arming.
type uploadRefusingRegistry struct {
	inner http.Handler

	mu       sync.Mutex
	armed    bool
	refused  bool
	putSeen  map[string]bool
	requests []wireRequest
}

func (r *uploadRefusingRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	if !r.armed {
		r.mu.Unlock()
		r.inner.ServeHTTP(w, req)
		return
	}
	rec := wireRequest{method: req.Method, path: req.URL.Path, digest: req.URL.Query().Get("digest")}
	if req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/blobs/uploads/") {
		if r.putSeen[req.URL.Path] {
			rec.status = http.StatusRequestedRangeNotSatisfiable
			r.requests = append(r.requests, rec)
			r.mu.Unlock()
			_, _ = io.Copy(io.Discard, req.Body)
			writeRegistryError(w, rec.status, "RANGE_ERROR", "the upload's state does not match this request")
			return
		}
		r.putSeen[req.URL.Path] = true
		if !r.refused {
			r.refused = true
			rec.status = http.StatusTooManyRequests
			r.requests = append(r.requests, rec)
			r.mu.Unlock()
			_, _ = io.Copy(io.Discard, req.Body)
			writeRegistryError(w, rec.status, "TOOMANYREQUESTS", "slow down")
			return
		}
	}
	r.requests = append(r.requests, rec)
	r.mu.Unlock()
	r.inner.ServeHTTP(w, req)
}

// TestAWriteRefusedToSlowDownOpensANewUploadAndIsNotWaitedOutTwice pushes an
// entity to a registry that asks to slow down on the first request carrying an
// upload's body, and requires the push to land, on the wire, the way a write may be
// sent again: a new POST opening a new upload, then a PUT on the location that POST
// opened — never the refused PUT sent again to its own location, which the registry
// above answers 416, as the registry the hub runs does.
//
// The retry budget's backoff is set to an hour. A write whose refusal also spent
// that budget — waiting out its backoff on top of the wait the registry asked for —
// would not return, and the ceiling below is a liveness bound separating "returns"
// from "waits twice", not a performance budget.
func TestAWriteRefusedToSlowDownOpensANewUploadAndIsNotWaitedOutTwice(t *testing.T) {
	// Not parallel: mutates the process-global retry.BaseDelay seam.
	prev := retry.BaseDelay
	retry.BaseDelay = time.Hour
	t.Cleanup(func() { retry.BaseDelay = prev })

	reg := &uploadRefusingRegistry{inner: registry.New()}
	ts := httptest.NewServer(reg)
	t.Cleanup(ts.Close)
	host := strings.TrimPrefix(ts.URL, "http://")
	ctx := busyTestTiming(t.Context())
	repo, err := oci.NewRemoteRepository(host+"/component/core/busy", &http.Client{})
	if err != nil {
		t.Fatalf("NewRemoteRepository: %v", err)
	}
	reg.mu.Lock()
	reg.armed = true
	reg.putSeen = map[string]bool{}
	reg.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := oci.Push(ctx, repo, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\nmetadata:\n  name: busy\n"), nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			reg.mu.Lock()
			wire := describeWire(reg.requests)
			reg.mu.Unlock()
			t.Fatalf("the push failed on one refusal to slow down: %v\n%s", err, wire)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the push did not return: the refusal spent the retry budget's backoff as well as the wait the registry asked for")
	}

	reg.mu.Lock()
	requests := slices.Clone(reg.requests)
	reg.mu.Unlock()
	refusedAt := -1
	for i, r := range requests {
		if r.status == http.StatusTooManyRequests {
			refusedAt = i
			break
		}
	}
	if refusedAt < 0 {
		t.Fatalf("the registry never refused a PUT, so this run proves nothing about a refused write\n%s", describeWire(requests))
	}
	refused := requests[refusedAt]
	postBetween, sentAgainAt := false, -1
	for i := refusedAt + 1; i < len(requests); i++ {
		r := requests[i]
		if r.method == http.MethodPost && strings.HasSuffix(strings.TrimSuffix(r.path, "/"), "/blobs/uploads") {
			postBetween = true
			continue
		}
		if r.method == http.MethodPut && r.digest == refused.digest {
			sentAgainAt = i
			break
		}
	}
	if sentAgainAt < 0 {
		t.Fatalf("the blob whose PUT was refused (%s) was never uploaded again\n%s", refused.digest, describeWire(requests))
	}
	if !postBetween || requests[sentAgainAt].path == refused.path {
		t.Fatalf("the refused PUT was sent again without a new upload opened for it\n%s", describeWire(requests))
	}
}

// burstLimiter is a registry fronting an in-memory one that, once armed, limits
// its one client per window the way a rate limit counting requests does: in each
// of the first `windows` windows it serves `burst` requests and refuses the next
// `block` with 429, naming no wait; after those windows it serves everything. A
// window is counted in requests rather than in time, so whether the limit is met
// does not depend on how fast the machine runs the test.
type burstLimiter struct {
	inner                 http.Handler
	burst, block, windows int

	mu      sync.Mutex
	armed   bool
	seen    int
	refused int
}

func (l *burstLimiter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	l.mu.Lock()
	refuse := false
	if l.armed {
		window, at := l.seen/(l.burst+l.block), l.seen%(l.burst+l.block)
		refuse = window < l.windows && at >= l.burst
		l.seen++
		if refuse {
			l.refused++
		}
	}
	l.mu.Unlock()
	if refuse {
		http.Error(w, "slow down", http.StatusTooManyRequests)
		return
	}
	l.inner.ServeHTTP(w, req)
}

func (l *burstLimiter) arm() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.armed = true
}

// allowanceTrail records every allowance ObserveRegistryAllowance reports for one
// host, in order. It is test instrumentation only.
type allowanceTrail struct {
	host string
	mu   sync.Mutex
	seen []int
}

func watchAllowance(t *testing.T, host string) *allowanceTrail {
	trail := &allowanceTrail{host: host}
	stop := oci.ObserveRegistryAllowance(func(h string, allowed int) {
		if h != host {
			return
		}
		trail.mu.Lock()
		defer trail.mu.Unlock()
		trail.seen = append(trail.seen, allowed)
	})
	t.Cleanup(stop)
	return trail
}

// fellAndRecovered fails t unless the trail went below ceiling and ended back at
// it.
func (a *allowanceTrail) fellAndRecovered(t *testing.T, ceiling int) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.seen) == 0 {
		t.Fatalf("the allowance for %s never moved", a.host)
	}
	if slices.Min(a.seen) >= ceiling {
		t.Errorf("the allowance for %s never fell below %d: %v", a.host, ceiling, a.seen)
	}
	if last := a.seen[len(a.seen)-1]; last != ceiling {
		t.Errorf("the allowance for %s ended at %d, not back at %d: %v", a.host, last, ceiling, a.seen)
	}
}

// TestPushesAndPullsSlowDownTogetherAndRecover pushes forty entities, eight at a
// time under a stated ceiling of eight (oci.WithConcurrencyCeiling), to a registry
// that rate-limits its client per window, and then pulls them back eight at a time
// from the same registry limiting again. Every push and every pull completes, and
// for each the allowance of requests in flight to the registry — shared by every
// request of the process — falls below eight when the registry refuses and is
// raised back to eight as it answers again (oci.ObserveRegistryAllowance, test
// instrumentation; no time is measured).
//
// A client that ignored the refusals fails the pushes and the pulls; one that
// never lowered its allowance never reports one below eight; one that never raised
// it again ends below eight.
func TestPushesAndPullsSlowDownTogetherAndRecover(t *testing.T) {
	t.Parallel()
	// Forty entities leave the pulls — two requests each — enough answers after the
	// last refusal to raise an allowance halved twice, to two, back to eight
	// (2+3+4+5+6+7 = 27 answers), with the refused windows taken out.
	const (
		entities = 40
		ceiling  = 8
	)
	limiter := &burstLimiter{inner: registry.New(), burst: 6, block: 3, windows: 2}
	ts := httptest.NewServer(limiter)
	t.Cleanup(ts.Close)
	host := strings.TrimPrefix(ts.URL, "http://")
	ctx := oci.WithConcurrencyCeiling(busyTestTiming(t.Context()), ceiling)

	repos := make([]string, entities)
	for i := range repos {
		repos[i] = fmt.Sprintf("%s/component/core/busy-%02d", host, i)
	}
	contentOf := func(i int) []byte {
		return []byte(fmt.Sprintf("kind: Component\nmetadata:\n  name: busy-%02d\n", i))
	}
	eightAtATime := func(t *testing.T, what string, do func(i int) error) {
		t.Helper()
		next := make(chan int)
		errs := make(chan error, entities)
		var wg sync.WaitGroup
		for range ceiling {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range next {
					if err := do(i); err != nil {
						errs <- fmt.Errorf("%s %s: %w", what, repos[i], err)
					}
				}
			}()
		}
		for i := range repos {
			next <- i
		}
		close(next)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
	}

	t.Run("pushes", func(t *testing.T) {
		limiter.arm()
		trail := watchAllowance(t, host)
		eightAtATime(t, "push", func(i int) error {
			repo, err := oci.NewRemoteRepository(repos[i], &http.Client{})
			if err != nil {
				return err
			}
			_, err = oci.Push(ctx, repo, "1.0.0", oci.MediaTypeComponent, contentOf(i), nil)
			return err
		})
		trail.fellAndRecovered(t, ceiling)
	})

	t.Run("pulls", func(t *testing.T) {
		limiter.mu.Lock()
		limiter.seen = 0
		limiter.mu.Unlock()
		trail := watchAllowance(t, host)
		eightAtATime(t, "pull", func(i int) error {
			repo, err := oci.NewRemoteRepository(repos[i], &http.Client{})
			if err != nil {
				return err
			}
			got, err := oci.Pull(ctx, repo, "1.0.0")
			if err != nil {
				return err
			}
			if string(got.Bytes) != string(contentOf(i)) {
				return fmt.Errorf("pulled %q; want what was pushed", got.Bytes)
			}
			return nil
		})
		trail.fellAndRecovered(t, ceiling)
	})

	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.refused == 0 {
		t.Fatal("the registry refused nothing, so this run proves nothing about slowing down")
	}
}

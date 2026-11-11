// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package egress_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// shortWait is the wait every request below states for itself, so a refusal
// arrives in a fraction of a second rather than after the production wait.
const shortWait = 200 * time.Millisecond

// liveness is how long a request below may take before the test gives up on it.
// It separates "refused" from "waits without end" and is not a performance
// budget: a request held to shortWait ends long before it.
const liveness = 10 * time.Second

// get sends one request through the production egress client, under a context
// stating shortWait and bounded by the liveness ceiling, and reads the whole
// answer. It returns what was read and the error that ended the request or the
// read.
func get(t *testing.T, url string, body io.Reader) (string, error) {
	t.Helper()
	client, err := egress.HTTPClient(workspaceconfig.Config{})
	if err != nil {
		t.Fatalf("HTTPClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), liveness)
	defer cancel()
	method := http.MethodGet
	if body != nil {
		method = http.MethodPut
	}
	req, err := http.NewRequestWithContext(egress.WithAnswerWait(ctx, shortWait), method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return string(data), err
}

// TestARequestIsHeldToTheWaitOnItsServersAnswer drives the egress client against
// servers that never answer, that stop part way through an answer, that answer
// slowly but steadily, and that are sent a slow upload, and reads which of them
// are refused and how.
func TestARequestIsHeldToTheWaitOnItsServersAnswer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		handler http.HandlerFunc
		body    io.Reader
		// refused names the words the refusal must carry; empty where the request
		// must succeed.
		refused string
		want    string
	}{
		{
			name: "a server that never begins its answer is refused, naming it",
			handler: func(w http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
			},
			refused: "did not answer within 200ms",
		},
		{
			name: "a server that stops part way through its answer is refused, naming it",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("partial"))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			},
			refused: "stopped answering: nothing more of its answer arrived for 200ms",
		},
		{
			name: "an answer that keeps arriving is never refused, however long it takes in all",
			handler: func(w http.ResponseWriter, r *http.Request) {
				for range 8 {
					_, _ = w.Write([]byte("x"))
					w.(http.Flusher).Flush()
					time.Sleep(shortWait / 4)
				}
			},
			want: "xxxxxxxx",
		},
		{
			name: "a slow upload is never refused: the wait begins once the request is sent",
			handler: func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				_, _ = w.Write(data)
			},
			body: &slowReader{chunks: 6, pause: shortWait / 2},
			want: "uuuuuu",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(tc.handler)
			t.Cleanup(srv.Close)
			got, err := get(t, srv.URL, tc.body)
			if tc.refused == "" {
				if err != nil {
					t.Fatalf("the request was refused: %v", err)
				}
				if got != tc.want {
					t.Errorf("read %q, want %q", got, tc.want)
				}
				return
			}
			if !errors.Is(err, egress.ErrNoAnswerInTime()) {
				t.Fatalf("want the no-answer refusal, got %v", err)
			}
			host := strings.TrimPrefix(srv.URL, "http://")
			if !strings.Contains(err.Error(), host+" "+tc.refused) {
				t.Errorf("the refusal must name the server and the wait (%q); got %q", host+" "+tc.refused, err.Error())
			}
			var netErr net.Error
			if !errors.As(err, &netErr) || !netErr.Timeout() {
				t.Errorf("the refusal must report a timeout, so a request asked again on one is asked again; got %v", err)
			}
			if !oci.IsTransientRegistryError(err) {
				t.Errorf("the registry-failure classifier must read the refusal as a failure worth asking again; got %v", err)
			}
		})
	}
}

// TestAWaitStatedForARequestReplacesTheUsualOne pins the wait a request gets: the
// usual one where its context states none or a wait that is not positive, and the
// stated one otherwise.
func TestAWaitStatedForARequestReplacesTheUsualOne(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	if got := egress.AnswerWaitFor(ctx); got != egress.AnswerWait {
		t.Errorf("no stated wait: got %s, want %s", got, egress.AnswerWait)
	}
	if got := egress.AnswerWaitFor(egress.WithAnswerWait(ctx, 0)); got != egress.AnswerWait {
		t.Errorf("a wait that is not positive states nothing: got %s, want %s", got, egress.AnswerWait)
	}
	if got := egress.AnswerWaitFor(egress.WithAnswerWait(ctx, 5*time.Minute)); got != 5*time.Minute {
		t.Errorf("stated wait: got %s, want 5m0s", got)
	}
}

// TestALibraryTakingAStandardTransportGetsOneBoundedByTheWait pins what a library
// that takes a concrete *http.Transport — Helm's chart getter — is handed from
// the egress client, policy by policy: every policy the egress transport enforces
// has its counterpart on the standard transport handed out in its place, because
// a policy enforced only in the wrapper would not reach such a library. A policy
// added to the wrapper adds its row here.
func TestALibraryTakingAStandardTransportGetsOneBoundedByTheWait(t *testing.T) {
	t.Parallel()
	online, err := egress.HTTPClient(workspaceconfig.Config{})
	if err != nil {
		t.Fatalf("HTTPClient: %v", err)
	}
	offline, _ := egress.HTTPClient(workspaceconfig.Config{Offline: true})
	policies := []struct {
		name  string
		holds func() error
	}{
		{
			name: "an offline workspace reaches no network: no transport is handed out",
			holds: func() error {
				if _, ok := egress.StandardTransport(offline); ok {
					return errors.New("the offline workspace's client yielded a standard transport")
				}
				return nil
			},
		},
		{
			name: "the wait for an answer to begin is AnswerWait",
			holds: func() error {
				std, ok := egress.StandardTransport(online)
				if !ok {
					return fmt.Errorf("no standard transport beneath the egress transport (%T)", online.Transport)
				}
				if std.ResponseHeaderTimeout != egress.AnswerWait {
					return fmt.Errorf("it waits %s for an answer to begin; want %s", std.ResponseHeaderTimeout, egress.AnswerWait)
				}
				return nil
			},
		},
	}
	for _, p := range policies {
		if err := p.holds(); err != nil {
			t.Errorf("%s: %v", p.name, err)
		}
	}
	first, _ := egress.StandardTransport(online)
	second, _ := egress.StandardTransport(online)
	if first != second {
		t.Error("the bounded copy must be made once and shared, so its connection pool is one pool")
	}
}

// slowReader is a request body that hands over one byte per read, pausing before
// each, so sending it takes longer than the wait on the answer.
type slowReader struct {
	chunks int
	pause  time.Duration
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.chunks == 0 {
		return 0, io.EOF
	}
	time.Sleep(r.pause)
	r.chunks--
	p[0] = 'u'
	return 1, nil
}

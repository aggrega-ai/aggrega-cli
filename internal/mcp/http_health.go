// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
)

// The two paths the health listener answers, and the only two it serves.
//
// They are fixed rather than configured because the address is the whole of
// what a deployment chooses here: an operator decides where these checks are
// answered, and a supervisor, a balancer or a container runtime is pointed at
// that address with the two names it already expects to find there.
const (
	livenessPath  = "/healthz"
	readinessPath = "/readyz"
)

// readinessState reports whether this process has everything it needs to answer
// a call, which is a different question from whether it is running at all.
//
// The value is read on the health listener's own goroutines and written once on
// the goroutine that launched the process, so it is atomic rather than guarded
// by a lock: there is one transition, it runs in one direction, and a reader
// that sees the earlier value has read an answer that was true when it asked.
type readinessState struct{ ready atomic.Bool }

// markReady records that the endpoint has bound.
//
// That is the later of the two conditions readiness reports and no second flag
// states the other one: whether there is a workspace to serve is settled before
// anything binds, so an endpoint that has bound is an endpoint whose workspace
// resolved. Two flags that can only agree would be two statements of one fact.
func (s *readinessState) markReady() { s.ready.Store(true) }

// isReady reports what a readiness probe is answered with.
func (s *readinessState) isReady() bool { return s.ready.Load() }

// healthHandler answers liveness and readiness, and nothing else.
//
// It reads a request's path and its method and reads nothing else — no header,
// no body, no credential — and every answer it gives is a status code with an
// empty body. That is the whole of what a supervisor asks for, and anything
// beyond it would be a surface on an address that deliberately offers none.
type healthHandler struct{ ready *readinessState }

// ServeHTTP answers one probe.
//
// The path is settled before the method, so a path this listener does not serve
// is answered not-found rather than method-not-allowed — the second would tell a
// caller that a path exists when it does not.
func (h healthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != livenessPath && r.URL.Path != readinessPath {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set(headerAllow, http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == readinessPath && !h.ready.isReady() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// healthListener is the second listener a deployment may ask for: the one the
// two checks are answered on, which is never the one the endpoint is served on.
//
// It is a listener of its own rather than a pair of routes on the endpoint's
// because a deployment reached from the open network refuses every request that
// carries no credential, and answering a probe there would hand any caller a
// free reading of whether the process is up together with a free way to make it
// do work. Keeping the two checks on an address the operator names is what makes
// the endpoint's answer to them the same not-found every other unknown path
// receives.
type healthListener struct {
	l    *httpListener
	stop context.CancelFunc
	done chan error
	logw io.Writer
}

// startHealthListener binds the health address and starts answering on it, or
// reports that this deployment configured none.
//
// An empty address returns no listener and no error: the two checks exist only
// where an operator named an address for them, so their absence is a deployment
// that did not ask rather than a failure.
//
// The address is read by the same reader the endpoint's address is read by, so
// the two forms a deployment may write are the same forms here. What is
// deliberately not applied is the confinement that reader's caller applies to a
// plaintext endpoint. That confinement bounds where a surface the open network
// reaches may sit; these two checks carry no body, ask for nothing and disclose
// nothing but whether the process is up and ready, and where their address sits
// is the operator's own decision. The reference page says what that address is
// for, and a deployment that puts it elsewhere has chosen to.
//
// No certificate is presented either. The address is the operator's own
// interface to a process they run, rather than a surface a caller who is not on
// that host reaches.
func startHealthListener(ctx context.Context, address string, ready *readinessState,
	logw io.Writer) (*healthListener, error) {
	if address == "" {
		return nil, nil
	}
	target, err := parseListenAddress(address)
	if err != nil {
		return nil, fmt.Errorf("read the health address in mcp.health.listen: %w", err)
	}
	l, err := bindHTTPListener(target, nil, healthHandler{ready: ready}, logw)
	if err != nil {
		return nil, fmt.Errorf("listen for health checks on the address in mcp.health.listen: %w", err)
	}
	serveCtx, stop := context.WithCancel(ctx)
	h := &healthListener{l: l, stop: stop, done: make(chan error, 1), logw: listenerLogWriter(logw)}
	go func() { h.done <- h.l.serve(serveCtx) }()
	return h, nil
}

// close stops the health listener and waits for it to let go of its address.
//
// The wait is the point rather than a courtesy. A startup refusal made after
// this listener is answering returns while the process is still deciding whether
// it can serve at all, and a refused launch that returned before its health
// address was released would leave a socket behind for the next launch to trip
// over. A nil receiver is a deployment that configured no health address, and
// stopping one of those is nothing.
//
// A listener that stopped answering for a reason of its own is reported on the
// stream every other diagnostic here goes to. It is not returned: the two checks
// are how a supervisor watches this process, and a supervisor whose probes stop
// being answered already has the signal it acts on.
func (h *healthListener) close() {
	if h == nil {
		return
	}
	h.stop()
	if err := <-h.done; err != nil {
		fmt.Fprintf(h.logw, "mcp: the health listener stopped answering: %v\n", err)
	}
}

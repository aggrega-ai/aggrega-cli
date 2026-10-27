// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// The tests in this package resolve against registries they own, and never
// against the built-in default registry.
//
// A dependency resolution reaches a registry for whatever no local tier holds,
// and a workspace that names no registry resolves against the built-in default —
// a live service on the public network. A test that reached it would pass or
// fail with that service's latency and availability rather than with the code
// under test, and would wait on it with no bound. So two things hold here:
//
//   - every fixture workspace a resolution runs over names a registry the test
//     owns, through testOwnedRegistryConfig — one on this machine that answers
//     every request as not found, which is what a registry answers for an
//     identity it does not serve;
//   - the whole binary refuses every request to the built-in default registry's
//     host before anything is dialled, and fails when any test made one, naming
//     each request. A fixture that stopped naming its own registry and then
//     reached the default is caught there whether or not the test that drove it
//     noticed the refusal, because a lookup that reads a transport failure as a
//     miss would otherwise hide it.
//
// The refusal sits in the proxy decision of the process's default transport,
// which every network client this package's code reaches is built over, and
// which the transport consults for every request before it dials. It is hooked
// there rather than by replacing the transport, because code that expects the
// default transport to be the standard one type-asserts it, and a replacement
// would make that code panic. It is installed once, before any test runs, so no
// test races it.

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestPerUserHomesAreIsolated is the regression floor for the redirect TestMain
// runs the suite under: if it is ever dropped, this fails loudly rather than
// letting the developer's own configuration, credentials or netrc file reach the
// suite.
func TestPerUserHomesAreIsolated(t *testing.T) {
	hometest.AssertRedirected(t)
}

// defaultRegistryRefusal is a proxy decision that refuses every request to one
// registry host and records it, and hands every other request to the decision
// it wraps.
type defaultRegistryRefusal struct {
	host string
	next func(*http.Request) (*url.URL, error)

	mu      sync.Mutex
	refused []string
}

// proxy is the decision itself. A transport aborts a request whose proxy
// decision returns an error, before it dials anything.
func (r *defaultRegistryRefusal) proxy(req *http.Request) (*url.URL, error) {
	if registryhost.Normalize(req.URL.Host) == r.host {
		r.mu.Lock()
		r.refused = append(r.refused, req.Method+" "+req.URL.String())
		r.mu.Unlock()
		return nil, fmt.Errorf("refused %s %s: tests here never reach %s, the built-in default registry; "+
			"the fixture's workspace configuration names no registry of the test's own", req.Method, req.URL, r.host)
	}
	if r.next == nil {
		return nil, nil
	}
	return r.next(req)
}

// requests returns every request the decision refused, in the order made.
func (r *defaultRegistryRefusal) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.refused...)
}

// builtInDefaultRegistryHost is the host a workspace naming no registry resolves
// against, read from the documented defaults rather than spelled here, so the
// refusal follows the default if it moves.
func builtInDefaultRegistryHost() string {
	return workspaceconfig.Default().DefaultRegistryLocation().Host
}

// TestMain installs the refusal for the whole binary and fails the run when any
// test made a request it refused. It runs the suite with the per-user homes
// redirected (hometest.RunHermetic), so a resolution a test drives reads none of
// the developer's own configuration, registry credentials or netrc file.
func TestMain(m *testing.M) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		fmt.Fprintf(os.Stderr, "the default transport is a %T rather than the standard one, so the refusal of the "+
			"built-in default registry cannot be installed and nothing would stop a test reaching it\n", http.DefaultTransport)
		os.Exit(2)
	}
	refusal := &defaultRegistryRefusal{host: builtInDefaultRegistryHost(), next: transport.Proxy}
	transport.Proxy = refusal.proxy
	code := hometest.RunHermetic(m)
	if refused := refusal.requests(); len(refused) > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: %d request(s) reached %s, the built-in default registry; every test here "+
			"resolves against a registry it owns (testOwnedRegistryConfig):\n", len(refused), refusal.host)
		for _, r := range refused {
			fmt.Fprintf(os.Stderr, "  %s\n", r)
		}
		code = 1
	}
	os.Exit(code)
}

// testOwnedRegistryConfig returns the workspace-configuration line naming, as the
// default registry, a registry this test owns: one on this machine, contacted in
// plain HTTP as every loopback registry is, that answers every request as not
// found. A fixture a dependency resolution runs over appends it to its
// configuration, so whatever no local tier holds is asked of it rather than of
// the built-in default.
func testOwnedRegistryConfig(t *testing.T) string {
	t.Helper()
	return "default_registry: " + strconv.Quote(loopbackRegistry(t, http.NotFound)) + "\n"
}

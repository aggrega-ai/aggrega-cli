// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// registryProbe stands an endpoint at a configured registry's address and counts
// how many times it was asked for anything.
//
// The count is the observable both proofs below turn on, and it is taken from
// OUTSIDE the code under test. It does not serve a usable registry API and does
// not need to: what is being measured is whether the walk made the round trip at
// all, so a request the endpoint never saw is work that did not happen. The walk
// failing afterwards is expected and is not what is asserted.
func registryProbe(t *testing.T) (host string, contacted *atomic.Int32) {
	t.Helper()
	contacted = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		contacted.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), contacted
}

// indexProbeWorkspace resolves a workspace configured with the given registry
// hosts, over a two-layer cascade of this test's own.
//
// The global layer is redirected through a frozen lookup rather than read from
// the machine: without that, these operations would consult the developer's own
// configured registries and write an index under their workspace.
func indexProbeWorkspace(t *testing.T, hosts ...string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := t.TempDir()

	var b strings.Builder
	b.WriteString("registries:\n")
	for _, h := range hosts {
		fmt.Fprintf(&b, "  - host: %s\n", h)
	}

	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(globalPath, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write the global layer: %v", err)
	}
	wsPath := filepath.Join(root, workspaceconfig.ConfigFilename)
	if err := os.WriteFile(wsPath, []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write the workspace layer: %v", err)
	}
	lookup := func(k string) string {
		if k == workspaceconfig.EnvConfigOverride {
			return globalPath
		}
		return ""
	}
	rw, err := workspaceconfig.Resolve(root, root, lookup)
	if err != nil {
		t.Fatalf("resolve the probe workspace: %v", err)
	}
	return rw
}

// TestTheIndexWalkContactsNoRegistryWhenTheCallerStops proves the stop reaches
// the walk rather than only the reply.
//
// The observable is the endpoint's own request count, which is work not done: a
// returned error looks much the same whether the round trip was never made or
// was made and abandoned. The context is cancelled BEFORE the call, so there is
// no window in which a fast machine completes the walk first — whether the
// registry is contacted is a property of the threading and of nothing else, and
// the assertion carries no clock.
//
// 🔴 Second observable, and it is the LOAD-BEARING one — the request count alone
// is not enough, which was established by planting the defect rather than
// reasoned about. With the interruption point removed the walk still reported
// zero requests, because the context now reaches the HTTP transport too and the
// transport aborts the round trip before the server ever handles it. So the
// count separates "withdrawn" from "live" and does NOT separate "the stop
// reached the loop seam" from "the stop reached only the transport".
//
// What separates those is the host-result count: a walk stopped at the seam
// reports NO host result, where one stopped inside the transport reports a host
// carrying its own cancelled-transport error. Both assertions are kept, with
// this comment recording which of them is decisive, so a later reader does not
// take the weaker one for the proof.
func TestTheIndexWalkContactsNoRegistryWhenTheCallerStops(t *testing.T) {
	t.Parallel()
	host, contacted := registryProbe(t)
	rw := indexProbeWorkspace(t, host)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	res, err := Update(ctx, rw, UpdateOptions{}, io.Discard)

	// t.Error rather than t.Fatal: the request count below is the load-bearing
	// half of this proof, and a Fatal here would skip it on exactly the failure it
	// exists to characterise.
	if err == nil {
		t.Error("a withdrawn index walk returned no error, so nothing distinguishes it from one that ran")
	}
	if got := contacted.Load(); got != 0 {
		t.Errorf("the registry endpoint was asked %d time(s) after the caller withdrew; the cancellation did not reach the walk", got)
	}
	if len(res.Hosts) != 0 {
		t.Errorf("the withdrawn walk reported %d host result(s): %+v; want none, since the stop is observed before the first round trip", len(res.Hosts), res.Hosts)
	}
	if len(res.Failed) != 0 {
		t.Errorf("the withdrawn walk reported %d failed host(s): %+v; a host never contacted is not a host that failed", len(res.Failed), res.Failed)
	}
}

// TestTheIndexWalkContactsTheRegistryWhenNobodyStops is the positive control,
// and it is what makes the zero above a measurement rather than a claim about
// the instrument: the same probe under a live context is contacted, and the walk
// reports that host's result.
//
// Without it the test above would pass just as well over an endpoint nothing
// could ever reach — which is exactly the vacuous shape a control caught on this
// package's other cancellation proof.
func TestTheIndexWalkContactsTheRegistryWhenNobodyStops(t *testing.T) {
	t.Parallel()
	host, contacted := registryProbe(t)
	rw := indexProbeWorkspace(t, host)

	res, _ := Update(t.Context(), rw, UpdateOptions{}, io.Discard)
	// The walk's own error is deliberately not asserted: the probe is not a usable
	// registry, so the walk is expected to fail against it. What is asserted is
	// that the round trip HAPPENED, which is the fact the negative above denies.

	if got := contacted.Load(); got == 0 {
		t.Error("the registry endpoint was never contacted under a live context, so its not being contacted above is not evidence of a stop")
	}
	if len(res.Hosts) == 0 {
		t.Error("the completed walk reported no host result at all, so the empty result above proves nothing")
	}
}

// TestTheSearchReadsNoIndexWhenTheCallerStops proves the stop reaches the
// per-host index reads.
//
// Search contacts no network — it reads the offline index written under the
// workspace's own cache directory — so its observable is the work not done: how
// many of the configured hosts were consulted at all. With no index built, a
// completed search consults every host and records each as index-less; a
// withdrawn one records none, because it stopped before the first read.
//
// That count is the discriminator. An empty record list is not: a search that
// ran and found nothing produces one too.
func TestTheSearchReadsNoIndexWhenTheCallerStops(t *testing.T) {
	t.Parallel()

	t.Run("a withdrawn search consults no host", func(t *testing.T) {
		t.Parallel()
		rw := indexProbeWorkspace(t, "one.example.test", "two.example.test")

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		res, err := Search(ctx, rw, Query{Text: "anything"}, io.Discard, time.Now())

		// t.Error rather than t.Fatal, so the consulted-host count below is reached.
		if err == nil {
			t.Error("a withdrawn search returned no error, so nothing distinguishes it from one that ran")
		}
		if len(res.MissingIndexHosts) != 0 {
			t.Errorf("the withdrawn search consulted %d host(s) (%v); want 0, since the stop is observed before the first index is read",
				len(res.MissingIndexHosts), res.MissingIndexHosts)
		}
	})

	t.Run("control: an uncancelled search consults both hosts", func(t *testing.T) {
		t.Parallel()
		rw := indexProbeWorkspace(t, "one.example.test", "two.example.test")

		res, err := Search(t.Context(), rw, Query{Text: "anything"}, io.Discard, time.Now())
		if err == nil {
			t.Fatal("a search over a workspace with no index built returned no error; the fixture is not the one this proof assumes")
		}
		if len(res.MissingIndexHosts) != 2 {
			t.Fatalf("the control search consulted %d host(s) (%v); want 2, otherwise the zero above is not evidence of a stop",
				len(res.MissingIndexHosts), res.MissingIndexHosts)
		}
	})
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// slowDownOnceTransport answers its first request 429 Too Many Requests, naming
// no wait, and every later one not found, without dialing, and counts the
// requests it was handed.
type slowDownOnceTransport struct{ asked *atomic.Int32 }

func (s slowDownOnceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	status := http.StatusNotFound
	if s.asked.Add(1) == 1 {
		status = http.StatusTooManyRequests
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    r,
	}, nil
}

// TestTheTrustPolicyReadIsARegistryRequestLikeAnyOther holds the one registry
// request made outside a registry target — the read of the trust policy a
// registry advertises — to the busy-registry handling every registry target goes
// through.
//
// The client the read runs over is a registry client (oci.IsRegistryClient)
// whether the egress gate supplied it or a caller injected one; an offline
// workspace still gets none. And a registry that asks the read to slow down once
// is waited on and asked again: the registration completes on the registry's second
// answer, that it advertises nothing, rather than failing on the first.
//
// Planted defect: returning the egress client unwrapped from trustClientFor fails
// the first two rows and the registration row, which then meets the 429 itself:
// the endpoint is asked once, and the reason the outcome carries is the 429 rather
// than the registry's answer after the wait.
func TestTheTrustPolicyReadIsARegistryRequestLikeAnyOther(t *testing.T) {
	t.Parallel()

	t.Run("the client the egress gate supplies", func(t *testing.T) {
		t.Parallel()
		rw, _ := registrationProbeWorkspace(t, "workspace: {}\n")
		if client := trustClientFor(rw, nil); !oci.IsRegistryClient(client) {
			t.Errorf("the trust read runs over %+v, which is not a registry client", client)
		}
	})

	t.Run("a client a caller injected", func(t *testing.T) {
		t.Parallel()
		rw, _ := registrationProbeWorkspace(t, "workspace: {}\n")
		if client := trustClientFor(rw, &http.Client{}); !oci.IsRegistryClient(client) {
			t.Errorf("the trust read runs over %+v, which is not a registry client", client)
		}
	})

	t.Run("an offline workspace gets no client", func(t *testing.T) {
		t.Parallel()
		rw, _ := registrationProbeWorkspace(t, "offline: true\nworkspace: {}\n")
		if client := trustClientFor(rw, nil); client != nil {
			t.Errorf("an offline workspace was handed a client: %+v", client)
		}
	})

	t.Run("a registry that asks the read to slow down once is asked again", func(t *testing.T) {
		t.Parallel()
		rw, _ := registrationProbeWorkspace(t, "workspace: {}\n")
		asked := &atomic.Int32{}
		ctx := oci.WithBusyTiming(t.Context(), oci.BusyWaitLimit, 5*time.Millisecond, 0)
		res, err := Add(ctx, rw, AddOptions{
			Host:        "registry.example.test",
			FetchTrust:  true,
			Workspace:   true,
			TrustClient: &http.Client{Transport: slowDownOnceTransport{asked: asked}},
		})
		if err != nil {
			t.Fatalf("the registration failed: %v", err)
		}
		if got := asked.Load(); got != 2 {
			t.Errorf("the trust endpoint was asked %d time(s); want 2, the refusal and the answer after the wait", got)
		}
		if !res.Trust.NotAdvertised || res.Trust.Unreachable || strings.Contains(res.Trust.Reason, "429") {
			t.Errorf("trust outcome = %+v; want the registry's second answer, that it advertises nothing", res.Trust)
		}
	})
}

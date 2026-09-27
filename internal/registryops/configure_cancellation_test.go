// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The advertised trust document the probe registry serves. A registry that
// advertises a policy is what makes a completed fetch legible on the result: the
// outcome reports the policy as pinned, so an answer that reports it pinned is
// proof the endpoint was read.
const probeAdvertisedTrust = `{"requires_signatures": true, ` +
	`"trusted_identities": [{"identity": "dev@example.com", "issuer": "https://accounts.google.com"}]}`

// trustProbe stands a registry that advertises a trust policy and counts how many
// times it was asked for one.
//
// The count is the observable the cancellation proof turns on, and it is observed
// from OUTSIDE the code under test. A returned error looks much the same whether
// the request was never sent or was sent and abandoned; a request the endpoint
// never saw is work that did not happen.
//
// It listens on loopback, which is what makes a plain-HTTP probe the real path: the
// trust fetch speaks HTTPS to a public host and plain HTTP to a loopback one, so no
// certificate has to be arranged for this to be the endpoint the production code
// would have reached.
func trustProbe(t *testing.T) (host string, contacted *atomic.Int32, client *http.Client) {
	t.Helper()
	contacted = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		contacted.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, probeAdvertisedTrust)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), contacted, srv.Client()
}

// registrationProbeWorkspace resolves a workspace over a two-layer cascade of this
// test's own, with workspaceDoc as its workspace layer, and returns it with the
// workspace configuration file a registration targeted at the workspace will write.
//
// The global layer is redirected through a frozen lookup rather than read from the
// machine, because the operation under test defaults to the global file: a fixture
// that did not redirect it would write the developer's own registries. The frozen
// lookup is also what keeps the posture the layer states the only one in force.
func registrationProbeWorkspace(t *testing.T, workspaceDoc string) (*workspaceconfig.ResolvedWorkspace, string) {
	t.Helper()
	root := t.TempDir()
	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(globalPath, []byte("registries: []\n"), 0o644); err != nil {
		t.Fatalf("write the global layer: %v", err)
	}
	wsPath := filepath.Join(root, workspaceconfig.ConfigFilename)
	if err := os.WriteFile(wsPath, []byte(workspaceDoc), 0o644); err != nil {
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
	return rw, wsPath
}

// TestRegistrationNeverReachesTheRegistryWhenTheCallerStops is the transport-level
// half of the cancellation proof: the registration is called with an already
// cancelled context and the trust endpoint records no request at all.
//
// The context is cancelled BEFORE the call rather than during it, and that is what
// makes the assertion free of any clock: there is no window in which a fast machine
// completes the fetch before the withdrawal lands, so whether the endpoint is
// reached is a property of the threading and of nothing else.
//
// Two observables, both work not done: the endpoint was never asked, and no entry
// was written. The second is the load-bearing one — an interrupted fetch is
// indistinguishable, from its own error, from a registry that advertises nothing,
// so a registration that could not observe the withdrawal would proceed and write a
// policy the registry never declined to publish.
func TestRegistrationNeverReachesTheRegistryWhenTheCallerStops(t *testing.T) {
	t.Parallel()
	host, contacted, client := trustProbe(t)
	rw, wsPath := registrationProbeWorkspace(t, "workspace: {}\n")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	res, err := Add(ctx, rw, AddOptions{
		Host:        host,
		FetchTrust:  true,
		Workspace:   true,
		TrustClient: client,
	})

	if err == nil {
		t.Error("a withdrawn registration returned no error, so nothing distinguishes it from one the registry answered")
	}
	if res.Trust.NotAdvertised {
		t.Errorf("the withdrawal was reported as a registry that advertises nothing: %+v; those are different facts and conflating them writes an empty trust policy the registry never declined", res.Trust)
	}
	if got := contacted.Load(); got != 0 {
		t.Errorf("the trust endpoint was asked %d time(s) after the caller withdrew; the cancellation did not reach the fetch", got)
	}
	ws, readErr := os.ReadFile(wsPath)
	if readErr != nil {
		t.Fatalf("read the workspace layer: %v", readErr)
	}
	if strings.Contains(string(ws), host) {
		t.Errorf("the withdrawn registration wrote the entry anyway:\n%s", ws)
	}
}

// TestRegistrationReachesTheRegistryWhenNobodyStops is the positive control, and it
// is the reason the zero above is a measurement rather than a claim about the
// instrument: the same probe, the same transport and the same options under a live
// context are contacted exactly once, the advertised policy is pinned, and the entry
// is written.
//
// Without it the test above would pass just as well over an endpoint nothing could
// ever reach.
func TestRegistrationReachesTheRegistryWhenNobodyStops(t *testing.T) {
	t.Parallel()
	host, contacted, client := trustProbe(t)
	rw, wsPath := registrationProbeWorkspace(t, "workspace: {}\n")

	res, err := Add(t.Context(), rw, AddOptions{
		Host:        host,
		FetchTrust:  true,
		Workspace:   true,
		TrustClient: client,
	})
	if err != nil {
		t.Fatalf("an uncancelled registration: %v", err)
	}

	if got := contacted.Load(); got != 1 {
		t.Errorf("the trust endpoint was asked %d time(s); want exactly 1", got)
	}
	if !res.Trust.Pinned {
		t.Errorf("the advertised policy was not pinned: %+v", res.Trust)
	}
	ws, readErr := os.ReadFile(wsPath)
	if readErr != nil {
		t.Fatalf("read the workspace layer: %v", readErr)
	}
	if !strings.Contains(string(ws), host) {
		t.Errorf("the completed registration wrote no entry:\n%s", ws)
	}
}

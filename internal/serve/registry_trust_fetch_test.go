// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/registrytrust"
)

// advertisedTrustDocument is the policy the loopback registry below advertises:
// signatures required, two signing identities and one deprecation identity. The
// identities are invented for these tests, and no key is served.
const advertisedTrustDocument = `{"requires_signatures": true, "trusted_identities": [` +
	`{"identity": "signer@example.test", "issuer": "https://issuer.example.test"}, ` +
	`{"identity_regexp": "^https://ci\\.example\\.test/", "issuer": "https://ci-issuer.example.test"}], ` +
	`"trusted_deprecation_identities": [{"identity": "security@example.test", "issuer": "https://issuer.example.test"}]}`

// countingTrustRegistry stands a loopback registry that serves doc at the
// well-known trust path, or a not-found everywhere when doc is empty, and counts
// every request it receives.
//
// The count is read from outside the code under test, and the offline row turns
// on it: a fetch refused before any dial and a fetch sent and answered with
// nothing both end in a not-advertised outcome, and only the endpoint can say
// which of the two happened. It listens on loopback, so the fetch speaks plain
// HTTP to it and no certificate has to be arranged.
func countingTrustRegistry(t *testing.T, doc string) (host string, asked *atomic.Int32) {
	t.Helper()
	asked = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if doc == "" || r.URL.Path != registrytrust.WellKnownPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, doc)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), asked
}

// TestRegistryAddOnTheDaemonEstablishesTrustAsEverySurfaceDoes drives a
// registration through the daemon's own protocol, with no transport supplied by
// the test, and holds it to the registration every surface shares.
//
// Each row registers one loopback registry twice: once as a request to the
// daemon, and once through the shared registration directly, with the options
// `aggrega registry add --workspace` hands it — which is the whole of what that
// command does before it prints. The two written configuration files must be
// byte-identical and the two trust outcomes equal, so the daemon cannot establish
// trust in a way the command line does not.
//
// Parity alone would pass over two surfaces that are wrong together, so each row
// also pins the absolute outcome: an online workspace asks the endpoint once and
// pins what it advertises; a registry advertising nothing is reported as that,
// with its own reason; and an offline workspace never reaches the endpoint and
// reports the network-egress gate's refusal as the reason.
//
// Planted defects, each caught by a row here: a registration handed no client
// fails the online row, which sees an endpoint never asked, nothing pinned and
// the workspace reported offline; a daemon building a client of its own fails the
// offline row, which sees the endpoint asked and a policy pinned under a posture
// that forbids the fetch.
func TestRegistryAddOnTheDaemonEstablishesTrustAsEverySurfaceDoes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		seed        string
		doc         string
		wantAsked   int32
		wantPinned  bool
		wantOffline bool
	}{
		{
			name:       "an online workspace fetches the advertised policy once and pins it",
			seed:       "workspace: {}\n",
			doc:        advertisedTrustDocument,
			wantAsked:  1,
			wantPinned: true,
		},
		{
			name:      "a registry advertising nothing is reported as advertising nothing",
			seed:      "workspace: {}\n",
			wantAsked: 1,
		},
		{
			name:        "an offline workspace never reaches the registry and reports the refusal",
			seed:        "offline: true\nworkspace: {}\n",
			doc:         advertisedTrustDocument,
			wantOffline: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			host, asked := countingTrustRegistry(t, tc.doc)

			srv, _, wsPath := registryWriteFixture(t, tc.seed)
			var res registryAddResult
			decodeDaemonResult(t, callDaemonMethod(t, srv, "aggrega/registryAdd", map[string]any{
				"host":      host,
				"workspace": true,
			}), &res)
			daemonAsked := asked.Load()

			ref, _, refPath := registryWriteFixture(t, tc.seed)
			refRes, err := registryops.Add(t.Context(), ref.resolvedWorkspace, registryops.AddOptions{
				Host:       host,
				FetchTrust: true,
				Workspace:  true,
			})
			if err != nil {
				t.Fatalf("the shared registration: %v", err)
			}
			sharedAsked := asked.Load() - daemonAsked

			if daemonAsked != tc.wantAsked {
				t.Errorf("the daemon's registration asked the trust endpoint %d time(s); want %d", daemonAsked, tc.wantAsked)
			}
			if sharedAsked != tc.wantAsked {
				t.Errorf("the shared registration asked the trust endpoint %d time(s); want %d", sharedAsked, tc.wantAsked)
			}
			if res.Trust.Pinned != tc.wantPinned {
				t.Errorf("pinned = %v; want %v (trust %+v)", res.Trust.Pinned, tc.wantPinned, res.Trust)
			}
			if !tc.wantPinned && !res.Trust.NotAdvertised {
				t.Errorf("a policy that was not pinned must be reported as not advertised; got %+v", res.Trust)
			}
			offlineReason := "registrytrust: fetch " + host + ": " + egress.ErrOfflineEgress().Error()
			if gotOffline := res.Trust.Reason == offlineReason; gotOffline != tc.wantOffline {
				t.Errorf("reason = %q; want the offline refusal as the reason: %v", res.Trust.Reason, tc.wantOffline)
			}
			if res.Trust != refRes.Trust {
				t.Errorf("the daemon's trust outcome %+v differs from the shared registration's %+v", res.Trust, refRes.Trust)
			}

			daemonBytes, err := os.ReadFile(wsPath)
			if err != nil {
				t.Fatalf("read the daemon's written configuration: %v", err)
			}
			sharedBytes, err := os.ReadFile(refPath)
			if err != nil {
				t.Fatalf("read the shared registration's written configuration: %v", err)
			}
			if !bytes.Equal(daemonBytes, sharedBytes) {
				t.Errorf("the daemon wrote a different configuration from the shared registration\n--- daemon ---\n%s\n--- shared ---\n%s",
					daemonBytes, sharedBytes)
			}
			// The control on the comparison: two files carrying no policy at all are
			// equal too, so the pinned row also reads the policy off the bytes.
			if pinnedOnDisk := strings.Contains(string(daemonBytes), "signer@example.test"); pinnedOnDisk != tc.wantPinned {
				t.Errorf("the written configuration carries the advertised identity: %v; want %v\n%s",
					pinnedOnDisk, tc.wantPinned, daemonBytes)
			}
		})
	}
}

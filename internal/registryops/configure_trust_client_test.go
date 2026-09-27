// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/registrytrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestTheRegistrationTakesItsTrustClientFromTheEgressGate is the unit-level proof
// of where the advertised-trust fetch gets its transport when the caller supplies
// none — which is what every surface does, so it is the only transport a
// registration made in production ever uses — and of what an offline
// registration does with the refusal that transport's gate hands back.
//
// Online, the probe endpoint is asked exactly once and its policy is pinned and
// written. Offline, it is never asked, and what follows depends on whether the
// entry would carry a policy without the fetch: a registry other than the hub is
// refused with no entry written; the same registry with an identity supplied by
// hand is written with exactly that identity; the hub is written with its
// built-in baseline and the gate's refusal as the reason nothing was advertised.
// The count is observed at the endpoint, so a fetch refused before any dial and
// one sent and failed cannot be mistaken for each other.
//
// Planted defects: returning no client where none was injected fails the online
// row, which sees the endpoint never asked and the workspace reported offline;
// building a client that ignores the posture fails the offline rows, which see
// the endpoint asked and the policy pinned; removing the offline refusal fails
// the refusal row, which sees an entry written that trusts nothing; refusing the
// hub as well fails the hub row; refusing a registration that supplied its own
// policy fails the pinned row.
func TestTheRegistrationTakesItsTrustClientFromTheEgressGate(t *testing.T) {
	t.Parallel()
	const offline = "offline: true\nworkspace: {}\n"
	const handIdentity = "hand@example.test"
	for _, tc := range []struct {
		name      string
		workspace string
		hub       bool
		identity  bool

		wantAsked   int32
		wantRefused bool
		wantPinned  bool
		wantOffline bool
		// wantOnDisk is a string the written entry must carry; wantAbsent is one it
		// must not.
		wantOnDisk string
		wantAbsent string
	}{
		{
			name:       "an online workspace asks the registry once and pins what it advertises",
			workspace:  "workspace: {}\n",
			wantAsked:  1,
			wantPinned: true,
			wantOnDisk: "dev@example.com",
		},
		{
			name:        "an offline workspace never asks, and refuses a registry it could not pin",
			workspace:   offline,
			wantRefused: true,
		},
		{
			name:       "an offline workspace registers a registry whose trust is pinned by hand",
			workspace:  offline,
			identity:   true,
			wantOnDisk: handIdentity,
			wantAbsent: "dev@example.com",
		},
		{
			name:        "an offline workspace registers the hub on its built-in baseline",
			workspace:   offline,
			hub:         true,
			wantOffline: true,
			wantOnDisk:  workspaceconfig.DefaultBaseline().TrustedIdentities[0].Issuer,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			host, contacted, _ := trustProbe(t)
			if tc.hub {
				host = workspaceconfig.HubBaselineHost
			}
			rw, wsPath := registrationProbeWorkspace(t, tc.workspace)
			opts := AddOptions{
				Host:       host,
				FetchTrust: true,
				Workspace:  true,
			}
			if tc.identity {
				opts.Identities = []string{handIdentity + "=https://issuer.example.test"}
			}

			res, err := Add(t.Context(), rw, opts)

			if got := contacted.Load(); got != tc.wantAsked {
				t.Errorf("the trust endpoint was asked %d time(s); want %d", got, tc.wantAsked)
			}
			ws, readErr := os.ReadFile(wsPath)
			if readErr != nil {
				t.Fatalf("read the workspace layer: %v", readErr)
			}
			if tc.wantRefused {
				if !errors.Is(err, registrytrust.ErrOfflineRegistrationWithoutTrustPolicy()) {
					t.Fatalf("Add = %v; want the offline registration refused", err)
				}
				if !strings.Contains(err.Error(), host) {
					t.Errorf("the refusal %q does not name the registry it refused", err)
				}
				if res.Trust != (TrustFetchOutcome{}) {
					t.Errorf("a refused registration reported a trust outcome: %+v", res.Trust)
				}
				if strings.Contains(string(ws), host) {
					t.Errorf("a refused registration wrote the entry anyway:\n%s", ws)
				}
				return
			}
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
			if res.Trust.Pinned != tc.wantPinned {
				t.Errorf("pinned = %v; want %v (trust %+v)", res.Trust.Pinned, tc.wantPinned, res.Trust)
			}
			offlineReason := "registrytrust: fetch " + host + ": " + egress.ErrOfflineEgress().Error()
			if gotOffline := res.Trust.NotAdvertised && res.Trust.Reason == offlineReason; gotOffline != tc.wantOffline {
				t.Errorf("trust = %+v; want the offline refusal as a not-advertised reason: %v", res.Trust, tc.wantOffline)
			}
			if tc.identity && !res.Trust.Skipped {
				t.Errorf("a registration pinned by hand fetched anyway: %+v", res.Trust)
			}
			if !strings.Contains(string(ws), host) {
				t.Errorf("the registration wrote no entry:\n%s", ws)
			}
			if !strings.Contains(string(ws), tc.wantOnDisk) {
				t.Errorf("the written entry does not carry %q:\n%s", tc.wantOnDisk, ws)
			}
			if tc.wantAbsent != "" && strings.Contains(string(ws), tc.wantAbsent) {
				t.Errorf("the written entry carries %q, which only a fetch could have supplied:\n%s", tc.wantAbsent, ws)
			}
		})
	}
}

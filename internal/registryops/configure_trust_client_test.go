// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
)

// TestTheRegistrationTakesItsTrustClientFromTheEgressGate is the unit-level proof
// of where the advertised-trust fetch gets its transport when the caller supplies
// none — which is what every surface does, so it is the only transport a
// registration made in production ever uses.
//
// Online, the probe endpoint is asked exactly once and its policy is pinned and
// written. Offline, it is never asked, the outcome is not-advertised, and its
// reason is the gate's own refusal framed by the fetch. The count is observed at
// the endpoint, so a fetch refused before any dial and one sent and failed cannot
// be mistaken for each other.
//
// Planted defects: returning no client where none was injected fails the online
// row, which sees the endpoint never asked and the workspace reported offline;
// building a client that ignores the posture fails the offline row, which sees
// the endpoint asked and the policy pinned.
func TestTheRegistrationTakesItsTrustClientFromTheEgressGate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		workspace   string
		wantAsked   int32
		wantPinned  bool
		wantOffline bool
	}{
		{
			name:       "an online workspace asks the registry once and pins what it advertises",
			workspace:  "workspace: {}\n",
			wantAsked:  1,
			wantPinned: true,
		},
		{
			name:        "an offline workspace never asks and reports the gate's refusal",
			workspace:   "offline: true\nworkspace: {}\n",
			wantOffline: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			host, contacted, _ := trustProbe(t)
			rw, wsPath := registrationProbeWorkspace(t, tc.workspace)

			res, err := Add(t.Context(), rw, AddOptions{
				Host:       host,
				FetchTrust: true,
				Workspace:  true,
			})
			if err != nil {
				t.Fatalf("Add: %v", err)
			}

			if got := contacted.Load(); got != tc.wantAsked {
				t.Errorf("the trust endpoint was asked %d time(s); want %d", got, tc.wantAsked)
			}
			if res.Trust.Pinned != tc.wantPinned {
				t.Errorf("pinned = %v; want %v (trust %+v)", res.Trust.Pinned, tc.wantPinned, res.Trust)
			}
			offlineReason := "registrytrust: fetch " + host + ": " + egress.ErrOfflineEgress().Error()
			if gotOffline := res.Trust.NotAdvertised && res.Trust.Reason == offlineReason; gotOffline != tc.wantOffline {
				t.Errorf("trust = %+v; want the offline refusal as a not-advertised reason: %v", res.Trust, tc.wantOffline)
			}

			ws, err := os.ReadFile(wsPath)
			if err != nil {
				t.Fatalf("read the workspace layer: %v", err)
			}
			if !strings.Contains(string(ws), host) {
				t.Errorf("the registration wrote no entry, online or offline:\n%s", ws)
			}
			if pinnedOnDisk := strings.Contains(string(ws), "dev@example.com"); pinnedOnDisk != tc.wantPinned {
				t.Errorf("the written entry carries the advertised identity: %v; want %v\n%s", pinnedOnDisk, tc.wantPinned, ws)
			}
		})
	}
}

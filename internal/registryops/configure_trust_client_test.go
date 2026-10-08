// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/registrytrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// notFoundTransport answers every request with a not-found, without dialing, and
// counts the requests it was handed.
//
// It is what lets a test ask the official hub's own host whether it advertises a
// policy: the request reaches this transport and goes no further, so the answer
// is the one a registry publishing no trust document gives, and nothing leaves the
// machine.
type notFoundTransport struct{ asked *atomic.Int32 }

func (n notFoundTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	n.asked.Add(1)
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    r,
	}, nil
}

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
// built-in baseline, reported as the baseline pinned with the gate's refusal as
// the reason and never as a registry that advertises nothing, since it was never
// asked. The hub asked online and answering that it advertises nothing is the
// control on that last row: it was asked, so it is reported as not advertised
// AND as the baseline pinned. The count is observed at the endpoint, so a fetch
// refused before any dial and one sent and failed cannot be mistaken for each
// other.
//
// Every completed row compares the whole outcome, so a key set that should not be
// fails as surely as one missing.
//
// Planted defects: returning no client where none was injected fails the online
// row, which sees the endpoint never asked and the workspace reported offline;
// building a client that ignores the posture fails the offline rows, which see
// the endpoint asked and the policy pinned; removing the offline refusal fails
// the refusal row, which sees an entry written that trusts nothing; refusing the
// hub as well fails the hub row; reporting the hub registered offline as not
// advertised fails the hub row; dropping the baseline from the outcome fails both
// hub rows; refusing a registration that supplied its own policy fails the
// pinned row.
func TestTheRegistrationTakesItsTrustClientFromTheEgressGate(t *testing.T) {
	t.Parallel()
	const offline = "offline: true\nworkspace: {}\n"
	const handIdentity = "hand@example.test"
	for _, tc := range []struct {
		name      string
		workspace string
		hub       bool
		identity  bool
		// hubAnswersNotFound hands the registration a transport that answers every
		// request with a not-found without dialing, so the hub's own host can be
		// asked from a test and never reached.
		hubAnswersNotFound bool

		wantAsked   int32
		wantRefused bool
		// wantTrust is the whole outcome a completed registration reports, with
		// Reason left for the row's reason flags to fill.
		wantTrust TrustFetchOutcome
		// wantOfflineReason and wantNotFoundReason name the reason the outcome
		// carries: the gate's offline refusal, or the no-trust answer.
		wantOfflineReason  bool
		wantNotFoundReason bool
		// wantOnDisk is a string the written entry must carry; wantAbsent is one it
		// must not.
		wantOnDisk string
		wantAbsent string
	}{
		{
			name:       "an online workspace asks the registry once and pins what it advertises",
			workspace:  "workspace: {}\n",
			wantAsked:  1,
			wantTrust:  TrustFetchOutcome{Pinned: true, Identities: 1},
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
			wantTrust:  TrustFetchOutcome{Skipped: true},
			wantOnDisk: handIdentity,
			wantAbsent: "dev@example.com",
		},
		{
			name:              "an offline workspace registers the hub on its built-in baseline, never asked",
			workspace:         offline,
			hub:               true,
			wantTrust:         TrustFetchOutcome{Baseline: true},
			wantOfflineReason: true,
			wantOnDisk:        workspaceconfig.DefaultBaseline().TrustedIdentities[0].Issuer,
		},
		{
			name:               "the hub asked online and advertising nothing is written on its built-in baseline",
			workspace:          "workspace: {}\n",
			hub:                true,
			hubAnswersNotFound: true,
			wantAsked:          1,
			wantTrust:          TrustFetchOutcome{NotAdvertised: true, Baseline: true},
			wantNotFoundReason: true,
			wantOnDisk:         workspaceconfig.DefaultBaseline().TrustedIdentities[0].Issuer,
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
			if tc.hubAnswersNotFound {
				contacted = &atomic.Int32{}
				opts.TrustClient = &http.Client{Transport: notFoundTransport{asked: contacted}}
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
			want := tc.wantTrust
			switch {
			case tc.wantOfflineReason:
				want.Reason = host + " was not asked for its trust policy, because the workspace is offline"
			case tc.wantNotFoundReason:
				want.Reason = host + " publishes no trust policy (HTTP 404)"
			}
			// The property the hub's offline row exists for, named on its own so a
			// failure says which fact went wrong rather than only that two values differ.
			if tc.wantOfflineReason && res.Trust.NotAdvertised {
				t.Errorf("a registry the offline posture kept from being asked was reported as advertising nothing: %+v", res.Trust)
			}
			if res.Trust != want {
				t.Errorf("trust = %+v; want %+v", res.Trust, want)
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

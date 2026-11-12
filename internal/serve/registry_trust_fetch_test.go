// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/configops"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/registrytrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
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
// The count is read from outside the code under test, and the offline rows turn
// on it: a fetch refused before any dial and a fetch sent and answered with
// nothing both end without a pinned policy, and only the endpoint can say which
// of the two happened. It listens on loopback, so the fetch speaks plain HTTP to
// it and no certificate has to be arranged.
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

// trustOnTheWire returns the trust object exactly as the daemon's answer carries
// it, key for key.
//
// It reads the raw answer rather than decoding into the Go type, because which
// keys are ABSENT is part of what is asserted: a decoded struct reads an absent
// key and a false one alike, so it cannot tell an answer that says the hub was
// never asked from one that says the hub advertised nothing.
func trustOnTheWire(t *testing.T, resp jsonRPCResponse) map[string]any {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("expected a result; got error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	var answer struct {
		Trust map[string]any `json:"trust"`
	}
	if err := json.Unmarshal(resp.Result, &answer); err != nil {
		t.Fatalf("unmarshal the trust object: %v (raw: %s)", err, resp.Result)
	}
	return answer.Trust
}

// wantRegistrationRefusal holds a daemon answer to the refusal the shared layer
// returned: the same sentence, preceded by the condition's short code, so a
// caller of the daemon reads what the command line prints.
func wantRegistrationRefusal(t *testing.T, resp jsonRPCResponse, shared error) {
	t.Helper()
	if !errors.Is(shared, registrytrust.ErrOfflineRegistrationWithoutTrustPolicy()) {
		t.Fatalf("the shared layer returned %v; want the offline registration refused", shared)
	}
	short, ok := cerrors.ShortCodeOf(shared)
	if !ok || short != "E0310" {
		t.Errorf("the refusal carries short code %q (present %t); want E0310", short, ok)
	}
	if resp.Error == nil {
		t.Fatalf("the daemon answered with a result where the shared layer refused: %s", resp.Result)
	}
	if want := short + ": " + shared.Error(); resp.Error.Message != want {
		t.Errorf("the daemon's refusal is not the shared sentence behind its short code\n got: %q\nwant: %q",
			resp.Error.Message, want)
	}
}

// TestRegistryAddOnTheDaemonEstablishesTrustAsEverySurfaceDoes drives a
// registration through the daemon's own protocol, with no transport supplied by
// the test, and holds it to the registration every surface shares.
//
// Each row registers one registry twice: once as a request to the daemon, and
// once through the shared registration directly, with the options `aggrega
// registry add --workspace` hands it — which is the whole of what that command
// does before it prints. The two written configuration files must be
// byte-identical and the two outcomes equal, so the daemon cannot establish
// trust, or refuse to, in a way the command line does not.
//
// Parity alone would pass over two surfaces that are wrong together, so each row
// also pins the absolute outcome, and pins it as the wire carries it, key for
// key: an online workspace asks the endpoint once and pins what it advertises; a
// registry advertising nothing is reported as that, with its own reason; an
// offline workspace never reaches the endpoint and refuses a registry it cannot
// pin, registers exactly the identity a request pins by hand, and registers the
// hub on its built-in baseline, answered as the baseline pinned with the gate's
// refusal as the reason and with no not-advertised key, since the hub was never
// asked.
//
// Planted defects, each caught by a row here: a registration handed no client
// fails the online row, which sees an endpoint never asked, nothing pinned and
// the workspace reported offline; a daemon building a client of its own fails the
// offline rows, which see the endpoint asked under a posture that forbids the
// fetch; removing the offline refusal fails the refusal row, which sees a result
// and an entry that trusts nothing; refusing the hub, or a registration pinned by
// hand, fails that row; answering the hub registered offline as not advertised,
// or without the baseline, fails the hub row.
func TestRegistryAddOnTheDaemonEstablishesTrustAsEverySurfaceDoes(t *testing.T) {
	t.Parallel()
	const offline = "offline: true\nworkspace: {}\n"
	const handIdentity = "hand@example.test"
	for _, tc := range []struct {
		name              string
		seed              string
		doc               string
		hub               bool
		identity          bool
		wantAsked         int32
		wantPinned        bool
		wantNotAdvertised bool
		wantBaseline      bool
		wantOffline       bool
		wantRefused       bool
		wantOnDisk        string
	}{
		{
			name:       "an online workspace fetches the advertised policy once and pins it",
			seed:       "workspace: {}\n",
			doc:        advertisedTrustDocument,
			wantAsked:  1,
			wantPinned: true,
			wantOnDisk: "signer@example.test",
		},
		{
			name:              "a registry advertising nothing is reported as advertising nothing",
			seed:              "workspace: {}\n",
			wantAsked:         1,
			wantNotAdvertised: true,
		},
		{
			name:        "an offline workspace never reaches the registry and refuses to register it unpinned",
			seed:        offline,
			doc:         advertisedTrustDocument,
			wantRefused: true,
		},
		{
			name:       "an offline workspace registers exactly the identity a request pins by hand",
			seed:       offline,
			doc:        advertisedTrustDocument,
			identity:   true,
			wantOnDisk: handIdentity,
		},
		{
			name:         "an offline workspace registers the hub on its built-in baseline, never asked",
			seed:         offline,
			hub:          true,
			wantBaseline: true,
			wantOffline:  true,
			wantOnDisk:   workspaceconfig.DefaultBaseline().TrustedIdentities[0].Issuer,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			host, asked := countingTrustRegistry(t, tc.doc)
			if tc.hub {
				host = workspaceconfig.HubBaselineHost
			}
			var identities []string
			if tc.identity {
				identities = []string{handIdentity + "=https://issuer.example.test"}
			}

			srv, _, wsPath := registryWriteFixture(t, tc.seed)
			params := map[string]any{"host": host, "workspace": true}
			if identities != nil {
				params["identities"] = identities
			}
			resp := callDaemonMethod(t, srv, "aggrega/registryAdd", params)
			daemonAsked := asked.Load()

			ref, _, refPath := registryWriteFixture(t, tc.seed)
			refRes, refErr := registryops.Add(t.Context(), ref.resolvedWorkspace, registryops.AddOptions{
				Host:       host,
				Identities: identities,
				FetchTrust: true,
				Workspace:  true,
			})
			sharedAsked := asked.Load() - daemonAsked

			if daemonAsked != tc.wantAsked {
				t.Errorf("the daemon's registration asked the trust endpoint %d time(s); want %d", daemonAsked, tc.wantAsked)
			}
			if sharedAsked != tc.wantAsked {
				t.Errorf("the shared registration asked the trust endpoint %d time(s); want %d", sharedAsked, tc.wantAsked)
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

			if tc.wantRefused {
				wantRegistrationRefusal(t, resp, refErr)
				if string(daemonBytes) != tc.seed {
					t.Errorf("a refused registration changed the configuration:\n%s", daemonBytes)
				}
				return
			}
			if refErr != nil {
				t.Fatalf("the shared registration: %v", refErr)
			}
			var res registryAddResult
			decodeDaemonResult(t, resp, &res)

			if res.Trust.Pinned != tc.wantPinned {
				t.Errorf("pinned = %v; want %v (trust %+v)", res.Trust.Pinned, tc.wantPinned, res.Trust)
			}
			if res.Trust.NotAdvertised != tc.wantNotAdvertised {
				t.Errorf("notAdvertised = %v; want %v (trust %+v)", res.Trust.NotAdvertised, tc.wantNotAdvertised, res.Trust)
			}
			if res.Trust.Baseline != tc.wantBaseline {
				t.Errorf("baseline = %v; want %v (trust %+v)", res.Trust.Baseline, tc.wantBaseline, res.Trust)
			}
			if tc.identity && !res.Trust.Skipped {
				t.Errorf("a registration pinned by hand fetched anyway: %+v", res.Trust)
			}
			offlineReason := host + " was not asked for its trust policy, because the workspace is offline"
			if gotOffline := res.Trust.Reason == offlineReason; gotOffline != tc.wantOffline {
				t.Errorf("reason = %q; want the offline refusal as the reason: %v", res.Trust.Reason, tc.wantOffline)
			}

			// The same outcome as the wire carries it, key for key, so a key present
			// that should be absent fails as surely as one missing.
			wantWire := map[string]any{"pinned": tc.wantPinned}
			switch {
			case tc.wantPinned:
				wantWire["identities"] = float64(2)
			case tc.wantOffline:
				wantWire["reason"] = offlineReason
			case tc.wantNotAdvertised:
				wantWire["reason"] = host + " publishes no trust policy (HTTP 404)"
			}
			if tc.wantNotAdvertised {
				wantWire["notAdvertised"] = true
			}
			if tc.wantBaseline {
				wantWire["baseline"] = true
			}
			if tc.identity {
				wantWire["skipped"] = true
			}
			if got := trustOnTheWire(t, resp); !reflect.DeepEqual(got, wantWire) {
				t.Errorf("the daemon's trust object = %v; want %v", got, wantWire)
			}

			if res.Trust != refRes.Trust {
				t.Errorf("the daemon's trust outcome %+v differs from the shared registration's %+v", res.Trust, refRes.Trust)
			}
			// The control on the comparison: two files carrying no policy at all are
			// equal too, so each row that expects a policy reads it off the bytes.
			if tc.wantOnDisk != "" && !strings.Contains(string(daemonBytes), tc.wantOnDisk) {
				t.Errorf("the written configuration does not carry %q:\n%s", tc.wantOnDisk, daemonBytes)
			}
			if pinnedOnDisk := strings.Contains(string(daemonBytes), "signer@example.test"); pinnedOnDisk != tc.wantPinned {
				t.Errorf("the written configuration carries the advertised identity: %v; want %v\n%s",
					pinnedOnDisk, tc.wantPinned, daemonBytes)
			}
		})
	}
}

// TestConfigInitOnTheDaemonRefusesARegistryItCannotPinOffline drives the seeding
// method offline with a registry other than the hub, and holds it to the refusal
// the shared seeding returns for the same request.
//
// Seeding takes no identities or key, so offline there is nothing to pin such a
// registry by and the refusal is the only outcome; the row that seeds the hub
// instead is the control, and it writes the built-in baseline. Both rows compare
// against the shared seeding directly, so the daemon cannot refuse, or seed, where
// the command line would not.
//
// The hub row reads its trust object as the wire carries it: the baseline pinned,
// the offline refusal as the reason, and no not-advertised key, because the hub
// was never asked.
//
// Planted defects: removing the offline refusal fails the first row, which sees
// a result and a written entry that trusts nothing; refusing the hub fails the
// second, and so does answering it as not advertised.
func TestConfigInitOnTheDaemonRefusesARegistryItCannotPinOffline(t *testing.T) {
	t.Parallel()
	const offline = "offline: true\nworkspace: {}\n"
	for _, tc := range []struct {
		name        string
		hub         bool
		wantRefused bool
	}{
		{name: "a registry other than the hub is refused", wantRefused: true},
		{name: "the hub is seeded on its built-in baseline", hub: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			host, asked := countingTrustRegistry(t, advertisedTrustDocument)
			if tc.hub {
				host = workspaceconfig.HubBaselineHost
			}

			srv, _, wsPath := registryWriteFixture(t, offline)
			resp := callDaemonMethod(t, srv, "aggrega/configInit", map[string]any{
				"registry": host, "workspace": true, "force": true,
			})

			ref, _, refPath := registryWriteFixture(t, offline)
			_, refErr := configops.Init(t.Context(), ref.resolvedWorkspace, configops.InitOptions{
				Registry: host, Workspace: true, Force: true,
			})

			if got := asked.Load(); got != 0 {
				t.Errorf("an offline seeding asked the trust endpoint %d time(s); want 0", got)
			}
			daemonBytes, err := os.ReadFile(wsPath)
			if err != nil {
				t.Fatalf("read the daemon's configuration: %v", err)
			}
			sharedBytes, err := os.ReadFile(refPath)
			if err != nil {
				t.Fatalf("read the shared seeding's configuration: %v", err)
			}
			if !bytes.Equal(daemonBytes, sharedBytes) {
				t.Errorf("the daemon left a different configuration from the shared seeding\n--- daemon ---\n%s\n--- shared ---\n%s",
					daemonBytes, sharedBytes)
			}
			if tc.wantRefused {
				wantRegistrationRefusal(t, resp, refErr)
				if string(daemonBytes) != offline {
					t.Errorf("a refused seeding changed the configuration:\n%s", daemonBytes)
				}
				return
			}
			if refErr != nil {
				t.Fatalf("the shared seeding: %v", refErr)
			}
			var res configInitResult
			decodeDaemonResult(t, resp, &res)
			if !res.Written || !res.Trust.Baseline {
				t.Errorf("the hub was not seeded offline on its baseline: %+v", res)
			}
			wantWire := map[string]any{
				"pinned":   false,
				"baseline": true,
				"reason":   host + " was not asked for its trust policy, because the workspace is offline",
			}
			if got := trustOnTheWire(t, resp); !reflect.DeepEqual(got, wantWire) {
				t.Errorf("the daemon's trust object for the hub seeded offline = %v; want %v", got, wantWire)
			}
			if issuer := workspaceconfig.DefaultBaseline().TrustedIdentities[0].Issuer; !strings.Contains(string(daemonBytes), issuer) {
				t.Errorf("the seeded configuration does not carry the built-in baseline:\n%s", daemonBytes)
			}
		})
	}
}

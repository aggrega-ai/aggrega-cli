// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/registrytrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// advertisedTrustServer stands up an httptest server serving the given body at
// the well-known trust path and a 404 elsewhere, returning its loopback host
// (so the trust fetch speaks plain HTTP) and client. No live network is used.
func advertisedTrustServer(t *testing.T, body string) (host string, client *http.Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/aggrega-trust" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), srv.Client()
}

// TestRegistryAddFetchesAndPinsTrust proves trust-on-first-use: with no manual
// flags, `registry add` fetches the registry's advertised trust document and
// pins requires_signatures, both identity OR-sets, and the public key into the
// written config — round-tripped through the config resolver.
func TestRegistryAddFetchesAndPinsTrust(t *testing.T) {
	t.Parallel()
	host, client := advertisedTrustServer(t, `{
  "requires_signatures": true,
  "trusted_identities": [
    {"identity": "dev@acme.example", "issuer": "https://accounts.google.com"},
    {"identity_regexp": "^https://github.com/acme/", "issuer": "https://token.actions.githubusercontent.com"}
  ],
  "trusted_deprecation_identities": [
    {"identity": "security@acme.example", "issuer": "https://accounts.google.com"}
  ]
}`)

	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})
	var out bytes.Buffer
	if err := registryAddRun(t.Context(), registryAddConfig{
		Host:        host,
		FetchTrust:  true,
		Workspace:   true,
		ws:          rw,
		Out:         &out,
		trustClient: client,
	}); err != nil {
		t.Fatalf("registryAddRun: %v", err)
	}

	reloaded, err := workspaceconfig.Resolve(rw.Root(), "")
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	pol, found := reloaded.Config().RegistryTrustPolicy(host)
	if !found {
		t.Fatal("registry should be found after add")
	}
	if !pol.Signing.Required {
		t.Error("requires_signatures should be pinned from the advertised document")
	}
	if len(pol.TrustedIdentities) != 2 {
		t.Fatalf("trusted_identities = %v, want 2 pinned", pol.TrustedIdentities)
	}
	if pol.TrustedIdentities[0].Identity != "dev@acme.example" {
		t.Errorf("identity[0] = %q; want the advertised identity", pol.TrustedIdentities[0].Identity)
	}
	if pol.TrustedIdentities[1].IdentityRegexp != "^https://github.com/acme/" {
		t.Errorf("identity[1] regexp = %q; want the advertised regexp", pol.TrustedIdentities[1].IdentityRegexp)
	}
	if len(pol.TrustedDeprecationIdentities) != 1 || pol.TrustedDeprecationIdentities[0].Identity != "security@acme.example" {
		t.Errorf("trusted_deprecation_identities = %v; want the advertised deprecation identity", pol.TrustedDeprecationIdentities)
	}
	if !strings.Contains(out.String(), "Pinned advertised trust policy") {
		t.Errorf("expected a pin notice; got %q", out.String())
	}
}

// TestRegistryAddManualIdentityOverridesFetch proves a manual --identity
// overrides (skips) the fetched advertised set: the served document is NOT
// pinned, only the flag identity is written. The served body would otherwise
// pin different identities, so a pinned-from-fetch result would be visible.
func TestRegistryAddManualIdentityOverridesFetch(t *testing.T) {
	t.Parallel()
	host, client := advertisedTrustServer(t, `{
  "requires_signatures": true,
  "trusted_identities": [
    {"identity": "fetched@acme.example", "issuer": "https://accounts.google.com"}
  ]
}`)

	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})
	var out bytes.Buffer
	if err := registryAddRun(t.Context(), registryAddConfig{
		Host:        host,
		Identities:  []string{"manual@acme.example=https://accounts.google.com"},
		FetchTrust:  true, // on, but a manual policy must override it
		Workspace:   true,
		ws:          rw,
		Out:         &out,
		trustClient: client,
	}); err != nil {
		t.Fatalf("registryAddRun: %v", err)
	}

	reloaded, _ := workspaceconfig.Resolve(rw.Root(), "")
	pol, found := reloaded.Config().RegistryTrustPolicy(host)
	if !found {
		t.Fatal("registry should be found")
	}
	if len(pol.TrustedIdentities) != 1 {
		t.Fatalf("trusted_identities = %v; want exactly the one manual identity (fetch overridden)", pol.TrustedIdentities)
	}
	if pol.TrustedIdentities[0].Identity != "manual@acme.example" {
		t.Errorf("identity = %q; want the manual identity, not the fetched one", pol.TrustedIdentities[0].Identity)
	}
	// The fetched requires_signatures must NOT have been pinned, since the
	// fetch was skipped in favor of the manual policy.
	if pol.Signing.Required {
		t.Error("requires_signatures should be false: the manual override skipped the fetch")
	}
	if strings.Contains(out.String(), "Pinned advertised trust policy") {
		t.Errorf("a manual override must not pin from the fetch; got %q", out.String())
	}
}

// TestRegistryAddNoTrustAdvertisedFallsBack proves a registry that serves no
// trust document (404) does not hard-fail: the add succeeds, prints the
// manual-pinning notice, and writes an empty policy.
func TestRegistryAddNoTrustAdvertisedFallsBack(t *testing.T) {
	t.Parallel()
	// A server that 404s every path — including the well-known trust path.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})
	var out bytes.Buffer
	if err := registryAddRun(t.Context(), registryAddConfig{
		Host:        host,
		FetchTrust:  true,
		Workspace:   true,
		ws:          rw,
		Out:         &out,
		trustClient: srv.Client(),
	}); err != nil {
		t.Fatalf("registryAddRun should not hard-fail on an unadvertised registry: %v", err)
	}

	reloaded, _ := workspaceconfig.Resolve(rw.Root(), "")
	pol, found := reloaded.Config().RegistryTrustPolicy(host)
	if !found {
		t.Fatal("registry should still be registered after a fallback add")
	}
	if len(pol.TrustedIdentities) != 0 || pol.Signing.Required {
		t.Errorf("fallback should write an empty policy; got %+v", pol)
	}
	// The notice is the first line, worded whole, and it is said once; the
	// registration's own confirmation follows it on a line of its own.
	notice := "Notice: " + host + " publishes no trust policy (HTTP 404); pin its trust by hand with --identity or --key"
	if first, _, _ := strings.Cut(out.String(), "\n"); first != notice || strings.Count(out.String(), notice) != 1 {
		t.Errorf("output = %q; want it to open with the one manual-pinning notice %q", out.String(), notice)
	}
}

// TestRegistryAddUnreachableEndpointFallsBack proves a transport failure (the
// endpoint is unreachable) is tolerated like a 404 — the add succeeds with a
// notice — rather than hard-failing registration on a network error.
func TestRegistryAddUnreachableEndpointFallsBack(t *testing.T) {
	t.Parallel()
	// A loopback address that refuses connections: the fetch fails at connect (a
	// transport error, not a 404). No live DNS. The client is a real one rather
	// than a stubbed transport, so the refusal is a genuine dial failure.
	host := refusingLoopbackAddr(t)
	client := &http.Client{}

	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})
	var out bytes.Buffer
	if err := registryAddRun(t.Context(), registryAddConfig{
		Host:        host,
		FetchTrust:  true,
		Workspace:   true,
		ws:          rw,
		Out:         &out,
		trustClient: client,
	}); err != nil {
		t.Fatalf("an unreachable trust endpoint must not hard-fail the add: %v", err)
	}
	reloaded, _ := workspaceconfig.Resolve(rw.Root(), "")
	if _, found := reloaded.Config().RegistryTrustPolicy(host); !found {
		t.Fatal("registry should still be registered after a transport-failure fallback")
	}
	// The transport error's own words vary by platform, so the first line is
	// pinned at both ends rather than whole; the notice is said once.
	got := out.String()
	first, _, _ := strings.Cut(got, "\n")
	if !strings.HasPrefix(first, "Notice: "+host+" did not return its trust policy: ") ||
		!strings.HasSuffix(first, "; pin its trust by hand with --identity or --key") ||
		strings.Count(got, "; pin its trust by hand with --identity or --key") != 1 {
		t.Errorf("output = %q; want it to open with the one manual-pinning notice naming the failed request", got)
	}
}

// TestRegistryAddNoFetchTrustSkipsFetch proves --no-fetch-trust (FetchTrust
// false) writes the flag-derived policy without contacting the endpoint. The
// server would pin an identity if fetched; a nil trustClient with FetchTrust
// off proves the fetch path is never entered.
func TestRegistryAddNoFetchTrustSkipsFetch(t *testing.T) {
	t.Parallel()
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})
	var out bytes.Buffer
	if err := registryAddRun(t.Context(), registryAddConfig{
		Host:       "no-fetch.example.com",
		FetchTrust: false,
		Workspace:  true,
		// trustClient deliberately nil: if the fetch path were entered it would
		// build a default client and attempt a live GET, which this test forbids.
		ws:  rw,
		Out: &out,
	}); err != nil {
		t.Fatalf("registryAddRun: %v", err)
	}
	reloaded, _ := workspaceconfig.Resolve(rw.Root(), "")
	pol, found := reloaded.Config().RegistryTrustPolicy("no-fetch.example.com")
	if !found {
		t.Fatal("registry should be registered")
	}
	if len(pol.TrustedIdentities) != 0 {
		t.Errorf("no-fetch add should write an empty policy; got %+v", pol)
	}
	if strings.Contains(out.String(), "Pinned advertised trust policy") {
		t.Errorf("--no-fetch-trust must not pin from a fetch; got %q", out.String())
	}
}

// TestRegistryAddTakesItsTrustClientFromTheEgressGate drives `registry add` the
// way the command runs it — with no transport supplied — against a loopback
// registry that advertises a policy, online and offline.
//
// Every other test in this file injects a client, so none of them can see which
// client the command reaches for when nothing is injected, and that is the only
// client a person running the command ever gets. Online, the endpoint is asked
// once and its policy pinned. Offline, it is never asked: with no policy supplied
// the command refuses with the offline-registration condition, prints no notice
// and writes no entry; with an identity supplied by hand it registers exactly
// that identity and nothing a fetch would have brought; the hub is written on its
// built-in baseline, and the one line printed says that baseline was pinned and
// why no advertised policy was taken — not that the hub advertises nothing, and
// not that identities should be pinned by hand. The count is read at the
// endpoint, so a fetch refused before any dial and one sent and failed cannot be
// mistaken for each other.
//
// Planted defects: a registration handed no client fails the online row, which
// sees the endpoint never asked and the workspace reported offline; a command
// building a client of its own fails both offline rows, which see the endpoint
// asked under a posture that forbids it; removing the offline refusal fails the
// refusal row, which sees an entry written that trusts nothing; refusing a
// registration that supplied its own policy fails the pinned row; printing the
// not-advertised sentence for the hub fails the hub row.
func TestRegistryAddTakesItsTrustClientFromTheEgressGate(t *testing.T) {
	t.Parallel()
	const doc = `{"requires_signatures": true, "trusted_identities": [` +
		`{"identity": "signer@example.test", "issuer": "https://issuer.example.test"}, ` +
		`{"identity": "ci@example.test", "issuer": "https://ci-issuer.example.test"}]}`
	const offline = "offline: true\nworkspace: {}\n"

	for _, tc := range []struct {
		name        string
		seed        string
		hub         bool
		identities  []string
		wantAsked   int32
		wantPinned  bool
		wantRefused bool
	}{
		{name: "an online workspace fetches the advertised policy once and pins it",
			seed: "workspace: {}\n", wantAsked: 1, wantPinned: true},
		{name: "an offline workspace never reaches the registry and refuses to register it unpinned",
			seed: offline, wantRefused: true},
		{name: "an offline workspace registers exactly the identity pinned by hand",
			seed: offline, identities: []string{"hand@example.test=https://issuer.example.test"}},
		{name: "an offline workspace registers the hub on its built-in baseline and says so",
			seed: offline, hub: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			asked := &atomic.Int32{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				asked.Add(1)
				if r.URL.Path != registrytrust.WellKnownPath {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(doc))
			}))
			t.Cleanup(srv.Close)
			host := strings.TrimPrefix(srv.URL, "http://")
			if tc.hub {
				host = workspaceconfig.HubBaselineHost
			}

			// The workspace is resolved against a frozen environment, so the
			// posture under test is the one this row wrote and never one the
			// machine running the test happens to carry.
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(tc.seed), 0o644); err != nil {
				t.Fatalf("write the workspace configuration: %v", err)
			}
			rw := resolveTempWorkspaceAgainst(t, tempWorkspaceOptions{Root: root}, frozenEnv(nil))

			var out bytes.Buffer
			runErr := registryAddRun(t.Context(), registryAddConfig{
				Host:       host,
				Identities: tc.identities,
				FetchTrust: true,
				Workspace:  true,
				ws:         rw,
				Out:        &out,
			})

			if got := asked.Load(); got != tc.wantAsked {
				t.Errorf("the trust endpoint was asked %d time(s); want %d", got, tc.wantAsked)
			}
			reloaded, err := workspaceconfig.ResolveWithoutGlobal("", root, frozenEnv(nil))
			if err != nil {
				t.Fatalf("re-resolve: %v", err)
			}
			pol, found := reloaded.Config().RegistryTrustPolicy(host)
			if tc.wantRefused {
				if !errors.Is(runErr, registrytrust.ErrOfflineRegistrationWithoutTrustPolicy()) {
					t.Fatalf("registryAddRun = %v; want the offline registration refused", runErr)
				}
				if short, ok := cerrors.ShortCodeOf(runErr); !ok || short != "E0310" {
					t.Errorf("the refusal carries short code %q (present %t); want E0310", short, ok)
				}
				if found {
					t.Errorf("a refused registration wrote an entry for %s: %+v", host, pol)
				}
				if out.Len() != 0 {
					t.Errorf("a refused registration printed %q; the refusal is the whole answer", out.String())
				}
				return
			}
			if runErr != nil {
				t.Fatalf("registryAddRun: %v", runErr)
			}
			if !found {
				t.Fatalf("the registration wrote no entry for %s", host)
			}
			if tc.hub {
				// The hub resolves to its baseline whether or not an entry was written,
				// so the written file is read rather than the resolved policy.
				written, err := os.ReadFile(filepath.Join(root, workspaceconfig.ConfigFilename))
				if err != nil {
					t.Fatalf("read the workspace configuration: %v", err)
				}
				if issuer := workspaceconfig.DefaultBaseline().TrustedIdentities[0].Issuer; !strings.Contains(string(written), host) || !strings.Contains(string(written), issuer) {
					t.Errorf("the hub was not written on its built-in baseline:\n%s", written)
				}
				want := fmt.Sprintf("%s was not asked for its trust policy, because the workspace is offline; using the built-in trust baseline for %s instead\n",
					host, host)
				if !strings.HasPrefix(out.String(), want) || strings.Contains(out.String(), "pin its trust by hand") {
					t.Errorf("output = %q; want it to open with the one baseline notice %q", out.String(), want)
				}
				return
			}
			if tc.wantPinned {
				if want := fmt.Sprintf("Pinned advertised trust policy from %s (2 identities)\n", host); !strings.Contains(out.String(), want) {
					t.Errorf("output = %q; want the pin notice %q", out.String(), want)
				}
				if !pol.Signing.Required || len(pol.TrustedIdentities) != 2 {
					t.Errorf("the advertised policy was not pinned: %+v", pol)
				}
				return
			}
			if len(pol.TrustedIdentities) != 1 || pol.TrustedIdentities[0].Identity != "hand@example.test" {
				t.Errorf("the entry does not carry exactly the identity pinned by hand: %+v", pol.TrustedIdentities)
			}
			if pol.Signing.Required {
				t.Errorf("the entry carries the advertised signing requirement, which only a fetch could have supplied: %+v", pol)
			}
			if strings.Contains(out.String(), "Pinned advertised") || strings.Contains(out.String(), "pin its trust by hand") {
				t.Errorf("a registration pinned by hand reported a fetch outcome: %q", out.String())
			}
		})
	}
}

// TestRegistryAddMalformedTrustHardFails proves a registry that serves a
// malformed trust document (fetches but does not decode) fails the add — the
// endpoint answered with unusable content, a real misconfiguration.
func TestRegistryAddMalformedTrustHardFails(t *testing.T) {
	t.Parallel()
	host, client := advertisedTrustServer(t, `{"trusted_identities": [{"identity": "no-issuer-here"}]}`)
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})
	var out bytes.Buffer
	err := registryAddRun(t.Context(), registryAddConfig{
		Host:        host,
		FetchTrust:  true,
		ws:          rw,
		Out:         &out,
		trustClient: client,
	})
	if err == nil {
		t.Fatal("a malformed advertised trust document should fail the add")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Errorf("err = %v; want a malformed-trust failure", err)
	}
}

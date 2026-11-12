// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
)

// TestTheCredentialStoreIsReadOncePerRegistryForATarget sends requests to a
// registry that challenges every request made without its credential, through the
// clients remote targets are built with (signInClientFor), and counts the reads of
// the credential store. A store that delegates to a credential helper starts a
// process for every read, so a target reads it once per registry host however many
// requests it sends; a second target reads it again, which is how a login made
// between two operations reaches the next one.
//
// Planted defects and the rows they fail:
//   - no memo, so every request reads the store when the sign-in cache files it
//     and again when the token is looked up: "one target, many requests";
//   - a memo shared by every target of the process, so a later target never reads
//     the store again and a login made since is not seen: "two targets".
func TestTheCredentialStoreIsReadOncePerRegistryForATarget(t *testing.T) {
	t.Parallel()
	const requests = 6
	rows := []struct {
		name      string
		targets   int
		wantReads int64
	}{
		{name: "one target, many requests", targets: 1, wantReads: 1},
		{name: "two targets", targets: 2, wantReads: 2},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			user := strings.ReplaceAll(t.Name(), "/", "-")
			const pass = "store-read-password"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if u, p, ok := r.BasicAuth(); ok && u == user && p == pass {
					w.WriteHeader(http.StatusOK)
					return
				}
				w.Header().Set("WWW-Authenticate", `Basic realm="store-read test"`)
				w.WriteHeader(http.StatusUnauthorized)
			}))
			t.Cleanup(srv.Close)
			host := strings.TrimPrefix(srv.URL, "http://")
			challengedHosts.Delete(host)
			firstContacts.Delete(host)

			var reads atomic.Int64
			read := func(_ context.Context, hostport string) (auth.Credential, error) {
				reads.Add(1)
				if hostport != host {
					return auth.EmptyCredential, nil
				}
				return auth.Credential{Username: user, Password: pass}, nil
			}
			for target := range row.targets {
				client := signInClientFor(&http.Client{}, read)
				for i := range requests {
					req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/v2/", nil)
					if err != nil {
						t.Fatalf("build request: %v", err)
					}
					resp, err := client.Do(req)
					if err != nil {
						t.Fatalf("target %d request %d: %v", target, i, err)
					}
					_ = resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						t.Fatalf("target %d request %d answered %d; want 200 with the credential", target, i, resp.StatusCode)
					}
				}
			}
			if got := reads.Load(); got != row.wantReads {
				t.Fatalf("%d target(s) sending %d requests each read the credential store %d times; want %d",
					row.targets, requests, got, row.wantReads)
			}
		})
	}
}

// TestTheFirstRequestToARegistryGoesAlone drives the first-contact wait directly,
// with no clock: the first request entering goes at once; a request entering while
// it is unanswered waits — shown by one whose own context has already ended, which
// must come back with that context's error rather than go; and once the first is
// answered every request goes at once. Each row names the registry the first
// request reaches and the spelling the later requests name it by: two spellings of
// one registry share one wait, and a different registry has a wait of its own.
//
// Planted defects and the rows they fail:
//   - a wait that lets every request go: "one spelling", "two spellings of one
//     registry";
//   - a wait that makes every request the first, or none: every row;
//   - a first request that never lets the others go: every row but "another
//     registry", whose last step would otherwise wait on a context that never ends;
//   - a wait keyed on the spelling a request names rather than the registry's
//     lookup spelling: "two spellings of one registry", whose later request then
//     goes as the first of a registry of its own;
//   - one wait for every registry: "another registry".
func TestTheFirstRequestToARegistryGoesAlone(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name, first, later string
		// sameRegistry says the later spelling names the first's registry.
		sameRegistry bool
	}{
		{name: "one spelling", first: "first-contact-one.example", later: "first-contact-one.example", sameRegistry: true},
		{name: "two spellings of one registry", first: "First-Contact-Two.Example:443", later: "first-contact-two.example", sameRegistry: true},
		{name: "another registry", first: "first-contact-three.example", later: "first-contact-four.example"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			firstContacts.Delete(registryhost.Normalize(row.first))
			firstContacts.Delete(registryhost.Normalize(row.later))

			f := firstContactWith(row.first)
			lead, err := f.enter(context.Background())
			if err != nil || !lead {
				t.Fatalf("the first request goes at once: lead=%t err=%v; want lead=true", lead, err)
			}

			later := firstContactWith(row.later)
			ended, cancel := context.WithCancel(context.Background())
			cancel()
			lead, err = later.enter(ended)
			if !row.sameRegistry {
				if !lead || err != nil {
					t.Fatalf("the first request to another registry goes at once: lead=%t err=%v; want lead=true", lead, err)
				}
				return
			}
			if lead || !errors.Is(err, context.Canceled) {
				t.Fatalf("a request naming %q while the first to %q is unanswered waits: lead=%t err=%v; want it held until its own context ended",
					row.later, row.first, lead, err)
			}

			f.finish()
			for i := range 3 {
				lead, err = later.enter(context.Background())
				if lead || err != nil {
					t.Fatalf("every request after the first is answered goes at once: request %d lead=%t err=%v", i, lead, err)
				}
			}
		})
	}
}

// TestASignInIsFiledUnderTheCredentialItWasMadeWith drives the sign-in cache with
// a credential store whose answer changes between two reads — a login landing while
// a sign-in is under way, as a credential helper asked twice can show. The token the
// sign-in obtains is obtained with the credential the request was filed under, and
// is handed out to that credential alone.
//
// Planted defects and what fails:
//   - a sign-in that reads the store again rather than the credential the request
//     was filed under obtains the second credential's token under the first
//     identity, and fails "the sign-in is made with the credential it is filed
//     under";
//   - a cache filed by registry alone hands the first credential's token to a
//     request made with the second, and fails "another credential is handed
//     nothing";
//   - a record kept per identity for the life of the process, rather than one per
//     registry replaced when another credential signs in, fails "a registry holds
//     one credential's record at a time".
func TestASignInIsFiledUnderTheCredentialItWasMadeWith(t *testing.T) {
	t.Parallel()
	host := "sign-in-" + t.Name() + ".invalid"
	first := auth.Credential{Username: t.Name() + "-first", Password: "first"}
	second := auth.Credential{Username: t.Name() + "-second", Password: "second"}
	var reads atomic.Int64
	read := func(context.Context, string) (auth.Credential, error) {
		if reads.Add(1) == 1 {
			return first, nil
		}
		return second, nil
	}
	credential := pinnedFirst(read)
	cache := signInCache{credential: credential}
	ctx := context.Background()

	token, err := cache.Set(ctx, host, auth.SchemeBasic, "", func(ctx context.Context) (string, error) {
		cred, err := credential(ctx, host)
		return cred.Username, err
	})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if token != first.Username {
		t.Fatalf("the sign-in is made with the credential it is filed under: it obtained %q; want %q", token, first.Username)
	}

	// The credential the token was obtained with finds it.
	again := signInCache{credential: func(context.Context, string) (auth.Credential, error) { return first, nil }}
	if got, err := again.GetToken(ctx, host, auth.SchemeBasic, ""); err != nil || got != first.Username {
		t.Fatalf("the credential the token was obtained with finds it: got %q, %v", got, err)
	}

	// Every read of the store from here on answers the second credential.
	if _, err := cache.GetToken(ctx, host, auth.SchemeBasic, ""); err == nil {
		t.Fatalf("another credential is handed nothing: a request made with %q was handed the token obtained with %q",
			second.Username, first.Username)
	}
	if scheme, err := cache.GetScheme(ctx, host); err == nil {
		t.Fatalf("another credential is handed nothing: a request made with %q was handed the scheme %v learned with %q",
			second.Username, scheme, first.Username)
	}

	// The second credential's requests replaced what the registry held.
	if got, err := again.GetToken(ctx, host, auth.SchemeBasic, ""); err == nil {
		t.Fatalf("a registry holds one credential's record at a time: the first credential still finds %q after a request made with the second", got)
	}
}

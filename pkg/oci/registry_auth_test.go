// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// signInRequest is one request a signInRegistry received: what it asked, the user
// its credential named ("" for none), and whether it was refused with a challenge.
type signInRequest struct {
	method, path, user string
	challenged         bool
}

// signInRegistry serves the distribution protocol from an in-memory registry to
// requests carrying the Basic credential of a user it accepts, and answers every
// other request with 401 and a Basic challenge, the way a registry asking for a
// credential does. It records every request, in arrival order.
type signInRegistry struct {
	inner http.Handler
	users map[string]string

	mu       sync.Mutex
	requests []signInRequest
}

func newSignInRegistry(users map[string]string) *signInRegistry {
	return &signInRegistry{inner: registry.New(), users: users}
}

func (r *signInRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	user, pass, ok := req.BasicAuth()
	accepted := ok && pass != "" && r.users[user] == pass
	r.mu.Lock()
	r.requests = append(r.requests, signInRequest{method: req.Method, path: req.URL.Path, user: user, challenged: !accepted})
	r.mu.Unlock()
	if !accepted {
		_, _ = io.Copy(io.Discard, req.Body)
		w.Header().Set("WWW-Authenticate", `Basic realm="sign-in test"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	r.inner.ServeHTTP(w, req)
}

// mark returns how many requests the registry has received, so a step reads only
// the requests after it.
func (r *signInRegistry) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

// since returns the requests received from the mark on.
func (r *signInRegistry) since(mark int) []signInRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]signInRequest(nil), r.requests[mark:]...)
}

// challenges counts the requests among reqs answered with a challenge.
func challenges(reqs []signInRequest) int {
	n := 0
	for _, r := range reqs {
		if r.challenged {
			n++
		}
	}
	return n
}

// describeSignIns renders requests one per line, for a failure message.
func describeSignIns(reqs []signInRequest) string {
	var b strings.Builder
	for i, r := range reqs {
		fmt.Fprintf(&b, "  %3d %-6s %s user=%q challenged=%t\n", i, r.method, r.path, r.user, r.challenged)
	}
	return b.String()
}

// signInUser returns a user name no other test of this binary signs in as, so
// what an earlier test learned under its own credential is never this one's.
func signInUser(t *testing.T, role string) string {
	return strings.NewReplacer("/", "-", " ", "-").Replace(t.Name()) + "-" + role
}

// plantSignIns writes the credential file of this test's credential home with one
// Basic entry per host.
func plantSignIns(t *testing.T, entries map[string][2]string) {
	t.Helper()
	type entry struct {
		Auth string `json:"auth"`
	}
	auths := make(map[string]entry, len(entries))
	for host, up := range entries {
		auths[host] = entry{Auth: base64.StdEncoding.EncodeToString([]byte(up[0] + ":" + up[1]))}
	}
	body, err := json.Marshal(struct {
		Auths map[string]entry `json:"auths"`
	}{Auths: auths})
	if err != nil {
		t.Fatalf("encode the credential file: %v", err)
	}
	plantCredentialFile(t, string(body))
}

// startSignInRegistry starts a registry accepting users, and returns it with the
// host it answers on and the same registry under its other loopback name. This
// process is made to forget having reached either name, so the test starts from
// a registry it has never contacted.
func startSignInRegistry(t *testing.T, users map[string]string) (reg *signInRegistry, host, otherName string) {
	t.Helper()
	reg = newSignInRegistry(users)
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host = strings.TrimPrefix(srv.URL, "http://")
	otherName = strings.Replace(host, "127.0.0.1", "localhost", 1)
	oci.ForgetRegistrySignIn(host)
	oci.ForgetRegistrySignIn(otherName)
	return reg, host, otherName
}

// pushEntities publishes count entities to host, each through a target of its own
// built by the production constructor, as a catalog publish does.
func pushEntities(t *testing.T, host, prefix string, count int) {
	t.Helper()
	for i := range count {
		repo, err := oci.NewRemoteRepository(fmt.Sprintf("%s/component/core/%s-%02d", host, prefix, i), &http.Client{})
		if err != nil {
			t.Fatalf("NewRemoteRepository: %v", err)
		}
		content := []byte(fmt.Sprintf("kind: Component\nmetadata:\n  name: %s-%02d\n", prefix, i))
		if _, err := oci.Push(context.Background(), repo, "1.0.0", oci.MediaTypeComponent, content, nil); err != nil {
			t.Fatalf("push %s-%02d: %v", prefix, i, err)
		}
	}
}

// TestARegistryChallengesEachCredentialOnce drives pushes and pulls through the
// production repository constructor against a registry that challenges every
// request sent without its credential, and counts the challenges on the wire. A
// registry's challenge is remembered for the process, filed under the credential
// it was answered with: once one request has been challenged, every request after
// it — through any target — carries the credential the first time, and a request
// made with another credential is never sent with what was learned under the
// first. Each row names the planted defect it catches.
func TestARegistryChallengesEachCredentialOnce(t *testing.T) {
	// Not parallel: each row writes the credential home the whole binary shares.
	rows := []struct {
		name, defect string
		run          func(t *testing.T)
	}{
		{
			name: "a push of several entities, each through its own target",
			defect: "the auth client is given no cache, so every request of every push is sent once without the " +
				"credential and challenged",
			run: func(t *testing.T) {
				user, pass := signInUser(t, "publisher"), "publisher-password"
				reg, host, _ := startSignInRegistry(t, map[string]string{user: pass})
				plantSignIns(t, map[string][2]string{host: {user, pass}})

				pushEntities(t, host, "pushed", 5)
				reqs := reg.since(0)
				if got := challenges(reqs); got != 1 {
					t.Fatalf("the pushes were challenged %d times; want once for the registry\n%s", got, describeSignIns(reqs))
				}
				for i, r := range reqs[1:] {
					if r.user != user {
						t.Fatalf("request %d after the challenge carried user %q; want %q\n%s", i+1, r.user, user, describeSignIns(reqs))
					}
				}
			},
		},
		{
			name: "a pull of several entities, eight at a time, each through its own target",
			defect: "the auth client is given no cache, so every request of every pull is challenged; or the first " +
				"requests started at once to a registry this process has not reached each draw a challenge",
			run: func(t *testing.T) {
				user, pass := signInUser(t, "reader"), "reader-password"
				reg, host, otherName := startSignInRegistry(t, map[string]string{user: pass})
				plantSignIns(t, map[string][2]string{host: {user, pass}, otherName: {user, pass}})
				// Published under the registry's other name, so what the publish learned is
				// filed under that name and the pulls below meet the registry for the
				// first time.
				const entities = 3 * oci.RegistryRequestsAtOnce
				pushEntities(t, otherName, "pulled", entities)

				mark := reg.mark()
				errs := make([]error, entities)
				oci.EachAtOnce(context.Background(), entities, oci.RegistryRequestsAtOnce, func(ctx context.Context, i int) {
					repo, err := oci.NewRemoteRepository(fmt.Sprintf("%s/component/core/pulled-%02d", host, i), &http.Client{})
					if err != nil {
						errs[i] = err
						return
					}
					got, err := oci.Pull(ctx, repo, "1.0.0")
					if err != nil {
						errs[i] = err
						return
					}
					if want := fmt.Sprintf("kind: Component\nmetadata:\n  name: pulled-%02d\n", i); string(got.Bytes) != want {
						errs[i] = fmt.Errorf("pulled %q; want %q", got.Bytes, want)
					}
				})
				for i, err := range errs {
					if err != nil {
						t.Errorf("pull %d: %v", i, err)
					}
				}
				reqs := reg.since(mark)
				if got := challenges(reqs); got != 1 {
					t.Fatalf("the pulls were challenged %d times; want once for the registry\n%s", got, describeSignIns(reqs))
				}
			},
		},
		{
			name: "a login between two pushes",
			defect: "what a sign-in learned is filed by registry alone, so the second push is sent with the first " +
				"credential's token after the stored credential changed; or what each credential learned is kept for " +
				"the life of the process, so the earlier credential's record outlives the login",
			run: func(t *testing.T) {
				first, second := signInUser(t, "first"), signInUser(t, "second")
				firstCred := auth.Credential{Username: first, Password: "first-password"}
				secondCred := auth.Credential{Username: second, Password: "second-password"}
				reg, host, _ := startSignInRegistry(t, map[string]string{first: "first-password", second: "second-password"})
				plantSignIns(t, map[string][2]string{host: {first, "first-password"}})
				pushEntities(t, host, "before", 2)
				if !oci.SignInHeldFor(host, firstCred) {
					t.Fatalf("after the first push, nothing is held for its credential, so this row proves nothing about replacing it")
				}

				plantSignIns(t, map[string][2]string{host: {second, "second-password"}})
				defer func() {
					if oci.SignInHeldFor(host, firstCred) {
						t.Errorf("what the earlier credential's sign-in obtained is still held after a push made with another")
					}
					if !oci.SignInHeldFor(host, secondCred) {
						t.Errorf("nothing is held for the credential the later push was made with")
					}
				}()
				mark := reg.mark()
				pushEntities(t, host, "after", 2)
				reqs := reg.since(mark)
				for i, r := range reqs {
					if r.user == first {
						t.Fatalf("request %d after the stored credential changed was sent as %q, the earlier credential\n%s",
							i, first, describeSignIns(reqs))
					}
				}
				if got := challenges(reqs); got != 1 {
					t.Fatalf("the pushes under the new credential were challenged %d times; want once for it\n%s", got, describeSignIns(reqs))
				}
			},
		},
		{
			name: "a login check after a push",
			defect: "the login check reads what a sign-in learned under the stored credential, so a wrong password " +
				"is accepted on the strength of a token obtained with the right one",
			run: func(t *testing.T) {
				user, pass := signInUser(t, "checked"), "checked-password"
				_, host, _ := startSignInRegistry(t, map[string]string{user: pass})
				plantSignIns(t, map[string][2]string{host: {user, pass}})
				pushEntities(t, host, "checked", 1)

				err := oci.VerifyCredential(context.Background(), host, auth.Credential{Username: user, Password: "not-the-password"}, &http.Client{})
				if !errors.Is(err, oci.ErrFetchUnauthorized()) {
					t.Fatalf("checking a wrong password after a push under the right one: got %v; want ErrFetchUnauthorized", err)
				}
				if err := oci.VerifyCredential(context.Background(), host, auth.Credential{Username: user, Password: pass}, &http.Client{}); err != nil {
					t.Fatalf("checking the right password: %v", err)
				}
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			hometest.Isolate(t)
			row.run(t)
			if t.Failed() {
				t.Logf("defect this row catches: %s", row.defect)
			}
		})
	}
}

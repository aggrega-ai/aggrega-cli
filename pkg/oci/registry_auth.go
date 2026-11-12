// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"net/http"
	"sync"
	"sync/atomic"

	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
)

// This file is the one place that decides what a registry's sign-in teaches the
// requests that follow it. A registry that wants a credential answers a request
// sent without one with a challenge (401 and the scheme it signs in by), and the
// request is sent again with the credential. Without anything remembered, every
// request to such a registry is sent twice. What this file keeps:
//
//   - Which registries have challenged a request. It is a fact about the registry,
//     carries nothing of any credential, and is what lets a request to a registry
//     that never challenges go out without the credential store being read at all.
//   - For each registry, the identity of the credential its last sign-in was made
//     with — a digest of the registry and every field of that credential — with
//     the scheme the registry signs in by and the tokens obtained under it
//     (signInCache). A request reads the credential it is made with through the
//     same credential function the sign-in itself reads — one read of the store
//     per registry for the life of the remote target making it (onceForTheTarget)
//     — and is sent with what was learned under that identity and nothing else. A
//     token is filed under the identity of the credential it was obtained with,
//     because the sign-in that obtains it is handed that same credential
//     (pinnedCredential). A request made with another credential replaces what the
//     registry holds with an empty record of its own (hostSignIn). So a request is
//     never sent with a scheme or token obtained under another credential: a login
//     that changes the stored credential starts afresh under a new identity, and
//     two commands in one long-lived process share what is held only where they
//     authenticate as the same identity. Where requests to one registry alternate
//     between credentials — a credential helper handing out a new token on every
//     read, or two such commands at once — each replaces the other's record, and
//     those requests are challenged as they would be with nothing remembered.
//
// Every registry is keyed by registryhost.Normalize, the spelling every
// per-registry lookup keys on, so two spellings of one registry share what is
// kept, as they share one slow-down allowance. It lives for the process, is never
// written anywhere, and holds one record per registry reached. A registry signing
// in by Basic is challenged once per identity; a registry signing in by Bearer
// tokens is challenged once per identity and repository scope, because a token is
// granted for a scope.
//
// The first request this process sends a registry goes alone (firstContact):
// requests to the same registry wait until it has been answered, so a command
// that starts many requests at once to a registry it has not reached before draws
// one challenge rather than one per request.

// challengedHosts holds the registries that have answered a request with a
// challenge in this process, keyed by registryhost.Normalize — the spelling every
// per-registry lookup keys on, the slow-down allowance's included — so two
// spellings of one registry share one record.
var challengedHosts sync.Map

// signInCaches holds, keyed by registryhost.Normalize, what the sign-in to one
// registry has obtained (hostSignIn).
var signInCaches sync.Map

// hostSignIn is what one credential's sign-in to one registry has obtained: the
// identity of that credential and the auth.Cache filed under it. A registry holds
// one at a time. A request made with another credential replaces it with an empty
// one of its own rather than reading it, so what this process keeps stays bounded
// by the registries it reaches, and no identity ever reads another's tokens.
type hostSignIn struct {
	mu    sync.Mutex
	id    signInIdentity
	cache auth.Cache
}

// signInIdentity is a digest of a registry host and every field of the credential
// a request to it is made with. It is a key and nothing else: the credential itself
// is never recovered from it.
type signInIdentity [sha256.Size]byte

// identityOf returns the identity a request to host made with cred is filed under.
// Each field is written with its length ahead of it, so no two different
// credentials write the same bytes.
func identityOf(host string, cred auth.Credential) signInIdentity {
	h := sha256.New()
	for _, part := range []string{host, cred.Username, cred.Password, cred.RefreshToken, cred.AccessToken} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(part)))
		_, _ = h.Write(n[:])
		_, _ = h.Write([]byte(part))
	}
	var id signInIdentity
	copy(id[:], h.Sum(nil))
	return id
}

// signInCache is the auth.Cache every remote target this package builds hands its
// auth client (remoteClient). credential is that same client's credential
// function, so the identity a request is filed under is read exactly as the
// sign-in reads the credential it sends.
type signInCache struct {
	credential auth.CredentialFunc
}

// forIdentity returns the cache of what the sign-in of the credential a request to
// host is made with has obtained, the registry's lookup spelling (registryhost.
// Normalize) that cache is keyed by, and that credential. The credential is read
// for host exactly as the request names it, which is how the store is asked.
//
// Where the registry holds what another credential's sign-in obtained, it is
// replaced with an empty cache for this one (hostSignIn).
func (c signInCache) forIdentity(ctx context.Context, host string) (auth.Cache, string, auth.Credential, error) {
	cred, err := c.credential(ctx, host)
	if err != nil {
		return nil, "", auth.EmptyCredential, err
	}
	registry := registryhost.Normalize(host)
	id := identityOf(registry, cred)
	held, _ := signInCaches.LoadOrStore(registry, &hostSignIn{})
	h := held.(*hostSignIn)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cache == nil || h.id != id {
		h.id, h.cache = id, auth.NewCache()
	}
	return h.cache, registry, cred, nil
}

// GetScheme returns the scheme the registry signs in by, as learned under the
// credential the request is made with. A registry that has never challenged a
// request in this process has none, and the credential store is not read for it.
// A credential that cannot be read has none either: the request goes without a
// credential, and the sign-in its challenge starts reports the failure.
func (c signInCache) GetScheme(ctx context.Context, host string) (auth.Scheme, error) {
	if _, ok := challengedHosts.Load(registryhost.Normalize(host)); !ok {
		return auth.SchemeUnknown, errdef.ErrNotFound
	}
	cache, registry, _, err := c.forIdentity(ctx, host)
	if err != nil {
		return auth.SchemeUnknown, errdef.ErrNotFound
	}
	return cache.GetScheme(ctx, registry)
}

// GetToken returns the token obtained under the credential the request is made
// with, for scheme and key.
func (c signInCache) GetToken(ctx context.Context, host string, scheme auth.Scheme, key string) (string, error) {
	cache, registry, _, err := c.forIdentity(ctx, host)
	if err != nil {
		return "", errdef.ErrNotFound
	}
	return cache.GetToken(ctx, registry, scheme, key)
}

// Set obtains a token through fetch and files it under the identity of the
// credential the request is made with. fetch is handed that same credential
// (pinnedCredential), so the token is obtained under the identity it is filed
// under even where the credential store changes in between. Where the credential
// cannot be read, fetch runs as it would with nothing remembered, and reports the
// failure in its own words.
func (c signInCache) Set(ctx context.Context, host string, scheme auth.Scheme, key string, fetch func(context.Context) (string, error)) (string, error) {
	challengedHosts.Store(registryhost.Normalize(host), struct{}{})
	cache, registry, cred, err := c.forIdentity(ctx, host)
	if err != nil {
		return fetch(ctx)
	}
	return cache.Set(ctx, registry, scheme, key, func(fetchCtx context.Context) (string, error) {
		return fetch(withPinnedCredential(fetchCtx, host, cred))
	})
}

// pinnedCredentialKey keys the credential a sign-in is pinned to
// (withPinnedCredential).
type pinnedCredentialKey struct{}

// pinnedCredential is the credential one sign-in to host is made with.
type pinnedCredential struct {
	host string
	cred auth.Credential
}

// withPinnedCredential returns a context under which the credential function of a
// remote target answers cred for host, rather than reading the store again.
func withPinnedCredential(ctx context.Context, host string, cred auth.Credential) context.Context {
	return context.WithValue(ctx, pinnedCredentialKey{}, pinnedCredential{host: host, cred: cred})
}

// pinnedFirst returns read answering, for the host a sign-in is pinned to, the
// credential it is pinned to (withPinnedCredential), and reading the store as read
// does otherwise.
func pinnedFirst(read auth.CredentialFunc) auth.CredentialFunc {
	return func(ctx context.Context, hostport string) (auth.Credential, error) {
		if pinned, ok := ctx.Value(pinnedCredentialKey{}).(pinnedCredential); ok && pinned.host == hostport {
			return pinned.cred, nil
		}
		return read(ctx, hostport)
	}
}

// onceForTheTarget returns read answering each registry host from one read of it:
// the first call for a host reads the store, and every later call for that host
// is handed the same credential, or the same failure, without reading it again. It
// is made once per remote target (signInClientFor), so what it holds lives as long
// as that target.
func onceForTheTarget(read auth.CredentialFunc) auth.CredentialFunc {
	type answer struct {
		once sync.Once
		cred auth.Credential
		err  error
	}
	var answers sync.Map // host → *answer
	return func(ctx context.Context, hostport string) (auth.Credential, error) {
		a, _ := answers.LoadOrStore(hostport, &answer{})
		ans := a.(*answer)
		ans.once.Do(func() { ans.cred, ans.err = read(ctx, hostport) })
		return ans.cred, ans.err
	}
}

// signInClient is the client a remote target this package builds sends its
// requests through: the auth client, behind the first-contact wait.
type signInClient struct {
	auth *auth.Client
}

// Do sends req through the auth client once this process's first request to the
// registry it names has been answered, or as that first request.
func (c *signInClient) Do(req *http.Request) (*http.Response, error) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	first := firstContactWith(host)
	lead, err := first.enter(req.Context())
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	if lead {
		defer first.finish()
	}
	return c.auth.Do(req)
}

// firstContacts holds, keyed by registryhost.Normalize, whether this process has
// sent that registry a request yet (firstContact), so two spellings of one
// registry share one wait as they share one slow-down allowance.
var firstContacts sync.Map

// firstContact is whether this process has sent one registry its first request,
// and whether that request has been answered.
type firstContact struct {
	started atomic.Bool
	done    chan struct{}
}

// firstContactWith returns the first-contact record of the registry host names,
// under any spelling of it.
func firstContactWith(host string) *firstContact {
	registry := registryhost.Normalize(host)
	if f, ok := firstContacts.Load(registry); ok {
		return f.(*firstContact)
	}
	f, _ := firstContacts.LoadOrStore(registry, &firstContact{done: make(chan struct{})})
	return f.(*firstContact)
}

// enter reports whether the request entering is the registry's first (lead), which
// goes at once. Any other request waits until the first has been answered, or
// returns its own context's error where that ends first.
func (f *firstContact) enter(ctx context.Context) (lead bool, err error) {
	if f.started.CompareAndSwap(false, true) {
		return true, nil
	}
	select {
	case <-f.done:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// finish lets every request waiting on the first go. Only the first calls it.
func (f *firstContact) finish() {
	close(f.done)
}

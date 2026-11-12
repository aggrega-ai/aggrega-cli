// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci

import (
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
)

// AuthClientOf returns the authenticating client a remote target this package
// built sends its requests through, and false for a client this package did not
// build. It exists for tests that read the credential function the client was
// wired with.
func AuthClientOf(c remote.Client) (*auth.Client, bool) {
	s, ok := c.(*signInClient)
	if !ok {
		return nil, false
	}
	return s.auth, true
}

// ForgetRegistrySignIn forgets everything this process kept about the registry host
// names — that it was contacted, that it challenged a request, and what its last
// sign-in obtained — so a test starts from a registry this process has never
// reached even where its loopback port was used by an earlier test.
func ForgetRegistrySignIn(host string) {
	registry := registryhost.Normalize(host)
	challengedHosts.Delete(registry)
	firstContacts.Delete(registry)
	signInCaches.Delete(registry)
}

// SignInHeldFor reports whether what this process keeps for the registry host
// names was obtained by a sign-in made with cred.
func SignInHeldFor(host string, cred auth.Credential) bool {
	registry := registryhost.Normalize(host)
	held, ok := signInCaches.Load(registry)
	if !ok {
		return false
	}
	h := held.(*hostSignIn)
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cache != nil && h.id == identityOf(registry, cred)
}

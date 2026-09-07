// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"context"
	"fmt"

	"oras.land/oras-go/v2/registry/remote/credentials"
)

// LogoutOptions is what a credential removal decides.
//
// Registry is the host whose stored credential to remove, written as the
// credential store keys it — which is the same host a registry entry is
// addressed by, not a friendly alias. Nothing resolves an alias here, because
// the store holds no alias: it is keyed by the address a client authenticates
// to.
//
// Store is the credential store to remove from, and a nil value opens the
// shared one. It is a parameter rather than a default so a caller can address
// an isolated store, which is what keeps a test off the machine's own keychain.
type LogoutOptions struct {
	Registry string
	Store    credentials.Store
}

// LogoutResult is what a removal addressed.
//
// It carries the host and nothing else, and the absence is the decision. The
// store reports only whether the removal failed, so establishing whether an
// entry had been there would take a second read of a machine-wide keychain that
// no caller asked for — the same side effect AuthResolver's own contract avoids
// by opening no store for a surface that does not report the column. A caller
// that wants to know what the store holds asks for the registry listing, which
// reports presence as the answer to its own question.
type LogoutResult struct {
	Registry string `json:"registry"`
}

// Logout removes the stored credential for a registry from the credential
// store, and reports the host it addressed.
//
// Removal is idempotent: a host the store holds no entry for is removed
// successfully and the store is left byte-identical, because there was nothing
// to rewrite. That is the honest outcome rather than a refusal — a caller asking
// for a credential to be gone has got what it asked for, and reporting a failure
// would make "there is no credential for this host" indistinguishable from "the
// keychain could not be reached".
//
// The credential itself is never read. The store is asked to delete the host's
// entry and nothing inspects, measures, retains or reports what that entry held,
// so neither the result nor any error this returns can carry credential
// material.
//
// The store is machine-wide, so this reaches beyond any one workspace. There is
// no workspace parameter and no configured-registry check: a credential is keyed
// by the address a client authenticates to rather than by a project, so a host
// no workspace configures may still have an entry — and refusing to remove one
// would leave a credential unremovable through the very operation whose whole
// purpose is removing it.
func Logout(ctx context.Context, opts LogoutOptions) (LogoutResult, error) {
	var res LogoutResult
	if opts.Registry == "" {
		return res, fmt.Errorf("aggrega logout: registry argument required")
	}
	store := opts.Store
	if store == nil {
		s, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
		if err != nil {
			return res, fmt.Errorf("aggrega logout: credential store: %w", err)
		}
		store = s
	}
	if err := store.Delete(ctx, opts.Registry); err != nil {
		return res, fmt.Errorf("aggrega logout: remove credential for %s: %w", opts.Registry, err)
	}
	res.Registry = opts.Registry
	return res, nil
}

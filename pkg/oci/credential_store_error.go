// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"context"
	"fmt"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"

	"github.com/aggrega-ai/aggrega-cli/internal/credentialfailure"
)

// storeCredential is the credential function the registry client authenticates
// with. It reads the entry for the host the registry asked about, exactly as the
// store's own credential function does, and reports a failure to read it in the
// fixed sentence credentialfailure.Unreadable composes, naming the host and the
// store's configuration file — never in the store's own words, which can quote
// the entry.
//
// A host with no entry is not a failure: the store answers it with an empty
// credential and no error, and that answer passes through unchanged.
func storeCredential(store *credentials.DynamicStore) auth.CredentialFunc {
	read := credentials.Credential(store)
	configPath := store.ConfigPath()
	return func(ctx context.Context, hostport string) (auth.Credential, error) {
		cred, err := read(ctx, hostport)
		if err != nil {
			return auth.EmptyCredential, fmt.Errorf("oci: %w", credentialfailure.Unreadable(hostport, configPath, err))
		}
		return cred, nil
	}
}

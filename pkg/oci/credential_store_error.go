// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"context"
	"errors"
	"fmt"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

// credentialStoreError is a credential-store failure as the registry client
// reports it: a fixed sentence naming the registry and the credential file, in
// place of the store's own text.
//
// The store's own text is not safe to print. Reading a malformed entry, the
// store quotes the entry's decoded contents — the shape of a pasted token — and
// reading a file that is not valid JSON, it quotes a character of the file. Both
// would reach a terminal, and an editor's output panel, that never asked to see
// the credential.
//
// The store's failure is kept for matching rather than for printing, and that
// decides the methods. Is delegates to it, so a condition the store raised — a
// context already cancelled or expired when a credential helper was to be run, a
// credential helper that is not installed, a file the process may not open —
// matches as it did before the sentence replaced the text. There is deliberately no Unwrap: every walk that renders a chain one frame
// at a time prints each frame's own text, and the frames beneath this one are the
// store's. There is deliberately no As either: a match would hand the caller one
// of those frames, whose text is exactly what this type withholds.
type credentialStoreError struct {
	message string
	cause   error
}

// Error returns the fixed sentence. It never includes the store's text.
func (e *credentialStoreError) Error() string { return e.message }

// Is reports whether the store's own failure is, or wraps, target.
func (e *credentialStoreError) Is(target error) bool { return errors.Is(e.cause, target) }

// unreadableCredentialFile reports that the credential store could not be opened
// for registryHost. The store never came into being, so no resolved file path is
// available from it; the sentence names the file by the location rule the store
// follows instead.
func unreadableCredentialFile(registryHost string, cause error) error {
	return &credentialStoreError{
		message: fmt.Sprintf("oci: credential store: the credential for %q cannot be read, because the "+
			"credential file cannot be read: config.json in the directory DOCKER_CONFIG names, or "+
			"~/.docker/config.json when that variable is unset. What the file holds is not shown; "+
			"check that the file can be read and is valid JSON", registryHost),
		cause: cause,
	}
}

// storeCredential is the credential function the registry client authenticates
// with. It reads the entry for the host the registry asked about, exactly as the
// store's own credential function does, and reports a failure to read it as a
// credentialStoreError naming the host and the store's configuration file.
//
// A host with no entry is not a failure: the store answers it with an empty
// credential and no error, and that answer passes through unchanged.
func storeCredential(store *credentials.DynamicStore) auth.CredentialFunc {
	read := credentials.Credential(store)
	configPath := store.ConfigPath()
	return func(ctx context.Context, hostport string) (auth.Credential, error) {
		cred, err := read(ctx, hostport)
		if err != nil {
			return auth.EmptyCredential, &credentialStoreError{
				message: fmt.Sprintf("oci: credential store: the credential for %q cannot be read from %s "+
					"or the credential helper it names. What the entry holds is not shown; check the entry "+
					"for that registry, or replace it with `aggrega login %s`", hostport, configPath, hostport),
				cause: err,
			}
		}
		return cred, nil
	}
}

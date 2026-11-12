// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci

import (
	"context"

	"oras.land/oras-go/v2"
)

// PullPublishedEntity fetches the bytes a registry coordinate publishes its
// entity in, verified against the digest the artifact's own manifest records for
// them, and returns them as the registry served them.
//
// # Which bytes those are is read off the manifest, not supplied by the caller
//
// Every kind but one publishes its entity as the manifest's content layer. A
// Plugin publishes as a single manifest whose config blob is the entity and whose
// layer is the executable, so reading its content layer would hand back the
// executable under the entity's name. The two shapes are told apart by the
// manifest the reference resolves to, through IsPluginManifest, because a reader
// holding only a reference has no kind to pass in: the kind is written inside the
// entity it is about to fetch.
//
// The manifest is fetched once and both halves read the descriptor they want from
// it, through the same selection and the same verify Pull and FetchPluginEntity
// use, so this adds no third copy of either.
//
// # What it does not do
//
// It parses nothing and writes nothing. A content layer may be an archive, so a
// caller that wants the entity document passes Bytes through EntityYAML, which is
// where an archive is told from a document. Nothing is cached and nothing is
// pinned: the verify is against the manifest the registry served, not against any
// record kept beforehand, so it establishes that the bytes are the ones the
// manifest names and says nothing about who published them.
//
// Resolve-error mapping is Pull's: a reference nothing is published under wraps
// ErrFetchNotFound, and a refused or absent credential wraps ErrFetchUnauthorized.
func PullPublishedEntity(ctx context.Context, target oras.ReadOnlyTarget, reference string) (*PullResult, error) {
	manifestDesc, manifestBytes, err := fetchManifest(ctx, target, reference)
	if err != nil {
		return nil, err
	}
	if IsPluginManifest(manifestBytes) {
		return pluginEntityFromManifest(ctx, target, reference, manifestDesc, manifestBytes)
	}
	return contentFromManifest(ctx, target, reference, manifestDesc, manifestBytes)
}

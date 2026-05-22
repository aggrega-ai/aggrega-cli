// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

// PullResult holds the bytes pulled from a target plus the OCI
// manifest digest and the mediatype the manifest declared.
type PullResult struct {
	Bytes     []byte
	Digest    string
	MediaType string
}

// Pull fetches the artifact tagged `tag` from `target` and returns
// the bytes, the manifest digest, and the artifact's mediatype.
// The target may be a remote.Repository (production) or any in-
// memory CAS implementing oras.ReadOnlyTarget (tests).
//
// Error mapping:
//   - target reports "not found" → wrapped ErrFetchNotFound
//   - content digest mismatch between manifest and fetched bytes →
//     wrapped ErrFetchIntegrity
//
// Wrapping of oras-go's raw transport errors (401/403 → unauthorized,
// DNS/TCP failures → network unreachable) is intentionally minimal
// at this moment; the richer per-condition mapping arrives at
// the follow-up implementation that wires both fetch axes into the
// diagnostic surface uniformly. Until then, callers can still match
// against the sentinels in errors.go via errors.Is once they wrap
// their own diagnostic context.
func Pull(ctx context.Context, target oras.ReadOnlyTarget, tag string) (*PullResult, error) {
	manifestDesc, err := target.Resolve(ctx, tag)
	if err != nil {
		if errors.Is(err, errdef.ErrNotFound) {
			return nil, fmt.Errorf("oci: %q: %w", tag, ErrFetchNotFound)
		}
		return nil, fmt.Errorf("oci: resolve %q: %w", tag, err)
	}

	manifestRC, err := target.Fetch(ctx, manifestDesc)
	if err != nil {
		return nil, fmt.Errorf("oci: fetch manifest %q: %w", tag, err)
	}
	manifestBytes, err := io.ReadAll(manifestRC)
	_ = manifestRC.Close()
	if err != nil {
		return nil, fmt.Errorf("oci: read manifest %q: %w", tag, err)
	}

	contentDesc, mediaType, err := descriptorFromManifest(manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("oci: parse manifest %q: %w", tag, err)
	}

	contentRC, err := target.Fetch(ctx, contentDesc)
	if err != nil {
		if errors.Is(err, errdef.ErrNotFound) {
			return nil, fmt.Errorf("oci: content for %q: %w", tag, ErrFetchNotFound)
		}
		return nil, fmt.Errorf("oci: fetch content %q: %w", tag, err)
	}
	content, err := io.ReadAll(contentRC)
	_ = contentRC.Close()
	if err != nil {
		return nil, fmt.Errorf("oci: read content %q: %w", tag, err)
	}

	if got := computeDigest(content); got != contentDesc.Digest.String() {
		return nil, fmt.Errorf("oci: %q digest %s != manifest %s: %w",
			tag, got, contentDesc.Digest.String(), ErrFetchIntegrity)
	}

	return &PullResult{
		Bytes:     content,
		Digest:    manifestDesc.Digest.String(),
		MediaType: mediaType,
	}, nil
}

// Push uploads `content` with `mediaType` to `target`, builds an OCI
// manifest pointing at it, tags the manifest as `tag`, and returns
// the manifest digest. The target may be a remote.Repository
// (production) or any in-memory CAS implementing oras.Target
// (tests).
//
// Per Aggrega's deterministic-output discipline, callers SHOULD
// supply bundle bytes pre-canonicalised (sorted file entries in
// tar archives; zero timestamps; gzip default compression). Push
// itself adds no canonicalisation; it trusts its input.
func Push(ctx context.Context, target oras.Target, tag string, mediaType string, content []byte) (string, error) {
	contentDesc, err := oras.PushBytes(ctx, target, mediaType, content)
	if err != nil {
		return "", fmt.Errorf("oci: push content: %w", err)
	}

	manifestDesc, err := oras.PackManifest(
		ctx,
		target,
		oras.PackManifestVersion1_1,
		mediaType,
		oras.PackManifestOptions{
			Layers: []ocispec.Descriptor{contentDesc},
		},
	)
	if err != nil {
		return "", fmt.Errorf("oci: pack manifest: %w", err)
	}

	if err := target.Tag(ctx, manifestDesc, tag); err != nil {
		return "", fmt.Errorf("oci: tag %q: %w", tag, err)
	}

	return manifestDesc.Digest.String(), nil
}

// NewRemoteRepository constructs an authenticated remote.Repository
// for the given full repository reference (e.g.
// "hub.aggrega.ai/component/core/api-server"). Authentication
// delegates to oras-go/v2's standard credential mechanisms: Docker,
// containers-storage, and Helm registry configs are consulted in
// priority order, with credential-helper delegation via credsStore
// and credHelpers honoured.
//
// The returned repository targets the full reference path; the
// `tag` argument to Pull / Push selects the specific version within
// that repository.
func NewRemoteRepository(reference string) (*remote.Repository, error) {
	repo, err := remote.NewRepository(reference)
	if err != nil {
		return nil, fmt.Errorf("oci: parse reference %q: %w", reference, err)
	}

	store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err != nil {
		return nil, fmt.Errorf("oci: credential store: %w", err)
	}

	repo.Client = &auth.Client{
		Credential: credentials.Credential(store),
	}
	return repo, nil
}

// descriptorFromManifest parses an OCI image manifest's JSON bytes
// and returns the descriptor for the manifest's single artifact
// layer plus the artifact's mediatype. The expected manifest shape
// is the one Push produces via oras.PackManifest: one layer entry;
// mediatype on either the manifest's artifactType field or the
// layer's mediatype field.
func descriptorFromManifest(manifestBytes []byte) (ocispec.Descriptor, string, error) {
	var manifest struct {
		ArtifactType string               `json:"artifactType"`
		Config       ocispec.Descriptor   `json:"config"`
		Layers       []ocispec.Descriptor `json:"layers"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return ocispec.Descriptor{}, "", err
	}
	if len(manifest.Layers) == 0 {
		return ocispec.Descriptor{}, "", errors.New("oci: manifest carries no layers")
	}
	desc := manifest.Layers[0]
	mediaType := manifest.ArtifactType
	if mediaType == "" {
		mediaType = desc.MediaType
	}
	return desc, mediaType, nil
}

// computeDigest returns the OCI-format SHA-256 digest of content
// ("sha256:<hex>") for byte-identity verification.
func computeDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

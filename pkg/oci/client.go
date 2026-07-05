// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/Masterminds/semver/v3"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	"oras.land/oras-go/v2/registry/remote/errcode"
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
//   - authentication required, rejected, or no stored credential for
//     the registry → wrapped ErrFetchUnauthorized
//   - content digest mismatch between manifest and fetched bytes →
//     wrapped ErrFetchIntegrity
//
// Remaining raw transport errors (DNS/TCP failures → network
// unreachable) pass through unmapped; callers can still match the
// sentinels in errors.go via errors.Is once they wrap their own
// diagnostic context.
func Pull(ctx context.Context, target oras.ReadOnlyTarget, tag string) (*PullResult, error) {
	manifestDesc, err := target.Resolve(ctx, tag)
	if err != nil {
		if errors.Is(err, errdef.ErrNotFound) {
			return nil, fmt.Errorf("oci: %q: %w", tag, ErrFetchNotFound)
		}
		if isUnauthorized(err) {
			return nil, fmt.Errorf("oci: resolve %q: %v: %w", tag, err, ErrFetchUnauthorized)
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

	if got := digest.ContentDigest(content); got != contentDesc.Digest.String() {
		return nil, fmt.Errorf("oci: %q digest %s != manifest %s: %w",
			tag, got, contentDesc.Digest.String(), ErrFetchIntegrity)
	}

	return &PullResult{
		Bytes:     content,
		Digest:    manifestDesc.Digest.String(),
		MediaType: mediaType,
	}, nil
}

// epochCreated is the fixed value Push pins into every manifest's
// org.opencontainers.image.created annotation. oras stamps that
// annotation with the current wall-clock time unless a value is
// supplied, which would make byte-identical content produce a
// different manifest digest on every push. Pinning it to the zero
// epoch (the reproducible-build convention) makes the manifest digest
// a pure function of the content — the precondition the publish-time
// immutability guard relies on to recognise a re-push of identical
// content. This extends the Deterministic Output discipline from the
// content blob (which callers pre-canonicalise) to the manifest.
const epochCreated = "1970-01-01T00:00:00Z"

// packArtifact uploads `content` and packs a deterministic OCI manifest
// referencing it into `target`, returning the manifest descriptor. It
// does NOT tag. Push and ManifestDigest share it so the digest a guard
// previews against an in-memory store is byte-identical to the digest a
// real push produces.
func packArtifact(ctx context.Context, target oras.Target, mediaType string, content []byte) (ocispec.Descriptor, error) {
	contentDesc, err := oras.PushBytes(ctx, target, mediaType, content)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("oci: push content: %w", err)
	}
	manifestDesc, err := oras.PackManifest(
		ctx,
		target,
		oras.PackManifestVersion1_1,
		mediaType,
		oras.PackManifestOptions{
			Layers:              []ocispec.Descriptor{contentDesc},
			ManifestAnnotations: map[string]string{ocispec.AnnotationCreated: epochCreated},
		},
	)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("oci: pack manifest: %w", err)
	}
	return manifestDesc, nil
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
// adds no content canonicalisation; it trusts its input. It does pin
// the manifest's creation annotation, so byte-identical content always
// produces a byte-identical manifest digest.
func Push(ctx context.Context, target oras.Target, tag string, mediaType string, content []byte) (string, error) {
	manifestDesc, err := packArtifact(ctx, target, mediaType, content)
	if err != nil {
		return "", err
	}
	if err := target.Tag(ctx, manifestDesc, tag); err != nil {
		return "", fmt.Errorf("oci: tag %q: %w", tag, err)
	}
	return manifestDesc.Digest.String(), nil
}

// ManifestDigest computes, without contacting any remote, the manifest
// digest Push would produce for `content` of `mediaType`. It packs into
// an in-memory store and returns the descriptor digest. Because the
// manifest's creation annotation is pinned, the result is deterministic
// and equal to the digest a real Push of the same content yields — so a
// caller can preview the digest a push would land before deciding
// whether that push would overwrite an existing tag.
func ManifestDigest(ctx context.Context, mediaType string, content []byte) (string, error) {
	manifestDesc, err := packArtifact(ctx, memory.New(), mediaType, content)
	if err != nil {
		return "", err
	}
	return manifestDesc.Digest.String(), nil
}

// Resolve reports the manifest digest the target currently has for
// `tag`. It is the read-only pre-push HEAD: exists is false (with a nil
// error and empty digest) when the tag is simply absent, so callers can
// distinguish "no such tag, safe to publish" from a transport or
// authentication failure (non-nil error). The target may be a
// remote.Repository or any oras.ReadOnlyTarget.
func Resolve(ctx context.Context, target oras.ReadOnlyTarget, tag string) (digest string, exists bool, err error) {
	desc, err := target.Resolve(ctx, tag)
	if err != nil {
		if errors.Is(err, errdef.ErrNotFound) {
			return "", false, nil
		}
		if isUnauthorized(err) {
			return "", false, fmt.Errorf("oci: resolve %q: %v: %w", tag, err, ErrFetchUnauthorized)
		}
		return "", false, fmt.Errorf("oci: resolve %q: %w", tag, err)
	}
	return desc.Digest.String(), true, nil
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
//
// A reference whose registry host is loopback (localhost, 127.0.0.0/8,
// ::1 — with or without a port) is contacted over plain HTTP, so a
// local development registry works without TLS provisioning. The
// detection is host-based and automatic: plaintext can only ever go to
// the local machine, and every non-loopback host keeps HTTPS exactly
// as before. There is no configuration surface for this.
func NewRemoteRepository(reference string) (*remote.Repository, error) {
	repo, err := remote.NewRepository(reference)
	if err != nil {
		return nil, fmt.Errorf("oci: parse reference %q: %w", reference, err)
	}
	repo.PlainHTTP = registryhost.IsLoopback(repo.Reference.Registry)

	store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err != nil {
		return nil, fmt.Errorf("oci: credential store: %w", err)
	}

	repo.Client = &auth.Client{
		Credential: credentials.Credential(store),
	}
	return repo, nil
}

// VerifyCredential checks a username/password against a registry by pinging
// its /v2/ endpoint with that specific credential, before anything is stored.
// It is distinct from NewRemoteRepository, which reads whatever the credential
// store holds: VerifyCredential authenticates with the credential passed in, so
// `aggrega login` can fail loudly on a typo at login time rather than silently
// at the first push. A registry host that addresses the local machine is pinged
// over plain HTTP (mirroring NewRemoteRepository's loopback handling).
//
// A 401/403 maps to ErrFetchUnauthorized (wrong credential); other failures
// (DNS, TCP, TLS) pass through wrapped with the registry host for diagnosis.
func VerifyCredential(ctx context.Context, registryHost string, cred auth.Credential) error {
	reg, err := remote.NewRegistry(registryHost)
	if err != nil {
		return fmt.Errorf("oci: parse registry %q: %w", registryHost, err)
	}
	reg.PlainHTTP = registryhost.IsLoopback(reg.Reference.Registry)
	reg.Client = &auth.Client{
		Credential: auth.StaticCredential(reg.Reference.Registry, cred),
	}
	if err := reg.Ping(ctx); err != nil {
		if isUnauthorized(err) {
			return fmt.Errorf("oci: verify credential for %q: %v: %w", registryHost, err, ErrFetchUnauthorized)
		}
		return fmt.Errorf("oci: verify credential for %q: %w", registryHost, err)
	}
	return nil
}

// isUnauthorized reports whether a raw oras-go error represents an
// authentication failure: the registry answered 401/403, or the
// credential store holds no entry for a registry that demands basic
// auth. Both conditions have the same operator remediation (log in to
// the registry), so both map to ErrFetchUnauthorized.
func isUnauthorized(err error) bool {
	if errors.Is(err, auth.ErrBasicCredentialNotFound) {
		return true
	}
	var ec *errcode.ErrorResponse
	if errors.As(err, &ec) {
		return ec.StatusCode == http.StatusUnauthorized || ec.StatusCode == http.StatusForbidden
	}
	return false
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

// ListTags returns every tag the registry has for the target, fully
// materialized via registry.Tags paged iteration. For in-memory test
// targets (memory.Store) the returned set follows the store's
// internal order; for remote.Repository targets pagination is
// transparent. The target must implement registry.TagLister; a
// typed error names the missing capability when it does not.
//
// Returns an empty slice (not nil) when the target has zero tags.
func ListTags(ctx context.Context, target oras.ReadOnlyTarget) ([]string, error) {
	lister, ok := target.(registry.TagLister)
	if !ok {
		return nil, fmt.Errorf("oci: target does not support tag listing: %T", target)
	}
	tags, err := registry.Tags(ctx, lister)
	if err != nil {
		return nil, fmt.Errorf("oci: list tags: %w", err)
	}
	if tags == nil {
		return []string{}, nil
	}
	return tags, nil
}

// strictSemverTag parses a registry tag as strict SemVer 2.0.0 — the same
// grammar entity schemas mandate for metadata.version: MAJOR.MINOR.PATCH with
// an optional -prerelease and +build, no "v" prefix, and exactly three numeric
// parts. It reports the parsed version and whether the tag conformed.
// Non-conformant tags (a "v" prefix, a four-part "1.2.3.4", a bare "latest")
// report ok=false. Every "is this a usable version tag" decision derives from
// this one parse, so the selection and presence checks cannot disagree about
// what counts as a version.
func strictSemverTag(tag string) (*semver.Version, bool) {
	v, err := semver.StrictNewVersion(tag)
	if err != nil {
		return nil, false
	}
	return v, true
}

// SelectLatestVersion returns the highest tag that conforms to strict SemVer
// 2.0.0 (MAJOR.MINOR.PATCH, optional -prerelease / +build, no "v" prefix,
// exactly three numeric parts). Non-conformant tags — a "v" prefix, a four-part
// "1.2.3.4", a bare "latest" — are skipped and never selected. When
// includePreRelease is false, tags carrying a pre-release component are also
// skipped. Returns an empty string when no qualifying tag is present; the
// function never fails (all skip-or-include decisions are made silently).
func SelectLatestVersion(tags []string, includePreRelease bool) string {
	var highest *semver.Version
	var highestOrig string
	for _, t := range tags {
		v, ok := strictSemverTag(t)
		if !ok {
			continue
		}
		if !includePreRelease && v.Prerelease() != "" {
			continue
		}
		if highest == nil || v.GreaterThan(highest) {
			highest = v
			highestOrig = t
		}
	}
	return highestOrig
}

// HasAnySemverTag reports whether at least one tag conforms to strict SemVer
// 2.0.0 (regardless of pre-release status). It derives conformance from the
// same parse as SelectLatestVersion, so the two never disagree about what
// counts as a version. Used by the CLI to distinguish "no previous version
// exists" (true first publication) from "previous versions exist but all are
// pre-release" (different diagnostic).
func HasAnySemverTag(tags []string) bool {
	for _, t := range tags {
		if _, ok := strictSemverTag(t); ok {
			return true
		}
	}
	return false
}

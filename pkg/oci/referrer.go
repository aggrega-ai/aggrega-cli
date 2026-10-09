// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"context"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry"
)

// AttachReferrer attaches `payload` to the artifact identified by
// `subjectRef` as an OCI referrer of type `artifactType`. It packs a
// referrer manifest whose Subject is the subject artifact's manifest
// descriptor, so the registry's Referrers API (and an in-memory store's
// predecessor graph) reports the referrer when queried for the subject.
//
// The subject artifact is NOT re-pushed, re-tagged, or mutated: the
// referrer is a sibling manifest pointing back at the existing subject
// digest. Attaching a signature to a published artifact is therefore
// immutability-safe — the artifact's own bytes and tags are untouched.
//
// subjectRef is any reference the target resolves to the subject's
// manifest descriptor — a version tag or a manifest digest against a
// remote registry, or a tag against an in-memory store. The target may
// be a remote.Repository or any oras.Target (tests use memory.New()).
//
// Like Push, the referrer manifest pins its creation annotation, so a
// re-attach of byte-identical payload to the same subject is
// deterministic. Its two writes retry a transient registry failure as
// AttachReferrerToDescriptor describes; resolving the subject is a read and is
// not retried.
func AttachReferrer(ctx context.Context, target oras.Target, subjectRef string, artifactType string, payload []byte) (ocispec.Descriptor, error) {
	subjectDesc, err := target.Resolve(ctx, subjectRef)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("oci: resolve subject %q: %w", subjectRef, err)
	}
	return AttachReferrerToDescriptor(ctx, target, subjectDesc, artifactType, payload)
}

// AttachReferrerToDescriptor attaches `payload` as an OCI referrer of the
// subject identified by `subjectDesc`, the subject's already-resolved
// manifest descriptor. It is the descriptor-taking core of AttachReferrer:
// AttachReferrer resolves a reference string to a descriptor and delegates
// here, so the referrer-packing logic has one home.
//
// The descriptor entry point serves the case where the subject is itself an
// untagged referrer — a referrer of a referrer — whose manifest digest a
// tag-based Resolve cannot recover (an in-memory store resolves only tagged
// descriptors; a registry's Referrers API lists referrers rather than
// resolving them by digest). The caller holds the subject's descriptor from
// the AttachReferrer that produced it and passes it straight through, so a
// signature can be attached to a deprecation marker or any other referrer.
//
// The subject artifact is NOT re-pushed, re-tagged, or mutated: the referrer
// is a sibling manifest pointing back at the existing subject digest.
//
// The payload layer is titled from `artifactType`, so a standard registry client
// pulling the referrer writes the payload to a named file — a signature bundle
// to signature.json, a deprecation marker to deprecation.json (see
// contentTitle).
//
// The payload upload and the referrer manifest each retry a transient registry
// failure (see IsTransientRegistryError) up to three attempts with a short
// backoff, each attempt a new upload of the same bytes. Because the referrer
// manifest pins its creation annotation, a retried attach packs the same
// manifest, so an attempt whose response was lost after it landed leaves one
// referrer, not two. A write still failing after its last attempt fails the
// attach, and the error names the step: the payload or the referrer manifest.
func AttachReferrerToDescriptor(ctx context.Context, target oras.Target, subjectDesc ocispec.Descriptor, artifactType string, payload []byte) (ocispec.Descriptor, error) {
	payloadDesc, err := pushBlob(ctx, target, artifactType, payload)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("oci: push referrer payload: %w", err)
	}

	manifestDesc, err := packManifest(ctx, target, artifactType, oras.PackManifestOptions{
		Subject: &subjectDesc,
		Layers:  []ocispec.Descriptor{withTitle(payloadDesc, contentTitle(artifactType))},
	})
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("oci: pack referrer manifest: %w", err)
	}
	return manifestDesc, nil
}

// ListReferrers returns the descriptors of every referrer of
// `subjectRef` whose artifactType equals `artifactType` (pass an empty
// string to match all). It works uniformly against a remote.Repository
// (via the registry Referrers API) and an in-memory store (via the
// predecessor graph), so the same call discovers signatures in production
// and in tests.
//
// subjectRef is any reference the target resolves to the subject's
// manifest descriptor (a version tag or a manifest digest).
//
// The two ways of finding no referrer are reported differently, because a
// caller acting on an absent referrer needs to tell them apart:
//
//   - The lookup succeeded and the subject has no matching referrer — an
//     empty slice (not nil) and a nil error. The question was answered.
//   - The lookup could not be completed at all — an error wrapping
//     [ErrReferrerDiscoveryUnavailable], whatever prevented it: a target that
//     cannot serve discovery, a subject that could not be located, or a
//     failed discovery request. The question went unanswered, so the absence
//     of a referrer here is not evidence that none is attached.
//
// The underlying cause stays in the returned error's chain alongside the
// sentinel, so a caller may match either.
func ListReferrers(ctx context.Context, target oras.ReadOnlyTarget, subjectRef string, artifactType string) ([]ocispec.Descriptor, error) {
	store, ok := target.(content.ReadOnlyGraphStorage)
	if !ok {
		return nil, fmt.Errorf("oci: target does not support referrer discovery: %T: %w", target, ErrReferrerDiscoveryUnavailable())
	}
	subjectDesc, err := target.Resolve(ctx, subjectRef)
	if err != nil {
		return nil, fmt.Errorf("oci: resolve subject %q: %w: %w", subjectRef, err, ErrReferrerDiscoveryUnavailable())
	}
	refs, err := registry.Referrers(ctx, store, subjectDesc, artifactType)
	if err != nil {
		return nil, fmt.Errorf("oci: list referrers of %q: %w: %w", subjectRef, err, ErrReferrerDiscoveryUnavailable())
	}
	if refs == nil {
		return []ocispec.Descriptor{}, nil
	}
	return refs, nil
}

// FetchReferrerPayload fetches the layer payload of a referrer manifest
// (e.g. a signature bundle) given the referrer's manifest descriptor as
// returned by ListReferrers. It returns the bytes of the referrer's
// single artifact layer — the inverse of the payload AttachReferrer
// packed.
func FetchReferrerPayload(ctx context.Context, target oras.ReadOnlyTarget, referrer ocispec.Descriptor) ([]byte, error) {
	manifestBytes, err := content.FetchAll(ctx, target, referrer)
	if err != nil {
		return nil, fmt.Errorf("oci: fetch referrer manifest: %w", err)
	}
	payloadDesc, _, err := descriptorFromManifest(manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("oci: parse referrer manifest: %w", err)
	}
	payload, err := content.FetchAll(ctx, target, payloadDesc)
	if err != nil {
		return nil, fmt.Errorf("oci: fetch referrer payload: %w", err)
	}
	return payload, nil
}

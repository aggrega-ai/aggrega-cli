// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

const sigArtifactType = "application/vnd.dev.sigstore.bundle.v0.3+json"

// TestAttachReferrerAttachesToSubject proves a referrer attaches to the
// resolved subject and is discoverable via ListReferrers — the plumbing
// that delivers a signature bundle to a published artifact, proven
// against an in-memory store with no crypto and no network.
func TestAttachReferrerAttachesToSubject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	artifact := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/api-server\n  version: 1.0.0\n")
	if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, artifact, nil); err != nil {
		t.Fatalf("Push: %v", err)
	}

	bundle := []byte(`{"signature":"abc"}`)
	if _, err := oci.AttachReferrer(ctx, store, "1.0.0", sigArtifactType, bundle); err != nil {
		t.Fatalf("AttachReferrer: %v", err)
	}

	refs, err := oci.ListReferrers(ctx, store, "1.0.0", sigArtifactType)
	if err != nil {
		t.Fatalf("ListReferrers: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d referrers, want 1", len(refs))
	}
	if refs[0].ArtifactType != sigArtifactType {
		t.Errorf("referrer artifactType = %q, want %q", refs[0].ArtifactType, sigArtifactType)
	}

	// The bundle payload must round-trip back out of the referrer.
	got, err := oci.FetchReferrerPayload(ctx, store, refs[0])
	if err != nil {
		t.Fatalf("FetchReferrerPayload: %v", err)
	}
	if !bytes.Equal(got, bundle) {
		t.Errorf("payload = %q, want %q", got, bundle)
	}
}

// TestListReferrersFiltersByArtifactType proves the artifactType filter
// selects only matching referrers, so a verifier asking for signatures
// does not see unrelated referrers.
func TestListReferrersFiltersByArtifactType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	if _, err := oci.Push(ctx, store, "2.0.0", oci.MediaTypeComponent, []byte("content"), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}

	if _, err := oci.AttachReferrer(ctx, store, "2.0.0", sigArtifactType, []byte("sig")); err != nil {
		t.Fatalf("AttachReferrer (signature): %v", err)
	}
	if _, err := oci.AttachReferrer(ctx, store, "2.0.0", "application/vnd.example.other", []byte("other")); err != nil {
		t.Fatalf("AttachReferrer (other): %v", err)
	}

	refs, err := oci.ListReferrers(ctx, store, "2.0.0", sigArtifactType)
	if err != nil {
		t.Fatalf("ListReferrers: %v", err)
	}
	if len(refs) != 1 || refs[0].ArtifactType != sigArtifactType {
		t.Fatalf("filtered list = %+v, want exactly one signature referrer", refs)
	}
}

// TestListReferrersEmpty proves an artifact with no referrers yields an
// empty (non-nil) slice — the "unsigned" signal a verifier reads.
func TestListReferrersEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	if _, err := oci.Push(ctx, store, "3.0.0", oci.MediaTypeComponent, []byte("unsigned"), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	refs, err := oci.ListReferrers(ctx, store, "3.0.0", sigArtifactType)
	if err != nil {
		t.Fatalf("ListReferrers: %v", err)
	}
	if refs == nil {
		t.Fatal("ListReferrers returned nil; want empty slice")
	}
	if len(refs) != 0 {
		t.Fatalf("got %d referrers for an unsigned artifact, want 0", len(refs))
	}
	// The discriminating half: an ANSWERED lookup that found nothing must not
	// carry the unavailable sentinel, or a caller could not tell "none is
	// attached" from "nobody could say".
	if errors.Is(err, oci.ErrReferrerDiscoveryUnavailable()) {
		t.Error("an answered lookup that found no referrer must not report discovery as unavailable")
	}
}

// bareTarget satisfies oras.ReadOnlyTarget without satisfying
// content.ReadOnlyGraphStorage: it can fetch and resolve, but it holds no
// predecessor graph, so a referrer lookup cannot be served against it.
type bareTarget struct{ inner *memory.Store }

func (b bareTarget) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	return b.inner.Fetch(ctx, target)
}

func (b bareTarget) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	return b.inner.Exists(ctx, target)
}

func (b bareTarget) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	return b.inner.Resolve(ctx, ref)
}

// brokenGraph serves the predecessor graph by refusing to: it stands for a
// registry that accepts the discovery request and fails it.
type brokenGraph struct{ *memory.Store }

func (brokenGraph) Predecessors(context.Context, ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	return nil, errors.New("referrers unavailable")
}

// TestListReferrersUnanswerableCarriesSentinel proves every way the lookup can
// fail to complete reports the same condition, so a caller reads "this could
// not be determined" from the error rather than inferring it from the text.
//
// The three ways are structurally distinct — a target that cannot serve
// discovery at all, a subject that cannot be located, and a discovery request
// that fails — and a caller acting on an absent referrer must treat all three
// alike and none of them as "no referrer is attached".
func TestListReferrersUnanswerableCarriesSentinel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := memory.New()
	if _, err := oci.Push(ctx, store, "4.0.0", oci.MediaTypeComponent, []byte("subject"), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}

	cases := []struct {
		name       string
		target     oras.ReadOnlyTarget
		subjectRef string
	}{
		{"target cannot serve discovery", bareTarget{inner: store}, "4.0.0"},
		{"subject cannot be located", store, "5.0.0"},
		{"discovery request fails", brokenGraph{Store: store}, "4.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			refs, err := oci.ListReferrers(ctx, tc.target, tc.subjectRef, sigArtifactType)
			if err == nil {
				t.Fatalf("a lookup that cannot be completed must report an error; got %d referrers", len(refs))
			}
			if !errors.Is(err, oci.ErrReferrerDiscoveryUnavailable()) {
				t.Fatalf("error must carry the unavailable condition, got %v", err)
			}
		})
	}
}

// TestAttachReferrerDoesNotMutateSubjectTag proves attaching a referrer
// leaves the subject's tag pointing at the unchanged manifest digest —
// the immutability guarantee that lets `aggrega sign` amend a published
// artifact without re-pushing it.
func TestAttachReferrerDoesNotMutateSubjectTag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	subjectDigest, err := oci.Push(ctx, store, "1.2.3", oci.MediaTypeComponent, []byte("frozen bytes"), nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}

	if _, err := oci.AttachReferrer(ctx, store, "1.2.3", sigArtifactType, []byte("sig")); err != nil {
		t.Fatalf("AttachReferrer: %v", err)
	}

	afterDigest, exists, err := oci.Resolve(ctx, store, "1.2.3")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !exists {
		t.Fatal("subject tag disappeared after attaching a referrer")
	}
	if afterDigest != subjectDigest {
		t.Errorf("subject tag now points at %q, want unchanged %q", afterDigest, subjectDigest)
	}
}

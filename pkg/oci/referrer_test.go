// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"bytes"
	"context"
	"testing"

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

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"
	orasoci "oras.land/oras-go/v2/content/oci"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// makeDocsBundle writes a small deterministic gzipped tar (one file) to a
// temp path and returns the path and the bytes. It stands in for the rendered
// documentation bundle a CI job would build before publishing.
func makeDocsBundle(t *testing.T) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("# Aggrega docs\n")
	if err := tw.WriteHeader(&tar.Header{Name: "index.md", Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatalf("tar WriteHeader: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("tar Write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip Close: %v", err)
	}
	path := filepath.Join(t.TempDir(), "aggrega-docs.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	return path, buf.Bytes()
}

// TestPushArtifactDryRunPrintsDocsReference pins that --dry-run composes the
// reserved docs/<name> reference and prints it with the docs media type,
// without contacting any registry (PushTarget is nil; a real network call
// would fail).
func TestPushArtifactDryRunPrintsDocsReference(t *testing.T) {
	path, _ := makeDocsBundle(t)
	var buf bytes.Buffer
	if err := pushArtifactRun(t.Context(), pushArtifactConfig{
		Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{}),
		FilePath:  path,
		Name:      "aggrega-docs",
		Tag:       "1.0.0",
		Registry:  "example.com",
		DryRun:    true,
		Out:       &buf,
	}); err != nil {
		t.Fatalf("pushArtifactRun --dry-run: %v", err)
	}
	wantRef := "example.com/docs/aggrega-docs:1.0.0"
	if !strings.Contains(buf.String(), wantRef) {
		t.Errorf("dry-run stdout missing %q; got:\n%s", wantRef, buf.String())
	}
	if !strings.Contains(buf.String(), oci.MediaTypeDocs) {
		t.Errorf("dry-run stdout missing media type %q; got:\n%s", oci.MediaTypeDocs, buf.String())
	}
}

// TestPushArtifactRoundTripsThroughFileBackedStore is the transport proof: it
// packs a docs bundle, publishes it with the docs media type into a real
// file-backed OCI store, pulls it back, and asserts the stored artifact type
// and the content digest both match. This exercises the pack/tag/resolve/fetch
// path end-to-end without a network registry.
func TestPushArtifactRoundTripsThroughFileBackedStore(t *testing.T) {
	path, content := makeDocsBundle(t)

	store, err := orasoci.New(t.TempDir())
	if err != nil {
		t.Fatalf("orasoci.New: %v", err)
	}

	var buf bytes.Buffer
	if err := pushArtifactRun(t.Context(), pushArtifactConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{}),
		FilePath:   path,
		Name:       "aggrega-docs",
		Tag:        "1.0.0",
		Registry:   "example.com",
		PushTarget: store,
		Out:        &buf,
	}); err != nil {
		t.Fatalf("pushArtifactRun: %v", err)
	}
	if !strings.Contains(buf.String(), "Push complete: example.com/docs/aggrega-docs:1.0.0") {
		t.Errorf("push stdout missing completion line; got:\n%s", buf.String())
	}

	pulled, err := oci.Pull(context.Background(), store, "1.0.0")
	if err != nil {
		t.Fatalf("oci.Pull: %v", err)
	}
	if pulled.MediaType != oci.MediaTypeDocs {
		t.Errorf("artifactType: got %q; want %q", pulled.MediaType, oci.MediaTypeDocs)
	}
	if sha256Hex(pulled.Bytes) != sha256Hex(content) {
		t.Errorf("content digest mismatch: pulled sha256=%s, pushed sha256=%s",
			sha256Hex(pulled.Bytes), sha256Hex(content))
	}
	if !bytes.Equal(pulled.Bytes, content) {
		t.Errorf("round-trip bytes differ from the pushed bundle")
	}
}

// TestPushArtifactBypassesKindDispatch proves the generic push path never
// parses an entity envelope or dispatches on a Kind: the same opaque bytes
// that the kind-dispatched entity push (pushRun) rejects for lacking a kind
// field publish cleanly through pushArtifactRun. No fake entity Kind is
// synthesized; MediaTypeForKind is never consulted.
func TestPushArtifactBypassesKindDispatch(t *testing.T) {
	path, _ := makeDocsBundle(t)

	// The entity push path halts: the gzipped bundle carries no kind field.
	entityErr := pushRun(t.Context(), pushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{}),
		EntityPath: path,
		Registry:   "example.com",
		PushTarget: memory.New(),
	})
	if entityErr == nil {
		t.Fatal("entity push should reject a kind-less data blob; got nil")
	}

	// The generic data-artifact path publishes the identical bytes.
	if err := pushArtifactRun(t.Context(), pushArtifactConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{}),
		FilePath:   path,
		Name:       "aggrega-docs",
		Tag:        "1.0.0",
		Registry:   "example.com",
		PushTarget: memory.New(),
		Out:        &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("pushArtifactRun should publish opaque bytes without kind dispatch; got %v", err)
	}
}

// TestPushArtifactImmutableTag exercises the version-tag immutability guard:
// a fresh tag publishes, a re-push of byte-identical content under the same
// tag is an idempotent no-op, and a re-push of different content under the
// same tag is refused with ErrImmutableTagConflict.
func TestPushArtifactImmutableTag(t *testing.T) {
	store := memory.New()
	path, _ := makeDocsBundle(t)

	// First publish of a fresh tag succeeds.
	var first bytes.Buffer
	if err := pushArtifactRun(t.Context(), pushArtifactConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{}),
		FilePath:   path,
		Name:       "aggrega-docs",
		Tag:        "1.0.0",
		Registry:   "example.com",
		PushTarget: store,
		Out:        &first,
	}); err != nil {
		t.Fatalf("first pushArtifactRun: %v", err)
	}
	if !strings.Contains(first.String(), "Push complete") {
		t.Errorf("first push missing completion line; got:\n%s", first.String())
	}

	// Re-push of identical bytes under the same tag: idempotent no-op.
	var second bytes.Buffer
	if err := pushArtifactRun(t.Context(), pushArtifactConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{}),
		FilePath:   path,
		Name:       "aggrega-docs",
		Tag:        "1.0.0",
		Registry:   "example.com",
		PushTarget: store,
		Out:        &second,
	}); err != nil {
		t.Fatalf("idempotent re-push should return nil; got %v", err)
	}
	if !strings.Contains(second.String(), "Already published") {
		t.Errorf("idempotent re-push missing 'Already published'; got:\n%s", second.String())
	}
	if strings.Contains(second.String(), "Push complete") {
		t.Errorf("idempotent re-push should not overwrite; got:\n%s", second.String())
	}

	// Push of different bytes under the same tag: refused.
	otherPath := filepath.Join(t.TempDir(), "other.tar.gz")
	if err := os.WriteFile(otherPath, []byte("different opaque bytes"), 0o644); err != nil {
		t.Fatalf("write other artifact: %v", err)
	}
	var third bytes.Buffer
	err := pushArtifactRun(t.Context(), pushArtifactConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{}),
		FilePath:   otherPath,
		Name:       "aggrega-docs",
		Tag:        "1.0.0",
		Registry:   "example.com",
		PushTarget: store,
		Out:        &third,
	})
	if !errors.Is(err, oci.ErrImmutableTagConflict()) {
		t.Fatalf("conflicting re-push should return ErrImmutableTagConflict; got %v", err)
	}
}

// TestPushArtifactSignsByReference proves the existing reference-based
// aggrega sign signs a published docs artifact unchanged — signing is
// kind-agnostic, so the docs media type needs no special handling in sign.go.
func TestPushArtifactSignsByReference(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	path, _ := makeDocsBundle(t)
	if err := pushArtifactRun(t.Context(), pushArtifactConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{}),
		FilePath:   path,
		Name:       "aggrega-docs",
		Tag:        "1.0.0",
		Registry:   "example.com",
		PushTarget: store,
		Out:        &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("pushArtifactRun: %v", err)
	}
	published, _, err := oci.Resolve(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("Resolve published: %v", err)
	}

	ref := "example.com/docs/aggrega-docs:1.0.0"
	fake := &signingtest.FakeSigner{}
	if err := signRun(t.Context(), signConfig{
		Workspace:   resolveTempWorkspace(t, tempWorkspaceOptions{}),
		References:  []string{ref},
		SubjectRefs: map[string]string{ref: "1.0.0"},
		Target:      store,
		Signer:      fake,
		IDToken:     "supplied-token",
		EnvLookup:   func(string) string { return "" },
	}); err != nil {
		t.Fatalf("signRun: %v", err)
	}
	if len(fake.Signed) != 1 || fake.Signed[0] != published {
		t.Fatalf("signer saw %v, want one call for the published digest %q", fake.Signed, published)
	}
	refs, err := oci.ListReferrers(ctx, store, "1.0.0", signing.BundleMediaType)
	if err != nil {
		t.Fatalf("ListReferrers: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d signature referrers, want 1", len(refs))
	}
}

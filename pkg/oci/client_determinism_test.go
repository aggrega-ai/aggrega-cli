// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"context"
	"encoding/json"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// epochCreated is the fixed creation timestamp Push pins into every
// manifest so byte-identical content yields a byte-identical digest.
const epochCreated = "1970-01-01T00:00:00Z"

// TestPushPinsManifestCreated is the regression guard for the
// Deterministic Output Invariant at the manifest layer. oras stamps the
// manifest's `org.opencontainers.image.created` annotation with the
// current time unless a value is supplied; Push supplies the fixed epoch.
// Asserting the pinned value (rather than only packing twice) is the
// non-flaky guard: two packs within the same wall-clock second would
// otherwise match even with the time-derived stamp.
func TestPushPinsManifestCreated(t *testing.T) {
	ctx := context.Background()
	content := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/compute/api-server\n  version: 1.0.0\n")

	store := memory.New()
	if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, content); err != nil {
		t.Fatalf("Push: %v", err)
	}

	created := manifestCreated(t, ctx, store, "1.0.0")
	t.Logf("manifest created annotation: %q", created)
	if created != epochCreated {
		t.Errorf("manifest created annotation = %q, want pinned %q (non-deterministic, time-derived)", created, epochCreated)
	}
}

// TestPushManifestDigestDeterministic asserts the property the pin exists
// to provide: pushing byte-identical content yields a byte-identical
// manifest digest across independent pushes. The immutability guard in
// `aggrega push` relies on this to recognise a re-push of identical
// content as a no-op rather than a conflicting overwrite.
func TestPushManifestDigestDeterministic(t *testing.T) {
	ctx := context.Background()
	content := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/compute/api-server\n  version: 1.0.0\n")

	first, err := oci.Push(ctx, memory.New(), "1.0.0", oci.MediaTypeComponent, content)
	if err != nil {
		t.Fatalf("first Push: %v", err)
	}
	second, err := oci.Push(ctx, memory.New(), "1.0.0", oci.MediaTypeComponent, content)
	if err != nil {
		t.Fatalf("second Push: %v", err)
	}
	if first != second {
		t.Errorf("manifest digest non-deterministic for identical content:\n  #1 %s\n  #2 %s", first, second)
	}
}

// TestManifestDigestMatchesPush verifies that ManifestDigest — the
// no-remote-contact preview the push guard uses to compute the proposed
// digest — agrees with the digest a real Push produces for the same
// content. If these drift, the guard would compare against a digest the
// registry never receives.
func TestManifestDigestMatchesPush(t *testing.T) {
	ctx := context.Background()
	content := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Feature\nmetadata:\n  name: core/observability/logging\n  version: 2.3.1\n")

	preview, err := oci.ManifestDigest(ctx, oci.MediaTypeFeature, content)
	if err != nil {
		t.Fatalf("ManifestDigest: %v", err)
	}
	pushed, err := oci.Push(ctx, memory.New(), "2.3.1", oci.MediaTypeFeature, content)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if preview != pushed {
		t.Errorf("ManifestDigest preview %s != pushed digest %s", preview, pushed)
	}
}

// manifestCreated fetches the tagged manifest and returns its
// org.opencontainers.image.created annotation.
func manifestCreated(t *testing.T, ctx context.Context, store *memory.Store, tag string) string {
	t.Helper()
	desc, err := store.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("resolve %q: %v", tag, err)
	}
	manifestBytes, err := content.FetchAll(ctx, store, desc)
	if err != nil {
		t.Fatalf("fetch manifest %q: %v", tag, err)
	}
	var m struct {
		Annotations map[string]string `json:"annotations"`
	}
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		t.Fatalf("parse manifest %q: %v", tag, err)
	}
	return m.Annotations[ocispec.AnnotationCreated]
}

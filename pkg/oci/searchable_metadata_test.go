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
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// manifestAnnotations fetches the manifest tagged `tag` on store and returns its
// full annotation map, read by unmarshalling the raw manifest JSON locally so
// the assertion does not depend on any package-internal manifest view.
func manifestAnnotations(t *testing.T, ctx context.Context, store *memory.Store, tag string) map[string]string {
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
	return m.Annotations
}

// mustAnnotations renders the metadata's annotations and fails the test on an
// encoding error, so a call site that is not itself about the error path reads as
// one expression.
func mustAnnotations(t *testing.T, m oci.SearchableMetadata) map[string]string {
	t.Helper()
	a, err := m.Annotations()
	if err != nil {
		t.Fatalf("Annotations: %v", err)
	}
	return a
}

// TestSearchableMetadataAnnotationsOmitsEmpty pins the assembly contract: only
// populated facets render, capabilities join in author order, labels render as a
// JSON object in author order, and an empty struct renders no keys.
func TestSearchableMetadataAnnotationsOmitsEmpty(t *testing.T) {
	t.Parallel()

	labels := types.NewOrderedMap[string]()
	labels.Set("component.aggrega.ai/type", "server")
	labels.Set("component.aggrega.ai/category", "compute")

	full := mustAnnotations(t, oci.SearchableMetadata{
		Description:  "A demo plugin.",
		Category:     "compute",
		Kind:         "Plugin",
		PluginType:   "pipeline-step",
		Labels:       labels,
		Capabilities: []string{"helm", "compose"},
	})
	want := map[string]string{
		ocispec.AnnotationDescription: "A demo plugin.",
		oci.AnnotationCategory:        "compute",
		oci.AnnotationKind:            "Plugin",
		oci.AnnotationPluginType:      "pipeline-step",
		oci.AnnotationCapabilities:    "helm,compose",
		// Author order, not sorted: `type` was written before `category`.
		oci.AnnotationLabels: `{"component.aggrega.ai/type":"server","component.aggrega.ai/category":"compute"}`,
	}
	for k, v := range want {
		if full[k] != v {
			t.Errorf("annotation %q = %q; want %q", k, full[k], v)
		}
	}
	if len(full) != len(want) {
		t.Errorf("annotation count = %d; want %d (%v)", len(full), len(want), full)
	}

	// A catalog entity carries no plugin facets; those keys must be absent.
	comp := mustAnnotations(t, oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"})
	if _, ok := comp[oci.AnnotationPluginType]; ok {
		t.Error("component annotations should omit plugin-type")
	}
	if _, ok := comp[oci.AnnotationCapabilities]; ok {
		t.Error("component annotations should omit capabilities")
	}
	// An entity declaring no labels renders no label key at all, rather than an
	// empty JSON object a reader would have to special-case.
	if _, ok := comp[oci.AnnotationLabels]; ok {
		t.Error("an entity with no labels should omit the labels annotation")
	}
	if _, ok := mustAnnotations(t, oci.SearchableMetadata{
		Kind:   "Component",
		Labels: types.NewOrderedMap[string](),
	})[oci.AnnotationLabels]; ok {
		t.Error("an empty label map should omit the labels annotation")
	}

	if got := mustAnnotations(t, oci.SearchableMetadata{}); len(got) != 0 {
		t.Errorf("empty metadata should render no annotations; got %v", got)
	}
}

// TestManifestDigestMatchesPushWithAnnotations is the load-bearing determinism
// acceptance for a catalog entity: the in-memory ManifestDigest preview — the
// digest the publish signs and the immutability guard compares — equals the
// digest a real Push lands, WITH searchable annotations stamped. If these
// diverged, the guard would compare against a digest the registry never
// receives.
func TestManifestDigestMatchesPushWithAnnotations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	content := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: core/compute/api-server\n  version: 1.0.0\n")
	ann := mustAnnotations(t, oci.SearchableMetadata{
		Description: "The API server component.",
		Category:    "compute",
		Kind:        "Component",
	})

	preview, err := oci.ManifestDigest(ctx, oci.MediaTypeComponent, content, ann)
	if err != nil {
		t.Fatalf("ManifestDigest: %v", err)
	}
	store := memory.New()
	pushed, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, content, ann)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if preview != pushed {
		t.Fatalf("BLOCKER: preview digest %s != pushed digest %s (annotations present)", preview, pushed)
	}
	t.Logf("catalog: preview == pushed == %s", pushed)

	got := manifestAnnotations(t, ctx, store, "1.0.0")
	assertAnnotation(t, got, ocispec.AnnotationDescription, "The API server component.")
	assertAnnotation(t, got, oci.AnnotationCategory, "compute")
	assertAnnotation(t, got, oci.AnnotationKind, "Component")
	assertAnnotation(t, got, ocispec.AnnotationCreated, "1970-01-01T00:00:00Z")
}

// TestPluginManifestDigestMatchesPushWithAnnotations is the load-bearing
// determinism acceptance for a plugin: the PluginManifestDigest preview equals
// the digest PushPluginManifest lands, WITH searchable annotations stamped, and
// the landed manifest carries the plugin facets. The .wasm body is a minimal
// fake — this exercises the OCI packing with annotations, not the wasm runtime.
func TestPluginManifestDigestMatchesPushWithAnnotations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	entity := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Plugin\nmetadata:\n  name: frp/patch\n  version: 1.0.0\n")
	wasm := []byte("\x00asm\x01\x00\x00\x00fake-wasm-body")
	ann := mustAnnotations(t, oci.SearchableMetadata{
		Description:  "A patch step plugin.",
		Kind:         "Plugin",
		PluginType:   "pipeline-step",
		Capabilities: []string{"helm", "compose"},
	})

	preview, err := oci.PluginManifestDigest(ctx, entity, wasm, ann)
	if err != nil {
		t.Fatalf("PluginManifestDigest: %v", err)
	}
	store := memory.New()
	pushed, err := oci.PushPluginManifest(ctx, store, "1.0.0", entity, wasm, ann)
	if err != nil {
		t.Fatalf("PushPluginManifest: %v", err)
	}
	if preview != pushed {
		t.Fatalf("BLOCKER: plugin preview digest %s != pushed digest %s (annotations present)", preview, pushed)
	}
	t.Logf("plugin: preview == pushed == %s", pushed)

	got := manifestAnnotations(t, ctx, store, "1.0.0")
	assertAnnotation(t, got, ocispec.AnnotationDescription, "A patch step plugin.")
	assertAnnotation(t, got, oci.AnnotationKind, "Plugin")
	assertAnnotation(t, got, oci.AnnotationPluginType, "pipeline-step")
	assertAnnotation(t, got, oci.AnnotationCapabilities, "helm,compose")
	assertAnnotation(t, got, ocispec.AnnotationCreated, "1970-01-01T00:00:00Z")
}

func assertAnnotation(t *testing.T, got map[string]string, key, want string) {
	t.Helper()
	if got[key] != want {
		t.Errorf("annotation %q = %q; want %q", key, got[key], want)
	}
}

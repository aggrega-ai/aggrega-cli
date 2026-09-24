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

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// titleOf reads the org.opencontainers.image.title a descriptor carries, or ""
// when it carries none. A standard registry client names the file it writes a
// descriptor's bytes to from exactly this annotation, and skips a descriptor that
// has no title.
func titleOf(desc ocispec.Descriptor) string {
	return desc.Annotations[ocispec.AnnotationTitle]
}

// titleManifestAt fetches and decodes the manifest desc addresses in store.
func titleManifestAt(t *testing.T, store *memory.Store, desc ocispec.Descriptor) ocispec.Manifest {
	t.Helper()
	raw, err := content.FetchAll(context.Background(), store, desc)
	if err != nil {
		t.Fatalf("fetch manifest %s: %v", desc.Digest, err)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode manifest %s: %v", desc.Digest, err)
	}
	return m
}

// titleManifestTagged resolves tag in store and returns the manifest it names.
func titleManifestTagged(t *testing.T, store *memory.Store, tag string) ocispec.Manifest {
	t.Helper()
	desc, err := store.Resolve(context.Background(), tag)
	if err != nil {
		t.Fatalf("resolve %q: %v", tag, err)
	}
	return titleManifestAt(t, store, desc)
}

// TestEveryPublishableKindIsTitledByItsTransportShape proves the catalog path
// titles the content layer of every publishable kind in both of its shapes:
// entity.yaml where the entity ships as its authored document, entity.tar.gz
// where it ships as an archive with the files it declares.
//
// The expectation is written per media type rather than computed from the media
// type's suffix, so the test does not share the rule it checks. The set
// difference against the media-type table names any publishable kind and shape
// the table below does not cover, so a kind added there fails here until its
// titles are stated.
func TestEveryPublishableKindIsTitledByItsTransportShape(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		oci.MediaTypeComponent:                                    "entity.yaml",
		"application/vnd.aggrega.component.v1alpha1+tar+gz":       "entity.tar.gz",
		oci.MediaTypeFeature:                                      "entity.yaml",
		"application/vnd.aggrega.feature.v1alpha1+tar+gz":         "entity.tar.gz",
		oci.MediaTypeAggregate:                                    "entity.yaml",
		"application/vnd.aggrega.aggregate.v1alpha1+tar+gz":       "entity.tar.gz",
		oci.MediaTypeSchemaLibrary:                                "entity.yaml",
		"application/vnd.aggrega.schemalibrary.v1alpha1+tar+gz":   "entity.tar.gz",
		oci.MediaTypeMappingLibrary:                               "entity.yaml",
		"application/vnd.aggrega.mappinglibrary.v1alpha1+tar+gz":  "entity.tar.gz",
		oci.MediaTypeWorkloadLibrary:                              "entity.yaml",
		"application/vnd.aggrega.workloadlibrary.v1alpha1+tar+gz": "entity.tar.gz",
		oci.MediaTypeArtifactLibrary:                              "entity.yaml",
		"application/vnd.aggrega.artifactlibrary.v1alpha1+tar+gz": "entity.tar.gz",
		oci.MediaTypeRecipe:                                       "entity.tar.gz",
		"application/vnd.aggrega.recipe.v1alpha1+yaml":            "entity.yaml",
		oci.MediaTypeFeatureRecipe:                                "entity.tar.gz",
		"application/vnd.aggrega.featurerecipe.v1alpha1+yaml":     "entity.yaml",
		// A Plugin publishes through its own manifest; its titles are proven by
		// TestPluginManifestTitlesBothDescriptors. The catalog path still titles
		// the Plugin media types, which name the entity document.
		oci.MediaTypePlugin: "entity.yaml",
		"application/vnd.aggrega.wasmplugin.v1alpha1+tar+gz": "entity.tar.gz",
	}
	for _, kind := range oci.MediaTypeKinds() {
		for _, archive := range []bool{false, true} {
			mt, err := oci.MediaTypeFor(kind, archive)
			if err != nil {
				t.Fatalf("MediaTypeFor(%q, %v): %v", kind, archive, err)
			}
			if _, ok := want[mt]; !ok {
				t.Errorf("publishable kind %q (%s) has no stated title; add it to this table", kind, mt)
			}
		}
	}

	ctx := context.Background()
	for mt, title := range want {
		store := memory.New()
		if _, err := oci.Push(ctx, store, "1.0.0", mt, []byte("content for "+mt), nil); err != nil {
			t.Fatalf("Push(%s): %v", mt, err)
		}
		m := titleManifestTagged(t, store, "1.0.0")
		if len(m.Layers) != 1 {
			t.Fatalf("%s: manifest carries %d layers, want 1", mt, len(m.Layers))
		}
		if got := titleOf(m.Layers[0]); got != title {
			t.Errorf("%s: layer title = %q, want %q", mt, got, title)
		}
		if _, ok := m.Annotations[ocispec.AnnotationTitle]; ok {
			t.Errorf("%s: the title is stamped on the manifest; it belongs on the content descriptor", mt)
		}
	}
}

// TestPluginManifestTitlesBothDescriptors proves a Plugin publication titles its
// config descriptor (the entity) and its layer (the executable). A standard
// client writes a titled config to a file as it does a layer, so titling only
// one leaves the other skipped.
func TestPluginManifestTitlesBothDescriptors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.PushPluginManifest(ctx, store, "1.0.0", []byte("kind: Plugin\n"), []byte("\x00asm"), nil); err != nil {
		t.Fatalf("PushPluginManifest: %v", err)
	}
	m := titleManifestTagged(t, store, "1.0.0")
	if got := titleOf(m.Config); got != "entity.yaml" {
		t.Errorf("config title = %q, want %q", got, "entity.yaml")
	}
	if len(m.Layers) != 1 {
		t.Fatalf("manifest carries %d layers, want 1", len(m.Layers))
	}
	if got := titleOf(m.Layers[0]); got != "entity.wasm" {
		t.Errorf("layer title = %q, want %q", got, "entity.wasm")
	}
}

// TestReferrerPayloadIsTitledByItsArtifactType proves the two referrer payloads
// this product attaches — a signature bundle and a deprecation marker — are each
// titled, and that a referrer of a referrer is titled the same way.
func TestReferrerPayloadIsTitledByItsArtifactType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\n"), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}

	sig, err := oci.AttachReferrer(ctx, store, "1.0.0", oci.MediaTypeSignatureBundle, []byte(`{"bundle":"sig"}`))
	if err != nil {
		t.Fatalf("AttachReferrer(signature): %v", err)
	}
	marker, err := oci.AttachReferrer(ctx, store, "1.0.0", deprecation.ArtifactType, []byte(`{"severity":"warning"}`))
	if err != nil {
		t.Fatalf("AttachReferrer(deprecation): %v", err)
	}
	markerSig, err := oci.AttachReferrerToDescriptor(ctx, store, marker, oci.MediaTypeSignatureBundle, []byte(`{"bundle":"marker"}`))
	if err != nil {
		t.Fatalf("AttachReferrerToDescriptor(signature over the marker): %v", err)
	}

	cases := []struct {
		name string
		desc ocispec.Descriptor
		want string
	}{
		{"signature", sig, "signature.json"},
		{"deprecation marker", marker, "deprecation.json"},
		{"signature over the marker", markerSig, "signature.json"},
	}
	for _, c := range cases {
		m := titleManifestAt(t, store, c.desc)
		if len(m.Layers) != 1 {
			t.Fatalf("%s: referrer carries %d layers, want 1", c.name, len(m.Layers))
		}
		if got := titleOf(m.Layers[0]); got != c.want {
			t.Errorf("%s: payload title = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestNamedPublicationIsTitledByTheSuppliedName proves a data artifact is titled
// by the name its publisher supplied, and that the digest preview for that name
// equals what the publication lands — the name is part of the manifest, so a
// preview taken without it would not.
func TestNamedPublicationIsTitledByTheSuppliedName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	body := []byte("a documentation bundle")

	pushed, err := oci.PushNamed(ctx, store, "1.0.0", oci.MediaTypeDocs, "aggrega-docs", body, nil)
	if err != nil {
		t.Fatalf("PushNamed: %v", err)
	}
	previewed, err := oci.ManifestDigestNamed(ctx, oci.MediaTypeDocs, "aggrega-docs", body, nil)
	if err != nil {
		t.Fatalf("ManifestDigestNamed: %v", err)
	}
	if previewed != pushed {
		t.Errorf("previewed digest %s does not equal the digest the publication landed, %s", previewed, pushed)
	}
	unnamed, err := oci.ManifestDigest(ctx, oci.MediaTypeDocs, body, nil)
	if err != nil {
		t.Fatalf("ManifestDigest: %v", err)
	}
	if unnamed == pushed {
		t.Errorf("a preview taken without the name equals the named publication's digest %s; the name is not reaching the manifest", pushed)
	}

	m := titleManifestTagged(t, store, "1.0.0")
	if got := titleOf(m.Layers[0]); got != "aggrega-docs" {
		t.Errorf("layer title = %q, want %q", got, "aggrega-docs")
	}
}

// TestUnrecognisedMediaTypeIsPublishedUntitled pins the boundary of the derived
// title: a media type naming neither an entity kind nor a payload this package
// titles derives no name, and Push stamps none rather than inventing one. A
// caller publishing such content names it with PushNamed.
func TestUnrecognisedMediaTypeIsPublishedUntitled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, "1.0.0", "application/vnd.example.opaque", []byte("opaque"), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	m := titleManifestTagged(t, store, "1.0.0")
	if got := titleOf(m.Layers[0]); got != "" {
		t.Errorf("layer title = %q, want none", got)
	}
}

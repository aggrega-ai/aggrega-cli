// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// These literals are the CNCF Helm content and detached-provenance
// media types. They live here (not in pkg/oci) because pkg/oci is a
// generic transport that stays blind to any specific artifact media
// type — the caller names the content it expects.
const (
	testHelmContentMediaType = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
	testHelmProvMediaType    = "application/vnd.cncf.helm.chart.provenance.v1.prov"
)

// packManifest seeds `store` with each (mediaType, bytes) layer in order,
// packs a manifest whose layer list preserves that order, and tags it.
// It returns the packed layer bytes keyed by media type so a test can
// assert which blob a pull selected.
func packManifest(t *testing.T, ctx context.Context, store oras.Target, tag string, layers []struct {
	mediaType string
	body      []byte
}) {
	t.Helper()
	descs := make([]ocispec.Descriptor, 0, len(layers))
	for _, l := range layers {
		d, err := oras.PushBytes(ctx, store, l.mediaType, l.body)
		if err != nil {
			t.Fatalf("push layer %q: %v", l.mediaType, err)
		}
		descs = append(descs, d)
	}
	// A helm chart manifest carries a config blob and leaves artifactType
	// empty, so the content mediatype resolves from the selected layer.
	configDesc, err := oras.PushBytes(ctx, store, "application/vnd.cncf.helm.config.v1+json", []byte("{}"))
	if err != nil {
		t.Fatalf("push config: %v", err)
	}
	manifestDesc, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1, "",
		oras.PackManifestOptions{Layers: descs, ConfigDescriptor: &configDesc})
	if err != nil {
		t.Fatalf("pack manifest: %v", err)
	}
	if err := store.Tag(ctx, manifestDesc, tag); err != nil {
		t.Fatalf("tag %q: %v", tag, err)
	}
}

// TestPullSelectsContentLayerByMediaType pins the provenance-layer fix: a
// signed chart's manifest lists the detached provenance layer BEFORE the
// chart content layer, so a position-based selection would return the
// provenance blob. Pull, told the content media type, returns the chart
// content blob instead.
func TestPullSelectsContentLayerByMediaType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	content := []byte("chart-content-tar-gz-bytes\n")
	prov := []byte("-----BEGIN PGP SIGNED MESSAGE-----\nfake-provenance\n")
	// Provenance FIRST — the order that traps an index-based selection.
	packManifest(t, ctx, store, "25.0.8", []struct {
		mediaType string
		body      []byte
	}{
		{testHelmProvMediaType, prov},
		{testHelmContentMediaType, content},
	})

	got, err := oci.Pull(ctx, store, "25.0.8", testHelmContentMediaType)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if !bytes.Equal(got.Bytes, content) {
		t.Errorf("Pull selected the wrong layer.\n  got:  %q\n  want: %q (content, not provenance)", got.Bytes, content)
	}
	if bytes.Equal(got.Bytes, prov) {
		t.Error("Pull returned the provenance blob; expected the chart content")
	}
	if got.MediaType != testHelmContentMediaType {
		t.Errorf("MediaType = %q; want %q", got.MediaType, testHelmContentMediaType)
	}
}

// TestPullNoContentLayerHaltsLoudly pins the loud guardrail: a manifest
// that carries no layer of the expected content media type errors with
// ErrNoContentLayer rather than returning some other layer.
func TestPullNoContentLayerHaltsLoudly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	prov := []byte("only-a-provenance-layer\n")
	packManifest(t, ctx, store, "1.0.0", []struct {
		mediaType string
		body      []byte
	}{
		{testHelmProvMediaType, prov},
	})

	_, err := oci.Pull(ctx, store, "1.0.0", testHelmContentMediaType)
	if err == nil {
		t.Fatal("expected an error for a manifest with no content-type layer; got nil")
	}
	if !errors.Is(err, oci.ErrNoContentLayer) {
		t.Errorf("error should wrap ErrNoContentLayer; got %v", err)
	}
}

// TestPullAmbiguousContentLayerHaltsLoudly pins the loud guardrail for the
// other direction: more than one layer of the expected media type is
// ambiguous, so selection halts with ErrAmbiguousContentLayer rather than
// pick the first.
func TestPullAmbiguousContentLayerHaltsLoudly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	packManifest(t, ctx, store, "1.0.0", []struct {
		mediaType string
		body      []byte
	}{
		{testHelmContentMediaType, []byte("content-a\n")},
		{testHelmContentMediaType, []byte("content-b\n")},
	})

	_, err := oci.Pull(ctx, store, "1.0.0", testHelmContentMediaType)
	if err == nil {
		t.Fatal("expected an error for a manifest with two content-type layers; got nil")
	}
	if !errors.Is(err, oci.ErrAmbiguousContentLayer) {
		t.Errorf("error should wrap ErrAmbiguousContentLayer; got %v", err)
	}
}

// TestPullSingleLayerWithExpectedMediaType is the non-regression case: an
// ordinary single-content-layer manifest still pulls cleanly when the
// caller names the expected content media type.
func TestPullSingleLayerWithExpectedMediaType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	content := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\n")
	if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, content); err != nil {
		t.Fatalf("Push: %v", err)
	}

	got, err := oci.Pull(ctx, store, "1.0.0", oci.MediaTypeComponent)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if !bytes.Equal(got.Bytes, content) {
		t.Errorf("round-trip mismatch.\n  got:  %q\n  want: %q", got.Bytes, content)
	}
}

// TestVerifyContentAgainstManifestSelectsByMediaType pins that the content
// verify — not just the layer selection — is anchored to the expected
// content layer. Against a provenance-first manifest, verifying the chart
// content succeeds only because the verify selects the content layer's
// digest; verifying the provenance bytes against the content media type
// fails as an integrity mismatch.
func TestVerifyContentAgainstManifestSelectsByMediaType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	content := []byte("chart-content\n")
	prov := []byte("provenance-signature\n")
	packManifest(t, ctx, store, "1.0.0", []struct {
		mediaType string
		body      []byte
	}{
		{testHelmProvMediaType, prov},
		{testHelmContentMediaType, content},
	})

	res, err := oci.Pull(ctx, store, "1.0.0", testHelmContentMediaType)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}

	if err := oci.VerifyContentAgainstManifest(res.ManifestBytes, content, testHelmContentMediaType); err != nil {
		t.Errorf("verifying the chart content against the content layer should pass; got %v", err)
	}
	if err := oci.VerifyContentAgainstManifest(res.ManifestBytes, prov, testHelmContentMediaType); !errors.Is(err, oci.ErrFetchIntegrity) {
		t.Errorf("verifying the provenance bytes against the content layer should fail with ErrFetchIntegrity; got %v", err)
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"context"
	"fmt"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
)

// PushPluginManifest packs a Plugin entity and its .wasm executable into ONE
// OCI manifest and tags it, returning the manifest digest. The manifest's
// config blob carries the entity YAML under MediaTypePlugin; its single layer
// carries the .wasm bytes under MediaTypeWasm. The executable is therefore
// referenced by digest from the same manifest that describes the entity — one
// signed, tagged unit rather than two independently-addressed artifacts.
//
// The manifest's creation annotation is pinned, so byte-identical
// (entityYAML, wasm) inputs always produce a byte-identical manifest digest —
// the property a re-push idempotency guard and diff-based review rely on. Push
// adds no content canonicalisation; callers supply entity bytes and .wasm bytes
// as they intend them published.
func PushPluginManifest(ctx context.Context, target oras.Target, tag string, entityYAML, wasm []byte) (string, error) {
	manifestDesc, err := packPluginManifest(ctx, target, entityYAML, wasm)
	if err != nil {
		return "", err
	}
	if err := target.Tag(ctx, manifestDesc, tag); err != nil {
		return "", fmt.Errorf("oci: tag %q: %w", tag, err)
	}
	return manifestDesc.Digest.String(), nil
}

// packPluginManifest uploads the entity YAML as the config blob and the .wasm
// as the single layer, then packs a deterministic OCI manifest referencing
// both. It does NOT tag. The creation annotation is pinned so the manifest
// digest is a pure function of the two inputs.
func packPluginManifest(ctx context.Context, target oras.Target, entityYAML, wasm []byte) (ocispec.Descriptor, error) {
	configDesc, err := oras.PushBytes(ctx, target, MediaTypePlugin, entityYAML)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("oci: push plugin entity config: %w", err)
	}
	wasmDesc, err := oras.PushBytes(ctx, target, MediaTypeWasm, wasm)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("oci: push plugin wasm layer: %w", err)
	}
	manifestDesc, err := oras.PackManifest(
		ctx,
		target,
		oras.PackManifestVersion1_1,
		MediaTypePlugin,
		oras.PackManifestOptions{
			ConfigDescriptor:    &configDesc,
			Layers:              []ocispec.Descriptor{wasmDesc},
			ManifestAnnotations: map[string]string{ocispec.AnnotationCreated: epochCreated},
		},
	)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("oci: pack plugin manifest: %w", err)
	}
	return manifestDesc, nil
}

// FetchPluginManifest resolves tag on target and reads back the single-manifest
// plugin artifact PushPluginManifest produced: the entity YAML from the config
// blob and the .wasm bytes from the layer selected by MediaTypeWasm. It returns
// both blobs and the manifest digest.
//
// The .wasm layer is selected by media type, not by position, so a manifest
// that is missing (or ambiguous about) the .wasm layer halts loudly with
// ErrNoContentLayer / ErrAmbiguousContentLayer rather than yield the wrong blob.
func FetchPluginManifest(ctx context.Context, target oras.ReadOnlyTarget, tag string) (entityYAML, wasm []byte, manifestDigest string, err error) {
	manifestDesc, err := target.Resolve(ctx, tag)
	if err != nil {
		return nil, nil, "", fmt.Errorf("oci: resolve %q: %w", tag, err)
	}
	manifestBytes, err := fetchBlob(ctx, target, manifestDesc)
	if err != nil {
		return nil, nil, "", fmt.Errorf("oci: fetch manifest %q: %w", tag, err)
	}
	view, err := parseManifest(manifestBytes)
	if err != nil {
		return nil, nil, "", fmt.Errorf("oci: parse manifest %q: %w", tag, err)
	}
	entityYAML, err = fetchBlob(ctx, target, view.Config)
	if err != nil {
		return nil, nil, "", fmt.Errorf("oci: fetch plugin entity config %q: %w", tag, err)
	}
	wasmDesc, err := selectContentLayer(view.Layers, MediaTypeWasm)
	if err != nil {
		return nil, nil, "", fmt.Errorf("oci: %q: %w", tag, err)
	}
	wasm, err = fetchBlob(ctx, target, wasmDesc)
	if err != nil {
		return nil, nil, "", fmt.Errorf("oci: fetch plugin wasm layer %q: %w", tag, err)
	}
	return entityYAML, wasm, manifestDesc.Digest.String(), nil
}

// fetchBlob fetches a descriptor's bytes from target and closes the reader.
func fetchBlob(ctx context.Context, target oras.ReadOnlyTarget, desc ocispec.Descriptor) ([]byte, error) {
	rc, err := target.Fetch(ctx, desc)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

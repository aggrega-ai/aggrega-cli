// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"context"
	"fmt"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
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
//
// `annotations` carries the searchable catalog annotations to stamp onto the
// manifest (see SearchableMetadata); pass nil to stamp none. A caller that
// previews the digest with PluginManifestDigest before pushing MUST pass the
// same annotations map to both, or the previewed digest will not equal the
// pushed digest.
//
// Each write it makes to the target — the entity config blob, the .wasm layer,
// the manifest, the tag — retries a transient registry failure (see
// IsTransientRegistryError) up to three attempts with a short backoff. A
// retried upload is a new upload of the same bytes, and a retried manifest is
// the same manifest, so a retry lands the digest one attempt would. A write
// still failing after its last attempt fails the push, and the error names the
// step.
func PushPluginManifest(ctx context.Context, target oras.Target, tag string, entityYAML, wasm []byte, annotations map[string]string) (string, error) {
	manifestDesc, err := packPluginManifest(ctx, target, entityYAML, wasm, annotations)
	if err != nil {
		return "", err
	}
	if err := tagManifest(ctx, target, manifestDesc, tag); err != nil {
		return "", fmt.Errorf("oci: tag %q: %w", tag, err)
	}
	return manifestDesc.Digest.String(), nil
}

// PluginManifestDigest computes, without contacting any remote, the manifest
// digest PushPluginManifest would produce for (entityYAML, wasm). It packs into
// an in-memory store and returns the descriptor digest. Because the manifest's
// creation annotation is pinned and both the config and layer blobs are
// content-addressed, the result is deterministic and equal to the digest a real
// PushPluginManifest of the same inputs yields — so a caller can preview the
// digest a push would land, sign it before any bytes reach the registry, and
// compare it against an existing tag to decide whether the push would overwrite.
//
// `annotations` must equal the map the corresponding PushPluginManifest will
// stamp (see SearchableMetadata); the searchable annotations are part of the
// manifest and therefore part of the digest, so a preview computed with a
// different set will not match the pushed digest.
func PluginManifestDigest(ctx context.Context, entityYAML, wasm []byte, annotations map[string]string) (string, error) {
	manifestDesc, err := packPluginManifest(ctx, memory.New(), entityYAML, wasm, annotations)
	if err != nil {
		return "", err
	}
	return manifestDesc.Digest.String(), nil
}

// packPluginManifest uploads the entity YAML as the config blob and the .wasm
// as the single layer, then packs a deterministic OCI manifest referencing
// both. It does NOT tag. The creation annotation is pinned so the manifest
// digest is a pure function of the two inputs and the stamped annotations.
//
// Both descriptors carry a title, because a standard registry client writes
// each titled descriptor to a file — the config included — and skips an
// untitled one: the entity is entity.yaml and the executable entity.wasm (see
// contentTitle). The titles derive from the two media types alone, so they keep
// the manifest digest a pure function of the inputs.
//
// `annotations` carries the searchable catalog annotations (see
// SearchableMetadata); it is merged with the pinned creation annotation into
// one manifest-annotation map. A nil or empty map stamps only the creation pin.
//
// The two uploads and the manifest pack each retry a transient registry failure
// (see IsTransientRegistryError), each attempt a new upload of the same bytes.
func packPluginManifest(ctx context.Context, target oras.Target, entityYAML, wasm []byte, annotations map[string]string) (ocispec.Descriptor, error) {
	configDesc, err := pushBlob(ctx, target, MediaTypePlugin, entityYAML)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("oci: push plugin entity config: %w", err)
	}
	configDesc = withTitle(configDesc, contentTitle(MediaTypePlugin))
	wasmDesc, err := pushBlob(ctx, target, MediaTypeWasm, wasm)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("oci: push plugin wasm layer: %w", err)
	}
	wasmDesc = withTitle(wasmDesc, contentTitle(MediaTypeWasm))
	manifestDesc, err := packManifest(ctx, target, MediaTypePlugin, oras.PackManifestOptions{
		ConfigDescriptor:    &configDesc,
		Layers:              []ocispec.Descriptor{wasmDesc},
		ManifestAnnotations: annotations,
	})
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
//
// The manifest is read as Pull reads it, so its error mapping is Pull's: a tag
// nothing is published under wraps ErrFetchNotFound, a refused or absent
// credential wraps ErrFetchUnauthorized, and a manifest body that does not match
// its digest wraps ErrFetchIntegrity.
func FetchPluginManifest(ctx context.Context, target oras.ReadOnlyTarget, tag string) (entityYAML, wasm []byte, manifestDigest string, err error) {
	manifestDesc, manifestBytes, err := fetchManifest(ctx, target, tag)
	if err != nil {
		return nil, nil, "", err
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

// FetchPluginEntity resolves tag on target and reads back the Plugin entity YAML
// from the single plugin manifest's CONFIG blob, returning it as a PullResult:
// Bytes is the entity YAML, Digest is the manifest digest, MediaType is the
// config media type (MediaTypePlugin), and ManifestBytes is the raw manifest.
// The config blob is verified against the manifest's config descriptor before
// return, so a tampered entity halts loudly.
//
// It is the entity-read counterpart of Pull for the single-manifest plugin
// artifact: where Pull reads the content LAYER (the .wasm, when the caller
// resolves the executable), this reads the CONFIG (the entity, when the caller
// resolves the catalog entity). The two read different descriptors of the same
// manifest, so the entity and its executable resolve from one tagged unit.
//
// The manifest is read as Pull reads it, so the error mapping mirrors Pull
// (ErrFetchNotFound / ErrFetchUnauthorized / ErrFetchIntegrity), and a missing
// or auth-refused plugin entity surfaces the same sentinels the entity-pull tier
// and its remediation expect.
func FetchPluginEntity(ctx context.Context, target oras.ReadOnlyTarget, tag string) (*PullResult, error) {
	manifestDesc, manifestBytes, err := fetchManifest(ctx, target, tag)
	if err != nil {
		return nil, err
	}
	return pluginEntityFromManifest(ctx, target, tag, manifestDesc, manifestBytes)
}

// pluginEntityFromManifest reads the Plugin entity a fetched single plugin
// manifest carries as its config blob, verifies it against the manifest's config
// descriptor, and returns it as a PullResult.
//
// It is the second half of FetchPluginEntity, separated so a reader that has
// already fetched the manifest and found it to be a plugin manifest reads the
// config blob through the same verify rather than a copy of it.
func pluginEntityFromManifest(ctx context.Context, target oras.ReadOnlyTarget, tag string, manifestDesc ocispec.Descriptor, manifestBytes []byte) (*PullResult, error) {
	view, err := parseManifest(manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("oci: parse manifest %q: %w", tag, err)
	}
	entityYAML, err := fetchBlob(ctx, target, view.Config)
	if err != nil {
		return nil, fmt.Errorf("oci: fetch plugin entity config %q: %w", tag, err)
	}
	if err := VerifyConfigAgainstManifest(manifestBytes, entityYAML); err != nil {
		return nil, fmt.Errorf("oci: %q: %w", tag, err)
	}
	return &PullResult{
		Bytes:         entityYAML,
		Digest:        manifestDesc.Digest.String(),
		MediaType:     view.Config.MediaType,
		ManifestBytes: manifestBytes,
	}, nil
}

// PluginWasmLayerDigest returns the content digest of the .wasm layer a single
// plugin manifest commits to — the layer selected by MediaTypeWasm. That digest
// is the content-address key the .wasm artifact cache slot is written under, so a
// manifest-driven resolution can locate the executable blob from the manifest
// alone, without a separate recorded pin. A manifest missing (or ambiguous about)
// the .wasm layer halts loudly with ErrNoContentLayer / ErrAmbiguousContentLayer.
func PluginWasmLayerDigest(manifestBytes []byte) (string, error) {
	desc, _, err := descriptorFromManifest(manifestBytes, MediaTypeWasm)
	if err != nil {
		return "", err
	}
	return desc.Digest.String(), nil
}

// IsPluginManifest reports whether an OCI manifest is a single-manifest plugin
// artifact — its config blob carries the Plugin entity YAML (MediaTypePlugin),
// distinct from every other catalog manifest, whose entity content rides as a
// layer with no typed config. It lets a manifest-driven cache read verify a
// cached plugin entity against the config descriptor while every other entity
// verifies against its content layer, without a caller having to thread the kind.
// A manifest that does not parse is not a plugin manifest.
func IsPluginManifest(manifestBytes []byte) bool {
	view, err := parseManifest(manifestBytes)
	if err != nil {
		return false
	}
	return view.Config.MediaType == MediaTypePlugin
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

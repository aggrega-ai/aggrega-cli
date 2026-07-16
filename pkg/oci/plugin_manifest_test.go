// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"bytes"
	"context"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
)

// Sample Plugin entity YAML and .wasm bytes reused across the plugin-manifest
// round-trip tests. The .wasm body is a minimal fake — these tests exercise the
// OCI packing/unpacking primitive, not the wasm runtime.
var (
	testPluginEntity = []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Plugin\nmetadata:\n  name: frp/patch\n  version: 1.0.0\nspec:\n  plugin: \"oci://hub.aggrega.ai/plugin/frp/patch:1.0.0\"\n")
	testPluginWasm   = []byte("\x00asm\x01\x00\x00\x00fake-wasm-body")
)

// TestPushPluginManifestDeterministic proves the writer is byte-deterministic:
// two packs of identical (entity, wasm) inputs into independent stores yield the
// same manifest digest, so byte-identical content never produces a shifting
// digest.
func TestPushPluginManifestDeterministic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	first, err := PushPluginManifest(ctx, memory.New(), "1.0.0", testPluginEntity, testPluginWasm, nil)
	if err != nil {
		t.Fatalf("first PushPluginManifest: %v", err)
	}
	second, err := PushPluginManifest(ctx, memory.New(), "1.0.0", testPluginEntity, testPluginWasm, nil)
	if err != nil {
		t.Fatalf("second PushPluginManifest: %v", err)
	}
	if first != second {
		t.Errorf("manifest digest not deterministic:\n  first:  %s\n  second: %s", first, second)
	}
}

// TestPushPluginManifestInlinePairing proves the .wasm rides as an inline layer
// referenced by its own content digest: the manifest's wasm layer digest equals
// digest.ContentDigest(wasm), and selectContentLayer resolves exactly that layer
// by MediaTypeWasm.
func TestPushPluginManifestInlinePairing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	if _, err := PushPluginManifest(ctx, store, "1.0.0", testPluginEntity, testPluginWasm, nil); err != nil {
		t.Fatalf("PushPluginManifest: %v", err)
	}

	manifestDesc, err := store.Resolve(ctx, "1.0.0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	manifestBytes, err := fetchBlob(ctx, store, manifestDesc)
	if err != nil {
		t.Fatalf("fetch manifest: %v", err)
	}
	view, err := parseManifest(manifestBytes)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	layer, err := selectContentLayer(view.Layers, MediaTypeWasm)
	if err != nil {
		t.Fatalf("selectContentLayer(MediaTypeWasm): %v", err)
	}
	if want := digest.ContentDigest(testPluginWasm); layer.Digest.String() != want {
		t.Errorf("wasm layer digest = %s; want %s (== digest.ContentDigest(wasm))", layer.Digest.String(), want)
	}
	if layer.MediaType != MediaTypeWasm {
		t.Errorf("wasm layer media type = %q; want %q", layer.MediaType, MediaTypeWasm)
	}
}

// TestPushPluginManifestConfigRoundTrip proves the config blob is the entity
// YAML: fetched directly it equals the pushed entity bytes, and the companion
// reader returns entity + wasm + digest as a whole.
func TestPushPluginManifestConfigRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()

	pushedDigest, err := PushPluginManifest(ctx, store, "1.0.0", testPluginEntity, testPluginWasm, nil)
	if err != nil {
		t.Fatalf("PushPluginManifest: %v", err)
	}

	// Direct config-blob check.
	manifestDesc, err := store.Resolve(ctx, "1.0.0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	manifestBytes, err := fetchBlob(ctx, store, manifestDesc)
	if err != nil {
		t.Fatalf("fetch manifest: %v", err)
	}
	view, err := parseManifest(manifestBytes)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if view.Config.MediaType != MediaTypePlugin {
		t.Errorf("config media type = %q; want %q", view.Config.MediaType, MediaTypePlugin)
	}
	configBytes, err := fetchBlob(ctx, store, view.Config)
	if err != nil {
		t.Fatalf("fetch config: %v", err)
	}
	if !bytes.Equal(configBytes, testPluginEntity) {
		t.Errorf("config blob != entity YAML:\n  got:  %q\n  want: %q", configBytes, testPluginEntity)
	}

	// Whole-reader round-trip through the exported companion.
	gotEntity, gotWasm, gotDigest, err := FetchPluginManifest(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("FetchPluginManifest: %v", err)
	}
	if !bytes.Equal(gotEntity, testPluginEntity) {
		t.Errorf("reader entity != pushed entity:\n  got:  %q\n  want: %q", gotEntity, testPluginEntity)
	}
	if !bytes.Equal(gotWasm, testPluginWasm) {
		t.Errorf("reader wasm != pushed wasm:\n  got:  %q\n  want: %q", gotWasm, testPluginWasm)
	}
	if gotDigest != pushedDigest {
		t.Errorf("reader manifest digest = %s; want %s", gotDigest, pushedDigest)
	}
}

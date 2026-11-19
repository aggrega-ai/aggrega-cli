// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci

import (
	"bytes"
	"context"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
)

// Sample Plugin entity YAML and .wasm bytes reused across the plugin-manifest
// round-trip tests. The .wasm body is a minimal fake — these tests exercise the
// OCI packing/unpacking primitive, not the wasm runtime.
var (
	testPluginEntity = []byte("apiVersion: aggrega.ai/v1beta1\nkind: Plugin\nmetadata:\n  name: frp/patch\n  version: 1.0.0\nspec:\n  plugin: \"oci://hub.aggrega.ai/plugin/frp/patch:1.0.0\"\n")
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

// testPluginNotices is the third-party notices document the notices tests pack
// beside the .wasm.
var testPluginNotices = []byte("# Third-party notices\n\nexample-dependency 1.2.3, Apache-2.0\n")

// pluginManifestTagged resolves tag in store and returns the plugin manifest it
// names, decoded.
func pluginManifestTagged(t *testing.T, store *memory.Store, tag string) (ocispec.Descriptor, []byte, ociManifestView) {
	t.Helper()
	ctx := context.Background()
	desc, err := store.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("resolve %q: %v", tag, err)
	}
	raw, err := fetchBlob(ctx, store, desc)
	if err != nil {
		t.Fatalf("fetch manifest: %v", err)
	}
	view, err := parseManifest(raw)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return desc, raw, view
}

// TestPushPluginManifestWithNoticesPacksWasmThenNotices proves a plugin pushed
// with notices carries two layers, the .wasm first and the notices second under
// their own media type and title, and that the .wasm is still the layer
// PluginWasmLayerDigest and FetchPluginManifest select.
func TestPushPluginManifestWithNoticesPacksWasmThenNotices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	if _, err := PushPluginManifest(ctx, store, "1.0.0", testPluginEntity, testPluginWasm, nil, WithPluginNotices(testPluginNotices)); err != nil {
		t.Fatalf("PushPluginManifest: %v", err)
	}
	_, raw, view := pluginManifestTagged(t, store, "1.0.0")
	if len(view.Layers) != 2 {
		t.Fatalf("manifest carries %d layers, want 2", len(view.Layers))
	}
	if view.Layers[0].MediaType != MediaTypeWasm {
		t.Errorf("layer 0 media type = %q, want %q", view.Layers[0].MediaType, MediaTypeWasm)
	}
	notices := view.Layers[1]
	if notices.MediaType != MediaTypePluginNotices {
		t.Errorf("layer 1 media type = %q, want %q", notices.MediaType, MediaTypePluginNotices)
	}
	if want := "application/vnd.aggrega.wasmplugin.notices.v1beta1+markdown"; MediaTypePluginNotices != want {
		t.Errorf("MediaTypePluginNotices = %q, want %q", MediaTypePluginNotices, want)
	}
	if got := notices.Annotations[ocispec.AnnotationTitle]; got != "notices.markdown" {
		t.Errorf("layer 1 title = %q, want %q", got, "notices.markdown")
	}
	if want := digest.ContentDigest(testPluginNotices); notices.Digest.String() != want {
		t.Errorf("notices layer digest = %s, want %s", notices.Digest, want)
	}
	if _, _, ok := KindOfMediaType(MediaTypePluginNotices); ok {
		t.Errorf("KindOfMediaType resolves the notices media type to an entity kind")
	}

	wasmDigest, err := PluginWasmLayerDigest(raw)
	if err != nil {
		t.Fatalf("PluginWasmLayerDigest: %v", err)
	}
	if want := digest.ContentDigest(testPluginWasm); wasmDigest != want {
		t.Errorf("PluginWasmLayerDigest = %s, want %s", wasmDigest, want)
	}
	_, wasm, _, err := FetchPluginManifest(ctx, store, "1.0.0")
	if err != nil {
		t.Fatalf("FetchPluginManifest: %v", err)
	}
	if !bytes.Equal(wasm, testPluginWasm) {
		t.Errorf("FetchPluginManifest returned %q as the wasm, want %q", wasm, testPluginWasm)
	}
}

// TestPluginManifestDigestWithNoticesEqualsThePushedDigest proves the digest
// preview takes the same notices option as the push and yields the pushed
// digest, and that the notices change the digest.
func TestPluginManifestDigestWithNoticesEqualsThePushedDigest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	preview, err := PluginManifestDigest(ctx, testPluginEntity, testPluginWasm, nil, WithPluginNotices(testPluginNotices))
	if err != nil {
		t.Fatalf("PluginManifestDigest: %v", err)
	}
	pushed, err := PushPluginManifest(ctx, memory.New(), "1.0.0", testPluginEntity, testPluginWasm, nil, WithPluginNotices(testPluginNotices))
	if err != nil {
		t.Fatalf("PushPluginManifest: %v", err)
	}
	if preview != pushed {
		t.Errorf("preview %s != pushed %s", preview, pushed)
	}
	without, err := PluginManifestDigest(ctx, testPluginEntity, testPluginWasm, nil)
	if err != nil {
		t.Fatalf("PluginManifestDigest without notices: %v", err)
	}
	if without == preview {
		t.Errorf("digest with notices equals the digest without: %s", without)
	}
}

// TestPushPluginManifestWithoutNoticesKeepsTheSingleLayer proves a push without
// the option still carries the .wasm as its only layer, and that the digest
// preview agrees with it.
func TestPushPluginManifestWithoutNoticesKeepsTheSingleLayer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	pushed, err := PushPluginManifest(ctx, store, "1.0.0", testPluginEntity, testPluginWasm, nil)
	if err != nil {
		t.Fatalf("PushPluginManifest: %v", err)
	}
	_, _, view := pluginManifestTagged(t, store, "1.0.0")
	if len(view.Layers) != 1 || view.Layers[0].MediaType != MediaTypeWasm {
		t.Errorf("layers = %+v, want the .wasm alone", view.Layers)
	}
	preview, err := PluginManifestDigest(ctx, testPluginEntity, testPluginWasm, nil)
	if err != nil {
		t.Fatalf("PluginManifestDigest: %v", err)
	}
	if preview != pushed {
		t.Errorf("preview %s != pushed %s", preview, pushed)
	}
}

// TestPluginNoticesOptionRefusesEmptyNotices proves an option given empty notices
// is an error naming the problem from both entry points, and that a push refused
// this way leaves no tag behind.
func TestPluginNoticesOptionRefusesEmptyNotices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, empty := range map[string][]byte{"nil": nil, "zero length": {}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := memory.New()
			_, err := PushPluginManifest(ctx, store, "1.0.0", testPluginEntity, testPluginWasm, nil, WithPluginNotices(empty))
			if err == nil || !strings.Contains(err.Error(), "plugin notices are empty") {
				t.Errorf("PushPluginManifest error = %v, want one naming empty plugin notices", err)
			}
			if _, rerr := store.Resolve(ctx, "1.0.0"); rerr == nil {
				t.Errorf("a refused push left the tag resolvable")
			}
			_, err = PluginManifestDigest(ctx, testPluginEntity, testPluginWasm, nil, WithPluginNotices(empty))
			if err == nil || !strings.Contains(err.Error(), "plugin notices are empty") {
				t.Errorf("PluginManifestDigest error = %v, want one naming empty plugin notices", err)
			}
		})
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// slotShape is one state a cache slot a registry fetch fills can be found in: the
// complete entry a fetch leaves, and the two shapes of an incomplete one — the
// content with no manifest file beside it, and the content beside an empty one.
type slotShape struct {
	name string
	// lay adjusts the manifest of a complete slot at slot into the shape.
	lay      func(t *testing.T, slot string)
	complete bool
}

func slotShapes() []slotShape {
	return []slotShape{
		{name: "the content and its manifest, as a fetch writes them", lay: func(*testing.T, string) {}, complete: true},
		{name: "the content and no manifest", lay: func(t *testing.T, slot string) {
			t.Helper()
			if err := os.Remove(filepath.Join(slot, "manifest")); err != nil {
				t.Fatalf("remove the manifest: %v", err)
			}
		}},
		{name: "the content and an empty manifest", lay: func(t *testing.T, slot string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(slot, "manifest"), nil, 0o644); err != nil {
				t.Fatalf("empty the manifest: %v", err)
			}
		}},
	}
}

// TestAnEntitySlotWithoutItsManifestIsAMissToEveryReader holds the readers of an
// entity's cache slot to one answer about a slot holding content but no manifest:
// it is not an entry, so each misses it, and each reads the complete slot a fetch
// leaves. The readers are the cascade's cache tier as the pull meets it — a bare
// reference held at a prior pin — and as a build and the daemon meet it — a
// versioned reference with no prior pins — and the build's verifying read. Each
// shape is a fixture of its own, and the complete one is the control that keeps
// every miss below from passing on a slot no reader could read at all.
func TestAnEntitySlotWithoutItsManifestIsAMissToEveryReader(t *testing.T) {
	t.Parallel()
	const cacheDir = ".aggrega/cache"
	const bare, version = "test/app", "1.0.0"
	slotURI := bare + ":" + version
	body := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: test/app\n  version: \"1.0.0\"\n" +
		"  labels:\n    component.aggrega.ai/type: server\nspec: {}\n")

	for _, shape := range slotShapes() {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			pin, _ := seedVerifiedEntitySlot(t, ws, cacheDir, slotURI, version, oci.MediaTypeComponent, body)
			shape.lay(t, oci.CachePath(ws, cacheDir, slotURI, version))
			cat, err := LoadAll("", t.TempDir())
			if err != nil {
				t.Fatalf("LoadAll: %v", err)
			}

			cascade := []struct {
				name  string
				uri   string
				reuse *ReuseVerification
			}{
				{name: "a bare reference held at its prior pin", uri: bare,
					reuse: &ReuseVerification{PriorPins: map[string]Pin{bare: {ResolvedVersion: version, Digest: pin}}}},
				{name: "a versioned reference with no prior pins", uri: slotURI},
			}
			for _, c := range cascade {
				res, err := cat.ClosureLookupWithReuse(nil, ws, cacheDir, nil, nil, c.reuse).ResolveCatalogEntity(context.Background(), c.uri, "component")
				if err != nil {
					t.Fatalf("%s: ResolveCatalogEntity: %v", c.name, err)
				}
				switch {
				case shape.complete && (res.NotFound || res.ResolutionPath != closure.ResolutionPathCache || res.Digest != pin):
					t.Errorf("%s: the complete slot was not served from the cache: %+v", c.name, res)
				case !shape.complete && !res.NotFound:
					t.Errorf("%s: a slot holding its content without a manifest was served as an entry: %+v", c.name, res)
				}
			}

			_, _, err = tier2ReadVerifiedWithManifest(ws, cacheDir, slotURI, pin, "")
			switch {
			case shape.complete && err != nil:
				t.Errorf("the verifying read refused the complete slot: %v", err)
			case !shape.complete && !errors.Is(err, oci.ErrFetchNotFound()):
				t.Errorf("the verifying read met a slot without its manifest with %v; want the miss oci.ErrFetchNotFound", err)
			}
		})
	}
}

// TestAPluginSlotWithoutItsManifestIsAMissToEveryReader holds the two readers of a
// plugin's cache slots to the same answer: the cached Plugin entity a verdict
// socket reads, and a plugin's content-addressed .wasm. A slot holding the content
// without its manifest is a miss to each — never an error, which would halt a pull
// the registry could have completed — and the complete slot is read.
func TestAPluginSlotWithoutItsManifestIsAMissToEveryReader(t *testing.T) {
	t.Parallel()
	const cacheDir = ".aggrega/cache"
	const entityURI, version = "pipeline-step/pulled-step:1.0.0", "1.0.0"
	const artifactURI = "pipeline-step/pulled-step"
	entityYAML := []byte(provenancePulledPluginYAML)
	wasm := []byte("\x00asm\x01\x00\x00\x00 manifest-rule artifact")

	store := memory.New()
	if _, err := oci.PushPluginManifest(context.Background(), store, version, entityYAML, wasm, nil); err != nil {
		t.Fatalf("PushPluginManifest: %v", err)
	}
	published, err := oci.FetchPluginEntity(context.Background(), store, version)
	if err != nil {
		t.Fatalf("FetchPluginEntity: %v", err)
	}

	for _, shape := range slotShapes() {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()

			if err := writeEntityToCacheSlot(ws, cacheDir, entityURI, version, published); err != nil {
				t.Fatalf("writeEntityToCacheSlot: %v", err)
			}
			shape.lay(t, oci.CachePath(ws, cacheDir, entityURI, version))
			_, _, ok, err := NewLazyLookup(ws, cacheDir, nil, nil).PluginFromCacheByURI(entityURI)
			switch {
			case err != nil:
				t.Errorf("the cached Plugin entity read halted instead of answering: %v", err)
			case ok != shape.complete:
				t.Errorf("the cached Plugin entity read reported a hit=%v; want %v", ok, shape.complete)
			}

			pin := writeCachedPluginArtifact(t, ws, cacheDir, artifactURI, wasm)
			shape.lay(t, oci.CachePath(ws, cacheDir, artifactURI, pin.Digest))
			l := &catalogLookup{catalog: &Catalog{}, workspaceRoot: ws, cacheDir: cacheDir}
			_, hit, err := l.readCachedPluginArtifact(artifactURI, pin.Digest)
			switch {
			case err != nil:
				t.Errorf("the cached .wasm read halted instead of answering: %v", err)
			case hit != shape.complete:
				t.Errorf("the cached .wasm read reported a hit=%v; want %v", hit, shape.complete)
			}
		})
	}
}

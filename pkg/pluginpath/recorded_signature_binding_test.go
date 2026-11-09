// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

// The moment a plugin's bytes are resolved holds the signature fact recorded for it
// to the artifact those bytes were read as: the manifest persisted beside them in
// the content-cache slot they came from, once the bytes are held to it. A plugin
// read from anywhere else comes with no manifest and its fact is held to the
// re-hash alone. Every bundle here is a real one, made by the toolchain's own keyed
// signer over a manifest digest, so what the resolver reads is what a pull caches.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// packedPlugin is a plugin artifact packed the way a publish packs it — its entity
// as the manifest's config and its .wasm as the layer — and the manifest that
// commits to the two.
type packedPlugin struct {
	wasm           []byte
	manifest       []byte
	manifestDigest string
}

func packPlugin(t *testing.T, name string, wasm []byte) packedPlugin {
	t.Helper()
	entity := "apiVersion: aggrega.ai/v1beta1\nkind: Plugin\nmetadata:\n  name: " + name +
		"\n  version: \"1.0.0\"\n  pluginType: governance\nspec:\n  abiVersion: v1beta1\n  exports:\n    - name: verdict_ir\n"
	store := memory.New()
	if _, err := oci.PushPluginManifest(context.Background(), store, "1.0.0", []byte(entity), wasm, nil); err != nil {
		t.Fatalf("pack %s: %v", name, err)
	}
	pulled, err := oci.FetchPluginEntity(context.Background(), store, "1.0.0")
	if err != nil {
		t.Fatalf("read back %s: %v", name, err)
	}
	return packedPlugin{wasm: wasm, manifest: pulled.ManifestBytes, manifestDigest: pulled.Digest}
}

// cacheBundleMadeFor signs madeFor with a fresh keyed signer, caches the bundle
// where a pull caches a verified one, and returns its digest.
func cacheBundleMadeFor(t *testing.T, ws, cacheDir, madeFor string) string {
	t.Helper()
	priv, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(priv, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	bundle, _, err := signer.Sign(context.Background(), madeFor)
	if err != nil {
		t.Fatalf("sign %s: %v", madeFor, err)
	}
	bundleDigest := digest.ContentDigest(bundle)
	put(t, oci.SignatureBundlePath(ws, cacheDir, bundleDigest), string(bundle))
	return bundleDigest
}

// TestTheLoadSiteHoldsARecordedSignatureToTheArtifactItWasMadeFor is the load
// site's table: one fault per row beside the controls, and the bounds stated as
// rows of their own so a bound that quietly widened would show up here.
func TestTheLoadSiteHoldsARecordedSignatureToTheArtifactItWasMadeFor(t *testing.T) {
	t.Parallel()
	const ref = "governance/binding-probe"
	const cacheDir = ".aggrega/cache"
	const recordedIn = "release.lock"
	own := packPlugin(t, "binding-probe", []byte("\x00asm the probe's own guest"))
	other := packPlugin(t, "another-plugin", []byte("\x00asm another plugin's guest"))

	type laid struct {
		bundleDigest string
		colocated    map[string]ColocatedDir
	}
	inCache := func(t *testing.T, ws string, withManifest []byte) {
		t.Helper()
		version := digest.ContentDigest(own.wasm)
		if err := oci.WriteCached(ws, cacheDir, oci.PluginArtifactKind(), ref, version, own.wasm); err != nil {
			t.Fatalf("lay the plugin's cache slot: %v", err)
		}
		if withManifest != nil {
			if err := oci.WriteCachedManifest(ws, cacheDir, oci.PluginArtifactKind(), ref, version, withManifest); err != nil {
				t.Fatalf("lay the manifest beside it: %v", err)
			}
		}
	}
	rows := []struct {
		name   string
		layout func(t *testing.T, ws string) laid
		// want is the condition the resolution must refuse with; nil means it must
		// resolve.
		want error
	}{
		{name: "the plugin's own signature, read from the cache: resolves",
			layout: func(t *testing.T, ws string) laid {
				inCache(t, ws, own.manifest)
				return laid{bundleDigest: cacheBundleMadeFor(t, ws, cacheDir, own.manifestDigest)}
			}},
		{name: "a signature made for another plugin, read from the cache: refused",
			layout: func(t *testing.T, ws string) laid {
				inCache(t, ws, own.manifest)
				return laid{bundleDigest: cacheBundleMadeFor(t, ws, cacheDir, other.manifestDigest)}
			}, want: oci.ErrSignatureForAnotherArtifact()},
		{name: "a signature made for another plugin, read from beside its entity: re-hashed and otherwise admitted",
			layout: func(t *testing.T, ws string) laid {
				dir := filepath.Join(ws, "catalog", "probe")
				put(t, filepath.Join(dir, PluginLeaf(ref)), string(own.wasm))
				return laid{bundleDigest: cacheBundleMadeFor(t, ws, cacheDir, other.manifestDigest),
					colocated: map[string]ColocatedDir{ref: {Dir: dir, Root: ws}}}
			}},
		{name: "a signature made for another plugin, from a cache slot holding no manifest: re-hashed and otherwise admitted",
			layout: func(t *testing.T, ws string) laid {
				inCache(t, ws, nil)
				return laid{bundleDigest: cacheBundleMadeFor(t, ws, cacheDir, other.manifestDigest)}
			}},
		{name: "another plugin's manifest beside the bytes, made to match the signature: refused, not used",
			layout: func(t *testing.T, ws string) laid {
				inCache(t, ws, other.manifest)
				return laid{bundleDigest: cacheBundleMadeFor(t, ws, cacheDir, other.manifestDigest)}
			}, want: oci.ErrFetchIntegrity()},
		{name: "the plugin's own signature, its cached bundle rewritten: refused as a mismatch",
			layout: func(t *testing.T, ws string) laid {
				inCache(t, ws, own.manifest)
				bundleDigest := cacheBundleMadeFor(t, ws, cacheDir, own.manifestDigest)
				put(t, oci.SignatureBundlePath(ws, cacheDir, bundleDigest), "rewritten after the pull")
				return laid{bundleDigest: bundleDigest}
			}, want: oci.ErrSignatureBundleDigestMismatch()},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ws := filepath.Join(t.TempDir(), "ws")
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			l := row.layout(t, ws)
			version := digest.ContentDigest(own.wasm)
			r := RecordedPins(PinMaps{
				Pins:          map[string]string{ref: version},
				PinVersions:   map[string]string{ref: version},
				BundleDigests: map[string]string{ref: l.bundleDigest},
				RecordedIn:    recordedIn,
			}).Resolver(ResolutionContext{WorkspaceRoot: ws, CacheDir: cacheDir, ColocatedDirs: l.colocated})

			got, err := r.Resolve(ref)
			if row.want == nil {
				if err != nil {
					t.Fatalf("the resolution must go ahead: %v", err)
				}
				if string(got.Bytes) != string(own.wasm) {
					t.Errorf("resolved %q, want the plugin's own bytes", got.Bytes)
				}
				return
			}
			if !errors.Is(err, row.want) {
				t.Fatalf("want %v, got %v", row.want, err)
			}
			if got.Bytes != nil {
				t.Errorf("a refused resolution handed back %q", got.Bytes)
			}
			if !strings.Contains(err.Error(), `plugin "`+ref+`" resolved from`) {
				t.Errorf("the refusal does not name the plugin and where its bytes came from: %v", err)
			}
			if errors.Is(row.want, oci.ErrSignatureForAnotherArtifact()) {
				for _, text := range []string{recordedIn + " records it", "the artifact read has manifest " + own.manifestDigest} {
					if !strings.Contains(err.Error(), text) {
						t.Errorf("the refusal does not carry %q: %v", text, err)
					}
				}
				if errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) {
					t.Errorf("a signature made for another plugin was reported as a damaged bundle: %v", err)
				}
			}
		})
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

// The remedy the load site's refusal of a recorded fact names. Bytes read from the
// content cache are an acquisition a pull repairs — it verifies again the signature
// of a cache-served plugin whose recorded fact the decision refuses, and rewrites a
// cached bundle that no longer matches its digest — so the refusal names that pull:
// the Release the pin source belongs to, or a placeholder where it names none, and
// the file it records the fact in. Bytes read from beside a Plugin entity are never
// asked about at a registry, so their refusal names no pull.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestTheLoadSiteNamesThePullThatRepairsARecordedFactItRefuses is the table: each
// condition on bytes read from the cache, with and without a Release on the pin
// source, and the colocated read that names no pull.
func TestTheLoadSiteNamesThePullThatRepairsARecordedFactItRefuses(t *testing.T) {
	t.Parallel()
	const ref = "governance/remedy-probe"
	const cacheDir = ".aggrega/cache"
	const recordedIn = "release.lock"
	own := packPlugin(t, "remedy-probe", []byte("\x00asm the remedy probe's guest"))
	other := packPlugin(t, "remedy-other", []byte("\x00asm another plugin's guest"))
	version := digest.ContentDigest(own.wasm)

	inCache := func(t *testing.T, ws string) {
		t.Helper()
		if err := oci.WriteCached(ws, cacheDir, oci.PluginArtifactKind(), ref, version, own.wasm); err != nil {
			t.Fatalf("lay the plugin's cache slot: %v", err)
		}
		if err := oci.WriteCachedManifest(ws, cacheDir, oci.PluginArtifactKind(), ref, version, own.manifest); err != nil {
			t.Fatalf("lay the manifest beside it: %v", err)
		}
	}
	removed := func(t *testing.T, ws string) string {
		d := cacheBundleMadeFor(t, ws, cacheDir, own.manifestDigest)
		if err := os.Remove(oci.SignatureBundlePath(ws, cacheDir, d)); err != nil {
			t.Fatal(err)
		}
		return d
	}
	rows := []struct {
		name    string
		release string
		// colocated reads the bytes from beside the entity rather than the cache.
		colocated bool
		fact      func(t *testing.T, ws string) string
		cond      error
		// remedy is the remedy text the refusal must carry; empty means none.
		remedy string
	}{
		{name: "the cached bundle removed, the Release named", release: "release.yaml", fact: removed,
			cond: oci.ErrSignatureBundleDigestMismatch(),
			remedy: "remediation: run `aggrega pull --release release.yaml` to verify the artifact's signature at its " +
				"registry again, record it in release.lock and cache the bundle it names"},
		{name: "a signature made for another plugin, the Release named", release: "release.yaml",
			fact: func(t *testing.T, ws string) string { return cacheBundleMadeFor(t, ws, cacheDir, other.manifestDigest) },
			cond: oci.ErrSignatureForAnotherArtifact(),
			remedy: "remediation: run `aggrega pull --release release.yaml` to verify the artifact's signature at its " +
				"registry again, record it in release.lock and cache the bundle it names"},
		{name: "the cached bundle removed, no Release on the pin source: a placeholder", fact: removed,
			cond: oci.ErrSignatureBundleDigestMismatch(),
			remedy: "remediation: run `aggrega pull --release <release>` to verify the artifact's signature at its " +
				"registry again, record it in release.lock and cache the bundle it names"},
		{name: "read from beside its entity, the cached bundle removed: no pull named", release: "release.yaml",
			colocated: true, fact: removed, cond: oci.ErrSignatureBundleDigestMismatch()},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ws := filepath.Join(t.TempDir(), "ws")
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			var colocated map[string]ColocatedDir
			if row.colocated {
				dir := filepath.Join(ws, "catalog", "probe")
				put(t, filepath.Join(dir, PluginLeaf(ref)), string(own.wasm))
				colocated = map[string]ColocatedDir{ref: {Dir: dir, Root: ws}}
			} else {
				inCache(t, ws)
			}
			r := RecordedPins(PinMaps{
				Pins:          map[string]string{ref: version},
				PinVersions:   map[string]string{ref: version},
				BundleDigests: map[string]string{ref: row.fact(t, ws)},
				RecordedIn:    recordedIn,
				Release:       row.release,
			}).Resolver(ResolutionContext{WorkspaceRoot: ws, CacheDir: cacheDir, ColocatedDirs: colocated})

			_, err := r.Resolve(ref)
			if !errors.Is(err, row.cond) {
				t.Fatalf("want %v, got %v", row.cond, err)
			}
			text := err.Error()
			if !strings.HasPrefix(text, `plugin "`+ref+`" resolved from `) {
				t.Errorf("the refusal does not open by naming the plugin and where its bytes came from:\n%s", text)
			}
			if row.remedy == "" {
				if strings.Contains(text, "remediation:") {
					t.Errorf("bytes no pull asks about name a remedy:\n%s", text)
				}
				return
			}
			if !strings.Contains(text, row.remedy) {
				t.Errorf("the refusal does not name the pull that repairs it:\nwant %s\n got %s", row.remedy, text)
			}
		})
	}
}

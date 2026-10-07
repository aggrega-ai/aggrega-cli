// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// TestTheCacheTierReadsAPluginOnlyFromWhereThePullWroteIt drives the plugin
// cascade's cache tier, and the signature bundle a signed guest is checked
// against, through the resolver every build and pull loads a plugin with, over a
// cache laid out one way per row: plainly inside the workspace; behind a cache
// directory that is a link leading out; with the slot's bytes, or the bundle, a
// link leading out; and beneath a cache directory the configuration names outside
// the workspace, plainly and with the bytes linking out of it.
//
// A refused row is held to the read hold's condition, to the link it names, and to
// what the cascade does next: it halts on the refusal rather than taking the slot
// for a miss — the plugin-not-found answer a miss draws would let a later tier
// stand in for bytes the cascade may not read — and it hands back no bytes.
func TestTheCacheTierReadsAPluginOnlyFromWhereThePullWroteIt(t *testing.T) {
	const ref = "governance/cache-probe"
	const version = "1.0.0"
	wasm := []byte("\x00asm cached guest bytes")
	bundle := []byte("a signature bundle")
	bundleDigest := digest.ContentDigest(bundle)

	type place struct{ ws, cacheDir, cache, elsewhere string }
	slot := func(t *testing.T, p place) string {
		t.Helper()
		dir, err := oci.CachePath(p.ws, p.cacheDir, ref, version)
		if err != nil {
			t.Fatal(err)
		}
		return dir
	}
	bundleAt := func(p place) string { return oci.SignatureBundlePath(p.ws, p.cacheDir, bundleDigest) }
	rows := []struct {
		name     string
		cacheDir string
		signed   bool
		// layout lays the cache out and returns the link a refusal must name, or ""
		// for a row that resolves from the cache.
		layout func(t *testing.T, p place) string
	}{
		{name: "control: the slot inside the workspace", cacheDir: ".aggrega/cache", layout: func(t *testing.T, p place) string {
			put(t, filepath.Join(slot(t, p), "blob"), string(wasm))
			return ""
		}},
		{name: "a cache directory that is a link leading out", cacheDir: ".aggrega/cache", layout: func(t *testing.T, p place) string {
			link(t, p.elsewhere, p.cache)
			put(t, filepath.Join(slot(t, p), "blob"), string(wasm))
			return p.cache
		}},
		{name: "the slot's bytes a link leading out", cacheDir: ".aggrega/cache", layout: func(t *testing.T, p place) string {
			put(t, filepath.Join(p.elsewhere, "guest.wasm"), string(wasm))
			at := filepath.Join(slot(t, p), "blob")
			link(t, filepath.Join(p.elsewhere, "guest.wasm"), at)
			return at
		}},
		{name: "control: a cache directory the configuration names outside the workspace", cacheDir: "../shared-cache", layout: func(t *testing.T, p place) string {
			put(t, filepath.Join(slot(t, p), "blob"), string(wasm))
			return ""
		}},
		{name: "the slot's bytes linking out of a cache directory named outside the workspace", cacheDir: "../shared-cache", layout: func(t *testing.T, p place) string {
			put(t, filepath.Join(p.elsewhere, "guest.wasm"), string(wasm))
			at := filepath.Join(slot(t, p), "blob")
			link(t, filepath.Join(p.elsewhere, "guest.wasm"), at)
			return at
		}},
		{name: "control: a signed guest whose bundle sits inside the workspace", cacheDir: ".aggrega/cache", signed: true, layout: func(t *testing.T, p place) string {
			put(t, filepath.Join(slot(t, p), "blob"), string(wasm))
			put(t, bundleAt(p), string(bundle))
			return ""
		}},
		{name: "a signed guest whose bundle is a link leading out", cacheDir: ".aggrega/cache", signed: true, layout: func(t *testing.T, p place) string {
			put(t, filepath.Join(slot(t, p), "blob"), string(wasm))
			put(t, filepath.Join(p.elsewhere, "bundle"), string(bundle))
			link(t, filepath.Join(p.elsewhere, "bundle"), bundleAt(p))
			return bundleAt(p)
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := filepath.Join(t.TempDir(), "ws")
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatal(err)
			}
			p := place{ws: ws, cacheDir: row.cacheDir, cache: filepath.Join(ws, row.cacheDir), elsewhere: t.TempDir()}
			want := row.layout(t, p)
			pins := PinMaps{
				Pins:        map[string]string{ref: digest.ContentDigest(wasm)},
				PinVersions: map[string]string{ref: version},
			}
			if row.signed {
				pins.BundleDigests = map[string]string{ref: bundleDigest}
			}
			r := RecordedPins(pins).Resolver(ResolutionContext{WorkspaceRoot: ws, CacheDir: row.cacheDir})

			got, err := r.Resolve(ref)
			if want == "" {
				if err != nil {
					t.Fatalf("refused a cache its root holds: %v", err)
				}
				if got.Tier != TierCache || string(got.Bytes) != string(wasm) {
					t.Errorf("resolved %q from %s, want the cached bytes from the cache tier", got.Bytes, got.Tier)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read hold's refusal, got %v", err)
			}
			if errors.Is(err, ErrPluginArtifactNotFound()) || errors.Is(err, oci.ErrFetchNotFound()) || errors.Is(err, ErrPluginBundleDigestMismatch()) {
				t.Errorf("the refusal must be returned as itself, not as a miss or a mismatch: %v", err)
			}
			if got.Bytes != nil {
				t.Errorf("a refused resolution handed back %q", got.Bytes)
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name the link %s: %v", want, err)
			}
		})
	}
}

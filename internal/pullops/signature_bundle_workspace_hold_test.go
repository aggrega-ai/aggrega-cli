// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// bundleCacheDir is the artifact cache directory as a workspace configures it by
// default: relative, and joined to the workspace root by the pull that writes a
// signature bundle and the build that reads it.
const bundleCacheDir = ".aggrega/cache"

// bundleWorkdir names the working directory one side of a row runs from, given
// the workspace root and a directory outside the workspace.
type bundleWorkdir func(t *testing.T, ws, elsewhere string) string

var (
	workFromRoot    bundleWorkdir = func(_ *testing.T, ws, _ string) string { return ws }
	workFromOutside bundleWorkdir = func(_ *testing.T, _, elsewhere string) string { return elsewhere }
	workFromBeneath bundleWorkdir = func(t *testing.T, ws, _ string) string {
		dir := filepath.Join(ws, "releases", "staging")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
)

// bundleFile is where a bundle with digest d must land beneath the workspace at
// ws. It is spelled here, rather than asked of the helper that spells it for the
// product, so a helper that stops joining the workspace root cannot agree with
// itself.
func bundleFile(ws, d string) string {
	return filepath.Join(ws, bundleCacheDir, "signatures", strings.Replace(d, ":", "-", 1)+".bundle")
}

// TestASignatureBundleIsCachedBeneathTheWorkspaceAndHeldToIt drives the write of
// a pull's signature bundle from three working directories — the workspace root,
// a directory outside the workspace and one beneath it — and over three layouts
// in which a part of the bundle's path inside the workspace is a link leading
// out: the cache directory, its signatures directory, and the bundle itself as a
// link leading nowhere.
//
// A written row is held to the place the bundle must land and to its bytes, and
// to the working directory gaining no cache of its own. A refused row is held to
// the condition and to the directory outside the workspace being exactly as it
// was before the write: nothing created, nothing rewritten.
func TestASignatureBundleIsCachedBeneathTheWorkspaceAndHeldToIt(t *testing.T) {
	bundle := []byte(`{"signature-bundle":"held-to-the-workspace"}`)
	bundleDigest := digest.ContentDigest(bundle)
	rows := []struct {
		name    string
		workdir bundleWorkdir
		layout  func(t *testing.T, ws, outside string)
		refused bool
	}{
		{name: "control: written from the workspace root", workdir: workFromRoot},
		{name: "written from a directory outside the workspace", workdir: workFromOutside},
		{name: "written from a directory beneath the workspace", workdir: workFromBeneath},
		{name: "a cache directory that is a link leading out", workdir: workFromOutside, refused: true,
			layout: func(t *testing.T, ws, outside string) {
				target := filepath.Join(outside, "cache-target")
				mustMkdirBundle(t, target)
				mustMkdirBundle(t, filepath.Join(ws, ".aggrega"))
				symlinkOrSkipTrust(t, target, filepath.Join(ws, bundleCacheDir))
			}},
		{name: "a signatures directory that is a link leading out", workdir: workFromOutside, refused: true,
			layout: func(t *testing.T, ws, outside string) {
				target := filepath.Join(outside, "signatures-target")
				mustMkdirBundle(t, target)
				mustMkdirBundle(t, filepath.Join(ws, bundleCacheDir))
				symlinkOrSkipTrust(t, target, filepath.Join(ws, bundleCacheDir, "signatures"))
			}},
		{name: "a bundle that is a link leading nowhere outside", workdir: workFromOutside, refused: true,
			layout: func(t *testing.T, ws, outside string) {
				mustMkdirBundle(t, filepath.Join(ws, bundleCacheDir, "signatures"))
				symlinkOrSkipTrust(t, filepath.Join(outside, "absent.bundle"), bundleFile(ws, bundleDigest))
			}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws, outside, elsewhere := t.TempDir(), t.TempDir(), t.TempDir()
			if row.layout != nil {
				row.layout(t, ws, outside)
			}
			before := entriesBeneath(t, outside)
			workdir := row.workdir(t, ws, elsewhere)
			t.Chdir(workdir)

			err := cacheBundleBlob(ws, bundleCacheDir, bundleDigest, bundle)

			if row.refused {
				if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
					t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
				}
				if after := entriesBeneath(t, outside); !slices.Equal(before, after) {
					t.Errorf("the directory outside the workspace changed:\n before %q\n after  %q", before, after)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a bundle the workspace holds: %v", err)
			}
			got, rerr := os.ReadFile(bundleFile(ws, bundleDigest))
			if rerr != nil {
				t.Fatalf("the bundle is not beneath the workspace root: %v", rerr)
			}
			if !bytes.Equal(got, bundle) {
				t.Errorf("the cached bundle differs from the bundle written:\n got  %q\n want %q", got, bundle)
			}
			if workdir != ws {
				if _, serr := os.Lstat(filepath.Join(workdir, ".aggrega")); !os.IsNotExist(serr) {
					t.Errorf("a cache was written beneath the working directory %s (stat: %v)", workdir, serr)
				}
			}
		})
	}
}

// TestASignatureBundleBeneathACacheDirectoryOutsideTheWorkspaceIsHeldToIt drives
// the same write over a cache directory the configuration names climbing out of
// the workspace, one shape per row beside a control writing plainly beneath it.
//
// The directory is a root of its own: a signatures subtree beneath it that is a
// link leading out of it is refused, and the place the link leads to is left
// exactly as it was; the directory itself being a link is honoured where it was
// named, and the bundle is written where it leads.
func TestASignatureBundleBeneathACacheDirectoryOutsideTheWorkspaceIsHeldToIt(t *testing.T) {
	const cacheDir = "../shared-cache"
	bundle := []byte(`{"signature-bundle":"held-to-the-cache-directory"}`)
	bundleDigest := digest.ContentDigest(bundle)
	rows := []struct {
		name    string
		layout  func(t *testing.T, cache, elsewhere string)
		refused bool
	}{
		{name: "control: a cache directory outside the workspace", layout: func(*testing.T, string, string) {}},
		{name: "a signatures subtree that is a link leading out of it is refused", refused: true,
			layout: func(t *testing.T, cache, elsewhere string) {
				mustMkdirBundle(t, cache)
				symlinkOrSkipTrust(t, elsewhere, filepath.Join(cache, "signatures"))
			}},
		{name: "the cache directory itself a link is honoured where it was named",
			layout: func(t *testing.T, cache, elsewhere string) {
				symlinkOrSkipTrust(t, elsewhere, cache)
			}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws := filepath.Join(t.TempDir(), "ws")
			elsewhere := t.TempDir()
			mustMkdirBundle(t, ws)
			cache := filepath.Join(ws, cacheDir)
			row.layout(t, cache, elsewhere)
			before := entriesBeneath(t, elsewhere)

			err := cacheBundleBlob(ws, cacheDir, bundleDigest, bundle)
			if row.refused {
				if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
					t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
				}
				if after := entriesBeneath(t, elsewhere); !slices.Equal(before, after) {
					t.Errorf("the place the link leads to changed:\n before %q\n after  %q", before, after)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused a bundle its root holds: %v", err)
			}
			got, rerr := os.ReadFile(filepath.Join(cache, "signatures", strings.Replace(bundleDigest, ":", "-", 1)+".bundle"))
			if rerr != nil || !bytes.Equal(got, bundle) {
				t.Errorf("the bundle is not beneath the cache directory: %q (%v)", got, rerr)
			}
		})
	}
}

// TestTheBuildReadsTheSignatureBundleThePullCachedFromAnyDirectory caches a
// plugin's bytes and its signature bundle the way a pull does, then resolves the
// plugin the way a build does — through the cascade, holding the bundle to its
// recorded digest — with the write and the read each made from a working
// directory of its own: the workspace root, a directory outside the workspace, or
// one beneath it.
//
// A row whose bundle is intact must resolve from the cache. The row that rewrites
// the cached bundle after the write must be refused with the bundle-digest halt
// naming the digest of the bytes it rewrote, which shows the read reached the file
// the write produced: a read that found no bundle is refused with the same halt,
// and names no such digest.
func TestTheBuildReadsTheSignatureBundleThePullCachedFromAnyDirectory(t *testing.T) {
	const uri = "hub.example.invalid/plugin/governance/bundle-probe"
	const version = "1.0.0"
	wasm := []byte("\x00asm-bytes-the-pull-cached")
	bundle := []byte(`{"signature-bundle":"read-from-any-directory"}`)
	bundleDigest := digest.ContentDigest(bundle)
	tampered := []byte(`{"signature-bundle":"rewritten-after-the-pull"}`)
	rows := []struct {
		name      string
		writeFrom bundleWorkdir
		readFrom  bundleWorkdir
		tamper    bool
	}{
		{name: "control: written and read from the workspace root", writeFrom: workFromRoot, readFrom: workFromRoot},
		{name: "written from the workspace root, read from outside it", writeFrom: workFromRoot, readFrom: workFromOutside},
		{name: "written from outside the workspace, read from beneath it", writeFrom: workFromOutside, readFrom: workFromBeneath},
		{name: "written from beneath the workspace, read from outside it", writeFrom: workFromBeneath, readFrom: workFromOutside},
		{name: "a bundle rewritten after the pull, read from outside the workspace", writeFrom: workFromRoot,
			readFrom: workFromOutside, tamper: true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ws, elsewhere := t.TempDir(), t.TempDir()

			t.Chdir(row.writeFrom(t, ws, elsewhere))
			if err := oci.WriteCached(ws, bundleCacheDir, uri, version, wasm); err != nil {
				t.Fatalf("cache the plugin bytes: %v", err)
			}
			if err := cacheBundleBlob(ws, bundleCacheDir, bundleDigest, bundle); err != nil {
				t.Fatalf("cache the signature bundle: %v", err)
			}
			if row.tamper {
				if err := os.WriteFile(bundleFile(ws, bundleDigest), tampered, 0o644); err != nil {
					t.Fatalf("rewrite the cached bundle: %v", err)
				}
			}

			t.Chdir(row.readFrom(t, ws, elsewhere))
			resolver := pluginpath.RecordedPins(pluginpath.PinMaps{
				Pins:          map[string]string{uri: digest.ContentDigest(wasm)},
				PinVersions:   map[string]string{uri: version},
				BundleDigests: map[string]string{uri: bundleDigest},
			}).Resolver(pluginpath.ResolutionContext{WorkspaceRoot: ws, CacheDir: bundleCacheDir})
			got, err := resolver.Resolve(uri)

			if row.tamper {
				if !errors.Is(err, pluginpath.ErrPluginBundleDigestMismatch()) {
					t.Fatalf("want the bundle-digest halt, got %v", err)
				}
				if !strings.Contains(err.Error(), digest.ContentDigest(tampered)) {
					t.Errorf("the halt does not name the digest of the rewritten bundle, so the read did not reach it: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("the build did not find what the pull cached: %v", err)
			}
			if got.Tier != pluginpath.TierCache || !bytes.Equal(got.Bytes, wasm) {
				t.Errorf("resolved from %v with %q; want the cached bytes", got.Tier, got.Bytes)
			}
		})
	}
}

// entriesBeneath lists every entry beneath dir by its path relative to dir, a
// link with where it points, in the walk's lexical order — so two listings are
// equal only when nothing beneath dir was created, removed or repointed between
// them.
func entriesBeneath(t *testing.T, dir string) []string {
	t.Helper()
	var entries []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, lerr := os.Readlink(p)
			if lerr != nil {
				return lerr
			}
			rel += " -> " + target
		}
		entries = append(entries, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func mustMkdirBundle(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

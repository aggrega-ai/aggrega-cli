// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/provindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
)

const cacheTestCacheDir = ".aggrega/cache"

// cacheFixture is an on-disk cache state built under a temp workspace plus
// temp global caches, with a lockfile pinning a known subset of the digests.
type cacheFixture struct {
	loc cacheLocations

	refEntitySlot   string
	unrefEntitySlot string
	refSourceSlot   string
	unrefSourceSlot string
	refInputBlob    string
	unrefInputBlob  string
	refDescribe     string
	unrefDescribe   string
	compileModule   string
	compileLock     string
}

// writeEntitySlot writes an entity cache slot with the given blob bytes and
// returns the slot dir and the blob's content digest.
func writeEntitySlot(t *testing.T, wsRoot, uri, version string, blob []byte) (string, string) {
	t.Helper()
	if err := oci.WriteCached(wsRoot, cacheTestCacheDir, uri, version, blob); err != nil {
		t.Fatalf("WriteCached: %v", err)
	}
	return oci.CachePath(wsRoot, cacheTestCacheDir, uri, version), digest.ContentDigest(blob)
}

// writeSourceSlot writes a source-artifact cache slot (blob.tgz + metadata.yaml)
// and returns the slot dir. The recorded digest is dg.
func writeSourceSlot(t *testing.T, wsRoot, kind, srcURL, version, dg string, blob []byte) string {
	t.Helper()
	slot, err := cacheslot.Slot(wsRoot, cacheTestCacheDir, kind, srcURL, version, dg)
	if err != nil {
		t.Fatalf("Slot: %v", err)
	}
	if err := cacheslot.WriteBlobAndMetadata(slot, blob, kind, srcURL, version, dg, "application/x-yaml", nil); err != nil {
		t.Fatalf("WriteBlobAndMetadata: %v", err)
	}
	return slot
}

// writeInputBlob writes an external-input store blob named by its hex digest.
func writeInputBlob(t *testing.T, wsRoot, hexDigest string, blob []byte) string {
	t.Helper()
	dir := filepath.Join(externalinputs.StoreRoot(filepath.Join(wsRoot, cacheTestCacheDir)), "sha256")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir inputs: %v", err)
	}
	p := filepath.Join(dir, hexDigest)
	if err := os.WriteFile(p, blob, 0o644); err != nil {
		t.Fatalf("write input blob: %v", err)
	}
	return p
}

func writeDescribe(t *testing.T, describeDir, hexDigest string) string {
	t.Helper()
	if err := os.MkdirAll(describeDir, 0o755); err != nil {
		t.Fatalf("mkdir describe: %v", err)
	}
	p := filepath.Join(describeDir, hexDigest+".json")
	if err := os.WriteFile(p, []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatalf("write describe: %v", err)
	}
	return p
}

func newCacheFixture(t *testing.T) cacheFixture {
	t.Helper()
	wsRoot := t.TempDir()
	compileDir := t.TempDir()
	describeDir := t.TempDir()

	// Entity slots: one referenced, one not.
	refBlob := []byte("referenced-entity-artifact-bytes")
	unrefBlob := []byte("orphan-entity-artifact-bytes")
	refEntitySlot, refEntityDigest := writeEntitySlot(t, wsRoot, "oci://hub.aggrega.ai/component/core/api", "1.0.0", refBlob)
	unrefEntitySlot, _ := writeEntitySlot(t, wsRoot, "oci://hub.aggrega.ai/component/core/orphan", "9.9.9", unrefBlob)

	// Source-artifact slots.
	refSrcDigest := digest.ContentDigest([]byte("ref-src"))
	unrefSrcDigest := digest.ContentDigest([]byte("unref-src"))
	refSourceSlot := writeSourceSlot(t, wsRoot, "helm-oci-chart", "oci://hub.aggrega.ai/charts/api", "2.0.0", refSrcDigest, []byte("ref-src"))
	unrefSourceSlot := writeSourceSlot(t, wsRoot, "helm-oci-chart", "oci://hub.aggrega.ai/charts/orphan", "2.0.0", unrefSrcDigest, []byte("unref-src"))

	// External-input blobs (filename = hex digest).
	refInputHex := normalizeDigestHex(digest.ContentDigest([]byte("ref-input")))
	unrefInputHex := normalizeDigestHex(digest.ContentDigest([]byte("unref-input")))
	refInputBlob := writeInputBlob(t, wsRoot, refInputHex, []byte("ref-input"))
	unrefInputBlob := writeInputBlob(t, wsRoot, unrefInputHex, []byte("unref-input"))

	// Describe cache: referenced == a lockfile plugin digest; unreferenced not.
	pluginDigest := digest.ContentDigest([]byte("plugin-guest-bytes"))
	pluginHex := normalizeDigestHex(pluginDigest)
	unrefDescHex := normalizeDigestHex(digest.ContentDigest([]byte("orphan-guest")))
	refDescribe := writeDescribe(t, describeDir, pluginHex)
	unrefDescribe := writeDescribe(t, describeDir, unrefDescHex)

	// Compiled-module cache: one module file plus a transient compile-lock sidecar.
	modDir := filepath.Join(compileDir, "wazero-1.0-amd64-linux")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatalf("mkdir module: %v", err)
	}
	compileModule := filepath.Join(modDir, "abcdef0123")
	if err := os.WriteFile(compileModule, []byte("compiled-module-bytes"), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	compileLock := filepath.Join(compileDir, ".compile-"+pluginHex+".lock")
	if err := os.WriteFile(compileLock, nil, 0o644); err != nil {
		t.Fatalf("write compile lock: %v", err)
	}

	// Lockfile pinning the referenced digests only.
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "release.yaml",
		GeneratedAt: "2026-01-01T00:00:00Z",
		Entities: []lockfile.Entity{{
			URI: "oci://hub.aggrega.ai/component/core/api", ResolvedVersion: "1.0.0",
			Digest: refEntityDigest, ReferencedFrom: []string{"release.yaml"},
		}},
		SourceArtifacts: []lockfile.SourceArtifact{{
			Kind: "helm-oci-chart", URL: "oci://hub.aggrega.ai/charts/api", PinnedVersion: "2.0.0",
			Digest: refSrcDigest, Mediatype: "application/x-yaml",
			ReferencedBy: []lockfile.SourceArtifactReference{{Entity: "recipe", URI: "oci://hub.aggrega.ai/recipe/core/api", Field: "spec.source"}},
		}},
		ExternalInputs: []lockfile.ExternalInput{{
			URL: "https://example.test/ref", Sha256: refInputHex, Size: int64(len("ref-input")),
			RequiredBy: []string{"release.yaml"},
		}},
		Plugins: []lockfile.Plugin{{
			URI: "oci://hub.aggrega.ai/plugins/core/helm", ResolvedVersion: pluginDigest,
			Digest: pluginDigest, ReferencedFrom: []string{"release.yaml"},
		}},
	}
	lockPath := lockfile.PathFor(filepath.Join(wsRoot, "release.yaml"))
	if err := lockfile.WriteLockfile(lockPath, lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	// Confirm the fixture lockfile round-trips through the loader prune uses.
	if _, _, err := lockfile.LoadLockfile(lockPath); err != nil {
		t.Fatalf("fixture lockfile does not load: %v", err)
	}

	return cacheFixture{
		loc: cacheLocations{
			wsRoot: wsRoot, cacheDir: cacheTestCacheDir,
			compileDir: compileDir, describeDir: describeDir,
		},
		refEntitySlot: refEntitySlot, unrefEntitySlot: unrefEntitySlot,
		refSourceSlot: refSourceSlot, unrefSourceSlot: unrefSourceSlot,
		refInputBlob: refInputBlob, unrefInputBlob: unrefInputBlob,
		refDescribe: refDescribe, unrefDescribe: unrefDescribe,
		compileModule: compileModule, compileLock: compileLock,
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// writeDocsBundleSlot writes a documentation-bundle cache slot (a blob +
// manifest under docs-bundles/<encoded-ref>/<version>/) and returns the slot
// dir. The enumeration only walks the slot directory tree, so the file
// contents are placeholders.
func writeDocsBundleSlot(t *testing.T, wsRoot, ref, version string) string {
	t.Helper()
	slot := filepath.Join(docscache.DocsBundlesRoot(wsRoot, cacheTestCacheDir), url.PathEscape(ref), version)
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatalf("mkdir docs slot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(slot, "blob"), []byte("docs-bundle-bytes"), 0o644); err != nil {
		t.Fatalf("write docs blob: %v", err)
	}
	if err := os.WriteFile(filepath.Join(slot, "manifest"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write docs manifest: %v", err)
	}
	return slot
}

// TestCacheStatusListsDocsBundleClass proves the documentation-bundle cache is
// enumerated as its own class in status, with the slot's decoded reference and
// version as a deterministic label.
func TestCacheStatusListsDocsBundleClass(t *testing.T) {
	fx := newCacheFixture(t)
	slot := writeDocsBundleSlot(t, fx.loc.wsRoot, "reg.example.com/docs/aggrega-docs", "1.0.0")

	var a, b bytes.Buffer
	if err := runCacheStatus(&a, fx.loc); err != nil {
		t.Fatalf("status a: %v", err)
	}
	if err := runCacheStatus(&b, fx.loc); err != nil {
		t.Fatalf("status b: %v", err)
	}
	if a.String() != b.String() {
		t.Fatalf("status not byte-identical:\n--- a ---\n%s\n--- b ---\n%s", a.String(), b.String())
	}
	if !bytes.Contains(a.Bytes(), []byte("workspace docs-bundle cache:")) {
		t.Fatalf("status missing docs-bundle class header:\n%s", a.String())
	}
	if !bytes.Contains(a.Bytes(), []byte("reg.example.com/docs/aggrega-docs@1.0.0")) {
		t.Fatalf("status missing docs-bundle element label:\n%s", a.String())
	}
	if !exists(slot) {
		t.Fatalf("docs-bundle slot vanished")
	}
	t.Logf("status output:\n%s", a.String())
}

// TestCachePruneRetainsDocsBundle proves prune leaves the documentation-bundle
// cache untouched — it is not lockfile-referenced, so it is reported as
// not-prune-eligible rather than reclaimed — while clear removes it.
func TestCachePruneRetainsDocsBundle(t *testing.T) {
	fx := newCacheFixture(t)
	slot := writeDocsBundleSlot(t, fx.loc.wsRoot, "reg.example.com/docs/aggrega-docs", "1.0.0")

	var out bytes.Buffer
	if err := runCachePrune(&out, fx.loc); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if !exists(slot) {
		t.Fatalf("prune removed a docs-bundle slot it cannot map to a lockfile:\n%s", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("workspace docs-bundle cache element(s) not prune-eligible")) {
		t.Fatalf("prune did not report the docs-bundle class as out-of-scope:\n%s", out.String())
	}
	if err := runCacheClear(&bytes.Buffer{}, fx.loc); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if exists(slot) {
		t.Fatalf("clear did not reclaim the docs-bundle slot")
	}
}

func TestCacheStatusDeterministic(t *testing.T) {
	fx := newCacheFixture(t)
	var a, b bytes.Buffer
	if err := runCacheStatus(&a, fx.loc); err != nil {
		t.Fatalf("status a: %v", err)
	}
	if err := runCacheStatus(&b, fx.loc); err != nil {
		t.Fatalf("status b: %v", err)
	}
	if a.String() != b.String() {
		t.Fatalf("status not byte-identical:\n--- a ---\n%s\n--- b ---\n%s", a.String(), b.String())
	}
	// The compile-lock sidecar is not an element; the module file is.
	if bytes.Contains(a.Bytes(), []byte(".compile-")) {
		t.Fatalf("status listed a transient compile-lock sidecar:\n%s", a.String())
	}
	if !bytes.Contains(a.Bytes(), []byte("elements,")) {
		t.Fatalf("status missing summary line:\n%s", a.String())
	}
	t.Logf("status output:\n%s", a.String())
}

func TestCachePruneSelective(t *testing.T) {
	fx := newCacheFixture(t)
	var out bytes.Buffer
	if err := runCachePrune(&out, fx.loc); err != nil {
		t.Fatalf("prune: %v", err)
	}
	t.Logf("prune output:\n%s", out.String())

	// Referenced entries retained.
	for _, p := range []string{fx.refEntitySlot, fx.refSourceSlot, fx.refInputBlob, fx.refDescribe} {
		if !exists(p) {
			t.Errorf("prune removed a referenced entry: %s", p)
		}
	}
	// Unreferenced entries removed.
	for _, p := range []string{fx.unrefEntitySlot, fx.unrefSourceSlot, fx.unrefInputBlob, fx.unrefDescribe} {
		if exists(p) {
			t.Errorf("prune retained an unreferenced entry: %s", p)
		}
	}
	// The compiled-module store is never touched by prune.
	if !exists(fx.compileModule) {
		t.Errorf("prune removed a compiled module: %s", fx.compileModule)
	}
	if !exists(fx.compileLock) {
		t.Errorf("prune removed a compile-lock sidecar: %s", fx.compileLock)
	}
}

// TestCachePruneReportsOutOfScope asserts prune reports the compiled-module
// cache as out-of-scope — named, counted, and sized — so the retained count is
// not read against the full cache set, and clear is named as the reclaiming
// verb. The fixture holds one compiled module the prune must not touch.
func TestCachePruneReportsOutOfScope(t *testing.T) {
	fx := newCacheFixture(t)
	var out bytes.Buffer
	if err := runCachePrune(&out, fx.loc); err != nil {
		t.Fatalf("prune: %v", err)
	}
	s := out.String()
	t.Logf("prune output:\n%s", s)

	if !bytes.Contains(out.Bytes(), []byte("compiled-module cache element(s) not prune-eligible")) {
		t.Errorf("prune did not name the compiled-module cache as out-of-scope:\n%s", s)
	}
	// One compiled module is present in the fixture; the line reports its count.
	if !bytes.Contains(out.Bytes(), []byte("1 compiled-module cache element(s) not prune-eligible")) {
		t.Errorf("prune did not count the out-of-scope compiled module:\n%s", s)
	}
	// The aggregate size and the reclaiming verb are both present on the line.
	if !bytes.Contains(out.Bytes(), []byte("~")) || !bytes.Contains(out.Bytes(), []byte("reclaim with 'aggrega cache clear'")) {
		t.Errorf("prune out-of-scope line missing size or clear guidance:\n%s", s)
	}
}

func TestCachePruneNoWorkspace(t *testing.T) {
	loc := cacheLocations{compileDir: t.TempDir(), describeDir: t.TempDir()}
	err := runCachePrune(&bytes.Buffer{}, loc)
	if err == nil {
		t.Fatalf("expected a loud error with no workspace")
	}
}

func TestCachePruneNoLockfile(t *testing.T) {
	loc := cacheLocations{wsRoot: t.TempDir(), cacheDir: cacheTestCacheDir}
	err := runCachePrune(&bytes.Buffer{}, loc)
	if err == nil {
		t.Fatalf("expected a loud error with no lockfile")
	}
}

func TestCacheClearWipesAll(t *testing.T) {
	fx := newCacheFixture(t)
	var out bytes.Buffer
	if err := runCacheClear(&out, fx.loc); err != nil {
		t.Fatalf("clear: %v", err)
	}
	t.Logf("clear output:\n%s", out.String())

	roots := []string{
		fx.loc.entitiesRoot(), fx.loc.sourceArtifactsRoot(), fx.loc.inputsRoot(),
		fx.loc.compileDir, fx.loc.describeDir,
	}
	for _, r := range roots {
		if exists(r) {
			t.Errorf("clear left a cache subtree in place: %s", r)
		}
	}
	// Everything is gone, including the compiled-module store clear reclaims.
	if exists(fx.compileModule) {
		t.Errorf("clear left a compiled module: %s", fx.compileModule)
	}
}

// TestCacheClearConfirm exercises the confirmation gate that fronts the
// irreversible cache wipe: --yes bypasses it, a non-interactive stdin without
// --yes refuses (rather than blocking), and an interactive [y/N] answer decides.
func TestCacheClearConfirm(t *testing.T) {
	orig := isTerminal
	t.Cleanup(func() { isTerminal = orig })

	// --yes bypasses regardless of terminal state, without reading stdin.
	isTerminal = func() bool { return false }
	var out bytes.Buffer
	if err := confirmCacheClear(&out, strings.NewReader(""), true); err != nil {
		t.Fatalf("--yes should bypass the prompt, got: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("--yes should print no prompt, got: %q", out.String())
	}

	// Non-interactive stdin without --yes refuses rather than hanging.
	out.Reset()
	if err := confirmCacheClear(&out, strings.NewReader(""), false); err == nil {
		t.Fatal("non-interactive stdin without --yes should refuse, got nil error")
	}

	// Interactive terminal: an explicit yes consents.
	isTerminal = func() bool { return true }
	out.Reset()
	if err := confirmCacheClear(&out, strings.NewReader("y\n"), false); err != nil {
		t.Fatalf("interactive 'y' should consent, got: %v", err)
	}
	if !strings.Contains(out.String(), "[y/N]") {
		t.Errorf("interactive path should print a [y/N] prompt, got: %q", out.String())
	}

	// Interactive terminal: anything else aborts.
	out.Reset()
	if err := confirmCacheClear(&out, strings.NewReader("n\n"), false); err == nil {
		t.Fatal("interactive 'n' should abort, got nil error")
	}
	out.Reset()
	if err := confirmCacheClear(&out, strings.NewReader("\n"), false); err == nil {
		t.Fatal("interactive empty reply should abort (default No), got nil error")
	}
}

// TestCacheEntitySlotEncoding guards that an entity slot's display label
// round-trips the URL-encoded on-disk directory name back to a readable URI.
func TestCacheEntitySlotEncoding(t *testing.T) {
	fx := newCacheFixture(t)
	els := enumEntities(fx.loc.entitiesRoot())
	if len(els) == 0 {
		t.Fatal("no entity elements enumerated")
	}
	for _, el := range els {
		if _, err := url.PathUnescape(el.label); err != nil {
			t.Errorf("entity label not decodable: %q", el.label)
		}
	}
}

// writeCompileLeaf writes a synthetic compiled-module cache leaf of the given
// byte size and modification time, creating dir if needed, and returns its path.
func writeCompileLeaf(t *testing.T, dir, name string, size int, mtime time.Time) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir compile dir: %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), size), 0o644); err != nil {
		t.Fatalf("write compile leaf: %v", err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return p
}

// TestCacheEvictOverCap evicts oldest-compiled leaves first until the cache
// fits a cap below its total, retaining the newest leaves and reporting the
// reclaimed/retained accounting.
func TestCacheEvictOverCap(t *testing.T) {
	compileDir := t.TempDir()
	modDir := filepath.Join(compileDir, "wazero-1.0-amd64-linux")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	old1 := writeCompileLeaf(t, modDir, "leaf-old-1", 100, base)
	old2 := writeCompileLeaf(t, modDir, "leaf-old-2", 100, base.Add(time.Hour))
	newest := writeCompileLeaf(t, modDir, "leaf-new", 100, base.Add(2*time.Hour))

	loc := cacheLocations{compileDir: compileDir}
	var out bytes.Buffer
	// Total is 300 B; cap 150 B forces eviction of the two oldest leaves.
	if err := runCacheEvict(&out, loc, 150); err != nil {
		t.Fatalf("evict: %v", err)
	}
	s := out.String()
	t.Logf("evict output:\n%s", s)

	if exists(old1) || exists(old2) {
		t.Errorf("evict retained an oldest leaf that should be gone:\n%s", s)
	}
	if !exists(newest) {
		t.Errorf("evict removed the newest leaf that should survive: %s", newest)
	}
	if !bytes.Contains(out.Bytes(), []byte("evicted 2 leaf(s), 200 B reclaimed; 100 B retained (cap 150 B)")) {
		t.Errorf("evict summary line wrong:\n%s", s)
	}
}

// TestCacheEvictIdempotent asserts a second eviction at the same cap, after the
// first brought the cache under cap, removes nothing.
func TestCacheEvictIdempotent(t *testing.T) {
	compileDir := t.TempDir()
	modDir := filepath.Join(compileDir, "wazero-1.0-amd64-linux")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	writeCompileLeaf(t, modDir, "leaf-old", 100, base)
	newest := writeCompileLeaf(t, modDir, "leaf-new", 100, base.Add(time.Hour))

	loc := cacheLocations{compileDir: compileDir}
	if err := runCacheEvict(&bytes.Buffer{}, loc, 150); err != nil {
		t.Fatalf("first evict: %v", err)
	}
	var out bytes.Buffer
	if err := runCacheEvict(&out, loc, 150); err != nil {
		t.Fatalf("second evict: %v", err)
	}
	s := out.String()
	t.Logf("second evict output:\n%s", s)

	if !exists(newest) {
		t.Errorf("second evict removed the surviving leaf: %s", newest)
	}
	if !bytes.Contains(out.Bytes(), []byte("nothing evicted")) {
		t.Errorf("second evict was not a no-op:\n%s", s)
	}
}

// TestCacheEvictUnderCap asserts a cap at or above the total removes nothing.
func TestCacheEvictUnderCap(t *testing.T) {
	compileDir := t.TempDir()
	modDir := filepath.Join(compileDir, "wazero-1.0-amd64-linux")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := writeCompileLeaf(t, modDir, "leaf-a", 100, base)
	b := writeCompileLeaf(t, modDir, "leaf-b", 100, base.Add(time.Hour))

	loc := cacheLocations{compileDir: compileDir}
	var out bytes.Buffer
	// Total is 200 B; cap 200 B is exactly at the total, so nothing is evicted.
	if err := runCacheEvict(&out, loc, 200); err != nil {
		t.Fatalf("evict: %v", err)
	}
	s := out.String()
	t.Logf("evict output:\n%s", s)

	if !exists(a) || !exists(b) {
		t.Errorf("under-cap evict removed a leaf:\n%s", s)
	}
	if !bytes.Contains(out.Bytes(), []byte("compiled-module cache is 200 B, within cap 200 B; nothing evicted")) {
		t.Errorf("under-cap no-op line wrong:\n%s", s)
	}
}

// TestCacheEvictTieBreak asserts that for two leaves with EQUAL modification
// time the deterministic (mtime asc, name asc) order evicts the leaf with the
// lexicographically smaller name first.
func TestCacheEvictTieBreak(t *testing.T) {
	compileDir := t.TempDir()
	modDir := filepath.Join(compileDir, "wazero-1.0-amd64-linux")
	equal := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := writeCompileLeaf(t, modDir, "leaf-a", 100, equal)
	b := writeCompileLeaf(t, modDir, "leaf-b", 100, equal)

	loc := cacheLocations{compileDir: compileDir}
	var out bytes.Buffer
	// Total is 200 B; cap 100 B forces exactly one eviction. Equal mtimes break
	// on name ascending, so "leaf-a" is evicted first and "leaf-b" survives.
	if err := runCacheEvict(&out, loc, 100); err != nil {
		t.Fatalf("evict: %v", err)
	}
	s := out.String()
	t.Logf("evict output:\n%s", s)

	if exists(a) {
		t.Errorf("tie-break evicted the wrong leaf: leaf-a should be gone:\n%s", s)
	}
	if !exists(b) {
		t.Errorf("tie-break removed leaf-b that should survive: %s", b)
	}
	if !bytes.Contains(out.Bytes(), []byte("evicted 1 leaf(s), 100 B reclaimed; 100 B retained (cap 100 B)")) {
		t.Errorf("tie-break summary line wrong:\n%s", s)
	}
}

// TestCacheEvictSkipsSidecarsAndTmp asserts the compile-lock sidecars and *.tmp
// transients are neither counted toward the total nor ever evicted, even while a
// real leaf is being evicted.
func TestCacheEvictSkipsSidecarsAndTmp(t *testing.T) {
	compileDir := t.TempDir()
	modDir := filepath.Join(compileDir, "wazero-1.0-amd64-linux")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	old := writeCompileLeaf(t, modDir, "leaf-old", 100, base)
	newest := writeCompileLeaf(t, modDir, "leaf-new", 100, base.Add(time.Hour))
	// A large lock sidecar and a large *.tmp transient: if either were counted,
	// the total would far exceed the cap; if either were evictable it could be
	// removed. Both must be ignored on both axes.
	lock := writeCompileLeaf(t, compileDir, ".compile-deadbeef.lock", 500, base)
	tmp := writeCompileLeaf(t, modDir, "half-written.tmp", 500, base)

	loc := cacheLocations{compileDir: compileDir}
	var out bytes.Buffer
	// Only the two real leaves (200 B) are counted; cap 150 B evicts one.
	if err := runCacheEvict(&out, loc, 150); err != nil {
		t.Fatalf("evict: %v", err)
	}
	s := out.String()
	t.Logf("evict output:\n%s", s)

	if exists(old) {
		t.Errorf("evict retained the oldest real leaf:\n%s", s)
	}
	if !exists(newest) {
		t.Errorf("evict removed the newest real leaf: %s", newest)
	}
	if !exists(lock) {
		t.Errorf("evict removed a compile-lock sidecar: %s", lock)
	}
	if !exists(tmp) {
		t.Errorf("evict removed a *.tmp transient: %s", tmp)
	}
	// Retained 100 B proves the 500 B lock and 500 B tmp were never counted.
	if !bytes.Contains(out.Bytes(), []byte("evicted 1 leaf(s), 100 B reclaimed; 100 B retained (cap 150 B)")) {
		t.Errorf("evict counted a sidecar or tmp transient:\n%s", s)
	}
}

// TestCompileLeafMembershipUnified asserts the single membership predicate both
// the status and evict enumerations consult excludes a half-written *.tmp
// transient from BOTH — so the status listing and the eviction leaf set agree on
// which files are cache content. The *.tmp exclusion is the point: an in-flight
// compile's staging file must never be listed or evicted on either path.
func TestCompileLeafMembershipUnified(t *testing.T) {
	compileDir := t.TempDir()
	modDir := filepath.Join(compileDir, "wazero-1.0-amd64-linux")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	module := writeCompileLeaf(t, modDir, "abcdef0123", 100, base)
	tmp := writeCompileLeaf(t, modDir, "abcdef0123.tmp", 100, base)
	lock := writeCompileLeaf(t, compileDir, ".compile-deadbeef.lock", 100, base)

	// Status enumeration: only the finished module is a listed element.
	els := enumCompileModules(compileDir)
	if len(els) != 1 {
		t.Fatalf("status enumeration returned %d element(s), want 1:\n%+v", len(els), els)
	}
	if !strings.HasSuffix(filepath.ToSlash(els[0].path), "abcdef0123") {
		t.Fatalf("status enumeration listed a non-module leaf: %s", els[0].path)
	}
	for _, el := range els {
		if strings.HasSuffix(el.label, ".tmp") {
			t.Errorf("status enumeration listed a *.tmp transient: %s", el.label)
		}
		if strings.HasPrefix(filepath.Base(el.path), ".compile-") {
			t.Errorf("status enumeration listed a compile-lock sidecar: %s", el.path)
		}
	}

	// Evict enumeration: the same single leaf, agreeing with status.
	leaves, err := collectCompileLeaves(compileDir)
	if err != nil {
		t.Fatalf("collectCompileLeaves: %v", err)
	}
	if len(leaves) != 1 {
		t.Fatalf("evict enumeration returned %d leaf(s), want 1:\n%+v", len(leaves), leaves)
	}
	if leaves[0].path != module {
		t.Fatalf("evict enumeration leaf %q, want the module %q", leaves[0].path, module)
	}

	// The predicate itself, exercised directly on each name.
	if !isCompileLeaf("abcdef0123") {
		t.Errorf("isCompileLeaf rejected a finished module name")
	}
	if isCompileLeaf("abcdef0123.tmp") {
		t.Errorf("isCompileLeaf admitted a *.tmp transient")
	}
	if isCompileLeaf(".compile-deadbeef.lock") {
		t.Errorf("isCompileLeaf admitted a compile-lock sidecar")
	}

	// The fixtures exist; nothing was mutated by enumeration.
	for _, p := range []string{module, tmp, lock} {
		if !exists(p) {
			t.Errorf("enumeration mutated the tree; missing %s", p)
		}
	}
}

// TestCacheEvictNoCompileDir asserts a loud error when no compiled-module cache
// directory resolves.
func TestCacheEvictNoCompileDir(t *testing.T) {
	if err := runCacheEvict(&bytes.Buffer{}, cacheLocations{}, 100); err == nil {
		t.Fatalf("expected a loud error with no compiled-module cache directory")
	}
}

// TestParseHumanSize covers the shared binary-unit vocabulary parseHumanSize and
// humanSize agree on, plus the malformed-input error paths.
func TestParseHumanSize(t *testing.T) {
	ok := []struct {
		in   string
		want int64
	}{
		{"1024", 1024},
		{"0", 0},
		{"512 B", 512},
		{"1KiB", 1 << 10},
		{"1 kib", 1 << 10},
		{"512MiB", 512 << 20},
		{"5GiB", 5 << 30},
		{"4.5 GiB", int64(4.5 * float64(int64(1)<<30))},
		{"1TiB", 1 << 40},
	}
	for _, c := range ok {
		got, err := parseHumanSize(c.in)
		if err != nil {
			t.Errorf("parseHumanSize(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseHumanSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}

	bad := []string{"", "   ", "GiB", "abc", "5 ZiB", "1.5", "MiB"}
	for _, in := range bad {
		if _, err := parseHumanSize(in); err == nil {
			t.Errorf("parseHumanSize(%q) expected an error, got nil", in)
		}
	}

	// Round-trip: an exact unit multiple humanSize prints parses back to itself,
	// proving the display and parse sides share one unit scheme.
	for _, n := range []int64{512, 1 << 10, 512 << 20, 5 << 30, 1 << 40} {
		rt, err := parseHumanSize(humanSize(n))
		if err != nil {
			t.Errorf("round-trip parseHumanSize(humanSize(%d)) error: %v", n, err)
			continue
		}
		if rt != n {
			t.Errorf("round-trip %d -> %q -> %d", n, humanSize(n), rt)
		}
	}
}

// TestCacheStatusProvenanceSharedBy builds a provenance index in which two
// distinct URIs resolve to one content digest, then asserts status surfaces both
// identities on a single line marked "shared by 2", and that a solitary identity
// is listed without the marker. The section is deterministic and carries no
// timestamps.
func TestCacheStatusProvenanceSharedBy(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	idxPath := filepath.Join(t.TempDir(), "provenance-index.json")

	shared := "sha256:beef"
	if err := provindex.Upsert(idxPath, shared, provindex.Identity{URI: "oci://hub.aggrega.ai/plugins/core/helm", Version: "1.0.0"}, 100, now); err != nil {
		t.Fatalf("upsert shared A: %v", err)
	}
	if err := provindex.Upsert(idxPath, shared, provindex.Identity{URI: "oci://hub.aggrega.ai/plugins/team/helm-fork", Version: "2.0.0"}, 100, now); err != nil {
		t.Fatalf("upsert shared B: %v", err)
	}
	if err := provindex.Upsert(idxPath, "sha256:aaaa", provindex.Identity{URI: "oci://hub.aggrega.ai/plugins/core/kustomize", Version: "3.1.0"}, 50, now); err != nil {
		t.Fatalf("upsert solo: %v", err)
	}

	loc := cacheLocations{provIndexPath: idxPath}
	var a, b bytes.Buffer
	if err := runCacheStatus(&a, loc); err != nil {
		t.Fatalf("status a: %v", err)
	}
	if err := runCacheStatus(&b, loc); err != nil {
		t.Fatalf("status b: %v", err)
	}
	if a.String() != b.String() {
		t.Fatalf("provenance status not byte-identical:\n--- a ---\n%s\n--- b ---\n%s", a.String(), b.String())
	}
	s := a.String()
	t.Logf("status output:\n%s", s)

	if !bytes.Contains(a.Bytes(), []byte("plugin provenance:")) {
		t.Errorf("status missing plugin provenance section:\n%s", s)
	}
	if !bytes.Contains(a.Bytes(), []byte("shared by 2")) {
		t.Errorf("status did not mark the shared digest:\n%s", s)
	}
	if !bytes.Contains(a.Bytes(), []byte("oci://hub.aggrega.ai/plugins/core/helm@1.0.0")) ||
		!bytes.Contains(a.Bytes(), []byte("oci://hub.aggrega.ai/plugins/team/helm-fork@2.0.0")) {
		t.Errorf("status did not surface both shared identities:\n%s", s)
	}
	// The solitary identity is listed without a shared marker on its own line.
	if !bytes.Contains(a.Bytes(), []byte("oci://hub.aggrega.ai/plugins/core/kustomize@3.1.0\n")) {
		t.Errorf("status did not list the solitary identity plainly:\n%s", s)
	}
}

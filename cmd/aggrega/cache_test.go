// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
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

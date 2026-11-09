// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package verdicttest provides shared fixtures for tests that need a
// verdict-socket plugin already present in a workspace the way a completed
// dependency resolution leaves it.
//
// It exists as its own package because two unrelated test suites need the same
// fixture — the resolution layer's, which asserts what identity is resolved and
// recorded, and the command layer's, which asserts what a person invoking a
// command then sees — and a fixture written twice would let the two suites
// disagree about what "already resolved" looks like on disk.
package verdicttest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/apiversion"
	"github.com/aggrega-ai/aggrega-cli/internal/pluginabi"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// HeaderOnlyWasm is a valid, import-free Wasm 1.0 module: the four magic bytes
// and the version word, and nothing else. It parses as a module with no imports,
// so a plugin entity carrying no host-function stamp cross-checks against it
// cleanly — which is what lets an identity-plumbing test run without compiling a
// real guest.
var HeaderOnlyWasm = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

// PluginEntityYAML is a schema-valid Plugin entity for a verdict-socket plugin
// at the given socket, bare name and version.
func PluginEntityYAML(socket, name, version string) string {
	return "apiVersion: " + apiversion.APIVersion + "\n" +
		"kind: Plugin\n" +
		"metadata:\n" +
		"  name: " + name + "\n" +
		"  version: \"" + version + "\"\n" +
		"  pluginType: " + socket + "\n" +
		"spec:\n" +
		"  abiVersion: " + pluginabi.Version + "\n" +
		"  exports:\n" +
		"    - name: verdict_ir\n"
}

// Cached describes a verdict-socket plugin seeded into a workspace: the
// versioned entity URI an attachment names, the identity coordinate a record is
// addressed by, the content digest of the plugin's .wasm layer, and the OCI
// manifest digest — which is a DIFFERENT digest, and is the one a record must not
// carry.
type Cached struct {
	URI            string
	Coordinate     string
	WasmDigest     string
	ManifestDigest string
	ManifestBytes  []byte
	EntityYAML     string
}

// SeedCached lays a verdict-socket plugin into a workspace the way a completed
// resolution leaves it, with the minimal entity and the header-only .wasm that
// let an identity-plumbing test run without compiling a guest.
//
// It is the default-fixture face of SeedCachedArtifact; a caller needing a
// loadable guest, or an entity declaring more than one export, supplies both
// through that function instead of assembling a second seeding path.
func SeedCached(t *testing.T, root, catalogDir, socket, name, version string) Cached {
	t.Helper()
	return SeedCachedArtifact(t, root, catalogDir, socket, name, version,
		PluginEntityYAML(socket, name, version), HeaderOnlyWasm)
}

// SeedCachedArtifact lays a verdict-socket plugin into a workspace the way a
// completed resolution leaves it: the Plugin entity in a catalog directory, and
// in the content cache the entity's config blob, the OCI manifest committing to
// it, and the .wasm blob addressed by its own content digest.
//
// entityYAML and wasm are the artifact's two halves, supplied by the caller so
// one seeding path serves both an identity-plumbing fixture and a real guest. The
// entity must describe the .wasm handed alongside it — a resolution cross-checks
// the two — so the pair travels together rather than as two independent arguments
// a caller could mismatch silently.
//
// The manifest is built by the production packer rather than assembled here, so
// the .wasm layer digest and the manifest digest are the real pair — which is what
// makes a test that confuses the two able to fail.
//
// catalogDir receives the entity so a caller may put it on the catalog search
// stack or deliberately leave it off. Left off, the entity-fronted cascade misses
// and resolution falls through to the cache-not-pin read, which is the tier a
// recorded digest governs; there is no other way to reach that tier from a
// workspace with no registry.
func SeedCachedArtifact(t *testing.T, root, catalogDir, socket, name, version, entityYAML string, wasm []byte) Cached {
	t.Helper()

	coordinate := types.PluginCoordinate(socket, name)
	uri := coordinate + ":" + version

	store := memory.New()
	if _, err := oci.PushPluginManifest(context.Background(), store, version, []byte(entityYAML), wasm, nil); err != nil {
		t.Fatalf("pack plugin manifest for %s: %v", uri, err)
	}
	pulled, err := oci.FetchPluginEntity(context.Background(), store, version)
	if err != nil {
		t.Fatalf("read back packed plugin manifest for %s: %v", uri, err)
	}

	entityPath := filepath.Join(catalogDir, "plugin", socket, name, version, "plugin.yaml")
	if err := os.MkdirAll(filepath.Dir(entityPath), 0o755); err != nil {
		t.Fatalf("mkdir catalog entity dir: %v", err)
	}
	if err := os.WriteFile(entityPath, []byte(entityYAML), 0o644); err != nil {
		t.Fatalf("write catalog entity %s: %v", entityPath, err)
	}

	cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
	wasmDigest := digest.ContentDigest(wasm)
	if err := oci.WriteCached(root, cacheDir, oci.PluginArtifactKind(), uri, version, []byte(entityYAML)); err != nil {
		t.Fatalf("seed cached entity blob for %s: %v", uri, err)
	}
	if err := oci.WriteCachedManifest(root, cacheDir, oci.PluginArtifactKind(), uri, version, pulled.ManifestBytes); err != nil {
		t.Fatalf("seed cached manifest for %s: %v", uri, err)
	}
	if err := oci.WriteCached(root, cacheDir, oci.PluginArtifactKind(), coordinate, wasmDigest, wasm); err != nil {
		t.Fatalf("seed cached .wasm for %s: %v", coordinate, err)
	}

	return Cached{
		URI:            uri,
		Coordinate:     coordinate,
		WasmDigest:     wasmDigest,
		ManifestDigest: pulled.Digest,
		ManifestBytes:  pulled.ManifestBytes,
		EntityYAML:     entityYAML,
	}
}

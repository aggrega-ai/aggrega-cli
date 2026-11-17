// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package sourcecache_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
)

const moduleCacheDir = ".aggrega/cache"

// moduleCase is one OCI module kind as a test slot holds it.
type moduleCase struct {
	kind, url, version string
	layerMediaType     string
}

var (
	bicepCase = moduleCase{
		kind: sourcekind.BicepModuleOCI, url: "oci://registry.example/bicep/net", version: "1.0.0",
		layerMediaType: "application/vnd.ms.bicep.module.layer.v1+json",
	}
	terraformCase = moduleCase{
		kind: sourcekind.TerraformModuleOCI, url: "oci://registry.example/modules/net", version: "1.0.0",
		layerMediaType: "archive/zip",
	}
)

// moduleManifest returns the bytes of an OCI manifest committing to blob as the
// layer of mc's media type, beside a second layer of another type that a selection
// by position would take instead.
func moduleManifest(t *testing.T, mc moduleCase, blob []byte) []byte {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	other, err := oras.PushBytes(ctx, store, "application/vnd.example.other.layer", []byte("other layer"))
	if err != nil {
		t.Fatalf("push other layer: %v", err)
	}
	layer, err := oras.PushBytes(ctx, store, mc.layerMediaType, blob)
	if err != nil {
		t.Fatalf("push layer: %v", err)
	}
	config, err := oras.PushBytes(ctx, store, "application/vnd.example.module.config.v1+json", []byte("{}"))
	if err != nil {
		t.Fatalf("push config: %v", err)
	}
	desc, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1, "application/vnd.example.module.artifact",
		oras.PackManifestOptions{Layers: []ocispec.Descriptor{other, layer}, ConfigDescriptor: &config})
	if err != nil {
		t.Fatalf("pack manifest: %v", err)
	}
	out, err := content.FetchAll(ctx, store, desc)
	if err != nil {
		t.Fatalf("fetch manifest: %v", err)
	}
	return out
}

// moduleSlot writes the cache slot a pull of mc's kind leaves for blob at
// moduleSubdir, with manifest beside it, and returns the slot and the lockfile row
// that pins it. A nil manifest writes none.
func moduleSlot(t *testing.T, root string, mc moduleCase, moduleSubdir string, blob, manifest []byte, manifestDigest string) (string, *lockfile.Lockfile) {
	t.Helper()
	dig := digestOf(blob)
	slot, err := sourcecache.SlotPath(root, moduleCacheDir, mc.kind, mc.url, "", moduleSubdir, mc.version, dig)
	if err != nil {
		t.Fatalf("slot: %v", err)
	}
	if err := cacheslot.WriteBlobAndMetadata(root, moduleCacheDir, slot, blob, mc.kind, mc.url, mc.version, dig, mc.layerMediaType, "", manifest); err != nil {
		t.Fatalf("write slot: %v", err)
	}
	lf := &lockfile.Lockfile{
		Version: 1, Release: "./r.yaml", GeneratedAt: "2026-05-22T10:00:00Z",
		SourceArtifacts: []lockfile.SourceArtifact{{
			Kind: mc.kind, URL: mc.url, ModuleSubdir: moduleSubdir, PinnedVersion: mc.version,
			Digest: dig, ManifestDigest: manifestDigest, Mediatype: mc.layerMediaType,
			ReferencedBy: []lockfile.SourceArtifactReference{{Entity: "recipe", URI: "u", Field: "spec.source"}},
		}},
	}
	return slot, lf
}

func moduleBlob(t *testing.T, mc moduleCase) []byte {
	t.Helper()
	if mc.kind == sourcekind.BicepModuleOCI {
		return []byte(`{"$schema":"https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#"}`)
	}
	return buildZip(t,
		zipMember{name: "main.tf", body: "resource {}\n"},
		zipMember{name: "modules/vpc/main.tf", body: "variable {}\n"},
	)
}

func TestLookupAndExtractLaysOutACompiledTemplate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blob := moduleBlob(t, bicepCase)
	manifest := moduleManifest(t, bicepCase, blob)
	_, lf := moduleSlot(t, root, bicepCase, "", blob, manifest, digestOf(manifest))

	tree, err := sourcecache.LookupAndExtract(lf, bicepCase.kind, bicepCase.url, "", "", bicepCase.version, "", root, moduleCacheDir)
	if err != nil {
		t.Fatalf("LookupAndExtract: %v", err)
	}
	if got := treeListing(t, tree); !slices.Equal(got, []string{"main.json"}) {
		t.Fatalf("tree = %v; want only main.json", got)
	}
	got, err := os.ReadFile(filepath.Join(tree, "main.json"))
	if err != nil {
		t.Fatalf("read main.json: %v", err)
	}
	if string(got) != string(blob) {
		t.Errorf("main.json = %q; want the blob %q", got, blob)
	}
}

func TestLookupAndExtractReturnsTheModuleTreeSubdirectory(t *testing.T) {
	t.Parallel()
	for name, archive := range map[string][]byte{
		"explicit directory entries": buildZip(t,
			zipMember{name: "main.tf", body: "resource {}\n"},
			zipMember{name: "modules/"},
			zipMember{name: "modules/vpc/"},
			zipMember{name: "modules/vpc/main.tf", body: "variable {}\n"}),
		"implied directories only": buildZip(t, zipMember{name: "modules/vpc/main.tf", body: "variable {}\n"}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			manifest := moduleManifest(t, terraformCase, archive)
			_, lf := moduleSlot(t, root, terraformCase, "modules/vpc", archive, manifest, digestOf(manifest))
			dir, err := sourcecache.LookupAndExtract(lf, terraformCase.kind, terraformCase.url, "", "modules/vpc", terraformCase.version, "", root, moduleCacheDir)
			if err != nil {
				t.Fatalf("LookupAndExtract: %v", err)
			}
			if filepath.Base(dir) != "vpc" || filepath.Base(filepath.Dir(dir)) != "modules" {
				t.Errorf("returned %s; want the modules/vpc directory of the tree", dir)
			}
			if got, err := os.ReadFile(filepath.Join(dir, "main.tf")); err != nil || string(got) != "variable {}\n" {
				t.Errorf("modules/vpc/main.tf = %q, %v", got, err)
			}
		})
	}

	t.Run("absent directory", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		archive := moduleBlob(t, terraformCase)
		manifest := moduleManifest(t, terraformCase, archive)
		_, lf := moduleSlot(t, root, terraformCase, "modules/none", archive, manifest, digestOf(manifest))
		_, err := sourcecache.LookupAndExtract(lf, terraformCase.kind, terraformCase.url, "", "modules/none", terraformCase.version, "", root, moduleCacheDir)
		if !errors.Is(err, sourcecache.ErrFetchNotFound()) {
			t.Fatalf("absent module_subdir: err = %v; want ErrFetchNotFound", err)
		}
	})

	t.Run("whole package", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		archive := moduleBlob(t, terraformCase)
		manifest := moduleManifest(t, terraformCase, archive)
		_, lf := moduleSlot(t, root, terraformCase, "", archive, manifest, digestOf(manifest))
		tree, err := sourcecache.LookupAndExtract(lf, terraformCase.kind, terraformCase.url, "", "", terraformCase.version, "", root, moduleCacheDir)
		if err != nil {
			t.Fatalf("LookupAndExtract: %v", err)
		}
		if _, err := os.Stat(filepath.Join(tree, "main.tf")); err != nil {
			t.Errorf("main.tf missing at the tree root: %v", err)
		}
	})
}

func TestLookupAndExtractHoldsModuleKindsToTheirManifest(t *testing.T) {
	t.Parallel()
	for _, mc := range []moduleCase{bicepCase, terraformCase} {
		t.Run(mc.kind+"/tampered manifest", func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			blob := moduleBlob(t, mc)
			manifest := moduleManifest(t, mc, blob)
			slot, lf := moduleSlot(t, root, mc, "", blob, manifest, digestOf(manifest))
			if err := os.WriteFile(filepath.Join(slot, cacheslot.ManifestFile), append(manifest[:len(manifest)-1:len(manifest)-1], ' ', '}'), 0o644); err != nil {
				t.Fatalf("tamper manifest: %v", err)
			}
			_, err := sourcecache.LookupAndExtract(lf, mc.kind, mc.url, "", "", mc.version, "", root, moduleCacheDir)
			if !errors.Is(err, oci.ErrCachedManifestIntegrity()) {
				t.Fatalf("tampered manifest: err = %v; want ErrCachedManifestIntegrity", err)
			}
		})
		t.Run(mc.kind+"/forged pins", func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			original := moduleBlob(t, mc)
			forged := append(slices.Clone(original), '\n')
			if mc.kind == sourcekind.TerraformModuleOCI {
				forged = buildZip(t, zipMember{name: "main.tf", body: "forged\n"})
			}
			// The slot, its metadata and the lockfile all pin the forged bytes; only
			// the manifest still commits to the original.
			_, lf := moduleSlot(t, root, mc, "", forged, moduleManifest(t, mc, original), "")
			_, err := sourcecache.LookupAndExtract(lf, mc.kind, mc.url, "", "", mc.version, "", root, moduleCacheDir)
			if !errors.Is(err, sourcecache.ErrFetchIntegrity()) {
				t.Fatalf("forged blob and pins: err = %v; want ErrFetchIntegrity", err)
			}
		})
		t.Run(mc.kind+"/no manifest", func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			blob := moduleBlob(t, mc)
			_, lf := moduleSlot(t, root, mc, "", blob, nil, "")
			_, err := sourcecache.LookupAndExtract(lf, mc.kind, mc.url, "", "", mc.version, "", root, moduleCacheDir)
			if !errors.Is(err, sourcecache.ErrFetchNotFound()) {
				t.Fatalf("manifest-less slot: err = %v; want ErrFetchNotFound", err)
			}
		})
	}
}

func TestLookupAndExtractDiscardsAHandEditInAModuleTree(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blob := moduleBlob(t, terraformCase)
	manifest := moduleManifest(t, terraformCase, blob)
	_, lf := moduleSlot(t, root, terraformCase, "", blob, manifest, digestOf(manifest))
	read := func() string {
		tree, err := sourcecache.LookupAndExtract(lf, terraformCase.kind, terraformCase.url, "", "", terraformCase.version, "", root, moduleCacheDir)
		if err != nil {
			t.Fatalf("LookupAndExtract: %v", err)
		}
		return tree
	}
	tree := read()
	if err := os.WriteFile(filepath.Join(tree, "main.tf"), []byte("edited\n"), 0o644); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tree, "extra.tf"), []byte("added\n"), 0o644); err != nil {
		t.Fatalf("add: %v", err)
	}
	tree = read()
	if got, err := os.ReadFile(filepath.Join(tree, "main.tf")); err != nil || string(got) != "resource {}\n" {
		t.Errorf("main.tf = %q, %v; want the packaged bytes restored", got, err)
	}
	if _, err := os.Stat(filepath.Join(tree, "extra.tf")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("extra.tf survived the next read (stat err = %v)", err)
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
)

const (
	// tofuModuleArtifactType is the artifact type of an OpenTofu module package.
	tofuModuleArtifactType = "application/vnd.opentofu.modulepkg"
	// zipLayerMediaType is the media type of the one layer such a package holds.
	zipLayerMediaType = "archive/zip"
)

// ociLayer is one layer, or a config, of an artifact pushed by pushOCIModule.
type ociLayer struct {
	MediaType string
	Content   []byte
}

// pushOCIModule packs an OCI artifact of artifactType holding layers and pushes it
// to host under repository and tag, returning the manifest's digest. A nil config
// packs the OCI empty config, which is the OpenTofu module package's shape; a
// non-nil config is pushed and named as the manifest's config, which the 1.1
// packing requires wherever the artifact type is empty and which a Bicep module
// carries.
func pushOCIModule(t *testing.T, host, repository, tag, artifactType string, config *ociLayer, layers ...ociLayer) (manifestDigest string) {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	opts := oras.PackManifestOptions{}
	if config != nil {
		desc, err := oras.PushBytes(ctx, store, config.MediaType, config.Content)
		if err != nil {
			t.Fatalf("push the config %s: %v", config.MediaType, err)
		}
		opts.ConfigDescriptor = &desc
	}
	for _, layer := range layers {
		desc, err := oras.PushBytes(ctx, store, layer.MediaType, layer.Content)
		if err != nil {
			t.Fatalf("push the layer %s: %v", layer.MediaType, err)
		}
		opts.Layers = append(opts.Layers, desc)
	}
	manifest, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1, artifactType, opts)
	if err != nil {
		t.Fatalf("pack the manifest: %v", err)
	}
	if err := store.Tag(ctx, manifest, tag); err != nil {
		t.Fatalf("tag the manifest: %v", err)
	}
	repo, err := oci.NewRemoteRepository(host+"/"+repository, &http.Client{})
	if err != nil {
		t.Fatalf("open %s/%s: %v", host, repository, err)
	}
	if _, err := oras.Copy(ctx, store, tag, repo, tag, oras.DefaultCopyOptions); err != nil {
		t.Fatalf("push %s/%s:%s: %v", host, repository, tag, err)
	}
	return manifest.Digest.String()
}

// zipPackage writes entries, in the order given, as a zip archive. A name ending
// in a slash is a directory entry.
func zipPackage(t *testing.T, entries ...[2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		f, err := w.Create(e[0])
		if err != nil {
			t.Fatalf("create the zip entry %q: %v", e[0], err)
		}
		if _, err := f.Write([]byte(e[1])); err != nil {
			t.Fatalf("write the zip entry %q: %v", e[0], err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the zip: %v", err)
	}
	return buf.Bytes()
}

// TestInProcessRegistryHoldsAnOCIModuleArtifact pushes an OpenTofu-shaped module
// package to the in-process registry and reads it back: the registry keeps the
// artifact type and the archive/zip layer, so the tests below prove the product
// rather than the registry.
func TestInProcessRegistryHoldsAnOCIModuleArtifact(t *testing.T) {
	host := startRegistry(t, nil)
	pkg := zipPackage(t, [2]string{"main.tf", "# root\n"})
	manifestDigest := pushOCIModule(t, host, "modules/net", "v1.2.0", tofuModuleArtifactType, nil,
		ociLayer{MediaType: zipLayerMediaType, Content: pkg})

	repo, err := oci.NewRemoteRepository(host+"/modules/net", &http.Client{})
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}
	ctx := context.Background()
	desc, err := repo.Resolve(ctx, "v1.2.0")
	if err != nil {
		t.Fatalf("resolve the tag: %v", err)
	}
	if desc.Digest.String() != manifestDigest {
		t.Fatalf("the tag resolves to %s; pushed %s", desc.Digest, manifestDigest)
	}
	raw, err := content.FetchAll(ctx, repo, desc)
	if err != nil {
		t.Fatalf("fetch the manifest: %v", err)
	}
	manifest, err := decodeManifest(raw)
	if err != nil {
		t.Fatalf("decode the manifest: %v", err)
	}
	if manifest.ArtifactType != tofuModuleArtifactType {
		t.Errorf("artifactType = %q; want %q", manifest.ArtifactType, tofuModuleArtifactType)
	}
	if len(manifest.Layers) != 1 || manifest.Layers[0].MediaType != zipLayerMediaType {
		t.Fatalf("layers = %+v; want exactly one %s layer", manifest.Layers, zipLayerMediaType)
	}
	got, err := content.FetchAll(ctx, repo, manifest.Layers[0])
	if err != nil {
		t.Fatalf("fetch the layer: %v", err)
	}
	if !bytes.Equal(got, pkg) {
		t.Error("the layer reads back different bytes from those pushed")
	}
}

// decodeManifest reads an OCI image manifest.
func decodeManifest(raw []byte) (ocispec.Manifest, error) {
	var m ocispec.Manifest
	err := json.Unmarshal(raw, &m)
	return m, err
}

// moduleComponent is one component of an OpenTofu workspace: a Recipe of the same
// name sourcing a module package from the loopback registry.
type moduleComponent struct {
	name       string
	repository string
	version    string
	subdir     string
}

// ociModuleWorkspace writes a workspace whose components are each rendered by a
// terraform Recipe sourcing a module package from the registry at host, and, where
// feature is non-nil, one boundary Feature realized by a FeatureRecipe sourcing a
// package. It returns the Release's path, the catalog directory and the path of
// each Recipe and FeatureRecipe document, the components' in order.
func ociModuleWorkspace(t *testing.T, root, host string, components []moduleComponent, feature *moduleComponent) (releasePath, catalogDir string, entityPaths []string) {
	t.Helper()
	catalogDir = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nauto_pull: false\n")

	source := func(c moduleComponent) string {
		out := "  source:\n    kind: " + sourcekind.TerraformModuleOCI + "\n    url: oci://" + host + "/" + c.repository +
			"\n    version: " + c.version + "\n"
		if c.subdir != "" {
			out += "    module_subdir: " + c.subdir + "\n"
		}
		return out
	}
	var aggregate, mappings, boundary strings.Builder
	for _, c := range components {
		writeFixture(t, filepath.Join(catalogDir, "components", c.name+".yaml"), "apiVersion: aggrega.ai/v1beta1\n"+
			"kind: Component\nmetadata:\n  name: "+c.name+"\n  version: \"1.0.0\"\n  labels:\n"+
			"    component.aggrega.ai/type: \"server\"\nspec:\n  schema:\n    values: {}\n")
		recipe := filepath.Join(catalogDir, "recipes", c.name, "recipe.yaml")
		writeFixture(t, recipe, "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: "+c.name+"\n"+
			"  version: \"1.0.0\"\nspec:\n  capability: terraform\n  implements: "+c.name+"\n"+source(c)+
			"  valuesMapping: |\n    {}\n")
		entityPaths = append(entityPaths, recipe)
		aggregate.WriteString("    " + c.name + ":\n      type: " + c.name + "\n")
		mappings.WriteString("        " + c.name + ": " + c.name + "\n")
		boundary.WriteString("        " + c.name + ": {}\n")
	}
	manifestFeatures, releaseFeatures := "", ""
	if feature != nil {
		writeFixture(t, filepath.Join(catalogDir, "features", "net.yaml"), "apiVersion: aggrega.ai/v1beta1\n"+
			"kind: Feature\nmetadata:\n  name: probe/net\n  version: \"1.0.0\"\nspec: {}\n")
		featureRecipe := filepath.Join(catalogDir, "feature-recipes", "net", "featurerecipe.yaml")
		writeFixture(t, featureRecipe, "apiVersion: aggrega.ai/v1beta1\nkind: FeatureRecipe\nmetadata:\n  name: net\n"+
			"  version: \"1.0.0\"\nspec:\n  capability: terraform\n  implements: probe/net\n  executionScope: boundary\n"+
			source(*feature))
		entityPaths = append(entityPaths, featureRecipe)
		manifestFeatures = "    features:\n      type:\n        probe/net: net\n"
		releaseFeatures = "      features:\n        net:\n          type: probe/net\n"
	}
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\n"+
		"metadata:\n  name: aggregate\n  version: \"1.0.0\"\nspec:\n  components:\n"+aggregate.String())
	writeFixture(t, filepath.Join(root, "manifest.yaml"), "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\n"+
		"metadata:\n  name: manifest\n  version: \"1.0.0\"\nspec:\n  mappings:\n    components:\n      type:\n"+
		mappings.String()+manifestFeatures)
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, boundaryOutputRelease("oci-module",
		"    main:\n      directory: ./dist/main\n"+releaseFeatures+"      components:\n"+boundary.String()))
	seedEmbeddedPlugins(t, filepath.Join(catalogDir, "providers"), "provider/terraform")
	return releasePath, catalogDir, entityPaths
}

// pinAndPull runs `aggrega pin` on each entity and then a plain pull, the order an
// author follows, and returns the pull's error.
func pinAndPull(t *testing.T, root, releasePath, catalogDir string, entityPaths []string) error {
	t.Helper()
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	for _, p := range entityPaths {
		if err := pinRun(context.Background(), io.Discard, ws, p, false); err != nil {
			t.Fatalf("pin %s: %v", p, err)
		}
	}
	return plainPull(t, root, releasePath, catalogDir)
}

// plainPull runs a pull that records no source pin of its own.
func plainPull(t *testing.T, root, releasePath, catalogDir string) error {
	t.Helper()
	return pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{catalogDir},
		DiagOut:     io.Discard,
		Out:         io.Discard,
		diag:        buildDiagOptions{quiet: true},
	})
}

// makeOffline rewrites the workspace's configuration to declare it offline.
func makeOffline(t *testing.T, root string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nauto_pull: false\noffline: true\n")
}

// sourceRows reads the source-artifact rows the lockfile beside releasePath holds.
func sourceRows(t *testing.T, releasePath string) []lockfile.SourceArtifact {
	t.Helper()
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	return lf.SourceArtifacts
}

// sha256Of is the digest an OCI descriptor of data carries.
func sha256Of(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// moduleDirOf finds the directory of the output tree holding the boundary's root
// configuration, with a trailing slash.
func moduleDirOf(t *testing.T, tree map[string]string) string {
	t.Helper()
	for p := range tree {
		if strings.HasSuffix(p, "aggrega.modules.tf") {
			return strings.TrimSuffix(p, "aggrega.modules.tf")
		}
	}
	t.Fatalf("no root configuration in %v", sortedTreeKeys(tree))
	return ""
}

const (
	netRepository = "modules/net"
	netVersion    = "v1.2.0"
	netMain       = "resource \"terraform_data\" \"net\" {\n  input = \"net\"\n}\n\noutput \"id\" {\n  value = terraform_data.net.id\n}\n"
	netVPC        = "resource \"terraform_data\" \"vpc\" {\n  input = \"vpc\"\n}\n"
)

// netPackage is the module package the tests publish: a root module and one nested
// module, with a directory entry for the nested module's parent.
func netPackage(t *testing.T) []byte {
	t.Helper()
	return zipPackage(t,
		[2]string{"main.tf", netMain},
		[2]string{"modules/", ""},
		[2]string{"modules/vpc/", ""},
		[2]string{"modules/vpc/main.tf", netVPC})
}

// pushNet publishes pkg as the package under netVersion and returns its manifest
// digest.
func pushNet(t *testing.T, host string, pkg []byte, tag string) string {
	t.Helper()
	return pushOCIModule(t, host, netRepository, tag, tofuModuleArtifactType, nil,
		ociLayer{MediaType: zipLayerMediaType, Content: pkg})
}

// TestOCIModuleSourceTerraformPullAndBuild drives a terraform Recipe sourcing a
// module package from a loopback registry through pin, pull and build, and holds
// each recorded coordinate and each vendored byte to what was published.
func TestOCIModuleSourceTerraformPullAndBuild(t *testing.T) {
	host := startRegistry(t, nil)
	pkg := netPackage(t)
	manifestDigest := pushNet(t, host, pkg, netVersion)
	layerDigest := sha256Of(pkg)
	url := "oci://" + host + "/" + netRepository
	one := []moduleComponent{{name: "net", repository: netRepository, version: netVersion}}

	// fresh writes one workspace, pins and pulls it, and returns its parts.
	fresh := func(t *testing.T, components []moduleComponent, feature *moduleComponent) (root, releasePath, catalogDir string, entityPaths []string) {
		t.Helper()
		root = t.TempDir()
		t.Chdir(root)
		releasePath, catalogDir, entityPaths = ociModuleWorkspace(t, root, host, components, feature)
		if err := pinAndPull(t, root, releasePath, catalogDir, entityPaths); err != nil {
			t.Fatalf("pin and pull: %s", cerrors.CauseChain(err))
		}
		return root, releasePath, catalogDir, entityPaths
	}

	root, releasePath, catalogDir, entityPaths := fresh(t, one, nil)

	rows := sourceRows(t, releasePath)
	if len(rows) != 1 {
		t.Fatalf("want one source row; got %+v", rows)
	}
	row := rows[0]
	for _, c := range []struct{ field, got, want string }{
		{"kind", row.Kind, sourcekind.TerraformModuleOCI},
		{"url", row.URL, url},
		{"pinned_version", row.PinnedVersion, netVersion},
		{"digest", row.Digest, layerDigest},
		{"manifest_digest", row.ManifestDigest, manifestDigest},
		{"mediatype", row.Mediatype, zipLayerMediaType},
		{"module_subdir", row.ModuleSubdir, ""},
	} {
		if c.got != c.want {
			t.Errorf("the lockfile row's %s is %q; want %q", c.field, c.got, c.want)
		}
	}

	lock := readLockForTest(t, entityPaths[0])
	if len(lock.SourceDigests) != 1 {
		t.Fatalf("want one source pin in the entity.lock; got %+v", lock.SourceDigests)
	}
	pin := lock.SourceDigests[0]
	if pin.Digest != layerDigest || pin.ManifestDigest != manifestDigest || pin.Version != netVersion {
		t.Errorf("the entity.lock pins %+v; want layer %s, manifest %s, version %s", pin, layerDigest, manifestDigest, netVersion)
	}

	tree := builtTree(t, root, releasePath, catalogDir)
	dir := moduleDirOf(t, tree)
	for name, want := range map[string]string{"main.tf": netMain, "modules/vpc/main.tf": netVPC} {
		if got := tree[dir+"modules/net/"+name]; got != want {
			t.Errorf("modules/net/%s is %q; want the package's bytes %q", name, got, want)
		}
	}
	if !strings.Contains(tree[dir+"aggrega.modules.tf"], `source = "./modules/net"`) {
		t.Errorf("the root configuration does not call the module by a local path:\n%s", tree[dir+"aggrega.modules.tf"])
	}

	t.Run("Deterministic", func(t *testing.T) {
		other := t.TempDir()
		t.Chdir(other)
		otherRelease, otherCatalog, otherEntities := ociModuleWorkspace(t, other, host, one, nil)
		if err := pinAndPull(t, other, otherRelease, otherCatalog, otherEntities); err != nil {
			t.Fatalf("second pull: %s", cerrors.CauseChain(err))
		}
		for _, pair := range [][2]string{
			{lockfile.PathFor(releasePath), lockfile.PathFor(otherRelease)},
			{recipeLockPath(entityPaths[0]), recipeLockPath(otherEntities[0])},
		} {
			a, b := mustReadFile(t, pair[0]), mustReadFile(t, pair[1])
			if strings.HasSuffix(pair[0], ".lock") && filepath.Base(pair[0]) != recipepin.LockFileName {
				// The lockfile's wall-clock provenance differs between any two pulls.
				a, b = lockfile.ParityWaived(a), lockfile.ParityWaived(b)
			}
			if !bytes.Equal(a, b) {
				t.Errorf("two fresh pulls wrote different %s:\n%s\n---\n%s", filepath.Base(pair[0]), a, b)
			}
		}
		first := builtTree(t, root, releasePath, catalogDir)
		second := builtTree(t, root, releasePath, catalogDir)
		if !equalTrees(first, second) {
			t.Errorf("two builds wrote different output:\n%v\n%v", sortedTreeKeys(first), sortedTreeKeys(second))
		}
		if !equalTrees(first, builtTree(t, other, otherRelease, otherCatalog)) {
			t.Error("a build over the second workspace wrote different output")
		}
	})

	t.Run("OfflineBuildFromCache", func(t *testing.T) {
		offRoot, offRelease, offCatalog, _ := fresh(t, one, nil)
		makeOffline(t, offRoot)
		offTree := builtTree(t, offRoot, offRelease, offCatalog)
		if got := offTree[moduleDirOf(t, offTree)+"modules/net/main.tf"]; got != netMain {
			t.Errorf("the offline build vendors %q; want the package's main.tf", got)
		}
	})

	t.Run("OfflinePullRefused", func(t *testing.T) {
		offRoot := t.TempDir()
		t.Chdir(offRoot)
		offRelease, offCatalog, _ := ociModuleWorkspace(t, offRoot, host, one, nil)
		makeOffline(t, offRoot)
		err := plainPull(t, offRoot, offRelease, offCatalog)
		if !errors.Is(err, egress.ErrOfflineEgress()) {
			t.Fatalf("want the offline refusal over an empty cache; got %v", err)
		}
	})

	t.Run("MovedTag", func(t *testing.T) {
		movedRoot, movedRelease, movedCatalog, movedEntities := fresh(t, one, nil)
		lockBefore := mustReadFile(t, recipeLockPath(movedEntities[0]))
		pushNet(t, host, zipPackage(t, [2]string{"main.tf", "# a different package\n"}), netVersion)
		err := plainPull(t, movedRoot, movedRelease, movedCatalog)
		if err == nil {
			t.Fatal("a pull adopted a package moved under its tag")
		}
		if !errors.Is(err, recipepin.ErrEntityNotPinned()) {
			t.Fatalf("the halt does not carry the entity-not-pinned condition: %v", err)
		}
		for _, want := range []string{"the recorded pin no longer matches it", "aggrega pin"} {
			if !strings.Contains(cerrors.CauseChain(err), want) {
				t.Errorf("the refusal does not carry %q:\n%s", want, cerrors.CauseChain(err))
			}
		}
		if !bytes.Equal(lockBefore, mustReadFile(t, recipeLockPath(movedEntities[0]))) {
			t.Error("the refused pull rewrote the entity.lock")
		}
		// The tag is restored for the sub-tests that follow.
		pushNet(t, host, pkg, netVersion)
	})

	t.Run("ByManifestDigest", func(t *testing.T) {
		byDigest := []moduleComponent{{name: "net", repository: netRepository, version: manifestDigest}}
		dRoot, dRelease, dCatalog, _ := fresh(t, byDigest, nil)
		dRows := sourceRows(t, dRelease)
		if len(dRows) != 1 || dRows[0].PinnedVersion != manifestDigest {
			t.Fatalf("want one row pinned at %s; got %+v", manifestDigest, dRows)
		}
		dTree := builtTree(t, dRoot, dRelease, dCatalog)
		if got := dTree[moduleDirOf(t, dTree)+"modules/net/main.tf"]; got != netMain {
			t.Errorf("a build by digest vendors %q; want the package's main.tf", got)
		}
	})

	t.Run("TwoModuleSubdirs", func(t *testing.T) {
		both := zipPackage(t,
			[2]string{"modules/a/main.tf", "# module a\n"},
			[2]string{"modules/a/a.tf", "# only in a\n"},
			[2]string{"modules/b/main.tf", "# module b\n"},
			[2]string{"modules/b/b.tf", "# only in b\n"})
		pushOCIModule(t, host, "modules/pair", "v1.0.0", tofuModuleArtifactType, nil,
			ociLayer{MediaType: zipLayerMediaType, Content: both})
		pair := []moduleComponent{
			{name: "alpha", repository: "modules/pair", version: "v1.0.0", subdir: "modules/a"},
			{name: "beta", repository: "modules/pair", version: "v1.0.0", subdir: "modules/b"},
		}
		pRoot, pRelease, pCatalog, _ := fresh(t, pair, nil)
		pRows := sourceRows(t, pRelease)
		if len(pRows) != 2 {
			t.Fatalf("want two rows, one per module_subdir; got %+v", pRows)
		}
		subdirs := []string{pRows[0].ModuleSubdir, pRows[1].ModuleSubdir}
		if !(subdirs[0] == "modules/a" && subdirs[1] == "modules/b") && !(subdirs[0] == "modules/b" && subdirs[1] == "modules/a") {
			t.Errorf("the rows carry module_subdirs %v; want modules/a and modules/b", subdirs)
		}
		a, b := pRows[0], pRows[1]
		a.ModuleSubdir, b.ModuleSubdir = "", ""
		a.ReferencedBy, b.ReferencedBy = nil, nil
		if !reflect.DeepEqual(a, b) {
			t.Errorf("the rows differ in more than module_subdir:\n%+v\n%+v", pRows[0], pRows[1])
		}
		slots := 0
		slotRoot := filepath.Join(pRoot, ".aggrega", "cache", "source-artifacts", sourcekind.TerraformModuleOCI)
		if err := filepath.WalkDir(slotRoot, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.Name() == cacheslot.BlobFile {
				slots++
			}
			return err
		}); err != nil {
			t.Fatalf("walk the source slots: %v", err)
		}
		if slots != 2 {
			t.Errorf("want two source slots, one per module_subdir; found %d", slots)
		}
		pTree := builtTree(t, pRoot, pRelease, pCatalog)
		dir := moduleDirOf(t, pTree) + "modules/"
		for path, want := range map[string]string{
			dir + "alpha/main.tf": "# module a\n", dir + "alpha/a.tf": "# only in a\n",
			dir + "beta/main.tf": "# module b\n", dir + "beta/b.tf": "# only in b\n",
		} {
			if pTree[path] != want {
				t.Errorf("%s is %q; want %q", path, pTree[path], want)
			}
		}
		for _, absent := range []string{dir + "alpha/b.tf", dir + "beta/a.tf", dir + "alpha/modules", dir + "beta/modules"} {
			if _, found := pTree[absent]; found {
				t.Errorf("%s is vendored; each component vendors only its own subdirectory", absent)
			}
		}
	})

	t.Run("FeatureRecipe", func(t *testing.T) {
		withFeature := &moduleComponent{name: "net", repository: netRepository, version: netVersion}
		fRoot, fRelease, fCatalog, _ := fresh(t, []moduleComponent{{name: "app", repository: netRepository, version: netVersion}}, withFeature)
		fRows := sourceRows(t, fRelease)
		if len(fRows) != 1 || fRows[0].Digest != layerDigest {
			t.Fatalf("want the one package pinned once for Recipe and FeatureRecipe; got %+v", fRows)
		}
		if len(fRows[0].ReferencedBy) != 2 {
			t.Errorf("the row is referenced by %+v; want the Recipe and the FeatureRecipe", fRows[0].ReferencedBy)
		}
		fTree := builtTree(t, fRoot, fRelease, fCatalog)
		dir := moduleDirOf(t, fTree) + "modules/"
		for path, want := range map[string]string{
			dir + "net/main.tf": netMain, dir + "net/modules/vpc/main.tf": netVPC,
			dir + "app/main.tf": netMain, dir + "app/modules/vpc/main.tf": netVPC,
		} {
			if fTree[path] != want {
				t.Errorf("%s is %q; want the package's bytes %q (wrote %v)", path, fTree[path], want, sortedTreeKeys(fTree))
			}
		}
	})
}

// recipeLockPath is the entity.lock beside an entity document.
func recipeLockPath(entityPath string) string { return recipepin.LockPath(entityPath) }

// equalTrees reports whether two output trees hold the same files and bytes.
func equalTrees(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for p, body := range a {
		if other, found := b[p]; !found || other != body {
			return false
		}
	}
	return true
}

const (
	// bicepModuleArtifactType is the artifact type of a published Bicep module.
	bicepModuleArtifactType = "application/vnd.ms.bicep.module.artifact"
	// bicepConfigMediaType is the media type of the config such a module carries.
	bicepConfigMediaType = "application/vnd.ms.bicep.module.config.v1+json"
	// bicepTemplateMediaType is the media type of the module's compiled template layer.
	bicepTemplateMediaType = "application/vnd.ms.bicep.module.layer.v1+json"
	// bicepSourceMediaType is the media type of the module's source layer.
	bicepSourceMediaType = "application/vnd.ms.bicep.module.source.v1.tar+gzip"

	// armFixtureDir is the workspace the arm tests build, relative to this
	// package's directory.
	armFixtureDir = "testdata/oci-module-sources/arm-container-app"
	// armTemplatePath is a compiled template written for the tests, relative to
	// this package's directory.
	armTemplatePath = "../../internal/compiler/renderer/testdata/arm-published/container-app/main.json"
)

// TestOCIModuleSourceArmPullAndBuild drives an arm Recipe sourcing a published
// Bicep module from a loopback registry through pin, pull and build. The pull
// records the compiled template layer; the build nests that template as one
// template resource with telemetry off and writes nothing of the module's source layer.
func TestOCIModuleSourceArmPullAndBuild(t *testing.T) {
	template, err := os.ReadFile(armTemplatePath)
	if err != nil {
		t.Fatalf("read the compiled template: %v", err)
	}
	source := []byte("source layer bytes that no output carries")
	host := startRegistry(t, nil)
	const repository, tag = "bicep/avm/res/app/container-app", "0.23.1"
	manifestDigest := pushOCIModule(t, host, repository, tag, bicepModuleArtifactType,
		&ociLayer{MediaType: bicepConfigMediaType, Content: []byte("{}")},
		ociLayer{MediaType: bicepTemplateMediaType, Content: template},
		ociLayer{MediaType: bicepSourceMediaType, Content: source})

	fixture, err := filepath.Abs(armFixtureDir)
	if err != nil {
		t.Fatalf("locate the fixture workspace: %v", err)
	}
	// The standard catalog is found from the package directory, before the test
	// moves into its workspace.
	standardCatalog := standardCatalogDir(t)
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(fixture)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	t.Chdir(root)
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"),
		"workspace:\n  catalog_dirs:\n    - catalog\n    - "+standardCatalog+"\nauto_pull: false\n")
	recipe := filepath.Join(root, "catalog", "recipe", "arm", "container-app-oci", "1.0.0", "recipe.yaml")
	body := mustReadFile(t, recipe)
	const authoredURL = "oci://mcr.microsoft.com/bicep/avm/res/app/container-app"
	if !bytes.Contains(body, []byte(authoredURL)) {
		t.Fatalf("the fixture Recipe does not name %s", authoredURL)
	}
	writeFixture(t, recipe, strings.Replace(string(body), authoredURL, "oci://"+host+"/"+repository, 1))

	if err := pinRun(context.Background(), io.Discard, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), recipe, false); err != nil {
		t.Fatalf("pin the Recipe: %s", cerrors.CauseChain(err))
	}
	releasePath := filepath.Join(root, "release.yaml")
	for _, args := range [][]string{{"pull", "--release", "release.yaml"}, {"build", "--release", "release.yaml"}} {
		var out string
		var err error
		stderr := captureStderr(t, func() { out, err = runAggrega(t, args...) })
		if err != nil {
			t.Fatalf("aggrega %s failed: %v\ncommand output:\n%s\nstderr:\n%s", strings.Join(args, " "), err, out, stderr)
		}
	}

	rows := sourceRows(t, releasePath)
	if len(rows) != 1 {
		t.Fatalf("want one source row; got %+v", rows)
	}
	for _, c := range []struct{ field, got, want string }{
		{"kind", rows[0].Kind, sourcekind.BicepModuleOCI},
		{"url", rows[0].URL, "oci://" + host + "/" + repository},
		{"pinned_version", rows[0].PinnedVersion, tag},
		{"mediatype", rows[0].Mediatype, bicepTemplateMediaType},
		{"digest", rows[0].Digest, sha256Of(template)},
		{"manifest_digest", rows[0].ManifestDigest, manifestDigest},
	} {
		if c.got != c.want {
			t.Errorf("the lockfile row's %s is %q; want %q", c.field, c.got, c.want)
		}
	}

	tree := renderedTree(t, filepath.Join(root, "dist"))
	for p, content := range tree {
		if strings.Contains(content, string(source)) {
			t.Errorf("%s carries the module's source layer", p)
		}
	}
	var wantTemplate map[string]any
	if err := json.Unmarshal(template, &wantTemplate); err != nil {
		t.Fatalf("decode the pushed template: %v", err)
	}
	var nested []map[string]any
	for p, content := range tree {
		if !strings.HasSuffix(p, ".json") {
			continue
		}
		var boundary struct {
			Resources map[string]struct {
				Type       string         `json:"type"`
				Properties map[string]any `json:"properties"`
			} `json:"resources"`
		}
		if json.Unmarshal([]byte(content), &boundary) != nil {
			continue
		}
		for _, r := range boundary.Resources {
			if r.Type == "Microsoft.Resources/deployments" {
				nested = append(nested, r.Properties)
			}
		}
	}
	if len(nested) != 1 {
		t.Fatalf("want one nested template resource in the output; got %d in %v", len(nested), sortedTreeKeys(tree))
	}
	if !reflect.DeepEqual(nested[0]["template"], any(wantTemplate)) {
		t.Errorf("the nested resource's template is not the pushed template:\n%v", nested[0]["template"])
	}
	parameters, _ := nested[0]["parameters"].(map[string]any)
	telemetry, _ := parameters["enableTelemetry"].(map[string]any)
	if telemetry["value"] != false {
		t.Errorf("the nested template resource binds enableTelemetry %v; want false", parameters["enableTelemetry"])
	}
}

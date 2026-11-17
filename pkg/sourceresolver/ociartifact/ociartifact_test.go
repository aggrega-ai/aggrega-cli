// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package ociartifact

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/internal/archive"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

const (
	bicepArtifactType = "application/vnd.ms.bicep.module.artifact"
	bicepConfigType   = "application/vnd.ms.bicep.module.config.v1+json"
	bicepSourceType   = "application/vnd.ms.bicep.module.source.v1.tar+gzip"
	tofuArtifactType  = "application/vnd.opentofu.modulepkg"
	cacheDir          = ".aggrega/cache"
	tag               = "1.2.3"
)

var bicepJSON = []byte(`{"$schema":"https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#","resources":[]}`)

// adapterOver returns the adapter of kind reading target.
func adapterOver(t *testing.T, kind string, target oras.ReadOnlyTarget) *adapter {
	t.Helper()
	row, ok := sourcekind.OCIArtifactOf(kind)
	if !ok {
		t.Fatalf("kind %q carries no OCI artifact row", kind)
	}
	return &adapter{kind: kind, row: row, newTarget: func(string, *http.Client) (oras.ReadOnlyTarget, error) { return target, nil }}
}

func specOf(kind, root, version, subdir string) sourceresolver.SourceSpec {
	return sourceresolver.SourceSpec{
		Kind:          kind,
		URL:           "oci://registry.example.com/modules/net",
		Version:       version,
		ModuleSubdir:  subdir,
		WorkspaceRoot: root,
		CacheDir:      cacheDir,
	}
}

// pushed is a packed artifact: the target, its manifest descriptor and the
// descriptor of the layer the kind selects.
type pushed struct {
	store    *memory.Store
	manifest ocispec.Descriptor
	layer    ocispec.Descriptor
}

func pushLayer(t *testing.T, store *memory.Store, mediaType string, data []byte) ocispec.Descriptor {
	t.Helper()
	desc, err := oras.PushBytes(context.Background(), store, mediaType, data)
	if err != nil {
		t.Fatalf("push %s: %v", mediaType, err)
	}
	return desc
}

func pack(t *testing.T, store *memory.Store, artifactType string, opts oras.PackManifestOptions) ocispec.Descriptor {
	t.Helper()
	manifest, err := oras.PackManifest(context.Background(), store, oras.PackManifestVersion1_1, artifactType, opts)
	if err != nil {
		t.Fatalf("pack manifest: %v", err)
	}
	if err := store.Tag(context.Background(), manifest, tag); err != nil {
		t.Fatalf("tag manifest: %v", err)
	}
	return manifest
}

func bicepArtifact(t *testing.T) pushed {
	t.Helper()
	store := memory.New()
	config := pushLayer(t, store, bicepConfigType, []byte("{}"))
	layer := pushLayer(t, store, "application/vnd.ms.bicep.module.layer.v1+json", bicepJSON)
	source := pushLayer(t, store, bicepSourceType, []byte("source-tar-gz"))
	manifest := pack(t, store, bicepArtifactType, oras.PackManifestOptions{
		ConfigDescriptor: &config, Layers: []ocispec.Descriptor{layer, source},
	})
	return pushed{store: store, manifest: manifest, layer: layer}
}

func zipOf(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range entries {
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", name, err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatalf("write zip entry %q: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func tofuArtifact(t *testing.T, pkg []byte) pushed {
	t.Helper()
	store := memory.New()
	layer := pushLayer(t, store, "archive/zip", pkg)
	manifest := pack(t, store, tofuArtifactType, oras.PackManifestOptions{Layers: []ocispec.Descriptor{layer}})
	return pushed{store: store, manifest: manifest, layer: layer}
}

var vpcPackage = map[string]string{"main.tf": "# root\n", "modules/vpc/main.tf": "# vpc\n"}

func assertNothingCached(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(cacheslot.SourceArtifactsRoot(root, cacheDir)); !os.IsNotExist(err) {
		t.Errorf("a refused resolution left %s behind (stat error: %v)", cacheslot.SourceArtifactsRoot(root, cacheDir), err)
	}
}

func TestResolveBicepModuleTakesTheCompiledLayer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	art := bicepArtifact(t)
	got, err := adapterOver(t, sourcekind.BicepModuleOCI, art.store).Resolve(context.Background(),
		specOf(sourcekind.BicepModuleOCI, root, tag, ""))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Digest != art.layer.Digest.String() {
		t.Errorf("Digest = %q; want the compiled layer's descriptor digest %q", got.Digest, art.layer.Digest)
	}
	if got.Digest != archive.Digest(bicepJSON) {
		t.Errorf("Digest = %q; want the digest of the layer bytes", got.Digest)
	}
	if got.Mediatype != "application/vnd.ms.bicep.module.layer.v1+json" {
		t.Errorf("Mediatype = %q; want the layer's media type, not the artifact type", got.Mediatype)
	}
	if got.ManifestDigest != art.manifest.Digest.String() {
		t.Errorf("ManifestDigest = %q; want %q", got.ManifestDigest, art.manifest.Digest)
	}
	if got.PinnedVersion != tag || got.Kind != sourcekind.BicepModuleOCI || got.ModuleSubdir != "" {
		t.Errorf("resolved identity = kind %q version %q module_subdir %q", got.Kind, got.PinnedVersion, got.ModuleSubdir)
	}
	if filepath.Base(got.CachePath) != cacheslot.BlobFile {
		t.Errorf("CachePath = %q; want a %s file", got.CachePath, cacheslot.BlobFile)
	}
	blob, err := os.ReadFile(got.CachePath)
	if err != nil {
		t.Fatalf("read blob: %v", err)
	}
	if !bytes.Equal(blob, bicepJSON) {
		t.Errorf("cached blob = %q; want the layer bytes verbatim", blob)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(filepath.Dir(got.CachePath), cacheslot.ManifestFile))
	if err != nil {
		t.Fatalf("read persisted manifest: %v", err)
	}
	if verr := oci.VerifyContentAgainstManifest(manifestBytes, blob, got.Mediatype); verr != nil {
		t.Errorf("the persisted manifest does not authorize the cached layer: %v", verr)
	}
	meta, err := metadata.Read(filepath.Join(filepath.Dir(got.CachePath), "metadata.yaml"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if meta.Mediatype != got.Mediatype || meta.Kind != sourcekind.BicepModuleOCI || meta.Digest != got.Digest {
		t.Errorf("metadata = %+v; want the layer's media type, the kind and the digest", meta)
	}
}

func TestResolveBicepModuleWithOneLayerAndNoArtifactType(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store := memory.New()
	config := pushLayer(t, store, bicepConfigType, []byte("{}"))
	layer := pushLayer(t, store, "application/vnd.ms.bicep.module.layer.v1+json", bicepJSON)
	manifest := pack(t, store, "", oras.PackManifestOptions{ConfigDescriptor: &config, Layers: []ocispec.Descriptor{layer}})

	got, err := adapterOver(t, sourcekind.BicepModuleOCI, store).Resolve(context.Background(),
		specOf(sourcekind.BicepModuleOCI, root, tag, ""))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Digest != layer.Digest.String() || got.ManifestDigest != manifest.Digest.String() ||
		got.Mediatype != "application/vnd.ms.bicep.module.layer.v1+json" {
		t.Errorf("resolved = digest %q manifest %q mediatype %q", got.Digest, got.ManifestDigest, got.Mediatype)
	}
	blob, err := os.ReadFile(got.CachePath)
	if err != nil || !bytes.Equal(blob, bicepJSON) {
		t.Errorf("cached blob = %q (%v); want the layer bytes", blob, err)
	}
}

func TestResolveTerraformModulePackage(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pkg := zipOf(t, vpcPackage)
	art := tofuArtifact(t, pkg)
	got, err := adapterOver(t, sourcekind.TerraformModuleOCI, art.store).Resolve(context.Background(),
		specOf(sourcekind.TerraformModuleOCI, root, tag, "modules/vpc"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ModuleSubdir != "modules/vpc" {
		t.Errorf("ModuleSubdir = %q; want modules/vpc", got.ModuleSubdir)
	}
	if got.Chart != "" {
		t.Errorf("Chart = %q; want none recorded", got.Chart)
	}
	if got.Mediatype != "archive/zip" || got.Digest != archive.Digest(pkg) {
		t.Errorf("Mediatype = %q Digest = %q; want archive/zip and the package's digest", got.Mediatype, got.Digest)
	}
	if !strings.Contains(filepath.ToSlash(got.CachePath), "/modules/vpc/") {
		t.Errorf("CachePath %q does not hold the module_subdir segment", got.CachePath)
	}
	blob, err := os.ReadFile(got.CachePath)
	if err != nil || !bytes.Equal(blob, pkg) {
		t.Errorf("cached blob differs from the package as published (%v)", err)
	}
	meta, err := metadata.Read(filepath.Join(filepath.Dir(got.CachePath), "metadata.yaml"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if meta.Mediatype != "archive/zip" {
		t.Errorf("metadata mediatype = %q; want archive/zip", meta.Mediatype)
	}
}

func TestResolveByManifestDigest(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	art := bicepArtifact(t)
	version := art.manifest.Digest.String()
	// A memory store resolves a reference only through a tag; a registry resolves
	// a digest itself, so the digest is tagged here to stand in for that.
	if err := art.store.Tag(context.Background(), art.manifest, version); err != nil {
		t.Fatalf("tag by digest: %v", err)
	}
	got, err := adapterOver(t, sourcekind.BicepModuleOCI, art.store).Resolve(context.Background(),
		specOf(sourcekind.BicepModuleOCI, root, version, ""))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.PinnedVersion != version {
		t.Errorf("PinnedVersion = %q; want the manifest digest %q", got.PinnedVersion, version)
	}
	if _, err := metadata.Read(filepath.Join(filepath.Dir(got.CachePath), "metadata.yaml")); err != nil {
		t.Errorf("metadata of a slot named for a digest does not validate: %v", err)
	}
}

// tamperedTarget serves the layer of media type mediaType with other bytes than
// its descriptor commits to.
type tamperedTarget struct {
	oras.ReadOnlyTarget
	mediaType string
}

func (p tamperedTarget) Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	if desc.MediaType == p.mediaType {
		return io.NopCloser(strings.NewReader(strings.Repeat("x", int(desc.Size)))), nil
	}
	return p.ReadOnlyTarget.Fetch(ctx, desc)
}

// deniedTarget answers every resolution with the registry's 401.
type deniedTarget struct{ oras.ReadOnlyTarget }

func (deniedTarget) Resolve(context.Context, string) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, &errcode.ErrorResponse{StatusCode: http.StatusUnauthorized}
}

func resolveRefusal(t *testing.T, kind string, target oras.ReadOnlyTarget, version, subdir string) (string, error) {
	t.Helper()
	root := t.TempDir()
	_, err := adapterOver(t, kind, target).Resolve(context.Background(), specOf(kind, root, version, subdir))
	if err == nil {
		t.Fatal("Resolve returned no error")
	}
	assertNothingCached(t, root)
	return root, err
}

func wantShortCode(t *testing.T, err error, short string) {
	t.Helper()
	if code, ok := cerrors.ShortCodeOf(err); !ok || code != short {
		t.Errorf("ShortCodeOf(err) = %q, %v; want %s\n%v", code, ok, short, err)
	}
}

func TestResolveRefusesAnArtifactWithNoContentLayer(t *testing.T) {
	t.Parallel()
	store := memory.New()
	source := pushLayer(t, store, bicepSourceType, []byte("source-tar-gz"))
	config := pushLayer(t, store, bicepConfigType, []byte("{}"))
	pack(t, store, bicepArtifactType, oras.PackManifestOptions{ConfigDescriptor: &config, Layers: []ocispec.Descriptor{source}})
	_, err := resolveRefusal(t, sourcekind.BicepModuleOCI, store, tag, "")
	if !errors.Is(err, oci.ErrNoContentLayer()) {
		t.Errorf("error does not match ErrNoContentLayer: %v", err)
	}
	wantShortCode(t, err, "E0140")
}

func TestResolveRefusesAnArtifactWithTwoContentLayers(t *testing.T) {
	t.Parallel()
	store := memory.New()
	one := pushLayer(t, store, "archive/zip", zipOf(t, map[string]string{"a.tf": "1"}))
	two := pushLayer(t, store, "archive/zip", zipOf(t, map[string]string{"b.tf": "2"}))
	pack(t, store, tofuArtifactType, oras.PackManifestOptions{Layers: []ocispec.Descriptor{one, two}})
	_, err := resolveRefusal(t, sourcekind.TerraformModuleOCI, store, tag, "")
	if !errors.Is(err, oci.ErrAmbiguousContentLayer()) {
		t.Errorf("error does not match ErrAmbiguousContentLayer: %v", err)
	}
	wantShortCode(t, err, "E0132")
}

func TestResolveRefusesLayerBytesThatDifferFromTheirDescriptor(t *testing.T) {
	t.Parallel()
	art := bicepArtifact(t)
	target := tamperedTarget{ReadOnlyTarget: art.store, mediaType: art.layer.MediaType}
	_, err := resolveRefusal(t, sourcekind.BicepModuleOCI, target, tag, "")
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Errorf("error does not match ErrFetchIntegrity: %v", err)
	}
	wantShortCode(t, err, "E0135")
}

func TestResolveRefusesAModuleSubdirThatClimbs(t *testing.T) {
	t.Parallel()
	for _, subdir := range []string{`../x`, `..\x`} {
		t.Run(subdir, func(t *testing.T) {
			t.Parallel()
			art := tofuArtifact(t, zipOf(t, vpcPackage))
			_, err := resolveRefusal(t, sourcekind.TerraformModuleOCI, art.store, tag, subdir)
			if !errors.Is(err, oci.ErrCacheLocationOutsideCache()) {
				t.Errorf("error does not match ErrCacheLocationOutsideCache: %v", err)
			}
			wantShortCode(t, err, "E0307")
		})
	}
}

func TestResolveRefusesAModuleSubdirThePackageLacks(t *testing.T) {
	t.Parallel()
	art := tofuArtifact(t, zipOf(t, vpcPackage))
	_, err := resolveRefusal(t, sourcekind.TerraformModuleOCI, art.store, tag, "modules/absent")
	if !errors.Is(err, oci.ErrFetchNotFound()) {
		t.Errorf("error does not match ErrFetchNotFound: %v", err)
	}
	wantShortCode(t, err, "E0137")
	if !strings.Contains(err.Error(), "modules/absent") {
		t.Errorf("error does not name the module_subdir: %v", err)
	}
}

func TestResolveRefusesAPackageThatIsNotAZip(t *testing.T) {
	t.Parallel()
	art := tofuArtifact(t, []byte("this is not a zip archive"))
	_, err := resolveRefusal(t, sourcekind.TerraformModuleOCI, art.store, tag, "")
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Errorf("error does not match ErrFetchIntegrity: %v", err)
	}
	wantShortCode(t, err, "E0135")
}

func TestResolveRefusesAZipEntryThatEscapes(t *testing.T) {
	t.Parallel()
	art := tofuArtifact(t, zipOf(t, map[string]string{"main.tf": "# root\n", "../x": "escape"}))
	_, err := resolveRefusal(t, sourcekind.TerraformModuleOCI, art.store, tag, "")
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Errorf("error does not match ErrFetchIntegrity: %v", err)
	}
	wantShortCode(t, err, "E0135")
}

func TestResolveRefusesAnUnauthorizedTarget(t *testing.T) {
	t.Parallel()
	_, err := resolveRefusal(t, sourcekind.BicepModuleOCI, deniedTarget{memory.New()}, tag, "")
	if !errors.Is(err, oci.ErrFetchUnauthorized()) {
		t.Errorf("error does not match ErrFetchUnauthorized: %v", err)
	}
	wantShortCode(t, err, "E0138")
}

func TestResolveRefusesAnEvaluatedVersionThatIsNoTagOrDigest(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"", "a/b"} {
		t.Run("version="+version, func(t *testing.T) {
			t.Parallel()
			art := bicepArtifact(t)
			_, err := resolveRefusal(t, sourcekind.BicepModuleOCI, art.store, version, "")
			if !errors.Is(err, oci.ErrFetchNotFound()) {
				t.Errorf("error does not match ErrFetchNotFound: %v", err)
			}
			wantShortCode(t, err, "E0137")
			if errors.Is(err, oci.ErrFetchNetworkUnreachable()) {
				t.Errorf("a version no registry was asked about reads as an unreachable network: %v", err)
			}
			if !strings.Contains(err.Error(), `"`+version+`"`) && !strings.Contains(err.Error(), "version="+version) {
				t.Errorf("error does not name the version %q: %v", version, err)
			}
		})
	}
}

func TestResolveRegistersOneAdapterPerModuleKind(t *testing.T) {
	t.Parallel()
	registered := sourceresolver.Kinds()
	for _, kind := range sourcekind.OCIModuleKinds() {
		found := false
		for _, r := range registered {
			found = found || r == kind
		}
		if !found {
			t.Errorf("no adapter is registered for %q", kind)
		}
	}
}

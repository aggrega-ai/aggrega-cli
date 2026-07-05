// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	ocierr "github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
)

func TestOCIResolveHappyPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	store, archiveBytes, _ := buildChartStore(t, "mariadb", "25.0.8")
	a := &ociAdapter{newTarget: func(string) (oras.ReadOnlyTarget, error) { return store, nil }}

	got, err := a.Resolve(sourceresolver.SourceSpec{
		Kind:          KindOCI,
		URL:           "oci://hub.aggrega.ai/charts/mariadb",
		Version:       "25.0.8",
		WorkspaceRoot: root,
		CacheDir:      ".aggrega/cache",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Kind != KindOCI {
		t.Errorf("Kind = %q; want %s", got.Kind, KindOCI)
	}
	if got.PinnedVersion != "25.0.8" {
		t.Errorf("PinnedVersion = %q; want 25.0.8", got.PinnedVersion)
	}
	if !filenameHasSuffix(got.CachePath, "blob.tgz") {
		t.Errorf("CachePath should end in blob.tgz; got %s", got.CachePath)
	}
	blob, err := os.ReadFile(got.CachePath)
	if err != nil {
		t.Fatalf("read blob: %v", err)
	}
	if len(blob) != len(archiveBytes) {
		t.Errorf("blob size = %d; want %d", len(blob), len(archiveBytes))
	}
	meta, err := metadata.Read(filepath.Join(filepath.Dir(got.CachePath), "metadata.yaml"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if meta.Mediatype != MediatypeHelmChart {
		t.Errorf("metadata mediatype = %q; want %s", meta.Mediatype, MediatypeHelmChart)
	}
	// The OCI manifest is persisted beside the archive — the authority the
	// build-time read verifies the cached chart against.
	manifestBytes, err := os.ReadFile(filepath.Join(filepath.Dir(got.CachePath), cacheslot.ManifestFile))
	if err != nil {
		t.Fatalf("read persisted manifest: %v", err)
	}
	if verr := ocierr.VerifyContentAgainstManifest(manifestBytes, blob); verr != nil {
		t.Errorf("persisted manifest does not authority the cached archive: %v", verr)
	}
}

// TestOCIResolveSetsManifestDigest proves the OCI adapter records the manifest
// digest — the independent root of trust — on the ResolvedSource, and that it
// equals the digest of the manifest bytes persisted beside the archive. That
// equality (manifest_digest == ContentDigest(persisted manifest)) is the
// invariant the build-time VerifyManifestDigest relies on: the lockfile pin the
// cached manifest is later re-hashed against.
func TestOCIResolveSetsManifestDigest(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	store, _, manifestDigest := buildChartStore(t, "mariadb", "25.0.8")
	a := &ociAdapter{newTarget: func(string) (oras.ReadOnlyTarget, error) { return store, nil }}

	got, err := a.Resolve(sourceresolver.SourceSpec{
		Kind:          KindOCI,
		URL:           "oci://hub.aggrega.ai/charts/mariadb",
		Version:       "25.0.8",
		WorkspaceRoot: root,
		CacheDir:      ".aggrega/cache",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ManifestDigest == "" {
		t.Fatal("ManifestDigest is empty; OCI resolve must record the manifest digest")
	}
	if got.ManifestDigest != manifestDigest {
		t.Errorf("ManifestDigest = %q; want the push manifest digest %q", got.ManifestDigest, manifestDigest)
	}
	if got.ManifestDigest == got.Digest {
		t.Errorf("ManifestDigest must be independent of the content Digest; both = %q", got.Digest)
	}
	// The pin must equal the digest of the manifest bytes persisted in the slot —
	// the exact value the build-time VerifyManifestDigest recomputes and compares.
	manifestBytes, err := os.ReadFile(filepath.Join(filepath.Dir(got.CachePath), cacheslot.ManifestFile))
	if err != nil {
		t.Fatalf("read persisted manifest: %v", err)
	}
	if rehashed := digest.ContentDigest(manifestBytes); rehashed != got.ManifestDigest {
		t.Errorf("ContentDigest(persisted manifest) = %q; want the recorded ManifestDigest %q", rehashed, got.ManifestDigest)
	}
}

func TestOCIResolveNotFoundWrapsSentinel(t *testing.T) {
	t.Parallel()
	store, _, _ := buildChartStore(t, "mariadb", "25.0.8")
	a := &ociAdapter{newTarget: func(string) (oras.ReadOnlyTarget, error) { return store, nil }}

	_, err := a.Resolve(sourceresolver.SourceSpec{
		Kind:          KindOCI,
		URL:           "oci://hub.aggrega.ai/charts/mariadb",
		Version:       "9.9.9-missing",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	})
	if err == nil {
		t.Fatal("expected error for missing tag; got nil")
	}
	if !errors.Is(err, sourceresolver.ErrFetchNotFound) {
		t.Errorf("error should wrap ErrFetchNotFound; got %v", err)
	}
}

func TestOCIResolveTargetFactoryFailureWrapsNetwork(t *testing.T) {
	t.Parallel()
	boom := errors.New("dial tcp: lookup failed")
	a := &ociAdapter{newTarget: func(string) (oras.ReadOnlyTarget, error) { return nil, boom }}

	_, err := a.Resolve(sourceresolver.SourceSpec{
		Kind:          KindOCI,
		URL:           "oci://hub.aggrega.ai/charts/mariadb",
		Version:       "25.0.8",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	})
	if !errors.Is(err, sourceresolver.ErrFetchNetworkUnreachable) {
		t.Errorf("error should wrap ErrFetchNetworkUnreachable; got %v", err)
	}
}

func buildChartStore(t *testing.T, _ string, tag string) (oras.ReadOnlyTarget, []byte, string) {
	t.Helper()
	store := memory.New()
	body := []byte("fake-helm-chart-tar-gz-bytes\n")
	ctx := context.Background()
	manifestDigest, err := ocierr.Push(ctx, store, tag, MediatypeHelmChart, body)
	if err != nil {
		t.Fatalf("seed chart store: %v", err)
	}
	return store, body, manifestDigest
}

func filenameHasSuffix(p, suffix string) bool {
	if len(p) < len(suffix) {
		return false
	}
	return p[len(p)-len(suffix):] == suffix
}

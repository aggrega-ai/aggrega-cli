// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// archivedRecipeBody is a Recipe minimal enough to publish and to lint. Its
// version is substituted so one body serves as both the predecessor and the
// candidate.
const archivedRecipeBody = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: service
  version: "%s"
  description: >
    A generic HTTP service on Kubernetes via the helm provider.
spec:
  capability: helm
  implements: example/compute/service
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: "nginx:alpine"
        ports:
          http: 8080
`

// writeRecipeVersionDir writes a Recipe into the per-version directory layout
// the publish path requires and returns the entity path.
func writeRecipeVersionDir(t *testing.T, version string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "recipe.yaml")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(archivedRecipeBody, version)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// publishRecipe puts a Recipe into the in-memory registry as an archive —
// through the real bundler, carrying one file beside the document, which is the
// shape a Recipe declaring a file publishes in — so the artifact under test is
// the artifact a registry holds and not an approximation of one.
func publishRecipe(t *testing.T, target *memoryRegistryTarget, version string) {
	t.Helper()
	path := writeRecipeVersionDir(t, version)
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "companion.yaml"), []byte("carried: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blob, mediaType, err := oci.Bundle("Recipe", path, []string{"companion.yaml"})
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if !oci.IsTarGzBundle(blob) {
		t.Fatal("a Recipe carrying a file no longer publishes as an archive; this test is then about nothing")
	}
	if _, perr := oci.Push(t.Context(), target.Store, version, mediaType, blob, nil); perr != nil {
		t.Fatalf("Push: %v", perr)
	}
	target.tags = append(target.tags, version)
}

// TestPrePublishComparesAgainstAnArchivedPredecessor is the evolution gate's
// coverage for an entity published as an archive.
//
// The predecessor's published bytes are a tar.gz, so the artifact the gate pulls
// is an archive rather than a document. The assertion is that the comparison is
// PERFORMED — the predecessor's own version is selected and a rule verdict is
// reported over it — rather than that the run merely completed: a gate that
// fetched nothing and a gate that compared nothing both exit without objecting,
// and only the reported verdict tells them apart.
func TestPrePublishComparesAgainstAnArchivedPredecessor(t *testing.T) {
	target := &memoryRegistryTarget{Store: memory.New()}
	publishRecipe(t, target, "1.0.0")

	var diag bytes.Buffer
	err := RunEntity(t.Context(), Config{
		EntityPath:     writeRecipeVersionDir(t, "1.0.1"),
		PrePublish:     true,
		PluginPath:     wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm"),
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		FetchTarget:    target,
		Workspace:      resolveTempWorkspace(t),
		DiagOut:        &diag,
	})
	if err != nil {
		t.Fatalf("publishing a second version of a Recipe must not be refused by the evolution gate: %v (diag: %s)",
			err, diag.String())
	}
	if strings.Contains(diag.String(), "first publication") {
		t.Fatalf("the predecessor was not found at all, so nothing was compared; diag: %s", diag.String())
	}
	if !strings.Contains(diag.String(), "schema-evolution") {
		t.Errorf("the evolution rule reported no verdict over the archived predecessor, so the comparison did not happen; diag: %s",
			diag.String())
	}
}

// TestPrePublishReadsTheArchivedPredecessorsOwnIdentity pins the narrower fact
// underneath the run above: the predecessor selected is read out of the
// artifact, so its version and its converted body are the entity's and not an
// empty envelope. A conversion applied to the artifact directly yields neither.
func TestPrePublishReadsTheArchivedPredecessorsOwnIdentity(t *testing.T) {
	target := &memoryRegistryTarget{Store: memory.New()}
	publishRecipe(t, target, "1.0.0")

	candidate := lintEntityHeader{Kind: "Recipe"}
	candidate.Metadata.Name = "service"
	candidate.Metadata.Version = "1.0.1"
	candidate.Spec.Capability = "helm"

	var diag bytes.Buffer
	previousJSON, previousVersion, err := fetchPreviousRegistryVersion(
		t.Context(), target, resolveTempWorkspace(t).Config(), candidate, "", &diag)
	if err != nil {
		t.Fatalf("fetching an archived predecessor must succeed: %v", err)
	}
	if previousVersion != "1.0.0" {
		t.Errorf("selected predecessor %q; want 1.0.0", previousVersion)
	}
	if !strings.Contains(string(previousJSON), `"capability"`) {
		t.Errorf("the converted predecessor is not the entity document; got %q", string(previousJSON))
	}
}

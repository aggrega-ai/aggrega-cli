// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// manifestAnnotationsForTag fetches the manifest tagged `tag` on store and
// returns its annotation map, read by unmarshalling the raw manifest JSON
// locally so the assertion does not depend on any package-internal view.
func manifestAnnotationsForTag(t *testing.T, store *memory.Store, tag string) map[string]string {
	t.Helper()
	ctx := context.Background()
	desc, err := store.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("resolve %q: %v", tag, err)
	}
	manifestBytes, err := content.FetchAll(ctx, store, desc)
	if err != nil {
		t.Fatalf("fetch manifest %q: %v", tag, err)
	}
	var m struct {
		Annotations map[string]string `json:"annotations"`
	}
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		t.Fatalf("parse manifest %q: %v", tag, err)
	}
	return m.Annotations
}

// TestPushStampsCatalogSearchableAnnotations proves the catalog write site
// derives the searchable annotations from the entity's own metadata: the
// description, the category (from the component.aggrega.ai/category label), and
// the kind land on the published manifest, and the plugin-only facets are
// absent.
func TestPushStampsCatalogSearchableAnnotations(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "1.0.0", "entity.yaml")
	writeEntityFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api-server
  version: 1.0.0
  description: The API server component.
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: compute
spec:
  schema:
    values:
      type: object
`)

	store := memory.New()
	if err := pushRun(pushConfig{
		ws:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		EntityPath: entityPath,
		Registry:   "example.com",
		PushTarget: store,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}

	got := manifestAnnotationsForTag(t, store, "1.0.0")
	if got[ocispec.AnnotationDescription] != "The API server component." {
		t.Errorf("description annotation = %q; want %q", got[ocispec.AnnotationDescription], "The API server component.")
	}
	if got[oci.AnnotationCategory] != "compute" {
		t.Errorf("category annotation = %q; want %q", got[oci.AnnotationCategory], "compute")
	}
	if got[oci.AnnotationKind] != "Component" {
		t.Errorf("kind annotation = %q; want %q", got[oci.AnnotationKind], "Component")
	}
	if _, ok := got[oci.AnnotationPluginType]; ok {
		t.Error("a Component manifest must not carry a plugin-type annotation")
	}
	if _, ok := got[oci.AnnotationCapabilities]; ok {
		t.Error("a Component manifest must not carry a capabilities annotation")
	}
}

// TestPushStampsPluginSearchableAnnotations proves the plugin write site derives
// the searchable annotations from the entity as pushed: the description, the
// kind, the socket type (metadata.pluginType), and the capabilities (in author
// order) land on the published manifest, and a plugin carries no category.
func TestPushStampsPluginSearchableAnnotations(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "frp-step-patch.yaml")
	writeEntityFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: patch
  pluginType: pipeline-step
  version: "1.0.0"
  description: A patch-applying feature-recipe step.
spec:
  abiVersion: v1alpha1
  pipelineStep:
    capabilities:
      - helm
  exports:
    - name: step
      operates: patch
      scope: own
`)
	if _, err := os.Stat(pluginPath); err != nil {
		t.Fatalf("fixture .wasm missing: %v", err)
	}

	store := memory.New()
	if err := pushRun(pushConfig{
		ws:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		EntityPath: entityPath,
		PluginPath: pluginPath,
		Registry:   "hub.aggrega.ai",
		PushTarget: store,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}

	got := manifestAnnotationsForTag(t, store, "1.0.0")
	if got[ocispec.AnnotationDescription] != "A patch-applying feature-recipe step." {
		t.Errorf("description annotation = %q; want %q", got[ocispec.AnnotationDescription], "A patch-applying feature-recipe step.")
	}
	if got[oci.AnnotationKind] != "Plugin" {
		t.Errorf("kind annotation = %q; want %q", got[oci.AnnotationKind], "Plugin")
	}
	if got[oci.AnnotationPluginType] != "pipeline-step" {
		t.Errorf("plugin-type annotation = %q; want %q", got[oci.AnnotationPluginType], "pipeline-step")
	}
	if got[oci.AnnotationCapabilities] != "helm" {
		t.Errorf("capabilities annotation = %q; want %q", got[oci.AnnotationCapabilities], "helm")
	}
	if _, ok := got[oci.AnnotationCategory]; ok {
		t.Error("a Plugin manifest must not carry a category annotation")
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"oras.land/oras-go/v2/content/memory"
)

// These three tests cover the structured-source successful-build
// requirement: given previously-rejected structured-source fixtures
// (one each for helm, helm-chart, compose), aggrega build MUST
// produce successful render output.
//
// Each test exercises the full orchestrator path through RenderAll:
// fixture lockfile + pre-populated cache slot + Recipe with
// structured spec.source → RenderAll resolves the source via
// pkg/sourcecache, dispatches to the engine, engine renders from
// the resolved string path. Asserts no error + non-empty output.

func TestRenderAll_BuildsStructuredHelmSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	chart := minimalHelmChartContents()
	blob := buildIntegrationTGZ(t, chart)
	digest := integrationDigest(blob)
	seedIntegrationCacheSlot(t, root, "helm-oci-chart", "oci://r/c", "1.0.0", blob, digest)

	input := integrationInput(t, root, "core/compute/api-server", "api", "helm", digest, "oci://r/c", "1.0.0")
	lookup := structuredSourceRecipeLookup("helm", "helm-oci-chart", "oci://r/c", "1.0.0")
	factory := wasmEngineFactory(t)

	result, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if len(result.Files) == 0 {
		t.Fatalf("RenderAll produced no files; expected sourced render output")
	}
	found := false
	for path := range result.Files {
		if strings.Contains(path, "deployment") {
			found = true
		}
	}
	if !found {
		t.Errorf("structured helm source did not render a deployment; keys: %v", integrationKeys(result.Files))
	}
}

func TestRenderAll_BuildsStructuredHelmChartSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	chart := minimalHelmChartContents()
	blob := buildIntegrationTGZ(t, chart)
	digest := integrationDigest(blob)
	seedIntegrationCacheSlot(t, root, "helm-oci-chart", "oci://r/c", "1.0.0", blob, digest)

	input := integrationInput(t, root, "core/compute/api-server", "api", "helm-chart", digest, "oci://r/c", "1.0.0")
	lookup := structuredSourceRecipeLookup("helm-chart", "helm-oci-chart", "oci://r/c", "1.0.0")
	factory := wasmEngineFactory(t)

	result, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if len(result.Files) == 0 {
		t.Fatalf("RenderAll produced no files; expected sourced helm-chart output")
	}
	foundChart := false
	for path := range result.Files {
		if strings.Contains(path, "Chart.yaml") {
			foundChart = true
		}
	}
	if !foundChart {
		t.Errorf("structured helm-chart source did not copy Chart.yaml; keys: %v", integrationKeys(result.Files))
	}
}

func TestRenderAll_BuildsStructuredComposeSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	fragment := map[string]string{
		"compose.yaml": "services:\n  api:\n    image: nginx:1.27\n    ports:\n      - \"8080:8080\"\n",
	}
	blob := buildIntegrationTGZ(t, fragment)
	digest := integrationDigest(blob)
	seedIntegrationCacheSlot(t, root, "compose-https-fragment", "https://example.com/compose.yaml", "1.0.0", blob, digest)

	input := integrationInputCompose(t, root, "core/compute/api-server", "api", digest, "https://example.com/compose.yaml", "1.0.0")
	lookup := structuredSourceRecipeLookup("compose", "compose-https-fragment", "https://example.com/compose.yaml", "1.0.0")
	factory := wasmEngineFactory(t)

	result, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if len(result.Files) == 0 {
		t.Fatalf("RenderAll produced no files; expected sourced compose output")
	}
	if got, want := assembledServices(t, result.Files), []string{"api"}; !slices.Equal(got, want) {
		t.Errorf("structured compose source assembled services %v, want %v", got, want)
	}
}

// minimalHelmChartContents returns a minimal valid Helm chart's
// file content keyed by relative path. Sufficient input for
// helm.template to render a Deployment without error. Mirrors the
// shape of plugins/internal/helmcore/testdata/minimal-chart.
func minimalHelmChartContents() map[string]string {
	return map[string]string{
		"Chart.yaml":                "apiVersion: v2\nname: minimal\nversion: 0.1.0\n",
		"values.yaml":               "image: app:1.0.0\nreplicas: 1\n",
		"templates/deployment.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: {{ .Release.Name }}\nspec:\n  replicas: {{ .Values.replicas }}\n  template:\n    spec:\n      containers:\n        - name: app\n          image: {{ .Values.image }}\n",
	}
}

func buildIntegrationTGZ(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
			ModTime:  time.Unix(0, 0).UTC(),
			Format:   tar.FormatPAX,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar body: %v", err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func integrationDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func seedIntegrationCacheSlot(t *testing.T, workspaceRoot, kind, url, version string, blob []byte, digest string) {
	t.Helper()
	slot, err := sourcecache.SlotPath(workspaceRoot, ".aggrega/cache", kind, url, "", "", version, digest)
	if err != nil {
		t.Fatalf("slot: %v", err)
	}
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatalf("mkdir slot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(slot, "blob.tgz"), blob, 0o644); err != nil {
		t.Fatalf("write blob: %v", err)
	}
	writeIntegrationChartManifest(t, slot, blob)
	meta := metadata.CacheMetadata{
		Kind: kind, URL: url, Version: version, Digest: digest,
		Mediatype: "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
		PulledAt:  time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
	}
	if err := metadata.Write(filepath.Join(slot, "metadata.yaml"), meta); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
}

// writeIntegrationChartManifest writes the OCI manifest a real helm-oci-chart
// pull persists beside blob.tgz — the manifest whose single layer commits to
// blob's content digest. An OCI-chart cache slot without it reads as a
// fail-closed miss.
func writeIntegrationChartManifest(t *testing.T, slot string, blob []byte) {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, "seed", "application/vnd.cncf.helm.chart.content.v1.tar+gzip", blob, nil); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	res, err := oci.Pull(ctx, store, "seed")
	if err != nil {
		t.Fatalf("seed pull: %v", err)
	}
	if err := os.WriteFile(filepath.Join(slot, cacheslot.ManifestFile), res.ManifestBytes, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func integrationInput(t *testing.T, workspaceRoot, componentType, nodeID, _provider, digest, url, version string) renderer.RendererInput {
	t.Helper()
	labels := types.OrderedMap[string]{}
	labels.Set("component.aggrega.ai/type", "server")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(nodeID, types.IRNode{
		Component:       componentType,
		Labels:          labels,
		ResolvedVersion: "1.0.0",
		BoundaryPath:    "local/default",
	})
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set(nodeID, json.RawMessage(`{"image":"app:1.0.0","replicas":1}`))
	return renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test-app"},
			Spec: types.IRSpec{
				Nodes:         nodes,
				ValuesMapping: valuesMapping,
				InfraMapping:  types.OrderedMap[json.RawMessage]{},
				Provenance:    types.IRProvenance{CompilerVersion: "0.1.0", AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test-app"}},
			Release: &types.Release{
				Metadata: types.ReleaseMetadata{Name: "test-release", Version: "1.0.0", Environment: "local"},
			},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
		Lockfile: &lockfile.Lockfile{
			Version:     1,
			Release:     "./r.yaml",
			GeneratedAt: "2026-05-22T10:00:00Z",
			SourceArtifacts: []lockfile.SourceArtifact{{
				Kind:          "helm-oci-chart",
				URL:           url,
				PinnedVersion: version,
				Digest:        digest,
				Mediatype:     "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
				ReferencedBy: []lockfile.SourceArtifactReference{
					{Entity: "recipe", URI: componentType + ":1.0.0", Field: "spec.source"},
				},
			}},
		},
		WorkspaceRoot: workspaceRoot,
		CacheDir:      ".aggrega/cache",
	}
}

// integrationInputCompose is integrationInput for a compose Recipe sourcing a
// fetched compose file. The node's values are empty rather than the chart's
// image and replicas: a compose Recipe's values are laid over the compose file
// as compose keys, where a top-level image or replicas is a key the assembled
// file cannot hold and the build refuses, so a chart's values would make every
// compose build here fail on them instead of on what each test reads.
func integrationInputCompose(t *testing.T, workspaceRoot, componentType, nodeID, digest, url, version string) renderer.RendererInput {
	t.Helper()
	in := integrationInput(t, workspaceRoot, componentType, nodeID, "compose", digest, url, version)
	in.Lockfile.SourceArtifacts[0].Kind = "compose-https-fragment"
	in.Lockfile.SourceArtifacts[0].Mediatype = "application/x-yaml"
	in.Graph.Spec.ValuesMapping.Set(nodeID, json.RawMessage(`{}`))
	return in
}

func structuredSourceRecipeLookup(provider, kind, url, version string) renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/compute/api-server" {
				return nil, false
			}
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec: types.RecipeSpec{
					Capability: provider,
					Implements: "core/compute/api-server",
					Source: map[string]any{
						"kind":    kind,
						"url":     url,
						"version": version,
					},
				},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
}

func integrationKeys(m map[string]renderer.AssembledFile) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const chartMediatype = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"

// seedChartCache writes a cached helm-oci-chart slot for (url, ver) and returns
// its digest, so a lockfile-pinned source resolves to a real cache hit.
func seedChartCache(t *testing.T, root, url, ver string) string {
	t.Helper()
	blob := buildTGZForResolveTest(t, map[string]string{
		"Chart.yaml": "apiVersion: v2\nname: db\nversion: " + ver + "\n",
	})
	digest := digestOfBytes(blob)
	slot := sourcecache.SlotPath(root, ".aggrega/cache", "helm-oci-chart", url, "", ver, digest)
	mustMkdirAll(t, slot)
	mustWriteFile(t, filepath.Join(slot, "blob.tgz"), blob)
	mustWriteChartManifest(t, slot, blob)
	mustWriteMetadata(t, filepath.Join(slot, "metadata.yaml"), metadata.CacheMetadata{
		Kind: "helm-oci-chart", URL: url, Version: ver, Digest: digest,
		Mediatype: chartMediatype, PulledAt: time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
	})
	return digest
}

func chartArtifact(url, ver, digest, entity, uri string) lockfile.SourceArtifact {
	return lockfile.SourceArtifact{
		Kind: "helm-oci-chart", URL: url, PinnedVersion: ver, Digest: digest, Mediatype: chartMediatype,
		ReferencedBy: []lockfile.SourceArtifactReference{{Entity: entity, URI: uri, Field: "spec.source"}},
	}
}

// TestResolveSourceForNode_MaterializedValuesDriveSource pins the component-source
// values pillar. A component
// Recipe source whose version reads `.values.source_version` resolves against the
// MATERIALIZED IR values pillar (graph.Spec.ValuesMapping — where a schema
// default lands), so the value-driven identity is pinned and cache-extracted at
// build. Probe: drop the materialized value → the template cannot resolve (the
// green is not vacuous).
func TestResolveSourceForNode_MaterializedValuesDriveSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const url, ver = "oci://hub.aggrega.ai/charts/db", "25.0.8"
	digest := seedChartCache(t, root, url, ver)

	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "db-recipe", Version: "1.0.0"},
		Spec: types.RecipeSpec{Source: map[string]any{
			"kind": "helm-oci-chart", "url": url, "version": "{{ .values.source_version }}",
		}},
	}
	graph := &types.IRGraph{}
	// source_version is present only in the MATERIALIZED pillar (e.g. a schema
	// default the Aggregate omitted) — not in any author-only bag.
	graph.Spec.ValuesMapping.Set("api", json.RawMessage(`{"source_version":"25.0.8"}`))

	input := RendererInput{
		Graph:         graph,
		Lockfile:      &lockfile.Lockfile{Version: 1, Release: "./r.yaml", GeneratedAt: "x", SourceArtifacts: []lockfile.SourceArtifact{chartArtifact(url, ver, digest, "recipe", "db-recipe:1.0.0")}},
		WorkspaceRoot: root, CacheDir: ".aggrega/cache",
	}
	nodeCtx := NodeRenderContext{NodeID: "api"}
	if err := resolveSourceForNode(input, recipe, &nodeCtx); err != nil {
		t.Fatalf("resolveSourceForNode: %v", err)
	}
	if p, _ := nodeCtx.Source.(string); p == "" {
		t.Fatalf("Source should be an extracted path; got %v", nodeCtx.Source)
	} else if _, err := os.Stat(filepath.Join(p, "Chart.yaml")); err != nil {
		t.Errorf("Chart.yaml missing — materialized .values did not drive the source: %v", err)
	}

	// Probe: no materialized value → the version template cannot resolve.
	probe := input
	probe.Graph = &types.IRGraph{}
	if err := resolveSourceForNode(probe, recipe, &NodeRenderContext{NodeID: "api"}); err == nil {
		t.Error("without the materialized .values the source must not resolve (non-vacuous probe)")
	}
}

// TestResolveSourceForNode_MaterializedInfraDriveSource pins the component-source
// infra pillar. A component
// source whose version reads `.infra.chart_version` resolves against the
// MATERIALIZED infra pillar (graph.Spec.InfraMapping = mergeSchemaDefaults(...) ⊇
// the resolver's BoundaryInfra), so a schema-defaulted infra key drives the
// identity. Non-defaulted infra is unchanged (InfraMapping equals the author
// infra when no default applies — the full golden corpus is byte-identical).
// Probe: drop the infra → cannot resolve.
func TestResolveSourceForNode_MaterializedInfraDriveSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const url, ver = "oci://hub.aggrega.ai/charts/db", "25.0.8"
	digest := seedChartCache(t, root, url, ver)

	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "db-recipe", Version: "1.0.0"},
		Spec: types.RecipeSpec{Source: map[string]any{
			"kind": "helm-oci-chart", "url": url, "version": "{{ .infra.chart_version }}",
		}},
	}
	graph := &types.IRGraph{}
	graph.Spec.InfraMapping.Set("api", json.RawMessage(`{"chart_version":"25.0.8"}`))

	input := RendererInput{
		Graph:         graph,
		Lockfile:      &lockfile.Lockfile{Version: 1, Release: "./r.yaml", GeneratedAt: "x", SourceArtifacts: []lockfile.SourceArtifact{chartArtifact(url, ver, digest, "recipe", "db-recipe:1.0.0")}},
		WorkspaceRoot: root, CacheDir: ".aggrega/cache",
	}
	nodeCtx := NodeRenderContext{NodeID: "api"}
	if err := resolveSourceForNode(input, recipe, &nodeCtx); err != nil {
		t.Fatalf("resolveSourceForNode: %v", err)
	}
	if p, _ := nodeCtx.Source.(string); p == "" {
		t.Fatalf("Source should be an extracted path; got %v", nodeCtx.Source)
	} else if _, err := os.Stat(filepath.Join(p, "Chart.yaml")); err != nil {
		t.Errorf("Chart.yaml missing — materialized .infra did not drive the source: %v", err)
	}

	probe := input
	probe.Graph = &types.IRGraph{}
	if err := resolveSourceForNode(probe, recipe, &NodeRenderContext{NodeID: "api"}); err == nil {
		t.Error("without the materialized .infra the source must not resolve (non-vacuous probe)")
	}
}

// TestResolveEntitySource_PerInstanceValuesResolveDistinctSources pins the
// per-component FeatureRecipe / secret source values channel. The
// shared helper the FR/secret render sites call resolves `.values`-driven source
// versions per the values passed; two instances of the same FeatureRecipe type
// with different values resolve their OWN instance's source identity (the FR
// sites pass each attachment's per-instance values — TemplateAttachment.Values,
// populated from the FVM entry the lock side also reads, so build==lock).
func TestResolveEntitySource_PerInstanceValuesResolveDistinctSources(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const url = "oci://hub.aggrega.ai/charts/db"
	dA := seedChartCache(t, root, url, "1.0.0")
	dB := seedChartCache(t, root, url, "2.0.0")

	input := RendererInput{
		Lockfile: &lockfile.Lockfile{Version: 1, Release: "./r.yaml", GeneratedAt: "x", SourceArtifacts: []lockfile.SourceArtifact{
			chartArtifact(url, "1.0.0", dA, "feature_recipe", "db-fr:1.0.0"),
			chartArtifact(url, "2.0.0", dB, "feature_recipe", "db-fr:1.0.0"),
		}},
		WorkspaceRoot: root, CacheDir: ".aggrega/cache",
	}
	source := map[string]any{"kind": "helm-oci-chart", "url": url, "version": "{{ .values.v }}"}

	resolvedVersion := func(values map[string]any) string {
		got, err := resolveEntitySource(input, source, types.OrderedMap[string]{}, "db-fr:1.0.0", values, map[string]any{}, nil)
		if err != nil {
			t.Fatalf("resolveEntitySource(%v): %v", values, err)
		}
		p, _ := got.(string)
		b, err := os.ReadFile(filepath.Join(p, "Chart.yaml"))
		if err != nil {
			t.Fatalf("read extracted Chart.yaml: %v", err)
		}
		return string(b)
	}

	if a := resolvedVersion(map[string]any{"v": "1.0.0"}); !strings.Contains(a, "version: 1.0.0") {
		t.Errorf("instance A resolved %q; want the 1.0.0 chart", a)
	}
	if b := resolvedVersion(map[string]any{"v": "2.0.0"}); !strings.Contains(b, "version: 2.0.0") {
		t.Errorf("instance B resolved %q; want the 2.0.0 chart (per-instance values must pick the right source)", b)
	}
}

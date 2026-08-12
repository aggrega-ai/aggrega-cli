// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// sourceCaptureEngine records the NodeRenderContext.Source the dispatch site
// assigned, so the test can assert the boundary-scope FR's remote source was
// resolved to an extracted cache path (not verbatim-forwarded).
type sourceCaptureEngine struct{ captured *any }

func (e sourceCaptureEngine) Render(_ context.Context, _ map[string]any, nodeCtx NodeRenderContext) ([]RenderedFile, error) {
	*e.captured = nodeCtx.Source
	return nil, nil
}

func (e sourceCaptureEngine) Contract() ProviderContract {
	return ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

// TestDispatchBoundaryScopeFR_RemoteSourceCacheExtractsFromProjection is the
// headline source-identity test. It drives the real boundary-scope dispatch
// (dispatchBoundaryScopeFR) for a FeatureRecipe whose source is REMOTE, with a
// lockfile + cached archive pinned for that {kind,url,version}. The dispatch
// reads the FR's infra from graph.Spec.BoundaryFeatures (the projection) and
// resolves the source through the shared resolve+extract helper.
//
// A successful cache HIT (the engine receives the extracted directory path,
// which holds the archived file) proves the build-resolved source identity
// EQUALS the lockfile-pinned identity — sourcecache.LookupAndExtract only
// succeeds when the resolved {kind,url,version} matches a pinned artifact with a
// populated cache slot. The lock side (irEntityInfra) pins that artifact from
// the SAME projection infra (proven in the pull-side unit test), so this closes
// lock==build agreement: both derive the boundary-scope FR's source identity
// from one IR projection.
func TestDispatchBoundaryScopeFR_RemoteSourceCacheExtractsFromProjection(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blob := buildTGZForResolveTest(t, map[string]string{
		"Chart.yaml": "apiVersion: v2\nname: bridge\nversion: 2.0.0\n",
	})
	digest := digestOfBytes(blob)
	const (
		kind = "helm-oci-chart"
		url  = "oci://hub.aggrega.ai/charts/bridge"
		ver  = "2.0.0"
	)
	slot := sourcecache.SlotPath(root, ".aggrega/cache", kind, url, "", ver, digest)
	mustMkdirAll(t, slot)
	mustWriteFile(t, filepath.Join(slot, "blob.tgz"), blob)
	mustWriteChartManifest(t, slot, blob)
	mustWriteMetadata(t, filepath.Join(slot, "metadata.yaml"), metadata.CacheMetadata{
		Kind:      kind,
		URL:       url,
		Version:   ver,
		Digest:    digest,
		Mediatype: "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
		PulledAt:  time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
	})

	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "compose-bridge-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:     "stub",
			Implements:     "core/compose-bridge:1.0.0",
			ExecutionScope: "boundary",
			Source:         map[string]any{"kind": kind, "url": url, "version": ver},
			ValuesMapping:  types.ValuesMapping{Inline: "{}"},
		},
	}

	// Boundary-feature projection — the dispatch reads feat.Infra from here.
	clusterBF := types.OrderedMap[types.IRBoundaryFeature]{}
	clusterBF.Set("bridge", types.IRBoundaryFeature{Type: "core/compose-bridge:1.0.0", Infra: types.OrderedMap[any]{}})
	graph := &types.IRGraph{Metadata: types.IRMetadata{AggregateName: "bridge-app"}}
	graph.Spec.BoundaryFeatures.Set("cluster", clusterBF)
	graph.Spec.Nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster"})

	release := &types.Release{}
	release.Spec.Boundaries.Set("cluster", types.Boundary{Description: "cluster"})

	input := RendererInput{
		Lockfile: &lockfile.Lockfile{
			Version:     1,
			Release:     "./r.yaml",
			GeneratedAt: "2026-05-22T10:00:00Z",
			SourceArtifacts: []lockfile.SourceArtifact{{
				Kind:          kind,
				URL:           url,
				PinnedVersion: ver,
				Digest:        digest,
				Mediatype:     "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
				ReferencedBy: []lockfile.SourceArtifactReference{
					{Entity: "feature_recipe", URI: "compose-bridge-fr:1.0.0", Field: "spec.source"},
				},
			}},
		},
		WorkspaceRoot: root,
		CacheDir:      ".aggrega/cache",
	}

	var captured any
	factory := func(string) (Provider, error) { return sourceCaptureEngine{captured: &captured}, nil }
	key := boundaryFRKey{BoundaryPath: "cluster", FRInstance: "bridge"}

	if err := dispatchBoundaryScopeFR(
		context.Background(), fr, "bridge", key, &PhaseBState{}, NewBoundaryFRState(),
		NodeRenderContext{}, factory, graph, release, nil, nil, input,
	); err != nil {
		t.Fatalf("dispatchBoundaryScopeFR (remote boundary-scope FR source): %v", err)
	}

	resolvedPath, ok := captured.(string)
	if !ok || resolvedPath == "" {
		t.Fatalf("boundary-scope FR Source should resolve to a non-empty extracted path; got %T %v — a verbatim-forwarded source map means the cache extract was bypassed", captured, captured)
	}
	if _, err := os.Stat(filepath.Join(resolvedPath, "Chart.yaml")); err != nil {
		t.Errorf("Chart.yaml missing in resolved boundary-scope FR Source path — cache HIT (lock==build identity) not proven: %v", err)
	}
}

// TestDispatchBoundaryScopeFR_InfraTemplatedSourceResolvesFromProjectionInfra
// discriminates the BUILD side's infra source (the belt to the lock-side
// discrimination test's suspenders). The FR source version is a template
// (`{{ .infra.chart_version }}`), and chart_version lives ONLY on the
// boundary-feature projection entry. The dispatch must read feat.Infra from
// graph.Spec.BoundaryFeatures and evaluate the source template against it to
// resolve the version-dependent {kind,url,version} identity the lockfile
// pinned. A cache HIT therefore proves the projection infra — not some other
// bag, and not a literal — drove the build-time source identity: with empty or
// wrong infra the version renders empty (missingkey=zero) and the cache lookup
// misses (ErrFetchNotFound).
func TestDispatchBoundaryScopeFR_InfraTemplatedSourceResolvesFromProjectionInfra(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blob := buildTGZForResolveTest(t, map[string]string{
		"Chart.yaml": "apiVersion: v2\nname: bridge\nversion: 3.1.4\n",
	})
	digest := digestOfBytes(blob)
	const (
		kind = "helm-oci-chart"
		url  = "oci://hub.aggrega.ai/charts/bridge"
		ver  = "3.1.4"
	)
	slot := sourcecache.SlotPath(root, ".aggrega/cache", kind, url, "", ver, digest)
	mustMkdirAll(t, slot)
	mustWriteFile(t, filepath.Join(slot, "blob.tgz"), blob)
	mustWriteChartManifest(t, slot, blob)
	mustWriteMetadata(t, filepath.Join(slot, "metadata.yaml"), metadata.CacheMetadata{
		Kind:      kind,
		URL:       url,
		Version:   ver,
		Digest:    digest,
		Mediatype: "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
		PulledAt:  time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
	})

	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "compose-bridge-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:     "stub",
			Implements:     "core/compose-bridge:1.0.0",
			ExecutionScope: "boundary",
			Source:         map[string]any{"kind": kind, "url": url, "version": "{{ .infra.chart_version }}"},
			ValuesMapping:  types.ValuesMapping{Inline: "{}"},
		},
	}

	// The version-driving infra lives ONLY on the projection entry — nowhere
	// else can supply chart_version, so a HIT pins the projection as the source.
	bfInfra := types.OrderedMap[any]{}
	bfInfra.Set("chart_version", ver)
	clusterBF := types.OrderedMap[types.IRBoundaryFeature]{}
	clusterBF.Set("bridge", types.IRBoundaryFeature{Type: "core/compose-bridge:1.0.0", Infra: bfInfra})
	graph := &types.IRGraph{Metadata: types.IRMetadata{AggregateName: "bridge-app"}}
	graph.Spec.BoundaryFeatures.Set("cluster", clusterBF)
	graph.Spec.Nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster"})

	release := &types.Release{}
	release.Spec.Boundaries.Set("cluster", types.Boundary{Description: "cluster"})

	input := RendererInput{
		Lockfile: &lockfile.Lockfile{
			Version:     1,
			Release:     "./r.yaml",
			GeneratedAt: "2026-05-22T10:00:00Z",
			SourceArtifacts: []lockfile.SourceArtifact{{
				Kind:          kind,
				URL:           url,
				PinnedVersion: ver,
				Digest:        digest,
				Mediatype:     "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
				ReferencedBy: []lockfile.SourceArtifactReference{
					{Entity: "feature_recipe", URI: "compose-bridge-fr:1.0.0", Field: "spec.source"},
				},
			}},
		},
		WorkspaceRoot: root,
		CacheDir:      ".aggrega/cache",
	}

	var captured any
	factory := func(string) (Provider, error) { return sourceCaptureEngine{captured: &captured}, nil }
	key := boundaryFRKey{BoundaryPath: "cluster", FRInstance: "bridge"}

	if err := dispatchBoundaryScopeFR(
		context.Background(), fr, "bridge", key, &PhaseBState{}, NewBoundaryFRState(),
		NodeRenderContext{}, factory, graph, release, nil, nil, input,
	); err != nil {
		t.Fatalf("dispatchBoundaryScopeFR (infra-templated boundary-scope FR source): %v", err)
	}
	resolvedPath, ok := captured.(string)
	if !ok || resolvedPath == "" {
		t.Fatalf("infra-templated boundary-scope FR Source should resolve to an extracted path; got %T %v — the projection infra was not read", captured, captured)
	}
	if _, err := os.Stat(filepath.Join(resolvedPath, "Chart.yaml")); err != nil {
		t.Errorf("Chart.yaml missing — the projection's feat.Infra did not drive the build-time source identity: %v", err)
	}
}

// TestDispatchBoundaryScopeFR_LocalSourceForwardsVerbatim is the mandatory
// local-source regression lock for the boundary-scope dispatch: even with a
// lockfile + workspace + cache set, a local FR source short-circuits to verbatim
// (only remote sources route through resolve+extract), matching the prior
// verbatim-forward behavior for local boundary-scope FR sources.
func TestDispatchBoundaryScopeFR_LocalSourceForwardsVerbatim(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "compose-bridge-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:     "stub",
			Implements:     "core/compose-bridge:1.0.0",
			ExecutionScope: "boundary",
			Source:         "./charts/local-bridge",
			ValuesMapping:  types.ValuesMapping{Inline: "{}"},
		},
	}
	clusterBF := types.OrderedMap[types.IRBoundaryFeature]{}
	clusterBF.Set("bridge", types.IRBoundaryFeature{Type: "core/compose-bridge:1.0.0", Infra: types.OrderedMap[any]{}})
	graph := &types.IRGraph{Metadata: types.IRMetadata{AggregateName: "bridge-app"}}
	graph.Spec.BoundaryFeatures.Set("cluster", clusterBF)
	graph.Spec.Nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster"})
	release := &types.Release{}
	release.Spec.Boundaries.Set("cluster", types.Boundary{Description: "cluster"})

	input := RendererInput{
		Lockfile:      &lockfile.Lockfile{Version: 1},
		WorkspaceRoot: root,
		CacheDir:      ".aggrega/cache",
	}

	var captured any
	factory := func(string) (Provider, error) { return sourceCaptureEngine{captured: &captured}, nil }
	key := boundaryFRKey{BoundaryPath: "cluster", FRInstance: "bridge"}

	if err := dispatchBoundaryScopeFR(
		context.Background(), fr, "bridge", key, &PhaseBState{}, NewBoundaryFRState(),
		NodeRenderContext{}, factory, graph, release, nil, nil, input,
	); err != nil {
		t.Fatalf("dispatchBoundaryScopeFR (local source): %v", err)
	}
	if captured != "./charts/local-bridge" {
		t.Errorf("local boundary-scope FR source must forward verbatim (no cache route); got %v", captured)
	}
}

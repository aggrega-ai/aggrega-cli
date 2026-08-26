// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// helmOrComposeFactory renders through the embedded provider guests, serving the
// helm guest for the boundary-scope FR's provider and the compose guest for the
// per-component nodes — one boundary can mix providers, each landing at its own
// (path, provider) dispatch group.
func helmOrComposeFactory(t *testing.T) renderer.EngineFactory {
	return wasmEngineFactory(t)
}

// boundaryScopeHelmBridgeFR builds a boundary-scope FeatureRecipe served by the
// helm engine in sourced mode. The chart emits a single ConfigMap with a
// metadata block; the helm engine merges the aggrega.ai/* provenance labels
// into that metadata at render. The chart provides the resource name, so the
// only place the FR's node identity surfaces is the provenance labels — which
// is exactly the value under test.
func boundaryScopeHelmBridgeFR(t *testing.T) *types.FeatureRecipe {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"),
		[]byte("apiVersion: v2\nname: bridge\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "templates"), 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "templates", "configmap.yaml"),
		[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: bridge-config\ndata:\n  marker: \"x\"\n"), 0o644); err != nil {
		t.Fatalf("write configmap.yaml: %v", err)
	}
	return &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "compose-bridge-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:     "helm",
			Implements:     "core/compose-bridge:1.0.0",
			ExecutionScope: "boundary",
			Source:         dir,
			ValuesMapping:  types.ValuesMapping{Inline: `{}`},
		},
	}
}

// TestBoundaryScopeFR_HelmProvenanceUsesTheComposedComponentID is the end-to-end
// proof that a boundary-scope FeatureRecipe's emitted K8s manifest stamps the
// COMPOSED component identity, never the raw synthetic node-ID. A boundary FR's
// engine-facing node-ID carries marker syntax ("<boundary-fr>:bridge@cluster")
// that is not a legal Kubernetes label value (the ":" and "@" alone disqualify
// it). The dispatch composes LabelComponentID from the two parts an author wrote
// — the instance name and the boundary path — reaching
// "bfr--cluster--bridge", and every emitting surface stamps
// app.kubernetes.io/name and aggrega.ai/component-id from that, so the rendered
// manifest is charset-valid and two attachments an author can tell apart never
// arrive at one identity.
//
// This drives the real RenderAll pipeline: the boundary-scope dispatch builds
// the synthetic node-ID and the sanitised LabelComponentID, the helm engine
// renders the chart and merges the provenance labels into the ConfigMap
// metadata, and we inspect the actual rendered bytes. Revert
// ProvenanceComponentID's LabelComponentID branch (so it falls back to the raw
// NodeID) and aggrega.ai/component-id renders "<boundary-fr>:bridge@cluster" —
// the assertions below go red. The green is therefore non-vacuous: it depends
// on the sanitisation, not merely on a label being present.
func TestBoundaryScopeFR_HelmProvenanceUsesTheComposedComponentID(t *testing.T) {
	t.Parallel()

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster"})

	boundaryFeatures := types.OrderedMap[types.BoundaryFeature]{}
	boundaryFeatures.Set("bridge", types.BoundaryFeature{Type: "core/compose-bridge:1.0.0", Infra: types.OrderedMap[any]{}})
	clusterBoundary := types.Boundary{
		Directory:  "dist/cluster",
		Features:   boundaryFeatures,
		Components: types.OrderedMap[types.BoundaryComponent]{},
		Boundaries: types.OrderedMap[types.Boundary]{},
	}
	clusterBoundary.Components.Set("api", types.BoundaryComponent{})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("cluster", clusterBoundary)

	clusterBFProj := types.OrderedMap[types.IRBoundaryFeature]{}
	clusterBFProj.Set("bridge", types.IRBoundaryFeature{Type: "core/compose-bridge:1.0.0", Infra: types.OrderedMap[any]{}})
	bfProj := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	bfProj.Set("cluster", clusterBFProj)

	bi := map[string]*resolver.BoundaryInfra{
		"api": {BoundaryPath: "cluster", Directory: "dist/cluster"},
	}

	bridgeEntry := types.IRFeatureEntry{Type: "core/compose-bridge:1.0.0"}
	apiFV := types.OrderedMap[types.IRFeatureEntry]{}
	apiFV.Set("bridge", bridgeEntry)
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("api", apiFV)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "compose-bridge-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				BoundaryFeatures:     bfProj,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: ir.FeatureOrder{"api": []string{"bridge"}},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "compose-bridge-app"}},
			Release:       &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: bi,
		},
	}

	apiRecipe := composeWorkloadRecipe("api", "api:1.0", 8080)
	bridgeFR := boundaryScopeHelmBridgeFR(t)
	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType == "core/api" {
				return apiRecipe, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if strings.HasPrefix(featureType, "core/compose-bridge") {
				return bridgeFR, true
			}
			return nil, false
		},
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, helmOrComposeFactory(t))
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	// Locate the helm-rendered ConfigMap among the emitted files (the
	// boundary-FR's chart output). The file key carries the synthetic node-ID
	// marker, so select by content, not path.
	var cm string
	var cmPath string
	for path, body := range result.Files {
		if strings.Contains(string(body.Content), "kind: ConfigMap") {
			cm = string(body.Content)
			cmPath = path
			break
		}
	}
	if cm == "" {
		t.Fatalf("expected a helm-rendered ConfigMap from the boundary-scope FR; got files %v", keysOfFiles(result.Files))
	}

	// Written out rather than computed from the derivation: a value taken from the
	// derivation would agree with it whatever it did.
	const wantID = "bfr--cluster--bridge"
	for _, key := range []string{"aggrega.ai/component-id: " + wantID, "app.kubernetes.io/name: " + wantID} {
		if !strings.Contains(cm, key) {
			t.Errorf("rendered ConfigMap (%s) missing composed provenance %q\n--- body ---\n%s", cmPath, key, cm)
		}
	}
	// The raw synthetic node-ID must never reach a rendered label value — its
	// ":" / "@" / "<>" are charset-invalid for Kubernetes labels.
	for _, bad := range []string{"<boundary-fr>", "bridge@cluster"} {
		if strings.Contains(cm, bad) {
			t.Errorf("rendered ConfigMap (%s) leaked the raw synthetic node-ID fragment %q (charset-invalid label value)\n--- body ---\n%s", cmPath, bad, cm)
		}
	}

	// The emitted file KEY must also use the composed id: a synthetic NodeID's
	// ":" / "@" / "<>" are illegal in filenames on some platforms (Windows
	// reserves ":", "<", ">") and break archive tooling, and this output lands
	// in cross-platform GitOps repos. Reverting the file-key change makes the
	// key revert to "<boundary-fr>:bridge@cluster-configmap.yaml" → these go red.
	for _, bad := range []string{":", "@", "<", ">"} {
		if strings.Contains(cmPath, bad) {
			t.Errorf("emitted file key %q contains the filesystem-hostile char %q from a raw synthetic NodeID", cmPath, bad)
		}
	}
	if !strings.Contains(cmPath, wantID+"-") {
		t.Errorf("emitted file key %q should carry the composed prefix %q-", cmPath, wantID)
	}
}

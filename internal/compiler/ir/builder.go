// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"encoding/json"
	"sort"

	"github.com/cognitio/aggrega-cli/internal/compiler/topology"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// BuildIRConfig configures the IR graph builder.
type BuildIRConfig struct {
	Topology        *topology.TopologyResult
	ResolveResult   *resolver.ResolveResult
	CatalogLookup   resolver.CatalogLookup
	CompilerVersion string
}

// FeatureOrder tracks deterministic feature ordering per node.
// Key: nodeID, Value: ordered feature instance names following Onion Model.
type FeatureOrder map[string][]string

// BuildIR executes Step 3 of the compiler pipeline: graph finalization.
//  1. Build IR nodes from topology nodes
//  2. Convert topology edges to IR edges
//  3. Collect and normalize features (5-Tier Onion Model)
//  4. Populate values/infra/feature mappings
//  5. Build annotation buckets
//  6. Generate provenance
//
// Iteration over the topology's flattened node map is sorted alphabetically
// (topology stores nodes in a plain map for lookup); the output OrderedMap
// carries that same ordering. Author order from the Aggregate flows through
// the IR ValuesMapping and FeatureValuesMapping via the per-component
// OrderedMap values.
func BuildIR(cfg BuildIRConfig) (*types.IRGraph, FeatureOrder, error) {
	topo := cfg.Topology
	res := cfg.ResolveResult

	irNodes := types.OrderedMap[types.IRNode]{}
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	infraMapping := types.OrderedMap[json.RawMessage]{}
	featureValuesMapping := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	secrets := types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{}
	featureOrder := make(FeatureOrder)
	allMarkers := map[string]bool{}

	// 1. Build IR nodes (sorted for determinism — topo.Nodes is a plain map keyed by flattened ID)
	nodeIDs := sortedStringKeys(topo.Nodes)
	for _, nodeID := range nodeIDs {
		topoNode := topo.Nodes[nodeID]

		catalogAnnotations, releaseAnnotations, aggregateAnnotations := buildAnnotationBuckets(
			nodeID, topoNode.ComponentType, cfg.CatalogLookup, res.BoundaryInfra,
		)

		irNode := types.IRNode{
			Labels:               topoNode.Labels,
			Component:            topoNode.ComponentType,
			SubsystemPath:        topoNode.SubsystemPath,
			BoundaryPath:         topoNode.BoundaryPath,
			CatalogAnnotations:   catalogAnnotations,
			ReleaseAnnotations:   releaseAnnotations,
			AggregateAnnotations: aggregateAnnotations,
			CatalogMetadata:      buildCatalogMetadata(topoNode.ComponentType, cfg.CatalogLookup),
			SelfInject:           topoNode.SelfInject,
		}

		// Resolved version from catalog
		if cfg.CatalogLookup.Component != nil {
			catComp, found := cfg.CatalogLookup.Component(topoNode.ComponentType)
			if found {
				irNode.ResolvedVersion = catComp.Metadata.Version
			}
		}

		// Sync wave from Release
		if res.Release != nil {
			irNode.SyncWave = findSyncWave(res.Release.Spec.SyncWaves, nodeID)
		}

		irNodes.Set(nodeID, irNode)

		// 4. Values mapping
		if topoNode.Values.Len() > 0 {
			raw, err := json.Marshal(topoNode.Values)
			if err == nil {
				valuesMapping.Set(nodeID, raw)
			}
		}

		// Infra mapping from boundary infra
		if bi, ok := res.BoundaryInfra[nodeID]; ok && bi != nil && bi.Infra.Len() > 0 {
			raw, err := json.Marshal(bi.Infra)
			if err == nil {
				infraMapping.Set(nodeID, raw)
			}
		}

		// 3. Feature normalization (5-Tier Onion Model)
		featResult, err := collectNodeFeatures(
			nodeID, topoNode, topo, res, cfg.CatalogLookup,
		)
		if err != nil {
			return nil, nil, err
		}

		if featResult.Features.Len() > 0 {
			featureValuesMapping.Set(nodeID, featResult.Features)
			featureOrder[nodeID] = featResult.FeatureOrder
		}
		if featResult.Secrets.Len() > 0 {
			secrets.Set(nodeID, featResult.Secrets)
		}
		for _, m := range featResult.Markers {
			allMarkers[m] = true
		}
	}

	// 2. Convert topology edges to IR edges
	irEdges := make([]types.IREdge, 0, len(topo.Edges))
	for _, e := range topo.Edges {
		irEdge := types.IREdge{
			Source:        e.Source,
			Target:        e.Target,
			Socket:        e.Socket,
			Alias:         e.Alias,
			OriginFeature: e.OriginFeature,
			Inject:        convertInjectBlock(e.Inject),
		}
		irEdges = append(irEdges, irEdge)
	}

	// Feature markers (sorted)
	markers := make([]string, 0, len(allMarkers))
	for m := range allMarkers {
		if m != "" {
			markers = append(markers, m)
		}
	}
	sort.Strings(markers)

	// 6. Provenance
	prov := buildProvenance(res, cfg.CompilerVersion)

	graph := &types.IRGraph{
		APIVersion: types.IRAPIVersion,
		Kind:       types.IRKind,
		Metadata: types.IRMetadata{
			AggregateName: res.Aggregate.Metadata.Name,
		},
		Spec: types.IRSpec{
			Nodes:                irNodes,
			Edges:                irEdges,
			FeatureMarkers:       markers,
			ValuesMapping:        valuesMapping,
			InfraMapping:         infraMapping,
			FeatureValuesMapping: featureValuesMapping,
			Secrets:              secrets,
			Provenance:           prov,
		},
	}

	return graph, featureOrder, nil
}

// convertInjectBlock converts a topology InjectBlock to the IR edge inject format.
func convertInjectBlock(inject *types.InjectBlock) types.OrderedMap[types.OrderedMap[string]] {
	result := types.OrderedMap[types.OrderedMap[string]]{}
	if inject == nil {
		return result
	}
	convert := func(mech string, raw interface{}) {
		if raw == nil {
			return
		}
		switch v := raw.(type) {
		case map[string]interface{}:
			m := types.OrderedMap[string]{}
			for k, val := range v {
				if s, ok := val.(string); ok {
					m.Set(k, s)
				}
			}
			if m.Len() > 0 {
				result.Set(mech, m)
			}
		case map[string]string:
			if len(v) > 0 {
				m := types.OrderedMap[string]{}
				for k, val := range v {
					m.Set(k, val)
				}
				result.Set(mech, m)
			}
		}
	}
	convert("env", inject.Env)
	convert("file", inject.File)
	convert("arg", inject.Arg)
	return result
}

func findSyncWave(syncWaves types.OrderedMap[[]string], nodeID string) string {
	for wave, nodes := range syncWaves.All() {
		for _, n := range nodes {
			if n == nodeID {
				return wave
			}
		}
	}
	return ""
}

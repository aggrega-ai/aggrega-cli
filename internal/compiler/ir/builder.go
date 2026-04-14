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
func BuildIR(cfg BuildIRConfig) (*types.IRGraph, FeatureOrder, error) {
	topo := cfg.Topology
	res := cfg.ResolveResult

	irNodes := make(map[string]types.IRNode)
	valuesMapping := make(map[string]json.RawMessage)
	infraMapping := make(map[string]json.RawMessage)
	featureValuesMapping := make(map[string]map[string]types.IRFeatureEntry)
	secrets := make(map[string]map[string]types.IRSecretEntry)
	featureOrder := make(FeatureOrder)
	allMarkers := map[string]bool{}

	// 1. Build IR nodes (sorted for determinism)
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

		irNodes[nodeID] = irNode

		// 4. Values mapping
		if topoNode.Values != nil {
			raw, err := json.Marshal(topoNode.Values)
			if err == nil {
				valuesMapping[nodeID] = raw
			}
		}

		// Infra mapping from boundary infra
		if bi, ok := res.BoundaryInfra[nodeID]; ok && bi != nil && bi.Infra != nil {
			raw, err := json.Marshal(bi.Infra)
			if err == nil {
				infraMapping[nodeID] = raw
			}
		}

		// 3. Feature normalization (5-Tier Onion Model)
		featResult, err := collectNodeFeatures(
			nodeID, topoNode, topo, res, cfg.CatalogLookup,
		)
		if err != nil {
			return nil, nil, err
		}

		if len(featResult.Features) > 0 {
			featureValuesMapping[nodeID] = featResult.Features
			featureOrder[nodeID] = featResult.FeatureOrder
		}
		if len(featResult.Secrets) > 0 {
			secrets[nodeID] = featResult.Secrets
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
			AppName: res.Aggregate.Metadata.Name,
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

func findSyncWave(syncWaves map[string][]string, nodeID string) string {
	for wave, nodes := range syncWaves {
		for _, n := range nodes {
			if n == nodeID {
				return wave
			}
		}
	}
	return ""
}

package analyzer

import (
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// AnalysisResult is the output of the Step 4 static analyzer.
type AnalysisResult struct {
	Errors      []AnalysisError
	Paths       []PrecalculatedPath
	PolicyInput *PolicyValidatorInput
}

// AnalysisError is one selector violation or analysis error.
type AnalysisError struct {
	Source   string
	Target   string
	Socket   string
	Selector string
	Message  string
}

// PrecalculatedPath is one DFS-enumerated acyclic path through the graph.
type PrecalculatedPath struct {
	PathID           string   `json:"path_id"`
	Nodes            []string `json:"nodes"`
	AttachedFeatures []string `json:"attached_features"`
}

// Analyze executes Step 4 of the compiler pipeline:
//  1. Validate @selector rules on every edge
//  2. Calculate precalculated_paths via cycle-safe DFS
//  3. Assemble PolicyValidatorInput
func Analyze(graph *types.IRGraph, catalogLookup resolver.CatalogLookup) *AnalysisResult {
	// 1. Selector validation
	selectorErrors := validateSelectors(graph, catalogLookup)

	// 2. DFS path calculation
	paths := calculatePaths(graph)

	// 3. Assemble policy input
	policyInput := assemblePolicyInput(graph, paths)

	return &AnalysisResult{
		Errors:      selectorErrors,
		Paths:       paths,
		PolicyInput: policyInput,
	}
}

func assemblePolicyInput(graph *types.IRGraph, paths []PrecalculatedPath) *PolicyValidatorInput {
	policyNodes := make(map[string]PolicyNode, len(graph.Spec.Nodes))
	for id, node := range graph.Spec.Nodes {
		pn := PolicyNode{
			Component:       node.Component,
			ResolvedVersion: node.ResolvedVersion,
			Labels:          node.Labels,
		}
		if fvm, ok := graph.Spec.FeatureValuesMapping[id]; ok {
			pn.Features = fvm
		}
		policyNodes[id] = pn
	}

	policyEdges := make([]PolicyEdge, 0, len(graph.Spec.Edges))
	for _, e := range graph.Spec.Edges {
		policyEdges = append(policyEdges, PolicyEdge{
			Source:        e.Source,
			Target:        e.Target,
			Socket:        e.Socket,
			OriginFeature: e.OriginFeature,
		})
	}

	return &PolicyValidatorInput{
		Graph: PolicyGraph{
			Nodes: policyNodes,
			Edges: policyEdges,
		},
		PrecalculatedPaths: paths,
	}
}

package topology

import (
	"sort"

	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// TopologyResult is the output of the Step 2 topology builder.
type TopologyResult struct {
	Nodes              map[string]*TopologyNode
	Edges              []NormalizedEdge
	FeatureAttachments map[string][]FeatureAttachment
	SecretAttachments  map[string][]SecretAttachment
	ExportedComponents map[string]string // alias → resolved dot-notation ID
}

// TopologyNode is one flattened node in the topology graph.
type TopologyNode struct {
	InstanceID      string
	ComponentType   string
	Labels          map[string]string
	Values          map[string]interface{}
	SubsystemPath   string
	BoundaryPath    string
	SourceAggregate string // prefix of the sub-aggregate that contributed this node ("" = root)
}

// NormalizedEdge is one directed edge in the topology graph.
type NormalizedEdge struct {
	Source        string
	Target        string
	Socket        string
	Alias         string
	Inject        *types.InjectBlock
	OriginFeature string // non-empty if edge came from a Feature's uses
}

// FeatureAttachment is a feature attached to a node, pre-normalization.
type FeatureAttachment struct {
	InstanceName string
	Feature      types.FeatureInstance
	SourceTier   int // Onion Model tier (set by IR builder in Step 3)
}

// SecretAttachment is a secret attached to a node, pre-normalization.
type SecretAttachment struct {
	InstanceName string
	Secret       types.SecretInstance
	SourceTier   int
}

// SubAggregateLoader loads a sub-aggregate by type URI.
type SubAggregateLoader func(typeName string) (*types.Aggregate, error)

// BuildConfig configures the topology builder.
type BuildConfig struct {
	ResolveResult    *resolver.ResolveResult
	CatalogLookup    resolver.CatalogLookup
	LoadSubAggregate SubAggregateLoader
}

// Build executes Step 2 of the compiler pipeline: topology construction.
//  1. Flatten the Aggregate tree (sub-aggregates → dot-notation IDs)
//  2. Populate node labels from catalog
//  3. Populate boundary paths from resolver
//  4. Normalize uses → directed edges
//  5. Collect feature uses edges
func Build(cfg BuildConfig) (*TopologyResult, error) {
	agg := cfg.ResolveResult.Aggregate

	// 1. Flatten
	flat, err := flattenAggregate(agg, "", "", cfg.LoadSubAggregate, map[string]bool{}, 0)
	if err != nil {
		return nil, err
	}

	// 2. Populate labels from catalog
	if cfg.CatalogLookup.Component != nil {
		for _, node := range flat.Nodes {
			catComp, found := cfg.CatalogLookup.Component(node.ComponentType)
			if found {
				node.Labels = catComp.Metadata.Labels
			}
		}
	}

	// 3. Populate boundary paths from resolver
	for nodeID, node := range flat.Nodes {
		if bi, ok := cfg.ResolveResult.BoundaryInfra[nodeID]; ok && bi != nil {
			node.BoundaryPath = bi.BoundaryPath
		}
	}

	// 4. Normalize uses → edges (sorted node iteration)
	var edges []NormalizedEdge
	nodeIDs := sortedNodeIDs(flat.Nodes)
	for _, nodeID := range nodeIDs {
		// Component uses
		comp := findComponent(agg, nodeID)
		if comp != nil && len(comp.Uses) > 0 {
			compEdges, err := normalizeUses(comp.Uses, nodeID, "")
			if err != nil {
				return nil, err
			}
			edges = append(edges, compEdges...)
		}

		// 5. Feature uses (create edges with OriginFeature)
		if feats, ok := flat.FeatureAttachments[nodeID]; ok {
			for _, fa := range feats {
				if len(fa.Feature.Uses) > 0 {
					featEdges, err := normalizeUses(fa.Feature.Uses, nodeID, fa.Feature.Type)
					if err != nil {
						return nil, err
					}
					edges = append(edges, featEdges...)
				}
			}
		}

		// Secret uses
		if secs, ok := flat.SecretAttachments[nodeID]; ok {
			for _, sa := range secs {
				if len(sa.Secret.Uses) > 0 {
					secEdges, err := normalizeUses(sa.Secret.Uses, nodeID, sa.Secret.Type)
					if err != nil {
						return nil, err
					}
					edges = append(edges, secEdges...)
				}
			}
		}
	}

	return &TopologyResult{
		Nodes:              flat.Nodes,
		Edges:              edges,
		FeatureAttachments: flat.FeatureAttachments,
		SecretAttachments:  flat.SecretAttachments,
		ExportedComponents: flat.ExportedComponents,
	}, nil
}

// findComponent looks up a ComponentInstance by name in the root Aggregate.
// Does not search sub-aggregates (they are already flattened).
func findComponent(agg *types.Aggregate, name string) *types.ComponentInstance {
	if comp, ok := agg.Spec.Components[name]; ok {
		return &comp
	}
	// Search subsystems
	for _, sub := range agg.Spec.Subsystems {
		if c := findComponentInSubsystem(sub, name); c != nil {
			return c
		}
	}
	return nil
}

func findComponentInSubsystem(sub types.Subsystem, name string) *types.ComponentInstance {
	if comp, ok := sub.Components[name]; ok {
		return &comp
	}
	for _, nested := range sub.Subsystems {
		if c := findComponentInSubsystem(nested, name); c != nil {
			return c
		}
	}
	return nil
}

func sortedNodeIDs(nodes map[string]*TopologyNode) []string {
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

package topology

import (
	"fmt"
	"sort"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

const maxSubAggregateDepth = 10

// flattenResult is the intermediate output of aggregate flattening.
type flattenResult struct {
	Nodes              map[string]*TopologyNode
	FeatureAttachments map[string][]FeatureAttachment
	SecretAttachments  map[string][]SecretAttachment
	ExportedComponents map[string]string
}

// flattenAggregate recursively walks an Aggregate tree, flattening all components
// (including sub-aggregate components) into a flat node map with dot-notation IDs.
func flattenAggregate(
	agg *types.Aggregate,
	prefix string,
	subsystemPath string,
	loader SubAggregateLoader,
	visited map[string]bool,
	depth int,
) (*flattenResult, error) {
	if depth > maxSubAggregateDepth {
		return nil, fmt.Errorf("%w: depth %d at prefix %q", ErrMaxDepth, depth, prefix)
	}

	result := &flattenResult{
		Nodes:              make(map[string]*TopologyNode),
		FeatureAttachments: make(map[string][]FeatureAttachment),
		SecretAttachments:  make(map[string][]SecretAttachment),
		ExportedComponents: make(map[string]string),
	}

	// Walk top-level components (sorted for determinism)
	compNames := sortedMapKeys(agg.Spec.Components)
	for _, name := range compNames {
		comp := agg.Spec.Components[name]
		nodeID := qualifiedID(prefix, name)
		result.Nodes[nodeID] = &TopologyNode{
			InstanceID:      nodeID,
			ComponentType:   comp.Type,
			Values:          comp.Values,
			SubsystemPath:   subsystemPath,
			SourceAggregate: prefix,
		}
		collectAttachments(result, nodeID, comp, 0)
	}

	// Walk subsystems (sorted, recursive)
	subNames := sortedMapKeysSubsystems(agg.Spec.Subsystems)
	for _, subName := range subNames {
		sub := agg.Spec.Subsystems[subName]
		subPath := qualifiedID(subsystemPath, subName)
		flattenSubsystem(result, prefix, subPath, sub)
	}

	// Walk sub-aggregates (sorted)
	if loader != nil {
		aggNames := sortedMapKeysSubAggregates(agg.Spec.Aggregates)
		for _, aggName := range aggNames {
			subAgg := agg.Spec.Aggregates[aggName]
			childPrefix := qualifiedID(prefix, aggName)

			if visited[subAgg.Type] {
				return nil, fmt.Errorf("%w: circular sub-aggregate reference %q at %q", ErrSubAggregateLoad, subAgg.Type, childPrefix)
			}

			childAgg, err := loader(subAgg.Type)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrSubAggregateLoad, err)
			}

			childVisited := copyVisited(visited)
			childVisited[subAgg.Type] = true

			childResult, err := flattenAggregate(childAgg, childPrefix, "", loader, childVisited, depth+1)
			if err != nil {
				return nil, err
			}

			// Merge child results
			for id, node := range childResult.Nodes {
				result.Nodes[id] = node
			}
			for id, feats := range childResult.FeatureAttachments {
				result.FeatureAttachments[id] = append(result.FeatureAttachments[id], feats...)
			}
			for id, secs := range childResult.SecretAttachments {
				result.SecretAttachments[id] = append(result.SecretAttachments[id], secs...)
			}

			// Rewire exported_components: alias scoped to sub-aggregate prefix
			if childAgg != nil {
				exportNames := sortedMapKeysStringMap(childAgg.Spec.ExportedComponents)
				for _, exportAlias := range exportNames {
					localRef := childAgg.Spec.ExportedComponents[exportAlias]
					resolvedID := qualifiedID(childPrefix, localRef)
					result.ExportedComponents[qualifiedID(childPrefix, exportAlias)] = resolvedID
				}
			}
		}
	}

	return result, nil
}

func flattenSubsystem(result *flattenResult, prefix, subsystemPath string, sub types.Subsystem) {
	compNames := sortedMapKeys(sub.Components)
	for _, name := range compNames {
		comp := sub.Components[name]
		nodeID := qualifiedID(prefix, name)
		result.Nodes[nodeID] = &TopologyNode{
			InstanceID:      nodeID,
			ComponentType:   comp.Type,
			Values:          comp.Values,
			SubsystemPath:   subsystemPath,
			SourceAggregate: prefix,
		}
		collectAttachments(result, nodeID, comp, 0)
	}

	// Recurse into nested subsystems
	subNames := sortedMapKeysSubsystems(sub.Subsystems)
	for _, subName := range subNames {
		nested := sub.Subsystems[subName]
		nestedPath := subsystemPath + "." + subName
		flattenSubsystem(result, prefix, nestedPath, nested)
	}
}

func collectAttachments(result *flattenResult, nodeID string, comp types.ComponentInstance, tier int) {
	// Features (sorted)
	featNames := sortedMapKeysFeatures(comp.Features)
	for _, fname := range featNames {
		feat := comp.Features[fname]
		result.FeatureAttachments[nodeID] = append(result.FeatureAttachments[nodeID], FeatureAttachment{
			InstanceName: fname,
			Feature:      feat,
			SourceTier:   tier,
		})
	}

	// Secrets (sorted)
	secNames := sortedMapKeysSecrets(comp.Secrets)
	for _, sname := range secNames {
		sec := comp.Secrets[sname]
		result.SecretAttachments[nodeID] = append(result.SecretAttachments[nodeID], SecretAttachment{
			InstanceName: sname,
			Secret:       sec,
			SourceTier:   tier,
		})
	}
}

func qualifiedID(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func copyVisited(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Sorted key helpers for deterministic map iteration.

func sortedMapKeys(m map[string]types.ComponentInstance) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeysSubsystems(m map[string]types.Subsystem) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeysSubAggregates(m map[string]types.SubAggregate) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeysFeatures(m map[string]types.FeatureInstance) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeysSecrets(m map[string]types.SecretInstance) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeysStringMap(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

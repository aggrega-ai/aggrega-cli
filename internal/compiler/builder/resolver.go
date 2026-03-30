package builder

import (
	"fmt"
	"strings"
)

// ResolveManifestAgainstAggregate checks every component, attached feature, and secret store
// reference against spec.mappings. It fails on the first unresolved or wrongly-typed entry.
func ResolveManifestAgainstAggregate(agg *Aggregate, man *Manifest) error {
	if agg == nil || man == nil {
		return fmt.Errorf("resolver: aggregate and manifest are required")
	}
	mappings := man.Spec.Mappings

	for id, comp := range agg.Spec.Components {
		if err := resolveComponentMapping(mappings, comp.Type); err != nil {
			return fmt.Errorf("component %q (type %q): %w", id, comp.Type, err)
		}
		for _, feat := range comp.Features {
			if err := resolveFeatureMapping(mappings, feat.Type); err != nil {
				return fmt.Errorf("component %q feature %q: %w", id, feat.Type, err)
			}
		}
		for name, sec := range comp.Secrets {
			if err := resolveSecretMapping(mappings, sec.Store); err != nil {
				return fmt.Errorf("component %q secret %q (store %q): %w", id, name, sec.Store, err)
			}
		}
	}
	return nil
}

func resolveComponentMapping(mappings map[string]ManifestMapping, abstractType string) error {
	m, ok := mappings[abstractType]
	if !ok {
		return fmt.Errorf("no manifest mapping for abstract type %q", abstractType)
	}
	if strings.TrimSpace(m.ComponentRecipe) == "" {
		return fmt.Errorf("mapping for %q must include a non-empty componentRecipe", abstractType)
	}
	return nil
}

func resolveFeatureMapping(mappings map[string]ManifestMapping, featureType string) error {
	m, ok := mappings[featureType]
	if !ok {
		return fmt.Errorf("no manifest mapping for feature type %q", featureType)
	}
	if strings.TrimSpace(m.FeatureRecipe) == "" {
		return fmt.Errorf("mapping for %q must include a non-empty featureRecipe", featureType)
	}
	return nil
}

func resolveSecretMapping(mappings map[string]ManifestMapping, storeURI string) error {
	m, ok := mappings[storeURI]
	if !ok {
		return fmt.Errorf("no manifest mapping for secret store %q", storeURI)
	}
	if strings.TrimSpace(m.SecretRecipe) == "" {
		return fmt.Errorf("mapping for %q must include a non-empty secretRecipe", storeURI)
	}
	return nil
}

// InferResolvedType maps an abstract component.type string to a taxonomic class used in the IR
// (component.aggrega.ai/type style). This is a deterministic heuristic until catalog labels are
// threaded through the builder; it covers AI-native and cloud-native shapes called out in the
// platform taxonomy without embedding provider-specific rendering.
func InferResolvedType(abstractType string) string {
	t := strings.ToLower(abstractType)

	switch {
	case strings.Contains(t, "gateway"):
		return "ai-gateway"
	case strings.Contains(t, "datastore") || strings.Contains(t, "vector") ||
		strings.Contains(t, "knowledge-base") || strings.Contains(t, "event-logs") ||
		strings.Contains(t, "qdrant") || strings.Contains(t, "redis"):
		return "ai-datastore"
	case strings.Contains(t, "llm") || strings.Contains(t, "model") || strings.Contains(t, "hosted"):
		return "ai-model"
	case strings.Contains(t, "agent"):
		return "ai-agent"
	case strings.Contains(t, "server"):
		return "server"
	default:
		return "component"
	}
}

package builder

import (
	"fmt"
	"strings"
)

// ResolveManifestAgainstAggregate checks every component, attached feature, and secret store
// reference against spec.mappings, then deep-merges each component mapping's Values (Ops
// fallback) into the component's Values with developer intent winning on key collision.
func ResolveManifestAgainstAggregate(agg *Aggregate, man *Manifest) error {
	if agg == nil || man == nil {
		return fmt.Errorf("resolver: aggregate and manifest are required")
	}
	mappings := man.Spec.Mappings

	for id, comp := range agg.Spec.Components {
		m, err := componentMapping(mappings, comp.Type)
		if err != nil {
			return fmt.Errorf("component %q (type %q): %w", id, comp.Type, err)
		}
		comp.Values = mergeDeveloperOverManifest(m.Values, comp.Values)
		agg.Spec.Components[id] = comp

		for _, feat := range comp.Features {
			fk := FeatureKey(feat)
			if err := resolveFeatureMapping(mappings, fk); err != nil {
				return fmt.Errorf("component %q feature %q: %w", id, fk, err)
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

func componentMapping(mappings map[string]ManifestMapping, abstractType string) (ManifestMapping, error) {
	m, ok := mappings[abstractType]
	if !ok {
		return ManifestMapping{}, fmt.Errorf("no manifest mapping for abstract type %q", abstractType)
	}
	if strings.TrimSpace(m.ComponentRecipe) == "" {
		return ManifestMapping{}, fmt.Errorf("mapping for %q must include a non-empty componentRecipe", abstractType)
	}
	return m, nil
}

// mergeDeveloperOverManifest starts from Ops (manifest) values, then overlays developer Values.
// Top-level keys on the aggregate win; nested maps are not recursively merged (except environment).
func mergeDeveloperOverManifest(manifestVals, dev map[string]interface{}) map[string]interface{} {
	base := shallowCopyStringInterfaceMap(manifestVals)
	if base == nil {
		base = make(map[string]interface{})
	}
	if len(dev) == 0 {
		if len(base) == 0 {
			return nil
		}
		return base
	}
	// Deep-merge standard environment maps when both sides expose map[string]interface{}.
	if mergedEnv := mergeEnvironmentMaps(base["environment"], dev["environment"]); mergedEnv != nil {
		base["environment"] = mergedEnv
	}
	for k, v := range dev {
		if k == "environment" {
			continue // already merged above
		}
		base[k] = v
	}
	if len(base) == 0 {
		return nil
	}
	return base
}

func shallowCopyStringInterfaceMap(in map[string]interface{}) map[string]interface{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func mergeEnvironmentMaps(ops, dev interface{}) map[string]interface{} {
	opsMap, ok1 := ops.(map[string]interface{})
	devMap, ok2 := dev.(map[string]interface{})
	if !ok1 && !ok2 {
		return nil
	}
	out := make(map[string]interface{})
	if ok1 {
		for k, v := range opsMap {
			out[k] = v
		}
	}
	if ok2 {
		for k, v := range devMap {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

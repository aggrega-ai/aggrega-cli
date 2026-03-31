package linter

import (
	"fmt"
	"strings"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// ValidateRequiredFeatures enforces catalog-declared mandatory feature attachments on each
// aggregate component instance. If catalog is nil or empty, validation is skipped (no catalog —
// no required-features contract to enforce).
func ValidateRequiredFeatures(aggregate *types.Aggregate, catalog *types.Catalog) error {
	if aggregate == nil {
		return fmt.Errorf("linter.requiredFeatures: aggregate is required")
	}
	if catalog == nil || len(catalog.ComponentDefinitions) == 0 {
		return nil
	}

	for id, comp := range aggregate.Spec.Components {
		spec, ok := catalog.SpecForComponent(comp.Type)
		if !ok || spec == nil || len(spec.RequiredFeatures) == 0 {
			continue
		}
		for _, req := range spec.RequiredFeatures {
			if !instanceHasAttachedFeature(comp.Features, req) {
				return fmt.Errorf(
					"Capability Violation: Component instance '%s' requires the '%s' feature, but it is not attached in the Aggregate.",
					id,
					req,
				)
			}
		}
	}
	return nil
}

func instanceHasAttachedFeature(attachments []types.AggregateFeatureAttachment, required string) bool {
	wantNorm := normalizeFeatureID(required)
	for _, a := range attachments {
		if normalizeFeatureID(a.Type) == wantNorm {
			return true
		}
		if normalizeFeatureID(a.Name) == wantNorm {
			return true
		}
	}
	return false
}

func normalizeFeatureID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

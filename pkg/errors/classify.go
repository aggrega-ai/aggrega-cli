// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors

import "strings"

// ClassifyEntityCategory returns the Category for an entity reference whose
// prefix follows the "<Kind> <metadata.name>" convention used by structured-
// error types across the compiler. Today both resolver.ResolverError and
// validator.ValidationError emit Entity strings shaped this way; the helper
// centralises the prefix-to-Category mapping so future structured-error
// types adopting the same convention reuse it.
//
// Mapping:
//
//   - "Aggregate" → DeveloperError. The Aggregate is the developer's
//     authoring surface; field violations on it are developer-domain.
//
//   - "Release" → OpsError. The Release is the operator's authoring
//     surface; field violations on it are ops-domain.
//
//   - "Recipe", "Component", "Feature", "FeatureRecipe", "Provider",
//     "Manifest" → CatalogError. All six entity kinds are catalog
//     content; violations are catalog-author-domain.
//
//   - Unknown prefix → DeveloperError. Conservative: flag at the
//     developer's authoring surface rather than misroute to ops.
//     If a new structured-error type emits a different Entity prefix,
//     extend this switch rather than relying on the default branch.
func ClassifyEntityCategory(entity string) Category {
	parts := strings.Fields(entity)
	if len(parts) == 0 {
		return DeveloperError
	}
	switch parts[0] {
	case "Aggregate":
		return DeveloperError
	case "Release":
		return OpsError
	case "Recipe", "Component", "Feature", "FeatureRecipe", "Provider", "Manifest":
		return CatalogError
	}
	return DeveloperError
}

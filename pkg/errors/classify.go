// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package errors

import "strings"

// ClassifyEntityKind returns the Category that faults on an entity of the
// named kind belong to, and reports whether the kind is one the mapping
// covers.
//
// It is the single home of the kind-to-Category mapping: a caller that holds
// a kind asks here rather than keeping a copy of the correspondence. The kind
// is matched exactly, so nothing is parsed out of a rendered message. A kind
// outside the mapping is reported as uncovered rather than folded into a
// default, so a caller cannot acquire a category by omission — it has to
// decide what an unmappable kind means.
//
// Error types whose every instance belongs to one domain by construction —
// a Recipe validation failure, say — carry that Category directly and have no
// kind to map.
//
// Mapping:
//
//   - "Aggregate" → DeveloperError. The Aggregate is the developer's
//     authoring surface; field violations on it are developer-domain.
//
//   - "Release" → OpsError. The Release is the operator's authoring
//     surface; field violations on it are ops-domain.
//
//   - "Recipe", "Component", "Feature", "FeatureRecipe",
//     "Manifest" → CatalogError. All five entity kinds are catalog
//     content; violations are catalog-author-domain.
//
// Every other kind, the empty kind included, reports false. Extend the
// mapping when a new entity kind starts carrying categorised faults.
func ClassifyEntityKind(kind string) (Category, bool) {
	switch kind {
	case "Aggregate":
		return DeveloperError, true
	case "Release":
		return OpsError, true
	case "Recipe", "Component", "Feature", "FeatureRecipe", "Manifest":
		return CatalogError, true
	}
	return "", false
}

// ClassifyEntityCategory returns the Category for an entity reference
// rendered in the "<Kind> <metadata.name>" shape — resolver.ResolverError
// builds its Entity strings that way — by reading the leading kind word and
// classifying it through ClassifyEntityKind.
//
// It exists for callers whose only handle on the entity is that rendered
// reference. A caller holding the kind as a value calls ClassifyEntityKind
// instead: the kind is then a field it cannot forget to set, rather than a
// word it has to remember to prepend.
//
// A reference whose leading word is not a mapped kind yields DeveloperError.
// That is conservative rather than correct, and deliberately so: from a
// rendered string alone there is no way to tell an unrecognised kind from a
// reference that carries no kind at all, so the fault is reported at the
// developer's authoring surface rather than misrouted to ops.
func ClassifyEntityCategory(entity string) Category {
	parts := strings.Fields(entity)
	if len(parts) == 0 {
		return DeveloperError
	}
	if category, ok := ClassifyEntityKind(parts[0]); ok {
		return category
	}
	return DeveloperError
}

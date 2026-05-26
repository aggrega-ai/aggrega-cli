// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package completion

import "github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"

// ContextKind names the completion-only cursor positions that
// yamlpos.Classify does NOT recognize. Navigation cursors (Extends,
// ComponentsType, FeaturesType, RecipeImplements, ManifestRecipeReference,
// ReleaseAggregatePath, ReleaseManifestPath, UsesTarget, MetadataName,
// SchemaProperty) are handled separately via their yamlpos
// ContextKind in the dispatcher; this enum covers the residual
// completion-specific contexts.
type ContextKind int

const (
	// ContextKindUnknown — cursor does not match any recognized
	// completion context. Dispatcher returns an empty candidate list.
	ContextKindUnknown ContextKind = iota

	// ContextKindKind — cursor on the "kind:" value at the top of an
	// entity document. Candidates: the entity-kinds closed set.
	ContextKindKind

	// ContextKindAPIVersion — cursor on the "apiVersion:" value at
	// the top of an entity document. Candidates: the API-versions
	// closed set.
	ContextKindAPIVersion

	// ContextKindLabelValue — cursor on the value of
	// "metadata.labels.<label-name>:". Candidates depend on the
	// label-name (e.g., component.aggrega.ai/category → categories).
	ContextKindLabelValue

	// ContextKindProvider — cursor on "spec.provider:" value (Recipe
	// or FeatureRecipe). Candidates: Provider URIs (workspace-walked).
	ContextKindProvider

	// ContextKindSchemaKeyword — cursor inside the spec.schema
	// subtree at a position where a JSON Schema keyword is expected.
	// Candidates: vanilla JSON Schema keywords + Aggrega extensions.
	ContextKindSchemaKeyword
)

// ClassifyForCompletion inspects the cursor's path + side and returns
// the matching completion-specific ContextKind. Called when
// yamlpos.Classify returned ContextKindUnknown (the cursor is not a
// navigation reference shape) — this classifier picks up the
// completion-only contexts.
//
// The classifier mirrors yamlpos.ClassifyPath's strip-and-suffix-match
// pattern (strip leading "spec", strip recursive "(subsystems, <key>)"
// pairs greedily) so subsystem-nested cases are handled uniformly.
//
// When no completion-only suffix matches, ContextKindUnknown is
// returned and the dispatcher emits an empty candidate list.
func ClassifyForCompletion(path []string, side yamlpos.ContextSide) ContextKind {
	if len(path) == 0 {
		return ContextKindUnknown
	}

	// Top-of-document scalar fields.
	if len(path) == 1 {
		switch path[0] {
		case "kind":
			return ContextKindKind
		case "apiVersion":
			return ContextKindAPIVersion
		}
	}

	// metadata.labels.<label-name>:  — three elements, with optional
	// LabelValue dispatch on the cursor side (Value-side cursors land
	// inside the label-value scalar; Key-side cursors would be on the
	// label-name itself, which is a separate completion shape —
	// label-name completion is deferred).
	if len(path) == 3 && path[0] == "metadata" && path[1] == "labels" && side == yamlpos.ContextSideValue {
		return ContextKindLabelValue
	}

	if path[0] != "spec" {
		return ContextKindUnknown
	}

	rest := path[1:]

	// Strip recursive (subsystems, <key>) pairs for completion
	// contexts that can nest (currently no spec.<x>.provider equivalent
	// nests; this is forward-compat with hover's strip-and-match
	// pattern).
	for len(rest) >= 2 && rest[0] == "subsystems" {
		rest = rest[2:]
	}

	// spec.provider — cursor on the Recipe / FeatureRecipe provider name.
	if len(rest) == 1 && rest[0] == "provider" {
		return ContextKindProvider
	}

	// spec.schema.* — any cursor inside the schema subtree where a
	// schema keyword is expected. The completion handler offers
	// keyword candidates at the property-name position (mapping keys
	// inside schema.properties.<f> or inside the schema object
	// itself).
	if len(rest) >= 2 && rest[0] == "schema" {
		return ContextKindSchemaKeyword
	}

	return ContextKindUnknown
}

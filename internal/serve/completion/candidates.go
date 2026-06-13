// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package completion

import "strings"

// This file holds the per-context candidate generators. Each takes
// the inputs the dispatcher pre-enumerates (URI lists, path lists,
// local-name lists, label-name) and returns a sorted CompletionItem
// slice ready for the JSON-RPC response.
//
// The functions are exported so the dispatcher in internal/serve can
// invoke them directly after classifying the cursor.

// Kind returns the entity-kind candidate list. Order follows
// EntityKinds (authoring-progression: Operator → Architect → Ops →
// Platform).
func Kind() []CompletionItem {
	return orderedItems(EntityKinds, KindClass, func(label string) string {
		return "Aggrega entity kind"
	})
}

// APIVersion returns the apiVersion candidate list. Single value
// today.
func APIVersion() []CompletionItem {
	return orderedItems(APIVersions, KindValue, func(label string) string {
		return "Aggrega API version"
	})
}

// LabelValue dispatches on the label-name to the appropriate closed
// set. For component.aggrega.ai/category the candidates are the
// ComponentCategories slice (empty today). For other label
// names there is no closed set; empty list.
//
// labelName is the dotted key under metadata.labels (e.g.,
// "component.aggrega.ai/category").
func LabelValue(labelName string) []CompletionItem {
	switch labelName {
	case "component.aggrega.ai/category":
		return alphabeticalItems(ComponentCategories, KindEnum, func(label string) string {
			return "Component category"
		})
	}
	return nil
}

// Extends returns the candidate list for a spec.extends: cursor.
// uris is the pre-enumerated list of same-kind URIs from the
// workspace (the dispatcher reads the host file's kind: and filters
// accordingly).
//
// prefix narrows by string prefix when non-empty (per the
// trigger-character "/" narrowing convention).
func Extends(uris []string, prefix string) []CompletionItem {
	items := alphabeticalItems(uris, KindReference, func(label string) string {
		return "Workspace entity URI"
	})
	return filterByPrefix(items, prefix)
}

// ComponentsType returns the candidate list for a
// spec.components.<n>.type: cursor (Aggregate). uris is the
// pre-enumerated list of Component URIs from the workspace.
func ComponentsType(uris []string, prefix string) []CompletionItem {
	items := alphabeticalItems(uris, KindClass, func(label string) string {
		return "Component URI"
	})
	return filterByPrefix(items, prefix)
}

// FeaturesType returns the candidate list for a
// spec.components.<c>.features.<f>.type: cursor (Aggregate). uris is
// the pre-enumerated list of Feature URIs from the workspace.
func FeaturesType(uris []string, prefix string) []CompletionItem {
	items := alphabeticalItems(uris, KindClass, func(label string) string {
		return "Feature URI"
	})
	return filterByPrefix(items, prefix)
}

// Implements returns the candidate list for a spec.implements:
// cursor (Recipe / FeatureRecipe). uris is the pre-enumerated list
// of Component URIs (Recipe context) or Feature URIs (FeatureRecipe
// context); the dispatcher passes the right kind based on the host
// file's kind:.
func Implements(uris []string, prefix string) []CompletionItem {
	items := alphabeticalItems(uris, KindReference, func(label string) string {
		return "Catalog entity URI"
	})
	return filterByPrefix(items, prefix)
}

// Provider returns the candidate list for a spec.provider: cursor
// (Recipe / FeatureRecipe). uris is the pre-enumerated list of
// Provider URIs from the workspace.
func Provider(uris []string, prefix string) []CompletionItem {
	items := alphabeticalItems(uris, KindReference, func(label string) string {
		return "Provider URI"
	})
	return filterByPrefix(items, prefix)
}

// SchemaTypeValue returns the candidate list for cursors on the
// VALUE side of a `type:` keyword inside spec.schema. Offers the JSON
// Schema 2020-12 primitive type names — object, string, integer,
// number, boolean, array, null. Order matches the SchemaTypePrimitives
// slice (object / string first; the most common authoring choices).
func SchemaTypeValue() []CompletionItem {
	return orderedItems(SchemaTypePrimitives, KindEnum, func(label string) string {
		return "JSON Schema primitive type"
	})
}

// SchemaKeyword returns the candidate list for cursors inside the
// spec.schema subtree. Combines vanilla JSON Schema 2020-12 keywords
// (alphabetical, sorted first) with Aggrega x-aggrega-* extensions
// (alphabetical, sorted after vanilla via "z " sortText prefix to
// place them after the standard keywords).
func SchemaKeyword() []CompletionItem {
	out := alphabeticalItems(JSONSchemaKeywords, KindKeyword, func(label string) string {
		return "JSON Schema 2020-12 keyword"
	})
	out = append(out, schemaExtensionItems(AggregaSchemaExtensions, KindKeyword, func(label string) string {
		return "Aggrega schema extension"
	})...)
	return out
}

// ManifestRecipeReference returns the candidate list for a Manifest mapping
// value-side cursor. uris is the pre-enumerated (sorted) list of Recipe or
// FeatureRecipe URIs from the workspace. When mappingType names the
// component/feature type the cursor's mapping key binds, recipes whose
// spec.implements (implementsByName[uri]) is able to implement that type RANK
// FIRST — implements is a suggestion that helps the author pick the right
// recipe, never a constraint, so every recipe is still offered. Prefix filtering
// is preserved, as is alphabetical order within each rank group (uris arrives
// sorted and the partition is stable). An empty mappingType falls back to the
// plain alphabetical list.
func ManifestRecipeReference(uris []string, prefix, mappingType string, implementsByName map[string]string) []CompletionItem {
	if mappingType == "" {
		items := alphabeticalItems(uris, KindReference, func(string) string {
			return "Recipe URI"
		})
		return filterByPrefix(items, prefix)
	}
	var matches, rest []string
	for _, u := range uris {
		if implementsBaseMatch(implementsByName[u], mappingType) {
			matches = append(matches, u)
		} else {
			rest = append(rest, u)
		}
	}
	ranked := make([]string, 0, len(uris))
	ranked = append(ranked, matches...)
	ranked = append(ranked, rest...)
	items := orderedItems(ranked, KindReference, func(label string) string {
		if implementsBaseMatch(implementsByName[label], mappingType) {
			return "Recipe URI — implements " + mappingType
		}
		return "Recipe URI"
	})
	return filterByPrefix(items, prefix)
}

// implementsBaseMatch reports whether a recipe's spec.implements value is a
// candidate for the given Manifest mapping-key type. The comparison is on the
// version-stripped base (it is a ranking hint for authoring completion, not the
// resolution precedence — resolution is mapping-only and never consults this).
func implementsBaseMatch(impl, mappingType string) bool {
	if impl == "" {
		return false
	}
	return baseURI(impl) == baseURI(mappingType)
}

func baseURI(s string) string {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimPrefix(s, "^")
}

// ReleaseAggregatePath returns the candidate list for a Release's
// spec.aggregate: cursor. paths is the pre-enumerated list of
// workspace-relative Aggregate file paths (extension-stripped).
func ReleaseAggregatePath(paths []string, prefix string) []CompletionItem {
	items := alphabeticalItems(paths, KindFile, func(label string) string {
		return "Workspace-relative Aggregate path"
	})
	return filterByPrefix(items, prefix)
}

// ReleaseManifestPath returns the candidate list for a Release's
// spec.manifest: cursor. paths is the pre-enumerated list of
// workspace-relative Manifest file paths (extension-stripped).
func ReleaseManifestPath(paths []string, prefix string) []CompletionItem {
	items := alphabeticalItems(paths, KindFile, func(label string) string {
		return "Workspace-relative Manifest path"
	})
	return filterByPrefix(items, prefix)
}

// UsesTarget returns the candidate list for a uses.target: cursor.
// names is the pre-enumerated list of local Component instance keys
// from the same Aggregate document (no workspace walk).
func UsesTarget(names []string, prefix string) []CompletionItem {
	items := alphabeticalItems(names, KindField, func(label string) string {
		return "Local component instance"
	})
	return filterByPrefix(items, prefix)
}

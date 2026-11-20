// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ScopeToInstanceVersions returns lookup answering, for every reference table
// holds, at the version table resolves that reference to, and passing every other
// reference through as written. A component reference is answered from the
// table's component rows and a feature reference from its feature rows, so a
// Component and a Feature written alike each read their own version. A compilation of a Release reads each instance's
// Component and Feature through the lookup it is handed, so handing it this one
// compiles every instance at the version the instance-version table selected,
// whichever reader asks: the instance's document, its effective schema, and the
// type the mapped Recipe's implements is judged against.
//
// What it answers at the table's version:
//
//   - Component and Feature, asked with a reference the table holds, are asked
//     of lookup with that reference at the table's version, as an exact
//     reference. The table is keyed by the reference as written, so one
//     reference resolves to one version wherever in the Release it is read.
//   - EffectiveSchema, the composed schema of an extending Feature, is asked
//     with the reference at the version of the component row, then, where that
//     answers nothing, of the feature row. The eagerly-loaded catalog keys the
//     composed schemas by identity, so the version selects the schema read. The
//     lazy view keys them by name alone, one schema per Feature name, so the
//     version leaves its answer as it is.
//   - NodeRecipeResolution keeps finding the mapping that reaches an instance
//     with the reference as the Aggregate writes it: the Manifest's keys are
//     matched against what the author wrote, never against a version the table
//     chose. The type at the version the build compiles the instance as is the
//     table's, for an instance of a reference the table holds, whatever form the
//     reference takes. An instance no mapping reaches is offered the Recipes
//     implementing that version as its pin candidates: it is asked of lookup with
//     the reference at the table's version, which no mapping reaches either,
//     since a type-axis key matches a type by its base and a name-axis entry by
//     the instance's name.
//
// Every other field of lookup is handed on unchanged, the Manifest's mapping
// lookup among them, and a field lookup leaves nil stays nil, so a lookup that
// resolves nothing still resolves nothing.
func ScopeToInstanceVersions(lookup resolver.CatalogLookup, table InstanceVersions) resolver.CatalogLookup {
	scoped := lookup
	at := func(reference string) string {
		if versioned, held := table.at(reference); held {
			return versioned
		}
		return reference
	}
	if component := lookup.Component; component != nil {
		scoped.Component = func(typeURI string) (types.Component, bool) {
			return component(at(typeURI))
		}
	}
	if feature := lookup.Feature; feature != nil {
		scoped.Feature = func(typeURI string) (types.Feature, bool) {
			if versioned, held := table.featureAt(typeURI); held {
				return feature(versioned)
			}
			return feature(typeURI)
		}
	}
	if effectiveSchema := lookup.EffectiveSchema; effectiveSchema != nil {
		scoped.EffectiveSchema = func(typeURI string) (compactform.Schema, bool) {
			if versioned, held := table.at(typeURI); held {
				if schema, ok := effectiveSchema(versioned); ok {
					return schema, true
				}
			}
			if versioned, held := table.featureAt(typeURI); held {
				return effectiveSchema(versioned)
			}
			return effectiveSchema(typeURI)
		}
	}
	if nodeRecipe := lookup.NodeRecipeResolution; nodeRecipe != nil {
		scoped.NodeRecipeResolution = func(manifest *types.Manifest, instanceName, componentType string) resolver.NodeRecipeResolution {
			compiledAs, held := table.at(componentType)
			if !held {
				return nodeRecipe(manifest, instanceName, componentType)
			}
			resolution := nodeRecipe(manifest, instanceName, componentType)
			if !resolution.Mapped && len(resolution.AmbiguousKeys) == 0 {
				return nodeRecipe(manifest, instanceName, compiledAs)
			}
			resolution.ResolvedType = compiledAs
			return resolution
		}
	}
	return scoped
}

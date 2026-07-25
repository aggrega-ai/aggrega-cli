// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ComponentLookup resolves a component name URI to the raw catalog Component.
// Used by ResolveComponentExtends for chain walking.
type ComponentLookup func(uri string) (types.Component, bool)

// FeatureLookup resolves a feature name URI to the raw catalog Feature.
type FeatureLookup func(uri string) (types.Feature, bool)

// ResolveComponentExtends returns a fully-merged Component after walking the
// extends chain. Labels/Annotations maps are deep-merged (child wins per key),
// list fields (Inject, RequiredFeatures) are concatenated and
// deduplicated, UsesSchema entries are merged (child entry overrides parent on
// the same socket name). Returns the original component unchanged when
// Spec.Extends == "".
//
// Spec.Schema is left as-authored on the merged component. JSON Schema
// composition across the extends chain runs through the JSON-Schema-merge
// phase in ResolveInheritance when CatalogEntry.JSONSchema is populated; the
// schema-authoring migration leaves Schema content authored verbatim on each
// entry and lets the merger phase unify them.
func ResolveComponentExtends(comp types.Component, lookup ComponentLookup) (types.Component, []*ValidationError) {
	if comp.Spec.Extends == "" {
		return comp, nil
	}

	catalogLookup := func(uri string) (CatalogEntry, bool) {
		c, ok := lookup(uri)
		if !ok {
			return CatalogEntry{}, false
		}
		return CatalogEntry{URI: c.Metadata.Name, Extends: c.Spec.Extends}, true
	}
	entry := CatalogEntry{URI: comp.Metadata.Name, Extends: comp.Spec.Extends}

	if result := ResolveInheritance(entry, catalogLookup); len(result.Errors) > 0 {
		return comp, result.Errors
	}

	// Walk the chain child → root, collect *types.Component per ancestor, then
	// fold-merge from root into child so child fields take precedence.
	chain, chainErrs := collectComponentChain(comp, lookup)
	if len(chainErrs) > 0 {
		return comp, chainErrs
	}

	merged := chain[len(chain)-1] // root ancestor
	for i := len(chain) - 2; i >= 0; i-- {
		merged = mergeComponent(merged, chain[i])
	}
	return merged, nil
}

// ResolveFeatureExtends is the Feature analogue of ResolveComponentExtends.
func ResolveFeatureExtends(feat types.Feature, lookup FeatureLookup) (types.Feature, []*ValidationError) {
	if feat.Spec.Extends == "" {
		return feat, nil
	}

	catalogLookup := func(uri string) (CatalogEntry, bool) {
		f, ok := lookup(uri)
		if !ok {
			return CatalogEntry{}, false
		}
		return CatalogEntry{URI: f.Metadata.Name, Extends: f.Spec.Extends}, true
	}
	entry := CatalogEntry{URI: feat.Metadata.Name, Extends: feat.Spec.Extends}

	if result := ResolveInheritance(entry, catalogLookup); len(result.Errors) > 0 {
		return feat, result.Errors
	}

	chain, chainErrs := collectFeatureChain(feat, lookup)
	if len(chainErrs) > 0 {
		return feat, chainErrs
	}

	merged := chain[len(chain)-1]
	for i := len(chain) - 2; i >= 0; i-- {
		merged = mergeFeature(merged, chain[i])
	}
	return merged, nil
}

// collectComponentChain returns the chain [child, parent, grandparent, ...].
// Relies on ResolveInheritance having already validated cycles/depth/missing-parent,
// so errors here shouldn't normally occur — they're a safety net for concurrent edits.
func collectComponentChain(child types.Component, lookup ComponentLookup) ([]types.Component, []*ValidationError) {
	chain := []types.Component{child}
	visited := map[string]bool{child.Metadata.Name: true}
	current := child
	for current.Spec.Extends != "" {
		if visited[current.Spec.Extends] {
			// Should have been caught by ResolveInheritance; defensive only.
			return nil, []*ValidationError{{
				Entity:     child.Metadata.Name,
				FieldPath:  ".spec.extends",
				Constraint: "circular extends detected while rebuilding chain",
				Err:        ErrCyclicExtension,
			}}
		}
		parent, ok := lookup(current.Spec.Extends)
		if !ok {
			return nil, []*ValidationError{{
				Entity:     child.Metadata.Name,
				FieldPath:  ".spec.extends",
				Constraint: "parent component not found while rebuilding chain",
				Err:        ErrInheritanceMerge,
			}}
		}
		visited[current.Spec.Extends] = true
		chain = append(chain, parent)
		current = parent
	}
	return chain, nil
}

func collectFeatureChain(child types.Feature, lookup FeatureLookup) ([]types.Feature, []*ValidationError) {
	chain := []types.Feature{child}
	visited := map[string]bool{child.Metadata.Name: true}
	current := child
	for current.Spec.Extends != "" {
		if visited[current.Spec.Extends] {
			return nil, []*ValidationError{{
				Entity:     child.Metadata.Name,
				FieldPath:  ".spec.extends",
				Constraint: "circular extends detected while rebuilding chain",
				Err:        ErrCyclicExtension,
			}}
		}
		parent, ok := lookup(current.Spec.Extends)
		if !ok {
			return nil, []*ValidationError{{
				Entity:     child.Metadata.Name,
				FieldPath:  ".spec.extends",
				Constraint: "parent feature not found while rebuilding chain",
				Err:        ErrInheritanceMerge,
			}}
		}
		visited[current.Spec.Extends] = true
		chain = append(chain, parent)
		current = parent
	}
	return chain, nil
}

// mergeComponent overlays the child Component onto the parent. The result keeps
// the child's identity (Name, Version, Extends) but carries merged Metadata and
// Spec. Scalar Spec fields keep the child value if non-empty. Maps merge with
// child wins; slices concatenate and dedupe.
func mergeComponent(parent, child types.Component) types.Component {
	out := child
	out.Metadata.Labels = mergeStringMap(parent.Metadata.Labels, child.Metadata.Labels)
	out.Metadata.Annotations = mergeStringMap(parent.Metadata.Annotations, child.Metadata.Annotations)

	out.Spec.SecretsSchema = mergeSecretsSchema(parent.Spec.SecretsSchema, child.Spec.SecretsSchema)
	out.Spec.RequiredFeatures = concatDedupeString(parent.Spec.RequiredFeatures, child.Spec.RequiredFeatures)
	out.Spec.Inject = concatDedupeString(parent.Spec.Inject, child.Spec.Inject)
	out.Spec.UsesSchema = mergeUsesSchema(parent.Spec.UsesSchema, child.Spec.UsesSchema)
	return out
}

func mergeFeature(parent, child types.Feature) types.Feature {
	out := child
	out.Metadata.Labels = mergeStringMap(parent.Metadata.Labels, child.Metadata.Labels)
	out.Metadata.Annotations = mergeStringMap(parent.Metadata.Annotations, child.Metadata.Annotations)

	out.Spec.SecretsSchema = mergeSecretsSchema(parent.Spec.SecretsSchema, child.Spec.SecretsSchema)
	out.Spec.UsesSchema = mergeUsesSchema(parent.Spec.UsesSchema, child.Spec.UsesSchema)
	return out
}

// mergeStringMap returns a new OrderedMap with parent entries overridden by child entries.
// Parent keys come first (in author order), then child keys that weren't in parent.
// Empty input returns an empty OrderedMap.
func mergeStringMap(parent, child types.OrderedMap[string]) types.OrderedMap[string] {
	out := types.OrderedMap[string]{}
	for k, v := range parent.All() {
		out.Set(k, v)
	}
	for k, v := range child.All() {
		out.Set(k, v)
	}
	return out
}

// concatDedupeString preserves order (parent first, then child), dropping duplicates.
func concatDedupeString(parent, child []string) []string {
	if len(parent) == 0 && len(child) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(parent)+len(child))
	out := make([]string, 0, len(parent)+len(child))
	for _, s := range parent {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range child {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// mergeSecretsSchema overlays parent and child secretsSchema entries. Child
// wins on the same slot name; slots unique to the parent are preserved.
// Same semantics as mergeUsesSchema.
func mergeSecretsSchema(parent, child types.OrderedMap[types.AttachmentSlot]) types.OrderedMap[types.AttachmentSlot] {
	out := types.OrderedMap[types.AttachmentSlot]{}
	for k, v := range parent.All() {
		out.Set(k, v)
	}
	for k, v := range child.All() {
		out.Set(k, v)
	}
	return out
}

// mergeUsesSchema overlays parent and child usesSchema entries. Child wins on
// the same socket name; sockets unique to the parent are preserved. Preserves
// author order — parent keys first, then child-only keys.
func mergeUsesSchema(parent, child types.OrderedMap[types.UsesSchemaEntry]) types.OrderedMap[types.UsesSchemaEntry] {
	out := types.OrderedMap[types.UsesSchemaEntry]{}
	for k, v := range parent.All() {
		out.Set(k, v)
	}
	for k, v := range child.All() {
		out.Set(k, v)
	}
	return out
}

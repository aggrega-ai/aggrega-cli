// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package merger

import (
	"sort"

	"github.com/google/jsonschema-go/jsonschema"
)

// provenanceKey is the Extra map key that holds per-keyword provenance
// metadata on a merged schema's node. Reserved-namespace via the
// double-underscore prefix; authors writing such keys overwrite at
// their own risk.
const provenanceKey = "__source_origin"

// recordProvenance writes provenance[path][keyword] = source. Creates
// the inner map if absent. Empty source values are not recorded
// (avoids writing empty provenance entries for keywords whose merge
// resolved to a no-value state).
func recordProvenance(provenance map[string]map[string]string, path, keyword, source string) {
	if source == "" {
		return
	}
	inner, ok := provenance[path]
	if !ok {
		inner = map[string]string{}
		provenance[path] = inner
	}
	inner[keyword] = source
}

// seedProvenance records baseline provenance entries for a fresh
// Schema entering Merge's pairwise fold — every constraint present on
// the initial schema gets attributed to that schema's Source. The
// pairwise mergePair calls overwrite these entries as the chain
// progresses (child constraints replace parent's per the per-keyword
// rules), so the final post-walk reflects the last contributor.
//
// seedProvenance recurses into properties / $defs / items /
// additionalProperties / propertyNames / contains / etc., so deep
// nodes carry baseline provenance too.
func seedProvenance(s *Schema, path, source string, provenance map[string]map[string]string) {
	if s == nil || s.Schema == nil || source == "" {
		return
	}
	seedInnerProvenance(s.Schema, path, source, provenance)
}

// seedInnerProvenance is seedProvenance for a library *jsonschema.Schema.
func seedInnerProvenance(s *jsonschema.Schema, path, source string, provenance map[string]map[string]string) {
	if s == nil || source == "" {
		return
	}

	// Scalar / single-value constraints — record per-keyword entry
	// when present.
	if s.Type != "" {
		recordProvenance(provenance, path, "type", source)
	}
	if len(s.Types) > 0 {
		recordProvenance(provenance, path, "types", source)
	}
	if s.Pattern != "" {
		recordProvenance(provenance, path, "pattern", source)
	}
	if s.Format != "" {
		recordProvenance(provenance, path, "format", source)
	}
	if s.ContentEncoding != "" {
		recordProvenance(provenance, path, "contentEncoding", source)
	}
	if s.ContentMediaType != "" {
		recordProvenance(provenance, path, "contentMediaType", source)
	}
	if s.Ref != "" {
		recordProvenance(provenance, path, "ref", source)
	}
	if s.ID != "" {
		recordProvenance(provenance, path, "id", source)
	}
	if s.Anchor != "" {
		recordProvenance(provenance, path, "anchor", source)
	}
	if s.Comment != "" {
		recordProvenance(provenance, path, "comment", source)
	}
	if s.Title != "" {
		recordProvenance(provenance, path, "title", source)
	}
	if s.Description != "" {
		recordProvenance(provenance, path, "description", source)
	}
	if s.Minimum != nil {
		recordProvenance(provenance, path, "minimum", source)
	}
	if s.Maximum != nil {
		recordProvenance(provenance, path, "maximum", source)
	}
	if s.ExclusiveMinimum != nil {
		recordProvenance(provenance, path, "exclusiveMinimum", source)
	}
	if s.ExclusiveMaximum != nil {
		recordProvenance(provenance, path, "exclusiveMaximum", source)
	}
	if s.MultipleOf != nil {
		recordProvenance(provenance, path, "multipleOf", source)
	}
	if s.MinLength != nil {
		recordProvenance(provenance, path, "minLength", source)
	}
	if s.MaxLength != nil {
		recordProvenance(provenance, path, "maxLength", source)
	}
	if s.MinItems != nil {
		recordProvenance(provenance, path, "minItems", source)
	}
	if s.MaxItems != nil {
		recordProvenance(provenance, path, "maxItems", source)
	}
	if s.MinProperties != nil {
		recordProvenance(provenance, path, "minProperties", source)
	}
	if s.MaxProperties != nil {
		recordProvenance(provenance, path, "maxProperties", source)
	}
	if s.MinContains != nil {
		recordProvenance(provenance, path, "minContains", source)
	}
	if s.MaxContains != nil {
		recordProvenance(provenance, path, "maxContains", source)
	}
	if s.UniqueItems {
		recordProvenance(provenance, path, "uniqueItems", source)
	}
	if len(s.Enum) > 0 {
		recordProvenance(provenance, path, "enum", source)
	}
	if s.Const != nil {
		recordProvenance(provenance, path, "const", source)
	}
	if s.Default != nil {
		recordProvenance(provenance, path, "default", source)
	}
	if len(s.Required) > 0 {
		recordProvenance(provenance, path, "required", source)
	}

	// Extra entries — non-reserved keys carry provenance.
	for k := range s.Extra {
		if k == provenanceKey {
			continue
		}
		recordProvenance(provenance, path, k, source)
	}

	// Recurse into structural sub-schemas.
	for k, sub := range s.Properties {
		seedInnerProvenance(sub, joinPath(path, k), source, provenance)
	}
	if s.Items != nil {
		seedInnerProvenance(s.Items, joinPath(path, "items"), source, provenance)
	}
	if s.AdditionalProperties != nil {
		seedInnerProvenance(s.AdditionalProperties, joinPath(path, "additionalProperties"), source, provenance)
	}
	if s.PropertyNames != nil {
		seedInnerProvenance(s.PropertyNames, joinPath(path, "propertyNames"), source, provenance)
	}
	if s.Contains != nil {
		seedInnerProvenance(s.Contains, joinPath(path, "contains"), source, provenance)
	}

	// Composition + conditional keywords carry no instance-path
	// segments — entries apply at the parent position. Recurse with
	// the same path so any sub-schemas inside composition contribute
	// to provenance.
	for _, sub := range s.AllOf {
		seedInnerProvenance(sub, path, source, provenance)
	}
	for _, sub := range s.AnyOf {
		seedInnerProvenance(sub, path, source, provenance)
	}
	for _, sub := range s.OneOf {
		seedInnerProvenance(sub, path, source, provenance)
	}
	if s.Not != nil {
		seedInnerProvenance(s.Not, path, source, provenance)
	}
	if s.If != nil {
		seedInnerProvenance(s.If, path, source, provenance)
	}
	if s.Then != nil {
		seedInnerProvenance(s.Then, path, source, provenance)
	}
	if s.Else != nil {
		seedInnerProvenance(s.Else, path, source, provenance)
	}

	// $defs entries are reusable definitions, not instance positions.
	// Walk them so nested provenance is captured.
	for k, sub := range s.Defs {
		seedInnerProvenance(sub, joinPath(path, "$defs."+k), source, provenance)
	}
	for k, sub := range s.Definitions {
		seedInnerProvenance(sub, joinPath(path, "definitions."+k), source, provenance)
	}
}

// applyProvenance walks the merged tree at root path and writes
// Extra[provenanceKey] = provenance[path] at each node that has
// recorded provenance entries. Empty provenance entries skip the
// attachment (a node with no recorded constraints has no
// __source_origin tag).
//
// The Extra map values are stored as map[string]string (a stable,
// JSON-serializable shape) so downstream MarshalJSON renders the
// provenance deterministically (alphabetical key order via the
// stdlib's map-encoding rules).
func applyProvenance(s *jsonschema.Schema, path string, provenance map[string]map[string]string) {
	if s == nil {
		return
	}

	entries, ok := provenance[path]
	if ok && len(entries) > 0 {
		if s.Extra == nil {
			s.Extra = map[string]any{}
		}
		// Build a sorted-key string-to-string map so the rendered
		// output is deterministic. The library renders Extra values
		// via encoding/json which alphabetizes map keys by default,
		// but storing a fresh map (rather than reusing the merge-time
		// map) ensures no provenance-internal aliasing across nodes.
		copied := make(map[string]string, len(entries))
		keys := make([]string, 0, len(entries))
		for k := range entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			copied[k] = entries[k]
		}
		s.Extra[provenanceKey] = copied
	}

	for k, sub := range s.Properties {
		applyProvenance(sub, joinPath(path, k), provenance)
	}
	if s.Items != nil {
		applyProvenance(s.Items, joinPath(path, "items"), provenance)
	}
	if s.AdditionalProperties != nil {
		applyProvenance(s.AdditionalProperties, joinPath(path, "additionalProperties"), provenance)
	}
	if s.PropertyNames != nil {
		applyProvenance(s.PropertyNames, joinPath(path, "propertyNames"), provenance)
	}
	if s.Contains != nil {
		applyProvenance(s.Contains, joinPath(path, "contains"), provenance)
	}

	for _, sub := range s.AllOf {
		applyProvenance(sub, path, provenance)
	}
	for _, sub := range s.AnyOf {
		applyProvenance(sub, path, provenance)
	}
	for _, sub := range s.OneOf {
		applyProvenance(sub, path, provenance)
	}
	if s.Not != nil {
		applyProvenance(s.Not, path, provenance)
	}
	if s.If != nil {
		applyProvenance(s.If, path, provenance)
	}
	if s.Then != nil {
		applyProvenance(s.Then, path, provenance)
	}
	if s.Else != nil {
		applyProvenance(s.Else, path, provenance)
	}

	for k, sub := range s.Defs {
		applyProvenance(sub, joinPath(path, "$defs."+k), provenance)
	}
	for k, sub := range s.Definitions {
		applyProvenance(sub, joinPath(path, "definitions."+k), provenance)
	}
}

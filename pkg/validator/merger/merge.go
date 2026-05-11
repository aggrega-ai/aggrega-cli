// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package merger

import (
	"sort"

	"github.com/google/jsonschema-go/jsonschema"
)

// mergePair merges child into parent, returning a new *Schema that
// owns its merged tree. The provenance map records per-constraint
// origins; pathPrefix names the position of the parent/child schemas
// in the merged tree (empty at root, "port" for properties.port at
// depth 1, "tlsConfig.path" for properties.tlsConfig.properties.path
// at depth 2). Narrowing violations detected during the merge are
// returned as a slice; the caller (Merge) appends them to the final
// result.
//
// The merge does not mutate either input. parent's library Schema is
// already a clone in Merge's accumulator pattern, so mergePair safely
// writes into the accumulator's tree while constructing the merged
// output. Returns a fresh *Schema whose embedded library Schema is a
// new structure carrying merged constraints.
func mergePair(parent, child *Schema, pathPrefix string, provenance map[string]map[string]string) (*Schema, []NarrowingViolation) {
	out := &Schema{
		Schema: &jsonschema.Schema{},
		Source: child.Source,
	}

	pSchema := parent.Schema
	cSchema := child.Schema
	parentSource := parent.Source
	childSource := child.Source

	// Narrowing detection runs BEFORE the per-keyword merges so the
	// provenance map still reflects the parent's actual originator
	// (possibly several levels up the chain). After the per-keyword
	// merges run, provenance[pathPrefix][keyword] gets overwritten
	// to the child's source for keywords the child supplied — too
	// late for the narrowing detection to record the right parent
	// originator. detectNarrowing reads from the parent / child
	// Schema structures directly (which are untouched here); the
	// ParentSource field is resolved via the provenance map's
	// pre-overwrite state.
	violations := detectNarrowing(parent, child, pathPrefix, provenance)

	// Scalar / single-value constraints. mergeScalarString returns
	// the post-merge value and (optionally) the contributing source
	// to record in the provenance map at pathPrefix's keyword entry.

	type stringField struct {
		keyword string
		parent  string
		child   string
		set     func(v string)
	}
	stringFields := []stringField{
		{"type", pSchema.Type, cSchema.Type, func(v string) { out.Schema.Type = v }},
		{"pattern", pSchema.Pattern, cSchema.Pattern, func(v string) { out.Schema.Pattern = v }},
		{"format", pSchema.Format, cSchema.Format, func(v string) { out.Schema.Format = v }},
		{"contentEncoding", pSchema.ContentEncoding, cSchema.ContentEncoding, func(v string) { out.Schema.ContentEncoding = v }},
		{"contentMediaType", pSchema.ContentMediaType, cSchema.ContentMediaType, func(v string) { out.Schema.ContentMediaType = v }},
		{"ref", pSchema.Ref, cSchema.Ref, func(v string) { out.Schema.Ref = v }},
		{"id", pSchema.ID, cSchema.ID, func(v string) { out.Schema.ID = v }},
		{"anchor", pSchema.Anchor, cSchema.Anchor, func(v string) { out.Schema.Anchor = v }},
		{"comment", pSchema.Comment, cSchema.Comment, func(v string) { out.Schema.Comment = v }},
		{"title", pSchema.Title, cSchema.Title, func(v string) { out.Schema.Title = v }},
		{"description", pSchema.Description, cSchema.Description, func(v string) { out.Schema.Description = v }},
	}
	for _, f := range stringFields {
		value, isChild := pickString(f.parent, f.child)
		if value != "" {
			f.set(value)
			if isChild {
				recordProvenance(provenance, pathPrefix, f.keyword, childSource)
			}
			// If !isChild (parent contributed), the existing
			// provenance entry is the actual originator — possibly
			// several levels up the chain via prior pairwise rounds.
			// Don't overwrite.
		}
	}

	// Types slice (jsonschema 2020-12 allows multiple types).
	if len(cSchema.Types) > 0 {
		out.Schema.Types = append([]string(nil), cSchema.Types...)
		recordProvenance(provenance, pathPrefix, "types", childSource)
	} else if len(pSchema.Types) > 0 {
		out.Schema.Types = append([]string(nil), pSchema.Types...)
		// Parent contributed; preserve existing provenance entry.
	}

	// Numeric constraints — *float64 pointer fields.
	type numField struct {
		keyword string
		parent  *float64
		child   *float64
		set     func(v *float64)
	}
	numFields := []numField{
		{"minimum", pSchema.Minimum, cSchema.Minimum, func(v *float64) { out.Schema.Minimum = v }},
		{"maximum", pSchema.Maximum, cSchema.Maximum, func(v *float64) { out.Schema.Maximum = v }},
		{"exclusiveMinimum", pSchema.ExclusiveMinimum, cSchema.ExclusiveMinimum, func(v *float64) { out.Schema.ExclusiveMinimum = v }},
		{"exclusiveMaximum", pSchema.ExclusiveMaximum, cSchema.ExclusiveMaximum, func(v *float64) { out.Schema.ExclusiveMaximum = v }},
		{"multipleOf", pSchema.MultipleOf, cSchema.MultipleOf, func(v *float64) { out.Schema.MultipleOf = v }},
	}
	for _, f := range numFields {
		value, isChild := pickFloat(f.parent, f.child)
		if value != nil {
			f.set(value)
			if isChild {
				recordProvenance(provenance, pathPrefix, f.keyword, childSource)
			}
		}
	}

	// Integer constraints — *int pointer fields (length/count bounds).
	type intField struct {
		keyword string
		parent  *int
		child   *int
		set     func(v *int)
	}
	intFields := []intField{
		{"minLength", pSchema.MinLength, cSchema.MinLength, func(v *int) { out.Schema.MinLength = v }},
		{"maxLength", pSchema.MaxLength, cSchema.MaxLength, func(v *int) { out.Schema.MaxLength = v }},
		{"minItems", pSchema.MinItems, cSchema.MinItems, func(v *int) { out.Schema.MinItems = v }},
		{"maxItems", pSchema.MaxItems, cSchema.MaxItems, func(v *int) { out.Schema.MaxItems = v }},
		{"minProperties", pSchema.MinProperties, cSchema.MinProperties, func(v *int) { out.Schema.MinProperties = v }},
		{"maxProperties", pSchema.MaxProperties, cSchema.MaxProperties, func(v *int) { out.Schema.MaxProperties = v }},
		{"minContains", pSchema.MinContains, cSchema.MinContains, func(v *int) { out.Schema.MinContains = v }},
		{"maxContains", pSchema.MaxContains, cSchema.MaxContains, func(v *int) { out.Schema.MaxContains = v }},
	}
	for _, f := range intFields {
		value, isChild := pickInt(f.parent, f.child)
		if value != nil {
			f.set(value)
			if isChild {
				recordProvenance(provenance, pathPrefix, f.keyword, childSource)
			}
		}
	}

	// UniqueItems — bool field, no pointer wrapper. True wins (child
	// override; absent in parent means false).
	if cSchema.UniqueItems {
		out.Schema.UniqueItems = true
		recordProvenance(provenance, pathPrefix, "uniqueItems", childSource)
	} else if pSchema.UniqueItems {
		out.Schema.UniqueItems = true
	}

	// enum, const — child overrides if set.
	if len(cSchema.Enum) > 0 {
		out.Schema.Enum = append([]any(nil), cSchema.Enum...)
		recordProvenance(provenance, pathPrefix, "enum", childSource)
	} else if len(pSchema.Enum) > 0 {
		out.Schema.Enum = append([]any(nil), pSchema.Enum...)
	}
	if cSchema.Const != nil {
		out.Schema.Const = cSchema.Const
		recordProvenance(provenance, pathPrefix, "const", childSource)
	} else if pSchema.Const != nil {
		out.Schema.Const = pSchema.Const
	}

	// default — child override.
	if cSchema.Default != nil {
		out.Schema.Default = cSchema.Default
		recordProvenance(provenance, pathPrefix, "default", childSource)
	} else if pSchema.Default != nil {
		out.Schema.Default = pSchema.Default
	}

	// required — union; dedup; sort alphabetically.
	if len(pSchema.Required) > 0 || len(cSchema.Required) > 0 {
		seen := map[string]bool{}
		for _, k := range pSchema.Required {
			seen[k] = true
		}
		for _, k := range cSchema.Required {
			seen[k] = true
		}
		merged := make([]string, 0, len(seen))
		for k := range seen {
			merged = append(merged, k)
		}
		sort.Strings(merged)
		out.Schema.Required = merged
		// Provenance for required: record only when child
		// contributed — the existing provenance entry (from prior
		// rounds or initial seeding) is the actual originator when
		// only parent supplied. The union semantic means "both
		// contributed" lands on childSource (latest writer wins for
		// the keyword overall).
		if len(cSchema.Required) > 0 {
			recordProvenance(provenance, pathPrefix, "required", childSource)
		}
	}

	// properties — child keys layered into parent's; for matched
	// keys, recurse mergePair. PropertyOrder on the output is the
	// sorted-alphabetic union of property names.
	if len(pSchema.Properties) > 0 || len(cSchema.Properties) > 0 {
		merged := map[string]*jsonschema.Schema{}
		for k, v := range pSchema.Properties {
			// pSchema-only entries (not overridden in cSchema) get
			// cloned without recursion. Their provenance is already
			// in the map from prior pairwise steps or from Merge's
			// initial seedProvenance walk; re-seeding here would
			// overwrite multi-level chain entries with the immediate
			// pairwise parent's source (losing the actual originator).
			merged[k] = cloneInnerSchema(v)
		}
		for k, v := range cSchema.Properties {
			if pv, ok := pSchema.Properties[k]; ok {
				// Recurse pairwise on this property.
				subPath := joinPath(pathPrefix, k)
				pWrap := &Schema{Schema: pv, Source: parentSource}
				cWrap := &Schema{Schema: v, Source: childSource}
				subMerged, subViolations := mergePair(pWrap, cWrap, subPath, provenance)
				merged[k] = subMerged.Schema
				violations = append(violations, subViolations...)
			} else {
				merged[k] = cloneInnerSchema(v)
				subPath := joinPath(pathPrefix, k)
				seedInnerProvenance(v, subPath, childSource, provenance)
			}
		}
		out.Schema.Properties = merged
		// PropertyOrder = sorted-alphabetic union.
		names := make([]string, 0, len(merged))
		for k := range merged {
			names = append(names, k)
		}
		sort.Strings(names)
		out.Schema.PropertyOrder = names
	}

	// $defs — child entries layered into parent's; for matched keys,
	// child wins.
	if len(pSchema.Defs) > 0 || len(cSchema.Defs) > 0 {
		merged := map[string]*jsonschema.Schema{}
		for k, v := range pSchema.Defs {
			merged[k] = cloneInnerSchema(v)
		}
		for k, v := range cSchema.Defs {
			merged[k] = cloneInnerSchema(v)
		}
		out.Schema.Defs = merged
	}

	// definitions (draft-07) — same semantics as Defs.
	if len(pSchema.Definitions) > 0 || len(cSchema.Definitions) > 0 {
		merged := map[string]*jsonschema.Schema{}
		for k, v := range pSchema.Definitions {
			merged[k] = cloneInnerSchema(v)
		}
		for k, v := range cSchema.Definitions {
			merged[k] = cloneInnerSchema(v)
		}
		out.Schema.Definitions = merged
	}

	// allOf/anyOf/oneOf — child entries append to parent's list.
	out.Schema.AllOf = appendComposition(pSchema.AllOf, cSchema.AllOf)
	out.Schema.AnyOf = appendComposition(pSchema.AnyOf, cSchema.AnyOf)
	out.Schema.OneOf = appendComposition(pSchema.OneOf, cSchema.OneOf)

	// not — child override.
	if cSchema.Not != nil {
		out.Schema.Not = cloneInnerSchema(cSchema.Not)
	} else if pSchema.Not != nil {
		out.Schema.Not = cloneInnerSchema(pSchema.Not)
	}

	// if/then/else — child override atomically (any of the three set
	// on child means child's group wins).
	childHasIfThenElse := cSchema.If != nil || cSchema.Then != nil || cSchema.Else != nil
	if childHasIfThenElse {
		if cSchema.If != nil {
			out.Schema.If = cloneInnerSchema(cSchema.If)
		}
		if cSchema.Then != nil {
			out.Schema.Then = cloneInnerSchema(cSchema.Then)
		}
		if cSchema.Else != nil {
			out.Schema.Else = cloneInnerSchema(cSchema.Else)
		}
	} else {
		if pSchema.If != nil {
			out.Schema.If = cloneInnerSchema(pSchema.If)
		}
		if pSchema.Then != nil {
			out.Schema.Then = cloneInnerSchema(pSchema.Then)
		}
		if pSchema.Else != nil {
			out.Schema.Else = cloneInnerSchema(pSchema.Else)
		}
	}

	// items, additionalProperties, propertyNames, contains —
	// recurse pairwise if both present; otherwise child if set, else
	// parent.
	out.Schema.Items = mergeSubSchema(pSchema.Items, cSchema.Items, parentSource, childSource, joinPath(pathPrefix, "items"), provenance, &violations)
	out.Schema.AdditionalProperties = mergeSubSchema(pSchema.AdditionalProperties, cSchema.AdditionalProperties, parentSource, childSource, joinPath(pathPrefix, "additionalProperties"), provenance, &violations)
	out.Schema.PropertyNames = mergeSubSchema(pSchema.PropertyNames, cSchema.PropertyNames, parentSource, childSource, joinPath(pathPrefix, "propertyNames"), provenance, &violations)
	out.Schema.Contains = mergeSubSchema(pSchema.Contains, cSchema.Contains, parentSource, childSource, joinPath(pathPrefix, "contains"), provenance, &violations)

	// Extra — child entries override parent on key collision; non-
	// colliding entries union. __source_origin is reserved-namespace
	// and recomputed by the provenance pass; any author-supplied
	// value is overwritten.
	if len(pSchema.Extra) > 0 || len(cSchema.Extra) > 0 {
		merged := map[string]any{}
		for k, v := range pSchema.Extra {
			if k == "__source_origin" {
				continue
			}
			merged[k] = v
		}
		for k, v := range cSchema.Extra {
			if k == "__source_origin" {
				continue
			}
			merged[k] = v
		}
		out.Schema.Extra = merged
		// Record provenance only for child-contributed entries —
		// parent-only entries preserve their existing provenance
		// from prior rounds or initial seeding.
		for k := range merged {
			if _, inChild := cSchema.Extra[k]; inChild {
				recordProvenance(provenance, pathPrefix, k, childSource)
			}
		}
	}

	return out, violations
}

// pickString returns the post-merge string value plus an isChild
// flag indicating whether the value came from the child schema. The
// caller uses isChild to decide whether to recordProvenance: child-
// contributed values overwrite the provenance entry; parent-
// contributed values preserve the existing entry (which may name an
// originator several levels up the chain via prior pairwise rounds).
func pickString(parent, child string) (string, bool) {
	if child != "" {
		return child, true
	}
	if parent != "" {
		return parent, false
	}
	return "", false
}

// pickFloat returns the post-merge float pointer plus an isChild
// flag. Returns (nil, false) when both inputs are nil. Same
// provenance discipline as pickString.
func pickFloat(parent, child *float64) (*float64, bool) {
	if child != nil {
		v := *child
		return &v, true
	}
	if parent != nil {
		v := *parent
		return &v, false
	}
	return nil, false
}

// pickInt returns the post-merge int pointer plus an isChild flag.
// Returns (nil, false) when both inputs are nil. Same provenance
// discipline as pickString.
func pickInt(parent, child *int) (*int, bool) {
	if child != nil {
		v := *child
		return &v, true
	}
	if parent != nil {
		v := *parent
		return &v, false
	}
	return nil, false
}

// appendComposition concatenates parent's composing entries with child's,
// preserving both authors' constraints. Returns nil when both inputs
// are empty (to avoid forcing an empty slice into the output).
func appendComposition(parent, child []*jsonschema.Schema) []*jsonschema.Schema {
	if len(parent) == 0 && len(child) == 0 {
		return nil
	}
	out := make([]*jsonschema.Schema, 0, len(parent)+len(child))
	for _, s := range parent {
		out = append(out, cloneInnerSchema(s))
	}
	for _, s := range child {
		out = append(out, cloneInnerSchema(s))
	}
	return out
}

// mergeSubSchema merges two single-schema fields (e.g., Items,
// AdditionalProperties). If both present, recurse pairwise. Otherwise
// the present one wins (clone returned).
func mergeSubSchema(parent, child *jsonschema.Schema, parentSource, childSource, pathPrefix string, provenance map[string]map[string]string, violations *[]NarrowingViolation) *jsonschema.Schema {
	if parent == nil && child == nil {
		return nil
	}
	if parent == nil {
		// child introduces a sub-schema parent didn't have; seed
		// provenance with childSource for the new constraints.
		seedInnerProvenance(child, pathPrefix, childSource, provenance)
		return cloneInnerSchema(child)
	}
	if child == nil {
		// Parent's sub-schema is preserved; provenance is already
		// in the map from prior steps or initial seeding. No re-seed.
		return cloneInnerSchema(parent)
	}
	pWrap := &Schema{Schema: parent, Source: parentSource}
	cWrap := &Schema{Schema: child, Source: childSource}
	subMerged, subViolations := mergePair(pWrap, cWrap, pathPrefix, provenance)
	*violations = append(*violations, subViolations...)
	return subMerged.Schema
}

// cloneSchema returns a *Schema clone of s. Used by Merge to seed the
// pairwise fold's accumulator: the input chain's schemas are not
// mutated, so the accumulator starts as a clone of schemas[0] and
// each pairwise merge produces a fresh accumulator.
//
// Uses the library's CloneSchemas() for the embedded library Schema.
// Source and NarrowingViolations are shallow-copied (NarrowingViolations
// is intentionally fresh — the clone starts with no violations).
func cloneSchema(s *Schema) *Schema {
	if s == nil {
		return nil
	}
	return &Schema{
		Schema: cloneInnerSchema(s.Schema),
		Source: s.Source,
	}
}

// cloneInnerSchema is the library-side deep-clone for a *jsonschema.Schema.
func cloneInnerSchema(s *jsonschema.Schema) *jsonschema.Schema {
	if s == nil {
		return nil
	}
	return s.CloneSchemas()
}

// joinPath produces a dot-delimited path from a prefix and a segment.
func joinPath(prefix, segment string) string {
	if prefix == "" {
		return segment
	}
	if segment == "" {
		return prefix
	}
	return prefix + "." + segment
}

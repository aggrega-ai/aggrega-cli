// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package jsonschema

import (
	"reflect"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// localDefsPointer is the JSON Pointer prefix locating one of a schema's
// own reusable definitions, and localDefsPrefix is the same pointer in
// the fragment form a reference is written in. One is derived from the
// other so a diagnostic naming a definition and a reference naming one
// cannot drift apart.
const (
	localDefsPointer = "/$defs/"
	localDefsPrefix  = "#" + localDefsPointer
)

// evaluatedByWalk names the constraint families the walk evaluates at a
// position through its own structures, so the reduced copy of that
// position must not repeat them and report the same failure twice.
//
// Each field is set from what was actually indexed at the position rather
// than from what the schema declares, so a family the index could not
// build — a branch whose reference does not resolve on its own — stays in
// the reduced copy and keeps being checked. Deriving it from the
// declaration instead would drop the constraint precisely in the case
// where the walk cannot carry it.
type evaluatedByWalk struct {
	required         bool
	allOf            bool
	anyOf            bool
	oneOf            bool
	not              bool
	dependentSchemas bool
}

// pruneOwnKeywords returns a copy of s carrying every constraint that
// applies to s's own instance, with the declared property values replaced
// by schemas that constrain nothing.
//
// The copy exists so that validating an instance against it reports only
// the failures belonging to that instance position. A schema library
// evaluates an object's declared properties by iterating a map, so one
// call over a subtree with several equally-failing children reports
// whichever child the iteration reached first — a different one on each
// run. Removing the child positions leaves the library nothing to choose
// between, and the walk that owns this copy reaches each child on its
// own, in sorted key order, so no constraint goes unchecked.
//
// What is removed is exactly what the walk evaluates itself, and nothing
// more — a boundary that is wrong in both directions if it moves, and
// silently so in both. Removing a constraint the walk does not evaluate drops
// it silently; keeping one the walk does evaluate reports the same failure
// twice, once from each. So the caller states which families it evaluates
// at this position (see evaluatedByWalk) and the transform removes those,
// alongside the three child positions the walk always descends: the values
// under properties and patternProperties, and additionalProperties in its
// schema form.
//
// The distinction that matters most is between the two conditional
// families. The walk evaluates allOf, anyOf, oneOf, not and
// dependentSchemas through its own branch structures, so those are removed
// here unconditionally. if/then/else is removed only where the keyword
// index reports the walk applies it, which is everywhere except a node
// carrying a when: predicate — there the predicate governs and the
// applicator is the vestigial wrapper, whose gated constraints reach the
// instance through this copy and nowhere else, so removing it would drop
// the constraint outright.
//
// What is preserved, and why:
//
//   - The declared property names and pattern names, with permissive
//     values. A closed object (additionalProperties: false), a
//     minProperties bound and propertyNames all read the key set, so the
//     names must stay even though the values go.
//   - additionalProperties in its closed form, which is a statement about
//     which keys the object may carry rather than a constraint on any
//     child value.
//   - Any reference, exactly as authored.
//   - if/then/else where the walk does not evaluate the applicator — which
//     is the case for a node carrying a when: predicate, whose gated
//     constraints reach the instance through this copy and nowhere else.
//   - Every scalar keyword — type, enum, const, the numeric and string
//     bounds, format, dependentRequired — untouched.
//
// The array keywords, the key-string and key-count constraints, the
// conditional applicator, closedness and the leftover-value constraints are
// removed exactly when the keyword index says the walk evaluates them, and
// kept otherwise. The index is read rather than re-derived from the
// declaration for the same reason the families above are: a second reading
// of what the walk covers would agree on the day it was written and drift
// after, and drift in the removing direction drops a constraint silently.
//
// unevaluatedProperties and unevaluatedItems are removed unconditionally,
// and the two cases differ in what happens next. Where the index modelled
// them, the walk enforces them. Where it could not — the evaluated set
// spans an applicator this position cannot see — they are enforced by
// nothing, because their meaning is assembled from what the sibling and
// child positions matched and this copy no longer has those positions to
// read. That exclusion is stated in the package documentation's coverage
// contract rather than left for a reader to discover, and
// TestShippedSchemasCarryNoAnnotationDependentKeywords fails the build if a
// shipped schema starts relying on either.
func pruneOwnKeywords(s *jsonschema.Schema, walked evaluatedByWalk, modelled *positionKeywords) *jsonschema.Schema {
	if s == nil {
		return nil
	}

	// CloneSchemas copies every subschema position, so mutating the result
	// cannot reach the schema the caller still validates against.
	out := s.CloneSchemas()

	if walked.required {
		out.Required = nil
	}
	if walked.allOf {
		out.AllOf = nil
	}
	if walked.anyOf {
		out.AnyOf = nil
	}
	if walked.oneOf {
		out.OneOf = nil
	}
	if walked.not {
		out.Not = nil
	}
	if walked.dependentSchemas {
		out.DependentSchemas = nil
		out.DependencySchemas = nil
	}

	if out.Properties != nil {
		pruned := make(map[string]*jsonschema.Schema, len(out.Properties))
		for name := range out.Properties {
			pruned[name] = &jsonschema.Schema{}
		}
		out.Properties = pruned
	}
	if out.PatternProperties != nil {
		pruned := make(map[string]*jsonschema.Schema, len(out.PatternProperties))
		for pattern := range out.PatternProperties {
			pruned[pattern] = &jsonschema.Schema{}
		}
		out.PatternProperties = pruned
	}
	if out.AdditionalProperties != nil && !isClosedForm(out.AdditionalProperties) {
		out.AdditionalProperties = &jsonschema.Schema{}
	}

	pruneWalkedKeywords(out, modelled)

	out.UnevaluatedProperties = nil
	out.UnevaluatedItems = nil

	return out
}

// pruneWalkedKeywords removes from the reduced copy each keyword family the
// keyword index reports the walk evaluates at this position, so one
// violation is not reported once by the walk and once by the library.
//
// Reading the index directly is what keeps the two sides in agreement: the
// index is the single statement of what the walk covers, and both the walk
// and this transform consult it rather than each deciding for itself.
func pruneWalkedKeywords(out *jsonschema.Schema, modelled *positionKeywords) {
	if modelled == nil {
		return
	}
	if modelled.items != nil || len(modelled.prefixItems) > 0 {
		out.Items = nil
		out.PrefixItems = nil
	}
	if modelled.contains != nil {
		out.Contains = nil
		out.MinContains = nil
		out.MaxContains = nil
	}
	if modelled.propertyNames != nil {
		out.PropertyNames = nil
	}
	if modelled.minProperties != nil {
		out.MinProperties = nil
	}
	if modelled.maxProperties != nil {
		out.MaxProperties = nil
	}
	if modelled.conditional != nil {
		out.If = nil
		out.Then = nil
		out.Else = nil
	}
	// Closedness becomes the walk's, which reports each key that reached no
	// declared property at that key's own path, in key order. A library
	// collects the same keys by iterating the instance and names them together
	// at the enclosing object's path, in an order that varies — so neither the
	// position of the report nor its order survives.
	if modelled.closed {
		out.AdditionalProperties = nil
	}
}

// localDefsTarget resolves a reference naming one of the schema's own
// reusable definitions, returning the definition name and the schema it
// names. It reports false for any other reference shape — a pointer into
// a property, an anchor, or a reference into another document — so an
// unrecognised shape keeps whatever behaviour it had.
func localDefsTarget(ref string, defs map[string]*jsonschema.Schema) (string, *jsonschema.Schema, bool) {
	if ref == "" || defs == nil || !strings.HasPrefix(ref, localDefsPrefix) {
		return "", nil, false
	}
	name := strings.TrimPrefix(ref, localDefsPrefix)
	// A pointer reaching past the definition itself (#/$defs/x/properties/y)
	// is not a whole-definition reference and is left as authored.
	if name == "" || strings.Contains(name, "/") {
		return "", nil, false
	}
	target, ok := defs[name]
	if !ok || target == nil {
		return "", nil, false
	}
	return name, target, true
}

// isClosedForm reports whether an additionalProperties value is the
// closed-object form — written `additionalProperties: false` — rather
// than a schema constraining the undeclared values.
//
// The check mirrors the schema library's own: `false` in a schema
// position decodes to a negation of the everything-matches schema, and
// the library recognises the closed form by that shape. This is a
// deliberate second copy of an unexported predicate — agreement with the
// library on how `false` decodes is what makes the closed form survive
// pruning, and TestClosedFormMatchesLibraryDecoding pins the two together
// so a library change surfaces as a failing test rather than as silently
// dropped closedness.
//
// Reading it as false is the safe direction: a value not recognised as
// the closed form is replaced by a permissive schema, which under-reports
// rather than inventing a failure.
func isClosedForm(s *jsonschema.Schema) bool {
	return s != nil && s.Not != nil && reflect.ValueOf(*s.Not).IsZero()
}

// refOnly reports whether s carries a reference and no constraint of its
// own, so substituting the referenced schema for s loses nothing.
//
// The test is by exclusion: everything that is presentation or
// bookkeeping is cleared from a copy, and s qualifies only when what
// remains is empty. A keyword the exclusion list does not know about
// therefore keeps s from qualifying, which is the safe direction — the
// caller falls back to whatever it did before rather than dropping a
// constraint it failed to recognise.
func refOnly(s *jsonschema.Schema) bool {
	if s == nil || s.Ref == "" {
		return false
	}
	// The conditional extension keywords are evaluated from their own
	// pre-walked positions against this exact node, so a node carrying one
	// is not interchangeable with the schema it references.
	for key := range s.Extra {
		if key == "x-aggrega-when" || key == "x-aggrega-validate" {
			return false
		}
	}

	probe := *s
	probe.Ref = ""
	probe.ID = ""
	probe.Schema = ""
	probe.Comment = ""
	probe.Anchor = ""
	probe.DynamicAnchor = ""
	probe.Vocabulary = nil
	probe.Defs = nil
	probe.Definitions = nil
	probe.Title = ""
	probe.Description = ""
	probe.Default = nil
	probe.Deprecated = false
	probe.ReadOnly = false
	probe.WriteOnly = false
	probe.Examples = nil
	probe.Extra = nil
	probe.PropertyOrder = nil

	return reflect.ValueOf(probe).IsZero()
}

// substituteRefOnly returns the schema a reference-only node names, with
// the definitions in scope carried onto it so its own references keep
// resolving, or (nil, false) when s is not a reference-only node naming
// one of the schema's own definitions.
//
// The substitution is what lets a referencing position be walked instead
// of handed to the library whole. A reference is the shape of a leaf — it
// declares nothing itself — while what it names may declare a whole
// object, so reading the reference alone classifies a compound schema as
// a leaf and validates it in one call, which is the parent-level call
// that reports an arbitrary one of several equally-failing children.
func substituteRefOnly(s *jsonschema.Schema) (string, *jsonschema.Schema, bool) {
	if !refOnly(s) {
		return "", nil, false
	}
	name, target, ok := localDefsTarget(s.Ref, s.Defs)
	if !ok {
		return "", nil, false
	}
	substituted := target.CloneSchemas()
	merged := make(map[string]*jsonschema.Schema, len(s.Defs)+len(substituted.Defs))
	for key, value := range s.Defs {
		merged[key] = value.CloneSchemas()
	}
	for key, value := range substituted.Defs {
		merged[key] = value
	}
	substituted.Defs = merged
	return name, substituted, true
}

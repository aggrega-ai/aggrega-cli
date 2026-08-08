// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// ComponentLookup resolves a component name URI to the raw catalog Component.
// Used by ResolveComponentExtends for chain walking.
type ComponentLookup func(uri string) (types.Component, bool)

// FeatureLookup resolves a feature name URI to the raw catalog Feature.
type FeatureLookup func(uri string) (types.Feature, bool)

// ResolveComponentExtends returns a fully-merged Component after walking the
// extends chain. Labels/Annotations maps are deep-merged (child wins per key),
// the Inject list is concatenated and deduplicated, UsesSchema entries are
// merged (child entry overrides parent on the same socket name), and
// Assertions accumulate root-first with no per-name override. Returns the
// original component unchanged when Spec.Extends == "".
//
// Spec.Schema on the merged component is the chain root's node, copied down
// verbatim rather than composed — an extending Component authors no pillar
// content, so there is nothing to compose. See mergeComponent for why the copy
// is a node assignment rather than a one-element union.
//
// The merge can fail: an extending Component reusing an inherited assertion
// name to declare a different rule yields ErrAssertionNameCollision (a
// byte-identical restatement under the same name merges as a no-op — see
// mergeAssertions). Every collision along the chain is reported rather than only
// the first, so one rename pass clears the entity. The original component is
// returned unchanged alongside a non-empty error slice, matching the chain-walk
// failure paths.
func ResolveComponentExtends(comp types.Component, lookup ComponentLookup) (types.Component, []*ValidationError) {
	if comp.Spec.Extends == "" {
		return comp, nil
	}

	catalogLookup := func(uri string) (CatalogEntry, bool) {
		c, ok := lookup(uri)
		if !ok {
			return CatalogEntry{}, false
		}
		return CatalogEntry{Kind: envelope.KindComponent, URI: c.Metadata.Name, Extends: c.Spec.Extends}, true
	}
	entry := CatalogEntry{Kind: envelope.KindComponent, URI: comp.Metadata.Name, Extends: comp.Spec.Extends}

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
	var mergeErrs []*ValidationError
	for i := len(chain) - 2; i >= 0; i-- {
		var errs []*ValidationError
		merged, errs = mergeComponent(merged, chain[i])
		mergeErrs = append(mergeErrs, errs...)
	}
	if len(mergeErrs) > 0 {
		return comp, mergeErrs
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
		return CatalogEntry{Kind: envelope.KindFeature, URI: f.Metadata.Name, Extends: f.Spec.Extends}, true
	}
	entry := CatalogEntry{Kind: envelope.KindFeature, URI: feat.Metadata.Name, Extends: feat.Spec.Extends}

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

// ComponentAncestry returns the base Components comp descends from through
// extends, nearest parent first, resolved through lookup. The slice excludes
// comp itself, so a Component declaring no extends has an empty ancestry.
//
// It is a read-only view of the very chain walk the inheritance merge performs,
// exposed so a consumer that needs only "what does this descend from" answers
// the question with the merge's own safeguards instead of walking Spec.Extends
// again: a circular chain yields ErrCyclicExtension and an unresolvable parent
// yields ErrInheritanceMerge, in both cases rather than looping. A second walk
// could disagree with this one; there is no second walk.
func ComponentAncestry(comp types.Component, lookup ComponentLookup) ([]types.Component, []*ValidationError) {
	chain, errs := collectComponentChain(comp, lookup)
	if len(errs) > 0 {
		return nil, errs
	}
	return chain[1:], nil
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
			return nil, []*ValidationError{NewValidationError(envelope.KindComponent, child.Metadata.Name, ValidationDetail{
				FieldPath:  ".spec.extends",
				Constraint: "circular extends detected while rebuilding chain",
				Err:        ErrCyclicExtension(),
			})}
		}
		parent, ok := lookup(current.Spec.Extends)
		if !ok {
			return nil, []*ValidationError{NewValidationError(envelope.KindComponent, child.Metadata.Name, ValidationDetail{
				FieldPath:  ".spec.extends",
				Constraint: "parent component not found while rebuilding chain",
				Err:        ErrInheritanceMerge(),
			})}
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
			return nil, []*ValidationError{NewValidationError(envelope.KindFeature, child.Metadata.Name, ValidationDetail{
				FieldPath:  ".spec.extends",
				Constraint: "circular extends detected while rebuilding chain",
				Err:        ErrCyclicExtension(),
			})}
		}
		parent, ok := lookup(current.Spec.Extends)
		if !ok {
			return nil, []*ValidationError{NewValidationError(envelope.KindFeature, child.Metadata.Name, ValidationDetail{
				FieldPath:  ".spec.extends",
				Constraint: "parent feature not found while rebuilding chain",
				Err:        ErrInheritanceMerge(),
			})}
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
//
// Spec.Schema is the exception: it is not merged but replaced, taking the
// parent's node exactly as the parent authored it. An extending Component
// authors no pillar content — its own Spec.Schema carries the inheritance
// marker — so there is nothing of the child's to compose, and the assignment
// is a plain node copy rather than a run through the union primitive. The
// distinction is observable: the union primitive tags every node it emits with
// its origin, so a one-element run through it would leave provenance keys on
// what the language calls a verbatim inheritance. A chain therefore carries the
// root's schema at every level, and the authored schema of a resolved Component
// is the same object every enforcement path reads.
//
// Spec.Assertions is the one surface whose merge can fail — see mergeAssertions
// for why a shared name is refused rather than overridden. The returned slice is
// empty on success.
func mergeComponent(parent, child types.Component) (types.Component, []*ValidationError) {
	out := child
	out.Metadata.Labels = mergeStringMap(parent.Metadata.Labels, child.Metadata.Labels)
	out.Metadata.Annotations = mergeStringMap(parent.Metadata.Annotations, child.Metadata.Annotations)

	out.Spec.Schema = parent.Spec.Schema
	out.Spec.FeaturesSchema = mergeFeaturesSchema(parent.Spec.FeaturesSchema, child.Spec.FeaturesSchema)
	out.Spec.Inject = concatDedupeString(parent.Spec.Inject, child.Spec.Inject)
	out.Spec.UsesSchema = mergeUsesSchema(parent.Spec.UsesSchema, child.Spec.UsesSchema)

	assertions, errs := mergeAssertions(parent.Metadata.Name, child.Metadata.Name,
		parent.Spec.Assertions, child.Spec.Assertions)
	out.Spec.Assertions = assertions
	return out, errs
}

func mergeFeature(parent, child types.Feature) types.Feature {
	out := child
	out.Metadata.Labels = mergeStringMap(parent.Metadata.Labels, child.Metadata.Labels)
	out.Metadata.Annotations = mergeStringMap(parent.Metadata.Annotations, child.Metadata.Annotations)

	out.Spec.FeaturesSchema = mergeFeaturesSchema(parent.Spec.FeaturesSchema, child.Spec.FeaturesSchema)
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

// mergeAssertions accumulates the parent's named assertions and the child's,
// parent entries first in author order, and refuses a name the child reuses to
// declare a different rule.
//
// Two rules, two functions — deliberately not folded into mergeStringMap. That
// helper serves labels and annotations, where the child's value replacing the
// parent's on a shared key is the correct semantic. Here it would be wrong: an
// extending Component's assertions ADD to the ones it inherits and never replace
// one, so per-name override would silently drop a base's architectural invariant
// at exactly the moment a wrapper is introduced. The alternative to refusing —
// keeping both rules under disambiguated names — is worse, because the assertion
// name is a stable rule identifier the failure diagnostic reports, and renaming
// one behind the author's back destroys the one property that identifier has. A
// refusal costs the author one rename. Anyone unifying this with mergeStringMap
// re-admits the silent drop.
//
// A shared name carrying a byte-identical body is a no-op restatement, not a
// replacement — the rule that survives is the same rule — so it merges without
// complaint, and only a differing body halts. That distinction is also what
// makes this merge idempotent, which is a requirement and not a nicety: the
// chain walk resolves each entity independently and can therefore reach an
// ancestor that has already had its own chain folded in, so it re-applies an
// ancestor's entries to a descendant that already carries them. Every other
// field's merge is idempotent by shape (replace, child-wins, concat-dedupe);
// this one has to be idempotent on purpose. Refusing on the bare name would
// turn a three-deep chain into a self-collision against its own middle tier.
//
// Order is the contract, not incidental: evaluation walks the merged map in
// order and the first failing assertion halts, so a base's invariant reports
// before a descendant's. Along a chain the fold accumulates root-first.
func mergeAssertions(parentEntity, childEntity string, parent, child types.OrderedMap[string]) (types.OrderedMap[string], []*ValidationError) {
	out := types.OrderedMap[string]{}
	for k, v := range parent.All() {
		out.Set(k, v)
	}
	var errs []*ValidationError
	for k, v := range child.All() {
		if taken, exists := out.Get(k); exists {
			if taken == v {
				continue
			}
			errs = append(errs, NewValidationError(envelope.KindComponent, childEntity, ValidationDetail{
				FieldPath:  ".spec.assertions." + k,
				Constraint: fmt.Sprintf("assertion %q is already present on the base Component %q, declaring a different rule", k, parentEntity),
				Suggestion: "Rename this assertion; an extending Component's assertions add to the base's and never replace one.",
				Err:        ErrAssertionNameCollision(),
			}))
			continue
		}
		out.Set(k, v)
	}
	return out, errs
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

// mergeFeaturesSchema overlays parent and child featuresSchema entries. Child
// wins on the same slot name; slots unique to the parent are preserved.
// Same semantics as mergeUsesSchema.
func mergeFeaturesSchema(parent, child types.OrderedMap[types.AttachmentSlot]) types.OrderedMap[types.AttachmentSlot] {
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

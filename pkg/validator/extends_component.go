// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// ComponentLookup resolves a component name URI to the raw catalog Component.
// Used by ResolveComponentExtends for chain walking.
type ComponentLookup func(uri string) (types.Component, bool)

// FeatureLookup resolves a feature name URI to the raw catalog Feature.
type FeatureLookup func(uri string) (types.Feature, bool)

// ResolveComponentExtends returns a fully-merged Component after walking the
// extends chain. Labels and Annotations merge per key, base entries first and
// the child winning a shared key; every other Metadata field is the child's
// verbatim, so identity and provenance never inherit. The Inject list is
// concatenated and deduplicated, UsesSchema entries are
// merged (child entry overrides parent on the same socket name), and
// Assertions accumulate root-first with no per-name override. Returns the
// original component unchanged when Spec.Extends == "".
//
// Spec.Schema on the merged component is the chain root's node, copied down
// verbatim rather than composed — an extending Component authors no pillar
// content, so there is nothing to compose. See mergeComponent for why the copy
// is a node assignment rather than a one-element union.
//
// The resolution can fail: an extending Component reusing an inherited assertion
// name to declare a different rule yields ErrAssertionNameCollision (a
// byte-identical restatement under the same name is the same rule and passes —
// see validateChainAssertionNames). Every colliding name along the chain is
// reported rather than only the first, so one rename pass clears the entity. The
// original component is returned unchanged alongside a non-empty error slice,
// matching the chain-walk failure paths.
func ResolveComponentExtends(comp types.Component, lookup ComponentLookup) (types.Component, []*ValidationError) {
	if comp.Spec.Extends == "" {
		return comp, nil
	}

	// Walk the chain child → root, collect *types.Component per ancestor, then
	// fold-merge from root into child so child fields take precedence. The walk
	// gates the chain as it builds it — one traversal, not a gate pass followed
	// by a collection pass over the same edges.
	chain, chainErrs := collectComponentChain(comp, lookup)
	if len(chainErrs) > 0 {
		return comp, chainErrs
	}

	// The assertion-name rule is checked over the whole chain before the fold
	// rather than at each merge step, because the rule is about a name the chain
	// carries two rules under: a per-step check reports one such name once per
	// step it survives, and the author has one rename to make either way.
	if errs := validateChainAssertionNames(comp, chain); len(errs) > 0 {
		return comp, errs
	}

	merged := chain[len(chain)-1] // root ancestor
	for i := len(chain) - 2; i >= 0; i-- {
		merged = mergeComponent(merged, chain[i])
	}
	return merged, nil
}

// validateChainAssertionNames enforces the assertion-names rule: one assertion
// name carries one rule across the chain an entity extends.
//
// The rule itself is declared rather than written out here: see
// entityAssertionNamesCarryOneRule, which names the fact it reads and is invoked
// once per distinct assertion name the chain declares. What this function does is
// present the chain the resolve is holding and render whatever came back into the
// failure shape this package's callers read.
//
// The presented document has no file of its own, because the chain is assembled
// from catalog entries reached by reference rather than from one file a caller
// read. Every finding is positioned at the authored site inside the document
// instead, which is what the rule's subject names and what the failure's position
// has always carried for this rule.
func validateChainAssertionNames(comp types.Component, chain []types.Component) []*ValidationError {
	// The presentation is held as the view the rule declared rather than as its
	// own concrete type, because the entry takes the check and the view together
	// and infers one type from both. Handing over the concrete type would infer
	// that type and then refuse the rule, which declares the view.
	presented := newComponentDocumentView("", chain)
	var view constraint.DocumentView = presented
	return shapeRuleErrorsFrom(
		constraint.RunOnDocumentAssertions(
			presented.path,
			presented.assertionNames(),
			view,
			entityAssertionNamesCarryOneRule,
		),
		ruleSubject{
			Kind:    envelope.KindComponent,
			Name:    comp.Metadata.Name,
			Version: comp.Metadata.Version,
			Rule:    "assertions",
		},
	)
}

// ResolveFeatureExtends is the Feature analogue of ResolveComponentExtends.
func ResolveFeatureExtends(feat types.Feature, lookup FeatureLookup) (types.Feature, []*ValidationError) {
	if feat.Spec.Extends == "" {
		return feat, nil
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
// It is a read-only view of the very chain walk the inheritance merge performs
// — it calls collectComponentChain, the same collector ResolveComponentExtends
// folds its merge over — exposed so a consumer that needs only "what does this
// descend from" answers the question from the merge's own chain instead of
// walking Spec.Extends again. It reports the same failures that collector does:
// a circular chain yields ErrCyclicExtension, a chain past the depth bound
// yields ErrExtendsChainTooDeep, and an unresolvable parent yields
// ErrInheritanceMerge, in every case rather than looping.
//
// Sharing the collector is what makes the answer agree with the merge, and the
// agreement is total: the collector both gates the chain and builds it, so a
// chain this function reports without complaint is exactly a chain
// ResolveComponentExtends accepts.
func ComponentAncestry(comp types.Component, lookup ComponentLookup) ([]types.Component, []*ValidationError) {
	chain, errs := collectComponentChain(comp, lookup)
	if len(errs) > 0 {
		return nil, errs
	}
	return chain[1:], nil
}

// collectComponentChain returns the chain [child, parent, grandparent, ...],
// gated by the shared extends walk: a chain that returns to an entity already
// in it, one that runs past the depth bound, and a parent the catalog does not
// hold are each reported there, in the words every other entity path reports
// them in.
func collectComponentChain(child types.Component, lookup ComponentLookup) ([]types.Component, []*ValidationError) {
	chain, fault := walkExtendsChain(child,
		func(c types.Component) string { return c.Metadata.Name },
		func(c types.Component) string { return c.Spec.Extends },
		lookup)
	if fault != nil {
		return nil, []*ValidationError{chainFaultError(envelope.KindComponent, child.Metadata.Name, fault)}
	}
	return chain, nil
}

// collectFeatureChain is the Feature analogue of collectComponentChain, over
// the same shared walk.
func collectFeatureChain(child types.Feature, lookup FeatureLookup) ([]types.Feature, []*ValidationError) {
	chain, fault := walkExtendsChain(child,
		func(f types.Feature) string { return f.Metadata.Name },
		func(f types.Feature) string { return f.Spec.Extends },
		lookup)
	if fault != nil {
		return nil, []*ValidationError{chainFaultError(envelope.KindFeature, child.Metadata.Name, fault)}
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
// Spec.Assertions is the one surface with a rule of its own governing what a
// child may declare — see mergeAssertions for why a shared name accumulates
// rather than overriding, and entityAssertionNamesCarryOneRule for the rule that
// refuses a shared name carrying two different bodies. The merge itself cannot
// fail: it runs after that rule has passed.
func mergeComponent(parent, child types.Component) types.Component {
	out := child
	out.Metadata.Labels = mergeStringMap(parent.Metadata.Labels, child.Metadata.Labels)
	out.Metadata.Annotations = mergeStringMap(parent.Metadata.Annotations, child.Metadata.Annotations)

	out.Spec.Schema = parent.Spec.Schema
	out.Spec.FeaturesSchema = mergeFeaturesSchema(parent.Spec.FeaturesSchema, child.Spec.FeaturesSchema)
	out.Spec.Inject = concatDedupeString(parent.Spec.Inject, child.Spec.Inject)
	out.Spec.UsesSchema = mergeUsesSchema(parent.Spec.UsesSchema, child.Spec.UsesSchema)

	out.Spec.Assertions = mergeAssertions(parent.Spec.Assertions, child.Spec.Assertions)
	return out
}

// mergeFeature overlays the child Feature onto the parent. The result keeps the
// child's identity (Name, Version, Extends) but carries merged Metadata and
// Spec. Labels and Annotations merge per key with the child winning a shared
// key; every other Metadata field is the child's verbatim, so identity and
// provenance never inherit. FeaturesSchema and UsesSchema merge per entry name.
//
// The attachment surface — AppliesTo, AppliesToAnyOf and Interoperability —
// inherits whole-field: an extending Feature that declares one keeps its own,
// and one that declares none attaches exactly where its base attaches. The two
// selector forms inherit independently because they compose rather than
// replace: each form that is present must hold — the AppliesTo string must
// match, and at least one AppliesToAnyOf item must match — while a form left
// absent constrains nothing. So a Feature may take its base's AND-selector
// while declaring its own OR-set. A declared selector replaces the base's outright rather than
// merging the requirements inside it — a selector is one expression, and
// intersecting two of them would produce a constraint neither author wrote.
//
// Spec.Schema is absent here on purpose: a Feature's pillar content composes
// under JSON Schema composition during the inheritance merge phase, which
// stores the composed result separately from the entity struct. Assigning a
// parent's authored node here would overwrite the child's own pillars with the
// base's and shadow that composition.
func mergeFeature(parent, child types.Feature) types.Feature {
	out := child
	out.Metadata.Labels = mergeStringMap(parent.Metadata.Labels, child.Metadata.Labels)
	out.Metadata.Annotations = mergeStringMap(parent.Metadata.Annotations, child.Metadata.Annotations)

	out.Spec.FeaturesSchema = mergeFeaturesSchema(parent.Spec.FeaturesSchema, child.Spec.FeaturesSchema)
	out.Spec.UsesSchema = mergeUsesSchema(parent.Spec.UsesSchema, child.Spec.UsesSchema)

	if child.Spec.AppliesTo == "" {
		out.Spec.AppliesTo = parent.Spec.AppliesTo
	}
	if len(child.Spec.AppliesToAnyOf) == 0 {
		out.Spec.AppliesToAnyOf = parent.Spec.AppliesToAnyOf
	}
	out.Spec.Interoperability = mergeInteroperability(parent.Spec.Interoperability, child.Spec.Interoperability)
	return out
}

// mergeInteroperability overlays the child's interoperability controls onto the
// parent's one field at a time: a field the child declares wins, and a field it
// leaves unset keeps the base's value.
//
// Per-field rather than whole-block, because the two controls are independent
// and a Feature routinely needs only one of them. A Feature that specialises
// where its base's artifacts land, by declaring componentRefPath alone, still
// carries the incompatibilities its base declared — replacing the block
// wholesale would drop them without a word, which is exactly the failure the
// conflictsWith list exists to prevent. The child's conflictsWith list, when it
// declares one, replaces the base's rather than extending it: naming its own
// incompatibilities is a deliberate statement about what this Feature may sit
// beside, not an omission.
//
// The result is always a fresh value, so a merge never writes through a pointer
// a catalog entry still holds.
func mergeInteroperability(parent, child *types.Interoperability) *types.Interoperability {
	if parent == nil && child == nil {
		return nil
	}
	var merged types.Interoperability
	if parent != nil {
		merged = *parent
	}
	if child != nil {
		if len(child.ConflictsWith) > 0 {
			merged.ConflictsWith = child.ConflictsWith
		}
		if child.ComponentRefPath != "" {
			merged.ComponentRefPath = child.ComponentRefPath
		}
	}
	return &merged
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
// parent entries first in author order, keeping the entry already standing under
// a name the child reuses.
//
// Two rules, two functions — deliberately not folded into mergeStringMap. That
// helper serves labels and annotations, where the child's value replacing the
// parent's on a shared key is the correct semantic. Here it would be wrong: an
// extending Component's assertions ADD to the ones it inherits and never replace
// one, so per-name override would silently drop a base's architectural invariant
// at exactly the moment a wrapper is introduced. Anyone unifying this with
// mergeStringMap re-admits the silent drop.
//
// Whether a shared name is legitimate at all is not decided here. A name carrying
// two different rules is refused by entityAssertionNamesCarryOneRule, which runs
// over the whole chain before the fold begins — so by the time this runs, a
// shared name carries one rule and keeping the standing entry is keeping that
// rule. Alternatives to refusing were weighed there; the assertion name is a
// stable rule identifier the failure diagnostic reports, and disambiguating one
// behind the author's back destroys the one property that identifier has.
//
// Keeping the standing entry is also what makes this merge idempotent, which is a
// requirement and not a nicety: the chain walk resolves each entity independently
// and can therefore reach an ancestor that has already had its own chain folded
// in, so it re-applies an ancestor's entries to a descendant that already carries
// them. Every other field's merge is idempotent by shape (replace, child-wins,
// concat-dedupe); this one has to be idempotent on purpose.
//
// Order is the contract, not incidental: evaluation walks the merged map in
// order and the first failing assertion halts, so a base's invariant reports
// before a descendant's. Along a chain the fold accumulates root-first.
func mergeAssertions(parent, child types.OrderedMap[string]) types.OrderedMap[string] {
	out := types.OrderedMap[string]{}
	for k, v := range parent.All() {
		out.Set(k, v)
	}
	for k, v := range child.All() {
		if _, exists := out.Get(k); exists {
			continue
		}
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

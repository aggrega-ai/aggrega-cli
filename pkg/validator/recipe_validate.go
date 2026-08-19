// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// ValidateRecipe enforces the Recipe shape rules at load time. Called from
// catalog loading immediately after yaml.Unmarshal succeeds, before the
// Recipe is admitted to the catalog map. Returns a *ValidationError against
// the Recipe kind (or nil on pass).
//
// Rules enforced here: values-mapping and input-kinds, plus the eval-provider
// constraint. The workload-requires-provider-workloadSchema constraint is
// enforced at render time by the central input-kind gate (a synthesised Recipe
// bound to a provider that does not implement workload synthesis halts loudly
// there). The filename-collision rule (a collision across input-kind origins)
// is enforced at emission time by feature rendering.
//
// The eval-provider carve-out is two-sided:
//
//  1. Reject non-empty source / workload / artifacts (eval has no rendering
//     surface).
//  2. Exempt eval Recipes from the input-kinds rule (a Recipe declaring only
//     `provides:` is legitimate eval-mode authoring).
//
// What is deliberately NOT checked: the contents of `source:`. Whether a source
// is present is a shape question and is answered here; what its keys mean is
// not. A structured `source:` is an open dictionary, and the keys inside it —
// `registry`, `chart`, `appVersion`, `workspace`, or anything else a provider
// defines — are carried through uninterpreted, exposed to the Recipe's templates
// as `.source.*`, and forwarded verbatim to the provider engine that renders the
// Recipe. The core is blind to them by construction: it holds no list of
// provider-specific field names to check against, and the block is the
// provider's contract to define and to check. Adding a key check here would move
// that contract into the core and make the core opinionated about execution
// layers it is meant to know nothing about.
func ValidateRecipe(r *types.Recipe, sourcePath string) error {
	if r == nil {
		return nil
	}

	// The output-label rules are engine-agnostic and run before the eval
	// carve-out: an eval Recipe declares no rendering surface but must still not
	// claim a reserved provenance key, whether it names one literally or
	// promotes it from a component label.
	if err := validateRecipeOutputLabels(r, sourcePath); err != nil {
		return err
	}
	if err := validateRecipeOutputComponentLabels(r, sourcePath); err != nil {
		return err
	}

	if r.Spec.Capability == "eval" {
		return validateRecipeEvalProvider(r, sourcePath)
	}

	if err := validateRecipeRule1(r, sourcePath); err != nil {
		return err
	}
	if err := validateRecipeRule5(r, sourcePath); err != nil {
		return err
	}
	return nil
}

// validateRecipeEvalProvider enforces the eval-provider constraint: input-kind
// fields must be empty. The input-kinds rule is intentionally skipped here —
// an eval Recipe declaring zero input kinds is legitimate (it provides values
// without rendering anything).
func validateRecipeEvalProvider(r *types.Recipe, sourcePath string) error {
	var declared []string
	if !sourceIsEmpty(r.Spec.Source) {
		declared = append(declared, "source")
	}
	if !r.Spec.Workload.IsEmpty() {
		declared = append(declared, "workload")
	}
	if r.Spec.Artifacts.Len() > 0 {
		declared = append(declared, "artifacts")
	}
	if !r.Spec.ValuesMapping.IsEmpty() {
		declared = append(declared, "valuesMapping")
	}

	if len(declared) == 0 {
		return nil
	}
	return NewShapeRuleError(ShapeRuleViolation{
		Kind:       envelope.KindRecipe,
		Name:       r.Metadata.Name,
		Version:    r.Metadata.Version,
		Path:       sourcePath,
		Rule:       "eval",
		Suggestion: fmt.Sprintf("forbidden fields declared: %s", strings.Join(declared, ", ")),
		Err:        ErrRecipeEvalProviderConstraint(),
	})
}

// validateRecipeOutputLabels rejects any spec.outputLabels key under a
// platform-reserved namespace (aggrega.ai/ or app.kubernetes.io/). Those
// prefixes are the compiler-owned provenance keyspace; a recipe stamping them
// would spoof provenance, and reserving them keeps a validated output-label set
// disjoint from the fixed provenance set so the render-stage merge is
// collision-free. Non-reserved keys and their values are unpoliced — the core
// does not validate label charset (a downstream runtime rejects an invalid
// label). Keys are checked in sorted order so the first-reported collision is
// deterministic.
func validateRecipeOutputLabels(r *types.Recipe, sourcePath string) error {
	if len(r.Spec.OutputLabels) == 0 {
		return nil
	}
	keys := make([]string, 0, len(r.Spec.OutputLabels))
	for k := range r.Spec.OutputLabels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if isReservedLabelKey(key) {
			return NewShapeRuleError(ShapeRuleViolation{
				Kind:    envelope.KindRecipe,
				Name:    r.Metadata.Name,
				Version: r.Metadata.Version,
				Path:    sourcePath,
				Rule:    "output-labels",
				// The block is named in the key the author typed, because one
				// condition covers both label blocks and its registered
				// sentence therefore cannot say which of the two was authored.
				Suggestion: fmt.Sprintf(
					"key %q in outputLabels uses a reserved namespace (%q and %q are reserved for platform provenance)",
					key, types.ReservedLabelPrefixAggregaAI, types.ReservedLabelPrefixK8sRecommended),
				Err: ErrRecipeOutputLabelReservedNamespace(),
			})
		}
	}
	return nil
}

// isReservedLabelKey reports whether key sits in a platform-reserved label
// namespace. It is the single home for the plain-prefix test, so the literal
// and the promoted output-label rules cannot drift apart on what "reserved"
// means.
//
// The test is the bare root only. A key that merely contains a reserved root
// deeper in its own namespace — component.aggrega.ai/hosting, say — is a
// distinct keyspace the platform does not own, and stays authorable.
func isReservedLabelKey(key string) bool {
	return strings.HasPrefix(key, types.ReservedLabelPrefixAggregaAI) ||
		strings.HasPrefix(key, types.ReservedLabelPrefixK8sRecommended)
}

// validateRecipeOutputComponentLabels enforces the two load-time rules on
// spec.outputComponentLabels, the block naming component label keys whose
// values are promoted onto rendered output.
//
// A reserved-namespace key is rejected on the same grounds as a literal
// outputLabels key: the platform-owned provenance keyspace stays disjoint from
// anything a recipe stamps. A key declared in both this block and outputLabels
// is rejected because the two would stamp one key from two sources.
//
// The reserved rule is applied across the whole block before the collision
// rule, so a recipe violating both always reports the reserved namespace —
// the more fundamental fault, and the one whose fix does not depend on the
// other block's contents. Entries are checked in author order, which is
// already deterministic for a list and reports the offending entry the author
// wrote first; unlike the literal block, there is no map iteration to order.
func validateRecipeOutputComponentLabels(r *types.Recipe, sourcePath string) error {
	for _, key := range r.Spec.OutputComponentLabels {
		if isReservedLabelKey(key) {
			return NewShapeRuleError(ShapeRuleViolation{
				Kind:    envelope.KindRecipe,
				Name:    r.Metadata.Name,
				Version: r.Metadata.Version,
				Path:    sourcePath,
				Rule:    "output-component-labels",
				// The block is named in the key the author typed, for the
				// reason the literal block's site records.
				Suggestion: fmt.Sprintf(
					"key %q in outputComponentLabels uses a reserved namespace (%q and %q are reserved for platform provenance)",
					key, types.ReservedLabelPrefixAggregaAI, types.ReservedLabelPrefixK8sRecommended),
				Err: ErrRecipeOutputLabelReservedNamespace(),
			})
		}
	}
	for _, key := range r.Spec.OutputComponentLabels {
		if _, literal := r.Spec.OutputLabels[key]; literal {
			return NewShapeRuleError(ShapeRuleViolation{
				Kind:    envelope.KindRecipe,
				Name:    r.Metadata.Name,
				Version: r.Metadata.Version,
				Path:    sourcePath,
				Rule:    "output-component-labels",
				Suggestion: fmt.Sprintf(
					"key %q is promoted through outputComponentLabels and also declared literally in outputLabels; keep whichever source the label should come from and drop the other",
					key),
				Err: ErrRecipeOutputComponentLabelCollision(),
			})
		}
	}
	return nil
}

// validateRecipeRule1 enforces the values-mapping rule: valuesMapping requires
// source.
//
// Sourced-mode authoring presents a Source pointer alongside a values overlay.
// A Recipe with valuesMapping but no Source has no chart/module to overlay
// values onto.
func validateRecipeRule1(r *types.Recipe, sourcePath string) error {
	if r.Spec.ValuesMapping.IsEmpty() {
		return nil
	}
	if !sourceIsEmpty(r.Spec.Source) {
		return nil
	}
	return NewShapeRuleError(ShapeRuleViolation{
		Kind:       envelope.KindRecipe,
		Name:       r.Metadata.Name,
		Version:    r.Metadata.Version,
		Path:       sourcePath,
		Rule:       "values-mapping",
		Suggestion: "declare a source: pointer alongside valuesMapping (or remove the valuesMapping if no overlay is intended)",
		Err:        ErrRecipeRule1(),
	})
}

// validateRecipeRule5 enforces the input-kinds rule: at least one input kind
// must be present.
//
// The three input kinds (any one suffices):
//
//  1. Sourced — source + valuesMapping (and ValuesMapping has its own paired rules above).
//  2. Synthesized — workload (inline OR $import: file).
//  3. Artifacts — at least one entry under artifacts.
//
// A Recipe with none of these declared has no rendering surface.
func validateRecipeRule5(r *types.Recipe, sourcePath string) error {
	if !sourceIsEmpty(r.Spec.Source) {
		return nil
	}
	if !r.Spec.Workload.IsEmpty() {
		return nil
	}
	if r.Spec.Artifacts.Len() > 0 {
		return nil
	}
	return NewShapeRuleError(ShapeRuleViolation{
		Kind:       envelope.KindRecipe,
		Name:       r.Metadata.Name,
		Version:    r.Metadata.Version,
		Path:       sourcePath,
		Rule:       "input-kinds",
		Suggestion: "declare one of: source (with valuesMapping), workload, artifacts",
		Err:        ErrRecipeRule5(),
	})
}

// sourceIsEmpty reports whether the Recipe's Spec.Source field carries no
// value. Source is any and can be a string URI, a structured object,
// or nil; any non-nil non-empty value counts as a declared source.
func sourceIsEmpty(src any) bool {
	if src == nil {
		return true
	}
	if s, ok := src.(string); ok && s == "" {
		return true
	}
	return false
}

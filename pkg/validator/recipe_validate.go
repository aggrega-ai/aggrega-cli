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
// Rules enforced here: 1, 5 (chapter 16), plus the eval-provider constraint.
// The workload-requires-provider-workloadSchema constraint is enforced at
// render time by the central input-kind gate (a synthesised Recipe bound to a
// provider that does not implement workload synthesis halts loudly there).
// Rule 7 (cross-input-kind filename collision) is enforced at emission time by
// feature rendering.
//
// The eval-provider carve-out is two-sided:
//
//  1. Reject non-empty source / workload / artifacts (eval has no rendering
//     surface).
//  2. Exempt eval Recipes from rule 5 (a Recipe declaring only `provides:`
//     is legitimate eval-mode authoring).
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

	// The reserved-namespace rule is engine-agnostic and runs before the eval
	// carve-out: an eval Recipe declares no rendering surface but must still not
	// claim a reserved provenance key.
	if err := validateRecipeOutputLabels(r, sourcePath); err != nil {
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
// fields must be empty. Rule 5 is intentionally skipped here — an eval Recipe
// declaring zero input kinds is legitimate (it provides values without
// rendering anything).
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
		Constraint: "the eval provider executes formatting in-memory and forbids input-kind fields",
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
		if strings.HasPrefix(key, types.ReservedLabelPrefixAggregaAI) ||
			strings.HasPrefix(key, types.ReservedLabelPrefixK8sRecommended) {
			return NewShapeRuleError(ShapeRuleViolation{
				Kind:       envelope.KindRecipe,
				Name:       r.Metadata.Name,
				Version:    r.Metadata.Version,
				Path:       sourcePath,
				Rule:       "output-labels",
				Constraint: "outputLabels may not use a reserved namespace",
				Suggestion: fmt.Sprintf(
					"key %q uses a reserved namespace (%q and %q are reserved for platform provenance)",
					key, types.ReservedLabelPrefixAggregaAI, types.ReservedLabelPrefixK8sRecommended),
				Err: ErrRecipeOutputLabelReservedNamespace(),
			})
		}
	}
	return nil
}

// validateRecipeRule1: valuesMapping requires source.
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
		Rule:       "1",
		Constraint: "valuesMapping requires source",
		Suggestion: "declare a source: pointer alongside valuesMapping (or remove the valuesMapping if no overlay is intended)",
		Err:        ErrRecipeRule1(),
	})
}

// validateRecipeRule5: at least one input kind must be present.
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
		Rule:       "5",
		Constraint: "at least one input kind must be present",
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

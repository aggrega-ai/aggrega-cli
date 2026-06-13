// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ErrInjectEntryAmbiguous reports a verbose-form inject entry that
// simultaneously carries `value:` AND `from:` on the same element. The
// schema's inject_delivery definition rejects this case at envelope load
// (oneOf: [required:[from], required:[value]]) — entries reaching IR build
// in this state arrived via a code path that bypassed schema validation
// (in-memory InjectBlock construction in tests, programmatic catalog
// builders, or a hypothetical future bug in the JSON Schema validator).
// The sentinel is the second-line-of-defense halt that surfaces those
// paths with a specific error rather than silently picking one field over
// the other.
//
// Per the Aggrega architectural invariant set (Invariant 4): policy
// violations produce Loud Guardrails — the build halts with a specific
// error, never silent mutations.
var ErrInjectEntryAmbiguous = errors.New("inject entry carries both `value:` and `from:`; schema requires exactly one")

// ValidateInjectBlock walks a single InjectBlock's mechanisms (env, file,
// arg, and each Additional[...] entry) and reports any verbose-form entry
// that violates the schema's mutual-exclusivity invariant. The validator
// is **defense-in-depth, not primary enforcement** — the JSON Schema
// validator at envelope load is the primary check; this validator catches
// schema-bypass paths.
//
// Returns nil for compact-form (map-shaped) mechanism payloads — compact
// form cannot structurally express the both-present case. Returns nil for
// non-recognized shapes (the existing decode arms skip them, so validation
// stays silent for shapes that never decode). Returns a wrapped
// ErrInjectEntryAmbiguous only for verbose-form entries ([]any) where a
// single element carries both `value:` AND `from:`.
//
// pathPrefix anchors the error's field path. Callers pass an entity-level
// prefix (e.g., `.spec.components["api"].inject`) and the validator
// appends `.<mechanism>[<index>]` per violation.
//
// Out-of-scope conditions (intentionally not flagged by this validator):
//
//   - Entries lacking `in:` or carrying a non-string `in:` — the helper
//     [types.DecodeVerboseInjectEntry] returns ok=false and the decode arms
//     skip silently. Surfacing those as errors here is a separate concern
//     that may land at a future hardening pass.
//   - Compact-form (map-shaped) payloads — the JSON Schema validator
//     rejects these at envelope load now that inject_delivery accepts only
//     the verbose array form. Duplicating the check here would carry no
//     additional defensive value.
func ValidateInjectBlock(b *types.InjectBlock, pathPrefix string) error {
	if b == nil {
		return nil
	}
	var violations []string
	check := func(mech string, raw any) {
		arr, ok := raw.([]any)
		if !ok {
			return
		}
		for i, entry := range arr {
			_, _, _, hasValue, hasFrom, decodeOk := types.DecodeVerboseInjectEntry(entry)
			if !decodeOk {
				continue
			}
			if hasValue && hasFrom {
				violations = append(violations,
					fmt.Sprintf("%s.%s[%d]", pathPrefix, mech, i))
			}
		}
	}
	check("env", b.Env)
	check("file", b.File)
	check("arg", b.Arg)
	for mech, raw := range b.Additional.All() {
		check(mech, raw)
	}
	if len(violations) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInjectEntryAmbiguous, strings.Join(violations, "; "))
}

// ValidateInjectBlocks walks every InjectBlock surface in the resolved
// Aggregate and Release source-of-truth structures and aggregates errors
// across all findings. The walker is the IR-build-time entry point for
// the inject-block second-line-of-defense check; it runs once before
// per-edge / per-node consumer-site work begins and halts the build via
// the existing IR-build error-return path if any violation surfaces.
//
// Aggregate-side surfaces walked:
//
//   - Aggregate.Spec.Components[*].Inject (component self-inject)
//   - Aggregate.Spec.Components[*].Features[*].Inject (feature attachment)
//   - Aggregate.Spec.Components[*].Secrets[*].Inject (secret attachment)
//   - Aggregate.Spec.Components[*].Uses[*].Verbose[*].Inject (uses[].inject)
//   - Aggregate.Spec.Subsystems[*].{Components[*].(same four), Features[*].Inject,
//     Subsystems[*]...} recursively
//
// Release-side surfaces walked: the resolved Release.Spec.Boundaries tree
// is structurally walked for symmetry with the operator's stated scope.
// As of the current schema, no Release-side struct type carries a
// top-level *types.InjectBlock — BoundaryFeature has
// Type/Description/Infra/Uses (uses carries its own inject inline via
// the verbose UsesSocketValue form, not a struct-level InjectBlock);
// Boundary.Features is OrderedMap[BoundaryFeature]; Boundary.Secrets is
// OrderedMap[any]. The walk is therefore a structural no-op today; if a
// future schema revision adds an InjectBlock-bearing field to any Release
// surface, the walker is the extension point.
//
// Errors are aggregated across all walks with semicolon-separated field
// paths; the operator sees every violation at once rather than fixing one
// at a time. nil resolution targets (nil Aggregate or nil Release)
// short-circuit silently — IR build's existing nil-handling already
// surfaces those at upstream layers.
func ValidateInjectBlocks(na types.NormalizedAggregate, _ *types.Release) error {
	agg := na.Aggregate()
	if agg == nil {
		return nil
	}
	var errs []error
	for name, comp := range agg.Spec.Components.All() {
		errs = append(errs, validateComponentInstance(comp, fmt.Sprintf(".spec.components[%q]", name))...)
	}
	for name, sub := range agg.Spec.Subsystems.All() {
		errs = append(errs, validateSubsystem(sub, fmt.Sprintf(".spec.subsystems[%q]", name))...)
	}
	// Release-side: no typed *InjectBlock surfaces today. If a future
	// schema revision adds InjectBlock-bearing fields under Boundary,
	// BoundaryComponent, or BoundaryFeature, walk them here.
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

func validateComponentInstance(comp types.ComponentInstance, prefix string) []error {
	var errs []error
	if err := ValidateInjectBlock(comp.Inject, prefix+".inject"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, validateUsesBlock(comp.Uses, prefix)...)
	for fname, feat := range comp.Features.All() {
		errs = append(errs, validateFeatureInstance(feat, fmt.Sprintf("%s.features[%q]", prefix, fname))...)
	}
	for sname, sec := range comp.Secrets.All() {
		errs = append(errs, validateSecretInstance(sec, fmt.Sprintf("%s.secrets[%q]", prefix, sname))...)
	}
	return errs
}

// validateFeatureInstance walks a FeatureInstance recursively. A
// FeatureInstance carries its own Inject + Uses substructure (each Uses
// entry carries *InjectBlock via VerboseUsesEntry) + Secrets substructure
// (each SecretInstance carries its own Inject + Uses). Without this
// recursion, both-present InjectBlock instances reached via a feature's
// nested Uses or Secrets would slip past the validator.
func validateFeatureInstance(feat types.FeatureInstance, prefix string) []error {
	var errs []error
	if err := ValidateInjectBlock(feat.Inject, prefix+".inject"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, validateUsesBlock(feat.Uses, prefix)...)
	for sname, sec := range feat.Secrets.All() {
		errs = append(errs, validateSecretInstance(sec, fmt.Sprintf("%s.secrets[%q]", prefix, sname))...)
	}
	return errs
}

// validateSecretInstance walks a SecretInstance recursively. A
// SecretInstance carries Inject + Uses substructure (each Uses entry
// carries *InjectBlock via VerboseUsesEntry). Unlike FeatureInstance,
// SecretInstance cannot carry nested Secrets — credentials are leaf
// attachments per the type's contract — so recursion terminates here on
// the secret side; the Uses-side branch reaches no deeper than its
// VerboseUsesEntry.Inject leaves.
func validateSecretInstance(sec types.SecretInstance, prefix string) []error {
	var errs []error
	if err := ValidateInjectBlock(sec.Inject, prefix+".inject"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, validateUsesBlock(sec.Uses, prefix)...)
	return errs
}

// validateUsesBlock walks a uses block at any nesting level. Each
// verbose-entry's Inject is validated; compact-form (string-array) sockets
// carry no InjectBlock and are no-ops.
func validateUsesBlock(uses types.OrderedMap[types.UsesSocketValue], prefix string) []error {
	var errs []error
	for socket, usesVal := range uses.All() {
		for alias, entry := range usesVal.Verbose.All() {
			if err := ValidateInjectBlock(entry.Inject, fmt.Sprintf("%s.uses[%q][%q].inject", prefix, socket, alias)); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

func validateSubsystem(sub types.Subsystem, prefix string) []error {
	var errs []error
	for cname, comp := range sub.Components.All() {
		errs = append(errs, validateComponentInstance(comp, fmt.Sprintf("%s.components[%q]", prefix, cname))...)
	}
	for fname, feat := range sub.Features.All() {
		errs = append(errs, validateFeatureInstance(feat, fmt.Sprintf("%s.features[%q]", prefix, fname))...)
	}
	for sname, nested := range sub.Subsystems.All() {
		errs = append(errs, validateSubsystem(nested, fmt.Sprintf("%s.subsystems[%q]", prefix, sname))...)
	}
	return errs
}

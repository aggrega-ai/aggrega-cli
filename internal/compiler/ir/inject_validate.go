// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"errors"
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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
func ErrInjectEntryAmbiguous() error { return errInjectEntryAmbiguous }

var errInjectEntryAmbiguous = cerrors.Register("compiler-ir/inject-entry-ambiguous", cerrors.Meta{
	ShortCode: "E0014",
	Message:   "inject entry carries both `value:` and `from:`; schema requires exactly one",
})

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
	return fmt.Errorf("%w: %s", ErrInjectEntryAmbiguous(), strings.Join(violations, "; "))
}

// ValidateInjectBlocks walks every InjectBlock surface in the resolved
// Aggregate and Release source-of-truth structures and aggregates errors
// across all findings. The walker is the IR-build-time entry point for
// the inject-block second-line-of-defense check; it runs once before
// per-edge / per-node consumer-site work begins and halts the build via
// the existing IR-build error-return path if any violation surfaces.
//
// Aggregate-side surfaces walked, over the subsystem tree recursively — a
// component authored at the top-level `.spec.components` surface is reached
// through the synthesized default subsystem, and its diagnostics render at the
// root field shape the author typed:
//
//   - Subsystems[*].Components[*].Inject (component self-inject)
//   - Subsystems[*].Components[*].Features[*].Inject (attachment inject)
//   - Subsystems[*].Components[*].Uses[*].Verbose[*].Inject (uses[].inject)
//   - Subsystems[*].Features[*].Inject, and Subsystems[*].Subsystems[*]...
//     recursively
//
// Release-side surfaces walked: the resolved Release.Spec.Boundaries tree
// is structurally walked for symmetry with the operator's stated scope.
// As of the current schema, no Release-side struct type carries a
// top-level *types.InjectBlock — BoundaryFeature has
// Type/Description/Infra/Uses (uses carries its own inject inline via
// the verbose UsesSocketValue form, not a struct-level InjectBlock), and
// both Boundary.Features and BoundaryComponent.Features are
// OrderedMap[BoundaryFeature]. The walk is therefore a structural no-op
// today; if a future schema revision adds an InjectBlock-bearing field to
// any Release surface, the walker is the extension point.
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
	// No visitor callback fails, so the walk cannot: every violation is collected
	// and joined so the operator sees them all at once.
	_ = types.WalkInstanceTree(agg.Spec.Subsystems, types.SubsystemTree(), ".spec", struct{}{},
		types.InstanceTreeVisitor[types.Subsystem, types.ComponentInstance, struct{}]{
			// The synthesized default holds components authored at the top-level
			// surface; render their inject diagnostics at the root shape rather than
			// under the generated scope name. The IR itself keeps the literal default
			// scope — this elides only the diagnostic locator string.
			Path: func(parentPath, name string, synthesized bool) string {
				if synthesized {
					return parentPath
				}
				return fmt.Sprintf("%s.subsystems[%q]", parentPath, name)
			},
			Visit: func(comp types.ComponentInstance, name, path string, _ bool, _ struct{}) error {
				errs = append(errs, validateComponentInstance(comp, fmt.Sprintf("%s.components[%q]", path, name))...)
				return nil
			},
			// Subsystem-wide attachments are checked after the component loop and
			// before the nested descent, so the joined diagnostic reads outer scope's
			// components, then its own attachments, then inward.
			Leave: func(sub types.Subsystem, _, path string, _ struct{}) {
				for fname, feat := range sub.Features.All() {
					errs = append(errs, validateFeatureInstance(feat, fmt.Sprintf("%s.features[%q]", path, fname))...)
				}
			},
		})
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
	return errs
}

// validateFeatureInstance walks one attachment: its own Inject plus its Uses
// substructure (each Uses entry carries *InjectBlock via VerboseUsesEntry).
// Without the Uses descent, a both-present InjectBlock reached through an
// attachment's nested Uses would slip past the validator.
func validateFeatureInstance(feat types.FeatureInstance, prefix string) []error {
	var errs []error
	if err := ValidateInjectBlock(feat.Inject, prefix+".inject"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, validateUsesBlock(feat.Uses, prefix)...)
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

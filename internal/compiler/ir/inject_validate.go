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

var errInjectEntryAmbiguous = cerrors.Register("inject-entry-ambiguous", cerrors.Meta{
	ShortCode: "E0014",
	Message:   "inject entry carries both `value:` and `from:`; schema requires exactly one",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrInjectDeliveryShape reports an inject delivery mechanism — env, file, arg
// or a mechanism an author names beside them — whose value is not a list of
// entries. A mechanism is the list of {in, from?, value?} entries the
// inject_delivery definition describes, and every reader of a mechanism reads
// that list and nothing else. The Aggregate and Release schemas refuse any other
// shape at envelope load; a block that never passed an envelope — one built
// through the Go API — reaches IR build with whatever it carries, and this
// condition halts it there instead of letting the delivery vanish.
func ErrInjectDeliveryShape() error { return errInjectDeliveryShape }

var errInjectDeliveryShape = cerrors.Register("inject-delivery-shape", cerrors.Meta{
	ShortCode: "E0355",
	Message:   "inject delivery: shape must be a list of entries, each `in:` with exactly one of `from:` or `value:`",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ValidateInjectBlock checks a single InjectBlock's mechanisms (env, file, arg,
// and each Additional[...] entry, in that order) and reports two faults. The
// validator is **defense-in-depth, not primary enforcement** — the JSON Schema
// validator at envelope load is the primary check; this validator catches a
// block that reached IR build without passing it.
//
//   - A mechanism whose value is not a list ([]any) — a mapping, a scalar, any
//     other shape — returns a wrapped ErrInjectDeliveryShape naming
//     `<pathPrefix>.<mechanism>`. A mechanism carrying no value at all (nil)
//     delivers nothing and is not a fault.
//   - A list entry carrying both `value:` AND `from:` returns a wrapped
//     ErrInjectEntryAmbiguous naming `<pathPrefix>.<mechanism>[<index>]`.
//
// Every violation of one kind is named in one error, separated by "; ". Where
// both kinds occur, the two errors are joined, the shape refusal first.
//
// pathPrefix anchors the error's field path. Callers pass an entity-level
// prefix (e.g., `.spec.components["api"].inject`).
//
// Out of scope: an entry lacking `in:` or carrying a non-string `in:` — the
// helper [types.DecodeVerboseInjectEntry] returns ok=false and every reader of
// a mechanism skips the entry.
func ValidateInjectBlock(b *types.InjectBlock, pathPrefix string) error {
	if b == nil {
		return nil
	}
	var notAList, ambiguous []string
	check := func(mech string, raw any) {
		if raw == nil {
			return
		}
		arr, ok := raw.([]any)
		if !ok {
			notAList = append(notAList, fmt.Sprintf("%s.%s", pathPrefix, mech))
			return
		}
		for i, entry := range arr {
			_, _, _, hasValue, hasFrom, decodeOk := types.DecodeVerboseInjectEntry(entry)
			if !decodeOk {
				continue
			}
			if hasValue && hasFrom {
				ambiguous = append(ambiguous,
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
	var errs []error
	if len(notAList) > 0 {
		errs = append(errs, fmt.Errorf("%w: %s", ErrInjectDeliveryShape(), strings.Join(notAList, "; ")))
	}
	if len(ambiguous) > 0 {
		errs = append(errs, fmt.Errorf("%w: %s", ErrInjectEntryAmbiguous(), strings.Join(ambiguous, "; ")))
	}
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return errors.Join(errs...)
	}
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
// Release-side surfaces walked, over the boundary tree recursively, after the
// whole Aggregate: the one Release surface carrying an InjectBlock is a
// connection a Release attachment draws — [types.BoundaryFeature]'s Uses, whose
// verbose entries are [types.FeatureUsesEntry]. Per boundary, in author order:
//
//   - each component the boundary declares, on both of its axes (named directly,
//     or routed in through a sub-module's routing entry), and every attachment
//     on it: Components[*].Features[*].Uses[*].Verbose[*].Inject
//   - the boundary-wide attachments: Features[*].Uses[*].Verbose[*].Inject
//   - then each nested boundary
//
// A Release diagnostic names the key path the Release author wrote:
// `.spec.boundaries["<b>"]` per boundary level, a routed component through its
// `aggregates["<module>"]` entries under the local name it was written with, and
// a component authored at the top-level `.spec.components` surface at that root
// shape rather than under the boundary the compiler synthesizes for it.
//
// Errors are aggregated across all walks; the operator sees every violation at
// once rather than fixing one at a time. A nil Aggregate or a nil Release is
// not walked — IR build's existing nil-handling already surfaces those at
// upstream layers.
func ValidateInjectBlocks(na types.NormalizedAggregate, rel *types.Release) error {
	var errs []error
	if agg := na.Aggregate(); agg != nil {
		errs = append(errs, validateAggregateInjectBlocks(agg)...)
	}
	if rel != nil {
		errs = append(errs, validateReleaseInjectBlocks(types.NormalizeRelease(rel).Release())...)
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// validateAggregateInjectBlocks walks the Aggregate's subsystem tree.
func validateAggregateInjectBlocks(agg *types.Aggregate) []error {
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
	return errs
}

// validateReleaseInjectBlocks walks a normalized Release's boundary tree. The
// boundary's components are read through [types.BoundaryComponentDeclarations],
// the one enumeration of both axes that hands back the authored locator, so a
// routed component is named under the key its author typed.
func validateReleaseInjectBlocks(rel *types.Release) []error {
	var errs []error
	// No visitor callback fails, so the walk cannot: every violation is collected
	// and joined so the operator sees them all at once.
	_ = types.WalkInstanceTree(rel.Spec.Boundaries, types.BoundaryTree(), ".spec", struct{}{},
		types.InstanceTreeVisitor[types.Boundary, types.BoundaryComponent, struct{}]{
			// The synthesized default holds components authored at the top-level
			// `.spec.components` surface; render their diagnostics at that root shape
			// rather than under the generated boundary name.
			Path: func(parentPath, name string, synthesized bool) string {
				if synthesized {
					return parentPath
				}
				return fmt.Sprintf("%s.boundaries[%q]", parentPath, name)
			},
			// Components are read here rather than in Visit, which hands a routed
			// component its flattened id rather than the key the author wrote.
			Leave: func(b types.Boundary, _, path string, _ struct{}) {
				for _, decl := range types.BoundaryComponentDeclarations(b) {
					compPath := path + releaseLocator(decl.Locator)
					for fname, feat := range decl.Component.Features.All() {
						errs = append(errs, validateUsesBlock(types.FeatureUsesConnections(feat.Uses),
							fmt.Sprintf("%s.features[%q]", compPath, fname))...)
					}
				}
				for fname, feat := range b.Features.All() {
					errs = append(errs, validateUsesBlock(types.FeatureUsesConnections(feat.Uses),
						fmt.Sprintf("%s.features[%q]", path, fname))...)
				}
			},
		})
	return errs
}

// releaseLocator renders a boundary-relative authored key path — alternating
// key and name segments, such as ["aggregates", "data", "components", "store"] —
// in the locator spelling every inject diagnostic uses.
func releaseLocator(segments []string) string {
	var out string
	for i := 0; i+1 < len(segments); i += 2 {
		out += fmt.Sprintf(".%s[%q]", segments[i], segments[i+1])
	}
	return out
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
	errs = append(errs, validateUsesBlock(types.FeatureUsesConnections(feat.Uses), prefix)...)
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

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
// The Release is not walked. The one Release surface carrying an InjectBlock
// is a connection a Release attachment draws — [types.BoundaryFeature]'s Uses,
// whose verbose entries are [types.FeatureUsesEntry] — and its blocks are not
// examined here, so neither refusal above reaches them. The Release schema's
// inject_delivery admits only the list, so an authored Release is refused at
// envelope load all the same; a Release built through the Go API is not.
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

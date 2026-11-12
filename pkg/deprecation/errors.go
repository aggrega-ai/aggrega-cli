// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package deprecation

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrDeprecatedEntity halts a build that consumes a deprecated closure entity
// under a halting policy. It fires when a retroactive deprecation marker
// recorded in the lockfile carries a severity that meets or exceeds the
// configured halt threshold. Below the threshold, or with no threshold
// configured, the same consumption surfaces as an advisory rather than this
// error.
func ErrDeprecatedEntity() error { return errDeprecatedEntity }

var errDeprecatedEntity = cerrors.Register("deprecated-entity", cerrors.Meta{
	ShortCode: "E0122",
	Message:   "deprecation: build consumes a deprecated entity",
	RulePlace: cerrors.RuleNoStageReaches("Composing a build's inputs evaluates the rule, as the markers the " +
		"lockfile and the plugin trust record already hold are weighed against the severity threshold the " +
		"workspace configuration names. The composition finishes before the pipeline starts, and the one " +
		"stage that declares the workspace-configuration power produces its facts by resolving plugin " +
		"references and calling across the guest boundary — so a rule placed there would load and call the " +
		"very plugin this one exists to refuse."),
})

// HaltFor returns the halt a configured threshold produces for one recorded
// marker, or nil when that marker does not halt. notice is the marker's rendered
// sentence — the reference, its severity, its message, and when the record was
// observed — which the returned message carries verbatim, so the halt says exactly
// what the report said and adds only the policy that stopped the build.
//
// It is the single home of the halting rule, so every artifact-producing surface
// halts on the same markers. Two surfaces each deciding for themselves is how one
// would come to stop a build the other let through under the same configuration,
// with the workspace's configured policy silently not applying on one of them.
//
// An unset threshold is tested for explicitly rather than left to rank below every
// severity: MeetsThreshold's unranked arm exists for a malformed value, and
// resting the shipped default on it would let a later change in how an
// unrecognised severity ranks silently invert the default from advising to
// halting.
//
// The message wraps exactly ONE error, its sentinel: the threshold clause is
// interpolated as text rather than wrapped, because a multi-error renders as its
// wrapped errors alone — dropping the format string's own content, which is where
// the reference, the severity, the message and the observation moment all live.
func HaltFor(notice string, recorded, threshold Severity) error {
	if threshold == "" || !MeetsThreshold(recorded, threshold) {
		return nil
	}
	return fmt.Errorf("%w: %s: halt_on_deprecated_severity is %q", ErrDeprecatedEntity(), notice, threshold)
}

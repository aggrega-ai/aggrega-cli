// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package deprecation_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
)

// TestHaltForIsSilentWithNoConfiguredThreshold pins the shipped default, which is
// the reason the dial exists at all: a recorded marker is registry state applied
// after the version was pinned, so a workspace that configured nothing keeps
// building — loudly advised, never stopped. It is asserted at every severity,
// including the strongest, because a default that halted on the strongest one
// would break a build no input of the user's had changed.
func TestHaltForIsSilentWithNoConfiguredThreshold(t *testing.T) {
	for _, recorded := range []deprecation.Severity{
		deprecation.SeverityWarning, deprecation.SeverityError, deprecation.SeverityRetracted,
	} {
		if err := deprecation.HaltFor("api is deprecated", recorded, ""); err != nil {
			t.Errorf("an unset threshold must halt on nothing; %s produced: %v", recorded, err)
		}
	}
}

// TestHaltForHonoursTheOrderedSet walks the whole grid of recorded severity
// against configured threshold rather than its margins, because the rule is a
// comparison between two values and a per-axis assertion leaves the interior
// unstated.
func TestHaltForHonoursTheOrderedSet(t *testing.T) {
	severities := []deprecation.Severity{
		deprecation.SeverityWarning, deprecation.SeverityError, deprecation.SeverityRetracted,
	}
	rank := map[deprecation.Severity]int{
		deprecation.SeverityWarning: 0, deprecation.SeverityError: 1, deprecation.SeverityRetracted: 2,
	}
	for _, recorded := range severities {
		for _, threshold := range severities {
			wantHalt := rank[recorded] >= rank[threshold]
			err := deprecation.HaltFor("api is deprecated", recorded, threshold)
			if wantHalt != (err != nil) {
				t.Errorf("recorded=%s threshold=%s: wantHalt=%v, got err=%v",
					recorded, threshold, wantHalt, err)
			}
		}
	}
}

// TestHaltForRefusesToActOnAMalformedValue holds both directions of the
// unrecognised-value rule: a malformed recorded severity can neither trigger a
// halt nor, as a threshold, suppress one by ranking oddly.
func TestHaltForRefusesToActOnAMalformedValue(t *testing.T) {
	if err := deprecation.HaltFor("api is deprecated", "not-a-severity", deprecation.SeverityWarning); err != nil {
		t.Errorf("a malformed recorded severity must not halt: %v", err)
	}
	if err := deprecation.HaltFor("api is deprecated", deprecation.SeverityRetracted, "not-a-severity"); err != nil {
		t.Errorf("a malformed threshold must not halt: %v", err)
	}
}

// TestHaltForCarriesTheWholeSentenceAndItsSentinel pins what reaches the reader.
// The halt must carry the report's own sentence verbatim and add only the policy
// that stopped the build, and it must wrap exactly ONE error: a multi-error renders
// as its wrapped errors alone, which would drop the reference, the severity, the
// message and the observation moment — everything the sentence carries.
func TestHaltForCarriesTheWholeSentenceAndItsSentinel(t *testing.T) {
	const notice = "api is deprecated (retracted): withdrawn: bad release (recorded in the lockfile at pull, as of 2026-08-01T09:30:00Z; re-run `aggrega pull` to refresh it)"

	err := deprecation.HaltFor(notice, deprecation.SeverityRetracted, deprecation.SeverityError)
	if err == nil {
		t.Fatal("a recorded marker at or above the threshold must halt")
	}
	if !errors.Is(err, deprecation.ErrDeprecatedEntity()) {
		t.Errorf("the halt must carry the deprecated-entity sentinel; got: %v", err)
	}
	if !strings.Contains(err.Error(), notice) {
		t.Errorf("the halt must carry the report's sentence verbatim:\n%v", err)
	}
	if !strings.Contains(err.Error(), `halt_on_deprecated_severity is "error"`) {
		t.Errorf("the halt must name the policy that stopped it:\n%v", err)
	}
	// The moment qualifies the recorded fact, so it must precede the policy clause
	// rather than trail it, where it would read as though the policy were recorded.
	if i, j := strings.Index(err.Error(), "as of "), strings.Index(err.Error(), "halt_on_deprecated_severity"); i < 0 || j < 0 || i > j {
		t.Errorf("the observation moment must sit with the record, before the threshold clause:\n%v", err)
	}
	// Exactly one wrapped error: unwrapping once must reach the sentinel and no
	// further wrapped chain.
	if u := errors.Unwrap(err); u == nil || u.Error() != deprecation.ErrDeprecatedEntity().Error() {
		t.Errorf("the halt must wrap exactly its sentinel; unwrapped to: %v", u)
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package labels

import (
	"strings"
	"testing"
)

// TestInspectComponentIdentity_ReportsEachRuleAnIdentityCanFail drives the
// predicate over one input per outcome, including the ones that must come back
// usable — a table of refusals alone cannot tell a correct rule from one that
// refuses everything.
func TestInspectComponentIdentity_ReportsEachRuleAnIdentityCanFail(t *testing.T) {
	tests := []struct {
		name     string
		identity string
		want     ComponentIdentityDefect
	}{
		{"an ordinary authored name", "web", ComponentIdentityUsable},
		{"a name carrying a hyphen inside it", "main-db", ComponentIdentityUsable},
		{"a name carrying an underscore inside it", "main_db", ComponentIdentityUsable},
		{"a qualified identity a module contributed", "data-layer.main-db", ComponentIdentityUsable},
		{"a single character", "a", ComponentIdentityUsable},
		{"a digit", "0", ComponentIdentityUsable},
		{"the empty string, which a label value may be", "", ComponentIdentityUsable},
		{"exactly at the length bound", strings.Repeat("a", k8sLabelValueMaxLength), ComponentIdentityUsable},

		{"one character over the bound", strings.Repeat("a", k8sLabelValueMaxLength+1), ComponentIdentityTooLong},
		{"far over the bound", strings.Repeat("a", 200), ComponentIdentityTooLong},

		{"a character the target does not admit", "web:1", ComponentIdentityCharacterNotAdmitted},
		{"a path separator", "a/b", ComponentIdentityCharacterNotAdmitted},
		{"a space", "web app", ComponentIdentityCharacterNotAdmitted},

		{"opening on a hyphen", "-web", ComponentIdentityEdgeNotAlphanumeric},
		{"closing on a hyphen", "web-", ComponentIdentityEdgeNotAlphanumeric},
		{"opening on an underscore", "_web", ComponentIdentityEdgeNotAlphanumeric},
		{"closing on an underscore", "web_", ComponentIdentityEdgeNotAlphanumeric},
		{"opening on a dot", ".web", ComponentIdentityEdgeNotAlphanumeric},
		{"closing on a dot", "web.", ComponentIdentityEdgeNotAlphanumeric},
		{"a single hyphen", "-", ComponentIdentityEdgeNotAlphanumeric},

		{"the reserved opening alone", "bfr--", ComponentIdentityReservedOpening},
		{"the reserved opening and a name", "bfr--web", ComponentIdentityReservedOpening},

		// The reserved opening is reported ahead of every label rule, because a
		// reserved name has to be renamed whatever else is true of it. Each of
		// these breaks a label rule as well, and reporting that rule instead would
		// send the author to make an edit that cannot save the name.
		{"reserved and closing on a hyphen", "bfr--web-", ComponentIdentityReservedOpening},
		{"reserved and over the length bound", "bfr--" + strings.Repeat("a", 200), ComponentIdentityReservedOpening},
		{"reserved and carrying a refused character", "bfr--web:1", ComponentIdentityReservedOpening},

		// The prefix without the join is an ordinary name. Reserving the prefix on
		// its own would refuse names an author may legitimately write, and only the
		// prefix FOLLOWED BY the join is what a composed identity actually opens
		// with — so these are the cases that keep the reservation narrow.
		{"the prefix with no join after it", "bfr", ComponentIdentityUsable},
		{"the prefix and one hyphen", "bfr-web", ComponentIdentityUsable},
		{"the prefix as part of a longer word", "bfrontend", ComponentIdentityUsable},
		{"the reserved opening not at the start", "web--bfr--x", ComponentIdentityUsable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, opening := InspectComponentIdentity(tt.identity)
			if got != tt.want {
				t.Errorf("InspectComponentIdentity(%q) = %v, want %v", tt.identity, got, tt.want)
			}
			switch tt.want {
			case ComponentIdentityReservedOpening:
				if opening == "" {
					t.Errorf("InspectComponentIdentity(%q) named no opening; a reader cannot see how much of "+
						"the name is reserved without one", tt.identity)
				}
				if !strings.HasPrefix(tt.identity, opening) {
					t.Errorf("InspectComponentIdentity(%q) named opening %q, which the identity does not open with",
						tt.identity, opening)
				}
			default:
				if opening != "" {
					t.Errorf("InspectComponentIdentity(%q) named opening %q for a non-reserved outcome",
						tt.identity, opening)
				}
			}
		})
	}
}

// TestInspectComponentIdentity_AdmitsEveryIdentityTheCompositionProduces is the
// clause that catches a refusal written wider than the authoring surface.
//
// It runs the predicate over what [BoundaryFeatureComponentID] itself composes.
// Every one of those is a valid label value by construction, so the label half
// must admit all of them — and every one opens with the reserved sequence, so
// the reserved half must refuse all of them. A predicate failing the first has
// been written stricter than the target's rules; one failing the second would
// leave the reservation unenforced against the very shape it exists for.
func TestInspectComponentIdentity_AdmitsEveryIdentityTheCompositionProduces(t *testing.T) {
	instances := []string{"bridge", "b", "a-b", "a_b", "a.b", strings.Repeat("i", 80), "Ünïcödé", "\xff\xfe"}
	paths := [][]string{nil, {"cluster"}, {"cluster", "east"}, {strings.Repeat("p", 90)}, {"a/b", "c.d"}}

	for _, instance := range instances {
		for _, path := range paths {
			composed := BoundaryFeatureComponentID(instance, path)

			if defect := labelValueDefect(composed); defect != ComponentIdentityUsable {
				t.Errorf("the composition produced %q for instance %q path %v, which the label half "+
					"refuses as %v — the predicate is stricter than the composition", composed, instance, path, defect)
			}
			got, opening := InspectComponentIdentity(composed)
			if got != ComponentIdentityReservedOpening {
				t.Errorf("InspectComponentIdentity(%q) = %v, want the reserved-opening outcome: a composed "+
					"identity is exactly what an authored one must not be mistakable for", composed, got)
			}
			if opening == "" {
				t.Errorf("InspectComponentIdentity(%q) named no opening", composed)
			}
		}
	}
}

// TestInspectComponentIdentity_AgreesWithTheRepairBesideIt pins the predicate to
// the function that writes a value into the same rules.
//
// Whatever [SanitizeForK8sLabelValue] returns is by its own contract a valid
// label value, so the predicate's label half must admit every one of its
// outputs. That is the property a second, hand-written copy of the label rules
// would eventually break — and it would break silently, because each side keeps
// passing its own assertions while the two drift.
func TestInspectComponentIdentity_AgreesWithTheRepairBesideIt(t *testing.T) {
	inputs := []string{
		"web", "a:b", "a/b", "-web-", "___", "...", strings.Repeat("x", 200),
		strings.Repeat("x", 62) + "-y", "Ünïcödé", "\xff", "a b c", "",
	}
	for _, in := range inputs {
		repaired := SanitizeForK8sLabelValue(in)
		if defect := labelValueDefect(repaired); defect != ComponentIdentityUsable {
			t.Errorf("SanitizeForK8sLabelValue(%q) = %q, which the predicate refuses as %v; the repair and "+
				"the test have drifted about what a valid label value is", in, repaired, defect)
		}
	}
}

// TestComponentIdentityRequirement_ReadsTheBoundRatherThanRestatingIt asserts
// the length sentence is generated from the bound, so the advice cannot come to
// disagree with the rule it describes.
func TestComponentIdentityRequirement_ReadsTheBoundRatherThanRestatingIt(t *testing.T) {
	requirement := ComponentIdentityTooLong.Requirement()
	if !strings.Contains(requirement, "63") {
		t.Errorf("the length requirement reads %q and does not name the bound the rule enforces", requirement)
	}
	if k8sLabelValueMaxLength != 63 {
		t.Fatalf("the bound moved to %d; this assertion pins the sentence to the bound and must be "+
			"re-read, not merely updated", k8sLabelValueMaxLength)
	}

	// Every defect a build can report names what a name has to do instead. One
	// that reported nothing would leave an author holding a refusal with no
	// remedy in it.
	for _, d := range []ComponentIdentityDefect{
		ComponentIdentityTooLong, ComponentIdentityCharacterNotAdmitted, ComponentIdentityEdgeNotAlphanumeric,
	} {
		if d.Requirement() == "" {
			t.Errorf("defect %v states no requirement", d)
		}
	}
	// The two that carry none say so deliberately: nothing failed, and an
	// occurrence-specific opening respectively.
	if ComponentIdentityUsable.Requirement() != "" {
		t.Error("the usable outcome states a requirement, which reads as a refusal that did not happen")
	}
	if ComponentIdentityReservedOpening.Requirement() != "" {
		t.Error("the reserved-opening outcome states a class-level requirement; its requirement names the " +
			"opening matched and so belongs to the occurrence")
	}
}

// TestReservedOpeningOf_ReadsTheSetAndTheJoinFromTheComposition asserts the
// refusal resolves the reserved opening through the composition's own
// declarations rather than through a literal of its own.
//
// It is written against the declarations rather than against the string "bfr--"
// on purpose: a test spelling that literal would be a third copy of the very
// thing the single-home rule exists to prevent, and would keep passing if the
// predicate grew a copy too.
func TestReservedOpeningOf_ReadsTheSetAndTheJoinFromTheComposition(t *testing.T) {
	if len(reservedScopePrefixes) == 0 {
		t.Fatal("the reserved set is empty, so the refusal enforces nothing")
	}
	for _, prefix := range reservedScopePrefixes {
		expected := boundaryFeatureIDEncoding.Segment(prefix) + boundaryFeatureIDJoin
		opening, reserved := reservedOpeningOf(expected + "anything")
		if !reserved {
			t.Errorf("an identity opening with %q is not reported reserved, so the set member %q is not enforced",
				expected, prefix)
		}
		if opening != expected {
			t.Errorf("reservedOpeningOf named %q, want %q composed from the prefix and the join", opening, expected)
		}
		// The prefix alone is not the opening: reserving it would refuse names an
		// author may write.
		if _, reserved := reservedOpeningOf(boundaryFeatureIDEncoding.Segment(prefix)); reserved {
			t.Errorf("the prefix %q alone is reported reserved; only the prefix followed by the join is "+
				"what a composed identity opens with", prefix)
		}
	}
}

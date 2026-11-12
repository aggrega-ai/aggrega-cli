// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package constraint

import (
	"strings"
	"testing"
)

// TestComponentLocatorTurnsOnProvenanceAndNotOnTheName is the pin on the whole
// component-provenance rule, and the rows are the cases it exists for rather
// than a sweep of inputs.
//
// The discriminating pair is the two rows carrying the subsystem path "default":
// one string, and the answer differs because one container was synthesized and
// the other was written by the author. They are named by their content rather
// than by their position, so inserting a row cannot silently re-point the
// sentence at a different pair. A rule reading the path string cannot tell them
// apart, and getting it wrong in one direction discards a real locator for every
// component authored at the top level while getting it wrong in the other names a
// subsystem key the source file does not contain.
func TestComponentLocatorTurnsOnProvenanceAndNotOnTheName(t *testing.T) {
	t.Parallel()

	const sourceFile = "catalog/aggregate.yaml"

	cases := []struct {
		name                 string
		componentName        string
		subsystemPath        string
		synthesizedSubsystem bool
		want                 string
	}{
		{
			name:          "no subsystem at all names the authored site",
			componentName: "web",
			want:          ".spec.components.web",
		},
		{
			name:                 "a synthesized subsystem contributes no segment, so the authored site is named",
			componentName:        "web",
			subsystemPath:        "default",
			synthesizedSubsystem: true,
			want:                 ".spec.components.web",
		},
		{
			name:          "an author-declared subsystem contributes its own level, because the author wrote it",
			componentName: "db",
			subsystemPath: "data",
			want:          ".spec.subsystems.data.components.db",
		},
		{
			name:          "an author-declared subsystem named like the synthesized one is still author-declared",
			componentName: "api",
			subsystemPath: "default",
			want:          ".spec.subsystems.default.components.api",
		},
		{
			name:          "a declared subsystem that nests spells every level under the container's own key",
			componentName: "ledger",
			subsystemPath: "billing.payments",
			want:          ".spec.subsystems.billing.subsystems.payments.components.ledger",
		},
		{
			name:                 "a nested synthesized path is still synthesized",
			componentName:        "cache",
			subsystemPath:        "default.inner",
			synthesizedSubsystem: true,
			want:                 ".spec.components.cache",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The identifier is deliberately not the authored name, so a constructor
			// that supplied one value for both would be visible here rather than
			// agreeing by coincidence.
			nodeID := "billing." + tc.componentName

			subject := newComponentNodeSubject(
				sourceFile, nodeID, tc.componentName, tc.subsystemPath, tc.synthesizedSubsystem,
			)
			if got := subject.Locator(); got != tc.want {
				t.Errorf("locator for component %q under subsystem %q (synthesized=%t) = %q; want %q",
					tc.componentName, tc.subsystemPath, tc.synthesizedSubsystem, got, tc.want)
			}
			if got := subject.SourceFile(); got != sourceFile {
				t.Errorf("source file = %q; want %q — the locator names a site within the file and "+
					"never replaces the attribution to it",
					got, sourceFile)
			}
			if got := subject.NodeID(); got != nodeID {
				t.Errorf("node identifier = %q; want %q — the identifier the keyed facts take travels "+
					"independently of the locator, so a locator that grew a subsystem level did not "+
					"reach the identifier",
					got, nodeID)
			}
		})
	}
}

// TestComponentLocatorComposesThroughTheSharedLocatorAssembly pins that the rule
// contributes segments and never a format. Were it to assemble the dotted string
// itself, the dialect would have two homes and could drift in one of them.
func TestComponentLocatorComposesThroughTheSharedLocatorAssembly(t *testing.T) {
	t.Parallel()

	segments := componentAuthoredSegments("web", "default", true)
	if want := authoredLocator(segments...); newComponentNodeSubject("a.yaml", "web", "web", "default", true).Locator() != want {
		t.Errorf("the component subject's locator is not what the shared assembly makes of its segments; "+
			"want %q", want)
	}

	declared := componentAuthoredSegments("db", "data", false)
	if want := authoredLocator(declared...); newComponentNodeSubject("a.yaml", "data.db", "db", "data", false).Locator() != want {
		t.Errorf("the author-declared arm's locator is not what the shared assembly makes of its "+
			"segments; want %q — both arms go through one assembly or the dialect has two homes", want)
	}

	// The flattening defect, pinned at the arm that produces it rather than only
	// at the helper underneath. A scope path arrives joined, and a segment still
	// carrying the separator is a path spliced in whole — one key where the author
	// wrote two, which no document contains.
	for _, segment := range componentAuthoredSegments("ledger", "billing.payments", false) {
		if strings.Contains(segment, subsystemNesting.separator) {
			t.Errorf("segment %q carries the axis separator, so the scope path was named as one key "+
				"instead of level by level under the container's own key", segment)
		}
	}
}

// TestComponentLocatorNamesTheContainerWhenTheNameIsMissing records what the rule
// does with no component name rather than leaving it to be discovered. The
// shared assembly drops an empty segment, so the locator names the container the
// component would sit in — a site the file does contain — instead of rendering a
// trailing gap.
func TestComponentLocatorNamesTheContainerWhenTheNameIsMissing(t *testing.T) {
	t.Parallel()

	if got, want := newComponentNodeSubject("a.yaml", "unnamed", "", "", false).Locator(), ".spec.components"; got != want {
		t.Errorf("locator with no component name = %q; want %q", got, want)
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import "testing"

// TestComponentLocatorTurnsOnProvenanceAndNotOnTheName is the pin on the whole
// component-provenance rule, and the rows are the cases it exists for rather
// than a sweep of inputs.
//
// The discriminating pair is the last two rows: the same subsystem path, and the
// answer differs. A rule reading the path string cannot tell them apart, and
// getting it wrong in one direction discards a real locator for every component
// authored at the top level while getting it wrong in the other names a
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
			name:          "an author-declared subsystem yields no locator, because its authored site is not expressible",
			componentName: "db",
			subsystemPath: "data",
			want:          "",
		},
		{
			name:          "an author-declared subsystem named like the synthesized one is still author-declared",
			componentName: "api",
			subsystemPath: "default",
			want:          "",
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

			subject := newComponentNodeSubject(sourceFile, tc.componentName, tc.subsystemPath, tc.synthesizedSubsystem)
			if got := subject.Locator(); got != tc.want {
				t.Errorf("locator for component %q under subsystem %q (synthesized=%t) = %q; want %q",
					tc.componentName, tc.subsystemPath, tc.synthesizedSubsystem, got, tc.want)
			}
			if got := subject.SourceFile(); got != sourceFile {
				t.Errorf("source file = %q; want %q — an empty locator is still attributed to its file",
					got, sourceFile)
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
	if want := authoredLocator(segments...); newComponentNodeSubject("a.yaml", "web", "default", true).Locator() != want {
		t.Errorf("the component subject's locator is not what the shared assembly makes of its segments; "+
			"want %q", want)
	}

	if got := componentAuthoredSegments("db", "data", false); got != nil {
		t.Errorf("segments for an author-declared subsystem = %v; want none, so the empty locator comes "+
			"from having nothing authored to name rather than from a second empty-string convention", got)
	}
}

// TestComponentLocatorNamesTheContainerWhenTheNameIsMissing records what the rule
// does with no component name rather than leaving it to be discovered. The
// shared assembly drops an empty segment, so the locator names the container the
// component would sit in — a site the file does contain — instead of rendering a
// trailing gap.
func TestComponentLocatorNamesTheContainerWhenTheNameIsMissing(t *testing.T) {
	t.Parallel()

	if got, want := newComponentNodeSubject("a.yaml", "", "", false).Locator(), ".spec.components"; got != want {
		t.Errorf("locator with no component name = %q; want %q", got, want)
	}
}

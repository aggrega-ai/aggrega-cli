// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"reflect"
	"sort"
	"testing"
)

// librarySectionNames reads the library sections from the Closure type itself, by
// their Go type, rather than from a hand-written list. Reading them from the type
// is what makes the assertion mean anything: a second list would have to be
// updated by the same person adding the section, so the check would pass exactly
// when it was needed least.
func librarySectionNames() []string {
	t := reflect.TypeOf(Closure{})
	want := reflect.TypeOf([]ResolvedLibrary{})
	var names []string
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).Type == want {
			names = append(names, t.Field(i).Name)
		}
	}
	sort.Strings(names)
	return names
}

// declaredAxisNames reads the covered set from the declared axis table.
func declaredAxisNames() []string {
	names := make([]string, 0, len(libraryAxes))
	for _, a := range libraryAxes {
		names = append(names, a.Axis)
	}
	sort.Strings(names)
	return names
}

// missingFrom reports which of declared are absent from covered. It is a pure
// function of its inputs precisely so the assertion can be calibrated: a check run
// only over the real, already-passing population proves the population passes and
// says nothing about whether the check can fail.
func missingFrom(declared, covered []string) []string {
	set := make(map[string]struct{}, len(covered))
	for _, n := range covered {
		set[n] = struct{}{}
	}
	var missing []string
	for _, n := range declared {
		if _, ok := set[n]; !ok {
			missing = append(missing, n)
		}
	}
	sort.Strings(missing)
	return missing
}

// TestEveryLibrarySectionIsDeclaredAsAnAxis is the assertion that keeps every pass
// over the library axes complete. The walk, the reference grammar, resolution,
// materialisation, canonical ordering, drift reporting, the pin projection and the
// convention lint all range over the declared axis table; a library section
// missing from that table is therefore reached by none of them, and nothing else
// would report it.
//
// So adding a library section to Closure fails this test until the section is
// declared. That is the point: the declaration becomes a step someone has to take
// rather than an omission nothing reports.
func TestEveryLibrarySectionIsDeclaredAsAnAxis(t *testing.T) {
	declared := librarySectionNames()
	if len(declared) == 0 {
		t.Fatal("no library sections read from the Closure type; the assertion would pass vacuously")
	}

	for _, name := range missingFrom(declared, declaredAxisNames()) {
		t.Errorf("closure section %q holds libraries but is not declared in libraryAxes, so it is reached by "+
			"no library pass at all: not walked, not resolved, not canonically ordered, not drift-checked and "+
			"not linted. Declare it as an axis", name)
	}

	// The reverse direction: an axis naming a section the type no longer declares
	// would bind to nothing.
	for _, name := range missingFrom(declaredAxisNames(), declared) {
		t.Errorf("libraryAxes declares axis %q, which the Closure type no longer holds a library section for", name)
	}
}

// TestLibraryAxisCoverageReportsAPlantedSection calibrates the assertion above. A
// coverage check run only over a population that already passes proves the
// population passes; it does not show the check can fail. This plants a section
// name the declared set omits and requires it to be reported — so a green result
// from the real population is the instrument firing rather than the instrument
// silent.
func TestLibraryAxisCoverageReportsAPlantedSection(t *testing.T) {
	declared := append(librarySectionNames(), "PlantedLibraries")

	missing := missingFrom(declared, declaredAxisNames())

	if len(missing) != 1 || missing[0] != "PlantedLibraries" {
		t.Fatalf("the coverage check must report an undeclared library section; got %v", missing)
	}
}

// TestEveryAxisCarriesItsVocabulary pins the fields every pass reads off an axis.
// An axis missing one of them does not fail to compile — it silently produces an
// empty fragment surface that matches no reference, or an empty diagnostic label
// that names nothing to the operator reading it.
func TestEveryAxisCarriesItsVocabulary(t *testing.T) {
	seen := map[string]struct{}{}
	for _, a := range LibraryAxes() {
		if _, dup := seen[a.Axis]; dup {
			t.Errorf("axis %q is declared twice; the coverage assertions join on the name", a.Axis)
		}
		seen[a.Axis] = struct{}{}

		if a.Kind == "" {
			t.Errorf("axis %q states no catalog kind, so nothing can fetch or pin it", a.Axis)
		}
		if a.FragmentPrefix == "" {
			t.Errorf("axis %q states no fragment prefix, so its reference grammar matches nothing and "+
				"every reference on it is silently ignored by discovery", a.Axis)
		}
		if a.Label == "" {
			t.Errorf("axis %q states no diagnostic label, so drift on it is reported to the operator unnamed", a.Axis)
		}
		if a.libs == nil {
			t.Errorf("axis %q binds to no closure section, so everything discovered on it accumulates nowhere", a.Axis)
		}
	}
}

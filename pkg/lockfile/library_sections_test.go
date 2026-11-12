// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lockfile

import (
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// sectionsMissingFrom reports which declared axes the section binding does not
// name. It is a pure function of its inputs so the assertion can be calibrated
// against a population that does not already pass.
func sectionsMissingFrom(declared, bound []string) []string {
	set := make(map[string]struct{}, len(bound))
	for _, n := range bound {
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

func declaredAxisNames() []string {
	names := make([]string, 0, len(closure.LibraryAxes()))
	for _, a := range closure.LibraryAxes() {
		names = append(names, a.Axis)
	}
	return names
}

func boundSectionNames() []string {
	names := make([]string, 0, len(librarySections()))
	for _, s := range librarySections() {
		names = append(names, s.axis.Axis)
	}
	return names
}

// TestEveryLibraryAxisIsPinnedBySection is the assertion that keeps the lockfile's
// coverage of the library axes complete.
//
// The lockfile pins each axis in its own typed section, so binding the declared
// axis set to those sections is irreducible — the section names are wire form. What
// this asserts is that the binding is TOTAL: an axis the binding omits is an axis
// whose libraries are never drift-checked against the lockfile, never contribute a
// pin to the re-pull reuse check, and are never canonically ordered on write. All
// three failures are silent, which is why they are asserted rather than trusted.
func TestEveryLibraryAxisIsPinnedBySection(t *testing.T) {
	declared := declaredAxisNames()
	if len(declared) == 0 {
		t.Fatal("no library axes declared; the assertion would pass vacuously")
	}

	for _, name := range sectionsMissingFrom(declared, boundSectionNames()) {
		t.Errorf("library axis %q is declared but bound to no lockfile section: its libraries are never "+
			"drift-checked, never contribute a pin, and are never canonically ordered on write — each "+
			"failing in silence. Bind it in librarySections", name)
	}
}

// TestLibrarySectionCoverageReportsAPlantedAxis calibrates the assertion above by
// planting an axis the binding omits and requiring it to be reported, so a green
// result from the real population is the instrument firing rather than silent.
func TestLibrarySectionCoverageReportsAPlantedAxis(t *testing.T) {
	declared := append(declaredAxisNames(), "PlantedLibraries")

	missing := sectionsMissingFrom(declared, boundSectionNames())

	if len(missing) != 1 || missing[0] != "PlantedLibraries" {
		t.Fatalf("the coverage check must report an unbound axis; got %v", missing)
	}
}

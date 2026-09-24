// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import (
	"reflect"
	"sort"
	"testing"
)

// axisCoverage reports how a declared section set divides against the axes a
// caller covers and the exemptions it names: sections classified by neither, and
// exemptions that no longer describe the tree.
//
// It is a pure function of its three inputs precisely so the assertion can be
// calibrated. A coverage check that only ever runs over the real, already-passing
// population proves that the population passes and says nothing about whether the
// check can fail; running the same function over a population carrying a planted
// extra section shows it fires. Both tests below call this one function.
func axisCoverage(declared, covered []string, exempt map[string]string) (unclassified, staleExempt, exemptButCovered []string) {
	coveredSet := make(map[string]struct{}, len(covered))
	for _, name := range covered {
		coveredSet[name] = struct{}{}
	}
	declaredSet := make(map[string]struct{}, len(declared))
	for _, name := range declared {
		declaredSet[name] = struct{}{}
	}

	for _, name := range declared {
		if _, ok := coveredSet[name]; ok {
			continue
		}
		if _, ok := exempt[name]; ok {
			continue
		}
		unclassified = append(unclassified, name)
	}
	for name := range exempt {
		if _, ok := declaredSet[name]; !ok {
			staleExempt = append(staleExempt, name)
			continue
		}
		if _, ok := coveredSet[name]; ok {
			exemptButCovered = append(exemptButCovered, name)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(staleExempt)
	sort.Strings(exemptButCovered)
	return unclassified, staleExempt, exemptButCovered
}

// closureSectionNames reads the declared section set from the Closure type itself
// rather than from a hand-written list. Reading it from the type is what makes the
// assertion mean anything: a second list would have to be updated by the same
// person adding the section, so the check would pass exactly when it was needed
// least.
func closureSectionNames() []string {
	t := reflect.TypeOf(Closure{})
	names := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		names = append(names, t.Field(i).Name)
	}
	return names
}

// coveredAxisNames reads the covered set from VerifiableAxes itself, over an empty
// closure — every covered axis is returned whether or not it holds members, so the
// set is the closure's shape rather than one closure's contents.
func coveredAxisNames() []string {
	axes := Closure{}.VerifiableAxes()
	names := make([]string, 0, len(axes))
	for _, a := range axes {
		names = append(names, a.Axis)
	}
	return names
}

// TestEveryClosureSectionIsClassified is the assertion that keeps the
// registry-touching passes kind-independent. It does not enumerate the sections
// that ARE covered — a list of covered kinds passes while the next kind added is
// covered by nobody, which is the drift this assertion exists to remove. It
// asserts the complement instead: every section the Closure type declares is
// either covered by VerifiableAxes or named in axesWithoutVerifiableRefs with the
// reason it carries no verifiable reference.
//
// So adding a section to Closure fails this test until the section is classified.
// That is the point: the classification becomes a decision someone has to write
// down rather than an omission nothing reports.
func TestEveryClosureSectionIsClassified(t *testing.T) {
	declared := closureSectionNames()
	if len(declared) == 0 {
		t.Fatal("no sections read from the Closure type; the assertion would pass vacuously")
	}

	unclassified, staleExempt, exemptButCovered := axisCoverage(declared, coveredAxisNames(), axesWithoutVerifiableRefs)

	for _, name := range unclassified {
		t.Errorf("closure section %q is neither covered by VerifiableAxes nor named in "+
			"axesWithoutVerifiableRefs: a reference on it reaches no signature verification "+
			"and no withdrawal-marker discovery, and nothing else reports that. Cover it, or "+
			"name it exempt with the reason it can carry neither", name)
	}
	// The exemption set is walked in the reverse direction too, so it cannot rot
	// unnoticed: a member naming a section that no longer exists exempts nothing,
	// and a member that is also covered describes the tree wrongly.
	for _, name := range staleExempt {
		t.Errorf("axesWithoutVerifiableRefs names %q (%q), which the Closure type no longer "+
			"declares, so the exemption is stale and exempts nothing", name, axesWithoutVerifiableRefs[name])
	}
	for _, name := range exemptButCovered {
		t.Errorf("axesWithoutVerifiableRefs names %q (%q), which VerifiableAxes also covers, "+
			"so the exemption misdescribes the tree and should be removed", name, axesWithoutVerifiableRefs[name])
	}
}

// TestSourceArtifactsAndReleaseDocumentsAreTheOnlyExemptSections pins the exemption
// set to exactly those two members. The coverage assertion above tolerates any
// exemption that carries a reason, which is right — it must not decide which
// sections deserve one — so this asserts the membership separately: a further
// exemption appearing is a design change that should be read, not absorbed, and
// one of these two disappearing is the same change in the other direction.
//
// It compares names rather than a count, so a set that swapped one member for
// another at the same size is still reported, and each difference is named.
func TestSourceArtifactsAndReleaseDocumentsAreTheOnlyExemptSections(t *testing.T) {
	want := []string{"ReleaseDocuments", "SourceArtifacts"}

	wantSet := make(map[string]struct{}, len(want))
	for _, name := range want {
		wantSet[name] = struct{}{}
	}
	got := make([]string, 0, len(axesWithoutVerifiableRefs))
	for name := range axesWithoutVerifiableRefs {
		got = append(got, name)
	}
	sort.Strings(got)

	for _, name := range want {
		if _, ok := axesWithoutVerifiableRefs[name]; !ok {
			t.Errorf("closure section %q is no longer exempt; the exemption set is %v. Dropping an "+
				"exemption is a design change too: remove it from this pin once the section is covered", name, got)
		}
	}
	for _, name := range got {
		if _, ok := wantSet[name]; !ok {
			t.Errorf("closure section %q is exempt (%q) but is not one of the pinned exemptions %v: a "+
				"further exemption is a design change to be read, not absorbed. Cover the section, or add "+
				"it here once the exemption has been reviewed", name, axesWithoutVerifiableRefs[name], want)
		}
		if axesWithoutVerifiableRefs[name] == "" {
			t.Errorf("closure section %q is exempt with no reason, so the exemption cannot be reviewed; "+
				"state why the section can carry neither a signature nor a withdrawal marker", name)
		}
	}
}

// TestAxisCoverageReportsAPlantedSection calibrates the assertion above. A
// coverage check run only over a population that already passes proves the
// population passes; it does not show the check can fail. This plants a section
// name the covered set and the exemption set both omit and requires it to be
// reported — so a green result from the real population is the instrument firing
// rather than the instrument silent.
func TestAxisCoverageReportsAPlantedSection(t *testing.T) {
	declared := append(closureSectionNames(), "PlantedSection")

	unclassified, _, _ := axisCoverage(declared, coveredAxisNames(), axesWithoutVerifiableRefs)

	if len(unclassified) != 1 || unclassified[0] != "PlantedSection" {
		t.Fatalf("the coverage check must report an unclassified section; got %v", unclassified)
	}
}

// TestAxisCoverageReportsAStaleExemption calibrates the reverse direction, which
// is the half a forward-only check leaves silent: an exemption naming a section
// that no longer exists reads as a live decision while exempting nothing.
func TestAxisCoverageReportsAStaleExemption(t *testing.T) {
	exempt := map[string]string{"RemovedSection": "a reason that outlived its section"}

	_, staleExempt, _ := axisCoverage(closureSectionNames(), coveredAxisNames(), exempt)

	if len(staleExempt) != 1 || staleExempt[0] != "RemovedSection" {
		t.Fatalf("the coverage check must report a stale exemption; got %v", staleExempt)
	}
}

// TestEveryAxisStatesItsKindExactlyOnce pins the Kind / KindHint discipline the
// axis shape rests on: a single-kind axis names its kind on the axis and leaves
// every reference's hint empty, and a mixed-kind axis leaves the axis kind empty
// and carries a hint per reference. A caller resolving a kind prefers the axis
// kind and falls back to the hint, so an axis that stated both would make which
// one wins depend on the caller.
func TestEveryAxisStatesItsKindExactlyOnce(t *testing.T) {
	cl := Closure{
		Entities:          []ResolvedRef{{URI: "core/compute/api-server", KindHint: "component"}},
		SchemaLibraries:   []ResolvedLibrary{{URI: "PortSpec"}},
		MappingLibraries:  []ResolvedLibrary{{URI: "HelmValues"}},
		WorkloadLibraries: []ResolvedLibrary{{URI: "Deployments"}},
		ArtifactLibraries: []ResolvedLibrary{{URI: "Bundles"}},
		Plugins:           []ResolvedRef{{URI: "oci://reg.example/plugin/provider/helm", KindHint: "plugin"}},
	}

	seen := map[string]struct{}{}
	for _, axis := range cl.VerifiableAxes() {
		if _, dup := seen[axis.Axis]; dup {
			t.Errorf("axis %q is returned twice; the coverage assertion joins on the name", axis.Axis)
		}
		seen[axis.Axis] = struct{}{}

		if len(axis.Refs) == 0 {
			t.Errorf("axis %q projected no references from a closure that holds one", axis.Axis)
			continue
		}
		for _, ref := range axis.Refs {
			switch {
			case axis.Kind != "" && ref.KindHint != "":
				t.Errorf("axis %q states kind %q AND carries hint %q on %q; the two are alternatives",
					axis.Axis, axis.Kind, ref.KindHint, ref.URI)
			case axis.Kind == "" && ref.KindHint == "":
				t.Errorf("axis %q states no kind and %q carries no hint, so no caller can resolve its kind",
					axis.Axis, ref.URI)
			}
		}
	}
}

// TestLibraryAxesCarryTheManifestDigest pins the field the library axes exist to
// deliver. The manifest digest is the subject a signature commits to, so a library
// reference reaching a verification pass without one cannot be verified at all —
// and it used to be produced at the resolver and dropped at the closure boundary,
// where nothing observed the loss.
func TestLibraryAxesCarryTheManifestDigest(t *testing.T) {
	const manifest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	lib := []ResolvedLibrary{{URI: "PortSpec", ManifestDigest: manifest, ResolutionPath: "tier-3-pull"}}
	cl := Closure{
		SchemaLibraries:   lib,
		MappingLibraries:  lib,
		WorkloadLibraries: lib,
		ArtifactLibraries: lib,
	}

	for _, axis := range cl.VerifiableAxes() {
		if axis.Kind == "" {
			continue
		}
		for _, ref := range axis.Refs {
			if ref.ManifestDigest != manifest {
				t.Errorf("axis %q dropped the manifest digest: got %q, want %q", axis.Axis, ref.ManifestDigest, manifest)
			}
			if ref.ResolutionPath != "tier-3-pull" {
				t.Errorf("axis %q dropped the resolution path, so no caller can tell a registry-resolved library from a local one", axis.Axis)
			}
		}
	}
}

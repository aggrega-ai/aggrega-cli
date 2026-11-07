// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// The one URI per axis both fixtures below are written against. Naming them once
// keeps the two literals from drifting apart in the fixture itself, which would
// make this guard compare two different closures and report their difference as a
// builder defect.
const (
	parityEntityURI          = "core/mariadb:1.0.0"
	paritySchemaLibraryURI   = "core/DbSizing:1.0.0"
	parityMappingLibraryURI  = "core/BaseMappings:1.0.0"
	parityWorkloadLibraryURI = "core/BaseWorkloads:1.0.0"
	parityArtifactLibraryURI = "core/BaseArtifacts:1.0.0"
	parityPluginURI          = "core/policy:1.0.0"
)

// TestPinBuildersProjectTheSameAxes enforces that PinsFromLockfile and
// PinsFromClosure project the same URI set. The two build the pin map SetPins
// consumes — the map that decides which Tier-2 cache slots are read verify-on-read
// — from two different sources: a lockfile already on disk, and the walk that is
// about to produce one. They are separate projections, so an axis wired into one
// and missed in the other leaves that kind of dependency reachable from a cache
// slot nothing checks, and nothing fails while it happens.
//
// The comparison is over the KEY SET and deliberately not over the values. The two
// sources legitimately carry different digests for the same URI — that is the
// whole point of a freeze, which records what the walk resolved — so a value
// comparison would assert something untrue and would then be "fixed" by weakening
// it into nothing. The fixture makes that explicit: every digest below differs
// between the two sides, so a value comparison could not pass here even by
// accident.
//
// Three assertions, because set equality alone is not enough:
//
//   - Set difference in both directions, naming what is missing on each side. A
//     count comparison cannot do this: two sides each short one axis agree
//     perfectly.
//   - A positive floor naming the library axes. Set equality is symmetric, so two
//     builders that BOTH stopped projecting an axis would agree exactly; only
//     requiring a specific URI to be present can tell agreement from shared
//     blindness.
//   - Both sides non-empty, so a fixture that stopped populating either side
//     cannot make the comparison vacuous.
func TestPinBuildersProjectTheSameAxes(t *testing.T) {
	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{
			{Kind: closure.KindComponent, URI: parityEntityURI, ResolvedVersion: "1.0.0", Digest: "sha256:lock-entity"},
		},
		SchemaLibraries: []lockfile.SchemaLibrary{
			{URI: paritySchemaLibraryURI, ResolvedVersion: "1.0.0", Digest: "sha256:lock-schema"},
		},
		MappingLibraries: []lockfile.MappingLibrary{
			{URI: parityMappingLibraryURI, ResolvedVersion: "1.0.0", Digest: "sha256:lock-mapping"},
		},
		WorkloadLibraries: []lockfile.WorkloadLibrary{
			{URI: parityWorkloadLibraryURI, ResolvedVersion: "1.0.0", Digest: "sha256:lock-workload"},
		},
		ArtifactLibraries: []lockfile.ArtifactLibrary{
			{URI: parityArtifactLibraryURI, ResolvedVersion: "1.0.0", Digest: "sha256:lock-artifacts"},
		},
		Plugins: []lockfile.Plugin{
			{URI: parityPluginURI, ResolvedVersion: "1.0.0", Digest: "sha256:lock-plugin"},
		},
		// Source artifacts are excluded by both builders; carrying one proves the
		// exclusion is shared rather than absent from the fixture.
		SourceArtifacts: []lockfile.SourceArtifact{
			{Kind: "helm-oci-chart", URL: "oci://example/chart", Digest: "sha256:lock-source"},
		},
	}

	// The same closure, with every digest deliberately different from the lockfile
	// side. A key-set comparison is indifferent to that; a value comparison is not.
	cl := closure.Closure{
		Entities: []closure.ResolvedRef{
			{URI: parityEntityURI, KindHint: closure.KindComponent, ResolvedVersion: "1.0.0", Digest: "sha256:walk-entity"},
		},
		SchemaLibraries: []closure.ResolvedLibrary{
			{URI: paritySchemaLibraryURI, ResolvedVersion: "1.0.0", Digest: "sha256:walk-schema"},
		},
		MappingLibraries: []closure.ResolvedLibrary{
			{URI: parityMappingLibraryURI, ResolvedVersion: "1.0.0", Digest: "sha256:walk-mapping"},
		},
		WorkloadLibraries: []closure.ResolvedLibrary{
			{URI: parityWorkloadLibraryURI, ResolvedVersion: "1.0.0", Digest: "sha256:walk-workload"},
		},
		ArtifactLibraries: []closure.ResolvedLibrary{
			{URI: parityArtifactLibraryURI, ResolvedVersion: "1.0.0", Digest: "sha256:walk-artifacts"},
		},
		Plugins: []closure.ResolvedRef{
			{URI: parityPluginURI, ResolvedVersion: "1.0.0", Digest: "sha256:walk-plugin"},
		},
		SourceArtifacts: []closure.SourceArtifact{
			{Kind: "helm-oci-chart", URL: "oci://example/chart", Digest: "sha256:walk-source"},
		},
	}

	fromLockfile, err := PinsFromLockfile(lf)
	if err != nil {
		t.Fatalf("PinsFromLockfile: %v", err)
	}
	fromClosure, err := PinsFromClosure(cl)
	if err != nil {
		t.Fatalf("PinsFromClosure: %v", err)
	}

	if len(fromLockfile) == 0 || len(fromClosure) == 0 {
		t.Fatalf("fixture is inert: a pin set is empty, so the comparison below proves nothing (lockfile=%d closure=%d)",
			len(fromLockfile), len(fromClosure))
	}

	if missing := pinKeysMissingFrom(fromLockfile, fromClosure); len(missing) > 0 {
		t.Errorf("PinsFromClosure does not pin %v — the lockfile projection covers those URIs, so a closure-pinned view reads those cache slots with no digest to check them against", missing)
	}
	if extra := pinKeysMissingFrom(fromClosure, fromLockfile); len(extra) > 0 {
		t.Errorf("PinsFromLockfile does not pin %v — the closure projection covers those URIs, so the lockfile-pinned view is the narrower of the two", extra)
	}

	// The positive floor. Each library axis is named, because set equality is
	// symmetric: two builders that both dropped an axis agree perfectly, and only
	// requiring the URI to be present distinguishes that from real agreement.
	for _, want := range []struct {
		axis string
		uri  string
	}{
		{"schema libraries", paritySchemaLibraryURI},
		{"mapping libraries", parityMappingLibraryURI},
		{"workload libraries", parityWorkloadLibraryURI},
		{"artifact libraries", parityArtifactLibraryURI},
	} {
		if _, ok := pinsHoldURI(fromLockfile, want.uri); !ok {
			t.Errorf("PinsFromLockfile does not project the %s axis: %q absent", want.axis, want.uri)
		}
		if _, ok := pinsHoldURI(fromClosure, want.uri); !ok {
			t.Errorf("PinsFromClosure does not project the %s axis: %q absent", want.axis, want.uri)
		}
	}

	// Source artifacts stay out of both, so the exclusion cannot quietly become an
	// asymmetry between them.
	for _, pins := range []struct {
		name string
		set  Pins
	}{{"PinsFromLockfile", fromLockfile}, {"PinsFromClosure", fromClosure}} {
		if _, ok := pinsHoldURI(pins.set, "oci://example/chart"); ok {
			t.Errorf("%s pins a source artifact; source artifacts descend on their own read-time digest gate and are excluded from both projections", pins.name)
		}
	}
}

// pinsHoldURI reports whether pins holds a pin of any kind for uri, and its key.
func pinsHoldURI(pins Pins, uri string) (PinKey, bool) {
	for k := range pins {
		if k.URI == uri {
			return k, true
		}
	}
	return PinKey{}, false
}

// pinKeysMissingFrom returns the sorted keys of want that other does not carry.
func pinKeysMissingFrom(want, other Pins) []string {
	var missing []string
	for k := range want {
		if _, ok := other[k]; !ok {
			missing = append(missing, k.Kind+" "+k.URI)
		}
	}
	sort.Strings(missing)
	return missing
}

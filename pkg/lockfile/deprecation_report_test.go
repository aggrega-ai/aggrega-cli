// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// markerFor builds a recorded deprecation record at one severity.
func markerFor(severity, message string) *lockfile.DeprecationRecord {
	return &lockfile.DeprecationRecord{Severity: severity, Message: message}
}

// lockfileWithAMarkerInEverySection puts one marker-bearing row in each of the six
// sections the lockfile can record a marker on, at URIs whose sort order is NOT the
// section order, so a report that walked the sections in declaration order and
// forgot to sort would surface here.
func lockfileWithAMarkerInEverySection(generatedAt string) *lockfile.Lockfile {
	return &lockfile.Lockfile{
		GeneratedAt:        generatedAt,
		Entities:           []lockfile.Entity{{URI: "f-entity", Deprecation: markerFor("warning", "superseded")}},
		SchemaLibraries:    []lockfile.SchemaLibrary{{URI: "e-schema", Deprecation: markerFor("error", "unsound")}},
		MappingLibraries:   []lockfile.MappingLibrary{{URI: "d-mapping", Deprecation: markerFor("retracted", "withdrawn")}},
		WorkloadLibraries:  []lockfile.WorkloadLibrary{{URI: "c-workload", Deprecation: markerFor("warning", "superseded")}},
		ArtifactsLibraries: []lockfile.ArtifactsLibrary{{URI: "b-artifacts", Deprecation: markerFor("error", "stale")}},
		Plugins:            []lockfile.Plugin{{URI: "a-plugin", Deprecation: markerFor("retracted", "recalled")}},
	}
}

// TestDeprecationFindingsCoverEverySectionThatCanRecordAMarker is the coverage
// assertion the report's kind-independence rests on. A report that named the
// sections instead would cover exactly the sections someone remembered, and a
// reference on an unnamed section would be reported by nothing at all — with the
// silence reading as "nothing is deprecated".
//
// The expected count comes from the lockfile's own enumeration of its
// marker-bearing rows rather than from a number written here, so a section added
// to that enumeration is covered without anyone remembering to extend this test.
func TestDeprecationFindingsCoverEverySectionThatCanRecordAMarker(t *testing.T) {
	lf := lockfileWithAMarkerInEverySection("2026-08-01T09:30:00Z")

	var want []string
	for _, row := range lf.VerifiedFactRows() {
		if row.Deprecation() != nil {
			want = append(want, row.URI)
		}
	}
	if len(want) == 0 {
		t.Fatal("the fixture recorded no markers; every assertion below would pass vacuously")
	}

	findings := lf.DeprecationFindings()
	got := make(map[string]bool, len(findings))
	for _, f := range findings {
		got[f.URI] = true
	}
	for _, uri := range want {
		if !got[uri] {
			t.Errorf("the report omits %q — a section that can record a marker is reported by nothing", uri)
		}
	}
	if len(findings) != len(want) {
		t.Errorf("the report yielded %d findings for %d marker-bearing rows: %v", len(findings), len(want), findings)
	}
}

// TestDeprecationFindingsAreSortedByURI pins the determinism the surfaced order
// and every consumer's "first halting finding" rule depend on: the order must not
// depend on which section a row happens to sit in.
func TestDeprecationFindingsAreSortedByURI(t *testing.T) {
	findings := lockfileWithAMarkerInEverySection("2026-08-01T09:30:00Z").DeprecationFindings()
	for i := 1; i < len(findings); i++ {
		if findings[i-1].URI > findings[i].URI {
			t.Fatalf("findings are not sorted by URI at index %d: %q then %q",
				i, findings[i-1].URI, findings[i].URI)
		}
	}
	if len(findings) == 0 || findings[0].URI != "a-plugin" {
		t.Fatalf("the sort must be independent of section order; first finding: %+v", findings)
	}
}

// TestDeprecationFindingsSkipRowsWithNoMarker keeps the report inside the arm that
// owns it: a pinned row with no recorded marker produces nothing, so the
// observation-moment suffix cannot escape onto a row that carries no fact.
func TestDeprecationFindingsSkipRowsWithNoMarker(t *testing.T) {
	lf := &lockfile.Lockfile{
		GeneratedAt: "2026-08-01T09:30:00Z",
		Entities: []lockfile.Entity{
			{URI: "plain"},
			{URI: "marked", Deprecation: markerFor("warning", "superseded")},
		},
	}
	findings := lf.DeprecationFindings()
	if len(findings) != 1 || findings[0].URI != "marked" {
		t.Fatalf("only the marker-bearing row is reported; got %+v", findings)
	}
}

// TestDeprecationFindingsOnAnAbsentLockfileYieldNothing is the case every surface
// that tolerates a missing lockfile depends on. Where there is no lockfile there
// are no recorded markers either, so the report has nothing to say — and it must
// say so by returning nothing rather than by faulting, because a nil receiver
// reaches it from surfaces whose lockfile load is allowed to fail.
func TestDeprecationFindingsOnAnAbsentLockfileYieldNothing(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("an absent lockfile must report nothing, not fault: %v", r)
		}
	}()
	var lf *lockfile.Lockfile
	if findings := lf.DeprecationFindings(); len(findings) != 0 {
		t.Fatalf("an absent lockfile has no recorded markers; got %+v", findings)
	}
	if findings := (&lockfile.Lockfile{}).DeprecationFindings(); len(findings) != 0 {
		t.Fatalf("an empty lockfile has no recorded markers; got %+v", findings)
	}
}

// TestNoticeCarriesTheObservationMoment pins the sentence every surface renders.
// The moment is what stops the report implying a currency an offline read cannot
// provide: a recorded marker states what a registry reported at some past pull.
func TestNoticeCarriesTheObservationMoment(t *testing.T) {
	const stamp = "2026-08-01T09:30:00Z"
	lf := &lockfile.Lockfile{
		GeneratedAt: stamp,
		Entities: []lockfile.Entity{{
			URI:         "oci://reg.example/components/api",
			Deprecation: markerFor("retracted", "withdrawn: bad release"),
		}},
	}
	findings := lf.DeprecationFindings()
	if len(findings) != 1 {
		t.Fatalf("want one finding, got %+v", findings)
	}
	notice := findings[0].Notice()
	for _, want := range []string{
		"oci://reg.example/components/api is deprecated (retracted)",
		"withdrawn: bad release",
		"as of " + stamp,
		"aggrega pull",
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("the notice drops %q:\n%s", want, notice)
		}
	}
}

// TestNoticeWithoutAStampReportsItsAbsence keeps a lockfile carrying no generation
// time from rendering as a real observation in the year 1.
func TestNoticeWithoutAStampReportsItsAbsence(t *testing.T) {
	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{{URI: "api", Deprecation: markerFor("warning", "superseded")}},
	}
	findings := lf.DeprecationFindings()
	if len(findings) != 1 {
		t.Fatalf("want one finding, got %+v", findings)
	}
	notice := findings[0].Notice()
	if strings.Contains(notice, "0001-01-01") {
		t.Errorf("a missing stamp must not render as a zero moment:\n%s", notice)
	}
	if !strings.Contains(notice, "carries no generation time") {
		t.Errorf("a missing stamp must be reported as absent:\n%s", notice)
	}
	if !strings.Contains(notice, "aggrega pull") {
		t.Errorf("the refresh path is stated even with no stamp:\n%s", notice)
	}
}

// TestDeprecationFindingCarriesTheRecordedSeverityVerbatim holds the field a
// caller's halt policy compares against to the recorded string. A report that
// re-classified the severity would let a configured threshold act on something
// other than what the marker recorded.
func TestDeprecationFindingCarriesTheRecordedSeverityVerbatim(t *testing.T) {
	for _, severity := range []string{"warning", "error", "retracted"} {
		lf := &lockfile.Lockfile{
			Entities: []lockfile.Entity{{URI: "api", Deprecation: markerFor(severity, "why")}},
		}
		findings := lf.DeprecationFindings()
		if len(findings) != 1 {
			t.Fatalf("%s: want one finding, got %+v", severity, findings)
		}
		if findings[0].Severity != severity {
			t.Errorf("recorded severity %q surfaced as %q", severity, findings[0].Severity)
		}
		if findings[0].Message != "why" {
			t.Errorf("recorded message surfaced as %q", findings[0].Message)
		}
	}
}

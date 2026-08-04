// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// closureWithRecordedMarker builds the minimal input the build-time gate needs to
// reach its recorded-marker branch: one closure entity, one lockfile row carrying a
// deprecation record, and the lockfile's own generation stamp. KindHint is left
// empty on purpose so the author-time branch short-circuits without a lazy lookup —
// this exercises the recorded-marker arm in isolation.
func closureWithRecordedMarker(uri, severity, message, generatedAt string) (closure.Closure, *lockfile.Lockfile) {
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}
	lf := &lockfile.Lockfile{
		GeneratedAt: generatedAt,
		Entities: []lockfile.Entity{{
			URI: uri,
			Deprecation: &lockfile.DeprecationRecord{
				Severity: severity,
				Message:  message,
			},
		}},
	}
	return cl, lf
}

// TestBuildAdvisoryForRecordedMarkerCarriesTheObservationMoment pins the advisory
// arm: a recorded marker below the halt threshold is reported with the moment the
// lockfile was generated and the command that refreshes it, so the reader can tell
// how far behind the record might be.
func TestBuildAdvisoryForRecordedMarkerCarriesTheObservationMoment(t *testing.T) {
	const stamp = "2026-07-01T09:30:00Z"
	cl, lf := closureWithRecordedMarker("oci://reg.example/components/api", "warning", "superseded by v3", stamp)

	var out bytes.Buffer
	err := enforceClosureDeprecation(cl, lf, nil, workspaceconfig.BuildConfig{HaltOnDeprecatedSeverity: "error"}, &out)
	if err != nil {
		t.Fatalf("advisory severity must not halt the build: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "as of "+stamp) {
		t.Errorf("advisory omits the observation moment %q:\n%s", stamp, got)
	}
	if !strings.Contains(got, "aggrega pull") {
		t.Errorf("advisory omits the refresh path:\n%s", got)
	}
}

// TestBuildHaltForRecordedMarkerCarriesTheObservationMoment pins the halt arm. The
// halt is the surface most at risk of implying currency, because it stops the build
// on a fact that may predate the current registry state by any amount.
func TestBuildHaltForRecordedMarkerCarriesTheObservationMoment(t *testing.T) {
	const stamp = "2026-07-01T09:30:00Z"
	cl, lf := closureWithRecordedMarker("oci://reg.example/components/api", "retracted", "withdrawn: bad release", stamp)

	var out bytes.Buffer
	err := enforceClosureDeprecation(cl, lf, nil, workspaceconfig.BuildConfig{HaltOnDeprecatedSeverity: "error"}, &out)
	if err == nil {
		t.Fatal("a recorded marker at or above the threshold must halt the build")
	}

	got := err.Error()
	if !strings.Contains(got, "as of "+stamp) {
		t.Errorf("halt omits the observation moment %q:\n%s", stamp, got)
	}
	if !strings.Contains(got, "aggrega pull") {
		t.Errorf("halt omits the refresh path:\n%s", got)
	}
	// The moment qualifies the recorded fact, so it must precede the policy clause
	// rather than trail it, where it would read as though the policy were recorded.
	if i, j := strings.Index(got, "as of "+stamp), strings.Index(got, "halt_on_deprecated_severity"); i < 0 || j < 0 || i > j {
		t.Errorf("the observation moment must sit with the record, before the threshold clause:\n%s", got)
	}
}

// TestBuildRecordedMarkerWithoutAStampReportsItsAbsence pins the missing-stamp case:
// a lockfile carrying no generation time says so, and no arm of the gate ever
// renders a zero moment — which would read as a real observation in the year 1.
func TestBuildRecordedMarkerWithoutAStampReportsItsAbsence(t *testing.T) {
	const zeroTime = "0001-01-01"

	for _, tc := range []struct {
		name      string
		severity  string
		wantHalt  bool
		threshold string
	}{
		{name: "advisory", severity: "warning", wantHalt: false, threshold: "error"},
		{name: "halt", severity: "retracted", wantHalt: true, threshold: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cl, lf := closureWithRecordedMarker("oci://reg.example/components/api", tc.severity, "no longer maintained", "")

			var out bytes.Buffer
			err := enforceClosureDeprecation(cl, lf, nil, workspaceconfig.BuildConfig{HaltOnDeprecatedSeverity: tc.threshold}, &out)
			if tc.wantHalt != (err != nil) {
				t.Fatalf("wantHalt=%v, got err=%v", tc.wantHalt, err)
			}

			got := out.String()
			if err != nil {
				got = err.Error()
			}
			if strings.Contains(got, zeroTime) {
				t.Errorf("a missing stamp must not render as a zero moment %q:\n%s", zeroTime, got)
			}
			if !strings.Contains(got, "carries no generation time") {
				t.Errorf("a missing stamp must be reported as absent:\n%s", got)
			}
			if !strings.Contains(got, "aggrega pull") {
				t.Errorf("the refresh path is stated even with no stamp:\n%s", got)
			}
		})
	}
}

// TestBuildEmitsNothingWhenNeitherDeprecationSourceApplies keeps the observation
// moment inside the guard that owns it: an entity with no recorded marker produces
// no notice at all, so the suffix cannot escape the recorded-marker arm by someone
// hoisting the advisory out of its own conditional.
//
// It does NOT cover the author-time metadata.deprecated notice, which needs a
// resolved catalog to reach. That notice deliberately carries no observation moment
// — it is read from the entity during this build, so there is no past moment to
// state — and the asymmetry is enforced by the suffix being applied only inside the
// recorded-marker branch.
func TestBuildEmitsNothingWhenNeitherDeprecationSourceApplies(t *testing.T) {
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: "oci://reg.example/components/api"}}}
	lf := &lockfile.Lockfile{GeneratedAt: "2026-07-01T09:30:00Z"}

	var out bytes.Buffer
	if err := enforceClosureDeprecation(cl, lf, nil, workspaceconfig.BuildConfig{HaltOnDeprecatedSeverity: "error"}, &out); err != nil {
		t.Fatalf("an entity with no deprecation of either kind must not halt: %v", err)
	}
	if got := out.String(); got != "" {
		t.Errorf("no deprecation of either kind must emit nothing, got:\n%s", got)
	}
}

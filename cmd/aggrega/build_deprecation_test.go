// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
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

// closureWithThreeRecordedSeverities builds a closure carrying one entity per
// severity, keyed so the URI sort puts them in warning, error, retracted order.
// It is the input both axes need: what the gate REPORTS is asked of all three at
// once, and what it HALTS on is asked of a walk where the halting entity is not
// the last.
func closureWithThreeRecordedSeverities(generatedAt string) (closure.Closure, *lockfile.Lockfile) {
	const base = "oci://reg.example/components/"
	rows := []struct{ name, severity, message string }{
		{"a", "warning", "superseded by a2"},
		{"b", "error", "no longer fit for new use"},
		{"c", "retracted", "withdrawn"},
	}
	cl := closure.Closure{}
	lf := &lockfile.Lockfile{GeneratedAt: generatedAt}
	for _, r := range rows {
		cl.Entities = append(cl.Entities, closure.ResolvedRef{URI: base + r.name})
		lf.Entities = append(lf.Entities, lockfile.Entity{
			URI: base + r.name,
			Deprecation: &lockfile.DeprecationRecord{
				Severity: r.severity,
				Message:  r.message,
			},
		})
	}
	return cl, lf
}

// TestBuildWithNoThresholdAdvisesEverySeverity pins the default: with no halt
// threshold configured, every recorded severity — retracted included — is
// reported and none of them stops the build. A recorded marker is registry state
// applied after the version was pinned, so a build that succeeded once keeps
// succeeding, loudly advised.
func TestBuildWithNoThresholdAdvisesEverySeverity(t *testing.T) {
	const stamp = "2026-07-01T09:30:00Z"
	cl, lf := closureWithThreeRecordedSeverities(stamp)

	var out bytes.Buffer
	// workspaceconfig.Default() leaves HaltOnDeprecatedSeverity empty, so the
	// zero BuildConfig IS the default configuration.
	if err := enforceClosureDeprecation(cl, lf, nil, workspaceconfig.BuildConfig{}, &out); err != nil {
		t.Fatalf("no configured threshold must halt on nothing; got: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"components/a is deprecated (warning)",
		"components/b is deprecated (error)",
		"components/c is deprecated (retracted)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the notice must cover every severity; missing %q:\n%s", want, got)
		}
	}
	// The observation moment rides every recorded-marker notice, not only the
	// severities that used to fall below a threshold.
	if n := strings.Count(got, "as of "+stamp); n != 3 {
		t.Errorf("every recorded-marker notice must state when the record was observed; got %d of 3:\n%s", n, got)
	}
}

// TestBuildStrictThresholdStillReportsEveryEntity pins the other half of the
// separation: a workspace that configures a halt must not thereby learn LESS.
// The halt is returned after the walk completes, so an entity sorted after the
// halting one keeps its notice — the failure mode a mid-walk return produces
// silently, with nothing in the output to show a report went missing.
func TestBuildStrictThresholdStillReportsEveryEntity(t *testing.T) {
	const stamp = "2026-07-01T09:30:00Z"
	cl, lf := closureWithThreeRecordedSeverities(stamp)

	var out bytes.Buffer
	err := enforceClosureDeprecation(cl, lf, nil, workspaceconfig.BuildConfig{HaltOnDeprecatedSeverity: "error"}, &out)
	if !errors.Is(err, deprecation.ErrDeprecatedEntity) {
		t.Fatalf("a recorded marker at the configured threshold must halt; got: %v", err)
	}
	// The FIRST halting entry in the sorted walk owns the error, so the halt is
	// deterministic rather than dependent on which entry the walk stopped at.
	if !strings.Contains(err.Error(), "components/b is deprecated (error)") {
		t.Errorf("the halt must name the first halting entry in the walk:\n%v", err)
	}

	got := out.String()
	for _, want := range []string{
		"components/a is deprecated (warning)",
		"components/b is deprecated (error)",
		"components/c is deprecated (retracted)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("a configured halt must not cost a notice; missing %q:\n%s", want, got)
		}
	}
}

// TestBuildHaltRendersItsWholeMessageToTheTerminal guards the halt's rendered
// form, not just its error value. The CLI renders a multi-error as its wrapped
// errors alone — so a halt message wrapping TWO errors reaches the terminal as
// the sentinel and the policy clause with everything between them dropped: the
// entity, the severity, the message, and the observation moment. Asserting on
// err.Error() cannot see that, because Go renders the format string there
// regardless. This asserts on what the user actually reads.
func TestBuildHaltRendersItsWholeMessageToTheTerminal(t *testing.T) {
	const stamp = "2026-07-01T09:30:00Z"
	cl, lf := closureWithRecordedMarker("oci://reg.example/components/api", "retracted", "withdrawn: bad release", stamp)

	var out bytes.Buffer
	err := enforceClosureDeprecation(cl, lf, nil, workspaceconfig.BuildConfig{HaltOnDeprecatedSeverity: "retracted"}, &out)
	if err == nil {
		t.Fatal("a recorded marker at the configured threshold must halt the build")
	}

	rendered := formatCompilerError(err)
	for _, want := range []string{
		"components/api",
		"(retracted)",
		"withdrawn: bad release",
		"as of " + stamp,
		"aggrega pull",
		`halt_on_deprecated_severity is "retracted"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendered halt drops %q — the message reached the terminal incomplete:\n%s", want, rendered)
		}
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

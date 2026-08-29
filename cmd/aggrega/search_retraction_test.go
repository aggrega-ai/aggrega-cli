// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
)

// searchRow returns the fields of the result row for uri, failing when the result
// carries no row for it. Screening must never drop a row, so the absence of one is
// itself the failure this reports rather than a condition a caller handles.
func searchRow(t *testing.T, stdout, uri string) []string {
	t.Helper()
	for _, line := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
		if strings.HasPrefix(line, uri+" ") {
			return strings.Fields(line)
		}
	}
	t.Fatalf("no result row for %s in:\n%s", uri, stdout)
	return nil
}

// searchRowVersion returns the version cell of the result row for uri.
func searchRowVersion(t *testing.T, stdout, uri string) string {
	t.Helper()
	fields := searchRow(t, stdout, uri)
	if len(fields) < 3 {
		t.Fatalf("result row for %s has no version cell: %q", uri, fields)
	}
	return fields[2]
}

// TestSearchScreensOnBothConditionsTheSelectionPathUses walks the grid rather than
// its margins: withdrawal severity and a verified signer are two independent
// conditions, and screening must require BOTH. Each row differs from the screened
// case in exactly one condition, so a screen keyed on either one alone fails
// precisely one row.
//
// The direction of the two negative rows matters as much as the positive one. A
// browse surface that screened on an unverified marker would exclude a version the
// product would still select, which is a disagreement in the opposite direction
// from reporting a withdrawn version as the answer.
func TestSearchScreensOnBothConditionsTheSelectionPathUses(t *testing.T) {
	// Not parallel: buildScreenIndex mutates the process-global index-source seam.
	cases := []struct {
		name        string
		severity    deprecation.Severity
		signed      bool
		wantVersion string
		wantNote    bool
	}{
		{
			name:        "withdrawal_severity_and_verified_signer_screens",
			severity:    deprecation.SeverityRetracted,
			signed:      true,
			wantVersion: "1.0.0",
			wantNote:    true,
		},
		{
			name:        "withdrawal_severity_without_a_verified_signer_screens_nothing",
			severity:    deprecation.SeverityRetracted,
			signed:      false,
			wantVersion: "2.0.0",
			wantNote:    false,
		},
		{
			name:        "verified_signer_at_an_advisory_severity_screens_nothing",
			severity:    deprecation.SeverityWarning,
			signed:      true,
			wantVersion: "2.0.0",
			wantNote:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rw := buildScreenIndex(t, map[string]deprecation.Severity{"2.0.0": tc.severity}, tc.signed)
			stdout, _, err := runSearch(t, rw, searchOptions{Output: "table"})
			if err != nil {
				t.Fatalf("searchRun: %v", err)
			}
			uri := indexURIForReference(retractionScreenReference)
			if got := searchRowVersion(t, stdout, uri); got != tc.wantVersion {
				t.Errorf("reported version = %q; want %q\n%s", got, tc.wantVersion, stdout)
			}
			hasNote := strings.Contains(stdout, "retracted: 2.0.0")
			if hasNote != tc.wantNote {
				t.Errorf("row marked = %v; want %v\n%s", hasNote, tc.wantNote, stdout)
			}
			// The notes column is what makes the reported version explicable, so it
			// appears exactly when some row has something to report.
			if hasColumn := strings.Contains(stdout, "NOTES"); hasColumn != tc.wantNote {
				t.Errorf("NOTES column present = %v; want %v\n%s", hasColumn, tc.wantNote, stdout)
			}
		})
	}
}

// TestSearchReportsARepositoryWhoseEveryVersionIsWithdrawn is the case screening
// most naturally gets wrong. An entity that exists and holds no selectable version
// must still be reported, because an empty result is indistinguishable from the
// entity not existing — which is the failure this whole surface change removes.
func TestSearchReportsARepositoryWhoseEveryVersionIsWithdrawn(t *testing.T) {
	// Not parallel: the fixture mutates the process-global index-source seam. The
	// two markers carry distinct reasons because a marker payload is
	// content-addressed and two identical ones would be one blob, not two.
	rw := buildMarkedScreenIndex(t, map[string]deprecation.Marker{
		"1.0.0": {Severity: deprecation.SeverityRetracted, Reason: "withdrawn, unsupported", IssuedOn: "2026-05-01T00:00:00Z"},
		"2.0.0": {Severity: deprecation.SeverityRetracted, Reason: "withdrawn, superseded", IssuedOn: "2026-05-02T00:00:00Z"},
	}, true)
	uri := indexURIForReference(retractionScreenReference)

	t.Run("the_table_reports_it_with_no_version_to_take", func(t *testing.T) {
		stdout, _, err := runSearch(t, rw, searchOptions{Output: "table"})
		if err != nil {
			t.Fatalf("searchRun: %v", err)
		}
		if strings.Contains(stdout, "(no matches)") {
			t.Fatalf("a fully withdrawn entity must not vanish from the result:\n%s", stdout)
		}
		if got := searchRowVersion(t, stdout, uri); got != "(none)" {
			t.Errorf("version cell = %q; want the absence named explicitly\n%s", got, stdout)
		}
		for _, want := range []string{"retracted: 1.0.0, 2.0.0", "no version remains"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("result must say %q; got:\n%s", want, stdout)
			}
		}
	})

	t.Run("the_machine_format_says_the_same_thing", func(t *testing.T) {
		stdout, _, err := runSearch(t, rw, searchOptions{Output: "json"})
		if err != nil {
			t.Fatalf("searchRun: %v", err)
		}
		var doc searchJSON
		if uerr := json.Unmarshal([]byte(stdout), &doc); uerr != nil {
			t.Fatalf("stdout is not one JSON document: %v\n%s", uerr, stdout)
		}
		var found bool
		for _, rec := range doc.Records {
			if rec.URI != uri {
				continue
			}
			found = true
			if rec.Version != "" {
				t.Errorf("version = %q; want empty, since no version is selectable", rec.Version)
			}
			if got := strings.Join(rec.Retracted, ","); got != "1.0.0,2.0.0" {
				t.Errorf("retracted = %v; want both published versions", rec.Retracted)
			}
		}
		if !found {
			t.Fatalf("a fully withdrawn entity must still be reported:\n%s", stdout)
		}
	})
}

// TestSearchIncludeRetractedReportsTheWithdrawnVersion covers the other question a
// reader may be asking — what is published, whatever its status. The flag changes
// only the reported version: the row stays marked, so the answer is never presented
// as though nothing had been withdrawn.
func TestSearchIncludeRetractedReportsTheWithdrawnVersion(t *testing.T) {
	// Not parallel: buildScreenIndex mutates the process-global index-source seam.
	rw := buildScreenIndex(t, map[string]deprecation.Severity{"2.0.0": deprecation.SeverityRetracted}, true)
	uri := indexURIForReference(retractionScreenReference)

	stdout, _, err := runSearch(t, rw, searchOptions{Output: "table", IncludeRetracted: true})
	if err != nil {
		t.Fatalf("searchRun: %v", err)
	}
	if got := searchRowVersion(t, stdout, uri); got != "2.0.0" {
		t.Errorf("reported version = %q; want the withdrawn published version\n%s", got, stdout)
	}
	if !strings.Contains(stdout, "retracted: 2.0.0") {
		t.Errorf("the row must stay marked even when the withdrawn version is reported:\n%s", stdout)
	}

	jsonOut, _, jerr := runSearch(t, rw, searchOptions{Output: "json", IncludeRetracted: true})
	if jerr != nil {
		t.Fatalf("searchRun: %v", jerr)
	}
	var doc searchJSON
	if uerr := json.Unmarshal([]byte(jsonOut), &doc); uerr != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", uerr, jsonOut)
	}
	for _, rec := range doc.Records {
		if rec.URI != uri {
			continue
		}
		if rec.Version != "2.0.0" {
			t.Errorf("version = %q; want the withdrawn published version", rec.Version)
		}
		if len(rec.Retracted) != 1 || rec.Retracted[0] != "2.0.0" {
			t.Errorf("retracted = %v; want it named whichever version is reported", rec.Retracted)
		}
	}
}

// TestSearchWithoutWithdrawalIsShapedExactlyAsBefore pins the non-regression half:
// a result meeting no withdrawal carries neither the notes column nor the retracted
// field, so every existing invocation keeps the output it had.
func TestSearchWithoutWithdrawalIsShapedExactlyAsBefore(t *testing.T) {
	// Not parallel: buildSearchWorkspace mutates the process-global index-source seam.
	rw := buildSearchWorkspace(t)

	stdout, _, err := runSearch(t, rw, searchOptions{Output: "table"})
	if err != nil {
		t.Fatalf("searchRun: %v", err)
	}
	header := strings.SplitN(stdout, "\n", 2)[0]
	if got := strings.Fields(header); len(got) != 4 {
		t.Errorf("header = %v; want the four unchanged columns", got)
	}
	if strings.Contains(stdout, "NOTES") || strings.Contains(stdout, "retracted") {
		t.Errorf("a result with no withdrawal must report none:\n%s", stdout)
	}

	jsonOut, _, jerr := runSearch(t, rw, searchOptions{Output: "json"})
	if jerr != nil {
		t.Fatalf("searchRun: %v", jerr)
	}
	if strings.Contains(jsonOut, "retracted") {
		t.Errorf("a record with no withdrawal must carry no retracted field:\n%s", jsonOut)
	}
}

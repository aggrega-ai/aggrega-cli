// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
)

// TestFindingFromCarriesTextVerbatim pins that the projection copies the
// diagnostic's own text rather than reformatting it. The message is deliberately
// awkward — leading space, embedded newline, quotes, trailing space — because a
// well-meant trim or re-wrap anywhere on the path would be invisible against a
// tidy fixture.
func TestFindingFromCarriesTextVerbatim(t *testing.T) {
	t.Parallel()
	const msg = "  metadata.name \"BadName\" does not match\nthe documented pattern  "
	got := FindingFrom(abi.Diagnostic{
		Severity: abi.SeverityWarn,
		Message:  msg,
		RuleID:   "name-shape",
	}, "component.yaml")

	if got.Message != msg {
		t.Errorf("message was altered on the way into the payload:\n got:  %q\n want: %q", got.Message, msg)
	}
	if got.Severity != abi.SeverityWarn || got.RuleID != "name-shape" {
		t.Errorf("severity/ruleId not carried: %+v", got)
	}
	if got.Location == nil || got.Location.File != "component.yaml" {
		t.Errorf("the linted file must locate the finding; got %+v", got.Location)
	}
}

// TestFindingFromCarriesSpanPosition proves the finer-grained position is
// actually plumbed through, not merely declared.
//
// This matters because no rule in today's publishing-linter populates a
// diagnostic location — every rule body leaves it unset — so an end-to-end run
// cannot exercise this path and the fields would otherwise be a consumer reading
// something always empty. The projection is exercised directly instead: a
// diagnostic carrying a full byte span must surface as a line and column.
func TestFindingFromCarriesSpanPosition(t *testing.T) {
	t.Parallel()
	got := FindingFrom(abi.Diagnostic{
		Severity: abi.SeverityHalt,
		Message:  "located",
		RuleID:   "some-rule",
		Location: &abi.Location{
			KeyPath: "metadata.name",
			Span: &abi.Span{
				StartLine: 4, StartColumn: 9,
				EndLine: 4, EndColumn: 16,
				StartOffset: 42, EndOffset: 49,
			},
		},
	}, "component.yaml")

	if got.Location == nil {
		t.Fatal("a diagnostic carrying a span must produce a location")
	}
	if got.Location.Line != 4 || got.Location.Column != 9 {
		t.Errorf("start position = %d:%d; want 4:9", got.Location.Line, got.Location.Column)
	}
	if got.Location.EndLine != 4 || got.Location.EndColumn != 16 {
		t.Errorf("end position = %d:%d; want 4:16", got.Location.EndLine, got.Location.EndColumn)
	}
	if got.Location.KeyPath != "metadata.name" {
		t.Errorf("keyPath = %q; want metadata.name", got.Location.KeyPath)
	}
}

// TestFindingFromInventsNoPosition is the other half: a diagnostic that located
// nothing must not be reported as located at line zero. A consumer distinguishing
// "position unknown" from "line 0" depends on the fields being absent, so this
// asserts the emitted JSON omits them rather than merely that they are zero.
func TestFindingFromInventsNoPosition(t *testing.T) {
	t.Parallel()
	got := FindingFrom(abi.Diagnostic{
		Severity: abi.SeverityWarn,
		Message:  "unlocated",
		RuleID:   "some-rule",
	}, "component.yaml")

	if got.Location.Line != 0 || got.Location.Column != 0 {
		t.Errorf("a diagnostic with no location must carry no position; got %+v", got.Location)
	}
	enc, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"\"line\"", "\"column\"", "\"endLine\"", "\"endColumn\"", "\"keyPath\""} {
		if strings.Contains(string(enc), key) {
			t.Errorf("payload emits %s for an unlocated finding: %s", key, enc)
		}
	}
	if !strings.Contains(string(enc), "\"file\"") {
		t.Errorf("payload must always carry the file: %s", enc)
	}
}

// TestFindingFromDocumentLineWithoutSpan pins the second location representation:
// a rule that reports a document line without a byte span still located the
// finding, and that line is carried rather than dropped.
func TestFindingFromDocumentLineWithoutSpan(t *testing.T) {
	t.Parallel()
	got := FindingFrom(abi.Diagnostic{
		Severity: abi.SeverityWarn,
		Message:  "structurally located",
		Location: &abi.Location{Line: 12, KeyPath: "spec.schema"},
	}, "component.yaml")

	if got.Location.Line != 12 {
		t.Errorf("line = %d; want 12", got.Location.Line)
	}
	if got.Location.Column != 0 {
		t.Errorf("column = %d; a document line carries no column", got.Location.Column)
	}
}

// TestNewReportSummaryAgreesWithFindings pins that the summary is derived from
// the list rather than tracked alongside it, so the two cannot drift.
func TestNewReportSummaryAgreesWithFindings(t *testing.T) {
	t.Parallel()
	r := NewReport([]Finding{
		{Severity: abi.SeverityHalt, Message: "a"},
		{Severity: abi.SeverityWarn, Message: "b"},
		{Severity: abi.SeverityWarn, Message: "c"},
	}, 2)

	if r.Summary.Findings != 3 || r.Summary.Errors != 1 || r.Summary.Warnings != 2 {
		t.Errorf("summary = %+v; want 3 findings, 1 error, 2 warnings", r.Summary)
	}
	if r.Summary.EntitiesChecked != 2 {
		t.Errorf("entitiesChecked = %d; want 2", r.Summary.EntitiesChecked)
	}
}

// TestReportEmitsEmptyListNotNull pins that a clean run emits an empty array
// rather than null, so a consumer iterates without a nil check.
func TestReportEmitsEmptyListNotNull(t *testing.T) {
	t.Parallel()
	enc, err := json.Marshal(NewReport(nil, 1))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(enc), "\"findings\":[]") {
		t.Errorf("a clean run must emit an empty findings array; got %s", enc)
	}
}

// TestCollectorNilIsInert pins that collection is additive: every collector
// method is safe on a nil receiver, which is what lets the operation call them
// unconditionally while a caller that wants only the human report passes nothing.
func TestCollectorNilIsInert(t *testing.T) {
	t.Parallel()
	var c *FindingCollector
	c.Add([]abi.Diagnostic{{Severity: abi.SeverityHalt, Message: "x"}}, "f.yaml")
	c.Entities(3)
	if c.Reached() {
		t.Error("a nil collector must never report having been reached")
	}
	if r := c.Report(); len(r.Findings) != 0 || r.Summary.EntitiesChecked != 0 {
		t.Errorf("a nil collector must report nothing; got %+v", r)
	}
}

// TestCollectorReachedDistinguishesCleanRunFromNoRun pins the distinction a
// caller depends on to decide whether emitting a report is honest at all: a run
// that produced no findings has still been reached, while a run that failed before
// any rule executed has not.
func TestCollectorReachedDistinguishesCleanRunFromNoRun(t *testing.T) {
	t.Parallel()
	untouched := &FindingCollector{}
	if untouched.Reached() {
		t.Error("an untouched collector must not report having been reached")
	}

	clean := &FindingCollector{}
	clean.Add(nil, "f.yaml")
	clean.Entities(1)
	if !clean.Reached() {
		t.Error("a run that reported zero findings has still been reached")
	}
	if r := clean.Report(); len(r.Findings) != 0 || r.Summary.EntitiesChecked != 1 {
		t.Errorf("clean report = %+v; want zero findings over one entity", r)
	}
}

// TestCollectorAttributesFindingsPerFile pins that a multi-entity run attributes
// each finding to the file it came from, in the order the files were walked —
// which is what makes a whole-catalog report navigable rather than a flat list of
// messages with no provenance.
func TestCollectorAttributesFindingsPerFile(t *testing.T) {
	t.Parallel()
	c := &FindingCollector{}
	c.Add([]abi.Diagnostic{{Severity: abi.SeverityWarn, Message: "first"}}, "a.yaml")
	c.Add([]abi.Diagnostic{{Severity: abi.SeverityWarn, Message: "second"}}, "b.yaml")
	c.Entities(2)

	r := c.Report()
	if len(r.Findings) != 2 {
		t.Fatalf("expected both findings; got %d", len(r.Findings))
	}
	if r.Findings[0].Location.File != "a.yaml" || r.Findings[1].Location.File != "b.yaml" {
		t.Errorf("findings not attributed to their files in walk order: %+v, %+v",
			r.Findings[0].Location, r.Findings[1].Location)
	}
}

// TestNewReportCountsEverySeverity is the arbiter for clause "no finding is
// counted nowhere": the summary carries one count per severity the shared
// diagnostic contract declares, so the three counts sum to the finding total for
// any mix. Before the third count existed an informational finding incremented
// only the total — visible in the list and invisible in the summary — and the gap
// between the two was silent.
func TestNewReportCountsEverySeverity(t *testing.T) {
	t.Parallel()
	r := NewReport([]Finding{
		{Severity: abi.SeverityHalt, Message: "a"},
		{Severity: abi.SeverityWarn, Message: "b"},
		{Severity: abi.SeverityWarn, Message: "c"},
		{Severity: abi.SeverityInfo, Message: "d"},
		{Severity: abi.SeverityInfo, Message: "e"},
		{Severity: abi.SeverityInfo, Message: "f"},
	}, 1)

	if r.Summary.Errors != 1 {
		t.Errorf("errors = %d; want 1", r.Summary.Errors)
	}
	if r.Summary.Warnings != 2 {
		t.Errorf("warnings = %d; want 2", r.Summary.Warnings)
	}
	if r.Summary.Infos != 3 {
		t.Errorf("infos = %d; want 3", r.Summary.Infos)
	}
	if sum := r.Summary.Errors + r.Summary.Warnings + r.Summary.Infos; sum != r.Summary.Findings {
		t.Errorf("counts sum to %d but the run produced %d findings; a finding is counted nowhere", sum, r.Summary.Findings)
	}
}

// TestReportSummaryPublishesEverySeverityCount pins that each count reaches the
// structured surface a caller reads, so a consumer counting by severity does not
// have to re-walk the findings list to recover the one the summary omitted.
func TestReportSummaryPublishesEverySeverityCount(t *testing.T) {
	t.Parallel()
	enc, err := json.Marshal(NewReport([]Finding{{Severity: abi.SeverityInfo, Message: "note"}}, 1))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"errors":0`, `"warnings":0`, `"infos":1`, `"findings":1`} {
		if !strings.Contains(string(enc), want) {
			t.Errorf("structured report is missing %s:\n  %s", want, enc)
		}
	}
}

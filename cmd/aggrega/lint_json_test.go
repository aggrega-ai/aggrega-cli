// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// runLintJSON runs `aggrega lint -o json <entity>` over a workspace rooted at
// root and returns the decoded report, the raw stdout bytes, and the human
// findings report the same invocation wrote to its diagnostic stream.
//
// One invocation produces both renderings — the structured document on stdout and
// the human report on stderr — so the parity assertions below compare two
// renderings of one run rather than two runs that might have seen different
// inputs.
func runLintJSON(t *testing.T, root, entityPath string) (lintops.Report, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	diag := captureStderr(t, func() {
		cmd := newLintCmd()
		cmd.SetOut(&stdout)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetContext(withWorkspaceResolver(context.Background(), root, nil))
		cmd.SetArgs([]string{entityPath, "-o", "json"})
		_ = cmd.Execute()
	})

	var got lintops.Report
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\nstdout:\n%s", err, stdout.String())
	}
	return got, stdout.String(), diag
}

// TestLintJSONEmitsFindingFields pins the structured lint surface's per-finding
// content: a consumer gets a severity, the rule that produced the finding, its
// message, and the entity file it concerns — the four a navigable presentation
// needs. It fails if any one of them stops being emitted.
func TestLintJSONEmitsFindingFields(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	entityPath := filepath.Join(root, "component.yaml")
	writeFixture(t, entityPath, parityBadComponent)

	report, raw, _ := runLintJSON(t, root, entityPath)

	// Non-vacuity: the fixture is chosen because it trips the naming rules. A
	// report with no findings would let every assertion below pass over nothing.
	if len(report.Findings) == 0 {
		t.Fatalf("fixture produced no findings; the field assertions would be vacuous\nstdout:\n%s", raw)
	}
	if report.Summary.Findings != len(report.Findings) {
		t.Errorf("summary counts %d findings, the list carries %d", report.Summary.Findings, len(report.Findings))
	}
	if report.Summary.EntitiesChecked != 1 {
		t.Errorf("entitiesChecked = %d; a single-entity run checks 1", report.Summary.EntitiesChecked)
	}

	for i, f := range report.Findings {
		if f.Severity == "" {
			t.Errorf("finding %d carries no severity", i)
		}
		if f.Message == "" {
			t.Errorf("finding %d carries no message", i)
		}
		if f.RuleID == "" {
			t.Errorf("finding %d carries no ruleId", i)
		}
		if f.Location == nil || f.Location.File == "" {
			t.Errorf("finding %d carries no source location file", i)
			continue
		}
		if f.Location.File != entityPath {
			t.Errorf("finding %d locates %q; want the linted entity %q", i, f.Location.File, entityPath)
		}
	}

	// Severity counts are derived from the findings, so they cannot disagree.
	// This switch deliberately re-derives them rather than calling the production
	// helper: an independent second reading is what makes the comparison able to
	// fail, so the duplication is the point and must not be unified away.
	var errs, warns int
	for _, f := range report.Findings {
		switch f.Severity {
		case abi.SeverityHalt:
			errs++
		case abi.SeverityWarn:
			warns++
		}
	}
	if report.Summary.Errors != errs || report.Summary.Warnings != warns {
		t.Errorf("summary errors/warnings = %d/%d; the findings carry %d/%d",
			report.Summary.Errors, report.Summary.Warnings, errs, warns)
	}
}

// advisoryRow reconstructs the human report's row for one non-halting finding:
// the severity it carries, the short code of the condition naming its rule, the
// finding's identity — the rule's own identifier, or the finding's own code where
// it names no rule — and the rule's message. A colon closes the identifiers where
// there is at least one; a finding carrying neither reads as its severity and
// then its message.
//
// 🔴 This is a DELIBERATE near-duplicate of the row the operation renders, and
// unifying the two would destroy the test: an independently written second
// reading is the whole reason the comparison below is able to fail. The
// operation's renderer is unexported in another package, so the duplication is
// also structural — but it is recorded as deliberate here so that a later pass
// exporting the renderer does not read this as an oversight to tidy away.
//
// The linter guest assigns no code of its own, so the short code on the row is
// the host's, registered under the rule's own identifier. It is read back here
// through the registry's by-code lookup rather than off the structured finding,
// which carries it too, and rather than copied into a table here: an independent
// reading is what lets the row comparison fail on a code the rule's condition
// does not declare, and a rule added to the set needs no edit in this file.
// [TestLintJSONCarriesEachFindingsShortCode] holds the structured finding's own
// short code to the same reading.
func advisoryRow(f lintops.Finding) string {
	row := f.Severity + ":"
	short := registeredShortCode(f.RuleID)
	if short != "" {
		row += " " + short
	}
	identity := f.RuleID
	if identity == "" {
		identity = f.Code
	}
	if identity != "" {
		row += " " + identity
	}
	if short != "" || identity != "" {
		row += ":"
	}
	return row + " " + f.Message
}

// registeredShortCode reads the short code of the condition registered under a
// rule's identifier through the registry's by-code lookup, or returns empty
// where no condition is registered under it.
func registeredShortCode(ruleID string) string {
	if condition, ok := cerrors.ConditionByCode(ruleID); ok {
		return condition.ShortCode
	}
	return ""
}

// TestLintJSONCarriesEachFindingsShortCode pins the short code on the structured
// surface: each finding carries, under its own key, the short code of the
// condition its rule names — the code printed at the head of its row in the
// human report — and carries no such key where its rule names no condition, as
// its row then prints none.
//
// The key is read off the document as a client decodes it rather than through
// the report type, so a renamed key fails here instead of round-tripping through
// one struct tag. The expected code is read through the registry's by-code
// lookup, independently of the lookup the operation fills the finding from,
// which is what lets the comparison fail.
func TestLintJSONCarriesEachFindingsShortCode(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	entityPath := filepath.Join(root, "component.yaml")
	writeFixture(t, entityPath, parityBadComponent)

	_, raw, humanReport := runLintJSON(t, root, entityPath)

	var doc struct {
		Findings []map[string]any `json:"findings"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\nstdout:\n%s", err, raw)
	}

	quoted := 0
	for i, entry := range doc.Findings {
		ruleID, _ := entry["ruleId"].(string)
		got, present := entry["shortCode"]
		want := registeredShortCode(ruleID)
		if want == "" {
			if present {
				t.Errorf("finding %d (rule %q) names no registered condition and carries shortCode %v; its "+
					"row prints none", i, ruleID, got)
			}
			continue
		}
		if got != want {
			t.Errorf("finding %d (rule %q) carries shortCode %v (present %v); the condition registered under "+
				"the rule declares %q", i, ruleID, got, present, want)
			continue
		}
		// The row the same run printed for this finding carries the same code at
		// its head, ahead of the rule's identity.
		if !strings.Contains(humanReport, " "+want+" "+ruleID+": ") {
			t.Errorf("finding %d carries shortCode %q and no row in the human report prints it ahead of %q:\n%s",
				i, want, ruleID, humanReport)
		}
		quoted++
	}
	// Non-vacuity: the fixture trips the naming rules, which are registered, so a
	// document in which no finding carried a short code proves nothing.
	if quoted == 0 {
		t.Fatalf("no finding carried a short code to compare; this fixture must draw at least one finding "+
			"whose rule names a registered condition\nstdout:\n%s", raw)
	}
}

// TestLintJSONCarriesDiagnosticTextVerbatim is the single-source assertion: the
// message on the structured surface is the rule's own diagnostic text, not a
// re-worded or truncated copy of it. It reconstructs the human report's row for
// each finding and requires that exact row to appear in the human output of the
// same run — so a paraphrase, a trim, or a re-wrap on either side fails here.
//
// The invocation carries no pre-publish flag, and in that mode every rule's
// output is reported at the advisory severity, so every finding this fixture
// produces is a row in the human report and the comparison has one arm.
//
// A halting finding reaches a reader by an entirely different route: it becomes
// the run's returned error, whose head the terminal composes, and nothing writes
// it to the diagnostic stream this run captures. One arriving here is therefore a
// signal to point a second assertion at that route — never to loosen this one
// into something both routes satisfy, which would leave neither surface checked
// against the other. The loop says so at the place it would happen.
func TestLintJSONCarriesDiagnosticTextVerbatim(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	entityPath := filepath.Join(root, "component.yaml")
	writeFixture(t, entityPath, parityBadComponent)

	report, raw, humanReport := runLintJSON(t, root, entityPath)

	if len(report.Findings) == 0 {
		t.Fatalf("fixture produced no findings; parity would be vacuous\nstdout:\n%s", raw)
	}
	if strings.TrimSpace(humanReport) == "" {
		t.Fatalf("the run wrote no human findings report; there is nothing to compare against")
	}

	for i, f := range report.Findings {
		if f.Severity == abi.SeverityHalt {
			t.Errorf("finding %d halted, so its text reaches a reader as the run's returned error "+
				"rather than as a row in the report this run captured. Point a second assertion at "+
				"that error; do not relax the row comparison to span both.\n finding: %+v", i, f)
			continue
		}
		row := advisoryRow(f)
		if !strings.Contains(humanReport, row) {
			t.Errorf("structured finding %d does not reproduce the human row byte for byte.\n"+
				" reconstructed: %q\n human report:\n%s", i, row, humanReport)
		}
	}
}

// TestLintJSONStdoutCarriesExactlyOneDocument pins that the structured surface
// leaves stdout clean: the operation's own confirmation and summary lines are
// routed off it, so a consumer can decode stdout whole. A stray human line would
// make the stream undecodable, which is the failure this guards.
func TestLintJSONStdoutCarriesExactlyOneDocument(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	entityPath := filepath.Join(root, "component.yaml")
	writeFixture(t, entityPath, parityBadComponent)

	_, raw, _ := runLintJSON(t, root, entityPath)

	if !strings.HasPrefix(raw, "{") {
		t.Errorf("stdout does not begin with the document; got:\n%s", raw)
	}
	if !strings.HasSuffix(raw, "}\n") {
		t.Errorf("stdout does not end with the document and one newline; got:\n%q", raw)
	}
	// A second document, or a human line after the first, would show up as a
	// decode failure on the whole buffer — which runLintJSON already asserts — but
	// pin the trailing-newline count too, since the seam promises exactly one.
	if n := strings.Count(raw, "\n}"); n != 1 {
		t.Errorf("stdout carries %d closing braces at line start; want exactly one document", n)
	}
}

// TestLintRejectsUnknownOutputValue pins that the format value is validated
// through the shared rejection rather than silently falling back to the human
// report, which would let a typo look like a command with no findings.
func TestLintRejectsUnknownOutputValue(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	entityPath := filepath.Join(root, "component.yaml")
	writeFixture(t, entityPath, parityBadComponent)

	var out bytes.Buffer
	cmd := newLintCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(withWorkspaceResolver(context.Background(), root, nil))
	cmd.SetArgs([]string{entityPath, "-o", "yaml"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("an unrecognized --output value must be refused; got nil error, output:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "unknown --output value") {
		t.Errorf("expected the shared rejection wording; got %v", err)
	}
}

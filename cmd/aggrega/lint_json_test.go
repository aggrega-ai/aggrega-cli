// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
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
		case "halt":
			errs++
		case "warn":
			warns++
		}
	}
	if report.Summary.Errors != errs || report.Summary.Warnings != warns {
		t.Errorf("summary errors/warnings = %d/%d; the findings carry %d/%d",
			report.Summary.Errors, report.Summary.Warnings, errs, warns)
	}
}

// TestLintJSONCarriesDiagnosticTextVerbatim is the single-source assertion: the
// message on the structured surface is the rule's own diagnostic text, not a
// re-worded or truncated copy of it. It reconstructs the human report's line from
// the structured fields and requires that exact line to appear in the human
// output of the same run — so a paraphrase, a trim, or a re-wrap on either side
// fails here.
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

	for _, f := range report.Findings {
		line := fmt.Sprintf("[%s] %s: %s", f.Severity, f.RuleID, f.Message)
		if !strings.Contains(humanReport, line) {
			t.Errorf("structured finding does not reproduce the human line byte for byte.\n"+
				" reconstructed: %q\n human report:\n%s", line, humanReport)
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

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/importops"
)

// demoEchoBody is the body the demo importer echoes for the "demo" format — one
// drafted Component whose name is the requested format — and the content of the
// matching scaffold file. It mirrors the guest at aggrega-pdk/go/examples/import.
const demoEchoBody = "kind: Component\nmetadata:\n  name: demo\n"

// The operation itself — format selection, the importer seam, the
// consumer-scoped attachment inputs, the write and its containment refusal — is
// covered where it lives, in internal/importops. What is covered here is what
// stayed behind: the command's own rendering of a result, and its exit outcome.
//
// 🔴 That split is deliberate rather than incidental. Moving a subsystem moves
// its tests with it, which leaves the CALL SITE uncovered while every moved test
// passes, so the renderings below exist to cover the half that did not move.

// TestWriteImportStdoutRendersTheDeterministicConcatenation pins the standard-
// output form: a "# path" header per file followed by its content, in the order
// the importer returned them, with a supplied newline where the content lacks
// one so consecutive files stay separated.
//
// It is a separate path from the write, so a green on the write says nothing
// about it.
func TestWriteImportStdoutRendersTheDeterministicConcatenation(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := writeImportStdout(&out, []abi.ScaffoldFile{
		{Path: "demo.yaml", Content: demoEchoBody},
		{Path: "nested/second.yaml", Content: "kind: Feature\n"},
		{Path: "no-trailing-newline.yaml", Content: "bare"},
	})
	if err != nil {
		t.Fatalf("writeImportStdout: %v", err)
	}
	want := "# demo.yaml\n" + demoEchoBody +
		"# nested/second.yaml\nkind: Feature\n" +
		"# no-trailing-newline.yaml\nbare\n"
	if out.String() != want {
		t.Fatalf("writeImportStdout = %q; want %q", out.String(), want)
	}
}

// TestWriteImportStdoutEmitsNothingForAnEmptySet keeps the empty case honest: an
// import that drafted nothing prints nothing rather than a stray header.
func TestWriteImportStdoutEmitsNothingForAnEmptySet(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := writeImportStdout(&out, nil); err != nil {
		t.Fatalf("writeImportStdout: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("writeImportStdout(nil) wrote %q; want nothing", out.String())
	}
}

// TestReportImportDiagnosticsRendersSeverityAndMessage pins the terminal's
// diagnostic line and its order, which is what makes an unresolved node visible
// rather than silently dropped.
func TestReportImportDiagnosticsRendersSeverityAndMessage(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	reportImportDiagnostics(&errOut, []abi.Diagnostic{
		{Severity: "warn", Message: "one node was not mapped"},
		{Severity: abi.SeverityHalt, Message: "an edge referenced an unknown node"},
	})
	want := "warn: one node was not mapped\n" +
		string(abi.SeverityHalt) + ": an edge referenced an unknown node\n"
	if errOut.String() != want {
		t.Fatalf("reportImportDiagnostics = %q; want %q", errOut.String(), want)
	}
}

// TestImportDiagnosticsErrorGovernsOnlyTheExitOutcome pins the command's exit
// rule: a halt-severity diagnostic exits non-zero, anything less exits zero, and
// the sentence points the reader at the messages already on stderr rather than
// repeating them.
func TestImportDiagnosticsErrorGovernsOnlyTheExitOutcome(t *testing.T) {
	t.Parallel()

	halted := importops.Result{Diagnostics: []abi.Diagnostic{
		{Severity: abi.SeverityHalt, Message: "an edge referenced an unknown node"},
	}}
	err := importDiagnosticsError(halted)
	if err == nil {
		t.Fatal("a halt-severity diagnostic must make the command exit non-zero")
	}
	if !strings.Contains(err.Error(), "see the messages above") {
		t.Fatalf("the exit sentence must point at the diagnostics already printed; got %q", err.Error())
	}

	warned := importops.Result{Diagnostics: []abi.Diagnostic{{Severity: "warn", Message: "one node was not mapped"}}}
	if err := importDiagnosticsError(warned); err != nil {
		t.Fatalf("a warn-only import must exit zero; got %v", err)
	}
	if err := importDiagnosticsError(importops.Result{}); err != nil {
		t.Fatalf("a clean import must exit zero; got %v", err)
	}
}

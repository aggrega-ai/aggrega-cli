// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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

// TestReportedImportedFilesRoundTripThroughTheSharedForm pins the reported form
// by reading it back: every drafted file's path and content survive the trip,
// in the importer's own order.
//
// 🔴 The assertion is a round trip rather than a pinned byte string, and the
// reason is the property being claimed. What matters is that a caller can
// RECOVER the set — which the framing this replaced could not do at all, since a
// file whose own first line began with the header marker was indistinguishable
// from the start of the next file. The third row below is exactly that case, and
// it is the row a header-per-file concatenation cannot get right. A pinned byte
// string would also make this test a golden over the encoder's choice of block
// scalar, which library drift moves without moving anything a caller observes.
//
// It is a separate path from the write, so a green on the write says nothing
// about it.
func TestReportedImportedFilesRoundTripThroughTheSharedForm(t *testing.T) {
	t.Parallel()

	files := []abi.ScaffoldFile{
		{Path: "demo.yaml", Content: demoEchoBody},
		{Path: "nested/second.yaml", Content: "kind: Feature\n"},
		{Path: "leads-with-the-old-header-marker.yaml", Content: "# demo.yaml\nkind: Feature\n"},
		{Path: "no-trailing-newline.yaml", Content: "bare"},
	}

	var out bytes.Buffer
	if err := reportImportedFiles(&out, files); err != nil {
		t.Fatalf("reportImportedFiles: %v", err)
	}

	docs, err := authoredcontent.Decode(out.Bytes())
	if err != nil {
		t.Fatalf("decode the reported answer: %v\n%s", err, out.String())
	}
	if len(docs) != len(files) {
		t.Fatalf("reported %d documents for %d drafted files:\n%s", len(docs), len(files), out.String())
	}
	for i, f := range files {
		if docs[i].Path != f.Path {
			t.Errorf("document %d names %q; the importer drafted %q, and the order is the importer's",
				i, docs[i].Path, f.Path)
		}
		if docs[i].Content != f.Content {
			t.Errorf("document %d for %s carries %q; want %q", i, f.Path, docs[i].Content, f.Content)
		}
	}

	// The stream is exactly one report and nothing else, asserted by re-encoding
	// what was decoded and comparing bytes. A decode alone cannot make this claim:
	// prose beside the documents key still decodes.
	var reencoded bytes.Buffer
	if err := authoredcontent.Report(&reencoded, docs); err != nil {
		t.Fatalf("re-encode the decoded report: %v", err)
	}
	if reencoded.String() != out.String() {
		t.Errorf("the reported stream is not exactly one report:\nstream:\n%s\nthe report alone:\n%s",
			out.String(), reencoded.String())
	}
}

// TestReportedImportedFilesReportTheEmptySetForAnEmptyImport keeps the empty
// case honest: an import that drafted nothing reports the empty document set
// rather than an empty stream, so a caller can tell "nothing was drafted" from a
// truncated answer.
func TestReportedImportedFilesReportTheEmptySetForAnEmptyImport(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := reportImportedFiles(&out, nil); err != nil {
		t.Fatalf("reportImportedFiles: %v", err)
	}
	docs, err := authoredcontent.Decode(out.Bytes())
	if err != nil {
		t.Fatalf("decode the reported answer: %v\n%s", err, out.String())
	}
	if len(docs) != 0 {
		t.Fatalf("reported %d documents for an import that drafted none:\n%s", len(docs), out.String())
	}
	if out.Len() == 0 {
		t.Fatal("reported an empty stream; a caller cannot tell that from a truncated answer")
	}
}

// TestReportImportPreambleNamesBothFactsFromTheResult pins the terminal's
// framing line against the shared result's own fields: the format the import
// ran for, and the root the drafted paths are relative to. Neither is
// recoverable from the drafted output, and the format is not recoverable at all
// when --format is omitted and the shape decides it.
//
// 🔴 The expectation is derived from the result's fields rather than restated as
// a literal, and the rows vary BOTH fields independently. One row would pass on
// a renderer that printed constants and read neither field; rows varying one
// field would pass on a renderer that had stopped reading the other.
//
// The root is printed as written, the way the command line prints every
// location it reports. The third row is a Windows path: quoting it would double
// each separator, so a renderer that quoted fails there as well as on the
// others.
//
// 🔴 PLANTED DEFECT: print the root with %q in reportImportPreamble
// (cmd/aggrega/import.go) and every row reddens.
func TestReportImportPreambleNamesBothFactsFromTheResult(t *testing.T) {
	t.Parallel()

	for _, res := range []importops.Result{
		{Format: "demo", OutputDir: "/srv/workspace"},
		{Format: "langflow", OutputDir: "/srv/workspace/drafts/generated"},
		{Format: "flowise", OutputDir: `C:\Users\author\workspace`},
	} {
		var errOut bytes.Buffer
		reportImportPreamble(&errOut, res)
		want := "read as the " + res.Format + " format, with drafted paths relative to " +
			res.OutputDir + "\n"
		if errOut.String() != want {
			t.Fatalf("reportImportPreamble(%+v) = %q; want %q", res, errOut.String(), want)
		}
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
//
// The refusal is matched through the condition's accessor rather than against
// its wording, so re-wording the registered sentence does not silently stop this
// from covering anything.
func TestImportDiagnosticsErrorGovernsOnlyTheExitOutcome(t *testing.T) {
	t.Parallel()

	halted := importops.Result{Diagnostics: []abi.Diagnostic{
		{Severity: abi.SeverityHalt, Message: "an edge referenced an unknown node"},
	}}
	err := halted.Refusal()
	if err == nil {
		t.Fatal("a halt-severity diagnostic must make the command exit non-zero")
	}
	if !errors.Is(err, importops.ErrImportHalted()) {
		t.Fatalf("the refusal must name the registered condition; got %v", err)
	}
	if code, ok := cerrors.ShortCodeOf(err); !ok || code == "" {
		t.Fatalf("the refusal must carry a short code a reader can quote; got %q, %v", code, ok)
	}
	if !strings.Contains(err.Error(), "reported above") {
		t.Fatalf("the exit sentence must point at the diagnostics already printed; got %q", err.Error())
	}

	warned := importops.Result{Diagnostics: []abi.Diagnostic{{Severity: "warn", Message: "one node was not mapped"}}}
	if err := warned.Refusal(); err != nil {
		t.Fatalf("a warn-only import must exit zero; got %v", err)
	}
	if err := (importops.Result{}).Refusal(); err != nil {
		t.Fatalf("a clean import must exit zero; got %v", err)
	}
}

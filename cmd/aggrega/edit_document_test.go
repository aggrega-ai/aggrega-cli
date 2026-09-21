// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/editops"
)

// The edit itself — where an entry goes, how it is rendered, the splice, and the
// validation the write is gated on — is covered where it lives, in
// internal/editops. What is covered here is what stayed behind: this surface's
// own resolution of a value from the characters a caller typed, its rendering of
// the two writing outcomes, and the delivery it selects.
//
// 🔴 That split is deliberate. A subsystem's tests follow the subsystem, which
// leaves the CALL SITE uncovered while every moved test passes, so the cases
// below exist to cover the half that did not move.

// authoredAggregateForEdit is the fixture the edits below are applied to. It
// carries what a full re-serialize destroys — a leading comment block, a
// hand-aligned trailing comment, a blank line inside a block, a literal block
// scalar indented deeper than its key, a single-quoted scalar, and a template
// expression that only survives as text — so an edit that reformatted the
// document would fail the preservation assertion rather than passing it.
const authoredAggregateForEdit = `# The storefront application.
#
# The ordering below is deliberate: the web tier reads first.
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: storefront        # the module's own name
  version: 1.4.2
spec:
  parameters:
    db_size:
      type: string
      default: '10Gi'

  components:

    web:
      type: core/api-server:1.0.0
      description: |
        The public web tier.
          This line is indented inside a literal block.
      values:
        replicas: 3
      uses:
        database:
          primary:
            target: db

    # The data tier is intentionally last.
    db:
      type: core/postgres:2.1.0
      values:
        size: '{{ .parameters.db_size }}'
`

// authoredReleaseForEdit is the Release-side fixture. The authoring schema
// gives a Release's component surface operator concerns only — no developer
// values — so it is the document the invariant refusal is driven against.
const authoredReleaseForEdit = `# The production deployment.
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: storefront-prod
  environment: production
  version: 1.4.2
spec:
  aggregate: storefront
  manifest: prod-manifest
  components:
    web:
      infra:
        replicas: 5
`

// editDocumentWorkspace lays a fixture down in an isolated workspace and returns
// the resolved workspace, the document's workspace-relative name, and its
// absolute path.
func editDocumentWorkspace(t *testing.T, rel, content string) (ws *workspaceForEdit, absPath string) {
	t.Helper()
	root := t.TempDir()
	abs := filepath.Join(root, rel)
	writeFixture(t, abs, content)
	return &workspaceForEdit{root: root, rel: rel}, abs
}

// workspaceForEdit carries the two coordinates every case below needs, so each
// case names the document once.
type workspaceForEdit struct {
	root string
	rel  string
}

// TestEditingADocumentFromTheCommandLineLandsTheEntryAndPreservesEveryOtherByte
// drives the command's own entry point and asserts against the bytes on disk
// rather than against the returned value, because what a caller inspects
// afterwards is the file.
func TestEditingADocumentFromTheCommandLineLandsTheEntryAndPreservesEveryOtherByte(t *testing.T) {
	t.Parallel()
	ws, abs := editDocumentWorkspace(t, "storefront.aggregate.yaml", authoredAggregateForEdit)

	var out bytes.Buffer
	err := editDocumentRun(t.Context(), editDocumentConfig{
		Edit:       "add-component",
		File:       ws.rel,
		Node:       []string{"spec", "components"},
		Name:       "checkout",
		Value:      "core/api-server:1.0.0",
		ValueGiven: true,
		ws:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws.root}),
		Out:        &out,
	})
	if err != nil {
		t.Fatalf("editDocumentRun: %v", err)
	}
	// The first line is pinned exactly, and only the first: what a caller has to
	// be told here is which file reached disk. The placement sentences that
	// follow it are held to the shared operation's own result elsewhere, so
	// pinning the whole stream here would be a second, weaker statement of that
	// parity — one that moves with this surface rather than against it.
	if got := firstLineOf(out.String()); got != "wrote storefront.aggregate.yaml" {
		t.Errorf("the writing outcome opens with %q; a caller has to be told which file reached disk", got)
	}

	onDisk := readFileForEdit(t, abs)
	if !strings.Contains(onDisk, "    checkout:\n      type: core/api-server:1.0.0\n") {
		t.Errorf("the entry did not land\n--- document ---\n%s", onDisk)
	}

	// Preservation is asserted by requiring the specific lines the author wrote
	// to survive. A command that re-serialized the document would still land the
	// entry, so an assertion about the entry alone would pass for it.
	for _, line := range []string{
		"# The storefront application.\n",
		"  name: storefront        # the module's own name\n",
		"          This line is indented inside a literal block.\n",
		"    # The data tier is intentionally last.\n",
		"        size: '{{ .parameters.db_size }}'\n",
		"\n  components:\n\n",
	} {
		if !strings.Contains(onDisk, line) {
			t.Errorf("the edit did not preserve %q\n--- document ---\n%s", line, onDisk)
		}
	}
}

// TestAnEditTheSchemaRefusesLeavesTheFileByteIdentical is the invariant proof at
// this surface.
//
// The assertion is byte-identity of the file rather than the presence of an
// error, and that is the half that matters: a command which composed the
// document, wrote it, and then reported a failure would satisfy an
// error-is-non-nil assertion and fail this one.
func TestAnEditTheSchemaRefusesLeavesTheFileByteIdentical(t *testing.T) {
	t.Parallel()
	ws, abs := editDocumentWorkspace(t, "prod.release.yaml", authoredReleaseForEdit)
	before := readFileForEdit(t, abs)

	var out bytes.Buffer
	err := editDocumentRun(t.Context(), editDocumentConfig{
		Edit:       "set-value",
		File:       ws.rel,
		Node:       []string{"spec", "components", "web"},
		Name:       "replicas",
		Value:      "9",
		ValueGiven: true,
		ws:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws.root}),
		Out:        &out,
	})
	if err == nil {
		t.Fatal("a Release document asked to carry a developer value must be refused")
	}
	if !errorsIsEditRefused(err) {
		t.Errorf("the refusal does not name the invalid-result condition: %v", err)
	}
	if after := readFileForEdit(t, abs); after != before {
		t.Errorf("the refusal wrote to the file\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	if out.Len() != 0 {
		t.Errorf("a refused edit wrote %q to the answer stream; nothing landed, so there is nothing to report", out.String())
	}
}

// TestTheReleaseFixtureAcceptsALegitimateEditFirst is the positive control for
// the refusal above.
//
// Without it the refusal could be reported for a document that was never valid,
// and the case above would pass while proving nothing about the edit. It drives
// an operator concern the Release surface does carry and requires it to land.
func TestTheReleaseFixtureAcceptsALegitimateEditFirst(t *testing.T) {
	t.Parallel()
	ws, abs := editDocumentWorkspace(t, "prod.release.yaml", authoredReleaseForEdit)

	err := editDocumentRun(t.Context(), editDocumentConfig{
		Edit:       "attach-feature",
		File:       ws.rel,
		Node:       []string{"spec", "components", "web"},
		Name:       "budget-alert",
		Value:      "core/finops/budget-alert:1.0.0",
		ValueGiven: true,
		ws:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws.root}),
		Out:        &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("a legitimate Release-side edit was refused: %v", err)
	}
	if on := readFileForEdit(t, abs); !strings.Contains(on, "type: core/finops/budget-alert:1.0.0\n") {
		t.Errorf("the attachment did not land\n--- document ---\n%s", on)
	}
}

// TestEditingADocumentReportsContentAndWritesNothing drives the reporting
// delivery and holds it to the filesystem, then requires the writing delivery to
// put exactly the reported bytes on disk.
//
// The tree is fingerprinted by content digest over the whole workspace, so a
// reporting delivery that rewrote a file in place — preserving its size and
// timestamp — is caught rather than passing a size comparison.
func TestEditingADocumentReportsContentAndWritesNothing(t *testing.T) {
	t.Parallel()
	ws, abs := editDocumentWorkspace(t, "storefront.aggregate.yaml", authoredAggregateForEdit)
	resolved := resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws.root})

	edit := func(dryRun bool, out *bytes.Buffer) error {
		return editDocumentRun(t.Context(), editDocumentConfig{
			Edit:       "attach-feature",
			File:       ws.rel,
			Node:       []string{"spec", "components", "web"},
			Name:       "tls",
			Value:      "core/tls:1.0.0",
			ValueGiven: true,
			DryRun:     dryRun,
			ws:         resolved,
			Out:        out,
		})
	}

	before := treeFingerprint(t, ws.root)

	var reported bytes.Buffer
	if err := edit(true, &reported); err != nil {
		t.Fatalf("editing under the reporting delivery: %v", err)
	}
	if diffs := fingerprintDifferences(before, treeFingerprint(t, ws.root)); len(diffs) != 0 {
		t.Fatalf("the reporting delivery touched the filesystem:\n  %s", strings.Join(diffs, "\n  "))
	}

	docs := reportedDocuments(t, reported.Bytes())
	if len(docs) != 1 {
		t.Fatalf("the edit reported %d documents; one edit produces one document", len(docs))
	}
	if docs[0].Path != ws.rel {
		t.Errorf("the reported document names %q; the edited document is %q", docs[0].Path, ws.rel)
	}

	if err := edit(false, &bytes.Buffer{}); err != nil {
		t.Fatalf("editing under the writing delivery: %v", err)
	}
	if onDisk := readFileForEdit(t, abs); onDisk != docs[0].Content {
		t.Errorf("the written bytes differ from the reported content\n--- reported ---\n%s\n--- written ---\n%s",
			docs[0].Content, onDisk)
	}
}

// TestTheDefaultDeliveryWrites pins the default, which is the property most
// likely to be changed later by someone who reads a reporting mode as the safer
// one.
//
// All three rows are asserted rather than only the absent one: a change that
// made reporting the default would leave the "explicitly false" row passing, so
// the absent row is the one carrying the claim and it is stated beside the other
// two so the difference is visible.
func TestTheDefaultDeliveryWrites(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		report  bool
		reports bool
	}{
		{"a request that says nothing about delivery writes", false, false},
		{"a request that asks to be reported to reports", true, true},
		{"a request that explicitly declines reporting writes", false, false},
	} {
		if got := authoredcontent.DeliveryFor(tc.report).Reports(); got != tc.reports {
			t.Errorf("%s: Reports() = %v, want %v", tc.name, got, tc.reports)
		}
	}

	// And the same claim at this surface, driven to the filesystem: with no
	// delivery stated the document is edited on disk.
	ws, abs := editDocumentWorkspace(t, "storefront.aggregate.yaml", authoredAggregateForEdit)
	before := readFileForEdit(t, abs)
	if err := editDocumentRun(t.Context(), editDocumentConfig{
		Edit:       "set-value",
		File:       ws.rel,
		Node:       []string{"spec", "components", "web"},
		Name:       "workers",
		Value:      "4",
		ValueGiven: true,
		ws:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws.root}),
		Out:        &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("editDocumentRun: %v", err)
	}
	if readFileForEdit(t, abs) == before {
		t.Error("a request stating no delivery left the document unchanged; the default is the write")
	}
}

// TestAValueKeepsTheTypeItsCharactersCarry pins the scalar resolution, which is
// what keeps this surface from narrowing the capability to strings.
//
// A command line carries only characters, so without resolving them the surface
// could set only strings while every other surface can set a number, a boolean
// or nothing at all. The rows include the quoted forms, because those are how a
// caller says "this is text" — the same way they would in the document.
func TestAValueKeepsTheTypeItsCharactersCarry(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		text  string
		given bool
		want  any
	}{
		{"an unquoted integer is a number", "4", true, 4},
		{"an unquoted boolean is a boolean", "true", true, true},
		{"a quoted integer is text", `"7"`, true, "7"},
		{"a quoted boolean is text", `"true"`, true, "true"},
		{"a bare word is text", "core/api-server:1.0.0", true, "core/api-server:1.0.0"},
		{"an explicit null is nothing", "null", true, nil},
		{"an omitted value is nothing at all", "", false, nil},
		{"an empty value is nothing, as an empty document is", "", true, nil},
	} {
		got, err := editDocumentValue(tc.text, tc.given)
		if err != nil {
			t.Errorf("%s: editDocumentValue(%q, %v) errored: %v", tc.name, tc.text, tc.given, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: editDocumentValue(%q, %v) = %#v, want %#v", tc.name, tc.text, tc.given, got, tc.want)
		}
	}
}

// TestAValueThatIsNotAScalarIsRefusedRatherThanFlattened keeps the resolution
// honest at its edge: characters that parse as a collection are not silently
// turned into a string, because writing a block is outside the edit set and a
// flattened one would land as a value nobody typed.
func TestAValueThatIsNotAScalarIsRefusedRatherThanFlattened(t *testing.T) {
	t.Parallel()
	ws, abs := editDocumentWorkspace(t, "storefront.aggregate.yaml", authoredAggregateForEdit)
	before := readFileForEdit(t, abs)

	err := editDocumentRun(t.Context(), editDocumentConfig{
		Edit:       "set-value",
		File:       ws.rel,
		Node:       []string{"spec", "components", "web"},
		Name:       "limits",
		Value:      "{cpu: 2, memory: 4Gi}",
		ValueGiven: true,
		ws:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws.root}),
		Out:        &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("a mapping given where a scalar belongs must be refused; adding a block is not one of the edits")
	}
	if after := readFileForEdit(t, abs); after != before {
		t.Error("the refusal wrote to the file")
	}
}

// TestAnEditNamingNoKnownKindIsRefused holds the bound at this surface: the
// admissible set is the shared layer's enumeration, and a name outside it has no
// path through the command.
func TestAnEditNamingNoKnownKindIsRefused(t *testing.T) {
	t.Parallel()
	ws, abs := editDocumentWorkspace(t, "storefront.aggregate.yaml", authoredAggregateForEdit)
	before := readFileForEdit(t, abs)

	err := editDocumentRun(t.Context(), editDocumentConfig{
		Edit:       "delete-component",
		File:       ws.rel,
		Node:       []string{"spec", "components"},
		Name:       "web",
		Value:      "x",
		ValueGiven: true,
		ws:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws.root}),
		Out:        &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("an edit outside the admissible set must be refused rather than interpreted")
	}
	if after := readFileForEdit(t, abs); after != before {
		t.Error("a refused edit wrote to the file")
	}
}

// TestTheHelpNamesTheEditsTheSharedLayerApplies holds the help text's statement
// of the bound to the layer's own enumeration, so the command cannot come to
// advertise a set it does not apply.
//
// It reads the rendered help rather than the helper, because what a reader is
// shown is the help: a helper returning the right list into a sentence nobody
// interpolated it into would satisfy an assertion on the helper alone.
func TestTheHelpNamesTheEditsTheSharedLayerApplies(t *testing.T) {
	t.Parallel()
	long := newEditDocumentCmd().Long
	for _, kind := range editops.Kinds() {
		if !strings.Contains(long, string(kind)) {
			t.Errorf("the help does not name the %q edit, which the shared layer applies", kind)
		}
	}
	if !strings.Contains(long, editDocumentKindList()) {
		t.Errorf("the help does not carry the derived set %q, so its statement of the bound is a restatement",
			editDocumentKindList())
	}
}

// TestTheWritingOutcomeTellsANoOpFromAWrite pins the two writing renderings.
//
// An edit whose entry was already there writes nothing, and saying nothing at
// all would leave that indistinguishable from a write that failed quietly — so
// the no-op names the document it found the entry in.
func TestTheWritingOutcomeTellsANoOpFromAWrite(t *testing.T) {
	t.Parallel()
	write := authoredcontent.DeliveryFor(false)

	var wrote bytes.Buffer
	if err := writeEditDocumentOutcome(&wrote, write, editops.Result{
		Documents: []authoredcontent.Document{{Path: "a.yaml", Content: "x"}},
		Written:   []string{"a.yaml"},
	}); err != nil {
		t.Fatalf("writeEditDocumentOutcome: %v", err)
	}
	if wrote.String() != "wrote a.yaml\n" {
		t.Errorf("a write reads %q; it must name the file that reached disk", wrote.String())
	}

	var noop bytes.Buffer
	if err := writeEditDocumentOutcome(&noop, write, editops.Result{
		Documents: []authoredcontent.Document{{Path: "a.yaml", Content: "x"}},
	}); err != nil {
		t.Fatalf("writeEditDocumentOutcome: %v", err)
	}
	if !strings.Contains(noop.String(), "a.yaml already carries that entry") {
		t.Errorf("a no-op reads %q; it must say the entry was already there rather than printing nothing",
			noop.String())
	}
	if noop.String() == wrote.String() {
		t.Error("a no-op and a write render identically, so a caller cannot tell one from the other")
	}
}

// TestTheReportedOutcomeCarriesOneParseableDocumentAndNothingElse holds the
// answer stream's shape: under the reporting delivery it carries the reported
// form and no prose, so a caller can parse it.
func TestTheReportedOutcomeCarriesOneParseableDocumentAndNothingElse(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := writeEditDocumentOutcome(&out, authoredcontent.DeliveryFor(true), editops.Result{
		Documents: []authoredcontent.Document{{Path: "a.yaml", Content: "kind: Aggregate\n"}},
	}); err != nil {
		t.Fatalf("writeEditDocumentOutcome: %v", err)
	}
	docs, err := authoredcontent.Decode(out.Bytes())
	if err != nil {
		t.Fatalf("the reported answer does not parse as the reported form: %v\n%s", err, out.String())
	}
	if len(docs) != 1 || docs[0].Path != "a.yaml" || docs[0].Content != "kind: Aggregate\n" {
		t.Errorf("the reported answer does not carry the document: %#v", docs)
	}
	if strings.Contains(out.String(), "wrote ") {
		t.Errorf("the reported answer carries prose beside the document:\n%s", out.String())
	}
}

// readFileForEdit reads a document back for a byte-level assertion.
func readFileForEdit(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back %s: %v", path, err)
	}
	return string(b)
}

// errorsIsEditRefused reports whether err names the invalid-result condition.
//
// It matches through the condition's own accessor rather than against its
// registered code as a string: the string comparison type-checks, reads like a
// match, and would keep compiling while silently ceasing to match were the
// condition re-registered under a different code.
func errorsIsEditRefused(err error) bool {
	return stderrors.Is(err, editops.ErrEditRefusedInvalidResult())
}

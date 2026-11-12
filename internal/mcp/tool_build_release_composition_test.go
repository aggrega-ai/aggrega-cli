// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The shared preview raises its findings into two places: the render's advisory
// channel, and a list of classed warnings about the Release and what it reaches.
// A command-line build prints both. These proofs hold the build tool to carrying
// the second as well, through the one class that exercises it end to end: a
// Release that extends another and inherits a boundary directory carrying no
// template variable, which resolves to the path the extended Release writes to.
//
// The fixture set sits in this package's test data and is laid over a copy of
// the hermetic end-to-end workspace, whose Aggregate, Manifests and catalog the
// Releases name. One parent authors literal directories and one authors
// templated ones; each has a child that extends it and writes nothing else, and
// a third child extends the literal parent while naming a Manifest that maps a
// type onto a recipe that does not implement it.

// compositionBoundaries are the boundaries the literal parent authors, in the
// order it authors them. They are the fixture's, written here as the fixture
// writes them, so an entry naming them has to have travelled from the fixture.
var compositionBoundaries = []string{"frontend-ns", "backend-ns", "data-ns"}

// compositionWorkspace copies the end-to-end workspace into a temporary root and
// lays the composition fixtures beside its Releases.
func compositionWorkspace(t *testing.T) (root, catalogDir string) {
	t.Helper()
	root = t.TempDir()
	src := filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "fullstack-app")
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy fixture workspace: %v", err)
	}
	layCompositionFixtures(t, root)
	return root, filepath.Join(root, "catalog")
}

// layCompositionFixtures copies every composition fixture into root. A fixture
// set that went missing would leave every Release below unresolvable, which
// fails loudly; one that grew is copied whole.
func layCompositionFixtures(t *testing.T, root string) {
	t.Helper()
	fixtures := filepath.Join(moduleRoot(t), "internal", "mcp", "testdata", "release-composition")
	entries, err := os.ReadDir(fixtures)
	if err != nil {
		t.Fatalf("read the composition fixtures: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the composition fixture directory is empty, so no Release below would extend anything")
	}
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(fixtures, e.Name()))
		if err != nil {
			t.Fatalf("read fixture %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(root, e.Name()), body, 0o644); err != nil {
			t.Fatalf("lay fixture %s: %v", e.Name(), err)
		}
	}
}

// inheritedLiteralDirectoryCode is the code the condition reports, read off the
// condition through its accessor rather than spelled, so an entry matched on it
// is matched on the condition.
func inheritedLiteralDirectoryCode(t *testing.T) string {
	t.Helper()
	code, ok := cerrors.CodeOf(resolver.ErrInheritedBoundaryDirectoryIsLiteral())
	if !ok || code == "" {
		t.Fatal("the inherited-literal-directory condition reports no code, so no entry could carry one")
	}
	return code
}

// compositionEntries are the advisories carrying the composition condition's
// code, in the order the answer carries them.
func compositionEntries(advisories []buildAdvisory, code string) []buildAdvisory {
	var out []buildAdvisory
	for _, a := range advisories {
		if a.Code == code {
			out = append(out, a)
		}
	}
	return out
}

// sharedPreviewCompositionTexts is the text of each composition warning the
// shared preview raises for a Release — the field a command-line build prints
// behind its "release composition:" prefix. It is computed through the shared
// computation, over the workspace the tool itself resolves, rather than spelled
// here: the comparison is then between two readings of one run, not between the
// tool and a sentence this file wrote.
func sharedPreviewCompositionTexts(t *testing.T, root, catalogDir, releasePath string) []string {
	t.Helper()
	tw, err := newTestServer(t).resolveToolWorkspace(root, []string{catalogDir})
	if err != nil {
		t.Fatalf("resolve the workspace the tool resolves: %v", err)
	}
	preview, err := compilerops.PreviewForArtifact(
		context.Background(),
		compilerops.NewPreviewInputs(compilerops.NoExternalInputs(), tw.newView(releasePath, nil), releasePath, tw.root, tw.cfg, "test"),
		pipeline.Options{},
	)
	if err != nil {
		t.Fatalf("the shared preview refused a Release the tool builds: %v", err)
	}
	var texts []string
	for _, w := range preview.Warnings {
		if w.Class == compilerops.WarningReleaseComposition {
			texts = append(texts, w.Text)
		}
	}
	return texts
}

// assertCompositionEntries holds a set of composition entries to the fixture:
// one per authored boundary, in authored order, at warn, each naming its
// boundary.
func assertCompositionEntries(t *testing.T, entries []buildAdvisory, all []buildAdvisory) {
	t.Helper()
	if len(entries) != len(compositionBoundaries) {
		t.Fatalf("the answer carries %d composition finding(s); the child inherits %d literal directories, one "+
			"finding each.\nadvisories: %+v", len(entries), len(compositionBoundaries), all)
	}
	for i, boundary := range compositionBoundaries {
		e := entries[i]
		if !strings.Contains(e.Message, `"`+boundary+`"`) {
			t.Errorf("composition finding %d = %q; want it to name the boundary %q, in the order the parent "+
				"authors its boundaries", i, e.Message, boundary)
		}
		if e.Severity != "warn" {
			t.Errorf("composition finding %d severity = %q; want %q — the rule raising it declares warning "+
				"severity, and a finding that halts nothing must not arrive at the halting tier", i, e.Severity, "warn")
		}
	}
}

// TestBuildToolCarriesTheReleaseCompositionFindingsTheCommandPrints is the proof
// that a caller with no terminal receives the composition findings a
// command-line build prints: on the structured answer, with the condition's
// code, in the text form too, and with the message the command prints behind
// its prefix.
func TestBuildToolCarriesTheReleaseCompositionFindingsTheCommandPrints(t *testing.T) {
	root, catalogDir := compositionWorkspace(t)
	releasePath := filepath.Join(root, "child-literal.yaml")
	code := inheritedLiteralDirectoryCode(t)

	payload, text := buildPayloadOf(t, root, catalogDir, releasePath)
	if len(payload.Files) == 0 {
		t.Fatal("the build rendered no files, so this is not the successful-build case the assertions below assume")
	}
	entries := compositionEntries(payload.Advisories, code)
	assertCompositionEntries(t, entries, payload.Advisories)

	for _, e := range entries {
		if !strings.Contains(text, e.Message) {
			t.Errorf("the text form does not list %q; a caller reading text receives fewer findings than one "+
				"reading the payload.\ntext:\n%s", e.Message, text)
		}
	}

	// The message is the one the command prints, prefix aside, and in the same
	// sequence.
	printed := sharedPreviewCompositionTexts(t, root, catalogDir, releasePath)
	if len(printed) != len(entries) {
		t.Fatalf("the shared preview raises %d composition finding(s) and the tool carries %d:\nraised:  %q\ncarried: %+v",
			len(printed), len(entries), printed, entries)
	}
	for i := range printed {
		if entries[i].Message != printed[i] {
			t.Errorf("composition finding %d reads differently from what the command prints behind its prefix:\n"+
				"tool:    %q\ncommand: %q", i, entries[i].Message, printed[i])
		}
	}
}

// TestBuildToolRaisesNoCompositionFindingForAnInheritedTemplateDirectory is the
// control. The same child shape, extending a parent whose directories carry a
// template variable, resolves each directory per Release, so there is nothing to
// advise about. Without it the proof above would also pass on a surface that
// attached the finding to every child.
func TestBuildToolRaisesNoCompositionFindingForAnInheritedTemplateDirectory(t *testing.T) {
	root, catalogDir := compositionWorkspace(t)
	payload, text := buildPayloadOf(t, root, catalogDir, filepath.Join(root, "child-template.yaml"))

	if len(payload.Files) == 0 {
		t.Fatal("the control rendered no files, so it is not the successful build it stands in for")
	}
	if len(payload.Advisories) != 0 {
		t.Errorf("a child inheriting templated directories raised %d advisory(ies): %+v",
			len(payload.Advisories), payload.Advisories)
	}
	if strings.Contains(text, "advisory finding") {
		t.Errorf("the text form reports advisories for the control:\n%s", text)
	}
}

// TestBuildToolRefusalCarriesTheCompositionFindingsRaisedAheadOfIt is the
// refusal row. The composition findings are raised the moment the Release
// resolves, ahead of every gate that can refuse it, so a child that goes on to
// fail a later gate still hands them back. The later gate here is the Manifest
// check: the child names a Manifest mapping a type onto a recipe that does not
// implement it.
func TestBuildToolRefusalCarriesTheCompositionFindingsRaisedAheadOfIt(t *testing.T) {
	root, catalogDir := compositionWorkspace(t)
	code := inheritedLiteralDirectoryCode(t)

	refused := callTool(t, newTestServer(t), "build", buildArgs{
		Release:       filepath.Join(root, "child-mismatch.yaml"),
		WorkspaceRoot: root,
		CatalogDirs:   []string{catalogDir},
	})
	if !refused.IsError {
		t.Fatalf("the mismatched Manifest must refuse the build, or this is not the refusal row: %s",
			refused.Content[0].Text)
	}
	if !strings.Contains(refused.Content[0].Text, "the Manifest maps a type onto a recipe that does not implement it") {
		t.Fatalf("the build refused at a gate other than the Manifest check, so the row does not sit where it "+
			"claims to:\n%s", refused.Content[0].Text)
	}
	payload := unmarshalBuildPayload(t, refused)
	assertCompositionEntries(t, compositionEntries(payload.Advisories, code), payload.Advisories)
	if len(payload.Files) != 0 {
		t.Errorf("the refusal reports %d rendered file(s); a build that refused emitted none", len(payload.Files))
	}
}

// TestBuildToolListsTheRenderAdvisoriesAheadOfTheCompositionFindings holds the
// order to the terminal's. A command-line build prints each render advisory as
// the render raises it and prints the preview's classed warnings once the render
// has returned, so the composition findings follow the render's even though they
// were raised earlier. The fixture raises one of each kind in a single build: a
// parameter nothing reads, spliced into the Aggregate, and the literal child.
func TestBuildToolListsTheRenderAdvisoriesAheadOfTheCompositionFindings(t *testing.T) {
	root, catalogDir, _ := advisoryBuildWorkspace(t)
	layCompositionFixtures(t, root)
	code := inheritedLiteralDirectoryCode(t)

	payload, _ := buildPayloadOf(t, root, catalogDir, filepath.Join(root, "child-literal.yaml"))
	want := 1 + len(compositionBoundaries)
	if len(payload.Advisories) != want {
		t.Fatalf("the build carries %d advisory(ies); the fixture raises %d — the unread parameter, then one "+
			"composition finding per inherited literal directory.\nadvisories: %+v",
			len(payload.Advisories), want, payload.Advisories)
	}
	if !strings.Contains(payload.Advisories[0].Message, "unread_probe") {
		t.Errorf("advisory 0 = %+v; want the unread-parameter finding first, as a terminal prints it",
			payload.Advisories[0])
	}
	composition := payload.Advisories[1:]
	for i, e := range composition {
		if e.Code != code {
			t.Errorf("advisory %d = %+v; want a composition finding, after the render's own", i+1, e)
		}
	}
	assertCompositionEntries(t, composition, payload.Advisories)
}

// TestBuildToolCarriesTheSchemaOverlapAdvisory is the second class the preview
// raises as a classed warning rather than on the render's channel: a property a
// Component the Release reaches declares in both its values and infra pillars.
// The rewrite is anchored on the Component's existing schema block, so a fixture
// reorganisation fails here rather than leaving the row with nothing to find.
func TestBuildToolCarriesTheSchemaOverlapAdvisory(t *testing.T) {
	root, catalogDir := compositionWorkspace(t)
	path := filepath.Join(root, "catalog", "base", "components", "web-ui.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the fixture no longer carries the Component this row rewrites: %v", err)
	}
	const anchor = "  schema:\n    infra:\n"
	if !bytes.Contains(body, []byte(anchor)) {
		t.Fatalf("the Component no longer carries the %q anchor; the rewrite would be silently inert", anchor)
	}
	body = bytes.Replace(body, []byte(anchor),
		[]byte("  schema:\n    values:\n      port: { type: integer, default: 3000 }\n    infra:\n"), 1)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write the overlapping Component: %v", err)
	}

	payload, text := buildPayloadOf(t, root, catalogDir, filepath.Join(root, "release.yaml"))
	var found *buildAdvisory
	for i := range payload.Advisories {
		if strings.Contains(payload.Advisories[i].Message, `"port"`) &&
			strings.Contains(payload.Advisories[i].Message, "core/compute/web-ui") {
			found = &payload.Advisories[i]
		}
	}
	if found == nil {
		t.Fatalf("no advisory names the property the Component declares in both pillars; a caller with no "+
			"terminal is told nothing a command-line build prints.\nadvisories: %+v", payload.Advisories)
	}
	if found.Severity != "warn" {
		t.Errorf("overlap advisory severity = %q; want %q", found.Severity, "warn")
	}
	if !strings.Contains(text, found.Message) {
		t.Errorf("the text form does not list the overlap advisory:\n%s", text)
	}
}

// brokenExtendsComponent rewrites one catalog Component the fixture's closure
// reaches so that it extends a parent no catalog entity provides. The rewrite is
// anchored on the file's existing text, so a fixture reorganisation fails here
// instead of leaving the workspace resolvable and the row vacuous.
func brokenExtendsComponent(t *testing.T, root string) string {
	t.Helper()
	const missingParent = "core/compute/web-ui-base-that-is-not-there"
	path := filepath.Join(root, "catalog", "base", "components", "web-ui.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the fixture no longer carries the Component this row rewrites: %v", err)
	}
	for _, r := range []struct{ anchor, replacement string }{
		{"spec:\n  inject:", "spec:\n  extends: " + missingParent + "\n  inject:"},
		{"  schema:\n    infra:\n      port: { type: integer, default: 3000 }\n      replicas: { type: integer, default: 1 }\n",
			"  schema: inherited\n"},
	} {
		if !bytes.Contains(body, []byte(r.anchor)) {
			t.Fatalf("the Component no longer carries the %q anchor; the rewrite would be silently inert", r.anchor)
		}
		body = bytes.Replace(body, []byte(r.anchor), []byte(r.replacement), 1)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write the broken Component: %v", err)
	}
	return missingParent
}

// TestBuildToolExtendsRefusalNamesTheParentItCouldNotResolve is where the extends
// class goes instead of the advisory list. It accompanies a halt and names what
// refused, so it travels in the refusal's guidance text, and a caller told only
// how many inheritances failed would not know which one to repair.
func TestBuildToolExtendsRefusalNamesTheParentItCouldNotResolve(t *testing.T) {
	root, catalogDir := compositionWorkspace(t)
	releasePath := filepath.Join(root, "release.yaml")
	missingParent := brokenExtendsComponent(t, root)

	refused := callTool(t, newTestServer(t), "build", buildArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   []string{catalogDir},
	})
	if !refused.IsError {
		t.Fatalf("an unresolvable parent must refuse the build: %s", refused.Content[0].Text)
	}
	text := refused.Content[0].Text
	if !strings.Contains(text, "inherits from a parent that could not be resolved") {
		t.Fatalf("the build refused at a gate other than inheritance, so the row does not sit where it claims "+
			"to:\n%s", text)
	}
	if !strings.Contains(text, missingParent) {
		t.Errorf("the refusal does not name the parent %q it could not resolve, so a caller cannot tell which "+
			"inheritance to repair:\n%s", missingParent, text)
	}
	payload := unmarshalBuildPayload(t, refused)
	for _, a := range payload.Advisories {
		if strings.Contains(a.Message, missingParent) {
			t.Errorf("the inheritance failure travelled as an advisory, %+v; it accompanies the halt and "+
				"belongs in the refusal", a)
		}
	}
}

// TestBuildExtendsSummaryQuotesEveryInheritanceAndNothingElse pins the extends
// arm's selection over a preview carrying every other class beside it: each
// extends detail is quoted, in order, and no finding of another class is.
func TestBuildExtendsSummaryQuotesEveryInheritanceAndNothingElse(t *testing.T) {
	t.Parallel()
	summary := previewFailureSummary(&compilerops.PreviewResult{
		FailedGate: compilerops.GateExtends,
		Warnings: []compilerops.Warning{
			{Class: compilerops.WarningReleaseComposition, Text: "a composition reading"},
			{Class: compilerops.WarningExtends, Text: "the first broken parent"},
			{Class: compilerops.WarningDeprecation, Text: "a withdrawn pin"},
			{Class: compilerops.WarningExtends, Text: "the second broken parent"},
		},
	}, nil)

	first := strings.Index(summary, "the first broken parent")
	second := strings.Index(summary, "the second broken parent")
	if first < 0 || second < 0 {
		t.Fatalf("the extends refusal does not quote every broken parent:\n%s", summary)
	}
	if second < first {
		t.Errorf("the extends refusal quotes the broken parents out of the order they were raised:\n%s", summary)
	}
	for _, other := range []string{"a composition reading", "a withdrawn pin"} {
		if strings.Contains(summary, other) {
			t.Errorf("the extends refusal quotes %q, which is not an inheritance failure:\n%s", other, summary)
		}
	}
}

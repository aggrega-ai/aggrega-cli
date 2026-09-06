// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// The explain command's four answers, driven through the real command tree.
//
// A code resolves to what the compiler declares and to the published prose; a
// code that names nothing is refused; a number that named something once is
// answered with what it named; and a condition whose prose is missing — because
// the page documents no entry for it, or because this workspace has no copy of
// the documentation — still yields the condition, with the reason the prose is
// absent stated rather than swallowed.
//
// Every case seeds a real documentation bundle into a real workspace cache and
// lets the command read it through the same offline path a session uses, so what
// is proved is the whole read rather than a parser over a string.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// explainProbeEntry is the heading and prose the fixture page documents the probe
// condition under.
const (
	explainProbeHeading = "The bundle carries no page at the path asked for"
	explainProbeProse   = "Nothing is broken in your files."
)

// explainProbe returns a condition that is really registered, together with both
// of its codes.
//
// It is read off an accessor rather than quoted, so the case names a condition
// that exists at the moment it runs. Quoting a number here would pin the test to
// one package's allocation and turn a legitimate renumbering into a failure in a
// file that has nothing to do with it.
func explainProbe(t *testing.T) (short, code, message string) {
	t.Helper()
	probe := docscache.ErrPageNotFound()
	short, ok := cerrors.ShortCodeOf(probe)
	if !ok {
		t.Fatal("the probe condition reports no short code, so these cases would assert nothing")
	}
	code, ok = cerrors.CodeOf(probe)
	if !ok {
		t.Fatal("the probe condition reports no descriptive code, so these cases would assert nothing")
	}
	return short, code, probe.Error()
}

// guestProbe is a condition declared inside a provider guest: registered when
// that guest runs, and therefore absent from the host binary's own registry. Its
// short code, descriptive code and sentence are what the published reference
// carries for it.
//
// It is quoted rather than read off an accessor because there is nothing here to
// read it off — which is the whole property the case exercises. A renumbering
// would fail this case, which is the failure worth having: it is the one place
// that says the command answers for a condition the host does not declare.
const (
	guestProbeShort    = "E0230"
	guestProbeCode     = "cloudformation-provider-cfncore/duplicate-mapping-key"
	guestProbeSentence = "cloudformation-provider: one mapping key is written more than once"
)

// explainFixturePage renders a halting-conditions page in the shape the generator
// writes: an entry, and a generated region pairing short codes with the entries
// documenting them. documented selects whether the region names the probe code,
// which is what separates a condition the page documents from one it does not.
func explainFixturePage(shortCode, sentence string, documented bool) string {
	row := "| `E0100` | `something else entirely` | " + explainProbeHeading + " |\n"
	if documented {
		row = "| `" + shortCode + "` | `" + sentence + "` | " + explainProbeHeading + " |\n"
	}
	return "# Fatal Halts\n\n" +
		"## When reading documentation\n\n" +
		"### " + explainProbeHeading + "\n\n" +
		explainProbeProse + " The bundle predates the page, or the path asked for is not the one it is published under.\n\n" +
		"## Finding a halt from the short code the build printed\n\n" +
		"<!-- BEGIN GENERATED: " + haltref.RegionID + " -->\n" +
		"| Short code | Opens with | Documented under |\n| --- | --- | --- |\n" +
		row +
		"<!-- END GENERATED: " + haltref.RegionID + " -->\n"
}

// explainFixtureReference renders the sentinel reference in the shape the
// generator writes: every condition under its short code, its descriptive code
// and the sentence it registers, including one a provider guest declares.
func explainFixtureReference(shortCode, code, sentence string) string {
	return "# Error sentinels\n\n" +
		"## Every condition, by short code\n\n" +
		"<!-- BEGIN GENERATED: " + haltref.ReferenceRegionID + " -->\n" +
		"| Code | Condition | Message |\n| --- | --- | --- |\n" +
		"| `" + shortCode + "` | `" + code + "` | `" + sentence + "` |\n" +
		"| `" + guestProbeShort + "` | `" + guestProbeCode + "` | `" + guestProbeSentence + "` |\n" +
		"<!-- END GENERATED: " + haltref.ReferenceRegionID + " -->\n"
}

// seedExplainWorkspace builds a documentation bundle carrying the fixture pages,
// caches it for a fresh workspace at exactly the coordinates the command derives,
// and returns that workspace's root.
//
// The coordinates are derived here the same way the command derives them rather
// than pinned, so a case cannot pass by seeding a slot the command never reads.
func seedExplainWorkspace(t *testing.T, pages map[string]string) string {
	t.Helper()
	ws := t.TempDir()
	cfg := workspaceconfig.Default()
	cacheDir := cfg.ResolvedArtifactCacheDir()
	ref := docsbundle.Ref(cfg.DefaultRegistry)
	version := provenanceCompilerVersion()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// Written in a fixed order, so the bundle a case builds is the same bundle
	// every time it runs.
	for _, path := range []string{haltref.ReferencePagePath, haltref.PagePath} {
		body, ok := pages[path]
		if !ok {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: path, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	store := memory.New()
	if _, err := oci.Push(context.Background(), store, version, oci.MediaTypeDocs, buf.Bytes(), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if _, err := docscache.ResolveAndCache(context.Background(), store, ref, version, ws, cacheDir, nil); err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}
	return ws
}

// seededPages is the fixture bundle's content for a probe condition: the
// reference that resolves it and the halting-conditions page that documents it.
func seededPages(short, code, sentence string, documented bool) map[string]string {
	return map[string]string{
		haltref.ReferencePagePath: explainFixtureReference(short, code, sentence),
		haltref.PagePath:          explainFixturePage(short, sentence, documented),
	}
}

// runExplainCommand drives the real command tree and returns what the command
// wrote to each stream and the error it reported.
func runExplainCommand(t *testing.T, args ...string) (out, notes string, err error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := newRootCmd()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err = root.Execute()
	return stdout.String(), stderr.String(), err
}

// TestExplainAnswersARealConditionWithItsPublishedProse is the primary proof: a
// code a build could print, resolved to both of its identifiers, the sentence its
// diagnostics open with, and the entry that teaches the fix.
func TestExplainAnswersARealConditionWithItsPublishedProse(t *testing.T) {
	// Not parallel: isolates the process environment and the working directory.
	isolateCLIEnvironment(t)
	short, code, message := explainProbe(t)
	ws := seedExplainWorkspace(t, seededPages(short, code, message, true))

	out, notes, err := runExplainCommand(t, "explain", short, "--workspace-root", ws)
	if err != nil {
		t.Fatalf("aggrega explain %s: %v\nnotes: %s", short, err, notes)
	}
	for _, want := range []string{short, code, message, explainProbeHeading, explainProbeProse} {
		if !strings.Contains(out, want) {
			t.Errorf("the answer omits %q:\n%s", want, out)
		}
	}
	// The prose stops at the next entry. Without this the command could be
	// printing the whole page and satisfying every assertion above.
	if strings.Contains(out, "Finding a halt from the short code") {
		t.Errorf("the answer ran past the entry it resolved to:\n%s", out)
	}
	if strings.Contains(notes, "not available") {
		t.Errorf("the walk-through was reported missing while it was printed:\n%s", notes)
	}
}

// TestExplainAnswersFromThePublishedPages is the same proof over the committed
// documentation rather than a fixture.
//
// A fixture page is written by whoever writes the test, so it agrees with the
// reader by construction and proves only that the command drives its own parser.
// This bundles the real pages, caches them the way a workspace caches them, and
// asks the real command for a real condition — which is what says the capability
// works against what is actually published.
//
// It logs what the command wrote, so a reader of a failing run sees the reply
// rather than only which assertion tripped.
func TestExplainAnswersFromThePublishedPages(t *testing.T) {
	// The pages are read BEFORE the environment is isolated, and the order is
	// load-bearing rather than incidental: isolation moves the working directory,
	// and the pages are addressed relative to this package. Reading them
	// afterwards finds nothing and reports a standalone checkout — a skip that
	// looks like an environment fact and is really this case not running at all.
	published := publishedErrorPages(t)
	isolateCLIEnvironment(t)

	pages := map[string]string{}
	for _, p := range []string{haltref.ReferencePagePath, haltref.PagePath} {
		body, ok := published[filepath.Base(p)]
		if !ok {
			t.Fatalf("the published error pages carry no %s", filepath.Base(p))
		}
		pages[p] = body
	}
	ws := seedExplainWorkspace(t, pages)

	short, code, message := explainProbe(t)
	out, notes, err := runExplainCommand(t, "explain", short, "--workspace-root", ws)
	if err != nil {
		t.Fatalf("aggrega explain %s: %v\nnotes: %s", short, err, notes)
	}
	t.Logf("aggrega explain %s\n%s", short, out)

	for _, want := range []string{short, code, message} {
		if !strings.Contains(out, want) {
			t.Errorf("the answer omits %q:\n%s", want, out)
		}
	}
	// The published entry is prose beneath a heading, so a reply that carried only
	// the condition would satisfy everything above. What proves the walk-through
	// arrived is that the reply is substantially longer than the condition alone
	// and opens the entry with a heading.
	if !strings.Contains(out, "\n#") {
		t.Errorf("the answer carries no entry heading, so no published walk-through reached it:\n%s", out)
	}
	if len(out) < len(short)+len(code)+len(message)+200 {
		t.Errorf("the answer is barely longer than the condition itself, so the entry is missing:\n%s", out)
	}
	if strings.Contains(notes, "not available") || strings.Contains(notes, "no entry for") {
		t.Errorf("the walk-through was reported missing against the real pages:\n%s", notes)
	}
}

// TestExplainAcceptsTheDescriptiveCodeToo pins the other spelling of the
// argument, which is the one a caller reading a structured diagnostic holds.
func TestExplainAcceptsTheDescriptiveCodeToo(t *testing.T) {
	isolateCLIEnvironment(t)
	short, code, message := explainProbe(t)
	ws := seedExplainWorkspace(t, seededPages(short, code, message, true))

	byShort, _, err := runExplainCommand(t, "explain", short, "--workspace-root", ws)
	if err != nil {
		t.Fatalf("aggrega explain %s: %v", short, err)
	}
	byCode, _, err := runExplainCommand(t, "explain", code, "--workspace-root", ws)
	if err != nil {
		t.Fatalf("aggrega explain %s: %v", code, err)
	}
	if byShort != byCode {
		t.Errorf("the two spellings of one condition's code answer differently.\nshort:\n%s\ndescriptive:\n%s",
			byShort, byCode)
	}
}

// TestExplainAnswersAConditionTheHostDoesNotDeclare is the case the registry
// alone cannot serve. A provider's conditions are declared inside the guest that
// raises them, so the host binary registers none of them — yet a build reports
// them and the published reference carries them, so the command has to answer.
//
// The negative control is the same code against the registry directly, which must
// NOT resolve. Without it this case would pass just as well if the host happened
// to declare the condition after all, and it would then be proving nothing about
// the fallback.
func TestExplainAnswersAConditionTheHostDoesNotDeclare(t *testing.T) {
	isolateCLIEnvironment(t)
	if _, err := haltref.Resolve(guestProbeShort); err == nil {
		t.Fatalf("%s resolves from this binary's own registry, so this case no longer exercises the "+
			"fallback it exists for; point it at a condition a guest declares", guestProbeShort)
	}
	short, code, message := explainProbe(t)
	ws := seedExplainWorkspace(t, seededPages(short, code, message, true))

	out, notes, err := runExplainCommand(t, "explain", guestProbeShort, "--workspace-root", ws)
	if err != nil {
		t.Fatalf("a condition a guest declares was refused: %v\nnotes: %s", err, notes)
	}
	for _, want := range []string{guestProbeShort, guestProbeCode, guestProbeSentence} {
		if !strings.Contains(out, want) {
			t.Errorf("the answer omits %q:\n%s", want, out)
		}
	}
	// The fixture page documents no entry for it, so the walk-through is reported
	// missing rather than silently absent.
	if !strings.Contains(notes, "no entry for "+guestProbeShort) {
		t.Errorf("the note does not say which condition the page does not document:\n%s", notes)
	}
}

// TestExplainRefusesACodeNoConditionCarries is the first of the distinct
// unanswerable cases. Neither the binary's declarations nor the published
// reference carries the code, so there is nothing to report and this one reports
// failure.
//
// The reference is present and consulted, which is what makes the refusal mean
// what it says: a code missing from an unread reference is a different fact, and
// the case below covers it.
func TestExplainRefusesACodeNoConditionCarries(t *testing.T) {
	isolateCLIEnvironment(t)
	short, code, message := explainProbe(t)
	ws := seedExplainWorkspace(t, seededPages(short, code, message, true))

	out, _, err := runExplainCommand(t, "explain", "E0000", "--workspace-root", ws)
	if err == nil {
		t.Fatal("a code no condition carries was answered rather than refused")
	}
	if out != "" {
		t.Errorf("a refused code still wrote an answer:\n%s", out)
	}
	for _, want := range []string{"E0164", "resolver/parameter-contract-violation", "sentinels.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal omits %q, so a reader cannot correct from it: %s", want, err)
		}
	}
}

// TestExplainSaysWhenItCouldNotConsultTheReference covers the case a single
// refusal would misreport. A code this binary does not declare may be a typo or
// may be one a provider declares, and only the published reference tells the two
// apart — so where the reference cannot be read, the answer says both that the
// code resolved to nothing here and that the reference was not consulted.
//
// The workspace is pinned offline, so the case reaches no network.
func TestExplainSaysWhenItCouldNotConsultTheReference(t *testing.T) {
	isolateCLIEnvironment(t)
	t.Setenv("AGGREGA_OFFLINE", "1")
	ws := t.TempDir()

	out, _, err := runExplainCommand(t, "explain", guestProbeShort, "--workspace-root", ws)
	if err == nil {
		t.Fatal("a code nothing here declares was answered without the reference being read")
	}
	if out != "" {
		t.Errorf("a refused code still wrote an answer:\n%s", out)
	}
	for _, want := range []string{guestProbeShort, "sentinels.md", "not cached"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal omits %q, so a reader cannot tell a typo from unread documentation: %s", want, err)
		}
	}
	// The condition is real, published and reportable — it is simply declared
	// somewhere this binary cannot see. Denying it outright would be a false
	// statement to the one reader most likely to be holding the code.
	//
	// The denial is constructed rather than quoted, so the assertion tracks the
	// refusal's own text instead of a phrase copied out of it that could go on
	// passing after the text moved.
	denial := (&haltref.UnknownCodeError{Code: guestProbeShort}).Error()
	if strings.Contains(err.Error(), denial) {
		t.Errorf("a condition a provider declares was reported as registered under nothing, while "+
			"the reference that carries it went unread: %s", err)
	}
}

// TestExplainRefusesOneCodeTwoWaysByWhatItCouldRead is the pin on the distinction
// between the two refusals. It holds the code constant and varies only whether
// the published reference could be read, so neither half can pass for a reason
// other than the one under test.
//
// With the reference read, the refusal is entitled to the definitive negative:
// every place a condition is declared has been searched. With the reference
// unreachable it is not — a provider's conditions are declared inside the guest
// that raises them, so a code missing from the host's own declarations may still
// name one — and the refusal has to report what it measured instead.
//
// Both refusals are constructed from their own types rather than quoted, so the
// case asserts the claims the product actually makes and cannot drift into
// asserting phrases nobody prints.
func TestExplainRefusesOneCodeTwoWaysByWhatItCouldRead(t *testing.T) {
	isolateCLIEnvironment(t)
	// Pinned offline so neither half reaches a network. The seeded half is served
	// from its cache, which is probed before any target is resolved, and the
	// unseeded half is the case under test.
	t.Setenv("AGGREGA_OFFLINE", "1")

	const absent = "E0000"
	short, code, message := explainProbe(t)

	seeded := seedExplainWorkspace(t, seededPages(short, code, message, true))
	_, _, consulted := runExplainCommand(t, "explain", absent, "--workspace-root", seeded)
	if consulted == nil {
		t.Fatal("a code the published reference does not carry was answered rather than refused")
	}
	_, _, unread := runExplainCommand(t, "explain", absent, "--workspace-root", t.TempDir())
	if unread == nil {
		t.Fatal("a code nothing here declares was answered without the reference being read")
	}

	// One code, refused two ways. Without this each half could be asserting a
	// phrase the other carries too, and the pair would prove nothing.
	if consulted.Error() == unread.Error() {
		t.Fatalf("one code is refused identically whether or not the reference was read, so a "+
			"reader cannot tell an established negative from an unfinished lookup:\n%s", consulted)
	}

	denial := (&haltref.UnknownCodeError{Code: absent}).Error()
	undetermined := (&haltref.UnresolvedCodeError{Code: absent}).Error()

	if !strings.Contains(consulted.Error(), denial) {
		t.Errorf("the reference was read and does not carry the code, yet the refusal stops short "+
			"of saying so: %s", consulted)
	}
	if strings.Contains(unread.Error(), denial) {
		t.Errorf("the code was declared unregistered while the reference that would settle it went "+
			"unread: %s", unread)
	}
	// Both repairs stay visible: the narrow claim carries the code-side one, and
	// the reason the documentation is missing carries the other.
	for _, want := range []string{undetermined, "not cached", "sentinels.md"} {
		if !strings.Contains(unread.Error(), want) {
			t.Errorf("the undetermined refusal omits %q, so it does not leave both repairs open: %s",
				want, unread)
		}
	}
}

// TestExplainAnswersARetiredNumberWithWhatItNamed is the second: the code is not
// a typo, it is a number that named a condition once, and what a reader needs is
// what it named.
func TestExplainAnswersARetiredNumberWithWhatItNamed(t *testing.T) {
	isolateCLIEnvironment(t)
	ws := t.TempDir()

	_, _, err := runExplainCommand(t, "explain", "E0157", "--workspace-root", ws)
	if err == nil {
		t.Fatal("a retired number was answered as a live condition")
	}
	if !strings.Contains(err.Error(), "retired") {
		t.Errorf("the answer does not say the number is retired, so it reads as a code that never existed: %s", err)
	}
	if !strings.Contains(err.Error(), "never") {
		t.Errorf("the answer does not say the number is never reissued, which is the fact it exists to carry: %s", err)
	}
}

// TestExplainReportsACodeThePageDoesNotDocument is the third: the condition
// resolves, so the answer stands, and what is missing is the published
// walk-through. That is a note rather than a refusal.
func TestExplainReportsACodeThePageDoesNotDocument(t *testing.T) {
	isolateCLIEnvironment(t)
	short, code, message := explainProbe(t)
	ws := seedExplainWorkspace(t, seededPages(short, code, message, false))

	out, notes, err := runExplainCommand(t, "explain", short, "--workspace-root", ws)
	if err != nil {
		t.Fatalf("a condition with no entry was refused rather than reported: %v", err)
	}
	for _, want := range []string{short, code, message} {
		if !strings.Contains(out, want) {
			t.Errorf("the condition itself was not answered, and it does not depend on the page:\n%s", out)
		}
	}
	if strings.Contains(out, explainProbeProse) {
		t.Errorf("prose belonging to another entry was printed:\n%s", out)
	}
	if !strings.Contains(notes, "no entry for "+short) {
		t.Errorf("the note does not say which condition the page does not document:\n%s", notes)
	}
}

// TestExplainReportsAWorkspaceWithNoCachedBundle is the fourth: the condition
// resolves and the documentation cannot be reached at all. The reason names the
// artifact and the version, because "not found" is not an answer when the cause
// is a bundle this workspace has no copy of.
//
// The workspace is pinned offline, so the acquisition refuses at construction and
// this case reaches no network.
func TestExplainReportsAWorkspaceWithNoCachedBundle(t *testing.T) {
	isolateCLIEnvironment(t)
	t.Setenv("AGGREGA_OFFLINE", "1")
	short, code, message := explainProbe(t)
	ws := t.TempDir()

	out, notes, err := runExplainCommand(t, "explain", short, "--workspace-root", ws)
	if err != nil {
		t.Fatalf("an unreachable bundle refused the whole answer: %v\nnotes: %s", err, notes)
	}
	for _, want := range []string{short, code, message} {
		if !strings.Contains(out, want) {
			t.Errorf("the condition itself was not answered, and it does not depend on the bundle:\n%s", out)
		}
	}
	ref := docsbundle.Ref(workspaceconfig.Default().DefaultRegistry)
	for _, want := range []string{ref, provenanceCompilerVersion(), haltref.PagePath} {
		if !strings.Contains(notes, want) {
			t.Errorf("the note omits %q, so it does not say what was missing:\n%s", want, notes)
		}
	}
}

// TestExplainWritesNothingToTheWorkspace pins the promise the command's help
// makes about a read: the answer is text, and producing it leaves the tree it was
// asked about alone.
func TestExplainWritesNothingToTheWorkspace(t *testing.T) {
	isolateCLIEnvironment(t)
	short, code, message := explainProbe(t)
	ws := seedExplainWorkspace(t, seededPages(short, code, message, true))

	before := treeSnapshot(t, ws)
	if len(before) == 0 {
		t.Fatal("the snapshot read nothing, so comparing it against itself would assert nothing")
	}
	if _, _, err := runExplainCommand(t, "explain", short, "--workspace-root", ws); err != nil {
		t.Fatalf("aggrega explain %s: %v", short, err)
	}
	after := treeSnapshot(t, ws)
	if len(after) != len(before) {
		t.Errorf("the workspace changed: %d entries before, %d after", len(before), len(after))
	}
	for path, digest := range before {
		if after[path] != digest {
			t.Errorf("%s changed while explaining a condition", path)
		}
	}
}

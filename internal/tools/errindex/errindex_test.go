// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scanFixture reads the fixture module and keys the result the way the generator
// does — on (package, name) — so a test cannot accidentally assert against a
// name-keyed view the generator never uses.
func scanFixture(t *testing.T, root, modulePath string) map[string]Identity {
	t.Helper()
	ids, err := scan(root, modulePath)
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}
	out := map[string]Identity{}
	for _, id := range ids {
		out[id.Handle()] = id
	}
	return out
}

func TestScan_ReadsDeclarationsRegistrationsAndReExports(t *testing.T) {
	t.Parallel()
	got := scanFixture(t, "testdata/module", "example.test/fixture")

	for _, tc := range []struct {
		handle     string
		code       string
		message    string
		aliasOf    string
		importable bool
	}{
		{handle: "pkg/thing.ErrPlain", code: "thing/plain", message: "a plain condition", importable: true},
		{handle: "pkg/thing.ErrQuoted", code: "thing/quoted", message: "the `uses:` block is malformed", importable: true},
		{handle: "pkg/other.ErrOwn", code: "other/own", message: "declared here", importable: true},
		// A re-export declares no code of its own and takes the message
		// registered against the identity it forwards to.
		{handle: "pkg/other.ErrPlain", aliasOf: "pkg/thing.ErrPlain", message: "a plain condition", importable: true},
		{handle: "pkg/wrap.ErrHidden", aliasOf: "internal/hidden.ErrHidden", message: "raised from a package no reader can import", importable: true},
		{handle: "internal/hidden.ErrHidden", code: "hidden/hidden", message: "raised from a package no reader can import", importable: false},
	} {
		id, ok := got[tc.handle]
		if !ok {
			t.Errorf("%s: not extracted", tc.handle)
			continue
		}
		if id.Code != tc.code {
			t.Errorf("%s: code = %q, want %q", tc.handle, id.Code, tc.code)
		}
		if id.Message != tc.message {
			t.Errorf("%s: message = %q, want %q", tc.handle, id.Message, tc.message)
		}
		if id.AliasOf != tc.aliasOf {
			t.Errorf("%s: aliasOf = %q, want %q", tc.handle, id.AliasOf, tc.aliasOf)
		}
		if id.Importable != tc.importable {
			t.Errorf("%s: importable = %v, want %v", tc.handle, id.Importable, tc.importable)
		}
	}

	// The same bare name is declared in two packages. Keying on the name alone
	// would merge them, which is the collapse the (package, name) key exists to
	// prevent, so both must survive as separate entries.
	if len(got) != 6 {
		t.Errorf("extracted %d identities, want 6: %v", len(got), got)
	}
	for handle := range got {
		if strings.HasSuffix(handle, ".NotAnIdentity") {
			t.Errorf("%s was extracted; a constant of the identity type whose name is not an identity must not be", handle)
		}
	}
}

func TestScan_RefusesACodeWithNoRegisteredMessage(t *testing.T) {
	t.Parallel()
	// Refusing is the point. A code with no registration renders as text naming
	// the unregistered code, and a page carrying that reads as a description
	// while describing nothing — the shape a generator must not paper over.
	_, err := scan("testdata/unregistered", "example.test/unregistered")
	if err == nil {
		t.Fatal("scan accepted a code with no registered message")
	}
	if !strings.Contains(err.Error(), "pkg/thing.ErrOrphan") {
		t.Errorf("error does not name the offending handle: %v", err)
	}
}

func TestRenderRegion_OmitsUnimportablePackagesAndUnreachableOrigins(t *testing.T) {
	t.Parallel()
	ids, err := scan("testdata/module", "example.test/fixture")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	region, err := renderRegion(ids)
	if err != nil {
		t.Fatalf("renderRegion: %v", err)
	}

	// An entry naming a package a reader cannot import advertises a match no
	// reader can perform, so no unimportable path may appear anywhere in the
	// region — neither as a section nor beside a re-export.
	if strings.Contains(region, "internal/") {
		t.Errorf("region names an unimportable package:\n%s", region)
	}
	if strings.Contains(region, "ErrHidden` is the same value as") {
		t.Error("region names the origin of a re-export whose origin is unreachable")
	}
	// The re-exported name itself is matchable, so its row belongs on the page.
	if !strings.Contains(region, "| `ErrHidden` |") {
		t.Errorf("region omits an importable re-export:\n%s", region)
	}
	// Where both names are reachable, the pairing is stated.
	if !strings.Contains(region, "- `ErrPlain` is the same value as `pkg/thing.ErrPlain`, so a match against either name succeeds.") {
		t.Errorf("region omits the reachable re-export pairing:\n%s", region)
	}
	// A message quoting authoring syntax carries a backtick; a fixed one-backtick
	// wrapper would end the span early and spill the rest into the page as prose.
	if !strings.Contains(region, "| ``the `uses:` block is malformed`` |") {
		t.Errorf("region does not fence around a backtick in a message:\n%s", region)
	}
	// Sections are ordered so the output is a function of the declarations alone.
	if got := strings.Index(region, "### pkg/other"); got == -1 || got > strings.Index(region, "### pkg/thing") {
		t.Errorf("sections are not in package order:\n%s", region)
	}
}

func TestRenderRegion_RefusesAMessageCarryingATableSeparator(t *testing.T) {
	t.Parallel()
	// No message carries one today. The refusal exists because no escaping
	// renders a separator correctly inside a cell, so the alternative to
	// refusing is a silently malformed table.
	_, err := renderRegion([]Identity{
		{Pkg: "pkg/thing", Name: "ErrPiped", Message: "a | b", Importable: true},
	})
	if err == nil {
		t.Fatal("renderRegion accepted a message carrying a table separator")
	}
	if !strings.Contains(err.Error(), "pkg/thing.ErrPiped") {
		t.Errorf("error does not name the offending handle: %v", err)
	}
}

func TestRenderRegion_LiveModuleNamesNoUnimportablePackage(t *testing.T) {
	t.Parallel()
	// The fixture proves the rule; this proves it holds for what actually ships.
	// The module really does re-export identities whose origins are internal, so
	// this is a live case rather than a hypothetical one.
	modulePath, err := modulePathOf("../../..")
	if err != nil {
		t.Fatalf("module path: %v", err)
	}
	ids, err := scan("../../..", modulePath)
	if err != nil {
		t.Fatalf("scan module: %v", err)
	}
	region, err := renderRegion(ids)
	if err != nil {
		t.Fatalf("renderRegion: %v", err)
	}
	if strings.Contains(region, "internal/") {
		t.Error("the rendered region names an unimportable package")
	}
	// Calibration: both assertions above read as "nothing is wrong" over an empty
	// region, so the region is proven non-empty against known content first.
	if !strings.Contains(region, "### pkg/resolver") {
		t.Fatal("the rendered region carries no resolver section; the scan is broken, not the rule")
	}
}

func TestCodeSpan_FencesAndPads(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{in: "plain", want: "`plain`"},
		{in: "one `tick` inside", want: "``one `tick` inside``"},
		{in: "``two`` inside", want: "``` ``two`` inside ```"},
		{in: "`leading", want: "`` `leading ``"},
		{in: "trailing`", want: "`` trailing` ``"},
	} {
		if got := codeSpan(tc.in); got != tc.want {
			t.Errorf("codeSpan(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// haltsPageIn writes a halting-conditions page carrying body into a temporary
// documentation tree and returns the tree root.
func haltsPageIn(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, filepath.Dir(haltsRelPath))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, haltsRelPath), []byte(body), 0o600); err != nil {
		t.Fatalf("write page: %v", err)
	}
	return root
}

func fixtureIdentities(t *testing.T) []Identity {
	t.Helper()
	ids, err := scan("testdata/module", "example.test/fixture")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return ids
}

func TestResolveCoverage_CreditsAReExportToTheIdentityItForwardsTo(t *testing.T) {
	t.Parallel()
	// The anchor names the re-export, which is the name a caller was told to
	// match. Crediting it to the origin is what keeps one condition from being
	// counted as covered under one name and gapped under the other.
	root := haltsPageIn(t, `# Halts

### Something went wrong

<!-- covers: pkg/other.ErrPlain -->

Prose.
`)
	covered, err := resolveCoverage(fixtureIdentities(t), root)
	if err != nil {
		t.Fatalf("resolveCoverage: %v", err)
	}
	if !covered["pkg/thing.ErrPlain"] {
		t.Errorf("naming the re-export did not credit its origin: %v", covered)
	}
	if covered["pkg/other.ErrPlain"] {
		t.Errorf("the re-export was credited as an identity of its own: %v", covered)
	}
}

func TestResolveCoverage_RefusesADeclarationThatResolvesToNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, anchor, want string
	}{
		{
			name:   "no such identity",
			anchor: "pkg/thing.ErrAbsent",
			want:   "no such identity is declared: pkg/thing.ErrAbsent",
		},
		{
			// A bare name names no single identity in a module that declares
			// the same name in two packages, so it is refused rather than
			// guessed at — a guess would silently claim the wrong one.
			name:   "unqualified name",
			anchor: "ErrPlain",
			want:   "not a qualified package.Name handle: ErrPlain",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := haltsPageIn(t, "# Halts\n\n### Entry\n\n<!-- covers: "+tc.anchor+" -->\n")
			_, err := resolveCoverage(fixtureIdentities(t), root)
			if err == nil {
				t.Fatalf("resolveCoverage accepted %q", tc.anchor)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not name the defect: %v", err)
			}
		})
	}
}

func TestResolveCoverage_RefusesAPageWithNoAnchors(t *testing.T) {
	t.Parallel()
	// Zero anchors and a page that covers nothing are indistinguishable in the
	// output, so the moved-marker case is reported as the instrument failure it
	// is rather than served as a full set of gaps.
	root := haltsPageIn(t, "# Halts\n\n### Entry\n\nProse with no anchor at all.\n")
	if _, err := resolveCoverage(fixtureIdentities(t), root); err == nil {
		t.Fatal("resolveCoverage accepted a page carrying no anchor")
	} else if !strings.Contains(err.Error(), "the anchor form or the page layout moved") {
		t.Errorf("error does not report an instrument failure: %v", err)
	}
}

func TestReportGaps_NamesEveryUncoveredOriginAndNoReExport(t *testing.T) {
	t.Parallel()
	root := haltsPageIn(t, "# Halts\n\n### Entry\n\n<!-- covers: pkg/other.ErrPlain, pkg/other.ErrOwn -->\n")
	ids := fixtureIdentities(t)
	covered, err := resolveCoverage(ids, root)
	if err != nil {
		t.Fatalf("resolveCoverage: %v", err)
	}
	var b strings.Builder
	reportGaps(&b, ids, covered, true)
	got := b.String()

	// Four origins; two are covered, so two remain — and the count in the
	// header is derived from the same walk rather than written down.
	if !strings.Contains(got, "WARN: 2 of 4 declared halting conditions have no entry") {
		t.Errorf("header does not report the derived counts:\n%s", got)
	}
	for _, want := range []string{"pkg/thing.ErrQuoted", "internal/hidden.ErrHidden"} {
		if !strings.Contains(got, want) {
			t.Errorf("gap report omits %s:\n%s", want, got)
		}
	}
	// A re-export is the same identity under a second name. Counting it would
	// report one condition as two gaps against a population that never grew.
	if strings.Contains(got, "pkg/wrap.ErrHidden") {
		t.Errorf("gap report names a re-export:\n%s", got)
	}
	// The message is printed beside the handle so the report can be acted on
	// without looking each identity up.
	if !strings.Contains(got, "pkg/thing.ErrQuoted: the `uses:` block is malformed") {
		t.Errorf("gap report omits the registered message:\n%s", got)
	}

	// The summary form is what the routine build prints, so it carries the same
	// counts and names no identity — the enumeration is what -gaps adds.
	var summary strings.Builder
	reportGaps(&summary, ids, covered, false)
	s := summary.String()
	if !strings.Contains(s, "WARN: 2 of 4 declared halting conditions have no entry") {
		t.Errorf("summary does not report the derived counts:\n%s", s)
	}
	if strings.Contains(s, "pkg/thing.ErrQuoted") {
		t.Errorf("summary enumerates identities without -gaps:\n%s", s)
	}
	if !strings.Contains(s, "-gaps") {
		t.Errorf("summary does not say how to see the list:\n%s", s)
	}
}

func TestResolveCoverage_LiveModuleAnchorsAllResolve(t *testing.T) {
	t.Parallel()
	// The fixture proves the rules; this proves the published page's own
	// declarations still name identities that exist. A rename that left an
	// anchor behind would otherwise be found only by the drift gate.
	modulePath, err := modulePathOf("../../..")
	if err != nil {
		t.Fatalf("module path: %v", err)
	}
	ids, err := scan("../../..", modulePath)
	if err != nil {
		t.Fatalf("scan module: %v", err)
	}
	docsRoot := "../../../../aggrega-docs"
	if _, serr := os.Stat(filepath.Join(docsRoot, haltsRelPath)); serr != nil {
		t.Skipf("published pages not present (standalone checkout): %v", serr)
	}
	covered, err := resolveCoverage(ids, docsRoot)
	if err != nil {
		t.Fatalf("the published page declares a correspondence that resolves to nothing: %v", err)
	}
	// Calibration: the assertion above reads as "nothing is wrong" over an
	// empty set, so the set is proven non-empty against a known entry first.
	if !covered["pkg/resolver.ErrParameterMissing"] {
		t.Fatal("no coverage extracted for a known anchored identity; the extraction is broken, not the page")
	}
}

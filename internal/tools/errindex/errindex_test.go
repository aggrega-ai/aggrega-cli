// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"go/build/constraint"
	"os"
	"path/filepath"
	"slices"
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
		// A re-export registers nothing of its own. It forwards to a condition and
		// returns that condition's value, so it reports the same code and the same
		// message; both are resolved through the forward rather than left empty.
		{handle: "pkg/other.ErrPlain", code: "thing/plain", aliasOf: "pkg/thing.ErrPlain", message: "a plain condition", importable: true},
		{handle: "pkg/wrap.ErrHidden", code: "hidden/hidden", aliasOf: "internal/hidden.ErrHidden", message: "raised from a package no reader can import", importable: true},
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
			t.Errorf("%s was extracted; a registered condition published under a name that is not "+
				"an identity must not be", handle)
		}
	}
}

func TestScan_SkipsGuestEntryPointsAndKeepsTheCoresBesideThem(t *testing.T) {
	t.Parallel()
	// The fixture puts all four cases under one root, because the property is a
	// distinction between files in the same tree and a fixture holding only one
	// side of it would pass under a predicate that answered by directory.
	got := scanFixture(t, "testdata/guestsplit", "example.test/guestsplit")

	for _, tc := range []struct {
		handle string
		want   bool
		why    string
	}{
		{
			handle: "plugins/thing-provider/thingcore.ErrCoreShared",
			want:   true,
			why:    "a core under the plugin tree carries no constraint and declares a condition a caller matches",
		},
		{
			handle: "pkg/portable.ErrNonUnixFallback",
			want:   true,
			why:    "a constraint a tag-free build satisfies restricts the file to nothing",
		},
		{
			handle: "plugins/thing-provider.ErrGuestEntryOnly",
			want:   false,
			why:    "a guest entry point is constrained to the guest target and enters no host binary",
		},
		{
			handle: "plugins/thing-provider/thingcore.ErrSuffixConstrained",
			want:   false,
			why:    "a file name restricting the build to the guest target constrains as firmly as the comment does",
		},
	} {
		if _, ok := got[tc.handle]; ok != tc.want {
			t.Errorf("%s extracted = %v, want %v: %s", tc.handle, ok, tc.want, tc.why)
		}
	}

	// Both assertions above read as "nothing is wrong" over an empty result, so
	// the population is pinned: two of the four declarations survive and no
	// other identity appears.
	if len(got) != 2 {
		t.Errorf("extracted %d identities, want 2: %v", len(got), got)
	}
}

func TestRequiresGuestTag_DecidesNecessityRatherThanOneAssignment(t *testing.T) {
	t.Parallel()
	// Each row is a constraint shape the module carries or plausibly will. The
	// two marked below are the reason this is decided by satisfiability: a
	// predicate evaluating the expression once, with every non-guest tag held
	// true, answers both of them wrongly and in opposite directions.
	for _, tc := range []struct {
		line string
		want bool
	}{
		{line: "//go:build wasip1", want: true},
		{line: "//go:build wasm", want: true},
		{line: "//go:build wasip1 && light", want: true},
		{line: "//go:build !wasip1", want: false},
		{line: "//go:build !wasip1 && !light", want: false},
		{line: "//go:build !wasip1 && light", want: false},
		{line: "//go:build unix", want: false},
		{line: "//go:build clidocs", want: false},
		{line: "//go:build !clidocs", want: false},
		// Held true by the one-assignment reading, which reports a host file as
		// guest-only because `!unix` is false exactly when `unix` is held true.
		{line: "//go:build !unix", want: false},
		// Held false by the one-assignment reading for the mirror-image reason:
		// the file does build for a target that is not a guest.
		{line: "//go:build linux || wasip1", want: false},
	} {
		t.Run(tc.line, func(t *testing.T) {
			t.Parallel()
			expr, err := constraint.Parse(tc.line)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.line, err)
			}
			got, err := requiresGuestTag(expr)
			if err != nil {
				t.Fatalf("requiresGuestTag(%q): %v", tc.line, err)
			}
			if got != tc.want {
				t.Errorf("requiresGuestTag(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

func TestTargetSuffix_ReadsOnlyASuffixTheToolchainReads(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ path, want string }{
		{path: "a/b/source_resolution_wasip1.go", want: "wasip1"},
		{path: "a/b/shape_wasm.go", want: "wasm"},
		// No underscore, so the toolchain reads no suffix and neither does this:
		// a file called wasm.go is restricted by nothing.
		{path: "a/b/wasm.go", want: ""},
		{path: "a/b/errors.go", want: ""},
		// A segment that is no build target reads as itself and matches nothing
		// in the guest-tag set, which is what keeps an ordinary name inert.
		{path: "a/b/render_core.go", want: "core"},
	} {
		if got := targetSuffix(tc.path); got != tc.want {
			t.Errorf("targetSuffix(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestScan_RefusesAHandleWithNoRegistrationBehindIt(t *testing.T) {
	t.Parallel()
	// Refusing is the point. A handle with nothing registered behind it matches
	// whatever the name it returns happens to hold, and a page carrying that reads
	// as a description while describing nothing — the shape a generator must not
	// paper over.
	_, err := scan("testdata/unregistered", "example.test/unregistered")
	if err == nil {
		t.Fatal("scan accepted a handle with no registration behind it")
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
	// The row carries the call, because the bare name is a function value and
	// does not compile where an error is expected.
	if !strings.Contains(region, "| `ErrHidden()` |") {
		t.Errorf("region omits an importable re-export:\n%s", region)
	}
	// Every row reports the code, so a consumer holding one off the wire can
	// resolve it. A declaring name and a re-export both report it, and a
	// re-export reports the code of the condition it forwards to rather than
	// a blank cell — which is what a handle that reports a code at run time and
	// none here would look like.
	if !strings.Contains(region, "| `ErrOwn()` | `other/own` | `declared here` |") {
		t.Errorf("region omits the code of a declaring name:\n%s", region)
	}
	if !strings.Contains(region, "| `ErrHidden()` | `hidden/hidden` |") {
		t.Errorf("region omits the code a re-export forwards to:\n%s", region)
	}
	if !strings.Contains(region, "| Sentinel | Code | Message |") {
		t.Errorf("region does not head the code column:\n%s", region)
	}
	// Where both names are reachable, the pairing is stated.
	if !strings.Contains(region, "- `ErrPlain()` is the same value as `pkg/thing.ErrPlain()`, so a match against either name succeeds.") {
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

func TestRenderRegion_RefusesADeclaredStringCarryingATableSeparator(t *testing.T) {
	t.Parallel()
	// Neither a code nor a message carries one today. The refusal exists because
	// no escaping renders a separator correctly inside a cell, so the alternative
	// to refusing is a silently malformed table — and it covers both declared
	// strings, because a separator breaks a row identically whichever cell holds
	// it.
	for _, tc := range []struct {
		what string
		id   Identity
	}{
		{what: "message", id: Identity{Pkg: "pkg/thing", Name: "ErrPiped", Code: "thing/piped", Message: "a | b", Importable: true}},
		{what: "code", id: Identity{Pkg: "pkg/thing", Name: "ErrPiped", Code: "thing/a | b", Message: "a message", Importable: true}},
	} {
		_, err := renderRegion([]Identity{tc.id})
		if err == nil {
			t.Fatalf("renderRegion accepted a %s carrying a table separator", tc.what)
		}
		if !strings.Contains(err.Error(), "pkg/thing.ErrPiped") {
			t.Errorf("%s: error does not name the offending handle: %v", tc.what, err)
		}
		if !strings.Contains(err.Error(), tc.what) {
			t.Errorf("%s: error does not name which declared string carried it: %v", tc.what, err)
		}
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

	// Every row reports all three cells for what actually ships. An empty code
	// cell is the shape a broken re-export resolution takes — the row still
	// renders, the table still parses, and the reader is told the condition has no
	// code — so it is asserted structurally rather than against any copied value:
	// naming a real code here would be a second copy of a declaration.
	rows := 0
	for _, line := range strings.Split(region, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		rows++
		cells := strings.Split(strings.Trim(line, "|"), " | ")
		if len(cells) != 3 {
			t.Fatalf("row does not carry three cells: %q", line)
		}
		for i, c := range cells {
			if strings.TrimSpace(strings.Trim(strings.TrimSpace(c), "`")) == "" {
				t.Errorf("row cell %d is empty: %q", i, line)
			}
		}
	}
	if rows == 0 {
		t.Fatal("the rendered region carries no table rows; the walk above asserted nothing")
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

// resolveIn reads the page in docsRoot and resolves its declarations, which is
// the order the generator performs them in: one read, one segmentation, then
// every tier over the same entries.
func resolveIn(t *testing.T, ids []Identity, docsRoot string) (map[string]bool, error) {
	t.Helper()
	return resolveCoverage(ids, entriesIn(t, docsRoot), docsRoot)
}

// entriesIn reads and segments the halting-conditions page written into
// docsRoot.
func entriesIn(t *testing.T, docsRoot string) []pageEntry {
	t.Helper()
	entries, err := readEntries(docsRoot)
	if err != nil {
		t.Fatalf("readEntries: %v", err)
	}
	return entries
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
	covered, err := resolveIn(t, fixtureIdentities(t), root)
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
			_, err := resolveIn(t, fixtureIdentities(t), root)
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
	if _, err := resolveIn(t, fixtureIdentities(t), root); err == nil {
		t.Fatal("resolveCoverage accepted a page carrying no anchor")
	} else if !strings.Contains(err.Error(), "the anchor form or the page layout moved") {
		t.Errorf("error does not report an instrument failure: %v", err)
	}
}

func TestReportGaps_NamesEveryUncoveredOriginAndNoReExport(t *testing.T) {
	t.Parallel()
	root := haltsPageIn(t, "# Halts\n\n### Entry\n\n<!-- covers: pkg/other.ErrPlain, pkg/other.ErrOwn -->\n")
	ids := fixtureIdentities(t)
	covered, err := resolveIn(t, ids, root)
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

func TestProseWords_TreatsTypographyAsASeparatorAndNothingElse(t *testing.T) {
	t.Parallel()
	// A message and the prose quoting it are authored on different surfaces, so
	// they differ in typography routinely and in wording only when one of them
	// has drifted. Each pair below must compare equal for the tier's count to
	// mean drift rather than punctuation.
	same := []struct {
		name, a, b string
	}{
		{"case", "Two Contributors", "two contributors"},
		{"backticks and emphasis", "the `uses:` block", "the **uses:** block"},
		{"wrapped whitespace", "a plain\n   condition", "a plain condition"},
		{"dash width", "input-kind origins", "input—kind origins"},
		{"en dash and hyphen", "input–kind", "input-kind"},
		{"quotation curl", "the kind’s schema", "the kind's schema"},
		{"trailing punctuation", "no dots.", "no dots"},
		{"a colon inside a name", "locals: entry", "locals entry"},
	}
	for _, tc := range same {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, want := proseWords(tc.a), proseWords(tc.b); !slices.Equal(got, want) {
				t.Errorf("%q and %q read as different word sequences: %v vs %v", tc.a, tc.b, got, want)
			}
		})
	}

	// The other direction is what keeps the comparison worth making: a changed
	// word is a changed sequence.
	if slices.Equal(proseWords("write the same path"), proseWords("emit at the same path")) {
		t.Error("a reworded phrase read as the same word sequence")
	}
}

func TestQuotesRun_RequiresTheWholeSequenceInOrder(t *testing.T) {
	t.Parallel()
	have := proseWords("Recipe `<name>`: validation error — rule 1: valuesMapping requires source. declare a source")
	for _, tc := range []struct {
		name, want string
		found      bool
	}{
		{name: "a contiguous run", want: "rule 1: valuesMapping requires source", found: true},
		{name: "a run the prose splits", want: "recipe rule 1", found: false},
		{name: "the right words reordered", want: "source requires valuesMapping", found: false},
		{name: "a word the prose does not carry", want: "rule 1: valuesMapping demands source", found: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := quotesRun(have, proseWords(tc.want)); got != tc.found {
				t.Errorf("quotesRun(%q) = %v, want %v", tc.want, got, tc.found)
			}
		})
	}
}

func TestClaimsIn_CountsAnEntryWhoseProseDoesNotQuoteTheRegisteredMessage(t *testing.T) {
	t.Parallel()
	// The first entry quotes its condition's message in the frame the build
	// emits; the second describes its condition without carrying what the build
	// reports, which is the drift this tier exists to see.
	root := haltsPageIn(t, "# Halts\n\n"+
		"### Something plain went wrong\n\n"+
		"<!-- covers: pkg/thing.ErrPlain -->\n\n"+
		"Emitted message:\n> thing: a plain condition: fix the thing\n\n"+
		"### The block is not shaped right\n\n"+
		"<!-- covers: pkg/thing.ErrQuoted -->\n\n"+
		"Emitted message:\n> thing: the block cannot be read\n")

	unquoted, compared, unreadable := claimsIn(fixtureIdentities(t), entriesIn(t, root))
	if len(unreadable) != 0 {
		t.Fatalf("comparisons that did not happen: %v", unreadable)
	}
	if compared != 2 {
		t.Errorf("compared %d claims, want 2", compared)
	}
	if len(unquoted) != 1 {
		t.Fatalf("counted %d entries, want 1: %+v", len(unquoted), unquoted)
	}
	if unquoted[0].Handle != "pkg/thing.ErrQuoted" {
		t.Errorf("named %s rather than the entry that does not quote its message", unquoted[0].Handle)
	}
	if unquoted[0].Heading != "The block is not shaped right" {
		t.Errorf("named the heading %q rather than the entry's own", unquoted[0].Heading)
	}
}

func TestClaimsIn_DoesNotPassAnEntryOnProseBelongingToAnother(t *testing.T) {
	t.Parallel()
	// One condition covered by two entries, because it is met in two ways. The
	// entry that quotes the message passes; the entry that does not is counted
	// even though the words sit elsewhere on the page. Crediting the condition
	// once would let the second entry read as verified while describing nothing.
	root := haltsPageIn(t, "# Halts\n\n"+
		"### The block is malformed as authored\n\n"+
		"<!-- covers: pkg/thing.ErrQuoted -->\n\n"+
		"Emitted message:\n> the `uses:` block is malformed\n\n"+
		"### The block is malformed after a merge\n\n"+
		"<!-- covers: pkg/thing.ErrQuoted -->\n\n"+
		"The merge produced something unreadable.\n")

	unquoted, compared, unreadable := claimsIn(fixtureIdentities(t), entriesIn(t, root))
	if len(unreadable) != 0 {
		t.Fatalf("comparisons that did not happen: %v", unreadable)
	}
	if compared != 2 {
		t.Errorf("compared %d claims, want 2 — the unit is the pair, not the condition", compared)
	}
	if len(unquoted) != 1 || unquoted[0].Heading != "The block is malformed after a merge" {
		t.Fatalf("the entry lacking the message was not the one counted: %+v", unquoted)
	}
}

func TestClaimsIn_ReadsASubsectionAsAnEntryOfItsOwn(t *testing.T) {
	t.Parallel()
	// The page nests entries under a shared heading, so a subsection's claim is
	// checked against the subsection alone. A flat split at one heading level
	// would hand both siblings the same prose, and either sibling would then pass
	// on the other's text.
	root := haltsPageIn(t, "# Halts\n\n"+
		"### Shape rules\n\n"+
		"Rules a Recipe has to satisfy.\n\n"+
		"#### Rule one\n\n"+
		"<!-- covers: pkg/thing.ErrPlain -->\n\n"+
		"Emitted message:\n> a plain condition\n\n"+
		"#### Rule two\n\n"+
		"<!-- covers: pkg/thing.ErrQuoted -->\n\n"+
		"Emitted message:\n> something else entirely\n")

	unquoted, compared, unreadable := claimsIn(fixtureIdentities(t), entriesIn(t, root))
	if len(unreadable) != 0 {
		t.Fatalf("comparisons that did not happen: %v", unreadable)
	}
	if compared != 2 {
		t.Errorf("compared %d claims, want 2", compared)
	}
	if len(unquoted) != 1 || unquoted[0].Heading != "Rule two" {
		t.Fatalf("a sibling subsection passed on its neighbour's prose: %+v", unquoted)
	}
}

func TestClaimsIn_CreditsAReExportWithTheMessageItForwardsTo(t *testing.T) {
	t.Parallel()
	// A re-export registers no message of its own, so the text an entry has to
	// quote is the one the identity it forwards to registers. Naming either
	// handle has to compare against the same words.
	root := haltsPageIn(t, "# Halts\n\n"+
		"### Something plain went wrong\n\n"+
		"<!-- covers: pkg/other.ErrPlain -->\n\n"+
		"Emitted message:\n> a plain condition\n")

	unquoted, compared, unreadable := claimsIn(fixtureIdentities(t), entriesIn(t, root))
	if len(unreadable) != 0 {
		t.Fatalf("comparisons that did not happen: %v", unreadable)
	}
	if compared != 1 || len(unquoted) != 0 {
		t.Fatalf("compared %d and counted %d; a re-export's entry did not read against its origin's message: %+v",
			compared, len(unquoted), unquoted)
	}
}

func TestClaimsIn_ReportsADeclarationItCouldNotPlaceRatherThanSkippingIt(t *testing.T) {
	t.Parallel()
	// A declaration no heading opens has no entry prose to read, so no
	// comparison is possible. Dropping it would leave the tier comparing fewer
	// claims than the page declares and reporting nothing, which reads exactly
	// like a page with nothing wrong.
	root := haltsPageIn(t, "<!-- covers: pkg/thing.ErrPlain -->\n\n# Halts\n\nProse.\n")

	unquoted, compared, unreadable := claimsIn(fixtureIdentities(t), entriesIn(t, root))
	if compared != 0 || len(unquoted) != 0 {
		t.Errorf("a declaration with no entry was compared anyway: compared=%d counted=%+v", compared, unquoted)
	}
	if len(unreadable) != 1 || !strings.Contains(unreadable[0], "pkg/thing.ErrPlain") {
		t.Fatalf("the unplaceable declaration was not reported: %v", unreadable)
	}
	if !strings.Contains(unreadable[0], "no heading opens") {
		t.Errorf("the report does not say why the comparison was impossible: %q", unreadable[0])
	}

	// And it reaches the writer whether or not the enumeration was asked for,
	// because it is the instrument failing rather than a note about the page.
	var b strings.Builder
	reportClaims(&b, fixtureIdentities(t), entriesIn(t, root), false)
	if !strings.Contains(b.String(), "could not be compared") {
		t.Errorf("the summary form hides a comparison that did not happen:\n%s", b.String())
	}
}

func TestReportClaims_CountsAlwaysAndEnumeratesOnRequest(t *testing.T) {
	t.Parallel()
	root := haltsPageIn(t, "# Halts\n\n"+
		"### Something plain went wrong\n\n"+
		"<!-- covers: pkg/thing.ErrPlain -->\n\n"+
		"Emitted message:\n> a plain condition\n\n"+
		"### The block is not shaped right\n\n"+
		"<!-- covers: pkg/thing.ErrQuoted -->\n\n"+
		"Emitted message:\n> the block cannot be read\n")
	ids := fixtureIdentities(t)
	entries := entriesIn(t, root)

	var listed strings.Builder
	reportClaims(&listed, ids, entries, true)
	got := listed.String()
	if !strings.Contains(got, "WARN: 1 of 2 entry claims") {
		t.Errorf("header does not report the derived counts:\n%s", got)
	}
	if !strings.Contains(got, "pkg/thing.ErrQuoted") {
		t.Errorf("the enumeration omits the entry counted:\n%s", got)
	}
	// The registered message is printed beside the entry, because deciding
	// whether an entry drifted or is reframing means seeing the text it was
	// meant to carry.
	if !strings.Contains(got, "the `uses:` block is malformed") {
		t.Errorf("the enumeration omits the registered message:\n%s", got)
	}

	// The summary form is what the routine build prints: the same counts, no
	// entry named, and the way to see the list.
	var summary strings.Builder
	reportClaims(&summary, ids, entries, false)
	s := summary.String()
	if !strings.Contains(s, "WARN: 1 of 2 entry claims") {
		t.Errorf("summary does not report the derived counts:\n%s", s)
	}
	if strings.Contains(s, "pkg/thing.ErrQuoted") {
		t.Errorf("summary enumerates entries unasked:\n%s", s)
	}
	if !strings.Contains(s, "-claims") {
		t.Errorf("summary does not say how to see the list:\n%s", s)
	}
}

func TestReportClaims_SaysSoWhenEveryEntryQuotesItsMessage(t *testing.T) {
	t.Parallel()
	// A clean page has to read as a clean page rather than as silence, so the
	// count is printed either way.
	root := haltsPageIn(t, "# Halts\n\n"+
		"### Something plain went wrong\n\n"+
		"<!-- covers: pkg/thing.ErrPlain -->\n\n"+
		"Emitted message:\n> A PLAIN CONDITION.\n")
	var b strings.Builder
	reportClaims(&b, fixtureIdentities(t), entriesIn(t, root), false)
	if got := b.String(); !strings.Contains(got, "WARN: none — all 1 entry claims") {
		t.Errorf("a clean page does not report its own count:\n%s", got)
	}
}

func TestHeadingLevel_ReadsOnlyAMarkerASpaceFollows(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		line string
		want int
	}{
		{"# Halts", 1},
		{"#### Rule one", 4},
		{"####### too deep", 0},
		{"#NoSpace", 0},
		{"#", 0},
		{"", 0},
		{"Prose mentioning # a marker", 0},
	} {
		if got := headingLevel(tc.line); got != tc.want {
			t.Errorf("headingLevel(%q) = %d, want %d", tc.line, got, tc.want)
		}
	}
}

func TestReadEntries_DoesNotReadAFencedSampleAsAHeading(t *testing.T) {
	t.Parallel()
	// A code sample is the one place a line may open with the heading marker
	// while opening no heading. Reading one as a heading would split the page at
	// a boundary no reader sees, and every claim after it would be compared
	// against the wrong prose.
	root := haltsPageIn(t, "# Halts\n\n"+
		"### Something plain went wrong\n\n"+
		"<!-- covers: pkg/thing.ErrPlain -->\n\n"+
		"```yaml\n# a comment, not a heading\nvalues: {}\n```\n\n"+
		"Emitted message:\n> a plain condition\n")

	unquoted, compared, unreadable := claimsIn(fixtureIdentities(t), entriesIn(t, root))
	if len(unreadable) != 0 {
		t.Fatalf("comparisons that did not happen: %v", unreadable)
	}
	if compared != 1 || len(unquoted) != 0 {
		t.Fatalf("a fenced sample split the entry: compared=%d counted=%+v", compared, unquoted)
	}
}

// There is deliberately no test here reading the published halting-conditions
// page. The generator resolves every coverage anchor on that page before it
// writes anything, and the drift check that regenerates the sentinel reference
// runs the generator over a copy of the whole published errors directory on
// every routine build — so an anchor naming an identity nothing declares
// already fails there, unconditionally.
//
// A test doing the same reads a file outside this module root, which no test
// cache key covers: it would be the weaker of the two checks and the only one
// able to serve a stale pass. It would also be the only one able to skip
// itself when the sibling tree is absent. The fixture tests above prove every
// rule the resolver applies, which is the part that belongs in this package.

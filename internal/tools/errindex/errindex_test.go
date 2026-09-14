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
		shortCode  string
		message    string
		aliasOf    string
		importable bool
	}{
		{handle: "pkg/thing.ErrPlain", code: "thing/plain", shortCode: "E9001", message: "a plain condition", importable: true},
		{handle: "pkg/thing.ErrQuoted", code: "thing/quoted", shortCode: "E9002", message: "the `uses:` block is malformed", importable: true},
		{handle: "pkg/other.ErrOwn", code: "other/own", shortCode: "E9004", message: "declared here", importable: true},
		// A re-export registers nothing of its own. It forwards to a condition and
		// returns that condition's value, so it reports the same two codes and the
		// same message; all three are resolved through the forward rather than
		// left empty.
		{handle: "pkg/other.ErrPlain", code: "thing/plain", shortCode: "E9001", aliasOf: "pkg/thing.ErrPlain", message: "a plain condition", importable: true},
		{handle: "pkg/wrap.ErrHidden", code: "hidden/hidden", shortCode: "E9005", aliasOf: "internal/hidden.ErrHidden", message: "raised from a package no reader can import", importable: true},
		{handle: "internal/hidden.ErrHidden", code: "hidden/hidden", shortCode: "E9005", message: "raised from a package no reader can import", importable: false},
	} {
		id, ok := got[tc.handle]
		if !ok {
			t.Errorf("%s: not extracted", tc.handle)
			continue
		}
		if id.Code != tc.code {
			t.Errorf("%s: code = %q, want %q", tc.handle, id.Code, tc.code)
		}
		if id.ShortCode != tc.shortCode {
			t.Errorf("%s: shortCode = %q, want %q", tc.handle, id.ShortCode, tc.shortCode)
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

// TestScan_RefusesARegistrationWithNoShortCode is the companion refusal: a
// declaration that HAS a registration behind it but omits one of the two
// identifiers that registration declares.
//
// It is refused rather than served with an empty column for the reason the
// refusal above is refused — a blank cell reads as a condition that has no short
// code, not as a declaration that forgot one — and the scan is the only place
// that can say so, because it reads syntax rather than a running program.
func TestScan_RefusesARegistrationWithNoShortCode(t *testing.T) {
	t.Parallel()
	_, err := scan("testdata/noshortcode", "example.test/noshortcode")
	if err == nil {
		t.Fatal("scan accepted a registration declaring no short code")
	}
	if !strings.Contains(err.Error(), "pkg/thing.ErrNoShortCode") {
		t.Errorf("error does not name the offending handle: %v", err)
	}
	if !strings.Contains(err.Error(), "short code") {
		t.Errorf("error does not say which identifier is missing, so a reader cannot act on it: %v", err)
	}
}

// The one registration the rule-place fixtures below declare, and the two values
// every assertion over them is taken against. They are constants so that a test
// asserts the message the fixture declared rather than a second copy of it — the
// corruption these tests exist to catch is one that replaces the message with
// something else, and an assertion written against a literal repeated at the
// assertion could be written against the wrong one.
const (
	placeFixtureModule    = "example.test/place"
	placeFixtureHandle    = "pkg/thing.ErrPlaced"
	placeFixtureShortCode = "E0001"
	placeFixtureMessage   = "the sentence this condition registers"
)

// moduleDeclaringPlace writes a module whose single registration carries the
// given rule-place value, and returns its root. An empty value writes no
// rule-place key at all, which is the ordinary shape of every registration that
// has not declared its place.
func moduleDeclaringPlace(t *testing.T, place string) string {
	t.Helper()

	root := t.TempDir()
	// The stand-in for the package that declares the identity carries the
	// rule-place field and the four constructors, so a fixture below is a
	// registration that declares the key rather than one that could not express
	// it — the distinction every fixture in this tree is written to make.
	errorsBody := `package errors

type Meta struct {
	ShortCode string
	Message   string
	RulePlace RulePlace
}

type RulePlace struct{ class int }

func RuleOverAuthoredInput() RulePlace              { return RulePlace{class: 1} }
func RuleEvaluatedElsewhere(string) RulePlace       { return RulePlace{class: 2} }
func RuleNoStageReaches(string) RulePlace           { return RulePlace{class: 3} }
func NotARule(string) RulePlace                     { return RulePlace{class: 4} }

type sealed struct{ code, shortCode, message string }

func (s *sealed) Error() string { return s.message }

func Register(code string, meta Meta) *sealed {
	return &sealed{code: code, shortCode: meta.ShortCode, message: meta.Message}
}
`
	declaration := ""
	if place != "" {
		declaration = "\n\tRulePlace: " + place + ","
	}
	thingBody := `package thing

import cerrors "` + placeFixtureModule + `/pkg/errors"

func ErrPlaced() error { return errPlaced }

var errPlaced = cerrors.Register("thing/placed", cerrors.Meta{
	ShortCode: "` + placeFixtureShortCode + `",
	Message:   "` + placeFixtureMessage + `",` + declaration + `
})
`
	for path, body := range map[string]string{
		filepath.Join("pkg", "errors", "code.go"):  errorsBody,
		filepath.Join("pkg", "thing", "errors.go"): thingBody,
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

// TestScan_ReadsARulePlaceAndLeavesTheMessageIntact is the positive half, and
// the message assertion is the load-bearing one rather than a formality.
//
// The scan recognises a fixed set of metadata keys and skips every other. The
// shape that fails here is a key admitted to that set without an arm of its own,
// which falls through to whatever handles the remainder — and where the
// remainder is the message, the condition's own sentence is replaced by the
// value of the newly admitted key, reported as the message, with nothing
// failing and no column blank. Asserting the message BYTE FOR BYTE on a
// registration that carries the new key is what catches it; asserting only that
// the scan succeeded would not.
//
// All four places are exercised, and the absent case with them. They differ in
// whether the value carries a literal at all, and a reader that had come to fold
// one of them into the message would pass an assertion taken over the others.
func TestScan_ReadsARulePlaceAndLeavesTheMessageIntact(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		place string
	}{
		{"no place declared", ""},
		{"a rule over authored input", "cerrors.RuleOverAuthoredInput()"},
		{"a rule another evaluator owns", `cerrors.RuleEvaluatedElsewhere("A plugin guest evaluates it.")`},
		{"a rule no stage reaches", `cerrors.RuleNoStageReaches("A catalog directory load reads it.")`},
		{"no rule at all", `cerrors.NotARule("A registry answered the request.")`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ids, err := scan(moduleDeclaringPlace(t, tc.place), placeFixtureModule)
			if err != nil {
				t.Fatalf("scan refused a well-formed declaration of %s: %v", tc.name, err)
			}
			var found bool
			for _, id := range ids {
				if id.Handle() != placeFixtureHandle {
					continue
				}
				found = true
				if id.Message != placeFixtureMessage {
					t.Errorf("the message read for %s is %q, want %q; a metadata key admitted without an "+
						"arm of its own is written into the condition's own sentence",
						tc.name, id.Message, placeFixtureMessage)
				}
				if id.ShortCode != placeFixtureShortCode {
					t.Errorf("the short code read for %s is %q, want %q", tc.name, id.ShortCode, placeFixtureShortCode)
				}
			}
			if !found {
				t.Fatalf("the scan returned no identity for %s, so nothing above was asserted", placeFixtureHandle)
			}
		})
	}
}

// TestScan_RefusesARulePlaceItCannotRead is the negative half. The reasoning for
// refusing rather than skipping is on declaredPlace: a value this scan passes
// over as absent is a value the other reader of these same registrations reports
// on, and the two readings then describe different declarations with nothing
// failing.
//
// Each row names one malformed shape and the text the refusal must carry, so a
// refusal that had collapsed into one message for every shape fails here rather
// than passing as "it refused something".
func TestScan_RefusesARulePlaceItCannotRead(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		place string
		names string
	}{
		{"a value that is not a call", `"a rule over authored input"`, "not a call"},
		{"a constructor this scan does not know", "cerrors.SomewhereElse()", "not one of the four"},
		{"the place that takes no reason, given one", `cerrors.RuleOverAuthoredInput("why")`, "takes no reason"},
		{"a place that takes a reason, given none", "cerrors.NotARule()", "takes one reason"},
		{"a reason that is not a literal", "cerrors.NotARule(whyThisIsNotARule)", "not a string literal"},
		{"a reason that says nothing", `cerrors.NotARule("   ")`, "says nothing"},
		{"the newest place, given a reason that says nothing", `cerrors.RuleNoStageReaches(" \t ")`, "says nothing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ids, err := scan(moduleDeclaringPlace(t, tc.place), placeFixtureModule)
			if err == nil {
				t.Fatalf("the scan accepted %s and read %d identity(ies)", tc.name, len(ids))
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("the refusal does not say %q, so a reader cannot tell what is wrong with the "+
					"declaration: %v", tc.names, err)
			}
			if !strings.Contains(err.Error(), "errPlaced") {
				t.Errorf("the refusal does not name the offending registration, so a reader cannot find "+
					"it: %v", err)
			}
		})
	}
}

// TestRenderIndex_CarriesBothIdentifiersOnEveryRecord pins the machine-readable
// record shape, which is a published contract: two consumers decode it by
// position and each pins the field count deliberately, so a silent change to the
// width or the order breaks them rather than degrading them.
//
// A re-export is included because its short code is resolved through the forward
// rather than declared, so it is the row a naive extraction leaves blank.
func TestRenderIndex_CarriesBothIdentifiersOnEveryRecord(t *testing.T) {
	t.Parallel()

	ids, err := scan("testdata/module", "example.test/fixture")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	lines := strings.Split(strings.TrimRight(renderIndex(ids), "\n"), "\n")
	if len(lines) != len(ids) {
		t.Fatalf("rendered %d record(s) for %d identities", len(lines), len(ids))
	}

	const wantFields = 5
	seen := map[string][]string{}
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != wantFields {
			t.Fatalf("record %q has %d field(s), want %d; two consumers decode this by position",
				line, len(fields), wantFields)
		}
		if fields[0] != "origin" && fields[0] != "alias" {
			t.Errorf("record %q has an unknown kind %q", line, fields[0])
		}
		if fields[wantFields-1] == "" {
			t.Errorf("record %q carries no short code", line)
		}
		seen[fields[1]+"."+fields[2]] = fields
	}

	for _, tc := range []struct {
		handle string
		want   []string
	}{
		{handle: "pkg/thing.ErrPlain", want: []string{"origin", "pkg/thing", "ErrPlain", "thing/plain", "E9001"}},
		{handle: "internal/hidden.ErrHidden", want: []string{"origin", "internal/hidden", "ErrHidden", "hidden/hidden", "E9005"}},
		// The re-export's fourth field is the handle it forwards to, and its
		// short code is the forwarded condition's own.
		{handle: "pkg/other.ErrPlain", want: []string{"alias", "pkg/other", "ErrPlain", "pkg/thing.ErrPlain", "E9001"}},
	} {
		got, ok := seen[tc.handle]
		if !ok {
			t.Errorf("%s: no record rendered", tc.handle)
			continue
		}
		if strings.Join(got, "\t") != strings.Join(tc.want, "\t") {
			t.Errorf("%s: record = %q, want %q", tc.handle, got, tc.want)
		}
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
	uncovered, considered := reportGaps(&b, ids, covered, true)
	got := b.String()

	// Four origins; two are covered, so two remain — and the count in the
	// header is derived from the same walk rather than written down.
	if !strings.Contains(got, "WARN: 2 of 4 declared halting conditions have no entry") {
		t.Errorf("header does not report the derived counts:\n%s", got)
	}
	// The caller exits on these, so they are part of the contract rather than a
	// convenience: a report the caller cannot act on leaves the tier warning.
	if uncovered != 2 || considered != 4 {
		t.Errorf("returned (%d, %d), want (2, 4)", uncovered, considered)
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
	if n, _ := reportGaps(&summary, ids, covered, false); n != 2 {
		t.Errorf("summary form returned %d, want 2 — the shortfall cannot depend on the enumeration", n)
	}
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

func TestReportGaps_ReportsAShortfallAndClearsACompleteSet(t *testing.T) {
	t.Parallel()
	ids := fixtureIdentities(t)
	all := []string{"pkg/other.ErrPlain", "pkg/other.ErrOwn", "pkg/thing.ErrQuoted", "internal/hidden.ErrHidden"}
	pageCovering := func(t *testing.T, handles []string) map[string]bool {
		t.Helper()
		root := haltsPageIn(t, "# Halts\n\n### Entry\n\n<!-- covers: "+strings.Join(handles, ", ")+" -->\n")
		covered, err := resolveIn(t, ids, root)
		if err != nil {
			t.Fatalf("resolveCoverage: %v", err)
		}
		return covered
	}

	// The complete set is established first, so each shortfall below is a planted
	// defect against a baseline somebody looked at rather than a reading of a
	// page whose state nobody fixed. A tier whose figure has never been watched to
	// move is decoration: this is the pair that shows it moves on a hole and stays
	// quiet without one. The tier reports rather than refusing, deliberately, and
	// the assertion below that a clean run carries no refusal word is what keeps
	// the two readable apart.
	var clean strings.Builder
	if n, considered := reportGaps(&clean, ids, pageCovering(t, all), false); n != 0 || considered != 4 {
		t.Fatalf("a complete set returned (%d, %d), want (0, 4):\n%s", n, considered, clean.String())
	}
	if got := clean.String(); !strings.Contains(got, "coverage: all 4 declared halting conditions are named by an entry") {
		t.Errorf("the passing line does not report the size of the set it cleared:\n%s", got)
	}
	// The passing line carries none of the word a refusal carries, so the two are
	// told apart by reading either line rather than only by an exit code.
	if strings.Contains(clean.String(), "REFUSED") {
		t.Errorf("a complete set reports a refusal:\n%s", clean.String())
	}

	// One handle dropped at a time and nothing else changed, so every origin is
	// shown to be individually load-bearing. Dropping a single handle and
	// stopping there would leave three others able to carry the refusal, and a
	// tier that only ever refused for one of its members would look identical.
	//
	// A declaration may name a re-export, which is the same condition under a
	// name a caller was told to match, so the origin a dropped handle leaves
	// uncovered is not always the handle itself. Each row names both.
	for _, tc := range []struct{ dropped, uncovers string }{
		{"pkg/other.ErrPlain", "pkg/thing.ErrPlain"},
		{"pkg/other.ErrOwn", "pkg/other.ErrOwn"},
		{"pkg/thing.ErrQuoted", "pkg/thing.ErrQuoted"},
		{"internal/hidden.ErrHidden", "internal/hidden.ErrHidden"},
	} {
		t.Run("uncovered "+tc.uncovers, func(t *testing.T) {
			t.Parallel()
			kept := make([]string, 0, len(all)-1)
			for _, h := range all {
				if h != tc.dropped {
					kept = append(kept, h)
				}
			}
			var b strings.Builder
			n, considered := reportGaps(&b, ids, pageCovering(t, kept), true)
			if n != 1 || considered != 4 {
				t.Fatalf("dropping %s returned (%d, %d), want (1, 4):\n%s", tc.dropped, n, considered, b.String())
			}
			if got := b.String(); !strings.Contains(got, tc.uncovers) {
				t.Errorf("the refusal does not name the condition left uncovered:\n%s", got)
			}
		})
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

	unquoted, compared, _, unreadable := claimsIn(fixtureIdentities(t), entriesIn(t, root))
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

	unquoted, compared, _, unreadable := claimsIn(fixtureIdentities(t), entriesIn(t, root))
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

	unquoted, compared, _, unreadable := claimsIn(fixtureIdentities(t), entriesIn(t, root))
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

	unquoted, compared, _, unreadable := claimsIn(fixtureIdentities(t), entriesIn(t, root))
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

	unquoted, compared, _, unreadable := claimsIn(fixtureIdentities(t), entriesIn(t, root))
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
	if !strings.Contains(got, "REFUSED: 1 of 2 entry claims") {
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
	if !strings.Contains(s, "REFUSED: 1 of 2 entry claims") {
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
	if got := b.String(); !strings.Contains(got, "claims: all 1 entry claims") {
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

	unquoted, compared, _, unreadable := claimsIn(fixtureIdentities(t), entriesIn(t, root))
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

// shortCodeRows returns the short code of every row in a rendered short-code
// region, in the order the region carries them.
//
// It reads the first cell rather than matching a whole row, so a test asserting
// coverage cannot be satisfied by a row whose other cells moved.
func shortCodeRows(t *testing.T, region string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(region, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), " | ")
		if len(cells) != 3 {
			t.Fatalf("row does not carry three cells: %q", line)
		}
		for i, c := range cells {
			if strings.TrimSpace(strings.Trim(strings.TrimSpace(c), "`")) == "" {
				t.Errorf("row cell %d is empty: %q", i, line)
			}
		}
		out = append(out, strings.Trim(strings.TrimSpace(cells[0]), "`"))
	}
	return out
}

// TestRenderShortCodeRegion_CoversAConditionTheByPackageTablesCannot is the
// property the short-code table exists for, and the page's prose promises. The
// by-package region is keyed on an importable name and therefore cannot carry a
// condition declared where the module keeps it unreachable; this one is keyed on
// the short code, which every condition declares, so it carries that condition
// too.
//
// The two identities are built here rather than scanned, because the case needs
// an unreachable origin that NO importable name re-exports, and the shared
// fixture deliberately has the opposite: its unreachable origin is re-exported
// from a package a reader can import, which puts the forwarded condition on the
// by-package tables after all. That fixture is right for what it tests and adding
// a second unreachable origin to it would change the population every other test
// over it asserts against, so the distinction is drawn on identities constructed
// for it.
func TestRenderShortCodeRegion_CoversAConditionTheByPackageTablesCannot(t *testing.T) {
	t.Parallel()
	reachable := Identity{
		Pkg: "pkg/thing", Name: "ErrReachable", Code: "thing/reachable",
		ShortCode: "E0001", Message: "declared where a reader can import it", Importable: true,
	}
	// Unreachable and re-exported nowhere: the shape that has no matchable name
	// to put in the by-package region's left-hand column.
	unreachable := Identity{
		Pkg: "internal/deep", Name: "ErrUnreachable", Code: "deep/unreachable",
		ShortCode: "E0002", Message: "declared where no reader can import it",
	}
	ids := []Identity{reachable, unreachable}

	region, err := renderShortCodeRegion(ids, nil)
	if err != nil {
		t.Fatalf("renderShortCodeRegion: %v", err)
	}
	if !strings.Contains(region, "| Code | Condition | Message |") {
		t.Errorf("region does not head its columns:\n%s", region)
	}
	for _, want := range []string{
		"| `E0001` | `thing/reachable` | `declared where a reader can import it` |",
		"| `E0002` | `deep/unreachable` | `declared where no reader can import it` |",
	} {
		if !strings.Contains(region, want) {
			t.Errorf("region omits %s:\n%s", want, region)
		}
	}

	// The by-package region carries the reachable one and cannot carry the other,
	// so the two regions really do differ rather than both happening to be
	// complete. Calibrated in both directions: an assertion that it omits the
	// unreachable condition would also pass over an empty region.
	byPkg, err := renderRegion(ids)
	if err != nil {
		t.Fatalf("renderRegion: %v", err)
	}
	if !strings.Contains(byPkg, "`thing/reachable`") {
		t.Fatalf("the by-package region carries nothing at all, so the omission below proves nothing:\n%s", byPkg)
	}
	if strings.Contains(byPkg, "deep/unreachable") {
		t.Errorf("the by-package region names a condition no reader can match:\n%s", byPkg)
	}
}

// TestRenderShortCodeRegion_KeysOneCodeToOneRow holds the table's key property. A
// re-export reports the short code of the condition it forwards to, so admitting
// re-exports would put two rows under one code and leave a reader unable to tell
// which condition they are holding — in the one table whose whole job is to
// resolve that code.
func TestRenderShortCodeRegion_KeysOneCodeToOneRow(t *testing.T) {
	t.Parallel()
	ids, err := scan("testdata/module", "example.test/fixture")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	region, err := renderShortCodeRegion(ids, nil)
	if err != nil {
		t.Fatalf("renderShortCodeRegion: %v", err)
	}
	rows := shortCodeRows(t, region)

	// The fixture re-exports two conditions, so a region admitting re-exports
	// would carry more rows than there are conditions. Calibration first: the
	// fixture must actually contain a re-export, or this asserts nothing.
	reExports, origins := 0, 0
	for _, id := range ids {
		if id.AliasOf != "" {
			reExports++
			continue
		}
		origins++
	}
	if reExports == 0 {
		t.Fatal("the fixture declares no re-export, so this test cannot detect one being admitted")
	}
	if len(rows) != origins {
		t.Errorf("region carries %d rows for %d conditions and %d re-exports:\n%s",
			len(rows), origins, reExports, region)
	}
	seen := map[string]int{}
	for _, c := range rows {
		seen[c]++
	}
	for code, n := range seen {
		if n != 1 {
			t.Errorf("short code %s heads %d rows; a lookup keyed on it cannot name one condition", code, n)
		}
	}
}

// TestRenderShortCodeRegion_OrdersByShortCode pins the ordering the page's prose
// promises. Ordering as text is correct only because a short code is zero-padded
// to a fixed width; the assertion is against a numerically-sorted expectation, so
// a code that stopped being padded would fail here rather than silently sorting
// into the wrong place.
func TestRenderShortCodeRegion_OrdersByShortCode(t *testing.T) {
	t.Parallel()
	ids := []Identity{
		{Pkg: "pkg/a", Name: "ErrTen", Code: "a/ten", ShortCode: "E0010", Message: "ten", Importable: true},
		{Pkg: "pkg/a", Name: "ErrTwo", Code: "a/two", ShortCode: "E0002", Message: "two", Importable: true},
		{Pkg: "internal/b", Name: "ErrNine", Code: "b/nine", ShortCode: "E0009", Message: "nine"},
	}
	region, err := renderShortCodeRegion(ids, nil)
	if err != nil {
		t.Fatalf("renderShortCodeRegion: %v", err)
	}
	got := shortCodeRows(t, region)
	want := []string{"E0002", "E0009", "E0010"}
	if !slices.Equal(got, want) {
		t.Errorf("rows ordered %v, want %v", got, want)
	}
}

// TestRenderShortCodeRegion_IsAFunctionOfTheDeclarationsAlone proves the region
// does not depend on the order the declarations were walked in. A generated page
// whose bytes move with map iteration order drifts against its own drift gate on
// alternate runs, which reads as a stale page rather than as the non-determinism
// it is.
func TestRenderShortCodeRegion_IsAFunctionOfTheDeclarationsAlone(t *testing.T) {
	t.Parallel()
	ids, err := scan("testdata/module", "example.test/fixture")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	first, err := renderShortCodeRegion(ids, nil)
	if err != nil {
		t.Fatalf("renderShortCodeRegion: %v", err)
	}
	shuffled := slices.Clone(ids)
	slices.Reverse(shuffled)
	second, err := renderShortCodeRegion(shuffled, nil)
	if err != nil {
		t.Fatalf("renderShortCodeRegion (reversed input): %v", err)
	}
	if first != second {
		t.Errorf("region depends on input order:\n--- walked ---\n%s\n--- reversed ---\n%s", first, second)
	}
}

// TestRenderShortCodeRegion_RefusesADuplicateShortCode covers the one way this
// table can mislead rather than merely be incomplete. Registration already
// refuses a duplicate at package initialisation, so reaching the renderer with
// one means the extraction went wrong — and publishing two conditions under one
// key is worse than refusing, because the reader has no way to tell which row is
// theirs.
func TestRenderShortCodeRegion_RefusesADuplicateShortCode(t *testing.T) {
	t.Parallel()
	_, err := renderShortCodeRegion([]Identity{
		{Pkg: "pkg/a", Name: "ErrFirst", Code: "a/first", ShortCode: "E0001", Message: "first", Importable: true},
		{Pkg: "pkg/b", Name: "ErrSecond", Code: "b/second", ShortCode: "E0001", Message: "second", Importable: true},
	}, nil)
	if err == nil {
		t.Fatal("renderShortCodeRegion accepted two conditions under one short code")
	}
	for _, want := range []string{"pkg/a.ErrFirst", "pkg/b.ErrSecond", "E0001"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %s: %v", want, err)
		}
	}
}

// TestRenderShortCodeRegion_RefusesADeclaredStringCarryingATableSeparator is the
// by-package region's refusal applied to this table's cells. Both regions route
// the check through one function, so this holds the third cell that only this
// table renders.
func TestRenderShortCodeRegion_RefusesADeclaredStringCarryingATableSeparator(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		what string
		id   Identity
	}{
		{what: "message", id: Identity{Pkg: "pkg/thing", Name: "ErrPiped", Code: "thing/piped", ShortCode: "E0001", Message: "a | b"}},
		{what: "code", id: Identity{Pkg: "pkg/thing", Name: "ErrPiped", Code: "thing/a | b", ShortCode: "E0001", Message: "a message"}},
		{what: "short code", id: Identity{Pkg: "pkg/thing", Name: "ErrPiped", Code: "thing/piped", ShortCode: "E|001", Message: "a message"}},
	} {
		_, err := renderShortCodeRegion([]Identity{tc.id}, nil)
		if err == nil {
			t.Fatalf("renderShortCodeRegion accepted a %s carrying a table separator", tc.what)
		}
		if !strings.Contains(err.Error(), "pkg/thing.ErrPiped") {
			t.Errorf("%s: error does not name the offending handle: %v", tc.what, err)
		}
		if !strings.Contains(err.Error(), tc.what) {
			t.Errorf("%s: error does not name which declared string carried it: %v", tc.what, err)
		}
	}
}

// TestRenderShortCodeRegion_LiveModuleCoversEveryDeclaredCondition is the
// acceptance-grade check: the fixture proves the rule, and this proves it holds
// for what actually ships. Every declared condition has exactly one row and no
// re-export adds one, asserted as set identity rather than as a count — a count
// written here would be a second copy of a figure the declarations already carry,
// and it would need editing every time a condition is added.
// TestRenderShortCodeRegion_LiveModuleCoversEveryPublishedCondition holds the
// region to the corpus the generator actually hands it.
//
// 🔴 It runs [admit] rather than rendering the scan's whole output, and that is
// load-bearing rather than incidental. The admission rule lives in the generator,
// so a test rendering the unfiltered set exercises a path the published pages do
// not take: it would keep passing while the real page carried a different corpus,
// which is a test asserting a contract that has been superseded — green, and
// certifying nothing.
//
// The property asserted is that the region carries a row for every condition the
// reference publishes, and for nothing else. A condition the rule excludes must
// appear nowhere on the page: absence is the whole mechanism, and a row for an
// excluded condition would promise a check the build cannot perform.
func TestRenderShortCodeRegion_LiveModuleCoversEveryPublishedCondition(t *testing.T) {
	t.Parallel()
	modulePath, err := modulePathOf("../../..")
	if err != nil {
		t.Fatalf("module path: %v", err)
	}
	ids, err := scan("../../..", modulePath)
	if err != nil {
		t.Fatalf("scan module: %v", err)
	}
	published, excluded := admit(ids)
	region, err := renderShortCodeRegion(published, nil)
	if err != nil {
		t.Fatalf("renderShortCodeRegion: %v", err)
	}

	declared := map[string]string{}
	reExports := 0
	for _, id := range published {
		if id.AliasOf != "" {
			reExports++
			continue
		}
		declared[id.ShortCode] = id.Handle()
	}
	// Calibration: both walks below read as "nothing is wrong" over an empty set,
	// so the inputs are proven non-empty before either verdict is believed.
	if len(declared) == 0 {
		t.Fatal("the module declared no condition; the scan is broken, not the rule")
	}
	if reExports == 0 {
		t.Fatal("the module declares no re-export, so the exclusion below asserts nothing")
	}

	rows := shortCodeRows(t, region)
	if len(rows) == 0 {
		t.Fatal("the rendered region carries no rows; the comparison below would certify nothing")
	}
	onPage := map[string]bool{}
	for _, c := range rows {
		if onPage[c] {
			t.Errorf("short code %s heads more than one row", c)
		}
		onPage[c] = true
	}
	for code, handle := range declared {
		if !onPage[code] {
			t.Errorf("%s declares %s and the region carries no row for it", handle, code)
		}
	}
	for code := range onPage {
		if _, ok := declared[code]; !ok {
			t.Errorf("the region carries a row for %s, which no condition declares", code)
		}
	}
	// A re-export reports its origin's short code, so admitting one would show up
	// here as more rows than published conditions even with every code accounted
	// for above.
	if len(rows) != len(declared) {
		t.Errorf("region carries %d rows for %d published conditions", len(rows), len(declared))
	}

	// The exclusions are the other half of the property, and they are asserted
	// against the rendered page rather than against the partition that produced
	// it: a partition that agrees with itself proves nothing about what was
	// published. The set is legitimately empty once every declared condition is
	// raised or noted, so emptiness is not failed — what is failed is a code the
	// rule excluded turning up in the table anyway.
	for _, id := range excluded {
		if id.AliasOf != "" {
			continue
		}
		if onPage[id.ShortCode] {
			t.Errorf("%s is raised by nothing and names no work that will, yet the region carries a "+
				"row for %s; the page promises a check the build cannot perform",
				id.Handle(), id.ShortCode)
		}
	}

	// Published and excluded together are the whole scanned set. A condition
	// falling out of both would be off the page AND out of the report that names
	// what is off the page, which is the invisibility this rule exists to end.
	if len(published)+len(excluded) != len(ids) {
		t.Errorf("the admission partition covers %d of %d scanned identities, so some condition is "+
			"neither published nor reported", len(published)+len(excluded), len(ids))
	}

	// The wider corpus is a live property, not only a constructed one: the module
	// really does declare conditions the by-package region cannot carry, and the
	// page's prose tells a reader this table covers them. Asserted as a non-empty
	// difference rather than against a number — a figure written here would be a
	// second copy of what the declarations already determine, and it would need
	// editing whenever a condition moves between packages.
	// Rendered from the published set, like the region above, because both pages
	// take the same corpus. Rendering this one from the whole scan would compare
	// two different corpora and report every excluded condition as "published
	// nowhere", which is true and is not what this assertion is about.
	byPkg, err := renderRegion(published)
	if err != nil {
		t.Fatalf("renderRegion: %v", err)
	}
	onlyHere := 0
	for _, id := range published {
		if id.AliasOf != "" || strings.Contains(byPkg, codeSpan(id.Code)) {
			continue
		}
		onlyHere++
		if !onPage[id.ShortCode] {
			t.Errorf("%s appears in neither region, so it is published nowhere", id.Handle())
		}
	}
	if onlyHere == 0 {
		t.Fatal("every declared condition already has a row in the by-package region, so this table's " +
			"wider corpus is untested here and the page's claim about it rests on nothing")
	}
}

// haltRows returns one record per row of the halting-conditions region, each the
// row's three cells with their code fences removed, and refuses a row whose shape
// or emptiness would make a later comparison certify nothing.
func haltRows(t *testing.T, region string) [][3]string {
	t.Helper()
	var out [][3]string
	for _, line := range strings.Split(region, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), " | ")
		if len(cells) != 3 {
			t.Fatalf("row does not carry three cells: %q", line)
		}
		var row [3]string
		for i, c := range cells {
			row[i] = strings.TrimSpace(strings.Trim(strings.TrimSpace(c), "`"))
			if row[i] == "" {
				t.Errorf("row cell %d is empty: %q", i, line)
			}
		}
		out = append(out, row)
	}
	return out
}

func haltRegionIn(t *testing.T, ids []Identity, docsRoot string) (string, error) {
	t.Helper()
	region, _, err := renderHaltRegion(ids, entriesIn(t, docsRoot))
	return region, err
}

// TestRenderHaltRegion_CarriesThePagesCorpusAndNotTheDeclaredSet is the property
// that separates this region from the whole-set table on the sentinel reference.
// This page teaches an authoring fix per entry, so its region resolves a code to
// an entry — and a row for a condition no entry documents would resolve to
// nothing while reading as a resolved correspondence.
func TestRenderHaltRegion_CarriesThePagesCorpusAndNotTheDeclaredSet(t *testing.T) {
	t.Parallel()
	ids := fixtureIdentities(t)
	// Calibration: the fixture has to declare more conditions than the page
	// covers, or the assertion below passes on a page that happens to cover
	// everything and asserts nothing about scoping.
	declared := 0
	for _, id := range ids {
		if id.AliasOf == "" {
			declared++
		}
	}
	if declared < 2 {
		t.Fatalf("the fixture declares %d conditions; the scoping below would assert nothing", declared)
	}

	root := haltsPageIn(t, `# Halts

### A plain thing went wrong

<!-- covers: pkg/thing.ErrPlain -->

Prose.
`)
	region, err := haltRegionIn(t, ids, root)
	if err != nil {
		t.Fatalf("renderHaltRegion: %v", err)
	}
	rows := haltRows(t, region)
	if len(rows) != 1 {
		t.Fatalf("region carries %d rows for a page covering one condition: %q", len(rows), region)
	}
	want := [3]string{"E9001", "a plain condition", "A plain thing went wrong"}
	if rows[0] != want {
		t.Errorf("row = %q, want %q", rows[0], want)
	}
}

// TestRenderHaltRegion_CreditsAReExportToTheConditionItForwardsTo mirrors the
// coverage resolution: a declaration may name either name, and both name one
// condition, so either spelling places one row carrying the forwarded code and
// the forwarded sentence.
func TestRenderHaltRegion_CreditsAReExportToTheConditionItForwardsTo(t *testing.T) {
	t.Parallel()
	root := haltsPageIn(t, `# Halts

### Something went wrong

<!-- covers: pkg/other.ErrPlain -->

Prose.
`)
	region, err := haltRegionIn(t, fixtureIdentities(t), root)
	if err != nil {
		t.Fatalf("renderHaltRegion: %v", err)
	}
	rows := haltRows(t, region)
	if len(rows) != 1 {
		t.Fatalf("region carries %d rows for one re-exported condition: %q", len(rows), region)
	}
	if rows[0][0] != "E9001" || rows[0][1] != "a plain condition" {
		t.Errorf("row = %q, want the forwarded code and sentence", rows[0])
	}
}

// TestRenderHaltRegion_NamesEveryEntryThatDocumentsOneCondition covers the
// several-entries-one-condition shape the live page has: one condition met in
// more than one way, each way with its own entry. One row names both, in the
// order the page introduces them, because a reader holding the code needs every
// entry that could explain it rather than whichever one the page happened to
// declare first.
func TestRenderHaltRegion_NamesEveryEntryThatDocumentsOneCondition(t *testing.T) {
	t.Parallel()
	root := haltsPageIn(t, `# Halts

### Met while loading

<!-- covers: pkg/thing.ErrPlain -->

Prose.

### Met while rendering

<!-- covers: pkg/thing.ErrPlain -->

Prose.
`)
	region, err := haltRegionIn(t, fixtureIdentities(t), root)
	if err != nil {
		t.Fatalf("renderHaltRegion: %v", err)
	}
	rows := haltRows(t, region)
	if len(rows) != 1 {
		t.Fatalf("one condition documented twice produced %d rows: %q", len(rows), region)
	}
	if rows[0][2] != "Met while loading; Met while rendering" {
		t.Errorf("entry cell = %q, want both entries in page order", rows[0][2])
	}
}

// TestRenderHaltRegion_KeepsTwoConditionsUnderOneEntryAsTwoRows is the shape
// that makes a real disagreement visible on the page rather than averaged away.
// Two conditions may register different wording and be documented by a single
// entry; no one sentence is true of both, so the region carries a row each and a
// reader tells them apart by the sentence rather than by the shared heading.
func TestRenderHaltRegion_KeepsTwoConditionsUnderOneEntryAsTwoRows(t *testing.T) {
	t.Parallel()
	root := haltsPageIn(t, `# Halts

### One entry, two conditions

<!-- covers: pkg/thing.ErrPlain, pkg/other.ErrOwn -->

Prose.
`)
	region, err := haltRegionIn(t, fixtureIdentities(t), root)
	if err != nil {
		t.Fatalf("renderHaltRegion: %v", err)
	}
	rows := haltRows(t, region)
	if len(rows) != 2 {
		t.Fatalf("two conditions under one entry produced %d rows: %q", len(rows), region)
	}
	for _, r := range rows {
		if r[2] != "One entry, two conditions" {
			t.Errorf("row %q does not name the entry that documents it", r)
		}
	}
	if rows[0][1] == rows[1][1] {
		t.Errorf("both rows carry the same sentence, so the disagreement is invisible: %q", region)
	}
}

// TestRenderHaltRegion_OrdersByShortCode fixes the order a reader arrives
// expecting: they hold the code the build printed, and a short code is padded to
// a fixed width so a text order is the numeric one.
func TestRenderHaltRegion_OrdersByShortCode(t *testing.T) {
	t.Parallel()
	root := haltsPageIn(t, `# Halts

### Declared later, lower code

<!-- covers: pkg/other.ErrOwn -->

Prose.

### Declared earlier, higher code

<!-- covers: pkg/thing.ErrQuoted -->

Prose.
`)
	region, err := haltRegionIn(t, fixtureIdentities(t), root)
	if err != nil {
		t.Fatalf("renderHaltRegion: %v", err)
	}
	rows := haltRows(t, region)
	if len(rows) != 2 {
		t.Fatalf("region carries %d rows: %q", len(rows), region)
	}
	if rows[0][0] != "E9002" || rows[1][0] != "E9004" {
		t.Errorf("rows are ordered %s then %s, want E9002 then E9004", rows[0][0], rows[1][0])
	}
}

// TestRenderHaltRegion_RefusesADeclarationUnderNoHeading refuses rather than
// dropping the row. A row whose entry cell is empty points a reader at nothing
// while reading exactly like a resolved correspondence, and a silently short
// region reads exactly like a complete one.
func TestRenderHaltRegion_RefusesADeclarationUnderNoHeading(t *testing.T) {
	t.Parallel()
	root := haltsPageIn(t, `<!-- covers: pkg/thing.ErrPlain -->

Prose with no heading above the declaration.
`)
	_, err := haltRegionIn(t, fixtureIdentities(t), root)
	if err == nil {
		t.Fatal("a declaration under no heading was rendered rather than refused")
	}
	if !strings.Contains(err.Error(), "under no heading") {
		t.Errorf("refusal does not say what it refused: %v", err)
	}
}

// TestRenderHaltRegion_RefusesAPageDocumentingNothing refuses an empty region for
// the reason the generator refuses an empty identity set: an empty table reads as
// "nothing is documented" whether that is true or the declaration form moved.
func TestRenderHaltRegion_RefusesAPageDocumentingNothing(t *testing.T) {
	t.Parallel()
	_, _, err := renderHaltRegion(fixtureIdentities(t), nil)
	if err == nil {
		t.Fatal("a page documenting no condition was rendered rather than refused")
	}
	if !strings.Contains(err.Error(), "no condition") {
		t.Errorf("refusal does not say what it refused: %v", err)
	}
}

// TestRenderHaltRegion_RefusesAHeadingCarryingATableSeparator covers the one
// cell whose text comes from the page rather than from a declaration. The
// declared strings are refused by the shared separator check; a heading is not
// declared anywhere, so it needs its own.
func TestRenderHaltRegion_RefusesAHeadingCarryingATableSeparator(t *testing.T) {
	t.Parallel()
	root := haltsPageIn(t, `# Halts

### A heading with a | separator in it

<!-- covers: pkg/thing.ErrPlain -->

Prose.
`)
	_, err := haltRegionIn(t, fixtureIdentities(t), root)
	if err == nil {
		t.Fatal("a heading carrying a table separator was rendered rather than refused")
	}
	if !strings.Contains(err.Error(), "table separator") {
		t.Errorf("refusal does not say what it refused: %v", err)
	}
}

// documentedSet builds the second argument rejectCoverageDisagreement takes from
// the handles alone. Only the handle participates in the comparison, so a test
// naming the other fields would be describing something the check never reads.
func documentedSet(handles ...string) []Identity {
	out := make([]Identity, 0, len(handles))
	for _, h := range handles {
		i := strings.LastIndex(h, ".")
		out = append(out, Identity{Pkg: h[:i], Name: h[i+1:]})
	}
	return out
}

// coveredSet builds the first argument, which is the shape resolveCoverage
// returns.
func coveredSet(handles ...string) map[string]bool {
	out := make(map[string]bool, len(handles))
	for _, h := range handles {
		out[h] = true
	}
	return out
}

// TestRejectCoverageDisagreement_AcceptsTwoDerivationsNamingTheSameConditions is
// the calibration every refusal below rests on. A check that refused everything
// would pass each of those cases while breaking the generator outright, so the
// accepting case is asserted first and separately.
func TestRejectCoverageDisagreement_AcceptsTwoDerivationsNamingTheSameConditions(t *testing.T) {
	t.Parallel()
	covered := coveredSet("pkg/thing.ErrPlain", "pkg/other.ErrOwn")
	documented := documentedSet("pkg/other.ErrOwn", "pkg/thing.ErrPlain")
	// Deliberately in a different order on the two sides: the sets are what
	// agree, and a check sensitive to order would refuse a page whose entries
	// were merely rearranged.
	if err := rejectCoverageDisagreement(covered, documented); err != nil {
		t.Errorf("two derivations naming the same conditions were refused: %v", err)
	}
}

// TestRejectCoverageDisagreement_RefusesEitherDirection covers both, because they
// are different defects with different repairs: a condition an entry documents
// that the region omits leaves a reader who searches the page unable to resolve
// the code they arrived holding, and a row for a condition no entry documents
// resolves to an entry that is not there.
func TestRejectCoverageDisagreement_RefusesEitherDirection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		covered    map[string]bool
		documented []Identity
		want       string
	}{
		{
			name:       "documented by an entry and carried by no row",
			covered:    coveredSet("pkg/thing.ErrPlain", "pkg/other.ErrOwn"),
			documented: documentedSet("pkg/thing.ErrPlain"),
			want:       "carried by no row: pkg/other.ErrOwn",
		},
		{
			name:       "carried by a row and documented by no entry",
			covered:    coveredSet("pkg/thing.ErrPlain"),
			documented: documentedSet("pkg/thing.ErrPlain", "pkg/other.ErrOwn"),
			want:       "documented by no entry: pkg/other.ErrOwn",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := rejectCoverageDisagreement(tc.covered, tc.documented)
			if err == nil {
				t.Fatal("a disagreement between the two derivations was accepted rather than refused")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal does not name what disagreed: %v", err)
			}
		})
	}
}

// TestRejectCoverageDisagreement_RefusesASwapOfEqualSize is the case a size
// comparison passes. Two sets of the same size can name different conditions, so
// comparing counts would report a page whose region carries a row for the wrong
// condition as current — which is the correspondence that reads as verified while
// describing nothing.
func TestRejectCoverageDisagreement_RefusesASwapOfEqualSize(t *testing.T) {
	t.Parallel()
	covered := coveredSet("pkg/thing.ErrPlain", "pkg/other.ErrOwn")
	documented := documentedSet("pkg/thing.ErrPlain", "pkg/thing.ErrQuoted")
	// Calibration: the assertion below asserts nothing unless the two sides are
	// genuinely the same size, which is the whole point of this case.
	if len(covered) != len(documented) {
		t.Fatalf("the two sides are %d and %d; this case asserts nothing unless they are equal",
			len(covered), len(documented))
	}
	err := rejectCoverageDisagreement(covered, documented)
	if err == nil {
		t.Fatal("a swap of equal size was accepted, so the check is comparing sizes rather than conditions")
	}
	if !strings.Contains(err.Error(), "pkg/other.ErrOwn") || !strings.Contains(err.Error(), "pkg/thing.ErrQuoted") {
		t.Errorf("refusal names only one side of the swap: %v", err)
	}
}

// TestRejectCoverageDisagreement_NamesEveryDisagreementAtOnce keeps a page with
// several defects repairable in one pass. Reporting the first would need as many
// runs to fix as the page has faults, which is the shape resolveCoverage already
// avoids for the same reason.
func TestRejectCoverageDisagreement_NamesEveryDisagreementAtOnce(t *testing.T) {
	t.Parallel()
	covered := coveredSet("pkg/thing.ErrPlain", "pkg/other.ErrOwn")
	documented := documentedSet("pkg/thing.ErrQuoted", "internal/hidden.ErrHidden")
	err := rejectCoverageDisagreement(covered, documented)
	if err == nil {
		t.Fatal("four disagreements were accepted rather than refused")
	}
	for _, want := range []string{
		"pkg/other.ErrOwn", "pkg/thing.ErrPlain", "internal/hidden.ErrHidden", "pkg/thing.ErrQuoted",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal omits %s: %v", want, err)
		}
	}
}

// TestRejectCoverageDisagreement_AgreesOverThePagesRealDerivations runs the two
// derivations the generator actually uses over one page and hands their results
// to the check, which is the reading the generator performs on every refresh.
//
// The page names one condition by the accessor that declares it and one by a
// re-export, because that is where the two derivations could most plausibly part
// company: each credits a re-export to the identity it forwards to, and they do
// it in separate code. A page covering only origins would exercise neither.
func TestRejectCoverageDisagreement_AgreesOverThePagesRealDerivations(t *testing.T) {
	t.Parallel()
	ids := fixtureIdentities(t)
	root := haltsPageIn(t, `# Halts

### Something quoted went wrong

<!-- covers: pkg/thing.ErrQuoted -->

Prose.

### A plain thing went wrong

<!-- covers: pkg/other.ErrPlain -->

Prose.
`)
	entries := entriesIn(t, root)
	covered, err := resolveCoverage(ids, entries, root)
	if err != nil {
		t.Fatalf("resolveCoverage: %v", err)
	}
	_, documented, err := renderHaltRegion(ids, entries)
	if err != nil {
		t.Fatalf("renderHaltRegion: %v", err)
	}
	// Calibration: both sides are proven non-empty and proven to carry the
	// re-export's origin rather than the re-export, or the agreement below would
	// hold over two empty sets and certify nothing.
	if len(covered) != 2 || len(documented) != 2 {
		t.Fatalf("the two derivations read %d and %d conditions from a page covering two",
			len(covered), len(documented))
	}
	if !covered["pkg/thing.ErrPlain"] {
		t.Errorf("the coverage derivation did not credit the re-export to its origin: %v", covered)
	}
	if err := rejectCoverageDisagreement(covered, documented); err != nil {
		t.Errorf("the generator's own two derivations were reported as disagreeing: %v", err)
	}
}

// TestScanRetirements_ReadsTheRecordAndBothSpellingsOfAnEntry is the extraction
// half of the retirement lookup: the record the registry consults to refuse a
// reissued number is the same declaration the reference page is composed from, so
// this proves the reading rather than the publishing.
//
// The fixture declares one entry positionally and one by field name because the
// scan reads either, and a fixture carrying only one spelling would leave the
// other proven by nothing — a reader switching the declaration to field names
// would silently empty the published list.
func TestScanRetirements_ReadsTheRecordAndBothSpellingsOfAnEntry(t *testing.T) {
	t.Parallel()
	got, err := scanRetirements("testdata/module")
	if err != nil {
		t.Fatalf("scanRetirements: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("the fixture declares a retirement record and the scan read nothing from it")
	}
	want := map[string]string{
		"E9901": "It named a fixture condition that no longer exists.",
		"E9902": "It named a second fixture condition, recorded by field name.",
	}
	for _, r := range got {
		note, expected := want[r.ShortCode]
		if !expected {
			t.Errorf("the scan read %s, which the fixture does not record", r.ShortCode)
			continue
		}
		if r.Note != note {
			t.Errorf("%s: the scan read the note %q and the fixture records %q", r.ShortCode, r.Note, note)
		}
		delete(want, r.ShortCode)
	}
	// Named rather than counted: two sets of equal size can still name different
	// entries, so a size comparison would pass a swap.
	for code := range want {
		t.Errorf("the fixture records %s and the scan read no entry for it", code)
	}
}

// TestScanRetirements_ReportsNoRecordAsEmptyRatherThanAsAFailure holds the state
// of a tree that has retired nothing. A module early in its life declares no
// record, and refusing that would make the generator unusable on it.
//
// The other fixture modules are the corpus for it, so this also states plainly
// that a fixture without a record is a legitimate input rather than an oversight.
func TestScanRetirements_ReportsNoRecordAsEmptyRatherThanAsAFailure(t *testing.T) {
	t.Parallel()
	for _, root := range []string{"testdata/raised", "testdata/unregistered", "testdata/noshortcode"} {
		got, err := scanRetirements(root)
		if err != nil {
			t.Errorf("%s: scanRetirements: %v", root, err)
			continue
		}
		if len(got) != 0 {
			t.Errorf("%s: the scan read %d entries from a module declaring no record", root, len(got))
		}
	}
}

// TestScanRetirements_RefusesAnEntryItCannotRead is the calibration the empty
// case above needs. A record whose entries the scan cannot read would otherwise
// produce exactly the empty result a module with no record produces — and an
// unread entry is a number that stops being reported as spent while still being
// spent.
//
// The records are written into a temporary tree rather than committed as
// fixtures, because each is deliberately malformed and a malformed fixture in the
// tree is one a later reader tidies.
func TestScanRetirements_RefusesAnEntryItCannotRead(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		record string
		names  string
	}{
		{
			name:   "an element that is not a literal",
			record: "var retiredShortCodes = []retirement{someHelper()}",
			names:  "entry 0",
		},
		{
			name:   "a value built rather than declared",
			record: `var retiredShortCodes = []retirement{{"E9901", noteFor("E9901")}}`,
			names:  "not a string literal",
		},
		{
			name:   "an entry naming an unknown field",
			record: `var retiredShortCodes = []retirement{{shortCode: "E9901", reason: "It named something."}}`,
			names:  "reason",
		},
		{
			name:   "an entry mixing positional and named values",
			record: `var retiredShortCodes = []retirement{{"E9901", note: "It named something."}}`,
			names:  "ambiguous",
		},
		{
			name:   "an entry carrying only a short code",
			record: `var retiredShortCodes = []retirement{{"E9901"}}`,
			names:  "1 values",
		},
		{
			name:   "an entry recording an empty note",
			record: `var retiredShortCodes = []retirement{{"E9901", ""}}`,
			names:  "E9901",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "pkg", "errors")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			body := "package errors\n\ntype retirement struct {\n\tshortCode string\n\tnote string\n}\n\n" + tc.record + "\n"
			if err := os.WriteFile(filepath.Join(dir, "code.go"), []byte(body), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			got, err := scanRetirements(root)
			if err == nil {
				t.Fatalf("the scan accepted %s and read %d entries", tc.name, len(got))
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("the refusal does not name %q, so a reader cannot find the entry to fix: %v",
					tc.names, err)
			}
		})
	}
}

// TestRenderShortCodeRegion_NamesEveryRetiredCodeBeneathTheTable is what makes
// the page's promise true for a number that no longer names a condition: a reader
// quoting one off an old report finds it here and is told what it named, rather
// than finding the table silent and being unable to tell a spent number from a
// mistyped one.
//
// It runs over the LIVE module and the live record, so it asserts the published
// surface rather than a constructed one, and it fails on an empty record instead
// of passing over nothing.
func TestRenderShortCodeRegion_NamesEveryRetiredCodeBeneathTheTable(t *testing.T) {
	t.Parallel()
	modulePath, err := modulePathOf("../../..")
	if err != nil {
		t.Fatalf("module path: %v", err)
	}
	ids, err := scan("../../..", modulePath)
	if err != nil {
		t.Fatalf("scan module: %v", err)
	}
	retired, err := scanRetirements("../../..")
	if err != nil {
		t.Fatalf("scanRetirements: %v", err)
	}
	if len(retired) == 0 {
		t.Fatal("the module records no retired code, so every assertion below would pass over nothing")
	}
	published, _ := admit(ids)
	region, err := renderShortCodeRegion(published, retired)
	if err != nil {
		t.Fatalf("renderShortCodeRegion: %v", err)
	}

	rows := map[string]bool{}
	for _, c := range shortCodeRows(t, region) {
		rows[c] = true
	}
	if len(rows) == 0 {
		t.Fatal("the region carries no rows, so the comparison below would certify nothing")
	}
	for _, r := range retired {
		// The note, not merely the code: a code appearing anywhere in the region
		// would also be satisfied by a row, which is the one thing a retired code
		// must not have.
		if !strings.Contains(region, codeSpan(r.ShortCode)+" is retired") {
			t.Errorf("the region names no retired note for %s, so a reader quoting it resolves nothing",
				r.ShortCode)
		}
		if !strings.Contains(region, r.Note) {
			t.Errorf("the region carries no note text for %s, so it says the number is spent without "+
				"saying what spent it", r.ShortCode)
		}
		if rows[r.ShortCode] {
			t.Errorf("the region carries a ROW for the retired code %s, which resolves it to a live "+
				"condition", r.ShortCode)
		}
	}

	// Calibration in the other direction: the note block is absent when nothing
	// is retired, so the assertions above are reading a block the retired set put
	// there rather than one that is always present.
	bare, err := renderShortCodeRegion(published, nil)
	if err != nil {
		t.Fatalf("renderShortCodeRegion (no retirements): %v", err)
	}
	if strings.Contains(bare, "is retired") {
		t.Error("the region names a retired code with no retirement record, so the assertions above " +
			"are not reading the record")
	}
}

// TestRenderShortCodeRegion_RefusesACodeThatIsBothRetiredAndRegistered is the
// contradiction the registry's own guard cannot reach. Registration refuses a
// retired number at package initialisation, so the pair is unreachable in a
// running program — but this reads declarations without running them, which is
// exactly where such a pair would survive to reach the page.
func TestRenderShortCodeRegion_RefusesACodeThatIsBothRetiredAndRegistered(t *testing.T) {
	t.Parallel()
	_, err := renderShortCodeRegion(
		[]Identity{{Pkg: "pkg/a", Name: "ErrLive", Code: "a/live", ShortCode: "E0001",
			Message: "a live condition", Importable: true, Raised: true}},
		[]Retirement{{ShortCode: "E0001", Note: "It named something else."}},
	)
	if err == nil {
		t.Fatal("a code that is both registered and retired was accepted; the table would resolve it " +
			"to a condition and the note beneath would say nothing reports it")
	}
	for _, want := range []string{"E0001", "pkg/a.ErrLive"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
}

// TestRenderShortCodeRegion_RefusesAMultiLineRetirementNote is the retirement
// note's half of the refusal the declared-ahead note already carries: a note
// carrying a line break ends the list item it renders into and spills the rest
// onto the page as prose, silently, because the page still renders.
func TestRenderShortCodeRegion_RefusesAMultiLineRetirementNote(t *testing.T) {
	t.Parallel()
	_, err := renderShortCodeRegion(
		[]Identity{{Pkg: "pkg/a", Name: "ErrLive", Code: "a/live", ShortCode: "E0001",
			Message: "a live condition", Importable: true, Raised: true}},
		[]Retirement{{ShortCode: "E0002", Note: "It named something.\nAnd then some more."}},
	)
	if err == nil {
		t.Fatal("a multi-line retirement note was accepted; it would spill onto the page as prose")
	}
	if !strings.Contains(err.Error(), "E0002") {
		t.Errorf("the refusal does not name the entry to repair: %v", err)
	}
}

// TestRenderShortCodeRegion_OrdersRetiredCodesIndependentlyOfTheRecord holds the
// determinism the page needs. The record is authored in whatever order conditions
// were withdrawn; the region is a function of WHICH numbers are spent, so the
// same set declared in either order renders the same bytes.
func TestRenderShortCodeRegion_OrdersRetiredCodesIndependentlyOfTheRecord(t *testing.T) {
	t.Parallel()
	live := []Identity{{Pkg: "pkg/a", Name: "ErrLive", Code: "a/live", ShortCode: "E0009",
		Message: "a live condition", Importable: true, Raised: true}}
	ascending := []Retirement{
		{ShortCode: "E0001", Note: "It named the first."},
		{ShortCode: "E0002", Note: "It named the second."},
	}
	descending := []Retirement{ascending[1], ascending[0]}

	first, err := renderShortCodeRegion(live, ascending)
	if err != nil {
		t.Fatalf("renderShortCodeRegion: %v", err)
	}
	second, err := renderShortCodeRegion(live, descending)
	if err != nil {
		t.Fatalf("renderShortCodeRegion (reversed record): %v", err)
	}
	if first != second {
		t.Errorf("the region depends on the order the record declares its entries:\n%s\n---\n%s", first, second)
	}
	if strings.Index(first, "`E0001` is retired") > strings.Index(first, "`E0002` is retired") {
		t.Errorf("retired codes are not ordered by short code:\n%s", first)
	}
	// The caller's slice is not reordered under it: the generator hands the same
	// record to nothing else today, and a renderer that sorted its input in place
	// would make that a hazard the day it does.
	if descending[0].ShortCode != "E0002" {
		t.Error("the renderer reordered the caller's record in place")
	}
}

// TestClaimsIn_DoesNotLetAnEntryHeadingAnswerForItsBlock pins the defect the
// comparison was tightened to remove.
//
// The heading here carries the registered sentence word for word and the
// emitted-message block carries something the module never registers. Compared
// against the entry's prose, that page passes — which is what it used to do, and
// which is why the tier reported whether an entry mentioned its condition rather
// than whether the output it quotes is what a build prints.
// Both rows carry a block the module never registers, so a passing entry could
// only have passed on its heading. The rows differ in HOW the heading carries the
// sentence, and the difference matters: the first is excluded because ordinary
// prose is in neither compared text, and the second only because the heading
// itself is dropped. A heading written entirely as a code span is a legal thing to
// write, so without the drop it would answer for its entry through the very
// mechanism that admits a code span in the body.
func TestClaimsIn_DoesNotLetAnEntryHeadingAnswerForItsBlock(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		heading string
	}{
		{name: "a heading in ordinary words", heading: "### A plain condition"},
		{name: "a heading written as a code span", heading: "### `a plain condition`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := haltsPageIn(t, "# Halts\n\n"+
				tc.heading+"\n\n"+
				"<!-- covers: pkg/thing.ErrPlain -->\n\n"+
				"Emitted message:\n> something the module never registers\n")

			unquoted, compared, _, unreadable := claimsIn(fixtureIdentities(t), entriesIn(t, root))
			if len(unreadable) != 0 {
				t.Fatalf("comparisons that did not happen: %v", unreadable)
			}
			if compared != 1 {
				t.Errorf("compared %d claims, want 1", compared)
			}
			if len(unquoted) != 1 {
				t.Fatalf("the heading answered for the block: counted %d, want 1: %+v", len(unquoted), unquoted)
			}
		})
	}
}

// TestClaimsIn_DoesNotReadOrdinaryProseAsAQuotation holds the other side of the
// same line. Explanatory prose describing the condition in the author's words is
// not the entry quoting it, so it satisfies nothing — otherwise the tightening
// would only have moved which prose the heading's job passes to.
func TestClaimsIn_DoesNotReadOrdinaryProseAsAQuotation(t *testing.T) {
	t.Parallel()
	root := haltsPageIn(t, "# Halts\n\n"+
		"### Something went wrong\n\n"+
		"<!-- covers: pkg/thing.ErrPlain -->\n\n"+
		"The build reports a plain condition and stops.\n")

	unquoted, _, _, _ := claimsIn(fixtureIdentities(t), entriesIn(t, root))
	if len(unquoted) != 1 {
		t.Fatalf("body prose was read as a quotation: %+v", unquoted)
	}
}

// TestClaimsIn_ReadsACodeSpanNamingTheConditionAndCountsItApart covers the entries
// no transcript can serve: a condition whose emitted line interpolates a position
// into the middle of its registered sentence, so the fixed words appear
// contiguously in no output at all and the entry names the condition it is
// recorded under instead.
//
// It passes, and it is counted apart, because the two are different evidence: one
// says a build prints this, the other says the module records it under this.
func TestClaimsIn_ReadsACodeSpanNamingTheConditionAndCountsItApart(t *testing.T) {
	t.Parallel()
	root := haltsPageIn(t, "# Halts\n\n"+
		"### Something went wrong\n\n"+
		"<!-- covers: pkg/thing.ErrPlain -->\n\n"+
		"Emitted message:\n> thing at `<path>`:`<line>`: a plain, interpolated condition\n\n"+
		"The condition it is recorded under is `a plain condition`.\n")

	unquoted, compared, offBlock, _ := claimsIn(fixtureIdentities(t), entriesIn(t, root))
	if len(unquoted) != 0 {
		t.Fatalf("a code span naming the condition did not satisfy the claim: %+v", unquoted)
	}
	if compared != 1 || offBlock != 1 {
		t.Errorf("compared=%d offBlock=%d, want 1 and 1 — the residue figure has to separate the two",
			compared, offBlock)
	}
}

// TestEntryText_ReadsATranscriptFenceAndNotACodeSample separates the two kinds of
// fenced block. A text fence reproduces output; a fence carrying source in some
// other language is a sample of something the reader writes, and admitting one
// would let a snippet answer for a message.
func TestEntryText_ReadsATranscriptFenceAndNotACodeSample(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		prose   string
		inBlock bool
	}{
		{
			name:    "a transcript fence is the emitted-message block",
			prose:   "### H\n\n```text\na plain condition\n```\n",
			inBlock: true,
		},
		{
			name:    "a sample in another language is neither",
			prose:   "### H\n\n```yaml\na plain condition\n```\n",
			inBlock: false,
		},
		{
			name:    "a blockquote is the emitted-message block",
			prose:   "### H\n\n> a plain condition\n",
			inBlock: true,
		},
		{
			name:    "the heading is in neither text",
			prose:   "### a plain condition\n\nProse.\n",
			inBlock: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			block, quoted := entryText(tc.prose)
			want := proseWords("a plain condition")
			if got := quotesRun(block, want); got != tc.inBlock {
				t.Errorf("in the emitted-message block = %v, want %v", got, tc.inBlock)
			}
			// The heading case is the one that must be absent from BOTH texts. A
			// tightening that merely moved the heading out of the narrower text
			// would leave it answering through the wider one.
			if !tc.inBlock && quotesRun(quoted, want) && strings.HasPrefix(tc.prose, "### a plain") {
				t.Error("the heading reached the wider text, which is the defect this excludes")
			}
		})
	}
}

// transcriptsFor writes pages into a temporary documentation tree and returns the
// reproductions of emitted output found across them.
func transcriptsFor(t *testing.T, pages map[string]string) []transcript {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, errorsRelDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, body := range pages {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	ts, err := transcriptsIn(root)
	if err != nil {
		t.Fatalf("transcriptsIn: %v", err)
	}
	return ts
}

// TestReportTranscripts_HoldsAReproductionToWhatTheModuleRegisters walks the four
// verdicts a reproduction can draw, because the exemption is what makes the tier
// shippable and an exemption held from only one end is an exemption that spreads.
func TestReportTranscripts_HoldsAReproductionToWhatTheModuleRegisters(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		page    string
		faults  int
		wantSay string
	}{
		{
			name:    "a reproduction quoting a registered sentence is held",
			page:    "# W\n\n```text\nLINT: a plain condition here\n```\n",
			faults:  0,
			wantSay: "1 of 1 reproductions",
		},
		{
			name:    "a reproduction quoting nothing registered, and declaring nothing, is refused",
			page:    "# W\n\n```text\nLINT: something nothing registers\n```\n",
			faults:  1,
			wantSay: "quotes no sentence any condition registers",
		},
		{
			name: "a reproduction declaring the surface it reproduces is exempt",
			page: "# W\n\n<!-- unregistered-output: lint text -->\n\n" +
				"```text\nLINT: something nothing registers\n```\n",
			faults:  0,
			wantSay: "the other 1 declaring",
		},
		{
			name: "a declaration on a reproduction that does quote a registered sentence is refused",
			page: "# W\n\n<!-- unregistered-output: lint text -->\n\n" +
				"```text\nLINT: a plain condition here\n```\n",
			faults:  1,
			wantSay: "drop the declaration",
		},
		{
			name: "a declaration saying nothing is refused",
			page: "# W\n\n<!-- unregistered-output: -->\n\n" +
				"```text\nLINT: something nothing registers\n```\n",
			faults:  1,
			wantSay: "name the surface it reproduces",
		},
		{
			name: "a declaration is cleared by intervening prose rather than binding to a later block",
			page: "# W\n\n<!-- unregistered-output: lint text -->\n\nProse.\n\n" +
				"```text\nLINT: something nothing registers\n```\n",
			faults:  1,
			wantSay: "declares no reason",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ts := transcriptsFor(t, map[string]string{"warnings.md": tc.page})
			var b strings.Builder
			faults, considered := reportTranscripts(&b, fixtureIdentities(t), ts, true)
			if faults != tc.faults {
				t.Errorf("faults = %d, want %d\n%s", faults, tc.faults, b.String())
			}
			if considered != 1 {
				t.Errorf("read %d reproductions, want 1", considered)
			}
			if !strings.Contains(b.String(), tc.wantSay) {
				t.Errorf("verdict does not say %q:\n%s", tc.wantSay, b.String())
			}
		})
	}
}

// TestTranscriptsIn_WalksPastASampleWithoutLosingTheFenceState pins the state
// machine. A fence carrying source in another language is walked past, but its
// fence is still tracked — an unbalanced walk would read the prose between two
// samples as though it were inside one, and every later block on the page with it.
func TestTranscriptsIn_WalksPastASampleWithoutLosingTheFenceState(t *testing.T) {
	t.Parallel()
	ts := transcriptsFor(t, map[string]string{
		"warnings.md": "# W\n\n```go\nfunc x() {}\n```\n\nProse.\n\n" +
			"```text\nfirst transcript\n```\n\n```yaml\nk: v\n```\n\n" +
			"```text\nsecond transcript\n```\n",
	})
	if len(ts) != 2 {
		t.Fatalf("read %d reproductions, want 2: %+v", len(ts), ts)
	}
	if !strings.Contains(ts[0].Text, "first") || !strings.Contains(ts[1].Text, "second") {
		t.Errorf("the walk collected the wrong bodies: %+v", ts)
	}
}

// crossrefsFor writes a documentation tree and returns the references found in it
// that touch the published error reference.
func crossrefsFor(t *testing.T, files map[string]string) (string, []crossref) {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	refs, err := crossrefsIn(root)
	if err != nil {
		t.Fatalf("crossrefsIn: %v", err)
	}
	return root, refs
}

// TestReportCrossrefs_ResolvesATitleToAHeadingAndRefusesOneThatNamesNothing
// covers the check in both directions of travel, because the failure runs both
// ways: a title written on one of these pages goes stale when the page it names
// is edited, and a title written elsewhere goes stale when a section here is
// renamed — and the second is the one nothing else in the build could see.
func TestReportCrossrefs_ResolvesATitleToAHeadingAndRefusesOneThatNamesNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		files      map[string]string
		refs       int
		unresolved int
	}{
		{
			name: "an outbound title resolving to a heading",
			files: map[string]string{
				errorsRelDir + "/warnings.md":              "# W\n\nsee [c](../entities/component.md) \"Ad-hoc sockets\"\n",
				"language-reference/entities/component.md": "# C\n\n## Ad-hoc sockets\n",
			},
			refs: 1, unresolved: 0,
		},
		{
			name: "an outbound title naming a heading that does not exist",
			files: map[string]string{
				errorsRelDir + "/warnings.md":              "# W\n\nsee [c](../entities/component.md) \"Ad-hoc sockets\"\n",
				"language-reference/entities/component.md": "# C\n\n## Topology sockets\n",
			},
			refs: 1, unresolved: 1,
		},
		{
			name: "an inbound title naming a section of the reference",
			files: map[string]string{
				errorsRelDir + "/fatal-halts.md":           "# H\n\n### A plain condition\n",
				"language-reference/entities/component.md": "# C\n\nsee [h](../errors/fatal-halts.md) \"A plain condition\"\n",
			},
			refs: 1, unresolved: 0,
		},
		{
			name: "an inbound title left behind by a rename",
			files: map[string]string{
				errorsRelDir + "/fatal-halts.md":           "# H\n\n### A renamed condition\n",
				"language-reference/entities/component.md": "# C\n\nsee [h](../errors/fatal-halts.md) \"A plain condition\"\n",
			},
			refs: 1, unresolved: 1,
		},
		{
			name: "the reverse order, joined by a word saying the title is inside the page",
			files: map[string]string{
				errorsRelDir + "/fatal-halts.md": "# H\n\n\"Binding lint\" in [w](./warnings.md)\n",
				errorsRelDir + "/warnings.md":    "# W\n\n## Binding lint\n",
			},
			refs: 1, unresolved: 0,
		},
		{
			name: "typography differing between the title and the heading is not a difference",
			files: map[string]string{
				errorsRelDir + "/warnings.md":              "# W\n\nsee [c](../entities/component.md) \"Topology Sockets: `usesSchema`\"\n",
				"language-reference/entities/component.md": "# C\n\n## Topology Sockets: `usesSchema`\n",
			},
			refs: 1, unresolved: 0,
		},
		{
			name: "a quoted value merely standing next to a link is not a section reference",
			files: map[string]string{
				errorsRelDir + "/warnings.md":              "# W\n\nthe value \"storage_size\" [c](../entities/component.md) follows\n",
				"language-reference/entities/component.md": "# C\n\n## Topology sockets\n",
			},
			refs: 0, unresolved: 1, // no reference at all is an instrument failure, not a clean corpus
		},
		{
			name: "a reference between two pages neither of which is the error reference",
			files: map[string]string{
				errorsRelDir + "/warnings.md":              "# W\n\nsee [x](./fatal-halts.md) \"H\"\n",
				errorsRelDir + "/fatal-halts.md":           "# H\n",
				"language-reference/entities/component.md": "# C\n\nsee [r](./recipe.md) \"Nowhere at all\"\n",
				"language-reference/entities/recipe.md":    "# R\n",
			},
			// Only the errors-page reference is collected, and it resolves. The
			// component-to-recipe one names a heading that does not exist and is
			// still not reported, because a gate refusing on a page outside the
			// surface it is named over surprises whoever edits that page.
			refs: 1, unresolved: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, refs := crossrefsFor(t, tc.files)
			if len(refs) != tc.refs {
				t.Fatalf("collected %d references, want %d: %+v", len(refs), tc.refs, refs)
			}
			var b strings.Builder
			unresolved, _ := reportCrossrefs(&b, root, refs, true)
			if unresolved != tc.unresolved {
				t.Errorf("unresolved = %d, want %d\n%s", unresolved, tc.unresolved, b.String())
			}
		})
	}
}

// advisoryRows returns the short code of every row in a rendered advisory
// region, in the order the region carries them.
//
// It reads the first cell rather than matching a whole row, for the reason
// [shortCodeRows] does: a test asserting coverage must not be satisfiable by a
// row whose other cells moved. The cell count differs because the table does —
// this region carries no third column, and a helper that tolerated either width
// would stop noticing a column appearing or disappearing.
func advisoryRows(t *testing.T, region string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(region, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), " | ")
		if len(cells) != 2 {
			t.Fatalf("row does not carry two cells: %q", line)
		}
		for i, c := range cells {
			if strings.TrimSpace(strings.Trim(strings.TrimSpace(c), "`")) == "" {
				t.Errorf("row cell %d is empty: %q", i, line)
			}
		}
		out = append(out, strings.Trim(strings.TrimSpace(cells[0]), "`"))
	}
	return out
}

// TestScan_ReadsSeverityFromTheCheckAndNotFromTheRegistration is the property the
// advisory region rests on. A condition's registration declares its wording and
// its two codes and says nothing about what a violation does to a build; the
// check that raises it declares that, and names the condition in the same
// literal. So the severity reaches an identity through the check or it does not
// reach it at all.
//
// Every row states what would go wrong if the reader answered differently, since
// each is a distinct way the extraction has to be wrong rather than a variation
// on one.
func TestScan_ReadsSeverityFromTheCheckAndNotFromTheRegistration(t *testing.T) {
	t.Parallel()
	got := scanFixture(t, "testdata/advisory", "example.test/advisory")

	for _, tc := range []struct {
		handle string
		want   bool
		why    string
	}{
		{
			handle: "pkg/thing.ErrAdvised",
			want:   true,
			why:    "a check names it and declares warning severity, which is the whole declaration",
		},
		{
			handle: "pkg/thing.ErrHalting",
			want:   false,
			why:    "a check names it and declares fatal severity, so a reader marking every check would be wrong here",
		},
		{
			handle: "pkg/thing.ErrNestedAdvised",
			want:   true,
			why:    "the check is an element of a larger literal, which a reader of package-level declarations alone would miss",
		},
		{
			handle: "pkg/thing.ErrOriginOnly",
			want:   true,
			why:    "the check names the re-export, and a re-export and its origin are one value, so the credit propagates to the origin",
		},
		{
			handle: "pkg/other.ErrReexported",
			want:   true,
			why:    "the re-export reports the severity of the value it forwards to, as it reports the forwarded message",
		},
		{
			handle: "pkg/thing.ErrUntouched",
			want:   false,
			why:    "no check names it, so it carries no severity declaration at all",
		},
		{
			handle: "internal/compiler/constraint.ErrInPackage",
			want:   true,
			why:    "the check is written inside the constraint package, where the type and the severity are spelled unqualified",
		},
		{
			handle: "pkg/decoy.ErrDecoy",
			want:   false,
			why:    "the literal naming it is an unrelated type that merely shares the name Check, in a package that imports no constraint package",
		},
	} {
		id, ok := got[tc.handle]
		if !ok {
			t.Errorf("%s: not extracted", tc.handle)
			continue
		}
		if id.Advisory != tc.want {
			t.Errorf("%s: advisory = %v, want %v: %s", tc.handle, id.Advisory, tc.want, tc.why)
		}
	}

	// The rows above read as "nothing is wrong" over an empty extraction, so the
	// population is pinned. Eight conditions are declared across the fixture and
	// all eight must survive.
	if len(got) != 8 {
		t.Errorf("extracted %d identities, want 8: %v", len(got), got)
	}
}

// TestRenderAdvisoryRegion_CarriesTheDeclaredAdvisoriesInShortCodeOrder pins both
// halves of the region's membership at once: which identities earn a row, and the
// order they arrive in. Asserting the order separately would let a set-only test
// pass over a region whose rows moved between runs, which is the failure the
// deterministic-output contract exists to prevent.
func TestRenderAdvisoryRegion_CarriesTheDeclaredAdvisoriesInShortCodeOrder(t *testing.T) {
	t.Parallel()
	region, err := renderAdvisoryRegion([]Identity{
		{Pkg: "pkg/z", Name: "ErrLate", ShortCode: "E0300", Message: "declared last, advised", Advisory: true},
		{Pkg: "pkg/a", Name: "ErrHalts", ShortCode: "E0100", Message: "declared first, halts"},
		{Pkg: "pkg/m", Name: "ErrEarly", ShortCode: "E0200", Message: "advised", Advisory: true},
		// A re-export reports the short code of the condition it forwards to, so
		// admitting one would put two rows under a single key in the one table
		// whose whole job is to resolve that key.
		{Pkg: "pkg/n", Name: "ErrEarly", ShortCode: "E0200", Message: "advised", Advisory: true, AliasOf: "pkg/m.ErrEarly"},
	})
	if err != nil {
		t.Fatalf("renderAdvisoryRegion: %v", err)
	}

	got := advisoryRows(t, region)
	want := []string{"E0200", "E0300"}
	if !slices.Equal(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
	if strings.Contains(region, "halts") {
		t.Errorf("the region carries a condition no check declares at warning severity:\n%s", region)
	}
}

// TestRenderAdvisoryRegion_RefusesASetWithNoAdvisoryInIt holds the renderer to
// reporting an instrument failure rather than serving an empty table. An empty
// table reads as a compiler in which nothing advises, which is indistinguishable
// from a reader that stopped recognising the check declaration — and the second
// is what an empty result over this module would actually mean.
func TestRenderAdvisoryRegion_RefusesASetWithNoAdvisoryInIt(t *testing.T) {
	t.Parallel()
	_, err := renderAdvisoryRegion([]Identity{
		{Pkg: "pkg/a", Name: "ErrHalts", ShortCode: "E0100", Message: "halts"},
	})
	if err == nil {
		t.Fatal("rendered a region from a set carrying no advisory; an empty table reads as a compiler in which nothing advises")
	}
	if !strings.Contains(err.Error(), "warning severity") {
		t.Errorf("refusal does not name what stopped it: %v", err)
	}
}

// TestRenderAdvisoryRegion_RefusesADuplicateShortCode covers the extraction
// having gone wrong rather than the declaration: registration already refuses a
// duplicate at package initialisation, so two rows under one key here mean the
// reader produced them, and publishing an ambiguous lookup is worse than
// refusing.
func TestRenderAdvisoryRegion_RefusesADuplicateShortCode(t *testing.T) {
	t.Parallel()
	_, err := renderAdvisoryRegion([]Identity{
		{Pkg: "pkg/a", Name: "ErrOne", ShortCode: "E0100", Message: "one", Advisory: true},
		{Pkg: "pkg/b", Name: "ErrTwo", ShortCode: "E0100", Message: "two", Advisory: true},
	})
	if err == nil {
		t.Fatal("rendered two rows under one short code; a lookup keyed on it then names no single condition")
	}
	if !strings.Contains(err.Error(), "E0100") {
		t.Errorf("refusal does not name the ambiguous code: %v", err)
	}
}

// TestRenderAdvisoryRegion_RefusesADeclaredStringCarryingATableSeparator holds
// this region to the same refusal the other two carry. A separator inside a cell
// renders as extra columns, so the row silently stops meaning what it says, and
// no escaping fixes it.
func TestRenderAdvisoryRegion_RefusesADeclaredStringCarryingATableSeparator(t *testing.T) {
	t.Parallel()
	_, err := renderAdvisoryRegion([]Identity{
		{Pkg: "pkg/a", Name: "ErrPiped", ShortCode: "E0100", Message: "a message | with a separator", Advisory: true},
	})
	if err == nil {
		t.Fatal("rendered a message carrying a table separator")
	}
	if !strings.Contains(err.Error(), "pkg/a.ErrPiped") {
		t.Errorf("refusal does not name the declaration: %v", err)
	}
}

// TestAdvisoriesIn_LiveModuleAdvisoriesAreAllRaised is the claim the generator
// makes when it renders this region from the whole identity set rather than from
// the admitted subset: a condition a check declares at warning severity IS raised
// by that check, so the two corpora cannot disagree.
//
// It is asserted rather than assumed because it rests on the raise reader
// counting the sentinel field's invocation, which is a property of that reader
// and not of the check. If it ever stopped counting, this region would publish a
// condition the reference itself excludes as unraisable, and nothing else would
// say so.
func TestAdvisoriesIn_LiveModuleAdvisoriesAreAllRaised(t *testing.T) {
	t.Parallel()
	modulePath, err := modulePathOf("../../..")
	if err != nil {
		t.Fatalf("module path: %v", err)
	}
	ids, err := scan("../../..", modulePath)
	if err != nil {
		t.Fatalf("scan module: %v", err)
	}

	advisories := advisoriesIn(ids)
	// Calibration: every walk below reads as "nothing is wrong" over an empty
	// set, so the input is proven non-empty before any verdict is believed.
	if len(advisories) == 0 {
		t.Fatal("the module declares no advisory; the check reader is broken, not the rule")
	}

	published, _ := admit(ids)
	admitted := map[string]bool{}
	for _, id := range published {
		admitted[id.Handle()] = true
	}
	for _, id := range advisories {
		if !id.Raised {
			t.Errorf("%s is declared at warning severity and reads as raised by nothing; "+
				"the check that declares it invokes its accessor, so the raise reader has stopped counting that invocation",
				id.Handle())
		}
		if !admitted[id.Handle()] {
			t.Errorf("%s is declared at warning severity and the admission rule excludes it, "+
				"so the region would publish a condition the reference itself withholds", id.Handle())
		}
		if id.ShortCode == "" || id.Message == "" {
			t.Errorf("%s carries no short code or no message, so its row would be empty in a cell", id.Handle())
		}
	}

	// The advisory set is a strict subset of the declared set. Equality would mean
	// the reader marked everything, which every assertion above passes over.
	if len(advisories) >= len(ids) {
		t.Errorf("%d advisories over %d declared handles; the reader is not discriminating", len(advisories), len(ids))
	}
}

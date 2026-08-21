// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// The compiler half of the authored-schema parity check.
//
// Two readers answer questions about an entity's schema and neither can call
// the other: the compiler reads the compiled schema, while the
// publishing-linter guest reads the authored entity because it cannot run
// schema compilation. The duplication is structural, so it is held by a shared
// expectation rather than by eye.
//
// Two such questions live here, because they are the same duplication over the
// same bytes and one handshake holds both: which leaves a schema declares
// sensitive, and which property names one pillar of a schema exposes. Splitting
// them into two fixtures would mean two copies of the handshake below, and the
// handshake is itself a mechanic that must have one home.
//
// This file is one of the two halves. Both read
// plugins/publishing-linter/tests/fixtures/sensitivity_parity.json — the ONE
// source of cases — so a case is checked on both sides or on neither. Add a case
// to that file rather than inline here or in the reader's own unit tests; a case
// added inline is invisible to the other side.
//
// Every case's schema is written as JSON, which is valid YAML, so the bytes the
// guest receives and the bytes compiled here are identical.
//
// Reading the same file is not the same as agreeing, and the difference is what
// the handshake at the bottom of this file exists for. Two halves that each
// assert independently cannot tell agreement apart from one half never having
// been asked: silence a half and the other still passes, and the pair goes on
// reporting a parity that only one side checked. So each half pins the revision
// of the fixture it verified, and each half reads the other's pin and the
// other's test declarations. A half that is absent, suppressed, or pinned to an
// older fixture is named out loud instead of being read as agreement.

package lintops

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// sensitivityParityFixture is the shared case file, held by the publishing
// linter's crate because the guest reaches it without leaving its own tree.
const sensitivityParityFixture = "../../plugins/publishing-linter/tests/fixtures/sensitivity_parity.json"

// sensitivityParityGuestHalf is the guest's half of the check, read as text so
// this half can see whether the other one is still there and still current.
const sensitivityParityGuestHalf = "../../plugins/publishing-linter/tests/sensitivity_parity_test.rs"

// sensitivityParityFixtureRevision is this half's record of the fixture it
// verified, derived from the fixture's own bytes rather than tracked by hand so
// that no edit can leave it behind. The guest half carries the same value in its
// own source, and the two are compared against the fixture and against each
// other.
//
// This is deliberately a SECOND copy of a value the guest also holds, and
// unifying the two would destroy the only thing it does: one copy cannot
// disagree with itself, and disagreement is the signal. When the fixture
// changes, both copies move, and a run that updates only one is exactly the
// half-verified state this pins.
const sensitivityParityFixtureRevision = "29c12bd61856f53b"

type sensitivityParityCases struct {
	LeafCases []struct {
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
		Expect struct {
			Values   []string `json:"values"`
			Infra    []string `json:"infra"`
			Provides []string `json:"provides"`
		} `json:"expect"`
	} `json:"leafCases"`
	SlotCases []struct {
		Name            string          `json:"name"`
		Slot            json.RawMessage `json:"slot"`
		ExpectSensitive bool            `json:"expectSensitive"`
		CompilerDecodes bool            `json:"compilerDecodes"`
	} `json:"slotCases"`
	PillarKeyCases []struct {
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
		// ExpectKeys is the compiler's own answer, so this half is what
		// establishes it and what fails when compilation moves it out from
		// under the guest.
		ExpectKeys []string `json:"expectKeys"`
		// GuestReadable records which side of the enumerable/unenumerable line
		// a case sits on. Only the guest can assert its own half of that, the
		// same way only the compiler asserts CompilerDecodes above.
		GuestReadable bool `json:"guestReadable"`
	} `json:"pillarKeyCases"`
}

func readSensitivityParityFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(sensitivityParityFixture))
	if err != nil {
		t.Fatalf("read shared parity fixture %s: %v", sensitivityParityFixture, err)
	}
	return raw
}

func loadSensitivityParityCases(t *testing.T) sensitivityParityCases {
	t.Helper()
	raw := readSensitivityParityFixture(t)
	var cases sensitivityParityCases
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse shared parity fixture %s: %v", sensitivityParityFixture, err)
	}
	return cases
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameSet(got map[string]bool, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, w := range want {
		if !got[w] {
			return false
		}
	}
	return true
}

// TestSensitivityLeafParityAgainstSharedFixture runs every shared case through
// the compiler's reader. The expectation in the fixture is the compiler's truth
// by definition, so this test is what establishes it — and what fails when a
// change to compilation or to the reader moves it out from under the guest.
func TestSensitivityLeafParityAgainstSharedFixture(t *testing.T) {
	t.Parallel()
	cases := loadSensitivityParityCases(t)
	if len(cases.LeafCases) < 30 {
		t.Fatalf("the shared fixture lost cases (%d left); it is the only source of cases for both sides", len(cases.LeafCases))
	}

	for _, c := range cases.LeafCases {
		var node yaml.Node
		if err := yaml.Unmarshal(c.Schema, &node); err != nil {
			t.Errorf("%s: schema does not parse: %v", c.Name, err)
			continue
		}
		compiled, err := compactform.Compile(&node)
		if err != nil {
			t.Errorf("%s: schema does not compile: %v", c.Name, err)
			continue
		}
		declared := resolver.DeclaredSensitiveLeaves(compiled)

		for _, pillar := range []struct {
			name string
			got  map[string]bool
			want []string
		}{
			{"values", declared.Values, c.Expect.Values},
			{"infra", declared.Infra, c.Expect.Infra},
			{"provides", declared.Provides, c.Expect.Provides},
		} {
			if !sameSet(pillar.got, pillar.want) {
				t.Errorf("%s / %s: compiler collected %v, shared expectation %v",
					c.Name, pillar.name, sortedKeys(pillar.got), pillar.want)
			}
		}

		wantAny := len(c.Expect.Values)+len(c.Expect.Infra)+len(c.Expect.Provides) > 0
		if declared.Any() != wantAny {
			t.Errorf("%s: Any() = %v, want %v", c.Name, declared.Any(), wantAny)
		}
	}
}

// TestSensitivitySlotParityAgainstSharedFixture runs the shared slot cases
// through the compiler's own decode of a slot. The compiler reads the marker as
// a boolean field, so a non-boolean is not merely "not a declaration" there — it
// is refused, and the fixture records which side of that line each case sits on.
func TestSensitivitySlotParityAgainstSharedFixture(t *testing.T) {
	t.Parallel()
	cases := loadSensitivityParityCases(t)
	if len(cases.SlotCases) < 5 {
		t.Fatalf("the shared fixture lost slot cases (%d left)", len(cases.SlotCases))
	}

	for _, c := range cases.SlotCases {
		var slot types.AttachmentSlot
		err := yaml.Unmarshal(c.Slot, &slot)
		if c.CompilerDecodes {
			if err != nil {
				t.Errorf("%s: the shared expectation says the compiler decodes this slot, but it does not: %v", c.Name, err)
				continue
			}
			if slot.Sensitive != c.ExpectSensitive {
				t.Errorf("%s: compiler read %v, shared expectation %v", c.Name, slot.Sensitive, c.ExpectSensitive)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: the shared expectation says the compiler refuses this slot, but it decoded to %+v", c.Name, slot)
		}
		if c.ExpectSensitive {
			t.Errorf("%s: a slot the compiler refuses cannot be expected to declare sensitivity", c.Name)
		}
	}
}

// TestSensitivityParityFixtureCoversEveryDivergenceShape pins the shape classes
// the two readers can genuinely disagree on. Each is a shape whose authored
// spelling and compiled spelling differ in a way a naive reader gets wrong, so
// dropping one from the fixture would silence the case that motivates the parity
// check in the first place.
func TestSensitivityParityFixtureCoversEveryDivergenceShape(t *testing.T) {
	t.Parallel()
	cases := loadSensitivityParityCases(t)
	present := map[string]bool{}
	for _, c := range cases.LeafCases {
		present[c.Name] = true
	}
	for _, required := range []string{
		// A marker without a type reads as a property named after the marker
		// unless the marker is known to be a keyword.
		"typeless-leaf-declares",
		// Three shapes that carry the marker without routing through the leaf
		// emitter, each with its own emit site.
		"marker-beside-ref",
		"marker-beside-allof",
		"marker-beside-anyof",
		"marker-beside-oneof",
		// The path spelling a nested declaration is keyed by.
		"nested-implicit-object",
		"nested-explicit-properties",
		// The boolean-only strictness, which a presence test would fail.
		"only-boolean-true-declares",
		// Authored JSON Schema, where the same key is content and not a marker.
		"explicit-jsonschema-mode-declares-nothing",
		"escape-mapping-body-declares-nothing-at-its-own-node",
		"escape-mapping-body-nested-leaf-declares",
		// The one declared divergence: a body only a YAML parser can open.
		"escape-text-body-is-unreadable-in-the-guest",
		// A declaration INSIDE a composing branch, which lands on the
		// composing node rather than below the branch — one case per branch
		// shape, because the list-valued and single-schema keywords are two
		// separate keyword sets and a reader can descend one and miss the
		// other.
		"composition-branch-leaf-declares-on-the-composing-node",
		"composition-branch-properties-declare-below-the-composing-node",
		"conditional-branch-properties-declare-below-the-carrying-node",
		"negated-branch-leaf-declares-on-the-carrying-node",
		// A declaration INSIDE a reference target, which lands at the
		// referring path — from a definitions block at each of the two scopes
		// a pointer can resolve against.
		"reference-target-declares-at-the-referring-path",
		"reference-target-at-the-document-root-resolves-too",
		// The two halves of the cycle rule, which are opposite failures: a
		// recursive target that declares withholds its tail, one that declares
		// nothing stays readable.
		"recursive-reference-declaring-terminates-and-withholds-the-tail",
		"recursive-reference-declaring-nothing-stays-readable",
		// A pointer nothing resolves, where withholding the referring path is
		// the only reading that cannot under-report.
		"unresolvable-reference-collects-the-referring-path",
		// The shapes the walk deliberately does not descend into.
		"composition-sibling-properties-are-not-walked",
		"array-items-are-not-walked",
	} {
		if !present[required] {
			t.Errorf("the shared fixture no longer carries the %q case; that shape is where the two readers can diverge", required)
		}
	}
}

// TestProvidesPillarKeyParityAgainstSharedFixture runs every shared pillar-key
// case through the compiler's own reader — the exported entry point a build
// uses, not a private helper, so what is pinned is the answer the inject guard
// actually acts on. The expectation in the fixture is the compiler's truth by
// definition, so this test is what establishes it.
//
// It also asserts the case names that carry the shapes the two readers can
// genuinely disagree on. Dropping one of those would silence the case that
// motivates holding this pair together at all, and a shrunken fixture reads as a
// passing one.
func TestProvidesPillarKeyParityAgainstSharedFixture(t *testing.T) {
	t.Parallel()
	cases := loadSensitivityParityCases(t)
	if len(cases.PillarKeyCases) < 15 {
		t.Fatalf("the shared fixture lost pillar-key cases (%d left); it is the only source of cases for both sides", len(cases.PillarKeyCases))
	}

	present := map[string]bool{}
	for _, c := range cases.PillarKeyCases {
		present[c.Name] = true

		var node yaml.Node
		if err := yaml.Unmarshal(c.Schema, &node); err != nil {
			t.Errorf("%s: schema does not parse: %v", c.Name, err)
			continue
		}
		inner := node
		if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
			inner = *node.Content[0]
		}
		comp := types.Component{Spec: types.ComponentSpec{Schema: inner}}
		got := resolver.ComponentProvidesKeys(comp, nil)
		if !sameSet(got, c.ExpectKeys) {
			t.Errorf("%s: compiler exposed %v, shared expectation %v",
				c.Name, sortedKeys(got), c.ExpectKeys)
		}
	}

	for _, required := range []string{
		// A pillar node whose own shape decides whether it exposes anything at
		// all. Each is a spelling a reader can classify wrongly in a way that
		// changes the answer rather than merely the path.
		"pillar-exposes-its-implicit-properties",
		"pillar-property-named-like-a-compact-keyword-makes-the-pillar-a-leaf",
		"pillar-with-explicit-properties-exposes-them",
		"pillar-declaring-jsonschema-mode-exposes-its-properties",
		"pillar-declaring-compact-mode-becomes-a-leaf",
		// The two definition-block spellings, which name reference targets
		// rather than data. A reader excluding one and not the other reports a
		// definitions block as a published key.
		"definitions-blocks-are-reference-scopes-not-properties",
		"legacy-definitions-block-is-also-a-reference-scope",
		// An empty contract, which is a real answer and not an absent one.
		"no-provides-pillar-is-an-empty-contract",
		// The escaped body in both authored forms: the mapping both readers can
		// open, and the text form only a YAML parser can.
		"escaped-mapping-body-exposes-its-properties",
		"escaped-text-body-is-unreadable-in-the-guest",
		// The reference forms, whose compiled answer is not what an author
		// would predict from the authored text.
		"reference-pillar-is-unreadable-in-the-guest",
		"reference-pillar-with-sibling-properties-is-unreadable-in-the-guest",
		// One case per list-valued composition keyword, because the three are a
		// set and a reader can handle one and miss another.
		"all-of-pillar-is-unreadable-in-the-guest",
		"any-of-pillar-is-unreadable-in-the-guest",
		"one-of-pillar-is-unreadable-in-the-guest",
	} {
		if !present[required] {
			t.Errorf("the shared fixture no longer carries the %q case; that shape is where the two readers can diverge", required)
		}
	}
}

// ---------------------------------------------------------------------
// The cross-half handshake
// ---------------------------------------------------------------------

// sensitivityParityRevision derives a fixture's revision from its own bytes.
//
// Derived rather than authored, because an authored revision is one an editor
// can forget to bump, and a forgotten bump leaves both halves pinned to a value
// that no longer describes what they read — the stale record this handshake
// exists to refuse. Any change to the fixture, down to a byte, moves it.
//
// FNV-1a is used because what is needed is a short, exactly reproducible
// fingerprint of a file both halves already read, in two languages, with no
// dependency added to a guest crate to get it. The risk being managed is an
// edit going unnoticed, not an adversary constructing a collision. The guest
// half spells the same function out by hand; that duplication is deliberate and
// is explained where the pinned constant is declared.
func sensitivityParityRevision(fixture []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(fixture)
	return fmt.Sprintf("%016x", h.Sum64())
}

// characterLiteralEnd returns the index of the quote that closes a character
// literal opening at i, or i itself when what opens there is not one.
//
// The closing quote is what distinguishes the two things a single quote can
// begin in the languages read here: a character literal closes, and a lifetime
// does not. Anything unrecognised is left to be treated as ordinary text, which
// is the safe direction — a lifetime read as a character literal would swallow
// the code after it.
func characterLiteralEnd(src string, i int) int {
	const longestEscape = 9 // the widest escape form, plus its two quotes
	if i+2 < len(src) && src[i+1] == '\\' {
		for j := i + 2; j < len(src) && j <= i+longestEscape; j++ {
			if src[j] == '\'' {
				return j
			}
		}
		return i
	}
	if i+2 < len(src) && src[i+2] == '\'' {
		return i + 2
	}
	return i
}

// stripSourceComments removes line and block comments, leaving code text and
// line structure intact. The two halves are written in languages that share
// C-style comments, double-quoted strings and single-quoted character
// literals, so one reader serves both.
//
// Stripping first is what makes the checks below safe rather than merely
// convenient. Every token they hunt is also a word this file and its
// counterpart use in explanation — a test's name, the attribute that suppresses
// a test, a revision. A scan over raw text would read a sentence describing the
// mechanism as an instance of it. What survives stripping is code, and only
// code can silence a test.
func stripSourceComments(src string) string {
	const (
		code = iota
		lineComment
		blockComment
		stringLiteral
	)
	var out strings.Builder
	out.Grow(len(src))
	state := code
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch state {
		case code:
			switch {
			case c == '/' && i+1 < len(src) && src[i+1] == '/':
				state = lineComment
				i++
			case c == '/' && i+1 < len(src) && src[i+1] == '*':
				state = blockComment
				i++
			case c == '\'':
				// Skipped whole when it is a character literal, because a
				// double quote inside one is an ordinary thing to write and
				// reading it as the start of a string would invert every
				// string boundary in the rest of the file — leaving comments
				// unstripped in the tail, which is exactly where a silenced
				// test would then hide.
				if end := characterLiteralEnd(src, i); end > i {
					out.WriteString(src[i : end+1])
					i = end
				} else {
					out.WriteByte(c)
				}
			default:
				if c == '"' {
					state = stringLiteral
				}
				out.WriteByte(c)
			}
		case lineComment:
			if c == '\n' {
				state = code
				out.WriteByte(c)
			}
		case blockComment:
			switch {
			case c == '*' && i+1 < len(src) && src[i+1] == '/':
				state = code
				i++
			case c == '\n':
				out.WriteByte(c)
			}
		case stringLiteral:
			out.WriteByte(c)
			switch {
			case c == '\\' && i+1 < len(src):
				i++
				out.WriteByte(src[i])
			case c == '"':
				state = code
			}
		}
	}
	return out.String()
}

// sensitivityParityPinnedValue reads the value a declaration assigns, refusing
// both zero matches and more than one. Zero means the other half's record of
// what it verified is gone, and a comparison against nothing passes no matter
// what the other half does — the exact shape of failure this handshake replaces.
// More than one means the wrong record could be read.
func sensitivityParityPinnedValue(t *testing.T, source, declaration, where string) (string, bool) {
	t.Helper()
	switch n := strings.Count(source, declaration); {
	case n == 0:
		t.Errorf("%s no longer declares %s…\": without it there is no record of which fixture "+
			"that half verified, and this check would compare against nothing", where, declaration)
		return "", false
	case n > 1:
		t.Errorf("%s declares %s…\" %d times; the record has to be unambiguous or the wrong one "+
			"can be read", where, declaration, n)
		return "", false
	}
	rest := source[strings.Index(source, declaration)+len(declaration):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		t.Errorf("%s: the value assigned by %s…\" is unterminated", where, declaration)
		return "", false
	}
	return rest[:end], true
}

// sensitivityParityGuestChecks are the guest-side tests this half requires to be
// declared and live.
//
// The guest's own half of the handshake is in the list on purpose. Leaving it
// out would let it be removed while this half's view of the guest survived,
// which is half a handshake and reports as a whole one.
var sensitivityParityGuestChecks = []string{
	"leaf_cases_match_the_shared_expectation",
	"slot_cases_match_the_shared_expectation",
	"pillar_key_cases_match_the_shared_expectation",
	"both_halves_pin_the_same_fixture_revision",
}

// assertGuestCheckIsLive reports a guest-side test that is missing, commented
// out, or suppressed by an attribute.
//
// The attribute run immediately above the declaration is what is read, rather
// than the whole file, so a suppression attribute on some unrelated test cannot
// be mistaken for one here.
func assertGuestCheckIsLive(t *testing.T, source, name string) {
	t.Helper()
	lines := strings.Split(source, "\n")
	declaration := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "fn "+name+"(") {
			declaration = i
			break
		}
	}
	if declaration < 0 {
		t.Errorf("%s no longer declares the check %q. Either it was renamed — in which case this "+
			"list needs the new name — or it was removed or commented out, in which case the guest "+
			"side of the comparison is not running and this half's green says nothing about it",
			sensitivityParityGuestHalf, name)
		return
	}
	marked, suppressed := false, false
	for i := declaration - 1; i >= 0; i-- {
		attribute := strings.TrimSpace(lines[i])
		if attribute == "" {
			continue
		}
		if !strings.HasPrefix(attribute, "#[") {
			break
		}
		switch {
		case attribute == "#[test]":
			marked = true
		case strings.HasPrefix(attribute, "#[ignore"):
			suppressed = true
		}
	}
	if !marked {
		t.Errorf("%s declares %q but no longer marks it as a test, so it never runs", sensitivityParityGuestHalf, name)
	}
	if suppressed {
		t.Errorf("%s marks %q as a test and then suppresses it, so it does not run and the parity "+
			"claim covers one side only", sensitivityParityGuestHalf, name)
	}
}

// TestBothHalvesPinTheSameFixtureRevision is the handshake: it refuses a parity
// claim that only one half is making.
//
// Reading a shared fixture is not agreement. Two halves that each check
// themselves against it and never look at each other cannot distinguish "we
// agree" from "the other one never ran" — silence one and the other still
// passes. So this half asserts three things, and the guest half asserts their
// mirror image:
//
//   - that the fixture it just read is the revision it records having verified,
//     which is what makes its own green mean something specific;
//   - that the other half records the SAME revision, so a half left behind by a
//     fixture edit is reported as not having verified the current one rather
//     than standing in for it;
//   - that the other half's checks are still declared and still live, so a
//     removed, renamed or suppressed check is named out loud instead of quietly
//     reducing the pair to one.
//
// What it does not catch, stated so the guarantee is not read as wider than it
// is: a run that filters the other half out at the command line. No test can
// observe its own non-execution, and a filtered-out half leaves its source live
// and correctly pinned. That case is someone choosing not to check, rather than
// a check that stopped happening on its own.
func TestBothHalvesPinTheSameFixtureRevision(t *testing.T) {
	t.Parallel()

	revision := sensitivityParityRevision(readSensitivityParityFixture(t))
	if revision != sensitivityParityFixtureRevision {
		t.Errorf("this half records having verified fixture revision %s, but the fixture it just "+
			"read is revision %s. The fixture changed and this half's record did not follow it; "+
			"update the recorded revision to %s in the same change that edits the fixture, and "+
			"make sure the other half's record moves too",
			sensitivityParityFixtureRevision, revision, revision)
	}

	raw, err := os.ReadFile(filepath.FromSlash(sensitivityParityGuestHalf))
	if err != nil {
		t.Fatalf("read the guest half at %s: %v. Both halves live in this tree; if this one cannot "+
			"be found, the pair is not being compared at all", sensitivityParityGuestHalf, err)
	}
	guest := stripSourceComments(string(raw))

	if pinned, ok := sensitivityParityPinnedValue(t, guest,
		"const SENSITIVITY_PARITY_FIXTURE_REVISION: &str = \"", sensitivityParityGuestHalf); ok {
		if pinned != revision {
			t.Errorf("the guest half records having verified fixture revision %s, but the fixture "+
				"is revision %s. That half has not checked itself against the current cases, so "+
				"whatever this half reports covers one reader only. Re-run the guest half against "+
				"the current fixture and update its recorded revision to %s",
				pinned, revision, revision)
		}
	}

	for _, name := range sensitivityParityGuestChecks {
		assertGuestCheckIsLive(t, guest, name)
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The declared parameter-read rule, driven over authored text this package
// composes rather than over a committed fixture.
//
// The fixture-backed proofs live beside the rule's other assertions: one over a
// real Aggregate through the presentation the resolve builds, and one over the
// resolve result the advisory rides out on. What this file adds are the shapes a
// committed workspace does not carry — a declaration whose default might be read
// as an exemption, text that does not parse at all, and a document authored in
// several orders — each of which needs its authored text composed for it.

// contractOf builds a parameter contract carrying the given keys in the order
// supplied, each with a minimal typed body.
func contractOf(keys ...string) types.OrderedMap[any] {
	c := types.OrderedMap[any]{}
	for _, k := range keys {
		c.Set(k, map[string]any{"type": "string"})
	}
	return c
}

// fieldsOf returns each finding's Field locator, in the order reported.
func fieldsOf(findings []*ResolverError) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Field)
	}
	return out
}

// declaredReadLocators invokes the declared rule the way the resolve invokes it
// — once per name the presented contract declares, over the presented document's
// authored text — and returns the locator each objection names, in the order
// raised.
//
// It goes through the presentation rather than calling the rule's Run directly,
// because the invocation is half of what these assertions are about: a rule
// handed the wrong subject, or a presentation answering from the contract
// instead of the text, would both pass a direct call.
func declaredReadLocators(t *testing.T, raw []byte, contract types.OrderedMap[any]) []string {
	t.Helper()

	const document = "composed/aggregate.yaml"
	var presented constraint.DocumentView = newAggregateDocumentView(document, raw, contract)
	objections := constraint.RunOnDocumentParameters(
		document,
		presented.DeclaredParameterKeys(document),
		presented,
		declaredParameterRead,
	)
	out := make([]string, 0, len(objections))
	for _, o := range objections {
		out = append(out, o.Locator())
	}
	return out
}

// A default is not an exemption. A default no reference reads reaches the
// rendered output no more than a value a Release supplies does, so a
// defaulted declaration is objected about exactly as a bare one is.
//
// Nothing about the rule's body distinguishes the two — which is the point: the
// contract entry carries the default and the rule never looks at it, so this is
// the assertion that would catch a later exemption being introduced on the
// grounds that a default "has a value anyway".
func TestDeclaredParameterRead_DefaultedButUnreferencedIsObjectedAbout(t *testing.T) {
	t.Parallel()

	raw := []byte(`spec:
  parameters:
    replicas:
      type: integer
      default: 3
  components:
    api:
      values:
        fixed: "always"
`)
	contract := types.OrderedMap[any]{}
	contract.Set("replicas", map[string]any{"type": "integer", "default": 3})

	got := declaredReadLocators(t, raw, contract)
	if want := []string{".spec.parameters.replicas"}; !equalStrings(got, want) {
		t.Fatalf("defaulted-but-unread declaration: locators = %v; want %v", got, want)
	}
}

// The blinding hazard. Text that does not parse yields no reference set, and
// objecting about every declaration on that basis would turn one parse failure
// into a wall of wrong advisories that buries the diagnostic raised over the
// same text. Nothing is objected about instead.
func TestDeclaredParameterRead_UnparseableTextObjectsAboutNothing(t *testing.T) {
	t.Parallel()

	raw := []byte(`spec:
  parameters:
    a:
      type: string
  components:
    api:
      values:
        broken: "{{ .parameters.a "
`)
	// The needle is calibrated before it is used: text that parsed would leave
	// this test passing for the wrong reason, over a document whose references
	// were collected normally and happened to read nothing.
	if _, err := gotmpl.CollectTemplateFieldAccesses(string(raw)); err == nil {
		t.Fatalf("the planted text parses, so this test does not exercise the parse-failure path")
	}
	if got := declaredReadLocators(t, raw, contractOf("a", "b", "c")); len(got) != 0 {
		t.Fatalf("a parse failure produced %d objection(s) (a blind verdict): %v", len(got), got)
	}
}

// Determinism, stated against this rule's own ordering contract rather than
// against a sorted one.
//
// The rule is invoked once per declared name and raises in that order, so the
// sequence it produces is the order the contract declares — and the same
// document authored in a different order therefore reports in that different
// order, deterministically, rather than collapsing to one sorted sequence. Both
// halves are asserted: each order reproduces itself, and the orders are required
// to differ from one another, without which a sorting implementation would
// satisfy every row.
func TestDeclaredParameterRead_SequenceFollowsAuthorOrderDeterministically(t *testing.T) {
	t.Parallel()

	raw := []byte("spec:\n  components: {}\n")
	orders := [][]string{
		{"alpha", "beta", "gamma"},
		{"gamma", "alpha", "beta"},
		{"beta", "gamma", "alpha"},
	}

	locatorsFor := func(order []string) []string {
		out := make([]string, 0, len(order))
		for _, key := range order {
			out = append(out, ".spec.parameters."+key)
		}
		return out
	}

	// Calibration: at least one row must differ from every other, or the table
	// cannot tell an author-ordered result from a sorted one.
	var distinct bool
	for i := range orders {
		for j := i + 1; j < len(orders); j++ {
			if !equalStrings(orders[i], orders[j]) {
				distinct = true
			}
		}
	}
	if !distinct {
		t.Fatal("every row authors the same order, so this table cannot distinguish following " +
			"author order from sorting")
	}

	for _, order := range orders {
		want := locatorsFor(order)
		first := declaredReadLocators(t, raw, contractOf(order...))
		if !equalStrings(first, want) {
			t.Errorf("author order %v produced %v; want %v, the order the contract declares",
				order, first, want)
		}
		// Same input, again: the sequence is a function of the input and not of
		// anything the first invocation left behind.
		if second := declaredReadLocators(t, raw, contractOf(order...)); !equalStrings(second, first) {
			t.Errorf("author order %v produced %v then %v; the rule is not deterministic",
				order, first, second)
		}
	}
}

// The rule must parse with the same function set the interpolation renders with,
// or it reports a wrong verdict with nothing failing: text the evaluator accepts
// but the rule cannot parse yields no reference set, and the rule then objects
// about nothing at all — silently, on a document that does carry an unread
// declaration.
//
// So the planted text is first put through the interpolation, and only then
// asked about: a needle the evaluator rejected would be a template this rule
// never has to handle.
func TestDeclaredParameterRead_ParsesTextTheInterpolationAccepts(t *testing.T) {
	t.Parallel()

	raw := []byte(`spec:
  components:
    api:
      values:
        name: '{{ .parameters.given | upper | trimAll "x" }}'
        other: '{{ .parameters.missing_ref }}'
`)
	if _, err := InterpolateParameters(raw, map[string]any{
		"given": "v", "missing_ref": "w",
	}); err != nil {
		t.Fatalf("the interpolation rejected the planted text, so it is the wrong needle: %v", err)
	}

	got := declaredReadLocators(t, raw, contractOf("given", "unused_one"))
	if want := []string{".spec.parameters.unused_one"}; !equalStrings(got, want) {
		t.Fatalf("locators = %v; want %v — a parse failure here would object about nothing at all",
			got, want)
	}
}

// End-to-end over real files through the one boundary every command resolves
// behind: the advisory must ride the resolve result, because that is what
// carries it to the terminal drain and to the editor surface alike. The fixture
// is a genuine instance of the smell — its Release supplies `env: prod` and no
// reference in the Aggregate reads it, so the supplied value reaches nothing.
func TestDeclaredParameterReadRidesTheResolveResult(t *testing.T) {
	t.Parallel()
	result, err := resolveForTest(t, ResolveConfig{
		AggregatePath: filepath.Join("testdata", "typed-params-pass", "aggregate.yaml"),
		ReleasePath:   filepath.Join("testdata", "typed-params-pass", "release.yaml"),
		ManifestPath:  filepath.Join("testdata", "typed-params-pass", "manifest.yaml"),
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if want := []string{".spec.parameters.env"}; !equalStrings(fieldsOf(result.LintAdvisories), want) {
		t.Fatalf("LintAdvisories = %v; want %v", fieldsOf(result.LintAdvisories), want)
	}
	// The two referenced parameters must NOT be reported, or the reading is
	// inverted and every fixture would light up.
	for _, f := range fieldsOf(result.LintAdvisories) {
		if strings.Contains(f, "cache_ttl_seconds") || strings.Contains(f, "display_name") {
			t.Errorf("referenced parameter reported unread: %s", f)
		}
	}
	if !errors.Is(result.LintAdvisories[0], ErrUnreadParameter()) {
		t.Errorf("advisory does not carry ErrUnreadParameter: %v", result.LintAdvisories[0])
	}
	// The Entity names the Aggregate so a reader can tell which document the
	// advisory is about.
	if !strings.Contains(result.LintAdvisories[0].Entity, "typed-params-pass") {
		t.Errorf("Entity does not name the Aggregate: %q", result.LintAdvisories[0].Entity)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

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

func TestLintUnreadParameters_UnreferencedParameterIsReported(t *testing.T) {
	t.Parallel()
	raw := []byte(`spec:
  parameters:
    read_one:
      type: string
    never_read:
      type: string
  components:
    api:
      values:
        name: "{{ .parameters.read_one }}"
`)
	got := lintUnreadParameters(raw, contractOf("read_one", "never_read"), "demo")
	if want := []string{".spec.parameters.never_read"}; !equalStrings(fieldsOf(got), want) {
		t.Fatalf("fields = %v; want %v", fieldsOf(got), want)
	}
	if !errors.Is(got[0], ErrUnreadParameter()) {
		t.Errorf("finding does not carry ErrUnreadParameter: %v", got[0])
	}
	if !strings.Contains(got[0].Message, `"never_read"`) {
		t.Errorf("message does not name the parameter: %q", got[0].Message)
	}
	// The advisory names the parameter that was NOT read; naming the one that was
	// would be the inverted verdict this test exists to catch.
	if strings.Contains(got[0].Message, "read_one") {
		t.Errorf("message names the referenced parameter: %q", got[0].Message)
	}
}

func TestLintUnreadParameters_ReferencedParameterIsNotReported(t *testing.T) {
	t.Parallel()
	raw := []byte(`spec:
  parameters:
    db_size:
      type: string
  components:
    api:
      values:
        size: "{{ .parameters.db_size }}"
`)
	if got := lintUnreadParameters(raw, contractOf("db_size"), "demo"); len(got) != 0 {
		t.Fatalf("referenced parameter reported unread: %v", fieldsOf(got))
	}
}

// A default is not an exemption: the ruling is that a declaration nothing reads
// is reported whether or not it carries one, because the default reaches the
// output no more than a Release-supplied value does.
func TestLintUnreadParameters_DefaultedButUnreferencedStillReported(t *testing.T) {
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

	got := lintUnreadParameters(raw, contract, "demo")
	if want := []string{".spec.parameters.replicas"}; !equalStrings(fieldsOf(got), want) {
		t.Fatalf("defaulted-but-unread parameter not reported: fields = %v; want %v",
			fieldsOf(got), want)
	}
}

// The blinding hazard: text that does not parse yields no reference set, and
// reporting every declared parameter as unread on that basis would turn a parse
// failure into a wall of wrong advisories. Nothing is reported instead.
func TestLintUnreadParameters_UnparseableTextReportsNothing(t *testing.T) {
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
	// Confirm the planted text really is unparseable — a needle that parses would
	// make this test pass for the wrong reason.
	if _, err := gotmpl.CollectTemplateFieldAccesses(string(raw)); err == nil {
		t.Fatalf("planted text parses; the needle does not exercise the parse-failure path")
	}
	if got := lintUnreadParameters(raw, contractOf("a", "b", "c"), "demo"); len(got) != 0 {
		t.Fatalf("parse failure produced %d advisories (blind verdict): %v",
			len(got), fieldsOf(got))
	}
}

// Reading the whole parameter map makes no per-key claim possible, so every
// declaration counts as read.
func TestLintUnreadParameters_WholeMapReadReportsNothing(t *testing.T) {
	t.Parallel()
	raw := []byte(`spec:
  components:
    api:
      values:
        all: "{{ range $k, $v := .parameters }}{{ $k }}{{ end }}"
`)
	if got := lintUnreadParameters(raw, contractOf("a", "b"), "demo"); len(got) != 0 {
		t.Fatalf("whole-map read still reported keys: %v", fieldsOf(got))
	}
}

// A nested read marks its top-level declaration read — the same granularity the
// authored-key reading uses.
func TestLintUnreadParameters_NestedReadMarksTopLevelDeclaration(t *testing.T) {
	t.Parallel()
	raw := []byte(`spec:
  components:
    api:
      values:
        host: "{{ .parameters.database.host }}"
`)
	if got := lintUnreadParameters(raw, contractOf("database"), "demo"); len(got) != 0 {
		t.Fatalf("nested read did not mark the top-level declaration: %v", fieldsOf(got))
	}
}

// Determinism: findings are sorted by key, so a contract authored in one order
// and the same contract authored in another report identically.
func TestLintUnreadParameters_OrderIsDeterministicAcrossAuthorOrder(t *testing.T) {
	t.Parallel()
	raw := []byte("spec:\n  components: {}\n")
	want := []string{
		".spec.parameters.alpha",
		".spec.parameters.beta",
		".spec.parameters.gamma",
	}
	for _, order := range [][]string{
		{"alpha", "beta", "gamma"},
		{"gamma", "alpha", "beta"},
		{"beta", "gamma", "alpha"},
	} {
		got := fieldsOf(lintUnreadParameters(raw, contractOf(order...), "demo"))
		if !equalStrings(got, want) {
			t.Errorf("author order %v produced %v; want %v", order, got, want)
		}
	}
}

func TestLintUnreadParameters_NoDeclaredParametersReportsNothing(t *testing.T) {
	t.Parallel()
	raw := []byte("spec:\n  components: {}\n")
	if got := lintUnreadParameters(raw, types.OrderedMap[any]{}, "demo"); got != nil {
		t.Fatalf("empty contract produced advisories: %v", fieldsOf(got))
	}
}

// The check must parse with the same function set the interpolation renders
// with, or it reports a wrong verdict with nothing failing. An Aggregate calling
// a catalog helper the interpolation accepts must therefore still yield a
// reference set here rather than a parse failure (which would report nothing) —
// and the reference inside that call must still be collected.
func TestLintUnreadParameters_ParsesTextTheInterpolationAccepts(t *testing.T) {
	t.Parallel()
	raw := []byte(`spec:
  components:
    api:
      values:
        name: '{{ .parameters.given | upper | trimAll "x" }}'
        other: '{{ .parameters.missing_ref }}'
`)
	// The evaluator accepts this text; if it did not, the comparison below would
	// be against a template the check never has to handle.
	if _, err := InterpolateParameters(raw, map[string]any{
		"given": "v", "missing_ref": "w",
	}); err != nil {
		t.Fatalf("interpolation rejected the planted text, so it is the wrong needle: %v", err)
	}
	got := lintUnreadParameters(raw, contractOf("given", "unused_one"), "demo")
	if want := []string{".spec.parameters.unused_one"}; !equalStrings(fieldsOf(got), want) {
		t.Fatalf("fields = %v; want %v (a parse failure here would report nothing at all)",
			fieldsOf(got), want)
	}
}

// End-to-end over real files through the one boundary every command resolves
// behind: the advisory must ride the resolve result, because that is what
// carries it to the terminal drain and to the editor surface alike. The fixture
// is a genuine instance of the smell — its Release supplies `env: prod` and no
// reference in the Aggregate reads it, so the supplied value reaches nothing.
func TestLintUnreadParameters_RidesTheResolveResult(t *testing.T) {
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

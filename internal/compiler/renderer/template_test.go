// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestEvalTemplate_Simple(t *testing.T) {
	t.Parallel()
	out, err := EvalTemplate("test", `hello {{ .Name }}`, map[string]string{"Name": "world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello world" {
		t.Errorf("got %q", out)
	}
}

func TestEvalTemplate_Sprig(t *testing.T) {
	t.Parallel()
	out, err := EvalTemplate("test", `{{ "hello" | upper }}`, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "HELLO" {
		t.Errorf("got %q", out)
	}
}

func TestEvalTemplate_MissingKeyZero(t *testing.T) {
	t.Parallel()
	// After JSON round-trip, map[string]string becomes map[string]interface{}.
	// Missing keys return nil (zero of interface{}), which renders as "<no value>".
	// Templates should use Sprig default for optional values.
	out, err := EvalTemplate("test", `value={{ default "" .Missing }}`, map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "value=" {
		t.Errorf("got %q, expected 'value='", out)
	}
}

func TestEvalTemplate_ParseError(t *testing.T) {
	t.Parallel()
	_, err := EvalTemplate("bad", `{{ .Invalid }{`, nil)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestEvalTemplate_JSON(t *testing.T) {
	t.Parallel()
	data := map[string]interface{}{
		"Port": 8080,
		"Host": "localhost",
	}
	out, err := EvalTemplate("json", `{"port": {{ .Port }}, "host": "{{ .Host }}"}`, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != `{"port": 8080, "host": "localhost"}` {
		t.Errorf("got %q", out)
	}
}

// Walker tests — verify toLowercaseMap's reflection-based replacement
// produces a tree shaped correctly for text/template consumption and
// unwraps OrderedMap values into plain map[string]any.

func TestWalker_OrderedMapUnwrapped(t *testing.T) {
	t.Parallel()
	om := types.OrderedMap[int]{}
	om.Set("zeta", 3)
	om.Set("alpha", 1)
	om.Set("mu", 2)

	result := toLowercaseMap(om)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("walker result = %T, want map[string]any", result)
	}
	if len(m) != 3 {
		t.Errorf("len = %d, want 3", len(m))
	}
	if v, ok := m["zeta"].(int); !ok || v != 3 {
		t.Errorf("m[zeta] = %v (%T), want 3 (int)", m["zeta"], m["zeta"])
	}
	if v, ok := m["alpha"].(int); !ok || v != 1 {
		t.Errorf("m[alpha] = %v (%T), want 1 (int)", m["alpha"], m["alpha"])
	}
}

func TestWalker_NestedOrderedMap(t *testing.T) {
	t.Parallel()
	inner := types.OrderedMap[string]{}
	inner.Set("inner_z", "zz")
	inner.Set("inner_a", "aa")

	outer := types.OrderedMap[types.OrderedMap[string]]{}
	outer.Set("outer_b", inner)
	outer.Set("outer_a", types.OrderedMap[string]{})

	result := toLowercaseMap(outer)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("outer walker result = %T, want map[string]any", result)
	}
	// Nested should also be unwrapped to map[string]any (not left as OrderedMap).
	innerUnwrapped, ok := m["outer_b"].(map[string]any)
	if !ok {
		t.Fatalf("nested m[outer_b] = %T, want map[string]any (nested OrderedMap not unwrapped)", m["outer_b"])
	}
	if innerUnwrapped["inner_z"] != "zz" {
		t.Errorf("innerUnwrapped[inner_z] = %v, want zz", innerUnwrapped["inner_z"])
	}
}

func TestWalker_StructWithJSONTags(t *testing.T) {
	t.Parallel()
	type row struct {
		PublicField  string `json:"publicField"`
		Ignored      string `json:"-"`
		NoTag        string
		OmitEmpty    string `json:"omitEmpty,omitempty"`
		NonEmptyOmit string `json:"nonEmptyOmit,omitempty"`
	}
	in := row{
		PublicField:  "hello",
		Ignored:      "skip-me",
		NoTag:        "fallback",
		OmitEmpty:    "",
		NonEmptyOmit: "keep",
	}
	result := toLowercaseMap(in)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("walker result = %T, want map[string]any", result)
	}
	if m["publicField"] != "hello" {
		t.Errorf("publicField = %v, want hello", m["publicField"])
	}
	if _, present := m["ignored"]; present {
		t.Error("json:\"-\" field leaked into output")
	}
	if _, present := m["-"]; present {
		t.Error("literal '-' key leaked into output")
	}
	if m["notag"] != "fallback" {
		t.Errorf("notag (lowercased fallback) = %v, want fallback", m["notag"])
	}
	if _, present := m["omitEmpty"]; present {
		t.Error("omitempty zero-value field leaked into output")
	}
	if m["nonEmptyOmit"] != "keep" {
		t.Errorf("nonEmptyOmit = %v, want keep", m["nonEmptyOmit"])
	}
}

func TestWalker_SlicesPreserveOrder(t *testing.T) {
	t.Parallel()
	input := []string{"third", "first", "second"}
	result := toLowercaseMap(input)
	s, ok := result.([]any)
	if !ok {
		t.Fatalf("walker result = %T, want []any", result)
	}
	want := []string{"third", "first", "second"}
	for i, w := range want {
		if s[i] != w {
			t.Errorf("[%d] = %v, want %v", i, s[i], w)
		}
	}
}

func TestWalker_PlainMapAlphabetical(t *testing.T) {
	t.Parallel()
	// Plain Go map has no author order — the walker passes it through as
	// map[string]any with its underlying (hash-random) storage. Downstream,
	// text/template iterates maps alphabetically. Assertion here: the
	// walker produces a map[string]any shape, not a special ordered type.
	input := map[string]string{"z": "Z", "a": "A", "m": "M"}
	result := toLowercaseMap(input)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("walker result = %T, want map[string]any", result)
	}
	if len(m) != 3 {
		t.Errorf("len = %d, want 3", len(m))
	}
	if m["z"] != "Z" || m["a"] != "A" || m["m"] != "M" {
		t.Errorf("values not passed through: %+v", m)
	}
}

// Template-iteration alphabetical-stability — the  contract:
// after the walker unwraps OrderedMap, text/template range over the
// resulting map iterates in alphabetical order regardless of the
// OrderedMap's author insertion order. This is load-bearing and tested
// here as a regression guard.

func TestEvalTemplate_MapIterationIsAlphabetical(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[string]{}
	values.Set("zeta", "z")
	values.Set("alpha", "a")
	values.Set("mu", "m")

	data := map[string]any{"values": values}

	out, err := EvalTemplate("iter", `{{ range $k, $v := .values }}{{ $k }},{{ end }}`, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Text/template ranges maps alphabetically: alpha, mu, zeta.
	// The author-insertion order was zeta, alpha, mu — this test fails if
	// something (e.g. a Sprig helper, a walker regression) leaks
	// author-order into the template-iteration boundary.
	want := "alpha,mu,zeta,"
	if out != want {
		t.Errorf("template iteration leaked author-order:\ngot:  %q\nwant: %q", out, want)
	}
	// Sanity: the first iterated key must NOT be the author-first key.
	if strings.HasPrefix(out, "zeta,") {
		t.Errorf("template iteration started with author-first key — author-order leaked")
	}
}

// TestEvalTemplate_OrderedMapKeysPipeline demonstrates the recommended
// authoring pattern when a template author *does* need a deterministic
// non-alphabetical winner from a map: use Sprig keys + sortAlpha + first.
// This pattern is the one applied to k8s-dns-patcher.yaml to fix the
// last-write-wins correctness bug.
func TestEvalTemplate_OrderedMapKeysPipeline(t *testing.T) {
	t.Parallel()
	uses := types.OrderedMap[string]{}
	uses.Set("zeta", "Z-target")
	uses.Set("alpha", "A-target")
	uses.Set("mu", "M-target")

	data := map[string]any{"uses": uses}

	out, err := EvalTemplate(
		"winner",
		`{{- $k := keys .uses | sortAlpha | first -}}{{ index .uses $k }}`,
		data,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "A-target" {
		t.Errorf("sortAlpha|first winner = %q, want A-target", out)
	}
}

// TestEvalTemplate_DnsPatcherPattern exercises the exact template body
// shipped by the rewritten k8s-dns-patcher FeatureRecipe at
// testdata/example22/catalog/base/feature-recipes/k8s-dns-patcher.yaml.
// The body picks an alphabetically-first edge via `keys | sortAlpha | first`
// and pulls DNS host / namespace from that edge's annotations. Multiple
// edges in the input confirm the winner is the alphabetically-first (zeta)
// even though author-insertion order was zeta, alpha, mu.
func TestEvalTemplate_DnsPatcherPattern(t *testing.T) {
	t.Parallel()

	mkEdge := func(target, namespace string) map[string]any {
		return map[string]any{
			"target": target,
			"annotations": map[string]any{
				"release": map[string]any{
					"aggrega.ai/namespace": namespace,
				},
			},
		}
	}

	uses := types.OrderedMap[map[string]any]{}
	uses.Set("zeta", mkEdge("zeta-host", "zeta-ns"))
	uses.Set("alpha", mkEdge("alpha-host", "alpha-ns"))
	uses.Set("mu", mkEdge("mu-host", "mu-ns"))

	data := map[string]any{
		"infra":   map[string]any{"target_var": "BACKEND_DNS"},
		"context": map[string]any{"uses": uses},
	}

	body := `{{- $targetVar := .infra.target_var -}}
{{- $aliases := keys .context.uses | sortAlpha -}}
{{- $targetHost := "" -}}
{{- $namespace := "" -}}
{{- if $aliases -}}
  {{- $winner := first $aliases -}}
  {{- $edge := index .context.uses $winner -}}
  {{- $targetHost = $edge.target -}}
  {{- if and $edge.annotations $edge.annotations.release -}}
    {{- $ns := index $edge.annotations.release "aggrega.ai/namespace" -}}
    {{- if $ns -}}
      {{- $namespace = $ns -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- if and $targetHost $namespace -}}
{"env": {"{{ $targetVar }}": "{{ $targetHost }}.{{ $namespace }}.svc"}}
{{- else -}}
{}
{{- end -}}`

	// Run 10 iterations to catch any map-iteration-randomization leak
	// through the Sprig `keys` function (which reflects on the underlying
	// map — sortAlpha re-orders its output deterministically).
	for i := 0; i < 10; i++ {
		out, err := EvalTemplate("dns-patcher", body, data)
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		want := `{"env": {"BACKEND_DNS": "alpha-host.alpha-ns.svc"}}`
		if out != want {
			t.Errorf("iteration %d: winner not alphabetically-first:\ngot:  %s\nwant: %s", i, out, want)
		}
	}
}

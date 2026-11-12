// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TestEvalTemplate_Simple(t *testing.T) {
	t.Parallel()
	out, err := EvalTemplate("test", `hello {{ .Name }}`, map[string]string{"Name": "world"}, redact.Keys{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello world" {
		t.Errorf("got %q", out)
	}
}

func TestEvalTemplate_Sprig(t *testing.T) {
	t.Parallel()
	out, err := EvalTemplate("test", `{{ "hello" | upper }}`, nil, redact.Keys{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "HELLO" {
		t.Errorf("got %q", out)
	}
}

func TestEvalTemplate_MissingKeyZero(t *testing.T) {
	t.Parallel()
	// .Missing is a one-segment reference — it names a root rather than a key —
	// so it resolves to nil and the default pipe is what supplies the fallback.
	out, err := EvalTemplate("test", `value={{ default "" .Missing }}`, map[string]string{}, redact.Keys{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "value=" {
		t.Errorf("got %q, expected 'value='", out)
	}
}

func TestEvalTemplate_ParseError(t *testing.T) {
	t.Parallel()
	_, err := EvalTemplate("bad", `{{ .Invalid }{`, nil, redact.Keys{})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestEvalTemplate_JSON(t *testing.T) {
	t.Parallel()
	data := map[string]any{
		"Port": 8080,
		"Host": "localhost",
	}
	out, err := EvalTemplate("json", `{"port": {{ .Port }}, "host": "{{ .Host }}"}`, data, redact.Keys{})
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

// Template-iteration alphabetical-stability contract: after the walker
// unwraps OrderedMap, text/template range over the resulting map
// iterates in alphabetical order regardless of the OrderedMap's author
// insertion order. This is load-bearing and tested here as a regression
// guard.

func TestEvalTemplate_MapIterationIsAlphabetical(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[string]{}
	values.Set("zeta", "z")
	values.Set("alpha", "a")
	values.Set("mu", "m")

	data := map[string]any{"values": values}

	out, err := EvalTemplate("iter", `{{ range $k, $v := .values }}{{ $k }},{{ end }}`, data, redact.Keys{})
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
		data, redact.Keys{})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "A-target" {
		t.Errorf("sortAlpha|first winner = %q, want A-target", out)
	}
}

// TestTemplateContext_JSONBoundaryAuthorOrder constructs a
// TemplateContext whose .Values OrderedMap is populated via Set calls in
// deliberately non-alphabetical author order, serializes the entire
// TemplateContext via json.Marshal, and asserts the emitted JSON's
// `values` object key sequence matches author order.
//
// This is the JSON-boundary author-order contract: every downstream JSON
// consumer (IR emission, --trace output, Wasm guest payloads,
// diagnostics) reads keys in author order. Author order is preserved
// through OrderedMap.MarshalJSON; the construction-time Set sequence is
// what determines the emitted order.
//
// The test guards against future regressions where someone might switch
// OrderedMap.MarshalJSON to alphabetical-iteration in the name of "more
// canonical output," silently violating the author-order pillar for
// user-committable artifacts.
func TestTemplateContext_JSONBoundaryAuthorOrder(t *testing.T) {
	t.Parallel()

	values := types.OrderedMap[any]{}
	values.Set("zeta", "z-value")
	values.Set("alpha", "a-value")
	values.Set("mu", "m-value")

	infra := types.OrderedMap[any]{}
	infra.Set("zulu-region", "us-west-2")
	infra.Set("alpha-region", "us-east-1")

	ctx := &types.TemplateContext{
		Values: values,
		Infra:  infra,
	}

	jsonBytes, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("Marshal TemplateContext: %v", err)
	}

	gotValuesOrder := readObjectKeyOrder(t, jsonBytes, "values")
	wantValuesOrder := []string{"zeta", "alpha", "mu"}
	if !slicesEqual(gotValuesOrder, wantValuesOrder) {
		t.Errorf(".values key order in JSON = %v, want %v (author order lost)\nfull JSON:\n%s",
			gotValuesOrder, wantValuesOrder, jsonBytes)
	}

	gotInfraOrder := readObjectKeyOrder(t, jsonBytes, "infra")
	wantInfraOrder := []string{"zulu-region", "alpha-region"}
	if !slicesEqual(gotInfraOrder, wantInfraOrder) {
		t.Errorf(".infra key order in JSON = %v, want %v (author order lost)\nfull JSON:\n%s",
			gotInfraOrder, wantInfraOrder, jsonBytes)
	}
}

// readObjectKeyOrder walks JSON tokens and returns the keys of the object
// stored at the named top-level field, in source order. Used to verify
// JSON-boundary author-order preservation without losing order to
// json.Unmarshal-into-map collapsing.
func readObjectKeyOrder(t *testing.T, jsonBytes []byte, topLevelKey string) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(jsonBytes))

	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("opening token: %v", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		t.Fatalf("expected outer '{', got %v", tok)
	}

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			t.Fatalf("read top-level key: %v", err)
		}
		key := keyTok.(string)
		if key != topLevelKey {
			skipNextValue(t, dec)
			continue
		}
		if d, err := dec.Token(); err != nil {
			t.Fatalf("read inner opening: %v", err)
		} else if delim, ok := d.(json.Delim); !ok || delim != '{' {
			t.Fatalf("inner %q is not an object: %v", topLevelKey, d)
		}
		var keys []string
		for dec.More() {
			innerTok, err := dec.Token()
			if err != nil {
				t.Fatalf("read inner key: %v", err)
			}
			keys = append(keys, innerTok.(string))
			skipNextValue(t, dec)
		}
		return keys
	}
	t.Fatalf("top-level key %q not found in JSON: %s", topLevelKey, jsonBytes)
	return nil
}

func skipNextValue(t *testing.T, dec *json.Decoder) {
	t.Helper()
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("skipNextValue token: %v", err)
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return
	}
	depth := 1
	for depth > 0 {
		next, err := dec.Token()
		if err != nil {
			t.Fatalf("skipNextValue depth-loop token: %v", err)
		}
		if d, ok := next.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else if d == '}' || d == ']' {
				depth--
			}
		}
	}
	_ = delim
}

func slicesEqual(a, b []string) bool {
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

// TestEvalTemplate_DnsPatcherPattern exercises the exact template body
// shipped by the rewritten k8s-dns-patcher FeatureRecipe at
// testdata/fullstack-app/catalog/base/feature-recipes/k8s-dns-patcher.yaml.
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
		out, err := EvalTemplate("dns-patcher", body, data, redact.Keys{})
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		want := `{"env": {"BACKEND_DNS": "alpha-host.alpha-ns.svc"}}`
		if out != want {
			t.Errorf("iteration %d: winner not alphabetically-first:\ngot:  %s\nwant: %s", i, out, want)
		}
	}
}

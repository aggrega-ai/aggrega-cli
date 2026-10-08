// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// strictIndexSurface evaluates one template on one of the renderer's template
// kinds. data is the map the template reads as its open pillar: .values on a
// value template, an assertion and a self-inject value, .parameters on a path
// template, .locals on a provides entry.
type strictIndexSurface struct {
	name string
	// pillar is the dotted root the rows' templates read, spelled as the surface
	// delivers it.
	pillar string
	eval   func(t *testing.T, tmpl string, data map[string]any) (string, error)
	// conditionInChain is whether the surface's diagnostic carries the strict
	// index's condition for errors.Is. Every surface carries the key in its
	// text; the provides surface reports its own condition and carries the
	// evaluation's report, key included, in its message.
	conditionInChain bool
	// rendersText is false for the assertion surface, whose output is discarded:
	// a row there is held to evaluating without a halt rather than to its text.
	rendersText bool
}

func strictIndexSurfaces() []strictIndexSurface {
	return []strictIndexSurface{
		{
			name:   "value template",
			pillar: ".values",
			eval: func(t *testing.T, tmpl string, data map[string]any) (string, error) {
				return EvalTemplate("probe", tmpl, map[string]any{"values": data}, redact.Keys{})
			},
			conditionInChain: true,
			rendersText:      true,
		},
		{
			name:   "path template",
			pillar: ".parameters",
			eval: func(t *testing.T, tmpl string, data map[string]any) (string, error) {
				return EvalPathTemplate("probe", "directory", "some-boundary", tmpl, PathTemplateContext{Parameters: data})
			},
			conditionInChain: true,
			rendersText:      true,
		},
		{
			name:   "assertion",
			pillar: ".values",
			eval: func(t *testing.T, tmpl string, data map[string]any) (string, error) {
				return "", EvaluateAssertion("probe", tmpl, AssertionContext{Values: data}, redact.Keys{})
			},
			conditionInChain: true,
			rendersText:      false,
		},
		{
			name:   "self-inject value",
			pillar: ".values",
			eval: func(t *testing.T, tmpl string, data map[string]any) (string, error) {
				values := types.OrderedMap[any]{}
				for _, k := range []string{"region", "zones", "empty", "nothing"} {
					if v, ok := data[k]; ok {
						values.Set(k, v)
					}
				}
				return evalSelfInjectValue("self-inject.env[PROBE]", tmpl, SelfInjectContext{Values: values}, redact.Keys{})
			},
			conditionInChain: true,
			rendersText:      true,
		},
		{
			name:   "provides entry",
			pillar: ".locals",
			eval: func(t *testing.T, tmpl string, data map[string]any) (string, error) {
				nodes := types.OrderedMap[types.IRNode]{}
				nodes.Set("db", types.IRNode{})
				graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}
				locals := types.OrderedMap[string]{}
				for _, k := range []string{"region", "zones", "empty", "nothing"} {
					if v, ok := data[k]; ok {
						// A local is a template whose output is text, so a row's
						// value is written as the literal it renders to.
						locals.Set(k, localsLiteral(v))
					}
				}
				provides := types.OrderedMap[string]{}
				provides.Set("host", tmpl)
				recipes := map[string]*types.Recipe{
					"db": {Metadata: types.RecipeMetadata{Name: "probe-recipe"}, Spec: types.RecipeSpec{Locals: locals, Provides: provides}},
				}
				result, _, err := EvaluateAllProvides(graph, recipes, nil, refusalDeclarations{})
				if err != nil {
					return "", err
				}
				dbProvides := result["db"]
				got, _ := dbProvides.Get("host")
				return got, nil
			},
			conditionInChain: false,
			rendersText:      true,
		},
	}
}

// localsLiteral writes a row's value as the locals template that renders to it.
func localsLiteral(v any) string {
	switch s := v.(type) {
	case string:
		return s
	default:
		return ""
	}
}

// TestStrictIndex_EveryRendererTemplateKindHaltsOnAnAbsentMapKey drives each
// template kind the renderer evaluates through the one evaluator they share: a
// map key the map does not hold halts naming the key, and the reads the
// documentation teaches instead — a present key, a presence-tested read — render
// what they rendered before.
func TestStrictIndex_EveryRendererTemplateKindHaltsOnAnAbsentMapKey(t *testing.T) {
	t.Parallel()
	data := map[string]any{"region": "eu-west-1", "empty": ""}
	for _, s := range strictIndexSurfaces() {
		t.Run(s.name, func(t *testing.T) {
			p := s.pillar
			t.Run("absent key halts naming it", func(t *testing.T) {
				for _, tmpl := range []string{
					`{{ index ` + p + ` "nothing" }}`,
					`{{ index ` + p + ` "nothing" | default "fb" }}`,
					`{{ if index ` + p + ` "nothing" }}y{{ end }}`,
				} {
					out, err := s.eval(t, tmpl, data)
					if err == nil {
						t.Errorf("%s rendered %q; want a halt naming the key", tmpl, out)
						continue
					}
					if s.conditionInChain && !errors.Is(err, gotmpl.ErrIndexOfAbsentMapKey()) {
						t.Errorf("%s: the halt does not carry the strict index's condition: %v", tmpl, err)
					}
					if text := cerrors.CauseChain(err); !strings.Contains(text, `key "nothing"`) {
						t.Errorf("%s: no frame of the halt names the key: %s", tmpl, text)
					}
				}
			})
			t.Run("present key and guarded reads are unchanged", func(t *testing.T) {
				for _, row := range []struct{ tmpl, want string }{
					{`{{ index ` + p + ` "region" }}`, "eu-west-1"},
					{`[{{ index ` + p + ` "empty" }}]`, "[]"},
					{`{{ optional ` + p + ` "nothing" "fb" }}`, "fb"},
					{`[{{ get ` + p + ` "nothing" }}]`, "[]"},
					{`{{ if hasKey ` + p + ` "nothing" }}y{{ else }}n{{ end }}`, "n"},
					{`{{ if and (hasKey ` + p + ` "nothing") (index ` + p + ` "nothing") }}y{{ else }}n{{ end }}`, "n"},
				} {
					out, err := s.eval(t, row.tmpl, data)
					if err != nil {
						t.Errorf("%s halted: %v", row.tmpl, err)
						continue
					}
					if s.rendersText && strings.TrimSpace(out) != row.want {
						t.Errorf("%s rendered %q, want %q", row.tmpl, out, row.want)
					}
				}
			})
		})
	}
}

// TestStrictIndex_SlicesAndPresentNullsAnswerAsBefore covers the two cases the
// open-pillar rows above cannot carry on every kind: a position in a list, and a
// present key holding null, which still prints the engine's placeholder.
func TestStrictIndex_SlicesAndPresentNullsAnswerAsBefore(t *testing.T) {
	t.Parallel()
	data := map[string]any{
		"values": map[string]any{"zones": []any{"a", "b"}, "unset": nil},
	}
	for _, row := range []struct{ tmpl, want string }{
		{`{{ index .values.zones 1 }}`, "b"},
		{`{{ index .values "zones" 0 }}`, "a"},
		{`{{ index .values "unset" }}`, "<no value>"},
	} {
		out, err := EvalTemplate("probe", row.tmpl, data, redact.Keys{})
		if err != nil {
			t.Errorf("%s halted: %v", row.tmpl, err)
			continue
		}
		if out != row.want {
			t.Errorf("%s rendered %q, want %q", row.tmpl, out, row.want)
		}
	}
	if _, err := EvalTemplate("probe", `{{ index .values.zones 2 }}`, data, redact.Keys{}); err == nil ||
		errors.Is(err, gotmpl.ErrIndexOfAbsentMapKey()) || !strings.Contains(cerrors.CauseChain(err), "out of range") {
		t.Errorf("a position past a list's end must fail as the engine reports it, not as an absent map key: %v", err)
	}
}

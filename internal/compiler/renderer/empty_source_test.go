// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestPhaseB0AdmitsTheTwoDeclaredModes holds the source sub-pass to both
// authoring modes a Recipe's source entry selects between: an absent entry, and
// one naming a path or a structured source. An entry written and left empty is
// refused ahead of the node walk by the rule declared over the source entry, so
// this sub-pass is never the place that ambiguity is decided.
func TestPhaseB0AdmitsTheTwoDeclaredModes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		source any
	}{
		{"no source at all", nil},
		{"a real source", "./chart"},
		{"a structured source", map[string]any{"url": "oci://example.test/chart", "version": "1.0.0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := minimalRecipe("declared")
			r.Spec.Source = tc.source
			state := NewPhaseBState(r, "api", "default", "recipes/helm/declared.yaml")
			if err := phaseB0(minimalCtx(), state, types.OrderedMap[string]{}); err != nil {
				t.Fatalf("a declared source mode must not be refused: %v", err)
			}
		})
	}
}

// TestSourceValueDeclaredIsTheOneEmptinessDefinition covers the predicate every
// reader of the source-declared question now reaches, over every shape the source
// field can take. The structured row is the one worth stating: a container that
// exists names something, whatever its contents, because reading inside it is a
// different question from whether a source was declared.
func TestSourceValueDeclaredIsTheOneEmptinessDefinition(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		src   any
		want  bool
		alsoR bool // the same expectation through the Recipe-shaped reader
	}{
		{name: "absent", src: nil, want: false, alsoR: true},
		{name: "empty string", src: "", want: false, alsoR: true},
		{name: "real path", src: "./chart", want: true, alsoR: true},
		{name: "structured", src: map[string]any{"url": "oci://example.test/c"}, want: true, alsoR: true},
		{name: "structured and empty", src: map[string]any{}, want: true, alsoR: true},
		{name: "non-string scalar", src: 7, want: true, alsoR: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sourceValueDeclared(tc.src); got != tc.want {
				t.Errorf("sourceValueDeclared(%#v) = %v, want %v", tc.src, got, tc.want)
			}
			if !tc.alsoR {
				return
			}
			r := &types.Recipe{Spec: types.RecipeSpec{Source: tc.src}}
			if got := recipeSourceDeclared(r); got != tc.want {
				t.Errorf("recipeSourceDeclared with source %#v = %v, want %v", tc.src, got, tc.want)
			}
		})
	}
	if recipeSourceDeclared(nil) {
		t.Error("a nil Recipe carries no source")
	}
}

// TestResolveSourceForNodeTreatsAnEmptySourceAsNone is the reader-unification
// regression: source resolution used to test its subject for a nil source only,
// while the named predicate reads nil-or-empty, and the two disagreed on exactly
// the value the rule declared over the source entry refuses. This pins the
// agreement so the narrower reading cannot come back — an empty source is no
// source here, so nothing downstream is handed an empty string to treat as a path.
func TestResolveSourceForNodeTreatsAnEmptySourceAsNone(t *testing.T) {
	t.Parallel()
	nodeCtx := NodeRenderContext{}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "r", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Source: ""},
	}
	if err := resolveSourceForNode(RendererInput{}, recipe, "", &nodeCtx, sourceOrigin{}); err != nil {
		t.Fatalf("an empty source must resolve as sourceless rather than error: %v", err)
	}
	if nodeCtx.Source != nil {
		t.Errorf("Source must stay nil for an empty source; got %#v", nodeCtx.Source)
	}
	if nodeCtx.OriginalSource != nil {
		t.Errorf("OriginalSource must stay nil for an empty source; got %#v", nodeCtx.OriginalSource)
	}
}

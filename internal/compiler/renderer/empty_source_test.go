// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestPhaseB0RefusesAnAuthoredEmptySource pins the halt at the site that raises
// it: the first point in a node's render sequence holding the source as the
// author wrote it. The assertion is against the condition rather than against
// "any error", because this site raises several and an unrelated one passing a
// loose check would leave the ambiguity uncovered.
func TestPhaseB0RefusesAnAuthoredEmptySource(t *testing.T) {
	t.Parallel()
	r := minimalRecipe("empty-source")
	r.Spec.Source = ""
	state := NewPhaseBState(r, "api", "default", "recipes/helm/empty.yaml")

	err := phaseB0(minimalCtx(), state, types.OrderedMap[string]{})
	if err == nil {
		t.Fatal("an authored empty-string source must be refused")
	}
	if !errors.Is(err, ErrEmptySource()) {
		t.Fatalf("the refusal must carry the empty-source condition; got %v", err)
	}
	if code, ok := cerrors.ShortCodeOf(err); !ok || code != "E0251" {
		t.Fatalf("the refusal must carry the condition's short code; got %q (present=%v)", code, ok)
	}
	// The diagnostic has to separate the two modes the empty string is ambiguous
	// between, not merely report that a value is empty — an author told only that
	// the value is empty still cannot tell which of the two was expected.
	for _, want := range []string{"sourceless mode", "sourced mode", "omit the source field"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the diagnostic must name %q; got %v", want, err)
		}
	}
}

// TestPhaseB0AdmitsTheTwoDeclaredModes is the halt's boundary at its own raise
// site: the mode it disambiguates FROM must survive it, and so must a real
// source. Without both rows the refusal above is satisfied by a check that
// refuses everything.
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
// the value the halt above now refuses. This pins the agreement so the narrower
// reading cannot come back — an empty source is no source here, so nothing
// downstream is handed an empty string to treat as a path.
func TestResolveSourceForNodeTreatsAnEmptySourceAsNone(t *testing.T) {
	t.Parallel()
	nodeCtx := NodeRenderContext{}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "r", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Source: ""},
	}
	if err := resolveSourceForNode(RendererInput{}, recipe, &nodeCtx); err != nil {
		t.Fatalf("an empty source must resolve as sourceless rather than error: %v", err)
	}
	if nodeCtx.Source != nil {
		t.Errorf("Source must stay nil for an empty source; got %#v", nodeCtx.Source)
	}
	if nodeCtx.OriginalSource != nil {
		t.Errorf("OriginalSource must stay nil for an empty source; got %#v", nodeCtx.OriginalSource)
	}
}

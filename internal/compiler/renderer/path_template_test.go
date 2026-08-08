// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"
)

func pathTemplateFixture() PathTemplateContext {
	return PathTemplateContext{
		Capability:   "helm",
		BoundaryName: "web",
		ComponentID:  "db",
		Manifest:     PathTemplateManifest{RecipeName: "postgres-helm"},
		Parameters: map[string]any{
			"region": "eu-west-1",
			"list":   []any{map[string]any{"name": "alpha"}},
			"tls":    map[string]any{"ca": "ca.pem"},
		},
		ParentBoundary: map[string]any{"directory": "boundaries/cluster", "boundary_name": "cluster"},
	}
}

// A path template that reads a value inside a range or with body, or through a
// chained fallback, is legitimate authoring: the dot is rebound inside the body,
// and the trailing key of a chained expression hangs off that expression's value
// rather than off the context root. Neither is a root reference, so neither is the
// reference check's business — and each of these renders.
//
// These forms have no coverage before this test, which is the whole reason a
// reference check paired against the wrong walk direction could ship: it refused
// all three, and nothing exercised them.
func TestEvalPathTemplate_RangeWithAndChainedFormsRender(t *testing.T) {
	t.Parallel()
	data := pathTemplateFixture()
	cases := []struct {
		tmpl string
		want string
	}{
		{`{{ range .parameters.list }}{{ .name }}{{ end }}`, "alpha"},
		{`{{ (default dict .parameters.tls).ca }}`, "ca.pem"},
		{`{{ with .parameters }}{{ .region }}{{ end }}`, "eu-west-1"},
	}
	for _, tc := range cases {
		t.Run(tc.tmpl, func(t *testing.T) {
			for _, kind := range []string{"directory", "subDirectory"} {
				got, err := EvalPathTemplate("t", kind, "web", tc.tmpl, data)
				if err != nil {
					t.Fatalf("%s: form must render, got: %v", kind, err)
				}
				if got != tc.want {
					t.Errorf("%s: rendered %q, want %q", kind, got, tc.want)
				}
			}
		})
	}
}

// A boundary `directory:` template is evaluated once for the boundary, so
// admitting a per-component variable would make the emitted path depend on which
// component happened to be evaluated first. The reference is refused on that
// surface and admitted on the per-component one.
func TestEvalPathTemplate_ComponentIDRefusedOnDirectoryOnly(t *testing.T) {
	t.Parallel()
	data := pathTemplateFixture()
	_, err := EvalPathTemplate("directory:web", "directory", "web", `{{ .component_id }}`, data)
	if !errors.Is(err, ErrPathTemplateInvalidField()) {
		t.Fatalf("expected ErrPathTemplateInvalidField, got: %v", err)
	}
	if !strings.Contains(err.Error(), ".component_id") || !strings.Contains(err.Error(), `boundary "web"`) {
		t.Errorf("diagnostic must name the reference and the boundary: %v", err)
	}
	t.Logf("directory: diagnostic: %v", err)

	got, err := EvalPathTemplate("subDirectory:web", "subDirectory", "web", `{{ .component_id }}`, data)
	if err != nil {
		t.Fatalf("subDirectory: must admit .component_id, got: %v", err)
	}
	if got != "db" {
		t.Errorf("subDirectory: rendered %q, want db", got)
	}

	// An unknown contextKind selects the narrower surface, so a typo in the
	// dispatch cannot silently widen what a boundary directory may read.
	if _, err := EvalPathTemplate("d", "typo", "web", `{{ .component_id }}`, data); !errors.Is(err, ErrPathTemplateInvalidField()) {
		t.Errorf("an unrecognised contextKind must select the directory: surface, got: %v", err)
	}
}

// A root-level boundary has no parent, so .parent_boundary carries no keys. The
// reference must halt rather than render an empty path segment — an empty segment
// silently collapses the emitted layout and is only discovered after the build.
// The pillar stays an open map precisely so the halt names the KEY the author
// reached for.
func TestEvalPathTemplate_ParentBoundaryAtRootLevelHalts(t *testing.T) {
	t.Parallel()
	// The value the orchestrator actually supplies for a root-level boundary,
	// taken from the same helper rather than hand-built, so the fixture cannot
	// drift from the caller.
	parent := parentBoundaryData("cluster", map[string]any{}, map[string]string{}, nil)
	if len(parent) != 0 {
		t.Fatalf("a root-level boundary must contribute no parent keys, got %v", parent)
	}
	data := PathTemplateContext{
		BoundaryName:   "cluster",
		Parameters:     map[string]any{},
		ParentBoundary: parent,
	}
	out, err := EvalPathTemplate("directory:cluster", "directory", "cluster",
		`p/{{ .parent_boundary.directory }}`, data)
	if err == nil {
		t.Fatalf("expected a halt, rendered %q", out)
	}
	if !strings.Contains(err.Error(), "execute") {
		t.Errorf("the halt must come from execution under missingkey=error: %v", err)
	}
	t.Logf("root-level parent_boundary halt: %v", err)
	t.Logf("  unwrapped: %v", errors.Unwrap(err))

	// A nested boundary supplies the key, and the same template renders.
	got, err := EvalPathTemplate("directory:cluster/web", "directory", "cluster/web",
		`p/{{ .parent_boundary.directory }}`, pathTemplateFixture())
	if err != nil {
		t.Fatalf("a nested boundary must render .parent_boundary.directory: %v", err)
	}
	if got != "p/boundaries/cluster" {
		t.Errorf("rendered %q, want p/boundaries/cluster", got)
	}
}

// .manifest is a closed struct, so a mistyped key under it is refused rather than
// reaching the emitted path. The refusal is reported as the general
// unknown-reference halt, not as the path-template variable halt: the variable
// itself is admitted and only the key beneath it is wrong.
func TestEvalPathTemplate_MistypedKeyUnderClosedVariableHalts(t *testing.T) {
	t.Parallel()
	data := pathTemplateFixture()
	out, err := EvalPathTemplate("directory:web", "directory", "web", `{{ .manifest.recipe_nmae }}`, data)
	if err == nil {
		t.Fatalf("expected a halt, rendered %q", out)
	}
	if !errors.Is(err, ErrTemplateUnknownRoot()) {
		t.Errorf("expected ErrTemplateUnknownRoot, got: %v", err)
	}
	if !strings.Contains(err.Error(), ".manifest.recipe_name") {
		t.Errorf("diagnostic must offer the key the variable does define: %v", err)
	}
	t.Logf("diagnostic: %v", err)

	if got, err := EvalPathTemplate("d", "directory", "web", `{{ .manifest.recipe_name }}`, data); err != nil || got != "postgres-helm" {
		t.Errorf("the declared key must render: got %q err=%v", got, err)
	}
}

// The boundary-level walk leaves .capability and .manifest.recipe_name zero
// because no component is placed yet, and both render as empty segments. Neither
// field carries omitempty, so neither key vanishes — an omitted key would halt
// under missingkey=error and turn a rendered empty segment into a build failure.
func TestEvalPathTemplate_BoundaryWalkEmptyFieldsRenderRatherThanHalt(t *testing.T) {
	t.Parallel()
	data := PathTemplateContext{BoundaryName: "cluster", Parameters: map[string]any{}, ParentBoundary: map[string]any{}}
	for _, tc := range []struct{ tmpl, want string }{
		{`x-{{ .capability }}-y`, "x--y"},
		{`x-{{ .manifest.recipe_name }}-y`, "x--y"},
		{`{{ .boundary_name }}`, "cluster"},
	} {
		got, err := EvalPathTemplate("directory:cluster", "directory", "cluster", tc.tmpl, data)
		if err != nil {
			t.Errorf("%s must render, got: %v", tc.tmpl, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s rendered %q, want %q", tc.tmpl, got, tc.want)
		}
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// chainCategories returns the category of every *CompilerError frame in a
// diagnostic chain, outermost first. The formatter renders each frame it finds with
// that frame's own category tag, so the whole list is what an author reads — an
// assertion against the outermost frame alone would miss a mis-attributed one
// nested beneath it.
func chainCategories(err error) []cerrors.Category {
	var out []cerrors.Category
	for err != nil {
		if ce, ok := err.(*cerrors.CompilerError); ok {
			out = append(out, ce.Category())
		}
		err = stderrors.Unwrap(err)
	}
	return out
}

func selfContainmentValues(t *testing.T) types.OrderedMap[any] {
	t.Helper()
	values := types.OrderedMap[any]{}
	// One element carrying its own context.inject, so a reference to it from inside a
	// range body — where the dot is rebound to the element and the reference names a
	// field of that element, not a context root — has something to resolve to.
	values.Set("list", []any{map[string]any{"context": map[string]any{"inject": "SAFE"}}})
	return values
}

func localsRecipe(tmpl string) *types.Recipe {
	r := &types.Recipe{}
	r.Metadata.Name = "probe-recipe"
	r.Spec.Locals.Set("k", tmpl)
	return r
}

func localsFeatureRecipe(tmpl string) *types.FeatureRecipe {
	fr := &types.FeatureRecipe{}
	fr.Metadata.Name = "probe-fr"
	fr.Spec.Locals.Set("k", tmpl)
	return fr
}

func selfInjectValue(tmpl string) error {
	inject := types.InjectPipe{}
	si := &types.InjectBlock{Env: []any{map[string]any{"in": "BAD", "value": tmpl}}}
	err := ExecutePass2a(&inject, si, "api", types.OrderedMap[string]{}, SelfInjectContext{}, redact.Keys{})
	return err
}

// selfContainedSurfaces is every surface whose contract is "node-local data only".
// Each is exercised through its own public entry point rather than through the
// evaluator, so what is pinned is the halt an author actually reaches.
func selfContainedSurfaces(t *testing.T) []struct {
	name     string
	category cerrors.Category
	eval     func(string) error
} {
	t.Helper()
	values := selfContainmentValues(t)
	infra := types.OrderedMap[any]{}
	return []struct {
		name     string
		category cerrors.Category
		eval     func(string) error
	}{
		// Recipe and FeatureRecipe locals: and source: are catalog-authored, so a
		// diagnostic on them is the catalog author's to act on.
		{"Recipe locals:", cerrors.CatalogError, func(tmpl string) error {
			_, err := EvaluateRecipeLocals(localsRecipe(tmpl), "api", values, infra, redact.Keys{})
			return err
		}},
		{"FeatureRecipe locals:", cerrors.CatalogError, func(tmpl string) error {
			_, err := EvaluateFeatureRecipeLocals(localsFeatureRecipe(tmpl), "mon", "api", "", values, infra, resolver.SensitiveLeaves{})
			return err
		}},
		{"Recipe source:", cerrors.CatalogError, func(tmpl string) error {
			_, err := EvaluateRecipeSource(tmpl, "api", values, infra, types.OrderedMap[string]{}, redact.Keys{})
			return err
		}},
		// A component-level inject: block is authored on the Aggregate, so this one
		// is the developer's.
		{"component self-inject value:", cerrors.DeveloperError, selfInjectValue},
	}
}

// Every self-contained surface refuses cross-node data, and refuses it because its
// context type defines no cross-node pillar — not because anything scans the
// authored text for the two names. The halt therefore also covers a mistyped key
// under the same pillar, which is why there is one condition here rather than a
// separate one per forbidden name.
func TestSelfContainedSurfacesRefuseCrossNodeData(t *testing.T) {
	t.Parallel()
	for _, surface := range selfContainedSurfaces(t) {
		for _, tmpl := range []string{
			`{{ .context.inject.env.SECRET }}`,
			`{{ .context.uses.db.host }}`,
			`{{ .context.inject }}`,
			`{{ .context.uses }}`,
			// A fallback wrapper cannot launder an undefined key into an empty value.
			`{{ default "x" .context.inject.env.SECRET }}`,
			// Not cross-node but refused by the same mechanism, and for an ordering
			// reason: each of these surfaces is evaluated BEFORE the node's own
			// provides exist, so none of their context types defines the pillar.
			`{{ .provides.host }}`,
			`{{ default "x" .provides.host }}`,
		} {
			err := surface.eval(tmpl)
			if err == nil {
				t.Errorf("%s: %q must halt", surface.name, tmpl)
				continue
			}
			if !stderrors.Is(err, ErrTemplateUnknownRoot()) {
				t.Errorf("%s: %q must report ErrTemplateUnknownRoot, got: %v", surface.name, tmpl, err)
			}
			// Every frame is rendered to the author, so every frame has to name the
			// artifact actually at fault.
			for _, category := range chainCategories(err) {
				if category != surface.category {
					t.Errorf("%s: %q carries a %s frame; this surface's diagnostics are %s: %v",
						surface.name, tmpl, category, surface.category, err)
				}
			}
		}
	}
}

// The diagnostic names the reference the author wrote and the keys the pillar does
// define, so the fix is readable from the halt without opening the compiler.
func TestSelfContainedSurfaceDiagnosticNamesPathAndAvailableKeys(t *testing.T) {
	t.Parallel()
	for _, surface := range selfContainedSurfaces(t) {
		err := surface.eval(`{{ .context.uses.db.host }}`)
		if err == nil {
			t.Fatalf("%s: expected a halt", surface.name)
		}
		if !strings.Contains(err.Error()+diagnosticChainText(err), ".context.uses") {
			t.Errorf("%s: diagnostic does not name the authored path: %v", surface.name, err)
		}
		if !strings.Contains(diagnosticChainText(err), ".context.instance_id") {
			t.Errorf("%s: diagnostic does not offer the keys .context does define: %v", surface.name, err)
		}
		t.Logf("%s: %s", surface.name, diagnosticChainText(err))
	}
}

// diagnosticChainText joins every frame's message, which is the text the formatter
// lays out as the "caused by:" continuations an author reads.
func diagnosticChainText(err error) string {
	var parts []string
	for err != nil {
		if ce, ok := err.(*cerrors.CompilerError); ok {
			parts = append(parts, ce.Message())
		} else {
			parts = append(parts, err.Error())
		}
		err = stderrors.Unwrap(err)
	}
	return strings.Join(parts, " | ")
}

// The reference check reads the parse tree, so the two names appearing as TEXT
// somewhere the tree does not attribute to the context root is legitimate authoring
// and renders. Each of these forms is refused by any check that matches the authored
// text instead, which is why the check is defined over the tree the template is
// about to execute rather than over its source.
func TestSelfContainedSurfacesAdmitTheNamesWhereTheyAreNotReferences(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		// The text is inside a string literal the template prints.
		"string literal": `{{ printf "%s" ".context.inject" }}`,
		// The text is inside a comment, which is not part of the executed tree.
		"comment": `{{/* .context.inject */}}ok`,
		// A range body rebinds the dot, so .context.inject there names a field of the
		// element and reaches nothing in the render context at all.
		"rebound dot in a range body": `{{ range .values.list }}{{ .context.inject }}{{ end }}`,
	}
	for _, surface := range selfContainedSurfaces(t) {
		for name, tmpl := range cases {
			if err := surface.eval(tmpl); err != nil {
				t.Errorf("%s: %s form %q must render, got: %v", surface.name, name, tmpl, err)
			}
		}
	}
}

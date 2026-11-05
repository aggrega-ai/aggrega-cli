// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// The unconveyed-sensitivity advisory on an attachment, proved through RenderAll
// over the shipped terraform guest rather than over the reading in isolation.
//
// An attachment's sourced render whose FeatureRecipe maps its values writes keys
// its template invented, so a `values` leaf the attached Feature declares
// sensitive reaches the target with no per-key marking — the same shape the
// Recipe case reports, with the same lever. The renders here differ in exactly
// one authored fact each from the mapped component-scope case: whether the
// FeatureRecipe declares a values mapping, or whether the attachment sits on the
// boundary rather than on the component. So the advisory's presence in one and
// absence in another is attributable to that one fact.

package renderer_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	// The leaf the attached Feature declares sensitive, the rendered input the
	// FeatureRecipe's mapping writes it to, and the value authored for it. The
	// value is a planted stand-in, asserted to appear in no diagnostic's text.
	unconveyedFeatLeaf    = "token"
	unconveyedMappedInput = "token_arg"
	unconveyedPlanted     = "zq-planted-attachment-value"

	// The FeatureRecipe the attachment binds, which the advisory must name as the
	// entity whose mapping gates the join.
	unconveyedFRName = "aux-fr"

	// The host's one leaf, which nothing declares sensitive, so the host's own
	// render raises no advisory in any case and every advisory observed is the
	// attachment's.
	unconveyedHostLeaf = "bucket"
)

// unconveyedCase is one render: one host component and one attachment whose
// Feature declares a values leaf sensitive.
type unconveyedCase struct {
	// mapped selects whether the attachment's FeatureRecipe declares a values
	// mapping reading that leaf.
	mapped bool
	// boundaryScope selects an attachment declared on the host's boundary over
	// one declared on the component. A boundary-scope attachment renders against
	// an empty values pillar, so its mapping writes a literal there.
	boundaryScope bool
}

// attachmentDir is where this case's attachment places its files, relative to
// the boundary's output directory.
func (c unconveyedCase) attachmentDir() string {
	if c.boundaryScope {
		return "modules/" + declSensInstance
	}
	return "modules/" + declSensNode + "--" + declSensInstance
}

// renderUnconveyedAttachment drives RenderAll over one case and returns the
// build's structured advisories and emitted files.
func renderUnconveyedAttachment(t *testing.T, c unconveyedCase) ([]renderer.Diagnostic, map[string]renderer.AssembledFile) {
	t.Helper()

	hostModule := declSensModule(t, unconveyedHostLeaf)
	// The module declares the input each case writes: the mapped name where the
	// mapping invents it, the leaf path itself where no mapping runs.
	featInput := unconveyedFeatLeaf
	if c.mapped {
		featInput = unconveyedMappedInput
	}
	featModule := declSensModule(t, featInput)

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(declSensNode, types.IRNode{Component: declSensHostType, BoundaryPath: declSensBoundary})

	hostValues := types.OrderedMap[json.RawMessage]{}
	hostValues.Set(declSensNode, json.RawMessage(`{"`+unconveyedHostLeaf+`":"app-artifacts"}`))

	attachValues := types.OrderedMap[any]{}
	attachValues.Set(unconveyedFeatLeaf, unconveyedPlanted)
	inner := types.OrderedMap[types.IRFeatureEntry]{}
	inner.Set(declSensInstance, types.IRFeatureEntry{Type: declSensFeatType, Values: attachValues})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set(declSensNode, inner)

	clusterBoundary := types.Boundary{
		Directory:  declSensBoundaryDir,
		Components: types.OrderedMap[types.BoundaryComponent]{},
		Boundaries: types.OrderedMap[types.Boundary]{},
	}
	clusterBoundary.Components.Set(declSensNode, types.BoundaryComponent{})
	bfProj := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	if c.boundaryScope {
		boundaryFeatures := types.OrderedMap[types.BoundaryFeature]{}
		boundaryFeatures.Set(declSensInstance, types.BoundaryFeature{Type: declSensFeatType, Infra: types.OrderedMap[any]{}})
		clusterBoundary.Features = boundaryFeatures
		clusterBF := types.OrderedMap[types.IRBoundaryFeature]{}
		clusterBF.Set(declSensInstance, types.IRBoundaryFeature{Type: declSensFeatType, Infra: types.OrderedMap[any]{}})
		bfProj.Set(declSensBoundary, clusterBF)
	}
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set(declSensBoundary, clusterBoundary)

	hostSchema := "values:\n  " + unconveyedHostLeaf + ": { type: string }\n"
	featSchema := "values:\n  " + unconveyedFeatLeaf + ": { type: string, sensitive: true }\n"

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "unconveyed-attachment"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        hostValues,
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				BoundaryFeatures:     bfProj,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: ir.FeatureOrder{declSensNode: []string{declSensInstance}},
		CatalogLookup: resolver.CatalogLookup{
			Component: func(typeURI string) (types.Component, bool) {
				if typeURI == declSensHostType || typeURI == declSensHostType+":1.0.0" {
					return types.Component{
						Metadata: types.ComponentMetadata{Name: declSensHostType, Version: "1.0.0"},
						Spec:     types.ComponentSpec{Schema: declSensSchema(t, hostSchema)},
					}, true
				}
				return types.Component{}, false
			},
			Feature: func(typeURI string) (types.Feature, bool) {
				if strings.HasPrefix(typeURI, "example/aux") {
					return types.Feature{
						Metadata: types.FeatureMetadata{Name: "example/aux", Version: "1.0.0"},
						Spec:     types.FeatureSpec{Schema: declSensSchema(t, featSchema)},
					}, true
				}
				return types.Feature{}, false
			},
		},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "unconveyed-attachment"}},
			Release:   &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				declSensNode: {BoundaryPath: declSensBoundary, Directory: clusterBoundary.Directory},
			},
		},
	}

	componentRecipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Capability: "terraform",
			Implements: declSensHostType,
			Source:     hostModule,
		},
	}
	frRecipe := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: unconveyedFRName, Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability: "terraform",
			Implements: declSensFeatType,
			Source:     featModule,
		},
	}
	switch {
	case c.mapped && c.boundaryScope:
		// The boundary's values pillar is empty, so the mapping has no authored
		// value to read and writes a literal instead. What it reads is not what
		// decides the advisory: a mapping is declared, and that is the shape.
		frRecipe.Spec.ExecutionScope = "boundary"
		frRecipe.Spec.ValuesMapping = types.ValuesMapping{Inline: unconveyedMappedInput + `: "boundary-literal"` + "\n"}
	case c.boundaryScope:
		frRecipe.Spec.ExecutionScope = "boundary"
	case c.mapped:
		frRecipe.Spec.ValuesMapping = types.ValuesMapping{
			Inline: unconveyedMappedInput + `: "{{ .values.` + unconveyedFeatLeaf + ` }}"` + "\n",
		}
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if strings.HasPrefix(componentType, declSensHostType) {
				return componentRecipe, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if strings.HasPrefix(featureType, "example/aux") {
				return frRecipe, true
			}
			return nil, false
		},
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, wasmEngineFactory(t))
	if err != nil {
		t.Fatalf("RenderAll (%+v): %v", c, err)
	}
	return result.Diagnostics, result.Files
}

// unconveyedAdvisories is every advisory of the unconveyed-sensitivity class in
// a build's structured stream, matched by the class's exported code.
func unconveyedAdvisories(diags []renderer.Diagnostic) []renderer.Diagnostic {
	var out []renderer.Diagnostic
	for _, d := range diags {
		if d.Code == renderer.SensitivityNotConveyedCode {
			out = append(out, d)
		}
	}
	return out
}

// TestRenderAll_AttachmentSensitivityNotConveyed holds the three cases through
// one render path. A mapped component-scope attachment raises exactly one
// advisory, leading with the attachment's name, naming the Feature's
// declaration and the FeatureRecipe, located at the host node, and quoting no
// value. The same attachment with no mapping raises none, while its own
// declaration still marks its rendered input — which shows the declarations
// reached the attachment's render, so the silence is the predicate's answer
// rather than a render that read nothing. And a mapped attachment on the
// boundary raises none: its values pillar is empty by construction, so there is
// no lever for the advisory to name.
func TestRenderAll_AttachmentSensitivityNotConveyed(t *testing.T) {
	t.Parallel()

	t.Run("a component-scope FeatureRecipe mapping its values raises the advisory once", func(t *testing.T) {
		t.Parallel()
		c := unconveyedCase{mapped: true}
		diags, files := renderUnconveyedAttachment(t, c)
		if !declSensDirHoldsFiles(files, c.attachmentDir()) {
			t.Fatalf("the attachment emitted nothing, so no advisory about its render means anything; emitted paths: %v",
				sortedEmittedPaths(files))
		}
		got := unconveyedAdvisories(diags)
		if len(got) != 1 {
			t.Fatalf("want exactly one %q advisory for the attachment's one declared leaf, got %d: %+v",
				renderer.SensitivityNotConveyedCode, len(got), got)
		}
		d := got[0]
		want := renderer.FeatureRecipeSensitivityNotConveyedDiagnostic(declSensInstance, unconveyedFRName, "values."+unconveyedFeatLeaf)
		if d.Message != want.Message || d.SourceRef != want.SourceRef || d.Severity != want.Severity {
			t.Errorf("advisory is\n%+v\nwant the attachment-case advisory\n%+v", d, want)
		}
		if lead := `attachment "` + declSensInstance + `": `; !strings.HasPrefix(d.Message, lead) {
			t.Errorf("advisory must lead with the attachment's name %q; got %q", lead, d.Message)
		}
		if !strings.Contains(d.Message, `FeatureRecipe "`+unconveyedFRName+`"`) {
			t.Errorf("advisory must name the FeatureRecipe whose mapping gates the join; got %q", d.Message)
		}
		if d.Location == nil || d.Location.Instance != declSensNode {
			t.Errorf("advisory must be located at the host node %q; got location %+v", declSensNode, d.Location)
		}
		for _, other := range diags {
			if strings.Contains(other.Message, unconveyedPlanted) {
				t.Errorf("a diagnostic quotes the value declared sensitive: %q", other.Message)
			}
		}
	})

	t.Run("a component-scope FeatureRecipe mapping nothing raises no advisory", func(t *testing.T) {
		t.Parallel()
		c := unconveyedCase{}
		diags, files := renderUnconveyedAttachment(t, c)
		if !declSensDirHoldsFiles(files, c.attachmentDir()) {
			t.Fatalf("the attachment emitted nothing, so an absent advisory is an absent render; emitted paths: %v",
				sortedEmittedPaths(files))
		}
		// The positive control: with no mapping the attachment's keys ARE its
		// Feature's leaf paths, so the declaration marks the rendered input. A
		// render whose declarations never arrived would be silent here too.
		if got := declSensOverridesByDir(files)[c.attachmentDir()]; got != declSensOverrideDocumentFor(unconveyedFeatLeaf) {
			t.Fatalf("the attachment's own declaration does not mark its rendered input, so this control cannot tell a silent predicate from absent declarations; override document:\n%s", got)
		}
		if got := unconveyedAdvisories(diags); len(got) != 0 {
			t.Errorf("an attachment whose FeatureRecipe declares no values mapping conveys its declaration, yet %d advisories were raised: %+v",
				len(got), got)
		}
	})

	t.Run("a boundary-scope FeatureRecipe mapping its values raises no advisory", func(t *testing.T) {
		t.Parallel()
		c := unconveyedCase{mapped: true, boundaryScope: true}
		diags, files := renderUnconveyedAttachment(t, c)
		if !declSensDirHoldsFiles(files, c.attachmentDir()) {
			t.Fatalf("the boundary-scope attachment emitted nothing, so an absent advisory is an absent render; emitted paths: %v",
				sortedEmittedPaths(files))
		}
		if got := unconveyedAdvisories(diags); len(got) != 0 {
			t.Errorf("a boundary-scope attachment renders against an empty values pillar, so it has no lever to be told about, yet %d advisories were raised: %+v",
				len(got), got)
		}
	})
}

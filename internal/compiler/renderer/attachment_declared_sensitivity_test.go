// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// WHOSE declarations an attachment's rendered values are joined against, proved
// over emitted bytes rather than over a context field.
//
// A sensitivity declaration is authored on a Component, and the join that
// recovers a rendered key from it runs per render dispatch. An attachment renders
// through its own dispatch, and the target used here carries a sensitivity
// primitive of its own — so whether a declaration reached an attachment's output
// is readable in the emitted file set rather than inferable from the host's
// internal state. That is what makes this file's subject observable: the
// attachment's output either carries the override document or it does not.
//
// Both attachment scopes are driven, because each copies the host's render
// context at its own site and neither is a working reference for the other. They
// also reach the values join by different routes: the component-scope attachment
// declares no values mapping, so its rendered keys are its own authored leaves;
// the boundary-scope one declares a mapping, so its rendered keys are invented
// inside a template and no declaration names them at all.

package renderer_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	declSensBoundary = "cluster"
	declSensNode     = "api"
	declSensInstance = "aux"
	declSensHostType = "example/compute/api"
	declSensFeatType = "example/aux:1.0.0"

	// Where the boundary's output lands, and where this target's root
	// configuration for it sits. Every path read below is relative to the second.
	declSensBoundaryDir = "dist/cluster"
	declSensOutputDir   = declSensBoundaryDir + "/terraform"

	// The one leaf name the host Component and the attachment's render both use.
	// The collision is this file's subject: a name is all the two have in common,
	// and a name is not a declaration, so a join that marked the attachment's key
	// would be reading the host's declaration over the attachment's render.
	declSensCollidingLeaf = "secret_access_key"

	// The filename the guest appends beside a module's own files when it marks
	// one of that module's inputs. Written out here rather than read from the
	// guest-side package that owns it: that package sits under the plugin tree's
	// own internal directory, which this package may not import, so the name is
	// restated at the one site that reads the emitted output. The name is fixed
	// by the target — it merges a file whose name ends in `_override` into the
	// configuration in its own directory and loads any other name as a second
	// declaration — so it is part of the emitted contract rather than a spelling
	// either side is free to change.
	declSensOverrideFileName = "aggrega_override.tf.json"

	// A second leaf on each side that no declaration ever names, so every
	// assertion below has an unmarked sibling travelling the identical route. A
	// marking that reached everything would pass a collision-only assertion.
	declSensHostPublicLeaf = "bucket"
	declSensFeatPublicLeaf = "region"
)

// declSensOverrideDocument is the whole emitted override document for a render
// that marks the colliding leaf and nothing else — the bytes this target reads,
// pinned verbatim rather than parsed, because the subject is what lands on disk.
const declSensOverrideDocument = `{
  "variable": {
    "` + declSensCollidingLeaf + `": {
      "sensitive": true
    }
  }
}
`

// declSensCase is one render: one host component with one attachment, at the
// scope named, with the host's declaration present or absent.
type declSensCase struct {
	name string
	// boundaryScope selects an attachment declared on the boundary over one
	// declared on the component. The two copy the host's render context at
	// different sites, so a proof over one says nothing about the other.
	boundaryScope bool
	// hostDeclaresSensitive toggles the ONE declaration in the fixture. With it
	// off, nothing anywhere declares secret material, so nothing is recoverable
	// and no override document is emitted at all — which is what keeps the
	// absence assertions below from passing over a render that produced nothing.
	hostDeclaresSensitive bool
	// attachmentDir is where this scope places the attachment's own files,
	// relative to the boundary's output directory.
	attachmentDir string
}

func declSensCases() []declSensCase {
	return []declSensCase{
		{
			name:                  "a component-scope attachment, host declaration present",
			hostDeclaresSensitive: true,
			attachmentDir:         "modules/api--aux",
		},
		{
			name:                  "a boundary-scope attachment, host declaration present",
			boundaryScope:         true,
			hostDeclaresSensitive: true,
			attachmentDir:         "modules/aux",
		},
		{
			name:          "a component-scope attachment, no declaration anywhere",
			attachmentDir: "modules/api--aux",
		},
		{
			name:          "a boundary-scope attachment, no declaration anywhere",
			boundaryScope: true,
			attachmentDir: "modules/aux",
		},
	}
}

// declSensModule writes a module directory declaring one input variable per name,
// and returns its path. The bodies are what a module author wrote; nothing here
// declares the target's own sensitivity attribute, so an attribute observed on a
// rendered input came from the compiler rather than from these bytes.
func declSensModule(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	var body strings.Builder
	for _, n := range names {
		body.WriteString("variable \"" + n + "\" {\n  type = string\n}\n\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "variables.tf"), []byte(body.String()), 0o644); err != nil {
		t.Fatalf("write module variables: %v", err)
	}
	return dir
}

// declSensSchema parses a compact-form pillar schema for a fixture entity.
func declSensSchema(t *testing.T, body string) yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(body), &n); err != nil {
		t.Fatalf("schema fixture parse: %v", err)
	}
	return n
}

// renderDeclSensCase drives RenderAll over one case through the shipped guests
// and returns the emitted file map.
func renderDeclSensCase(t *testing.T, c declSensCase) map[string]renderer.AssembledFile {
	t.Helper()

	hostModule := declSensModule(t, declSensHostPublicLeaf, declSensCollidingLeaf)
	featModule := declSensModule(t, declSensFeatPublicLeaf, declSensCollidingLeaf)

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(declSensNode, types.IRNode{Component: declSensHostType, BoundaryPath: declSensBoundary})

	// The host's authored values. The Recipe declares no values mapping, so these
	// keys ARE the leaf paths the Component declared and the host's own join is
	// exact — which is what makes the host's override document the positive
	// control for the whole fixture.
	hostValues := types.OrderedMap[json.RawMessage]{}
	hostValues.Set(declSensNode, json.RawMessage(
		`{"`+declSensHostPublicLeaf+`":"app-artifacts","`+declSensCollidingLeaf+`":"host-placeholder"}`))

	// The attachment's own authored values, consumed on the component-scope route.
	attachValues := types.OrderedMap[any]{}
	attachValues.Set(declSensFeatPublicLeaf, "eu")
	attachValues.Set(declSensCollidingLeaf, "attachment-placeholder")
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

	// The ONE declaration this fixture moves. The public sibling beside it is
	// declared either way, so the two cases differ by exactly the sensitivity of
	// one leaf and by nothing else.
	hostSchema := "values:\n  " + declSensHostPublicLeaf + ": { type: string }\n  " + declSensCollidingLeaf + ": { type: string"
	if c.hostDeclaresSensitive {
		hostSchema += ", sensitive: true"
	}
	hostSchema += " }\n"

	// The attachment's own Feature declares the same two leaf names and declares
	// NEITHER sensitive, so nothing on the attachment's side of the fixture can
	// account for a marking on its output.
	featSchema := "values:\n  " + declSensFeatPublicLeaf + ": { type: string }\n  " + declSensCollidingLeaf + ": { type: string }\n"

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "attachment-declared-sensitivity"},
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
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "attachment-declared-sensitivity"}},
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
		Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability: "terraform",
			Implements: declSensFeatType,
			Source:     featModule,
		},
	}
	if c.boundaryScope {
		frRecipe.Spec.ExecutionScope = "boundary"
		// A boundary-scope attachment is rendered against a context whose values
		// pillar is empty by construction, so its rendered inputs can only come
		// from a values mapping — and a mapping's output keys are its template's
		// own invention, which no declaration names.
		frRecipe.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"` +
			declSensFeatPublicLeaf + `":"eu","` + declSensCollidingLeaf + `":"attachment-placeholder"}`}
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
		t.Fatalf("case %q: RenderAll: %v", c.name, err)
	}
	return result.Files
}

// declSensOverridesByDir is every emitted override document, keyed by the
// directory it landed in, relative to the boundary's output directory. The
// target merges an override only into the configuration in its own directory, so
// the directory is what decides whose rendered inputs a document marks.
func declSensOverridesByDir(files map[string]renderer.AssembledFile) map[string]string {
	out := map[string]string{}
	for path, f := range files {
		rel, ok := strings.CutPrefix(path, declSensOutputDir+"/")
		if !ok {
			continue
		}
		cut := strings.LastIndex(rel, "/")
		if cut < 0 || rel[cut+1:] != declSensOverrideFileName {
			continue
		}
		out[rel[:cut]] = string(f.Content)
	}
	return out
}

// declSensDirHoldsFiles reports whether the render emitted anything at all under
// one directory. It is the anti-vacuity read every absence assertion below is
// paired with: an absent override in a directory that holds no files is a render
// that did not happen, not a marking that did not reach.
func declSensDirHoldsFiles(files map[string]renderer.AssembledFile, dir string) bool {
	prefix := declSensOutputDir + "/" + dir + "/"
	for path := range files {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// TestAttachmentDeclaredSensitivity_HostDeclarationDoesNotReachTheAttachment is
// the proof. In one render, the host Component's declaration marks the host's own
// rendered input — the positive control, which is what establishes that the
// marking machinery is alive in this build — and does NOT mark the attachment's
// identically-named rendered input, whose own Feature declares nothing.
//
// The failure mode is the defect: an attachment's render inheriting the host's
// declarations marks a value nobody declared, and a consumer that starts
// withholding a value has no way to discover it should not have.
func TestAttachmentDeclaredSensitivity_HostDeclarationDoesNotReachTheAttachment(t *testing.T) {
	t.Parallel()
	for _, c := range declSensCases() {
		if !c.hostDeclaresSensitive {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			files := renderDeclSensCase(t, c)
			overrides := declSensOverridesByDir(files)

			// POSITIVE CONTROL, from this same render: the host's own rendered
			// input carries the marking, as the whole document rather than as a
			// substring, so a document that also named something else would fail.
			host, marked := overrides["modules/"+declSensNode]
			if !marked {
				t.Fatalf("case %q: the host's own rendered input is not marked, so this render proves nothing about whose declaration reaches where; emitted paths: %v",
					c.name, sortedEmittedPaths(files))
			}
			if host != declSensOverrideDocument {
				t.Errorf("case %q: the host's override document is\n%s\nwant\n%s", c.name, host, declSensOverrideDocument)
			}

			// The subject. The attachment rendered its own inputs, one of which
			// shares a name with the host's declared-sensitive leaf, and no
			// declaration on the attachment's side names it.
			if !declSensDirHoldsFiles(files, c.attachmentDir) {
				t.Fatalf("case %q: the attachment emitted nothing under %q, so an absent marking there is an absent render; emitted paths: %v",
					c.name, c.attachmentDir, sortedEmittedPaths(files))
			}
			if got, marked := overrides[c.attachmentDir]; marked {
				t.Errorf("case %q: the attachment's rendered input is marked sensitive from the HOST Component's declaration; its own Feature declares nothing sensitive, and a value nobody declared has been marked:\n%s",
					c.name, got)
			}
		})
	}
}

// TestAttachmentDeclaredSensitivity_ControlDeclaresNothingAnywhere moves the one
// declaration and nothing else. With no leaf declared sensitive on either side,
// nothing is recoverable and no override document is emitted at all — while both
// renders still emit their module files.
//
// Without it, the absence assertion above would pass over a render that emitted
// no override anywhere for reasons having nothing to do with whose declaration
// was read.
func TestAttachmentDeclaredSensitivity_ControlDeclaresNothingAnywhere(t *testing.T) {
	t.Parallel()
	for _, c := range declSensCases() {
		if c.hostDeclaresSensitive {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			files := renderDeclSensCase(t, c)

			for _, dir := range []string{"modules/" + declSensNode, c.attachmentDir} {
				if !declSensDirHoldsFiles(files, dir) {
					t.Fatalf("case %q: nothing was emitted under %q, so this control observes an empty render rather than an unmarked one; emitted paths: %v",
						c.name, dir, sortedEmittedPaths(files))
				}
			}
			if overrides := declSensOverridesByDir(files); len(overrides) != 0 {
				t.Errorf("case %q: with no leaf declared sensitive anywhere the render marks %v; a marking that survives the removal of every declaration is reading something other than a declaration",
					c.name, sortedSetKeys(declSensDirSet(overrides)))
			}
		})
	}
}

// declSensDirSet is one override map's directories as a set, for a failure
// message that names them in a stable order.
func declSensDirSet(overrides map[string]string) map[string]struct{} {
	out := map[string]struct{}{}
	for dir := range overrides {
		out[dir] = struct{}{}
	}
	return out
}

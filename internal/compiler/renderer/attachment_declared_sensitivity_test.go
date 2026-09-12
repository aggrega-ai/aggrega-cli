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
//
// Each side's declaration is toggled independently of the other, so the file
// covers a grid rather than two opposed cases: neither side declaring, each side
// alone, and both at once on one colliding name — which is the only cell where a
// join reading the wrong entity still marks something, and so the only cell where
// a wrong answer looks like a right one.

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

	// A leaf the host alone carries, which no declaration ever names, so every
	// host-side assertion has an unmarked sibling travelling the identical route.
	// A marking that reached everything would pass a collision-only assertion.
	declSensHostPublicLeaf = "bucket"

	// The leaf the attachment's Feature alone carries — the only leaf the Feature
	// ever declares sensitive. It is deliberately a name the host does not use, so
	// a marking observed on it can have come from nowhere but the Feature's own
	// declaration. Its unmarked sibling is the colliding leaf above, which the
	// Feature declares and never declares sensitive.
	declSensFeatOwnLeaf = "region"
)

// declSensOverrideDocumentFor is the whole emitted override document for a render
// that marks one named rendered input and nothing else — the bytes this target
// reads, pinned verbatim rather than parsed, because the subject is what lands on
// disk. Every expectation below marks at most one key, so no ordering between two
// marked keys is asserted anywhere.
func declSensOverrideDocumentFor(name string) string {
	return `{
  "variable": {
    "` + name + `": {
      "sensitive": true
    }
  }
}
`
}

// declSensCase is one render: one host component with one attachment, at the
// scope named, with each side's declaration present or absent.
//
// Three booleans, so eight cases — the whole grid rather than its margins. Each
// side's declaration is toggled independently because the two are exactly what
// the join must tell apart, and a case list that moved them together would leave
// the cell where both are present, which is the collision, unreached.
type declSensCase struct {
	name string
	// boundaryScope selects an attachment declared on the boundary over one
	// declared on the component. The two copy the host's render context at
	// different sites, so a proof over one says nothing about the other.
	boundaryScope bool
	// hostDeclaresSensitive toggles the HOST Component's declaration over the
	// colliding leaf. The attachment's Feature carries a leaf of that same name
	// and never declares it sensitive, so a marking on the attachment's copy of
	// that name can only have come from this declaration.
	hostDeclaresSensitive bool
	// featureDeclaresSensitive toggles the attachment Feature's OWN declaration,
	// over the leaf only the Feature carries. The host declares no leaf of that
	// name at all, so a marking on it can only have come from this declaration.
	featureDeclaresSensitive bool
	// attachmentDir is where this scope places the attachment's own files,
	// relative to the boundary's output directory.
	attachmentDir string
}

// attachmentMarks is the rendered input this case's attachment must carry a
// marking on, or "" where its output must carry no marking at all.
//
// A boundary-scope attachment renders against an empty values pillar, so its
// rendered inputs come from its own values mapping and their keys are that
// template's own invention — no declaration names them, on either side. Its
// answer is therefore "nothing marked" whatever the Feature declares, and that is
// a live guard on the gate over the leaf join rather than a case the per-Feature
// join flips.
func (c declSensCase) attachmentMarks() string {
	if c.featureDeclaresSensitive && !c.boundaryScope {
		return declSensFeatOwnLeaf
	}
	return ""
}

// hostMarks is the rendered input this case's HOST must carry a marking on, or ""
// where the host's output must carry no marking. It follows the host's own
// declaration and nothing else: the attachment's Feature is a different entity,
// and what it declares governs no key of the host's render.
func (c declSensCase) hostMarks() string {
	if c.hostDeclaresSensitive {
		return declSensCollidingLeaf
	}
	return ""
}

func declSensCases() []declSensCase {
	var cases []declSensCase
	for _, scope := range []struct {
		boundary bool
		name     string
		dir      string
	}{
		{name: "a component-scope attachment", dir: "modules/api--aux"},
		{boundary: true, name: "a boundary-scope attachment", dir: "modules/aux"},
	} {
		for _, decl := range []struct {
			host, feature bool
			name          string
		}{
			{name: "no declaration anywhere"},
			{host: true, name: "the host's declaration present"},
			{feature: true, name: "the attachment Feature's own declaration present"},
			{host: true, feature: true, name: "both declarations present, on one colliding name"},
		} {
			cases = append(cases, declSensCase{
				name:                     scope.name + ", " + decl.name,
				boundaryScope:            scope.boundary,
				hostDeclaresSensitive:    decl.host,
				featureDeclaresSensitive: decl.feature,
				attachmentDir:            scope.dir,
			})
		}
	}
	return cases
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
	featModule := declSensModule(t, declSensFeatOwnLeaf, declSensCollidingLeaf)

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
	attachValues.Set(declSensFeatOwnLeaf, "eu")
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

	// The host's declaration, over the colliding leaf. Its public sibling is
	// declared either way, so two cases differing on this flag differ by exactly
	// the sensitivity of one leaf and by nothing else.
	hostSchema := "values:\n  " + declSensHostPublicLeaf + ": { type: string }\n  " + declSensCollidingLeaf + ": { type: string"
	if c.hostDeclaresSensitive {
		hostSchema += ", sensitive: true"
	}
	hostSchema += " }\n"

	// The attachment Feature's own declaration, over the leaf only it carries. It
	// declares the colliding leaf too and NEVER declares that one sensitive, so the
	// colliding name is the unmarked sibling on this side: a marking observed on it
	// was read from the host's declaration over this Feature's render.
	featSchema := "values:\n  " + declSensFeatOwnLeaf + ": { type: string"
	if c.featureDeclaresSensitive {
		featSchema += ", sensitive: true"
	}
	featSchema += " }\n  " + declSensCollidingLeaf + ": { type: string }\n"

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
			declSensFeatOwnLeaf + `":"eu","` + declSensCollidingLeaf + `":"attachment-placeholder"}`}
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
		if !declSensHostOnly(c) {
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
			if want := declSensOverrideDocumentFor(c.hostMarks()); host != want {
				t.Errorf("case %q: the host's override document is\n%s\nwant\n%s", c.name, host, want)
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

// TestAttachmentDeclaredSensitivity_ControlDeclaresNothingAnywhere turns both
// declarations off and changes nothing else. With no leaf declared sensitive on
// either side, nothing is recoverable and no override document is emitted at all
// — while both renders still emit their module files.
//
// Without it, the absence assertions above would pass over a render that emitted
// no override anywhere for reasons having nothing to do with whose declaration
// was read.
func TestAttachmentDeclaredSensitivity_ControlDeclaresNothingAnywhere(t *testing.T) {
	t.Parallel()
	for _, c := range declSensCases() {
		if !declSensNeither(c) {
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

// TestAttachmentDeclaredSensitivity_TheAttachmentsOwnFeatureDeclarationReaches is
// the other direction, and it covers the two cells where the attachment Feature
// declares a leaf of its own sensitive.
//
// With the host's declaration ABSENT, the attachment's own declaration is the only
// one in the render, so a marking on its output can have come from nowhere else.
// With the host's declaration PRESENT on a name the Feature also carries, the
// collision is live: the attachment's output must mark what its own Feature
// declared and must not mark the colliding name, whose only declaration lives on
// the host.
//
// The failure mode is the everyday one: a value an author declared sensitive is
// not marked at all, because the declaration sits on the Feature and the join was
// reading the host.
func TestAttachmentDeclaredSensitivity_TheAttachmentsOwnFeatureDeclarationReaches(t *testing.T) {
	t.Parallel()
	for _, c := range declSensCases() {
		if !declSensFeatureDeclares(c) {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			files := renderDeclSensCase(t, c)
			overrides := declSensOverridesByDir(files)

			// ANTI-VACUITY, first: an absent marking under a directory that holds
			// no files is an absent render rather than an unmarked one.
			if !declSensDirHoldsFiles(files, c.attachmentDir) {
				t.Fatalf("case %q: the attachment emitted nothing under %q, so nothing here reads a marking; emitted paths: %v",
					c.name, c.attachmentDir, sortedEmittedPaths(files))
			}

			// The subject: whose declaration the attachment's render was joined
			// against, read as the whole emitted document rather than as a
			// substring — so a document naming the colliding leaf as well fails.
			got, marked := overrides[c.attachmentDir]
			switch want := c.attachmentMarks(); {
			case want == "" && marked:
				t.Errorf("case %q: the attachment's rendered inputs come from its own values mapping, whose keys no declaration names, yet its output is marked:\n%s",
					c.name, got)
			case want != "" && !marked:
				t.Errorf("case %q: the attachment's own Feature declares %q sensitive and its render carries no marking at all, so a value its author declared sensitive reaches the target unmarked; emitted paths: %v",
					c.name, want, sortedEmittedPaths(files))
			case want != "" && got != declSensOverrideDocumentFor(want):
				t.Errorf("case %q: the attachment's override document is\n%s\nwant\n%s\n— it must name the leaf this attachment's OWN Feature declared and no other",
					c.name, got, declSensOverrideDocumentFor(want))
			}

			// The host's own render in the same build, which the Feature's
			// declaration governs no key of. It is the discriminating control for
			// this direction: a join reading the wrong entity in the other
			// direction would mark the host from the Feature's declaration.
			host, hostMarked := overrides["modules/"+declSensNode]
			switch want := c.hostMarks(); {
			case want == "" && hostMarked:
				t.Errorf("case %q: the host Component declares nothing sensitive, yet its own rendered input is marked; the only declaration in this render belongs to the attachment's Feature:\n%s",
					c.name, host)
			case want != "" && host != declSensOverrideDocumentFor(want):
				t.Errorf("case %q: the host's override document is\n%s\nwant\n%s", c.name, host, declSensOverrideDocumentFor(want))
			}
		})
	}
}

// The three predicates that partition the case grid, named once and read by both
// the tests above and the coverage guard below. A case matching none of them
// would be rendered by nothing and asserted by nothing, in silence.
func declSensNeither(c declSensCase) bool {
	return !c.hostDeclaresSensitive && !c.featureDeclaresSensitive
}

func declSensHostOnly(c declSensCase) bool {
	return c.hostDeclaresSensitive && !c.featureDeclaresSensitive
}

func declSensFeatureDeclares(c declSensCase) bool { return c.featureDeclaresSensitive }

// TestAttachmentDeclaredSensitivity_EveryCellIsAsserted holds the grid closed.
//
// The subject is gated on three independent conditions, so the behaviour is a
// grid rather than a list, and a defect can sit in a cell no test reaches while
// every test above passes. Two things are checked, and neither is a count: that
// the case set IS the full grid, named as a set difference so a missing
// combination is reported rather than inferred from a total; and that each case
// is claimed by exactly one of the predicates above, so none is asserted twice
// and none goes unasserted.
func TestAttachmentDeclaredSensitivity_EveryCellIsAsserted(t *testing.T) {
	t.Parallel()

	type cell struct{ boundary, host, feature bool }
	want := map[cell]struct{}{}
	for _, boundary := range []bool{false, true} {
		for _, host := range []bool{false, true} {
			for _, feature := range []bool{false, true} {
				want[cell{boundary, host, feature}] = struct{}{}
			}
		}
	}

	got := map[cell]string{}
	for _, c := range declSensCases() {
		k := cell{c.boundaryScope, c.hostDeclaresSensitive, c.featureDeclaresSensitive}
		if prior, seen := got[k]; seen {
			t.Errorf("two cases occupy one cell %+v: %q and %q", k, prior, c.name)
		}
		got[k] = c.name

		claims := 0
		for _, claimed := range []bool{declSensNeither(c), declSensHostOnly(c), declSensFeatureDeclares(c)} {
			if claimed {
				claims++
			}
		}
		if claims != 1 {
			t.Errorf("case %q is claimed by %d of the three predicates; a case claimed by none is rendered by nothing and asserted by nothing", c.name, claims)
		}
	}

	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("no case covers the cell %+v, so that combination of declarations is never rendered", k)
		}
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

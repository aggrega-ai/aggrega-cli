// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// One key, one component, and the environment override a boundary-scope
// attachment computes for it.
//
// An attachment a boundary declares can deliver into the pipe of a component it
// reaches by a route no other attachment has: its rendering plugin computes, from
// the boundary's connections, an override of that component's environment, and
// the override is applied after every injectMapping has rendered. The rule over
// that route reads the same record of the component's deliveries, through the
// same walk, as the rules over an attachment's own block and its mapping — so
// whether an override is refused depends on the PAIR of deliveries meeting on a
// key, and this file walks the pairs with the override as the second claim:
//
//	first claim                              | override from a boundary attachment
//	-----------------------------------------+------------------------------------
//	the component's own block                | renders: interception
//	a connection's inject block              | renders: interception
//	another attachment's own block           | refused
//	another attachment's mapping             | refused
//	another boundary attachment's override   | refused
//
// Every row carries one fault at most, and the control row comes first: it
// delivers each surface's key once, the override's included, so a rule refusing
// anything at all on this fixture fails there before any refusal row can be read
// as proof.
//
// The rows go through the render entry point with the stub engine standing in for
// a provider whose plugin advertises the override, because the attribution is
// made where the overrides are merged and read where the node walk records them —
// a fixture over the predicate alone would prove neither.
//
// No graph enumeration is supplied, which leaves the two rules over an
// attachment's own block and its mapping uninvoked. That is deliberate: these rows
// measure the override reading alone, and the grid beside this file proves the
// other two over the same record. What the override rule needs in its place — the
// components each boundary attachment reaches — is answered by the presentation
// below.

// overrideRenderEngine is the stub engine that hands back one fixed override map,
// given what a test needs to read the pipe a component rendered against: it emits
// each artifact entry as the provider would, and as a provider that assembles its
// boundary's output it receives every node's files, which it keeps.
type overrideRenderEngine struct {
	envOverrideStubEngine
	captured []EmittedFile
}

// BoundaryAssembly marks the engine as one that folds its boundary's per-node
// output, so every node's files reach AssembleBoundary.
func (e *overrideRenderEngine) BoundaryAssembly() string { return "merge" }

// AssembleBoundary keeps every file it was handed and assembles nothing.
func (e *overrideRenderEngine) AssembleBoundary(_ context.Context, info BoundaryInfo) (map[string]AssembledFile, error) {
	for _, files := range info.IntermediatesByNode {
		e.captured = append(e.captured, files...)
	}
	return map[string]AssembledFile{}, nil
}

// ArtifactTemplateDelimiters are the host's default delimiters.
func (e *overrideRenderEngine) ArtifactTemplateDelimiters() (string, string) { return "{{", "}}" }

// EmitArtifacts places each evaluated entry at its name, marked intermediate
// from the engine's own boundary-assembly report.
func (e *overrideRenderEngine) EmitArtifacts(_ context.Context, proj ArtifactsProjection, _ NodeRenderContext) ([]EmittedFile, error) {
	intermediate := RenderIsIntermediate(e)
	files := make([]EmittedFile, 0, len(proj.Entries))
	for _, ent := range proj.Entries {
		origin := EmitOriginArtifactsInline
		if ent.Origin == "import" {
			origin = EmitOriginArtifactsImport
		}
		files = append(files, EmittedFile{Name: ent.Name, Origin: origin, Body: ent.Content, Intermediate: intermediate})
	}
	return files, nil
}

// overrideReachGraphFacts answers the one graph fact the override rule reads —
// which components each boundary attachment reaches. Every other fact is left to
// the nil presentation it embeds, so a read the rows do not expect panics rather
// than answering a zero value.
type overrideReachGraphFacts struct {
	constraint.GraphView
	reached map[string][]string
}

// BoundaryAttachmentReachedNodes answers from the reach the fixture declared.
func (f overrideReachGraphFacts) BoundaryAttachmentReachedNodes(boundary, attachment string) []string {
	return f.reached[boundary+"\x00"+attachment]
}

// overrideKeyAttachment is one attachment the "api" component carries from its
// own block, or — with slot set — from its connection "main".
type overrideKeyAttachment struct {
	name, slot string
	// blockKey, when set, is the env key the attachment's own inject block
	// delivers its published value under.
	blockKey string
	// mapMechanism and mapKey, when mapKey is set, are where the attachment's
	// FeatureRecipe mapping writes mapValue.
	mapMechanism, mapKey, mapValue string
}

// overrideKeyRow is one pair, or one of the extra rows beside the pairs.
type overrideKeyRow struct {
	name string
	// selfKey, when set, is an env key the component's own inject block delivers.
	selfKey string
	// connectionKey, when set, is an env key the connection "main" delivers from
	// its target's published host.
	connectionKey string
	attachments   []overrideKeyAttachment
	// overrideKey is the env key the boundary attachments' override names for "api".
	overrideKey string
	// secondBoundaryAttachment declares a second boundary-scope attachment, "gw2",
	// whose override names the same key.
	secondBoundaryAttachment bool
	// gatewayMapKey, when set, is a key the boundary attachment "gw" itself writes
	// through its own FeatureRecipe mapping.
	gatewayMapKey string

	// refused is the condition the build halts with, nil where it renders.
	refused error
	// locator is where that finding is positioned.
	locator string
	// detail is what the finding's message carries beyond the registered wording.
	detail []string
	// pipe is, where the build renders, lines the rendered pipe must carry.
	pipe []string
}

const (
	overrideValue   = "aggrega-gw:10000"
	overrideRelease = "release.yaml"
)

// overrideKeyInput builds the render for one row: an "api" and a "db" component in
// the boundary "cluster", "api" connected to "db" as "main", the boundary-scope
// attachment "gw" (and, where the row asks, "gw2") declared on "cluster" and
// reaching both, and the row's own attachments on "api" in the order the row lists
// them.
func overrideKeyInput(row overrideKeyRow) (RendererInput, RecipeLookup, *overrideRenderEngine) {
	const boundary = "cluster"
	boundaryAttachments := []string{"gw"}
	if row.secondBoundaryAttachment {
		boundaryAttachments = append(boundaryAttachments, "gw2")
	}

	nodes := types.OrderedMap[types.IRNode]{}
	api := types.IRNode{Component: "core/compute/api-server", ResolvedVersion: "1.0.0", BoundaryPath: boundary}
	if row.selfKey != "" {
		api.SelfInject = &types.InjectBlock{Env: map[string]any{row.selfKey: "from-component"}}
	}
	nodes.Set("api", api)
	nodes.Set("db", types.IRNode{Component: "core/datastore/db", ResolvedVersion: "1.0.0", BoundaryPath: boundary})

	connectionInject := types.OrderedMap[types.OrderedMap[string]]{}
	if row.connectionKey != "" {
		env := types.OrderedMap[string]{}
		env.Set(row.connectionKey, "host")
		connectionInject.Set("env", env)
	}
	edges := []types.IREdge{{Source: "api", Target: "db", Socket: "databases", Alias: "main", Inject: connectionInject}}

	// The boundary-scope attachments, cascaded onto every component the boundary
	// holds under the name the boundary declares them by — which is what the build
	// does, and the name an override is attributed to.
	boundaryEntry := func(name string) types.IRFeatureEntry {
		values := types.OrderedMap[any]{}
		values.Set("key", "")
		if name == "gw" {
			values.Set("key", row.gatewayMapKey)
		}
		return types.IRFeatureEntry{Type: "core/bridge:1.0.0", Values: values}
	}

	apiEntries := types.OrderedMap[types.IRFeatureEntry]{}
	var apiOrder []string
	for _, a := range row.attachments {
		values := types.OrderedMap[any]{}
		values.Set("secret", "from-"+a.name+"-block")
		values.Set("mechanism", a.mapMechanism)
		values.Set("key", a.mapKey)
		values.Set("value", a.mapValue)
		entry := types.IRFeatureEntry{Type: "core/credential", Values: values}
		if a.blockKey != "" {
			env := types.OrderedMap[string]{}
			env.Set(a.blockKey, "secret")
			entry.Inject.Set("env", env)
		}
		if a.slot != "" {
			entry.Connection = &types.AttachmentConnection{Socket: "databases", Alias: "main", Attachment: a.slot}
		}
		apiEntries.Set(a.name, entry)
		apiOrder = append(apiOrder, a.name)
	}
	dbEntries := types.OrderedMap[types.IRFeatureEntry]{}
	var dbOrder []string
	projection := types.OrderedMap[types.IRBoundaryFeature]{}
	declared := types.OrderedMap[types.BoundaryFeature]{}
	reached := map[string][]string{}
	for _, name := range boundaryAttachments {
		apiEntries.Set(name, boundaryEntry(name))
		apiOrder = append(apiOrder, name)
		dbEntries.Set(name, boundaryEntry(name))
		dbOrder = append(dbOrder, name)
		projection.Set(name, types.IRBoundaryFeature{Type: "core/bridge:1.0.0", Infra: types.OrderedMap[any]{}})
		declared.Set(name, types.BoundaryFeature{Type: "core/bridge:1.0.0", Infra: types.OrderedMap[any]{}})
		reached[boundary+"\x00"+name] = []string{"api", "db"}
	}
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("api", apiEntries)
	fvm.Set("db", dbEntries)
	boundaryFeatures := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	boundaryFeatures.Set(boundary, projection)

	components := types.OrderedMap[types.BoundaryComponent]{}
	components.Set("api", types.BoundaryComponent{})
	components.Set("db", types.BoundaryComponent{})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set(boundary, types.Boundary{
		Directory:  "dist/cluster",
		Features:   declared,
		Components: components,
		Boundaries: types.OrderedMap[types.Boundary]{},
	})

	input := RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "override-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				Edges:                edges,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: fvm,
				BoundaryFeatures:     boundaryFeatures,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: ir.FeatureOrder{"api": apiOrder, "db": dbOrder},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:   &types.Aggregate{Metadata: types.AggregateMetadata{Name: "override-app"}},
			Release:     &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			ReleasePath: overrideRelease,
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				"api": {BoundaryPath: boundary, Directory: "dist/cluster"},
				"db":  {BoundaryPath: boundary, Directory: "dist/cluster"},
			},
		},
		GraphView: overrideReachGraphFacts{reached: reached},
	}

	lookup := RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			artifacts := types.OrderedMap[types.ArtifactEntry]{}
			switch componentType {
			case "core/compute/api-server":
				// The rendered pipe, one line per delivery, so a row can read which
				// value a key ended up carrying.
				artifacts.Set("api-pipe.txt", types.ArtifactEntry{Inline: "" +
					"{{ range $k, $v := .context.inject.env }}env {{ $k }}={{ $v }}\n{{ end }}" +
					"{{ range $k, $v := .context.inject.file }}file {{ $k }}={{ $v }}\n{{ end }}"})
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
					Spec: types.RecipeSpec{
						Capability: "compose", Implements: componentType, Artifacts: artifacts,
					},
				}, true
			case "core/datastore/db":
				provides := types.OrderedMap[string]{}
				provides.Set("host", "db-host")
				artifacts.Set("db.txt", types.ArtifactEntry{Inline: "db\n"})
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "db-recipe", Version: "1.0.0"},
					Spec: types.RecipeSpec{
						Capability: "compose", Implements: componentType, Provides: provides, Artifacts: artifacts,
					},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			switch featureType {
			case "core/credential":
				provides := types.OrderedMap[string]{}
				provides.Set("secret", "{{ .values.secret }}")
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "credential-recipe"},
					Spec: types.FeatureRecipeSpec{
						Capability: "compose",
						Implements: featureType,
						Provides:   provides,
						InjectMapping: `{{ if .values.key }}` +
							`{"{{ .values.mechanism }}": {"{{ .values.key }}": "{{ .values.value }}"}}` +
							`{{ end }}`,
					},
				}, true
			case "core/bridge:1.0.0":
				// Generates nothing of its own, so the render never dispatches it;
				// what it contributes is the override its plugin computes and,
				// where the row gives it a key, a mapping of its own.
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "bridge-recipe"},
					Spec: types.FeatureRecipeSpec{
						Capability:     "compose",
						Implements:     featureType,
						ExecutionScope: executionScopeBoundary,
						InjectMapping:  `{{ if .values.key }}{"env": {"{{ .values.key }}": "from-gw-mapping"}}{{ end }}`,
					},
				}, true
			}
			return nil, false
		},
	}

	engine := &overrideRenderEngine{envOverrideStubEngine: envOverrideStubEngine{
		overrides: map[string]map[string]string{"api": {row.overrideKey: overrideValue}},
	}}
	return input, lookup, engine
}

// blockAttachment is an attachment whose own inject block delivers key.
func blockAttachment(name, key string) overrideKeyAttachment {
	return overrideKeyAttachment{name: name, blockKey: key}
}

// mappingAttachment is an attachment whose mapping writes value under key, in env.
func mappingAttachment(name, key, value string) overrideKeyAttachment {
	return overrideKeyAttachment{name: name, mapMechanism: "env", mapKey: key, mapValue: value}
}

// TestRenderAll_BoundaryEnvOverrideOnAKeyAnotherAttachmentDelivered walks the
// pairs and the rows beside them.
func TestRenderAll_BoundaryEnvOverrideOnAKeyAnotherAttachmentDelivered(t *testing.T) {
	t.Parallel()
	const key = "API_KEY"
	rule := ErrBoundaryEnvOverrideKeyCollision()
	gwEntry := ".spec.boundaries.cluster.features.gw"

	rows := []overrideKeyRow{
		{
			name:          "CONTROL: every surface, the override included, delivers a key of its own",
			selfKey:       "API_SELF",
			connectionKey: "API_CONN",
			attachments: []overrideKeyAttachment{
				blockAttachment("alpha", "API_BLOCK"),
				mappingAttachment("beta", "API_MAP", "from-beta-mapping"),
			},
			overrideKey: "API_GW",
			pipe: []string{
				"env API_SELF=from-component",
				"env API_CONN=db-host",
				"env API_BLOCK=from-alpha-block",
				"env API_MAP=from-beta-mapping",
				"env API_GW=" + overrideValue,
			},
		},
		{
			name:        "PAIR (component's own block, override): interception renders",
			selfKey:     key,
			overrideKey: key,
			pipe:        []string{"env API_KEY=" + overrideValue},
		},
		{
			name:          "PAIR (connection's inject block, override): interception renders",
			connectionKey: key,
			overrideKey:   key,
			pipe:          []string{"env API_KEY=" + overrideValue},
		},
		{
			name:        "PAIR (another attachment's own block, override): refused",
			attachments: []overrideKeyAttachment{blockAttachment("alpha", key)},
			overrideKey: key,
			refused:     rule,
			locator:     gwEntry,
			detail: []string{`"API_KEY"`, `mechanism "env"`, `attachment "gw"`, `on component "api"`,
				`delivered first by attachment "alpha"`},
		},
		{
			name:        "PAIR (another attachment's mapping, override): refused",
			attachments: []overrideKeyAttachment{mappingAttachment("alpha", key, "from-alpha-mapping")},
			overrideKey: key,
			refused:     rule,
			locator:     gwEntry,
			detail: []string{`"API_KEY"`, `mechanism "env"`, `attachment "gw"`, `on component "api"`,
				`delivered first by attachment "alpha"`},
		},
		{
			name:                     "PAIR (another boundary attachment's override, override): refused at the second",
			overrideKey:              key,
			secondBoundaryAttachment: true,
			refused:                  rule,
			locator:                  ".spec.boundaries.cluster.features.gw2",
			detail: []string{`"API_KEY"`, `mechanism "env"`, `attachment "gw2"`, `on component "api"`,
				`delivered first by attachment "gw"`},
		},
		{
			name: "EXTRA: one key under two mechanisms is two keyed spaces and renders",
			attachments: []overrideKeyAttachment{
				{name: "alpha", mapMechanism: "file", mapKey: key, mapValue: "from-alpha-mapping"},
			},
			overrideKey: key,
			pipe:        []string{"env API_KEY=" + overrideValue, "file API_KEY=from-alpha-mapping"},
		},
		{
			name: "EXTRA: a connection's attachment is named as the component carries it",
			attachments: []overrideKeyAttachment{
				{name: "main.credential", slot: "credential", mapMechanism: "env", mapKey: key, mapValue: "from-connection"},
			},
			overrideKey: key,
			refused:     rule,
			locator:     gwEntry,
			detail:      []string{`"API_KEY"`, `attachment "gw"`, `delivered first by attachment "main.credential"`},
		},
		{
			name:          "EXTRA: an override rewriting its own attachment's mapping displaces nobody and renders",
			gatewayMapKey: key,
			overrideKey:   key,
			pipe:          []string{"env API_KEY=" + overrideValue},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			input, lookup, engine := overrideKeyInput(row)
			factory := func(string) (Provider, error) { return engine, nil }

			_, err := RenderAll(context.Background(), input, lookup, factory)
			if row.refused == nil {
				if err != nil {
					t.Fatalf("the build must render; got %v", err)
				}
				pipe := overrideCapturedFile(t, engine, "api-pipe.txt")
				for _, line := range row.pipe {
					if !strings.Contains(pipe, line+"\n") {
						t.Errorf("the rendered pipe must carry %q; got:\n%s", line, pipe)
					}
				}
				return
			}
			if err == nil {
				t.Fatalf("the build rendered; it must halt with %v", row.refused)
			}
			if !errors.Is(err, row.refused) {
				t.Fatalf("the halt must carry %v; got %v", row.refused, err)
			}
			for _, other := range []error{ErrAttachmentInjectKeyCollision(), ErrAttachmentInjectMappingKeyCollision()} {
				if errors.Is(err, other) {
					t.Fatalf("the halt must carry one condition, not also %v; got %v", other, err)
				}
			}
			var ce *cerrors.CompilerError
			if !errors.As(err, &ce) {
				t.Fatalf("the halt is not a compiler diagnostic: %v", err)
			}
			if ce.Category() != cerrors.OpsError {
				t.Errorf("category = %s, want %s", ce.Category(), cerrors.OpsError)
			}
			if ce.Source().File != overrideRelease {
				t.Errorf("source file = %q, want the Release the boundary attachment was declared in", ce.Source().File)
			}
			if ce.Source().Locator != row.locator {
				t.Errorf("locator = %q, want %q", ce.Source().Locator, row.locator)
			}
			for _, part := range row.detail {
				if !strings.Contains(ce.Message(), part) {
					t.Errorf("the finding must carry %s; got %q", part, ce.Message())
				}
			}
		})
	}
}

// overrideCapturedFile returns the body of the one captured file whose name ends
// in suffix, failing the test where there is not exactly one.
func overrideCapturedFile(t *testing.T, engine *overrideRenderEngine, suffix string) string {
	t.Helper()
	var found []string
	for _, f := range engine.captured {
		if strings.HasSuffix(f.Name, suffix) {
			found = append(found, string(f.Body))
		}
	}
	if len(found) != 1 {
		names := make([]string, 0, len(engine.captured))
		for _, f := range engine.captured {
			names = append(names, f.Name)
		}
		t.Fatalf("want exactly one captured %s, got %d among %s", suffix, len(found), fmt.Sprint(names))
	}
	return found[0]
}

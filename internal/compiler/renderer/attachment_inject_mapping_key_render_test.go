// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// One key, one component, every pairing of two deliveries.
//
// A component's delivery pipe is written by five things: its own inject block,
// the inject block on each of its connections, and each attachment's own inject
// block and FeatureRecipe mapping. Two rules hold that space, both declared over
// one record and both reading it through one walk: the one over an attachment's
// own block, and the one over an attachment's mapping. Which of them fires — or
// whether neither does — depends on the PAIR of deliveries meeting on a key, so
// the behaviour is a grid and this file walks it as one, through the render entry
// point, because the recording, the presentation and the axes are three things the
// render has to have in hand at the seam and a fixture over the predicate alone
// proves none of them.
//
// The grid is first claim × second claim:
//
//	first \ second              | an attachment's block | an attachment's mapping
//	----------------------------+-----------------------+------------------------
//	the component's own block   | refused, block rule   | renders: interception
//	a connection's inject block | refused, block rule   | renders: interception
//	another attachment's block  | refused, block rule   | refused, mapping rule
//	another attachment's mapping| IMPOSSIBLE            | refused, mapping rule
//
// The impossible cell is impossible by construction rather than by authoring:
// every attachment's block delivers before any mapping renders, so a mapping can
// never be the FIRST claim against a block. It is not omitted. Its row authors the
// pair in the order that cell names — the mapping's attachment attached first —
// and pins what the build does with it, which is the cell below it in the other
// column: the block holds the key and the mapping is refused.
//
// Every row carries one fault at most, and the control row comes first: it
// delivers each surface's key once, so a rule refusing anything at all on this
// fixture fails there before any refusal row can be read as proof.

// pipeKeyCoordinates stands in for the enumeration the finalised graph carries,
// including the site an attachment a connection carries was written at — which is
// what the production enumeration supplies and what a finding about such an
// attachment is positioned from.
type pipeKeyCoordinates struct {
	nodes       []constraint.NodeInstance
	attachments map[string][]string
	sites       map[string]constraint.AttachmentSite
}

func (c pipeKeyCoordinates) Nodes() []constraint.NodeInstance { return c.nodes }

func (c pipeKeyCoordinates) Attachments(node string) []string { return c.attachments[node] }

// Edges answers empty: the rule over a connection's two providers is not what
// these rows measure, and the connection the fixture draws is between two nodes
// rendered by one provider.
func (c pipeKeyCoordinates) Edges() []constraint.EdgeInstance { return nil }

func (c pipeKeyCoordinates) AttachmentSite(
	node, attachment string,
) (constraint.AttachmentSite, bool) {
	if node != "api" {
		return constraint.AttachmentSite{}, false
	}
	site, ok := c.sites[attachment]
	return site, ok
}

// pipeKeyAttachment is one attachment on the "api" node.
type pipeKeyAttachment struct {
	// name is the attachment's name on the component.
	name string
	// slot, when set, makes this an attachment the connection "main" carries in
	// that slot; name is then the name the build composes for it.
	slot string
	// blockKey, when set, is the env key the attachment's own inject block
	// delivers its published value under.
	blockKey string
	// mapMechanism and mapKey, when mapKey is set, are where the attachment's
	// FeatureRecipe mapping writes mapValue. An empty mapKey renders the mapping to
	// nothing at all.
	mapMechanism, mapKey, mapValue string
	// releaseBoundary, when set, makes this an attachment the Release wrote, on
	// its entry for the component inside that boundary.
	releaseBoundary string
}

// pipeKeyRow is one cell of the grid, or one of the extra rows beside it.
type pipeKeyRow struct {
	name string
	// selfKey, when set, is an env key the component's own inject block delivers.
	selfKey string
	// connectionKey, when set, is an env key the connection "main" delivers from
	// its target's published host.
	connectionKey string
	attachments   []pipeKeyAttachment

	// refused is the condition the build halts with, nil where it renders.
	refused error
	// locator is where that finding is positioned.
	locator string
	// operator reports that the finding is positioned at an attachment the
	// Release wrote, so it names the Release's file and is filed under the
	// operator; every other finding names the Aggregate and the developer.
	operator bool
	// detail is what the finding's message carries beyond the registered wording.
	detail []string
	// pipe is, where the build renders, lines the rendered pipe must carry.
	pipe []string
}

// pipeKeyInput builds the render for one row: an "api" node and the "db" node its
// connection "main" points at, both rendered by one capturing provider, and the
// row's attachments in the order the row lists them.
//
// Every attachment binds one Feature, whose FeatureRecipe publishes the
// attachment's own value and writes, through its mapping, wherever the
// attachment's values point it. One Feature for every attachment is deliberate:
// what separates two attachments here is how each delivers, never which recipe it
// binds, so the rows vary the authoring and nothing else.
func pipeKeyInput(row pipeKeyRow) (renderer.RendererInput, renderer.RecipeLookup) {
	input := testInput()

	api, _ := input.Graph.Spec.Nodes.Get("api")
	if row.selfKey != "" {
		api.SelfInject = &types.InjectBlock{Env: []any{map[string]any{"in": row.selfKey, "value": "from-component"}}}
	}
	input.Graph.Spec.Nodes.Set("api", api)
	input.Graph.Spec.Nodes.Set("db", types.IRNode{
		Component: "core/datastore/db", ResolvedVersion: "1.0.0", BoundaryPath: "local/default",
	})

	connectionInject := types.OrderedMap[types.OrderedMap[string]]{}
	if row.connectionKey != "" {
		env := types.OrderedMap[string]{}
		env.Set(row.connectionKey, "host")
		connectionInject.Set("env", env)
	}
	input.Graph.Spec.Edges = []types.IREdge{{
		Source: "api", Target: "db", Socket: "databases", Alias: "main", Inject: connectionInject,
	}}

	inner := types.OrderedMap[types.IRFeatureEntry]{}
	order := make([]string, 0, len(row.attachments))
	sites := map[string]constraint.AttachmentSite{}
	apiInstance := constraint.NodeInstance{ID: "api", AuthoredName: "api", SourceFile: "aggregate.yaml"}
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
			sites[a.name] = constraint.ConnectionAttachmentSiteInAggregate(apiInstance, "databases", "main", a.slot)
		}
		if a.releaseBoundary != "" {
			sites[a.name] = constraint.AttachmentSiteInBlock(constraint.AuthoringDocumentRelease,
				constraint.AuthoredSite{ScopePath: a.releaseBoundary, Component: "api"}, a.name, "release.yaml")
		}
		inner.Set(a.name, entry)
		order = append(order, a.name)
	}
	outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	outer.Set("api", inner)
	input.Graph.Spec.FeatureValuesMapping = outer
	input.FeatureOrder = map[string][]string{"api": order}

	input.GraphView = injectRenderGraphFacts{}
	input.GraphCoordinates = pipeKeyCoordinates{
		nodes: []constraint.NodeInstance{
			apiInstance,
			{ID: "db", AuthoredName: "db", SourceFile: "aggregate.yaml"},
		},
		attachments: map[string][]string{"api": order},
		sites:       sites,
	}

	lookup := renderer.RecipeLookup{
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
			if featureType != "core/credential" {
				return nil, false
			}
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
		},
	}
	return input, lookup
}

// mapped is an attachment whose mapping writes value under key, in env.
func mapped(name, key, value string) pipeKeyAttachment {
	return pipeKeyAttachment{name: name, mapMechanism: "env", mapKey: key, mapValue: value}
}

// blocked is an attachment whose own inject block delivers key.
func blocked(name, key string) pipeKeyAttachment {
	return pipeKeyAttachment{name: name, blockKey: key}
}

// TestRenderAll_TwoDeliveriesOnOneKeyGrid walks the grid and the rows beside it.
func TestRenderAll_TwoDeliveriesOnOneKeyGrid(t *testing.T) {
	t.Parallel()
	const key = "API_KEY"
	blockRule := renderer.ErrAttachmentInjectKeyCollision()
	mappingRule := renderer.ErrAttachmentInjectMappingKeyCollision()

	rows := []pipeKeyRow{
		{
			name:          "CONTROL: every surface delivers a key of its own",
			selfKey:       "API_SELF",
			connectionKey: "API_CONN",
			attachments: []pipeKeyAttachment{
				blocked("alpha", "API_BLOCK"),
				mapped("beta", "API_MAP", "from-beta-mapping"),
			},
			pipe: []string{
				"env API_SELF=from-component",
				"env API_CONN=db-host",
				"env API_BLOCK=from-alpha-block",
				"env API_MAP=from-beta-mapping",
			},
		},
		{
			name:        "CELL (component's own block, attachment's block): refused by the block rule",
			selfKey:     key,
			attachments: []pipeKeyAttachment{blocked("alpha", key)},
			refused:     blockRule,
			locator:     ".spec.components.api.features.alpha",
			detail:      []string{`"API_KEY"`, `mechanism "env"`},
		},
		{
			name:        "CELL (component's own block, attachment's mapping): interception renders",
			selfKey:     key,
			attachments: []pipeKeyAttachment{mapped("alpha", key, "from-alpha-mapping")},
			pipe:        []string{"env API_KEY=from-alpha-mapping"},
		},
		{
			name:          "CELL (connection's inject block, attachment's block): refused by the block rule",
			connectionKey: key,
			attachments:   []pipeKeyAttachment{blocked("alpha", key)},
			refused:       blockRule,
			locator:       ".spec.components.api.features.alpha",
			detail:        []string{`"API_KEY"`, `mechanism "env"`},
		},
		{
			name:          "CELL (connection's inject block, attachment's mapping): interception renders",
			connectionKey: key,
			attachments:   []pipeKeyAttachment{mapped("alpha", key, "from-alpha-mapping")},
			pipe:          []string{"env API_KEY=from-alpha-mapping"},
		},
		{
			name:        "CELL (another attachment's block, attachment's block): refused by the block rule",
			attachments: []pipeKeyAttachment{blocked("alpha", key), blocked("beta", key)},
			refused:     blockRule,
			locator:     ".spec.components.api.features.beta",
			detail:      []string{`"API_KEY"`, `mechanism "env"`},
		},
		{
			name:        "CELL (another attachment's block, attachment's mapping): refused by the mapping rule",
			attachments: []pipeKeyAttachment{blocked("alpha", key), mapped("beta", key, "from-beta-mapping")},
			refused:     mappingRule,
			locator:     ".spec.components.api.features.beta",
			detail:      []string{`"API_KEY"`, `mechanism "env"`, `attachment "beta"`, `delivered first by attachment "alpha"`},
		},
		{
			name: "CELL (another attachment's mapping, attachment's block): IMPOSSIBLE — the block delivers first",
			// Authored in the order the cell names: the mapping's attachment is
			// attached first. Blocks still deliver before any mapping renders, so
			// the block holds the key and the mapping is the later claim.
			attachments: []pipeKeyAttachment{mapped("alpha", key, "from-alpha-mapping"), blocked("beta", key)},
			refused:     mappingRule,
			locator:     ".spec.components.api.features.alpha",
			detail:      []string{`"API_KEY"`, `mechanism "env"`, `attachment "alpha"`, `delivered first by attachment "beta"`},
		},
		{
			name: "CELL (another attachment's mapping, attachment's mapping): refused by the mapping rule",
			attachments: []pipeKeyAttachment{
				mapped("alpha", key, "from-alpha-mapping"), mapped("beta", key, "from-beta-mapping"),
			},
			refused: mappingRule,
			locator: ".spec.components.api.features.beta",
			detail:  []string{`"API_KEY"`, `mechanism "env"`, `attachment "beta"`, `delivered first by attachment "alpha"`},
		},
		{
			name: "EXTRA: one key under two mechanisms is two keyed spaces and renders",
			attachments: []pipeKeyAttachment{
				mapped("alpha", key, "from-alpha-mapping"),
				{name: "beta", mapMechanism: "file", mapKey: key, mapValue: "from-beta-mapping"},
			},
			pipe: []string{"env API_KEY=from-alpha-mapping", "file API_KEY=from-beta-mapping"},
		},
		{
			name: "EXTRA: two mappings agreeing on the value are still refused",
			attachments: []pipeKeyAttachment{
				mapped("alpha", key, "same-value"), mapped("beta", key, "same-value"),
			},
			refused: mappingRule,
			locator: ".spec.components.api.features.beta",
			detail:  []string{`"API_KEY"`, `attachment "beta"`, `delivered first by attachment "alpha"`},
		},
		{
			name: "EXTRA: a mapping writing nothing under the key leaves it free",
			attachments: []pipeKeyAttachment{
				mapped("alpha", "", ""), mapped("beta", key, "from-beta-mapping"),
			},
			pipe: []string{"env API_KEY=from-beta-mapping"},
		},
		{
			name: "EXTRA: a mapping writing an empty value has delivered under the key",
			attachments: []pipeKeyAttachment{
				mapped("alpha", key, ""), mapped("beta", key, "from-beta-mapping"),
			},
			refused: mappingRule,
			locator: ".spec.components.api.features.beta",
			detail:  []string{`"API_KEY"`, `attachment "beta"`, `delivered first by attachment "alpha"`},
		},
		{
			name: "EXTRA: a connection's attachment and the component's own, of one Feature, are refused at the connection entry",
			attachments: []pipeKeyAttachment{
				mapped("alpha", key, "from-alpha-mapping"),
				{name: "main.credential", slot: "credential", mapMechanism: "env", mapKey: key, mapValue: "from-connection"},
			},
			refused: mappingRule,
			locator: ".spec.components.api.uses.databases.main.features.credential",
			detail:  []string{`"API_KEY"`, `attachment "main.credential"`, `delivered first by attachment "alpha"`},
		},
		{
			name: "EXTRA: an attachment the Release wrote, whose mapping meets another's key, is refused at the Release's entry",
			attachments: []pipeKeyAttachment{
				blocked("alpha", key),
				{name: "beta", mapMechanism: "env", mapKey: key, mapValue: "from-beta-mapping", releaseBoundary: "local"},
			},
			refused:  mappingRule,
			locator:  ".spec.boundaries.local.components.api.features.beta",
			operator: true,
			detail:   []string{`"API_KEY"`, `attachment "beta"`, `delivered first by attachment "alpha"`},
		},
		{
			// Two attachments the Release alone wrote, each delivering under the key its
			// mapping chooses from the attachment's own values — which the Release cannot
			// write, so neither can be given another key there. The remedy says so.
			name: "EXTRA: two attachments the Release wrote, both mapping one key, are refused with the remedy",
			attachments: []pipeKeyAttachment{
				{name: "alpha", mapMechanism: "env", mapKey: key, mapValue: "from-alpha-mapping", releaseBoundary: "local"},
				{name: "beta", mapMechanism: "env", mapKey: key, mapValue: "from-beta-mapping", releaseBoundary: "local"},
			},
			refused:  mappingRule,
			locator:  ".spec.boundaries.local.components.api.features.beta",
			operator: true,
			detail: []string{
				`"API_KEY"`, `attachment "beta"`, `delivered first by attachment "alpha"`,
				"A Release sets an attachment's infra and never its values, so where a key is chosen by a value, " +
					"attach that attachment in the Aggregate with the value, and keep giving the rest from the Release.",
			},
		},
		{
			name: "EXTRA: an attachment's mapping rewriting its own block's key displaces nobody and renders",
			attachments: []pipeKeyAttachment{
				{name: "alpha", blockKey: key, mapMechanism: "env", mapKey: key, mapValue: "from-alpha-mapping"},
			},
			pipe: []string{"env API_KEY=from-alpha-mapping"},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			input, lookup := pipeKeyInput(row)
			engine := &captureComposerEngine{}
			factory := func(string) (renderer.Provider, error) { return engine, nil }

			_, err := renderer.RenderAll(context.Background(), input, lookup, factory)
			if row.refused == nil {
				if err != nil {
					t.Fatalf("the build must render; got %v", err)
				}
				pipe := capturedFile(t, engine, "api-pipe.txt")
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
			for _, other := range []error{blockRule, mappingRule} {
				if other != row.refused && errors.Is(err, other) {
					t.Fatalf("the halt must carry one condition, not also %v; got %v", other, err)
				}
			}
			var ce *cerrors.CompilerError
			if !errors.As(err, &ce) {
				t.Fatalf("the halt is not a compiler diagnostic: %v", err)
			}
			wantCategory, wantFile := cerrors.DeveloperError, "aggregate.yaml"
			if row.operator {
				wantCategory, wantFile = cerrors.OpsError, "release.yaml"
			}
			if ce.Category() != wantCategory {
				t.Errorf("category = %s, want %s", ce.Category(), wantCategory)
			}
			if ce.Source().File != wantFile {
				t.Errorf("source file = %q, want %q, the document the attachment was written in",
					ce.Source().File, wantFile)
			}
			if ce.Source().Locator != row.locator {
				t.Errorf("locator = %q, want %q", ce.Source().Locator, row.locator)
			}
			for _, part := range row.detail {
				if !strings.Contains(ce.Message(), part) {
					t.Errorf("the finding must carry %s; got %q", part, ce.Message())
				}
			}
			// Every refusal by the mapping rule says what to write instead; the block
			// rule's registered wording is unchanged and carries no remedy of its own.
			const remedy = "Give one of the two attachments a key of its own, or attach one fewer: " +
				"the key this attachment delivers under is chosen by its FeatureRecipe, which can read the " +
				"attachment's own values and infra, and the other's is chosen the same way or by its own " +
				"inject block where it delivered through one."
			if carries := strings.Contains(ce.Message(), remedy); carries != (row.refused == mappingRule) {
				t.Errorf("the remedy must be carried exactly by the mapping rule's refusals (carried: %v); got %q",
					carries, ce.Message())
			}
			// The mapping rule's refusal is read by whoever wrote either attachment, so it
			// names no catalog field.
			if row.refused == mappingRule && strings.Contains(ce.Message(), "injectMapping") {
				t.Errorf("the mapping rule's refusal names the catalog field: %q", ce.Message())
			}
		})
	}
}

// capturedFile returns the body of the one captured file whose name ends in
// suffix, failing the test where there is not exactly one.
func capturedFile(t *testing.T, engine *captureComposerEngine, suffix string) string {
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

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Tests for the three facts an assertion reads about each connection — the
// target's labels, the values written on the connection and the attachments
// written on it — held to what a Recipe template reads about the same connection
// at .context.uses.<alias>, on every assertion surface, and for what a failure
// quoting one of them may say when a Feature declares it sensitive.
package renderer

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	factsRouterType = "probe/router"
	factsModelType  = "probe/model"
	factsStoreType  = "probe/store"
	factsMarkerType = "probe/marker"
	factsValueType  = "probe/secret-value"
	factsRefType    = "probe/referenced-secret"

	// factsOrigin is what the fast connection's credential carries at the one leaf
	// its Feature may declare sensitive.
	factsOrigin = "fast-origin-plaintext"
)

// assertionSurfaces names the four surfaces an assertion is evaluated on.
var assertionSurfaces = []string{"Component", "Feature", "Recipe", "FeatureRecipe"}

// connectionFactsGraph is a router with two connections on its models socket and
// one on its cache_store socket. Each target carries labels of its own and the
// router carries different ones, every connection carries values, and two of the
// three carry a credential — of two different Features, under the same slot name —
// beside an attachment of the router's own block.
func connectionFactsGraph(t *testing.T) *types.IRGraph {
	t.Helper()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("router", types.IRNode{Component: factsRouterType, Labels: omString(t,
		"component.aggrega.ai/hosting", "consumer-side", "probe.example/name", "router")})
	nodes.Set("llama", types.IRNode{Component: factsModelType, ResolvedVersion: "1.0.0", Labels: omString(t,
		"component.aggrega.ai/hosting", "self-hosted", "probe.example/name", "llama")})
	nodes.Set("turbo", types.IRNode{Component: factsModelType, ResolvedVersion: "1.0.0", Labels: omString(t,
		"component.aggrega.ai/hosting", "managed", "probe.example/name", "turbo")})
	nodes.Set("redis", types.IRNode{Component: factsStoreType, ResolvedVersion: "2.1.0", Labels: omString(t,
		"probe.example/name", "redis")})

	attachments := types.OrderedMap[types.IRFeatureEntry]{}
	attachments.Set("marker", types.IRFeatureEntry{Type: factsMarkerType, Values: omAny(t, "note", "own")})
	attachments.Set(types.JoinConnectionAttachmentName("fast", "credential"), types.IRFeatureEntry{
		Type:            factsValueType,
		ResolvedVersion: "1.0.0",
		Labels:          omString(t, "feature.aggrega.ai/kind", "secret"),
		Values:          omAny(t, "origin", factsOrigin, "key", "api-key"),
		Infra:           omAny(t, "rotation", "hourly"),
		Connection:      &types.AttachmentConnection{Socket: "models", Alias: "fast", Attachment: "credential"},
	})
	attachments.Set(types.JoinConnectionAttachmentName("store", "credential"), types.IRFeatureEntry{
		Type:            factsRefType,
		ResolvedVersion: "2.0.0",
		Values:          omAny(t, "name", "redis-auth", "key", "password"),
		Connection:      &types.AttachmentConnection{Socket: "cache_store", Alias: "store", Attachment: "credential"},
	})

	graph := &types.IRGraph{
		Metadata: types.IRMetadata{AggregateName: "facts"},
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "router", Target: "llama", Socket: "models", Alias: "local", Values: omAny(t, "unauthenticated", true, "tier", "edge")},
				{Source: "router", Target: "turbo", Socket: "models", Alias: "fast", Values: omAny(t, "unauthenticated", false)},
				{Source: "router", Target: "redis", Socket: "cache_store", Alias: "store", Values: omAny(t, "database", "2")},
			},
		},
	}
	graph.Spec.FeatureValuesMapping.Set("router", attachments)
	return graph
}

// factsSchema parses a compact-form pillar schema for a fixture Feature.
func factsSchema(t *testing.T, body string) yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(body), &n); err != nil {
		t.Fatalf("schema fixture parse: %v", err)
	}
	return n
}

// evaluateConnectionFacts evaluates body as the named surface's only assertion
// over connectionFactsGraph, through that surface's own phase driver. schemas
// gives the credential Features their schemas, so what each declares sensitive is
// read the way a build reads it: through the catalog for the Component and
// Feature surfaces, and through the declarations a render reads for the Recipe
// and FeatureRecipe surfaces. Only the router's Component and the marker's Feature
// and FeatureRecipe carry the assertion, so it runs once, on the router.
func evaluateConnectionFacts(t *testing.T, surface, body string, schemas map[string]string) error {
	t.Helper()
	graph := connectionFactsGraph(t)
	assertions := omString(t, "facts", body)
	lookup := resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI != factsRouterType {
				return types.Component{}, false
			}
			if surface == "Component" {
				return types.Component{Spec: types.ComponentSpec{Assertions: assertions}}, true
			}
			return types.Component{}, true
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			if typeURI == factsMarkerType {
				if surface == "Feature" {
					return types.Feature{Spec: types.FeatureSpec{Assertions: assertions}}, true
				}
				return types.Feature{}, true
			}
			schema, declared := schemas[typeURI]
			if !declared {
				return types.Feature{}, false
			}
			return types.Feature{Spec: types.FeatureSpec{Schema: factsSchema(t, schema)}}, true
		},
	}
	switch surface {
	case "Component", "Feature":
		return EvaluateComponentAndFeatureAssertions(graph, lookup)
	case "Recipe":
		recipe := &types.Recipe{
			Metadata: types.RecipeMetadata{Name: "probe-router", Version: "1.0.0"},
			Spec:     types.RecipeSpec{Assertions: assertions},
		}
		recipes := map[string]*types.Recipe{"router": recipe}
		declared := readRefusalDeclarations(graph, recipes, newDeclarationReaders(lookup))
		return EvaluateRecipeAndFeatureRecipeAssertions(graph, recipes, RecipeLookup{}, NodeProvides{}, NodeLocals{}, declared)
	case "FeatureRecipe":
		fr := types.FeatureRecipe{
			Metadata: types.FeatureRecipeMetadata{Name: "probe-marker", Version: "1.0.0"},
			Spec:     types.FeatureRecipeSpec{Assertions: assertions},
		}
		recipes := map[string]*types.Recipe{"router": {Metadata: types.RecipeMetadata{Name: "probe-router", Version: "1.0.0"}}}
		declared := readRefusalDeclarations(graph, recipes, newDeclarationReaders(lookup))
		frLookup := RecipeLookup{FeatureRecipe: func(_, typeURI string) (*types.FeatureRecipe, bool) {
			return &fr, typeURI == factsMarkerType
		}}
		return EvaluateRecipeAndFeatureRecipeAssertions(graph, recipes, frLookup, NodeProvides{}, NodeLocals{}, declared)
	}
	t.Fatalf("no assertion surface named %q", surface)
	return nil
}

// recipeTemplateReads renders tmpl against the render context a Recipe of the
// router is evaluated against, assembled from the same graph.
func recipeTemplateReads(t *testing.T, tmpl string) string {
	t.Helper()
	inject, injectRaw := BuildInjectPipe(nil)
	ctx := AssembleNodeContext("router", connectionFactsGraph(t), NodeProvides{}, nil, inject, injectRaw, nil, nil)
	out, err := EvalTemplate("recipe-reads", tmpl, ctx, redact.Keys{})
	if err != nil {
		t.Fatalf("the Recipe template %s: %v", tmpl, err)
	}
	return out
}

// TestAssertions_EachConnectionCarriesWhatARecipeTemplateReadsAboutIt holds, for
// every connection and on every assertion surface, each of the three facts an
// assertion reads at .uses.<socket>.<alias> to the bytes a Recipe template reads
// for the same connection at .context.uses.<alias>: labels — the TARGET's —,
// values, and features, the latter in the shape every attachment takes on an
// assertion (type, resolved version, values). The router's own labels differ from
// every target's, so a view showing the consumer's labels fails the labels rows.
func TestAssertions_EachConnectionCarriesWhatARecipeTemplateReadsAboutIt(t *testing.T) {
	t.Parallel()
	featuresAsAssertionReads := func(alias string) string {
		return `{{- $out := dict -}}` +
			`{{- range $slot, $f := (default dict .context.uses.` + alias + `.features) -}}` +
			`{{- $_ := set $out $slot (dict "type" $f.type "resolved_version" $f.resolved_version "values" $f.values) -}}` +
			`{{- end -}}{{ toJson $out }}`
	}
	type connection struct{ socket, alias string }
	connections := []connection{{"models", "local"}, {"models", "fast"}, {"cache_store", "store"}}
	facts := []struct {
		name     string
		template func(alias string) string
	}{
		{"labels", func(alias string) string { return `{{ toJson .context.uses.` + alias + `.labels }}` }},
		{"values", func(alias string) string { return `{{ toJson .context.uses.` + alias + `.values }}` }},
		{"features", featuresAsAssertionReads},
	}

	// The rows are not vacuous: each fact differs between two connections, and
	// the target's labels are not the router's.
	if a, b := recipeTemplateReads(t, facts[0].template("local")), recipeTemplateReads(t, facts[0].template("fast")); a == b {
		t.Fatalf("the fixture's targets carry the same labels: %s", a)
	}
	if got := recipeTemplateReads(t, `{{ toJson .context.labels }}`); got == recipeTemplateReads(t, facts[0].template("local")) {
		t.Fatalf("the fixture's router carries its target's labels: %s", got)
	}
	if got := recipeTemplateReads(t, featuresAsAssertionReads("fast")); !strings.Contains(got, factsOrigin) {
		t.Fatalf("the fixture's fast connection carries no credential: %s", got)
	}

	for _, surface := range assertionSurfaces {
		for _, c := range connections {
			for _, fact := range facts {
				t.Run(surface+"/"+c.alias+"/"+fact.name, func(t *testing.T) {
					t.Parallel()
					want := recipeTemplateReads(t, fact.template(c.alias))
					body := `{{- fail (printf "FACT[%s]" (toJson .uses.` + c.socket + `.` + c.alias + `.` + fact.name + `)) -}}`
					err := evaluateConnectionFacts(t, surface, body, nil)
					if !errors.Is(err, ErrAssertionFailed()) {
						t.Fatalf("%s assertion: want ErrAssertionFailed, got %v", surface, err)
					}
					if !strings.Contains(err.Error(), "FACT["+want+"]") {
						t.Errorf("%s assertion reads %s of %s.%s differently from a Recipe template\nwant FACT[%s]\n got %s",
							surface, fact.name, c.socket, c.alias, want, err.Error())
					}
				})
			}
		}
	}
}

// TestAssertions_AConnectionsFactsAreAlwaysPresent: a connection carrying no
// attachment still has a features entry, empty, and the refusal the facts exist
// for reads them directly — a managed target reached with neither a credential nor
// the opt-out — naming the connection, while a self-hosted target and a managed
// one carrying a credential pass.
func TestAssertions_AConnectionsFactsAreAlwaysPresent(t *testing.T) {
	t.Parallel()
	const refusal = `{{- range $socket, $connections := .uses -}}` +
		`{{- range $alias, $c := $connections -}}` +
		`{{- if and (eq (optional $c.labels "component.aggrega.ai/hosting" "") "managed") ` +
		`(not (hasKey $c.features "credential")) (not (optional $c.values "unauthenticated" false)) -}}` +
		`{{- fail (printf "connection %s on socket %s reaches a managed target with no credential" $alias $socket) -}}` +
		`{{- end -}}{{- end -}}{{- end -}}`
	const localFeatures = `{{- fail (printf "local carries %d attachments" (len .uses.models.local.features)) -}}`
	for _, surface := range assertionSurfaces {
		t.Run(surface+"/no attachment reads as an empty features entry", func(t *testing.T) {
			t.Parallel()
			err := evaluateConnectionFacts(t, surface, localFeatures, nil)
			if err == nil || !strings.Contains(err.Error(), "local carries 0 attachments") {
				t.Errorf("%s assertion: want the empty features entry counted, got %v", surface, err)
			}
		})
		t.Run(surface+"/the managed target carrying a credential passes", func(t *testing.T) {
			t.Parallel()
			if err := evaluateConnectionFacts(t, surface, refusal, nil); err != nil {
				t.Errorf("%s assertion: the fixture's one managed target carries a credential and must pass, got %v", surface, err)
			}
		})
	}
}

// TestAssertions_AConnectionAttachmentsDeclaredLeafIsWithheldFromAFailure: an
// assertion quoting the leaf a connection credential's Feature declares sensitive
// reports without it on every surface, exactly as a Recipe template quoting it at
// .context.uses.<alias>.features.<slot> does; the same assertion with nothing
// declared quotes it, which is what keeps the withheld row from passing on its own.
// The undeclared sibling, and the same slot on a connection whose Feature declares
// nothing, are quoted as they are.
func TestAssertions_AConnectionAttachmentsDeclaredLeafIsWithheldFromAFailure(t *testing.T) {
	t.Parallel()
	declares := map[string]string{factsValueType: "values:\n  origin: { type: string, sensitive: true }\n  key: { type: string }\n"}
	declaresNothing := map[string]string{factsValueType: "values:\n  origin: { type: string }\n  key: { type: string }\n"}
	cases := []struct {
		name      string
		body      string
		withheld  string
		inTheOpen string
	}{
		{
			name:      "the declared leaf beside its undeclared sibling",
			body:      `{{- fail (printf "origin %s key %s" .uses.models.fast.features.credential.values.origin .uses.models.fast.features.credential.values.key) -}}`,
			withheld:  "origin " + redact.Placeholder() + " key api-key",
			inTheOpen: "origin " + factsOrigin + " key api-key",
		},
		{
			name:      "the same slot on a connection whose Feature declares nothing",
			body:      `{{- fail (printf "store secret %s" .uses.cache_store.store.features.credential.values.name) -}}`,
			withheld:  "store secret redis-auth",
			inTheOpen: "store secret redis-auth",
		},
	}
	for _, surface := range assertionSurfaces {
		for _, c := range cases {
			t.Run(surface+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				got := evaluateConnectionFacts(t, surface, c.body, declares)
				plain := evaluateConnectionFacts(t, surface, c.body, declaresNothing)
				if !errors.Is(got, ErrAssertionFailed()) || !errors.Is(plain, ErrAssertionFailed()) {
					t.Fatalf("%s assertion must fail either way; declared=%v nothing declared=%v", surface, got, plain)
				}
				if strings.Contains(got.Error(), factsOrigin) {
					t.Errorf("%s assertion's failure carries the declared value:\n%s", surface, got)
				}
				if !strings.Contains(got.Error(), c.withheld) {
					t.Errorf("%s assertion's failure must read %q:\n%s", surface, c.withheld, got)
				}
				if !strings.Contains(plain.Error(), c.inTheOpen) {
					t.Errorf("%s assertion with nothing declared must read %q:\n%s", surface, c.inTheOpen, plain)
				}
			})
		}
	}
}

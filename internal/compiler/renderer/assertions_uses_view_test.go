// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestExtractNodeBaseContext_UsesKeysEveryConnectionByAliasUnderItsSocket pins
// the shape of an assertion's `.uses`: one entry per socket, and under it one
// entry per connection keyed by its alias, so no connection on a socket hides
// another.
func TestExtractNodeBaseContext_UsesKeysEveryConnectionByAliasUnderItsSocket(t *testing.T) {
	t.Parallel()
	conn := func(target, alias string) map[string]any {
		return map[string]any{"target": target, "alias": alias}
	}
	for _, tc := range []struct {
		name  string
		edges []types.IREdge
		want  map[string]any
	}{
		{
			name:  "a node with no connection has an empty view",
			edges: nil,
			want:  map[string]any{},
		},
		{
			name:  "one connection sits under its socket and its alias",
			edges: []types.IREdge{{Source: "router", Target: "llama", Socket: "models", Alias: "local"}},
			want:  map[string]any{"models": map[string]any{"local": conn("llama", "local")}},
		},
		{
			name: "two connections on one socket are both kept",
			edges: []types.IREdge{
				{Source: "router", Target: "llama", Socket: "models", Alias: "local"},
				{Source: "router", Target: "turbo", Socket: "models", Alias: "fast"},
			},
			want: map[string]any{"models": map[string]any{
				"local": conn("llama", "local"),
				"fast":  conn("turbo", "fast"),
			}},
		},
		{
			name: "one alias on two sockets is two connections",
			edges: []types.IREdge{
				{Source: "router", Target: "llama", Socket: "models", Alias: "main"},
				{Source: "router", Target: "store", Socket: "cache_store", Alias: "main"},
			},
			want: map[string]any{
				"models":      map[string]any{"main": conn("llama", "main")},
				"cache_store": map[string]any{"main": conn("store", "main")},
			},
		},
		{
			name:  "a list entry's connection is keyed by its target, which is its alias",
			edges: []types.IREdge{{Source: "router", Target: "llama", Socket: "models", Alias: "llama"}},
			want:  map[string]any{"models": map[string]any{"llama": conn("llama", "llama")}},
		},
		{
			name:  "a connection an attachment drew carries its origin",
			edges: []types.IREdge{{Source: "router", Target: "llama", Socket: "models", Alias: "local", OriginFeature: "probe/marker"}},
			want: map[string]any{"models": map[string]any{"local": map[string]any{
				"target": "llama", "alias": "local", "origin_feature": "probe/marker",
			}}},
		},
		{
			name: "another node's connections and a socketless edge are left out",
			edges: []types.IREdge{
				{Source: "other", Target: "llama", Socket: "models", Alias: "local"},
				{Source: "router", Target: "llama", Alias: "local"},
			},
			want: map[string]any{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			graph := &types.IRGraph{}
			graph.Spec.Nodes.Set("router", types.IRNode{Component: "probe/router"})
			graph.Spec.Edges = tc.edges
			got := ExtractNodeBaseContext("router", graph).Uses
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("assertion .uses\n got %v\nwant %v", got, tc.want)
			}
		})
	}
}

// TestAssertions_EverySurfaceSeesEveryConnectionOnASocket drives each of the four
// assertion surfaces through its own phase driver over one node with two
// connections on its models socket, and pins what each reads: every connection,
// one connection by its alias, the socket's presence, and the refusal of a read
// that skips the alias.
func TestAssertions_EverySurfaceSeesEveryConnectionOnASocket(t *testing.T) {
	t.Parallel()
	const (
		everyTarget = `{{- $t := list -}}{{- range $alias, $c := .uses.models -}}{{- $t = append $t (printf "%s=%s" $alias $c.target) -}}{{- end -}}{{- fail (printf "models: %s" (join ", " $t)) -}}`
		oneByAlias  = `{{- fail (printf "local reads %s" .uses.models.local.target) -}}`
		presence    = `{{- if not (hasKey .uses "models") -}}{{- fail "the models socket is absent" -}}{{- end -}}{{- if hasKey .uses "absent" -}}{{- fail "an unwired socket is present" -}}{{- end -}}`
		skipsAlias  = `{{- fail (printf "socket reads %s" .uses.models.target) -}}`
	)
	type outcome int
	const (
		passes outcome = iota
		failsWith
		haltsOnMissingKey
	)
	surfaces := []string{"Component", "Feature", "Recipe", "FeatureRecipe"}
	cases := []struct {
		name      string
		assertion string
		outcome   outcome
		want      string
	}{
		{"names every connection on the socket", everyTarget, failsWith, "models: fast=turbo, local=llama"},
		{"reads one connection by its alias", oneByAlias, failsWith, "local reads llama"},
		{"reads a socket's presence by its key", presence, passes, ""},
		{"refuses a read that skips the alias", skipsAlias, haltsOnMissingKey, `"target"`},
	}
	for _, surface := range surfaces {
		for _, tc := range cases {
			t.Run(surface+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				err := evaluateOnSurface(t, surface, tc.assertion)
				switch tc.outcome {
				case passes:
					if err != nil {
						t.Fatalf("%s assertion: want it to pass, got %v", surface, err)
					}
				case failsWith:
					if !errors.Is(err, ErrAssertionFailed()) {
						t.Fatalf("%s assertion: want ErrAssertionFailed, got %v", surface, err)
					}
					if !strings.Contains(err.Error(), tc.want) {
						t.Errorf("%s assertion: want the halt to read %q, got %q", surface, tc.want, err.Error())
					}
				case haltsOnMissingKey:
					if err == nil || errors.Is(err, ErrAssertionFailed()) {
						t.Fatalf("%s assertion: want a missing-key halt, got %v", surface, err)
					}
					if !strings.Contains(err.Error(), tc.want) {
						t.Errorf("%s assertion: want the halt to name %s, got %q", surface, tc.want, err.Error())
					}
				}
			})
		}
	}
}

// evaluateOnSurface evaluates one assertion body as the named surface's only
// assertion, over a router whose models socket carries local -> llama and
// fast -> turbo, through that surface's own phase driver. The Component and
// the Feature are found by their own type only, as the catalog finds them, so
// the assertion runs on the router and never on the two model nodes, which are
// of another type and draw no connection.
func evaluateOnSurface(t *testing.T, surface, body string) error {
	t.Helper()
	const (
		routerType = "probe/router"
		modelType  = "probe/model"
		markerType = "probe/marker"
	)
	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set("router", types.IRNode{Component: routerType})
	graph.Spec.Nodes.Set("llama", types.IRNode{Component: modelType})
	graph.Spec.Nodes.Set("turbo", types.IRNode{Component: modelType})
	graph.Spec.Edges = []types.IREdge{
		{Source: "router", Target: "llama", Socket: "models", Alias: "local"},
		{Source: "router", Target: "turbo", Socket: "models", Alias: "fast"},
	}
	attachments := types.OrderedMap[types.IRFeatureEntry]{}
	attachments.Set("marker", types.IRFeatureEntry{Type: markerType, Values: omAny(t)})
	graph.Spec.FeatureValuesMapping.Set("router", attachments)

	assertions := omString(t, "connections", body)
	plainRecipe := &types.Recipe{Metadata: types.RecipeMetadata{Name: "probe-router", Version: "1.0.0"}}
	switch surface {
	case "Component":
		comp := types.Component{Spec: types.ComponentSpec{Assertions: assertions}}
		return EvaluateComponentAndFeatureAssertions(graph, resolver.CatalogLookup{
			Component: func(typeURI string) (types.Component, bool) {
				if typeURI != routerType {
					return types.Component{}, false
				}
				return comp, true
			},
		})
	case "Feature":
		feat := types.Feature{Spec: types.FeatureSpec{Assertions: assertions}}
		return EvaluateComponentAndFeatureAssertions(graph, resolver.CatalogLookup{
			Feature: func(typeURI string) (types.Feature, bool) {
				if typeURI != markerType {
					return types.Feature{}, false
				}
				return feat, true
			},
		})
	case "Recipe":
		recipe := &types.Recipe{
			Metadata: types.RecipeMetadata{Name: "probe-router", Version: "1.0.0"},
			Spec:     types.RecipeSpec{Assertions: assertions},
		}
		return EvaluateRecipeAndFeatureRecipeAssertions(graph,
			map[string]*types.Recipe{"router": recipe}, RecipeLookup{}, NodeProvides{}, NodeLocals{})
	case "FeatureRecipe":
		fr := types.FeatureRecipe{
			Metadata: types.FeatureRecipeMetadata{Name: "probe-marker", Version: "1.0.0"},
			Spec:     types.FeatureRecipeSpec{Assertions: assertions},
		}
		return EvaluateRecipeAndFeatureRecipeAssertions(graph,
			map[string]*types.Recipe{"router": plainRecipe},
			RecipeLookup{FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return &fr, true }},
			NodeProvides{}, NodeLocals{})
	}
	t.Fatalf("no assertion surface named %q", surface)
	return nil
}

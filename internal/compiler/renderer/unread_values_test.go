// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// uvRecipe wraps an inline valuesMapping in the minimum Recipe the reading needs.
func uvRecipe(inline string) *types.Recipe {
	return &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "consumer-recipe"},
		Spec: types.RecipeSpec{
			ValuesMapping: types.ValuesMapping{Inline: inline},
		},
	}
}

// uvGraph builds a graph over the named nodes and edges. Every edge target must
// be among the nodes or the projection drops the edge.
func uvGraph(nodeIDs []string, edges ...types.IREdge) *types.IRGraph {
	nodes := types.OrderedMap[types.IRNode]{}
	for _, id := range nodeIDs {
		nodes.Set(id, types.IRNode{Component: "core/compute/" + id, ResolvedVersion: "1.0.0"})
	}
	return &types.IRGraph{Spec: types.IRSpec{Nodes: nodes, Edges: edges}}
}

// uvWarnings runs the reading for one node through the same context assembly the
// render loop uses, so the per-edge group is reached exactly as it is in a build.
//
// The advisories come back as the lines a reader is shown, rendered through the
// one display formatter, so the rows below keep asserting on the text a build
// prints rather than on a shape only this reading produces.
func uvWarnings(t *testing.T, nodeID string, graph *types.IRGraph, recipe *types.Recipe) []string {
	t.Helper()
	inject, injectRaw := BuildInjectPipe(nil)
	ctx := AssembleNodeContext(nodeID, graph, NodeProvides{}, nil, inject, injectRaw, nil, nil)
	raised := unreadValueWarnings(nodeID, recipe, ctx.Values, ctx.Infra, ctx.Context.Uses)
	if raised == nil {
		return nil
	}
	out := make([]string, 0, len(raised))
	for _, d := range raised {
		out = append(out, RenderDiagnosticText(d))
	}
	return out
}

func uvJoined(warnings []string) string { return strings.Join(warnings, "\n") }

// TestUnreadValueWarnings_NodeValuesAndInfra pins the node's own two groups: a
// key the valuesMapping reads is silent, a key it does not is reported, and the
// values group is reported ahead of the infra group.
func TestUnreadValueWarnings_NodeValuesAndInfra(t *testing.T) {
	t.Parallel()

	graph := uvGraph([]string{"api"})
	graph.Spec.ValuesMapping.Set("api", json.RawMessage(`{"port":8080,"orphan_value":"x"}`))
	graph.Spec.InfraMapping.Set("api", json.RawMessage(`{"replicas":2,"orphan_infra":"y"}`))

	got := uvWarnings(t, "api", graph, uvRecipe(`p: {{ .values.port }} r: {{ .infra.replicas }}`))
	want := []string{
		`node "api": .values key "orphan_value" is read by no template in recipe "consumer-recipe" — correct the spelling, or remove the key`,
		`node "api": .infra key "orphan_infra" is read by no template in recipe "consumer-recipe" — correct the spelling, or remove the key`,
	}
	if len(got) != len(want) {
		t.Fatalf("warning count = %d, want %d:\n%s", len(got), len(want), uvJoined(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("warning[%d]:\n got  %s\n want %s", i, got[i], want[i])
		}
	}
}

// TestUnreadValueWarnings_PerEdgeValueReadAndUnread is the increment's core
// reading: a per-connection value the consumer's valuesMapping reads is silent,
// and a sibling it never reads is reported naming the consuming node, the alias
// (through the path the author types) and the key.
func TestUnreadValueWarnings_PerEdgeValueReadAndUnread(t *testing.T) {
	t.Parallel()

	graph := uvGraph([]string{"api", "db"},
		types.IREdge{Source: "api", Target: "db", Socket: "databases", Alias: "main",
			Values: projOM("pool_size", 20, "statement_cache", 5)})

	got := uvWarnings(t, "api", graph,
		uvRecipe(`pool: {{ .context.uses.main.values.pool_size }}`))

	if len(got) != 1 {
		t.Fatalf("warning count = %d, want 1:\n%s", len(got), uvJoined(got))
	}
	for _, want := range []string{`node "api"`, `.context.uses.main.values`, `"statement_cache"`} {
		if !strings.Contains(got[0], want) {
			t.Errorf("warning does not name %s:\n %s", want, got[0])
		}
	}
	if strings.Contains(got[0], "pool_size") {
		t.Errorf("a per-connection value the valuesMapping reads was reported unread:\n %s", got[0])
	}
}

// TestUnreadValueWarnings_PerEdgeIsConsumerAttributed pins the attribution: the
// per-connection value authored on the api->db edge is reported against api and
// never against db, whose render context does not carry it at all.
func TestUnreadValueWarnings_PerEdgeIsConsumerAttributed(t *testing.T) {
	t.Parallel()

	graph := uvGraph([]string{"api", "db", "log"},
		types.IREdge{Source: "api", Target: "db", Socket: "databases", Alias: "main",
			Values: projOM("pool_size", 20)},
		types.IREdge{Source: "db", Target: "log", Socket: "logs", Alias: "audit"})

	// The consumer reads nothing, so its own edge value is reported.
	consumer := uvWarnings(t, "api", graph, uvRecipe(`x: 1`))
	if len(consumer) != 1 || !strings.Contains(consumer[0], `.context.uses.main.values`) {
		t.Fatalf("consumer must be told about its own unread per-connection value; got:\n%s", uvJoined(consumer))
	}

	// The target reads nothing either, and must hear nothing about an edge
	// pointing at it — a target's reading is invariant w.r.t. its consumers.
	target := uvWarnings(t, "db", graph, uvRecipe(`x: 1`))
	if len(target) != 0 {
		t.Errorf("target attributed a consumer's per-connection value; got:\n%s", uvJoined(target))
	}
}

// TestUnreadValueWarnings_SocketValueSchemaIsNeverReported is the guard on the
// contract/instance split: a socket's valueSchema declares per-connection keys,
// and declaring them can never produce a warning. Only an authored instance can.
//
// The Component is built here, and its populated valueSchema asserted, so the
// test is a real guard rather than a vacuous one: the contract demonstrably
// exists, carries key names, and still reaches no warning — the reading is given
// value instances only, so there is no path from a contract to a report.
func TestUnreadValueWarnings_SocketValueSchemaIsNeverReported(t *testing.T) {
	t.Parallel()

	const componentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api
  version: "1.0.0"
spec:
  usesSchema:
    databases:
      selector: "component.aggrega.ai/type=stateful-server"
      valueSchema:
        pool_size: { type: integer, minimum: 1, maximum: 100 }
        contract_only_key: { type: string }
`
	var component types.Component
	if err := yaml.Unmarshal([]byte(componentYAML), &component); err != nil {
		t.Fatalf("unmarshal component: %v", err)
	}
	entry, ok := component.Spec.UsesSchema.Get("databases")
	if !ok {
		t.Fatal("usesSchema socket `databases` not populated; the guard would be vacuous")
	}
	if entry.ValueSchema.Kind == 0 {
		t.Fatal("valueSchema not populated; the guard would be vacuous")
	}

	// The contract is declared and the edge carries no authored instance.
	empty := uvGraph([]string{"api", "db"},
		types.IREdge{Source: "api", Target: "db", Socket: "databases", Alias: "main"})
	got := uvWarnings(t, "api", empty, uvRecipe(`x: 1`))
	if len(got) != 0 {
		t.Errorf("a socket's declared per-connection contract produced a warning; got:\n%s", uvJoined(got))
	}
	for _, schemaKey := range []string{"pool_size", "contract_only_key", "valueSchema"} {
		if strings.Contains(uvJoined(got), schemaKey) {
			t.Errorf("warning text names the contract key %q", schemaKey)
		}
	}

	// The same socket, now with one authored instance nothing reads: reported.
	instance := uvGraph([]string{"api", "db"},
		types.IREdge{Source: "api", Target: "db", Socket: "databases", Alias: "main",
			Values: projOM("contract_only_key", "authored")})
	got = uvWarnings(t, "api", instance, uvRecipe(`x: 1`))
	if len(got) != 1 || !strings.Contains(got[0], `"contract_only_key"`) {
		t.Errorf("an unread authored per-connection instance must be reported; got:\n%s", uvJoined(got))
	}
}

// TestUnreadValueWarnings_EnclosingUsesReadSilencesPerEdge is the false-positive
// guard on the forms a template actually uses to reach per-connection values.
// Iterating the uses map, or reaching one edge by index, or rebinding it, reads
// the keys through a value the template bound itself, so the census records only
// the enclosing path — and every key beneath it must count as read.
func TestUnreadValueWarnings_EnclosingUsesReadSilencesPerEdge(t *testing.T) {
	t.Parallel()

	graph := uvGraph([]string{"api", "db"},
		types.IREdge{Source: "api", Target: "db", Socket: "databases", Alias: "main",
			Values: projOM("pool_size", 20, "statement_cache", 5)})

	silent := []struct {
		name string
		mapp string
	}{
		{"iterate the whole uses map", `{{ range $alias, $edge := .context.uses }}{{ $alias }}{{ end }}`},
		{"reach one edge by index", `p: {{ (index .context.uses "main").values.pool_size }}`},
		{"rebind one edge", `{{ with .context.uses.main }}p: {{ .values.pool_size }}{{ end }}`},
		{"iterate the whole per-edge bag", `{{ range $k, $v := .context.uses.main.values }}{{ $k }}{{ end }}`},
		{"dump the whole context", `{{ toJson .context }}`},
	}
	for _, c := range silent {
		got := uvWarnings(t, "api", graph, uvRecipe(c.mapp))
		if len(got) != 0 {
			t.Errorf("%s: per-connection values reported unread; got:\n%s", c.name, uvJoined(got))
		}
	}

	// Control: a valuesMapping reaching no part of the uses map still reports both.
	control := uvWarnings(t, "api", graph, uvRecipe(`x: {{ .values.absent }}`))
	if len(control) != 2 {
		t.Fatalf("control: warning count = %d, want 2:\n%s", len(control), uvJoined(control))
	}
}

// TestUnreadValueWarnings_DeterministicOrder pins the advisory stream's order:
// the node's own groups first, then each alias in the order its edge was
// emitted, and keys within a group in one fixed order. Repeated readings of one
// input produce one stream.
func TestUnreadValueWarnings_DeterministicOrder(t *testing.T) {
	t.Parallel()

	graph := uvGraph([]string{"api", "db", "cache"},
		types.IREdge{Source: "api", Target: "db", Socket: "databases", Alias: "zeta",
			Values: projOM("z_two", 2, "z_one", 1)},
		types.IREdge{Source: "api", Target: "cache", Socket: "caches", Alias: "alpha",
			Values: projOM("a_two", 2, "a_one", 1)})
	graph.Spec.ValuesMapping.Set("api", json.RawMessage(`{"node_key":1}`))

	const suffix = ` is read by no template in recipe "consumer-recipe" — correct the spelling, or remove the key`
	want := []string{
		`node "api": .values key "node_key"` + suffix,
		`node "api": .context.uses.zeta.values key "z_one"` + suffix,
		`node "api": .context.uses.zeta.values key "z_two"` + suffix,
		`node "api": .context.uses.alpha.values key "a_one"` + suffix,
		`node "api": .context.uses.alpha.values key "a_two"` + suffix,
	}
	first := uvWarnings(t, "api", graph, uvRecipe(`x: 1`))
	if len(first) != len(want) {
		t.Fatalf("warning count = %d, want %d:\n%s", len(first), len(want), uvJoined(first))
	}
	for i := range want {
		if first[i] != want[i] {
			t.Errorf("warning[%d]:\n got  %s\n want %s", i, first[i], want[i])
		}
	}
	for range 4 {
		again := uvWarnings(t, "api", graph, uvRecipe(`x: 1`))
		if uvJoined(again) != uvJoined(first) {
			t.Fatalf("advisory stream not stable across readings:\n first:\n%s\n again:\n%s",
				uvJoined(first), uvJoined(again))
		}
	}
}

// TestUnreadValueWarnings_NoInlineValuesMappingReadsNothing pins the bound: with
// no inline mapping in hand there are no references to compare against, so no
// claim is made about any group — including the per-connection one.
func TestUnreadValueWarnings_NoInlineValuesMappingReadsNothing(t *testing.T) {
	t.Parallel()

	graph := uvGraph([]string{"api", "db"},
		types.IREdge{Source: "api", Target: "db", Socket: "databases", Alias: "main",
			Values: projOM("pool_size", 20)})

	if got := uvWarnings(t, "api", graph, uvRecipe("")); got != nil {
		t.Errorf("a Recipe with no inline valuesMapping produced warnings:\n%s", uvJoined(got))
	}
	if got := uvWarnings(t, "api", graph, nil); got != nil {
		t.Errorf("a nil Recipe produced warnings:\n%s", uvJoined(got))
	}
	// A mapping that does not parse yields no reference set, so nothing is said.
	if got := uvWarnings(t, "api", graph, uvRecipe(`{{ .values.port `)); got != nil {
		t.Errorf("an unparseable valuesMapping produced warnings:\n%s", uvJoined(got))
	}
}

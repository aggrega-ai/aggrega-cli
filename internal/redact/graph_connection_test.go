// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package redact_test

import (
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// connectionGraph is the fixture the connection tests read. Its consuming node
// draws two connections to one target: one whose configuration the edge marks —
// a values leaf, a leaf beneath a values object, and an infra leaf, each beside a
// readable sibling — and a control whose configuration writes a leaf of the SAME
// name and carries no marking. The node's authored `uses` block writes the values
// of both, as an author does, so the second position the configuration sits at is
// present for each.
func connectionGraph(t *testing.T) *types.IRGraph {
	t.Helper()

	uses := types.OrderedMap[types.UsesSocketValue]{}
	databases := types.UsesSocketValue{}
	databases.Verbose.Set("primary", types.VerboseUsesEntry{
		Target: "db",
		Values: om("endpoint", "db.internal", "token", "edge-token-secret", "auth", om("user", "app", "password", "edge-auth-secret")),
	})
	uses.Set("databases", databases)
	caches := types.UsesSocketValue{}
	caches.Verbose.Set("control", types.VerboseUsesEntry{
		Target: "db",
		Values: om("token", "control-token-readable"),
	})
	uses.Set("caches", caches)

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("app", types.IRNode{Component: "mark/app", Uses: uses})
	nodes.Set("db", types.IRNode{Component: "mark/db"})

	g := &types.IRGraph{}
	g.Spec.Nodes = nodes
	g.Spec.Edges = []types.IREdge{
		{
			Source: "app", Target: "db", Socket: "databases", Alias: "primary",
			SocketDeclaration: types.SocketDeclarationDeclared,
			// The default-filled leaf "seed" is in the edge's payload and not in the
			// authored block, as the graph build leaves it.
			Values: om("endpoint", "db.internal", "token", "edge-token-secret",
				"auth", om("user", "app", "password", "edge-auth-secret"), "seed", "edge-default-secret"),
			Infra: om("pool_size", 10, "api_key", "edge-infra-secret"),
			SensitivePayloadLeaves: []string{
				"values.token", "values.auth.password", "values.seed", "infra.api_key",
			},
		},
		{
			Source: "app", Target: "db", Socket: "caches", Alias: "control",
			SocketDeclaration: types.SocketDeclarationDeclared,
			Values:            om("token", "control-token-readable"),
		},
	}
	return g
}

// edgeByAlias returns one edge of a graph by its alias, failing when it is absent.
func edgeByAlias(t *testing.T, g *types.IRGraph, alias string) types.IREdge {
	t.Helper()
	for _, e := range g.Spec.Edges {
		if e.Alias == alias {
			return e
		}
	}
	t.Fatalf("the graph carries no edge with alias %q", alias)
	return types.IREdge{}
}

// nestedGet reads one key of a nested ordered map, failing when the value is not
// one.
func nestedGet(t *testing.T, m types.OrderedMap[any], key, sub string) any {
	t.Helper()
	raw, _ := m.Get(key)
	inner, ok := raw.(types.OrderedMap[any])
	if !ok {
		t.Fatalf("%s is %T, want an ordered map", key, raw)
	}
	v, _ := inner.Get(sub)
	return v
}

// TestGraph_ConnectionConfigurationWithholdsMarkedLeaves is the edge's own
// position: every leaf of the connection's values and infra the edge's marking
// names arrives as the withheld marker, and every sibling arrives as written.
func TestGraph_ConnectionConfigurationWithholdsMarkedLeaves(t *testing.T) {
	t.Parallel()
	out := redact.Graph(connectionGraph(t))
	edge := edgeByAlias(t, out, "primary")

	token, _ := edge.Values.Get("token")
	assertWithheldGo(t, "edge values.token (declared at the leaf)", token, redact.WithheldDeclared)
	assertWithheldGo(t, "edge values.auth.password (a leaf beneath a values object)",
		nestedGet(t, edge.Values, "auth", "password"), redact.WithheldDeclared)
	seed, _ := edge.Values.Get("seed")
	assertWithheldGo(t, "edge values.seed (a declared default the edge carries)", seed, redact.WithheldDeclared)
	apiKey, _ := edge.Infra.Get("api_key")
	assertWithheldGo(t, "edge infra.api_key (the infra pillar)", apiKey, redact.WithheldDeclared)

	if got, _ := edge.Values.Get("endpoint"); got != "db.internal" {
		t.Errorf("edge values.endpoint is %#v, want the authored value; nothing marks it", got)
	}
	if got := nestedGet(t, edge.Values, "auth", "user"); got != "app" {
		t.Errorf("edge values.auth.user is %#v, want the authored value; only its sibling is marked", got)
	}
	if got, _ := edge.Infra.Get("pool_size"); got != 10 {
		t.Errorf("edge infra.pool_size is %#v, want the authored value", got)
	}
	if got := edge.SensitivePayloadLeaves; len(got) != 4 {
		t.Errorf("the edge's marking is %v, want it carried unchanged beside the withheld values", got)
	}
}

// TestGraph_ConnectionAuthoredUsesEntryWithholdsMarkedLeaves is the second
// position the same configuration sits at: the consuming node's authored `uses`
// entry for the connection. It carries no marking of its own, so it is withheld
// by the marking of the edge the entry drew — and an entry of the same node whose
// edge marks nothing, writing a leaf of the same name, is shown as written.
func TestGraph_ConnectionAuthoredUsesEntryWithholdsMarkedLeaves(t *testing.T) {
	t.Parallel()
	out := redact.Graph(connectionGraph(t))
	node, ok := out.Spec.Nodes.Get("app")
	if !ok {
		t.Fatal("the result carries no node app")
	}

	socket, ok := node.Uses.Get("databases")
	if !ok {
		t.Fatal("the node's uses block lost the databases socket")
	}
	entry, ok := socket.Verbose.Get("primary")
	if !ok {
		t.Fatal("the databases socket lost its primary entry")
	}
	token, _ := entry.Values.Get("token")
	assertWithheldGo(t, "uses databases/primary values.token", token, redact.WithheldDeclared)
	assertWithheldGo(t, "uses databases/primary values.auth.password",
		nestedGet(t, entry.Values, "auth", "password"), redact.WithheldDeclared)
	if got, _ := entry.Values.Get("endpoint"); got != "db.internal" {
		t.Errorf("uses databases/primary values.endpoint is %#v, want the authored value", got)
	}
	if entry.Target != "db" {
		t.Errorf("uses databases/primary target is %q, want the authored target kept", entry.Target)
	}

	control, _ := node.Uses.Get("caches")
	controlEntry, ok := control.Verbose.Get("control")
	if !ok {
		t.Fatal("the caches socket lost its control entry")
	}
	if got, _ := controlEntry.Values.Get("token"); got != "control-token-readable" {
		t.Errorf("uses caches/control values.token is %#v, want the authored value: its edge marks nothing, "+
			"and a leaf is withheld because a declaration covers it, never because of its name", got)
	}

	// The control edge's own payload is untouched for the same reason.
	controlEdge := edgeByAlias(t, out, "control")
	if got, _ := controlEdge.Values.Get("token"); got != "control-token-readable" {
		t.Errorf("edge control values.token is %#v, want the authored value", got)
	}
}

// TestGraph_ConnectionWithholdingLeavesTheInputAlone keeps the withholding with
// the surface handing the graph out: the edges and the node the result withholds
// are copies, so the graph the build renders from still carries every value.
func TestGraph_ConnectionWithholdingLeavesTheInputAlone(t *testing.T) {
	t.Parallel()
	in := connectionGraph(t)
	before, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	_ = redact.Graph(in)
	after, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal input after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("the input graph was mutated.\n  before: %s\n  after:  %s", before, after)
	}
}

// TestGraph_UnmarkedConnectionsAreByteIdentical is the non-regression half: a
// graph whose edges mark nothing emits exactly the bytes it emitted before the
// connection's configuration was withheld at all — its edges, and the node's
// authored block, configuration included.
func TestGraph_UnmarkedConnectionsAreByteIdentical(t *testing.T) {
	t.Parallel()
	in := connectionGraph(t)
	for i := range in.Spec.Edges {
		in.Spec.Edges[i].SensitivePayloadLeaves = nil
	}
	want, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	got, err := json.Marshal(redact.Graph(in))
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("a graph marking no connection was rewritten.\n  got:  %s\n  want: %s", got, want)
	}
}

// TestConnectionOf_SplitsTheMarkingByPillar pins the one reading a surface
// displaying a connection outside the graph shares with the graph's withholding.
func TestConnectionOf_SplitsTheMarkingByPillar(t *testing.T) {
	t.Parallel()
	got := redact.ConnectionOf(types.IREdge{SensitivePayloadLeaves: []string{"values.a.b", "infra.c", "provides.d"}})
	if len(got.Values) != 1 || !got.Values["a.b"] {
		t.Errorf("values paths = %v, want exactly a.b", got.Values)
	}
	if len(got.Infra) != 1 || !got.Infra["c"] {
		t.Errorf("infra paths = %v, want exactly c; a coordinate under any other pillar names nothing here", got.Infra)
	}
	if (redact.ConnectionOf(types.IREdge{})).Any() {
		t.Error("an edge marking nothing reported a marked leaf")
	}
}

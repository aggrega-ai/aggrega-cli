// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// connectionProbePolicy reports every string a connection's configuration holds,
// at both positions the graph carries it — the edge's own values and infra, and
// the consuming node's authored `uses` entry — verbatim, so a value reaching the
// plugin unwithheld appears in a verdict whatever leaf it sits at; every leaf that
// arrived as the withheld marker, with its reason; and the edge's marking, which
// is a fact a policy reasons on.
const connectionProbePolicy = `package connection

import rego.v1

warn contains msg if {
	some e in input.graph.spec.edges
	some pillar in ["values", "infra"]
	some key, v in object.get(e, pillar, {})
	is_string(v)
	msg := sprintf("edge %v/%v %v.%v = %v", [e.source, e.alias, pillar, key, v])
}

warn contains msg if {
	some e in input.graph.spec.edges
	some pillar in ["values", "infra"]
	some key, v in object.get(e, pillar, {})
	is_object(v)
	reason := v["WITHHELD_MARKER_KEY"]
	msg := sprintf("edge %v/%v %v.%v withheld: %v", [e.source, e.alias, pillar, key, reason])
}

warn contains msg if {
	some e in input.graph.spec.edges
	leaves := object.get(e, "sensitive_payload_leaves", [])
	count(leaves) > 0
	msg := sprintf("edge %v/%v marks %v", [e.source, e.alias, concat(",", leaves)])
}

warn contains msg if {
	some node_id, n in input.graph.spec.nodes
	some socket, entries in object.get(n, "uses", {})
	is_object(entries)
	some alias, entry in entries
	is_object(entry)
	some key, v in object.get(entry, "values", {})
	is_string(v)
	msg := sprintf("node %v uses %v/%v values.%v = %v", [node_id, socket, alias, key, v])
}

warn contains msg if {
	some node_id, n in input.graph.spec.nodes
	some socket, entries in object.get(n, "uses", {})
	is_object(entries)
	some alias, entry in entries
	is_object(entry)
	some key, v in object.get(entry, "values", {})
	is_object(v)
	reason := v["WITHHELD_MARKER_KEY"]
	msg := sprintf("node %v uses %v/%v values.%v withheld: %v", [node_id, socket, alias, key, reason])
}
`

// connectionWithholdingGraph carries one consuming node with two connections to
// one target, set as the graph build sets them: a connection whose edge marks a
// values leaf and an infra leaf of its configuration, each beside a readable
// sibling, and a control whose edge marks nothing and whose configuration writes a
// leaf of the same name. The node's authored `uses` block writes the values of
// both connections, as an author does.
func connectionWithholdingGraph() *types.IRGraph {
	pillar := func(kv ...string) types.OrderedMap[any] {
		m := types.OrderedMap[any]{}
		for i := 0; i+1 < len(kv); i += 2 {
			m.Set(kv[i], kv[i+1])
		}
		return m
	}

	uses := types.OrderedMap[types.UsesSocketValue]{}
	databases := types.UsesSocketValue{}
	databases.Verbose.Set("primary", types.VerboseUsesEntry{
		Target: "db",
		Values: pillar("endpoint", "db.internal", "token", "planted-connection-token"),
	})
	uses.Set("databases", databases)
	caches := types.UsesSocketValue{}
	caches.Verbose.Set("control", types.VerboseUsesEntry{
		Target: "db",
		Values: pillar("token", "readable-control-token"),
	})
	uses.Set("caches", caches)

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("app", types.IRNode{Component: "hand/app", ResolvedVersion: "1.0.0", Uses: uses})
	nodes.Set("db", types.IRNode{Component: "hand/db", ResolvedVersion: "1.0.0"})

	g := &types.IRGraph{APIVersion: types.IRAPIVersion, Kind: types.IRKind}
	g.Spec.Provenance = types.IRProvenance{SchemaVersion: types.IRAPIVersion}
	g.Spec.Nodes = nodes
	g.Spec.Edges = []types.IREdge{
		{
			Source: "app", Target: "db", Socket: "databases", Alias: "primary",
			SocketDeclaration:      types.SocketDeclarationDeclared,
			Values:                 pillar("endpoint", "db.internal", "token", "planted-connection-token"),
			Infra:                  pillar("tier", "small", "api_key", "planted-connection-infra"),
			SensitivePayloadLeaves: []string{"values.token", "infra.api_key"},
		},
		{
			Source: "app", Target: "db", Socket: "caches", Alias: "control",
			SocketDeclaration: types.SocketDeclarationDeclared,
			Values:            pillar("token", "readable-control-token"),
		},
	}
	return g
}

// TestPolicySocketWithholdsAConnectionsDeclaredSensitiveConfiguration proves a
// governance plugin receives a connection's configuration with every leaf the
// edge's marking names withheld, at both positions the graph carries it, while the
// readable leaves, the control connection's same-named leaf and the marking itself
// still reach it. The policy is read by the engine the compiler ships, so what a
// verdict quotes is what crossed the socket.
func TestPolicySocketWithholdsAConnectionsDeclaredSensitiveConfiguration(t *testing.T) {
	t.Parallel()

	g := connectionWithholdingGraph()
	analysis := analyzer.Analyze(g)
	cfg, lookup := withholdingAttachment(t, connectionProbePolicy, nil)
	inputs := Inputs{WorkspaceConfig: cfg, PluginPathLookup: lookup, PluginPins: pluginpath.NoPins()}
	if err := runPolicySocket(context.Background(), inputs, resolver.CatalogLookup{}, analysis); err != nil {
		t.Fatalf("policy socket: %v", err)
	}
	verdicts := withholdingVerdicts(analysis.Warnings)

	planted := []string{"planted-connection-token", "planted-connection-infra"}
	for _, value := range planted {
		for _, v := range verdicts {
			if strings.Contains(v, value) {
				t.Errorf("the plugin received the declared-sensitive connection value %q unwithheld; its verdict quoted it:\n  %s", value, v)
			}
		}
	}

	want := []string{
		"edge app/primary values.endpoint = db.internal",
		"edge app/primary values.token withheld: declared",
		"edge app/primary infra.tier = small",
		"edge app/primary infra.api_key withheld: declared",
		"edge app/primary marks values.token,infra.api_key",
		"edge app/control values.token = readable-control-token",
		"node app uses databases/primary values.endpoint = db.internal",
		"node app uses databases/primary values.token withheld: declared",
		"node app uses caches/control values.token = readable-control-token",
	}
	slices.Sort(want)
	if !slices.Equal(verdicts, want) {
		t.Errorf("the plugin's verdicts differ from what the withheld payload carries.\n  missing: %v\n  unexpected: %v",
			setDifference(want, verdicts), setDifference(verdicts, want))
	}

	// The graph the build goes on to read is not the plugin's copy.
	graph, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal the graph: %v", err)
	}
	for _, value := range planted {
		if !strings.Contains(string(graph), value) {
			t.Errorf("the graph the run holds after the dispatch no longer carries %q; the withholding reached the graph the render reads", value)
		}
	}
}

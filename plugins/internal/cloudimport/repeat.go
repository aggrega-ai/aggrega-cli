// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"maps"
	"sort"
	"strconv"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// Expand writes each repeated node of g as the nodes it deploys. Where the
// source states the instances literally, the node is replaced by one node per
// key: the node Repetition.Instance returns for the key, its ID "<id>-<key>",
// or where a node of g or an instance expanded before it takes that ID,
// "<id>-<key>-<n>", the least n from 2 none takes, the instances expanded in
// the order of g.Nodes, so the IDs are the same on every run; the node takes
// the repeated node's Source where it names none; and a dependency of
// any node on the repeated node becomes a dependency on each of its instances.
// Where the literal keys are none, the source deploys no instance: the node is
// removed, nothing is drafted for it, and a resource-not-carried warning names
// it. Where the source computes the instances when it deploys, the node stays
// one node, with a repetition-not-expanded warning naming it. A Ref or
// Credential elsewhere in g that names a repeated node keeps naming that
// node's own ID; g.Expanded records the IDs of the instances each node
// replaced or removed deploys, none for a removed node, and a reader of the
// edges resolves such an ID to them. No node of g repeats afterwards, and
// g.Nodes stays ordered by ID.
func Expand(g *Graph) []aggrega.Diagnostic {
	if g == nil {
		return nil
	}
	var diags []aggrega.Diagnostic
	var nodes []*Node
	instances := map[string][]string{}
	taken := map[string]bool{}
	for _, n := range g.Nodes {
		if n != nil {
			taken[n.ID] = true
		}
	}
	for _, n := range g.Nodes {
		switch {
		case n == nil || n.Repeat == nil:
			nodes = append(nodes, n)
		case n.Repeat.Keys == nil:
			n.Repeat = nil
			nodes = append(nodes, n)
			diags = append(diags, repetitionNotExpanded(n))
		case len(n.Repeat.Keys) == 0:
			instances[n.ID] = nil
			diags = append(diags, noInstance(n))
		default:
			ids := make([]string, 0, len(n.Repeat.Keys))
			for _, key := range n.Repeat.Keys {
				inst := instance(n, key, taken)
				ids = append(ids, inst.ID)
				nodes = append(nodes, inst)
			}
			instances[n.ID] = ids
		}
	}
	if len(instances) == 0 {
		return diags
	}
	if g.Expanded == nil {
		g.Expanded = make(map[string][]string, len(instances))
	}
	maps.Copy(g.Expanded, instances)
	for _, n := range nodes {
		if n != nil {
			n.DependsOn = dependencies(n.DependsOn, instances)
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool { return idOf(nodes[i]) < idOf(nodes[j]) })
	g.Nodes = nodes
	return diags
}

// instance returns the node n deploys for key: a node of its own, so no two
// instances share one, with no repetition left, its ID one taken does not
// hold, which it records there.
func instance(n *Node, key string, taken map[string]bool) *Node {
	var inst Node
	if made := callInstance(n.Repeat, key); made != nil {
		inst = *made
	} else {
		inst = *n
	}
	id := n.ID + "-" + key
	inst.ID = id
	for i := 2; taken[inst.ID]; i++ {
		inst.ID = id + "-" + strconv.Itoa(i)
	}
	taken[inst.ID] = true
	inst.Repeat = nil
	if inst.Source == "" {
		inst.Source = n.Source
	}
	return &inst
}

// callInstance returns r's node for key, nil where r has no Instance.
func callInstance(r *Repetition, key string) *Node {
	if r.Instance == nil {
		return nil
	}
	return r.Instance(key)
}

// dependencies returns deps with every ID of an expanded node replaced by the
// IDs of its instances.
func dependencies(deps []string, instances map[string][]string) []string {
	var out []string
	for i, dep := range deps {
		ids, expanded := instances[dep]
		if expanded && out == nil {
			out = append(make([]string, 0, len(deps)+len(ids)), deps[:i]...)
		}
		switch {
		case expanded:
			out = append(out, ids...)
		case out != nil:
			out = append(out, dep)
		}
	}
	if out == nil {
		return deps
	}
	return out
}

// idOf returns n's ID, empty for no node.
func idOf(n *Node) string {
	if n == nil {
		return ""
	}
	return n.ID
}

// noInstance warns that n's repetition deploys no instance, so nothing is
// drafted for it.
func noInstance(n *Node) aggrega.Diagnostic {
	d := Report(CodeResourceNotCarried, n.ID+" repeats over no key, so the source deploys no instance of it and "+
		"nothing is drafted for it: where it is wanted, write an instance of it in the Aggregate")
	d.SourceRef = n.Source
	return d
}

// repetitionNotExpanded warns that n is drafted once.
func repetitionNotExpanded(n *Node) aggrega.Diagnostic {
	d := Report(CodeRepetitionNotExpanded, n.ID+" is repeated as many times as the source computes when it "+
		"deploys, and is drafted once: write in the Aggregate an instance for each further copy the source deploys")
	d.SourceRef = n.Source
	return d
}

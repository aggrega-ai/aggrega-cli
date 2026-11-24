// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"math/rand"
	"reflect"
	"testing"
)

// ids returns the IDs of nodes, in order.
func ids(nodes []*Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}

func TestOrderBreaksCyclesAtTheSmallestId(t *testing.T) {
	t.Run("a self-reference", func(t *testing.T) {
		// z reads its own identifier, and a consumes z: z still comes first.
		g := &Graph{Nodes: []*Node{
			{ID: "a", DependsOn: []string{"z"}},
			{ID: "z", Body: []Prop{{Name: "self", Value: Ref{Node: "z"}}}},
		}}
		if got, want := ids(Order(g)), []string{"z", "a"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("order = %q, want %q", got, want)
		}
	})
	t.Run("two nodes naming each other", func(t *testing.T) {
		// b and m name each other and a consumes b: the cycle is broken at b, its
		// smallest ID, not at a, the graph's smallest, which waits on the cycle.
		g := &Graph{Nodes: []*Node{
			{ID: "a", Body: []Prop{{Name: "host", Value: Ref{Node: "b", Attr: []string{"host"}}}}},
			{ID: "b", DependsOn: []string{"m"}},
			{ID: "m", DependsOn: []string{"b"}},
		}}
		if got, want := ids(Order(g)), []string{"b", "a", "m"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("order = %q, want %q", got, want)
		}
	})
}

func TestOrderIsStable(t *testing.T) {
	nodes := []*Node{
		{ID: "vnet"},
		{ID: "subnet", DependsOn: []string{"vnet"}},
		{ID: "db", Body: []Prop{{Name: "subnet", Value: Ref{Node: "subnet"}}}},
		{ID: "kv"},
		{ID: "farm"},
		{ID: "app", Body: []Prop{
			{Name: "host", Value: Ref{Node: "db", Attr: []string{"fqdn"}}},
			{Name: "secret", Value: Credential{Node: "kv", What: "listSecrets"}},
		}, DependsOn: []string{"farm"}},
		{ID: "x", DependsOn: []string{"y"}},
		{ID: "y", DependsOn: []string{"x"}, Body: []Prop{{Name: "app", Value: Ref{Node: "app"}}}},
	}
	want := []string{"farm", "kv", "vnet", "subnet", "db", "app", "x", "y"}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 10; i++ {
		shuffled := append([]*Node(nil), nodes...)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if got := ids(Order(&Graph{Nodes: shuffled})); !reflect.DeepEqual(got, want) {
			t.Fatalf("shuffle %d (%q): order = %q, want %q", i, ids(shuffled), got, want)
		}
	}
}

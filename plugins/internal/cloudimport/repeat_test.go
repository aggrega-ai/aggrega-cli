// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// repeated returns a node db repeating over keys, each instance's Body holding
// its key under index.
func repeated(keys []string) *Node {
	return &Node{ID: "db", Type: "server", Source: "main.json#/resources/db", Repeat: &Repetition{
		Keys: keys,
		Instance: func(key string) *Node {
			return &Node{ID: "db", Type: "server", Body: []Prop{{Name: "index", Value: Literal{JSON: json.RawMessage(strconv.Quote(key))}}}}
		},
	}}
}

func TestExpandLiteralKeys(t *testing.T) {
	g := &Graph{Nodes: []*Node{{ID: "app"}, repeated([]string{"0", "1", "2"})}}
	if diags := Expand(g); len(diags) != 0 {
		t.Fatalf("diagnostics = %+v", diags)
	}
	if got, want := ids(g.Nodes), []string{"app", "db-0", "db-1", "db-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nodes = %q, want %q", got, want)
	}
	for i, n := range g.Nodes[1:] {
		key := strconv.Itoa(i)
		if n.Repeat != nil {
			t.Fatalf("%s still repeats", n.ID)
		}
		if want := []Prop{{Name: "index", Value: Literal{JSON: json.RawMessage(strconv.Quote(key))}}}; !reflect.DeepEqual(n.Body, want) {
			t.Fatalf("%s body = %+v, want the instance of key %s", n.ID, n.Body, key)
		}
	}
}

func TestExpandRewritesDependenciesOnTheRepeatedNode(t *testing.T) {
	g := &Graph{Nodes: []*Node{{ID: "app", DependsOn: []string{"net", "db"}}, repeated([]string{"0", "1"})}}
	Expand(g)
	if got, want := g.Nodes[0].DependsOn, []string{"net", "db-0", "db-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("app depends on %q, want %q", got, want)
	}
}

func TestExpandComputedWarns(t *testing.T) {
	db := repeated(nil)
	db.Repeat.Instance = func(string) *Node {
		t.Fatal("an instance was drafted for a repetition the source computes")
		return nil
	}
	g := &Graph{Nodes: []*Node{db}}
	diags := Expand(g)
	if len(diags) != 1 || diags[0].Code != string(CodeRepetitionNotExpanded) || diags[0].Severity != aggrega.SeverityWarn {
		t.Fatalf("diagnostics = %+v, want one repetition-not-expanded warning", diags)
	}
	if diags[0].SourceRef != db.Source {
		t.Fatalf("source ref = %q, want %q", diags[0].SourceRef, db.Source)
	}
	if got := ids(g.Nodes); !reflect.DeepEqual(got, []string{"db"}) || g.Nodes[0].Repeat != nil {
		t.Fatalf("nodes = %q, repeat = %+v; want db once, no longer repeating", got, g.Nodes[0].Repeat)
	}
}

func TestExpandZeroInstancesReportsTheResource(t *testing.T) {
	db := repeated([]string{})
	g := &Graph{Nodes: []*Node{{ID: "app"}, db}}
	diags := Expand(g)
	if got := ids(g.Nodes); !reflect.DeepEqual(got, []string{"app"}) {
		t.Fatalf("nodes = %q, want db absent", got)
	}
	if len(diags) != 1 || diags[0].Code != string(CodeResourceNotCarried) || diags[0].Severity != aggrega.SeverityWarn ||
		!strings.Contains(diags[0].Message, "db") || diags[0].SourceRef != db.Source {
		t.Fatalf("diagnostics = %+v, want one resource-not-carried warning naming db", diags)
	}
}

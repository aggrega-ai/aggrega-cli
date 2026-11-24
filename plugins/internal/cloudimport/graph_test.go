// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEdgesFromRefsCredentialsAndDependencies(t *testing.T) {
	app := &Node{
		ID: "app",
		Body: []Prop{
			{Name: "settings", Props: []Prop{
				{Name: "tier", Value: Literal{JSON: json.RawMessage(`"basic"`)}},
				{Name: "connection", Items: []Prop{
					{Value: Param{Name: "region"}},
					{Value: Compose{Parts: []Value{
						Literal{JSON: json.RawMessage(`"host="`)},
						Ref{Node: "db", Attr: []string{"properties", "fqdn"}},
					}}},
				}},
			}},
			{Name: "key", Value: Credential{Node: "store", What: "listKeys"}},
			{Name: "zone", Value: Opaque{Text: "[resourceGroup().location]", Target: "arm"}},
		},
		DependsOn: []string{"net"},
	}
	g := &Graph{Nodes: []*Node{{ID: "store"}, app, {ID: "db"}, {ID: "net"}}}

	want := []Edge{
		{From: "app", To: "db", Kind: EdgeRef, Path: []string{"settings", "connection", "1"},
			Attr: []string{"properties", "fqdn"}},
		{From: "app", To: "store", Kind: EdgeCredential, Path: []string{"key"}},
		{From: "app", To: "net", Kind: EdgeDepends},
	}
	if got := Edges(g); !reflect.DeepEqual(got, want) {
		t.Fatalf("edges = %+v, want %+v", got, want)
	}
}

func TestKeysAreUniqueAndNonEmpty(t *testing.T) {
	nodes := []*Node{{ID: "db"}, {ID: "DB"}, {ID: "--"}}
	want := []string{"db", "db-2", "node"}
	if got := Keys(nodes); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %q, want %q", got, want)
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// words writes an ID as its words, lowercased and joined by hyphens: a word
// begins at an upper-case letter following a lower-case one.
func words(id string) string {
	var b strings.Builder
	prev := rune(0)
	for _, r := range id {
		if unicode.IsUpper(r) && unicode.IsLower(prev) {
			b.WriteByte('-')
		}
		b.WriteRune(unicode.ToLower(r))
		prev = r
	}
	return b.String()
}

// TestFormatKeyIsAppliedThenUniqued gives each instance the key the format's
// Key gives its ID, then made unique as every importer's keys are: a Key
// giving Db and DB one key keys them db and db-2, and one giving OrdersDb
// orders-db keys it so, its instance named by its ID in the comment above it.
// A format with no Key keeps the keys Keys gives byte for byte.
func TestFormatKeyIsAppliedThenUniqued(t *testing.T) {
	keyed := Format{Key: words}
	if got, want := keyed.Keys([]*Node{{ID: "Db"}, {ID: "DB"}}), []string{"db", "db-2"}; !slices.Equal(got, want) {
		t.Errorf("the keys of Db and DB are %q, want %q", got, want)
	}

	nodes := []*Node{{ID: "Db"}, {ID: "DB"}, {ID: "OrdersDb"}, nil, {ID: ""}, {ID: "orders db"}}
	if got, want := Keys(nodes), []string{"db", "db-2", "ordersdb", "node", "node-2", "orders-db"}; !slices.Equal(got,
		want) {
		t.Fatalf("Keys gives %q, want %q", got, want)
	}
	if got, want := (Format{}).Keys(nodes), Keys(nodes); !slices.Equal(got, want) {
		t.Errorf("a format with no Key gives %q, want the keys Keys gives, %q", got, want)
	}

	f := toyFormat(nil)
	f.Key = words
	f.Read = func(aggrega.ImportInput, Settings, ParseFunc) (*Graph, []aggrega.Diagnostic) {
		var g Graph
		for _, id := range []string{"DB", "Db", "OrdersDb"} {
			g.Nodes = append(g.Nodes, &Node{ID: id, Type: toyServer, Form: FormRaw,
				Body: []Prop{{Name: "version", Value: text("16")}}})
		}
		return &g, nil
	}
	out := Import(toyInput(t, "toy", "", []string{"main.json"}), f, toyParse)
	noHalt(t, out.Diagnostics)
	aggregate := fileOf(t, out, "aggregate.yaml")
	for key, id := range map[string]string{"db": "DB", "db-2": "Db", "orders-db": "OrdersDb"} {
		if block := instanceBlock(t, aggregate, key); !strings.HasPrefix(block, "    # The resource "+id+".\n") {
			t.Errorf("the instance %s does not name the resource %s above it:\n%s", key, id, block)
		}
	}
	if release := fileOf(t, out, "release.yaml"); !strings.Contains(release, "    orders-db:") {
		t.Errorf("the Release does not key OrdersDb by its format's key:\n%s", release)
	}
}

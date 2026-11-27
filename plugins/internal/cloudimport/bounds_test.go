// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"testing"
)

// TestSizeMeasuresTextAndParts measures each kind of value by the bytes of
// the text and JSON it holds, one more for each part of composed text, and a
// property by its value and one more for each member and item.
func TestSizeMeasuresTextAndParts(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		v    Value
		want int
	}{
		{Literal{JSON: json.RawMessage(`"abc"`)}, 5},
		{Param{Name: "Env"}, 3},
		{Ref{Node: "Db", Attr: []string{"Endpoint", "Port"}}, 14},
		{Credential{Node: "Db", What: "SecretString"}, 14},
		{Opaque{Text: "!Ref AWS::Region", Target: "cloudformation", Params: []string{"Env"}}, 16},
		{Compose{Parts: []Value{Param{Name: "Env"}, Text("-net")}}, 1 + 3 + 1 + 6},
		{Stored{Store: "ssm-secure", Locator: "/db", Text: "{{resolve:ssm-secure:/db}}"}, 0},
		{nil, 0},
	} {
		if got := Size(c.v); got != c.want {
			t.Errorf("Size(%#v) = %d, want %d", c.v, got, c.want)
		}
	}
	p := Prop{Name: "Tags", Props: []Prop{{Name: "a", Value: Text("x")}, {Name: "b", Items: []Prop{{Value: Text("yz")}}}}}
	if got, want := PropSize(p), (1+3)+(1+(1+4)); got != want {
		t.Errorf("PropSize = %d, want %d", got, want)
	}
}

// TestBudgetSpendsToMaxExpansion spends expansions up to MaxExpansion and
// refuses past it, recording nothing for a refused spend.
func TestBudgetSpendsToMaxExpansion(t *testing.T) {
	t.Parallel()
	var b Budget
	if !b.Spend(MaxExpansion - 1) {
		t.Fatalf("a budget refuses %d expansions", MaxExpansion-1)
	}
	if b.Spend(2) {
		t.Errorf("a budget spends past MaxExpansion")
	}
	if !b.Spend(1) {
		t.Errorf("a refused spend recorded expansions: the last one within the bound is refused")
	}
	if b.Spend(1) {
		t.Errorf("a budget spends past MaxExpansion")
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armexpr

import "testing"

func TestParsePrintRoundTrip(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"[concat('a', parameters('p'))]", "[concat('a', parameters('p'))]"},
		{"[reference('storageAccount').primaryEndpoints.blob]", "[reference('storageAccount').primaryEndpoints.blob]"},
		{"[ reference('storageAccount') ]", "[reference('storageAccount')]"},
		{"[parameters('arr')[0]]", "[parameters('arr')[0]]"},
		{"[format('{0}''s', 'x')]", "[format('{0}''s', 'x')]"},
		{"[my.fn(1)]", "[my.fn(1)]"},
		{"[createArray(1, 2, 3)]", "[createArray(1, 2, 3)]"},
		{"[lambda('x', lambdaVariables('x'))]", "[lambda('x', lambdaVariables('x'))]"},
		{"[if(true(), 'a', 'b')]", "[if(true(), 'a', 'b')]"},
		{"[concat('a','b' ,'c')]", "[concat('a', 'b', 'c')]"},
	} {
		n, err := Parse(tc.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.in, err)
			continue
		}
		if got := Expression(n); got != tc.want {
			t.Errorf("Expression(Parse(%q)) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPrintEscapesQuotes(t *testing.T) {
	if got := Print(String{Value: "a'b"}); got != "'a''b'" {
		t.Errorf("Print = %q", got)
	}
}

func TestWalkVisitsInAuthorOrderAndSkips(t *testing.T) {
	n, err := Parse("[concat(parameters('a'), reference('b').c, 'd'[1])]")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	Walk(n, func(x Node) bool {
		if c, ok := x.(Call); ok {
			names = append(names, c.Name)
		}
		return true
	})
	want := []string{"concat", "parameters", "reference"}
	if len(names) != len(want) {
		t.Fatalf("calls = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("calls = %v, want %v", names, want)
		}
	}
	count := 0
	Walk(n, func(Node) bool { count++; return false })
	if count != 1 {
		t.Errorf("a false visit visited %d nodes, want 1", count)
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armexpr

import "testing"

// FuzzParse asserts that Parse never panics and that a successful parse is
// stable: printing the tree and parsing the result prints identically.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"[concat('a', parameters('p'))]",
		"[reference('storageAccount').primaryEndpoints.blob]",
		"[ reference('storageAccount') ]",
		"[parameters('arr')[0]]",
		"[format('{0}''s', 'x')]",
		"[my.fn(1)]",
		"[createArray(1, 2, -3)]",
		"[lambda('x', lambdaVariables('x'))]",
		"[if(true(), 'a', 'b')]",
		"[prod]", "[[x]", "[", "[]", "['open]", "[f(]", "[f(a,)]", "[f()]]",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n, err := Parse(s)
		if err != nil {
			if _, ok := err.(*SyntaxError); !ok {
				t.Fatalf("Parse(%q) error %T, want *SyntaxError", s, err)
			}
			return
		}
		again, err := Parse(Expression(n))
		if err != nil {
			t.Fatalf("Parse(Expression(Parse(%q))) failed: %v", s, err)
		}
		if Print(again) != Print(n) {
			t.Fatalf("print is not stable for %q: %q then %q", s, Print(n), Print(again))
		}
		for i := 0; i < len(s); i++ {
			if s[i] == '[' {
				ParseSpan(s, i)
			}
		}
	})
}

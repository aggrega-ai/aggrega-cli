// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package jsonc

import "testing"

// TestStrip writes what JSON with comments admits and JSON does not as spaces,
// keeping every line break and every other byte where it is.
func TestStrip(t *testing.T) {
	t.Parallel()
	for _, row := range []struct{ name, in, want string }{
		{"json already", `{"a": [1, 2], "b": "c"}`, `{"a": [1, 2], "b": "c"}`},
		{"line comment", "{\n// note\n\"a\": 1\n}", "{\n       \n\"a\": 1\n}"},
		{"block comment", "{/* a\nb */\"a\": 1}", "{    \n    \"a\": 1}"},
		{"comment markers inside a string", `{"a": "x // y /* z */"}`, `{"a": "x // y /* z */"}`},
		{"escaped quote before a comment marker", `{"a": "q\" // r"}`, `{"a": "q\" // r"}`},
		{"trailing commas", "{\"a\": [1, 2,], \"b\": 3,\n}", "{\"a\": [1, 2 ], \"b\": 3 \n}"},
		{"trailing comma before a comment", "{\"a\": 1, // c\n}", "{\"a\": 1      \n}"},
		{"a comma inside a string", `{"a": "1,]"}`, `{"a": "1,]"}`},
		{"unterminated block comment", `{"a": 1} /* open`, `{"a": 1} /* open`},
	} {
		if got := string(Strip([]byte(row.in))); got != row.want {
			t.Errorf("%s: Strip(%q) = %q, want %q", row.name, row.in, got, row.want)
		}
	}
}

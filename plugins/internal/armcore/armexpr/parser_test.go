// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armexpr

import (
	"errors"
	"testing"
)

func TestIsExpression(t *testing.T) {
	for s, want := range map[string]bool{
		"[x]":  true,
		"[[x]": false,
		"[x":   false,
		"x]":   false,
		"[":    false,
		"":     false,
	} {
		if got := IsExpression(s); got != want {
			t.Errorf("IsExpression(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestParseRefusesWithOffset(t *testing.T) {
	for _, tc := range []struct {
		in     string
		offset int
	}{
		{"[concat('a', ]", 13},
		{"[foo(]", 5},
		{"['open]", 1},
		{"[prod]", 5},
		{"[foo() bar]", 7},
		{"[foo()]]", 7},
	} {
		_, err := Parse(tc.in)
		var se *SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("Parse(%q) error = %v, want *SyntaxError", tc.in, err)
			continue
		}
		if se.Offset != tc.offset {
			t.Errorf("Parse(%q) offset = %d (%s), want %d", tc.in, se.Offset, se.Reason, tc.offset)
		}
	}
}

func TestParseReadsStructure(t *testing.T) {
	n, err := Parse("[ my.fn(1, 'it''s').a[0] ]")
	if err != nil {
		t.Fatal(err)
	}
	idx, ok := n.(Index)
	if !ok {
		t.Fatalf("root = %T, want Index", n)
	}
	mem, ok := idx.Target.(Member)
	if !ok || mem.Name != "a" {
		t.Fatalf("index target = %#v, want Member a", idx.Target)
	}
	call, ok := mem.Target.(Call)
	if !ok || call.Namespace != "my" || call.Name != "fn" || len(call.Args) != 2 {
		t.Fatalf("member target = %#v, want my.fn with two arguments", mem.Target)
	}
	if s, ok := call.Args[1].(String); !ok || s.Value != "it's" {
		t.Fatalf("second argument = %#v, want the unescaped string", call.Args[1])
	}
}

func TestParseSpanFindsOnlyCompleteCalls(t *testing.T) {
	text := "postgres://[reference('db').outputs.fqdn.value]:5432"
	open := 11
	n, end, ok := ParseSpan(text, open)
	if !ok {
		t.Fatal("ParseSpan found nothing in the connection string")
	}
	if got := text[end:]; got != ":5432" {
		t.Errorf("text after the span = %q, want :5432", got)
	}
	if got := Print(n); got != "reference('db').outputs.fqdn.value" {
		t.Errorf("Print = %q", got)
	}
	for _, lit := range []string{"array[0]", "[a-z]", "[test] value", "[ -f x ]", "[[x]", "['a']", "[1]"} {
		for i := 0; i < len(lit); i++ {
			if lit[i] != '[' {
				continue
			}
			if _, _, ok := ParseSpan(lit, i); ok {
				t.Errorf("ParseSpan(%q, %d) found an expression", lit, i)
			}
		}
	}
}

func TestParseDepthIsBounded(t *testing.T) {
	deep := "[" + repeat("f(", 5000) + repeat(")", 5000) + "]"
	_, err := Parse(deep)
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("Parse(deeply nested) error = %v, want *SyntaxError", err)
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

func TestParseAccessorChainsAreBounded(t *testing.T) {
	for name, chain := range map[string]string{
		"members": repeat(".a", 100000),
		"indexes": repeat("[0]", 100000),
	} {
		_, err := Parse("[f()" + chain + "]")
		var se *SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("%s: Parse(long chain) error = %v, want *SyntaxError", name, err)
		}
	}
	n, err := Parse("[f()" + repeat(".a", 50) + "]")
	if err != nil {
		t.Fatal(err)
	}
	Walk(n, func(Node) bool { return true })
	_ = Print(n)
}

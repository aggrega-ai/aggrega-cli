// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armexpr

import "strings"

// Print renders n canonically: single-quoted strings with doubled-quote escapes, ", "
// between arguments, and no whitespace elsewhere.
func Print(n Node) string {
	var b strings.Builder
	printNode(&b, n)
	return b.String()
}

// Expression renders n as a bracketed expression string, "[" + Print(n) + "]".
func Expression(n Node) string { return "[" + Print(n) + "]" }

func printNode(b *strings.Builder, n Node) {
	switch v := n.(type) {
	case Call:
		if v.Namespace != "" {
			b.WriteString(v.Namespace)
			b.WriteByte('.')
		}
		b.WriteString(v.Name)
		b.WriteByte('(')
		for i, a := range v.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			printNode(b, a)
		}
		b.WriteByte(')')
	case String:
		b.WriteByte('\'')
		b.WriteString(strings.ReplaceAll(v.Value, "'", "''"))
		b.WriteByte('\'')
	case Int:
		b.WriteString(v.Text)
	case Member:
		printNode(b, v.Target)
		b.WriteByte('.')
		b.WriteString(v.Name)
	case Index:
		printNode(b, v.Target)
		b.WriteByte('[')
		printNode(b, v.Index)
		b.WriteByte(']')
	}
}

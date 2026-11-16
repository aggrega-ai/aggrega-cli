// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armexpr

import "fmt"

// Node is one element of a parsed expression.
type Node interface {
	// Pos is the byte offset in the parsed text where the node begins; for a
	// Member or an Index it is the offset of the accessor's opening token.
	Pos() int
}

// Call is a function call. Namespace is empty for a built-in function.
type Call struct {
	Namespace, Name string
	Args            []Node
	At              int
}

// String is a string literal with its quotes removed and each pair of single
// quotes (`”`) collapsed to one.
type String struct {
	Value string
	At    int
}

// Int is an integer literal, kept as written.
type Int struct {
	Text string
	At   int
}

// Member is a property access, Target.Name.
type Member struct {
	Target Node
	Name   string
	At     int
}

// Index is an element access, Target[Index].
type Index struct {
	Target, Index Node
	At            int
}

// Pos implements Node.
func (n Call) Pos() int { return n.At }

// Pos implements Node.
func (n String) Pos() int { return n.At }

// Pos implements Node.
func (n Int) Pos() int { return n.At }

// Pos implements Node.
func (n Member) Pos() int { return n.At }

// Pos implements Node.
func (n Index) Pos() int { return n.At }

// SyntaxError reports text that is not a well-formed expression. Offset is a
// byte offset in the text handed to the reader; Reason names the fault and
// never quotes the text.
type SyntaxError struct {
	Offset int
	Reason string
}

// Error implements error.
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("ARM expression syntax error at offset %d: %s", e.Offset, e.Reason)
}

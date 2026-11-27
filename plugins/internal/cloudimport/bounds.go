// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import "fmt"

// The bounds every format's reader holds a source to, so a hostile source
// cannot grow what one read lowers without bound.
const (
	// MaxNestedResources bounds the resources of nested templates one read
	// takes into the graph, so templates deploying one another many times
	// over cannot grow it without bound; past it a nested template is not
	// read.
	MaxNestedResources = 10000
	// MaxValueSize bounds the size of the value one output or one parameter
	// of a nested template holds, as [Size] measures it, so outputs and
	// parameters composing each other cannot grow it without bound; past it
	// the value is not carried.
	MaxValueSize = 1 << 16
	// MaxExpansion bounds the expansions one lowering spends ([Budget]): the
	// variables it follows, the parts it composes, the property copies it
	// writes.
	MaxExpansion = 10000
)

// TooLarge says, in a warning naming a value, that it is larger than
// MaxValueSize and so not carried.
func TooLarge() string {
	return fmt.Sprintf("larger than the %d bytes the draft carries of one value, its parts composing one another, so "+
		"what reads it is not carried", MaxValueSize)
}

// Budget counts the expansions one lowering spends, shared by every lowerer
// of one value, so a source whose variables, compositions or loops multiply
// each other cannot grow the graph without bound. The zero Budget has spent
// nothing.
type Budget struct{ spent int }

// Spend records n expansions, false where they would pass MaxExpansion, and
// then records none.
func (b *Budget) Spend(n int) bool {
	if b.spent+n > MaxExpansion {
		return false
	}
	b.spent += n
	return true
}

// Size returns how large v is: the bytes of the text and JSON it holds, a
// stored secret's reference text among them, and one for each part of
// composed text.
func Size(v Value) int {
	switch v := v.(type) {
	case Literal:
		return len(v.JSON)
	case Param:
		return len(v.Name)
	case Compose:
		n := 0
		for _, p := range v.Parts {
			n += 1 + Size(p)
		}
		return n
	case Ref:
		n := len(v.Node)
		for _, a := range v.Attr {
			n += len(a)
		}
		return n
	case Credential:
		return len(v.Node) + len(v.What)
	case Opaque:
		return len(v.Text)
	case Stored:
		return len(v.Text)
	}
	return 0
}

// PropSize returns how large p is: the size of its value, and one for each
// member and item below it with theirs.
func PropSize(p Prop) int {
	n := Size(p.Value)
	for _, c := range p.Props {
		n += 1 + PropSize(c)
	}
	for _, c := range p.Items {
		n += 1 + PropSize(c)
	}
	return n
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command factgen generates the constraint framework's view ladder, its derived
// subject-kind set, and the reviewable placement table, from the fact registry
// declared in registry.go.
//
// Nothing here reads a hand-maintained document. The registry is Go source, the
// derivations below are computed from it, and every generated artifact is a
// projection of it — so a placement change is a one-line registry edit whose
// consequences appear as a diff in committed output.
//
// Usage, from this directory:
//
//	go run . <output-file>
//
// The output file's base name selects the target. Every target is also declared
// as a go:generate directive in generate.go, so `go generate ./...` over this
// package regenerates all of them.
package main

// rungDecl declares one rung of the view ladder: a point in the compilation at
// which some set of facts is complete.
//
// A rung exists if and only if some fact lands there. A rung declared here with
// no fact landing on it is refused rather than silently dropped, because a rung
// nothing occupies is vocabulary every future author must learn and no check
// uses.
type rungDecl struct {
	// Name is the rung's name. It names the state the rung holds, never a
	// position in a sequence, so the name survives a change to the order
	// things happen in.
	Name string

	// Extends names the rung this one contains: every fact complete at
	// Extends is still complete here. It is empty for the ladder's root.
	//
	// The relation is declared rather than derived, and that is deliberate.
	// Facts accumulate, so deriving the order from the fact sets alone would
	// make "the chain is linear" true by construction and the assertion
	// worthless. Declared, the relation can be wrong — a second rung
	// extending an already-extended rung is a branch, which means two rungs
	// no order relates, and the generator refuses it.
	Extends string

	// Holds describes what is complete at this rung, for the placement table.
	Holds string
}

// factDecl declares one fact: one distinct question about the pipeline product
// that a check reads to decide its predicate.
//
// One fact, one accessor, strictly one to one. There are no convenience
// variants: a fact exposed both as a predicate and as a set-returning form is
// two code paths for one question. An accessor may return a composite record
// where the fact itself is composite; that is still one fact.
type factDecl struct {
	// Name is the fact's name in kebab-case, and it is the single name for
	// the fact. The accessor's Go method name is derived from it rather than
	// declared beside it, which is what makes duplicate declaration a compile
	// error: two declarations of one name emit two same-named methods on one
	// interface, and that does not compile.
	Name string

	// Rung names the rung at which this fact is complete.
	Rung string

	// SourceField names the pipeline product field the fact reads, as a Go
	// structure and field. Where the product is carried as a function
	// parameter rather than as a field, it names that single carrier instead;
	// what matters is that exactly one carrier is named, because two facts
	// sharing a rung, a carrier and a subject are the mechanically flaggable
	// shape of same-semantics-under-a-different-name duplication.
	SourceField string

	// Subject is the coordinate list the fact is keyed by, outermost first —
	// so {"node", "socket"} means the fact is asked once per socket per node.
	// The subject-kind set is derived from these lists and from nothing else.
	Subject []string

	// Params are the accessor's arguments. They are declared separately from
	// Subject and are usually related to it without being identical: a fact
	// keyed per node and socket may legitimately be read through the node's
	// component type, because the type is a projection of the node.
	Params []paramDecl

	// Returns is the accessor's result, emitted verbatim after the parameter
	// list — so a single type or a parenthesized list are both admissible.
	//
	// A return type may not mention an authored-entity type, by pointer or by
	// value, inside a container or as a type argument; nor any diagnostic
	// shape. A view exposes named facts and never a raw document, and one
	// accessor handing back a document reopens re-derivation for every check.
	Returns string

	// Doc is the accessor's godoc body, in declarative voice.
	Doc string

	// ReportedException, when non-empty, records that this fact's return shape
	// does not satisfy the no-raw-document rule, in the terms of what it
	// exposes. The framework reports such a fact rather than admitting it
	// silently: the guard names it, the placement table prints it, and the
	// exception is a standing question rather than a settled shape.
	ReportedException string
}

// paramDecl declares one accessor argument.
type paramDecl struct {
	Name string
	Type string
}

// recordDecl declares a composite record a composite fact returns. Its fields
// are scalars: a record exists to carry one fact's several coordinates, not to
// hand a check a structure it can walk into.
type recordDecl struct {
	Name   string
	Doc    string
	Fields []recordField
}

// recordField declares one field of a composite record.
type recordField struct {
	Name string
	Type string
	Doc  string
}

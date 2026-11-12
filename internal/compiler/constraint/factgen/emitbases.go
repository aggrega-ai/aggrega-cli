// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// The refusing bases and the obligation interfaces are generated into the parent
// package, from the same registry the view chain is generated from.
//
// The directive sits beside the emitter rather than with the chain's, so that
// the target and the code producing it are read in one place.
//
//go:generate go run . ../viewbases_gen.go

import (
	"fmt"
	"go/format"
	"strings"
)

// baseName derives a stage's refusing-base type name from the view it stands
// under, so the two names move together and a reader crossing from one to the
// other has nothing to look up.
//
// 🔴 The name is the stage whose implementations EMBED the base, never the stage
// whose facts it refuses. The two readings are one stage apart and only the first
// one works: a base supplying its own stage's facts satisfies that stage's
// obligation interface by method promotion, so the assertion holds an
// implementation to nothing and a fact added at that stage compiles everywhere
// and refuses only when it is read. The off-by-one is therefore the design rather
// than an accident of naming.
func baseName(stage string) string { return viewName(stage) + "Base" }

// documentBaseName derives the refusing-base type name for one authored document
// kind, pairing by name with that kind's obligation interface.
func documentBaseName(kind string) string { return kind + "DocumentBase" }

// documentFactsName derives the obligation interface name for one authored
// document kind.
func documentFactsName(kind string) string { return kind + "DocumentFacts" }

// stageFactsName derives the obligation interface name for one stage.
func stageFactsName(stage string) string { return pascal(stage) + "StageFacts" }

// emitViewBases renders the refusing bases and the obligation interfaces: one
// base per authored document kind at the root stage, one per stage after it, and
// one interface per document-stage kind and per later stage.
//
// The two halves answer one question from opposite sides. A base supplies the
// methods a view declares that the implementation embedding it does not carry,
// so such an implementation compiles without writing a body for them — and each
// unwritten one refuses at the point a check reads it, naming what would have
// answered. An obligation interface is what an implementation asserts in order to
// be held to a named subset, so that a fact added to that subset fails to compile
// on the implementations claiming it and on no other.
//
// 🔴 What a base omits is therefore load-bearing, and omitting nothing is the one
// shape that silently defeats the whole arrangement. A base supplying the very
// facts its embedder's obligation interface demands satisfies that interface by
// method promotion, at which point the assertion holds the implementation to
// nothing: the fact is answered nowhere, the build stays green, and the refusal
// arrives at run time. So each base declares refusals for exactly what its
// embedder does not carry — the other kinds' facts at the root stage, and the
// earlier stages' facts after it.
func emitViewBases(c chain) ([]byte, error) {
	groups, err := deriveDocumentKinds(facts, stages)
	if err != nil {
		return nil, err
	}

	// The kind per fact is read back out of the groups rather than resolved a
	// second time, so the refusal a base states and the obligation an interface
	// declares cannot name different kinds for one fact.
	documentKindOf := make(map[string]string, len(facts))
	for _, g := range groups {
		for _, f := range g.Facts {
			documentKindOf[f.Name] = g.Kind
		}
	}

	var b strings.Builder
	b.WriteString(goHeader)

	if mentionsAuthoredTypes(facts) {
		fmt.Fprintf(&b, "\nimport %q\n", typesImportPath)
	}

	// The root stage has two axes, so its bases are per authored document kind:
	// each refuses the document-stage facts its own kind does not carry, and
	// declares nothing for the ones it does.
	root := c.order[0].Name
	for _, g := range groups {
		carried := make(map[string]bool, len(g.Facts))
		for _, f := range g.Facts {
			carried[f.Name] = true
		}
		name := documentBaseName(g.Kind)
		b.WriteString("\n")
		b.WriteString(wrapComment("", documentBaseDoc(c, g)))
		fmt.Fprintf(&b, "type %s struct{}\n", name)
		for _, f := range c.facts[root] {
			if carried[f.Name] {
				continue
			}
			writeRefusal(&b, name, f, documentKindOf[f.Name])
		}
	}

	// Every stage after the root has a single document kind — a compilation for most of
	// them, one authored kind the stage itself fixes for a stage presenting one
	// document — so its base is per stage rather than per kind: it refuses the
	// facts of the stage it extends and, by embedding, of everything that stage
	// contains.
	for i := 1; i < len(c.order); i++ {
		name := baseName(c.order[i].Name)
		prev := c.order[i-1].Name

		b.WriteString("\n")
		b.WriteString(wrapComment("", baseDoc(c, i)))
		if i == 1 {
			fmt.Fprintf(&b, "type %s struct{}\n", name)
		} else {
			fmt.Fprintf(&b, "type %s struct {\n\t%s\n}\n", name, baseName(prev))
		}

		for _, f := range c.facts[prev] {
			writeRefusal(&b, name, f, documentKindOf[f.Name])
		}
	}

	for _, g := range groups {
		b.WriteString("\n")
		b.WriteString(wrapComment("", documentFactsDoc(g)))
		writeFactMethods(&b, documentFactsName(g.Kind), g.Facts)
	}

	for _, s := range c.order {
		if s.Name == documentStage {
			continue
		}
		b.WriteString("\n")
		b.WriteString(wrapComment("", stageFactsDoc(s)))
		writeFactMethods(&b, stageFactsName(s.Name), c.facts[s.Name])
	}

	out, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("the generated refusing bases are not valid Go: %w", err)
	}
	return out, nil
}

// writeRefusal renders one base method: the accessor's signature, a body that
// panics, and the godoc opening with the same sentence the panic carries.
func writeRefusal(b *strings.Builder, base string, f factDecl, documentKind string) {
	refusal := refusalSentence(f, documentKind)
	b.WriteString("\n")
	b.WriteString(wrapComment("", fmt.Sprintf("%s panics. %s.", accessorName(f.Name), capitalised(refusal))))
	fmt.Fprintf(b, "func (%s) %s(%s) %s {\n", base, accessorName(f.Name), paramList(f.Params), f.Returns)
	fmt.Fprintf(b, "\tpanic(%q)\n", refusal)
	b.WriteString("}\n")
}

// writeFactMethods renders one obligation interface: the accessors for the facts
// given, in declaration order, and nothing else.
//
// It embeds nothing, and that is load-bearing rather than incidental. An
// interface embedding a view is read as a view by anything ordering the chain,
// so one that embedded a stage would place itself on the chain as a second
// branch out of that stage; one that embeds nothing states the same obligation
// and sits outside the chain entirely.
func writeFactMethods(b *strings.Builder, name string, fs []factDecl) {
	fmt.Fprintf(b, "type %s interface {\n", name)
	for _, f := range fs {
		fmt.Fprintf(b, "\t%s(%s) %s\n", accessorName(f.Name), paramList(f.Params), f.Returns)
	}
	b.WriteString("}\n")
}

// refusalSentence is what a base's method says when it is reached: which fact
// this view does not carry, and what does carry it.
//
// The two halves of the answer differ by stage, because the registry records two
// different things. A document-stage fact names the authored document kind, which
// is the axis that stage carries. A later-stage fact has one kind for the
// whole stage — a compilation, or the one authored kind that stage fixes — so
// what a reader needs is the field it is read from.
func refusalSentence(f factDecl, documentKind string) string {
	if documentKind != "" {
		return fmt.Sprintf("this view does not carry %s, which %s %s document carries",
			f.Name, article(documentKind), documentKind)
	}
	return fmt.Sprintf("this view does not carry %s, which is read from %s",
		f.Name, f.SourceField)
}

// capitalised is a sentence with its first letter upper-cased.
//
// A refusal is written once and read in two places: as the panic's message,
// where the convention is a lower-case clause, and as the opening of the
// accessor's own documentation, where it follows a full stop. Writing it once
// and casing it at the point of use is what keeps the two from being two
// sentences that have to be kept in step.
func capitalised(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// article is the indefinite article a document kind takes, so the refusal reads
// as a sentence rather than as a slot with a value dropped into it.
func article(kind string) string {
	if kind == "" {
		return "a"
	}
	switch kind[0] {
	case 'A', 'E', 'I', 'O', 'U':
		return "an"
	}
	return "a"
}

// baseDoc is the godoc for the base at index i on the chain, which is the base
// an implementation of that stage's view embeds.
func baseDoc(c chain, i int) string {
	s := c.order[i]
	prev := c.order[i-1]

	doc := fmt.Sprintf(
		"%s is a base for an implementation of %s.", baseName(s.Name), viewName(s.Name))
	doc += fmt.Sprintf(
		"\n\nIt declares a refusing method for every fact complete before the %s stage, and for none of that stage's own. The name is the stage whose implementations embed it rather than the stage whose facts it refuses, and the two are one stage apart on purpose: a base supplying its own stage's facts would satisfy %s by method promotion, so the assertion below would hold an implementation to nothing and a fact added at this stage would compile everywhere and refuse only when it was read.",
		s.Name, stageFactsName(s.Name))
	if i > 1 {
		doc += fmt.Sprintf(
			"\n\nIt embeds %s, so one embedding covers every earlier stage rather than repeating those refusals here.",
			baseName(prev.Name))
	}
	doc += fmt.Sprintf(
		"\n\nAn implementation embeds it, writes a body for each fact the %s stage declares, and asserts %s. A fact added at this stage then fails to compile on those implementations. An implementation that instead delegates the earlier stages to a value of an earlier view needs no base at all: delegation already answers everything this would refuse.",
		s.Name, stageFactsName(s.Name))
	return doc
}

// documentBaseDoc is the godoc for one authored document kind's refusing base.
func documentBaseDoc(c chain, g documentKindGroup) string {
	root := c.order[0]
	return fmt.Sprintf(
		"%s is a base for an implementation of %s that presents %s %s.\n\n"+
			"It declares a refusing method for every %s-stage fact %s %s does not carry, and for none of the facts it does. The omission is the design rather than an economy: a base supplying this kind's own facts would satisfy %s by method promotion, so the assertion would hold an implementation to nothing and a fact declared for this kind would compile everywhere and refuse only when it was read.\n\n"+
			"An implementation embeds it, writes a body for each fact this kind carries, and asserts %s. A fact then declared for this kind fails to compile on those implementations, and a fact declared for another kind does not reach them.",
		documentBaseName(g.Kind), viewName(root.Name), article(g.Kind), g.Kind,
		root.Name, article(g.Kind), g.Kind,
		documentFactsName(g.Kind), documentFactsName(g.Kind))
}

// documentFactsDoc is the godoc for one authored document kind's obligation
// interface.
func documentFactsDoc(g documentKindGroup) string {
	return fmt.Sprintf(
		"%s are the document-stage facts %s %s carries.\n\n"+
			"An implementation presenting %s %s asserts it, and that assertion is the thing a refusing base cannot supply. A fact declared for this kind then fails to compile on the implementations claiming it, and on no other; without the assertion the same declaration compiles everywhere and refuses when it is read, which moves the discovery from building to running.\n\n"+
			"Each method is an accessor the view chain declares and documents. The documentation is not repeated here: a fact's documentation has one home, and a second copy would be the one that drifts.",
		documentFactsName(g.Kind), article(g.Kind), g.Kind, article(g.Kind), g.Kind)
}

// stageFactsDoc is the godoc for one stage's obligation interface.
func stageFactsDoc(s stageDecl) string {
	return fmt.Sprintf(
		"%s are the facts the %s stage declares itself, and not the ones it reads through the stages it contains.\n\n"+
			"An implementation of %s asserts it, and that assertion is the thing a refusing base cannot supply. A fact declared at this stage then fails to compile on the implementations claiming it, and on no other; without the assertion the same declaration compiles everywhere and refuses when it is read, which moves the discovery from building to running.\n\n"+
			"Each method is an accessor the view chain declares and documents. The documentation is not repeated here: a fact's documentation has one home, and a second copy would be the one that drifts.",
		stageFactsName(s.Name), s.Name, viewName(s.Name))
}

// mentionsAuthoredTypes reports whether any declared signature names the
// authored types package.
//
// The import is decided per generated file from that file's own declarations,
// because a file importing a package nothing in it mentions does not compile and
// one omitting a package it does mention does not either.
func mentionsAuthoredTypes(fs []factDecl) bool {
	for _, f := range fs {
		if strings.Contains(f.Returns, authoredTypePrefix) {
			return true
		}
		for _, p := range f.Params {
			if strings.Contains(p.Type, authoredTypePrefix) {
				return true
			}
		}
	}
	return false
}

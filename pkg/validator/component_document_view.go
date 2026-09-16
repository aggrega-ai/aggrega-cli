// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// componentDocumentView presents one authored Component to a check as named
// facts: the assertion names the entity declares across the chain it extends.
//
// It answers about the one document it was built for, which is the framework's
// contract rather than a simplification of it. The subject a check over a
// document is invoked with stands for the one document being presented rather
// than a member of a document set, because entity documents are validated one at
// a time as they are read. So the document argument each accessor takes names the
// document this value was built for, and asking about another is a mistake rather
// than a lookup that misses.
//
// It holds the answers rather than the Component, and that is the shape rather
// than a convenience. A view presents facts already produced; holding the
// entities would leave every accessor free to read a field no fact declares,
// which is exactly the reach a view exists to close.
//
// It satisfies the whole document-stage view, which declares facts a Component
// cannot carry: an Aggregate's text and parameter contract, a Manifest's
// annotation bindings, a Recipe's overlay and source pointer. The embedded base
// supplies those and refuses each at the point a check reads it, naming the
// document kind that does carry it. What holds this type to the facts a Component
// itself carries is the obligation asserted at the bottom of this file: a
// Component fact added to the registry stops this type compiling, and a fact
// carried by any other kind does not reach it.
type componentDocumentView struct {
	// ComponentDocumentBase supplies the document-stage facts a Component does
	// not carry, and declares nothing for the ones it does — so this type writes
	// bodies for exactly the Component's own facts.
	constraint.ComponentDocumentBase

	// path is the workspace-relative file the document was read from. It is the
	// document coordinate itself, which is why an accessor's argument is checked
	// against it.
	path string

	// declarations are the assertion names the chain declares, root-first and in
	// each entity's own author order, carrying a name once per distinct rule
	// declared under it. A name appears more than once exactly when two entities
	// along the chain declare different rules under it.
	declarations []string

	// names are the distinct assertion names, in the order they are first
	// declared. They are the coordinates a check over one assertion is invoked
	// for, and not a fact: a rule about a name reports once about that name,
	// whatever the chain did to produce it.
	names []string

	// declaredBy maps each assertion name to the entity declaring it nearest
	// the chain root. It is keyed rather than ordered because the fact it
	// answers is asked one name at a time, and it is filled in the same walk
	// the names are: a second walk could reach a different entity for a name
	// and nothing would compare the two.
	declaredBy map[string]string
}

// newComponentDocumentView presents the Component a resolve is holding: the file
// it was read from, and the chain it extends, nearest entity first with the chain
// root last.
//
// The answers are read here rather than taken from the caller because the reading
// has exactly one producer, and routing the presentation through it is what keeps
// the fact single-valued. A caller passing its own name list could pass one read
// from a different chain, or read differently.
//
// A name restated under a byte-identical rule contributes nothing beyond its
// first declaration, because the rule that survives such a restatement is the
// same rule: the chain declares one assertion there, not two. A name restated
// under a different rule contributes again, because the chain then carries two
// rules under one name.
func newComponentDocumentView(path string, chain []types.Component) componentDocumentView {
	v := componentDocumentView{path: path, declaredBy: map[string]string{}}
	declared := map[string]string{}
	for i := len(chain) - 1; i >= 0; i-- {
		for name, rule := range chain[i].Spec.Assertions.All() {
			standing, seen := declared[name]
			if seen && standing == rule {
				continue
			}
			declared[name] = rule
			v.declarations = append(v.declarations, name)
			if !seen {
				v.names = append(v.names, name)
				v.declaredBy[name] = chain[i].Metadata.Name
			}
		}
	}
	return v
}

// EntityAssertionNameSet returns the assertion names the presented Component
// declares across the chain it extends, root-first and in each entity's own
// author order.
func (v componentDocumentView) EntityAssertionNameSet(document string) []string {
	v.refuseOtherDocument(document, "EntityAssertionNameSet")
	return v.declarations
}

// EntityAssertionDeclaringEntity returns the Component declaring the named
// assertion nearest the root of the chain the presented Component extends, and
// whether the chain declares that name at all.
//
// The walk that fills it is root-first and records the first declarer, so a name
// several Components along the chain declare answers with the one furthest from
// the presented Component — the entity the name originates on rather than a
// restatement of it nearer the leaf.
func (v componentDocumentView) EntityAssertionDeclaringEntity(document, assertion string) (string, bool) {
	v.refuseOtherDocument(document, "EntityAssertionDeclaringEntity")
	entity, declared := v.declaredBy[assertion]
	return entity, declared
}

// assertionNames returns the distinct assertion names the presented Component
// declares, in the order they are first declared.
//
// It is the coordinate enumeration a check over one assertion is invoked for
// rather than a fact a check reads, which is why it is unexported and why it is
// derived in the same walk the fact is: two walks over one chain could disagree
// about which names it holds.
func (v componentDocumentView) assertionNames() []string { return v.names }

// refuseOtherDocument refuses an accessor call naming a document this value was
// not built for, because answering would hand back this document's fact under
// another document's name.
func (v componentDocumentView) refuseOtherDocument(document, accessor string) {
	if document == v.path {
		return
	}
	panic(fmt.Sprintf(
		"componentDocumentView.%s asked about document %q while presenting %q: "+
			"a view presents one document, so present the document being asked about",
		accessor, document, v.path))
}

// The compiler holds the type to the contract rather than a comment claiming it:
// a signature drifting from the declared facts stops compiling here.
//
// The second assertion is the half the embedded base cannot supply. Embedding
// makes every document-stage fact compile, so the view assertion alone would
// accept a presentation answering none of them; naming the kind's own obligation
// is what makes a Component fact added to the registry fail here, and on no
// presentation of another kind.
var (
	_ constraint.DocumentView           = componentDocumentView{}
	_ constraint.ComponentDocumentFacts = componentDocumentView{}
)

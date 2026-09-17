// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
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
// componentPresentation names which of a Component's document-stage facts a
// presentation was built to answer.
//
// It exists because the Component's facts are read off two different things. The
// assertion facts are read off the chain a resolve walked, which a load holding
// one file does not have; the schema keys are read off the authored block, which
// a resolve holding a merged chain no longer distinguishes from its base's. So a
// presentation carries one or the other, while the obligation interface holds this
// type to answering all of them — and the honest answer to the facts it was not
// built for is a refusal rather than an empty one, which a rule cannot tell from
// an entity that declared nothing.
//
// The values are the phrase the refusal reads, so what a presentation carries is
// said once rather than once in the field and again in each message.
type componentPresentation string

const (
	componentExtendsChain      componentPresentation = "the assertions declared across the chain the Component extends"
	componentExtendsChainShape componentPresentation = "the shape of the chain the Component extends"
	componentSchemaBlock       componentPresentation = "the Component's authored schema block"
)

type componentDocumentView struct {
	// ComponentDocumentBase supplies the document-stage facts a Component does
	// not carry, and declares nothing for the ones it does — so this type writes
	// bodies for exactly the Component's own facts.
	constraint.ComponentDocumentBase

	// presents is what this value was built from, and every accessor outside that
	// presentation refuses.
	presents componentPresentation

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

	// pillarKeys are the top-level keys the authored schema block declares, in
	// the order the author wrote them. They are the Component's half of the
	// schema-block presentation and are empty on every other.
	pillarKeys []string

	// shape is what the walk found about the chain's structure, on a
	// presentation built from that walk and empty on every other.
	shape extendsChainShape
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
	v := componentDocumentView{
		presents:   componentExtendsChain,
		path:       path,
		declaredBy: map[string]string{},
	}
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
	v.refuseOtherPresentation(componentExtendsChain, "EntityAssertionNameSet")
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
	v.refuseOtherPresentation(componentExtendsChain, "EntityAssertionDeclaringEntity")
	entity, declared := v.declaredBy[assertion]
	return entity, declared
}

// ComponentSchemaPillarKeys returns the top-level keys the presented Component's
// schema block declares, in the order the author wrote them.
//
// They are the keys as authored and not the pillars they were meant to name, so a
// key naming no pillar is among them — which is the whole of what a rule over them
// reports on.
func (v componentDocumentView) ComponentSchemaPillarKeys(document string) []string {
	v.refuseOtherDocument(document, "ComponentSchemaPillarKeys")
	v.refuseOtherPresentation(componentSchemaBlock, "ComponentSchemaPillarKeys")
	return v.pillarKeys
}

// newComponentExtendsChainShapeView presents what a walk found about the shape
// of the chain a Component extends.
//
// It is a second constructor rather than a second field on the first, because
// the two are built at different moments from different things: the assertion
// presentation is built from a chain that walked cleanly, and this one is built
// from the walk's outcome whether it did or not. A constructor answering both
// would have to accept a chain it may not have.
//
// The document has no file of its own, because the chain is assembled from
// entities reached by reference rather than from one file a caller read. The
// finding a rule over it raises is attributed to the entity the resolution was
// requested for instead.
func newComponentExtendsChainShapeView(shape extendsChainShape) componentDocumentView {
	return componentDocumentView{presents: componentExtendsChainShape, shape: shape}
}

// ComponentExtendsChainCycleEdge returns the edge at which the chain the
// presented Component extends returns to an entity already in it, and whether
// the chain closes on itself at all.
func (v componentDocumentView) ComponentExtendsChainCycleEdge(
	document string,
) (constraint.ExtendsChainCycleEdge, bool) {
	v.refuseOtherDocument(document, "ComponentExtendsChainCycleEdge")
	v.refuseOtherPresentation(componentExtendsChainShape, "ComponentExtendsChainCycleEdge")
	return v.shape.cycleEdge()
}

// ComponentExtendsChainLength returns how many entities the chain the presented
// Component extends reaches, and whether that chain ends cleanly.
func (v componentDocumentView) ComponentExtendsChainLength(document string) (int, bool) {
	v.refuseOtherDocument(document, "ComponentExtendsChainLength")
	v.refuseOtherPresentation(componentExtendsChainShape, "ComponentExtendsChainLength")
	return v.shape.length()
}

// assertionNames returns the distinct assertion names the presented Component
// declares, in the order they are first declared.
//
// It is the coordinate enumeration a check over one assertion is invoked for
// rather than a fact a check reads, which is why it is unexported and why it is
// derived in the same walk the fact is: two walks over one chain could disagree
// about which names it holds.
func (v componentDocumentView) assertionNames() []string { return v.names }

// newComponentSchemaBlockView presents the schema block a load is holding: the
// file the Component was read from, and the block's own mapping node.
//
// mapping is the node after the caller has settled which of the block's authoring
// shapes it holds, so the keys are read from a mapping and from nothing else. That
// settling has one home, at [ValidateSchemaPillars], and passing the settled node
// is what keeps it there: a second reading here could admit a shape that one
// rejects, or reject one it admits.
func newComponentSchemaBlockView(path string, mapping *yaml.Node) componentDocumentView {
	return componentDocumentView{
		presents:   componentSchemaBlock,
		path:       path,
		pillarKeys: authoredSchemaPillarKeys(mapping),
	}
}

// refuseOtherPresentation refuses an accessor call for a fact this value was not
// built to answer, because answering would report an absence the document does not
// have — a rule cannot tell an empty answer from an entity that declared nothing.
func (v componentDocumentView) refuseOtherPresentation(
	presentation componentPresentation,
	accessor string,
) {
	if v.presents == presentation {
		return
	}
	panic(fmt.Sprintf(
		"componentDocumentView.%s asked of a view built from %s: "+
			"the fact is read from %s, so present that",
		accessor, v.presents, presentation))
}

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

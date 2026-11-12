// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// authoredSchemaPillarKeys returns the top-level keys a schema block's mapping
// node declares, in the order the author wrote them.
//
// It is one reading shared by both authored kinds that carry the block, because
// the block is the same shape on each: a key naming a pillar and a body beneath
// it. Two readings would agree on the day they were written and would be free to
// disagree afterwards, at which point one kind's keys would be enumerated
// differently from the other's and the rule over them would refuse one kind on
// input it admitted for the other.
//
// It takes the mapping node rather than the field, so which of the block's
// authoring shapes is in hand has been settled by the caller. A nil node, and a
// node that is not a mapping, yield no keys rather than a guess.
func authoredSchemaPillarKeys(mapping *yaml.Node) []string {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	keys := make([]string, 0, len(mapping.Content)/2)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		keys = append(keys, mapping.Content[i].Value)
	}
	return keys
}

// featureDocumentView presents one authored Feature to a check as named facts:
// the top-level keys its schema block declares, the shape of its extends chain,
// and the label selectors its own document declares.
//
// It answers about the one document it was built for, which is the framework's
// contract rather than a simplification of it. The subject a check over a document
// is invoked with stands for the one document being presented rather than a member
// of a document set, because entity documents are validated one at a time as they
// are read. So the document argument each accessor takes names the document this
// value was built for, and asking about another is a mistake rather than a lookup
// that misses.
//
// It holds the answers rather than the Feature, and that is the shape rather than
// a convenience. A view presents facts already produced; holding the entity would
// leave every accessor free to read a field no fact declares, which is exactly the
// reach a view exists to close.
//
// It satisfies the whole document-stage view, which declares facts a Feature
// cannot carry: an Aggregate's text and parameter contract, a Component's
// assertions, a Manifest's annotation bindings, a Recipe's overlay and source
// pointer. The embedded base supplies those and refuses each at the point a check
// reads it, naming the document kind that does carry it. What holds this type to
// the facts a Feature itself carries is the obligation asserted at the bottom of
// this file: a Feature fact added to the registry stops this type compiling, and a
// fact carried by any other kind does not reach it.
// featurePresentation names which of a Feature's document-stage facts a
// presentation was built to answer.
//
// It exists for the reason the Component's own does: the Feature's facts are read
// off two different things. The schema keys are read off the authored block, which
// a resolve holding a walked chain no longer distinguishes from its base's; the
// chain's shape is read off that walk, which a load holding one file does not have.
// So a presentation carries one or the other, while the obligation interface holds
// this type to answering all of them — and the honest answer to the facts it was
// not built for is a refusal rather than an empty one, which a rule cannot tell
// from a Feature that declared nothing.
//
// The values are the phrase the refusal reads, so what a presentation carries is
// said once rather than once in the field and again in each message.
type featurePresentation string

const (
	featureSchemaBlock       featurePresentation = "the Feature's authored schema block"
	featureExtendsChainShape featurePresentation = "the shape of the chain the Feature extends"
	featureDeclaredSelectors featurePresentation = "the selectors the Feature's own document declares"
)

type featureDocumentView struct {
	// FeatureDocumentBase supplies the document-stage facts a Feature does not
	// carry, and declares nothing for the ones it does — so this type writes
	// bodies for exactly the Feature's own facts.
	constraint.FeatureDocumentBase

	// presents is what this value was built from, and every accessor outside that
	// presentation refuses.
	presents featurePresentation

	// path is the workspace-relative file the document was read from. It is the
	// document coordinate itself, which is why an accessor's argument is checked
	// against it.
	path string

	// pillarKeys are the top-level keys the authored schema block declares, in
	// the order the author wrote them.
	pillarKeys []string

	// shape is what the walk found about the chain's structure, on a
	// presentation built from that walk and empty on every other.
	shape extendsChainShape

	// socketSelectors and slotSelectors are the selectors the document declares
	// on its sockets and on its slots, on a presentation built from the
	// document's own declarations and empty on every other.
	socketSelectors []constraint.DeclaredSelector
	slotSelectors   []constraint.DeclaredSelector
}

// newFeatureSchemaBlockView presents the schema block a load is holding: the file
// the Feature was read from, and the block's own mapping node.
//
// mapping is the node after the caller has settled which of the block's authoring
// shapes it holds, for the reason the Component's own constructor gives: the
// settling has one home and a second reading here could diverge from it.
func newFeatureSchemaBlockView(path string, mapping *yaml.Node) featureDocumentView {
	return featureDocumentView{
		presents:   featureSchemaBlock,
		path:       path,
		pillarKeys: authoredSchemaPillarKeys(mapping),
	}
}

// newFeatureExtendsChainShapeView presents what a walk found about the shape of
// the chain a Feature extends.
//
// It is a second constructor rather than a second field on the first, for the
// reason the Component's own states: the two are built at different moments from
// different things, and a constructor answering both would have to accept a chain
// it may not have.
//
// The document has no file of its own, because the chain is assembled from
// entities reached by reference rather than from one file a caller read.
func newFeatureExtendsChainShapeView(shape extendsChainShape) featureDocumentView {
	return featureDocumentView{presents: featureExtendsChainShape, shape: shape}
}

// FeatureSchemaPillarKeys returns the top-level keys the presented Feature's
// schema block declares, in the order the author wrote them.
//
// They are the keys as authored and not the pillars they were meant to name, so a
// key naming no pillar is among them — which is the whole of what a rule over them
// reports on.
func (v featureDocumentView) FeatureSchemaPillarKeys(document string) []string {
	v.refuseOtherDocument(document, "FeatureSchemaPillarKeys")
	v.refuseOtherPresentation(featureSchemaBlock, "FeatureSchemaPillarKeys")
	return v.pillarKeys
}

// FeatureExtendsChainCycleEdge returns the edge at which the chain the presented
// Feature extends returns to an entity already in it, and whether the chain
// closes on itself at all.
func (v featureDocumentView) FeatureExtendsChainCycleEdge(
	document string,
) (constraint.ExtendsChainCycleEdge, bool) {
	v.refuseOtherDocument(document, "FeatureExtendsChainCycleEdge")
	v.refuseOtherPresentation(featureExtendsChainShape, "FeatureExtendsChainCycleEdge")
	return v.shape.cycleEdge()
}

// FeatureExtendsChainLength returns how many entities the chain the presented
// Feature extends reaches, and whether that chain ends cleanly.
func (v featureDocumentView) FeatureExtendsChainLength(document string) (int, bool) {
	v.refuseOtherDocument(document, "FeatureExtendsChainLength")
	v.refuseOtherPresentation(featureExtendsChainShape, "FeatureExtendsChainLength")
	return v.shape.length()
}

// PresentFeatureDeclaredSelectors presents the label selectors one Feature
// document declares, on the terms [PresentComponentDeclaredSelectors] presents a
// Component's: the Feature as its own document declares it, every socket and
// every slot presented with its selector as written, in the author's order —
// the sockets, then the slots.
//
// A Feature's sockets declare no slots of their own, so no presented slot names
// a socket. Its applicability selectors are not presented: they are a different
// declaration with rules of their own, read wherever an attachment of the
// Feature is evaluated.
func PresentFeatureDeclaredSelectors(path string, feat *types.Feature) constraint.DocumentView {
	v := featureDocumentView{presents: featureDeclaredSelectors, path: path}
	for socket, entry := range feat.Spec.UsesSchema.All() {
		v.socketSelectors = append(v.socketSelectors,
			constraint.DeclaredSelector{Socket: socket, Selector: entry.Selector})
	}
	for slot, declared := range feat.Spec.FeaturesSchema.All() {
		v.slotSelectors = append(v.slotSelectors,
			constraint.DeclaredSelector{Slot: slot, Selector: declared.Selector})
	}
	return v
}

// FeatureSocketSelectors returns the selectors the presented Feature's own
// document declares on its dependency sockets, one per socket in author order.
func (v featureDocumentView) FeatureSocketSelectors(document string) []constraint.DeclaredSelector {
	v.refuseOtherDocument(document, "FeatureSocketSelectors")
	v.refuseOtherPresentation(featureDeclaredSelectors, "FeatureSocketSelectors")
	return v.socketSelectors
}

// FeatureSlotSelectors returns the selectors the presented Feature's own
// document declares on its attachment slots, one per slot in author order.
func (v featureDocumentView) FeatureSlotSelectors(document string) []constraint.DeclaredSelector {
	v.refuseOtherDocument(document, "FeatureSlotSelectors")
	v.refuseOtherPresentation(featureDeclaredSelectors, "FeatureSlotSelectors")
	return v.slotSelectors
}

// refuseOtherPresentation refuses an accessor call for a fact this value was not
// built to answer, because answering would report an absence the document does not
// have — a rule cannot tell an empty answer from a Feature that declared nothing.
func (v featureDocumentView) refuseOtherPresentation(
	presentation featurePresentation,
	accessor string,
) {
	if v.presents == presentation {
		return
	}
	panic(fmt.Sprintf(
		"featureDocumentView.%s asked of a view built from %s: "+
			"the fact is read from %s, so present that",
		accessor, v.presents, presentation))
}

// refuseOtherDocument refuses an accessor call naming a document this value was
// not built for, because answering would hand back this document's fact under
// another document's name.
func (v featureDocumentView) refuseOtherDocument(document, accessor string) {
	if document == v.path {
		return
	}
	panic(fmt.Sprintf(
		"featureDocumentView.%s asked about document %q while presenting %q: "+
			"a view presents one document, so present the document being asked about",
		accessor, document, v.path))
}

// The compiler holds the type to the contract rather than a comment claiming it:
// a signature drifting from the declared facts stops compiling here.
//
// The second assertion is the half the embedded base cannot supply. Embedding
// makes every document-stage fact compile, so the view assertion alone would
// accept a presentation answering none of them; naming the kind's own obligation
// is what makes a Feature fact added to the registry fail here, and on no
// presentation of another kind.
var (
	_ constraint.DocumentView         = featureDocumentView{}
	_ constraint.FeatureDocumentFacts = featureDocumentView{}
)

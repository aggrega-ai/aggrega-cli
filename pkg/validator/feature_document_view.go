// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
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
// the top-level keys its schema block declares.
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
type featureDocumentView struct {
	// FeatureDocumentBase supplies the document-stage facts a Feature does not
	// carry, and declares nothing for the ones it does — so this type writes
	// bodies for exactly the Feature's own facts.
	constraint.FeatureDocumentBase

	// path is the workspace-relative file the document was read from. It is the
	// document coordinate itself, which is why an accessor's argument is checked
	// against it.
	path string

	// pillarKeys are the top-level keys the authored schema block declares, in
	// the order the author wrote them.
	pillarKeys []string
}

// newFeatureSchemaBlockView presents the schema block a load is holding: the file
// the Feature was read from, and the block's own mapping node.
//
// mapping is the node after the caller has settled which of the block's authoring
// shapes it holds, for the reason the Component's own constructor gives: the
// settling has one home and a second reading here could diverge from it.
func newFeatureSchemaBlockView(path string, mapping *yaml.Node) featureDocumentView {
	return featureDocumentView{path: path, pillarKeys: authoredSchemaPillarKeys(mapping)}
}

// FeatureSchemaPillarKeys returns the top-level keys the presented Feature's
// schema block declares, in the order the author wrote them.
//
// They are the keys as authored and not the pillars they were meant to name, so a
// key naming no pillar is among them — which is the whole of what a rule over them
// reports on.
func (v featureDocumentView) FeatureSchemaPillarKeys(document string) []string {
	v.refuseOtherDocument(document, "FeatureSchemaPillarKeys")
	return v.pillarKeys
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

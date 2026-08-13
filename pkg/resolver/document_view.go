// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// aggregateDocumentView presents one authored Aggregate to a check as named
// facts: the bytes the author wrote, and the parameter names the same document's
// contract declares.
//
// It answers about the one document it was built for, and that is the framework's
// contract rather than a simplification of it. A [constraint.DocumentSubject]
// stands for the one document being presented rather than a member of a document
// set, and the entries that invoke a check over a document exist because no point
// in the compilation holds a materialised set of documents. So the document
// argument each accessor takes names the document this value was built for, and
// asking about another document is a mistake rather than a lookup that misses —
// the accessor says so instead of answering.
//
// It satisfies [constraint.DocumentView], which declares one fact an Aggregate
// cannot carry: an Aggregate has no entity inheritance chain, so
// EntityAssertionNameSet refuses rather than projecting something. The reasoning
// for refusing sits on that method.
type aggregateDocumentView struct {
	// path is the workspace-relative file the document was read from. It is the
	// document coordinate itself, which is why an accessor's argument is checked
	// against it.
	path string

	// raw is the document's bytes as authored, before parameter interpolation.
	raw []byte

	// contract is the parameter contract loaded from those same bytes.
	contract types.OrderedMap[any]
}

// newAggregateDocumentView presents the Aggregate a resolve is holding: the file
// it was read from, its bytes as authored, and the parameter contract loaded from
// those bytes.
//
// The two halves are taken together rather than derived here, because a view
// projects facts already produced and never produces one of its own. A caller
// holding both is what makes the presentation possible at all.
func newAggregateDocumentView(
	path string,
	raw []byte,
	contract types.OrderedMap[any],
) aggregateDocumentView {
	return aggregateDocumentView{path: path, raw: raw, contract: contract}
}

// RawAggregateText returns the presented Aggregate's bytes as authored, before
// parameter interpolation.
func (v aggregateDocumentView) RawAggregateText(document string) []byte {
	v.refuseOtherDocument(document, "RawAggregateText")
	return v.raw
}

// DeclaredParameterKeys returns the parameter names the presented Aggregate's
// contract declares, in author order.
func (v aggregateDocumentView) DeclaredParameterKeys(document string) []string {
	v.refuseOtherDocument(document, "DeclaredParameterKeys")
	return v.contract.Keys()
}

// EntityAssertionNameSet refuses: an Aggregate declares no entity inheritance
// chain, so this view holds no assertion names to present.
//
// Refusing is deliberate where returning an empty set would compile and read like
// an answer. An empty set is the truthful answer for an entity that declares no
// assertions, so nothing downstream could tell that answer apart from this one:
// the value would be plausible, permanent, and wrong. A rule reading assertion
// names is presented a catalog entity's document, which is a different document
// and therefore a different presentation.
func (v aggregateDocumentView) EntityAssertionNameSet(string) []string {
	panic("aggregateDocumentView presents an Aggregate, which declares no entity " +
		"inheritance chain: present the catalog entity's own document to read its assertion names")
}

// refuseOtherDocument refuses an accessor call naming a document this value was
// not built for, because answering would hand back this document's fact under
// another document's name.
func (v aggregateDocumentView) refuseOtherDocument(document, accessor string) {
	if document == v.path {
		return
	}
	panic(fmt.Sprintf(
		"aggregateDocumentView.%s asked about document %q while presenting %q: "+
			"a view presents one document, so present the document being asked about",
		accessor, document, v.path))
}

// The compiler holds this type to the contract rather than a comment claiming it:
// a signature drifting from the declared facts stops compiling here.
var _ constraint.DocumentView = aggregateDocumentView{}

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

// ManifestAnnotationBindingKeys refuses: an Aggregate declares no annotation
// binding registry, so this view holds no binding keys to present.
//
// Refusing where an empty set would compile is the same judgement the refusal
// above makes, and it bites harder here. A rule invoked once per binding over an
// empty set is invoked never, and a rule that never runs raises nothing — which
// is indistinguishable from a rule that ran and approved.
func (v aggregateDocumentView) ManifestAnnotationBindingKeys(string) []string {
	panic("aggregateDocumentView presents an Aggregate, which declares no annotation binding " +
		"registry: present the Manifest to read its binding keys")
}

// ManifestAnnotationBindingFeature refuses, for the reason above.
func (v aggregateDocumentView) ManifestAnnotationBindingFeature(string, string) (string, bool) {
	panic("aggregateDocumentView presents an Aggregate, which declares no annotation binding " +
		"registry: present the Manifest to read a binding's Feature")
}

// manifestDocumentView presents one authored Manifest to a check as named facts:
// the annotation binding keys its registry declares, and the Feature each of
// those bindings routes to.
//
// It is the Manifest's counterpart to [aggregateDocumentView] and is deliberately
// the same shape rather than a shape of its own: both satisfy the one generated
// [constraint.DocumentView], both answer only about the document they were built
// for, and both refuse the facts their document cannot carry. A separate
// Manifest-only view interface written beside that one would give the document
// stage two contracts, and a check would then have to know which of them it was
// handed.
//
// It answers about the one document it was built for, on the same terms
// [aggregateDocumentView] does: an accessor asked about another document refuses
// rather than answering, because answering would hand back this document's fact
// under another document's name.
//
// It presents the flat registry — the bindings written under
// mappings.features.annotations — and not the per-provider tiers a Manifest may
// also carry. That is the surface annotation expansion consumes, so presenting it
// is presenting what the compiler acts on; the bound is stated on the fact's own
// declaration so a rule meets it there rather than discovering it.
type manifestDocumentView struct {
	// path is the workspace-relative file the Manifest was read from. It is the
	// document coordinate itself, which is why an accessor's argument is checked
	// against it.
	path string

	// bindings is the annotation binding registry built from that document.
	//
	// The registry is held rather than the Manifest, and that is the point
	// rather than a convenience: the registry is where reading the flat tier
	// already happens, so presenting from it keeps one reading of that tier
	// instead of a second one here that agrees with it today.
	bindings *AnnotationRegistry
}

// newManifestDocumentView presents the Manifest a caller is holding: the file it
// was read from, and the annotation binding registry derived from it.
//
// The registry is derived here rather than taken from the caller because it has
// exactly one producer, and routing the presentation through that producer is
// what keeps the fact single-valued. A caller passing its own registry could pass
// one built from a different document.
func newManifestDocumentView(path string, manifest *types.Manifest) manifestDocumentView {
	return manifestDocumentView{path: path, bindings: BuildAnnotationRegistry(manifest)}
}

// ManifestAnnotationBindingKeys returns the annotation keys the presented
// Manifest's registry declares, in author order.
func (v manifestDocumentView) ManifestAnnotationBindingKeys(document string) []string {
	v.refuseOtherDocument(document, "ManifestAnnotationBindingKeys")
	return v.bindings.Keys()
}

// ManifestAnnotationBindingFeature returns the Feature the named binding routes
// to, and whether the presented Manifest declares a binding of that key at all.
func (v manifestDocumentView) ManifestAnnotationBindingFeature(
	document string,
	binding string,
) (string, bool) {
	v.refuseOtherDocument(document, "ManifestAnnotationBindingFeature")
	declared, ok := v.bindings.Lookup(binding)
	if !ok {
		return "", false
	}
	return declared.Feature, true
}

// RawAggregateText refuses: a Manifest is not an Aggregate, so this view holds no
// Aggregate text to present.
func (v manifestDocumentView) RawAggregateText(string) []byte {
	panic("manifestDocumentView presents a Manifest: present the Aggregate to read its text " +
		"as authored")
}

// DeclaredParameterKeys refuses: a Manifest declares no parameter contract, so
// this view holds no parameter names to present.
//
// An empty set is the truthful answer for an Aggregate declaring no parameters,
// so nothing downstream could tell that answer apart from this one.
func (v manifestDocumentView) DeclaredParameterKeys(string) []string {
	panic("manifestDocumentView presents a Manifest, which declares no parameter contract: " +
		"present the Aggregate to read its declared parameters")
}

// EntityAssertionNameSet refuses: a Manifest declares no entity inheritance
// chain, so this view holds no assertion names to present.
func (v manifestDocumentView) EntityAssertionNameSet(string) []string {
	panic("manifestDocumentView presents a Manifest, which declares no entity inheritance " +
		"chain: present the catalog entity's own document to read its assertion names")
}

// refuseOtherDocument refuses an accessor call naming a document this value was
// not built for, for the reason [aggregateDocumentView.refuseOtherDocument]
// states.
func (v manifestDocumentView) refuseOtherDocument(document, accessor string) {
	if document == v.path {
		return
	}
	panic(fmt.Sprintf(
		"manifestDocumentView.%s asked about document %q while presenting %q: "+
			"a view presents one document, so present the document being asked about",
		accessor, document, v.path))
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

// The compiler holds both types to the contract rather than a comment claiming
// it: a signature drifting from the declared facts stops compiling here.
//
// Both are asserted against the one generated interface, which is what makes the
// two presentations the same shape rather than two shapes that agree today.
var (
	_ constraint.DocumentView = aggregateDocumentView{}
	_ constraint.DocumentView = manifestDocumentView{}
)

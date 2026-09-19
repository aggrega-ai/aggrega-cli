// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// extendsChainShape is what one walk found about the shape of the chain it
// followed, in the terms the two rules over that shape read: whether the chain
// returned to an entity already in it and at which edge, and how many entities a
// chain that ended cleanly reached.
//
// It carries strings and counts rather than the chain itself, which is what lets
// one value serve every authored kind that declares extends: the walk is generic
// over the entity type it follows, and the shape it produces is not.
//
// The derivation of what each rule reads lives on this type rather than at each
// view, which is what keeps three carriers' presentations answering one question
// the same way. A per-view derivation would agree on the day it was written and
// would be free to diverge afterwards, at which point one authored kind's chain
// would be judged by a rule the other two are exempt from.
type extendsChainShape struct {
	// cycle is the edge at which the chain returned to an entity already in it.
	// It is meaningful only when closes is true.
	cycle constraint.ExtendsChainCycleEdge

	// closes reports whether the chain returned to an entity already in it.
	closes bool

	// reached is the number of entities the walk reached. It is the chain's
	// length only where the chain ended cleanly, which is why it is read
	// through length rather than directly.
	reached int
}

// cycleEdge is the answer the cycle fact carries: the edge that closes the
// chain, and whether the chain closes on itself at all.
func (s extendsChainShape) cycleEdge() (constraint.ExtendsChainCycleEdge, bool) {
	return s.cycle, s.closes
}

// length is the answer the length fact carries: how many entities a chain that
// ends cleanly reached, and whether it ended cleanly.
//
// A chain that closes on itself has no length to report, and reporting one is
// what would let a rule comparing the count against a bound judge a loop by its
// length — advice to shorten a chain cannot remove a loop. Answering the
// question here rather than at each rule is what makes the two rules invocable
// in either order.
func (s extendsChainShape) length() (int, bool) {
	if s.closes {
		return 0, false
	}
	return s.reached, true
}

// manifestChainDocumentView presents to a check what a walk found about the
// shape of the chain a Manifest extends.
//
// It is a second Manifest presentation rather than a field on the one the
// resolve already builds, and the split is forced rather than chosen: that one
// is built from a Manifest document and lives in the package that reads
// Manifests, while this one is built from a walk and lives here, where the walk
// is. The package that reads Manifests depends on this one, so a single type
// could not sit in both.
//
// It answers about the one document it was built for, which is the framework's
// contract rather than a simplification of it. The chain has no file of its own,
// because it is assembled from links reached by reference rather than from one
// file a caller read, so the document it answers about is the empty one and a
// finding is attributed to the Manifest the resolution was requested for.
//
// It satisfies the whole document-stage view, which declares facts this
// presentation cannot carry: an Aggregate's text and parameter contract, a
// Component's assertions, a Recipe's overlay and source pointer. The embedded
// base supplies those and refuses each at the point a check reads it, naming the
// document kind that does carry it. What the base cannot supply is the Manifest's
// OWN facts, which this type therefore writes — and the ones this presentation
// was not built for refuse here, in the same terms and for the same reason.
type manifestChainDocumentView struct {
	// ManifestDocumentBase supplies the document-stage facts a Manifest does not
	// carry, and declares nothing for the ones it does — so this type writes
	// bodies for exactly the Manifest's own facts.
	constraint.ManifestDocumentBase

	// shape is what the walk found about the chain's structure.
	shape extendsChainShape
}

// newManifestChainDocumentView presents what a walk found about the shape of the
// chain a Manifest extends.
func newManifestChainDocumentView(shape extendsChainShape) manifestChainDocumentView {
	return manifestChainDocumentView{shape: shape}
}

// ManifestExtendsChainCycleEdge returns the edge at which the chain the
// presented Manifest extends returns to a Manifest already in it, and whether
// the chain closes on itself at all.
func (v manifestChainDocumentView) ManifestExtendsChainCycleEdge(
	document string,
) (constraint.ExtendsChainCycleEdge, bool) {
	v.refuseOtherDocument(document, "ManifestExtendsChainCycleEdge")
	return v.shape.cycleEdge()
}

// ManifestExtendsChainLength returns how many Manifests the chain the presented
// Manifest extends reaches, and whether that chain ends cleanly.
func (v manifestChainDocumentView) ManifestExtendsChainLength(document string) (int, bool) {
	v.refuseOtherDocument(document, "ManifestExtendsChainLength")
	return v.shape.length()
}

// ManifestAnnotationBindingKeys refuses: this view carries a chain's shape.
func (v manifestChainDocumentView) ManifestAnnotationBindingKeys(document string) []string {
	panic(v.refusal("ManifestAnnotationBindingKeys"))
}

// ManifestAnnotationBindingFeature refuses: this view carries a chain's shape.
func (v manifestChainDocumentView) ManifestAnnotationBindingFeature(
	document, binding string,
) (string, bool) {
	panic(v.refusal("ManifestAnnotationBindingFeature"))
}

// ManifestAnnotationBindingRoutes refuses: this view carries a chain's shape.
func (v manifestChainDocumentView) ManifestAnnotationBindingRoutes(
	document, binding string,
) []constraint.AnnotationBindingRoute {
	panic(v.refusal("ManifestAnnotationBindingRoutes"))
}

// AnnotationBindingSubkeyTarget refuses: this view carries a chain's shape.
func (v manifestChainDocumentView) AnnotationBindingSubkeyTarget(
	binding, subkey string,
) (constraint.AnnotationSubkeyTarget, bool) {
	panic(v.refusal("AnnotationBindingSubkeyTarget"))
}

// ManifestRecipeMappingEntries refuses: this view carries a chain's shape.
func (v manifestChainDocumentView) ManifestRecipeMappingEntries(
	document string,
) []constraint.ManifestRecipeMapping {
	panic(v.refusal("ManifestRecipeMappingEntries"))
}

// ManifestRecipeMappingReference refuses: this view carries a chain's shape.
func (v manifestChainDocumentView) ManifestRecipeMappingReference(
	document, tier, axis, key string,
) (string, bool) {
	panic(v.refusal("ManifestRecipeMappingReference"))
}

// refusal is what this view says about a fact it was not built to answer.
//
// Answering such a fact would report an absence the document does not have, and a
// rule cannot tell an empty answer from a Manifest that declared nothing. The
// message names where the fact IS read from, because the caller's mistake is
// which presentation it built rather than which accessor it called.
func (v manifestChainDocumentView) refusal(accessor string) string {
	return fmt.Sprintf(
		"manifestChainDocumentView.%s asked of a view built from the shape of the chain the "+
			"Manifest extends: the fact is read from the Manifest's own document, so present that",
		accessor)
}

// refuseOtherDocument refuses an accessor call naming a document this value was
// not built for, because answering would hand back this document's fact under
// another document's name.
//
// The chain has no file of its own, so the document this view answers about is
// the empty one and every accessor is called with it.
func (v manifestChainDocumentView) refuseOtherDocument(document, accessor string) {
	if document == "" {
		return
	}
	panic(fmt.Sprintf(
		"manifestChainDocumentView.%s asked about document %q while presenting a chain assembled "+
			"from links reached by reference, which has no file of its own: ask about that chain",
		accessor, document))
}

// The compiler holds the type to the contract rather than a comment claiming it:
// a signature drifting from the declared facts stops compiling here.
//
// The second assertion is the half the embedded base cannot supply. Embedding
// makes every document-stage fact compile, so the view assertion alone would
// accept a presentation answering none of them; naming the kind's own obligation
// is what makes a Manifest fact added to the registry fail here, and on no
// presentation of another kind.
var (
	_ constraint.DocumentView          = manifestChainDocumentView{}
	_ constraint.ManifestDocumentFacts = manifestChainDocumentView{}
)

// releaseChainDocumentView presents to a check what a walk found about the shape
// of the chain a Release extends.
//
// It is a second Release presentation rather than a field on the one the load
// already builds, and the split is forced rather than chosen, exactly as it is
// for the Manifest above: that one is built from a Release document and lives in
// the package that reads Releases, while this one is built from a walk and lives
// here, where the walk is. The package that reads Releases depends on this one,
// so a single type could not sit in both.
//
// It answers about the one document it was built for, which is the framework's
// contract rather than a simplification of it. The chain has no file of its own,
// because it is assembled from links reached by reference rather than from one
// file a caller read, so the document it answers about is the empty one and a
// finding is attributed to the Release the resolution was requested for.
//
// It satisfies the whole document-stage view, which declares facts this
// presentation cannot carry. The embedded base supplies those and refuses each at
// the point a check reads it, naming the document kind that does carry it. What
// the base cannot supply is the Release's OWN facts, which this type therefore
// writes — and the one this presentation was not built for refuses here, in the
// same terms and for the same reason.
type releaseChainDocumentView struct {
	// ReleaseDocumentBase supplies the document-stage facts a Release does not
	// carry, and declares nothing for the ones it does — so this type writes
	// bodies for exactly the Release's own facts.
	constraint.ReleaseDocumentBase

	// shape is what the walk found about the chain's structure.
	shape extendsChainShape
}

// newReleaseChainDocumentView presents what a walk found about the shape of the
// chain a Release extends.
func newReleaseChainDocumentView(shape extendsChainShape) releaseChainDocumentView {
	return releaseChainDocumentView{shape: shape}
}

// ReleaseExtendsChainCycleEdge returns the edge at which the chain the presented
// Release extends returns to a Release already in it, and whether the chain
// closes on itself at all.
func (v releaseChainDocumentView) ReleaseExtendsChainCycleEdge(
	document string,
) (constraint.ExtendsChainCycleEdge, bool) {
	v.refuseOtherDocument(document, "ReleaseExtendsChainCycleEdge")
	return v.shape.cycleEdge()
}

// ReleaseExtendsChainLength returns how many Releases the chain the presented
// Release extends reaches, and whether that chain ends cleanly.
func (v releaseChainDocumentView) ReleaseExtendsChainLength(document string) (int, bool) {
	v.refuseOtherDocument(document, "ReleaseExtendsChainLength")
	return v.shape.length()
}

// ReleaseBoundaryPlacements refuses: this view carries a chain's shape.
func (v releaseChainDocumentView) ReleaseBoundaryPlacements() []constraint.BoundaryPlacement {
	panic(v.refusal("ReleaseBoundaryPlacements"))
}

// ReleaseExtendsChainBoundariesDeclaredBy refuses: this view carries what the
// walk found about a chain's shape, and which link declares which routing key is
// a property of the links' content.
//
// The walk reads references and never the blocks a link authors, so the answer
// is not something this presentation declined to carry — it is something the
// walk never saw. The presentation built where the chain is folded carries it,
// because folding is the point at which every link's content is in hand.
func (v releaseChainDocumentView) ReleaseExtendsChainBoundariesDeclaredBy(
	document string,
) (string, bool) {
	panic(v.refusal("ReleaseExtendsChainBoundariesDeclaredBy"))
}

// ReleaseExtendsChainComponentsDeclaredBy refuses, for the reason
// [releaseChainDocumentView.ReleaseExtendsChainBoundariesDeclaredBy] states.
func (v releaseChainDocumentView) ReleaseExtendsChainComponentsDeclaredBy(
	document string,
) (string, bool) {
	panic(v.refusal("ReleaseExtendsChainComponentsDeclaredBy"))
}

// ReleaseExtendsChainInheritedLiteralDirectories refuses, for the reason
// [releaseChainDocumentView.ReleaseExtendsChainBoundariesDeclaredBy] states.
func (v releaseChainDocumentView) ReleaseExtendsChainInheritedLiteralDirectories(
	document string,
) []string {
	panic(v.refusal("ReleaseExtendsChainInheritedLiteralDirectories"))
}

// refusal is what this view says about a fact it was not built to answer.
//
// Answering such a fact would report an absence the document does not have, and a
// rule cannot tell an empty answer from a Release that declared nothing. The
// message names where the fact IS read from, because the caller's mistake is
// which presentation it built rather than which accessor it called.
func (v releaseChainDocumentView) refusal(accessor string) string {
	return fmt.Sprintf(
		"releaseChainDocumentView.%s asked of a view built from the shape of the chain the "+
			"Release extends: the fact is read from the Release's own document, so present that",
		accessor)
}

// refuseOtherDocument refuses an accessor call naming a document this value was
// not built for, because answering would hand back this document's fact under
// another document's name.
//
// The chain has no file of its own, so the document this view answers about is
// the empty one and every accessor is called with it.
func (v releaseChainDocumentView) refuseOtherDocument(document, accessor string) {
	if document == "" {
		return
	}
	panic(fmt.Sprintf(
		"releaseChainDocumentView.%s asked about document %q while presenting a chain assembled "+
			"from links reached by reference, which has no file of its own: ask about that chain",
		accessor, document))
}

// The compiler holds the type to the contract rather than a comment claiming it,
// for the reason the Manifest chain presentation's own assertions state.
var (
	_ constraint.DocumentView         = releaseChainDocumentView{}
	_ constraint.ReleaseDocumentFacts = releaseChainDocumentView{}
)

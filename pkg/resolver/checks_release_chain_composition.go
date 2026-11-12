// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The rules over what a Release extends chain composes to, expressed as
// constraint declarations. See checks.go for what a declaration here is.
//
// Both read facts no single document carries. Every Release along a chain is
// well-formed on its own — the envelope holds each one to that as it is read —
// and what these two are about appears only once the links are composed. That is
// why the view below is built at the fold rather than at the load, and why
// neither rule could be asked one document at a time.
//
// One halts and one advises, and the difference is in the declarations rather
// than in which collection a caller appends the output to. A composition
// carrying two mutually exclusive routing keys is a shape no author may write,
// so it halts. A directory inherited without a template variable is a shape an
// author may perfectly well mean, so it advises: the reading is an inference
// about intent, and a rule that halted on an inference would refuse legal input
// on a guess.

// releaseChainCompositionView presents to a check what folding a Release extends
// chain found about the document the chain composes to.
//
// It is a third Release view, beside the one built from a Release document and
// the one built from a walk of its chain, and the split is forced rather than
// chosen. The document view holds one file, so it cannot see a composition. The
// walk view holds references and never the blocks a link authors, so it cannot
// see content. Folding is the one point at which every link's content is in
// hand, and these facts are readable nowhere else.
//
// It answers about the one document it was built for, which is the framework's
// contract rather than a simplification of it. A composition has no file of its
// own — it is assembled from links reached by reference — so the document it
// answers about is the empty one, and a finding is attributed to the Release the
// resolution was requested for.
//
// It satisfies the whole document-stage view, which declares facts this one
// cannot carry. The embedded base supplies those; the Release's own facts it
// does not carry are refused here, in the same terms.
type releaseChainCompositionView struct {
	// ReleaseDocumentBase supplies the document-stage facts a Release does not
	// carry, and declares nothing for the ones it does — so this type writes
	// bodies for exactly the Release's own facts.
	constraint.ReleaseDocumentBase

	boundariesDeclaredBy string
	componentsDeclaredBy string
	inheritedLiteralDirs []string
}

// ReleaseExtendsChainBoundariesDeclaredBy returns the Release along the chain
// that declares named boundaries, and whether any Release along it does.
func (v releaseChainCompositionView) ReleaseExtendsChainBoundariesDeclaredBy(
	document string,
) (string, bool) {
	v.refuseOtherDocument(document, "ReleaseExtendsChainBoundariesDeclaredBy")
	return v.boundariesDeclaredBy, v.boundariesDeclaredBy != ""
}

// ReleaseExtendsChainComponentsDeclaredBy returns the Release along the chain
// that declares top-level components, and whether any Release along it does.
func (v releaseChainCompositionView) ReleaseExtendsChainComponentsDeclaredBy(
	document string,
) (string, bool) {
	v.refuseOtherDocument(document, "ReleaseExtendsChainComponentsDeclaredBy")
	return v.componentsDeclaredBy, v.componentsDeclaredBy != ""
}

// ReleaseExtendsChainInheritedLiteralDirectories returns the boundary paths
// whose directory the composition inherited and which carry no template
// variable.
func (v releaseChainCompositionView) ReleaseExtendsChainInheritedLiteralDirectories(
	document string,
) []string {
	v.refuseOtherDocument(document, "ReleaseExtendsChainInheritedLiteralDirectories")
	return v.inheritedLiteralDirs
}

// ReleaseExtendsChainCycleEdge refuses: this view carries what a fold found, and
// whether the chain closed on itself was decided before the fold ran.
func (v releaseChainCompositionView) ReleaseExtendsChainCycleEdge(
	document string,
) (constraint.ExtendsChainCycleEdge, bool) {
	panic(v.refusal("ReleaseExtendsChainCycleEdge"))
}

// ReleaseExtendsChainLength refuses, for the reason
// [releaseChainCompositionView.ReleaseExtendsChainCycleEdge] states.
func (v releaseChainCompositionView) ReleaseExtendsChainLength(document string) (int, bool) {
	panic(v.refusal("ReleaseExtendsChainLength"))
}

// ReleaseBoundaryPlacements refuses: this view carries a composition's shape,
// and a placement is read from the Release document that carries it.
func (v releaseChainCompositionView) ReleaseBoundaryPlacements() []constraint.BoundaryPlacement {
	panic(v.refusal("ReleaseBoundaryPlacements"))
}

// BoundaryPlacements refuses, for the reason
// [releaseChainCompositionView.ReleaseBoundaryPlacements] states.
func (v releaseChainCompositionView) BoundaryPlacements() []constraint.BoundaryPlacementInstance {
	panic(v.refusal("BoundaryPlacements"))
}

// refusal is what this view says about a fact it was not built to answer.
func (v releaseChainCompositionView) refusal(accessor string) string {
	return fmt.Sprintf(
		"releaseChainCompositionView.%s asked of a view built from what folding a Release extends "+
			"chain produced: the fact is read elsewhere, so present the document or the walk that "+
			"carries it", accessor)
}

// refuseOtherDocument refuses an accessor call naming a document this value was
// not built for. A composition has no file of its own, so the document it
// answers about is the empty one and every accessor is called with it.
func (v releaseChainCompositionView) refuseOtherDocument(document, accessor string) {
	if document == "" {
		return
	}
	panic(fmt.Sprintf(
		"releaseChainCompositionView.%s asked about document %q while presenting what a chain "+
			"composes to, which has no file of its own: ask about that composition",
		accessor, document))
}

// The compiler holds the type to the contract rather than a comment claiming it.
var (
	_ constraint.DocumentView         = releaseChainCompositionView{}
	_ constraint.ReleaseDocumentFacts = releaseChainCompositionView{}
)

// releaseChainComposesOneRoutingShape reports a chain whose links between them
// declare both named boundaries and top-level components.
//
// It names both Releases and both keys. Neither document is at fault on its own
// — each is a shape the envelope accepts — so a message naming only the one the
// build was pointed at would send its author to the file with nothing wrong in
// it. The two are captioned rather than bare because the wording names neither,
// and a reader handed two quoted paths has no way to tell which declared which.
var releaseChainComposesOneRoutingShape = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrReleaseChainComposesBothRoutingKeys(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		boundariesAt, declaresBoundaries := v.ReleaseExtendsChainBoundariesDeclaredBy("")
		componentsAt, declaresComponents := v.ReleaseExtendsChainComponentsDeclaredBy("")
		if !declaresBoundaries || !declaresComponents {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(ErrReleaseChainComposesBothRoutingKeys(),
			constraint.Captioned("boundaries declared by", boundariesAt),
			constraint.Captioned("components declared by", componentsAt))}
	},
}

// inheritedBoundaryDirectoryResolvesWhereItsParentWrites reports each boundary
// whose output directory the composition inherited and which carries no template
// variable, so parent and child resolve it to one path.
//
// It raises once per such boundary rather than once for the composition, because
// the boundary is what an author would change and a single finding listing
// several would make them read a list to find which.
var inheritedBoundaryDirectoryResolvesWhereItsParentWrites = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrInheritedBoundaryDirectoryIsLiteral(),
	Severity: constraint.SeverityWarning,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentSubject,
	) []constraint.Objection {
		var out []constraint.Objection
		for _, boundary := range v.ReleaseExtendsChainInheritedLiteralDirectories("") {
			out = append(out, s.RaiseWith(ErrInheritedBoundaryDirectoryIsLiteral(),
				constraint.Uncaptioned(boundary)))
		}
		return out
	},
}

// directoryCarriesATemplateVariable reports whether a boundary's authored
// directory defers to something resolved per Release.
//
// It reads the template's own opening delimiter rather than parsing the text,
// and the looseness is deliberate in one direction only: anything carrying the
// delimiter is treated as deferred and draws no advisory, so the reading never
// advises about a directory that does vary. A value using the delimiter for
// something that resolves identically everywhere is missed, which leaves an
// author un-advised about a path they may have meant anyway — the direction this
// rule already errs in by not halting.
func directoryCarriesATemplateVariable(directory string) bool {
	return strings.Contains(directory, "{{")
}

// releaseChainCompositionFacts reads, from a walked chain and the Release it
// composed to, the facts the two composition rules are asked about.
//
// The boundaries and components answers name the FIRST Release along the chain
// declaring each, walking from the child outward, because that is the one whose
// author most recently made the choice and so the one a reader opens first.
//
// Which routing key a link authored is read from the synthesized marker
// rather than from the components block, and it has to be. A Release is
// normalized as it is loaded: a top-level components surface is folded into a
// boundary named default and the components block is cleared, so by the time a
// link reaches a fold its components are gone and every link presents
// boundaries. The marker is the only thing that still distinguishes the two,
// which is what it is for — it is derived provenance the authoring surface
// cannot carry, set at normalization and nowhere else.
//
// Reading the components block here instead would find it empty on every link
// and clear every chain, silently: the rule would be declared, invoked, and
// incapable of objecting.
func releaseChainCompositionFacts(
	chain []ResolvedRelease,
	composed *types.Release,
) releaseChainCompositionView {
	view := releaseChainCompositionView{}
	for _, link := range chain {
		authored, synthesized := routingKeysAuthoredBy(link.Release)
		if view.boundariesDeclaredBy == "" && authored {
			view.boundariesDeclaredBy = link.Path
		}
		if view.componentsDeclaredBy == "" && synthesized {
			view.componentsDeclaredBy = link.Path
		}
	}
	if composed != nil {
		child := chain[0].Release
		view.inheritedLiteralDirs = inheritedLiteralDirectories(
			"", child.Spec.Boundaries, composed.Spec.Boundaries)
	}
	return view
}

// routingKeysAuthoredBy reports which routing shape one link's author wrote:
// named boundaries, top-level components, or neither.
//
// Both answers are read off the boundary map because normalization has already
// moved one of them there. A boundary the compiler synthesized stands for a
// components block the author wrote; every other boundary is one they named.
func routingKeysAuthoredBy(rel *types.Release) (authoredBoundaries, authoredComponents bool) {
	for _, boundary := range rel.Spec.Boundaries.All() {
		if boundary.Synthesized {
			authoredComponents = true
			continue
		}
		authoredBoundaries = true
	}
	return authoredBoundaries, authoredComponents
}

// inheritedLiteralDirectories walks the composed boundary tree beside the
// child's own and collects every boundary carrying a directory the child did not
// write and which resolves to one path.
//
// It walks the composition rather than the parent because the composition is
// what the build will use: a boundary the child never mentioned and a boundary
// it redeclared without a directory are the same case to an author, and both
// reach the parent's value.
func inheritedLiteralDirectories(
	prefix string,
	authored, composed types.OrderedMap[types.Boundary],
) []string {
	var out []string
	for name, composedBoundary := range composed.All() {
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		authoredBoundary, wasAuthored := authored.Get(name)
		childWroteDirectory := wasAuthored && authoredBoundary.Directory != ""
		if composedBoundary.Directory != "" &&
			!childWroteDirectory &&
			!directoryCarriesATemplateVariable(composedBoundary.Directory) {
			out = append(out, path)
		}
		out = append(out, inheritedLiteralDirectories(
			path, authoredBoundary.Boundaries, composedBoundary.Boundaries)...)
	}
	return out
}

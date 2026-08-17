// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// assembledOutputFacts is the seam the presentation below answers the render's
// own assembled-output facts through.
//
// It exists as a declared seam rather than as two method bodies reaching into the
// result for two reasons, and the second is the load-bearing one. The render
// already retains what these two facts read — the attribution beside each
// assembled group — so a presentation deriving them again would give one fact two
// producers, free to disagree with nothing comparing them; naming the seam is
// what makes the delegation visible as delegation. And the seam's method set is
// how the guard beside this file tells a fact answered from something the render
// already holds from a fact answered from the retention taken for this stage:
// with the split declared here, that guard derives it rather than carrying a list
// of which facts are which, and a list is exactly what would go stale the moment
// a further fact is declared at this stage.
type assembledOutputFacts interface {
	// EmittedFilePathSet are the output paths written for one provider within one
	// boundary, in path order.
	EmittedFilePathSet(boundary string, provider string) []string

	// AssembledFileContent is the bytes standing at one output path for one
	// provider within one boundary.
	AssembledFileContent(boundary string, provider string, path string) []byte
}

// assembledOutput answers those two facts from the render's own assembly
// attribution.
//
// It holds the result rather than a copy of the attribution, because the values
// it reads are the render's and are not written to once assembly has flushed
// them; a copy would carry nothing the originals do not.
//
// Neither accessor re-derives anything. The path set is the result's own reader
// verbatim, and the content reader differs from the fact by one result: the
// result reports whether the boundary and provider wrote the path at all, and a
// fact accessor returns a value and never a report, so the not-written case
// arrives as no bytes. That is the same answer this stage's other accessors give
// for a coordinate nothing recorded, so it needs no separate treatment — but it
// is an adaptation rather than a pass-through, and it is spelled out here because
// this is the only place the two shapes meet.
type assembledOutput struct {
	result *RenderResult
}

// EmittedFilePathSet returns the output paths written for one provider within one
// boundary, in path order.
func (a assembledOutput) EmittedFilePathSet(boundary string, provider string) []string {
	return a.result.EmittedPaths(boundary, provider)
}

// AssembledFileContent returns the bytes standing at one output path for one
// provider within one boundary, and no bytes when that pair wrote no such path.
func (a assembledOutput) AssembledFileContent(boundary string, provider string, path string) []byte {
	content, _ := a.result.AssembledFileContent(boundary, provider, path)
	return content
}

// renderedView presents, as named facts, what a render produced: the output paths
// each provider wrote within each boundary, the bytes standing at one of them,
// the artifact names each node projected, and the bytes each of those artifacts
// evaluated to.
//
// It satisfies [constraint.RenderedView], and the compiler holds it to that
// contract at the bottom of this file rather than a comment claiming it.
//
// # The earlier stage is embedded, not re-projected
//
// A stage's view contains the stage it extends, so every fact complete before the
// render's output existed is readable here. Those facts are answered by the
// presentation of the state each node reached before its artifact bytes existed,
// embedded and delegated to, rather than derived again: one fact has one
// producer, and two producers of a fact are free to disagree with nothing
// comparing them. That presentation in turn embeds the one below it, so a rule
// reading an earlier fact through this value reaches the same answer that
// stage's own presentation gives.
//
// # Two facts come from the render's own retention and two from this stage's
//
// The output-path set and the bytes at one path are read from the attribution the
// assembly retains beside each group, through the seam above. The artifact name
// set and the bytes one artifact evaluated to are read from the retention the
// artifacts walk fills, because the state those describe is a loop-local: it is
// built for one node, used to render it, and dropped when that node's turn ends.
//
// That split is not a convenience. Deriving the artifact facts from the text
// retained before evaluation would fail twice over, and both failures are silent:
// the retained text is the entry BEFORE anything evaluates it, so producing bytes
// from it means evaluating a template again against a context this presentation
// does not hold; and the two key sets differ, because the text is recorded before
// the walk skips an entry that evaluates to nothing and before it refuses a name
// already emitted, so such an entry is in the earlier key set and absent from
// this one.
//
// # A node or a coordinate the render did not reach answers with a zero value
//
// A fact accessor returns a value and never an error, so a node absent from the
// retention is answered with the zero value of the fact asked for. For a node
// that projected no artifacts that is also the truthful answer, and deliberately
// so at this layer: every artifact the walk projects is recorded, so a node with
// no answer is one that projected nothing or one a render never reached — and a
// render that stopped short reports why it stopped, which is not this stage's
// finding.
//
// # Values are handed over uncopied
//
// The contract every view shares makes a fact's value immutable to a check, and
// this value keeps that contract. What an accessor returns is the retained value
// itself rather than a copy of it, so a check that wrote through one would change
// what every later reader sees; no check does, and the contract is where that is
// declared rather than here.
type renderedView struct {
	constraint.HydratedView
	assembledOutputFacts

	// projections is the render's retention of what each node's artifacts walk
	// projected. It is held by pointer because the retention is filled as the
	// node walk proceeds.
	projections *artifactProjections
}

// newRenderedView presents what a render produced, with the facts complete
// earlier answered by the presentation of the state each node reached before its
// artifact bytes existed.
//
// It is unexported because the render is the one thing that builds it: both
// sources it reads are the render's own working state, so a caller outside this
// package has nothing to construct it from and the bypass does not compile.
//
// The products are taken rather than reached for, because a view projects facts
// already produced and never produces one of its own.
//
// A build constructs exactly one of these and publishes it on the render's
// result. Constructing a second one over a different retention would give this
// stage's facts a second producer, free to disagree with the first about an
// artifact neither of them records twice.
//
// It returns nil when the earlier presentation, the assembled-output seam or the
// retention is absent. A presentation missing any of them would answer a whole
// class of facts from nothing, and a presentation that answers emptily is worse
// than an absent one, because a rule reading it cannot tell an empty answer from
// a real one.
func newRenderedView(
	earlier constraint.HydratedView,
	assembled assembledOutputFacts,
	projections *artifactProjections,
) constraint.RenderedView {
	if earlier == nil || assembled == nil || projections == nil {
		return nil
	}
	return &renderedView{
		HydratedView:         earlier,
		assembledOutputFacts: assembled,
		projections:          projections,
	}
}

// ArtifactProjectionNameSet returns the artifact names a node projected, in
// author order.
//
// Author order is the fact rather than an incidental property of it: the walk
// reaches a Recipe's entries in the order that document declares them, and a
// library bundle's files in the order the bundle declares them, so the order this
// returns is the order somebody wrote. The names are the output names each entry
// resolved to, which for an inline entry is the authored key and for an imported
// one is the imported file's own name.
//
// The set is the RENDER's rather than the Recipe's. An entry whose template
// evaluated to nothing is not here, because the walk emits no file for it; a rule
// reading this therefore asks about artifacts that exist in the output rather
// than about entries that were declared.
func (v *renderedView) ArtifactProjectionNameSet(node string) []string {
	p, ok := v.projections.projection(node)
	if !ok {
		return nil
	}
	return p.ArtifactProjectionNameSet
}

// ArtifactProjectionContent returns the bytes one of a node's artifacts evaluated
// to.
//
// The artifact is named by the output name the entry resolved to, which is the
// name this stage's own name-set fact enumerates and the name the traversal
// invoking a rule keys each subject by.
//
// An artifact the retention does not hold answers with no bytes, which is what a
// node the render did not reach, an entry that evaluated to nothing, and an
// artifact of an attachment rather than of the node's own Recipe all give.
func (v *renderedView) ArtifactProjectionContent(node string, artifact string) []byte {
	p, ok := v.projections.projection(node)
	if !ok {
		return nil
	}
	return p.ArtifactProjectionContent[artifact]
}

var _ constraint.RenderedView = (*renderedView)(nil)

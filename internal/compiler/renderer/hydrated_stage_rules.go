// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// oneRecordedArtifact is the node axis and the artifact axis for a rule asked
// about a single artifact entry, narrowed to exactly the entry whose text the
// walk has just recorded.
//
// # Why the subject set is narrowed to one rather than swept
//
// The traversal it is handed to walks every node an axis yields and asks the
// artifact axis for each. Handed the build's own axes it would, at the k-th entry
// of a node, re-present the k-1 entries already recorded — so a node projecting n
// artifacts would be asked about n(n+1)/2 subjects and would raise the same
// finding once for every later entry in the same Recipe. An author would read one
// authoring fault repeated a number of times that depends on where in the block
// it was written.
//
// # Why one value serves both axes
//
// The pair is the subject: this node, this entry. Two values could be narrowed to
// two different entries and nothing would compare them, and the failure would be
// a rule invoked about an entry other than the one just recorded — which reads
// exactly like a rule that ran and approved. One value cannot be mispaired.
//
// # Why the output name is taken from the walk rather than read back
//
// It is the same variable the recording call on the line above was given, so the
// name a rule is invoked over and the name its facts are keyed by are one value
// rather than two readings that agree today. Reading the recorded key set back
// and filtering it would look more careful and would be weaker: it would
// introduce a case where the filter finds nothing and the rule is silently
// skipped, which is the one outcome indistinguishable from approval.
//
// # What the node coordinate carries, and what it deliberately does not
//
// Only the node's identity is supplied. On this axis that is the whole of what a
// node contributes: the traversal keys every fact by the node id, takes the file
// a finding is positioned in from the artifact coordinate rather than from the
// node, and the rule declared over this kind states that the node is deliberately
// absent from its detail because the fault is in the Recipe's text rather than in
// any one node that binds it. Supplying the node's other coordinates from what
// this site happens to hold would position a finding from values the axis never
// reads, which is how a locator comes to point somewhere plausible and wrong —
// and an absent label leaves a finding unpositioned rather than mispositioned.
type oneRecordedArtifact struct {
	// nodeID is the node whose Recipe projected the entry.
	nodeID string

	// outputName is the name the entry resolved to, which is the coordinate every
	// artifact fact is keyed by — never the authored key, which for an imported or
	// bundle-supplied entry names no output at all.
	outputName string

	// sourceFile positions a finding at the document the entry was written in. It
	// labels rather than answers: no rule reads it.
	sourceFile string
}

// Nodes yields the one node the entry belongs to.
func (o oneRecordedArtifact) Nodes() []constraint.NodeInstance {
	return []constraint.NodeInstance{{ID: o.nodeID}}
}

// Artifacts yields the one recorded entry, and nothing for any other node — so a
// traversal handed a wider node axis still reaches exactly this subject.
func (o oneRecordedArtifact) Artifacts(node string) []constraint.AuthoredCoordinate {
	if node != o.nodeID {
		return nil
	}
	return []constraint.AuthoredCoordinate{{Value: o.outputName, SourceFile: o.sourceFile}}
}

var (
	_ constraint.NodeEnumerator     = oneRecordedArtifact{}
	_ constraint.ArtifactEnumerator = oneRecordedArtifact{}
)

// runArtifactPillarRule asks the rule declared over this stage's facts about one
// artifact entry, and returns what it raised as the render's halt.
//
// It is called from inside the artifacts walk, between the point the entry's text
// is recorded and the point that text is evaluated. That is the only place both
// halves of the question are answerable: the text exists from the line above, and
// evaluation has not yet turned an undeclared pillar into either a placeholder
// written into the output or a halt naming the first hop of the expression rather
// than the expression itself.
//
// # It stands beside the pre-check rather than replacing it
//
// The two are complementary and each keeps what the other deliberately does not.
// This rule reports every offending reference in one template; the pre-check
// returns on the first. The pre-check keeps the parse-error arm, because a
// template that does not parse is not this rule's finding — evaluation reports
// the parse error against the same entry, so refusing it here as well would
// report one authoring fault twice.
//
// And the pre-check keeps its pillar arms, because they are not dead. The walk
// this sits in is reached by two renders: a node's own Recipe, and the render an
// attachment's or a boundary-scope FeatureRecipe carries. Only the first installs
// the retention and the presentation over it, so only the first contributes a
// subject here — a FeatureRecipe's entries flow through the same walk under the
// host component's node identity, which is why they are deliberately kept out of
// a fact declared over a node's own Recipe. The pre-check is therefore the pillar
// enforcement for every FeatureRecipe-carried artifact, and removing its arms
// would drop that enforcement with nothing failing to say so.
//
// # The two nil conditions are different questions
//
// An absent presentation says this render is not the one the rule's facts are
// declared over, which is the FeatureRecipe render and is an ordinary outcome: the
// caller skips the rule and the pre-check beside it does the work.
//
// An absent retention beneath a present presentation is not an outcome, it is a
// broken arrangement — the presentation would answer every entry's text with the
// empty string, the rule would collect no reference from it, and it would admit
// every template it was asked about. That is why it halts rather than returning
// nothing: the two are indistinguishable to any reader downstream, and silence is
// indistinguishable from approval. The condition is unreachable today because the
// retention and the presentation are installed together, unconditionally, at one
// site; the halt is what makes that an enforced guarantee rather than an
// arrangement a later change could quietly undo.
//
// Its wording says the fault is the compiler's rather than the author's, and that
// there is nothing to correct in the catalog. That is deliberate: the halt carries
// a catalog category because every category attributes a fault to a document or a
// producer and none of them says the compiler reached a check without its inputs —
// so the wording carries the attribution the category cannot, and an author meeting
// it is not sent looking through a Recipe that is not at fault.
func runArtifactPillarRule(
	cancelled constraint.Cancellation,
	state *PhaseBState,
	outputName string,
) error {
	if state == nil || state.hydratedFacts == nil {
		return nil
	}
	findingFile := artifactFindingFile(state)
	if state.hydrated == nil {
		return cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: findingFile},
			fmt.Sprintf("artifact %q could not be checked against the pillars its Recipe declares — "+
				"internal error: the check's inputs were incomplete. This is a fault in the compiler "+
				"rather than in the Recipe, so there is nothing to correct in the catalog", outputName), nil, nil)
	}

	subject := oneRecordedArtifact{
		nodeID:     state.NodeID,
		outputName: outputName,
		sourceFile: findingFile,
	}
	raised, err := constraint.RunOnNodeArtifacts(
		cancelled, subject, subject, state.hydratedFacts, ArtifactPillarDeclared,
	)
	if err != nil {
		return err
	}
	return objectionsAsHalt(raised, fmt.Sprintf(
		"artifact %q references pillars its Recipe does not declare", outputName))
}

// artifactFindingFile is the document an artifact finding is positioned at.
//
// It prefers the Recipe's own path and falls back to the Recipe's name. The path
// is what an author opens, so it wins wherever the render has one; today the
// catalog loader does not retain it on the parsed Recipe, and a finding positioned
// at nothing would leave an author with a reference and no document to look in.
// A name is a logical identifier a reader can still act on, which is what the
// position field admits where no file applies.
func artifactFindingFile(state *PhaseBState) string {
	if state == nil {
		return ""
	}
	if state.RecipePath != "" {
		return state.RecipePath
	}
	return state.RecipeName
}

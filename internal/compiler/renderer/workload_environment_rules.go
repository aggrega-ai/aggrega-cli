// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The rule that one container of a Workload names each environment variable once
// is asked here, at the one seam every render that parses a Workload passes
// through: immediately after the render's mid-render state is recorded, and before
// the artifacts pass and workload synthesis. Both of those read the parsed
// Workload — an artifact template may read it, and synthesis hands it to the
// provider — so this is the last point at which a refusal still keeps a pair from
// reaching any provider, and the first at which the Workload the rule reads is the
// one those passes read: parsed, validated and folded.
//
// # Why the subject is the owner's workload field
//
// Each value is invoked over one subject, the workload field of the document that
// declares the Workload, and the subject's position is what a finding is reported
// at: the Workload key of that document, in that document's own file. That is the
// position every other refusal about a Workload carries, so an author meets all of
// them at one key. The field is presented whichever form the Workload took there —
// written in place, or imported from a file or a library — because the rule reads
// the RENDERED Workload, which exists for both forms, and the key it is positioned
// at is the key the author wrote in both.
//
// A render that parsed no Workload presents no subject. There is no container to
// ask about, and presenting the field anyway would position a finding at a key the
// document may not carry.
//
// # Why the subject set is narrowed to one rather than swept
//
// The traversal walks every subject an axis yields, and the facts a value reads
// are complete for one render at a time, so a value is asked about the render
// that has just recorded them and about no other — the reason [oneRecordedArtifact]
// gives for the artifact axis, which this follows.

// oneRenderedWorkload is the node axis and the template-field axis for the rule
// asked about the Workload one node's own render has just parsed: the node, and its
// Recipe's workload field.
type oneRenderedWorkload struct {
	// nodeID is the node whose Recipe declares the Workload. It is the only node
	// coordinate supplied, for the reason [oneRecordedArtifact] states: the
	// locator is taken from the field coordinate, not from the node.
	nodeID string

	// sourceFile positions a finding at the document the Workload is declared in.
	// It labels rather than answers: no rule reads it.
	sourceFile string
}

// Nodes yields the one node the Workload belongs to.
func (o oneRenderedWorkload) Nodes() []constraint.NodeInstance {
	return []constraint.NodeInstance{{ID: o.nodeID}}
}

// NodeTemplateFields yields the Recipe's workload field, and nothing for any other
// node.
func (o oneRenderedWorkload) NodeTemplateFields(node string) []constraint.TemplateFieldCoordinate {
	if node != o.nodeID {
		return nil
	}
	return []constraint.TemplateFieldCoordinate{workloadFieldCoordinate(o.sourceFile)}
}

var (
	_ constraint.NodeEnumerator              = oneRenderedWorkload{}
	_ constraint.NodeTemplateFieldEnumerator = oneRenderedWorkload{}
)

// oneRenderedAttachmentWorkload is the node, attachment and template-field axes
// for the rule asked about the Workload the FeatureRecipe one attachment binds on a
// node has just parsed.
//
// The attachment coordinate is taken from the owner the render's state holds,
// because the render cannot see it any other way: the node identity it carries is
// the host component's.
type oneRenderedAttachmentWorkload struct {
	// key is the owner the Workload belongs to: this node, this attachment.
	key nodeAttachmentKey

	// sourceFile positions a finding at the FeatureRecipe the Workload is declared
	// in. It labels rather than answers.
	sourceFile string
}

// Nodes yields the one node carrying the attachment.
func (o oneRenderedAttachmentWorkload) Nodes() []constraint.NodeInstance {
	return []constraint.NodeInstance{{ID: o.key.node}}
}

// Attachments yields the one attachment, and nothing for any other node.
func (o oneRenderedAttachmentWorkload) Attachments(node string) []string {
	if node != o.key.node {
		return nil
	}
	return []string{o.key.attachment}
}

// AttachmentTemplateFields yields the FeatureRecipe's workload field, and nothing
// for any other owner.
func (o oneRenderedAttachmentWorkload) AttachmentTemplateFields(
	node string, attachment string,
) []constraint.TemplateFieldCoordinate {
	if node != o.key.node || attachment != o.key.attachment {
		return nil
	}
	return []constraint.TemplateFieldCoordinate{workloadFieldCoordinate(o.sourceFile)}
}

var (
	_ constraint.NodeEnumerator                    = oneRenderedAttachmentWorkload{}
	_ constraint.AttachmentEnumerator              = oneRenderedAttachmentWorkload{}
	_ constraint.AttachmentTemplateFieldEnumerator = oneRenderedAttachmentWorkload{}
)

// oneRenderedBoundaryAttachmentWorkload is the boundary, attachment and
// template-field axes for the rule asked about the Workload the FeatureRecipe one
// boundary-scope attachment binds has just parsed. It is rooted at the boundary
// because that is where such an attachment is declared, which is also why its
// facts are keyed there.
type oneRenderedBoundaryAttachmentWorkload struct {
	// key is the owner the Workload belongs to: this boundary, this attachment.
	key boundaryAttachmentKey

	// sourceFile positions a finding at the FeatureRecipe the Workload is declared
	// in. It labels rather than answers.
	sourceFile string
}

// Boundaries yields the one boundary the attachment was declared on. It carries
// no file, because the boundary contributes no locator segment on this axis: the
// locator names the Workload key inside the FeatureRecipe.
func (o oneRenderedBoundaryAttachmentWorkload) Boundaries() []constraint.AuthoredCoordinate {
	return []constraint.AuthoredCoordinate{{Value: o.key.boundary}}
}

// BoundaryAttachments yields the one attachment, and nothing for any other
// boundary.
func (o oneRenderedBoundaryAttachmentWorkload) BoundaryAttachments(boundary string) []string {
	if boundary != o.key.boundary {
		return nil
	}
	return []string{o.key.attachment}
}

// BoundaryAttachmentTemplateFields yields the FeatureRecipe's workload field, and
// nothing for any other owner.
func (o oneRenderedBoundaryAttachmentWorkload) BoundaryAttachmentTemplateFields(
	boundary string, attachment string,
) []constraint.TemplateFieldCoordinate {
	if boundary != o.key.boundary || attachment != o.key.attachment {
		return nil
	}
	return []constraint.TemplateFieldCoordinate{workloadFieldCoordinate(o.sourceFile)}
}

var (
	_ constraint.BoundaryEnumerator                        = oneRenderedBoundaryAttachmentWorkload{}
	_ constraint.BoundaryAttachmentEnumerator              = oneRenderedBoundaryAttachmentWorkload{}
	_ constraint.BoundaryAttachmentTemplateFieldEnumerator = oneRenderedBoundaryAttachmentWorkload{}
)

// workloadFieldCoordinate is the workload field of a Recipe or a FeatureRecipe as
// the template-field axis names it: the label and the authored key are the ones the
// field enumeration of both documents spells it with, so a finding positioned here
// names the same key a finding about the field's text would.
func workloadFieldCoordinate(sourceFile string) constraint.TemplateFieldCoordinate {
	return constraint.TemplateFieldCoordinate{
		Value:        recipeFieldWorkload,
		SourceFile:   sourceFile,
		AuthoredKeys: []string{recipeFieldWorkload},
	}
}

// runWorkloadEnvironmentRule asks the value declared over a node's own Workload
// whether one of its containers names an environment variable twice, and returns
// what it raised as the render's halt.
//
// # The absent inputs are different questions
//
// An absent presentation says this render published none, which only a caller
// assembling a render's input directly reaches: every build supplies one, and such
// a caller is asking for a render rather than for a build, so the rule is not asked.
// That is a real bound on this refusal and it is stated rather than implied: a
// caller supplying no presentation renders a container naming a variable twice
// instead of halting.
//
// An absent retention beneath a present presentation is not an outcome but a broken
// arrangement: the presentation would answer every container's names as empty, and
// the rule would admit every Workload it was asked about. It halts rather than
// returning nothing, because silence is indistinguishable from approval, and its
// wording says the fault is the compiler's rather than the Recipe's.
func runWorkloadEnvironmentRule(cancelled constraint.Cancellation, state *PhaseBState) error {
	if state == nil || state.hydratedFacts == nil || state.ParsedWorkload == nil {
		return nil
	}
	findingFile := artifactFindingFile(state)
	if state.hydrated == nil {
		return cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: findingFile},
			"the Workload this Recipe renders could not be checked for an environment variable named twice "+
				"in one container — internal error: the check's inputs were incomplete. This is a fault in "+
				"the compiler rather than in the Recipe, so there is nothing to correct in the catalog",
			nil, nil)
	}

	subject := oneRenderedWorkload{nodeID: state.NodeID, sourceFile: findingFile}
	raised, err := constraint.RunOnNodeTemplateFields(
		cancelled, subject, subject, state.hydratedFacts, WorkloadEnvironmentVariableNamedOnce,
	)
	if err != nil {
		return err
	}
	return objectionsAsHalt(raised, "containers of the Workload this Recipe renders name one environment "+
		"variable twice")
}

// runAttachmentWorkloadEnvironmentRule asks the value declared over whichever owner
// this FeatureRecipe render was reached from whether one container of its Workload
// names an environment variable twice, and returns what it raised as the render's
// halt.
//
// Both traversals are named here for the reason the artifact-pillar rule's owner
// entry gives: the framework's coverage read resolves a call to a declaration and
// follows nothing through a value, so a traversal reached through an interface
// method would belong to no calling surface.
//
// The two absent inputs are the different questions [runWorkloadEnvironmentRule]
// keeps apart, refused by the owner's own guard. An owner reporting neither key is
// refused rather than passing as a Workload with nothing to check: today the two
// owners exhaust the interface, and the halt is what keeps that an enforced
// guarantee.
func runAttachmentWorkloadEnvironmentRule(cancelled constraint.Cancellation, state *PhaseBState) error {
	if state == nil || state.attachmentHydrated == nil || state.ParsedWorkload == nil {
		return nil
	}
	owner := state.attachmentHydrated
	findingFile := artifactFindingFile(state)
	facts, err := owner.readableHydratedFacts(findingFile,
		"the Workload this FeatureRecipe renders could not be checked for an environment variable named "+
			"twice in one container")
	if err != nil || facts == nil {
		return err
	}

	if key, rooted := owner.nodeAttachmentOwnerKey(); rooted {
		subject := oneRenderedAttachmentWorkload{key: key, sourceFile: findingFile}
		raised, err := constraint.RunOnNodeAttachmentTemplateFields(
			cancelled, subject, subject, subject, facts, AttachmentWorkloadEnvironmentVariableNamedOnce,
		)
		if err != nil {
			return err
		}
		return attachmentWorkloadEnvironmentHalt(raised, key.attachment)
	}

	if key, rooted := owner.boundaryAttachmentOwnerKey(); rooted {
		subject := oneRenderedBoundaryAttachmentWorkload{key: key, sourceFile: findingFile}
		raised, err := constraint.RunOnBoundaryAttachmentTemplateFields(
			cancelled, subject, subject, subject, facts, BoundaryAttachmentWorkloadEnvironmentVariableNamedOnce,
		)
		if err != nil {
			return err
		}
		return attachmentWorkloadEnvironmentHalt(raised, key.attachment)
	}

	return cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: findingFile},
		"the Workload this FeatureRecipe renders could not be checked for an environment variable named "+
			"twice in one container — internal error: the render carries no identity to check it under. "+
			"This is a fault in the compiler rather than in the FeatureRecipe, so there is nothing to "+
			"correct in the catalog", nil, nil)
}

// attachmentWorkloadEnvironmentHalt is the halt both attachment owners' findings
// are returned as. It names the attachment, because that is what says which
// FeatureRecipe, and it is worded apart from the node's own halt so a reader can
// tell which document several findings are about.
func attachmentWorkloadEnvironmentHalt(raised []constraint.Objection, attachment string) error {
	return objectionsAsHalt(raised, fmt.Sprintf(
		"containers of the Workload rendered by the FeatureRecipe that attachment %q binds name one "+
			"environment variable twice", attachment))
}

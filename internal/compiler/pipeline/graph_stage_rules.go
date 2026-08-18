// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// runGraphStageRules invokes the rules declared over the finalised graph and
// folds what they raised into the analysis the build already carries.
//
// A rule needs a presentation for the facts it reads and an enumeration for the
// subjects it is invoked once per. Both are built from the products this scope
// holds, so a rule is asked about the coordinates the graph really carries and
// answers from the same graph — as against a set assembled beside it, which
// could name coordinates the facts cannot answer for and would leave the rule
// silently uninvoked over the ones it can.
//
// It reports the reason a walk stopped and nothing else. A rule raising is not
// an error here: an advisory joins the warning stream the caller surfaces, and
// a violation joins the slice the build gates on, exactly as the readings
// beside it do.
//
// # Two streams, chosen by the severity the rule declared
//
// A violation is appended to the analysis result's violation slice and an
// advisory to its warning slice, and which one a rule reaches is decided by the
// severity on its own declaration rather than by anything here. The analysis
// result is the one the caller already holds, so a violation appended here
// reaches the gate that halts the build and the joined error a build caller
// reads, while an advisory reaches neither of those and is surfaced alongside a
// build that finishes — the same two destinations the readings beside it use.
//
// The rules are invoked in the order the readings beside them run, so the order
// a caller sees findings in does not depend on which reading produced them.
func runGraphStageRules(
	ctx context.Context,
	out *stepsOneToFourOutput,
) error {
	// An absent presentation or an absent graph is an internal inconsistency
	// rather than a document the author can fix: this scope built both a moment
	// ago. It is reported rather than skipped, because a rule that never ran
	// reports exactly what a rule that ran and approved reports, and nothing
	// downstream could tell the two apart.
	//
	// The enumeration is taken from the phase output rather than derived here.
	// It is built where the graph and its presentation are both first in hand,
	// and the rules over what the render's plugins report are invoked over the
	// same node and attachment axes — so one derivation serves both and neither
	// can be asked about coordinates the other was not.
	coordinates := out.GraphCoordinates
	if coordinates == nil {
		return fmt.Errorf(
			"graph-stage rules: the compilation produced no presentation of the finalised graph to invoke them against")
	}

	// The label constraint a dependency socket declares over what may satisfy
	// it, evaluated once per connection the graph holds. The cancellation
	// signal is the caller's own, so a long walk over a large graph stops
	// within one connection of the caller asking it to.
	violations, err := constraint.RunOnEdges(
		ctx.Err,
		coordinates,
		out.GraphView,
		analyzer.EdgeTargetSatisfiesSocketSelector,
	)
	if err != nil {
		return err
	}
	out.Analysis.Errors = append(out.Analysis.Errors, analyzer.ErrorsFromObjections(violations)...)

	// The applicability a Feature declares over what it may attach to,
	// evaluated once per attachment a node carries after the whole attachment
	// cascade has folded. The merged set is the one the rule has to be asked
	// about: an attachment contributed at a scope above the node binds it just
	// as an attachment written on the node does, and only the folded set holds
	// both. The signal is read once per node, which is the granularity the
	// traversal promises and the same one the reading beside it had.
	violations, err = constraint.RunOnNodeAttachments(
		ctx.Err,
		coordinates,
		coordinates,
		out.GraphView,
		analyzer.AttachmentSatisfiesFeatureApplicability,
	)
	if err != nil {
		return err
	}
	out.Analysis.Errors = append(out.Analysis.Errors, analyzer.ErrorsFromObjections(violations)...)

	// The label constraint an attachment slot declares over what may fill it,
	// evaluated once per slot each node's component type declares. The axis is
	// the declared set rather than the bound one, so the rule is asked about a
	// slot nothing binds as well as one something does — it ends on the unbound
	// case without raising, and asking it is what keeps the question about the
	// declaration rather than about what the node happened to end up with. The
	// signal is read once per node, which is the granularity the traversal
	// promises and the same one the reading beside it had.
	violations, err = constraint.RunOnNodeSlots(
		ctx.Err,
		coordinates,
		coordinates,
		out.GraphView,
		analyzer.SlotFillSatisfiesSlotSelector,
	)
	if err != nil {
		return err
	}
	out.Analysis.Errors = append(out.Analysis.Errors, analyzer.ErrorsFromObjections(violations)...)

	// Each presence mandate a node's component type declares is satisfied by some
	// attachment of the type it names. The satisfying set is the node's merged
	// attachment names and each name's resolved type, so an attachment
	// contributed at a scope the node inherits from satisfies a mandate exactly as
	// one written on the node does — the authored block alone is empty for
	// precisely the deployments that rely on sharing. A mandate whose declaration
	// cannot be evaluated is not tested here: it is reported at the stage its own
	// rule is asked at, so a broken declaration never reads as a satisfied
	// requirement and never as an absent attachment either. The signal is read
	// once per node, which is the granularity the traversal promises and the same
	// one the reading beside it had.
	violations, err = constraint.RunOnNodes(
		ctx.Err,
		coordinates,
		out.GraphView,
		analyzer.RequiredAttachmentTypeAttached,
	)
	if err != nil {
		return err
	}
	out.Analysis.Errors = append(out.Analysis.Errors, analyzer.ErrorsFromObjections(violations)...)

	// Each attachment slot a node's component type declares required is bound by
	// some attachment. It walks the same declared slot axis as the constraint
	// rule above rather than the set the node ended up bound to, which is what
	// lets the question be asked at all: a slot nothing binds is exactly the case
	// this rule reports, so an axis built from the bindings would never present
	// the subject the rule exists for. Binding is name presence in the merged set,
	// so an attachment contributed at a scope the node inherits from binds the
	// slot exactly as one written on the node does. The signal is read once per
	// node, which is the granularity the traversal promises and the same one the
	// reading beside it had.
	violations, err = constraint.RunOnNodeSlots(
		ctx.Err,
		coordinates,
		coordinates,
		out.GraphView,
		analyzer.RequiredAttachmentSlotBound,
	)
	if err != nil {
		return err
	}
	out.Analysis.Errors = append(out.Analysis.Errors, analyzer.ErrorsFromObjections(violations)...)

	advisories, err := constraint.RunOnNodeAttachmentPillarLeaves(
		ctx.Err,
		coordinates,
		coordinates,
		coordinates,
		coordinates,
		out.GraphView,
		analyzer.OverriddenAnnotationAdvisory,
	)
	if err != nil {
		return err
	}
	out.Analysis.Warnings = append(out.Analysis.Warnings, analyzer.AdvisoriesFromObjections(advisories)...)
	return nil
}

// releaseFileOf reads the deployment's file off the resolve result, empty when
// no resolve result is in hand.
//
// It is read from there rather than from the pipeline's own inputs because the
// resolve result already carries it for exactly this purpose — building source
// locations downstream — so a second read of the same value could not disagree
// with the annotation source locations this stage's own advisories quote.
func releaseFileOf(result *resolver.ResolveResult) string {
	if result == nil {
		return ""
	}
	return result.ReleasePath
}

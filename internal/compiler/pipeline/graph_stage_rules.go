// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
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
	catalogLookup resolver.CatalogLookup,
) error {
	// An absent presentation or an absent graph is an internal inconsistency
	// rather than a document the author can fix: this scope built both a moment
	// ago. It is reported rather than skipped, because a rule that never ran
	// reports exactly what a rule that ran and approved reports, and nothing
	// downstream could tell the two apart.
	// The deployment's file comes from the resolve result, which already carries
	// it so downstream phases can build source locations without re-discovering
	// it. One axis needs it: an attachment's infrastructure configuration is
	// authored on the deployment and nowhere else, so a finding about one of its
	// leaves is positioned in that document rather than in the node's.
	// The per-leaf sites come from the fold that produced them rather than being
	// recovered here. Which scope's contribution won any one leaf of an
	// attachment is settled while the cascade folds, and re-running that
	// precedence at this point would be a second answer to a question the build
	// already answered — free to disagree with the bytes it produced.
	coordinates := ir.NewGraphStageCoordinates(
		out.IRGraph, out.GraphView, catalogLookup, releaseFileOf(out.ResolveResult),
		out.AttachmentSites,
	)
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

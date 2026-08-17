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
// a violation would join the slice the build gates on, exactly as the readings
// beside it do.
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
	coordinates := ir.NewGraphStageCoordinates(
		out.IRGraph, out.GraphView, catalogLookup, releaseFileOf(out.ResolveResult),
	)
	if coordinates == nil {
		return fmt.Errorf(
			"graph-stage rules: the compilation produced no presentation of the finalised graph to invoke them against")
	}

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

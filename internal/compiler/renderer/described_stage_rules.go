// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	stderrors "errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// selfReportRefusalReporter is a presentation that records the plugin
// self-reports it was asked for and could not obtain.
//
// It is an interface the presentation satisfies rather than a value the caller is
// handed, so the record cannot be mispaired: the presentation that swallowed the
// read is the one asked about it, and no caller can supply a record belonging to
// a different presentation or forget to supply one at all. The production
// presentation is held to it at compile time beside its own declaration, so an
// accessor that stops recording fails to build rather than leaving this read
// answering nothing.
type selfReportRefusalReporter interface {
	selfReportRefusal() error
}

// describedStageAxes is every axis the rules declared over what a build's plugins
// report about themselves are invoked once per.
//
// It is named as one set because the invocation needs all of them together: a
// value satisfying six of the seven cannot be asked one of the four rules at all,
// and the failure would be a rule invoked zero times — which reports exactly what
// a rule that ran and approved reports.
type describedStageAxes interface {
	constraint.NodeEnumerator
	constraint.AttachmentEnumerator
	constraint.PipelineStepEnumerator
	constraint.AttachmentPipelineStepEnumerator
	constraint.BoundaryEnumerator
	constraint.BoundaryAttachmentEnumerator
	constraint.BoundaryAttachmentPipelineStepEnumerator
}

// runDescribedStageRules invokes the rules declared over what every plugin a
// render reaches reports about itself, and returns what they raised as the
// render's own halt.
//
// It runs before the node walk, which is where the question belongs: every rule
// here is about whether a pipeline step may run at all, and the answer is
// complete once each plugin has described itself. Asking afterwards would let a
// step whose plugin declares no compatibility with the provider rendering the
// documents it addresses execute first and be reported second.
//
// A rule needs a presentation for the facts it reads and an enumeration for the
// subjects it is invoked once per. Both come from the render's own products, so
// a rule is asked about the coordinates the build really carries and answers
// from the same build.
//
// Every rule here is a violation, so there is one stream and no severity fork:
// a raised objection halts. Where the readings beside this one report a finding
// and carry on, these report a finding and stop, because a step that may not run
// leaves nothing to render.
//
// # A plugin the host could not ask outranks every verdict reached without it
//
// Every rule here reads what a plugin declared about itself, and the presentation
// answers a plugin it could not resolve, load or describe with the zero value of
// the fact asked for — an empty compatibility list, an empty scope. A rule
// reading that zero value refuses the step, because an empty compatibility list
// matches no provider; and the refusal would be reported as a plugin that
// declares no compatible provider, which is a claim the author never made and
// names the wrong fault.
//
// So the presentation records the reads it could not answer, and that record is
// returned here in preference to anything the rules raised. The two conditions
// stay distinguishable, which is the whole point: a plugin that genuinely
// declares an empty compatible set is still refused as incompatible, and a plugin
// the host could not ask halts with the host's own failure — the resolution, load
// or `describe()` error, which is the thing to repair.
//
// The precedence is the earliest failing class, which is what the pipeline
// reports everywhere else. It also matches what the reading this replaced did: it
// read each step's self-report before judging it and propagated a failed read as
// the halt.
//
// The rules are invoked in a fixed order — a node's own pipeline, then the
// pipelines its attachments carry, then the pipelines declared at a boundary —
// so the order a caller sees findings in does not depend on which rule produced
// them.
//
// # A render with no plugin resolution
//
// A caller that wires no plugin resolution validates each pipeline by its
// presence and runs none of it, and no plugin in such a build is resolved,
// loaded or described. The presentation answers every plugin fact from nothing
// there, and it cannot distinguish that from a plugin that genuinely declares
// nothing — so invoking these rules over such a build would refuse every step of
// a configuration that today renders. The rules are therefore not invoked when
// the render was given no resolution, which is the same short-circuit the
// executor takes for the same reason.
func runDescribedStageRules(
	ctx context.Context,
	view constraint.DescribedView,
	coordinates describedStageAxes,
	pluginResolutionWired bool,
) error {
	if view == nil || coordinates == nil || !pluginResolutionWired {
		return nil
	}

	// Each step of a node's own Recipe pipeline, against the provider rendering
	// the node's own documents. The cancellation signal is the caller's own, so a
	// long walk over a large graph stops within one node of the caller asking it
	// to.
	var raised []constraint.Objection
	objections, err := constraint.RunOnNodePipelineSteps(
		ctx.Err, coordinates, coordinates, view,
		PipelineStepProviderCompatible,
	)
	if err != nil {
		return err
	}
	raised = append(raised, objections...)

	// Each step of the pipeline an attachment's FeatureRecipe carries on a node.
	// Two rules walk this axis and both are asked, because they are separate
	// findings with separate remedies: one asks whether the step's plugin
	// declares the provider its scope names, the other whether the documents that
	// scope names will exist as discrete documents at all.
	objections, err = constraint.RunOnNodeAttachmentPipelineSteps(
		ctx.Err, coordinates, coordinates, coordinates, view,
		AttachmentPipelineStepProviderCompatible,
	)
	if err != nil {
		return err
	}
	raised = append(raised, objections...)

	objections, err = constraint.RunOnNodeAttachmentPipelineSteps(
		ctx.Err, coordinates, coordinates, coordinates, view,
		AttachmentPipelineStepOwnScopeHasAMergedTarget,
	)
	if err != nil {
		return err
	}
	raised = append(raised, objections...)

	// Each step of the pipeline an attachment declared at a boundary carries.
	// The boundary set is walked rather than the node set: such an attachment is
	// declared once and reaches every component the boundary holds, so a
	// node-rooted walk would ask about one authored pipeline once per component.
	objections, err = constraint.RunOnBoundaryAttachmentPipelineSteps(
		ctx.Err, coordinates, coordinates, coordinates, view,
		BoundaryAttachmentPipelineStepProviderCompatible,
	)
	if err != nil {
		return err
	}
	raised = append(raised, objections...)

	// A read the presentation could not answer outranks whatever a rule raised
	// from the zero value it was handed instead. The reporter is optional because
	// a presentation assembled for a proof of the walk itself keeps no such
	// record and refuses nothing; the production one is held to it where it is
	// declared.
	if reporter, keepsRecord := view.(selfReportRefusalReporter); keepsRecord {
		if refused := reporter.selfReportRefusal(); refused != nil {
			return refused
		}
	}

	return describedStageHalt(raised)
}

// describedStageHalt renders what the described stage's rules raised as the
// render's halt, nil when they raised nothing.
//
// Each objection becomes a diagnostic of its own carrying its own position: the
// entity that declares the step, and the authored key inside it. That is the
// whole reason the findings are not folded into one message — a step is
// something the author can navigate to, and a single message naming several
// steps can be navigated to none of them.
//
// One objection is returned as itself rather than as a set of one, so the
// ordinary case reads as the single diagnostic it is. Several are joined under
// an envelope naming what they are about, which is the shape a display surface
// already renders every member of.
func describedStageHalt(objections []constraint.Objection) error {
	if len(objections) == 0 {
		return nil
	}
	halts := make([]error, 0, len(objections))
	for _, o := range objections {
		halts = append(halts, cerrors.Wrap(
			o.Sentinel(),
			cerrors.CatalogError,
			cerrors.SourceLocation{File: o.SourceFile(), Locator: o.Locator()},
			constraint.ObjectionMessage(o),
		))
	}
	if len(halts) == 1 {
		return halts[0]
	}
	return cerrors.Wrap(
		stderrors.Join(halts...),
		cerrors.CatalogError,
		cerrors.SourceLocation{},
		"pipeline steps that cannot run as declared",
	)
}

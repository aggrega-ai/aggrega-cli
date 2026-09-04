// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
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
// value satisfying six of the seven cannot be asked the rules over the seventh at
// all, and the failure would be a rule invoked zero times — which reports exactly
// what a rule that ran and approved reports.
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

	// The second rule over the same axis: whether the documents a step's scope
	// names exist at this site at all. It is asked separately from the one above
	// because the rule above declines a host-addressed step — such a step has no
	// provider to be matched against — and a step declining both readings would
	// be reported by neither.
	objections, err = constraint.RunOnNodePipelineSteps(
		ctx.Err, coordinates, coordinates, view,
		PipelineStepScopeHasADocumentSet,
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

	// And the same second reading over that axis, for the same reason it is asked
	// over a node's own pipeline: a boundary-scope attachment stands against no
	// single component, so a step of its pipeline declaring it addresses host
	// documents is declined by the rule above and reported by this one.
	objections, err = constraint.RunOnBoundaryAttachmentPipelineSteps(
		ctx.Err, coordinates, coordinates, coordinates, view,
		BoundaryAttachmentPipelineStepScopeHasADocumentSet,
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
//
// The rendering itself is the shared one, so this reading and the one over a
// node's artifact entries cannot report the same class of fault in two shapes.
// What is this stage's own is the envelope: these findings are about steps that
// may not run, which is what the wording says.
func describedStageHalt(objections []constraint.Objection) error {
	return objectionsAsHalt(objections, "pipeline steps that cannot run as declared")
}

// boundaryScopeAttachmentAxes is the axis pair the rule over an attachment's
// execution scope is invoked once per: the node set, and the attachments each
// node carries.
//
// It is its own set beside the plugin-report one above rather than two more
// members of it, and for the reason the authored-text sets below are their own:
// both facts this rule reads are authored — the scope a FeatureRecipe declares
// and the attachments a deployment writes at a boundary — and neither depends on
// a plugin describing itself. Folding the axes together would put the rule
// behind that set's resolution guard, and a rule skipped by a guard reports
// exactly what a rule that ran and approved reports.
//
// Naming both axes together is what makes the inner one un-skippable: a value
// supplying nodes but no attachment enumeration does not compile here.
type boundaryScopeAttachmentAxes interface {
	constraint.NodeEnumerator
	constraint.AttachmentEnumerator
}

// runBoundaryScopeAttachmentRule invokes the rule declared over whether an
// attachment binding a FeatureRecipe that generates once for a boundary has a
// boundary to generate for, and returns what it raised as the render's halt.
//
// # Why it is asked first among the authored readings
//
// It is the coarsest of them. The readings beneath it ask what a template may
// say; this asks whether the document carrying those templates has a site to be
// evaluated at at all. An attachment with no boundary to generate for produces
// no output anywhere, so a finding about a reference inside its templates would
// describe text that is never evaluated where the author believes it is. The
// order the build reports the two in follows the order they are decided.
//
// It carries no plugin-resolution guard, for the reason the authored-text
// readings beneath it carry none: nothing it reads comes from a plugin
// describing itself, so a render that resolved no plugin still owes this
// refusal — which is also what keeps the refusal reaching a render wired with no
// plugin resolution at all, where the rules above it are skipped entirely.
//
// # Every offending attachment is reported, not the first
//
// The rule is invoked once per attachment, so a deployment carrying three such
// attachments raises three objections and an author corrects all three from one
// build. Each objection carries its own position — the document the attachment
// was written in, and the authored key inside it — which is why they are not
// folded into one message: a position is something an author navigates to, and a
// single message naming three of them can be navigated to none.
//
// The cancellation signal is the caller's own, so a long walk over a large graph
// stops within one node of the caller asking it to.
func runBoundaryScopeAttachmentRule(
	ctx context.Context,
	view constraint.DescribedView,
	coordinates boundaryScopeAttachmentAxes,
) error {
	if view == nil || coordinates == nil {
		return nil
	}
	raised, err := constraint.RunOnNodeAttachments(
		ctx.Err, coordinates, coordinates, view,
		AttachmentBoundaryScopeHasADeclaringBoundary,
	)
	if err != nil {
		return err
	}
	return objectionsAsHalt(raised, "attachments that generate once for a boundary with no boundary declaring them")
}

// authoredProvidesAxes is the axis pair the rule over an authored provides
// template is invoked once per: the node set, and the keys each node's Recipe
// publishes.
//
// It is a second set beside the one above rather than two more members of it,
// because the two readings answer different questions and are guarded
// differently. The set above is about what a build's plugins report, so a render
// that resolves no plugin has nothing to read and every rule over it is skipped;
// this one is about text an author wrote, which is there whether a plugin was
// ever loaded or not. Folding the axes together would put this rule behind the
// plugin guard, and a rule skipped by a guard reports exactly what a rule that
// ran and approved reports.
type authoredProvidesAxes interface {
	constraint.NodeEnumerator
	constraint.ProvidesKeyEnumerator
}

// runAuthoredProvidesRule invokes the rule declared over the text a Recipe wrote
// for each of its provides keys, and returns what it raised as the render's halt.
//
// It runs before anything is evaluated, which is what the rule reads for: the
// value a refused template would produce is never produced, so the authored text
// is the only input that can carry the question. Asking afterwards would mean
// asking about a value the refusal exists to prevent.
//
// # Why it is not part of the plugin-report reading beside it
//
// That reading is skipped for a render given no plugin resolution, because every
// fact it reads comes from a plugin describing itself and a render that loaded
// none would have every rule refuse a build that renders today. Nothing about a
// provides template depends on a plugin, so this reading carries no such guard:
// it is asked of every render holding the presentation and the enumeration a rule
// over this stage needs, whether or not that render resolved a plugin.
//
// It is asked after that reading rather than before it, which keeps the order the
// build reported these two classes of fault in before either was a declared rule:
// a step that cannot run at all is reported ahead of an entry that reaches outside
// its data.
//
// # Every offending entry is reported, not the first one
//
// The rule is invoked once per key, so a Recipe that reaches outside the
// self-contained data in three entries raises three objections and an author
// corrects all three from one build. Each objection carries its own position —
// the Recipe, and the authored key inside it — which is why they are not folded
// into one message: a position is something an author navigates to, and a single
// message naming three of them can be navigated to none.
//
// The cancellation signal is the caller's own, so a long walk over a large graph
// stops within one node of the caller asking it to.
func runAuthoredProvidesRule(
	ctx context.Context,
	view constraint.DescribedView,
	coordinates authoredProvidesAxes,
) error {
	if view == nil || coordinates == nil {
		return nil
	}
	raised, err := constraint.RunOnNodeProvidesKeys(
		ctx.Err, coordinates, coordinates, view, ProvidesTemplateSelfContained,
	)
	if err != nil {
		return err
	}
	return objectionsAsHalt(raised, "provides entries that reach outside the data they are evaluated against")
}

// runRenderedNameRule invokes the rule declared over a provides entry that reads
// the name its target renders author-supplied source under, and returns what it
// raised as the render's halt.
//
// It is a separate call from the self-containment reading above rather than a
// second rule inside it, because the two refuse different things and an author
// fixes them differently: that one is about a reference reaching outside the data
// the surface carries at all, this one about a reference the surface does carry
// and this node's provider has nothing to fill. Folding them into one invocation
// would report one identity for two faults and give a reader one remedy for both.
//
// It is asked immediately after that reading, and the order is the coarse-first
// order the block already keeps: a template reaching outside its data is refused
// before one that reads a value inside it. It runs before anything is evaluated,
// for the reason every rule in this block does — the value a refusal exists to
// prevent is exactly the one evaluation would produce.
//
// It carries no plugin-resolution guard, and that is deliberate rather than an
// oversight. A render that resolved no plugin reports no name for any node, so
// every provides entry reading the key is refused there — which is the honest
// answer rather than an over-refusal: such a render has no provider to supply
// the value and would resolve the reference away.
func runRenderedNameRule(
	ctx context.Context,
	view constraint.DescribedView,
	coordinates authoredProvidesAxes,
) error {
	if view == nil || coordinates == nil {
		return nil
	}
	raised, err := constraint.RunOnNodeProvidesKeys(
		ctx.Err, coordinates, coordinates, view, ProvidesTemplateRenderedNameAvailable,
	)
	if err != nil {
		return err
	}
	return objectionsAsHalt(raised, "provides entries reading a name the node's provider does not report")
}

// authoredLocalsAxes is the axis pair the rules over an authored locals entry are
// invoked once per: the node set, and the locals each node's Recipe declares.
//
// It is a third set beside the two above rather than more members of either,
// for the reason the provides pair is its own: these rules read text an author
// wrote, which is there whether a plugin was ever loaded or not, so folding them
// into the plugin-report set would put them behind that set's resolution guard —
// and a rule skipped by a guard reports exactly what a rule that ran and approved
// reports. It is separate from the provides pair because the axes are separate:
// one enumerates what a Recipe publishes, the other what it declares locally, and
// a value satisfying one does not satisfy the other.
// It names all five axes together rather than one per carrier, and that grouping
// is what makes a carrier structurally un-skippable: a value satisfying the node
// axes and not the attachment ones does not compile here, so a caller cannot
// supply coordinates for one carrier and leave another's rules invoked zero times
// — which reports exactly what a rule that ran and approved reports.
type authoredLocalsAxes interface {
	constraint.NodeEnumerator
	constraint.LocalsKeyEnumerator
	constraint.AttachmentEnumerator
	constraint.AttachmentLocalsKeyEnumerator
	constraint.BoundaryEnumerator
	constraint.BoundaryAttachmentEnumerator
	constraint.BoundaryAttachmentLocalsKeyEnumerator
}

// runAuthoredLocalsRule invokes the rules declared over the text a Recipe wrote
// for each of its locals, and returns what they raised as the render's halt.
//
// It runs before anything is evaluated, which is where the question belongs: a
// reference these rules refuse resolves through the missing-key policy to an empty
// string, so the value a refusal exists to prevent is exactly the one that would be
// produced. Asking afterwards would mean asking about output already written from
// a segment the author never supplied.
//
// # Two identities, one body, and both are asked
//
// The two faults a locals reference can carry have separate fixes and therefore
// separate identities — a sibling declared below its reader is an ordering fault, a
// key the block never declares is a naming one — so they are two declared rules.
// Both are asked over the same axis, and an entry raises at most one of them,
// because the shared classification names the first offending reference together
// with the fault it has.
//
// # Every offending entry is reported, not the first
//
// The rules are invoked once per local, so a block that reaches outside what it may
// read in three entries raises three objections and an author corrects all three
// from one build. That is wider than the evaluation-time refusal beneath this,
// which aborts the block at its first offender — and each objection carries its own
// position, the Recipe and the authored key inside it, which is why they are not
// folded into one message.
//
// # Three carriers, because the rule has three and not one
//
// A locals block is authored on three documents — a node's own Recipe, the
// FeatureRecipe an attachment binds on a node, and the FeatureRecipe a
// boundary-scope attachment binds — and each is reached by its own coordinate. The
// contract is identical at all three and the decision is one shared classification,
// so what differs is only the axis a rule is invoked over and the document a
// finding is positioned at. Both are exactly what a declared rule needs, which is
// why there are three pairs here rather than one pair over a widened axis: no
// node-rooted key can name a local authored against a boundary, and a node-rooted
// key alone cannot tell one attachment's block from another's.
//
// # What this does NOT reach, stated because an unstated bound reads as coverage
//
// The rules read a locals ENTRY against its own block's author order. A template on
// another surface that reads .locals — a provides entry, a Recipe's source
// coordinate — is decided from the block's fully resolved key set, which is a later
// product than this stage holds, so those refusals stay with the evaluation-time
// path and are not declared here. The block a Recipe resolves for a source
// coordinate at lock time is refused by its own walk, outside this pipeline
// entirely.
//
// The evaluation-time refusals beneath these rules are retained rather than
// replaced. They share this rule's decision rather than re-implementing it, and
// they abort a block at its first offender where these report every offending entry
// — so what the declaration adds is a position and a breadth, never a refusal the
// build did not already make.
//
// The cancellation signal is the caller's own, so a long walk over a large graph
// stops within one node of the caller asking it to.
func runAuthoredLocalsRule(
	ctx context.Context,
	view constraint.DescribedView,
	coordinates authoredLocalsAxes,
) error {
	if view == nil || coordinates == nil {
		return nil
	}
	// Each rule is named at its own call site rather than iterated over as a list.
	// That is not style: the derivation that reports which declared rules a build
	// invokes reads the check named at a traversal call, so a loop variable makes
	// both of these invisible to it — and a rule the derived surface cannot see
	// reads there exactly as a rule nothing invokes.
	var raised []constraint.Objection
	objections, err := constraint.RunOnNodeLocalsKeys(
		ctx.Err, coordinates, coordinates, view, LocalsEntryReadsNoLaterSibling,
	)
	if err != nil {
		return err
	}
	raised = append(raised, objections...)

	objections, err = constraint.RunOnNodeLocalsKeys(
		ctx.Err, coordinates, coordinates, view, LocalsEntryReadsOnlyDeclaredSiblings,
	)
	if err != nil {
		return err
	}
	raised = append(raised, objections...)

	// The block the FeatureRecipe an attachment binds on a node declares. It is a
	// separate axis rather than the same one reached differently: the block is
	// authored on the FeatureRecipe, and two attachments on one node write theirs
	// with no knowledge of each other.
	objections, err = constraint.RunOnNodeAttachmentLocalsKeys(
		ctx.Err, coordinates, coordinates, coordinates, view,
		AttachmentLocalsEntryReadsNoLaterSibling,
	)
	if err != nil {
		return err
	}
	raised = append(raised, objections...)

	objections, err = constraint.RunOnNodeAttachmentLocalsKeys(
		ctx.Err, coordinates, coordinates, coordinates, view,
		AttachmentLocalsEntryReadsOnlyDeclaredSiblings,
	)
	if err != nil {
		return err
	}
	raised = append(raised, objections...)

	// The block a boundary-scope attachment's FeatureRecipe declares. The boundary
	// set is walked rather than the node set, because such an attachment is written
	// once and reaches every component the boundary holds — so a node-rooted walk
	// would ask about one authored block once per component, and not at all for a
	// boundary holding none.
	objections, err = constraint.RunOnBoundaryAttachmentLocalsKeys(
		ctx.Err, coordinates, coordinates, coordinates, view,
		BoundaryAttachmentLocalsEntryReadsNoLaterSibling,
	)
	if err != nil {
		return err
	}
	raised = append(raised, objections...)

	objections, err = constraint.RunOnBoundaryAttachmentLocalsKeys(
		ctx.Err, coordinates, coordinates, coordinates, view,
		BoundaryAttachmentLocalsEntryReadsOnlyDeclaredSiblings,
	)
	if err != nil {
		return err
	}
	raised = append(raised, objections...)

	return objectionsAsHalt(raised, "locals entries that read a sibling they cannot")
}

// authoredTemplateFieldAxes is the axis set the rules over an authored template
// field on a boundary-declared attachment are invoked once per: the boundary set,
// the attachments each boundary declares, and the template fields each of those
// attachments' FeatureRecipes writes.
//
// It is a fourth set beside the three above rather than more members of any of
// them, for the reason the locals and provides sets are their own: these rules
// read text an author wrote, which is there whether a plugin was ever loaded or
// not, so folding them into the plugin-report set would put them behind that
// set's resolution guard — and a rule skipped by a guard reports exactly what a
// rule that ran and approved reports. It is separate from the locals set because
// the axes are separate: that one enumerates the keys of one block, this one the
// template-bearing fields of a whole document.
//
// Naming all three axes together is what makes the innermost one
// un-skippable: a value supplying boundaries and attachments but no field
// enumeration does not compile here, so these rules cannot be invoked over an
// empty subject set by a caller that merely forgot one axis.
type authoredTemplateFieldAxes interface {
	constraint.BoundaryEnumerator
	constraint.BoundaryAttachmentEnumerator
	constraint.BoundaryAttachmentTemplateFieldEnumerator
}

// runAuthoredTemplateFieldRule invokes the rules declared over the inline
// template fields a boundary-declared attachment's FeatureRecipe writes, and
// returns what they raised as the render's halt.
//
// It runs before anything is evaluated, which is what the rules read for: the
// value a refused template would produce is never produced, so the authored text
// is the only input that can carry the question. It carries no plugin-resolution
// guard, for the reason the two authored-text readings above carry none.
//
// # One declaration per field, one rule per fault
//
// The rules are invoked once per template field, so a FeatureRecipe reaching
// outside its scope in three of its fields raises three objections and an author
// corrects all three from one build. Each carries its own position — the
// FeatureRecipe, and the authored field inside it — which is why they are not
// folded into one message: a position is something an author navigates to, and a
// single message naming three of them can be navigated to none.
//
// Both rules over the axis are asked, because they are separate findings with
// separate remedies: one names a pillar that exists for the per-component path
// and carries nothing here, which the author rewrites against the boundary
// pillars; the other names a reference that exists nowhere, which no rewriting
// against a scope will fix. A field declining the first is reported by the second
// and never by neither.
//
// # What this does NOT reach, stated because an unstated bound reads as coverage
//
// The axis is the attachments a BOUNDARY declares. A FeatureRecipe declaring the
// boundary scope is dispatched to the same surface when its attachment was
// declared on a COMPONENT instead, and no boundary key enumerates that
// attachment — so the rules are not invoked over it. That carrier keeps the
// refusal the render makes at dispatch, which is retained for exactly this reason
// and shares this rule's decision rather than re-implementing it. The set of
// fields is the inline ones: content imported from a file or a library is read
// when the render reaches it and is not authored in this document.
//
// So what the declaration adds is a position and a breadth on one carrier, never
// a refusal the build did not already make.
//
// The cancellation signal is the caller's own, so a long walk over a large
// deployment stops within one boundary of the caller asking it to.
func runAuthoredTemplateFieldRule(
	ctx context.Context,
	view constraint.DescribedView,
	coordinates authoredTemplateFieldAxes,
) error {
	if view == nil || coordinates == nil {
		return nil
	}
	raised, err := constraint.RunOnBoundaryAttachmentTemplateFields(
		ctx.Err, coordinates, coordinates, coordinates, view,
		BoundaryAttachmentTemplateReadsNoPerComponentPillar,
	)
	if err != nil {
		return err
	}

	objections, err := constraint.RunOnBoundaryAttachmentTemplateFields(
		ctx.Err, coordinates, coordinates, coordinates, view,
		BoundaryAttachmentTemplateReadsOnlyDefinedReferences,
	)
	if err != nil {
		return err
	}
	raised = append(raised, objections...)

	return objectionsAsHalt(raised, "template fields that reach outside the context they are evaluated against")
}

// authoredPathTemplateAxes is the axis pair the rules over an authored path
// template are invoked once per: the boundary set a deployment declares, and the
// path fields each of those boundaries writes.
//
// It is a fifth set beside the four above rather than more members of any of
// them, for the reason the locals, provides and template-field sets are their
// own: these rules read text an author wrote, which is there whether a plugin was
// ever loaded or not, so folding them into the plugin-report set would put them
// behind that set's resolution guard — and a rule skipped by a guard reports
// exactly what a rule that ran and approved reports. It is separate from the
// template-field set because the axes are separate: that one enumerates the
// template fields of a FeatureRecipe an attachment binds, this one the path
// fields of the scope itself, and a value satisfying one does not satisfy the
// other.
//
// Naming both axes together is what makes the inner one un-skippable: a value
// supplying boundaries but no field enumeration does not compile here, so these
// rules cannot be invoked over an empty subject set by a caller that merely
// forgot one axis.
type authoredPathTemplateAxes interface {
	constraint.BoundaryEnumerator
	constraint.BoundaryPathTemplateFieldEnumerator
}

// runAuthoredPathTemplateRule invokes the rules declared over the path templates
// each boundary of a deployment writes, and returns what they raised as the
// render's halt.
//
// It runs before anything is evaluated, which is what the rules read for: a
// reference these rules refuse is one whose value is never produced, so the
// authored text is the only input that can carry the question. It carries no
// plugin-resolution guard, for the reason the three authored-text readings above
// carry none.
//
// # One declaration per field, one rule per fault
//
// The rules are invoked once per path template, so a deployment reaching outside
// what a scope may name in both of its fields raises two objections and an
// operator corrects both from one build. That is wider than the evaluation-time
// refusal beneath this, which stops at the first template it reaches — and each
// objection carries its own position, the scope's own key inside the deployment
// document, which is why they are not folded into one message: a position is
// something an operator navigates to, and a single message naming several can be
// navigated to none.
//
// Both rules over the axis are asked, because they are separate findings with
// separate remedies: one names a variable this surface does not offer, which the
// operator rewrites against the ones it does; the other names a key beneath a
// variable that is offered, which is a typo. A field declining the first is
// reported by the second and never by neither.
//
// # What this does NOT reach, stated because an unstated bound reads as coverage
//
// The axis is the boundaries the deployment declares. The per-component field is
// authored beneath a component inside a scope, at a coordinate no boundary key
// enumerates, so the rules are not invoked over it; and the default this package
// supplies where a scope writes nothing is not authored text at all, so it is not
// enumerated. Both carriers keep the refusal the render makes when it evaluates
// the template, which is retained for exactly this reason and shares these rules'
// decision rather than re-implementing it.
//
// So what the declaration adds is a position and a breadth on one carrier, never
// a refusal the build did not already make.
//
// The cancellation signal is the caller's own, so a long walk over a large
// deployment stops within one boundary of the caller asking it to.
func runAuthoredPathTemplateRule(
	ctx context.Context,
	view constraint.DescribedView,
	coordinates authoredPathTemplateAxes,
) error {
	if view == nil || coordinates == nil {
		return nil
	}
	raised, err := constraint.RunOnBoundaryPathTemplateFields(
		ctx.Err, coordinates, coordinates, view,
		BoundaryPathTemplateReadsOnlyAdmittedFields,
	)
	if err != nil {
		return err
	}

	objections, err := constraint.RunOnBoundaryPathTemplateFields(
		ctx.Err, coordinates, coordinates, view,
		BoundaryPathTemplateReadsOnlyDefinedKeys,
	)
	if err != nil {
		return err
	}
	raised = append(raised, objections...)

	return objectionsAsHalt(raised, "path templates that reach outside the context they are evaluated against")
}

// crossBoundaryLinkAxis is the axis the rule over a connection's two rendering
// providers is invoked once per: the connection set the finalised graph carries.
//
// It is named as its own interface rather than folded into the plugin-report set
// because the two are guarded differently. That set is skipped for a render given
// no plugin resolution, since every fact in it comes from a plugin describing
// itself; this rule reads a provider's capability claim through the render's own
// engine resolution, which a render either has or does not, and it reports an
// advisory rather than refusing a build — so a guard that silenced it would
// silence a finding no build depends on being absent.
type crossBoundaryLinkAxis interface {
	constraint.EdgeEnumerator
}

// runCrossBoundaryLinkProviderRule invokes the rule declared over the two
// providers a connection's ends render through, and returns what it raised as
// warnings on the render's own stream.
//
// It is asked at this stage because that is where its facts are complete: both
// ends' rendering providers and both ends' capability claims are settled once
// every plugin the render reaches has described itself, and nothing later changes
// either. Asking after the assembly instead would report the same finding about
// output that had already been produced under it.
//
// # It returns warnings rather than an error, and that is the declaration
//
// The rule declares itself advisory: a connection spanning two providers carries
// the scalar value the injection contract pipes, so the output is a working
// deployment and the build has no reason to stop. The advisory conversion is what
// keeps that declaration true at the invocation — a raised objection reaching the
// halt conversion beside it would turn a build that renders into one that fails,
// whatever the declaration said.
//
// What comes back is the advisory itself rather than a line of text, so the
// finding reaches a consumer that renders nothing — an editor asking which
// authored key a connection's provider seam sits on reads a field rather than
// parsing a position back out of a sentence.
//
// Every offending connection is reported rather than the first, because the rule
// is invoked once per connection: a deployment wiring three connections across a
// provider seam tells the author about all three from one build, each positioned
// at the site the connection was authored in.
//
// The cancellation signal is the caller's own, so a walk over a large connection
// set stops within one connection of the caller asking it to.
func runCrossBoundaryLinkProviderRule(
	ctx context.Context,
	view constraint.DescribedView,
	coordinates crossBoundaryLinkAxis,
) ([]Diagnostic, error) {
	if view == nil || coordinates == nil {
		return nil, nil
	}
	raised, err := constraint.RunOnEdges(
		ctx.Err, coordinates, view, CrossBoundaryLinkEndsShareOneProvider,
	)
	if err != nil {
		return nil, err
	}
	return objectionsAsAdvisories(raised), nil
}

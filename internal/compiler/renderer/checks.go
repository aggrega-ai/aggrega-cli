// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// This file holds this package's rules expressed as constraint declarations:
// each is a value naming the sentinel it enforces, what a violation does to the
// build, the facts it reads, and what it is invoked once per. Declared that
// way, a rule's point in the compilation follows from the facts it reads rather
// than from where a call happens to sit, and the provenance its diagnostic
// carries is supplied to it rather than assembled in its body.
//
// The rules are declared here and the hand-written functions beside this file
// still run. That is what a reader comparing the two should expect rather than
// read as duplication left behind: a declaration is invoked where something
// presents a view over the compilation products its stage names, and nothing
// presents one at the stages these rules declare. The declaration is what makes
// the placement computable; the invocation arrives with the change that supplies
// the view these rules need, and the hand-written form is removed by the change
// that no longer needs it.
//
// That is a statement about these declarations and not about the framework. A
// view over an authored document is presented elsewhere, and a rule declared
// there is invoked through it, so a reader must not read the paragraph above as
// saying no view exists anywhere.
//
// What a declaration here does not carry is the traversal its hand-written
// counterpart performs. A hand-written scan over assembled output walks every
// dispatch group and every file within it, and sorts both so the file it names
// is the same one across runs of identical input. A check is invoked once per
// subject instead, and the framework's enumeration order is what makes that
// sequence deterministic — so a declaration reads one file's bytes, holds no
// loop, and sorts nothing. One that kept the walk would have re-implemented the
// traversal inside its own predicate, which is the arrangement the framework
// replaces; one that enumerated the path set and then read each path's bytes
// would have done the same thing by a longer route.
//
// A detail carries fact values and never a rendering of them: turning an
// objection into a message is the framework's job, and a rule that formatted one
// would be doing it twice. A subject's coordinates are legitimate detail where
// the locator does not already name them. A boundary is authored, so the locator
// names it; a provider is selected and an emitted path is produced rather than
// written, so neither reaches the locator and a rule that has to say which file
// it objects about names them here.

// CrossBoundaryLinkPlaceholderResolved reports each assembled file that still
// carries a cross-boundary link placeholder.
//
// A placeholder is a build-internal marker the host stamps into a consuming
// node's injected input for a uses edge whose ends assemble into different
// dispatch groups, and the consuming provider's link resolution replaces it with
// provider-native cross-reference wiring. One standing in assembled output means
// resolution never reached it — the injected value landed at a position the
// resolver could not address, or an edge was stamped with no matching resolve. A
// build-internal marker is never valid shipped output, so a violation halts the
// build rather than advising and letting it finish.
//
// It reads one fact, the bytes standing at one output path, and reads them for
// the single path it was invoked for. The whole of its predicate is a substring
// test over those bytes, which is why the bytes are all it needs: what makes a
// placeholder detectable is a prefix the host chose, so the rule never has to
// establish which edge stamped it or which provider was meant to resolve it.
// That the rule finds the same file across runs of identical input follows from
// the order it is invoked in, not from anything it does.
//
// Its detail names the provider and the emitted path, because a path is unique
// only within a boundary and a provider together — so naming both is what makes
// the objection point at one file, and the boundary the locator already carries
// completes the coordinate.
var CrossBoundaryLinkPlaceholderResolved = constraint.Check[
	constraint.RenderedView,
	constraint.BoundaryProviderPathSubject,
]{
	Sentinel: ErrOrphanCrossBoundaryLinkSentinel(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.RenderedView,
		s constraint.BoundaryProviderPathSubject,
	) []constraint.Objection {
		content := v.AssembledFileContent(s.BoundaryPath(), s.ProviderName(), s.EmittedPath())
		if !bytes.Contains(content, []byte(rendercore.CrossBoundaryLinkSentinelPrefix)) {
			return nil
		}
		return []constraint.Objection{s.Raise(
			ErrOrphanCrossBoundaryLinkSentinel(), s.ProviderName(), s.EmittedPath(),
		)}
	},
}

// SelfInjectKeyNotDeliveredByAnEdge reports each delivery key a component
// declares for itself that one of its own dependency edges also delivers under
// the same mechanism.
//
// Two authoring surfaces can deliver a value under one key: the component's own
// injection block, and the injection block on an entry in its dependency block.
// A key written on both is ambiguous about which value arrives, so the build
// halts and the author resolves it by removing one of the two. Presence in both
// is the whole signal: the rule compares no values and does not treat two
// sources agreeing as acceptable, because the agreement is a property of one
// input rather than of the authoring.
//
// It is invoked once per key the component declares for itself, which is the
// side the objection is attributed to, and reads one fact — the sockets whose
// edges deliver that same key under that same mechanism. A non-empty answer is
// the violation, and each socket in it is its own objection, so an author
// wiring the same key through two dependencies is told about both rather than
// about whichever sorted first.
//
// Its detail names the socket alone. The locator already names the mechanism
// and the key, because the subject stands for the entry the author wrote at
// that position; the socket belongs to a different entry the locator does not
// reach, so naming it is what makes the objection point at the second
// declaration as well as the first.
var SelfInjectKeyNotDeliveredByAnEdge = constraint.Check[
	constraint.GraphView,
	constraint.NodeMechanismDeliveryKeySubject,
]{
	Sentinel: ErrSelfInjectKeyCollision(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.GraphView,
		s constraint.NodeMechanismDeliveryKeySubject,
	) []constraint.Objection {
		sockets := v.UsesInjectDeliverySockets(s.NodeID(), s.MechanismName(), s.DeliveryKey())
		if len(sockets) == 0 {
			return nil
		}
		out := make([]constraint.Objection, 0, len(sockets))
		for _, socket := range sockets {
			out = append(out, s.Raise(ErrSelfInjectKeyCollision(), socket))
		}
		return out
	},
}

// ProvidesTemplateSelfContained reports each provides entry whose template
// reaches outside the self-contained data a provides template is evaluated
// against.
//
// A provides entry is evaluated against node-local data alone — the instance
// identifier, the configuration pillars, and the node's own resolved locals —
// so a reference to anything else would render empty rather than fail. The rule
// refuses the reference instead, which is what turns a silently empty value
// into a halt naming the expression to fix. The admitted set is not restated
// here: the rule evaluates the same declared scope the provides evaluation
// itself uses, so a field added to that surface becomes reachable from a
// template with no edit to this rule.
//
// It reads one fact, the template text as authored, and that is what lets the
// rule run before anything is evaluated. The resolved value cannot serve as the
// input, because a template this rule refuses is exactly one whose value is
// never produced. Reading the text also means the rule derives its own
// references rather than receiving a reference set collected in the direction a
// consumption question needs — attributing a reference wrongly here would
// refuse authoring the template language permits.
//
// A template that does not parse is not this rule's finding. Evaluation reports
// the parse error against the same entry, so refusing it here as well would
// report one authoring fault twice.
//
// Its detail names the offending expression. The locator names the provides
// entry, which is the position the author edits, and the expression is what
// inside that entry has to change.
var ProvidesTemplateSelfContained = constraint.Check[
	constraint.DescribedView,
	constraint.NodeProvidesKeySubject,
]{
	Sentinel: ErrProvidesSelfContainment(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeProvidesKeySubject,
	) []constraint.Objection {
		text := v.AuthoredNodeProvides(s.NodeID(), s.ProvidesKey())
		if text == "" {
			return nil
		}
		tree, err := gotmpl.ParseTree(text, "{{", "}}")
		if err != nil {
			return nil
		}
		violation := providesScope.check(tree)
		if violation == nil {
			return nil
		}
		return []constraint.Objection{s.Raise(
			ErrProvidesSelfContainment(), violation.Ref.Path,
		)}
	},
}

// The two pillar roots an artifact template may only read when its Recipe
// supplies them. They are declared once and read by both forms of the rule —
// the value below and the hand-written pre-check that still runs — because the
// roots ARE the rule's subject: a copy in each site would agree today and would
// be the thing that silently diverges if a pillar were ever renamed, since a
// stale spelling matches nothing and the check goes quiet rather than failing.
const (
	pillarRefWorkload = ".workload"
	pillarRefSource   = ".source"
)

// pillarRefMatches reports whether a template reference names a pillar root or
// reaches into it. One helper rather than the same two-part test written at each
// arm of each form: what counts as reading a pillar is one decision, and the
// exact-or-descendant shape of it is the part a re-implementation gets subtly
// wrong (a prefix test alone would match a sibling root whose name merely starts
// with the pillar's).
func pillarRefMatches(ref, pillar string) bool {
	return ref == pillar || strings.HasPrefix(ref, pillar+".")
}

// recipeSourceDeclared reports whether a Recipe carries a source, reading the
// RESOLVED value rather than the text the author wrote.
//
// A source that evaluated to an empty string leaves the Recipe with none just as
// an absent source does, and an author cannot tell the two apart from the
// outcome — so this is one condition rather than two, which is what lets a rule
// about a template reading the source pillar name a single cause.
//
// One helper for the same reason the pillar roots above have one home: this is a
// single decision with more than one reader, and a copy per reader would agree
// today and would be exactly what diverges the next time the emptiness condition
// changes. A nil Recipe carries no source.
func recipeSourceDeclared(recipe *types.Recipe) bool {
	if recipe == nil {
		return false
	}
	switch src := recipe.Spec.Source.(type) {
	case nil:
		return false
	case string:
		return src != ""
	default:
		return true
	}
}

// ArtifactPillarDeclared reports each reference an artifact template makes to a
// configuration pillar the node's Recipe does not carry.
//
// Two pillars are readable from an artifact template only when the Recipe
// supplies them. A Recipe declaring no workload body reaches artifact evaluation
// with no workload to read, and one carrying no source reaches it with no source
// to read — so a template naming either would render an empty value rather than
// fail. The rule refuses the reference instead, which turns a silently empty
// artifact into a halt naming the expression to fix.
//
// The source arm reads whether a source survived resolution rather than whether
// one was authored, and the two differ: a source template evaluating to an empty
// string leaves the Recipe with no source just as an absent source does. One
// condition covers both, so an author is told the same thing whichever way the
// source went missing.
//
// It reads four facts and holds no traversal over subjects, because it is
// invoked once per artifact a node projects. The delimiter pair is one of the
// four and is what makes the reference set correct rather than approximate: a
// provider whose own target syntax uses the usual pair declares an alternate
// one, and read with the wrong pair a template's downstream syntax would surface
// as references it does not make. The pair completes before the text does, which
// is why the rule sits at the stage that holds the text and reads the pair
// through the stage containment.
//
// Two deliberate differences from the hand-written form beside this file, so a
// reader comparing them does not read either as drift. This value reports EVERY
// offending reference in one template rather than the first, because an author
// naming two absent pillars is better told about both than about whichever the
// parser reached first. And a template that does not parse is not this rule's
// finding: evaluation reports the parse error against the same entry, so
// refusing it here as well would report one authoring fault twice — the
// hand-written form reports it because it is the path that runs, and the entry
// would otherwise reach evaluation with no diagnostic at all.
//
// Its detail names the offending expression, and only that. The locator names
// the artifact entry inside the Recipe's own file — which is the position the
// author edits — so the expression is what inside that entry has to change. The
// node is a coordinate the subject carries and the locator does not, and it is
// deliberately left out of the detail: the fault is in the Recipe's text rather
// than in any one node that binds it, so naming a node would point an author at
// an instance when the edit is in the entry every instance shares.
var ArtifactPillarDeclared = constraint.Check[
	constraint.HydratedView,
	constraint.NodeArtifactSubject,
]{
	Sentinel: ErrArtifactPillarNotDeclared(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.HydratedView,
		s constraint.NodeArtifactSubject,
	) []constraint.Objection {
		delims := v.ProviderArtifactTemplateDelimiters(s.NodeID())
		refs, err := gotmpl.CollectTemplateFieldAccessesWithDelims(
			v.ArtifactTemplateText(s.NodeID(), s.ArtifactName()), delims.Left, delims.Right,
		)
		if err != nil {
			return nil
		}
		workloadDeclared := v.ParsedNodeWorkloadPresent(s.NodeID())
		sourceDeclared := v.ResolvedNodeSourceDeclared(s.NodeID())
		var out []constraint.Objection
		for _, ref := range refs {
			switch {
			case !workloadDeclared && pillarRefMatches(ref, pillarRefWorkload):
			case !sourceDeclared && pillarRefMatches(ref, pillarRefSource):
			default:
				continue
			}
			out = append(out, s.Raise(ErrArtifactPillarNotDeclared(), ref))
		}
		return out
	},
}

// The provider-compatibility rule, declared once per pipeline the rule governs.
//
// One rule, three values, and the reason is the type rather than the rule: a
// check carries exactly one subject type parameter, and three separately
// authored pipelines are three subject kinds — a component's own Recipe
// pipeline, the pipeline an attachment's FeatureRecipe carries on a node, and the
// pipeline a boundary-scope attachment's FeatureRecipe carries. A step
// identifier is meaningful only together with the pipeline it was written in, so
// one value over one kind would govern one of the three and read as though it
// governed the rule.
//
// # What each predicate does, and what it deliberately does not do
//
// Each reads the scope the step's export declares, the capabilities the step's
// plugin declares, and the provider rendering the documents that scope names —
// then objects when the provider is absent from the declared set. Matching is
// exact-string against the enumerated declaration, with no family abstraction:
// an empty capability set declares compatibility with nothing and therefore
// matches no provider, so a step whose plugin declares none objects rather than
// passing as unconstrained.
//
// None of them holds a loop over steps, because each is invoked once per step.
// None reads a descriptor: the capabilities and the scope arrive as declared
// facts, so a predicate here crosses to no guest and holds no plugin
// self-report — which is what lets the rule be complete at the point every
// plugin has described itself rather than at the point documents exist.
//
// # The provider facts are keyed where the value varies, and never by the part it plays
//
// A provider does not vary across the steps of one pipeline: one entity declares
// one capability for the whole of it. So the provider facts are keyed by the node
// or the attachment rather than by the step, and there is one fact per authoring
// site rather than one per part a provider plays at a site.
//
// That distinction is load-bearing here and easy to lose. The provider rendering
// a node's own documents is the provider a step of that node's own pipeline is
// matched against, AND it is the provider a host-addressed step of an
// attachment's pipeline on that node is matched against — one value, read at two
// sites. Which part it plays is a property of the reading site, so it is stated
// in each predicate below and carried in no fact name.
//
// # Emptiness
//
// An absent provider for the documents a scope names means the site has no such
// documents, and the rule does not enforce there. The concrete case is a
// host-addressed step at a site that stands against no host component: a
// component's own pipeline and a boundary-scope attachment's pipeline both have
// one, and a step addressing host documents there is refused by the rule that
// owns the missing document set rather than reported here as a compatibility
// claim against an empty provider.
//
// The opposite case does not arise at any site a traversal reaches: the
// capability an entity declares is required by both the Recipe and FeatureRecipe
// contracts, so the provider rendering a site's own documents is always present
// wherever a step of that site is enumerated.

// PipelineStepProviderCompatible reports each step of a node's own Recipe
// pipeline whose plugin does not declare compatibility with the provider
// rendering the documents the step addresses.
//
// A node's own pipeline stands against no host component, so a step addressing
// host documents has no provider to be matched against and is left to the rule
// that owns the missing document set. Every other step addresses the node's own
// documents, which the node's own provider renders.
//
// Its detail names that provider, because the locator names the authored step
// and a provider is selected rather than written — so the value the author has
// to reconcile the declaration against appears nowhere else in the objection.
var PipelineStepProviderCompatible = constraint.Check[
	constraint.DescribedView,
	constraint.NodePipelineStepSubject,
]{
	Sentinel: ErrPluginProviderIncompatible(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodePipelineStepSubject,
	) []constraint.Objection {
		if v.StepExportScope(s.NodeID(), s.StepID()) == types.ScopeHost {
			return nil
		}
		provider := v.NodeRenderingProvider(s.NodeID())
		if providerCompatible(provider, v.StepPluginCompatibleCapabilities(s.NodeID(), s.StepID())) {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrPluginProviderIncompatible(), provider)}
	},
}

// AttachmentPipelineStepProviderCompatible reports each step of the pipeline an
// attachment's FeatureRecipe carries on a node whose plugin does not declare
// compatibility with the provider rendering the documents the step addresses.
//
// This is the one site where both arms are live, and the two providers are
// different values from different authored entities: a step addressing the
// attachment's own documents is matched against the provider its FeatureRecipe
// declares, while a step addressing the host component's documents is matched
// against the provider the host node's own Recipe declares — the same value that
// site's own pipeline is matched against. A host component with no bound Recipe
// renders no host documents, so a host-addressed step there is left to the rule
// that owns the missing document set.
//
// Its detail names the attachment and the provider. The locator names the
// authored step inside the FeatureRecipe, which is the document to edit, and
// neither the attachment that bound it nor the selected provider appears there.
var AttachmentPipelineStepProviderCompatible = constraint.Check[
	constraint.DescribedView,
	constraint.NodeAttachmentPipelineStepSubject,
]{
	Sentinel: ErrPluginProviderIncompatible(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeAttachmentPipelineStepSubject,
	) []constraint.Objection {
		provider := v.AttachmentRenderingProvider(s.NodeID(), s.AttachmentName())
		if v.AttachmentStepExportScope(s.NodeID(), s.AttachmentName(), s.StepID()) == types.ScopeHost {
			provider = v.NodeRenderingProvider(s.NodeID())
			if provider == "" {
				return nil
			}
		}
		declared := v.AttachmentStepPluginCompatibleCapabilities(
			s.NodeID(), s.AttachmentName(), s.StepID(),
		)
		if providerCompatible(provider, declared) {
			return nil
		}
		return []constraint.Objection{s.Raise(
			ErrPluginProviderIncompatible(), s.AttachmentName(), provider,
		)}
	},
}

// AttachmentPipelineStepOwnScopeHasAMergedTarget reports each step of an
// attachment's pipeline that addresses the attachment's own documents on a node
// whose documents are merged with its dispatch group's, where the step has
// nothing to address by the time it runs.
//
// It is the second rule over an attachment's pipeline steps, and the two are
// separate because they are separate findings with separate conditions and
// separate remedies. The one beside it asks whether a step's plugin declares
// compatibility with the provider its scope names. This one asks whether the
// documents that scope names will exist as discrete documents at all — a
// question that has an answer before any provider is matched against anything,
// and a different answer to give the author.
//
// # Why the condition is about the host's provider and not the attachment's
//
// A provider that merges its dispatch group's per-node output renders that
// output as fragments: the discrete documents arrive only once the group is
// merged. An attachment's pipeline against such a component therefore cannot
// run while the component is rendered, and runs once the merge has produced
// the documents instead. The attachment's OWN output is never part of that
// merge — an attachment's documents are written under its own position, as
// discrete files, whichever provider the component uses — so a step addressing
// the attachment's own documents has no merged document waiting for it, and
// nothing to address when it finally runs.
//
// So the finding is a property of the component's provider paired with the
// step's own scope, and the attachment's provider does not enter it. That is
// what makes the condition readable here: both halves are declared facts at the
// point every plugin and provider has described itself.
//
// The remedies are the ones the condition admits: give the attachment's own
// output a provider that does not merge, address the component's documents
// instead, or drop the step.
var AttachmentPipelineStepOwnScopeHasAMergedTarget = constraint.Check[
	constraint.DescribedView,
	constraint.NodeAttachmentPipelineStepSubject,
]{
	Sentinel: ErrPipelinePatchPreAssembly(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.NodeAttachmentPipelineStepSubject,
	) []constraint.Objection {
		if v.AttachmentStepExportScope(
			s.NodeID(), s.AttachmentName(), s.StepID(),
		) == types.ScopeHost {
			return nil
		}
		if v.ProviderBoundaryAssembly(s.NodeID()) != wasm.BoundaryAssemblyMerge {
			return nil
		}
		return []constraint.Objection{s.Raise(
			ErrPipelinePatchPreAssembly(), s.AttachmentName(),
		)}
	},
}

// BoundaryAttachmentPipelineStepProviderCompatible reports each step of the
// pipeline a boundary-scope attachment's FeatureRecipe carries whose plugin does
// not declare compatibility with the provider rendering the documents the step
// addresses.
//
// A boundary-scope attachment stands against no single component, so it has no
// host documents and a host-addressed step is left to the rule that owns the
// missing document set — the same shape a node's own pipeline has, reached for a
// different reason. Every other step addresses the attachment's own documents,
// which the provider its FeatureRecipe declares renders.
//
// Its detail names the attachment and the provider, on the same reading the
// node-carried attachment pipeline follows: the locator names the authored step
// inside the FeatureRecipe and carries neither.
var BoundaryAttachmentPipelineStepProviderCompatible = constraint.Check[
	constraint.DescribedView,
	constraint.BoundaryAttachmentPipelineStepSubject,
]{
	Sentinel: ErrPluginProviderIncompatible(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DescribedView,
		s constraint.BoundaryAttachmentPipelineStepSubject,
	) []constraint.Objection {
		if v.BoundaryAttachmentStepExportScope(
			s.BoundaryPath(), s.AttachmentName(), s.StepID(),
		) == types.ScopeHost {
			return nil
		}
		provider := v.BoundaryAttachmentRenderingProvider(s.BoundaryPath(), s.AttachmentName())
		declared := v.BoundaryAttachmentStepPluginCompatibleCapabilities(
			s.BoundaryPath(), s.AttachmentName(), s.StepID(),
		)
		if providerCompatible(provider, declared) {
			return nil
		}
		return []constraint.Objection{s.Raise(
			ErrPluginProviderIncompatible(), s.AttachmentName(), provider,
		)}
	},
}

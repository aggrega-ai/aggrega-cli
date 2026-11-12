// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"fmt"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// describedView presents, as named facts, what every plugin a node reaches has
// reported about itself: the provider capabilities each pipeline step's plugin
// declares its step is compatible with, the scope of the export each step
// dispatches to, the provider that renders each site's own documents, the
// assembly claim and artifact delimiter pair that provider declares, and the
// text a Recipe authored for each provides key and for each local.
//
// The two authored-text facts sit here rather than beside the values they resolve
// to, and that placement is the whole of what makes a rule over them possible: a
// template such a rule refuses is exactly one whose value is never produced, so a
// fact read from the resolved side could not carry the question at all.
//
// It satisfies [constraint.DescribedView], and the compiler holds it to that
// contract at the bottom of this file rather than a comment claiming it.
//
// # The earlier stage is embedded, not re-projected
//
// A stage's view contains the stage it extends, so every fact complete before
// any plugin described itself is readable here. Those facts are answered by the
// presentation the graph finalisation already built, embedded and delegated to,
// rather than re-derived: one fact has one producer, and two producers of a fact
// are free to disagree with nothing comparing them. This value holds the graph
// and the Recipe set, so re-reading a node's labels or its attachment set here
// would be easy and would be exactly that second producer; no accessor below
// does.
//
// What this stage adds is what a self-report carries and what the entity binding
// the plugin declares, and nothing that was complete before them.
//
// # Every plugin fact is read through one retention
//
// A step plugin's self-report is a pure function of its bytes, so two readers of
// one plugin cannot legitimately be told different things. The accessors below
// therefore read it through the render's own retention — one value per plugin for
// the whole render, reached through the single descriptor seam — rather than
// describing a plugin a second time for this presentation. Nothing is described
// until an accessor asks, so presenting this stage loads no plugin on its own.
//
// The provider facts read the provider engine's own retained self-report through
// the same optional interfaces the render path reads them through, for the same
// reason.
//
// # A plugin the host could not ask is not a plugin that declared nothing
//
// A fact accessor returns a value and never an error, so a plugin the host cannot
// resolve, load or describe is answered with the zero value of the fact asked
// for — which reads exactly like a plugin that declared nothing. The two are
// different facts and only one of them is a declaration: an empty compatibility
// list is a claim its author made, while an unobtainable self-report is the
// absence of any claim at all. A rule handed the second as if it were the first
// refuses a step on the strength of something nobody said.
//
// So this value keeps the zero-value answer AND records the reads it could not
// answer. The record is what its caller halts on, in preference to whatever a
// rule raised from the same read: the failure the host met is the finding, and a
// verdict reached over a declaration nothing ever read is not. That record is
// read through [describedView.selfReportRefusal], which is addressed to the
// caller invoking the rules and to nothing else — it is not a fact, because no
// author declared it.
//
// # Values are handed over uncopied
//
// The contract every view shares makes a fact's value immutable to a check, and
// this value keeps that contract. The plugin retention hands out a copy that
// shares no mutable memory with what it retains, so a check reading a declared
// capability set or a schema document cannot reach what a later reader sees.
type describedView struct {
	constraint.GraphView

	// ctx carries the fetch surface and the shared compilation cache a plugin
	// load reads. It is held rather than taken per accessor because a fact
	// accessor's shape is fixed by the contract it satisfies and admits no
	// argument for it, and the presentation is built and dropped inside the one
	// render whose context this is.
	ctx context.Context

	// graph is the finalised graph, read only for the attachment sets that key
	// this stage's own facts — never for a fact the embedded presentation
	// already answers.
	graph *types.IRGraph

	// recipes is the Recipe bound to each node, by node identity, as the render
	// resolved it. A node with no bound Recipe is absent rather than nil-valued.
	recipes map[string]*types.Recipe

	// artifactLibraries is the ArtifactLibrary set the load resolved, keyed by
	// bare name, read only for the facts about what an authored artifacts entry's
	// library reference resolves to.
	//
	// It is held rather than reached for because there is nothing here to reach
	// through: the set arrives on the render's input already resolved, and a
	// presentation resolving one itself would be a second reading of the catalog.
	// A render given none answers every fact over it as an entry naming a library
	// nothing declares, which is the same answer the render's own walk would
	// reach for such a build.
	artifactLibraries map[string]types.ArtifactLibrary

	// mappingLibraries is the MappingLibrary set the load resolved, keyed by bare
	// name, read only for the facts about what an authored values-mapping import
	// reference resolves to.
	//
	// It is held rather than reached for, on the reading the ArtifactLibrary set
	// beside it carries: the set arrives on the render's input already resolved,
	// and a presentation resolving one itself would be a second reading of the
	// catalog. A render given none answers every fact over it as a field naming a
	// library nothing declares, which is the same answer the render's own resolver
	// would reach for such a build.
	mappingLibraries map[string]types.MappingLibrary

	// workloadLibraries is the WorkloadLibrary set the load resolved, keyed by
	// bare name, read only for the facts about what an authored workload import
	// reference resolves to.
	//
	// It is held rather than reached for, on the reading the two library sets
	// beside it carry: the set arrives on the render's input already resolved, and
	// a presentation resolving one itself would be a second reading of the
	// catalog. A render given none answers every fact over it as a field naming a
	// library nothing declares, which is the same answer the render's own resolver
	// would reach for such a build.
	workloadLibraries map[string]types.WorkloadLibrary

	// recipeLookup resolves the FeatureRecipe an attachment binds. It is the
	// same lookup the render resolves attachments through, so a fact about an
	// attachment names the document the render actually bound.
	recipeLookup RecipeLookup

	// release is the Release document, read only for the text an operator
	// authored on a boundary. It is held rather than reached for through the
	// graph, because the graph's projection of a boundary records one only once
	// something is attached at it — so a scope carrying a path template and
	// nothing else would be absent, and the fact would answer empty for text the
	// operator did write.
	release *types.Release

	// describe reads one step's plugin self-report through the render's retention,
	// so a plugin several steps or several nodes name is described once for the
	// whole render.
	describe StepDescriber

	// lookup resolves a plugin URI to its verified bytes. It is held as well as
	// bound into describe because a render given no resolution resolves nothing at
	// all, and every plugin fact answers from nothing there — which this value is
	// what tells.
	lookup pluginpath.Lookup

	// engineFactory resolves a provider binding to the engine that renders it,
	// which carries that provider's own retained self-report.
	engineFactory EngineFactory

	// renderedNames is the name each node's own provider reported for the
	// resources it renders for that node, as the pass ahead of this presentation
	// collected it. A node whose provider reported none is absent.
	//
	// It is TAKEN rather than asked for here, and that is what keeps the fact
	// single-sourced. The answers reach two readers — the rule declared over a
	// provides template that names the value, and the evaluation that renders one
	// — and a presentation asking the guest again would be a second producer of
	// one fact, free to disagree with the first about a name neither of them
	// composed.
	renderedNames NodeRenderedNames

	// refusals records the plugin self-reports the accessors below asked for and
	// could not obtain. It is a value rather than a pointer so every presentation
	// has one without a constructor supplying it: a nil record would put the
	// swallow back, silently, in exactly the reads it exists to keep.
	refusals selfReportRefusals
}

// newDescribedView presents what every plugin a render reaches has reported
// about itself, with the facts complete before any of them described itself
// answered by the presentation the graph finalisation produced.
//
// It is unexported because the render is the one thing that builds it: every
// product it names is the render's own working state, so a caller outside this
// package has nothing to construct it from and the bypass does not compile.
//
// The products are taken rather than reached for, because a view projects facts
// already produced and never produces one of its own. The retention it is handed
// is lazy, so building this presentation describes nothing.
//
// A build constructs exactly one of these, where every product it names is first
// in hand, and publishes it on the render's result. Constructing a second one
// over different products would give this stage's facts a second producer, free
// to disagree with the first about a plugin neither of them describes twice.
//
// It returns nil when the graph, the earlier presentation or the retention is
// absent. A presentation missing any of them would answer a whole class of facts
// from nothing, and a presentation that answers emptily is worse than an absent
// one, because a rule reading it cannot tell an empty answer from a real one.
//
// The Release document is taken but is NOT among the products whose absence
// returns nil, and the asymmetry is deliberate. The graph, the earlier
// presentation and the retention key whole classes of fact; the Release
// document keys one, and a render given none enumerates no boundary at all — so a
// rule over that fact is invoked zero times rather than being answered emptily.
// The ArtifactLibrary set is taken but is NOT among the products whose absence
// returns nil, on the same reading the Release document carries: a render
// given none has no entry importing a library that could resolve, so the facts
// over it answer as they would for a library nothing declares — which is an
// answer rather than an absence. The MappingLibrary and WorkloadLibrary sets are
// taken on the same reading and for the same reason.
func newDescribedView(
	ctx context.Context,
	graph *types.IRGraph,
	earlier constraint.GraphView,
	recipes map[string]*types.Recipe,
	recipeLookup RecipeLookup,
	release *types.Release,
	descriptors *stepDescriptorMemo,
	lookup pluginpath.Lookup,
	engineFactory EngineFactory,
	renderedNames NodeRenderedNames,
	artifactLibraries map[string]types.ArtifactLibrary,
	mappingLibraries map[string]types.MappingLibrary,
	workloadLibraries map[string]types.WorkloadLibrary,
) constraint.DescribedView {
	if graph == nil || earlier == nil || descriptors == nil {
		return nil
	}
	return &describedView{
		GraphView:         earlier,
		ctx:               ctx,
		graph:             graph,
		recipes:           recipes,
		recipeLookup:      recipeLookup,
		release:           release,
		describe:          stepDescriberOver(descriptors, lookup),
		lookup:            lookup,
		engineFactory:     engineFactory,
		renderedNames:     renderedNames,
		artifactLibraries: artifactLibraries,
		mappingLibraries:  mappingLibraries,
		workloadLibraries: workloadLibraries,
	}
}

// NodeRenderedName returns the name the provider rendering a node's own documents
// reported it will give the resources it renders for that node, empty when it
// reported none.
//
// It answers from the collection taken ahead of this presentation rather than by
// asking the guest again, so the name a rule reads and the name a provides
// template resolves to are one answer. A node absent from that collection has no
// such name — the same answer a node with no bound Recipe gives, and the same
// answer for the author, who has nothing of this kind to read either way.
func (v *describedView) NodeRenderedName(node string) string {
	return v.renderedNames[node]
}

// NodeDeclaresAuthoredSource reports whether a node's Recipe declares source the
// author supplied, read from the text that Recipe wrote.
//
// It reads the authored value through the same predicate the render's own source
// resolution is gated on, so the presented fact is the one the render acts on. A
// node with no bound Recipe declares none.
func (v *describedView) NodeDeclaresAuthoredSource(node string) bool {
	return recipeSourceDeclared(v.recipes[node])
}

// StepPluginCompatibleCapabilities returns the provider capabilities the plugin
// behind one step of a node's own Recipe pipeline declares its step is
// compatible with, in declared order.
func (v *describedView) StepPluginCompatibleCapabilities(node string, step string) []string {
	desc, ok := v.stepDescriptor(v.nodePipeline(node), step)
	if !ok {
		return nil
	}
	return desc.Capabilities
}

// StepExportScope returns the document root the export one step of a node's own
// Recipe pipeline dispatches to declares it operates on, empty when the export
// declares none.
func (v *describedView) StepExportScope(node string, step string) string {
	return v.scopeOf(v.nodePipeline(node), step)
}

// ProviderBoundaryAssembly returns how the provider rendering a node's own
// documents declares it combines a dispatch group's per-node output, empty when
// it declares no assembly claim.
//
// It reads the claim through the same reporter the render classification reads
// it through, so the presented value is the one the render acts on.
func (v *describedView) ProviderBoundaryAssembly(node string) string {
	engine, ok := v.nodeEngine(node)
	if !ok {
		return ""
	}
	return boundaryAssemblyOf(engine)
}

// ProviderCrossBoundaryLinking reports whether the provider rendering a node's
// own documents advertises cross-boundary linking.
//
// It reads the claim through the same seam the link pass reads it through, so
// the presented value is the one the render acts on. A node with no bound
// Recipe, and a binding the factory cannot resolve, both answer false — the same
// answer a provider making no such claim gives, and for the same reason: none of
// the three leaves the render anything to wire, so the connection carries the
// scalar value the injection contract already pipes.
func (v *describedView) ProviderCrossBoundaryLinking(node string) bool {
	engine, ok := v.nodeEngine(node)
	if !ok {
		return false
	}
	return linksCrossBoundary(engine)
}

// ProviderHonoursOmits reports whether the provider rendering a node's own
// documents declares that it honours omit entries, and whether a provider resolved
// behind the node's binding at all.
//
// It reads the claim through the one seam that claim has, [honoursOmits], so the
// presented value is the one the provider stated about itself.
func (v *describedView) ProviderHonoursOmits(node string) (bool, bool) {
	return omitsClaimOf(v.nodeEngine(node))
}

// AttachmentProviderHonoursOmits reports whether the provider rendering one
// attachment's own documents on a node declares that it honours omit entries, and
// whether a provider resolved behind that attachment's binding at all.
func (v *describedView) AttachmentProviderHonoursOmits(node string, attachment string) (bool, bool) {
	return omitsClaimOf(v.attachmentEngine(node, attachment))
}

// BoundaryAttachmentProviderHonoursOmits reports whether the provider rendering
// one boundary-scope attachment's documents declares that it honours omit entries,
// and whether a provider resolved behind that attachment's binding at all.
func (v *describedView) BoundaryAttachmentProviderHonoursOmits(boundary string, attachment string) (bool, bool) {
	return omitsClaimOf(v.boundaryAttachmentEngine(boundary, attachment))
}

// omitsClaimOf is the claim one resolved engine makes about omit entries, and
// the one place every form of that fact reads it from. It takes the outcome of an
// engine resolution rather than resolving one, for the reason
// [declaredArtifactDelimiters] does: the three forms resolve their engine from
// three different bindings, and the reading of the resolved engine is the same for
// all of them.
//
// An unresolved engine answers false in both results. No provider stands behind
// the binding, so there is no claim to report, and the second result is what keeps
// that from reading as a provider that declined.
func omitsClaimOf(engine Provider, resolved bool) (bool, bool) {
	if !resolved {
		return false, false
	}
	return honoursOmits(engine), true
}

// NodeDeclaresOmits reports whether a node's own Recipe declares omit entries, as
// written. A node with no bound Recipe declares none.
func (v *describedView) NodeDeclaresOmits(node string) bool {
	recipe, ok := v.recipes[node]
	if !ok || recipe == nil {
		return false
	}
	return declaresOmits(recipe.Spec.Omits)
}

// AttachmentDeclaresOmits reports whether the FeatureRecipe one attachment binds
// on a node declares omit entries, as written. An attachment with no bound
// FeatureRecipe declares none.
func (v *describedView) AttachmentDeclaresOmits(node string, attachment string) bool {
	fr, ok := v.attachmentFeatureRecipe(node, attachment)
	if !ok {
		return false
	}
	return declaresOmits(fr.Spec.Omits)
}

// BoundaryAttachmentDeclaresOmits reports whether the FeatureRecipe one attachment
// declared on a boundary binds declares omit entries, as written.
func (v *describedView) BoundaryAttachmentDeclaresOmits(boundary string, attachment string) bool {
	fr, ok := v.boundaryAttachmentFeatureRecipe(boundary, attachment)
	if !ok {
		return false
	}
	return declaresOmits(fr.Spec.Omits)
}

// ProviderAcceptsBoundaryArtifacts reports whether the provider rendering a node's
// own documents declares that it accepts the files a Recipe contributes to its
// boundary's shared output, and whether a provider resolved behind the node's
// binding at all.
//
// It reads the claim through the one seam that claim has,
// [acceptsBoundaryArtifacts], and an unresolved engine answers false in both
// results on the reading [omitsClaimOf] states for its own claim.
func (v *describedView) ProviderAcceptsBoundaryArtifacts(node string) (bool, bool) {
	engine, resolved := v.nodeEngine(node)
	if !resolved {
		return false, false
	}
	return acceptsBoundaryArtifacts(engine), true
}

// RecipeBoundaryArtifactsDeclared reports whether a node's own Recipe declares
// files for its boundary's shared output, as written. A node with no bound Recipe
// declares none, and a coordinate naming any position but the boundary block's
// answers false.
func (v *describedView) RecipeBoundaryArtifactsDeclared(node string, block string) bool {
	if block != recipeFieldBoundary {
		return false
	}
	recipe, ok := v.recipes[node]
	if !ok || recipe == nil {
		return false
	}
	return recipe.Spec.Boundary.Artifacts.Len() > 0
}

// NodeRenderingProvider returns the provider that renders a node's own
// documents, as the engine kind its Recipe's declared capability projects to.
//
// A node with no bound Recipe renders through no provider and answers empty.
func (v *describedView) NodeRenderingProvider(node string) string {
	recipe, ok := v.recipes[node]
	if !ok || recipe == nil {
		return ""
	}
	return ProviderEngineKind(recipe.Spec.Capability)
}

// AttachmentRenderingProvider returns the provider that renders one attachment's
// own documents on a node, as the engine kind its bound FeatureRecipe's declared
// capability projects to.
//
// An attachment with no bound FeatureRecipe renders through no provider and
// answers empty.
func (v *describedView) AttachmentRenderingProvider(node string, attachment string) string {
	fr, ok := v.attachmentFeatureRecipe(node, attachment)
	if !ok {
		return ""
	}
	return ProviderEngineKind(fr.Spec.Capability)
}

// AttachmentExecutionScope returns the scope one attachment's bound
// FeatureRecipe declares its resource generation runs at, empty when it declares
// none.
//
// An attachment with no bound FeatureRecipe answers empty, which is the same
// answer a FeatureRecipe stating no scope gives: both generate per component.
func (v *describedView) AttachmentExecutionScope(node string, attachment string) string {
	fr, ok := v.attachmentFeatureRecipe(node, attachment)
	if !ok {
		return ""
	}
	return fr.Spec.ExecutionScope
}

// AttachmentReadsCallerRenderFacts reports whether one attachment's bound
// FeatureRecipe declares that its templates read its callers' render facts.
//
// It reads the declaration through the one reading the render decides where an
// attachment renders by, so the rule asking this and the render cannot come to
// disagree about what was declared. An attachment with no bound FeatureRecipe
// answers false.
func (v *describedView) AttachmentReadsCallerRenderFacts(node string, attachment string) bool {
	fr, ok := v.attachmentFeatureRecipe(node, attachment)
	if !ok {
		return false
	}
	return readsCallerRenderFacts(fr)
}

// AttachmentReadsTargetRenderFacts reports whether one attachment's bound
// FeatureRecipe declares that its templates read the render facts of the
// components its host is wired to.
//
// It reads the declaration through the one reading the render decides where an
// attachment renders, and what its connections carry, by, so the rule asking this
// and the render cannot come to disagree about what was declared. An attachment
// with no bound FeatureRecipe answers false.
func (v *describedView) AttachmentReadsTargetRenderFacts(node string, attachment string) bool {
	fr, ok := v.attachmentFeatureRecipe(node, attachment)
	if !ok {
		return false
	}
	return readsTargetRenderFacts(fr)
}

// AttachmentStepPluginCompatibleCapabilities returns the provider capabilities
// the plugin behind one step of an attachment's pipeline on a node declares its
// step is compatible with, in declared order.
func (v *describedView) AttachmentStepPluginCompatibleCapabilities(
	node string, attachment string, step string,
) []string {
	desc, ok := v.stepDescriptor(v.attachmentPipeline(node, attachment), step)
	if !ok {
		return nil
	}
	return desc.Capabilities
}

// AttachmentStepExportScope returns the document root the export one step of an
// attachment's pipeline on a node dispatches to declares it operates on, empty
// when the export declares none.
func (v *describedView) AttachmentStepExportScope(
	node string, attachment string, step string,
) string {
	return v.scopeOf(v.attachmentPipeline(node, attachment), step)
}

// BoundaryAttachmentRenderingProvider returns the provider that renders the
// documents of one attachment declared on a boundary, as the engine kind its
// bound FeatureRecipe's declared capability projects to.
func (v *describedView) BoundaryAttachmentRenderingProvider(
	boundary string, attachment string,
) string {
	fr, ok := v.boundaryAttachmentFeatureRecipe(boundary, attachment)
	if !ok {
		return ""
	}
	return ProviderEngineKind(fr.Spec.Capability)
}

// BoundaryAttachmentStepPluginCompatibleCapabilities returns the provider
// capabilities the plugin behind one step of the pipeline of an attachment
// declared on a boundary declares its step is compatible with, in declared
// order.
func (v *describedView) BoundaryAttachmentStepPluginCompatibleCapabilities(
	boundary string, attachment string, step string,
) []string {
	desc, ok := v.stepDescriptor(v.boundaryAttachmentPipeline(boundary, attachment), step)
	if !ok {
		return nil
	}
	return desc.Capabilities
}

// BoundaryAttachmentStepExportScope returns the document root the export one
// step of the pipeline of an attachment declared on a boundary dispatches to
// declares it operates on, empty when the export declares none.
func (v *describedView) BoundaryAttachmentStepExportScope(
	boundary string, attachment string, step string,
) string {
	return v.scopeOf(v.boundaryAttachmentPipeline(boundary, attachment), step)
}

// AuthoredNodeProvides returns the template text a node's Recipe declares for
// one provides key, as written and before anything is evaluated.
func (v *describedView) AuthoredNodeProvides(node string, key string) string {
	recipe, ok := v.recipes[node]
	if !ok || recipe == nil {
		return ""
	}
	text, _ := recipe.Spec.Provides.Get(key)
	return text
}

// DeclaredLocalsKeys returns the local names a node's Recipe declares, in author
// order.
//
// Author order is the fact rather than an incidental property of it: locals are
// evaluated in the order they are declared, so a rule about what a local may
// reference is a rule about this order. The order is the one the Recipe's own
// ordered mapping holds, so it is the order the author wrote.
//
// It is answered from the Recipe set this presentation already holds rather than
// from any retention of what the block resolved to. The keys are authored, so they
// are complete as soon as the binding is known — and reading them from a later
// product would put the fact behind the evaluation a rule over it exists to
// prevent.
func (v *describedView) DeclaredLocalsKeys(node string) []string {
	recipe, ok := v.recipes[node]
	if !ok || recipe == nil {
		return nil
	}
	return recipe.Spec.Locals.Keys()
}

// AuthoredNodeLocals returns the template text a node's Recipe declares for one
// local, as written and before anything is evaluated.
//
// It is answered from the same Recipe the key order above is read from, so a key
// that order presents is a key this can answer — a rule invoked over the order
// cannot be asked about an entry whose text nothing would supply.
func (v *describedView) AuthoredNodeLocals(node string, key string) string {
	recipe, ok := v.recipes[node]
	if !ok || recipe == nil {
		return ""
	}
	text, _ := recipe.Spec.Locals.Get(key)
	return text
}

// AttachmentDeclaredLocalsKeys returns the local names the FeatureRecipe one
// attachment binds on a node declares, in author order.
//
// It is answered from the FeatureRecipe the render resolved for the attachment,
// through the same binding the render evaluates that block with — so a key
// presented here is a key that evaluation will reach.
//
// The answer is the attachment's OWN block and nothing else. The node's resolved
// locals are merged over it after it has resolved, so folding them in would
// present a rule with keys the block cannot read at the point it runs.
func (v *describedView) AttachmentDeclaredLocalsKeys(node string, attachment string) []string {
	fr, ok := v.attachmentFeatureRecipe(node, attachment)
	if !ok {
		return nil
	}
	return fr.Spec.Locals.Keys()
}

// AttachmentAuthoredLocals returns the template text the FeatureRecipe one
// attachment binds on a node declares for one local, as written and before
// anything is evaluated.
//
// It is answered from the same FeatureRecipe the key order above is read from, so
// a key that order presents is a key this can answer.
func (v *describedView) AttachmentAuthoredLocals(node string, attachment string, key string) string {
	fr, ok := v.attachmentFeatureRecipe(node, attachment)
	if !ok {
		return ""
	}
	text, _ := fr.Spec.Locals.Get(key)
	return text
}

// BoundaryAttachmentDeclaredLocalsKeys returns the local names the FeatureRecipe
// one boundary-scope attachment binds declares, in author order.
//
// It is answered from the FeatureRecipe the render resolved for the attachment at
// the boundary it was declared on, which is the block that render evaluates.
func (v *describedView) BoundaryAttachmentDeclaredLocalsKeys(
	boundary string, attachment string,
) []string {
	fr, ok := v.boundaryAttachmentFeatureRecipe(boundary, attachment)
	if !ok {
		return nil
	}
	return fr.Spec.Locals.Keys()
}

// BoundaryAttachmentAuthoredLocals returns the template text the FeatureRecipe one
// boundary-scope attachment binds declares for one local, as written and before
// anything is evaluated.
//
// It is answered from the same FeatureRecipe the key order above is read from, so
// a key that order presents is a key this can answer.
func (v *describedView) BoundaryAttachmentAuthoredLocals(
	boundary string, attachment string, key string,
) string {
	fr, ok := v.boundaryAttachmentFeatureRecipe(boundary, attachment)
	if !ok {
		return ""
	}
	text, _ := fr.Spec.Locals.Get(key)
	return text
}

// BoundaryAttachmentExecutionScope returns the scope the FeatureRecipe one
// attachment declared on a boundary binds states its resource generation runs at,
// empty when it states none.
//
// It is answered from the FeatureRecipe the render resolved for the attachment,
// through the same binding the render dispatches it with — so a rule reading it
// is told which surface that dispatch will actually use.
func (v *describedView) BoundaryAttachmentExecutionScope(
	boundary string, attachment string,
) string {
	fr, ok := v.boundaryAttachmentFeatureRecipe(boundary, attachment)
	if !ok {
		return ""
	}
	return fr.Spec.ExecutionScope
}

// AuthoredNodeTemplateText returns the template text a node's own Recipe writes
// for one of its inline template fields, as written and before anything is
// evaluated.
//
// It is answered from the same enumeration the field axis is built from, so a
// field that axis presents is a field this can answer. Reading the field out of
// the spec by name here instead would be a second reading of which fields exist,
// and a field the two disagreed about would be enumerated with no text or carry
// text nothing was invoked for.
//
// A label the enumeration does not carry answers empty, which is the honest
// reading: the field is enumerated rather than looked up, so a label reaching
// here that the document does not write names nothing.
func (v *describedView) AuthoredNodeTemplateText(node string, field string) string {
	recipe, ok := v.recipes[node]
	if !ok || recipe == nil {
		return ""
	}
	for _, entry := range RecipeTemplateFields(recipe) {
		if entry.Label == field {
			return entry.Text
		}
	}
	return ""
}

// AttachmentAuthoredTemplateText returns the template text the FeatureRecipe one
// attachment binds on a node writes for one of its inline template fields, as
// written and before anything is evaluated.
//
// It is answered from the same enumeration the field axis is built from, so a
// field that axis presents is a field this can answer. Reading the field out of
// the spec by name here instead would be a second reading of which fields exist,
// and a field the two disagreed about would be enumerated with no text or carry
// text nothing was invoked for.
//
// A label the enumeration does not carry answers empty, which is the honest
// reading: the field is enumerated rather than looked up, so a label reaching
// here that the document does not write names nothing.
func (v *describedView) AttachmentAuthoredTemplateText(
	node string, attachment string, field string,
) string {
	fr, ok := v.attachmentFeatureRecipe(node, attachment)
	if !ok {
		return ""
	}
	for _, entry := range AttachmentTemplateFields(fr) {
		if entry.Label == field {
			return entry.Text
		}
	}
	return ""
}

// BoundaryAttachmentAuthoredTemplateText returns the template text the
// FeatureRecipe one attachment declared on a boundary writes for one of its
// inline template fields, as written and before anything is evaluated.
//
// It is answered from the same enumeration the field axis is built from, so a
// field that axis presents is a field this can answer. Reading the field out of
// the spec by name here instead would be a second reading of which fields exist,
// and a field the two disagreed about would be enumerated with no text or carry
// text nothing was invoked for.
//
// A label the enumeration does not carry answers empty, which is the honest
// reading: the field is enumerated rather than looked up, so a label reaching
// here that the document does not write names nothing.
func (v *describedView) BoundaryAttachmentAuthoredTemplateText(
	boundary string, attachment string, field string,
) string {
	fr, ok := v.boundaryAttachmentFeatureRecipe(boundary, attachment)
	if !ok {
		return ""
	}
	for _, entry := range BoundaryScopeFRTemplateFields(fr) {
		if entry.Label == field {
			return entry.Text
		}
	}
	return ""
}

// BoundaryAuthoredPathTemplateText returns the template text one boundary of a
// Release writes for one of its path fields, as written and before anything is
// evaluated.
//
// It is answered from the same enumeration the field axis is built from, so a
// field that axis presents is a field this can answer. Reading the field out of
// the boundary by name here instead would be a second reading of which fields
// exist, and a field the two disagreed about would be enumerated with no text or
// carry text nothing was invoked for.
//
// The boundary is located through the same walk the axis names scopes by, so a
// path that axis produced is a path this resolves. A field the enumeration does
// not carry answers empty, which is the honest reading: the field is enumerated
// rather than looked up, so a name reaching here that the document does not write
// names nothing.
func (v *describedView) BoundaryAuthoredPathTemplateText(boundary string, field string) string {
	b, held := boundaryAtPath(v.release, boundary)
	if !held {
		return ""
	}
	for _, entry := range BoundaryPathTemplateFields(b) {
		if entry.Field == field {
			return entry.Text
		}
	}
	return ""
}

// ArtifactsEntryImportReference returns the library reference one entry of a
// node's own artifacts block was authored with, as written and before anything
// resolves it.
//
// It answers for the bare-name library form alone. An entry written inline, one
// supplied by a file of the workspace, and one whose reference carries a registry
// host each answer empty: the first names no reference at all, the second names a
// path rather than a library, and the third is refused where it is consumed as a
// host-bearing reference rather than read as a library one. The three tests are
// made here rather than in each rule, so a rule over this fact is handed the one
// authoring form it governs.
//
// It reads the same field the artifacts walk resolves, through the same two
// predicates the walk dispatches its authoring modes on, so a reference this
// presents is the reference that walk will resolve.
func (v *describedView) ArtifactsEntryImportReference(node string, entry string) string {
	authored, held := v.artifactsEntry(node, entry)
	if !held || authored.HasOCIPrefix() || !authored.IsLibraryImport() {
		return ""
	}
	return authored.Import
}

// ArtifactsEntryLibraryDeclared reports whether the ArtifactLibrary one entry of
// a node's own artifacts block names is in the set the load resolved.
//
// An entry naming no library, and a reference that does not split into the
// bare-name shape, both report false: neither names a library the set could hold.
// A rule telling those apart from a library genuinely missing reads the reference
// fact beside this one.
func (v *describedView) ArtifactsEntryLibraryDeclared(node string, entry string) bool {
	return v.artifactsEntryLookup(node, entry).LibraryFound
}

// ArtifactsEntryBundleDeclared reports whether the ArtifactLibrary one entry of
// a node's own artifacts block names declares the bundle that entry asks for.
//
// It reports false wherever the library itself was not found, because a library
// the set does not hold declares nothing.
func (v *describedView) ArtifactsEntryBundleDeclared(node string, entry string) bool {
	return v.artifactsEntryLookup(node, entry).BundleFound
}

// ArtifactsEntryBundleFileNames returns the names of the files the bundle one
// entry of a node's own artifacts block imports holds, in the order that bundle
// declares them.
//
// An entry importing no bundle, and one whose library or bundle was not found,
// each return nothing: there is no bundle to read files out of.
func (v *describedView) ArtifactsEntryBundleFileNames(node string, entry string) []string {
	found := v.artifactsEntryLookup(node, entry)
	if !found.BundleFound {
		return nil
	}
	return found.Bundle.Files.Keys()
}

// ArtifactsEntryBundleNestedImportFiles returns the names of the files whose own
// body is a field-root import, inside the bundle one entry of a node's own
// artifacts block imports, in the order that bundle declares its files.
//
// It reads the bodies through the same predicate the consumption site refuses
// them by, so a file this names is a file that site would refuse and one it omits
// is one that site admits.
func (v *describedView) ArtifactsEntryBundleNestedImportFiles(node string, entry string) []string {
	found := v.artifactsEntryLookup(node, entry)
	if !found.BundleFound {
		return nil
	}
	return bundleNestedImportFiles(found.Bundle)
}

// RecipeValuesMappingImportReference returns the library reference a node's own
// Recipe supplies its values mapping by, as written and before anything resolves
// it.
//
// It answers for the values-mapping field alone and empty for every other field
// the coordinate names, so a rule reading it is handed the one field it governs.
//
// It answers for the bare-name library form alone. A values mapping written
// inline, one supplied by a file of the workspace, and one whose reference carries
// a registry host each answer empty: the first names no reference at all, the
// second names a path rather than a library, and the third is refused where it is
// consumed as a host-bearing reference rather than read as a library one. The
// three tests are made here rather than in each rule, so a rule over this fact is
// handed the one authoring form it governs.
//
// It reads the same field the render resolves, through the same two predicates
// that resolution dispatches its authoring modes on, so a reference this presents
// is the reference the render will resolve.
func (v *describedView) RecipeValuesMappingImportReference(node string, field string) string {
	authored, held := v.recipeValuesMappingImport(node, field)
	if !held {
		return ""
	}
	return authored.Import
}

// RecipeValuesMappingLibraryDeclared reports whether the MappingLibrary a node's
// own Recipe supplies its values mapping from is in the set the load resolved.
//
// A field naming no library, and a reference that does not split into the
// bare-name shape, both report false: neither names a library the set could hold.
// A rule telling those apart from a library genuinely missing reads the reference
// fact beside this one.
func (v *describedView) RecipeValuesMappingLibraryDeclared(node string, field string) bool {
	return v.recipeValuesMappingLookup(node, field).LibraryFound
}

// RecipeValuesMappingEntryDeclared reports whether the MappingLibrary a node's own
// Recipe supplies its values mapping from declares the entry that reference asks
// for.
//
// It reports false wherever the library itself was not found, because a library
// the set does not hold declares nothing.
func (v *describedView) RecipeValuesMappingEntryDeclared(node string, field string) bool {
	return v.recipeValuesMappingLookup(node, field).EntryFound
}

// recipeValuesMappingImport returns a node's own Recipe values mapping and whether
// that Recipe supplies it by a bare-name library import, for the one coordinate
// that names the values-mapping field.
//
// A coordinate naming any other field reports false, which is what leaves a rule
// over this axis declining the fields it does not govern rather than judging them.
// A node with no bound Recipe reports false too, which is the honest reading: the
// fields are enumerated rather than looked up, so a coordinate reaching here that
// the document does not write names nothing.
func (v *describedView) recipeValuesMappingImport(
	node string, field string,
) (types.ValuesMapping, bool) {
	if field != recipeFieldValuesMapping {
		return types.ValuesMapping{}, false
	}
	recipe, ok := v.recipes[node]
	if !ok || recipe == nil {
		return types.ValuesMapping{}, false
	}
	authored := recipe.Spec.ValuesMapping
	if authored.HasOCIPrefix() || !authored.IsLibraryImport() {
		return types.ValuesMapping{}, false
	}
	return authored, true
}

// recipeValuesMappingLookup resolves a node's own values-mapping import reference
// against the resolved MappingLibrary set, through the single reading the render
// resolves that import through.
//
// It is one helper under two accessors rather than two resolutions, and the reason
// is not brevity: the two facts are two steps of one cascade, so two resolutions
// would be two readings of one reference, each free to answer for a different step
// of a different reading. The zero value is what a field naming no library resolves
// to, and every step of it reports the absence its own accessor reports.
func (v *describedView) recipeValuesMappingLookup(node string, field string) mappingLibraryLookup {
	authored, held := v.recipeValuesMappingImport(node, field)
	if !held {
		return mappingLibraryLookup{}
	}
	return lookUpMappingLibraryEntry(authored.Import, v.mappingLibraries)
}

// RecipeWorkloadImportReference returns the library reference a node's own Recipe
// supplies its workload by, as written and before anything resolves it.
//
// It answers for the workload field alone and empty for every other field the
// coordinate names, so a rule reading it is handed the one field it governs.
//
// It answers for the bare-name library form alone. A workload written inline, one
// supplied by a file of the workspace, and one whose reference carries a registry
// host each answer empty: the first names no reference at all, the second names a
// path rather than a library, and the third is refused where it is consumed as a
// host-bearing reference rather than read as a library one. The three tests are made
// here rather than in each rule, so a rule over this fact is handed the one authoring
// form it governs.
//
// It reads the same field the render resolves, through the same two predicates that
// resolution dispatches its authoring modes on, so a reference this presents is the
// reference the render will resolve.
func (v *describedView) RecipeWorkloadImportReference(node string, field string) string {
	authored, held := v.recipeWorkloadImport(node, field)
	if !held {
		return ""
	}
	return authored.Import
}

// RecipeWorkloadLibraryDeclared reports whether the WorkloadLibrary a node's own
// Recipe supplies its workload from is in the set the load resolved.
//
// A field naming no library, and a reference that does not split into the bare-name
// shape, both report false: neither names a library the set could hold. A rule
// telling those apart from a library genuinely missing reads the reference fact
// beside this one.
func (v *describedView) RecipeWorkloadLibraryDeclared(node string, field string) bool {
	return v.recipeWorkloadLookup(node, field).LibraryFound
}

// RecipeWorkloadEntryDeclared reports whether the WorkloadLibrary a node's own
// Recipe supplies its workload from declares the entry that reference asks for.
//
// It reports false wherever the library itself was not found, because a library the
// set does not hold declares nothing.
func (v *describedView) RecipeWorkloadEntryDeclared(node string, field string) bool {
	return v.recipeWorkloadLookup(node, field).EntryFound
}

// recipeWorkloadImport returns a node's own Recipe workload and whether that Recipe
// supplies it by a bare-name library import, for the one coordinate that names the
// workload field.
//
// A coordinate naming any other field reports false, which is what leaves a rule over
// this axis declining the fields it does not govern rather than judging them. A node
// with no bound Recipe reports false too, which is the honest reading: the fields are
// enumerated rather than looked up, so a coordinate reaching here that the document
// does not write names nothing.
func (v *describedView) recipeWorkloadImport(
	node string, field string,
) (types.WorkloadTemplate, bool) {
	if field != recipeFieldWorkload {
		return types.WorkloadTemplate{}, false
	}
	recipe, ok := v.recipes[node]
	if !ok || recipe == nil {
		return types.WorkloadTemplate{}, false
	}
	authored := recipe.Spec.Workload
	if authored.HasOCIPrefix() || !authored.IsLibraryImport() {
		return types.WorkloadTemplate{}, false
	}
	return authored, true
}

// recipeWorkloadLookup resolves a node's own workload import reference against the
// resolved WorkloadLibrary set, through the single reading the render resolves that
// import through.
//
// It is one helper under two accessors rather than two resolutions, and the reason is
// not brevity: the two facts are two steps of one cascade, so two resolutions would be
// two readings of one reference, each free to answer for a different step of a
// different reading. The zero value is what a field naming no library resolves to, and
// every step of it reports the absence its own accessor reports.
func (v *describedView) recipeWorkloadLookup(node string, field string) workloadLibraryLookup {
	authored, held := v.recipeWorkloadImport(node, field)
	if !held {
		return workloadLibraryLookup{}
	}
	return lookUpWorkloadLibraryEntry(authored.Import, v.workloadLibraries)
}

// artifactsEntry returns one entry of a node's own Recipe artifacts block, and
// whether the Recipe writes that key.
//
// A node with no bound Recipe and a key the Recipe does not declare report false,
// which is the honest reading: the keys are enumerated rather than looked up, so
// a key reaching here that the document does not write names nothing.
func (v *describedView) artifactsEntry(node string, entry string) (types.ArtifactEntry, bool) {
	recipe, ok := v.recipes[node]
	if !ok || recipe == nil {
		return types.ArtifactEntry{}, false
	}
	return recipe.Spec.Artifacts.Get(entry)
}

// artifactsEntryLookup resolves one entry's library reference against the
// resolved ArtifactLibrary set, through the single reading the artifacts walk
// expands a bundle through.
//
// It is one helper under four accessors rather than four resolutions, and the
// reason is not brevity: the four facts are four steps of one cascade, so four
// resolutions would be four readings of one reference, each free to answer for a
// different step of a different reading. The zero value is what an entry naming
// no library resolves to, and every step of it reports the absence its own
// accessor reports.
func (v *describedView) artifactsEntryLookup(node string, entry string) artifactLibraryLookup {
	authored, held := v.artifactsEntry(node, entry)
	if !held || authored.HasOCIPrefix() || !authored.IsLibraryImport() {
		return artifactLibraryLookup{}
	}
	return lookUpArtifactLibraryBundle(authored.Import, v.artifactLibraries)
}

// ProviderArtifactTemplateDelimiters returns the delimiter pair the provider
// rendering a node's documents declares its artifact entries are read with.
//
// A provider declaring no pair is answered with the usual pair rather than with
// an absence, and so is a node whose provider cannot be resolved: what a rule
// needs is the pair an entry will actually be read with, and the render applies
// that same fallback. The value is read through the same emitter interface the
// artifact evaluation reads it through, so the presented pair is the one the
// render applies.
func (v *describedView) ProviderArtifactTemplateDelimiters(
	node string,
) constraint.ArtifactTemplateDelimiters {
	return declaredArtifactDelimiters(v.nodeEngine(node))
}

// AttachmentProviderArtifactTemplateDelimiters returns the delimiter pair the
// provider rendering one attachment's documents on a node declares its artifact
// entries are read with.
//
// It is a separate answer from the pair the node's own provider declares, and
// that separation is what makes it correct rather than approximate. The
// attachment binds its own FeatureRecipe, which declares its own capability, so
// the provider reading the attachment's entries is independently authored from
// the one reading the node's — and an entry read with the wrong pair has its
// downstream target syntax surface as references it does not make.
//
// An attachment with no bound FeatureRecipe is answered with the usual pair, on
// the reading the node-scoped form carries: the fallback is made here once
// rather than in every rule.
func (v *describedView) AttachmentProviderArtifactTemplateDelimiters(
	node string, attachment string,
) constraint.ArtifactTemplateDelimiters {
	return declaredArtifactDelimiters(v.attachmentEngine(node, attachment))
}

// BoundaryAttachmentProviderArtifactTemplateDelimiters returns the delimiter
// pair the provider rendering one boundary-scope attachment's documents declares
// its artifact entries are read with.
//
// It is rooted at the boundary because that is where such an attachment is
// declared: it stands against no single component, so neither a node's own pair
// nor the pair of an attachment on some component is the pair its entries are
// read with.
func (v *describedView) BoundaryAttachmentProviderArtifactTemplateDelimiters(
	boundary string, attachment string,
) constraint.ArtifactTemplateDelimiters {
	return declaredArtifactDelimiters(v.boundaryAttachmentEngine(boundary, attachment))
}

// declaredArtifactDelimiters is the pair one engine declares its artifact
// entries are read with, and is the one place the fallback every form of that
// fact shares is applied.
//
// It takes the outcome of an engine resolution rather than resolving one itself,
// because the three forms of the fact resolve their engine from three different
// bindings — a node's own Recipe, and the FeatureRecipe an attachment binds at
// either of its two owners — while the reading of the resolved engine and the
// fallback are the same for all three. A copy of that reading per accessor would
// be free to answer a differently-shaped absence.
//
// An unresolved engine and one that places no artifacts both answer the usual
// pair, which is the pair the render applies in both situations.
func declaredArtifactDelimiters(engine Provider, resolved bool) constraint.ArtifactTemplateDelimiters {
	usual := constraint.ArtifactTemplateDelimiters{Left: "{{", Right: "}}"}
	if !resolved {
		return usual
	}
	emitter, ok := engine.(ArtifactsEmitter)
	if !ok {
		return usual
	}
	left, right := emitter.ArtifactTemplateDelimiters()
	return constraint.ArtifactTemplateDelimiters{Left: left, Right: right}
}

// nodePipeline returns the steps of a node's own Recipe pipeline, in pipeline
// order. A node with no bound Recipe carries none.
func (v *describedView) nodePipeline(node string) []types.PipelineStep {
	recipe, ok := v.recipes[node]
	if !ok || recipe == nil {
		return nil
	}
	return recipe.Spec.Pipeline
}

// attachmentPipeline returns the steps of the pipeline one attachment's
// FeatureRecipe carries on a node, in pipeline order.
func (v *describedView) attachmentPipeline(node string, attachment string) []types.PipelineStep {
	fr, ok := v.attachmentFeatureRecipe(node, attachment)
	if !ok {
		return nil
	}
	return fr.Spec.Pipeline
}

// boundaryAttachmentPipeline returns the steps of the pipeline one
// boundary-scope attachment's FeatureRecipe carries, in pipeline order.
func (v *describedView) boundaryAttachmentPipeline(
	boundary string, attachment string,
) []types.PipelineStep {
	fr, ok := v.boundaryAttachmentFeatureRecipe(boundary, attachment)
	if !ok {
		return nil
	}
	return fr.Spec.Pipeline
}

// attachmentFeatureRecipe resolves the FeatureRecipe one attachment binds on a
// node, through the same lookup and the same instance-plus-type key the render
// resolves attachments with.
func (v *describedView) attachmentFeatureRecipe(
	node string, attachment string,
) (*types.FeatureRecipe, bool) {
	if v.recipeLookup.FeatureRecipe == nil {
		return nil, false
	}
	entries, ok := v.graph.Spec.FeatureValuesMapping.Get(node)
	if !ok {
		return nil, false
	}
	entry, ok := entries.Get(attachment)
	if !ok {
		return nil, false
	}
	fr, found := v.recipeLookup.FeatureRecipe(attachment, entry.Type)
	if !found || fr == nil {
		return nil, false
	}
	return fr, true
}

// boundaryAttachmentFeatureRecipe resolves the FeatureRecipe one boundary-scope
// attachment binds, through the same lookup the render resolves them with.
func (v *describedView) boundaryAttachmentFeatureRecipe(
	boundary string, attachment string,
) (*types.FeatureRecipe, bool) {
	if v.recipeLookup.FeatureRecipe == nil {
		return nil, false
	}
	feats, ok := v.graph.Spec.BoundaryFeatures.Get(boundary)
	if !ok {
		return nil, false
	}
	feat, ok := feats.Get(attachment)
	if !ok {
		return nil, false
	}
	fr, found := v.recipeLookup.FeatureRecipe(attachment, feat.Type)
	if !found || fr == nil {
		return nil, false
	}
	return fr, true
}

// stepDescriptor returns the retained self-report of the plugin the named step
// runs, and whether one was obtained.
//
// It reads through the render's shared retention, so a plugin already described
// for a step that ran crosses to no guest here.
//
// Three situations report false and they are not the same situation, which is
// why only one of them is recorded:
//
//   - A render wired with no plugin resolution reports false for every step.
//     That configuration validates each pipeline without running it, so no plugin
//     is resolved anywhere in the build and there is no self-report to present.
//   - A step the pipeline does not declare reports false because there is no
//     plugin to ask about at all.
//   - A plugin the host could not resolve, load or describe reports false having
//     been asked and having failed, and that failure is recorded on the
//     presentation. It is the one arm where something went wrong rather than
//     there being nothing to say, and the record is what lets the caller halt on
//     the failure instead of on a rule's verdict over the zero value returned
//     here.
func (v *describedView) stepDescriptor(
	steps []types.PipelineStep, step string,
) (wasm.PluginDescriptor, bool) {
	if v.lookup == nil {
		return wasm.PluginDescriptor{}, false
	}
	entry, ok := pipelineStepByID(steps, step)
	if !ok {
		return wasm.PluginDescriptor{}, false
	}
	desc, err := v.describe(v.ctx, entry)
	if err != nil {
		v.refusals.record(entry, err)
		return wasm.PluginDescriptor{}, false
	}
	return desc, true
}

// selfReportRefusal returns the first plugin self-report this presentation was
// asked for and could not obtain, nil when every read it received was answered.
//
// It is unexported and stands outside the fact contract because it is not a
// fact: no author declared it, no rule reads it, and it says what the host could
// not do rather than what a document says. Its one reader is the site that
// invokes the rules over this presentation, which is the only place that can
// halt on the failure rather than on a verdict reached without the declaration
// the failure withheld.
func (v *describedView) selfReportRefusal() error { return v.refusals.refusal() }

// selfReportRefusals records the plugin self-reports a presentation was asked
// for and could not obtain.
//
// It keeps the FIRST one alone. The first is the earliest in the traversal order
// the rules are invoked in, which is fixed for a given input, so the halt a
// build produces for one broken plugin is fixed too. Keeping the whole set would
// report one unreachable plugin once per step that names it, where there is one
// thing wrong and one thing to repair.
//
// Reads are serialised for the same reason the retention beneath them is: two
// readers of one plugin may race on a cold read, and the record must name one of
// them rather than tear between both.
type selfReportRefusals struct {
	mu    sync.Mutex
	first error
}

// record notes that the plugin the given step names was asked what it declares
// and did not answer. A later refusal is dropped rather than replacing the one
// held, so the value a caller reads does not depend on how far a walk got after
// the first failure.
func (r *selfReportRefusals) record(step types.PipelineStep, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.first != nil {
		return
	}
	r.first = selfReportUnreadableErr(step, err)
}

// refusal returns the recorded failure, nil when nothing was refused.
func (r *selfReportRefusals) refusal() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.first
}

// selfReportUnreadableErr is the Loud Guardrail for a pipeline step whose plugin
// the host could not ask what it declares. A plugin's `describe()` self-report
// is what states which providers its step is compatible with and what each of
// its exports operates on, so a step whose plugin cannot be asked can be neither
// admitted nor refused on any of it — and reporting it as a plugin that declared
// nothing would name the wrong fault and hide the real one.
//
// It names the step and the plugin, and carries the host's own failure beneath
// it: the resolution, load or `describe()` error is the cause and is what the
// author acts on. It registers no condition of its own, because the cause
// already carries one and a second name for one failure is a second thing to
// keep in step.
//
// It names no component, and that is the honest position rather than a missing
// one. A plugin's self-report is a pure function of its bytes and is read once
// per plugin for the whole render, so a plugin the host cannot ask is unaskable
// for every step and every component that names it — naming one of them would
// present a build-wide fault as one component's, and it would name whichever
// component the walk happened to reach first.
func selfReportUnreadableErr(step types.PipelineStep, err error) error {
	return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{},
		fmt.Sprintf("pipeline step %q: plugin %q did not report what it declares, so whether the step may run cannot be decided",
			step.ID, step.Plugin))
}

// scopeOf returns the declared scope of the export the named step dispatches to.
// It reads the scope through the same find-an-export-by-name home the pipeline
// seam shares, so the presented scope is the one the executor routes on.
func (v *describedView) scopeOf(steps []types.PipelineStep, step string) string {
	entry, ok := pipelineStepByID(steps, step)
	if !ok {
		return ""
	}
	desc, ok := v.stepDescriptor(steps, step)
	if !ok {
		return ""
	}
	return exportScope(desc, entry.Function)
}

// nodeEngine resolves the engine that renders a node's own documents, which
// carries that provider's retained self-report. A node with no bound Recipe, and
// a binding the factory cannot resolve, both report false.
func (v *describedView) nodeEngine(node string) (Provider, bool) {
	recipe, ok := v.recipes[node]
	if !ok || recipe == nil {
		return nil, false
	}
	return v.capabilityEngine(recipe.Spec.Capability)
}

// attachmentEngine resolves the engine that renders one attachment's own
// documents on a node — the engine behind the capability its bound FeatureRecipe
// declares, which is a different document from the node's own Recipe and may
// therefore name a different provider.
//
// An attachment with no bound FeatureRecipe reports false, which is the same
// report a binding the factory cannot resolve gives.
func (v *describedView) attachmentEngine(node string, attachment string) (Provider, bool) {
	fr, ok := v.attachmentFeatureRecipe(node, attachment)
	if !ok {
		return nil, false
	}
	return v.capabilityEngine(fr.Spec.Capability)
}

// boundaryAttachmentEngine resolves the engine that renders one boundary-scope
// attachment's own documents, behind the capability its bound FeatureRecipe
// declares.
func (v *describedView) boundaryAttachmentEngine(
	boundary string, attachment string,
) (Provider, bool) {
	fr, ok := v.boundaryAttachmentFeatureRecipe(boundary, attachment)
	if !ok {
		return nil, false
	}
	return v.capabilityEngine(fr.Spec.Capability)
}

// capabilityEngine resolves the engine behind one declared capability binding,
// which carries that provider's retained self-report.
//
// It is the one resolution the provider facts reach an engine through, whichever
// entity declared the binding: a node's Recipe and the FeatureRecipe an
// attachment binds are different documents naming capabilities the same way, and
// a second resolution beside this one would be free to resolve a binding
// differently for the same string.
//
// The factory receives the full binding, version pin included, exactly as the
// render's own engine resolution does: the pin selects which provider-rendering
// plugin version answers, and that is the plugin whose self-report this stage's
// provider facts report.
func (v *describedView) capabilityEngine(capability string) (Provider, bool) {
	return resolveCapabilityEngine(v.engineFactory, capability)
}

// resolveCapabilityEngine is the resolution [describedView.capabilityEngine]
// presents, over a factory rather than over a presentation, so a reading made
// where the render holds the factory itself resolves a binding exactly as every
// provider fact does. A nil factory, a binding it cannot resolve and a nil engine
// all report false.
func resolveCapabilityEngine(factory EngineFactory, capability string) (Provider, bool) {
	if factory == nil {
		return nil, false
	}
	engine, err := factory(capability)
	if err != nil || engine == nil {
		return nil, false
	}
	return engine, true
}

// pipelineStepByID returns the step declaring the given identifier, and whether
// one was found. A pipeline is an ordered list whose entries carry an
// identifier, so the walk is over that list rather than over a map — the answer
// is the same on every run of identical input.
func pipelineStepByID(steps []types.PipelineStep, step string) (types.PipelineStep, bool) {
	for _, s := range steps {
		if s.ID == step {
			return s, true
		}
	}
	return types.PipelineStep{}, false
}

var _ constraint.DescribedView = (*describedView)(nil)

// The presentation is held to the record its caller reads, so an accessor that
// stops recording what it could not answer fails to compile here rather than
// leaving the caller reading an absence it cannot tell from a clean read.
var _ selfReportRefusalReporter = (*describedView)(nil)

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// describedView presents, as named facts, what every plugin a node reaches has
// reported about itself: the provider capabilities each pipeline step's plugin
// declares its step is compatible with, the scope and the declared input and
// output contracts of the export each step dispatches to, the provider that
// renders each site's own documents, the assembly claim and artifact delimiter
// pair that provider declares, and the text a Recipe authored for each provides
// key.
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
// # Every plugin fact is read through the retention the render shares
//
// A step plugin's self-report is a pure function of its bytes, so two readers of
// one plugin cannot legitimately be told different things. The accessors below
// therefore read it through the same retention the pipeline executor reads — one
// accessor, one value per plugin, for the whole render — rather than describing a
// plugin a second time for this presentation. Nothing is described until an
// accessor asks, so presenting this stage loads no plugin on its own.
//
// The provider facts read the provider engine's own retained self-report through
// the same optional interfaces the render path reads them through, for the same
// reason.
//
// # An unloadable plugin answers as one that declared nothing
//
// A fact accessor returns a value and never an error, so a plugin the host cannot
// resolve, load or describe is answered with the zero value of the fact asked
// for. That is indistinguishable from a plugin that declared nothing, and
// deliberately so at this layer: the load failure itself is not this stage's
// finding, and it is reported in full by the path that loads the guest in order
// to run it, which reaches every plugin a build actually uses.
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

	// recipeLookup resolves the FeatureRecipe an attachment binds. It is the
	// same lookup the render resolves attachments through, so a fact about an
	// attachment names the document the render actually bound.
	recipeLookup RecipeLookup

	// descriptors is the render's retention of step-plugin self-reports, shared
	// with the pipeline executor.
	descriptors *stepDescriptorMemo

	// lookup resolves a plugin URI to its verified bytes. The retention takes it
	// per read rather than holding one, so it is supplied here.
	lookup pluginpath.Lookup

	// engineFactory resolves a provider binding to the engine that renders it,
	// which carries that provider's own retained self-report.
	engineFactory EngineFactory
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
func newDescribedView(
	ctx context.Context,
	graph *types.IRGraph,
	earlier constraint.GraphView,
	recipes map[string]*types.Recipe,
	recipeLookup RecipeLookup,
	descriptors *stepDescriptorMemo,
	lookup pluginpath.Lookup,
	engineFactory EngineFactory,
) constraint.DescribedView {
	if graph == nil || earlier == nil || descriptors == nil {
		return nil
	}
	return &describedView{
		GraphView:     earlier,
		ctx:           ctx,
		graph:         graph,
		recipes:       recipes,
		recipeLookup:  recipeLookup,
		descriptors:   descriptors,
		lookup:        lookup,
		engineFactory: engineFactory,
	}
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

// StepExportInputSchema returns the schema document the export one step of a
// node's own Recipe pipeline dispatches to declares for the step-local input a
// pipeline entry supplies, empty when the export declares no input contract.
func (v *describedView) StepExportInputSchema(node string, step string) []byte {
	return v.exportInput(v.nodePipeline(node), step)
}

// StepExportOutputSchema returns the schema document the export one step of a
// node's own Recipe pipeline dispatches to declares for the outputs it returns,
// empty when the export declares no output contract.
func (v *describedView) StepExportOutputSchema(node string, step string) []byte {
	return v.exportOutput(v.nodePipeline(node), step)
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

// BoundaryAttachmentRenderingProvider returns the provider that renders one
// boundary-scope attachment's documents, as the engine kind its bound
// FeatureRecipe's declared capability projects to.
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
// capabilities the plugin behind one step of a boundary-scope attachment's
// pipeline declares its step is compatible with, in declared order.
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
// step of a boundary-scope attachment's pipeline dispatches to declares it
// operates on, empty when the export declares none.
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
	usual := constraint.ArtifactTemplateDelimiters{Left: "{{", Right: "}}"}
	engine, ok := v.nodeEngine(node)
	if !ok {
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
// runs, and whether one was obtained. A step the pipeline does not declare, and
// a plugin that cannot be resolved, loaded or described, both report false.
//
// It reads through the render's shared retention, so a plugin already described
// for a step that ran crosses to no guest here.
//
// A render wired with no plugin resolution reports false for every step. That
// configuration validates each pipeline without running it, so no plugin is
// resolved anywhere in the build and there is no self-report to present — the
// same answer an unloadable plugin gives, for the same reason.
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
	desc, err := v.descriptors.descriptor(v.ctx, entry.Plugin, v.lookup)
	if err != nil {
		return wasm.PluginDescriptor{}, false
	}
	return desc, true
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

// exportInput returns the declared input schema of the export the named step
// dispatches to, empty when the export declares none.
func (v *describedView) exportInput(steps []types.PipelineStep, step string) []byte {
	export, ok := v.stepExport(steps, step)
	if !ok {
		return nil
	}
	return export.Input
}

// exportOutput returns the declared output schema of the export the named step
// dispatches to, empty when the export declares none.
func (v *describedView) exportOutput(steps []types.PipelineStep, step string) []byte {
	export, ok := v.stepExport(steps, step)
	if !ok {
		return nil
	}
	return export.Output
}

// stepExport returns the export the named step dispatches to, through the
// shared find-an-export-by-name home.
func (v *describedView) stepExport(
	steps []types.PipelineStep, step string,
) (wasm.PluginExport, bool) {
	entry, ok := pipelineStepByID(steps, step)
	if !ok {
		return wasm.PluginExport{}, false
	}
	desc, ok := v.stepDescriptor(steps, step)
	if !ok {
		return wasm.PluginExport{}, false
	}
	return findExport(desc, entry.Function)
}

// nodeEngine resolves the engine that renders a node's own documents, which
// carries that provider's retained self-report. A node with no bound Recipe, and
// a binding the factory cannot resolve, both report false.
func (v *describedView) nodeEngine(node string) (Provider, bool) {
	if v.engineFactory == nil {
		return nil, false
	}
	recipe, ok := v.recipes[node]
	if !ok || recipe == nil {
		return nil, false
	}
	// The factory receives the full binding, version pin included, exactly as
	// the render's own engine resolution does: the pin selects which provider-
	// rendering plugin version answers, and that is the plugin whose self-report
	// this stage's provider facts report.
	engine, err := v.engineFactory(recipe.Spec.Capability)
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

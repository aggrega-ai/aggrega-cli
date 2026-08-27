// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
// text a Recipe authored for each provides key.
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

	// recipeLookup resolves the FeatureRecipe an attachment binds. It is the
	// same lookup the render resolves attachments through, so a fact about an
	// attachment names the document the render actually bound.
	recipeLookup RecipeLookup

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
		describe:      stepDescriberOver(descriptors, lookup),
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
// either of its two carriers — while the reading of the resolved engine and the
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
	if v.engineFactory == nil {
		return nil, false
	}
	engine, err := v.engineFactory(capability)
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

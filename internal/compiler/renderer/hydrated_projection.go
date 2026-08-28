// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The render's per-node working state is a loop-local: it is built, used to
// render one node, and dropped when that node's turn ends. Anything asking a
// question about the state a node reached before its artifact bytes existed has
// nothing left to read by the time the render finishes.
//
// What survives here is a PROJECTION of that state and never the state itself.
// The distinction is what bounds the cost: the whole per-node state grows with
// everything a node accumulates, while a projection carries one field per
// question the framework declares at this point in the compilation — a fixed and
// small set, so the retention grows with the declared questions rather than with
// the build.
//
// The field names below are the correspondence, and they are checked rather than
// trusted: each names the accessor that reads it, and a guard beside this file
// compares the two sets in both directions. A question declared here later
// therefore cannot leave the projection behind silently — the guard names the
// field nobody added.

// valuesMappingRefs is the reference set a node's inline values-mapping template
// reads, together with whether that node's mapping is authored inline at all.
//
// The second half is load-bearing and is why this is one value rather than a
// bare slice. A mapping authored as an import carries no inline text, so its
// reference set is ABSENT rather than empty, and a reader unable to tell the two
// apart would report every value unread for exactly those nodes whose mapping it
// never read.
type valuesMappingRefs struct {
	// Paths are the context paths the inline template reads, sorted. A template
	// has no single reading order to preserve, so sorting is what makes the value
	// the same on every run.
	Paths []string
	// Inline reports whether the node's mapping is authored inline.
	Inline bool
}

// hydratedProjection is what one node's mid-render state is reduced to once that
// state is complete: the node's inline values-mapping reference set, the resolved
// source, the parsed workload, its attributed deliveries, and each artifact
// entry's template text before anything evaluates it.
//
// Every field is a value already read out of the state. None of them is a
// carrier the state can be walked back out of, which is the property that keeps
// this a projection: a reader holding one of these can answer the declared
// questions and can reach nothing else.
type hydratedProjection struct {
	// ValuesMappingTemplateRefs is the node's inline values-mapping reference
	// set, and whether the mapping is authored inline.
	ValuesMappingTemplateRefs valuesMappingRefs

	// ResolvedNodeSourceDeclared reports whether the node's Recipe carries a
	// source once source resolution has run.
	//
	// The value recorded is the RESOLVED source rather than the text the author
	// wrote: source resolution replaces the node's Recipe with a local copy whose
	// source holds the evaluated result, and that copy is what everything
	// downstream reads. So a source template evaluating to an empty string is
	// recorded the same as an absent source, which is one condition rather than
	// two.
	ResolvedNodeSourceDeclared bool

	// ParsedNodeWorkloadPresent reports whether the node's render state carries a
	// workload. A Recipe declaring no workload body reaches artifact evaluation
	// with none, because workload synthesis had nothing to parse.
	ParsedNodeWorkloadPresent bool

	// NodeInjectDeliveries are the deliveries the node's injection pipe carries
	// once every attachment attached to it has delivered, in delivery order,
	// each naming whatever attachment asked for it and whether a value arrived.
	//
	// It is recorded where the attachment deliveries are made rather than beside
	// the fields around it, because that is the only point at which both halves
	// are in hand: the pipe carries the component's own deliveries and its
	// connections' by then, and the attachments' claims are being made right
	// there. Read back afterwards the pipe would answer what it holds and
	// nothing about who put it there, and a claim that delivered nothing would
	// have left no trace at all.
	NodeInjectDeliveries []constraint.InjectDelivery

	// ArtifactTemplateText is the template text standing at each of the node's
	// artifacts, keyed by the output name the entry resolves to and held in the
	// order the entries were walked.
	//
	// The text is the entry's content whichever form the entry took — written
	// inline, supplied by an imported file, or supplied by an entry of a library
	// bundle — because it is recorded where that content is resolved rather than
	// where the entry is authored. Recording it at the authoring site would hand
	// back an import directive for two of the three forms, and resolving it a
	// second time somewhere else would read the file twice and could answer
	// differently each time.
	ArtifactTemplateText types.OrderedMap[string]

	// ArtifactRecordedContentPin is the content digest the node's Recipe recorded
	// for each of its artifacts, keyed by the output name the entry resolves to.
	//
	// An entry is present only where the walk resolved a form that carries a pin —
	// content supplied by an imported file — so presence in this map is the whole
	// of what says a comparison is owed. That gate sits at the recording site
	// rather than in whatever reads this, because the walk is the only place that
	// knows which form it resolved: an inline entry is pinned by whatever pins the
	// entity, and a bundle-supplied one by the version the bundle resolved at.
	ArtifactRecordedContentPin types.OrderedMap[string]
}

// An artifacts block is authored on the entity that carries it, and three
// entities carry one: a component's Recipe, the FeatureRecipe an attachment binds
// on a node, and the FeatureRecipe an operator attaches at a boundary. The same
// holds for the source and the workload a render reads its pillars from. So the
// state one render reaches is retained under the CARRIER it was authored
// against, and the three carriers are three key spaces rather than one.
//
// The separation is not filing. An attachment's render runs under the host
// component's node identity over documents the FeatureRecipe declares, so a
// single node-keyed space would answer the host's pillars for the attachment's
// templates — admitting a reference to a pillar the attachment does not carry
// wherever the host carries it, and refusing a legitimate one wherever the host
// does not. Both directions are wrong and neither is visible from the outcome.
//
// The two attachment carriers are held in separate maps under separate key types
// rather than in one map under a carrier discriminator. A boundary-rooted
// identity is then unable to be written into a node-rooted key at all, which is a
// property of the types rather than of a discriminator being set correctly at
// every write.

// nodeAttachmentKey identifies the render of one attachment on one node. Both
// coordinates are required: two attachments on one node are independently
// authored, and nothing makes one's output names distinct from the other's.
type nodeAttachmentKey struct {
	node       string
	attachment string
}

// boundaryAttachmentKey identifies the render of one boundary-scope attachment.
//
// It is rooted at the boundary because that is where such an attachment is
// declared — it is written once and reaches every component the boundary holds,
// so a component-rooted key would hold one authored document under as many keys
// as the boundary has members, and none at all for a boundary holding none.
//
// The boundary is its scope path rather than a bare name, because boundaries nest
// and two scopes at different depths may carry one name.
type boundaryAttachmentKey struct {
	boundary   string
	attachment string
}

// nodeAttachmentProjection is what the mid-render state of one attachment's
// render on a node is reduced to once that state is complete.
//
// The field names are the correspondence with the accessors that read them, on
// the reading the per-node projection above follows, and the guard beside this
// file compares the two sets in both directions.
type nodeAttachmentProjection struct {
	// AttachmentResolvedSourceDeclared reports whether the FeatureRecipe the
	// attachment binds carries a source once source resolution has run.
	//
	// The value recorded is the RESOLVED source: the attachment's render is built
	// around a Recipe carrying the resolved value, so a source template evaluating
	// to an empty string is recorded the same as an absent source.
	AttachmentResolvedSourceDeclared bool

	// AttachmentParsedWorkloadPresent reports whether the attachment's render
	// state carries a workload. A FeatureRecipe declaring no workload body
	// reaches artifact evaluation with none, because workload synthesis had
	// nothing to parse.
	AttachmentParsedWorkloadPresent bool

	// AttachmentArtifactTemplateText is the template text standing at each of the
	// attachment's artifacts, keyed by the output name the entry resolves to and
	// held in the order the entries were walked.
	AttachmentArtifactTemplateText types.OrderedMap[string]

	// AttachmentArtifactRecordedContentPin is the content digest the
	// FeatureRecipe recorded for each of the attachment's artifacts, keyed by the
	// output name the entry resolves to.
	//
	// An entry is present only where the walk resolved a form that carries a pin,
	// on the reading the node's own field states.
	AttachmentArtifactRecordedContentPin types.OrderedMap[string]

	// AttachmentInjectMappingMechanisms pairs each mechanism the host component's
	// own injection block declares with whether this attachment's rendered
	// injectMapping output named it, in the order the component declared them.
	//
	// It is recorded where that mapping is evaluated rather than beside the
	// fields around it, because that is the only point at which both halves are
	// in hand: the component's declarations are read once for the node, and the
	// mechanisms the output names exist only in the parsed overlay, which the
	// merge folds into a pipe that remembers no mapping.
	AttachmentInjectMappingMechanisms []constraint.InjectMappingMechanism
}

// boundaryAttachmentProjection is what the mid-render state of one boundary-scope
// attachment's render is reduced to once that state is complete.
type boundaryAttachmentProjection struct {
	// BoundaryAttachmentResolvedSourceDeclared reports whether the FeatureRecipe
	// the boundary-scope attachment binds carries a source once source resolution
	// has run, read as the resolved value for the reason its node-carried sibling
	// is.
	BoundaryAttachmentResolvedSourceDeclared bool

	// BoundaryAttachmentParsedWorkloadPresent reports whether the boundary-scope
	// attachment's render state carries a workload.
	BoundaryAttachmentParsedWorkloadPresent bool

	// BoundaryAttachmentArtifactTemplateText is the template text standing at
	// each of the boundary-scope attachment's artifacts, keyed by the output name
	// the entry resolves to and held in the order the entries were walked.
	BoundaryAttachmentArtifactTemplateText types.OrderedMap[string]

	// BoundaryAttachmentArtifactRecordedContentPin is the content digest the
	// FeatureRecipe recorded for each of the boundary-scope attachment's
	// artifacts, keyed by the output name the entry resolves to.
	//
	// An entry is present only where the walk resolved a form that carries a pin,
	// on the reading the node's own field states.
	BoundaryAttachmentArtifactRecordedContentPin types.OrderedMap[string]
}

// hydratedProjections is one render's retained set of those projections, keyed
// by node and, for what an attachment's render reaches, by that attachment's
// carrier.
//
// It is installed once per render and lives for the whole of it. The per-node
// projections are written as each node's state completes, the per-attachment ones
// as each attachment's render completes; the resolved provides are not written at
// all, because the render already holds them.
type hydratedProjections struct {
	// ResolvedNodeProvides is the render's own resolved-provides bag, held here
	// by reference rather than copied per node.
	//
	// The bag is evaluated once for the whole render before any node is rendered
	// and is not written to afterwards, so a second copy would carry no
	// information the original does not and would grow with the node count. What
	// is retained here is the one map.
	ResolvedNodeProvides NodeProvides

	// mu serialises writes. The node walk is sequential today, so the lock is
	// never contended; it is here because a map written from two goroutines is a
	// crash rather than a wrong answer, and the walk's sequence is not a property
	// this value can assert about its callers.
	mu                   sync.Mutex
	byNode               map[string]*hydratedProjection
	byNodeAttachment     map[nodeAttachmentKey]*nodeAttachmentProjection
	byBoundaryAttachment map[boundaryAttachmentKey]*boundaryAttachmentProjection
}

// newHydratedProjections returns an empty set for one render, holding the
// render's resolved-provides bag by reference.
func newHydratedProjections(provides NodeProvides) *hydratedProjections {
	return &hydratedProjections{
		ResolvedNodeProvides: provides,
		byNode:               map[string]*hydratedProjection{},
		byNodeAttachment:     map[nodeAttachmentKey]*nodeAttachmentProjection{},
		byBoundaryAttachment: map[boundaryAttachmentKey]*boundaryAttachmentProjection{},
	}
}

// recordNode reduces one node's completed mid-render state to its projection and
// retains that.
//
// It READS the state and retains no reference to it: every field it takes is
// either a value or a slice built here, so the state itself is free to be
// dropped when the node's turn ends.
//
// It is called at the one point where each field's source holds the value its
// own accessor names — after source resolution and workload parsing, and before
// anything evaluates an artifact template. A nil set records nothing, which is
// the state of a render whose caller assembled its own input.
func (h *hydratedProjections) recordNode(state *PhaseBState) {
	if h == nil || state == nil {
		return
	}
	sourceDeclared, workloadPresent := pillarsReached(state)
	p := &hydratedProjection{
		ResolvedNodeSourceDeclared: sourceDeclared,
		ParsedNodeWorkloadPresent:  workloadPresent,
	}
	if recipe := state.Recipe; recipe != nil {
		p.ValuesMappingTemplateRefs = collectValuesMappingRefs(recipe.Spec.ValuesMapping)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.byNode[state.NodeID]; ok {
		// The artifact walk and the attachment-delivery site may each have
		// reached this node first; keep what either wrote.
		p.ArtifactTemplateText = existing.ArtifactTemplateText
		p.NodeInjectDeliveries = existing.NodeInjectDeliveries
	}
	h.byNode[state.NodeID] = p
}

// recordAttachmentInjectMappingMechanisms retains the pairing between the
// mechanisms one node's own injection block declares and the mechanisms one
// attachment's rendered injectMapping output named.
//
// It is called from the node walk, once per attachment whose mapping produced an
// overlay, rather than where the attachment's own render completes: the mapping
// is evaluated in the host node's Phase A and the attachment's render is a
// separate walk that never sees it. The carrier is still the attachment's,
// because the mapping is that attachment's FeatureRecipe's and two attachments on
// one node answer independently.
//
// A nil set records nothing, which is the state of a render whose caller
// assembled its own input.
func (h *hydratedProjections) recordAttachmentInjectMappingMechanisms(
	nodeID, attachment string, mechanisms []constraint.InjectMappingMechanism,
) {
	if h == nil || nodeID == "" || attachment == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.nodeAttachmentEntry(nodeAttachmentKey{node: nodeID, attachment: attachment})
	p.AttachmentInjectMappingMechanisms = mechanisms
}

// recordArtifactTemplate retains the template text standing at one of a node's
// artifacts, under the output name that entry resolves to.
//
// It is called from the artifacts walk rather than beside recordNode, because
// that walk is where an imported entry's and a bundle entry's content is
// resolved — the content the recorded fact names for all three authoring forms.
//
// A nil set records nothing, and that is what keeps the attachment-carried
// artifacts flowing through the same walk out of a fact declared over a node's
// own Recipe: an attachment's render is given no node retention, so this is not
// reached for it. Those entries are recorded against their own carrier instead,
// through the seam below.
func (h *hydratedProjections) recordArtifactTemplate(nodeID, outputName, text string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.byNode[nodeID]
	if !ok {
		p = &hydratedProjection{}
		h.byNode[nodeID] = p
	}
	p.ArtifactTemplateText.Set(outputName, text)
}

// recordArtifactContentPin retains the content digest a node's Recipe recorded
// for one of its artifacts, under the output name that entry resolves to.
//
// It is called from the artifacts walk beside the text recording, and only for an
// entry whose resolved form carries a pin. So an artifact absent from this map is
// one no comparison is owed for, which is the whole of the form gate: an inline
// entry is pinned by whatever pins the entity it is part of, and a bundle-supplied
// one by the version the bundle was resolved at.
//
// A nil set records nothing, which keeps the attachment-carried artifacts flowing
// through the same walk out of a fact declared over a node's own Recipe.
func (h *hydratedProjections) recordArtifactContentPin(nodeID, outputName, pin string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.byNode[nodeID]
	if !ok {
		p = &hydratedProjection{}
		h.byNode[nodeID] = p
	}
	p.ArtifactRecordedContentPin.Set(outputName, pin)
}

// recordNodeInjectDeliveries retains the attributed deliveries one node's
// injection pipe carries.
//
// It is called from the attachment-delivery site rather than beside recordNode,
// for the reason the artifact recording above is called from the artifacts walk:
// that site is the only one holding what the fact names. The pipe there already
// carries what the component's own block and its connections delivered, and the
// attachments' own claims are being made at that point — including the ones that
// delivered nothing, which leave the pipe unchanged and so cannot be read back
// out of it afterwards.
//
// It creates the node's entry when the walk has not reached recordNode yet, the
// same way the artifact recording does, and recordNode keeps whatever this wrote.
// A nil set records nothing, which is the state of a render whose caller
// assembled its own input.
func (h *hydratedProjections) recordNodeInjectDeliveries(nodeID string, deliveries []constraint.InjectDelivery) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.byNode[nodeID]
	if !ok {
		p = &hydratedProjection{}
		h.byNode[nodeID] = p
	}
	p.NodeInjectDeliveries = deliveries
}

// projection returns one node's retained projection, and reports whether the
// node was recorded.
func (h *hydratedProjections) projection(nodeID string) (hydratedProjection, bool) {
	if h == nil {
		return hydratedProjection{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.byNode[nodeID]
	if !ok {
		return hydratedProjection{}, false
	}
	return *p, true
}

// artifactNames returns the output names one node's artifacts walk recorded text
// under, in the order that walk reached them.
//
// It reads the same retention the artifact text is answered from, so the names an
// axis built on it enumerates and the texts a rule then asks for are one set by
// construction rather than by agreement.
func (h *hydratedProjections) artifactNames(nodeID string) []string {
	p, ok := h.projection(nodeID)
	if !ok {
		return nil
	}
	return p.ArtifactTemplateText.Keys()
}

// recordedNodes reports how many nodes the set holds.
func (h *hydratedProjections) recordedNodes() int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.byNode)
}

// attachmentCarrier is everything one attachment's render does under the carrier
// that attachment was declared against: what it records, and what the rule
// declared over that carrier's facts is asked about.
//
// It exists because the render path that produces those values is one path
// reached from two carriers: the same function renders an attachment declared on
// a node and one declared at a boundary, and it cannot tell which from anything
// it holds. The node identity it carries is the HOST component's for the first
// and a composite marker built for dispatch uniqueness for the second, so neither
// is the carrier. Handing that path a carrier its caller built is what supplies
// the identity without the path having to decide it — and it makes writing a
// boundary-rooted identity into a node-rooted key impossible rather than merely
// unintended.
//
// Recording and reading are one value rather than two, and that is the point
// rather than a convenience. A render given the recording but not the presentation
// would record facts nothing is then asked about, which is a rule invoked zero
// times — indistinguishable from a rule that ran and approved, and green under
// every determinism and byte-identity proof over the same build. One value cannot
// be half-supplied.
//
// # It spans two stages, because the identity problem it solves is one problem
//
// The carrier answers which attachment a render belongs to, and that question has
// the same two answers whichever stage's facts are being recorded. So the mid-render
// state this render reached and the file set it went on to emit are recorded through
// ONE carrier rather than through two built at the same site from the same knowledge.
// A second carrier for the emitted stage would be a second answer to "which
// attachment is this", free to disagree with the first — and disagreement here files
// one document's emission under another document's key, which is exactly the
// mis-keying the carrier exists to prevent.
//
// The key reports are therefore named for the carrier rather than for either stage's
// content: one method answering "which attachment" serves every stage that keys a
// fact there.
type attachmentCarrier interface {
	// recordPillars reduces one attachment render's completed mid-render state to
	// the projection that outlives it.
	recordPillars(state *PhaseBState)

	// recordArtifactTemplate retains the template text standing at one artifact
	// of this attachment's FeatureRecipe, under the output name that entry
	// resolves to.
	recordArtifactTemplate(outputName, text string)

	// recordArtifactContentPin retains the content digest this attachment's
	// FeatureRecipe recorded for one artifact, under the output name that entry
	// resolves to.
	//
	// It is called only where the walk resolved a form that carries a pin, so a
	// carrier reaching this at all is a carrier for which a comparison is owed.
	recordArtifactContentPin(outputName, pin string)

	// recordEmission reduces this attachment render's completed emitted file set to
	// the projection that outlives it, under this carrier's own key.
	//
	// It is a separate act from recordPillars rather than folded into it, because
	// the two are complete at different points of one render: the pillars once the
	// state is resolved and before any artifact template is evaluated, the emission
	// only after every pass that can contribute a file has run.
	recordEmission(state *PhaseBState)

	// readableFacts reports the presentation a rule over this carrier reads, and
	// refuses a presentation standing over no retention. Every carrier gets it
	// from the inputs it embeds.
	readableFacts(findingFile, outputName string) (constraint.HydratedView, error)

	// readableEmittedFacts reports the emitted-stage presentation a rule over this
	// carrier reads, and refuses a presentation standing over no retention, on the
	// same reading readableFacts follows for the stage before it.
	readableEmittedFacts(findingFile string) (constraint.EmittedView, error)

	// emittedAxes reports the coordinate axes a rule over this carrier is invoked
	// over, read from the same retention the presentation answers from.
	//
	// It comes from the carrier rather than from the render's own state because an
	// attachment's render builds a FRESH state around a synthesized Recipe and
	// carries none of the render-wide products on it: the carrier is the one thing
	// that travels into that render holding them. Reading the axis anywhere else
	// would leave a rule with a presentation and nothing to invoke it over, which is
	// a rule invoked zero times.
	emittedAxes() *emittedStageCoordinates

	// nodeAttachmentCarrierKey reports the node-rooted key this carrier is, and
	// false for a carrier rooted elsewhere.
	nodeAttachmentCarrierKey() (nodeAttachmentKey, bool)

	// boundaryAttachmentCarrierKey reports the boundary-rooted key this carrier is,
	// and false for a carrier rooted elsewhere.
	boundaryAttachmentCarrierKey() (boundaryAttachmentKey, bool)
}

// The two key reports above are a pair of narrow questions rather than one method
// that invokes the carrier's rule, and the reason is what the framework's own
// coverage read can see.
//
// That read resolves a call to a declaration and follows nothing through a value —
// not a method on a receiver, and not an interface. A rule invoked behind either is
// therefore attributed to no calling surface, so the placement table would report
// it as invoked and reachable from nowhere, with no row saying which rule was
// missing. Asking the carrier which key it is, and naming both traversals in one
// function the walk calls directly, keeps the invocation legible to the instrument
// whose whole subject is where a rule is enforced.
//
// Each carrier answers exactly one of the two, and a carrier answering neither is
// refused loudly where the pair is read rather than passing as nothing to check.

// attachmentCarrierInputs are the two products every carrier works through: the
// retention it records into, and the presentation over that retention a rule
// reads.
//
// They are held together because they are one arrangement: the walk that records
// a carrier's facts is exactly the walk that may read them, and a carrier holding
// one without the other is what the guard below refuses.
type attachmentCarrierInputs struct {
	// into is the render's own retention, shared by every carrier of that render.
	into *hydratedProjections

	// facts presents that retention as named facts. nil for a render that
	// published no presentation, which is what a caller assembling its own input
	// reaches.
	facts constraint.HydratedView

	// emittedInto is the render's own emitted-file retention, shared by every
	// carrier of that render exactly as into is.
	//
	// It is a second retention rather than a second field on the first, because the
	// two are declared at two stages: what a stage retains is derived from what it
	// declares, and a guard per stage compares the two sets in both directions. One
	// structure holding both stages' fields would fail each of those guards from the
	// side the other stage's fields sit on.
	emittedInto *emittedProjections

	// emittedFacts presents the emitted retention as named facts. nil for a render
	// that published no presentation.
	emittedFacts constraint.EmittedView

	// callerPresentationSupplied reports whether the render's CALLER handed in the
	// presentation this render's chain extends.
	//
	// It is what separates the two ways emittedFacts can be nil, and the separation
	// matters because one of them must not be silent. A caller that supplied
	// nothing is asking for a render rather than for a build, and a rule invoked
	// over facts nothing produced would be worse than one not invoked. A caller
	// that DID supply one, standing over a nil presentation here, is a chain that
	// broke inside this package — and for a rule that halts, letting that pass
	// would turn a build that refuses today into one that renders.
	callerPresentationSupplied bool
}

// readableFacts reports the presentation a carrier's rule reads, and refuses the
// one arrangement that would let such a rule admit every entry it was asked about.
//
// The two absences are different questions, on the same reading the node's own
// value states about its own pair. An absent presentation says this render
// published none, and nothing then reads this carrier's pillar facts — a state
// only a caller assembling a render's input directly reaches, and one no pillar
// reading covers, since the hand-written arms that once stood in for it are gone.
// An absent retention beneath a
// PRESENT presentation is not an outcome but a broken arrangement — the
// presentation would answer every entry's text with the empty string, the rule
// would collect no reference from it, and it would admit every template it was
// asked about. That is why it halts rather than returning nothing: silence is
// indistinguishable from approval to any reader downstream.
//
// The condition is unreachable while the retention and the presentation are
// installed together at the one site that builds a carrier; the halt is what makes
// that an enforced guarantee rather than an arrangement a later change could
// quietly undo. Its wording says the fault is the compiler's rather than the
// author's, because the halt carries a catalog category and no category attributes
// a fault to the compiler reaching a check without its inputs.
func (i attachmentCarrierInputs) readableFacts(
	findingFile, outputName string,
) (constraint.HydratedView, error) {
	if i.facts == nil {
		return nil, nil
	}
	if i.into == nil {
		return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: findingFile},
			fmt.Sprintf("artifact %q could not be checked against the pillars the FeatureRecipe "+
				"declares — internal error: the check's inputs were incomplete. This is a fault in "+
				"the compiler rather than in the FeatureRecipe, so there is nothing to correct in "+
				"the catalog", outputName), nil, nil)
	}
	return i.facts, nil
}

// readableEmittedFacts reports the emitted-stage presentation a carrier's rule reads,
// and refuses the one arrangement that would let such a rule admit every emission it
// was asked about.
//
// The two absences are the different questions readableFacts keeps apart, met at the
// next stage. An absent presentation says this render published none — a state only a
// caller assembling a render's input directly reaches, and one no collision reading
// then covers. An absent retention beneath a PRESENT presentation is not an outcome
// but a broken arrangement: the presentation would answer every name with no blocks,
// the rule would see nothing meeting anywhere, and it would admit every emission it
// was asked about. That is why it halts rather than returning nothing — silence is
// indistinguishable from approval to any reader downstream.
//
// It takes no output name because the emitted rule is invoked per name from an
// enumeration rather than at a walk that holds one, so the wording names the
// FeatureRecipe rather than a single entry.
func (i attachmentCarrierInputs) readableEmittedFacts(
	findingFile string,
) (constraint.EmittedView, error) {
	if i.emittedFacts == nil {
		if !i.callerPresentationSupplied {
			return nil, nil
		}
		return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: findingFile},
			"the files this FeatureRecipe emitted could not be checked for two of its blocks "+
				"meeting on one output name — internal error: the render published no presentation "+
				"over its emission. This is a fault in the compiler rather than in the "+
				"FeatureRecipe, so there is nothing to correct in the catalog", nil, nil)
	}
	if i.emittedInto == nil {
		return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: findingFile},
			"the files this FeatureRecipe emitted could not be checked for two of its blocks "+
				"meeting on one output name — internal error: the check's inputs were incomplete. "+
				"This is a fault in the compiler rather than in the FeatureRecipe, so there is "+
				"nothing to correct in the catalog", nil, nil)
	}
	return i.emittedFacts, nil
}

// emittedAxes presents this carrier's retention as the coordinate axes a rule over it
// is invoked across.
//
// It reads the retention the presentation answers from, so the names a rule is
// invoked over and the names its facts are keyed by come from one reader. A nil
// retention yields nil, which the caller separates from an ordinary skip.
func (i attachmentCarrierInputs) emittedAxes() *emittedStageCoordinates {
	return i.emittedInto.coordinates()
}

// nodeAttachmentCarrier is the carrier of an attachment declared on a node.
type nodeAttachmentCarrier struct {
	attachmentCarrierInputs
	key nodeAttachmentKey
}

// boundaryAttachmentCarrier is the carrier of an attachment declared at a
// boundary.
type boundaryAttachmentCarrier struct {
	attachmentCarrierInputs
	key boundaryAttachmentKey
}

// carrierProducts are the two stages' products every carrier works through, taken
// off the state that builds the carrier.
//
// They are ONE parameter rather than four, and that is what makes a half-supplied
// carrier hard to write rather than merely discouraged: a call site cannot pass the
// mid-render pair and forget the emitted pair, because there is one value to pass and
// one site that assembles it.
type carrierProducts struct {
	// hydrated is the render's mid-render retention and hydratedFacts the
	// presentation over it.
	hydrated      *hydratedProjections
	hydratedFacts constraint.HydratedView

	// emitted is the render's emitted-file retention and emittedFacts the
	// presentation over it.
	emitted      *emittedProjections
	emittedFacts constraint.EmittedView

	// callerPresentationSupplied reports whether the render's caller handed in the
	// presentation the chain extends, which is what tells a render asked for by a
	// caller assembling its own input from a chain that broke in this package.
	callerPresentationSupplied bool
}

// carrierProductsOf reads both stages' products off a node's own state.
//
// It is the one place the four are gathered, so a carrier built for an attachment on
// a node and one built for a boundary-scope attachment cannot be given different
// subsets of them.
func carrierProductsOf(state *PhaseBState) carrierProducts {
	if state == nil {
		return carrierProducts{}
	}
	return carrierProducts{
		hydrated:                   state.hydrated,
		hydratedFacts:              state.hydratedFacts,
		emitted:                    state.emitted,
		emittedFacts:               state.emittedFacts,
		callerPresentationSupplied: state.callerPresentationSupplied,
	}
}

// inputs reduces the products to what a carrier embeds.
func (p carrierProducts) inputs() attachmentCarrierInputs {
	return attachmentCarrierInputs{
		into:                       p.hydrated,
		facts:                      p.hydratedFacts,
		emittedInto:                p.emitted,
		emittedFacts:               p.emittedFacts,
		callerPresentationSupplied: p.callerPresentationSupplied,
	}
}

// attachmentOnNode returns the carrier of one attachment's render on a node,
// reading and recording through this render's retentions and the presentations over
// them.
//
// A nil retention yields a carrier that records nothing, which is the state of a
// render whose caller assembled its own input; so does a nil presentation, and the
// two are separate conditions the carrier keeps apart.
func (p carrierProducts) attachmentOnNode(node, attachment string) attachmentCarrier {
	return nodeAttachmentCarrier{
		attachmentCarrierInputs: p.inputs(),
		key:                     nodeAttachmentKey{node: node, attachment: attachment},
	}
}

// attachmentOnBoundary returns the carrier of one boundary-scope attachment's
// render.
func (p carrierProducts) attachmentOnBoundary(boundary, attachment string) attachmentCarrier {
	return boundaryAttachmentCarrier{
		attachmentCarrierInputs: p.inputs(),
		key:                     boundaryAttachmentKey{boundary: boundary, attachment: attachment},
	}
}

func (r nodeAttachmentCarrier) recordPillars(state *PhaseBState) {
	h := r.into
	if h == nil || state == nil {
		return
	}
	sourceDeclared, workloadPresent := pillarsReached(state)

	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.nodeAttachmentEntry(r.key)
	p.AttachmentResolvedSourceDeclared = sourceDeclared
	p.AttachmentParsedWorkloadPresent = workloadPresent
}

func (r nodeAttachmentCarrier) recordArtifactTemplate(outputName, text string) {
	h := r.into
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nodeAttachmentEntry(r.key).AttachmentArtifactTemplateText.Set(outputName, text)
}

func (r nodeAttachmentCarrier) recordArtifactContentPin(outputName, pin string) {
	h := r.into
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nodeAttachmentEntry(r.key).AttachmentArtifactRecordedContentPin.Set(outputName, pin)
}

func (r nodeAttachmentCarrier) recordEmission(state *PhaseBState) {
	r.emittedInto.recordNodeAttachment(r.key, state)
}

func (r boundaryAttachmentCarrier) recordPillars(state *PhaseBState) {
	h := r.into
	if h == nil || state == nil {
		return
	}
	sourceDeclared, workloadPresent := pillarsReached(state)

	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.boundaryAttachmentEntry(r.key)
	p.BoundaryAttachmentResolvedSourceDeclared = sourceDeclared
	p.BoundaryAttachmentParsedWorkloadPresent = workloadPresent
}

func (r boundaryAttachmentCarrier) recordArtifactTemplate(outputName, text string) {
	h := r.into
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.boundaryAttachmentEntry(r.key).BoundaryAttachmentArtifactTemplateText.Set(outputName, text)
}

func (r boundaryAttachmentCarrier) recordArtifactContentPin(outputName, pin string) {
	h := r.into
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.boundaryAttachmentEntry(r.key).BoundaryAttachmentArtifactRecordedContentPin.Set(outputName, pin)
}

func (r boundaryAttachmentCarrier) recordEmission(state *PhaseBState) {
	r.emittedInto.recordBoundaryAttachment(r.key, state)
}

// nodeAttachmentCarrierKey reports this carrier's own key.
func (r nodeAttachmentCarrier) nodeAttachmentCarrierKey() (nodeAttachmentKey, bool) {
	return r.key, true
}

// boundaryAttachmentCarrierKey reports that this carrier is not boundary-rooted.
func (r nodeAttachmentCarrier) boundaryAttachmentCarrierKey() (boundaryAttachmentKey, bool) {
	return boundaryAttachmentKey{}, false
}

// nodeAttachmentCarrierKey reports that this carrier is not node-rooted.
func (r boundaryAttachmentCarrier) nodeAttachmentCarrierKey() (nodeAttachmentKey, bool) {
	return nodeAttachmentKey{}, false
}

// boundaryAttachmentCarrierKey reports this carrier's own key.
func (r boundaryAttachmentCarrier) boundaryAttachmentCarrierKey() (boundaryAttachmentKey, bool) {
	return r.key, true
}

// nodeAttachmentEntry returns the entry one carrier's values are recorded into,
// creating it on first write.
//
// The pillars and the artifact texts are recorded at two different points of one
// render — the pillars once the state is complete, each text as the artifacts walk
// resolves it — so whichever arrives first creates the entry and the other adds to
// it. The caller holds the lock.
func (h *hydratedProjections) nodeAttachmentEntry(key nodeAttachmentKey) *nodeAttachmentProjection {
	p, ok := h.byNodeAttachment[key]
	if !ok {
		p = &nodeAttachmentProjection{}
		h.byNodeAttachment[key] = p
	}
	return p
}

// boundaryAttachmentEntry returns the entry one boundary-scope carrier's values
// are recorded into, creating it on first write. The caller holds the lock.
func (h *hydratedProjections) boundaryAttachmentEntry(
	key boundaryAttachmentKey,
) *boundaryAttachmentProjection {
	p, ok := h.byBoundaryAttachment[key]
	if !ok {
		p = &boundaryAttachmentProjection{}
		h.byBoundaryAttachment[key] = p
	}
	return p
}

// nodeAttachmentProjectionOf returns one node-carried attachment's retained
// projection, and reports whether that carrier was recorded.
func (h *hydratedProjections) nodeAttachmentProjectionOf(
	key nodeAttachmentKey,
) (nodeAttachmentProjection, bool) {
	if h == nil {
		return nodeAttachmentProjection{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.byNodeAttachment[key]
	if !ok {
		return nodeAttachmentProjection{}, false
	}
	return *p, true
}

// boundaryAttachmentProjectionOf returns one boundary-scope attachment's retained
// projection, and reports whether that carrier was recorded.
func (h *hydratedProjections) boundaryAttachmentProjectionOf(
	key boundaryAttachmentKey,
) (boundaryAttachmentProjection, bool) {
	if h == nil {
		return boundaryAttachmentProjection{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.byBoundaryAttachment[key]
	if !ok {
		return boundaryAttachmentProjection{}, false
	}
	return *p, true
}

// recordedNodeAttachments reports how many node-carried attachment renders the
// set holds.
func (h *hydratedProjections) recordedNodeAttachments() int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.byNodeAttachment)
}

// recordedBoundaryAttachments reports how many boundary-scope attachment renders
// the set holds.
func (h *hydratedProjections) recordedBoundaryAttachments() int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.byBoundaryAttachment)
}

// pillarsReached reads, from one completed mid-render state, the two pillar
// answers every form of this retention records: whether the render carries a
// source once resolution has run, and whether it carries a workload.
//
// It is one function rather than an expression at each recording site because a
// node's own render and an attachment's render must answer these the same way.
// The attachment's render is built around a Recipe carrying the FeatureRecipe's
// own resolved source, and its state carries the workload parsed from the
// FeatureRecipe's own body, so the two are the same reading over different
// documents — and two copies of it would be free to diverge over a state neither
// reads twice.
func pillarsReached(state *PhaseBState) (sourceDeclared, workloadPresent bool) {
	if state == nil {
		return false, false
	}
	return recipeSourceDeclared(state.Recipe), state.ParsedWorkload != nil
}

// collectValuesMappingRefs derives the reference set of a node's values mapping.
//
// The set is derived from the INLINE text alone, because that is the authoring
// form the question is about: a mapping authored as an import carries no inline
// text, so its reference set is absent rather than empty.
//
// A text that does not parse still reports the authoring form it took, and
// carries no paths. That combination is unreachable in a completed render — the
// same text is evaluated through the same parser earlier in the node's turn, and
// a fault there halts the build before this is recorded — so the branch states
// what is true about the authoring rather than inventing a third condition.
func collectValuesMappingRefs(mapping types.ValuesMapping) valuesMappingRefs {
	if mapping.Inline == "" {
		return valuesMappingRefs{}
	}
	paths, err := gotmpl.CollectTemplateFieldAccesses(mapping.Inline)
	if err != nil {
		return valuesMappingRefs{Inline: true}
	}
	return valuesMappingRefs{Paths: paths, Inline: true}
}

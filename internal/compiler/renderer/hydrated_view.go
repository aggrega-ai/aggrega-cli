// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// hydratedView presents, as named facts, the state each render reached before any
// of its artifact bytes existed: the value it resolved for each provides key, the
// reference set of its inline values mapping, whether a source survived
// resolution, whether a workload was parsed, the attributed deliveries its
// injection pipe carries, and the template text standing at each artifact before
// anything evaluates it.
//
// The last three are presented per carrier rather than per node alone, because
// three documents author them: a node's own Recipe, the FeatureRecipe an
// attachment binds on a node, and the FeatureRecipe an operator attaches at a
// boundary. An attachment's render runs under the host component's node identity
// over documents its FeatureRecipe declares, so a node-keyed answer would report
// the host's pillars for the attachment's templates — wrong in both directions and
// invisible from the outcome.
//
// It satisfies [constraint.HydratedView], and the compiler holds it to that
// contract at the bottom of this file rather than a comment claiming it.
//
// # The earlier stage is embedded, not re-projected
//
// A stage's view contains the stage it extends, so every fact complete before
// this state was reached is readable here. Those facts are answered by the
// presentation of what each plugin reports about itself, embedded and delegated
// to, rather than re-derived: one fact has one producer, and two producers of a
// fact are free to disagree with nothing comparing them. That presentation in
// turn embeds the one below it, so a rule reading a graph fact through this
// value reaches the same answer the graph's own presentation gives.
//
// The delegation is load-bearing for the rules placed here rather than
// incidental. A rule over an artifact entry reads the entry's text — a fact of
// this stage — together with the delimiter pair its provider declares, which is
// a fact of the stage below; asking the embedded presentation for the pair is
// what lets one rule read both without either stage producing the other's fact.
//
// # Every fact is read from the retention the render already holds
//
// The state each of these facts describes is a loop-local: it is built for one
// render, used to produce that render's documents, and dropped when its turn
// ends. What survives is a projection of that state, one value per fact declared
// here, recorded as each node completes and as each attachment's render on that
// node completes. The accessors below read that retention and nothing else,
// so presenting this stage re-reads no document and re-derives no value — a
// second derivation beside the recording would be free to disagree with it about
// a node neither of them reads twice.
//
// The resolved provides are the exception in placement rather than in principle:
// they are evaluated once for the whole render before any node is rendered, so
// the retention holds that one bag by reference instead of copying it under every
// node. It is not written to after its evaluation, which is what lets a reader
// take it without a lock.
//
// # A node the render did not reach answers as one that declared nothing
//
// A fact accessor returns a value and never an error, so a node absent from the
// retention is answered with the zero value of the fact asked for. That is
// indistinguishable from a node whose Recipe declared nothing, and deliberately
// so at this layer: a node is recorded unconditionally as its state completes, so
// the only nodes missing from the retention are those a render never reached —
// and a render that stopped short reports why it stopped, which is not this
// stage's finding.
//
// # Values are handed over uncopied
//
// The contract every view shares makes a fact's value immutable to a check, and
// this value keeps that contract. What an accessor returns is the retained value
// itself rather than a copy of it, so a check that wrote through one would change
// what every later reader sees; no check does, and the contract is where that is
// declared rather than here.
type hydratedView struct {
	constraint.DescribedView

	// projections is the render's retention of per-node mid-render state,
	// together with the render-wide resolved-provides bag. It is held by pointer
	// because the retention is filled as the node walk proceeds: this
	// presentation is built once, before any node has rendered, and answers from
	// whatever the walk has recorded by the time a reader asks.
	projections *hydratedProjections
}

// newHydratedView presents the state each node reached before its artifact bytes
// existed, with the facts complete earlier answered by the presentation of what
// every plugin reports about itself.
//
// It is unexported because the render is the one thing that builds it: the
// retention it reads is the render's own working state, so a caller outside this
// package has nothing to construct it from and the bypass does not compile.
//
// The products are taken rather than reached for, because a view projects facts
// already produced and never produces one of its own. The retention is taken by
// pointer rather than by value, so this presentation is built once at the point
// the retention is installed and answers from what the node walk records into it
// afterwards.
//
// A build constructs exactly one of these and publishes it on the render's
// result. Constructing a second one over a different retention would give this
// stage's facts a second producer, free to disagree with the first about a node
// neither of them records twice.
//
// It returns nil when the earlier presentation or the retention is absent. A
// presentation missing either would answer a whole class of facts from nothing,
// and a presentation that answers emptily is worse than an absent one, because a
// rule reading it cannot tell an empty answer from a real one.
func newHydratedView(
	earlier constraint.DescribedView,
	projections *hydratedProjections,
) constraint.HydratedView {
	if earlier == nil || projections == nil {
		return nil
	}
	return &hydratedView{DescribedView: earlier, projections: projections}
}

// ResolvedNodeProvides returns the value a node's Recipe resolved for one
// provides key, and whether the key was resolved at all.
//
// It reads the render's own resolved bag, which is evaluated once for the whole
// render and not written to afterwards — so this takes no lock and answers the
// same value whenever it is asked. A node that resolved no provides at all, and
// a key that node did not resolve, both report false.
func (v *hydratedView) ResolvedNodeProvides(node string, key string) (string, bool) {
	bag, ok := v.projections.ResolvedNodeProvides[node]
	if !ok {
		return "", false
	}
	return bag.Get(key)
}

// ValuesMappingTemplateRefs returns the context paths a node's inline
// values-mapping template reads, and whether the node's mapping is authored
// inline at all.
//
// The second result is load-bearing. A mapping authored as an import carries no
// inline text, so its reference set is absent rather than empty, and a rule that
// could not tell the two apart would report every value unread for exactly those
// nodes whose mapping it never read. A node the render did not reach reports the
// same absence, for the same reason it answers every other fact with its zero
// value.
//
// The paths come back sorted, as the retention recorded them: a template has no
// single reading order to preserve, so sorting is what makes the result the same
// on every run.
func (v *hydratedView) ValuesMappingTemplateRefs(node string) ([]string, bool) {
	p, ok := v.projections.projection(node)
	if !ok {
		return nil, false
	}
	return p.ValuesMappingTemplateRefs.Paths, p.ValuesMappingTemplateRefs.Inline
}

// ResolvedNodeSourceDeclared reports whether a node's Recipe carries a source
// once source resolution has run.
//
// The value is the RESOLVED source rather than the text the author wrote: source
// resolution replaces the Recipe with a copy whose source holds the evaluated
// result, and the retention records that copy — so a source template evaluating
// to an empty string reports the same as an absent source. That is one condition
// rather than two, which is what lets a rule about a template reading the source
// pillar name a single cause instead of distinguishing two states an author
// cannot tell apart from the outcome.
func (v *hydratedView) ResolvedNodeSourceDeclared(node string) bool {
	p, _ := v.projections.projection(node)
	return p.ResolvedNodeSourceDeclared
}

// ParsedNodeWorkloadPresent reports whether a node's render state carries a
// workload.
//
// A Recipe declaring no workload body reaches artifact evaluation with none,
// because workload synthesis had nothing to parse. So this is what separates a
// node whose templates may read the workload pillar from one whose reference to
// it has nothing to resolve against.
func (v *hydratedView) ParsedNodeWorkloadPresent(node string) bool {
	p, _ := v.projections.projection(node)
	return p.ParsedNodeWorkloadPresent
}

// NodeInjectDeliveries returns the deliveries a node's injection pipe carries
// once every attachment attached to it has delivered, in delivery order.
//
// Each entry names the mechanism and the key, whatever attachment asked for the
// delivery, and whether a value arrived. The attribution is answered from the
// retention rather than from the pipe because the pipe cannot answer it: it
// holds values under keys and remembers nothing about which surface put one
// there, and a delivery whose reference resolved to nothing left it unchanged.
//
// A node the render did not reach answers empty, which is what a node whose pipe
// carries nothing also gives — the same reading every fact of this stage carries
// for a node absent from the retention.
func (v *hydratedView) NodeInjectDeliveries(node string) []constraint.InjectDelivery {
	p, ok := v.projections.projection(node)
	if !ok {
		return nil
	}
	return p.NodeInjectDeliveries
}

// ArtifactTemplateText returns the template text standing at one of a node's
// artifacts, as it is before anything evaluates it.
//
// The artifact is named by the output name the entry resolves to, which is the
// name the traversal invoking a rule keys each subject by. The text is the
// entry's content whichever form the entry took — written inline, supplied by an
// imported file, or supplied by an entry of a library bundle — because the
// retention records it where that content is resolved rather than where the entry
// is authored.
//
// An artifact the retention does not hold answers empty, which is what a node the
// render did not reach and an artifact of an attachment rather than of the node's
// own Recipe both give.
func (v *hydratedView) ArtifactTemplateText(node string, artifact string) string {
	p, ok := v.projections.projection(node)
	if !ok {
		return ""
	}
	text, _ := p.ArtifactTemplateText.Get(artifact)
	return text
}

// ArtifactRecordedContentPin returns the content digest a node's Recipe recorded
// for one of its artifacts, and whether the entry carries a recorded pin at all.
//
// The second result is what says a comparison is owed. The retention holds an
// entry only where the walk resolved a form that carries a pin, so an absent
// answer covers three cases that need the same treatment: a Recipe that was never
// frozen, an entry whose form carries no pin, and a node or an artifact this
// retention does not hold — including an artifact of an attachment rather than of
// the node's own Recipe, which is answered through that attachment's own carrier.
func (v *hydratedView) ArtifactRecordedContentPin(node string, artifact string) (string, bool) {
	p, ok := v.projections.projection(node)
	if !ok {
		return "", false
	}
	return p.ArtifactRecordedContentPin.Get(artifact)
}

// AttachmentResolvedSourceDeclared reports whether the FeatureRecipe one
// attachment binds on a node carries a source once source resolution has run.
//
// It is answered from the attachment's own carrier and never from the node's,
// and that is where this fact earns its coordinate rather than merely filing
// tidily. The attachment's render runs under the host component's node identity
// over a source the FeatureRecipe declares, so asked of the node the answer would
// be the host's: a host declaring a source would have an attachment's reference
// to an absent one admitted, and a host declaring none would have a legitimate
// attachment reference refused.
//
// An attachment the render did not reach answers false, which is what an
// attachment whose FeatureRecipe declares no source also gives — the same reading
// every fact of this stage carries for a carrier absent from the retention.
func (v *hydratedView) AttachmentResolvedSourceDeclared(node string, attachment string) bool {
	p, _ := v.projections.nodeAttachmentProjectionOf(
		nodeAttachmentKey{node: node, attachment: attachment},
	)
	return p.AttachmentResolvedSourceDeclared
}

// AttachmentParsedWorkloadPresent reports whether the render of one attachment on
// a node carries a workload.
//
// A FeatureRecipe declaring no workload body reaches artifact evaluation with
// none, because workload synthesis had nothing to parse. So this is what
// separates an attachment whose artifact templates may read the workload pillar
// from one whose reference to it has nothing to resolve against.
//
// It is answered from the attachment's own carrier for the reason its resolved
// source is, and two attachments on one node answer independently: one declaring
// a workload says nothing about the other.
func (v *hydratedView) AttachmentParsedWorkloadPresent(node string, attachment string) bool {
	p, _ := v.projections.nodeAttachmentProjectionOf(
		nodeAttachmentKey{node: node, attachment: attachment},
	)
	return p.AttachmentParsedWorkloadPresent
}

// AttachmentArtifactTemplateText returns the template text standing at one
// artifact of the FeatureRecipe one attachment binds on a node, as it is before
// anything evaluates it.
//
// The artifact is named by the output name the entry resolves to. The text is the
// entry's content whichever form the entry took — written inline, supplied by an
// imported file, or supplied by an entry of a library bundle — because the
// retention records it where that content is resolved rather than where the entry
// is authored.
//
// An artifact this carrier does not hold answers empty, which is what an
// attachment the render did not reach and an artifact of the node's own Recipe
// both give: the node's entries are recorded against the node and are not
// reachable through this key.
func (v *hydratedView) AttachmentArtifactTemplateText(
	node string, attachment string, artifact string,
) string {
	p, ok := v.projections.nodeAttachmentProjectionOf(
		nodeAttachmentKey{node: node, attachment: attachment},
	)
	if !ok {
		return ""
	}
	text, _ := p.AttachmentArtifactTemplateText.Get(artifact)
	return text
}

// AttachmentArtifactRecordedContentPin returns the content digest the
// FeatureRecipe one attachment binds on a node recorded for one of its artifacts,
// and whether the entry carries a recorded pin at all.
//
// It is answered from the attachment's own carrier and never from the node's, on
// the reading the text accessor beside it states: the attachment's render walks the
// same producer under the host component's node identity, so a node-keyed answer
// would measure this FeatureRecipe's content against whatever the host's Recipe
// recorded.
func (v *hydratedView) AttachmentArtifactRecordedContentPin(
	node string, attachment string, artifact string,
) (string, bool) {
	p, ok := v.projections.nodeAttachmentProjectionOf(
		nodeAttachmentKey{node: node, attachment: attachment},
	)
	if !ok {
		return "", false
	}
	return p.AttachmentArtifactRecordedContentPin.Get(artifact)
}

// AttachmentInjectMappingMechanisms returns the mechanisms the host component's
// own injection block declares, paired with whether one attachment's rendered
// injectMapping output named each of them, and with the FeatureRecipe that output
// was read from.
//
// It is answered from the attachment's own carrier rather than from the node's,
// because each attachment carries its own mapping: two attachments on one node
// are evaluated against the same declarations and may name different mechanisms,
// so a node-keyed answer would report one attachment's mapping for the other's.
//
// An attachment whose mapping named no mechanism answers empty, and so does an
// attachment the render did not reach and a component that declared no injection
// block. All three are the ordinary answer rather than a claim that a mechanism
// went unnamed: what says a mechanism was left out is an entry reporting it
// unmapped, and an empty answer contains none.
func (v *hydratedView) AttachmentInjectMappingMechanisms(
	node string, attachment string,
) []constraint.InjectMappingMechanism {
	p, ok := v.projections.nodeAttachmentProjectionOf(
		nodeAttachmentKey{node: node, attachment: attachment},
	)
	if !ok {
		return nil
	}
	return p.AttachmentInjectMappingMechanisms
}

// BoundaryAttachmentResolvedSourceDeclared reports whether the FeatureRecipe one
// boundary-scope attachment binds carries a source once source resolution has
// run.
//
// It is answered from a carrier rooted at the boundary, because that is where
// such an attachment is declared and it reaches every component the boundary
// holds. It stands against no single component, so no component's resolved source
// is its own and no node-rooted key can name it.
func (v *hydratedView) BoundaryAttachmentResolvedSourceDeclared(
	boundary string, attachment string,
) bool {
	p, _ := v.projections.boundaryAttachmentProjectionOf(
		boundaryAttachmentKey{boundary: boundary, attachment: attachment},
	)
	return p.BoundaryAttachmentResolvedSourceDeclared
}

// BoundaryAttachmentParsedWorkloadPresent reports whether the render of one
// boundary-scope attachment carries a workload.
//
// A FeatureRecipe declaring no workload body reaches artifact evaluation with
// none, because workload synthesis had nothing to parse. The carrier is rooted at
// the boundary for the reason the boundary-scope resolved source is.
func (v *hydratedView) BoundaryAttachmentParsedWorkloadPresent(
	boundary string, attachment string,
) bool {
	p, _ := v.projections.boundaryAttachmentProjectionOf(
		boundaryAttachmentKey{boundary: boundary, attachment: attachment},
	)
	return p.BoundaryAttachmentParsedWorkloadPresent
}

// BoundaryAttachmentArtifactTemplateText returns the template text standing at
// one artifact of the FeatureRecipe one boundary-scope attachment binds, as it is
// before anything evaluates it.
//
// The output names in that block are chosen independently of any component's and
// of any other attachment's, and nothing declares them unique across documents —
// so the coordinates identifying one entry are the boundary and the attachment
// together with the entry's own name.
func (v *hydratedView) BoundaryAttachmentArtifactTemplateText(
	boundary string, attachment string, artifact string,
) string {
	p, ok := v.projections.boundaryAttachmentProjectionOf(
		boundaryAttachmentKey{boundary: boundary, attachment: attachment},
	)
	if !ok {
		return ""
	}
	text, _ := p.BoundaryAttachmentArtifactTemplateText.Get(artifact)
	return text
}

// BoundaryAttachmentArtifactRecordedContentPin returns the content digest the
// FeatureRecipe one boundary-scope attachment binds recorded for one of its
// artifacts, and whether the entry carries a recorded pin at all.
//
// It is rooted at the boundary for the reason the text accessor beside it is: such
// an attachment is declared once on a boundary and reaches every component that
// boundary holds, so a component-rooted key would answer one recorded pin under as
// many keys as the boundary has members.
func (v *hydratedView) BoundaryAttachmentArtifactRecordedContentPin(
	boundary string, attachment string, artifact string,
) (string, bool) {
	p, ok := v.projections.boundaryAttachmentProjectionOf(
		boundaryAttachmentKey{boundary: boundary, attachment: attachment},
	)
	if !ok {
		return "", false
	}
	return p.BoundaryAttachmentArtifactRecordedContentPin.Get(artifact)
}

var _ constraint.HydratedView = (*hydratedView)(nil)

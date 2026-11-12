// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"context"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// runGraphStageRules invokes the rules declared over the finalised graph and
// accumulates what they raised onto the scope that holds it.
//
// A rule needs a presentation for the facts it reads and an enumeration for the
// subjects it is invoked once per. Both are built from the products this scope
// holds, so a rule is asked about the coordinates the graph really carries and
// answers from the same graph — as against a set assembled beside it, which
// could name coordinates the facts cannot answer for and would leave the rule
// silently uninvoked over the ones it can.
//
// It reports the reason a walk stopped and nothing else. A rule raising is not
// an error here, and that is the load-bearing part: the suite walks its whole
// axis and every rule in it before anyone reads what it accumulated, so one
// offending connection costs a document neither the rest of that rule's axis
// nor anything the rules beside it would have reported. A caller that refuses
// on a finding refuses once, holding all of them.
//
// # Two collections, chosen by the severity the rule declared
//
// A violation is appended to the scope's violation slice and an advisory to its
// advisory slice, and which one a rule reaches is decided by the severity on its
// own declaration rather than by anything here. What each collection then means
// is the caller's: the pipeline folds the first into the slice a build gates on
// and the second into the warning stream it displays, and a surface with neither
// of those refuses on the first and carries the second.
//
// The rules are invoked in the order the readings beside them run, so the order
// a caller sees findings in does not depend on which reading produced them.
//
// # A rule's declaring package does not fix where it runs
//
// The rules invoked here are declared in more than one package, and which package
// holds a declaration says nothing about which scope invokes it: a rule runs at
// the stage of the presentation it declares, and every rule declaring the
// finalised graph's presentation belongs here whatever package its condition is
// registered in. One of them is declared beside the render's own readings for
// that reason alone — the condition it reports is registered there — and it is
// invoked from here because the facts it reads are complete the moment the graph
// is final, which is before anything renders.
func runGraphStageRules(
	ctx context.Context,
	out *GraphScope,
) error {
	// An absent presentation or an absent graph is an internal inconsistency
	// rather than a document the author can fix: this scope built both a moment
	// ago. It is reported rather than skipped, because a rule that never ran
	// reports exactly what a rule that ran and approved reports, and nothing
	// downstream could tell the two apart.
	//
	// The enumeration is taken from the phase output rather than derived here.
	// It is built where the graph and its presentation are both first in hand,
	// and the rules over what the render's plugins report are invoked over the
	// same node and attachment axes — so one derivation serves both and neither
	// can be asked about coordinates the other was not.
	coordinates := out.GraphCoordinates
	if coordinates == nil {
		return fmt.Errorf(
			"graph-stage rules: the compilation produced no view of the finalised graph to invoke them against")
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
	out.Violations = append(out.Violations, violations...)

	// Each connection points at a component the composition holds, evaluated once
	// per connection. The two rules beside each other are one absence split by its
	// cause: a target nothing answers to, and a target an activation toggle
	// removed. They read the target the connection RESOLVED to, which is why they
	// are asked here — an export alias is rewritten and every authoring surface
	// has drawn by the time the graph is final, so the earlier reading would refuse
	// a reference that does resolve and would miss one drawn later. Both are asked
	// over the whole connection set, so a connection an attachment projected or the
	// Release wired is refused exactly as a component's own is, and every
	// offending connection is reported in one build rather than the first.
	violations, err = constraint.RunOnEdges(
		ctx.Err,
		coordinates,
		out.GraphView,
		topology.ConnectionTargetIsAComponent,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

	violations, err = constraint.RunOnEdges(
		ctx.Err,
		coordinates,
		out.GraphView,
		topology.ConnectionTargetIsActive,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

	// Each connection that reaches a component a sub-module contributes does so
	// through a name the module exports, evaluated once per connection. It is
	// asked beside the two rules above because it shares their subject — the
	// component a connection resolved to — and ends silent where they speak, so a
	// target nothing answers to is reported by them alone. It is asked over the
	// whole connection set, so a connection an attachment projected or the
	// Release wired reaches into a module on the same terms as a component's own.
	violations, err = constraint.RunOnEdges(
		ctx.Err,
		coordinates,
		out.GraphView,
		topology.ConnectionTargetIsExported,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

	// One alias names one connection of a component, evaluated once per
	// connection the composition drew together with the surface that drew it.
	// The alias is what a component's Recipe and its assertions read a
	// connection by, so a second connection under it — on the same socket or
	// another, drawn by any surface — would leave one of the two out of what
	// they see. It is asked here because the place each connection's entry was
	// written is settled only once the attachment cascade has folded, and the
	// finding is positioned at the later connection's own entry, so the axis is
	// the one carrying each connection's origin. Every offending connection is
	// reported in one build rather than the first.
	violations, err = constraint.RunOnOriginatedConnections(
		ctx.Err,
		coordinates,
		out.GraphView,
		topology.ConnectionAliasNamesOneConnection,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

	// No Release entry redirects a connection the document that drew it
	// already targeted, evaluated once per connection. Both targets are read as
	// written rather than as resolved: a connection naming a module's published
	// alias resolves to the component id behind it, so a comparison against the
	// resolved target would report a disagreement between two documents that
	// named the very same alias. Every offending entry is reported in one build
	// rather than the first, so an author clears them in a single pass.
	//
	// 🔴 This rule declares the flattened composition rather than the finalised
	// graph, because both targets it reads are complete once the composition is
	// flattened. It is asked here all the same, and the reason is the axis: the
	// flattened composition enumerates only the connections a component's own
	// block wrote, so the earliest enumeration of every connection — what this
	// rule is asked of — is the one this scope holds. A declaration reading facts
	// complete earlier than the suite that walks it is the expected shape, not a
	// placement to correct.
	// The traversal is instantiated at the presentation the rule declares rather
	// than at the one this scope holds. Type inference would otherwise take the
	// view from the argument and refuse the rule: a check carries its view as a
	// type parameter, and a parameterised type does not widen with the interface
	// it embeds, so the finalised graph's presentation satisfies the flattened
	// composition's contract while the two check types remain unrelated.
	violations, err = constraint.RunOnEdges[constraint.TopologyView](
		ctx.Err,
		coordinates,
		out.GraphView,
		topology.ReleaseKeepsTheDeclaredConnectionTarget,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

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
	out.Violations = append(out.Violations, violations...)

	// The label constraint an attachment slot declares over what may fill it,
	// evaluated once per slot each node's component type declares. The axis is
	// the declared set rather than the bound one, so the rule is asked about a
	// slot nothing binds as well as one something does — it ends on the unbound
	// case without raising, and asking it is what keeps the question about the
	// declaration rather than about what the node happened to end up with. The
	// signal is read once per node, which is the granularity the traversal
	// promises and the same one the reading beside it had.
	violations, err = constraint.RunOnNodeSlots(
		ctx.Err,
		coordinates,
		coordinates,
		out.GraphView,
		analyzer.SlotFillSatisfiesSlotSelector,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

	// The same constraint declared on a slot a dependency socket declares,
	// evaluated once per attachment a node carries and asked only of the ones a
	// connection carries. Each such attachment is the fill one connection entry
	// wrote, so the axis is the attachments themselves rather than the declared
	// slots: no scope reaches a socket's slot, and an unfilled one is the
	// required-slot reading's question below. The enumeration positions each at
	// the connection entry its type was written on. The signal is read once per
	// node, which is the granularity the traversal promises.
	violations, err = constraint.RunOnNodeAttachments(
		ctx.Err,
		coordinates,
		coordinates,
		out.GraphView,
		analyzer.ConnectionAttachmentSatisfiesSocketSlotSelector,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

	// An attachment written on a component instance under a name no slot declares
	// does not fit a slot that instance leaves empty, evaluated once per attachment
	// a node carries after the whole attachment cascade has folded. Both halves of
	// the question need that point: which slots end up bound is known only once
	// every scope has folded, and which scope each attachment was written at is
	// recorded as the cascade folds — so an attachment a subsystem or a boundary
	// shares is told apart from one the instance wrote here and nowhere earlier.
	// The signal is read once per node, which is the granularity the traversal
	// promises.
	violations, err = constraint.RunOnNodeAttachments(
		ctx.Err,
		coordinates,
		coordinates,
		out.GraphView,
		analyzer.AttachmentFittingAnEmptySlotIsNamedForIt,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

	// Each presence mandate a node's component type declares is satisfied by some
	// attachment of the type it names. The satisfying set is the node's merged
	// attachment names and each name's resolved type, so an attachment
	// contributed at a scope the node inherits from satisfies a mandate exactly as
	// one written on the node does — the authored block alone is empty for
	// precisely the Releases that rely on sharing. A mandate whose declaration
	// cannot be evaluated is not tested here: it is reported at the stage its own
	// rule is asked at, so a broken declaration never reads as a satisfied
	// requirement and never as an absent attachment either. The signal is read
	// once per node, which is the granularity the traversal promises and the same
	// one the reading beside it had.
	violations, err = constraint.RunOnNodes(
		ctx.Err,
		coordinates,
		out.GraphView,
		analyzer.RequiredAttachmentTypeAttached,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

	// Each attachment slot a node's component type declares required is bound by
	// some attachment. It walks the same declared slot axis as the constraint
	// rule above rather than the set the node ended up bound to, which is what
	// lets the question be asked at all: a slot nothing binds is exactly the case
	// this rule reports, so an axis built from the bindings would never present
	// the subject the rule exists for. Binding is name presence in the merged set,
	// so an attachment contributed at a scope the node inherits from binds the
	// slot exactly as one written on the node does. The signal is read once per
	// node, which is the granularity the traversal promises and the same one the
	// reading beside it had.
	violations, err = constraint.RunOnNodeSlots(
		ctx.Err,
		coordinates,
		coordinates,
		out.GraphView,
		analyzer.RequiredAttachmentSlotBound,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

	// Each slot a dependency socket declares required is filled on every
	// connection wired through that socket, evaluated once per connection. A
	// socket's slot is filled per connection and never by a scope, so the demand
	// is owed by each connection and the axis is the connection set the graph
	// holds — a connection the Release wired included, which either document may
	// then fill. The fills are read off the attachments the node carries, by the
	// connection each was written on. The signal is read once per connection,
	// which is the granularity the traversal promises.
	violations, err = constraint.RunOnEdges(
		ctx.Err,
		coordinates,
		out.GraphView,
		analyzer.RequiredSocketSlotFilledOnEveryConnection,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

	// No two Features one of which declares a conflict with the other reach one
	// component, evaluated once per node over its merged attachment set. Reach is
	// what that set already holds: an attachment written on a subsystem or a
	// boundary is folded into every component the scope holds, so a boundary's
	// attachment and a component's own meet here and nowhere earlier, and two
	// sibling boundaries sharing no component never meet at all. The signal is
	// read once per node, which is the granularity the traversal promises.
	violations, err = constraint.RunOnNodes(
		ctx.Err,
		coordinates,
		out.GraphView,
		analyzer.ConflictingFeaturesDoNotReachOneComponent,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

	// No delivery key a component declares for itself is also delivered by one of
	// that component's own dependency connections, evaluated once per key the
	// component's own injection block delivers. The axis is that block as the
	// finalised graph carries it, keyed by the identity the graph gives the
	// component — which is what lets the question be asked of a component a
	// sub-aggregate contributed: the equivalent reading earlier in the build is
	// answered from the Aggregate's own documents and holds no entry for such a
	// component, and a rule presented no subject reports exactly what a rule
	// invoked and satisfied reports. The connections the answer reads are the
	// graph's own, so a key delivered through a connection an attachment
	// projected is in range here and is out of range earlier, where the only
	// connections in hand are the ones the component's author wrote. The signal
	// is read once per component, which is the granularity the traversal
	// promises.
	violations, err = constraint.RunOnNodeMechanismDeliveryKeys(
		ctx.Err,
		coordinates,
		coordinates,
		coordinates,
		out.GraphView,
		renderer.SelfInjectKeyNotDeliveredByAnEdge,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

	// No delivery key one of a component's dependency connections delivers is
	// also delivered by another of that component's connections, evaluated once
	// per connection. It is the sibling of the reading above over the other side
	// of the same keyed space: that one asks whether a key the component declared
	// for itself is also delivered by a connection, this one whether two
	// connections deliver it between them — a pairing no rule was invoked over,
	// so the later delivery silently displaced the earlier and the build stayed
	// green. The keys are the ones the connections DELIVER rather than the ones
	// their authors wrote, because a delivery that reaches no consumer takes no
	// key and collides with nothing. The connection set is the graph's own, so a
	// connection an attachment projected or the Release wired is in range
	// exactly as the component's own is. The signal is read once per connection,
	// which is the granularity the traversal promises, and each finding is
	// positioned at the delivery entry the author can change.
	violations, err = constraint.RunOnEdges(
		ctx.Err,
		coordinates,
		out.GraphView,
		resolver.ConnectionInjectKeyNotDeliveredByAnotherConnection,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

	// No delivery entry on any connection reads from a key the component the
	// connection points at does not publish, evaluated once per connection the
	// composition drew together with the surface that drew it. The equivalent
	// reading earlier in the build is answered from the Aggregate's own
	// dependency block, which holds no entry for a connection an attachment
	// contributed or the Release wired — and a rule presented no subject
	// reports exactly what a rule invoked and satisfied reports, so the same
	// authoring mistake halted on one surface and reached rendered output on the
	// others. The delivery it reads is the one the author wrote rather than the
	// one the graph stores, because the graph stores a literal and a reference to
	// a published key in the same shape. The signal is read once per connection,
	// which is the granularity the traversal promises.
	violations, err = constraint.RunOnOriginatedConnections(
		ctx.Err,
		coordinates,
		out.GraphView,
		resolver.OriginatedConnectionInjectReferenceIsPublished,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

	// No delivery entry on any connection carries a literal, evaluated over the
	// same set and for the same reason as the reading above: a connection delivers
	// what its target publishes, and a literal written on one — on whichever
	// surface drew it — delivered nothing and said so nowhere. It reads the
	// delivery the author wrote, which is the only form that still tells a literal
	// from a reference.
	violations, err = constraint.RunOnOriginatedConnections(
		ctx.Err,
		coordinates,
		out.GraphView,
		resolver.OriginatedConnectionInjectEntryNamesAKey,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

	// No connection dictates a delivery mechanism the socket it is wired into
	// does not permit, evaluated once per connection the composition drew
	// together with the surface that drew it. The socket is read on whichever
	// entity declared it, and which entity that is follows from the surface: a
	// connection an attachment drew is wired into a socket the attached Feature
	// declares, which no reading keyed by the consuming component could answer
	// for — so a rule asked once per node, socket and alias would approve every
	// attachment-drawn dictation without reading the declaration that governs
	// it. The delivery it reads is the one the author wrote rather than the one
	// the graph stores, because the conversion into the graph drops a payload it
	// cannot express as one string per delivery key. The signal is read once per
	// connection, which is the granularity the traversal promises.
	violations, err = constraint.RunOnOriginatedConnections(
		ctx.Err,
		coordinates,
		out.GraphView,
		ir.OriginatedConnectionDeliveryIsSocketPermitted,
	)
	if err != nil {
		return err
	}
	out.Violations = append(out.Violations, violations...)

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
	out.Advisories = append(out.Advisories, advisories...)

	return runManifestNameMappingRules(out)
}

// graphStageMappingEntity is the entity a finding about one of the Manifest's
// mapping entries is reported against: the Manifest, named as an entity
// reference names one, because the remedy is an edit to its mapping table. The
// kind decides the fault category a caller routes on, so it is the kind the
// author of that table owns.
const graphStageMappingEntity = "Manifest"

// runManifestNameMappingRules invokes the rules declared over the Manifest's own
// mapping entries that read the finalised graph: a name-tier key has to name a
// component instance or an attachment the build holds.
//
// They are invoked once per entry of the Manifest the build was compiled with,
// against a presentation putting that Manifest beside the graph's: the entry is a
// fact of the one document, and what it could name is complete only once the
// graph is final. A build holding no resolve result holds no Manifest to ask
// about, and nothing is asked.
//
// Both rules declare the severity that refuses only a run emitting something a
// person will apply, so what they raise is converted at this site — where the
// document the findings are about is known — and joins the slice the entries
// that decide their own posture read. Which stream a finding lands on is read
// from the rule's own declaration rather than chosen here.
//
// The components tier is asked before the features tier, so the findings arrive
// in the order the Manifest's mapping table is read.
func runManifestNameMappingRules(out *GraphScope) error {
	if out.ResolveResult == nil {
		return nil
	}
	manifest := out.ResolveResult.Manifest
	presented := resolver.PresentManifestBesideGraph(manifest, out.GraphView)
	if presented == nil {
		return fmt.Errorf(
			"graph-stage rules: the compilation produced no view of the finalised graph to present the Manifest beside")
	}
	entity := graphStageMappingEntity
	if manifest != nil && manifest.Metadata.Name != "" {
		entity += " " + manifest.Metadata.Name
	}
	document := resolver.ComposedManifestDocument
	entries := presented.ManifestRecipeMappingEntries(document)

	componentObjections := constraint.RunOnDocumentMappingEntries(
		document, entries, presented, catalog.ComponentNameMappingNamesAComponent)
	if err := recordByDeclaredSeverity(
		out, catalog.ComponentNameMappingNamesAComponent.Severity, entity, componentObjections,
	); err != nil {
		return err
	}

	featureObjections := constraint.RunOnDocumentMappingEntries(
		document, entries, presented, catalog.FeatureNameMappingNamesAnAttachment)
	return recordByDeclaredSeverity(
		out, catalog.FeatureNameMappingNamesAnAttachment.Severity, entity, featureObjections)
}

// recordByDeclaredSeverity appends what one rule raised to the stream its
// declared severity names: a violation to the slice a build gates on, an advisory
// to the warning stream, and a finding that refuses only an emitting run to the
// slice the entries deciding their own posture read, converted there under the
// entity the rule's findings are attributed to.
//
// An unknown severity is refused rather than defaulted, for the reason the stage
// before this one refuses one: a default would pick a stream for a value none of
// them names, which is a consequence nobody declared.
func recordByDeclaredSeverity(
	out *GraphScope,
	severity constraint.Severity,
	entity string,
	objections []constraint.Objection,
) error {
	if len(objections) == 0 {
		return nil
	}
	switch severity {
	case constraint.SeverityFatal:
		out.Violations = append(out.Violations, objections...)
	case constraint.SeverityWarning:
		out.Advisories = append(out.Advisories, objections...)
	case constraint.SeverityEmissionFatal:
		out.EmissionViolations = append(out.EmissionViolations,
			resolver.FindingsFromObjections(objections, entity)...)
	default:
		return fmt.Errorf(
			"graph-stage rules: a rule raised %d finding(s) at severity %q, which this site carries no stream for",
			len(objections), severity)
	}
	return nil
}

// releaseFileOf reads the Release's file off the resolve result, empty when
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

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The entity a finding from this stage is reported against.
//
// It names the kind whose declaration the finding is about rather than the
// document the locator points into, and the two genuinely differ here: the
// broken declaration is on a catalog Component and the position an author sees
// it at is the component instance that names that type. The kind is what decides
// the fault category a caller routes on, so naming the authoring surface instead
// would report a catalog fault as the Aggregate author's to fix.
const topologyStageEntity = "Component"

// The entity an identity finding from this stage is reported against, which is
// deliberately not the one above.
//
// The kind decides the fault category a caller routes on, and these rules name
// different faults. A broken presence mandate is declared on a catalog
// Component, so the remedy is in the catalog and the kind says so. An identity
// that cannot be stamped, and an identity a second component was also declared
// under, are both names their own author wrote in the Aggregate they authored,
// so reporting either as a catalog fault would send that author to a file
// holding nothing they can change — the failure a category exists to prevent.
//
// The kind is named without an instance because this stage holds no aggregate
// name to put after it: a composition carries the components a module tree
// contributed and not the documents' own metadata. The locator is what positions
// the finding, and it names the authored key either way.
const topologyStageIdentityEntity = "Aggregate"

// The entity a finding about a node's Recipe mapping is reported against, and it
// is a third kind rather than either of the two above.
//
// Every arm of the mapping rules below asks for an entry under the Manifest's own
// mapping table — written, corrected, or pinned at a version — so the remedy is
// in the Manifest whichever arm fired. The kind decides the fault category a
// caller routes on, and a category names one document: reporting these against
// the component would send the reader into the catalog, and against the
// Aggregate would send them into a file that records no mapping at all.
const topologyStageMappingEntity = "Manifest"

// topologyStageFindings accumulates what this site's rules raised, holding apart
// the findings that refuse any run from the findings that refuse only a run
// emitting something a person will apply.
//
// It exists so that which stream a finding lands on is read from the DECLARED
// severity of the rule that raised it rather than chosen by hand at each call.
// Routing by hand is how a rule comes to declare one consequence and receive
// another: both spellings compile, both read correctly, and nothing compares
// them. Here the declaration is the only input, so the two cannot disagree.
//
// 🔴 The bound, stated because an unstated one reads as full coverage: this
// holds at THIS site and at no other. Every other suite still chooses its
// stream by hand, so a rule invoked from one of them receives whatever that
// call site appends it to, whatever it declared — which is the defect above,
// still live everywhere this accumulator is not. Migrating the rest is not
// this site's to do; reading it as already done is the mistake this paragraph
// prevents.
type topologyStageFindings struct {
	// halting refuses every caller of this stage, which is what this site does
	// with a finding: it returns it, and the graph entry that reaches this stage
	// has no channel to carry one on.
	halting []*resolver.ResolverError

	// carried travels out of this stage instead, for an entry that decides
	// whether its own run is one that emits.
	carried []*resolver.ResolverError
}

// record routes what one rule raised, converting it to this boundary's finding
// shape under the entity that rule's findings are attributed to.
//
// An unknown severity is refused rather than defaulted. A default would pick one
// of the two streams for a value neither of them names, which is a consequence
// nobody declared — and picking the carried stream would silence the rule on
// every surface while picking the halting one would refuse a run the rule never
// meant to refuse.
//
// The advisory severity is among the values refused here, and that is a bound of
// this site rather than of the severity: this stage reports before the analysis
// exists, so there is no warning stream to append to. A rule declared advisory
// belongs at a stage that has one.
func (f *topologyStageFindings) record(
	severity constraint.Severity,
	entity string,
	objections []constraint.Objection,
) error {
	if len(objections) == 0 {
		return nil
	}
	switch severity {
	case constraint.SeverityFatal:
		f.halting = append(f.halting, resolver.FindingsFromObjections(objections, entity)...)
	case constraint.SeverityEmissionFatal:
		f.carried = append(f.carried, resolver.FindingsFromObjections(objections, entity)...)
	default:
		return fmt.Errorf(
			"topology-stage rules: a rule raised %d finding(s) at severity %q, which this site "+
				"carries no stream for; it reports before the analysis exists, so it holds a "+
				"halting stream and a stream for findings that refuse only a run that emits",
			len(objections), severity)
	}
	return nil
}

// runTopologyStageRules invokes the rules declared over the flattened
// composition, returning what the halting ones raised as a halt and what the
// rest raised for the caller to decide about.
//
// A rule needs a presentation for the facts it reads and an enumeration for the
// subjects it is invoked once per. The stage that flattened the composition
// produced both and carries them on its result, so a rule is asked about the
// coordinates the composition really produced and answers from the same
// flattening — as against a set assembled here, which could name coordinates the
// facts cannot answer for and would leave the rule silently uninvoked over the
// ones it can.
//
// # Why the halting findings halt here rather than joining a later stream
//
// This stage has no diagnostic slice to append to: the analysis that carries one
// is built two stages later, and the products it is built from do not exist yet.
// So a violation is returned, which is how every finding raised before the
// analysis reports — the resolve's guards and the sub-module placement rule both
// take this shape — and the build stops at the earliest failing class rather than
// carrying it forward to be reported among a mixture.
//
// The consequence is worth stating rather than discovering: a document carrying
// one of these faults reports it alone, and the findings the later stages would
// have raised for that same document are not reached. That is the shape every
// pre-analysis check already has, and it is what placing a rule at the stage its
// facts complete at buys — the author is told about the broken declaration
// before anything downstream reasons about what it declares.
//
// # Why one class of finding is handed back instead
//
// A rule declaring the emission severity says its finding refuses a run that
// emits something a person will apply and is carried by a run that only
// describes the composition. This site cannot make that distinction: it runs
// inside the one entry every surface reaches a graph through, so a halt returned
// here refuses an inspection and an acquisition exactly as it refuses a build.
// Such findings therefore travel out with the composition, and the entry that
// knows what its own run is decides.
//
// It reports the reason a walk stopped and nothing else when nothing was raised.
func runTopologyStageRules(
	ctx context.Context,
	topoResult *topology.TopologyResult,
) ([]*resolver.ResolverError, error) {
	// A build holding no resolve result produces no presentation of this stage,
	// because the earlier stage's facts are answered from the documents that
	// result carries. There is nothing to ask a rule, and nothing to report: the
	// caller supplied no composition rather than a faulty one.
	if topoResult == nil || topoResult.StageView == nil || topoResult.StageCoordinates == nil {
		return nil, nil
	}
	var found topologyStageFindings

	// Each presence mandate a node's component type declares is well formed.
	// An annotation value is an opaque string that nothing upstream has
	// structurally checked, so a mandate whose declaration cannot be evaluated is
	// reported as broken rather than tested and reported as unsatisfied — the two
	// have different remediations. The rule reads the mandate set a component type
	// declares and the type the flattened node carries, both complete here, which
	// is why it is asked at this stage and not at the one its sibling is asked at.
	// The cancellation signal is the caller's own, so a long walk over a large
	// composition stops within one node of the caller asking it to.
	objections, err := constraint.RunOnNodes(
		ctx.Err,
		topoResult.StageCoordinates,
		topoResult.StageView,
		analyzer.AttachmentRequirementWellFormed,
	)
	if err != nil {
		return nil, err
	}
	if err := found.record(
		analyzer.AttachmentRequirementWellFormed.Severity, topologyStageEntity, objections,
	); err != nil {
		return nil, err
	}

	// Every node's identity can be the name its emitted resources are stamped
	// with. The identity read is the flattened one, which is the value actually
	// stamped and the only form in which two acceptable authored names can join
	// into an unacceptable identity — so the rule is asked here, where the
	// composition has produced it, rather than over the authored keys, which
	// complete earlier and are not what reaches an output.
	//
	// 🔴 Unlike the two rules around it, this one reads no fact from the view it is
	// handed: its whole input is the identity its subject carries. So what places it
	// is the ENUMERATION rather than the declaration — this stage's coordinates,
	// which are the flattened composition's nodes and the only enumeration carrying
	// a module-contributed component's qualified identity. Moving this call to a
	// stage whose coordinates are the authored keys would leave the rule invoked,
	// green, and silently blind to the case it exists for.
	identityObjections, err := constraint.RunOnNodes(
		ctx.Err,
		topoResult.StageCoordinates,
		topoResult.StageView,
		analyzer.ComponentIdentityIsUsable,
	)
	if err != nil {
		return nil, err
	}
	if err := found.record(
		analyzer.ComponentIdentityIsUsable.Severity, topologyStageIdentityEntity, identityObjections,
	); err != nil {
		return nil, err
	}

	// Every identity in the composition was declared by one component instance.
	// A subsystem contributes nothing to the identity a component carries, so two
	// subsystems of one Aggregate may each declare a component under one name and
	// the two then answer to one identity — a pair the node set cannot show,
	// because it holds one component per identity and the second simply replaced
	// the first. The rule reads the declarations the flattening recorded instead,
	// and is asked here because that is where a module's own subsystems have been
	// read: asked over one authored document it would never see them.
	identityDeclarationObjections, err := constraint.RunOnNodes(
		ctx.Err,
		topoResult.StageCoordinates,
		topoResult.StageView,
		topology.ComponentIdentityIsDeclaredOnce,
	)
	if err != nil {
		return nil, err
	}
	if err := found.record(
		topology.ComponentIdentityIsDeclaredOnce.Severity,
		topologyStageIdentityEntity,
		identityDeclarationObjections,
	); err != nil {
		return nil, err
	}

	// Every dependency socket a component type declares as one that must carry a
	// connection does carry one. Three surfaces can populate such a socket and
	// the rule reads all three; the last of them is the connection set the
	// composition produced, which is why the rule is asked here — it is the
	// earliest point at which every surface that can satisfy the requirement has
	// contributed. Asked over every node the composition holds, so a component a
	// module contributed is judged by the same rule as one the Aggregate
	// declared directly.
	socketObjections, err := constraint.RunOnNodeSockets(
		ctx.Err,
		topoResult.StageCoordinates,
		topoResult.StageCoordinates,
		topoResult.StageView,
		resolver.RequiredSocketIsPopulated,
	)
	if err != nil {
		return nil, err
	}
	if err := found.record(
		resolver.RequiredSocketIsPopulated.Severity, topologyStageEntity, socketObjections,
	); err != nil {
		return nil, err
	}

	// Every component instance the composition holds resolves to a Recipe the
	// catalog holds, through an explicit entry in the Manifest's mapping table.
	// The node set it walks has to be complete, and a component a sub-aggregate
	// contributes joins that set only when the composition is flattened — asked
	// over the Aggregate's own instance list the rule would never be invoked for
	// one, and a rule that is never invoked reports exactly what a rule that ran
	// and approved reports. So this is the earliest stage that can ask it, and the
	// enumeration here is the flattened composition's own.
	//
	// It declares the emission severity, so what it raises is handed back rather
	// than halting every caller of this stage.
	resolutionObjections, err := constraint.RunOnNodes(
		ctx.Err,
		topoResult.StageCoordinates,
		topoResult.StageView,
		catalog.NodeResolvesToARecipe,
	)
	if err != nil {
		return nil, err
	}
	if err := found.record(
		catalog.NodeResolvesToARecipe.Severity, topologyStageMappingEntity, resolutionObjections,
	); err != nil {
		return nil, err
	}

	// No component instance is mapped to a Recipe reference whose version pin is
	// none of the forms a pin takes. It is the sibling of the rule above over one
	// outcome that rule passes over: such a reference resolves to nothing, and
	// reporting it as a Recipe the catalog does not hold would send the author
	// looking for a Recipe when the fault is the pin's text. It reads the same
	// facts over the same enumeration, so a module-contributed component is judged
	// as one the Aggregate declares, and it is recorded under the same entity
	// because the remedy is the same document's: the mapping value in the Manifest.
	pinObjections, err := constraint.RunOnNodes(
		ctx.Err,
		topoResult.StageCoordinates,
		topoResult.StageView,
		catalog.NodeMappedRecipePinIsWellFormed,
	)
	if err != nil {
		return nil, err
	}
	if err := found.record(
		catalog.NodeMappedRecipePinIsWellFormed.Severity, topologyStageMappingEntity, pinObjections,
	); err != nil {
		return nil, err
	}

	// No component instance's type matches several version-distinct mapping keys
	// with none matching it exactly. It is a sibling of the resolution rule above
	// over another outcome that rule passes over: a refused lookup names no
	// reference, so it reads there as an instance nothing maps, and the two faults
	// have opposite remedies. It is asked once per instance rather than once per
	// mapping entry because a table holding one type at two versions is legal
	// authoring — an exact key match wins first, so the same table resolves a
	// pinned instance and refuses an unpinned one.
	ambiguityObjections, err := constraint.RunOnNodes(
		ctx.Err,
		topoResult.StageCoordinates,
		topoResult.StageView,
		catalog.NodeTypeMappingIsUnambiguous,
	)
	if err != nil {
		return nil, err
	}
	if err := found.record(
		catalog.NodeTypeMappingIsUnambiguous.Severity, topologyStageMappingEntity, ambiguityObjections,
	); err != nil {
		return nil, err
	}

	// No component instance reached through a mapping key that names no version
	// renders through a Recipe whose spec.implements its own type version does not
	// satisfy. Such a key matches every version of its type, so the rule over the
	// mapping entry can only judge the Recipe against the type; which version it
	// has to serve is the instance's, and the key the lookup settled on for the
	// instance is known only here, where the instance set is complete. It halts
	// at the severity the rule over the entry declares, and is recorded under the
	// Manifest because the remedy is a mapping entry.
	versionObjections, err := constraint.RunOnNodes(
		ctx.Err,
		topoResult.StageCoordinates,
		topoResult.StageView,
		resolver.NodeMappedRecipeImplementsTheNodeVersion,
	)
	if err != nil {
		return nil, err
	}
	if err := found.record(
		resolver.NodeMappedRecipeImplementsTheNodeVersion.Severity, topologyStageMappingEntity, versionObjections,
	); err != nil {
		return nil, err
	}

	// One halt carrying every halting rule's findings, rule by rule. A document
	// offending against more than one is reported for all of them rather than for
	// whichever rule ran first: they are asked at one stage, so there is no later
	// pass that would report the rest, and halting on the first would make the
	// author fix one fault to discover the next.
	if len(found.halting) == 0 {
		return found.carried, nil
	}
	return found.carried, &resolver.MultiResolverError{
		Entity:     topologyStageEntity,
		Summary:    fmt.Sprintf("%d violation(s) over the composed topology", len(found.halting)),
		Violations: found.halting,
	}
}

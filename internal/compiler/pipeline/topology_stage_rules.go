// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The entity a finding from this stage is reported against.
//
// It names the kind whose declaration the finding is about rather than the
// document the locator points into, and the two genuinely differ here: the
// broken declaration is on a catalog Component and the position an author sees
// it at is the component instance that names that type. The kind is what decides
// the fault category a caller routes on, so naming the authoring surface instead
// would report a catalog fault as the deployment author's to fix.
const topologyStageEntity = "Component"

// runTopologyStageRules invokes the rules declared over the flattened
// composition and returns what they raised as a halt.
//
// A rule needs a presentation for the facts it reads and an enumeration for the
// subjects it is invoked once per. The stage that flattened the composition
// produced both and carries them on its result, so a rule is asked about the
// coordinates the composition really produced and answers from the same
// flattening — as against a set assembled here, which could name coordinates the
// facts cannot answer for and would leave the rule silently uninvoked over the
// ones it can.
//
// # Why the findings halt here rather than joining a later stream
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
// It reports the reason a walk stopped and nothing else when nothing was raised.
func runTopologyStageRules(
	ctx context.Context,
	topoResult *topology.TopologyResult,
) error {
	// A build holding no resolve result produces no presentation of this stage,
	// because the earlier stage's facts are answered from the documents that
	// result carries. There is nothing to ask a rule, and nothing to report: the
	// caller supplied no composition rather than a faulty one.
	if topoResult == nil || topoResult.StageView == nil || topoResult.StageCoordinates == nil {
		return nil
	}

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
		return err
	}

	// Every dependency socket a component type declares as one that must carry a
	// connection does carry one. Three surfaces can populate such a socket and
	// the rule reads all three; the last of them is the connection set the
	// composition produced, which is why the rule is asked here — it is the
	// earliest point at which every surface that can satisfy the requirement has
	// contributed. Asked over every node the composition holds, so a component a
	// module contributed is judged by the same rule as one the application
	// declared directly.
	socketObjections, err := constraint.RunOnNodeSockets(
		ctx.Err,
		topoResult.StageCoordinates,
		topoResult.StageCoordinates,
		topoResult.StageView,
		resolver.RequiredSocketIsPopulated,
	)
	if err != nil {
		return err
	}

	// One halt carrying both rules' findings, rule by rule. A document offending
	// against both is reported for both rather than for whichever rule ran first:
	// the two are asked at one stage, so there is no later pass that would report
	// the second, and halting on the first would make the author fix one fault to
	// discover the other.
	violations := append(
		resolver.FindingsFromObjections(objections, topologyStageEntity),
		resolver.FindingsFromObjections(socketObjections, topologyStageEntity)...,
	)
	if len(violations) == 0 {
		return nil
	}
	return &resolver.MultiResolverError{
		Entity:     topologyStageEntity,
		Summary:    fmt.Sprintf("%d violation(s) over the composed application", len(violations)),
		Violations: violations,
	}
}

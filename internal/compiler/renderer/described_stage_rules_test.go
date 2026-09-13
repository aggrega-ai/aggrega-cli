// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the site that invokes the rules declared over
// what a build's plugins report about themselves: that it observes the caller's
// cancellation signal per iteration rather than at the end, that it renders what
// the rules raised as a halt carrying each finding's own position, and that it
// asks nothing of a render given no plugin resolution.

// The cancellation fixture. Three components, each carrying two steps of its own
// Recipe pipeline, so a walk stopped after the first component's turn is a
// different number from a walk that ran to the end. Every step is refused, so a
// completed walk raises six objections and a stopped one raises none.
const cancelNodeCount, cancelStepsPerNode = 3, 2

// cancelAxes presents the fixture's nodes and their steps, and nothing else: the
// attachment and boundary axes answer empty, so the walk under observation is the
// first of the four and the withdrawal it sees is its own.
type cancelAxes struct{}

func (cancelAxes) Nodes() []constraint.NodeInstance {
	out := make([]constraint.NodeInstance, 0, cancelNodeCount)
	for _, id := range cancelNodeIDs() {
		out = append(out, constraint.NodeInstance{ID: id, SourceFile: "aggregate.yaml", AuthoredName: id})
	}
	return out
}

func (cancelAxes) PipelineSteps(_ string) []constraint.AuthoredCoordinate {
	out := make([]constraint.AuthoredCoordinate, 0, cancelStepsPerNode)
	for _, step := range cancelStepIDs() {
		out = append(out, constraint.AuthoredCoordinate{Value: step, SourceFile: "recipes/node.yaml"})
	}
	return out
}

func (cancelAxes) Attachments(_ string) []string { return nil }

func (cancelAxes) AttachmentPipelineSteps(_ string, _ string) []constraint.AuthoredCoordinate {
	return nil
}

func (cancelAxes) Boundaries() []constraint.AuthoredCoordinate { return nil }

func (cancelAxes) BoundaryAttachments(_ string) []string { return nil }

func (cancelAxes) BoundaryAttachmentPipelineSteps(_ string, _ string) []constraint.AuthoredCoordinate {
	return nil
}

func cancelNodeIDs() []string { return []string{"node-a", "node-b", "node-c"} }
func cancelStepIDs() []string { return []string{"first", "second"} }

// refusingFacts answers every step's declared targets with a set that names no
// provider, so every subject the walk reaches is refused. It counts the reads so a
// stopped walk is measured by the subjects it reached rather than by the error it
// returned.
//
// It embeds the presentation interface with no value behind it, so a rule reading
// a fact these proofs do not supply panics rather than answering a zero value.
type refusingFacts struct {
	constraint.DescribedView

	reads    *int
	withdraw func()
	// triggerAt is the 1-based read at which the caller withdraws, or 0 for a
	// caller that never does.
	triggerAt int
}

func (f refusingFacts) StepExportScope(_ string, _ string) string { return types.ScopeOwn }

func (f refusingFacts) NodeRenderingProvider(_ string) string { return "helm" }

func (f refusingFacts) StepPluginCompatibleCapabilities(_ string, _ string) []string {
	*f.reads++
	if f.triggerAt != 0 && *f.reads == f.triggerAt && f.withdraw != nil {
		f.withdraw()
	}
	return nil
}

// TestTheDescribedStageWalkStopsOnTheIterationAfterTheSignalTurns proves the
// invocation observes the caller's signal per iteration rather than reporting a
// withdrawal after a completed walk.
//
// # The trigger index is derived from THIS walk's order, not carried
//
// The walk over a node's own pipeline is outermost by node and inner by step, and
// the signal is read once per node — as the first statement of the outer loop's
// body. So a withdrawal inside the FIRST node's first step read is seen at the
// second node's signal read: the first node's remaining steps are evaluated
// (they are inside the iteration already entered) and no later node is entered at
// all. The reached count is therefore exactly one node's worth of steps.
//
// # Why the count and not the error
//
// A walk that ran to the end and reported the withdrawal afterwards returns the
// same error as one that stopped partway. The exact equality below is what
// separates them, and the control beside it is what stops the pair passing on a
// walk that reaches nothing.
func TestTheDescribedStageWalkStopsOnTheIterationAfterTheSignalTurns(t *testing.T) {
	t.Parallel()

	total := cancelNodeCount * cancelStepsPerNode
	if cancelNodeCount < 2 || cancelStepsPerNode < 1 {
		t.Fatalf("the fixture holds %d node(s) of %d step(s); one node's worth and the whole walk are the same number",
			cancelNodeCount, cancelStepsPerNode)
	}

	// CONTROL — an uncancellable caller reaches every step and the rules raise one
	// objection per step. Without this the assertions below would pass on a walk
	// that reached nothing.
	controlReads := 0
	controlErr := runDescribedStageRules(
		context.Background(),
		constraint.DescribedView(refusingFacts{reads: &controlReads}),
		cancelAxes{}, true,
	)
	if controlReads != total {
		t.Fatalf("the control walk reached %d step(s) of %d; the fixture does not present what these proofs assume",
			controlReads, total)
	}
	if controlErr == nil {
		t.Fatal("the control walk raised nothing over a fixture whose every step is refused")
	}
	if errors.Is(controlErr, context.Canceled) {
		t.Fatalf("the control walk reported a withdrawal no caller made: %v", controlErr)
	}

	// THE MEASUREMENT — the caller withdraws inside the first node's first step.
	withdrawn, withdraw := context.WithCancel(context.Background())
	defer withdraw()
	reads := 0
	err := runDescribedStageRules(
		withdrawn,
		constraint.DescribedView(refusingFacts{reads: &reads, withdraw: withdraw, triggerAt: 1}),
		cancelAxes{}, true,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the walk returned %v for a caller that withdrew, want the withdrawal", err)
	}
	if reads != cancelStepsPerNode {
		t.Errorf("the walk reached %d step(s) for a caller that withdrew during the first node's first step; "+
			"want exactly %d — the first node's own steps and no later node's.\n"+
			"The exact equality is the point: a walk that reached all %d and reported the withdrawal at the "+
			"end returns the same error as one that stopped here.",
			reads, cancelStepsPerNode, total)
	}
	if reads >= total {
		t.Errorf("the walk reached every step of the enumeration (%d of %d), so it reported the "+
			"cancellation at the end rather than observing it per iteration", reads, total)
	}
	t.Logf("the walk reached %d step(s) of %d before stopping, and returned the caller's withdrawal", reads, total)
}

// TestTheDescribedStageHaltCarriesEveryFindingsOwnPosition proves the conversion
// renders each raised objection as a diagnostic of its own, positioned at the
// entity that declares the step and the authored key inside it.
//
// A single message naming several steps can be navigated to none of them, which
// is why the findings are not folded together.
func TestTheDescribedStageHaltCarriesEveryFindingsOwnPosition(t *testing.T) {
	t.Parallel()

	reads := 0
	err := runDescribedStageRules(
		context.Background(),
		constraint.DescribedView(refusingFacts{reads: &reads}),
		cancelAxes{}, true,
	)
	if err == nil {
		t.Fatal("the rules raised nothing over a fixture whose every step is refused")
	}
	if !errors.Is(err, ErrPluginProviderIncompatible()) {
		t.Errorf("the halt does not carry the incompatibility condition: %v", err)
	}

	// The envelope carries the joined set rather than one member, which is the
	// shape a display surface renders every member of. Reading the position off
	// each member is the assertion: an envelope's own Error text is its message
	// alone, so a string search over it would report nothing whichever shape the
	// chain has.
	var envelope *cerrors.CompilerError
	if !errors.As(err, &envelope) {
		t.Fatalf("the halt is not a categorised diagnostic: %v", err)
	}
	joined, isMulti := envelope.Unwrap().(interface{ Unwrap() []error })
	if !isMulti {
		t.Fatalf("the envelope wraps a single error rather than the set the rules raised: %T", envelope.Unwrap())
	}
	members := joined.Unwrap()
	if got, want := len(members), cancelNodeCount*cancelStepsPerNode; got != want {
		t.Fatalf("the envelope carries %d finding(s); want %d — one per refused step", got, want)
	}

	positions := map[string]int{}
	for _, member := range members {
		var finding *cerrors.CompilerError
		if !errors.As(member, &finding) {
			t.Fatalf("a finding is not a categorised diagnostic: %v", member)
		}
		if finding.Source().File != "recipes/node.yaml" {
			t.Errorf("finding positioned in %q, want the entity that declares the step", finding.Source().File)
		}
		positions[finding.Source().Locator]++
	}
	for _, step := range cancelStepIDs() {
		locator := ".spec.pipeline." + step
		if got, want := positions[locator], cancelNodeCount; got != want {
			t.Errorf("the halt positions %d finding(s) at %q; want %d — one per component declaring that step",
				got, locator, want)
		}
	}
}

// TestTheDescribedStageRulesAreNotInvokedWithoutPluginResolution proves a render
// given no plugin resolution is asked nothing.
//
// Such a render validates each pipeline by its presence and runs none of it, and
// no plugin in it is resolved, loaded or described — so the presentation answers
// every plugin fact from nothing, which is indistinguishable from a plugin that
// declares nothing. Invoking rules that read those facts there would refuse every
// step of a configuration that renders today.
func TestTheDescribedStageRulesAreNotInvokedWithoutPluginResolution(t *testing.T) {
	t.Parallel()

	reads := 0
	if err := runDescribedStageRules(
		context.Background(),
		constraint.DescribedView(refusingFacts{reads: &reads}),
		cancelAxes{}, false,
	); err != nil {
		t.Errorf("the rules were invoked over a render with no plugin resolution: %v", err)
	}
	if reads != 0 {
		t.Errorf("the rules read %d plugin fact(s) from a render that resolved no plugin", reads)
	}
}

// The fixture for the two conditions a zero-valued plugin fact conflates. One
// component, two steps naming two plugins, so a walk order exists and the read a
// refusal is recorded at is the earlier of two rather than the only one.
type unaskedPluginAxes struct{ cancelAxes }

func (unaskedPluginAxes) Nodes() []constraint.NodeInstance {
	return []constraint.NodeInstance{{ID: "svc", SourceFile: "aggregate.yaml", AuthoredName: "svc"}}
}

func (unaskedPluginAxes) PipelineSteps(_ string) []constraint.AuthoredCoordinate {
	return []constraint.AuthoredCoordinate{
		{Value: "patch", SourceFile: "recipes/svc.yaml"},
		{Value: "stamp", SourceFile: "recipes/svc.yaml"},
	}
}

// unaskedPluginView presents the fixture's one component through the real
// presentation, with the plugin self-report read replaced by a canned one. Every
// other product is the real thing, so the accessor under observation, the rules,
// and the invocation are all production code.
//
// The lookup is present but never consulted: the presentation reads it only to
// tell a render that resolves no plugin at all from one that does, and a canned
// self-report read stands in for the resolution itself.
func unaskedPluginView(describe StepDescriber) *describedView {
	return &describedView{
		ctx: context.Background(),
		recipes: map[string]*types.Recipe{
			"svc": {Spec: types.RecipeSpec{
				Capability: "helm:1.0.0",
				Pipeline: []types.PipelineStep{
					{ID: "patch", Plugin: "pipeline-step/patch-server:1.0.0", Function: "step"},
					{ID: "stamp", Plugin: "pipeline-step/stamper:2.0.0", Function: "step"},
				},
			}},
		},
		describe: describe,
		lookup: func(string) (pluginpath.ResolvedPlugin, error) {
			return pluginpath.ResolvedPlugin{}, nil
		},
	}
}

// TestAPluginTheHostCouldNotAskIsNotAPluginThatDeclaredNothing holds the two
// conditions apart in one place, because holding them apart is the whole
// property and a proof of either alone leaves them free to be conflated again.
//
// A fact accessor returns a value and never an error, so a plugin the host could
// not resolve, load or describe is answered with an empty compatible-provider
// list — the same answer a plugin that declared an empty list gives. One of those
// is a claim an author made and the other is the absence of any claim, and the
// build owes a different halt for each:
//
//   - asked and not answered: the host's own failure, which is what there is to
//     repair;
//   - answered with nothing: the incompatibility, because an empty list matches
//     no provider.
//
// Asserting only that each arm returns AN error would pass on the regression this
// proof exists for, where the second halt was reported for the first condition.
// So both arms assert the identity of what came back, and each asserts the
// other's halt did NOT.
func TestAPluginTheHostCouldNotAskIsNotAPluginThatDeclaredNothing(t *testing.T) {
	t.Parallel()

	// ASKED AND NOT ANSWERED. The read fails for every step, so the recorded
	// refusal is the first step's in walk order.
	unreachable := errors.New("resolve pipeline-step/patch-server:1.0.0: module closed with context deadline exceeded")
	reads := 0
	refused := runDescribedStageRules(
		context.Background(),
		unaskedPluginView(func(context.Context, types.PipelineStep) (wasm.PluginDescriptor, error) {
			reads++
			return wasm.PluginDescriptor{}, unreachable
		}),
		unaskedPluginAxes{}, true,
	)
	if refused == nil {
		t.Fatal("a step whose plugin the host could not ask was admitted")
	}
	if !errors.Is(refused, unreachable) {
		t.Errorf("the halt does not carry the failure the host met: %v", refused)
	}
	if errors.Is(refused, ErrPluginProviderIncompatible()) {
		t.Errorf("a plugin the host could not ask was reported as declaring no compatible provider, "+
			"which is a claim its author never made: %v", refused)
	}
	// The position is the authored step and the plugin it names: the fault is in
	// the plugin the step reaches, not in a key of the document that reaches it.
	var reported *cerrors.CompilerError
	if !errors.As(refused, &reported) {
		t.Fatalf("the halt is not a categorised diagnostic: %v", refused)
	}
	for _, want := range []string{`"patch"`, `"pipeline-step/patch-server:1.0.0"`} {
		if !strings.Contains(reported.Message(), want) {
			t.Errorf("the halt does not name %s: %q", want, reported.Message())
		}
	}
	if strings.Contains(reported.Message(), "stamper") {
		t.Errorf("the halt names a later step's plugin as well as the first: %q", reported.Message())
	}
	// ANTI-VACUITY. A walk that asked nothing would also raise nothing to
	// conflate, so the reads are counted rather than assumed.
	if reads == 0 {
		t.Error("the rules asked no plugin what it declares, so neither arm below is about anything")
	}

	// ANSWERED WITH NOTHING. The read succeeds and the plugin declares an empty
	// compatible-provider list, which matches no provider — so the step is still
	// refused as incompatible, and that behaviour is not what the arm above
	// changed.
	declaredNothing := runDescribedStageRules(
		context.Background(),
		unaskedPluginView(func(context.Context, types.PipelineStep) (wasm.PluginDescriptor, error) {
			return wasm.PluginDescriptor{Capabilities: nil}, nil
		}),
		unaskedPluginAxes{}, true,
	)
	if declaredNothing == nil {
		t.Fatal("a step whose plugin declares no compatible provider was admitted")
	}
	if !errors.Is(declaredNothing, ErrPluginProviderIncompatible()) {
		t.Errorf("a plugin declaring an empty compatible set did not halt as incompatible: %v", declaredNothing)
	}
	if errors.Is(declaredNothing, unreachable) {
		t.Errorf("a plugin that answered was reported as one the host could not ask: %v", declaredNothing)
	}

	// The two halts are different halts, stated as its own assertion so a future
	// change that made one of them stand for both fails here.
	if refused.Error() == declaredNothing.Error() {
		t.Errorf("both conditions produced one halt (%q), so the build no longer tells them apart", refused.Error())
	}
	t.Logf("asked and not answered → %v", refused)
	t.Logf("answered with nothing  → %v", declaredNothing)
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the rule about a presence mandate whose
// declaration cannot be evaluated. It is the sibling of the rule beside it and
// runs at a different point: its facts are complete once the composition is
// flattened, which is earlier than the finalised graph its sibling reads, so it
// is invoked where that flattening's own presentation is in hand.
//
// The catalog splice is shared with the sibling's proofs rather than copied —
// see catalogDeclaringMandates. The two rules need the same splice and differ
// only in what the annotation says.

// The two shapes of a declaration that cannot be evaluated: a value that is not
// a Feature type reference, and a key carrying no label at all. Both are
// declared on both entities so every count assertion below has two members.
const (
	malformedMandateValue = "    " + mandateKey + ": not a type reference\n"
	labellessMandateKey   = "    features.aggrega.ai/requiredType: " + mandateType + "\n"
)

// compileMalformedWorkspace drives the real pre-render pipeline over the fixture
// against a catalog carrying the supplied annotations, and hands the caller the
// error instead of failing on it.
//
// It exists because the halt is the thing under proof: this rule reports before
// the analysis, so the shared helper's "a compilation that reached its outputs"
// contract is exactly what a proof here must not assume.
func compileMalformedWorkspace(
	t *testing.T,
	annotationsBody string,
	aggregate []byte,
) (*stepsOneToFourOutput, resolver.CatalogLookup, error) {
	t.Helper()
	rel, err := resolver.LoadRelease(invocationReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(invocationManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	cat, err := catalog.LoadAll("", catalogDeclaringMandates(t, annotationsBody))
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}
	var captured *stepsOneToFourOutput
	runErr := runStepsOneToFour(
		context.Background(),
		Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggregate,
			AggregatePath:     invocationAggregatePath,
			ReleasePath:       invocationReleasePath,
			WorkspaceConfig:   defaultWorkspaceConfigForTest(),
			PluginPins:        pluginpath.NoPins(),
			CompilerVersion:   invocationVersion,
		},
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(manifest).Recipe,
		cat.SubAggregateLoader(),
		func(out *stepsOneToFourOutput) error { captured = out; return nil },
	)
	return captured, cat.CatalogLookup(), runErr
}

// malformedViolations pulls the per-violation slice out of the halt, failing
// when the halt is not the one this rule raises.
//
// It reads the structured slice rather than the summary text, because the field
// a front end positions on is carried per violation and the summary only counts
// them.
func malformedViolations(t *testing.T, err error) []*resolver.ResolverError {
	t.Helper()
	if err == nil {
		t.Fatal("a catalog declaring a mandate that cannot be evaluated did not halt the build")
	}
	if !errors.Is(err, analyzer.ErrMalformedAttachmentRequirement()) {
		t.Fatalf("the halt does not carry the malformed-attachment-requirement sentinel: %v", err)
	}
	var multi *resolver.MultiResolverError
	if !errors.As(err, &multi) {
		t.Fatalf("the halt must carry its violations; got %T: %v", err, err)
	}
	return multi.Violations
}

// TestTheRequirementShapeRuleHaltsThroughTheViolationChannel is the
// invocation-and-content proof.
//
// Both arms of the fault are compiled, because the two are reached by different
// branches of the shape reading and a proof over one says nothing about the
// other. The clean leg is what stops either arm passing on a rule that reports
// unconditionally.
func TestTheRequirementShapeRuleHaltsThroughTheViolationChannel(t *testing.T) {
	t.Parallel()

	arms := []struct {
		name        string
		annotations string
		wantKey     string
		wantReason  string
	}{
		{
			name:        "a value that is not a Feature type reference",
			annotations: malformedMandateValue,
			wantKey:     mandateKey,
			wantReason:  "Feature type URI",
		},
		{
			name:        "a key carrying no label",
			annotations: labellessMandateKey,
			wantKey:     "features.aggrega.ai/requiredType",
			wantReason:  "needs a label",
		},
	}

	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := compileMalformedWorkspace(t, arm.annotations, readAggregate(t))
			violations := malformedViolations(t, err)

			// Two offences rather than one: a reading that returned on the first
			// would pass a single-offence document.
			if len(violations) != 2 {
				t.Fatalf("violations = %d, want 2 (one per component whose type declares the broken mandate): %+v",
					len(violations), violations)
			}
			wantLocators := []string{".spec.components.backend", ".spec.components.frontend"}
			wantTypes := []string{"core/compute/api-server", "core/compute/web-ui"}
			for i, violation := range violations {
				if violation.Field != wantLocators[i] {
					t.Errorf("violation %d locator = %q, want %q", i, violation.Field, wantLocators[i])
				}
				// The declaring type, the key and the reason are all in the text,
				// because the remedy is in the catalog entity and the locator
				// names the component instance that uses it rather than the
				// entity itself.
				for _, want := range []string{wantTypes[i], arm.wantKey, arm.wantReason} {
					if !strings.Contains(violation.Message, want) {
						t.Errorf("violation %d message does not name %q: %q", i, want, violation.Message)
					}
				}
				// The fault is the catalog author's, not the Aggregate author's,
				// and the entity is what carries that to the conversion.
				converted := violation.ToCompilerError()
				if converted.Category() != cerrors.CatalogError {
					t.Errorf("violation %d category = %v, want %v", i, converted.Category(), cerrors.CatalogError)
				}
			}
		})
	}

	t.Run("a well-formed mandate does not reach this rule", func(t *testing.T) {
		t.Parallel()
		// The same document, differing only in that the declaration parses. It
		// offends the SIBLING rule at the later point, which is what makes this
		// the control for this rule rather than for both: the build gets past the
		// flattening.
		out, _, err := compileMalformedWorkspace(t, unsatisfiedMandate, readAggregate(t))
		if err != nil {
			t.Fatalf("a well-formed mandate halted the flattening: %v", err)
		}
		if out == nil {
			t.Fatal("the pipeline completed without invoking the continuation")
		}
		for _, violation := range out.Analysis.Errors {
			if errors.Is(violation.Sentinel, analyzer.ErrMalformedAttachmentRequirement()) {
				t.Errorf("a well-formed mandate reported as a broken declaration: %+v", violation)
			}
		}
	})
}

// countingRequirementShapeRule is the declared rule with a counter around its
// predicate, so a proof can assert what the rule was ASKED about rather than
// only what it raised.
func countingRequirementShapeRule(
	visited *[]string,
) constraint.Check[constraint.TopologyView, constraint.NodeSubject] {
	rule := analyzer.AttachmentRequirementWellFormed
	return constraint.Check[constraint.TopologyView, constraint.NodeSubject]{
		Sentinel: rule.Sentinel,
		Severity: rule.Severity,
		Run: func(v constraint.TopologyView, s constraint.NodeSubject) []constraint.Objection {
			*visited = append(*visited, s.NodeID())
			return rule.Run(v, s)
		},
	}
}

// TestTheRequirementShapeRuleIsInvokedOncePerNodeTheCompositionHolds is the
// named enumeration assertion, in the two halves the sibling's has and for the
// same reason.
//
// The AXIS half runs the traversal itself over the presentation the flattening
// produced, and establishes what that stage's enumeration holds. It says nothing
// about what the BUILD handed the rule, so the PRODUCTION half compares the
// halt's own violation slice against a yardstick derived by a different route —
// every node whose component type declares a mandate the shape reading rejects,
// read off the catalog entity's own annotations.
//
// A green equivalence check cannot substitute for either half: a narrowed axis
// asks both readings about fewer things, they agree, and nothing is raised.
func TestTheRequirementShapeRuleIsInvokedOncePerNodeTheCompositionHolds(t *testing.T) {
	t.Parallel()

	// The axis half compiles a clean workspace, because a workspace this rule
	// halts on never reaches the continuation the presentation is carried on.
	// What it measures — the enumeration the flattening produced — is the same
	// value either way.
	clean, catalogLookup := compileMandateWorkspace(t, unsatisfiedMandate, readAggregate(t))
	if clean.Topology == nil || clean.Topology.StageCoordinates == nil || clean.Topology.StageView == nil {
		t.Fatal("the flattening produced no view of the composition; no rule over it can be invoked")
	}

	held := clean.Topology.Nodes.Keys()
	if len(held) < 2 {
		t.Fatalf("the composition holds %d node(s); a node-axis assertion needs at least 2", len(held))
	}

	var visited []string
	if _, err := constraint.RunOnNodes(
		context.Background().Err,
		clean.Topology.StageCoordinates,
		clean.Topology.StageView,
		countingRequirementShapeRule(&visited),
	); err != nil {
		t.Fatalf("the walk over an uncancelled signal returned %v", err)
	}

	if got := len(clean.Topology.StageCoordinates.Nodes()); got != len(held) {
		t.Errorf("the enumeration yields %d node(s) where the composition holds %d", got, len(held))
	}
	if len(visited) != len(held) {
		t.Errorf("the rule was invoked %d time(s) for %d node(s) the composition holds", len(visited), len(held))
	}
	// A set difference rather than a count comparison: both sides short by one
	// still agree on the count, and the missing coordinate is what a reader needs.
	for _, node := range held {
		if !slices.Contains(visited, node) {
			t.Errorf("the rule was never invoked for the node %q the composition holds", node)
		}
	}

	// The production half, over a workspace the rule really halts on. The
	// yardstick is derived from THAT run's own catalog — a different route from
	// the declared fact the rule reads — so a narrowing on either side shows up
	// as a difference rather than as agreement.
	_, brokenCatalog, runErr := compileMalformedWorkspace(t, malformedMandateValue, readAggregate(t))
	violations := malformedViolations(t, runErr)
	_ = catalogLookup

	var wantOffending []string
	for _, node := range held {
		record, ok := clean.Topology.Nodes.Get(node)
		if !ok || record == nil {
			continue
		}
		entity, found := brokenCatalog.Component(record.ComponentType)
		if !found {
			continue
		}
		for _, mandate := range types.RequiredAttachmentTypes(entity.Metadata.Annotations) {
			if mandate.Malformed == "" {
				continue
			}
			wantOffending = append(wantOffending, ".spec.components."+constraint.AuthoredComponentName(node))
		}
	}
	if len(wantOffending) < 2 {
		t.Fatalf("the workspace carries %d broken declaration(s); the production half needs at least 2 so a reading that returned on the first would fail", len(wantOffending))
	}

	var reported []string
	for _, violation := range violations {
		reported = append(reported, violation.Field)
	}
	if len(reported) != len(wantOffending) {
		t.Errorf("the build reported %d violation(s) for %d broken declaration(s): %v",
			len(reported), len(wantOffending), reported)
	}
	for _, want := range wantOffending {
		if !slices.Contains(reported, want) {
			t.Errorf("the build reported nothing at %q, the authoring key of a component whose type declares a mandate that cannot be evaluated", want)
		}
	}
}

// TestTheRequirementShapeRuleStopsPartwayOnACancelledWalk proves the property
// the acceptance names: a cancelled walk stops, rather than running to
// completion and reporting the withdrawal afterwards.
//
// The two are indistinguishable by the returned error, so the assertion is the
// exact invocation count.
func TestTheRequirementShapeRuleStopsPartwayOnACancelledWalk(t *testing.T) {
	t.Parallel()

	clean, _ := compileMandateWorkspace(t, unsatisfiedMandate, readAggregate(t))
	if clean.Topology == nil || clean.Topology.StageCoordinates == nil {
		t.Fatal("the flattening produced no enumeration of the composition")
	}
	nodes := len(clean.Topology.StageCoordinates.Nodes())
	if nodes < 2 {
		t.Fatalf("the enumeration holds %d node(s); a stops-partway assertion needs at least 2", nodes)
	}

	t.Run("the walk stops on the node after the signal turns", func(t *testing.T) {
		var reads int
		signal := func() error {
			reads++
			if reads > 1 {
				return context.Canceled
			}
			return nil
		}
		var visited []string
		objections, err := constraint.RunOnNodes(
			signal, clean.Topology.StageCoordinates, clean.Topology.StageView, countingRequirementShapeRule(&visited))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v; want the withdrawal", err)
		}
		if len(visited) != 1 {
			t.Errorf("the rule was invoked %d time(s) after a signal that turned on the second read; want exactly 1 out of the enumeration's %d node(s)",
				len(visited), nodes)
		}
		if len(objections) != 0 {
			t.Errorf("a cancelled walk returned %d objection(s)", len(objections))
		}
	})

	t.Run("an uncancelled walk reaches every node", func(t *testing.T) {
		var visited []string
		if _, err := constraint.RunOnNodes(
			context.Background().Err, clean.Topology.StageCoordinates, clean.Topology.StageView,
			countingRequirementShapeRule(&visited)); err != nil {
			t.Fatalf("the walk over an uncancelled signal returned %v", err)
		}
		if len(visited) != nodes {
			t.Errorf("the rule was invoked %d time(s) for %d node(s)", len(visited), nodes)
		}
	})
}

// TestTheTopologyStageRulesThreadTheCallersSignalIntoTheRequirementShapeWalk
// proves that the signal EVERY walk at this site reads is the caller's own.
//
// The discriminator is how many times the signal was read and not what the
// function returned: each walk reads it once per node, so an uncancelled run
// reads it once per node per walk. A walk handed some other signal reads that
// other signal instead, which shows up as a read count short by one walk's worth
// — so the count is what makes "every walk threads the caller's signal" a
// measurement rather than a claim.
//
// 🔴 The walk count is written out below rather than derived, and the coupling is
// the assertion. Adding a rule to this site changes the expected count, and the
// edit that updates it is the moment somebody has to look at whether the new
// walk was handed the caller's signal. A test that derived the count from the
// site itself would agree with a walk that read nothing.
func TestTheTopologyStageRulesThreadTheCallersSignalIntoTheRequirementShapeWalk(t *testing.T) {
	t.Parallel()

	clean, _ := compileMandateWorkspace(t, unsatisfiedMandate, readAggregate(t))
	nodes := clean.Topology.Nodes.Len()
	if nodes < 2 {
		t.Fatalf("the composition holds %d node(s); the arithmetic below needs at least 2", nodes)
	}
	// The walks this site performs, each rooted at the node axis: the
	// attachment-requirement shape, the component-identity refusal, the
	// identity-declared-once refusal, the required-socket population, the
	// recipe-resolution refusal, the mapped-recipe pin refusal, and the
	// mapping-ambiguity refusal. Each is handed `ctx.Err` like the ones around
	// it, which is the thing this count exists to make somebody check when a
	// walk is added.
	const walks = 7
	expectedReads := nodes * walks

	t.Run("a caller that withdraws stops the walk before its first node", func(t *testing.T) {
		var reads int
		signal := countingCancellation{reads: &reads, withdrawAfter: 0}
		_, err := runTopologyStageRules(signal, clean.Topology)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v; want the caller's withdrawal", err)
		}
		// One read and no more: the walk entered its loop, read the signal, and
		// returned. A site that never reached the walk would read zero times.
		if reads != 1 {
			t.Errorf("the signal was read %d time(s) by a caller that had already withdrawn; want exactly 1 — the walk's first read", reads)
		}
	})

	t.Run("a caller that withdraws during a later walk still stops the site", func(t *testing.T) {
		// The withdrawal lands after the first walk has finished, so it can only
		// be observed by a walk that comes after it. A later walk handed some
		// other signal would read nothing here and the site would return nil.
		var seen int
		signal := countingCancellation{reads: &seen, withdrawAfter: nodes + 1}
		if _, err := runTopologyStageRules(signal, clean.Topology); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v; want the caller's withdrawal, read by a walk after the first", err)
		}
		if seen != nodes+2 {
			t.Errorf("the signal was read %d time(s); want %d — every node of the first walk, the second walk's first node, then the read at its second node that finds the withdrawal",
				seen, nodes+2)
		}
	})

	t.Run("a caller that does not withdraw reaches every node of every walk", func(t *testing.T) {
		var seen int
		signal := countingCancellation{reads: &seen, withdrawAfter: expectedReads + 1}
		if _, err := runTopologyStageRules(signal, clean.Topology); err != nil {
			t.Fatalf("a composition carrying no broken declaration returned %v", err)
		}
		// The control. It is what makes the count above a measure of walks that
		// read the caller's signal rather than of a site that returned early for
		// some other reason.
		if seen != expectedReads {
			t.Errorf("the signal was read %d time(s); want %d — %d node(s) over %d walk(s)", seen, expectedReads, nodes, walks)
		}
	})
}

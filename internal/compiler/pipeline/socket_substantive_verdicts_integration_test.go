// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package pipeline_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// These tests close the two residual substantive-verdict coverage gaps on the
// unified governance socket: a verdict DERIVED from the IR graph the guest reads,
// a verdict DERIVED from the rendered content the guest reads, and the
// label-transform -> ir-verdict chain proving a stamped label reaches the verdict
// payload. They extend the shared socket suite (loadFullstackForSockets,
// fixtureSocketLookup, captureStderr, assertArtifactsByteIdentical) rather than
// forking a harness.

// graphRuleInputs attaches the graph-reading verdict guest at the ir-verdict role
// with a required-label rule and severity.
func graphRuleInputs(t *testing.T, requiredLabel, severity string) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader) {
	t.Helper()
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{
		URI:  "proof://policy-graph-rule",
		Role: plugintype.RoleIRVerdict,
		With: &workspaceconfig.PluginRefWith{Settings: map[string]any{"required_label": requiredLabel, "severity": severity}},
	}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{
		"proof://policy-graph-rule": "policy-graph-rule-go.wasm",
	})
	return inputs, buildLookup, recipeLookup, subAgg
}

// contentRuleInputs attaches the content-reading verdict guest at the
// render-verdict role with a forbidden-token rule and severity.
func contentRuleInputs(t *testing.T, forbiddenToken, severity string) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader) {
	t.Helper()
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{
		URI:  "proof://policy-content-rule",
		Role: plugintype.RoleRenderVerdict,
		With: &workspaceconfig.PluginRefWith{Settings: map[string]any{"forbidden_token": forbiddenToken, "severity": severity}},
	}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{
		"proof://policy-content-rule": "policy-content-rule-go.wasm",
	})
	return inputs, buildLookup, recipeLookup, subAgg
}

// missingGovernanceLabel is a governance label no fixture node carries, so a
// required-label rule keyed to it is violated by every node.
const missingGovernanceLabel = "governance.example/reviewed"

// presentGovernanceLabel is a label every fixture node carries, so a
// required-label rule keyed to it is satisfied by the graph as-authored.
const presentGovernanceLabel = "component.aggrega.ai/type"

// TestGraphRuleIRVerdictDenyE2E proves an IR-stage verdict derived from the graph
// the guest reads: with a required label no node carries, the guest denies and
// the halt names the offending node and the missing label — proving the guest
// inspected the graph rather than emitting a constant.
func TestGraphRuleIRVerdictDenyE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := graphRuleInputs(t, missingGovernanceLabel, analyzer.SeverityDeny)

	_, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err == nil {
		t.Fatalf("graph-rule deny: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation) {
		t.Fatalf("graph-rule deny not errors.Is ErrPolicyViolation: %v", err)
	}
	msg := err.Error()
	// The nodes sort backend < data.main-db < frontend, so backend is the first
	// offender named — proving the guest walked the actual graph.
	if !strings.Contains(msg, `node "backend"`) {
		t.Errorf("graph-rule deny does not name the first offending node:\n%v", msg)
	}
	if !strings.Contains(msg, missingGovernanceLabel) {
		t.Errorf("graph-rule deny does not name the missing required label %q:\n%v", missingGovernanceLabel, msg)
	}
}

// TestGraphRuleIRVerdictSatisfiedE2E proves the graph rule passes when the graph
// as-authored satisfies it: every node carries the required label, so the guest
// emits no verdict and the build produces artifacts.
func TestGraphRuleIRVerdictSatisfiedE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := graphRuleInputs(t, presentGovernanceLabel, analyzer.SeverityDeny)

	res, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err != nil {
		t.Fatalf("graph-rule satisfied: unexpected halt: %v", err)
	}
	if len(res.Artifacts) == 0 {
		t.Fatalf("graph-rule satisfied build produced no artifacts")
	}
}

// TestGraphRuleIRVerdictWarnE2E proves a warn verdict from the graph rule flags
// on stderr and lets the build proceed, byte-identical across two runs.
func TestGraphRuleIRVerdictWarnE2E(t *testing.T) {
	ctx := context.Background()

	var res *pipeline.Result
	stderr := captureStderr(t, func() {
		inputs, buildLookup, recipeLookup, subAgg := graphRuleInputs(t, missingGovernanceLabel, analyzer.SeverityWarn)
		r, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
		if err != nil {
			t.Fatalf("graph-rule warn: unexpected halt: %v", err)
		}
		res = r
	})
	if res == nil || len(res.Artifacts) == 0 {
		t.Fatalf("graph-rule warn build produced no artifacts")
	}
	if !strings.Contains(stderr, "warning:") || !strings.Contains(stderr, missingGovernanceLabel) {
		t.Errorf("graph-rule warn did not flag the graph detail on stderr:\n%s", stderr)
	}

	inputs2, buildLookup2, recipeLookup2, subAgg2 := graphRuleInputs(t, missingGovernanceLabel, analyzer.SeverityWarn)
	res2, err := pipeline.RunPipeline(ctx, inputs2, buildLookup2, recipeLookup2, subAgg2, pipeline.Options{})
	if err != nil {
		t.Fatalf("graph-rule warn (second build): %v", err)
	}
	assertArtifactsByteIdentical(t, res.Artifacts, res2.Artifacts)
}

// TestGraphRuleIRVerdictAuditE2E proves an audit verdict from the graph rule
// records under the audit label and the build proceeds, byte-identical.
func TestGraphRuleIRVerdictAuditE2E(t *testing.T) {
	ctx := context.Background()

	var res *pipeline.Result
	stderr := captureStderr(t, func() {
		inputs, buildLookup, recipeLookup, subAgg := graphRuleInputs(t, missingGovernanceLabel, analyzer.SeverityAudit)
		r, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
		if err != nil {
			t.Fatalf("graph-rule audit: unexpected halt: %v", err)
		}
		res = r
	})
	if res == nil || len(res.Artifacts) == 0 {
		t.Fatalf("graph-rule audit build produced no artifacts")
	}
	if !strings.Contains(stderr, "audit:") || !strings.Contains(stderr, missingGovernanceLabel) {
		t.Errorf("graph-rule audit did not record the graph detail on stderr:\n%s", stderr)
	}

	inputs2, buildLookup2, recipeLookup2, subAgg2 := graphRuleInputs(t, missingGovernanceLabel, analyzer.SeverityAudit)
	res2, err := pipeline.RunPipeline(ctx, inputs2, buildLookup2, recipeLookup2, subAgg2, pipeline.Options{})
	if err != nil {
		t.Fatalf("graph-rule audit (second build): %v", err)
	}
	assertArtifactsByteIdentical(t, res.Artifacts, res2.Artifacts)
}

// renderContentForbiddenToken is a token every rendered manifest carries, so a
// forbidden-token rule keyed to it is violated by the rendered output.
const renderContentForbiddenToken = "apiVersion"

// TestContentRuleRenderVerdictDenyE2E proves a render-stage verdict derived from
// the rendered content the guest reads: with a token every manifest carries, the
// guest denies and the halt names the offending artifact and the token — proving
// the guest read the rendered content.
func TestContentRuleRenderVerdictDenyE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := contentRuleInputs(t, renderContentForbiddenToken, analyzer.SeverityDeny)

	_, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err == nil {
		t.Fatalf("content-rule deny: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation) {
		t.Fatalf("content-rule deny not errors.Is ErrPolicyViolation: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, renderContentForbiddenToken) {
		t.Errorf("content-rule deny does not name the forbidden token %q:\n%v", renderContentForbiddenToken, msg)
	}
	// The artifacts sort by path, so the first offender named is deterministic —
	// proving the guest read a real rendered artifact's content.
	if !strings.Contains(msg, `artifact "dist/backend/helm/app-config.yaml"`) {
		t.Errorf("content-rule deny does not name the first offending artifact:\n%v", msg)
	}
}

// TestContentRuleRenderVerdictSatisfiedE2E proves the content rule passes when no
// rendered artifact contains the forbidden token: the build proceeds and produces
// artifacts.
func TestContentRuleRenderVerdictSatisfiedE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := contentRuleInputs(t, "token-that-appears-in-no-rendered-artifact-xyz", analyzer.SeverityDeny)

	res, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err != nil {
		t.Fatalf("content-rule satisfied: unexpected halt: %v", err)
	}
	if len(res.Artifacts) == 0 {
		t.Fatalf("content-rule satisfied build produced no artifacts")
	}
}

// TestContentRuleRenderVerdictWarnE2E proves a warn verdict from the content rule
// flags on stderr and lets the build proceed, byte-identical across two runs.
func TestContentRuleRenderVerdictWarnE2E(t *testing.T) {
	ctx := context.Background()

	var res *pipeline.Result
	stderr := captureStderr(t, func() {
		inputs, buildLookup, recipeLookup, subAgg := contentRuleInputs(t, renderContentForbiddenToken, analyzer.SeverityWarn)
		r, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
		if err != nil {
			t.Fatalf("content-rule warn: unexpected halt: %v", err)
		}
		res = r
	})
	if res == nil || len(res.Artifacts) == 0 {
		t.Fatalf("content-rule warn build produced no artifacts")
	}
	if !strings.Contains(stderr, "warning:") || !strings.Contains(stderr, renderContentForbiddenToken) {
		t.Errorf("content-rule warn did not flag the content detail on stderr:\n%s", stderr)
	}

	inputs2, buildLookup2, recipeLookup2, subAgg2 := contentRuleInputs(t, renderContentForbiddenToken, analyzer.SeverityWarn)
	res2, err := pipeline.RunPipeline(ctx, inputs2, buildLookup2, recipeLookup2, subAgg2, pipeline.Options{})
	if err != nil {
		t.Fatalf("content-rule warn (second build): %v", err)
	}
	assertArtifactsByteIdentical(t, res.Artifacts, res2.Artifacts)
}

// TestContentRuleRenderVerdictAuditE2E proves an audit verdict from the content
// rule records under the audit label and the build proceeds, byte-identical.
func TestContentRuleRenderVerdictAuditE2E(t *testing.T) {
	ctx := context.Background()

	var res *pipeline.Result
	stderr := captureStderr(t, func() {
		inputs, buildLookup, recipeLookup, subAgg := contentRuleInputs(t, renderContentForbiddenToken, analyzer.SeverityAudit)
		r, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
		if err != nil {
			t.Fatalf("content-rule audit: unexpected halt: %v", err)
		}
		res = r
	})
	if res == nil || len(res.Artifacts) == 0 {
		t.Fatalf("content-rule audit build produced no artifacts")
	}
	if !strings.Contains(stderr, "audit:") || !strings.Contains(stderr, renderContentForbiddenToken) {
		t.Errorf("content-rule audit did not record the content detail on stderr:\n%s", stderr)
	}

	inputs2, buildLookup2, recipeLookup2, subAgg2 := contentRuleInputs(t, renderContentForbiddenToken, analyzer.SeverityAudit)
	res2, err := pipeline.RunPipeline(ctx, inputs2, buildLookup2, recipeLookup2, subAgg2, pipeline.Options{})
	if err != nil {
		t.Fatalf("content-rule audit (second build): %v", err)
	}
	assertArtifactsByteIdentical(t, res.Artifacts, res2.Artifacts)
}

// proofChainLabel is the label the label-transform guest stamps and the graph
// rule requires in the chain proof: the rule's verdict turns on its presence.
const proofChainLabel = "proof.aggrega/touched"

// TestLabelTransformToVerdictChainE2E proves a label a label-transform role stamps
// reaches the ir-verdict payload and drives the verdict. Two builds of the same
// input, both attaching the graph rule keyed to the stamped label:
//   - with the label-transform attached, the rule finds the label on every node
//     and the build succeeds;
//   - with the label-transform detached, the rule finds no label and denies,
//     naming the label — proving the label crossed the transform->verdict boundary.
//
// The attached build is byte-identical across two runs (determinism holds with
// both a transform and a verdict role active).
func TestLabelTransformToVerdictChainE2E(t *testing.T) {
	ctx := context.Background()

	attach := func(withLabelTransform bool) []workspaceconfig.PluginRef {
		refs := []workspaceconfig.PluginRef{{
			URI:  "proof://policy-graph-rule",
			Role: plugintype.RoleIRVerdict,
			With: &workspaceconfig.PluginRefWith{Settings: map[string]any{"required_label": proofChainLabel, "severity": analyzer.SeverityDeny}},
		}}
		if withLabelTransform {
			refs = append([]workspaceconfig.PluginRef{{URI: "proof://mutator", Role: plugintype.RoleLabelTransform}}, refs...)
		}
		return refs
	}
	lookup := map[string]string{
		"proof://policy-graph-rule": "policy-graph-rule-go.wasm",
		"proof://mutator":           "mutator-proof-go.wasm",
	}

	// Attached: the stamped label satisfies the rule; the build succeeds.
	buildAttached := func() *pipeline.Result {
		inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
		inputs.WorkspaceConfig.Plugins.Governance = attach(true)
		inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, lookup)
		res, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
		if err != nil {
			t.Fatalf("chain (label-transform attached): expected the stamped label to satisfy the rule, got halt: %v", err)
		}
		return res
	}
	res := buildAttached()
	if len(res.Artifacts) == 0 {
		t.Fatalf("chain (attached) produced no artifacts")
	}

	// Detached: no stamped label; the rule denies naming the label.
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	inputs.WorkspaceConfig.Plugins.Governance = attach(false)
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, lookup)
	_, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err == nil {
		t.Fatalf("chain (label-transform detached): expected a deny halt (no stamped label), got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation) {
		t.Fatalf("chain detached halt not errors.Is ErrPolicyViolation: %v", err)
	}
	if !strings.Contains(err.Error(), proofChainLabel) {
		t.Errorf("chain detached deny does not name the label the verdict turned on %q:\n%v", proofChainLabel, err)
	}

	// Determinism: the attached build is byte-identical across two runs.
	res2 := buildAttached()
	assertArtifactsByteIdentical(t, res.Artifacts, res2.Artifacts)
}

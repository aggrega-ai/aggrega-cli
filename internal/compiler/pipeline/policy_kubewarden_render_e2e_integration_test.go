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
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The reference Kubewarden adapter runs a require-label validating policy over
// the rendered Kubernetes manifests. Every object the fullstack fixture renders
// carries the app.kubernetes.io/name label, so a policy requiring that label
// accepts the whole render (compliant); a policy requiring a label no object
// carries rejects every object (violating).
const (
	kubewardenPresentLabel = "app.kubernetes.io/name"
	kubewardenAbsentLabel  = "example.com/team"
)

// kubewardenRenderPolicyInputs clones the fullstack inputs and attaches the
// real Kubewarden render-stage adapter at the policy socket with the given
// require-label setting, returning the inputs and the lookups RunPipeline
// consumes. The guest is built on demand from its own source under its pinned
// toolchain; the artifact is not committed.
func kubewardenRenderPolicyInputs(t *testing.T, requiredLabel string) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader) {
	t.Helper()
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{
		URI:  "proof://policy-kubewarden-render",
		Role: plugintype.RoleRenderVerdict,
		With: &workspaceconfig.PluginRefWith{Settings: map[string]any{"required_label": requiredLabel}},
	}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{
		"proof://policy-kubewarden-render": "policy-kubewarden-render-rust.wasm",
	})
	return inputs, buildLookup, recipeLookup, subAgg
}

// runKubewardenRenderPolicy builds the guest, attaches it, and runs the
// pipeline. It builds the guest first so a toolchain-absent skip lands cleanly
// before the pipeline runs.
func runKubewardenRenderPolicy(t *testing.T, requiredLabel string) (*pipeline.Result, error) {
	t.Helper()
	wasmtest.EnsureFixtureBuilt(t, "policy-kubewarden-render-rust.wasm")
	inputs, buildLookup, recipeLookup, subAgg := kubewardenRenderPolicyInputs(t, requiredLabel)
	return pipeline.RunPipeline(context.Background(), inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
}

// TestKubewardenRenderPolicyDenyHaltsE2E proves the adapter runs a real
// Kubewarden validating policy over the rendered manifests and halts the build
// when the policy rejects: requiring a label no rendered object carries makes
// the policy reject, which halts loudly carrying ErrPolicyViolation and the
// Kubewarden policy's rejection message (naming the missing label and the
// rejected object).
func TestKubewardenRenderPolicyDenyHaltsE2E(t *testing.T) {
	_, err := runKubewardenRenderPolicy(t, kubewardenAbsentLabel)
	if err == nil {
		t.Fatalf("violating render: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("violating halt not errors.Is ErrPolicyViolation: %v", err)
	}
	msg := err.Error()
	// The halt must carry the Kubewarden policy's own rejection message.
	if !strings.Contains(msg, "required label") || !strings.Contains(msg, kubewardenAbsentLabel) {
		t.Errorf("violating halt does not carry the policy rejection message for %q:\n%v", kubewardenAbsentLabel, msg)
	}
	// The message must prove the policy evaluated a real rendered object, not
	// an empty payload: the adapter prefixes each verdict with the object
	// identity (its kind), so a rejected Deployment names its kind.
	if !strings.Contains(msg, "Deployment") {
		t.Errorf("violating halt does not name a rejected rendered object kind:\n%v", msg)
	}
}

// TestKubewardenRenderPolicyCompliantProceedsE2E proves a compliant render — one
// where every object carries the required label — passes the policy: the build
// completes and produces artifacts, and a second build of identical input is
// byte-identical (the policy emits no verdict and does not perturb output).
func TestKubewardenRenderPolicyCompliantProceedsE2E(t *testing.T) {
	res, err := runKubewardenRenderPolicy(t, kubewardenPresentLabel)
	if err != nil {
		t.Fatalf("compliant render: unexpected halt: %v", err)
	}
	if res == nil || len(res.Artifacts) == 0 {
		t.Fatalf("compliant render produced no artifacts")
	}

	res2, err := runKubewardenRenderPolicy(t, kubewardenPresentLabel)
	if err != nil {
		t.Fatalf("compliant render (second build): %v", err)
	}
	assertArtifactsByteIdentical(t, res.Artifacts, res2.Artifacts)
}

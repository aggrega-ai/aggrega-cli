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
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The OPA-adapter guest embeds a policy authored in Rego and compiled to a
// WebAssembly module with OPA's own toolchain. The baked module carries the
// full OPA evaluation semantics; the adapter drives it in-guest and maps the
// decision to verdicts. The reference policy denies when the build graph holds
// more nodes than the attachment's max_nodes setting permits — a real
// graph-evaluating rule reading input.graph and data.max_nodes — so the same
// baked guest yields a deny or an allow purely by settings, without a source
// swap the baked road does not allow.

// runOpaWasmPolicy builds the OPA-adapter guest, attaches it at the policy
// socket with the given settings, and runs the pipeline, returning the build
// result and error (nil error on success). It builds the guest first so a
// toolchain-absent skip lands cleanly before the pipeline runs.
func runOpaWasmPolicy(t *testing.T, settings map[string]any) (*pipeline.Result, error) {
	t.Helper()
	wasmtest.EnsureFixtureBuilt(t, "policy-opa-wasm-rust.wasm")
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{
		URI:  "proof://policy-opa-wasm",
		Role: plugintype.RoleIRVerdict,
		With: &workspaceconfig.PluginRefWith{
			Settings: settings,
		},
	}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{
		"proof://policy-opa-wasm": "policy-opa-wasm-rust.wasm",
	})
	return pipeline.RunPipeline(context.Background(), inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
}

// runOpaWasmPolicyResult is runOpaWasmPolicy plus a fatal on error, for the
// determinism build that must succeed.
func runOpaWasmPolicyResult(t *testing.T, settings map[string]any) *pipeline.Result {
	t.Helper()
	res, err := runOpaWasmPolicy(t, settings)
	if err != nil {
		t.Fatalf("OPA-adapter policy: unexpected halt: %v", err)
	}
	return res
}

// TestOpaWasmPolicyDenyHaltsE2E proves the baked OPA policy, driven in-guest by
// the adapter, halts the build loudly when its deny rule fires: a max_nodes of
// 0 makes any non-empty graph exceed the ceiling. The halt carries
// ErrPolicyViolation and the OPA-evaluated deny message.
func TestOpaWasmPolicyDenyHaltsE2E(t *testing.T) {
	_, err := runOpaWasmPolicy(t, map[string]any{"max_nodes": 0})
	if err == nil {
		t.Fatalf("deny policy: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("deny halt not errors.Is ErrPolicyViolation: %v", err)
	}
	const wantMsg = "the build graph exceeds the permitted node count"
	if !strings.Contains(err.Error(), wantMsg) {
		t.Errorf("deny halt does not carry the OPA deny message %q:\n%v", wantMsg, err)
	}
}

// TestOpaWasmPolicyAllowProceedsE2E proves a compliant build (a max_nodes
// ceiling the graph stays under) passes the baked OPA policy and produces
// artifacts, and that a second build of identical input yields byte-identical
// artifacts with the adapter active.
func TestOpaWasmPolicyAllowProceedsE2E(t *testing.T) {
	res := runOpaWasmPolicyResult(t, map[string]any{"max_nodes": 1000})
	if res == nil || len(res.Artifacts) == 0 {
		t.Fatalf("allow policy build produced no artifacts")
	}
	res2 := runOpaWasmPolicyResult(t, map[string]any{"max_nodes": 1000})
	assertArtifactsByteIdentical(t, res.Artifacts, res2.Artifacts)
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package pipeline_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

// The reference policy guest evaluates operator-supplied Rego against the IR
// graph in-guest. These policies declare their decision rules in the package
// the guest evaluates. A populated deny set halts the build; a declared-but-
// empty deny set allows it; a rule that calls an unimplemented builtin fails
// evaluation, which the guest reports as an error rather than a verdict.
const (
	regorusDenyPolicy = `package aggrega.policy

import rego.v1

deny contains msg if {
	msg := "reference policy: build rejected"
}
`

	regorusAllowPolicy = `package aggrega.policy

import rego.v1

# The deny rule is declared but its guard never holds, so the set stays empty
# and the build proceeds. This exercises the declared-but-no-verdict path.
deny contains msg if {
	input.graph.spec.this_field_never_exists == true
	msg := "unreachable"
}
`

	// crypto.md5 is not implemented by the embedded engine. A constant argument
	// guarantees the builtin is actually invoked (an undefined argument would
	// short-circuit before the call), so evaluation fails loudly.
	regorusUnsupportedBuiltinPolicy = `package aggrega.policy

import rego.v1

deny contains msg if {
	h := crypto.md5("payload")
	msg := sprintf("digest %v", [h])
}
`

	// http.send can never return a response inside a plugin, which has no
	// network. The engine is built without it, so the call fails evaluation
	// naming the function rather than evaluating to nothing and leaving this
	// deny silently unfired. The address is one nothing listens on.
	regorusHTTPSendPolicy = `package aggrega.policy

import rego.v1

deny contains msg if {
	response := http.send({"method": "get", "url": "http://127.0.0.1:9/unreachable"})
	msg := sprintf("status %v", [response.status_code])
}
`

	// The attachment's settings object is the Rego data document, so this deny
	// fires only when the operator sets forbidden=true in settings.
	regorusSettingsPolicy = `package aggrega.policy

import rego.v1

deny contains msg if {
	data.forbidden == true
	msg := "denied by settings"
}
`
)

// regorusPolicyInputs clones the fullstack inputs and attaches the real regorus
// reference guest at the policy socket with the given Rego source and settings,
// returning the inputs and the lookups RunPipeline consumes. The guest is built
// on demand from its own source under its pinned toolchain; the ~6MB artifact
// is not committed.
func regorusPolicyInputs(t *testing.T, rego string, settings map[string]any) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader) {
	t.Helper()
	inputs, buildLookup, recipeLookup, subAgg := loadFullstackForSockets(t)
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "policy.rego")
	if err := os.WriteFile(srcPath, []byte(rego), 0o644); err != nil {
		t.Fatalf("write rego: %v", err)
	}
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{
		URI:  "proof://policy-regorus",
		Role: plugintype.RoleIRVerdict,
		With: &workspaceconfig.PluginRefWith{
			Sources:  []string{srcPath},
			Settings: settings,
		},
	}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{
		"proof://policy-regorus": "policy-regorus-rust.wasm",
	})
	return inputs, buildLookup, recipeLookup, subAgg
}

// runRegorusPolicy builds the guest, attaches it, and runs the pipeline,
// returning the build result and error (nil error on success). It builds the
// guest first so a toolchain-absent skip lands cleanly before the pipeline
// runs.
func runRegorusPolicy(t *testing.T, rego string, settings map[string]any) (*pipeline.Result, error) {
	t.Helper()
	wasmtest.EnsureFixtureBuilt(t, "policy-regorus-rust.wasm")
	inputs, buildLookup, recipeLookup, subAgg := regorusPolicyInputs(t, rego, settings)
	return runPipelineCompleted(context.Background(), inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
}

// TestRegorusPolicyDenyHaltsE2E proves a normal Rego deny policy attached via
// with.sources halts the build loudly, carrying ErrPolicyViolation and the
// guest-evaluated deny message.
func TestRegorusPolicyDenyHaltsE2E(t *testing.T) {
	_, err := runRegorusPolicy(t, regorusDenyPolicy, nil)
	if err == nil {
		t.Fatalf("deny policy: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("deny halt not errors.Is ErrPolicyViolation: %v", err)
	}
	const wantMsg = "reference policy: build rejected"
	if !strings.Contains(err.Error(), wantMsg) {
		t.Errorf("deny halt does not carry the guest deny message %q:\n%v", wantMsg, err)
	}
}

// TestRegorusPolicyAllowProceedsE2E proves a Rego policy whose deny set stays
// empty lets the build proceed to produce artifacts.
func TestRegorusPolicyAllowProceedsE2E(t *testing.T) {
	res, err := runRegorusPolicy(t, regorusAllowPolicy, nil)
	if err != nil {
		t.Fatalf("allow policy: unexpected halt: %v", err)
	}
	if res == nil || len(res.Artifacts) == 0 {
		t.Fatalf("allow policy build produced no artifacts")
	}
}

// TestRegorusPolicyUnsupportedBuiltinFaultsE2E proves a policy that calls a
// builtin the embedded engine does not implement halts the build as a BUILD
// FAULT (an invocation error naming the failed verdict_ir call), NOT as a deny
// verdict and NOT as a silent pass. This is the load-bearing distinction
// between "the engine cannot evaluate this policy" and "the policy denies".
func TestRegorusPolicyUnsupportedBuiltinFaultsE2E(t *testing.T) {
	res, err := runRegorusPolicy(t, regorusUnsupportedBuiltinPolicy, nil)
	if err == nil {
		t.Fatalf("unsupported-builtin policy: expected a halt, got nil (silent pass is the exact failure this guards)")
	}
	// It must NOT be a policy verdict: an unevaluable policy is a fault, not a halt verdict.
	if errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("unsupported-builtin halt was routed as a policy verdict (ErrPolicyViolation); it must be a build fault:\n%v", err)
	}
	// It must be the invocation-fault path: the guest returned an error from
	// verdict_ir, which the socket surfaces naming the failed call.
	if !strings.Contains(err.Error(), `call "verdict_ir"`) {
		t.Errorf("unsupported-builtin halt is not the verdict_ir-invocation fault path:\n%v", err)
	}
	if res != nil && len(res.Artifacts) > 0 {
		t.Errorf("unsupported-builtin policy produced artifacts; the build must have halted")
	}
}

// TestRegorusPolicyHTTPSendFaultsNamingItE2E proves a policy calling http.send
// halts the build as a fault naming the function — not as a deny verdict, and
// not as a silent pass with the deny rule never firing.
func TestRegorusPolicyHTTPSendFaultsNamingItE2E(t *testing.T) {
	res, err := runRegorusPolicy(t, regorusHTTPSendPolicy, nil)
	if err == nil {
		t.Fatalf("http.send policy: expected a halt, got nil (the deny rule silently not firing is the exact failure this guards)")
	}
	if errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("http.send halt was routed as a policy verdict (ErrPolicyViolation); it must be a build fault:\n%v", err)
	}
	if !strings.Contains(err.Error(), `call "verdict_ir"`) {
		t.Errorf("http.send halt is not the verdict_ir-invocation fault path:\n%v", err)
	}
	if !strings.Contains(err.Error(), "http.send") {
		t.Errorf("http.send halt does not name the function:\n%v", err)
	}
	if res != nil && len(res.Artifacts) > 0 {
		t.Errorf("http.send policy produced artifacts; the build must have halted")
	}
}

// TestRegorusPolicySettingsReachDataE2E proves the attachment's settings object
// reaches the Rego data document: a policy that denies on data.forbidden==true
// halts the build when the attachment sets forbidden=true.
func TestRegorusPolicySettingsReachDataE2E(t *testing.T) {
	_, err := runRegorusPolicy(t, regorusSettingsPolicy, map[string]any{"forbidden": true})
	if err == nil {
		t.Fatalf("settings-driven deny: expected a halt, got nil (settings did not reach Rego data)")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("settings-driven deny not errors.Is ErrPolicyViolation: %v", err)
	}
	const wantMsg = "denied by settings"
	if !strings.Contains(err.Error(), wantMsg) {
		t.Errorf("settings-driven deny does not carry the message %q:\n%v", wantMsg, err)
	}
}

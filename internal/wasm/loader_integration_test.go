// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// loadFixturePlugin builds the fixture on demand (via ensureFixtureBuilt)
// and constructs a Plugin from the resulting .wasm artifact. The
// fixture is rebuilt only when stale relative to its source or absent
// on disk; the artifact is gitignored, so a fresh checkout always
// builds. ensureFixtureBuilt cleanly skips when the per-language PDK
// source is not present or when the required toolchain is missing.
func loadFixturePlugin(t *testing.T, fixtureName string) (*wasm.Plugin, func()) {
	t.Helper()
	artifactPath := ensureFixtureBuilt(t, fixtureName)
	loader := &wasm.Loader{}
	plugin, err := loader.Load(context.Background(), wasm.PluginSpec{
		Name: strings.TrimSuffix(fixtureName, ".wasm"),
		Path: artifactPath,
	})
	if err != nil {
		t.Fatalf("loader.Load %s: %v", fixtureName, err)
	}
	return plugin, func() { _ = plugin.Close(context.Background()) }
}

// mutatorFixtureInput is the canonical mutator input shared across the
// per-language mutator round-trip tests. Shape mirrors the
// wasm-mutator JSON Schema's MutatorInput definition; one node keeps
// the per-language assertion simple.
func mutatorFixtureInput(t *testing.T) []byte {
	t.Helper()
	input := map[string]any{
		"compilation_context": map[string]any{
			"release_destination": "aws-prod/eks-main",
			"release_name":        "payments",
			"environment":         "prod",
		},
		"graph": map[string]any{
			"nodes": map[string]any{
				"payments-api": map[string]any{
					"component":        "core/web-service",
					"resolved_version": "1.4.2",
					"labels":           map[string]string{"team": "payments"},
				},
			},
		},
	}
	inputBytes, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal input: %v", err)
	}
	return inputBytes
}

// assertMutatorOutput parses the mutator plugin's output bytes,
// asserts there is exactly one patch (one input node), and that the
// patch carries the fixture's label key plus the per-language value.
// Substring matching rather than full JSON Patch parsing keeps the
// assertion focused on the fixture's behavioural contract — emit one
// patch per node with the known label key + language tag — not the
// exact JSON Patch envelope.
func assertMutatorOutput(t *testing.T, outputBytes []byte, langSuffix string) {
	t.Helper()
	var out struct {
		Patches []json.RawMessage `json:"patches"`
	}
	if err := json.Unmarshal(outputBytes, &out); err != nil {
		t.Fatalf("unmarshal MutatorOutput: %v (output: %s)", err, outputBytes)
	}
	if len(out.Patches) != 1 {
		t.Fatalf("expected exactly one patch for the one-node input; got %d", len(out.Patches))
	}
	if !strings.Contains(string(out.Patches[0]), "fixture.aggrega-pdk/touched") {
		t.Errorf("patch does not contain expected fixture label key; got %s", string(out.Patches[0]))
	}
	if !strings.Contains(string(out.Patches[0]), langSuffix) {
		t.Errorf("patch does not identify the %s fixture; got %s", langSuffix, string(out.Patches[0]))
	}
}

// assertPolicyValidatorOutput parses the policy-validator plugin's
// output bytes, asserts there are exactly two results, and that the
// verdict pattern is one-pass-one-fail in that order. The fixture's
// contract is constant across languages; the assertion is shared.
func assertPolicyValidatorOutput(t *testing.T, outputBytes []byte) {
	t.Helper()
	var out struct {
		Results []struct {
			Pass    bool   `json:"pass"`
			Message string `json:"message"`
		} `json:"results"`
	}
	if err := json.Unmarshal(outputBytes, &out); err != nil {
		t.Fatalf("unmarshal PolicyValidatorOutput: %v (output: %s)", err, outputBytes)
	}
	if len(out.Results) != 2 {
		t.Fatalf("expected exactly two results from the fixture; got %d (output: %q)", len(out.Results), string(outputBytes))
	}
	if !out.Results[0].Pass {
		t.Errorf("expected results[0].pass=true; got false (message: %s)", out.Results[0].Message)
	}
	if out.Results[1].Pass {
		t.Errorf("expected results[1].pass=false; got true (message: %s)", out.Results[1].Message)
	}
}

// TestPluginRoundTrip_MutatorGo loads the Go fixture mutator plugin
// and exercises the loader end-to-end against the canonical input.
func TestPluginRoundTrip_MutatorGo(t *testing.T) {
	plugin, cleanup := loadFixturePlugin(t, "mutator-go.wasm")
	defer cleanup()

	outputBytes, err := plugin.Call(context.Background(), "mutate", mutatorFixtureInput(t))
	if err != nil {
		t.Fatalf("plugin.Call mutate: %v", err)
	}
	assertMutatorOutput(t, outputBytes, "mutator-go")
}

// TestPluginRoundTrip_MutatorRust loads the Rust fixture mutator
// plugin and exercises the loader end-to-end against the same
// canonical input as the Go counterpart. Cross-language fixture
// parity: identical input must produce a patch carrying the Rust
// fixture's label tag.
func TestPluginRoundTrip_MutatorRust(t *testing.T) {
	plugin, cleanup := loadFixturePlugin(t, "mutator-rust.wasm")
	defer cleanup()

	outputBytes, err := plugin.Call(context.Background(), "mutate", mutatorFixtureInput(t))
	if err != nil {
		t.Fatalf("plugin.Call mutate: %v", err)
	}
	assertMutatorOutput(t, outputBytes, "mutator-rust")
}

// TestPluginRoundTrip_PolicyValidatorGo loads the Go fixture
// policy-validator plugin and asserts the constant two-result output.
func TestPluginRoundTrip_PolicyValidatorGo(t *testing.T) {
	plugin, cleanup := loadFixturePlugin(t, "policy-validator-go.wasm")
	defer cleanup()

	outputBytes, err := plugin.Call(context.Background(), "validate", []byte(`{}`))
	if err != nil {
		t.Fatalf("plugin.Call validate: %v", err)
	}
	assertPolicyValidatorOutput(t, outputBytes)
}

// TestPluginRoundTrip_PolicyValidatorRust loads the Rust fixture
// policy-validator plugin and asserts the same constant two-result
// output. Cross-language fixture parity: identical input must produce
// the identical verdict pattern.
func TestPluginRoundTrip_PolicyValidatorRust(t *testing.T) {
	plugin, cleanup := loadFixturePlugin(t, "policy-validator-rust.wasm")
	defer cleanup()

	outputBytes, err := plugin.Call(context.Background(), "validate", []byte(`{}`))
	if err != nil {
		t.Fatalf("plugin.Call validate: %v", err)
	}
	assertPolicyValidatorOutput(t, outputBytes)
}

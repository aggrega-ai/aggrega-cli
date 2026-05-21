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

// TestPluginRoundTrip_MutatorGo loads the Go fixture mutator plugin
// and exercises the loader end-to-end. The fixture appends a known
// label to every input node; the test asserts the output parses as a
// list of JSON Patch documents and carries one patch per input node
// containing the fixture's label.
func TestPluginRoundTrip_MutatorGo(t *testing.T) {
	plugin, cleanup := loadFixturePlugin(t, "mutator-go.wasm")
	defer cleanup()

	// Input shape mirrors the wasm-mutator JSON Schema's MutatorInput
	// definition. The fixture iterates Graph.Nodes; one node here keeps
	// the assertion simple.
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

	outputBytes, err := plugin.Call(context.Background(), "mutate", inputBytes)
	if err != nil {
		t.Fatalf("plugin.Call mutate: %v", err)
	}

	var out struct {
		Patches []json.RawMessage `json:"patches"`
	}
	if err := json.Unmarshal(outputBytes, &out); err != nil {
		t.Fatalf("unmarshal MutatorOutput: %v (output: %s)", err, outputBytes)
	}
	if len(out.Patches) != 1 {
		t.Fatalf("expected exactly one patch for the one-node input; got %d", len(out.Patches))
	}

	// The fixture's patch carries fixtureLabelKey/value. We verify the
	// substring rather than parsing the patch JSON: the fixture's
	// contract is the label-and-value pair, not the exact JSON Patch
	// envelope.
	if !strings.Contains(string(out.Patches[0]), "fixture.aggrega-pdk/touched") {
		t.Errorf("patch does not contain expected fixture label key; got %s", string(out.Patches[0]))
	}
	if !strings.Contains(string(out.Patches[0]), "mutator-go") {
		t.Errorf("patch does not identify the Go-language fixture; got %s", string(out.Patches[0]))
	}
}

// TestPluginRoundTrip_PolicyValidatorGo loads the Go fixture
// policy-validator plugin and exercises the loader end-to-end. The
// fixture emits a constant two-result output (one pass, one fail);
// the test asserts the output shape and the verdict pattern.
func TestPluginRoundTrip_PolicyValidatorGo(t *testing.T) {
	plugin, cleanup := loadFixturePlugin(t, "policy-validator-go.wasm")
	defer cleanup()

	// The fixture does not inspect its input; an empty object suffices.
	inputBytes := []byte(`{}`)
	outputBytes, err := plugin.Call(context.Background(), "validate", inputBytes)
	if err != nil {
		t.Fatalf("plugin.Call validate: %v", err)
	}

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

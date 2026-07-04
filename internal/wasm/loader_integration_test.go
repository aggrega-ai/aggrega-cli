// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"context"
	"encoding/json"
	"reflect"
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
			"release_name": "payments",
			"environment":  "prod",
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
	if !strings.Contains(string(out.Patches[0]), "fixture.aggrega-pdk~1touched") {
		t.Errorf("patch does not contain expected RFC-6901-escaped fixture label key; got %s", string(out.Patches[0]))
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

// TestPluginRoundTrip_MutatorTypescript loads the TypeScript fixture
// mutator plugin (built via extism-js, with QuickJS runtime emitting
// WASI runtime-init imports that the stubbed-subset firewall accepts)
// and exercises the loader end-to-end against the same canonical
// input as the Go and Rust counterparts.
func TestPluginRoundTrip_MutatorTypescript(t *testing.T) {
	plugin, cleanup := loadFixturePlugin(t, "mutator-typescript.wasm")
	defer cleanup()

	outputBytes, err := plugin.Call(context.Background(), "mutate", mutatorFixtureInput(t))
	if err != nil {
		t.Fatalf("plugin.Call mutate: %v", err)
	}
	assertMutatorOutput(t, outputBytes, "mutator-typescript")
}

// TestPluginRoundTrip_PolicyValidatorTypescript loads the TypeScript
// fixture policy-validator plugin and asserts the constant
// two-result output, completing the three-language matrix for both
// sockets.
func TestPluginRoundTrip_PolicyValidatorTypescript(t *testing.T) {
	plugin, cleanup := loadFixturePlugin(t, "policy-validator-typescript.wasm")
	defer cleanup()

	outputBytes, err := plugin.Call(context.Background(), "validate", []byte(`{}`))
	if err != nil {
		t.Fatalf("plugin.Call validate: %v", err)
	}
	assertPolicyValidatorOutput(t, outputBytes)
}

// crossLanguageFixtureLangs enumerates the three source languages for
// the parity tests. Each subtest's outputs are decoded into a Go
// shape and compared via reflect.DeepEqual rather than asserting
// byte-identical encoded bytes — JSON encoders may legitimately
// differ in whitespace or key ordering even when the wire shape is
// structurally equivalent. Structural equivalence is the invariant
// the parity discipline asserts.
var crossLanguageFixtureLangs = []string{"go", "rust", "typescript"}

// TestCrossLanguageFixtureParity_PolicyValidator loads all three
// policy-validator fixtures, calls each with the same input, and
// asserts the decoded outputs are structurally equivalent across
// languages. The policy-validator fixture's contract is constant
// across languages so the output shapes should match exactly.
//
// Each language's plugin is loaded, called, and closed inside an
// inner-function scope so the deferred cleanup fires at end of
// iteration rather than at end of test. Holding all three QuickJS
// plugins open simultaneously would pin ~7 MB of Wazero state for
// the duration of the test; per-iteration release keeps memory
// pressure bounded to one plugin at a time.
func TestCrossLanguageFixtureParity_PolicyValidator(t *testing.T) {
	type result struct {
		Pass    bool   `json:"pass"`
		Message string `json:"message"`
	}
	type output struct {
		Results []result `json:"results"`
	}

	var decoded []output
	for _, lang := range crossLanguageFixtureLangs {
		func() {
			plugin, cleanup := loadFixturePlugin(t, "policy-validator-"+lang+".wasm")
			defer cleanup()

			outputBytes, err := plugin.Call(context.Background(), "validate", []byte(`{}`))
			if err != nil {
				t.Fatalf("plugin.Call validate (%s): %v", lang, err)
			}
			var o output
			if err := json.Unmarshal(outputBytes, &o); err != nil {
				t.Fatalf("unmarshal %s output: %v (output: %s)", lang, err, outputBytes)
			}
			decoded = append(decoded, o)
		}()
	}

	for i := 1; i < len(decoded); i++ {
		if !reflect.DeepEqual(decoded[0], decoded[i]) {
			t.Errorf("cross-language parity broken between %s and %s:\n  %s: %+v\n  %s: %+v",
				crossLanguageFixtureLangs[0], crossLanguageFixtureLangs[i],
				crossLanguageFixtureLangs[0], decoded[0],
				crossLanguageFixtureLangs[i], decoded[i])
		}
	}
}

// TestCrossLanguageFixtureParity_Mutator loads all three mutator
// fixtures, calls each with the same input, and asserts the decoded
// outputs are structurally equivalent modulo the per-language label
// tag. The mutator's output shape is "one JSON Patch per node, each
// patch is an array of one add operation"; the only language-
// dependent value is the label value (mutator-go vs mutator-rust vs
// mutator-typescript). The test asserts:
//
//   - All three outputs have the same number of patches.
//   - Each patch is a single-op array with op="add" and a path that
//     is identical across languages (the same input → the same node
//     IDs → the same paths).
//   - The value field matches the expected per-language tag.
func TestCrossLanguageFixtureParity_Mutator(t *testing.T) {
	type patchOp struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value string `json:"value"`
	}
	type output struct {
		Patches [][]patchOp `json:"patches"`
	}

	// Per-iteration IIFE scopes the deferred cleanup so each plugin
	// closes before the next one loads. See the policy-validator
	// parity test for the memory-pressure rationale.
	decodedByLang := map[string]output{}
	for _, lang := range crossLanguageFixtureLangs {
		func() {
			plugin, cleanup := loadFixturePlugin(t, "mutator-"+lang+".wasm")
			defer cleanup()

			outputBytes, err := plugin.Call(context.Background(), "mutate", mutatorFixtureInput(t))
			if err != nil {
				t.Fatalf("plugin.Call mutate (%s): %v", lang, err)
			}
			var o output
			if err := json.Unmarshal(outputBytes, &o); err != nil {
				t.Fatalf("unmarshal %s output: %v (output: %s)", lang, err, outputBytes)
			}
			decodedByLang[lang] = o
		}()
	}

	// Patch count parity.
	patchCount := len(decodedByLang["go"].Patches)
	for _, lang := range crossLanguageFixtureLangs[1:] {
		if got := len(decodedByLang[lang].Patches); got != patchCount {
			t.Errorf("patch count mismatch: go=%d %s=%d", patchCount, lang, got)
		}
	}

	// Per-patch parity: each language's i-th patch has the same path
	// + op as Go's i-th patch; value matches the language-specific tag.
	for i, goPatchArr := range decodedByLang["go"].Patches {
		if len(goPatchArr) != 1 {
			t.Fatalf("go patch[%d] expected length 1, got %d", i, len(goPatchArr))
		}
		goOp := goPatchArr[0]
		for _, lang := range crossLanguageFixtureLangs[1:] {
			if i >= len(decodedByLang[lang].Patches) {
				t.Fatalf("%s produced %d patch group(s) but go produced %d; cannot compare patch[%d] — the %s socket returned an empty or short patch set (a wrong-export dispatch yields zero results)",
					lang, len(decodedByLang[lang].Patches), len(decodedByLang["go"].Patches), i, lang)
			}
			langPatchArr := decodedByLang[lang].Patches[i]
			if len(langPatchArr) != 1 {
				t.Errorf("%s patch[%d] expected length 1, got %d", lang, i, len(langPatchArr))
				continue
			}
			langOp := langPatchArr[0]
			if langOp.Op != goOp.Op {
				t.Errorf("patch[%d] op mismatch: go=%q %s=%q", i, goOp.Op, lang, langOp.Op)
			}
			if langOp.Path != goOp.Path {
				t.Errorf("patch[%d] path mismatch: go=%q %s=%q", i, goOp.Path, lang, langOp.Path)
			}
			expectedTag := "mutator-" + lang
			if langOp.Value != expectedTag {
				t.Errorf("patch[%d] value mismatch: %s=%q want %q", i, lang, langOp.Value, expectedTag)
			}
		}
	}
}

// TestPluginRoundTrip_WasiProbeRust loads the WASI-importing probe
// fixture through the real loader and verifies it runs clean under the
// deterministic, capability-denied WASI configuration: the firewall
// accepts the WASI imports the fixture declares (clock_time_get,
// random_get, fd_write), the Extism-bundled wazero WASI runtime serves
// them under the loader's ModuleConfig, and the call returns without a
// trap, an error, or a host-side side effect (fd_write's bytes are
// discarded — nothing reaches host stdout).
//
// The fixture writes its observed responses into a fixed memory buffer
// rather than through the Extism call/return ABI, and the Extism Plugin
// wrapper exposes only the Extism kernel's memory, not the guest main
// module's — so the byte-level values of that buffer are not readable
// through the production loader path. The deterministic VALUES the
// configuration produces (frozen wall time, zero entropy, discarded
// stdio) are proven byte-for-byte against the same fixture through a
// direct wazero instantiation in TestWasiProbeDeterministicValues.
func TestPluginRoundTrip_WasiProbeRust(t *testing.T) {
	plugin, cleanup := loadFixturePlugin(t, "wasi-probe-rust.wasm")
	defer cleanup()

	if _, err := plugin.Call(context.Background(), "probe_wasi_stubs", nil); err != nil {
		t.Fatalf("plugin.Call probe_wasi_stubs: %v", err)
	}
}

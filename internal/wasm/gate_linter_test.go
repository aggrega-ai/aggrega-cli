// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// linterOutput mirrors the linter plugin's response envelope for the
// gate-level assertions below. The field set is the subset the gate
// proof inspects; the canonical wire contract is the wasm-linter
// schema, exercised in full by the dispatcher's own suite.
type linterOutput struct {
	Findings []struct {
		RuleID         string `json:"rule_id"`
		Severity       string `json:"severity"`
		Classification string `json:"classification,omitempty"`
		Message        string `json:"message"`
	} `json:"findings"`
}

// TestLoadAndDescribePublishingLinter exercises the live describe()
// gate end-to-end against the real publishing-linter .wasm: load,
// describe(), validate the returned descriptor against the host
// contract, then dispatch the declared lint_rules export. It is the
// linter counterpart to TestLoadAndDescribeFixture (which covers the
// frp-step fixture); together they prove the gate over two of the
// three plugin types against real guests.
//
// The lint_rules invocation is the Pattern B handshake: the host
// delivers the full LinterInput envelope inline (InputPatternFull) and
// the guest produces its findings from that payload alone, calling no
// data-fetch host function. It is full-inline-envelope delivery, not a
// full-IR-graph projection.
//
// Skips cleanly when the plugin source or the wasm32 toolchain is
// absent — EnsureFixtureBuilt owns that decision.
func TestLoadAndDescribePublishingLinter(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")

	loader := &Loader{}
	ctx := context.Background()

	plugin, desc, err := loader.LoadAndDescribe(ctx, PluginSpec{
		Name: "publishing-linter",
		Path: pluginPath,
	})
	if err != nil {
		t.Fatalf("LoadAndDescribe: %v", err)
	}
	defer plugin.Close(ctx)

	if desc.AbiVersion != ABIVersionV1Alpha1 {
		t.Errorf("abi_version: got %q want %q", desc.AbiVersion, ABIVersionV1Alpha1)
	}
	if desc.PluginType != string(PluginTypeLinter) {
		t.Errorf("plugin_type: got %q want %q", desc.PluginType, PluginTypeLinter)
	}
	if !desc.hasExportWithPrefix(lintExportPrefix) {
		t.Errorf("descriptor declares no %s* export", lintExportPrefix)
	}
	if !desc.hasExport("lint_rules") {
		t.Errorf("descriptor does not declare the %q export", "lint_rules")
	}

	// Pattern B: full LinterInput envelope delivered inline. A
	// three-segment-violating Component name surfaces a name-shape
	// finding the guest derives from the payload alone.
	payload := []byte(`{"rule_id":"name-shape","entity_kind":"Component","proposed":{"kind":"Component","metadata":{"name":"BadName"}}}`)
	out, err := InvokeExport(ctx, plugin, desc, "lint_rules", payload)
	if err != nil {
		t.Fatalf("InvokeExport(lint_rules): %v", err)
	}
	if len(out) == 0 {
		t.Fatal("lint_rules returned empty output")
	}

	var parsed linterOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal lint_rules output: %v (raw: %s)", err, out)
	}
	if len(parsed.Findings) != 1 {
		t.Fatalf("expected exactly one finding; got %d (raw: %s)", len(parsed.Findings), out)
	}
	f := parsed.Findings[0]
	if f.RuleID != "name-shape" {
		t.Errorf("finding rule_id: got %q want %q", f.RuleID, "name-shape")
	}
	if f.Severity != abi.SeverityHalt {
		t.Errorf("finding severity: got %q want %q", f.Severity, abi.SeverityHalt)
	}

	// An export the descriptor does not declare is refused before the
	// guest runs, even on a live plugin.
	if _, err := InvokeExport(ctx, plugin, desc, "render_node", nil); !errors.Is(err, ErrWasmManifestRejected()) {
		t.Fatalf("undeclared export on live plugin: got %v want ErrWasmManifestRejected", err)
	}
}

// TestPublishingLinterDescribeGolden pins the linter's describe()
// self-report. The raw describe() bytes are unmarshaled into a
// PluginDescriptor and re-marshaled so the comparison is stable under
// encoding/json's sorted object keys regardless of the guest's literal
// field order; the descriptor's slices carry declared order. Regenerate
// with AGGREGA_UPDATE_GOLDEN=1 after an intentional descriptor change.
func TestPublishingLinterDescribeGolden(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")

	loader := &Loader{}
	ctx := context.Background()

	plugin, err := loader.Load(ctx, PluginSpec{
		Name: "publishing-linter",
		Path: pluginPath,
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer plugin.Close(ctx)

	raw, err := plugin.Call(ctx, "describe", nil)
	if err != nil {
		t.Fatalf("describe(): %v", err)
	}

	var desc PluginDescriptor
	if err := json.Unmarshal(raw, &desc); err != nil {
		t.Fatalf("unmarshal descriptor: %v (raw: %s)", err, raw)
	}
	normalized, err := json.MarshalIndent(desc, "", "  ")
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	normalized = append(normalized, '\n')

	golden := filepath.Join("testdata", "publishing-linter.describe.json.golden")
	if os.Getenv("AGGREGA_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, normalized, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated golden %s (%d bytes)", golden, len(normalized))
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s (regenerate with AGGREGA_UPDATE_GOLDEN=1): %v", golden, err)
	}
	if !bytes.Equal(normalized, want) {
		t.Errorf("describe() descriptor drifted from golden.\n--- got ---\n%s\n--- want ---\n%s", normalized, want)
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// The LinterInput / LinterOutput / LinterFinding wire types are generated from
// wasm-linter-v1alpha1 into linter_host_gen.go (the host vocabulary realizes the
// dynamically assembled policies payload as an opaque json.RawMessage),
// single-sourcing the wire shape with the portable guest codec in aggrega-abi
// and the PDKs. This file carries the dispatch behaviour that consumes them.

// lintExportName is the publishing-linter's rule-evaluation export.
// It carries the lint_ prefix the shared describe() gate requires of a
// linter plugin's minimum export; the host dispatches every rule
// invocation to this single export, selecting the rule via
// LinterInput.RuleID rather than per-rule exports.
const lintExportName = "lint_rules"

// DispatchPublishingLinter invokes the publishing-linter Wasm plugin
// against the supplied LinterInput and returns its findings.
//
// pluginBytes is the resolved .wasm artifact content, produced by the shared
// plugin-resolution cascade (pkg/pluginpath). Resolution — tier selection
// (embed / sidecar / local / cache / registry) and any digest-pin verification
// — happens at the caller before dispatch; this dispatcher only loads and runs
// the bytes it is handed.
//
// The plugin loads through the shared describe() gate: the host calls
// describe(), validates the descriptor (ABI version, plugin type,
// minimum lint_* export) against its contract, then invokes the
// lint_rules export. A plugin lacking describe() or carrying a bad
// descriptor halts here with a loud guardrail. Returns a wrapped error
// on loader/gate failure, plugin call failure, or output unmarshal
// failure.
func DispatchPublishingLinter(pluginBytes []byte, input LinterInput) ([]LinterFinding, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: marshal input: %w", err)
	}

	loader := &wasm.Loader{}
	ctx := context.Background()
	plugin, desc, err := loader.LoadAndDescribe(ctx, wasm.PluginSpec{
		Name:  "publishing-linter",
		Bytes: pluginBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: load: %w", err)
	}
	defer plugin.Close(ctx)

	out, err := wasm.InvokeExport(ctx, plugin, desc, lintExportName, payload)
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: call %s: %w", lintExportName, err)
	}

	var output LinterOutput
	if err := json.Unmarshal(out, &output); err != nil {
		return nil, fmt.Errorf("publishing_linter: unmarshal output: %w", err)
	}
	return output.Findings, nil
}

// DispatchPublishingLinterBatch invokes the publishing-linter Wasm
// plugin once per input, loading the plugin a single time at batch
// open. The returned slice carries one []LinterFinding per input,
// in input order. Errors from any individual call short-circuit the
// batch.
//
// The batch dispatcher exists because `aggrega lint` invokes the
// plugin once per rule per entity — 6 rules in default mode, 7 with
// --pre-publish. Loading the plugin once amortises the Extism
// instantiation cost across the rule iteration.
//
// pluginBytes is the resolved .wasm artifact content from the shared
// plugin-resolution cascade (pkg/pluginpath); the caller resolves the tier and
// verifies any pin before dispatch.
func DispatchPublishingLinterBatch(pluginBytes []byte, inputs []LinterInput) ([][]LinterFinding, error) {
	loader := &wasm.Loader{}
	ctx := context.Background()
	plugin, desc, err := loader.LoadAndDescribe(ctx, wasm.PluginSpec{
		Name:  "publishing-linter",
		Bytes: pluginBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: load: %w", err)
	}
	defer plugin.Close(ctx)

	results := make([][]LinterFinding, 0, len(inputs))
	for i, input := range inputs {
		payload, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("publishing_linter: marshal input[%d]: %w", i, err)
		}
		out, err := wasm.InvokeExport(ctx, plugin, desc, lintExportName, payload)
		if err != nil {
			return nil, fmt.Errorf("publishing_linter: call %s input[%d]: %w", lintExportName, i, err)
		}
		var output LinterOutput
		if err := json.Unmarshal(out, &output); err != nil {
			return nil, fmt.Errorf("publishing_linter: unmarshal output[%d]: %w", i, err)
		}
		results = append(results, output.Findings)
	}
	return results, nil
}

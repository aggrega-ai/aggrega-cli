// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// LinterInput is the JSON payload the publishing-linter Wasm plugin
// consumes per the wasm-linter-v1alpha1 schema. The struct shape
// mirrors aggrega-pdk-rust's LinterInput field-for-field; the
// canonical wire contract is the schema at
// aggrega-schemas/json/wasm-linter-v1alpha1.schema.json.
//
// Proposed and Previous carry the parsed YAML entity structure (the
// caller converts YAML → JSON before invocation; the plugin never
// sees raw bytes). Previous is nil for rules that do not need a
// previous-version reference (first publication; structural-only
// rules).
type LinterInput struct {
	RuleID     string          `json:"rule_id"`
	EntityKind string          `json:"entity_kind"`
	Proposed   json.RawMessage `json:"proposed"`
	// Previous carries the parsed prior-version entity for rules
	// that perform pairwise comparison (schema-evolution). Empty
	// when the rule does not need a previous-version reference.
	Previous json.RawMessage `json:"previous,omitempty"`
	// Parent carries the parsed resolved-parent entity for rules
	// that perform child-vs-parent comparison (narrowing). The
	// caller resolves the entity's spec.extends URI before
	// invocation. Empty when no parent exists or the rule does not
	// need parent context.
	Parent json.RawMessage `json:"parent,omitempty"`
	// SourcePath carries the filesystem path the entity was loaded
	// from. Populated by host callers walking the catalog
	// filesystem; consumed by path-aware rules (e.g.,
	// category-matches-directory). Empty when the caller has no path
	// context.
	SourcePath string          `json:"source_path,omitempty"`
	Policies   json.RawMessage `json:"policies,omitempty"`
}

// LinterOutput is the JSON response shape the plugin emits per the
// wasm-linter-v1alpha1 schema. Findings carries one entry per rule
// invocation that produced a result.
type LinterOutput struct {
	Findings []LinterFinding `json:"findings"`
}

// LinterFinding mirrors the schema's LinterFinding shape. Severity
// is "error" or "warning"; Classification is "PATCH" / "MINOR" /
// "MAJOR" when the rule emits a classification verdict (populated by
// the schema-evolution rule); empty for rules whose finding does
// not carry one.
type LinterFinding struct {
	RuleID         string `json:"rule_id"`
	Severity       string `json:"severity"`
	Classification string `json:"classification,omitempty"`
	Message        string `json:"message"`
}

// lintExportName is the publishing-linter's rule-evaluation export.
// It carries the lint_ prefix the shared describe() gate requires of a
// linter plugin's minimum export; the host dispatches every rule
// invocation to this single export, selecting the rule via
// LinterInput.RuleID rather than per-rule exports.
const lintExportName = "lint_rules"

// DispatchPublishingLinter invokes the publishing-linter Wasm plugin
// against the supplied LinterInput and returns its findings.
//
// pluginPath is the filesystem path to the built .wasm artifact —
// Tier 1 local path today; Tier 3 OCI resolution lands when the
// `aggrega lint --pre-publish` CLI mode arrives and consumes this
// dispatcher with the resolved cache-slot path.
//
// The plugin loads through the shared describe() gate: the host calls
// describe(), validates the descriptor (ABI version, plugin type,
// minimum lint_* export) against its contract, then invokes the
// lint_rules export. A plugin lacking describe() or carrying a bad
// descriptor halts here with a loud guardrail. Returns a wrapped error
// on loader/gate failure, plugin call failure, or output unmarshal
// failure.
func DispatchPublishingLinter(pluginPath string, input LinterInput) ([]LinterFinding, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: marshal input: %w", err)
	}

	loader := &wasm.Loader{}
	ctx := context.Background()
	plugin, desc, err := loader.LoadAndDescribe(ctx, wasm.PluginSpec{
		Name: "publishing-linter",
		Path: pluginPath,
	})
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: load %s: %w", pluginPath, err)
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
func DispatchPublishingLinterBatch(pluginPath string, inputs []LinterInput) ([][]LinterFinding, error) {
	loader := &wasm.Loader{}
	ctx := context.Background()
	plugin, desc, err := loader.LoadAndDescribe(ctx, wasm.PluginSpec{
		Name: "publishing-linter",
		Path: pluginPath,
	})
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: load %s: %w", pluginPath, err)
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

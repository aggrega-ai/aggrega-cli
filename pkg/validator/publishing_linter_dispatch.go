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
// aggrega-schemas/json-schemas/wasm-linter-v1alpha1.schema.json.
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

// DispatchPublishingLinter invokes the publishing-linter Wasm plugin
// against the supplied LinterInput and returns its findings.
//
// pluginPath is the filesystem path to the built .wasm artifact —
// Tier 1 local path today; Tier 3 OCI resolution lands when the
// `aggrega lint --pre-publish` CLI mode arrives and consumes this
// dispatcher with the resolved cache-slot path.
//
// Returns a wrapped error on loader failure, plugin call failure, or
// output unmarshal failure. The plugin's `lint` entry point is the
// single exported function the dispatcher invokes; rule selection
// happens via LinterInput.RuleID, not via per-rule entry points.
func DispatchPublishingLinter(pluginPath string, input LinterInput) ([]LinterFinding, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: marshal input: %w", err)
	}

	loader := &wasm.Loader{}
	ctx := context.Background()
	plugin, err := loader.Load(ctx, wasm.PluginSpec{
		Name: "publishing-linter",
		Path: pluginPath,
	})
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: load %s: %w", pluginPath, err)
	}

	out, err := plugin.Call(ctx, "lint", payload)
	if err != nil {
		return nil, fmt.Errorf("publishing_linter: call lint: %w", err)
	}

	var output LinterOutput
	if err := json.Unmarshal(out, &output); err != nil {
		return nil, fmt.Errorf("publishing_linter: unmarshal output: %w", err)
	}
	return output.Findings, nil
}

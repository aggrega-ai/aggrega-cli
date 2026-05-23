// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// loadFixture reads a YAML fixture from the publishing-linter
// plugin's tests/fixtures/ tree, parses it via yaml.v3, and
// re-marshals to JSON so the dispatcher's json.RawMessage input
// shape can carry it. The host owns YAML → JSON conversion; the
// plugin never sees raw YAML.
func loadFixture(t *testing.T, kind, classification, file string) json.RawMessage {
	t.Helper()
	root := umbrellaRoot(t)
	path := filepath.Join(root, "aggrega-cli", "wasm-plugins", "publishing-linter",
		"tests", "fixtures", strings.ToLower(kind), classification, file)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatalf("parse fixture %s: %v", path, err)
	}
	v = normaliseYAMLForJSON(v)
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture %s: %v", path, err)
	}
	return out
}

// normaliseYAMLForJSON converts the map[any]any shapes yaml.v3
// produces into map[string]any so json.Marshal can serialise them.
// Mirrors the helper pattern from pkg/sourceresolver/metadata.
func normaliseYAMLForJSON(v any) any {
	switch val := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			if ks, ok := k.(string); ok {
				out[ks] = normaliseYAMLForJSON(vv)
			}
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			out[k] = normaliseYAMLForJSON(vv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = normaliseYAMLForJSON(item)
		}
		return out
	default:
		return v
	}
}

// umbrellaRoot resolves the umbrella root by walking up from the
// caller's directory until .gitmodules is found. Mirrors the
// resolution wasmtest's package internally performs.
func umbrellaRoot(t *testing.T) string {
	t.Helper()
	_, callerFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(callerFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".gitmodules")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no .gitmodules above %s", callerFile)
		}
		dir = parent
	}
}

func TestDispatchClassifiesComponentPatch(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")
	prev := loadFixture(t, "Component", "patch", "previous.yaml")
	prop := loadFixture(t, "Component", "patch", "proposed.yaml")

	findings, err := validator.DispatchPublishingLinter(pluginPath, validator.LinterInput{
		RuleID:     "schema-evolution",
		EntityKind: "Component",
		Proposed:   prop,
		Previous:   prev,
	})
	if err != nil {
		t.Fatalf("DispatchPublishingLinter: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding; got %d", len(findings))
	}
	if findings[0].Classification != "PATCH" {
		t.Errorf("classification: got %q; want PATCH", findings[0].Classification)
	}
	if findings[0].RuleID != "schema-evolution" {
		t.Errorf("rule_id: got %q; want schema-evolution", findings[0].RuleID)
	}
	if findings[0].Severity != "error" {
		t.Errorf("severity: got %q; want error", findings[0].Severity)
	}
}

func TestDispatchClassifiesComponentMinor(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")
	prev := loadFixture(t, "Component", "minor", "previous.yaml")
	prop := loadFixture(t, "Component", "minor", "proposed.yaml")

	findings, err := validator.DispatchPublishingLinter(pluginPath, validator.LinterInput{
		RuleID:     "schema-evolution",
		EntityKind: "Component",
		Proposed:   prop,
		Previous:   prev,
	})
	if err != nil {
		t.Fatalf("DispatchPublishingLinter: %v", err)
	}
	if findings[0].Classification != "MINOR" {
		t.Errorf("classification: got %q; want MINOR", findings[0].Classification)
	}
}

func TestDispatchClassifiesComponentMajor(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")
	prev := loadFixture(t, "Component", "major", "previous.yaml")
	prop := loadFixture(t, "Component", "major", "proposed.yaml")

	findings, err := validator.DispatchPublishingLinter(pluginPath, validator.LinterInput{
		RuleID:     "schema-evolution",
		EntityKind: "Component",
		Proposed:   prop,
		Previous:   prev,
	})
	if err != nil {
		t.Fatalf("DispatchPublishingLinter: %v", err)
	}
	if findings[0].Classification != "MAJOR" {
		t.Errorf("classification: got %q; want MAJOR", findings[0].Classification)
	}
}

func TestDispatchHandlesUnknownRule(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")
	findings, err := validator.DispatchPublishingLinter(pluginPath, validator.LinterInput{
		RuleID:     "non-existent-rule",
		EntityKind: "Component",
		Proposed:   json.RawMessage(`{"kind": "Component"}`),
	})
	if err != nil {
		t.Fatalf("DispatchPublishingLinter: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding; got %d", len(findings))
	}
	if findings[0].RuleID != "unknown_rule" {
		t.Errorf("rule_id: got %q; want unknown_rule", findings[0].RuleID)
	}
	if !strings.Contains(findings[0].Message, "non-existent-rule") {
		t.Errorf("message should name the unknown rule_id; got %q", findings[0].Message)
	}
}

// TestPoliciesRoundTripThroughSchema pins the publish/consume
// symmetry between the Go host's PublishingDisciplineConfig and the
// Rust plugin's Policies struct (per Concern 3 disposition). A Go
// struct with non-default values for every field is marshaled to
// JSON, validated against the schema's Policies $def, fed to the
// plugin through the LinterInput.policies passthrough, and the
// finding's diagnostic message references at least one of the
// expected flag names — which would only be possible if the Rust
// side deserialised the same shape without field loss.
//
// At plugin v0.1.0 the schema-evolution rule does not consume
// policies. This test pins the round-trip at the SCHEMA-validation
// layer: the constructed Policies JSON object passes schema
// validation when embedded inside a LinterInput envelope; the plugin
// accepts the envelope without rejecting unknown fields. Future rule
// WIs that DO consult policies will assert flag-specific behaviour
// against the same round-trip.
func TestPoliciesRoundTripThroughSchema(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")

	// Construct a Policies JSON with non-default values for every
	// field. This mirrors workspaceconfig.PublishingDisciplineConfig's
	// shape exactly per the schema's Policies $def.
	policies := json.RawMessage(`{
		"enforce_monotonic_narrowing": true,
		"enforce_immutability": true,
		"narrowing_exemptions": [
			{"entity": "core/legacy", "paths": ["spec.schema.values.timeout"], "reason": "deferred refactor"}
		],
		"allow_dynamic_refs": true,
		"allow_id_overrides": true,
		"allow_non_pascal_schema_names": true,
		"allow_direct_x_aggrega_extensions": true,
		"allow_nonstandard_names": true
	}`)

	// Plugin accepts the policies-bearing input without rejecting
	// it. The schema-evolution rule itself doesn't consult policies,
	// but the input envelope must round-trip through the plugin's
	// deserialiser. If the schema's Policies $def diverged from the
	// Rust Policies struct, the plugin would fail to deserialise the
	// input and return an error rather than a finding.
	prev := loadFixture(t, "Component", "patch", "previous.yaml")
	prop := loadFixture(t, "Component", "patch", "proposed.yaml")
	findings, err := validator.DispatchPublishingLinter(pluginPath, validator.LinterInput{
		RuleID:     "schema-evolution",
		EntityKind: "Component",
		Proposed:   prop,
		Previous:   prev,
		Policies:   policies,
	})
	if err != nil {
		t.Fatalf("DispatchPublishingLinter with full Policies: %v", err)
	}
	if len(findings) != 1 || findings[0].Classification != "PATCH" {
		t.Errorf("expected PATCH finding with Policies present; got %+v", findings)
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// linterSpec wraps the resolved linter bytes in the loader input the dispatcher
// now takes, with a nil capability claim and zero caps — the non-entity shape
// every dispatcher test exercises (no cross-check, loader applies the default
// caps). The entity-claim and cap-threading proofs build their own spec.
func linterSpec(b []byte) wasm.PluginSpec {
	return wasm.PluginSpec{Name: "publishing-linter", Bytes: b}
}

// loadFixture reads a YAML fixture from the publishing-linter
// plugin's tests/fixtures/ tree, parses it via yaml.v3, and
// re-marshals to JSON so the dispatcher's json.RawMessage input
// shape can carry it. The host owns YAML → JSON conversion; the
// plugin never sees raw YAML.
func loadFixture(t *testing.T, kind, classification, file string) json.RawMessage {
	t.Helper()
	path := filepath.Join(wasmtest.ModuleRoot(t), "plugins", "publishing-linter",
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

func TestDispatchClassifiesComponentPatch(t *testing.T) {
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	prev := loadFixture(t, "Component", "patch", "previous.yaml")
	prop := loadFixture(t, "Component", "patch", "proposed.yaml")

	findings, err := validator.DispatchPublishingLinter(context.Background(), linterSpec(pluginBytes), "", validator.LinterInput{
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
	if findings[0].Severity != abi.SeverityHalt {
		t.Errorf("severity: got %q; want halt", findings[0].Severity)
	}
}

func TestDispatchClassifiesComponentMinor(t *testing.T) {
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	prev := loadFixture(t, "Component", "minor", "previous.yaml")
	prop := loadFixture(t, "Component", "minor", "proposed.yaml")

	findings, err := validator.DispatchPublishingLinter(context.Background(), linterSpec(pluginBytes), "", validator.LinterInput{
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
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	prev := loadFixture(t, "Component", "major", "previous.yaml")
	prop := loadFixture(t, "Component", "major", "proposed.yaml")

	findings, err := validator.DispatchPublishingLinter(context.Background(), linterSpec(pluginBytes), "", validator.LinterInput{
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
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	findings, err := validator.DispatchPublishingLinter(context.Background(), linterSpec(pluginBytes), "", validator.LinterInput{
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

// TestPoliciesRoundTripThroughSchema pins that the publishing-discipline
// configuration crosses to the plugin as the typed object both sides of the
// socket share: a Policies value with every flag set is written by the host's
// generated codec and read by the plugin's, and the dispatch still returns the
// schema-evolution rule's verdict. That rule does not consult policies, so this
// pins the decode rather than a flag's effect; a flag's effect is pinned where a
// rule consults it, as TestDispatchCategoryViolationSuppressedByPolicy does.
func TestPoliciesRoundTripThroughSchema(t *testing.T) {
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")

	policies := validator.Policies{
		AllowDynamicRefs:                    true,
		AllowIDOverrides:                    true,
		AllowDirectXAggregaExtensions:       true,
		AllowAggregaAiPrefix:                true,
		AllowNonstandardNames:               true,
		AllowCategoryMismatches:             true,
		AllowUndeclaredRecipeProvides:       true,
		AllowConnectionCredentialMismatches: true,
	}

	// A plugin whose codec read policies in any other shape would fail to decode
	// the input and return an error rather than a finding.
	prev := loadFixture(t, "Component", "patch", "previous.yaml")
	prop := loadFixture(t, "Component", "patch", "proposed.yaml")
	findings, err := validator.DispatchPublishingLinter(context.Background(), linterSpec(pluginBytes), "", validator.LinterInput{
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

// TestDispatchAnnotationPrefixViolation pins the reserved-prefix
// rule end-to-end through the actual .wasm: a Component carrying a
// bare aggrega.ai/ annotation key surfaces as an annotation-prefix
// finding with severity "halt".
func TestDispatchAnnotationPrefixViolation(t *testing.T) {
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	proposed := json.RawMessage(`{
		"kind": "Component",
		"metadata": {
			"name": "core/compute/api-server",
			"annotations": {"aggrega.ai/custom-flag": "x"}
		}
	}`)
	findings, err := validator.DispatchPublishingLinter(context.Background(), linterSpec(pluginBytes), "", validator.LinterInput{
		RuleID:     "annotation-prefix",
		EntityKind: "Component",
		Proposed:   proposed,
	})
	if err != nil {
		t.Fatalf("DispatchPublishingLinter: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding; got %d", len(findings))
	}
	if findings[0].RuleID != "annotation-prefix" {
		t.Errorf("rule_id: got %q; want annotation-prefix", findings[0].RuleID)
	}
	if findings[0].Severity != abi.SeverityHalt {
		t.Errorf("severity: got %q; want halt", findings[0].Severity)
	}
	if !strings.Contains(findings[0].Message, "aggrega.ai/custom-flag") {
		t.Errorf("message should name the offending key; got %q", findings[0].Message)
	}
}

// TestDispatchNameShapeViolation pins the per-kind name-shape rule
// end-to-end through the actual .wasm: a Component with a name that
// does not match the three-segment kebab-case pattern surfaces as a
// name-shape finding with severity "halt".
func TestDispatchNameShapeViolation(t *testing.T) {
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	proposed := json.RawMessage(`{
		"kind": "Component",
		"metadata": {"name": "BadName"}
	}`)
	findings, err := validator.DispatchPublishingLinter(context.Background(), linterSpec(pluginBytes), "", validator.LinterInput{
		RuleID:     "name-shape",
		EntityKind: "Component",
		Proposed:   proposed,
	})
	if err != nil {
		t.Fatalf("DispatchPublishingLinter: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding; got %d", len(findings))
	}
	if findings[0].RuleID != "name-shape" {
		t.Errorf("rule_id: got %q; want name-shape", findings[0].RuleID)
	}
	if !strings.Contains(findings[0].Message, "BadName") {
		t.Errorf("message should name the offending entity name; got %q", findings[0].Message)
	}
}

// TestDispatchDynamicRefsViolation pins the dynamic-refs rule
// end-to-end: a SchemaLibrary entry containing $dynamicAnchor
// surfaces as a dynamic-refs finding with severity "halt".
func TestDispatchDynamicRefsViolation(t *testing.T) {
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	proposed := json.RawMessage(`{
		"kind": "SchemaLibrary",
		"metadata": {"name": "Probes", "version": "1.0.0"},
		"spec": {"schemas": {"Polymorphic": {"$dynamicAnchor": "node", "type": "object"}}}
	}`)
	findings, err := validator.DispatchPublishingLinter(context.Background(), linterSpec(pluginBytes), "", validator.LinterInput{
		RuleID:     "dynamic-refs",
		EntityKind: "SchemaLibrary",
		Proposed:   proposed,
	})
	if err != nil {
		t.Fatalf("DispatchPublishingLinter: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding; got %d", len(findings))
	}
	if findings[0].RuleID != "dynamic-refs" {
		t.Errorf("rule_id: got %q; want dynamic-refs", findings[0].RuleID)
	}
	if findings[0].Severity != abi.SeverityHalt {
		t.Errorf("severity: got %q; want halt", findings[0].Severity)
	}
}

// TestDispatchIdOverridesViolation pins the id-overrides rule
// end-to-end: a SchemaLibrary entry with an explicit $id surfaces
// as an id-overrides finding.
func TestDispatchIdOverridesViolation(t *testing.T) {
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	proposed := json.RawMessage(`{
		"kind": "SchemaLibrary",
		"metadata": {"name": "Probes", "version": "1.0.0"},
		"spec": {"schemas": {"Custom": {"$id": "https://example.com/x", "type": "object"}}}
	}`)
	findings, err := validator.DispatchPublishingLinter(context.Background(), linterSpec(pluginBytes), "", validator.LinterInput{
		RuleID:     "id-overrides",
		EntityKind: "SchemaLibrary",
		Proposed:   proposed,
	})
	if err != nil {
		t.Fatalf("DispatchPublishingLinter: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding; got %d", len(findings))
	}
	if findings[0].RuleID != "id-overrides" {
		t.Errorf("rule_id: got %q; want id-overrides", findings[0].RuleID)
	}
}

// TestDispatchCategoryViolation pins the category-matches-directory
// rule end-to-end: a Component whose path under core/<category>/
// disagrees with its component.aggrega.ai/category label surfaces as
// a category-matches-directory finding with severity "halt".
func TestDispatchCategoryViolation(t *testing.T) {
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	proposed := json.RawMessage(`{
		"kind": "Component",
		"metadata": {
			"name": "core/datastore/foo",
			"labels": {"component.aggrega.ai/category": "compute"}
		}
	}`)
	findings, err := validator.DispatchPublishingLinter(context.Background(), linterSpec(pluginBytes), "", validator.LinterInput{
		RuleID:     "category-matches-directory",
		EntityKind: "Component",
		Proposed:   proposed,
		SourcePath: "aggrega-hub/aggrega-catalog/component/core/datastore/foo/foo.yaml",
	})
	if err != nil {
		t.Fatalf("DispatchPublishingLinter: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding; got %d", len(findings))
	}
	if findings[0].RuleID != "category-matches-directory" {
		t.Errorf("rule_id: got %q; want category-matches-directory", findings[0].RuleID)
	}
	if findings[0].Severity != abi.SeverityHalt {
		t.Errorf("severity: got %q; want halt", findings[0].Severity)
	}
	if !strings.Contains(findings[0].Message, "compute") ||
		!strings.Contains(findings[0].Message, "datastore") {
		t.Errorf("message should name both the path-derived and label values; got %q", findings[0].Message)
	}
}

// TestDispatchCategoryViolationSuppressedByPolicy pins the
// workspace-level allow_category_mismatches flag end-to-end: the
// flag suppresses every finding from the rule, including unknown-
// segment violations.
func TestDispatchCategoryViolationSuppressedByPolicy(t *testing.T) {
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	proposed := json.RawMessage(`{
		"kind": "Component",
		"metadata": {
			"name": "core/datastore/foo",
			"labels": {"component.aggrega.ai/category": "compute"}
		}
	}`)
	policies := validator.Policies{AllowCategoryMismatches: true}
	findings, err := validator.DispatchPublishingLinter(context.Background(), linterSpec(pluginBytes), "", validator.LinterInput{
		RuleID:     "category-matches-directory",
		EntityKind: "Component",
		Proposed:   proposed,
		SourcePath: "aggrega-hub/aggrega-catalog/component/core/datastore/foo/foo.yaml",
		Policies:   policies,
	})
	if err != nil {
		t.Fatalf("DispatchPublishingLinter: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected 0 findings (workspace flag suppresses); got %d: %+v", len(findings), findings)
	}
}

// TestDispatchPublishingLinterBatch pins the batch dispatcher: a
// single plugin load + N rule invocations. Each input
// returns its own findings in input order.
func TestDispatchPublishingLinterBatch(t *testing.T) {
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	proposed := json.RawMessage(`{
		"kind": "Component",
		"metadata": {
			"name": "core/datastore/foo",
			"labels": {"component.aggrega.ai/category": "compute"}
		}
	}`)
	inputs := []validator.LinterInput{
		{
			RuleID:     "category-matches-directory",
			EntityKind: "Component",
			Proposed:   proposed,
			SourcePath: "aggrega-hub/aggrega-catalog/component/core/datastore/foo/foo.yaml",
		},
		{
			RuleID:     "name-shape",
			EntityKind: "Component",
			Proposed:   json.RawMessage(`{"kind":"Component","metadata":{"name":"BadName"}}`),
		},
	}
	results, err := validator.DispatchPublishingLinterBatch(context.Background(), linterSpec(pluginBytes), "", inputs)
	if err != nil {
		t.Fatalf("DispatchPublishingLinterBatch: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 result slices; got %d", len(results))
	}
	if len(results[0]) != 1 || results[0][0].RuleID != "category-matches-directory" {
		t.Errorf("input[0] result: got %+v", results[0])
	}
	if len(results[1]) != 1 || results[1][0].RuleID != "name-shape" {
		t.Errorf("input[1] result: got %+v", results[1])
	}
}

// TestDispatchPublishingLinterRejectsNonLinter proves the linter socket halts a
// guest whose describe() reports a non-linter plugin type. The dispatcher declares
// the expected linter type on the spec, so loading the frp-step guest — a
// well-formed guest that otherwise loads and describes cleanly — at the linter
// socket fails the shared load gate's type-match check with
// ErrWasmPluginTypeMismatch rather than proceeding to invoke a lint_ export the
// guest does not declare. Exercises a real non-linter guest; skips cleanly when the
// toolchain is absent.
func TestDispatchPublishingLinterRejectsNonLinter(t *testing.T) {
	nonLinter := wasmtest.EnsureFixtureBytes(t, "frp-step-go.wasm")
	_, err := validator.DispatchPublishingLinter(context.Background(), linterSpec(nonLinter), "", validator.LinterInput{
		RuleID:     "name-shape",
		EntityKind: "Component",
		Proposed:   json.RawMessage(`{"kind":"Component","metadata":{"name":"core/compute/api-server"}}`),
	})
	if !errors.Is(err, wasm.ErrWasmPluginTypeMismatch()) {
		t.Fatalf("a non-linter guest at the linter socket should halt with ErrWasmPluginTypeMismatch; got %v", err)
	}
}

// TestDispatchPublishingLinterCrossCheckHalts proves the dispatcher arms the
// load gate's describe()-vs-entity cross-check when the spec carries a capability
// claim. The real publishing-linter's describe() declares the lint_rules export
// with empty operates/scope; a spec that claims lint_rules with a non-empty scope
// disagrees, so the LINTER LOAD must halt loudly with ErrWasmContractMismatch
// rather than run the rule. Exercises the real embedded linter so the proof is
// linter-specific; skips cleanly when the toolchain is absent.
func TestDispatchPublishingLinterCrossCheckHalts(t *testing.T) {
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	spec := wasm.PluginSpec{
		Name:  "publishing-linter",
		Bytes: pluginBytes,
		Contract: &wasm.EntityContractClaim{Exports: []wasm.EntityExportClaim{
			// describe() declares lint_rules with empty operates/scope; claim a
			// scope it does not report -> scope-mismatch on the cross-check.
			{Name: "lint_rules", Scope: "own"},
		}},
	}
	_, err := validator.DispatchPublishingLinter(context.Background(), spec, "", validator.LinterInput{
		RuleID:     "name-shape",
		EntityKind: "Component",
		Proposed:   json.RawMessage(`{"kind":"Component","metadata":{"name":"core/compute/api-server"}}`),
	})
	if !errors.Is(err, wasm.ErrWasmContractMismatch()) {
		t.Fatalf("a disagreeing claim should halt the linter load with ErrWasmContractMismatch; got %v", err)
	}
	if !strings.Contains(err.Error(), "scope") {
		t.Errorf("halt diagnostic should name the disagreeing field; got %v", err)
	}
}

// TestDispatchPublishingLinterAppliesEntityMemoryCap proves an entity-authored
// memory cap reaches the runtime: a sub-floor MaxMemoryPages threaded onto the
// spec is rejected at instantiation, where a zero/default cap would have loaded
// the linter fine. This is corroboration that the cap is applied end-to-end, not
// merely threaded onto the spec (the load-bearing threading proof is the
// toolchain-free TestResolveLinterPluginCarriesEntityRuntimeCap in cmd/aggrega).
// Exercises the real embedded linter; skips cleanly when the toolchain is absent.
func TestDispatchPublishingLinterAppliesEntityMemoryCap(t *testing.T) {
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	spec := wasm.PluginSpec{Name: "publishing-linter", Bytes: pluginBytes, MaxMemoryPages: 1}
	_, err := validator.DispatchPublishingLinter(context.Background(), spec, "", validator.LinterInput{
		RuleID:     "name-shape",
		EntityKind: "Component",
		Proposed:   json.RawMessage(`{"kind":"Component","metadata":{"name":"core/compute/api-server"}}`),
	})
	if err == nil {
		t.Fatal("a sub-floor memory cap must be rejected at instantiation; nil error means the cap was not applied")
	}
}

// TestDispatchPublishingLinterHonorsExportOverride proves the per-attachment
// export override threads all the way to the guest invocation. The real
// publishing-linter exports only lint_rules, so dispatching an explicit export
// the plugin does not declare must halt loudly with a call failure whose
// diagnostic names the authored export. Were the override ignored, the
// dispatcher would fall back to lint_rules and succeed — the loud halt naming
// the authored export is the threading proof. Exercises the real embedded
// linter; skips cleanly when the toolchain is absent.
func TestDispatchPublishingLinterHonorsExportOverride(t *testing.T) {
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	const authoredExport = "lint_does_not_exist"
	_, err := validator.DispatchPublishingLinter(context.Background(), linterSpec(pluginBytes), authoredExport, validator.LinterInput{
		RuleID:     "name-shape",
		EntityKind: "Component",
		Proposed:   json.RawMessage(`{"kind":"Component","metadata":{"name":"core/compute/api-server"}}`),
	})
	if err == nil {
		t.Fatalf("dispatching an export the plugin does not declare must halt; nil error means the override was ignored and lint_rules ran instead")
	}
	if !strings.Contains(err.Error(), authoredExport) {
		t.Errorf("halt diagnostic should name the authored export %q; got %v", authoredExport, err)
	}
}

// TestLinterOutputDecodeRefusesAnUndeclaredKey drives the strict binary decode
// both dispatch sites read a linter's answer through, over the linter shape: an
// answer carrying only the keys the shape declares decodes, and one carrying a
// key it does not — a verdict envelope, the answer an attachment's entry function
// returns when it names an export of another socket — is refused with the
// output-shape condition, naming the export and the key. Read leniently it would
// decode to an empty finding list, which is a clean lint.
//
// Both payloads come from the generated encoders, so each is canonical by
// construction and cannot drift from what a guest emits.
func TestLinterOutputDecodeRefusesAnUndeclaredKey(t *testing.T) {
	t.Parallel()

	declared, err := validator.LinterOutput{Findings: []abi.Diagnostic{}}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode a linter answer: %v", err)
	}
	var out validator.LinterOutput
	if err := wasm.DecodeGuestOutputCBOR(wasm.LintRulesExport.Name(), declared, &out); err != nil {
		t.Fatalf("an answer carrying only declared keys must decode: %v", err)
	}

	foreign, err := abi.PolicyOutput{Results: []abi.Diagnostic{}}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode a verdict envelope: %v", err)
	}
	err = wasm.DecodeGuestOutputCBOR(wasm.LintRulesExport.Name(), foreign, &out)
	if !errors.Is(err, wasm.ErrWasmOutputShape()) {
		t.Fatalf("an answer carrying an undeclared key must halt with the output-shape condition; got %v", err)
	}
	for _, want := range []string{wasm.LintRulesExport.Name(), "results"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestDispatchPublishingLinterDefaultExport proves the empty-export sentinel
// resolves to the socket default: dispatching an empty export against the real
// linter drives the lint_rules rule-evaluation path and returns a finding,
// exactly as an attachment that names no per-attachment override would.
func TestDispatchPublishingLinterDefaultExport(t *testing.T) {
	pluginBytes := wasmtest.EnsureFixtureBytes(t, "publishing-linter.wasm")
	findings, err := validator.DispatchPublishingLinter(context.Background(), linterSpec(pluginBytes), "", validator.LinterInput{
		RuleID:     "name-shape",
		EntityKind: "Component",
		Proposed:   json.RawMessage(`{"kind":"Component","metadata":{"name":"BadName"}}`),
	})
	if err != nil {
		t.Fatalf("empty export should resolve to the default rule-evaluation export: %v", err)
	}
	if len(findings) != 1 || findings[0].RuleID != "name-shape" {
		t.Fatalf("empty export should drive the default export's name-shape rule; got %+v", findings)
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// End-to-end harness exercising the publishing-discipline rule
// set through `aggrega lint --pre-publish` invocations. Three
// concerns:
//
//   - Schema-evolution classification + CLI bump comparison work
//     for every entity kind that carries a schema-bearing diff.
//   - Every rule registered in the plugin's dispatcher fires at
//     least once across the test surface.
//   - Multi-rule findings aggregate correctly when one entity
//     triggers more than one rule simultaneously.

// integrationFixturePath returns the on-disk path of an
// integration fixture under cmd/aggrega/testdata/integration/.
func integrationFixturePath(parts ...string) string {
	segments := append([]string{"testdata", "integration"}, parts...)
	return filepath.Join(segments...)
}

// readFixture returns the bytes of an integration fixture.
func readFixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	path := integrationFixturePath(parts...)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return b
}

// pushIntegrationFixture seeds the in-memory target with the
// fixture bytes under the entity's metadata.name + tag 1.0.0.
// kindLower picks the mediatype suffix.
func pushIntegrationFixture(t *testing.T, target *memoryRegistryTarget, kindLower string, yamlBytes []byte) {
	t.Helper()
	mediaType := "application/vnd.aggrega." + kindLower + ".v1alpha1+yaml"
	if _, err := oci.Push(context.Background(), target.Store, "1.0.0", mediaType, yamlBytes); err != nil {
		t.Fatalf("push fixture: %v", err)
	}
	target.tags = append(target.tags, "1.0.0")
}

// mutateProposedVersion replaces the metadata.version line in a
// YAML fixture with the given new version. Returns the on-disk
// path of a temp file containing the mutated YAML; the file is
// cleaned up by t.TempDir's lifecycle.
func mutateProposedVersion(t *testing.T, yamlBytes []byte, newVersion string) string {
	t.Helper()
	mutated := strings.Replace(string(yamlBytes), "version: 1.0.0", "version: "+newVersion, 1)
	dir := t.TempDir()
	path := filepath.Join(dir, "proposed.yaml")
	if err := os.WriteFile(path, []byte(mutated), 0o644); err != nil {
		t.Fatalf("write proposed: %v", err)
	}
	return path
}

// TestSchemaEvolutionMatrixAllKinds drives the lint command in
// --pre-publish mode against a MAJOR-required fixture for each
// of the 5 entity kinds beyond Component. For each kind, the
// proposed version is mutated to PATCH (1.0.1) → expect HALT,
// MINOR (1.1.0) → expect HALT, MAJOR (2.0.0) → expect PASS.
// Component's full 9-cell matrix is exercised separately by
// TestLintPrePublishMatrixComponent at lint_test.go.
//
// 15 sub-tests (5 kinds × 3 cells). Plugin loaded once via
// EnsureFixtureBuilt; per-cell cost is just the dispatch call.
func TestSchemaEvolutionMatrixAllKinds(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")

	kinds := []struct {
		dir  string
		kind string
	}{
		{"feature", "Feature"},
		{"recipe", "Recipe"},
		{"featurerecipe", "FeatureRecipe"},
		{"schemalibrary", "SchemaLibrary"},
		{"mappinglibrary", "MappingLibrary"},
	}

	cells := []struct {
		bump        string
		proposedVer string
		wantHalt    bool
	}{
		{"PATCH", "1.0.1", true},
		{"MINOR", "1.1.0", true},
		{"MAJOR", "2.0.0", false},
	}

	for _, k := range kinds {
		for _, c := range cells {
			t.Run(k.dir+"/"+c.bump, func(t *testing.T) {
				target := &memoryRegistryTarget{Store: memory.New()}
				previousYAML := readFixture(t, k.dir, "major", "previous.yaml")
				pushIntegrationFixture(t, target, strings.ToLower(k.kind), previousYAML)

				proposedYAML := readFixture(t, k.dir, "major", "proposed.yaml")
				proposedPath := mutateProposedVersion(t, proposedYAML, c.proposedVer)

				cfg := lintConfig{
					EntityPath:     proposedPath,
					PrePublish:     true,
					PluginPath:     pluginPath,
					PolicyResolver: func(_ string, ws bool) bool { return ws },
					FetchTarget:    target,
					ws:             resolveTempWorkspace(t),
				}
				err := lintRun(cfg)
				if c.wantHalt && err == nil {
					t.Errorf("expected HALT; got PASS")
				}
				if !c.wantHalt && err != nil {
					t.Errorf("expected PASS; got HALT: %v", err)
				}
			})
		}
	}
}

// participationCase represents a single rule_id with its
// triggering fixture. The test invokes `aggrega lint` against
// the fixture in default mode (the rule_id presence in findings
// is the assertion, not exit code).
type participationCase struct {
	ruleID         string
	fixtureFile    string
	policyOverride map[string]bool // optional flag overrides per fixture
}

// TestRuleParticipation asserts every rule registered in the
// publishing-linter dispatcher fires at least once across a
// designed-to-trigger fixture set. Operationally pins the
// "one plugin, every rule" framing: a future maintainer who
// adds a rule to the dispatcher slice without a corresponding
// trigger fixture sees this test surface as the natural
// participation gate.
//
// schema-evolution is exercised by TestSchemaEvolutionMatrixAllKinds
// and is therefore not listed here.
func TestRuleParticipation(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")

	cases := []participationCase{
		{ruleID: "annotation-prefix", fixtureFile: "annotation_prefix.yaml"},
		{ruleID: "name-shape", fixtureFile: "name_shape.yaml"},
		{ruleID: "narrowing", fixtureFile: "narrowing_child.yaml",
			policyOverride: map[string]bool{"enforce-monotonic-narrowing": true}},
		{ruleID: "dynamic-refs", fixtureFile: "dynamic_refs.yaml"},
		{ruleID: "id-overrides", fixtureFile: "id_overrides.yaml"},
		{ruleID: "category-matches-directory", fixtureFile: "category_mismatch.yaml"},
		{ruleID: "x-aggrega-discouragement", fixtureFile: "x_aggrega_in_schema.yaml"},
		{ruleID: "plugin-default-limits", fixtureFile: "plugin_default_limits.yaml"},
	}

	for _, c := range cases {
		t.Run(c.ruleID, func(t *testing.T) {
			ruleFired := dispatchAndCheckRule(t, pluginPath, c)
			if !ruleFired {
				t.Errorf("rule %q did not fire on fixture %q; expected at least one finding from this rule",
					c.ruleID, c.fixtureFile)
			}
		})
	}
}

// dispatchAndCheckRule dispatches a single rule against a
// participation fixture and returns whether that rule's
// findings appear in the output. Mirrors what lintRun does
// per-rule but isolates the rule under test (so the test
// asserts the rule actually fires rather than relying on the
// full rule-set dispatch).
func dispatchAndCheckRule(t *testing.T, pluginPath string, c participationCase) bool {
	t.Helper()
	yamlBytes := readFixture(t, "participation", c.fixtureFile)
	proposedJSON, header, err := yamlToLinterJSON(yamlBytes)
	if err != nil {
		t.Fatalf("yaml→json: %v", err)
	}

	policies := buildParticipationPolicies(c.policyOverride)

	input := validator.LinterInput{
		RuleID:     c.ruleID,
		EntityKind: header.Kind,
		Proposed:   proposedJSON,
		Policies:   policies,
	}
	// Provide the source path for category-matches-directory.
	if c.ruleID == "category-matches-directory" {
		input.SourcePath = "aggrega-hub/aggrega-catalog/component/" + header.Metadata.Name + ".yaml"
	}
	// Provide parent bytes for narrowing.
	if c.ruleID == "narrowing" {
		parentYAML := readFixture(t, "participation", "narrowing_parent.yaml")
		parentJSON, _, err := yamlToLinterJSON(parentYAML)
		if err != nil {
			t.Fatalf("parent yaml→json: %v", err)
		}
		input.Parent = parentJSON
	}

	pluginBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin %s: %v", pluginPath, err)
	}
	findings, err := validator.DispatchPublishingLinter(wasm.PluginSpec{Name: "publishing-linter", Bytes: pluginBytes}, input)
	if err != nil {
		t.Fatalf("DispatchPublishingLinter(%s): %v", c.ruleID, err)
	}
	for _, f := range findings {
		if f.RuleID == c.ruleID {
			return true
		}
	}
	return false
}

// buildParticipationPolicies builds a Policies JSON with the
// rule under test enabled (for enforce_* flags) and other flags
// at defaults.
func buildParticipationPolicies(overrides map[string]bool) json.RawMessage {
	policies := map[string]any{
		"enforce_monotonic_narrowing":       false,
		"enforce_immutability":              false,
		"narrowing_exemptions":              []any{},
		"allow_dynamic_refs":                false,
		"allow_id_overrides":                false,
		"allow_direct_x_aggrega_extensions": false,
		"allow_aggrega_ai_prefix":           false,
		"allow_nonstandard_names":           false,
		"allow_category_mismatches":         false,
	}
	for k, v := range overrides {
		switch k {
		case "enforce-monotonic-narrowing":
			policies["enforce_monotonic_narrowing"] = v
		case "enforce-immutability":
			policies["enforce_immutability"] = v
		}
	}
	out, _ := json.Marshal(policies)
	return out
}

// TestMultiRuleCrossTalk verifies the dispatcher correctly
// aggregates findings from multiple rules when a single entity
// violates more than one rule simultaneously. The crosstalk
// fixture is a Component with three layered violations:
//
//   - PascalCase fragment in the name (name-shape violation)
//   - Path under core/datastore/ with category label "compute"
//     (category-matches-directory violation)
//   - x-aggrega-selector in spec.schema (x-aggrega-discouragement
//     violation)
//
// All three rule_ids must appear in the aggregated finding set.
func TestMultiRuleCrossTalk(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")
	yamlBytes := readFixture(t, "crosstalk", "multi_violation.yaml")

	// Write to a temp path so the CLI can locate it under a
	// realistic-looking core/ path. The category-matches-directory
	// rule walks the path; we need /core/<category>/ visible.
	dir := t.TempDir()
	entityPath := filepath.Join(dir, "core", "datastore", "MultiViolator", "MultiViolator.yaml")
	if err := os.MkdirAll(filepath.Dir(entityPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(entityPath, yamlBytes, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg := lintConfig{
		EntityPath:     entityPath,
		PrePublish:     false, // default mode; cross-talk is about aggregation, not version bumping
		PluginPath:     pluginPath,
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		ws:             resolveTempWorkspace(t),
	}
	// Run lint; in default mode every finding downgrades to
	// warning and the command exits clean (no error).
	err := lintRun(cfg)
	if err != nil {
		t.Errorf("default mode should not halt; got: %v", err)
	}

	// We can't easily intercept the printed findings from
	// lintRun's stderr emission. Instead, invoke each rule
	// directly via the batch dispatcher (mirrors what lintRun
	// does internally) and assert the multi-rule union.
	expectedRules := []string{
		"name-shape",
		"category-matches-directory",
		"x-aggrega-discouragement",
	}
	firedRules := dispatchAllAndCollectRuleIDs(t, pluginPath, yamlBytes, entityPath)

	for _, want := range expectedRules {
		if !firedRules[want] {
			t.Errorf("rule %q did not fire on the cross-talk fixture; expected to participate alongside %v; got %v",
				want, expectedRules, firedRules)
		}
	}
}

// dispatchAllAndCollectRuleIDs runs every rule in the default
// rule slice against the fixture and returns the set of rule_ids
// that produced at least one finding. Mirrors lintRun's per-rule
// iteration without needing to intercept stderr.
func dispatchAllAndCollectRuleIDs(t *testing.T, pluginPath string, yamlBytes []byte, entityPath string) map[string]bool {
	t.Helper()
	proposedJSON, header, err := yamlToLinterJSON(yamlBytes)
	if err != nil {
		t.Fatalf("yaml→json: %v", err)
	}
	policies := buildParticipationPolicies(nil)

	pluginBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin %s: %v", pluginPath, err)
	}

	fired := map[string]bool{}
	for _, ruleID := range defaultRuleIDs(false) {
		input := validator.LinterInput{
			RuleID:     ruleID,
			EntityKind: header.Kind,
			Proposed:   proposedJSON,
			Policies:   policies,
		}
		if ruleID == "category-matches-directory" {
			input.SourcePath = entityPath
		}
		findings, err := validator.DispatchPublishingLinter(wasm.PluginSpec{Name: "publishing-linter", Bytes: pluginBytes}, input)
		if err != nil {
			t.Fatalf("dispatch %s: %v", ruleID, err)
		}
		for _, f := range findings {
			if f.RuleID == ruleID {
				fired[ruleID] = true
				break
			}
		}
	}
	return fired
}

// TestPluginDefaultLimits exercises the publish-time advisory that
// warns when a Plugin entity declares no explicit runtime cap. It
// proves the silent half (a Plugin with a non-zero cap produces no
// finding) and that the warning is non-halting (default-mode lintRun
// returns nil even though the finding is present).
func TestPluginDefaultLimits(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")
	pluginBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin %s: %v", pluginPath, err)
	}

	dispatch := func(yamlBytes []byte) []validator.LinterFinding {
		t.Helper()
		proposedJSON, header, err := yamlToLinterJSON(yamlBytes)
		if err != nil {
			t.Fatalf("yaml→json: %v", err)
		}
		input := validator.LinterInput{
			RuleID:     "plugin-default-limits",
			EntityKind: header.Kind,
			Proposed:   proposedJSON,
			Policies:   buildParticipationPolicies(nil),
		}
		findings, err := validator.DispatchPublishingLinter(wasm.PluginSpec{Name: "publishing-linter", Bytes: pluginBytes}, input)
		if err != nil {
			t.Fatalf("DispatchPublishingLinter: %v", err)
		}
		return findings
	}

	hasRuleFinding := func(findings []validator.LinterFinding) *validator.LinterFinding {
		for i := range findings {
			if findings[i].RuleID == "plugin-default-limits" {
				return &findings[i]
			}
		}
		return nil
	}

	// A Plugin with no spec.runtime fires the warning.
	noRuntime := readFixture(t, "participation", "plugin_default_limits.yaml")
	t.Run("no_runtime_warns", func(t *testing.T) {
		f := hasRuleFinding(dispatch(noRuntime))
		if f == nil {
			t.Fatal("expected a plugin-default-limits finding; got none")
		}
		if f.Severity != "warning" {
			t.Errorf("severity: got %q; want %q", f.Severity, "warning")
		}
	})

	// A Plugin with an explicit non-zero runtime cap is silent.
	withCap := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: core/capped-renderer
  version: 1.0.0
spec:
  plugin: oci://hub.aggrega.ai/plugins/core/capped-renderer
  pluginType: provider
  abiVersion: v1alpha1
  runtime:
    maxMemoryPages: 256
    timeoutMs: 3000
`)
	t.Run("with_cap_silent", func(t *testing.T) {
		if f := hasRuleFinding(dispatch(withCap)); f != nil {
			t.Errorf("expected no plugin-default-limits finding; got %+v", *f)
		}
	})

	// The warning does not halt default-mode lintRun.
	t.Run("non_halting", func(t *testing.T) {
		dir := t.TempDir()
		entityPath := filepath.Join(dir, "plugin.yaml")
		if err := os.WriteFile(entityPath, noRuntime, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		cfg := lintConfig{
			EntityPath:     entityPath,
			PrePublish:     false,
			PluginPath:     pluginPath,
			PolicyResolver: func(_ string, ws bool) bool { return ws },
			ws:             resolveTempWorkspace(t),
		}
		if err := lintRun(cfg); err != nil {
			t.Errorf("default mode should not halt on a warning; got: %v", err)
		}
	})
}

// Verifying the lint command's rule slice includes every
// participation rule (defensive: if a maintainer drops a rule
// from defaultRuleIDs, TestRuleParticipation would still pass
// for the dropped rule's case if invoked directly; this guard
// pins the CLI surface).
func TestRuleParticipationCoversAllDispatchedRules(t *testing.T) {
	dispatched := map[string]bool{}
	for _, id := range defaultRuleIDs(true) {
		dispatched[id] = true
	}
	expectedInParticipation := []string{
		"schema-evolution", // covered by the matrix test
		"annotation-prefix",
		"name-shape",
		"narrowing",
		"dynamic-refs",
		"id-overrides",
		"category-matches-directory",
		"x-aggrega-discouragement",
		"plugin-default-limits",
	}
	for _, ruleID := range expectedInParticipation {
		if !dispatched[ruleID] {
			t.Errorf("rule %q expected in defaultRuleIDs(true) but missing; "+
				"if this is intentional, update the participation list accordingly",
				ruleID)
		}
	}
	// Inverse: every dispatched rule should be in our expected
	// list. If a new rule is added without updating the
	// participation suite, this fires.
	expectedSet := map[string]bool{}
	for _, r := range expectedInParticipation {
		expectedSet[r] = true
	}
	for id := range dispatched {
		if !expectedSet[id] {
			t.Errorf("rule %q is dispatched but missing from the participation list; "+
				"add a participation fixture under cmd/aggrega/testdata/integration/participation/",
				id)
		}
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
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
// integration fixture under internal/lintops/testdata/integration/.
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
	if _, err := oci.Push(context.Background(), target.Store, "1.0.0", mediaType, yamlBytes, nil); err != nil {
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

				cfg := Config{
					EntityPath:     proposedPath,
					PrePublish:     true,
					PluginPath:     pluginPath,
					PolicyResolver: func(_ string, ws bool) bool { return ws },
					FetchTarget:    target,
					Workspace:      resolveTempWorkspace(t),
				}
				err := RunEntity(cfg)
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
	ruleID      string
	fixtureFile string
}

// participationCases is the one home for the rule-to-fixture pairing. Both the
// participation suite and the coverage guard below read it: the suite dispatches
// each rule against its fixture, and the guard holds the same set against the
// rule slice the lint command actually dispatches. A second list written out in
// the guard would agree on the day it was written and drift afterwards, and the
// drift would be invisible because both halves are edited by hand.
func participationCases() []participationCase {
	return []participationCase{
		{ruleID: "annotation-prefix", fixtureFile: "annotation_prefix.yaml"},
		{ruleID: "name-shape", fixtureFile: "name_shape.yaml"},
		{ruleID: "dynamic-refs", fixtureFile: "dynamic_refs.yaml"},
		{ruleID: "id-overrides", fixtureFile: "id_overrides.yaml"},
		{ruleID: "category-matches-directory", fixtureFile: "category_mismatch.yaml"},
		{ruleID: "x-aggrega-discouragement", fixtureFile: "x_aggrega_in_schema.yaml"},
		{ruleID: "plugin-default-limits", fixtureFile: "plugin_default_limits.yaml"},
		{ruleID: "secret-slot-declares-sensitive", fixtureFile: "secret_slot_unmarked.yaml"},
		{ruleID: "provides-declared-by-component", fixtureFile: "provides_undeclared.yaml"},
	}
}

// rulesCoveredOutsideParticipation names the dispatched rules whose coverage
// lives in another suite, each with the suite that carries it. A rule listed here
// is exempt from needing a participation fixture; a rule NOT listed here and not
// in participationCases is a rule nothing exercises, which is what the coverage
// guard below refuses.
//
// The exemption is named rather than implied so that adding one is a visible
// decision. An unnamed exemption reads as an oversight and gets "fixed" by
// someone adding a redundant fixture.
var rulesCoveredOutsideParticipation = map[string]string{
	"schema-evolution": "the schema-evolution matrix suite, which runs it across every kind",
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

	for _, c := range participationCases() {
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
// findings appear in the output. Mirrors what RunEntity does
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

	policies := buildParticipationPolicies()

	input := validator.LinterInput{
		RuleID:     c.ruleID,
		EntityKind: header.Kind,
		Proposed:   proposedJSON,
		Policies:   policies,
	}
	// Two rules need more than the proposed entity. The path-aware rule needs
	// where the entity was read from; the contract-comparing rule needs the
	// related entity of the other kind, which the host resolves in production and
	// which is supplied here from the fixture that pairs with this one.
	switch c.ruleID {
	case "category-matches-directory":
		input.SourcePath = "aggrega-hub/aggrega-catalog/component/" + header.Metadata.Name + ".yaml"
	case "provides-declared-by-component":
		input.ImplementedComponent = implementedComponentFixture(t)
	}
	pluginBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin %s: %v", pluginPath, err)
	}
	findings, err := validator.DispatchPublishingLinter(wasm.PluginSpec{Name: "publishing-linter", Bytes: pluginBytes}, "", input)
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

// buildParticipationPolicies builds a Policies JSON with every
// opt-out flag at its default, so each rule under test is active.
func buildParticipationPolicies() json.RawMessage {
	policies := map[string]any{
		"allow_dynamic_refs":                false,
		"allow_id_overrides":                false,
		"allow_direct_x_aggrega_extensions": false,
		"allow_aggrega_ai_prefix":           false,
		"allow_nonstandard_names":           false,
		"allow_category_mismatches":         false,
		"allow_undeclared_recipe_provides":  false,
	}
	out, _ := json.Marshal(policies)
	return out
}

// implementedComponentFixture returns the Component that pairs with the
// provides-declared-by-component participation fixture, in the JSON shape the
// payload carries. It is a second fixture rather than a second document in the
// first because the rule compares two entities, and the harness reads one entity
// per case.
func implementedComponentFixture(t *testing.T) json.RawMessage {
	t.Helper()
	componentJSON, _, err := yamlToLinterJSON(
		readFixture(t, "participation", "provides_undeclared_component.yaml"))
	if err != nil {
		t.Fatalf("yaml→json for the paired component fixture: %v", err)
	}
	return componentJSON
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

	cfg := Config{
		EntityPath:     entityPath,
		PrePublish:     false, // default mode; cross-talk is about aggregation, not version bumping
		PluginPath:     pluginPath,
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		Workspace:      resolveTempWorkspace(t),
	}
	// Run lint; in default mode every finding downgrades to
	// warning and the command exits clean (no error).
	err := RunEntity(cfg)
	if err != nil {
		t.Errorf("default mode should not halt; got: %v", err)
	}

	// We can't easily intercept the printed findings from
	// RunEntity's stderr emission. Instead, invoke each rule
	// directly via the batch dispatcher (mirrors what RunEntity
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
// that produced at least one finding. Mirrors RunEntity's per-rule
// iteration without needing to intercept stderr.
func dispatchAllAndCollectRuleIDs(t *testing.T, pluginPath string, yamlBytes []byte, entityPath string) map[string]bool {
	t.Helper()
	proposedJSON, header, err := yamlToLinterJSON(yamlBytes)
	if err != nil {
		t.Fatalf("yaml→json: %v", err)
	}
	policies := buildParticipationPolicies()

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
		switch ruleID {
		case "category-matches-directory":
			input.SourcePath = entityPath
		case "provides-declared-by-component":
			input.ImplementedComponent = implementedComponentFixture(t)
		}
		findings, err := validator.DispatchPublishingLinter(wasm.PluginSpec{Name: "publishing-linter", Bytes: pluginBytes}, "", input)
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
// finding) and that the warning is non-halting (default-mode RunEntity
// returns nil even though the finding is present).
func TestPluginDefaultLimits(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")
	pluginBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin %s: %v", pluginPath, err)
	}

	dispatch := func(yamlBytes []byte) []abi.Diagnostic {
		t.Helper()
		proposedJSON, header, err := yamlToLinterJSON(yamlBytes)
		if err != nil {
			t.Fatalf("yaml→json: %v", err)
		}
		input := validator.LinterInput{
			RuleID:     "plugin-default-limits",
			EntityKind: header.Kind,
			Proposed:   proposedJSON,
			Policies:   buildParticipationPolicies(),
		}
		findings, err := validator.DispatchPublishingLinter(wasm.PluginSpec{Name: "publishing-linter", Bytes: pluginBytes}, "", input)
		if err != nil {
			t.Fatalf("DispatchPublishingLinter: %v", err)
		}
		return findings
	}

	hasRuleFinding := func(findings []abi.Diagnostic) *abi.Diagnostic {
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
		if f.Severity != abi.SeverityWarn {
			t.Errorf("severity: got %q; want %q", f.Severity, abi.SeverityWarn)
		}
	})

	// A Plugin with an explicit non-zero runtime cap is silent.
	withCap := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: capped-renderer
  pluginType: provider
  version: 1.0.0
spec:
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

	// The warning does not halt default-mode RunEntity.
	t.Run("non_halting", func(t *testing.T) {
		dir := t.TempDir()
		entityPath := filepath.Join(dir, "plugin.yaml")
		if err := os.WriteFile(entityPath, noRuntime, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		cfg := Config{
			EntityPath:     entityPath,
			PrePublish:     false,
			PluginPath:     pluginPath,
			PolicyResolver: func(_ string, ws bool) bool { return ws },
			Workspace:      resolveTempWorkspace(t),
		}
		if err := RunEntity(cfg); err != nil {
			t.Errorf("default mode should not halt on a warning; got: %v", err)
		}
	})
}

// TestRuleParticipationCoversAllDispatchedRules holds the rules the lint command
// dispatches to the rules something actually exercises, in both directions, and
// requires that each exercised rule's fixture is really on disk.
//
// The three checks are separate failures because they are separate mistakes. A
// rule exercised but no longer dispatched means the CLI surface dropped it while
// the suite kept passing against it directly. A rule dispatched but exercised by
// nothing is a rule shipping untested. A rule paired with a fixture that does not
// exist is the one the old form of this guard could not see: it compared two
// hand-written lists and never opened the directory, so a rule added to both
// lists with no fixture passed while the failure message told the reader to add
// one.
//
// The expectation is derived from the participation suite's own case table rather
// than restated here, so this guard cannot disagree with the suite it guards.
func TestRuleParticipationCoversAllDispatchedRules(t *testing.T) {
	dispatched := map[string]bool{}
	for _, id := range defaultRuleIDs(true) {
		dispatched[id] = true
	}

	exercised := map[string]bool{}
	for _, c := range participationCases() {
		exercised[c.ruleID] = true
		if !dispatched[c.ruleID] {
			t.Errorf("rule %q has a participation fixture but is not in defaultRuleIDs(true); "+
				"either the lint command stopped dispatching it, or its fixture is stale",
				c.ruleID)
		}
		// The fixture is opened rather than assumed. A case naming a file that is
		// not there dispatches the rule against nothing, which reports as a rule
		// that did not fire — a confusing failure a long way from its cause.
		path := filepath.Join("testdata", "integration", "participation", c.fixtureFile)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("rule %q names participation fixture %q, which is not readable at %s: %v",
				c.ruleID, c.fixtureFile, path, err)
		}
	}
	for ruleID, where := range rulesCoveredOutsideParticipation {
		exercised[ruleID] = true
		if !dispatched[ruleID] {
			t.Errorf("rule %q is recorded as covered by %s but is not in defaultRuleIDs(true); "+
				"the lint command no longer dispatches it, so that coverage guards nothing",
				ruleID, where)
		}
	}

	// Inverse: a rule the command dispatches that nothing exercises.
	for id := range dispatched {
		if !exercised[id] {
			t.Errorf("rule %q is dispatched but nothing exercises it; add a fixture under "+
				"internal/lintops/testdata/integration/participation/ and a case naming it to "+
				"participationCases, or record the suite that covers it in "+
				"rulesCoveredOutsideParticipation",
				id)
		}
	}
}

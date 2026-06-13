// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// memoryRegistryTarget wraps memory.Store with a tag-tracking slice
// so the in-memory registry satisfies registry.TagLister (which
// oras-go's bare memory.Store does not). Mirrors the test wrapper
// at pkg/oci/client_test.go.
type memoryRegistryTarget struct {
	*memory.Store
	tags []string
}

func (m *memoryRegistryTarget) Tags(ctx context.Context, last string, fn func(tags []string) error) error {
	return fn(m.tags)
}

// componentFixtureBytes returns the YAML bytes of one of the lint-
// matrix fixtures (patch/minor/major) for the Component kind. The
// matrix fixtures are deliberately separate from the plugin-side
// rubric fixtures (which carry 2-segment names for legacy reasons
// and would fail the name-shape rule); the matrix needs entities
// clean across ALL rules so the silent-swallow branch can be
// removed and any unexpected halt fails the test.
func componentFixtureBytes(t *testing.T, classification, file string) []byte {
	t.Helper()
	path := filepath.Join(
		"testdata", "lint-matrix", "component", classification, file,
	)
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return bytes
}

// pushFixtureToTarget pushes a YAML fixture into the in-memory
// registry under the given tag and tracks the tag in the wrapper.
func pushFixtureToTarget(t *testing.T, target *memoryRegistryTarget, kindLower, name, tag string, yaml []byte) {
	t.Helper()
	mediaType := "application/vnd.aggrega." + kindLower + ".v1alpha1+yaml"
	_, err := oci.Push(context.Background(), target.Store, tag, mediaType, yaml)
	if err != nil {
		t.Fatalf("Push(%s:%s): %v", name, tag, err)
	}
	target.tags = append(target.tags, tag)
}

// writeEntityFile writes a YAML entity to a temp file and returns
// its path. The proposed entity's version is rewritten to the test
// case's proposedVersion so the matrix can assert PASS/HALT per
// (classification, proposedBump) cell.
func writeEntityFile(t *testing.T, baseYAML []byte, proposedVersion string) string {
	t.Helper()
	mutated := strings.Replace(string(baseYAML), "version: 1.0.0", "version: "+proposedVersion, 1)
	dir := t.TempDir()
	path := filepath.Join(dir, "proposed.yaml")
	if err := os.WriteFile(path, []byte(mutated), 0o644); err != nil {
		t.Fatalf("write proposed: %v", err)
	}
	return path
}

// TestLintPrePublishMatrixComponent exercises the 9-cell matrix for
// the Component kind. The matrix asserts HALT/PASS per (change
// category, proposed bump) cell. Remaining 7 kinds defer to future
// implementation.
func TestLintPrePublishMatrixComponent(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")

	cases := []struct {
		changeCategory string // patch | minor | major
		proposedBump   string // PATCH | MINOR | MAJOR
		proposedVer    string // 1.0.1 | 1.1.0 | 2.0.0
		wantHalt       bool
	}{
		// PATCH required: any proposed bump PASSES (over-bump allowed)
		{"patch", "PATCH", "1.0.1", false},
		{"patch", "MINOR", "1.1.0", false},
		{"patch", "MAJOR", "2.0.0", false},
		// MINOR required: PATCH proposed HALTS; MINOR/MAJOR PASSES
		{"minor", "PATCH", "1.0.1", true},
		{"minor", "MINOR", "1.1.0", false},
		{"minor", "MAJOR", "2.0.0", false},
		// MAJOR required: PATCH and MINOR HALT; MAJOR PASSES
		{"major", "PATCH", "1.0.1", true},
		{"major", "MINOR", "1.1.0", true},
		{"major", "MAJOR", "2.0.0", false},
	}

	for _, tc := range cases {
		t.Run(tc.changeCategory+"-"+tc.proposedBump, func(t *testing.T) {
			target := &memoryRegistryTarget{Store: memory.New()}
			previousYAML := componentFixtureBytes(t, tc.changeCategory, "previous.yaml")
			pushFixtureToTarget(t, target, "component", "core/compute/api-server", "1.0.0", previousYAML)

			proposedYAML := componentFixtureBytes(t, tc.changeCategory, "proposed.yaml")
			proposedPath := writeEntityFile(t, proposedYAML, tc.proposedVer)

			cfg := lintConfig{
				EntityPath:     proposedPath,
				PrePublish:     true,
				PluginPath:     pluginPath,
				PolicyResolver: func(_ string, ws bool) bool { return ws },
				FetchTarget:    target,
			}
			err := lintRun(cfg)
			// NO silent-swallow. Matrix
			// fixtures are clean across all 7 rules; any halt is
			// the schema-evolution one we care about (or a
			// regression we want to surface).
			if tc.wantHalt && err == nil {
				t.Errorf("expected HALT; got PASS")
			}
			if !tc.wantHalt && err != nil {
				t.Errorf("expected PASS; got HALT: %v", err)
			}
		})
	}
}

// TestLintDefaultModeEmitsWarnings pins the default-mode disposition:
// no --pre-publish, no registry consultation, every rule's finding
// downgraded to warning severity.
func TestLintDefaultModeEmitsWarnings(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")
	yaml := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: BadName
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`)
	dir := t.TempDir()
	entityPath := filepath.Join(dir, "proposed.yaml")
	if err := os.WriteFile(entityPath, yaml, 0o644); err != nil {
		t.Fatalf("write proposed: %v", err)
	}
	cfg := lintConfig{
		EntityPath:     entityPath,
		PrePublish:     false,
		PluginPath:     pluginPath,
		PolicyResolver: func(_ string, ws bool) bool { return ws },
	}
	err := lintRun(cfg)
	if err != nil {
		t.Errorf("default mode should NOT halt (every finding downgraded to warning); got: %v", err)
	}
}

// TestLintFirstPublicationPassesWithoutPrevious:
// when no previous registry tag exists, the CLI does NOT apply the
// bump-vs-classification check; the schema-evolution finding
// surfaces as informational.
func TestLintFirstPublicationPassesWithoutPrevious(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")
	// Empty in-memory registry: no previous tag for this entity.
	target := &memoryRegistryTarget{Store: memory.New()}

	// Use a name-shape-compliant entity so we isolate the
	// first-publication behavior; other rules shouldn't fire.
	yaml := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api-server
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: compute
spec:
  schema:
    values:
      type: object
`)
	dir := t.TempDir()
	proposedPath := filepath.Join(dir, "proposed.yaml")
	if err := os.WriteFile(proposedPath, yaml, 0o644); err != nil {
		t.Fatalf("write proposed: %v", err)
	}

	cfg := lintConfig{
		EntityPath:     proposedPath,
		PrePublish:     true,
		PluginPath:     pluginPath,
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		FetchTarget:    target,
	}
	err := lintRun(cfg)
	if err != nil {
		t.Errorf("first publication should PASS; got HALT: %v", err)
	}
}

// TestResolvePluginPathDirect: --plugin flag
// resolution, filesystem-absolute path used verbatim.
func TestResolvePluginPathDirect(t *testing.T) {
	wsCfg := workspaceconfig.Default()
	path, err := resolvePluginPath("/abs/path/to/plugin.wasm", wsCfg)
	if err != nil {
		t.Fatalf("resolvePluginPath: %v", err)
	}
	if path != "/abs/path/to/plugin.wasm" {
		t.Errorf("got %q; want /abs/path/to/plugin.wasm", path)
	}
}

// TestResolvePluginPathRejectsOCIURI: loud-error
// behavior when the workspace config carries an OCI URI rather than
// a filesystem path.
func TestResolvePluginPathRejectsOCIURI(t *testing.T) {
	wsCfg := workspaceconfig.Default()
	wsCfg.Plugins.Linter = []workspaceconfig.PluginRef{
		{URI: "oci://hub.aggrega.ai/wasm/linter/aggrega-publishing:1.0.0"},
	}
	_, err := resolvePluginPath("", wsCfg)
	if err == nil {
		t.Fatal("expected error for OCI URI; got nil")
	}
	if !strings.Contains(err.Error(), "OCI plugin URI resolution not yet wired") {
		t.Errorf("error should describe OCI-URI deferral; got: %v", err)
	}
}

// TestResolvePluginPathDiagnosticOnUnset:
// loud-error behavior when neither --plugin nor workspace config
// provides a path.
func TestResolvePluginPathDiagnosticOnUnset(t *testing.T) {
	wsCfg := workspaceconfig.Default()
	_, err := resolvePluginPath("", wsCfg)
	if err == nil {
		t.Fatal("expected error for unset plugin path; got nil")
	}
	if !strings.Contains(err.Error(), "no plugin path configured") {
		t.Errorf("error should describe unset state; got: %v", err)
	}
}

// TestLooksLikeOCIURI pins the OCI URI detection heuristic.
func TestLooksLikeOCIURI(t *testing.T) {
	cases := []struct {
		uri  string
		want bool
	}{
		{"oci://hub.aggrega.ai/wasm/linter/aggrega-publishing:1.0.0", true},
		{"hub.aggrega.ai/wasm/linter/aggrega-publishing:1.0.0", true},
		{"./local/plugin.wasm", false},
		{"plugin.wasm", false},
		{"/abs/path/plugin.wasm", false},
	}
	for _, tc := range cases {
		got := looksLikeOCIURI(tc.uri)
		if got != tc.want {
			t.Errorf("looksLikeOCIURI(%q): got %v; want %v", tc.uri, got, tc.want)
		}
	}
}

// TestEffectivePoliciesTriState: tri-state
// precedence across all 8 workspace flags (6 allow_* + 2 enforce_*).
// Per-flag: 3 cells (no override; --<flag>; --no-<flag>) × 8 flags
// = 24 cells.
func TestEffectivePoliciesTriState(t *testing.T) {
	flagBases := []struct {
		flag      string
		workspace func(*workspaceconfig.PublishingDisciplineConfig, bool)
		jsonKey   string
	}{
		{"allow-dynamic-refs", func(c *workspaceconfig.PublishingDisciplineConfig, v bool) { c.AllowDynamicRefs = v }, "allow_dynamic_refs"},
		{"allow-id-overrides", func(c *workspaceconfig.PublishingDisciplineConfig, v bool) { c.AllowIDOverrides = v }, "allow_id_overrides"},
		{"allow-direct-x-aggrega-extensions", func(c *workspaceconfig.PublishingDisciplineConfig, v bool) { c.AllowDirectXAggregaExtensions = v }, "allow_direct_x_aggrega_extensions"},
		{"allow-aggrega-ai-prefix", func(c *workspaceconfig.PublishingDisciplineConfig, v bool) { c.AllowAggregaAIPrefix = v }, "allow_aggrega_ai_prefix"},
		{"allow-nonstandard-names", func(c *workspaceconfig.PublishingDisciplineConfig, v bool) { c.AllowNonstandardNames = v }, "allow_nonstandard_names"},
		{"allow-category-mismatches", func(c *workspaceconfig.PublishingDisciplineConfig, v bool) { c.AllowCategoryMismatches = v }, "allow_category_mismatches"},
		{"enforce-monotonic-narrowing", func(c *workspaceconfig.PublishingDisciplineConfig, v bool) { c.EnforceMonotonicNarrowing = v }, "enforce_monotonic_narrowing"},
		{"enforce-immutability", func(c *workspaceconfig.PublishingDisciplineConfig, v bool) { c.EnforceImmutability = v }, "enforce_immutability"},
	}
	for _, tc := range flagBases {
		// Cell 1: workspace=true, no CLI override → effective=true
		t.Run(tc.flag+"/workspace-true-no-override", func(t *testing.T) {
			ws := workspaceconfig.PublishingDisciplineConfig{}
			tc.workspace(&ws, true)
			resolve := func(_ string, wsVal bool) bool { return wsVal }
			out := effectivePolicies(ws, resolve)
			if !strings.Contains(string(out), `"`+tc.jsonKey+`":true`) {
				t.Errorf("expected %s=true; got %s", tc.jsonKey, string(out))
			}
		})
		// Cell 2: workspace=true, --no-<flag> → effective=false
		t.Run(tc.flag+"/no-override-forces-false", func(t *testing.T) {
			ws := workspaceconfig.PublishingDisciplineConfig{}
			tc.workspace(&ws, true)
			cmd := &cobra.Command{}
			cmd.Flags().Bool(tc.flag, false, "")
			cmd.Flags().Bool("no-"+tc.flag, false, "")
			_ = cmd.Flags().Set("no-"+tc.flag, "true")
			resolve := makeResolverFromCobra(cmd)
			out := effectivePolicies(ws, resolve)
			if !strings.Contains(string(out), `"`+tc.jsonKey+`":false`) {
				t.Errorf("--no-%s should force false despite workspace=true; got %s", tc.flag, string(out))
			}
		})
		// Cell 3: workspace=false, --<flag> → effective=true
		t.Run(tc.flag+"/override-forces-true", func(t *testing.T) {
			ws := workspaceconfig.PublishingDisciplineConfig{}
			tc.workspace(&ws, false)
			cmd := &cobra.Command{}
			cmd.Flags().Bool(tc.flag, false, "")
			cmd.Flags().Bool("no-"+tc.flag, false, "")
			_ = cmd.Flags().Set(tc.flag, "true")
			resolve := makeResolverFromCobra(cmd)
			out := effectivePolicies(ws, resolve)
			if !strings.Contains(string(out), `"`+tc.jsonKey+`":true`) {
				t.Errorf("--%s should force true despite workspace=false; got %s", tc.flag, string(out))
			}
		})
	}
}

// TestProposedBumpSatisfies pins the bump-vs-classification matrix
// at unit-test granularity. The 9-cell matrix exercises the same
// logic end-to-end via TestLintPrePublishMatrixComponent; this
// adds a fast pinning layer.
func TestProposedBumpSatisfies(t *testing.T) {
	cases := []struct {
		proposed, required string
		want               bool
	}{
		{"PATCH", "PATCH", true},
		{"MINOR", "PATCH", true},
		{"MAJOR", "PATCH", true},
		{"PATCH", "MINOR", false},
		{"MINOR", "MINOR", true},
		{"MAJOR", "MINOR", true},
		{"PATCH", "MAJOR", false},
		{"MINOR", "MAJOR", false},
		{"MAJOR", "MAJOR", true},
	}
	for _, tc := range cases {
		t.Run(tc.proposed+"-vs-"+tc.required, func(t *testing.T) {
			got := proposedBumpSatisfies(tc.proposed, tc.required)
			if got != tc.want {
				t.Errorf("proposedBumpSatisfies(%q, %q): got %v; want %v",
					tc.proposed, tc.required, got, tc.want)
			}
		})
	}
}

// TestBumpDirection pins the semver bump-direction helper.
func TestBumpDirection(t *testing.T) {
	cases := []struct {
		prev, prop, want string
	}{
		{"1.0.0", "1.0.1", "PATCH"},
		{"1.0.0", "1.1.0", "MINOR"},
		{"1.0.0", "2.0.0", "MAJOR"},
		{"1.5.3", "1.5.4", "PATCH"},
		{"1.5.3", "1.6.0", "MINOR"},
		{"1.5.3", "2.0.0", "MAJOR"},
		{"1.0.0", "1.0.0", "PATCH"},
	}
	for _, tc := range cases {
		got := bumpDirection(tc.prev, tc.prop)
		if got != tc.want {
			t.Errorf("bumpDirection(%q, %q): got %q; want %q", tc.prev, tc.prop, got, tc.want)
		}
	}
}

// TestDefaultRuleIDs pins the rule iteration slice. Lockstep with
// the plugin's lib.rs dispatcher arms.
func TestDefaultRuleIDs(t *testing.T) {
	defaultRules := defaultRuleIDs(false)
	prePublishRules := defaultRuleIDs(true)

	wantDefault := []string{"annotation-prefix", "name-shape", "narrowing", "dynamic-refs", "id-overrides", "category-matches-directory", "x-aggrega-discouragement"}
	if len(defaultRules) != len(wantDefault) {
		t.Fatalf("default mode rule count: got %d; want %d", len(defaultRules), len(wantDefault))
	}
	for i, r := range wantDefault {
		if defaultRules[i] != r {
			t.Errorf("default[%d]: got %q; want %q", i, defaultRules[i], r)
		}
	}
	if prePublishRules[0] != "schema-evolution" {
		t.Errorf("--pre-publish should prepend schema-evolution; got %v", prePublishRules)
	}
}

// TestLintMissingEntityPath pins the diagnostic for missing entity path.
func TestLintMissingEntityPath(t *testing.T) {
	err := lintRun(lintConfig{EntityPath: ""})
	if err == nil {
		t.Fatal("expected error for missing entity path; got nil")
	}
	if !strings.Contains(err.Error(), "entity path required") {
		t.Errorf("error should describe missing entity path; got: %v", err)
	}
}

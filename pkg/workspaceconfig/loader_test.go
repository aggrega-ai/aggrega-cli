// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// writeFixture writes content to a fresh path under t.TempDir and
// returns the path. The helper avoids per-test boilerplate around
// path resolution + cleanup.
func writeFixture(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".aggrega-config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLoadReturnsAllSettledFields(t *testing.T) {
	t.Parallel()
	content := `
workspace:
  default_registry: my-registry.example.com
  catalog_dirs:
    - vendor/aggrega-catalog
  cache_dir: .aggrega-custom
  offline: true
  cue_registry:
    default: cue-registry.example.com
    overrides:
      - prefix: acme.com
        registry: acme-registry.example.com

build:
  authoring_release: prod-release.yaml
  halt_on_deprecated: true
  halt_on_deprecated_severity: warning

plugins:
  mutator:
    - uri: oci://hub.aggrega.ai/aggrega-team/mutator-bundle@v1
      function: custom_mutate
  policy:
    - uri: oci://hub.aggrega.ai/aggrega-team/policy-bundle@v1
  linter: []
  importer: []

lint:
  publishing_discipline:
    enforce_monotonic_narrowing: false
    enforce_immutability: false
    narrowing_exemptions:
      - entity: my-team/datastore/mariadb-eu
        paths:
          - spec.schema.values.primary.replicaCount
          - spec.schema.values.secondary.replicaCount
        reason: "Single-region deployment; secondary replicas not provisioned."
    allow_dynamic_refs: true
    allow_id_overrides: true
    allow_direct_x_aggrega_extensions: true
    allow_nonstandard_names: true
`
	path := writeFixture(t, content)
	got, err := workspaceconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Workspace.DefaultRegistry != "my-registry.example.com" {
		t.Errorf("Workspace.DefaultRegistry: %q", got.Workspace.DefaultRegistry)
	}
	if len(got.Workspace.CatalogDirs) != 1 || got.Workspace.CatalogDirs[0] != "vendor/aggrega-catalog" {
		t.Errorf("Workspace.CatalogDirs: %+v", got.Workspace.CatalogDirs)
	}
	if got.Workspace.CacheDir != ".aggrega-custom" {
		t.Errorf("Workspace.CacheDir: %q", got.Workspace.CacheDir)
	}
	if !got.Workspace.Offline {
		t.Errorf("Workspace.Offline should be true")
	}
	if got.Workspace.CueRegistry.Default != "cue-registry.example.com" {
		t.Errorf("Workspace.CueRegistry.Default: %q", got.Workspace.CueRegistry.Default)
	}
	if len(got.Workspace.CueRegistry.Overrides) != 1 {
		t.Errorf("Workspace.CueRegistry.Overrides len: %d", len(got.Workspace.CueRegistry.Overrides))
	} else {
		ov := got.Workspace.CueRegistry.Overrides[0]
		if ov.Prefix != "acme.com" || ov.Registry != "acme-registry.example.com" {
			t.Errorf("Workspace.CueRegistry.Overrides[0]: %+v", ov)
		}
	}

	if got.Build.AuthoringRelease != "prod-release.yaml" {
		t.Errorf("Build.AuthoringRelease: %q", got.Build.AuthoringRelease)
	}
	if !got.Build.HaltOnDeprecated {
		t.Errorf("Build.HaltOnDeprecated should be true")
	}
	if got.Build.HaltOnDeprecatedSeverity != "warning" {
		t.Errorf("Build.HaltOnDeprecatedSeverity: %q", got.Build.HaltOnDeprecatedSeverity)
	}

	if len(got.Plugins.Mutator) != 1 {
		t.Fatalf("Plugins.Mutator len: %d", len(got.Plugins.Mutator))
	}
	if got.Plugins.Mutator[0].URI == "" || got.Plugins.Mutator[0].Function != "custom_mutate" {
		t.Errorf("Plugins.Mutator[0]: %+v", got.Plugins.Mutator[0])
	}
	if len(got.Plugins.Policy) != 1 {
		t.Fatalf("Plugins.Policy len: %d", len(got.Plugins.Policy))
	}
	if got.Plugins.Policy[0].Function != "" {
		t.Errorf("Plugins.Policy[0].Function: want empty (use socket default); got %q", got.Plugins.Policy[0].Function)
	}

	pd := got.Lint.PublishingDiscipline
	if pd.EnforceMonotonicNarrowing {
		t.Errorf("EnforceMonotonicNarrowing should be false (file opt-out)")
	}
	if pd.EnforceImmutability {
		t.Errorf("EnforceImmutability should be false (file opt-out)")
	}
	if len(pd.NarrowingExemptions) != 1 {
		t.Fatalf("NarrowingExemptions len: %d", len(pd.NarrowingExemptions))
	}
	ex := pd.NarrowingExemptions[0]
	if ex.Entity != "my-team/datastore/mariadb-eu" {
		t.Errorf("exemption.Entity: %q", ex.Entity)
	}
	if len(ex.Paths) != 2 {
		t.Errorf("exemption.Paths len: %d", len(ex.Paths))
	}
	if ex.Reason == "" {
		t.Errorf("exemption.Reason should be non-empty")
	}
	if !pd.AllowDynamicRefs || !pd.AllowIDOverrides ||
		!pd.AllowDirectXAggregaExtensions || !pd.AllowNonstandardNames {
		t.Errorf("one or more allow_* flags failed to round-trip: %+v", pd)
	}
}

func TestLoadOverlaysDefaultsForOmittedFields(t *testing.T) {
	t.Parallel()
	// Fixture sets workspace.offline only; every other field omitted.
	// The overlay-on-Default pattern preserves the documented defaults
	// for absent keys: DefaultRegistry, CacheDir, AutoPull,
	// HaltOnDeprecatedSeverity, EnforceMonotonicNarrowing, and
	// EnforceImmutability.
	content := `
workspace:
  offline: true
`
	path := writeFixture(t, content)
	got, err := workspaceconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !got.Workspace.Offline {
		t.Errorf("Workspace.Offline: want true; got false")
	}
	if got.Workspace.DefaultRegistry != "hub.aggrega.ai" {
		t.Errorf("Workspace.DefaultRegistry: want %q (Default); got %q",
			"hub.aggrega.ai", got.Workspace.DefaultRegistry)
	}
	if got.Workspace.CacheDir != ".aggrega/cache" {
		t.Errorf("Workspace.CacheDir: want %q (Default); got %q",
			".aggrega/cache", got.Workspace.CacheDir)
	}
	if !got.Workspace.AutoPull {
		t.Errorf("Workspace.AutoPull: want true (Default); got false")
	}
	if got.Build.HaltOnDeprecatedSeverity != "error" {
		t.Errorf("Build.HaltOnDeprecatedSeverity: want %q (Default); got %q",
			"error", got.Build.HaltOnDeprecatedSeverity)
	}
	if !got.Lint.PublishingDiscipline.EnforceMonotonicNarrowing {
		t.Errorf("Lint.PublishingDiscipline.EnforceMonotonicNarrowing: want true (Default); got false")
	}
	if !got.Lint.PublishingDiscipline.EnforceImmutability {
		t.Errorf("Lint.PublishingDiscipline.EnforceImmutability: want true (Default); got false")
	}
}

func TestLoadHonoursAutoPullFalseOverride(t *testing.T) {
	t.Parallel()
	content := `
workspace:
  auto_pull: false
`
	path := writeFixture(t, content)
	got, err := workspaceconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Workspace.AutoPull {
		t.Errorf("Workspace.AutoPull: want false (overridden); got true")
	}
}

func TestLoadHonoursAllowAggregaAIPrefixOverride(t *testing.T) {
	t.Parallel()
	content := `
lint:
  publishing_discipline:
    allow_aggrega_ai_prefix: true
`
	path := writeFixture(t, content)
	got, err := workspaceconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.Lint.PublishingDiscipline.AllowAggregaAIPrefix {
		t.Errorf("Lint.PublishingDiscipline.AllowAggregaAIPrefix: want true (overridden); got false")
	}
}

func TestLoadEmptyFileReturnsDefaults(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, "")
	got, err := workspaceconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, workspaceconfig.Default()) {
		t.Errorf("empty file should yield Default(); got %+v", got)
	}
}

func TestLoadHandlesFileAbsenceAsDefault(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	got, err := workspaceconfig.Load(path)
	if err != nil {
		t.Fatalf("file-absence should not be an error; got %v", err)
	}
	if !reflect.DeepEqual(got, workspaceconfig.Default()) {
		t.Errorf("absent file should yield Default(); got %+v", got)
	}
}

func TestLoadHaltsOnMalformedField(t *testing.T) {
	t.Parallel()
	// narrowing_exemptions declared as a scalar string violates the
	// schema's `type: array` constraint at that field.
	content := `
lint:
  publishing_discipline:
    narrowing_exemptions: "not-a-list"
`
	path := writeFixture(t, content)
	_, err := workspaceconfig.Load(path)
	if err == nil {
		t.Fatal("expected validation error for non-list narrowing_exemptions; got nil")
	}
	if !errors.Is(err, workspaceconfig.ErrConfigInvalid) {
		t.Errorf("error should wrap ErrConfigInvalid; got %v", err)
	}
	if !strings.Contains(err.Error(), "narrowing_exemptions") {
		t.Errorf("error should name the offending field; got: %v", err)
	}
}

func TestLoadHaltsOnNonObjectListEntry(t *testing.T) {
	t.Parallel()
	// narrowing_exemptions is a list-of-objects per the schema; a
	// scalar or wrong-typed entry violates the item schema.
	content := `
lint:
  publishing_discipline:
    narrowing_exemptions:
      - 42
      - entity: my-team/foo
        paths: ["spec.x"]
        reason: ok
`
	path := writeFixture(t, content)
	_, err := workspaceconfig.Load(path)
	if err == nil {
		t.Fatal("expected validation error for non-object entry in narrowing_exemptions; got nil")
	}
	if !errors.Is(err, workspaceconfig.ErrConfigInvalid) {
		t.Errorf("error should wrap ErrConfigInvalid; got %v", err)
	}
	if !strings.Contains(err.Error(), "narrowing_exemptions") {
		t.Errorf("error should name the offending field path; got: %v", err)
	}
}

func TestDefaultReturnsDocumentedDefaults(t *testing.T) {
	t.Parallel()
	d := workspaceconfig.Default()
	if d.Workspace.DefaultRegistry != "hub.aggrega.ai" {
		t.Errorf("Default().Workspace.DefaultRegistry: %q", d.Workspace.DefaultRegistry)
	}
	if d.Workspace.CacheDir != ".aggrega/cache" {
		t.Errorf("Default().Workspace.CacheDir: %q", d.Workspace.CacheDir)
	}
	if d.Workspace.Offline {
		t.Errorf("Default().Workspace.Offline should be false")
	}
	if d.Build.HaltOnDeprecated {
		t.Errorf("Default().Build.HaltOnDeprecated should be false")
	}
	if d.Build.HaltOnDeprecatedSeverity != "error" {
		t.Errorf("Default().Build.HaltOnDeprecatedSeverity: %q", d.Build.HaltOnDeprecatedSeverity)
	}
	pd := d.Lint.PublishingDiscipline
	if !pd.EnforceMonotonicNarrowing {
		t.Errorf("Default().Lint.PublishingDiscipline.EnforceMonotonicNarrowing should be true")
	}
	if !pd.EnforceImmutability {
		t.Errorf("Default().Lint.PublishingDiscipline.EnforceImmutability should be true")
	}
	if pd.AllowDynamicRefs || pd.AllowIDOverrides ||
		pd.AllowDirectXAggregaExtensions || pd.AllowNonstandardNames {
		t.Errorf("Default() allow_* flags must all be false; got %+v", pd)
	}
	if len(pd.NarrowingExemptions) != 0 {
		t.Errorf("Default().Lint.PublishingDiscipline.NarrowingExemptions must be empty; got %v", pd.NarrowingExemptions)
	}
}

func TestLoadRejectsUnknownTopLevelKey(t *testing.T) {
	t.Parallel()
	// The schema declares additionalProperties: false at the root; an
	// unknown top-level key surfaces as a validation error rather than
	// being silently dropped.
	content := `unknown_block: true`
	path := writeFixture(t, content)
	_, err := workspaceconfig.Load(path)
	if err == nil {
		t.Fatal("expected validation error for unknown top-level key; got nil")
	}
	if !errors.Is(err, workspaceconfig.ErrConfigInvalid) {
		t.Errorf("error should wrap ErrConfigInvalid; got %v", err)
	}
}

func TestLoadRejectsHaltOnDeprecatedSeverityOutsideEnum(t *testing.T) {
	t.Parallel()
	// build.halt_on_deprecated_severity is constrained to one of
	// ["error", "warning"]; "info" violates the enum.
	content := `
build:
  halt_on_deprecated_severity: info
`
	path := writeFixture(t, content)
	_, err := workspaceconfig.Load(path)
	if err == nil {
		t.Fatal("expected validation error for halt_on_deprecated_severity outside enum; got nil")
	}
	if !errors.Is(err, workspaceconfig.ErrConfigInvalid) {
		t.Errorf("error should wrap ErrConfigInvalid; got %v", err)
	}
	if !strings.Contains(err.Error(), "halt_on_deprecated_severity") {
		t.Errorf("error should name the offending field; got: %v", err)
	}
}

func TestPluginRefRoundTripPreservesOptionalFunction(t *testing.T) {
	t.Parallel()
	// plugins.mutator carries function: custom_mutate (override);
	// plugins.policy omits function (use socket default). Both
	// must round-trip; the function-absent entry's PluginRef.Function
	// must be empty-string (sentinel for "use socket default").
	content := `
plugins:
  mutator:
    - uri: oci://hub.aggrega.ai/team/mutator@v1
      function: custom_mutate
  policy:
    - uri: oci://hub.aggrega.ai/team/policy@v1
`
	path := writeFixture(t, content)
	got, err := workspaceconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Plugins.Mutator) != 1 || got.Plugins.Mutator[0].Function != "custom_mutate" {
		t.Errorf("Plugins.Mutator: %+v", got.Plugins.Mutator)
	}
	if len(got.Plugins.Policy) != 1 || got.Plugins.Policy[0].Function != "" {
		t.Errorf("Plugins.Policy: %+v", got.Plugins.Policy)
	}
}

func TestLoadExplicitEmptyMapHonoursYAMLSemantics(t *testing.T) {
	t.Parallel()
	// Fixture sets workspace, build, and lint as explicit empty maps.
	// yaml.v3's recursive unmarshal into an existing value only writes
	// fields present in the source mapping. An empty mapping has no
	// fields to write, so the Default()-seeded values should survive.
	// The test pins the actual behaviour; if a future yaml.v3 upgrade
	// changes the disposition the test catches it.
	content := `
workspace: {}
build: {}
lint: {}
`
	path := writeFixture(t, content)
	got, err := workspaceconfig.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Documented defaults must survive the empty-mapping pass.
	if got.Workspace.DefaultRegistry != "hub.aggrega.ai" {
		t.Errorf("Workspace.DefaultRegistry: want %q (Default preserved); got %q",
			"hub.aggrega.ai", got.Workspace.DefaultRegistry)
	}
	if got.Workspace.CacheDir != ".aggrega/cache" {
		t.Errorf("Workspace.CacheDir: want %q (Default preserved); got %q",
			".aggrega/cache", got.Workspace.CacheDir)
	}
	if got.Build.HaltOnDeprecatedSeverity != "error" {
		t.Errorf("Build.HaltOnDeprecatedSeverity: want %q (Default preserved); got %q",
			"error", got.Build.HaltOnDeprecatedSeverity)
	}
	if !got.Lint.PublishingDiscipline.EnforceMonotonicNarrowing {
		t.Errorf("Lint.PublishingDiscipline.EnforceMonotonicNarrowing: want true (Default preserved); got false")
	}
	if !got.Lint.PublishingDiscipline.EnforceImmutability {
		t.Errorf("Lint.PublishingDiscipline.EnforceImmutability: want true (Default preserved); got false")
	}
}

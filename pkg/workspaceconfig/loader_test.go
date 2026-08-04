// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
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
default_registry: my-registry.example.com
artifact_cache_dir: .aggrega-custom
offline: true
workspace:
  catalog_dirs:
    - vendor/aggrega-catalog

build:
  authoring_release: prod-release.yaml
  halt_on_deprecated: true
  halt_on_deprecated_severity: warning

plugins:
  governance:
    - uri: oci://hub.aggrega.ai/aggrega-team/mutator-bundle@v1
      function: custom_mutate
      role: label-transform
    - uri: oci://hub.aggrega.ai/aggrega-team/policy-bundle@v1
      role: ir-verdict
  linter: []
  importer: []

lint:
  publishing_discipline:
    enforce_immutability: false
    allow_dynamic_refs: true
    allow_id_overrides: true
    allow_direct_x_aggrega_extensions: true
    allow_nonstandard_names: true
    allow_category_mismatches: true
`
	path := writeFixture(t, content)
	got, err := load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.DefaultRegistry != "my-registry.example.com" {
		t.Errorf("Workspace.DefaultRegistry: %q", got.DefaultRegistry)
	}
	if len(got.Workspace.CatalogDirs) != 1 || got.Workspace.CatalogDirs[0] != "vendor/aggrega-catalog" {
		t.Errorf("Workspace.CatalogDirs: %+v", got.Workspace.CatalogDirs)
	}
	if got.ArtifactCacheDir != ".aggrega-custom" {
		t.Errorf("Workspace.CacheDir: %q", got.ArtifactCacheDir)
	}
	if !got.Offline {
		t.Errorf("Workspace.Offline should be true")
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

	mut := got.Plugins.GovernanceRefsForRole(plugintype.RoleLabelTransform)
	if len(mut) != 1 {
		t.Fatalf("governance label-transform len: %d", len(mut))
	}
	if mut[0].URI == "" || mut[0].Function != "custom_mutate" {
		t.Errorf("governance label-transform[0]: %+v", mut[0])
	}
	pol := got.Plugins.GovernanceRefsForRole(plugintype.RoleIRVerdict)
	if len(pol) != 1 {
		t.Fatalf("governance ir-verdict len: %d", len(pol))
	}
	if pol[0].Function != "" {
		t.Errorf("governance ir-verdict[0].Function: want empty (use socket default); got %q", pol[0].Function)
	}

	pd := got.Lint.PublishingDiscipline
	if pd.EnforceImmutability {
		t.Errorf("EnforceImmutability should be false (file opt-out)")
	}
	if !pd.AllowDynamicRefs || !pd.AllowIDOverrides ||
		!pd.AllowDirectXAggregaExtensions || !pd.AllowNonstandardNames ||
		!pd.AllowCategoryMismatches {
		t.Errorf("one or more allow_* flags failed to round-trip: %+v", pd)
	}
}

func TestLoadOverlaysDefaultsForOmittedFields(t *testing.T) {
	t.Parallel()
	// Fixture sets workspace.offline only; every other field omitted.
	// The overlay-on-Default pattern preserves the documented defaults
	// for absent keys: DefaultRegistry, CacheDir, and
	// EnforceImmutability. AutoPull is the Go zero-value (false), and so
	// is HaltOnDeprecatedSeverity, whose documented default is unset.
	content := `
offline: true
`
	path := writeFixture(t, content)
	got, err := load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !got.Offline {
		t.Errorf("Workspace.Offline: want true; got false")
	}
	if got.DefaultRegistry != "hub.aggrega.ai" {
		t.Errorf("Workspace.DefaultRegistry: want %q (Default); got %q",
			"hub.aggrega.ai", got.DefaultRegistry)
	}
	if got.ArtifactCacheDir != ".aggrega/cache" {
		t.Errorf("Workspace.CacheDir: want %q (Default); got %q",
			".aggrega/cache", got.ArtifactCacheDir)
	}
	if got.AutoPull {
		t.Errorf("Workspace.AutoPull: want false (Default); got true")
	}
	if got.Build.HaltOnDeprecatedSeverity != "" {
		t.Errorf("Build.HaltOnDeprecatedSeverity: want %q (Default: no halt threshold); got %q",
			"", got.Build.HaltOnDeprecatedSeverity)
	}
	if !got.Lint.PublishingDiscipline.EnforceImmutability {
		t.Errorf("Lint.PublishingDiscipline.EnforceImmutability: want true (Default); got false")
	}
}

func TestLoadHonoursAutoPullFalseOverride(t *testing.T) {
	t.Parallel()
	content := `
auto_pull: false
`
	path := writeFixture(t, content)
	got, err := load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AutoPull {
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
	got, err := load(path)
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
	got, err := load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, Default()) {
		t.Errorf("empty file should yield Default(); got %+v", got)
	}
}

func TestLoadHandlesFileAbsenceAsDefault(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	got, err := load(path)
	if err != nil {
		t.Fatalf("file-absence should not be an error; got %v", err)
	}
	if !reflect.DeepEqual(got, Default()) {
		t.Errorf("absent file should yield Default(); got %+v", got)
	}
}

func TestLoadHaltsOnMalformedField(t *testing.T) {
	t.Parallel()
	// governance declared as a scalar string violates the schema's
	// `type: array` constraint at that field.
	content := `
plugins:
  governance: "not-a-list"
`
	path := writeFixture(t, content)
	_, err := load(path)
	if err == nil {
		t.Fatal("expected validation error for non-list governance; got nil")
	}
	if !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("error should wrap ErrConfigInvalid; got %v", err)
	}
	if !strings.Contains(err.Error(), "governance") {
		t.Errorf("error should name the offending field; got: %v", err)
	}
}

func TestLoadHaltsOnNonObjectListEntry(t *testing.T) {
	t.Parallel()
	// governance is a list-of-objects per the schema; a scalar or
	// wrong-typed entry violates the item schema.
	content := `
plugins:
  governance:
    - 42
    - uri: oci://hub.aggrega.ai/aggrega-team/policy-bundle@v1
      role: ir-verdict
`
	path := writeFixture(t, content)
	_, err := load(path)
	if err == nil {
		t.Fatal("expected validation error for non-object entry in governance; got nil")
	}
	if !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("error should wrap ErrConfigInvalid; got %v", err)
	}
	if !strings.Contains(err.Error(), "governance") {
		t.Errorf("error should name the offending field path; got: %v", err)
	}
}

func TestDefaultReturnsDocumentedDefaults(t *testing.T) {
	t.Parallel()
	d := Default()
	if d.DefaultRegistry != "hub.aggrega.ai" {
		t.Errorf("Default().DefaultRegistry: %q", d.DefaultRegistry)
	}
	if d.ArtifactCacheDir != ".aggrega/cache" {
		t.Errorf("Default().ArtifactCacheDir: %q", d.ArtifactCacheDir)
	}
	if d.Offline {
		t.Errorf("Default().Offline should be false")
	}
	if d.AutoPull {
		t.Errorf("Default().AutoPull should be false: build is read-only by default")
	}
	if d.Build.HaltOnDeprecated {
		t.Errorf("Default().Build.HaltOnDeprecated should be false")
	}
	// The documented default is UNSET: no recorded deprecation marker halts a
	// build the workspace did not configure to halt.
	if d.Build.HaltOnDeprecatedSeverity != "" {
		t.Errorf("Default().Build.HaltOnDeprecatedSeverity: want %q; got %q", "", d.Build.HaltOnDeprecatedSeverity)
	}
	pd := d.Lint.PublishingDiscipline
	if !pd.EnforceImmutability {
		t.Errorf("Default().Lint.PublishingDiscipline.EnforceImmutability should be true")
	}
	if pd.AllowDynamicRefs || pd.AllowIDOverrides ||
		pd.AllowDirectXAggregaExtensions || pd.AllowNonstandardNames ||
		pd.AllowCategoryMismatches {
		t.Errorf("Default() allow_* flags must all be false; got %+v", pd)
	}
}

func TestLoadRejectsUnknownTopLevelKey(t *testing.T) {
	t.Parallel()
	// The schema declares additionalProperties: false at the root; an
	// unknown top-level key surfaces as a validation error rather than
	// being silently dropped.
	content := `unknown_block: true`
	path := writeFixture(t, content)
	_, err := load(path)
	if err == nil {
		t.Fatal("expected validation error for unknown top-level key; got nil")
	}
	if !errors.Is(err, ErrConfigInvalid) {
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
	_, err := load(path)
	if err == nil {
		t.Fatal("expected validation error for halt_on_deprecated_severity outside enum; got nil")
	}
	if !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("error should wrap ErrConfigInvalid; got %v", err)
	}
	if !strings.Contains(err.Error(), "halt_on_deprecated_severity") {
		t.Errorf("error should name the offending field; got: %v", err)
	}
}

func TestPluginRefRoundTripPreservesOptionalFunction(t *testing.T) {
	t.Parallel()
	// the label-transform entry carries function: custom_mutate (override);
	// the ir-verdict entry omits function (use socket default). Both
	// must round-trip; the function-absent entry's PluginRef.Function
	// must be empty-string (sentinel for "use socket default").
	content := `
plugins:
  governance:
    - uri: oci://hub.aggrega.ai/team/mutator@v1
      function: custom_mutate
      role: label-transform
    - uri: oci://hub.aggrega.ai/team/policy@v1
      role: ir-verdict
`
	path := writeFixture(t, content)
	got, err := load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	mut := got.Plugins.GovernanceRefsForRole(plugintype.RoleLabelTransform)
	if len(mut) != 1 || mut[0].Function != "custom_mutate" {
		t.Errorf("governance label-transform: %+v", mut)
	}
	pol := got.Plugins.GovernanceRefsForRole(plugintype.RoleIRVerdict)
	if len(pol) != 1 || pol[0].Function != "" {
		t.Errorf("governance ir-verdict: %+v", pol)
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
	got, err := load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Documented defaults must survive the empty-mapping pass.
	if got.DefaultRegistry != "hub.aggrega.ai" {
		t.Errorf("Workspace.DefaultRegistry: want %q (Default preserved); got %q",
			"hub.aggrega.ai", got.DefaultRegistry)
	}
	if got.ArtifactCacheDir != ".aggrega/cache" {
		t.Errorf("Workspace.CacheDir: want %q (Default preserved); got %q",
			".aggrega/cache", got.ArtifactCacheDir)
	}
	if got.Build.HaltOnDeprecatedSeverity != "" {
		t.Errorf("Build.HaltOnDeprecatedSeverity: want %q (Default preserved: no halt threshold); got %q",
			"", got.Build.HaltOnDeprecatedSeverity)
	}
	if !got.Lint.PublishingDiscipline.EnforceImmutability {
		t.Errorf("Lint.PublishingDiscipline.EnforceImmutability: want true (Default preserved); got false")
	}
}

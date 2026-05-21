// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TestInterpolateParameters_ValidTemplate(t *testing.T) {
	raw := []byte(`name: {{ .parameters.app_name }}
port: {{ .parameters.port | default "8080" }}`)
	params := map[string]any{
		"app_name": "my-api",
		"port":     "3000",
	}

	result, err := InterpolateParameters(raw, params)
	if err != nil {
		t.Fatal(err)
	}
	s := string(result)
	if !strings.Contains(s, "name: my-api") {
		t.Errorf("expected interpolated app_name, got:\n%s", s)
	}
	if !strings.Contains(s, "port: 3000") {
		t.Errorf("expected interpolated port, got:\n%s", s)
	}
}

func TestInterpolateParameters_SprigDefault(t *testing.T) {
	raw := []byte(`port: {{ .parameters.port | default "8080" }}`)
	params := map[string]any{}

	result, err := InterpolateParameters(raw, params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), "port: 8080") {
		t.Errorf("expected default value, got:\n%s", string(result))
	}
}

func TestInterpolateParameters_SprigUpper(t *testing.T) {
	raw := []byte(`env: {{ .parameters.env | upper }}`)
	params := map[string]any{"env": "production"}

	result, err := InterpolateParameters(raw, params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), "env: PRODUCTION") {
		t.Errorf("expected upper case, got:\n%s", string(result))
	}
}

func TestInterpolateParameters_MissingParamProducesEmpty(t *testing.T) {
	// With missingkey=zero, missing params produce empty strings (Helm/Sprig convention).
	// Developers use Sprig's `required` function to mandate a parameter.
	raw := []byte(`name: {{ .parameters.missing_key }}`)
	params := map[string]any{}

	result, err := InterpolateParameters(raw, params)
	if err != nil {
		t.Fatalf("expected no error for missing key (zero value), got: %v", err)
	}
	if !strings.Contains(string(result), "name: ") {
		t.Errorf("expected empty interpolation, got:\n%s", string(result))
	}
}

func TestInterpolateParameters_SprigRequired(t *testing.T) {
	// Sprig's `required` function enforces mandatory parameters
	raw := []byte(`name: {{ .parameters.app_name | required "app_name is required" }}`)
	params := map[string]any{}

	_, err := InterpolateParameters(raw, params)
	if err == nil {
		t.Fatal("expected error from Sprig required function")
	}
	if !errors.Is(err, ErrInvalidParameterRef) {
		t.Errorf("expected ErrInvalidParameterRef, got %v", err)
	}
}

func TestLoadParametersImport_SingleFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "params.yaml")
	os.WriteFile(f, []byte("db_host: localhost\ndb_port: 5432"), 0644)

	params, err := LoadParametersImport(f)
	if err != nil {
		t.Fatal(err)
	}
	if params["db_host"] != "localhost" {
		t.Errorf("expected db_host=localhost, got %v", params["db_host"])
	}
}

// TestLoadParametersImport_FileNotFound verifies the error path: a
// non-existent path produces ErrParametersImportLoad. The pre-migration
// deep-merge-over-list test (TestLoadParametersFrom_DeepMerge) retires
// alongside the list-form retirement; multi-source layering migrates
// to Release extends: chains, which are exercised by their own tests.
func TestLoadParametersImport_FileNotFound(t *testing.T) {
	_, err := LoadParametersImport("/nonexistent/params.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, ErrParametersImportLoad) {
		t.Errorf("expected ErrParametersImportLoad, got %v", err)
	}
}

func TestMergeParameters_ReleaseOverridesExternal(t *testing.T) {
	inline := types.OrderedMap[any]{}
	inline.Set("key", "release-val")
	release := types.ParametersBlock{Inline: inline}
	external := map[string]any{"key": "external-val", "extra": "ext"}

	merged := MergeParameters(release, external)
	if merged["key"] != "release-val" {
		t.Errorf("release should override external, got %v", merged["key"])
	}
	if merged["extra"] != "ext" {
		t.Errorf("external-only key should survive, got %v", merged["extra"])
	}
}

func TestPurgeParameters(t *testing.T) {
	params := map[string]any{
		"secret_key":  "s3cr3t",
		"db_password": "p@ss",
	}
	PurgeParameters(&params)
	if params != nil {
		t.Errorf("expected nil after purge, got %v", params)
	}
}

// TestOrderedMapToPlain_NestedOrderedMapShapes_RecursivelyUnwraps pins
// orderedMapToPlain's delegation to TemplateAlphabeticalMapDeep. The fixture
// shape mirrors what round-trip-fidelity JSON/YAML unmarshal produces for a
// nested Release.spec.parameters block: an outer OrderedMap[any] whose
// values include nested *OrderedMap[any] entries. After conversion the
// result must be a fully-unwrapped map[string]any walkable by Go templates
// via dotted field-access syntax (.parameters.db.host).
func TestOrderedMapToPlain_NestedOrderedMapShapes_RecursivelyUnwraps(t *testing.T) {
	t.Parallel()

	dbInfra := types.NewOrderedMap[any]()
	dbInfra.Set("host", "db.svc.cluster.local")
	dbInfra.Set("port", "5432")

	release := types.OrderedMap[any]{}
	release.Set("db", dbInfra)
	release.Set("app_name", "billing-api")

	result := orderedMapToPlain(release)
	if result == nil {
		t.Fatal("orderedMapToPlain returned nil for non-empty input")
	}

	dbAny, ok := result["db"]
	if !ok {
		t.Fatal("result missing key 'db'")
	}
	dbMap, ok := dbAny.(map[string]any)
	if !ok {
		t.Fatalf("result[db] type = %T, want map[string]any (recursive unwrap should have flattened the nested OrderedMap)", dbAny)
	}
	if got, _ := dbMap["host"].(string); got != "db.svc.cluster.local" {
		t.Errorf("result[db][host] = %v, want db.svc.cluster.local", dbMap["host"])
	}

	tmpl, err := template.New("test").Parse(`{{ .parameters.db.host }}:{{ .parameters.db.port }}`)
	if err != nil {
		t.Fatalf("template parse: %v", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, map[string]any{"parameters": result}); err != nil {
		t.Fatalf("template execute: %v", err)
	}
	got := buf.String()
	want := "db.svc.cluster.local:5432"
	if got != want {
		t.Errorf("template output = %q, want %q", got, want)
	}
}

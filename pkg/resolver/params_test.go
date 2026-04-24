// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestInterpolateParameters_ValidTemplate(t *testing.T) {
	raw := []byte(`name: {{ .parameters.app_name }}
port: {{ .parameters.port | default "8080" }}`)
	params := map[string]interface{}{
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
	params := map[string]interface{}{}

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
	params := map[string]interface{}{"env": "production"}

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
	params := map[string]interface{}{}

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
	params := map[string]interface{}{}

	_, err := InterpolateParameters(raw, params)
	if err == nil {
		t.Fatal("expected error from Sprig required function")
	}
	if !errors.Is(err, ErrInvalidParameterRef) {
		t.Errorf("expected ErrInvalidParameterRef, got %v", err)
	}
}

func TestLoadParametersFrom_SingleFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "params.yaml")
	os.WriteFile(f, []byte("db_host: localhost\ndb_port: 5432"), 0644)

	params, err := LoadParametersFrom([]string{f})
	if err != nil {
		t.Fatal(err)
	}
	if params["db_host"] != "localhost" {
		t.Errorf("expected db_host=localhost, got %v", params["db_host"])
	}
}

func TestLoadParametersFrom_DeepMerge(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "base.yaml")
	f2 := filepath.Join(dir, "override.yaml")
	os.WriteFile(f1, []byte("db:\n  host: base-host\n  port: 5432"), 0644)
	os.WriteFile(f2, []byte("db:\n  host: override-host\n  name: mydb"), 0644)

	params, err := LoadParametersFrom([]string{f1, f2})
	if err != nil {
		t.Fatal(err)
	}
	db, ok := params["db"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected db to be map, got %T", params["db"])
	}
	if db["host"] != "override-host" {
		t.Errorf("expected overridden host, got %v", db["host"])
	}
	if db["port"] != 5432 {
		t.Errorf("expected inherited port, got %v", db["port"])
	}
	if db["name"] != "mydb" {
		t.Errorf("expected added name, got %v", db["name"])
	}
}

func TestLoadParametersFrom_FileNotFound(t *testing.T) {
	_, err := LoadParametersFrom([]string{"/nonexistent/params.yaml"})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, ErrParametersFromLoad) {
		t.Errorf("expected ErrParametersFromLoad, got %v", err)
	}
}

func TestMergeParameters_ReleaseOverridesExternal(t *testing.T) {
	release := types.OrderedMap[interface{}]{}
	release.Set("key", "release-val")
	external := map[string]interface{}{"key": "external-val", "extra": "ext"}

	merged := MergeParameters(release, external)
	if merged["key"] != "release-val" {
		t.Errorf("release should override external, got %v", merged["key"])
	}
	if merged["extra"] != "ext" {
		t.Errorf("external-only key should survive, got %v", merged["extra"])
	}
}

func TestPurgeParameters(t *testing.T) {
	params := map[string]interface{}{
		"secret_key":  "s3cr3t",
		"db_password": "p@ss",
	}
	PurgeParameters(&params)
	if params != nil {
		t.Errorf("expected nil after purge, got %v", params)
	}
}

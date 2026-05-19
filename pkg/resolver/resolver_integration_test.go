// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestResolve_Integration(t *testing.T) {
	tests := []struct {
		name     string
		fixtures string // subdirectory under testdata/
		wantErr  error  // nil for success, sentinel for expected error
		validate func(t *testing.T, result *ResolveResult)
	}{
		{
			name:     "valid aggregate resolves successfully",
			fixtures: "valid",
			wantErr:  nil,
			validate: func(t *testing.T, result *ResolveResult) {
				t.Helper()
				if result.Aggregate == nil {
					t.Fatal("expected non-nil Aggregate")
				}
				if result.Aggregate.Metadata.Name != "test-app" {
					t.Errorf("expected name test-app, got %s", result.Aggregate.Metadata.Name)
				}
				if _, ok := result.Aggregate.Spec.Components.Get("web"); !ok {
					t.Error("expected component 'web'")
				}
				if _, ok := result.Aggregate.Spec.Components.Get("worker"); !ok {
					t.Error("expected component 'worker'")
				}
				if result.Release == nil {
					t.Fatal("expected non-nil Release")
				}
				if result.Release.Metadata.Environment != "local" {
					t.Errorf("expected environment local, got %s", result.Release.Metadata.Environment)
				}
				if result.Manifest == nil {
					t.Fatal("expected non-nil Manifest")
				}
			},
		},
		{
			name:     "parameter interpolation resolves template expressions",
			fixtures: "parameter-merge-and-fallback",
			wantErr:  nil,
			validate: func(t *testing.T, result *ResolveResult) {
				t.Helper()
				comp, ok := result.Aggregate.Spec.Components.Get("api")
				if !ok {
					t.Fatal("expected component 'api'")
				}
				ttl, _ := comp.Values.Get("cache_ttl_seconds")
				if fmt.Sprint(ttl) != "8080" {
					t.Errorf("expected interpolated cache_ttl_seconds 8080, got %v", ttl)
				}
				nameVal, _ := comp.Values.Get("display_name")
				name, _ := nameVal.(string)
				if name != "staging-db.internal" {
					t.Errorf("expected interpolated display_name staging-db.internal, got %s", name)
				}
			},
		},
		{
			name:     "boundary infra wired to components",
			fixtures: "valid",
			wantErr:  nil,
			validate: func(t *testing.T, result *ResolveResult) {
				t.Helper()
				if result.BoundaryInfra == nil {
					t.Fatal("expected non-nil BoundaryInfra")
				}
				webInfra, ok := result.BoundaryInfra["web"]
				if !ok {
					t.Fatal("expected BoundaryInfra for 'web'")
				}
				if webInfra.BoundaryPath != "local" {
					t.Errorf("expected boundary path 'local', got %s", webInfra.BoundaryPath)
				}
				if webInfra.Infra.Len() == 0 {
					t.Error("expected non-empty infra for 'web'")
				}
			},
		},
		{
			name:     "dot in instance name produces ErrInvalidInstanceName",
			fixtures: "bad-name",
			wantErr:  ErrInvalidInstanceName,
		},
		{
			name:    "missing aggregate file produces ErrYAMLLoad",
			wantErr: ErrYAMLLoad,
			validate: func(t *testing.T, result *ResolveResult) {
				// Should not reach here — wantErr is set
			},
		},
		{
			name:     "typed parameters pass Step 1a validation end-to-end",
			fixtures: "typed-params-pass",
			wantErr:  nil,
			validate: func(t *testing.T, result *ResolveResult) {
				t.Helper()
				if result.Aggregate == nil {
					t.Fatal("expected non-nil Aggregate")
				}
				if result.Parameters["cache_ttl_seconds"] != 9090 {
					t.Errorf("cache_ttl_seconds = %v; want 9090", result.Parameters["cache_ttl_seconds"])
				}
				if result.Parameters["env"] != "prod" {
					t.Errorf("env = %v; want prod", result.Parameters["env"])
				}
			},
		},
		{
			name:     "typed parameters fail enum + range constraints at Step 1a",
			fixtures: "typed-params-fail",
			wantErr:  ErrParameterContractViolation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var cfg ResolveConfig
			if tt.fixtures != "" {
				base := filepath.Join("testdata", tt.fixtures)
				cfg = ResolveConfig{
					AggregatePath: filepath.Join(base, "aggregate.yaml"),
					ReleasePath:   filepath.Join(base, "release.yaml"),
					ManifestPath:  filepath.Join(base, "manifest.yaml"),
				}
			} else {
				// Missing file test
				cfg = ResolveConfig{
					AggregatePath: "/nonexistent/aggregate.yaml",
					ReleasePath:   "/nonexistent/release.yaml",
					ManifestPath:  "/nonexistent/manifest.yaml",
				}
			}

			result, err := Resolve(cfg)

			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.validate != nil {
				tt.validate(t, result)
			}
		})
	}
}

func TestResolve_ParametersImportFromFile(t *testing.T) {
	// Test that Release.spec.parameters.$import loads parameter values
	// from the referenced single-source file. The pre-migration list-
	// form (parametersFrom: [a, b, ...]) retires with the file-import
	// vocabulary unification; multi-source layering migrates to
	// Release extends: chains. This test exercises the post-migration
	// single-source $import: dispatch path.
	base := filepath.Join("testdata", "parameter-merge-and-fallback")

	dir := t.TempDir()
	releaseYAML := `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: param-release
  environment: staging
  version: "1.0.0"
spec:
  aggregate: param-app
  manifest: param-manifest
  parameters:
    $import: ` + filepath.Join(base, "parameters.yaml") + `
  boundaries:
    staging:
      directory: ./output
`
	relPath := filepath.Join(dir, "release.yaml")
	os.WriteFile(relPath, []byte(releaseYAML), 0644)

	cfg := ResolveConfig{
		AggregatePath: filepath.Join(base, "aggregate.yaml"),
		ReleasePath:   relPath,
		ManifestPath:  filepath.Join(base, "manifest.yaml"),
	}

	result, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The imported parameter values flow into the Aggregate's
	// templated values via the standard interpolation path.
	comp, _ := result.Aggregate.Spec.Components.Get("api")
	if comp.Values.Len() == 0 {
		t.Fatal("expected interpolated values on api component")
	}
}

func TestResolve_ParameterLifecycle(t *testing.T) {
	// After Resolve completes, the internal parameter map should have been purged.
	// We verify indirectly: the result should not contain raw parameter values
	// in the Aggregate (they should be interpolated, not template expressions).
	cfg := ResolveConfig{
		AggregatePath: filepath.Join("testdata", "parameter-merge-and-fallback", "aggregate.yaml"),
		ReleasePath:   filepath.Join("testdata", "parameter-merge-and-fallback", "release.yaml"),
		ManifestPath:  filepath.Join("testdata", "parameter-merge-and-fallback", "manifest.yaml"),
	}

	result, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify parameters were interpolated (not raw template expressions)
	comp, _ := result.Aggregate.Spec.Components.Get("api")
	ttl, _ := comp.Values.Get("cache_ttl_seconds")
	ttlStr := fmt.Sprint(ttl)
	if ttlStr == "{{ .parameters.cache_ttl_seconds | default \"3000\" }}" {
		t.Error("parameter expression was not interpolated")
	}
	if ttlStr != "8080" {
		t.Errorf("expected interpolated cache_ttl_seconds 8080, got %v", ttl)
	}
}

// TestResolve_ParameterDefaultBackfill_E2E exercises the three-case
// semantic for nested-object parameters through the full Resolve
// pipeline (parameter merge → default-fill → contract validation →
// template interpolation). Three Release variants share the same
// Aggregate (declaring `database` as `type: object` with child
// defaults plus a flat-scalar default for `timeout_seconds`):
//
//   - release-omit-parent: Release omits `database` entirely. Case 1
//     synthesizes the parent and fills children's defaults; the
//     interpolated `db_host` resolves to "localhost".
//
//   - release-empty-parent: Release authors `database: {}`. Case 2
//     preserves the empty parent; children's defaults are NOT
//     filled; `.parameters.database.host` is absent at interpolation
//     time, so under missingkey=zero it renders as the empty string.
//     The empty-parent case is counterintuitive for users coming
//     from ecosystems where `{}` is equivalent to omission — see
//     the LDR Evolution note and parameters-context.md for the
//     design rationale.
//
//   - release-partial-parent: Release authors `database: { host:
//     "db.prod.example.com" }`. Case 3 merges the Release-authored
//     `host` with the contract's default for sibling fields; the
//     interpolated `db_host` resolves to "db.prod.example.com".
//
// All three variants additionally exercise the flat-scalar default
// case via `timeout_seconds: { type: integer, default: 30 }` which
// every Release omits — `.values.timeout` resolves to "30" across
// the variants.
func TestResolve_ParameterDefaultBackfill_E2E(t *testing.T) {
	cases := []struct {
		name         string
		releaseFile  string
		wantTimeout  string
		wantDBHost   string
		dbHostNote   string
	}{
		{
			name:        "case1_omit_parent_synthesizes_and_fills",
			releaseFile: "release-omit-parent.yaml",
			wantTimeout: "30",
			wantDBHost:  "localhost",
			dbHostNote:  "fills from contract default",
		},
		{
			name:        "case2_empty_parent_suppresses_children_defaults",
			releaseFile: "release-empty-parent.yaml",
			wantTimeout: "30",
			// Explicit `database: {}` opts out of the children's
			// defaults; .parameters.database.host is absent at
			// interpolation time → missingkey=zero renders empty.
			wantDBHost: "<no value>",
			dbHostNote: "empty parent opts out of defaults",
		},
		{
			name:        "case3_partial_parent_merges_release_and_defaults",
			releaseFile: "release-partial-parent.yaml",
			wantTimeout: "30",
			wantDBHost:  "db.prod.example.com",
			dbHostNote:  "Release-authored host wins; siblings fill from default",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ResolveConfig{
				AggregatePath: filepath.Join("testdata", "parameter-defaults", "aggregate.yaml"),
				ReleasePath:   filepath.Join("testdata", "parameter-defaults", tc.releaseFile),
				ManifestPath:  filepath.Join("testdata", "parameter-defaults", "manifest.yaml"),
			}
			result, err := Resolve(cfg)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			comp, ok := result.Aggregate.Spec.Components.Get("api")
			if !ok {
				t.Fatalf("component api missing from resolved Aggregate")
			}

			gotTimeout, _ := comp.Values.Get("timeout")
			if fmt.Sprint(gotTimeout) != tc.wantTimeout {
				t.Errorf("timeout: got %q; want %q", fmt.Sprint(gotTimeout), tc.wantTimeout)
			}

			gotDBHost, _ := comp.Values.Get("db_host")
			if fmt.Sprint(gotDBHost) != tc.wantDBHost {
				t.Errorf("db_host (%s): got %q; want %q", tc.dbHostNote, fmt.Sprint(gotDBHost), tc.wantDBHost)
			}
		})
	}
}

func TestResolve_SprigDefaultFallback(t *testing.T) {
	// Test that Sprig default function works when parameter is missing
	dir := t.TempDir()

	// Release WITHOUT cache_ttl_seconds — should fall back to default "3000"
	releaseYAML := `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: minimal
  environment: local
  version: "1.0.0"
spec:
  aggregate: param-app
  manifest: param-manifest
  parameters:
    db:
      display_name: localhost
  boundaries:
    local:
      directory: ./output
`
	relPath := filepath.Join(dir, "release.yaml")
	os.WriteFile(relPath, []byte(releaseYAML), 0644)

	cfg := ResolveConfig{
		AggregatePath: filepath.Join("testdata", "parameter-merge-and-fallback", "aggregate.yaml"),
		ReleasePath:   relPath,
		ManifestPath:  filepath.Join("testdata", "parameter-merge-and-fallback", "manifest.yaml"),
	}

	result, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	comp, _ := result.Aggregate.Spec.Components.Get("api")
	ttl, _ := comp.Values.Get("cache_ttl_seconds")
	if fmt.Sprint(ttl) != "3000" {
		t.Errorf("expected Sprig default 3000, got %v", ttl)
	}
}

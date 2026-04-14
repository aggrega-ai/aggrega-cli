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
				if _, ok := result.Aggregate.Spec.Components["web"]; !ok {
					t.Error("expected component 'web'")
				}
				if _, ok := result.Aggregate.Spec.Components["worker"]; !ok {
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
			fixtures: "params",
			wantErr:  nil,
			validate: func(t *testing.T, result *ResolveResult) {
				t.Helper()
				comp, ok := result.Aggregate.Spec.Components["api"]
				if !ok {
					t.Fatal("expected component 'api'")
				}
				if fmt.Sprint(comp.Values["port"]) != "8080" {
					t.Errorf("expected interpolated port 8080, got %v", comp.Values["port"])
				}
				host, _ := comp.Values["host"].(string)
				if host != "staging-db.internal" {
					t.Errorf("expected interpolated host staging-db.internal, got %s", host)
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
				if webInfra.Infra == nil {
					t.Error("expected non-nil infra for 'web'")
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

func TestResolve_ParametersFromMerge(t *testing.T) {
	// Test that parametersFrom files are loaded and merged with Release parameters
	base := filepath.Join("testdata", "params")

	// Create a temporary release with parametersFrom pointing to parameters.yaml
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
    api_port: "8080"
    db:
      host: release-db.internal
  parametersFrom:
    - ` + filepath.Join(base, "parameters.yaml") + `
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

	// Release parameters override external: db.host should be release-db.internal (Release wins)
	comp := result.Aggregate.Spec.Components["api"]
	host, _ := comp.Values["host"].(string)
	if host != "release-db.internal" {
		t.Errorf("expected Release db.host to win over external, got %s", host)
	}
}

func TestResolve_ParameterLifecycle(t *testing.T) {
	// After Resolve completes, the internal parameter map should have been purged.
	// We verify indirectly: the result should not contain raw parameter values
	// in the Aggregate (they should be interpolated, not template expressions).
	cfg := ResolveConfig{
		AggregatePath: filepath.Join("testdata", "params", "aggregate.yaml"),
		ReleasePath:   filepath.Join("testdata", "params", "release.yaml"),
		ManifestPath:  filepath.Join("testdata", "params", "manifest.yaml"),
	}

	result, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify parameters were interpolated (not raw template expressions)
	comp := result.Aggregate.Spec.Components["api"]
	port := comp.Values["port"]
	portStr := fmt.Sprint(port)
	if portStr == "{{ .parameters.api_port | default \"3000\" }}" {
		t.Error("parameter expression was not interpolated")
	}
	if portStr != "8080" {
		t.Errorf("expected interpolated port 8080, got %v", port)
	}
}

func TestResolve_SprigDefaultFallback(t *testing.T) {
	// Test that Sprig default function works when parameter is missing
	dir := t.TempDir()

	// Release WITHOUT api_port — should fall back to default "3000"
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
      host: localhost
  boundaries:
    local:
      directory: ./output
`
	relPath := filepath.Join(dir, "release.yaml")
	os.WriteFile(relPath, []byte(releaseYAML), 0644)

	cfg := ResolveConfig{
		AggregatePath: filepath.Join("testdata", "params", "aggregate.yaml"),
		ReleasePath:   relPath,
		ManifestPath:  filepath.Join("testdata", "params", "manifest.yaml"),
	}

	result, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	comp := result.Aggregate.Spec.Components["api"]
	if fmt.Sprint(comp.Values["port"]) != "3000" {
		t.Errorf("expected Sprig default 3000, got %v", comp.Values["port"])
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// Lint single-source parity, MCP leg. The lint tool runs the shared lint core
// (internal/lintops) in-process on the server's resolved workspace: entity mode
// drives lintops.RunEntity, release mode drives lintops.RunRelease. These tests
// prove the in-process adapter surfaces that core faithfully — byte-identical
// success summary and diagnostic findings — for the SAME inputs in both modes.
// The oracle is a direct lintops call over the same resolved inputs the adapter
// builds internally; since the CLI leg equals the core over the same fixture
// shape (cmd/aggrega/lint_parity_test.go), CLI, MCP, and core all agree.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

func lintParityFullstackCatalogDir(t *testing.T) string {
	t.Helper()
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	moduleRoot := filepath.Dir(filepath.Dir(cwd))
	return filepath.Join(moduleRoot, "internal", "compiler", "testdata", "fullstack-app", "catalog")
}

func writeLintParityFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const lintParityBadComponent = `apiVersion: aggrega.ai/v1alpha1
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
`

const lintParityAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: parity-app
  version: "1.0.0"
spec:
  components:
    backend:
      type: core/compute/api-server
`

const lintParityManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: parity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/compute/api-server: helm-api-server-synthesized-with-artifacts:1.0.0
`

const lintParityRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: parity-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    app:
      directory: ./dist/app
      components:
        backend:
          infra:
            port: 8080
`

// TestLintToolEntityScopeCoreParity proves the in-process entity path equals the
// core: the success summary and the advisory findings match a direct
// lintops.RunEntity over the same entity.
func TestLintToolEntityScopeCoreParity(t *testing.T) {
	root := t.TempDir()
	writeLintParityFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	writeLintParityFixture(t, filepath.Join(root, "component.yaml"), lintParityBadComponent)

	// MCP leg: the tool workspace resolves from the server's start dir.
	s := newTestServer()
	s.startDir = root
	var mcpOut, mcpDiag bytes.Buffer
	mcpErr := s.lintInProcess(context.Background(), lintRequest{entity: "component.yaml"}, &mcpOut, &mcpDiag)

	// Core oracle.
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	var coreOut, coreDiag bytes.Buffer
	coreErr := lintops.RunEntity(lintops.Config{
		EntityPath:     filepath.Join(root, "component.yaml"),
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		Workspace:      rw,
		Out:            &coreOut,
		DiagOut:        &coreDiag,
	})

	if (mcpErr == nil) != (coreErr == nil) {
		t.Fatalf("verdict diverged: mcp=%v core=%v", mcpErr, coreErr)
	}
	if mcpOut.String() != coreOut.String() {
		t.Errorf("stdout diverged:\n mcp:  %q\n core: %q", mcpOut.String(), coreOut.String())
	}
	if mcpDiag.String() != coreDiag.String() {
		t.Errorf("diagnostics diverged:\n mcp:  %q\n core: %q", mcpDiag.String(), coreDiag.String())
	}
	if coreDiag.Len() == 0 {
		t.Fatalf("fixture produced no advisories; parity did not exercise the diagnostic stream")
	}
}

// TestLintToolReleaseScopeCoreParity proves the in-process release path equals
// the core: the conventions summary and per-entity advisory findings match a
// direct lintops.RunRelease over the same Release closure.
func TestLintToolReleaseScopeCoreParity(t *testing.T) {
	root := t.TempDir()
	catalogDir := lintParityFullstackCatalogDir(t)
	writeLintParityFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename),
		"workspace:\n  catalog_dirs:\n    - "+catalogDir+"\n")
	writeLintParityFixture(t, filepath.Join(root, "aggregate.yaml"), lintParityAggregate)
	writeLintParityFixture(t, filepath.Join(root, "manifest.yaml"), lintParityManifest)
	writeLintParityFixture(t, filepath.Join(root, "release.yaml"), lintParityRelease)

	// MCP leg.
	s := newTestServer()
	s.startDir = root
	var mcpOut, mcpDiag bytes.Buffer
	mcpErr := s.lintInProcess(context.Background(), lintRequest{release: "release.yaml"}, &mcpOut, &mcpDiag)

	// Core oracle.
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	var coreOut, coreDiag bytes.Buffer
	coreErr := lintops.RunRelease(rw, filepath.Join(root, "release.yaml"), nil, &coreOut, &coreDiag, nil)

	if (mcpErr == nil) != (coreErr == nil) {
		t.Fatalf("verdict diverged: mcp=%v core=%v", mcpErr, coreErr)
	}
	if mcpOut.String() != coreOut.String() {
		t.Errorf("stdout diverged:\n mcp:  %q\n core: %q", mcpOut.String(), coreOut.String())
	}
	if mcpDiag.String() != coreDiag.String() {
		t.Errorf("diagnostics diverged:\n mcp:  %q\n core: %q", mcpDiag.String(), coreDiag.String())
	}
}

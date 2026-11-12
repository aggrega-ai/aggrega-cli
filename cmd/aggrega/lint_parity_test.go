// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// Lint single-source parity, CLI leg. The `aggrega lint` command is a thin
// wrapper over the shared lint core (internal/lintops): entity mode drives
// lintops.RunEntity, release mode drives lintops.RunRelease. These tests prove
// the command surfaces that core faithfully — same success summary, same
// diagnostic findings, same verdict — for the SAME inputs in both modes. The
// oracle is a direct lintops call over the same resolved inputs the command
// builds internally; since the MCP leg equals the core over the same fixture
// shape (internal/mcp/tool_lint_parity_test.go), CLI, MCP, and core all agree.

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// captureStderr redirects os.Stderr for the duration of fn and returns whatever
// was written to it. The lint core defaults its diagnostic stream to os.Stderr,
// so the command's findings land there rather than on cobra's error writer; this
// captures them for a byte-level comparison against the core.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = old
	return <-done
}

const parityBadComponent = `apiVersion: aggrega.ai/v1beta1
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

// TestLintEntityCoreParity proves the CLI entity path equals the core: the
// success summary (stdout), the advisory findings (the diagnostic stream), and
// the verdict all match a direct lintops.RunEntity over the same entity.
func TestLintEntityCoreParity(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	entityPath := filepath.Join(root, "component.yaml")
	writeFixture(t, entityPath, parityBadComponent)

	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}

	// Core oracle.
	var coreOut, coreDiag bytes.Buffer
	coreErr := lintops.RunEntity(t.Context(), lintops.Config{
		EntityPath:     entityPath,
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		Workspace:      rw,
		Out:            &coreOut,
		DiagOut:        &coreDiag,
	})

	// CLI leg.
	var cliOut bytes.Buffer
	var cliErr error
	cliDiag := captureStderr(t, func() {
		cmd := newLintCmd()
		cmd.SetOut(&cliOut)
		cmd.SetErr(&cliOut)
		cmd.SetContext(withWorkspaceResolver(context.Background(), root, nil))
		cmd.SetArgs([]string{entityPath})
		cliErr = cmd.Execute()
	})

	if (cliErr == nil) != (coreErr == nil) {
		t.Fatalf("verdict diverged: cli=%v core=%v", cliErr, coreErr)
	}
	if cliOut.String() != coreOut.String() {
		t.Errorf("stdout diverged:\n cli:  %q\n core: %q", cliOut.String(), coreOut.String())
	}
	if cliDiag != coreDiag.String() {
		t.Errorf("diagnostics diverged:\n cli:  %q\n core: %q", cliDiag, coreDiag.String())
	}
	// The fixture is designed to produce advisory findings; a silent parity would
	// not exercise the diagnostic stream.
	if coreDiag.Len() == 0 {
		t.Fatalf("fixture produced no advisories; parity did not exercise the diagnostic stream")
	}
}

// TestLintReleaseCoreParity proves the CLI release path equals the core: the
// conventions summary (stdout), the per-entity advisory findings (the diagnostic
// stream), and the verdict all match a direct lintops.RunRelease over the same
// Release closure.
func TestLintReleaseCoreParity(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), cliBackendAggregate)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), cliCleanManifest)
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, cliDefaultRelease)
	dirs := []string{cliFullstackCatalogDir(t)}

	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}

	// Core oracle.
	var coreOut, coreDiag bytes.Buffer
	coreErr := lintops.RunRelease(t.Context(), rw, releasePath, dirs, nil, &coreOut, &coreDiag, nil)

	// CLI leg.
	var cliOut bytes.Buffer
	var cliErr error
	cliDiag := captureStderr(t, func() {
		cmd := newLintCmd()
		cmd.SetOut(&cliOut)
		cmd.SetErr(&cliOut)
		cmd.SetContext(withWorkspaceResolver(context.Background(), root, nil))
		cmd.SetArgs([]string{"--release", releasePath, "--catalog", dirs[0]})
		cliErr = cmd.Execute()
	})

	if (cliErr == nil) != (coreErr == nil) {
		t.Fatalf("verdict diverged: cli=%v core=%v", cliErr, coreErr)
	}
	if cliOut.String() != coreOut.String() {
		t.Errorf("stdout diverged:\n cli:  %q\n core: %q", cliOut.String(), coreOut.String())
	}
	if cliDiag != coreDiag.String() {
		t.Errorf("diagnostics diverged:\n cli:  %q\n core: %q", cliDiag, coreDiag.String())
	}
}

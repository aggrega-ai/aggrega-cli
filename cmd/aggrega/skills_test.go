// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// skillsWorkspace makes a workspace with a sub-folder and returns both, the root
// marked by a configuration file carrying a workspace block.
func skillsWorkspace(t *testing.T) (root, sub string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "ws")
	sub = filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("create %s: %v", sub, err)
	}
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	return root, sub
}

// runSkillsInstall runs `aggrega skills install` through the real command tree
// from dir, so the workspace it installs into is the one the shared resolution
// finds from there.
//
// It changes the working directory, the environment and the command-extension
// registrations seam, so no test using it may be parallel.
func runSkillsInstall(t *testing.T, dir string, args ...string) error {
	t.Helper()
	withCLIRegistrations(t, nil)
	t.Setenv(workspaceconfig.EnvWorkspaceRoot, "")
	t.Chdir(dir)
	cmd := newRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"skills", "install"}, args...))
	_, err := cmd.ExecuteContextC(context.Background())
	return err
}

// TestSkillsInstallHoldsAProjectInstallationToTheWorkspace runs a project
// installation from a sub-folder of a workspace and through a link leading out
// of it.
//
// The plain run is the positive control and also pins where the base comes
// from: the skill lands at the workspace root the shared resolution finds, not in
// the sub-folder the command runs in. The link run is the refusal: the
// assistant's folder at the root is a link leading out of the workspace, so the
// installation is refused with the condition every write held to a tree is
// refused with, and nothing is written where the link leads. Without the plain
// run, a guard that refused every path would pass the refusal half.
func TestSkillsInstallHoldsAProjectInstallationToTheWorkspace(t *testing.T) {
	t.Run("a plain project installation lands at the workspace root", func(t *testing.T) {
		root, sub := skillsWorkspace(t)
		if err := runSkillsInstall(t, sub, "--agent", "claude-code"); err != nil {
			t.Fatalf("a plain project installation was refused: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, ".claude", "skills", "aggrega", "SKILL.md")); err != nil {
			t.Fatalf("the installation wrote no skill at the workspace root: %v", err)
		}
		if _, err := os.Stat(filepath.Join(sub, ".claude")); !os.IsNotExist(err) {
			t.Errorf("the installation wrote into the folder the command ran in (stat err = %v)", err)
		}
	})

	t.Run("a folder reached through a link leading out is refused", func(t *testing.T) {
		root, sub := skillsWorkspace(t)
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(root, ".claude")); err != nil {
			t.Skipf("this platform cannot create the link the test needs: %v", err)
		}
		err := runSkillsInstall(t, sub, "--agent", "claude-code")
		if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
			t.Fatalf("err = %v; want the write-leads-out refusal", err)
		}
		entries, readErr := os.ReadDir(outside)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(entries) != 0 {
			t.Errorf("the refused installation wrote %d entr(ies) where the link leads", len(entries))
		}
	})
}

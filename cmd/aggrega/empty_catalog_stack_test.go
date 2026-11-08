// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// catalogDirsKey is the authored key every refusal on this surface has to name,
// because a reader told only that something is missing is told half of what
// they need. It reads the exported spelling rather than restating it, so a test
// cannot go on asserting a key the messages have stopped using.
const catalogDirsKey = workspaceconfig.CatalogDirsSetting

// workspaceWithCatalogShape authors a workspace root whose config body is
// exactly config, optionally creating the conventional catalog directory, and
// returns the resolved workspace. The config body is written rather than
// declared through the shared options helper because the shape under test IS
// the config text — an absent key and an authored empty list are different
// documents, and a helper that normalises them would erase the distinction this
// file exists to drive.
func workspaceWithCatalogShape(t *testing.T, config string, withConventionalDir bool) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	if withConventionalDir {
		if err := os.MkdirAll(filepath.Join(root, workspaceconfig.ConventionalCatalogDir), 0o755); err != nil {
			t.Fatalf("mkdir the conventional catalog directory: %v", err)
		}
	}
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return rw
}

// The three workspace shapes the whole-catalog scopes are driven over. The
// third is the discriminating one: without it, an implementation that refused
// on every workspace would satisfy the first two rows.
const (
	catalogOffSwitch  = "workspace:\n  catalog_dirs: []\n"
	catalogUndeclared = "workspace: {}\n"
)

// TestWholeCatalogScopesRefuseAnEmptyStack drives the two scopes whose whole job
// IS the catalog — whole-catalog validation and the whole-catalog conventions
// check — over each workspace shape, and reads the outcome.
//
// These two are the commands that REFUSE. Both name the authored key and the
// conventional directory, because those are the two things a reader can do
// about it.
//
// The refusal is keyed on the RESOLVED stack rather than on the flag and the
// configured list, which is what makes the third row possible at all: a
// workspace that declares nothing and keeps its entities in the conventional
// directory has a catalog to scan, and refusing it would refuse the shape the
// product recommends.
func TestWholeCatalogScopesRefuseAnEmptyStack(t *testing.T) {
	t.Parallel()
	shapes := []struct {
		name                string
		config              string
		withConventionalDir bool
		wantRefusal         bool
	}{
		{
			name:                "an authored empty list switches the local catalog off",
			config:              catalogOffSwitch,
			withConventionalDir: true,
			wantRefusal:         true,
		},
		{
			name:                "nothing declared and no conventional directory leaves nothing to scan",
			config:              catalogUndeclared,
			withConventionalDir: false,
			wantRefusal:         true,
		},
		{
			name:                "nothing declared, with the conventional directory, is scanned",
			config:              catalogUndeclared,
			withConventionalDir: true,
			wantRefusal:         false,
		},
	}
	scopes := []struct {
		name string
		run  func(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) error
	}{
		{
			name: "aggrega validate (whole catalog)",
			run: func(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) error {
				t.Helper()
				var out, diag bytes.Buffer
				dirs := stackFor(t, rw, "", nil)
				if len(dirs) == 0 {
					return validateCatalogWideFromWorkspace(rw, nil)
				}
				// With a stack to scan, the core is driven against buffers so the
				// summary line does not reach the runner's own output.
				return validateCatalogWide(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), dirs, namedStackFor(t, rw, "", nil), nil, &diag, &out)
			},
		},
		{
			name: "aggrega lint (whole catalog)",
			run: func(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) error {
				t.Helper()
				var out, diag bytes.Buffer
				return lintops.RunCatalogWide(context.Background(), rw, passesFor(t, rw, nil), nil, &out, &diag, nil)
			},
		},
	}

	for _, scope := range scopes {
		scope := scope
		for _, shape := range shapes {
			shape := shape
			t.Run(scope.name+"/"+shape.name, func(t *testing.T) {
				t.Parallel()
				rw := workspaceWithCatalogShape(t, shape.config, shape.withConventionalDir)
				err := scope.run(t, rw)
				if !shape.wantRefusal {
					if err != nil && strings.Contains(err.Error(), "needs catalog directories to scan") {
						t.Fatalf("%s refused a workspace whose entities live in the conventional `%s/` directory: %v",
							scope.name, workspaceconfig.ConventionalCatalogDir, err)
					}
					return
				}
				if err == nil {
					t.Fatalf("%s returned no error; a scope whose whole job is the catalog must refuse when there is none", scope.name)
				}
				msg := err.Error()
				if !strings.Contains(msg, "needs catalog directories to scan") {
					t.Errorf("%s failed with %q; want the refusal that says there is no catalog to scan", scope.name, msg)
				}
				if !strings.Contains(msg, catalogDirsKey) {
					t.Errorf("%s refusal %q does not name %s — a reader told only that something is missing cannot act on it",
						scope.name, msg, catalogDirsKey)
				}
				if !strings.Contains(msg, "`"+workspaceconfig.ConventionalCatalogDir+"/`") {
					t.Errorf("%s refusal %q does not name the conventional `%s/` directory, which is the other thing a reader can do about it",
						scope.name, msg, workspaceconfig.ConventionalCatalogDir)
				}
			})
		}
	}
}

// TestWholeCatalogRefusalReachesANonZeroStatus drives the real command tree
// through the exit-status boundary, so the refusal is proved to reach a process
// status rather than only a returned error. A returned error some layer above
// swallowed would leave every assertion above green and the shipped command
// still reporting success.
func TestWholeCatalogRefusalReachesANonZeroStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	rw := workspaceWithCatalogShape(t, catalogOffSwitch, true)

	root := newRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(""))
	root.SetArgs([]string{"--workspace-root", rw.Root(), "validate"})

	if code := executeRoot(context.Background(), root); code == 0 {
		t.Errorf("`aggrega validate` over a workspace that declares no local catalog exited %d; want non-zero\nstdout:\n%s\nstderr:\n%s",
			code, out.String(), errOut.String())
	}
}

// TestCommandsThatNeedNoLocalCatalogProceed drives the commands whose work does
// not require one, over the explicit off-switch, and reads the outcome.
//
// These are the commands that PROCEED. What is asserted per row is that the
// command does not answer with the whole-catalog refusal — it either completes,
// or it fails on its own subject.
//
// 🔴 The bound, stated so a green is not read as more than it is: a row asserts
// the command got PAST catalog resolution, not that everything downstream of it
// is correct. A command failing for its own reason satisfies the row, which is
// the honest assertion to make when the workspace deliberately carries nothing
// for it to work on.
func TestCommandsThatNeedNoLocalCatalogProceed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	rw := workspaceWithCatalogShape(t, catalogOffSwitch, true)

	cases := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{
			// The plugin cascade still serves the binary's embedded built-ins with
			// no local catalog at all, so a listing is a completed listing.
			name:     "aggrega plugin list",
			args:     []string{"plugin", "list"},
			wantCode: 0,
		},
		{
			// Inspecting a name nothing carries is a not-found about the NAME. The
			// command reached its own resolution rather than refusing for want of a
			// catalog directory, which is the property under test.
			name:     "aggrega inspect <unknown name>",
			args:     []string{"inspect", "no-such-entity"},
			wantCode: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newRootCmd()
			var out, errOut bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetIn(strings.NewReader(""))
			root.SetArgs(append([]string{"--workspace-root", rw.Root()}, tc.args...))

			code := executeRoot(context.Background(), root)
			if code != tc.wantCode {
				t.Errorf("%s over a workspace with no local catalog exited %d; want %d\nstdout:\n%s\nstderr:\n%s",
					tc.name, code, tc.wantCode, out.String(), errOut.String())
			}
			if strings.Contains(out.String(), "needs catalog directories to scan") ||
				strings.Contains(errOut.String(), "needs catalog directories to scan") {
				t.Errorf("%s answered with the whole-catalog refusal; its work does not need a local catalog\nstdout:\n%s\nstderr:\n%s",
					tc.name, out.String(), errOut.String())
			}
		})
	}
}

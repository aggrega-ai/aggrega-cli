// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestTheWorkspaceConfigDestinationIsHeldToTheWorkspace drives the two places a
// configuration write learns where it goes — the nature-keyed destination every
// by-key writer takes, and the workspace destination a capture takes — over a
// workspace whose configuration file is, one row at a time, a plain file, a link
// leading out, a link leading nowhere outside, and a link pointing inside.
//
// The global destination is asked too, beside a workspace file that leads out: it
// is the machine's own configuration rather than the workspace's, so the refusal
// must not reach it — and it is returned where it is spelled, beneath a home
// directory whose configuration directory is a link leading out of it, because
// nothing judges the machine's own store against any root.
func TestTheWorkspaceConfigDestinationIsHeldToTheWorkspace(t *testing.T) {
	rows := []struct {
		name    string
		layout  func(t *testing.T, root, outside string)
		global  bool
		refused bool
		// home lays out the home directory the global destination is composed
		// beneath, given the directory outside the workspace, where the row cares
		// which one it is; a row without it is handed a fresh one.
		home func(t *testing.T, outside string) string
	}{
		{name: "control: a configuration file inside", layout: func(t *testing.T, root, _ string) {
			if err := os.WriteFile(filepath.Join(root, ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a configuration file that is a link leading out", refused: true, layout: func(t *testing.T, root, outside string) {
			target := filepath.Join(outside, "config.yaml")
			if err := os.WriteFile(target, []byte("workspace: {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			symlinkOrSkip(t, target, filepath.Join(root, ConfigFilename))
		}},
		{name: "a configuration file that is a link leading nowhere outside", refused: true, layout: func(t *testing.T, root, outside string) {
			symlinkOrSkip(t, filepath.Join(outside, "absent.yaml"), filepath.Join(root, ConfigFilename))
		}},
		{name: "a configuration file that is a link pointing inside", layout: func(t *testing.T, root, _ string) {
			target := filepath.Join(root, "conf", "workspace.yaml")
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("workspace: {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			symlinkOrSkip(t, target, filepath.Join(root, ConfigFilename))
		}},
		{name: "the global destination beside a workspace file leading out", global: true, layout: func(t *testing.T, root, outside string) {
			symlinkOrSkip(t, filepath.Join(outside, "absent.yaml"), filepath.Join(root, ConfigFilename))
		}},
		{name: "the global destination beneath a configuration directory that is a link leading out of the home directory", global: true,
			layout: func(t *testing.T, root, _ string) {
				if err := os.WriteFile(filepath.Join(root, ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			home: func(t *testing.T, outside string) string {
				home := t.TempDir()
				if err := os.MkdirAll(filepath.Join(outside, "config"), 0o755); err != nil {
					t.Fatal(err)
				}
				symlinkOrSkip(t, filepath.Join(outside, "config"), filepath.Join(home, ".config"))
				return home
			}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			row.layout(t, root, outside)
			home := ""
			if row.home != nil {
				home = row.home(t, outside)
			}
			lookup := func(key string) string {
				if key == "HOME" {
					if home != "" {
						return home
					}
					return t.TempDir()
				}
				return ""
			}

			op := OpProviderBinding
			if row.global {
				op = OpRegistry
			}
			destination, byNature := WriteDestination(op, false, false, root, lookup)
			if home != "" {
				if want := filepath.Join(home, ".config", "aggrega", "config.yaml"); destination != want {
					t.Errorf("the global destination is %q, want %q — the machine's own store is returned where it "+
						"is spelled, through its link", destination, want)
				}
			}
			rw := &ResolvedWorkspace{root: root, lookup: lookup}
			_, forCapture := rw.WorkspaceConfigDestination()
			if row.global {
				forCapture = nil
			}

			for which, err := range map[string]error{"WriteDestination": byNature, "WorkspaceConfigDestination": forCapture} {
				if row.refused != (err != nil) {
					t.Fatalf("%s: refused = %t, want %t (%v)", which, err != nil, row.refused, err)
				}
				if row.refused && !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
					t.Errorf("%s: want the write-leads-out-of-workspace halt, got %v", which, err)
				}
			}
		})
	}
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}

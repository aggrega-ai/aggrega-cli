// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// outsideConfiguration is what each file kept outside the workspace holds: a root
// marker, so discovery is not what keeps it out, and a key no configuration
// admits, so a resolution that READ it would halt as an invalid file rather than
// as a read held to the workspace. The halt a row meets therefore says which of
// the two happened.
const outsideConfiguration = "workspace: {}\nnot_a_configuration_key: 1\n"

// linkOrSkip places a link at link pointing to target, skipping the test where the
// platform makes none.
func linkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}

// TestEveryFileTheCascadeMergesIsHeldToTheWorkspace drives the one resolution
// every surface configures a workspace through over each place a configuration
// file can stand: the root's own file, the CI-owned overlay beside it, a nearer
// file in the cascade, and a directory on the cascade's way — each a link leading
// out of the workspace on one row, and a link staying inside, or no link, on its
// controls. A file leading out is neither read nor used: the resolution halts with
// the read hold's condition, naming the link and where it leads, and the remedy
// names the global configuration file as the way a configuration kept elsewhere is
// read. A link leading nowhere has nothing behind it to read and is passed over as
// a file that is not there, as it always was.
func TestEveryFileTheCascadeMergesIsHeldToTheWorkspace(t *testing.T) {
	t.Parallel()

	type layout struct{ start, flagRoot, link string }
	rows := []struct {
		name string
		lay  func(t *testing.T, root, outside string) layout
		// held is whether the resolution must halt on the link.
		held bool
		// check reads the resolved configuration on a row that is not held.
		check func(t *testing.T, rw *workspaceconfig.ResolvedWorkspace)
	}{
		{
			name: "control: the root's file inside",
			lay: func(t *testing.T, root, _ string) layout {
				write(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\ndefault_registry: r.io\n")
				return layout{start: root}
			},
			check: func(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) {
				if got := rw.Config().DefaultRegistry; got != "r.io" {
					t.Errorf("DefaultRegistry = %q; want r.io, read from the root's own file", got)
				}
			},
		},
		{
			name: "the root's file a link leading out, the root discovered",
			lay: func(t *testing.T, root, outside string) layout {
				write(t, filepath.Join(outside, "config.yaml"), outsideConfiguration)
				link := filepath.Join(root, workspaceconfig.ConfigFilename)
				linkOrSkip(t, filepath.Join(outside, "config.yaml"), link)
				return layout{start: root, link: link}
			},
			held: true,
		},
		{
			name: "the root's file a link leading out, the root typed",
			lay: func(t *testing.T, root, outside string) layout {
				write(t, filepath.Join(outside, "config.yaml"), outsideConfiguration)
				link := filepath.Join(root, workspaceconfig.ConfigFilename)
				linkOrSkip(t, filepath.Join(outside, "config.yaml"), link)
				return layout{flagRoot: root, link: link}
			},
			held: true,
		},
		{
			name: "control: the root's file a link to a file inside",
			lay: func(t *testing.T, root, _ string) layout {
				write(t, filepath.Join(root, "conf", "workspace.yaml"), "workspace: {}\ndefault_registry: r.io\n")
				linkOrSkip(t, filepath.Join(root, "conf", "workspace.yaml"), filepath.Join(root, workspaceconfig.ConfigFilename))
				return layout{start: root}
			},
			check: func(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) {
				if got := rw.Config().DefaultRegistry; got != "r.io" {
					t.Errorf("DefaultRegistry = %q; want r.io, read through the link that stays inside", got)
				}
			},
		},
		{
			name: "the CI-owned overlay a link leading out",
			lay: func(t *testing.T, root, outside string) layout {
				write(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
				write(t, filepath.Join(outside, "ci.yaml"), outsideConfiguration)
				link := filepath.Join(root, workspaceconfig.AppendCIConfigFilename)
				linkOrSkip(t, filepath.Join(outside, "ci.yaml"), link)
				return layout{start: root, link: link}
			},
			held: true,
		},
		{
			name: "a nearer file in the cascade a link leading out",
			lay: func(t *testing.T, root, outside string) layout {
				write(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
				write(t, filepath.Join(outside, "sub.yaml"), outsideConfiguration)
				sub := filepath.Join(root, "sub")
				if err := os.MkdirAll(sub, 0o755); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(sub, workspaceconfig.ConfigFilename)
				linkOrSkip(t, filepath.Join(outside, "sub.yaml"), link)
				return layout{start: sub, link: link}
			},
			held: true,
		},
		{
			name: "a directory on the cascade's way a link leading out",
			lay: func(t *testing.T, root, outside string) layout {
				write(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
				write(t, filepath.Join(outside, "elsewhere", workspaceconfig.ConfigFilename), "not_a_configuration_key: 1\n")
				link := filepath.Join(root, "sub")
				linkOrSkip(t, filepath.Join(outside, "elsewhere"), link)
				return layout{start: link, link: link}
			},
			held: true,
		},
		{
			name: "control: the root's file a link leading nowhere outside",
			lay: func(t *testing.T, root, outside string) layout {
				write(t, filepath.Join(root, "release.yaml"), "kind: Release\n")
				linkOrSkip(t, filepath.Join(outside, "absent.yaml"), filepath.Join(root, workspaceconfig.ConfigFilename))
				return layout{start: root}
			},
			check: func(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) {
				if rw.ConfigFileContributed() {
					t.Errorf("a configuration file contributed (%v); a link leading nowhere has nothing to read", rw.ContributingFiles())
				}
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root, outside := t.TempDir(), t.TempDir()
			l := row.lay(t, root, outside)

			rw, err := workspaceconfig.Resolve(l.start, l.flagRoot)

			if !row.held {
				if err != nil {
					t.Fatalf("Resolve refused a layout it must read: %v", err)
				}
				row.check(t, rw)
				return
			}
			if err == nil {
				t.Fatalf("Resolve read a configuration through %s, which leads out of the workspace", l.link)
			}
			if errors.Is(err, workspaceconfig.ErrConfigInvalid()) {
				t.Fatalf("the file outside the workspace was READ — it halted as invalid: %v", err)
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read-leads-out-of-workspace halt, got %v", err)
			}
			for _, fragment := range []string{"the link " + l.link + " leads to " + outside, "--config or AGGREGA_CONFIG"} {
				if !strings.Contains(err.Error(), fragment) {
					t.Errorf("the halt does not carry %q: %v", fragment, err)
				}
			}
		})
	}
}

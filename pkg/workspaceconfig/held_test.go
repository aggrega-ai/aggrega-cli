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

// TestResolveOrHoldIsTheStrictResolutionUntilAConfigurationFileLeadsOut holds the
// held view's resolution to its two halves, row by row, over a real cascade: where
// nothing is held it is the strict resolution exactly, and where a configuration
// file the cascade merges is a link leading out it answers the held view — every
// held file in cascade order, the root, and the very refusal the strict resolution
// gives — and nothing else. A configuration broken in a second way as well is not
// held: the other failure is answered, as the strict resolution would answer it
// with the link removed.
func TestResolveOrHoldIsTheStrictResolutionUntilAConfigurationFileLeadsOut(t *testing.T) {
	t.Parallel()
	condition := readscope.ErrReadLeadsOutOfWorkspace()

	for _, row := range []struct {
		name string
		lay  func(t *testing.T, root, outside string)
		// wantHeld are the held files relative to the root; nil for a workspace
		// that is resolved.
		wantHeld []string
		// wantOther, where set, is a failure that is not the hold.
		wantOther bool
	}{
		{name: "control: the configuration inside is resolved",
			lay: func(t *testing.T, root, _ string) {
				writeHeldFile(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
			}},
		{name: "the workspace's own configuration file a link leading out",
			lay: func(t *testing.T, root, outside string) {
				writeHeldFile(t, filepath.Join(outside, "config.yaml"), "workspace: {}\n")
				linkHeld(t, filepath.Join(outside, "config.yaml"), filepath.Join(root, workspaceconfig.ConfigFilename))
			},
			wantHeld: []string{workspaceconfig.ConfigFilename}},
		{name: "the CI-owned overlay a link leading out",
			lay: func(t *testing.T, root, outside string) {
				writeHeldFile(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
				writeHeldFile(t, filepath.Join(outside, "ci.yaml"), "catalog_dirs_append: []\n")
				linkHeld(t, filepath.Join(outside, "ci.yaml"), filepath.Join(root, workspaceconfig.AppendCIConfigFilename))
			},
			wantHeld: []string{workspaceconfig.AppendCIConfigFilename}},
		{name: "both leading out are both held, in cascade order",
			lay: func(t *testing.T, root, outside string) {
				writeHeldFile(t, filepath.Join(outside, "config.yaml"), "workspace: {}\n")
				linkHeld(t, filepath.Join(outside, "config.yaml"), filepath.Join(root, workspaceconfig.ConfigFilename))
				writeHeldFile(t, filepath.Join(outside, "ci.yaml"), "catalog_dirs_append: []\n")
				linkHeld(t, filepath.Join(outside, "ci.yaml"), filepath.Join(root, workspaceconfig.AppendCIConfigFilename))
			},
			wantHeld: []string{workspaceconfig.ConfigFilename, workspaceconfig.AppendCIConfigFilename}},
		{name: "a held file beside an overlay that does not validate is not held: that failure is answered",
			lay: func(t *testing.T, root, outside string) {
				writeHeldFile(t, filepath.Join(outside, "config.yaml"), "workspace: {}\n")
				linkHeld(t, filepath.Join(outside, "config.yaml"), filepath.Join(root, workspaceconfig.ConfigFilename))
				writeHeldFile(t, filepath.Join(root, workspaceconfig.AppendCIConfigFilename), "not_a_configuration_key: 1\n")
			},
			wantOther: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root, outside := t.TempDir(), t.TempDir()
			row.lay(t, root, outside)
			flags := heldGlobalLayer(t)

			rw, held, err := workspaceconfig.ResolveOrHold("", root, flags)
			strict, strictErr := workspaceconfig.ResolveWithFlags("", root, flags)

			switch {
			case row.wantOther:
				if err == nil || errors.Is(err, condition) || held != nil {
					t.Fatalf("want the other failure answered and nothing held; got held %v, error %v", held, err)
				}
				if strictErr == nil {
					t.Fatal("the strict resolution resolved what the held one refused")
				}
				return
			case row.wantHeld == nil:
				if err != nil || held != nil || rw == nil || strictErr != nil {
					t.Fatalf("want the workspace resolved as the strict resolution resolves it; got held %v, error %v (strict %v)",
						held, err, strictErr)
				}
				if rw.Root() != strict.Root() || rw.Source() != strict.Source() {
					t.Errorf("resolved %s from %v; the strict resolution resolves %s from %v",
						rw.Root(), rw.Source(), strict.Root(), strict.Source())
				}
				return
			}

			if err != nil || rw != nil || held == nil {
				t.Fatalf("want the held view; got workspace %v, error %v", rw, err)
			}
			if held.Root() != root {
				t.Errorf("the held view's root is %s; want %s", held.Root(), root)
			}
			var got []string
			for _, f := range held.Files() {
				rel, _ := filepath.Rel(root, f)
				got = append(got, rel)
			}
			if strings.Join(got, ",") != strings.Join(row.wantHeld, ",") {
				t.Errorf("held %q; want %q", got, row.wantHeld)
			}
			if !errors.Is(held.Refusal(), condition) || strictErr == nil || held.Refusal().Error() != strictErr.Error() {
				t.Errorf("the held view's refusal is %v; want the strict resolution's own, %v", held.Refusal(), strictErr)
			}
			if named := held.NamedWith(nil); strings.Join(named, ",") != strings.Join(row.wantHeld, ",") {
				t.Errorf("a listing whose walk named nothing names %q; want the held files %q", named, row.wantHeld)
			}
		})
	}
}

// TestAHeldFileIsNamedOnceBesideWhatTheWalkNamed holds NamedWith to adding a held
// configuration file where the walk did not name it, after what the walk named,
// and never a second time.
func TestAHeldFileIsNamedOnceBesideWhatTheWalkNamed(t *testing.T) {
	t.Parallel()
	root, outside := t.TempDir(), t.TempDir()
	writeHeldFile(t, filepath.Join(outside, "config.yaml"), "workspace: {}\n")
	linkHeld(t, filepath.Join(outside, "config.yaml"), filepath.Join(root, workspaceconfig.ConfigFilename))
	_, held, err := workspaceconfig.ResolveOrHold("", root, heldGlobalLayer(t))
	if err != nil || held == nil {
		t.Fatalf("want the held view; got %v, %v", held, err)
	}
	for _, row := range []struct {
		walked, want []string
	}{
		{walked: nil, want: []string{workspaceconfig.ConfigFilename}},
		{walked: []string{"envs/linked.yaml"}, want: []string{"envs/linked.yaml", workspaceconfig.ConfigFilename}},
		{walked: []string{workspaceconfig.ConfigFilename, "x.yaml"}, want: []string{workspaceconfig.ConfigFilename, "x.yaml"}},
	} {
		if got := held.NamedWith(row.walked); strings.Join(got, ",") != strings.Join(row.want, ",") {
			t.Errorf("walk %q: named %q; want %q", row.walked, got, row.want)
		}
	}
}

// heldGlobalLayer is a command-line layer naming an empty global configuration
// file of the test's own, so nothing on the machine running the test is read.
func heldGlobalLayer(t *testing.T) workspaceconfig.FlagOverlay {
	t.Helper()
	global := filepath.Join(t.TempDir(), "config.yaml")
	writeHeldFile(t, global, "registries: []\n")
	return workspaceconfig.FlagOverlay{ConfigPath: global}
}

func writeHeldFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func linkHeld(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}

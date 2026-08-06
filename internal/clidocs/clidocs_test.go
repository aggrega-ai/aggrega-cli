// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package clidocs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
)

func TestRenderFlagSetSortsAndSkipsHidden(t *testing.T) {
	cmd := &cobra.Command{Use: "demo"}
	cmd.Flags().String("zulu", "", "last flag")
	cmd.Flags().String("alpha", "def", "first flag")
	cmd.Flags().String("secret", "", "hidden flag")
	_ = cmd.Flags().MarkHidden("secret")

	// Routed through the catalog rather than the flag set, so the projection's
	// capture of a hidden flag is exercised alongside the rendering policy.
	out := renderFlagSet(clisurface.Catalog(cmd)[0].LocalFlags)
	ai := strings.Index(out, "`--alpha`")
	zi := strings.Index(out, "`--zulu`")
	if ai == -1 || zi == -1 {
		t.Fatalf("expected both flags rendered:\n%s", out)
	}
	if ai > zi {
		t.Errorf("flags not sorted by name:\n%s", out)
	}
	if strings.Contains(out, "secret") {
		t.Errorf("hidden flag leaked into output:\n%s", out)
	}
	if !strings.Contains(out, "| `--alpha` | `def` | first flag |") {
		t.Errorf("default not rendered as expected:\n%s", out)
	}
}

// newTestTree builds a small cobra tree that exercises leaf commands,
// subcommands, and flags without any machine-specific defaults.
func newTestTree() *cobra.Command {
	root := &cobra.Command{Use: "aggrega", Short: "root"}
	root.PersistentFlags().String("workspace-root", "", "workspace root path")

	build := &cobra.Command{Use: "build", Short: "compile", Run: func(*cobra.Command, []string) {}}
	build.Flags().String("release", "", "release file")

	parent := &cobra.Command{Use: "auth", Short: "identity"}
	parent.AddCommand(&cobra.Command{Use: "token", Short: "get token", Run: func(*cobra.Command, []string) {}})

	root.AddCommand(build, parent)
	return root
}

func TestGenerateIsDeterministicAndPreservesCuratedProse(t *testing.T) {
	dir := t.TempDir()
	cliDir := filepath.Join(dir, "language-reference", "cli")
	if err := os.MkdirAll(cliDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Curated build page with prose above and below the future block.
	curated := "# aggrega build\n\nCurated explanation that must survive.\n\n## Examples\n\nExample prose.\n"
	buildPage := filepath.Join(cliDir, "aggrega-build.md")
	if err := os.WriteFile(buildPage, []byte(curated), 0o644); err != nil {
		t.Fatal(err)
	}
	// Overview and global-flags pages present so those regions render.
	if err := os.WriteFile(filepath.Join(dir, "language-reference", "07-cli.md"), []byte("# CLI\n\noverview\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "global-flags.md"), []byte("# Global flags\n\nprose\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Generate(newTestTree(), dir); err != nil {
		t.Fatalf("first Generate: %v", err)
	}

	after1, err := os.ReadFile(buildPage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after1), "Curated explanation that must survive.") {
		t.Errorf("curated leading prose lost:\n%s", after1)
	}
	if !strings.Contains(string(after1), "Example prose.") {
		t.Errorf("curated trailing prose lost:\n%s", after1)
	}
	if !strings.Contains(string(after1), "<!-- BEGIN GENERATED: cli-build -->") {
		t.Errorf("generated block missing:\n%s", after1)
	}
	// A page for the previously-undocumented parent command is created.
	if _, err := os.Stat(filepath.Join(cliDir, "aggrega-auth.md")); err != nil {
		t.Errorf("expected auth page created: %v", err)
	}

	// Second run must be a byte-identical no-op across every page.
	res, err := Generate(newTestTree(), dir)
	if err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	if len(res.Written) != 0 {
		t.Errorf("second run not idempotent, wrote: %v", res.Written)
	}
	after2, err := os.ReadFile(buildPage)
	if err != nil {
		t.Fatal(err)
	}
	if string(after1) != string(after2) {
		t.Errorf("build page not byte-identical across runs")
	}
}

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
// subcommands, and flags without any machine-specific defaults. It carries one
// family whose subcommands mix flag-bearing with flagless and one family that
// is wholly flagless, so the two ways a subcommand flags table can be
// suppressed are both reachable.
func newTestTree() *cobra.Command {
	root := &cobra.Command{Use: "aggrega", Short: "root"}
	root.PersistentFlags().String("workspace-root", "", "workspace root path")

	build := &cobra.Command{Use: "build", Short: "compile", Run: func(*cobra.Command, []string) {}}
	build.Flags().String("release", "", "release file")

	auth := &cobra.Command{Use: "auth", Short: "identity"}
	auth.AddCommand(&cobra.Command{Use: "token", Short: "get token", Run: func(*cobra.Command, []string) {}})

	capture := &cobra.Command{Use: "capture", Short: "freeze config", Run: func(*cobra.Command, []string) {}}
	capture.Flags().Bool("all-plugins", false, "capture every plugin socket")
	initCmd := &cobra.Command{Use: "init", Short: "seed config", Run: func(*cobra.Command, []string) {}}
	initCmd.Flags().String("registry", "", "registry host to seed")
	show := &cobra.Command{Use: "show", Short: "print config", Run: func(*cobra.Command, []string) {}}

	config := &cobra.Command{Use: "config", Short: "configuration"}
	config.AddCommand(capture, initCmd, show)

	root.AddCommand(build, auth, config)
	return root
}

// descriptorFor returns the catalog entry for path, failing the test when the
// tree does not carry it, so a renamed fixture command fails loudly rather than
// leaving an assertion to pass over a zero value.
func descriptorFor(t *testing.T, cat []clisurface.Descriptor, path string) clisurface.Descriptor {
	t.Helper()
	for _, d := range cat {
		if d.Path == path {
			return d
		}
	}
	t.Fatalf("no catalog entry for %q", path)
	return clisurface.Descriptor{}
}

func TestRenderSubcommandFlagsTablePerFlagBearingSubcommand(t *testing.T) {
	cat := clisurface.Catalog(newTestTree())
	out := renderSubcommandFlags(descriptorFor(t, cat, "aggrega config"), cat)

	captureAt := strings.Index(out, "**`aggrega config capture` flags**")
	initAt := strings.Index(out, "**`aggrega config init` flags**")
	if captureAt == -1 || initAt == -1 {
		t.Fatalf("expected a labelled table for each flag-bearing subcommand:\n%s", out)
	}
	if captureAt > initAt {
		t.Errorf("subcommand tables not in the order the subcommand table lists them:\n%s", out)
	}
	if !strings.Contains(out, "| `--all-plugins` | `false` | capture every plugin socket |") {
		t.Errorf("subcommand flag row not rendered as expected:\n%s", out)
	}
	// A subcommand declaring no flags of its own contributes nothing, so the
	// reference carries no empty table for it.
	if strings.Contains(out, "aggrega config show") {
		t.Errorf("flagless subcommand rendered a table:\n%s", out)
	}
}

func TestRenderSubcommandFlagsSuppressedWhenNoSubcommandDeclaresFlags(t *testing.T) {
	cat := clisurface.Catalog(newTestTree())
	cases := []struct {
		name string
		path string
		why  string
	}{
		{"wholly flagless family", "aggrega auth", "its only subcommand declares no flags"},
		{"leaf command", "aggrega build", "it has no subcommands at all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderSubcommandFlags(descriptorFor(t, cat, tc.path), cat); got != "" {
				t.Errorf("expected no output because %s, got:\n%s", tc.why, got)
			}
		})
	}
}

func TestRenderCommandPlacesSubcommandFlagsAfterTheSubcommandTable(t *testing.T) {
	cat := clisurface.Catalog(newTestTree())
	out := renderCommand(descriptorFor(t, cat, "aggrega config"), cat)

	subsAt := strings.Index(out, "**Subcommands**")
	flagsAt := strings.Index(out, "**`aggrega config capture` flags**")
	if subsAt == -1 || flagsAt == -1 {
		t.Fatalf("expected both the subcommand table and a subcommand flags table:\n%s", out)
	}
	if subsAt > flagsAt {
		t.Errorf("subcommand flags rendered before the subcommand table:\n%s", out)
	}
}

// TestRenderCommandEmitsNoHeadings pins the property that makes regeneration
// safe for a page's inbound links: a markdown heading inside the generated
// region would publish a link target that moves whenever the command tree
// changes, so every label in the fragment is bold text instead.
func TestRenderCommandEmitsNoHeadings(t *testing.T) {
	cat := clisurface.Catalog(newTestTree())
	for _, cmd := range visibleCommands(cat, cat[0].Path) {
		for i, line := range strings.Split(renderCommand(cmd, cat), "\n") {
			if strings.HasPrefix(line, "#") {
				t.Errorf("%s: generated line %d is a heading and would create an anchor: %q", cmd.Path, i+1, line)
			}
		}
	}
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
	// A page for a parent command with no page of its own is created.
	if _, err := os.Stat(filepath.Join(cliDir, "aggrega-auth.md")); err != nil {
		t.Errorf("expected auth page created: %v", err)
	}
	// A subcommand's flags reach the page of its parent command, which is the
	// only page the family has.
	configPage, err := os.ReadFile(filepath.Join(cliDir, "aggrega-config.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configPage), "**`aggrega config init` flags**") {
		t.Errorf("subcommand flags table missing from the parent page:\n%s", configPage)
	}
	if !strings.Contains(string(configPage), "| `--registry` | — | registry host to seed |") {
		t.Errorf("subcommand flag row missing from the parent page:\n%s", configPage)
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

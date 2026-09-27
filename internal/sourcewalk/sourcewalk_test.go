// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourcewalk

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// writeTree lays out files (slash path → body) under root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// chartRules are the rules a chart ecosystem's plugin declares, scoped to a local
// source directory called chart — the shape the pin step composes for one.
func chartRules() []types.IgnoreRule {
	var out []types.IgnoreRule
	for _, p := range []string{".git/", ".hg/", ".svn/", ".bzr/", "templates/.?*"} {
		out = append(out, types.IgnoreRule{Pattern: p, Within: "chart", From: types.IgnoreRuleFromPlugin})
	}
	return out
}

// TestAScopeKeepsExactlyWhatItsRulesKeep drives the walk over the shapes a declared
// chart directory takes — the rows of a directory growing a hidden file, a
// version-control directory, a dependency cache — under no rules, under the chart
// ecosystem's rules, and with the author's own line on top.
//
// Each row names the files the walk must return, so a planted defect names itself:
// a walk that skipped dot-named files on a rule of its own drops .DS_Store from the
// no-rule rows; one that ignored the rules keeps .git/HEAD; one that matched a
// directory's files but still entered it would keep .venv/lib/blob.bin once the
// author's line excludes .venv/.
func TestAScopeKeepsExactlyWhatItsRulesKeep(t *testing.T) {
	chart := map[string]string{
		"chart/Chart.yaml":             "apiVersion: v2\n",
		"chart/templates/cm.yaml":      "kind: ConfigMap\n",
		"chart/templates/.hidden.yaml": "kind: Hidden\n",
		"chart/.DS_Store":              "leftover\n",
		"chart/.git/HEAD":              "ref: refs/heads/main\n",
		"chart/.venv/lib/blob.bin":     "zzzz\n",
	}
	author := func(p string) types.IgnoreRule {
		return types.IgnoreRule{Pattern: p, Within: ".", From: AuthorIgnoreFile}
	}
	rows := []struct {
		name  string
		rules []types.IgnoreRule
		want  []string
	}{
		{
			name:  "no rule recorded keeps every file, a hidden one and a hidden directory's included",
			rules: nil,
			want: []string{"chart/.DS_Store", "chart/.git/HEAD", "chart/.venv/lib/blob.bin", "chart/Chart.yaml",
				"chart/templates/.hidden.yaml", "chart/templates/cm.yaml"},
		},
		{
			name:  "the chart ecosystem's rules leave out the version-control directory and hidden templates",
			rules: chartRules(),
			want:  []string{"chart/.DS_Store", "chart/.venv/lib/blob.bin", "chart/Chart.yaml", "chart/templates/cm.yaml"},
		},
		{
			name:  "the author's line leaves out a dependency cache the ecosystem does not name",
			rules: append(chartRules(), author(".venv/")),
			want:  []string{"chart/.DS_Store", "chart/Chart.yaml", "chart/templates/cm.yaml"},
		},
		{
			name:  "the author's negation brings back a directory the ecosystem left out",
			rules: append(chartRules(), author("!.git/")),
			want: []string{"chart/.DS_Store", "chart/.git/HEAD", "chart/.venv/lib/blob.bin", "chart/Chart.yaml",
				"chart/templates/cm.yaml"},
		},
		{
			name:  "a negated file does not come back while its directory is left out",
			rules: append(chartRules(), author("!.git/HEAD")),
			want:  []string{"chart/.DS_Store", "chart/.venv/lib/blob.bin", "chart/Chart.yaml", "chart/templates/cm.yaml"},
		},
		{
			name:  "a pattern scoped to the chart does not reach a directory beside it",
			rules: []types.IgnoreRule{{Pattern: "/Chart.yaml", Within: "elsewhere", From: types.IgnoreRuleFromPlugin}},
			want: []string{"chart/.DS_Store", "chart/.git/HEAD", "chart/.venv/lib/blob.bin", "chart/Chart.yaml",
				"chart/templates/.hidden.yaml", "chart/templates/cm.yaml"},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			base := t.TempDir()
			writeTree(t, base, chart)
			got, err := NewScope(base, row.rules).Files(filepath.Join(base, "chart"), nil)
			if err != nil {
				t.Fatalf("Files: %v", err)
			}
			if !reflect.DeepEqual(got, row.want) {
				t.Errorf("files = %q; want %q", got, row.want)
			}
		})
	}
}

// TestComposeOrdersThePluginThenItsFilesThenTheAuthor holds the composition to its
// order and its scoping: the plugin's patterns scoped to the source directory, then
// every ignore file the plugin names that the walk finds, a directory's own before
// those beneath it and each scoped to its own directory, then the author's
// .aggregaignore scoped to the entity's directory — and an ignore file inside a
// directory the rules already leave out is not read.
//
// A composition that put the author's lines first would lose to the plugin on the
// same path, which the last row's walk shows by keeping or losing .git/HEAD.
func TestComposeOrdersThePluginThenItsFilesThenTheAuthor(t *testing.T) {
	base := t.TempDir()
	writeTree(t, base, map[string]string{
		AuthorIgnoreFile:                      "# the author's word\n!.git/\n\nnotes.bak\n",
		"chart/Chart.yaml":                    "apiVersion: v2\n",
		"chart/.helmignore":                   "*.bak\n",
		"chart/charts/sub/.helmignore":        "values.schema.json\n",
		"chart/charts/sub/Chart.yaml":         "apiVersion: v2\n",
		"chart/.hg/.helmignore":               "never-read\n",
		"chart/.git/HEAD":                     "ref\n",
		"chart/charts/sub/notes.bak":          "x\n",
		"chart/charts/sub/values.schema.json": "{}\n",
	})
	declared := &types.PluginSourceIgnore{Patterns: []string{".git/", ".hg/"}, Files: []string{".helmignore"}}
	got, err := Compose(base, filepath.Join(base, "chart"), declared)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	want := []types.IgnoreRule{
		{Pattern: ".git/", Within: "chart", From: "plugin"},
		{Pattern: ".hg/", Within: "chart", From: "plugin"},
		{Pattern: "*.bak", Within: "chart", From: "chart/.helmignore"},
		{Pattern: "values.schema.json", Within: "chart/charts/sub", From: "chart/charts/sub/.helmignore"},
		{Pattern: "!.git/", Within: ".", From: AuthorIgnoreFile},
		{Pattern: "notes.bak", Within: ".", From: AuthorIgnoreFile},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("composed rules =\n%+v\nwant\n%+v", got, want)
	}
	files, err := NewScope(base, got).Files(filepath.Join(base, "chart"), nil)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	wantFiles := []string{"chart/.git/HEAD", "chart/.helmignore", "chart/Chart.yaml", "chart/charts/sub/.helmignore",
		"chart/charts/sub/Chart.yaml"}
	if !reflect.DeepEqual(files, wantFiles) {
		t.Errorf("files under the composed rules = %q; want %q", files, wantFiles)
	}

	// No plugin and no author file compose nothing at all.
	empty := t.TempDir()
	writeTree(t, empty, map[string]string{"chart/Chart.yaml": "apiVersion: v2\n"})
	if rules, err := Compose(empty, filepath.Join(empty, "chart"), nil); err != nil || len(rules) != 0 {
		t.Errorf("Compose with no plugin and no author file = %+v, %v; want no rules", rules, err)
	}
}

// TestEveryShapeOfLinkIsRefusedUnderOneCondition plants each shape a symbolic link
// takes inside a walked directory and asserts one condition for all five — the
// shape that used to succeed, a link to a file, among them. A walk that followed
// links would return the file link's target and report nothing, which the first row
// catches; one that skipped links would return no error at all. A link the rules
// leave out is passed over like any other excluded entry and never followed.
func TestEveryShapeOfLinkIsRefusedUnderOneCondition(t *testing.T) {
	outside := t.TempDir()
	writeTree(t, outside, map[string]string{"secret.txt": "outside\n", "dir/inner.txt": "inner\n"})
	plant := map[string]func(dir string) error{
		"a link to a file outside": func(dir string) error {
			return os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "linked"))
		},
		"a link to a directory outside": func(dir string) error {
			return os.Symlink(filepath.Join(outside, "dir"), filepath.Join(dir, "linked"))
		},
		"a broken link": func(dir string) error {
			return os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "linked"))
		},
		"a cycle": func(dir string) error {
			if err := os.Symlink(filepath.Join(dir, "linked2"), filepath.Join(dir, "linked")); err != nil {
				return err
			}
			return os.Symlink(filepath.Join(dir, "linked"), filepath.Join(dir, "linked2"))
		},
		"a self-link": func(dir string) error {
			return os.Symlink(filepath.Join(dir, "linked"), filepath.Join(dir, "linked"))
		},
	}
	for _, name := range []string{"a link to a file outside", "a link to a directory outside", "a broken link", "a cycle", "a self-link"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			writeTree(t, base, map[string]string{"chart/Chart.yaml": "apiVersion: v2\n"})
			if err := plant[name](filepath.Join(base, "chart")); err != nil {
				t.Fatal(err)
			}
			files, err := NewScope(base, nil).Files(filepath.Join(base, "chart"), nil)
			if !errors.Is(err, ErrSymbolicLinkInEntityFiles()) {
				t.Fatalf("walk = %q, %v; want the symbolic-link refusal", files, err)
			}
			if !strings.Contains(err.Error(), "chart/linked") {
				t.Errorf("the refusal does not name the link: %v", err)
			}
			if err := RefuseLink(filepath.Join(base, "chart", "linked"), "chart/linked"); !errors.Is(err, ErrSymbolicLinkInEntityFiles()) {
				t.Errorf("RefuseLink on the path itself = %v; want the same refusal", err)
			}
		})
	}
	t.Run("a link the rules leave out is passed over and never followed", func(t *testing.T) {
		base := t.TempDir()
		writeTree(t, base, map[string]string{"chart/Chart.yaml": "apiVersion: v2\n"})
		if err := plant["a link to a file outside"](filepath.Join(base, "chart")); err != nil {
			t.Fatal(err)
		}
		rules := []types.IgnoreRule{{Pattern: "linked", Within: ".", From: AuthorIgnoreFile}}
		files, err := NewScope(base, rules).Files(filepath.Join(base, "chart"), nil)
		if err != nil || !reflect.DeepEqual(files, []string{"chart/Chart.yaml"}) {
			t.Errorf("walk = %q, %v; want only chart/Chart.yaml and no refusal", files, err)
		}
	})
}

// TestPatternLinesSkipsBlanksAndCommentsAndNothingElse pins the one reading of an
// ignore file's lines both the entity walk and the workspace walk use.
func TestPatternLinesSkipsBlanksAndCommentsAndNothingElse(t *testing.T) {
	got, err := PatternLines(strings.NewReader("# comment\n\n  \n*.bak\n  # indented comment\n!keep.bak\nlead \n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"*.bak", "!keep.bak", "lead "}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PatternLines = %q; want %q", got, want)
	}
}

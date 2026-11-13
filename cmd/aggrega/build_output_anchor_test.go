// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// captureBuildStdout runs fn with the process standard output captured and
// returns what fn wrote there. The build's closing line goes to the process
// standard output rather than to the command's own writer, so it is read here.
// Standard error is captured alongside and discarded, which keeps the progress
// surface out of the test log.
func captureBuildStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	captureStderr(t, fn)
	_ = w.Close()
	os.Stdout = old
	return <-done
}

// buildClosingLine returns the line of out that opens with "Build completed: ".
func buildClosingLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Build completed: ") {
			return line
		}
	}
	return ""
}

// anchorFixture lays down a workspace whose configuration gives it its catalog
// directory, with its lockfile filled, and returns its root and the absolute path
// of its Release.
//
// The configuration names the catalog, so a build reads it with no flag on every
// surface, and its `workspace:` block is what a resolution from a folder beneath
// the root, or from the folder of the Release, anchors on.
func anchorFixture(t *testing.T) (root, release string) {
	t.Helper()
	root = t.TempDir()
	release, _, _ = composeUnsupportedFieldWorkspace(t, root)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root, CatalogDirs: []string{"catalog"}})
	if err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: release}); err != nil {
		t.Fatalf("fill the fixture's lockfile: %v", err)
	}
	return root, release
}

// TestTheCommandLineAndTheLanguageServerWriteTheDefaultOutputBeneathTheWorkspaceRoot
// drives one workspace through the command line's real command tree and through
// the language server's own framing, every row run from a folder that is not the
// workspace root, and asserts that both surfaces write the same directory beneath
// that root and name it alike.
//
// The command line is driven from another folder with --workspace-root and with
// AGGREGA_WORKSPACE_ROOT, from a folder beneath the root with a relative Release,
// from a folder that anchors no workspace with the Release named absolutely, and
// traced; each row writes <root>/dist (and, traced, <root>/.trace), nothing in the
// folder it ran from, and closes "Build completed: N files written to <root>/dist"
// with the directory named in full. The language server's build naming no
// destination answers outputDirectory <root>/dist with the same files, and one
// naming outputParent still writes beneath that folder.
//
// Not parallel: the rows move the process working directory, set the process
// environment and read the process standard output.
func TestTheCommandLineAndTheLanguageServerWriteTheDefaultOutputBeneathTheWorkspaceRoot(t *testing.T) {
	root, release := anchorFixture(t)
	dist := filepath.Join(root, "dist")
	trace := filepath.Join(root, ".trace")

	var reference map[string]string
	for _, row := range []struct {
		name string
		// arrange moves the process into the folder the row runs from, sets
		// whatever the row names, and returns that folder and the arguments.
		arrange func(t *testing.T) (runIn string, args []string)
		traced  bool
	}{
		{
			name: "--workspace-root, run from another folder",
			arrange: func(t *testing.T) (string, []string) {
				other := t.TempDir()
				t.Chdir(other)
				return other, []string{"--workspace-root", root, "build", "--release", release}
			},
		},
		{
			name: "AGGREGA_WORKSPACE_ROOT, run from another folder",
			arrange: func(t *testing.T) (string, []string) {
				other := t.TempDir()
				t.Setenv(workspaceconfig.EnvWorkspaceRoot, root)
				t.Chdir(other)
				return other, []string{"build", "--release", release}
			},
		},
		{
			name: "a folder beneath the workspace root, the Release named relative to it",
			arrange: func(t *testing.T) (string, []string) {
				sub := filepath.Join(root, "deep", "sub")
				if err := os.MkdirAll(sub, 0o755); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(root, "deep")) })
				t.Chdir(sub)
				rel, err := filepath.Rel(sub, release)
				if err != nil {
					t.Fatal(err)
				}
				return sub, []string{"build", "--release", rel}
			},
		},
		{
			name: "a folder that anchors no workspace, the Release named absolutely and nothing naming a root",
			arrange: func(t *testing.T) (string, []string) {
				startedIn := folderNoWorkspaceResolvesFrom(t)
				t.Chdir(startedIn)
				return startedIn, []string{"build", "--release", release}
			},
		},
		{
			name: "--trace, run from another folder",
			arrange: func(t *testing.T) (string, []string) {
				other := t.TempDir()
				t.Chdir(other)
				return other, []string{"--workspace-root", root, "build", "--release", release, "--trace"}
			},
			traced: true,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			for _, dir := range []string{dist, trace} {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatalf("clear %s: %v", dir, err)
				}
			}
			runIn, args := row.arrange(t)

			var err error
			var cobraOut string
			stdout := captureBuildStdout(t, func() { cobraOut, err = runAggrega(t, args...) })
			if err != nil {
				t.Fatalf("aggrega %s failed: %v\n%s", strings.Join(args, " "), err, cobraOut)
			}

			tree := renderedTree(t, dist)
			if reference == nil {
				reference = tree
			} else if !reflect.DeepEqual(tree, reference) {
				t.Errorf("the build wrote a different tree beneath %s than the first row did", dist)
			}
			want := fmt.Sprintf("Build completed: %s written to %s", countOfNoun(len(tree), "file", "files"), dist)
			if got := buildClosingLine(stdout); got != want {
				t.Errorf("the closing line reads %q, want %q", got, want)
			}
			if stray := strayBuildOutput(runIn); len(stray) > 0 {
				t.Errorf("the build wrote %v in %s, the folder it ran from", stray, runIn)
			}
			_, traceErr := os.Stat(trace)
			if row.traced && traceErr != nil {
				t.Errorf("the traced build wrote no trace beneath the workspace root at %s: %v", trace, traceErr)
			}
			if !row.traced && traceErr == nil {
				t.Errorf("an untraced build left a trace at %s", trace)
			}
		})
	}
	if reference == nil {
		t.Fatal("no row built, so there is nothing to hold the language server against")
	}

	// The language server, run from a folder that is not the root too.
	t.Chdir(t.TempDir())
	for _, dir := range []string{dist, trace} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("clear %s: %v", dir, err)
		}
	}
	var answer struct {
		OutputDirectory string   `json:"outputDirectory"`
		Files           []string `json:"files"`
	}
	raw := daemonAnswer(t, root, "aggrega/build", map[string]any{"workspaceRoot": root, "release": filepath.Base(release)})
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatalf("decode the build's answer: %v\n%s", err, raw)
	}
	if answer.OutputDirectory != dist {
		t.Errorf("the language server's build names %q as its output directory, want %q, the directory the "+
			"command line's closing line names", answer.OutputDirectory, dist)
	}
	if served := renderedTree(t, dist); !reflect.DeepEqual(served, reference) {
		t.Errorf("the language server wrote a different tree beneath %s than the command line did", dist)
	}

	// The regression row: a request naming outputParent writes beneath that folder,
	// and not beneath the root.
	if err := os.RemoveAll(dist); err != nil {
		t.Fatalf("clear %s: %v", dist, err)
	}
	raw = daemonAnswer(t, root, "aggrega/build", map[string]any{
		"workspaceRoot": root, "release": filepath.Base(release), "outputParent": "nested",
	})
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatalf("decode the build's answer: %v\n%s", err, raw)
	}
	beneath := filepath.Join(root, "nested", "dist")
	if answer.OutputDirectory != beneath {
		t.Errorf("a build naming outputParent names %q, want %q", answer.OutputDirectory, beneath)
	}
	renderedTree(t, beneath)
	if _, err := os.Stat(dist); err == nil {
		t.Errorf("a build naming outputParent also wrote %s", dist)
	}
}

// TestABuildWhoseResolutionFellBackToTheFolderItStartedInWritesThere is the
// negative half: where the resolution finds no workspace configuration and
// nothing names a root, the folder the build was run from is the root it uses,
// so its output lands there, as it does at a terminal in that folder.
//
// Not parallel: it moves the process working directory and reads the process
// standard output.
func TestABuildWhoseResolutionFellBackToTheFolderItStartedInWritesThere(t *testing.T) {
	startedIn := folderNoWorkspaceResolvesFrom(t)
	app := filepath.Join(startedIn, "app")
	release, catalogDir, _ := composeUnsupportedFieldWorkspace(t, app)
	// The lockfile is filled under the same root the build resolves, named here
	// rather than discovered, so nothing is written to mark the folder.
	named, err := workspaceconfig.Resolve(startedIn, startedIn)
	if err != nil {
		t.Fatalf("resolve %s as a named root: %v", startedIn, err)
	}
	if err := pullRun(context.Background(), pullConfig{ws: named, ReleasePath: release, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("fill the fixture's lockfile: %v", err)
	}
	if found, err := workspaceconfig.Resolve(startedIn, ""); err != nil || found.Root() != startedIn ||
		found.Marker() == workspaceconfig.MarkerConfig {
		t.Fatalf("the resolution from %s does not fall back to it (err %v), so the row would not exercise the fallback", startedIn, err)
	}
	t.Chdir(startedIn)

	var cobraOut string
	stdout := captureBuildStdout(t, func() {
		cobraOut, err = runAggrega(t, "build", "--release", filepath.Join("app", "release.yaml"), "--catalog", filepath.Join("app", "catalog"))
	})
	if err != nil {
		t.Fatalf("the build from %s failed: %v\n%s", startedIn, err, cobraOut)
	}
	dist := filepath.Join(startedIn, "dist")
	tree := renderedTree(t, dist)
	if want := fmt.Sprintf("Build completed: %s written to %s", countOfNoun(len(tree), "file", "files"), dist); buildClosingLine(stdout) != want {
		t.Errorf("the closing line reads %q, want %q", buildClosingLine(stdout), want)
	}
	if stray := strayBuildOutput(app); len(stray) > 0 {
		t.Errorf("the build wrote %v beside the Release in %s", stray, app)
	}
}

// TestADefaultOutputDirectoryLeadingOutOfTheWorkspaceIsRefused holds the default
// output directory to the workspace exactly as a named one is held: where
// <root>/dist is a link leading out of the workspace, the build halts with the
// write-leads-out-of-workspace refusal and the place the link leads to is left
// as it was.
//
// Not parallel: it moves the process working directory.
func TestADefaultOutputDirectoryLeadingOutOfTheWorkspaceIsRefused(t *testing.T) {
	root, release := anchorFixture(t)
	elsewhere := t.TempDir()
	writeFixture(t, filepath.Join(elsewhere, "boundaries", "old", "sentinel.txt"), "old\n")
	linkOrSkipBuild(t, elsewhere, filepath.Join(root, "dist"))
	before := treeSnapshot(t, elsewhere)
	t.Chdir(t.TempDir())

	var err error
	captureBuildStdout(t, func() { _, err = runAggrega(t, "--workspace-root", root, "build", "--release", release) })
	if !errors.Is(err, writescope.ErrWriteLeadsOutOfWorkspace()) {
		t.Fatalf("want the write-leads-out-of-workspace halt, got %v", err)
	}
	if d := diffSnapshots(before, treeSnapshot(t, elsewhere)); d != "" {
		t.Errorf("the place the link leads to changed:\n%s", d)
	}
}

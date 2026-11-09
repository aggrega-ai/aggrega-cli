// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// commandRunnerChildEnv, set in a test binary's environment, makes
// TestCommandRunnerChildReportsItsFolder report the folder it runs in rather than
// skip. The real runner re-invokes the running binary, which under test is this
// test binary, so that test is the child the real runner starts.
const commandRunnerChildEnv = "AGGREGA_TEST_COMMAND_RUNNER_CHILD"

// commandRunnerChildMarker opens the one line the child writes, so the parent
// reads it out of whatever else the test binary prints.
const commandRunnerChildMarker = "command-runner-child-folder: "

// TestCommandRunnerChildReportsItsFolder is not a test of its own: it is the
// child TestTheRealRunnerStartsItsChildInTheFolderItIsHanded starts, and it
// skips everywhere else.
func TestCommandRunnerChildReportsItsFolder(t *testing.T) {
	if os.Getenv(commandRunnerChildEnv) == "" {
		t.Skip("run only as the child the real command runner starts")
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("read the working folder: %v", err)
	}
	fmt.Println(commandRunnerChildMarker + dir)
}

// folderExists reports whether dir names an existing folder.
func folderExists(dir string) bool {
	st, err := os.Stat(dir)
	return err == nil && st.IsDir()
}

// sameFolder reports whether two folder names name one folder, links resolved.
func sameFolder(t *testing.T, a, b string) bool {
	t.Helper()
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		t.Fatalf("resolve %s: %v", a, err)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		t.Fatalf("resolve %s: %v", b, err)
	}
	return ra == rb
}

// TestTheRealRunnerStartsItsChildInTheFolderItIsHanded drives the production
// runner, which re-invokes the running binary: handed a folder, the child it
// starts reports that folder as its own; handed none, the child starts in the
// parent's own working folder.
//
// Not parallel: it sets the process environment the child inherits.
func TestTheRealRunnerStartsItsChildInTheFolderItIsHanded(t *testing.T) {
	t.Setenv(commandRunnerChildEnv, "1")
	parent, err := os.Getwd()
	if err != nil {
		t.Fatalf("read the working folder: %v", err)
	}
	handed := t.TempDir()
	for _, row := range []struct {
		name, dir, want string
	}{
		{name: "a folder handed", dir: handed, want: handed},
		{name: "no folder handed", dir: "", want: parent},
	} {
		t.Run(row.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := []string{"-test.run=^TestCommandRunnerChildReportsItsFolder$", "-test.count=1"}
			if err := selfExecCommand(context.Background(), row.dir, args, &stdout, &stderr); err != nil {
				t.Fatalf("the child failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
			}
			var reported string
			for _, line := range strings.Split(stdout.String(), "\n") {
				if folder, ok := strings.CutPrefix(line, commandRunnerChildMarker); ok {
					reported = folder
				}
			}
			if reported == "" {
				t.Fatalf("the child reported no folder:\n%s", stdout.String())
			}
			if !sameFolder(t, reported, row.want) {
				t.Errorf("the child started in %s, want %s", reported, row.want)
			}
		})
	}
}

// recordedRun is what a recording runner saw of one call: the folder it was
// handed and the argument vector.
type recordedRun struct {
	dir  string
	argv []string
}

// recordingFolderRunner records the folder and the argument vector of every run.
func recordingFolderRunner(seen *recordedRun) commandRunner {
	return func(_ context.Context, dir string, args []string, stdout, _ io.Writer) error {
		*seen = recordedRun{dir: dir, argv: append([]string(nil), args...)}
		_, _ = io.WriteString(stdout, "ran\n")
		return nil
	}
}

// TestACommandToolStartsInTheWorkspaceRootItsCallOrLaunchNames drives a generated
// command tool through the tools/call path with a recording runner, one shape of
// naming a root per row, and asserts the folder the runner is handed and the
// workspace root the command line carries.
//
// The folder is the call's own non-empty workspace_root, made absolute against
// this process's working folder and handed to the command in that absolute form;
// else the launch root. Where nothing names a root, or the root named is not an
// existing folder, the runner is handed no folder — the command starts where this
// server runs, as it always has — and the command line carries what was sent.
//
// Not parallel: one row moves the process working directory.
func TestACommandToolStartsInTheWorkspaceRootItsCallOrLaunchNames(t *testing.T) {
	launch := t.TempDir()
	named := t.TempDir()
	missing := filepath.Join(t.TempDir(), "absent")

	for _, row := range []struct {
		name string
		// launchRoot is the root the server was launched with, "" for none.
		launchRoot string
		// arrange returns the call's arguments and the folder the runner must be
		// handed ("" for none), and the workspace-root flag the command line must
		// carry ("" for none).
		arrange func(t *testing.T) (args map[string]any, wantDir, wantFlag string)
	}{
		{
			name:       "a launch root and no workspace_root starts the command in the launch root",
			launchRoot: launch,
			arrange: func(*testing.T) (map[string]any, string, string) {
				return map[string]any{}, launch, "--workspace-root=" + launch
			},
		},
		{
			name:       "an absolute workspace_root starts the command there",
			launchRoot: launch,
			arrange: func(*testing.T) (map[string]any, string, string) {
				return map[string]any{"workspace_root": named}, named, "--workspace-root=" + named
			},
		},
		{
			name:       "a relative workspace_root starts the command in that folder, handed in its absolute form",
			launchRoot: launch,
			arrange: func(t *testing.T) (map[string]any, string, string) {
				parent := t.TempDir()
				ws := filepath.Join(parent, "ws")
				if err := os.MkdirAll(ws, 0o755); err != nil {
					t.Fatal(err)
				}
				t.Chdir(parent)
				return map[string]any{"workspace_root": "ws"}, ws, "--workspace-root=" + ws
			},
		},
		{
			name:       "no launch root and no workspace_root starts the command where the server runs",
			launchRoot: "",
			arrange: func(*testing.T) (map[string]any, string, string) {
				return map[string]any{}, "", ""
			},
		},
		{
			name:       "an empty workspace_root names no root, so the command starts where the server runs",
			launchRoot: launch,
			arrange: func(*testing.T) (map[string]any, string, string) {
				return map[string]any{"workspace_root": ""}, "", "--workspace-root="
			},
		},
		{
			name:       "a workspace_root that is not an existing folder is handed as sent, the command starting where the server runs",
			launchRoot: launch,
			arrange: func(*testing.T) (map[string]any, string, string) {
				return map[string]any{"workspace_root": missing}, "", "--workspace-root=" + missing
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			var seen recordedRun
			s := newCommandToolServer(t, recordingFolderRunner(&seen))
			s.launchRoot = row.launchRoot
			args, wantDir, wantFlag := row.arrange(t)

			res := callTool(t, s, "cli-work", args)
			if res.IsError {
				t.Fatalf("cli-work reported error: %s", res.Content[0].Text)
			}
			switch {
			case wantDir == "" && seen.dir != "":
				t.Errorf("the runner was handed %q, want no folder", seen.dir)
			case wantDir != "" && (seen.dir == "" || !sameFolder(t, seen.dir, wantDir)):
				t.Errorf("the runner was handed %q, want %s", seen.dir, wantDir)
			}
			var flags []string
			for _, arg := range seen.argv {
				if strings.HasPrefix(arg, "--workspace-root=") {
					flags = append(flags, arg)
				}
			}
			switch {
			case wantFlag == "" && len(flags) > 0:
				t.Errorf("the command line carries %v, want no workspace root", flags)
			case wantFlag == "":
			case len(flags) != 1:
				t.Errorf("the command line carries %v, want exactly [%s]", flags, wantFlag)
			case flags[0] == wantFlag:
			default:
				// An absolute folder may be spelled through a link on one side; the
				// value is still the folder wanted when both name one existing folder.
				got := strings.TrimPrefix(flags[0], "--workspace-root=")
				want := strings.TrimPrefix(wantFlag, "--workspace-root=")
				if !filepath.IsAbs(got) || !folderExists(want) || !sameFolder(t, got, want) {
					t.Errorf("the command line carries %v, want exactly [%s]", flags, wantFlag)
				}
			}
		})
	}
}

// TestACommandPassingItsArgumentsOnVerbatimStartsInTheLaunchRoot covers the one
// command shape handed no flag at all: an installed extension's, which parses no
// global flag. It is still started in the root the server was launched with.
func TestACommandPassingItsArgumentsOnVerbatimStartsInTheLaunchRoot(t *testing.T) {
	root := &cobra.Command{Use: "aggrega", Short: "the compiler"}
	root.PersistentFlags().String("workspace-root", "", "Workspace root override")
	root.AddCommand(&cobra.Command{
		Use: "extension", Short: "An installed extension", DisableFlagParsing: true,
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	var seen recordedRun
	launch := t.TempDir()
	s := &server{
		out: io.Discard, logw: io.Discard, launchRoot: launch,
		runCommand:   recordingFolderRunner(&seen),
		commandTools: deriveCommandTools(projectCommandSurface(root)),
	}

	res := callTool(t, s, "cli-extension", map[string]any{})
	if res.IsError {
		t.Fatalf("cli-extension reported error: %s", res.Content[0].Text)
	}
	if seen.dir == "" || !sameFolder(t, seen.dir, launch) {
		t.Errorf("the runner was handed %q, want the launch root %s", seen.dir, launch)
	}
	if strings.Join(seen.argv, " ") != "extension" {
		t.Errorf("the command line is %v, want [extension], with no flag handed", seen.argv)
	}
}

// TestANativeToolReadsARelativeCatalogDirectoryFromTheWorkspaceRoot drives the
// native build tool over a workspace whose catalog sits at <root>/catalog, naming
// it as the relative catalog_dirs entry "catalog" — once with the workspace named
// by the launch, once by the call — from a process whose working folder holds no
// such directory. The build reads the catalog beneath the workspace root, as a
// relative 'release' is read, and writes nothing.
func TestANativeToolReadsARelativeCatalogDirectoryFromTheWorkspaceRoot(t *testing.T) {
	root, _, releasePath := fullstackWorkspace(t)
	if _, err := os.Stat("catalog"); !os.IsNotExist(err) {
		t.Fatalf("this process's working folder holds a catalog entry (%v), so a directory read from there "+
			"could not be told from one read beneath the workspace root", err)
	}
	before := hashTree(t, root)

	for _, row := range []struct {
		name string
		s    func(t *testing.T) *server
		args buildArgs
	}{
		{
			name: "the workspace named by the launch",
			s: func(t *testing.T) *server {
				s := newTestServer(t)
				s.launchRoot = root
				return s
			},
			args: buildArgs{Release: "release.yaml", CatalogDirs: []string{"catalog"}},
		},
		{
			name: "the workspace named by the call",
			s:    newTestServer,
			args: buildArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: []string{"catalog"}},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			res := callTool(t, row.s(t), "build", row.args)
			if res.IsError {
				t.Fatalf("build refused a catalog directory beneath the workspace root: %s", res.Content[0].Text)
			}
			var payload buildResultPayload
			if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
				t.Fatalf("unmarshal build payload: %v", err)
			}
			if len(payload.Files) == 0 {
				t.Fatal("build produced no rendered files")
			}
		})
	}

	if after := hashTree(t, root); after != before {
		t.Errorf("the native build changed the workspace on disk (tree hash %s -> %s); it writes nothing", before, after)
	}
	for _, dist := range []string{filepath.Join(root, "dist"), "dist"} {
		if _, err := os.Stat(dist); err == nil {
			t.Errorf("the native build wrote %s; it writes nothing", dist)
		}
	}
}

// TestACallsCatalogDirectoriesAreReadFromTheRootItResolvedFirst holds the one
// place a call's catalog_dirs enter its workspace context: a relative entry is
// read from the root the call resolved first, it stays read from there when the
// file the call is about moves the workspace, and the listing's held view reads
// it from the held workspace's root the same way.
//
// Not parallel: one row moves the process working directory.
func TestACallsCatalogDirectoriesAreReadFromTheRootItResolvedFirst(t *testing.T) {
	t.Run("from the launch root", func(t *testing.T) {
		root := t.TempDir()
		absolute := filepath.Join(t.TempDir(), "abs")
		s := &server{out: io.Discard, logw: io.Discard, launchRoot: root, transport: transportStdio}
		tw, err := s.resolveToolWorkspace("", []string{"cat", absolute})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		want := []string{filepath.Join(root, "cat"), absolute}
		if !slices.Equal(tw.flagDirs, want) {
			t.Errorf("the call's catalog directories are %v, want %v", tw.flagDirs, want)
		}
	})

	t.Run("kept from the first root where the Release moves the workspace", func(t *testing.T) {
		startedIn := aDirectoryNoWorkspaceResolvesFrom(t)
		subject := t.TempDir()
		writeLinkedDirsFile(t, filepath.Join(subject, ".aggrega-config.yaml"), "workspace: {}\n")
		release := filepath.Join(subject, "release.yaml")
		writeLinkedDirsFile(t, release,
			"apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: only\n  version: \"1.0.0\"\n")
		t.Chdir(startedIn)

		s := &server{out: io.Discard, logw: io.Discard, transport: transportStdio}
		first, err := s.resolveToolWorkspace("", []string{"cat"})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		moved, err := s.subjectWorkspace(first, release)
		if err != nil {
			t.Fatalf("resolve the Release's workspace: %v", err)
		}
		if !sameFolder(t, moved.root, subject) {
			t.Fatalf("the Release did not move the workspace to %s (it is %s), so the row exercises nothing", subject, moved.root)
		}
		if len(moved.flagDirs) != 1 || !sameFolder(t, filepath.Dir(moved.flagDirs[0]), startedIn) ||
			filepath.Base(moved.flagDirs[0]) != "cat" {
			t.Errorf("after the move the call's catalog directories are %v, want [%s]", moved.flagDirs,
				filepath.Join(startedIn, "cat"))
		}
	})

	t.Run("on the listing's held view", func(t *testing.T) {
		l := layHeldConfiguration(t, true, false, false)
		s := heldConfigurationServer(t, l.root)
		tw, err := s.resolveListingWorkspace("workspace-entity-index", "", []string{"cat"})
		if err != nil {
			t.Fatalf("resolve the listing's workspace: %v", err)
		}
		if tw.held == nil {
			t.Fatal("the workspace is not held, so the row exercises nothing")
		}
		if want := []string{filepath.Join(l.root, "cat")}; !slices.Equal(tw.flagDirs, want) {
			t.Errorf("the held view's catalog directories are %v, want %v", tw.flagDirs, want)
		}
	})
}

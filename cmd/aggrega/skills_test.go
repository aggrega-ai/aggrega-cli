// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// skillsMachine is a machine the detection tests state in full for this
// process: a home directory and a PATH of the test's own, and the terminal seam
// set as the row says. It changes the environment and a package-level seam, so
// no test using it may be parallel.
type skillsMachine struct {
	home string
	bin  string
}

func newSkillsMachine(t *testing.T, terminal bool) *skillsMachine {
	t.Helper()
	m := &skillsMachine{home: t.TempDir(), bin: t.TempDir()}
	t.Setenv("HOME", m.home)
	t.Setenv("USERPROFILE", m.home)
	t.Setenv("PATH", m.bin)
	t.Setenv("PATHEXT", ".EXE")
	saved := isTerminal
	isTerminal = func() bool { return terminal }
	t.Cleanup(func() { isTerminal = saved })
	return m
}

// command puts an executable file named for the host in the machine's PATH
// folder and returns its path.
func (m *skillsMachine) command(t *testing.T, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(m.bin, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// skillAt reports whether an installation wrote the skill in the per-user
// folder rel names beneath the machine's home directory.
func (m *skillsMachine) skillAt(rel string) bool {
	_, err := os.Stat(filepath.Join(m.home, filepath.FromSlash(rel), "aggrega", "SKILL.md"))
	return err == nil
}

// runSkillsInstallWith runs `aggrega skills install` through the real command
// tree from dir, with stdin as what a person at the terminal types, and returns
// what it printed.
func runSkillsInstallWith(t *testing.T, dir, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	withCLIRegistrations(t, nil)
	t.Setenv(workspaceconfig.EnvWorkspaceRoot, "")
	t.Chdir(dir)
	cmd := newRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(append([]string{"skills", "install"}, args...))
	_, err = cmd.ExecuteContextC(context.Background())
	return out.String(), errOut.String(), err
}

// TestSkillsInstallGlobalWithNoAgentInstallsForTheAssistantsOnThisMachine drives
// `aggrega skills install --global` naming no --agent against a machine with
// Claude Code found by its folder and Codex by its command, and Cursor absent.
//
// Each row reads both what landed and what the command said. The rows without a
// terminal are what a script, an assistant and the assistant's own tool for the
// command get; the rows with one are what a person gets, confirming with Enter
// or adding an assistant the detection did not find. The refusals are held to
// writing nothing: the home directory holds no skill folder after them.
func TestSkillsInstallGlobalWithNoAgentInstallsForTheAssistantsOnThisMachine(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
		stdin    string
		plant    bool
		args     []string
		want     []string // per-user skill folders that must hold the skill
		absent   []string // per-user skill folders that must not
		found    bool     // whether the Found lines are printed
		refusal  string   // a fragment of the refusal, when refused
		question bool     // whether the question was asked
	}{
		{
			name:  "without a terminal, the assistants found",
			plant: true, args: []string{"--global"},
			want: []string{".claude/skills", ".agents/skills"}, absent: []string{".cursor/skills"},
			found: true,
		},
		{
			name:     "at a terminal, Enter confirms the assistants found",
			terminal: true, stdin: "\n", plant: true, args: []string{"--global"},
			want: []string{".claude/skills", ".agents/skills"}, absent: []string{".cursor/skills"},
			found: true, question: true,
		},
		{
			name:     "at a terminal, typed identifiers are added",
			terminal: true, stdin: "cursor, gemini-cli\n", plant: true, args: []string{"--global"},
			want:  []string{".claude/skills", ".agents/skills", ".cursor/skills", ".gemini/skills"},
			found: true, question: true,
		},
		{
			name:  "a named assistant skips detection",
			plant: true, args: []string{"--global", "--agent", "cursor"},
			want: []string{".cursor/skills"}, absent: []string{".claude/skills", ".agents/skills"},
		},
		{
			name: "without a terminal and with none found, nothing is written",
			args: []string{"--global"}, absent: []string{".claude/skills", ".agents/skills"},
			refusal: "found no AI assistant",
		},
		{
			name:     "at a terminal with none found and none typed, nothing is written",
			terminal: true, stdin: "\n", args: []string{"--global"},
			absent:  []string{".claude/skills", ".agents/skills"},
			refusal: "no assistant was named", question: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newSkillsMachine(t, tc.terminal)
			var codex string
			if tc.plant {
				if err := os.MkdirAll(filepath.Join(m.home, ".claude"), 0o755); err != nil {
					t.Fatal(err)
				}
				codex = m.command(t, "codex")
			}
			stdout, stderr, err := runSkillsInstallWith(t, t.TempDir(), tc.stdin, tc.args...)
			if tc.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refusal) {
					t.Fatalf("err = %v; want a refusal mentioning %q", err, tc.refusal)
				}
			} else if err != nil {
				t.Fatalf("the installation was refused: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
			}
			for _, rel := range tc.want {
				if !m.skillAt(rel) {
					t.Errorf("no skill was written in ~/%s\nstdout:\n%s", rel, stdout)
				}
			}
			for _, rel := range tc.absent {
				if m.skillAt(rel) {
					t.Errorf("a skill was written in ~/%s", rel)
				}
			}
			foundClaude := "Found Claude Code (claude-code), from the folder " + filepath.Join(m.home, ".claude")
			foundCodex := "Found Codex (codex), from the command " + codex
			if printed := strings.Contains(stdout, foundClaude) && strings.Contains(stdout, foundCodex); printed != tc.found {
				t.Errorf("the Found lines printed = %v; want %v\nstdout:\n%s", printed, tc.found, stdout)
			}
			if asked := strings.Contains(stderr, "Press Enter") || strings.Contains(stderr, "press Enter"); asked != tc.question {
				t.Errorf("the question asked = %v; want %v\nstderr:\n%s", asked, tc.question, stderr)
			}
		})
	}
}

// TestSkillsInstallForAProjectStillNamesItsAssistants pins the other half of the
// rule: detection reads the machine, and a project installation is for the
// assistants a project's people use, so with no --agent and no --global it is
// refused, naming both remedies, and writes nothing into the workspace even on a
// machine where an assistant would be found.
func TestSkillsInstallForAProjectStillNamesItsAssistants(t *testing.T) {
	m := newSkillsMachine(t, false)
	if err := os.MkdirAll(filepath.Join(m.home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, sub := skillsWorkspace(t)
	_, _, err := runSkillsInstallWith(t, sub, "")
	if err == nil || !strings.Contains(err.Error(), "--agent") || !strings.Contains(err.Error(), "--global") {
		t.Fatalf("err = %v; want a refusal naming --agent and --global", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".claude")); !os.IsNotExist(statErr) {
		t.Errorf("the refused installation wrote into the workspace (stat err = %v)", statErr)
	}
	if m.skillAt(".claude/skills") {
		t.Error("the refused installation wrote into the home directory")
	}
}

// TestTheAssistantToolForSkillsInstallFindsTheAssistantsOnThisMachine drives the
// assistant's tool for the command, cli-skills-install, through the built binary
// as a client launches `aggrega mcp`, with global and no agents. The tool runs
// the command in a process of its own with nothing at its input, so it installs
// for the assistants found without asking, by the one detection the command line
// runs, and its output says which it found.
func TestTheAssistantToolForSkillsInstallFindsTheAssistantsOnThisMachine(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("on macOS the built binary's environment places its cache under HOME, so this test cannot state a home of its own")
	}
	t.Parallel()
	bin := buildServeBinary(t)
	home, pathDir, launch := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLaunchRootFile(t, filepath.Join(launch, workspaceconfig.ConfigFilename), "offline: true\n")

	var stdin, stdout, stderr bytes.Buffer
	stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
	stdin.WriteString(rpcLine(2, "tools/call", `{"name":"cli-skills-install","arguments":{"global":true}}`))
	cmd := exec.Command(bin, "mcp")
	cmd.Dir = t.TempDir()
	cmd.Env = builtBinaryEnv(t, "HOME="+home, "USERPROFILE="+home, "PATH="+pathDir,
		workspaceconfig.EnvWorkspaceRoot+"="+launch)
	cmd.Stdin = &stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("aggrega mcp: %v\nstderr:\n%s", err, stderr.String())
	}
	answers := answersByID(t, stdout.String(), 1, 2)
	_, output := commandToolAnswer(t, answers[2])

	if want := "Found Claude Code (claude-code), from the folder " + filepath.Join(home, ".claude"); !strings.Contains(output, want) {
		t.Errorf("the tool's output does not say what it found (%q):\n%s", want, output)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "aggrega", "SKILL.md")); err != nil {
		t.Errorf("the tool wrote no skill for the assistant it found: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor")); !os.IsNotExist(err) {
		t.Errorf("the tool wrote for an assistant it did not find (stat err = %v)", err)
	}
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

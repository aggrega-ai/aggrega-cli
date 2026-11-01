// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/skills"
)

// installedSkillFile is the SKILL.md an installation writes into folder.
func installedSkillFile(folder string) string {
	return filepath.Join(folder, "SKILL.md")
}

// wantSkillBytes is what an installation writes as SKILL.md, read from the one
// implementation that composes it, so the assertions below compare what landed
// with what the installation means to write rather than with a copy of it.
func wantSkillBytes(t *testing.T) []byte {
	t.Helper()
	files, err := skills.Files(skills.InstalledSkill)
	if err != nil {
		t.Fatalf("skills.Files: %v", err)
	}
	for _, f := range files {
		if f.Path == "SKILL.md" {
			return f.Content
		}
	}
	t.Fatalf("the shipped skill carries no SKILL.md")
	return nil
}

// treeOf lists every path beneath root, relative and slash-separated, so an
// assertion that nothing was written reads the whole tree rather than the one
// folder a test happened to expect.
func treeOf(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// TestSkillsInstallOnTheDaemonWritesIntoTheWorkspaceRoot is the project-install
// proof: the skill lands in the assistant's project folder inside the workspace
// the request names, with the bytes the installation composes, and the answer
// says the change is not machine-wide.
//
// The daemon's own working directory is the package directory here, never the
// workspace root, so a handler that installed relative to where the process runs
// would write beside this file and leave the root empty — which the folder and
// file assertions catch.
func TestSkillsInstallOnTheDaemonWritesIntoTheWorkspaceRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	srv := newTestServer(t, root, nil, nil)

	resp := callDaemonMethod(t, srv, "aggrega/skillsInstall", map[string]any{"agents": []string{"claude-code"}})
	var res skillsInstallResult
	decodeDaemonResult(t, resp, &res)

	wantFolder := filepath.Join(root, ".claude", "skills", "aggrega")
	if res.Skill != "aggrega" {
		t.Errorf("skill = %q; want aggrega", res.Skill)
	}
	if len(res.Installed) != 1 || res.Installed[0].Folder != wantFolder {
		t.Fatalf("installed = %+v; want one folder at %s", res.Installed, wantFolder)
	}
	if got := res.Installed[0].Agents; len(got) != 1 || got[0] != "claude-code" {
		t.Errorf("agents = %v; want [claude-code]", got)
	}
	if res.Installed[0].Replaced {
		t.Error("replaced = true on a first installation")
	}
	if res.Scope.MachineWide {
		t.Error("scope.machineWide = true for a project installation")
	}
	if res.Scope.WorkspaceRoot != root {
		t.Errorf("scope.workspaceRoot = %q; want %q", res.Scope.WorkspaceRoot, root)
	}

	got, err := os.ReadFile(installedSkillFile(wantFolder))
	if err != nil {
		t.Fatalf("the installation reported %s and nothing is there: %v", wantFolder, err)
	}
	if !bytes.Equal(got, wantSkillBytes(t)) {
		t.Errorf("the installed SKILL.md is not the skill the binary ships with its banner:\n%s", got)
	}
	if tree := treeOf(t, root); strings.Join(tree, "|") != ".claude|.claude/skills|.claude/skills/aggrega|.claude/skills/aggrega/SKILL.md" {
		t.Errorf("the workspace holds %v; want the one skill folder and nothing else", tree)
	}
	if _, err := os.Stat(filepath.Join(".claude", "skills", "aggrega")); !os.IsNotExist(err) {
		t.Errorf("the installation wrote into the daemon's working directory (stat err = %v)", err)
	}
}

// TestSkillsInstallOnTheDaemonGlobalWritesUnderTheHomeDirectory is the per-user
// proof, under a home directory of this test's own.
//
// 🔴 The redirect is a correctness requirement rather than hygiene: the per-user
// folders are the real user's, so a test that did not redirect the home
// directory would write into them. It is an environment variable, so this test
// cannot be parallel.
func TestSkillsInstallOnTheDaemonGlobalWritesUnderTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := t.TempDir()
	srv := newTestServer(t, root, nil, nil)

	resp := callDaemonMethod(t, srv, "aggrega/skillsInstall", map[string]any{
		"agents": []string{"codex", "cursor"},
		"global": true,
	})
	var res skillsInstallResult
	decodeDaemonResult(t, resp, &res)

	want := []string{
		filepath.Join(home, ".agents", "skills", "aggrega"),
		filepath.Join(home, ".cursor", "skills", "aggrega"),
	}
	var folders []string
	for _, in := range res.Installed {
		folders = append(folders, in.Folder)
	}
	if strings.Join(folders, "|") != strings.Join(want, "|") {
		t.Fatalf("folders = %v; want %v, in the order the assistants were named", folders, want)
	}
	for _, folder := range want {
		if _, err := os.Stat(installedSkillFile(folder)); err != nil {
			t.Errorf("the per-user installation reported %s and nothing is there: %v", folder, err)
		}
	}
	if !res.Scope.MachineWide {
		t.Error("scope.machineWide = false for an installation into the per-user folders every project reads")
	}
	if tree := treeOf(t, root); len(tree) != 0 {
		t.Errorf("a per-user installation wrote into the workspace: %v", tree)
	}
}

// TestSkillsInstallOnTheDaemonGlobalWithNoAgentsInstallsForTheAssistantsFound is
// the daemon's half of the detection parity: a global request naming no agents
// installs for the assistants found on this machine, by the detection the
// command line runs, read through the daemon's environment — Claude Code by its
// folder, Codex by its command — and not for an assistant that is absent. With
// none found it is refused with nothing written.
//
// 🔴 It redirects the home directory and PATH, which are environment variables,
// so it cannot be parallel; without the redirect it would write into the real
// user's folders.
func TestSkillsInstallOnTheDaemonGlobalWithNoAgentsInstallsForTheAssistantsFound(t *testing.T) {
	for _, tc := range []struct {
		name    string
		plant   bool
		params  map[string]any
		want    []string // "agents@folder" entries, in order
		refusal string
	}{
		{
			name:   "the assistants found",
			plant:  true,
			params: map[string]any{"global": true},
			want:   []string{"claude-code@.claude/skills/aggrega", "codex@.agents/skills/aggrega"},
		},
		{
			name:   "an empty list is no list",
			plant:  true,
			params: map[string]any{"global": true, "agents": []string{}},
			want:   []string{"claude-code@.claude/skills/aggrega", "codex@.agents/skills/aggrega"},
		},
		{
			name:   "named agents skip detection",
			plant:  true,
			params: map[string]any{"global": true, "agents": []string{"cursor"}},
			want:   []string{"cursor@.cursor/skills/aggrega"},
		},
		{
			name:    "none found",
			params:  map[string]any{"global": true},
			refusal: "found no AI assistant",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, bin := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("PATH", bin)
			t.Setenv("PATHEXT", ".EXE")
			if tc.plant {
				if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
					t.Fatal(err)
				}
				name := "codex"
				if runtime.GOOS == "windows" {
					name += ".exe"
				}
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			srv := newTestServer(t, t.TempDir(), nil, nil)
			before := treeOf(t, home)

			resp := callDaemonMethod(t, srv, "aggrega/skillsInstall", tc.params)
			if tc.refusal != "" {
				if resp.Error == nil || resp.Error.Code != jsonRPCInvalidParams || !strings.Contains(resp.Error.Message, tc.refusal) {
					t.Fatalf("response = %+v / %s; want an invalid-params refusal mentioning %q", resp.Error, resp.Result, tc.refusal)
				}
				if after := treeOf(t, home); strings.Join(after, "|") != strings.Join(before, "|") {
					t.Errorf("a refused installation changed the home directory:\nbefore %v\nafter  %v", before, after)
				}
				return
			}
			var res skillsInstallResult
			decodeDaemonResult(t, resp, &res)
			var got []string
			for _, in := range res.Installed {
				rel, err := filepath.Rel(home, in.Folder)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, strings.Join(in.Agents, ",")+"@"+filepath.ToSlash(rel))
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("installed = %v; want %v", got, tc.want)
			}
			for _, entry := range tc.want {
				folder := filepath.Join(home, filepath.FromSlash(entry[strings.Index(entry, "@")+1:]))
				if _, err := os.Stat(installedSkillFile(folder)); err != nil {
					t.Errorf("the installation reported %s and nothing is there: %v", folder, err)
				}
			}
			if !res.Scope.MachineWide {
				t.Error("scope.machineWide = false for a per-user installation")
			}
		})
	}
}

// TestSkillsInstallOnTheDaemonWritesASharedFolderOnce drives repeated and
// folder-sharing assistants: an identifier named twice is installed once, and
// two assistants reading one folder share one entry, in the order they were
// named. Installing again reports the copy as replaced.
func TestSkillsInstallOnTheDaemonWritesASharedFolderOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	srv := newTestServer(t, root, nil, nil)
	params := map[string]any{"agents": []string{"codex", "claude-code", "zed", "codex"}}

	var first skillsInstallResult
	decodeDaemonResult(t, callDaemonMethod(t, srv, "aggrega/skillsInstall", params), &first)

	type entry struct{ agents, folder string }
	var got []entry
	for _, in := range first.Installed {
		got = append(got, entry{strings.Join(in.Agents, ","), in.Folder})
	}
	want := []entry{
		{"codex,zed", filepath.Join(root, ".agents", "skills", "aggrega")},
		{"claude-code", filepath.Join(root, ".claude", "skills", "aggrega")},
	}
	if len(got) != len(want) {
		t.Fatalf("installed = %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v; want %+v", i, got[i], want[i])
		}
	}

	var again skillsInstallResult
	decodeDaemonResult(t, callDaemonMethod(t, srv, "aggrega/skillsInstall", params), &again)
	for _, in := range again.Installed {
		if !in.Replaced {
			t.Errorf("a second installation into %s did not report the copy it replaced", in.Folder)
		}
	}
}

// TestSkillsInstallOnTheDaemonRefusesWithNothingWritten drives every refusal and
// asserts, for each, that the workspace tree is exactly what it was before the
// request: a refusal that wrote first and refused after would still carry the
// message.
func TestSkillsInstallOnTheDaemonRefusesWithNothingWritten(t *testing.T) {
	t.Parallel()

	const handWritten = "---\nname: aggrega\ndescription: mine\n---\nmy own notes\n"

	for _, tc := range []struct {
		name    string
		seed    func(t *testing.T, root string)
		params  map[string]any
		mention []string
	}{
		{
			name:    "no assistant named",
			params:  map[string]any{"agents": []string{}},
			mention: []string{"agents is required"},
		},
		{
			name:    "the command line's singular flag spelling",
			params:  map[string]any{"agent": "claude-code"},
			mention: []string{"agent"},
		},
		{
			name:    "an assistant no folder is known for, beside one that is",
			params:  map[string]any{"agents": []string{"claude-code", "no-such-assistant"}},
			mention: []string{`"no-such-assistant"`, "claude-code", "aggrega mcp"},
		},
		{
			name: "a hand-written file at the target",
			seed: func(t *testing.T, root string) {
				dir := filepath.Join(root, ".claude", "skills", "aggrega")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(handWritten), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			params:  map[string]any{"agents": []string{"codex", "claude-code"}},
			mention: []string{filepath.Join(".claude", "skills", "aggrega", "SKILL.md"), "was not written by"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tc.seed != nil {
				tc.seed(t, root)
			}
			before := treeOf(t, root)
			srv := newTestServer(t, root, nil, nil)

			resp := callDaemonMethod(t, srv, "aggrega/skillsInstall", tc.params)
			if resp.Error == nil {
				t.Fatalf("the request was answered rather than refused: %s", resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("code = %d; want %d", resp.Error.Code, jsonRPCInvalidParams)
			}
			for _, m := range tc.mention {
				if !strings.Contains(resp.Error.Message, m) {
					t.Errorf("refusal = %q; want it to mention %q", resp.Error.Message, m)
				}
			}
			if after := treeOf(t, root); strings.Join(after, "|") != strings.Join(before, "|") {
				t.Errorf("a refused installation changed the workspace:\nbefore %v\nafter  %v", before, after)
			}
			if tc.seed != nil {
				got, err := os.ReadFile(filepath.Join(root, ".claude", "skills", "aggrega", "SKILL.md"))
				if err != nil || string(got) != handWritten {
					t.Errorf("the hand-written file was changed (err = %v):\n%s", err, got)
				}
			}
		})
	}
}

// TestSkillsInstallOnTheDaemonRefusesAFolderLeadingOutOfTheWorkspace holds the
// project installation to the workspace: an assistant's folder that is a link
// leading out of the workspace is refused, and nothing is written at the place
// the link leads to.
func TestSkillsInstallOnTheDaemonRefusesAFolderLeadingOutOfTheWorkspace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".claude")); err != nil {
		t.Skipf("this platform cannot create the link the test needs: %v", err)
	}
	srv := newTestServer(t, root, nil, nil)

	resp := callDaemonMethod(t, srv, "aggrega/skillsInstall", map[string]any{"agents": []string{"claude-code"}})
	if resp.Error == nil {
		t.Fatalf("an installation through a link leading out of the workspace was answered: %s", resp.Result)
	}
	if !strings.Contains(resp.Error.Message, outside) && !strings.Contains(resp.Error.Message, ".claude") {
		t.Errorf("refusal = %q; want it to name the link or where it leads", resp.Error.Message)
	}
	if tree := treeOf(t, outside); len(tree) != 0 {
		t.Errorf("the installation wrote where the link leads: %v", tree)
	}
}

// TestSkillsListOnTheDaemonReportsEveryAssistantAndBothFolders reads the listing
// against the one table the installation reads, then holds one row to folders
// spelled out here, so a listing that projected the table wrongly and a table
// that changed under it are both seen.
func TestSkillsListOnTheDaemonReportsEveryAssistantAndBothFolders(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)

	resp := callDaemonMethod(t, srv, "aggrega/skillsList", map[string]any{})
	var res skillsListResult
	decodeDaemonResult(t, resp, &res)

	meta, err := skills.Describe(skills.InstalledSkill)
	if err != nil {
		t.Fatalf("skills.Describe: %v", err)
	}
	if len(res.Skills) != 1 || res.Skills[0].Name != "aggrega" || res.Skills[0].Description != meta.Description {
		t.Errorf("skills = %+v; want the one shipped skill with its declared description", res.Skills)
	}

	known := skills.Agents()
	if len(res.Agents) != len(known) {
		t.Fatalf("agents: %d listed, %d known", len(res.Agents), len(known))
	}
	for i, a := range known {
		got := res.Agents[i]
		if got.ID != a.ID || got.Name != a.Name || got.ProjectFolder != a.ProjectDir || got.UserFolder != a.UserDir {
			t.Errorf("agent %d = %+v; want %+v, in the table's order", i, got, a)
		}
	}

	// The positive control, spelled out rather than read from the table.
	var claude *skillsListedAgent
	for i := range res.Agents {
		if res.Agents[i].ID == "claude-code" {
			claude = &res.Agents[i]
		}
	}
	if claude == nil || claude.ProjectFolder != ".claude/skills" || claude.UserFolder != ".claude/skills" {
		t.Errorf("claude-code = %+v; want both folders .claude/skills", claude)
	}

	raw := string(resp.Result)
	for _, key := range []string{`"skills"`, `"agents"`, `"id"`, `"projectFolder"`, `"userFolder"`} {
		if !strings.Contains(raw, key) {
			t.Errorf("the answer does not carry %s:\n%s", key, raw)
		}
	}
}

// TestSkillsListOnTheDaemonRefusesAFieldItDoesNotTake pins that the listing,
// which takes no field of its own, refuses one rather than answering as though
// it had been honoured.
func TestSkillsListOnTheDaemonRefusesAFieldItDoesNotTake(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)

	resp := callDaemonMethod(t, srv, "aggrega/skillsList", map[string]any{"agents": []string{"codex"}})
	if resp.Error == nil {
		t.Fatalf("a field the listing does not take was accepted: %s", resp.Result)
	}
	if resp.Error.Code != jsonRPCInvalidParams || !strings.Contains(resp.Error.Message, "agents") {
		t.Errorf("refusal = %d %q; want invalid params naming agents", resp.Error.Code, resp.Error.Message)
	}
}

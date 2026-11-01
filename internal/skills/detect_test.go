// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package skills

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/machinedirs"
)

// detectMachine is a machine a detection test states in full: a home directory
// and a PATH, both scratch folders, read through a lookup so nothing of the
// process running the test is read.
type detectMachine struct {
	home string
	bin  string
	env  map[string]string
}

func newDetectMachine(t *testing.T) *detectMachine {
	t.Helper()
	m := &detectMachine{home: t.TempDir(), bin: t.TempDir()}
	m.env = map[string]string{"HOME": m.home, "USERPROFILE": m.home, "PATH": m.bin}
	return m
}

func (m *detectMachine) lookup(name string) string { return m.env[name] }

// folder makes a folder relative to the home directory.
func (m *detectMachine) folder(t *testing.T, rel string) {
	t.Helper()
	if err := os.MkdirAll(fromSlash(m.home, rel), 0o755); err != nil {
		t.Fatal(err)
	}
}

// command puts a file named name in the PATH folder with the given mode.
func (m *detectMachine) command(t *testing.T, name string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(m.bin, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// exeName is the file name a command takes on the host: the name itself, and on
// Windows the name with the .exe extension PATHEXT lists by default.
func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// detected renders a detection result as "id=evidence" entries in order.
func detected(t *testing.T, found []Detection) string {
	t.Helper()
	var out []string
	for _, d := range found {
		out = append(out, d.Agent.ID+"="+d.Evidence())
	}
	return strings.Join(out, "|")
}

// TestDetectFindsAnAssistantByItsOwnFolderOrItsCommand drives the detection
// rule one sign at a time, against a machine stated in full. Each row carries
// one sign, or one thing that must not count as a sign, so a row fails on its
// own fault: the empty machine is the positive control that nothing is found
// from nothing, and every refusal row would pass against a detection that found
// nothing at all unless a sibling row shows the same sign, made right, is found.
func TestDetectFindsAnAssistantByItsOwnFolderOrItsCommand(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, m *detectMachine)
		want  func(m *detectMachine) string
	}{
		{
			name:  "an empty machine has none",
			plant: func(*testing.T, *detectMachine) {},
			want:  func(*detectMachine) string { return "" },
		},
		{
			name:  "a configuration folder in the home directory",
			plant: func(t *testing.T, m *detectMachine) { m.folder(t, ".claude") },
			want: func(m *detectMachine) string {
				return "claude-code=the folder " + filepath.Join(m.home, ".claude")
			},
		},
		{
			name:  "a nested configuration folder",
			plant: func(t *testing.T, m *detectMachine) { m.folder(t, ".config/goose") },
			want: func(m *detectMachine) string {
				return "goose=the folder " + filepath.Join(m.home, ".config", "goose")
			},
		},
		{
			name:  "an executable command on PATH",
			plant: func(t *testing.T, m *detectMachine) { m.command(t, exeName("codex"), 0o755) },
			want: func(m *detectMachine) string {
				return "codex=the command " + filepath.Join(m.bin, exeName("codex"))
			},
		},
		{
			name: "a folder found first leaves the command unread",
			plant: func(t *testing.T, m *detectMachine) {
				m.folder(t, ".cursor")
				m.command(t, exeName("cursor"), 0o755)
			},
			want: func(m *detectMachine) string {
				return "cursor=the folder " + filepath.Join(m.home, ".cursor")
			},
		},
		{
			name: "several assistants come back in the table's order",
			plant: func(t *testing.T, m *detectMachine) {
				m.command(t, exeName("zed"), 0o755)
				m.folder(t, ".codex")
				m.folder(t, ".claude")
			},
			want: func(m *detectMachine) string {
				return "claude-code=the folder " + filepath.Join(m.home, ".claude") +
					"|codex=the folder " + filepath.Join(m.home, ".codex") +
					"|zed=the command " + filepath.Join(m.bin, exeName("zed"))
			},
		},
		{
			name:  "a shared skill folder is no sign of any assistant",
			plant: func(t *testing.T, m *detectMachine) { m.folder(t, ".agents/skills") },
			want:  func(*detectMachine) string { return "" },
		},
		{
			name:  "the .gemini folder alone is no sign of Gemini CLI or Antigravity",
			plant: func(t *testing.T, m *detectMachine) { m.folder(t, ".gemini") },
			want:  func(*detectMachine) string { return "" },
		},
		{
			name:  "Antigravity's own folder beneath .gemini",
			plant: func(t *testing.T, m *detectMachine) { m.folder(t, ".gemini/antigravity") },
			want: func(m *detectMachine) string {
				return "antigravity=the folder " + filepath.Join(m.home, ".gemini", "antigravity")
			},
		},
		{
			name:  "Gemini CLI by its command",
			plant: func(t *testing.T, m *detectMachine) { m.command(t, exeName("gemini"), 0o755) },
			want: func(m *detectMachine) string {
				return "gemini-cli=the command " + filepath.Join(m.bin, exeName("gemini"))
			},
		},
		{
			name: "a file where the configuration folder belongs is not the folder",
			plant: func(t *testing.T, m *detectMachine) {
				if err := os.WriteFile(filepath.Join(m.home, ".claude"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: func(*detectMachine) string { return "" },
		},
		{
			name: "a folder on PATH named like a command is not the command",
			plant: func(t *testing.T, m *detectMachine) {
				if err := os.Mkdir(filepath.Join(m.bin, exeName("codex")), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: func(*detectMachine) string { return "" },
		},
		{
			name: "a relative or empty PATH entry is skipped",
			plant: func(t *testing.T, m *detectMachine) {
				t.Chdir(m.bin)
				m.command(t, exeName("codex"), 0o755)
				m.env["PATH"] = "::.:bin"
			},
			want: func(*detectMachine) string { return "" },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newDetectMachine(t)
			tc.plant(t, m)
			found, err := Detect(machinedirs.Host(), m.lookup)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if got, want := detected(t, found), tc.want(m); got != want {
				t.Errorf("found %q; want %q", got, want)
			}
		})
	}
}

// TestDetectDoesNotTakeAFileNobodyMayRunForACommand holds the execute-permission
// half of the PATH sign on the platforms that have one: the same file is found
// once it may be run, so the refusal is the permission and nothing else.
func TestDetectDoesNotTakeAFileNobodyMayRunForACommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows decides what runs by extension, which the PATHEXT test covers")
	}
	m := newDetectMachine(t)
	p := m.command(t, "codex", 0o644)
	found, err := Detect(machinedirs.Host(), m.lookup)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got := detected(t, found); got != "" {
		t.Errorf("a file nobody may run was taken for a command: %q", got)
	}
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	found, err = Detect(machinedirs.Host(), m.lookup)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got, want := detected(t, found), "codex=the command "+p; got != want {
		t.Errorf("found %q; want %q once the file may be run", got, want)
	}
}

// TestDetectReadsWindowsPathTheWayWindowsDoes states a Windows machine: PATH
// entries separated by semicolons, and a command found by an extension PATHEXT
// lists rather than by a permission bit. A file with no listed extension is not
// a command there.
func TestDetectReadsWindowsPathTheWayWindowsDoes(t *testing.T) {
	m := newDetectMachine(t)
	other := t.TempDir()
	m.env["PATH"] = other + ";" + m.bin
	m.env["PATHEXT"] = ".COM;.EXE"
	m.command(t, "gemini", 0o644)
	exe := m.command(t, "codex.exe", 0o644)

	found, err := Detect(machinedirs.Windows, m.lookup)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got, want := detected(t, found), "codex=the command "+exe; got != want {
		t.Errorf("found %q; want %q", got, want)
	}
}

// TestDetectRefusesWithoutAHomeDirectory pins the one refusal: with no home
// directory there is nowhere to look for a configuration folder, and nothing is
// answered as though the machine had been read.
func TestDetectRefusesWithoutAHomeDirectory(t *testing.T) {
	found, err := Detect(machinedirs.Host(), func(string) string { return "" })
	if err == nil {
		t.Fatalf("Detect answered %v with no home directory", found)
	}
}

// TestEveryAssistantCarriesADetectionSign holds the table to the detection rule:
// every assistant carries at least one sign, so none is silently undetectable,
// and every configuration folder is a clean relative path that is not a skill
// folder several assistants share — the property that keeps one assistant's
// sign from being read as another's.
func TestEveryAssistantCarriesADetectionSign(t *testing.T) {
	sharedSkillFolders := map[string]int{}
	for _, a := range Agents() {
		sharedSkillFolders[a.UserDir]++
	}
	configOwners := map[string]string{}
	for _, a := range Agents() {
		if a.ConfigDir == "" && a.Command == "" {
			t.Errorf("%q carries no detection sign, so it can only be named, never found", a.ID)
		}
		if a.ConfigDir != "" {
			if strings.HasPrefix(a.ConfigDir, "/") || strings.Contains(a.ConfigDir, "\\") ||
				path.Clean(a.ConfigDir) != a.ConfigDir || strings.HasPrefix(a.ConfigDir, "..") {
				t.Errorf("%q names the folder %q, which is not a clean relative slash-separated path", a.ID, a.ConfigDir)
			}
			if owner, dup := configOwners[a.ConfigDir]; dup {
				t.Errorf("%q and %q share the configuration folder %q, so finding it names neither", owner, a.ID, a.ConfigDir)
			}
			configOwners[a.ConfigDir] = a.ID
			for skillDir, readers := range sharedSkillFolders {
				if readers > 1 && (a.ConfigDir == skillDir || strings.HasPrefix(skillDir, a.ConfigDir+"/")) {
					t.Errorf("%q is found by %q, which holds the skill folder %q that %d assistants read",
						a.ID, a.ConfigDir, skillDir, readers)
				}
			}
		}
		if strings.ContainsAny(a.Command, `/\ `) {
			t.Errorf("%q names the command %q, which is not a bare program name", a.ID, a.Command)
		}
	}
}

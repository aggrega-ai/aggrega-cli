// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package skills

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// committedRepository is the copy of the public skills repository committed
// beside this package, which the go:generate directive in skills.go rewrites.
const committedRepository = "repository"

// TestTheCommittedRepositoryCopyMatchesTheEmbeddedSkills is the guard that the
// published copy and the skills built into the binary cannot drift: it
// generates the repository tree from the embedded skills and compares it, file
// set and bytes, with the committed copy.
//
// 🔴 PLANTED DEFECT: add a line to aggrega/SKILL.md without regenerating, and
// this test reddens on repository/skills/aggrega/SKILL.md; delete
// repository/README.md, and it reddens naming that file as missing.
func TestTheCommittedRepositoryCopyMatchesTheEmbeddedSkills(t *testing.T) {
	want, err := RepositoryFiles()
	if err != nil {
		t.Fatalf("generate the repository tree: %v", err)
	}
	if len(want) < 2 {
		t.Fatalf("the generated tree holds %d file(s); it holds a README and at least one skill file, so the "+
			"comparison below would prove nothing", len(want))
	}
	got, err := ReadRepository(committedRepository)
	if err != nil {
		t.Fatalf("read the committed copy at %s: %v", committedRepository, err)
	}
	if diffs := DiffRepository(want, got); len(diffs) != 0 {
		t.Fatalf("the committed copy of the skills repository at internal/skills/%s differs from what the "+
			"embedded skills generate; run `go generate ./internal/skills` and commit the result:\n  %s",
			committedRepository, strings.Join(diffs, "\n  "))
	}
}

// TestDiffRepositoryNamesEveryKindOfDifference drives the comparison the guard
// above relies on to each of its findings, so the guard's silence means the
// trees agree rather than that the comparison found nothing to say.
func TestDiffRepositoryNamesEveryKindOfDifference(t *testing.T) {
	want := []File{{Path: "a.md", Content: []byte("a")}, {Path: "b.md", Content: []byte("b")}}
	for _, tc := range []struct {
		name string
		got  []File
		want []string
	}{
		{"identical", []File{{Path: "a.md", Content: []byte("a")}, {Path: "b.md", Content: []byte("b")}}, nil},
		{"missing", []File{{Path: "a.md", Content: []byte("a")}}, []string{"b.md is missing"}},
		{"extra", []File{{Path: "a.md", Content: []byte("a")}, {Path: "b.md", Content: []byte("b")}, {Path: "c.md", Content: []byte("c")}},
			[]string{"c.md is there and no skill writes it"}},
		{"changed", []File{{Path: "a.md", Content: []byte("a")}, {Path: "b.md", Content: []byte("B")}},
			[]string{"b.md differs from what the embedded skills generate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DiffRepository(want, tc.got)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("DiffRepository = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEveryShippedSkillIsWellFormed holds each embedded skill to the open
// format and to the banner rule: a name equal to its folder, a description the
// format admits, no banner in the source, and the banner on every written file.
func TestEveryShippedSkillIsWellFormed(t *testing.T) {
	if len(Names()) == 0 {
		t.Fatal("the binary ships no skill")
	}
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			if _, err := Describe(name); err != nil {
				t.Fatalf("Describe: %v", err)
			}
			source, err := embedded.ReadFile(path.Join(name, "SKILL.md"))
			if err != nil {
				t.Fatalf("read the source: %v", err)
			}
			if HasBanner(source) {
				t.Error("the source carries the banner, which belongs only on a written copy")
			}
			files, err := Files(name)
			if err != nil {
				t.Fatalf("Files: %v", err)
			}
			var sawSkill bool
			for _, f := range files {
				if f.Path == "SKILL.md" {
					sawSkill = true
					if !bytes.HasPrefix(f.Content, []byte("---\n")) {
						t.Error("the written SKILL.md does not open with its frontmatter")
					}
				}
				if !HasBanner(f.Content) {
					t.Errorf("the written %s carries no banner", f.Path)
				}
			}
			if !sawSkill {
				t.Error("the written copy has no SKILL.md")
			}
		})
	}
}

// TestTheBannerFollowsTheFrontmatter pins where the banner goes: after a
// frontmatter block, which the format requires to open the file, and first in a
// file that has none; and that a file of a kind it cannot be written into is
// refused rather than written without it.
func TestTheBannerFollowsTheFrontmatter(t *testing.T) {
	got, err := withBanner("SKILL.md", []byte("---\nname: x\n---\nbody\n"))
	if err != nil {
		t.Fatalf("withBanner: %v", err)
	}
	if want := "---\nname: x\n---\n" + Banner + "\nbody\n"; string(got) != want {
		t.Errorf("withBanner with frontmatter = %q, want %q", got, want)
	}
	got, err = withBanner("notes.md", []byte("# Notes\n"))
	if err != nil {
		t.Fatalf("withBanner: %v", err)
	}
	if want := Banner + "\n# Notes\n"; string(got) != want {
		t.Errorf("withBanner without frontmatter = %q, want %q", got, want)
	}
	if _, err := withBanner("run.sh", []byte("echo\n")); err == nil {
		t.Error("withBanner accepted a file the banner cannot be written into")
	}
}

// TestTheAgentTableIsOrderedAndWellFormed holds the assistant table to the
// properties its listings and its writes rely on: ordered and unique
// identifiers, and folders that are relative and stay beneath the folder they
// are joined to.
func TestTheAgentTableIsOrderedAndWellFormed(t *testing.T) {
	ids := AgentIDs()
	if !sort.StringsAreSorted(ids) {
		t.Errorf("the assistant identifiers are not in order: %v", ids)
	}
	seen := map[string]bool{}
	for _, a := range Agents() {
		if seen[a.ID] {
			t.Errorf("%q appears twice", a.ID)
		}
		seen[a.ID] = true
		if a.Name == "" {
			t.Errorf("%q carries no name", a.ID)
		}
		for _, dir := range []string{a.ProjectDir, a.UserDir} {
			if dir == "" || strings.HasPrefix(dir, "/") || strings.Contains(dir, "\\") || path.Clean(dir) != dir ||
				strings.HasPrefix(dir, "..") {
				t.Errorf("%q names the folder %q, which is not a clean relative slash-separated path", a.ID, dir)
			}
		}
	}
	for _, required := range []string{"claude-code", "codex", "cursor", "gemini-cli"} {
		if !seen[required] {
			t.Errorf("the table carries no row for %q", required)
		}
	}
}

// TestInstallWritesReplacesAndRefuses drives an installation through each of its
// outcomes against a scratch folder.
func TestInstallWritesReplacesAndRefuses(t *testing.T) {
	files, err := Files("aggrega")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}

	t.Run("a fresh install writes the copy", func(t *testing.T) {
		base := t.TempDir()
		got, err := Install(InstallRequest{Skill: "aggrega", Agents: []string{"claude-code"}, Base: base})
		if err != nil {
			t.Fatalf("Install: %v", err)
		}
		dir := filepath.Join(base, ".claude", "skills", "aggrega")
		if len(got) != 1 || got[0].Dir != dir || got[0].Replaced {
			t.Fatalf("Install reported %+v, want one fresh folder at %s", got, dir)
		}
		assertCopy(t, dir, files)
	})

	t.Run("the per-user folder is taken with Global", func(t *testing.T) {
		home := t.TempDir()
		got, err := Install(InstallRequest{Skill: "aggrega", Agents: []string{"github-copilot"}, Base: home, Global: true})
		if err != nil {
			t.Fatalf("Install: %v", err)
		}
		dir := filepath.Join(home, ".copilot", "skills", "aggrega")
		if len(got) != 1 || got[0].Dir != dir {
			t.Fatalf("Install reported %+v, want the folder %s", got, dir)
		}
		assertCopy(t, dir, files)
	})

	t.Run("a shared folder is written once for every assistant reading it", func(t *testing.T) {
		base := t.TempDir()
		got, err := Install(InstallRequest{Skill: "aggrega", Agents: []string{"codex", "zed", "codex"}, Base: base})
		if err != nil {
			t.Fatalf("Install: %v", err)
		}
		if len(got) != 1 || got[0].AgentNames() != "Codex and Zed" {
			t.Fatalf("Install reported %+v, want one folder read by Codex and Zed", got)
		}
		assertCopy(t, filepath.Join(base, ".agents", "skills", "aggrega"), files)
	})

	t.Run("a copy carrying the banner is replaced", func(t *testing.T) {
		base := t.TempDir()
		target := filepath.Join(base, ".cursor", "skills", "aggrega", "SKILL.md")
		writeScratch(t, target, "---\nname: aggrega\ndescription: old\n---\n"+Banner+"\nan older copy\n")
		got, err := Install(InstallRequest{Skill: "aggrega", Agents: []string{"cursor"}, Base: base})
		if err != nil {
			t.Fatalf("Install: %v", err)
		}
		if len(got) != 1 || !got[0].Replaced {
			t.Fatalf("Install reported %+v, want the copy replaced", got)
		}
		assertCopy(t, filepath.Dir(target), files)
	})

	t.Run("a hand-written file is refused and nothing is written", func(t *testing.T) {
		base := t.TempDir()
		target := filepath.Join(base, ".gemini", "skills", "aggrega", "SKILL.md")
		const mine = "---\nname: aggrega\ndescription: my own\n---\nmine\n"
		writeScratch(t, target, mine)
		_, err := Install(InstallRequest{Skill: "aggrega", Agents: []string{"claude-code", "gemini-cli"}, Base: base})
		if err == nil || !strings.Contains(err.Error(), target) {
			t.Fatalf("Install error = %v, want a refusal naming %s", err, target)
		}
		if b, _ := os.ReadFile(target); string(b) != mine {
			t.Errorf("the hand-written file was changed to %q", b)
		}
		if _, err := os.Stat(filepath.Join(base, ".claude")); err == nil {
			t.Error("a refusal for one assistant still wrote the other assistant's folder")
		}
	})

	t.Run("an unknown assistant is refused by name", func(t *testing.T) {
		base := t.TempDir()
		_, err := Install(InstallRequest{Skill: "aggrega", Agents: []string{"no-such-assistant"}, Base: base})
		if err == nil {
			t.Fatal("Install accepted an assistant it knows no folder for")
		}
		for _, want := range []string{`"no-such-assistant"`, "aggrega mcp", SetupPage, "claude-code"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal %q does not carry %q", err, want)
			}
		}
		if entries, _ := os.ReadDir(base); len(entries) != 0 {
			t.Errorf("a refused install wrote %d entries", len(entries))
		}
	})
}

// assertCopy requires dir to hold exactly the given files' bytes at their paths.
func assertCopy(t *testing.T, dir string, files []File) {
	t.Helper()
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Errorf("read the written %s: %v", f.Path, err)
			continue
		}
		if !bytes.Equal(got, f.Content) {
			t.Errorf("the written %s is not the skill's bytes", f.Path)
		}
	}
}

// writeScratch writes content at p, creating its folders.
func writeScratch(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

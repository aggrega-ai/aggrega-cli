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
// repository/README.md or repository/LICENSE, and it reddens naming that file
// as missing.
func TestTheCommittedRepositoryCopyMatchesTheEmbeddedSkills(t *testing.T) {
	want, err := RepositoryFiles(".")
	if err != nil {
		t.Fatalf("generate the repository tree: %v", err)
	}
	if len(want) < 3 {
		t.Fatalf("the generated tree holds %d file(s); it holds a README, the licence and at least one skill "+
			"file, so the comparison below would prove nothing", len(want))
	}
	got, err := ReadRepository(committedRepository)
	if err != nil {
		t.Fatalf("read the committed copy at %s: %v", committedRepository, err)
	}
	if diffs := DiffRepository(want, got); len(diffs) != 0 {
		t.Fatalf("the committed copy of the skills repository at internal/skills/%s differs from what the "+
			"generator writes; run `go generate ./internal/skills` and commit the result:\n  %s",
			committedRepository, strings.Join(diffs, "\n  "))
	}
}

// moduleRootLicense is the module's LICENSE as this package's folder reaches
// it: the package sits two folders below the module root. The path, file name
// included, is written out rather than found the way the generator finds it, so
// the test below compares the generator's licence with one it did not read.
var moduleRootLicense = filepath.Join("..", "..", "LICENSE")

// publishedLicense is where the published repository carries its licence: at
// its root, under the name a code host and a reader look for. Written out for
// the same reason as moduleRootLicense.
const publishedLicense = "LICENSE"

// TestThePublishedRepositoryCarriesTheModuleLicense holds the published
// repository's LICENSE to the module's own, byte for byte, in the tree the
// generator writes and in the committed copy publishing takes. The module's
// LICENSE is read from inside the module, so the test cache tracks it and a
// change to it runs this test again.
//
// 🔴 PLANTED DEFECT: make RepositoryFiles write the licence with a byte added,
// or read a file other than LICENSE at the module root, and the generated half
// reddens; delete repository/LICENSE, or change one byte of it, and the
// committed half reddens.
func TestThePublishedRepositoryCarriesTheModuleLicense(t *testing.T) {
	want, err := os.ReadFile(moduleRootLicense)
	if err != nil {
		t.Fatalf("read the module's licence at %s: %v", moduleRootLicense, err)
	}
	if len(bytes.TrimSpace(want)) == 0 {
		t.Fatalf("the module's licence at %s is empty, so the comparisons below would prove nothing", moduleRootLicense)
	}

	files, err := RepositoryFiles(".")
	if err != nil {
		t.Fatalf("generate the repository tree: %v", err)
	}
	var generated []byte
	for _, f := range files {
		if f.Path == publishedLicense {
			generated = f.Content
		}
	}
	switch {
	case generated == nil:
		t.Errorf("the generated tree carries no %s", publishedLicense)
	case !bytes.Equal(generated, want):
		t.Errorf("the generated %s is not the bytes of the module's licence at %s", publishedLicense, moduleRootLicense)
	}

	committed, err := os.ReadFile(filepath.Join(committedRepository, publishedLicense))
	switch {
	case err != nil:
		t.Errorf("the committed copy carries no %s (%v); run `go generate ./internal/skills` and commit the result",
			publishedLicense, err)
	case !bytes.Equal(committed, want):
		t.Errorf("the committed %s/%s is not the bytes of the module's licence at %s; run `go generate "+
			"./internal/skills` and commit the result", committedRepository, publishedLicense, moduleRootLicense)
	}
}

// TestTheLicenseIsReadOnlyFromThisModule drives the licence lookup through each
// of its outcomes in scratch folders: found from a folder nested in this
// module's root, and refused for a folder of another module, a module root with
// no licence or an empty one, and a folder no module holds.
func TestTheLicenseIsReadOnlyFromThisModule(t *testing.T) {
	own, err := ownModule()
	if err != nil {
		t.Fatalf("ownModule: %v", err)
	}
	const text = "the licence text\n"
	for _, tc := range []struct {
		name    string
		goMod   string // the go.mod written at the scratch root; "" writes none
		license *string
		want    string // the licence returned, when wantErr is ""
		wantErr string
	}{
		{name: "this module", goMod: "module " + own + "\n\ngo 1.24\n", license: ptr(text), want: text},
		{name: "this module, quoted", goMod: "// a comment\nmodule \"" + own + "\" // trailing\n", license: ptr(text), want: text},
		{name: "another module", goMod: "module example.com/other\n", license: ptr(text), wantErr: `declares the module "example.com/other"`},
		{name: "no licence", goMod: "module " + own + "\n", wantErr: "read the licence of " + own},
		{name: "an empty licence", goMod: "module " + own + "\n", license: ptr(" \n"), wantErr: "is empty"},
		{name: "no module", wantErr: "no go.mod at or above"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.goMod == "" && hasGoModAbove(root) {
				t.Skipf("a go.mod sits above the scratch folder %s, so no folder here is outside every module", root)
			}
			if tc.goMod != "" {
				writeScratch(t, filepath.Join(root, "go.mod"), tc.goMod)
			}
			if tc.license != nil {
				writeScratch(t, filepath.Join(root, licenseFile), *tc.license)
			}
			nested := filepath.Join(root, "internal", "skills")
			if err := os.MkdirAll(nested, 0o755); err != nil {
				t.Fatal(err)
			}

			got, err := moduleLicense(nested)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("moduleLicense error = %v, want one carrying %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("moduleLicense: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("moduleLicense = %q, want %q", got, tc.want)
			}
		})
	}
}

// hasGoModAbove reports whether a go.mod sits in dir or any folder above it.
func hasGoModAbove(dir string) bool {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func ptr(s string) *string { return &s }

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
			[]string{"c.md is there and the generator does not write it"}},
		{"changed", []File{{Path: "a.md", Content: []byte("a")}, {Path: "b.md", Content: []byte("B")}},
			[]string{"b.md differs from what the generator writes"}},
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

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package skills

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
)

// repositorySkillsDir is the folder of the published repository that holds one
// folder per skill, which is a layout the open skills command-line tool
// discovers skills in.
const repositorySkillsDir = "skills"

// licenseFile is the name of the licence file at the root of this package's
// module, and of the copy at the root of the published repository.
const licenseFile = "LICENSE"

// gitEntry is the name of a checkout's repository folder, or of the file a
// submodule's checkout holds in its place, at the checkout's root.
const gitEntry = ".git"

// passOverGitEntry reports whether rel, the path of d relative to the root of a
// tree being walked, is that root's .git, and returns what the walk's callback
// returns to pass over it: SkipDir for a folder, so nothing beneath it is
// visited, and nil for a file. Both the writer and the reader of the tree ask
// it, so the two agree on what the tree is.
func passOverGitEntry(rel string, d fs.DirEntry) (bool, error) {
	if rel != gitEntry {
		return false, nil
	}
	if d.IsDir() {
		return true, filepath.SkipDir
	}
	return true, nil
}

// RepositoryFiles returns the whole tree of the public skills repository, in
// lexical path order: a README, the licence, each shipped skill's files
// beneath skills/<name>/ exactly as Files returns them, and the plugin
// manifests that make the tree installable as a plugin, one per format.
//
// The licence is this package's module's own LICENSE file, byte for byte, read
// from the root of the module that holds the folder from, so the published
// skills carry the terms of the source they are generated from and no second
// copy of the licence text is kept anywhere. from is any folder inside the
// module: the generator passes its working directory, which is the module root
// when it is run from there and this package's folder under go generate.
func RepositoryFiles(from string) ([]File, error) {
	readme, err := repositoryReadme()
	if err != nil {
		return nil, err
	}
	license, err := moduleLicense(from)
	if err != nil {
		return nil, err
	}
	out := []File{{Path: "README.md", Content: readme}, {Path: licenseFile, Content: license}}
	for _, name := range skillNames {
		files, err := Files(name)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			out = append(out, File{Path: path.Join(repositorySkillsDir, name, f.Path), Content: f.Content})
		}
	}
	manifests, err := pluginFiles()
	if err != nil {
		return nil, err
	}
	out = append(out, manifests...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// WriteRepository makes the folder dir hold exactly the tree RepositoryFiles
// returns for from: every file written, and every other file beneath dir
// removed along with any folder left empty, because the tree is generated as a
// whole and a file a skill no longer has must not be published.
//
// The one entry it leaves alone is a .git directly beneath dir — a checkout's
// repository folder, or the file a submodule's checkout holds in its place —
// which it neither reads nor removes, so dir may be a checkout of the public
// repository.
func WriteRepository(dir, from string) error {
	files, err := RepositoryFiles(from)
	if err != nil {
		return err
	}
	want := make(map[string]bool, len(files))
	for _, f := range files {
		want[f.Path] = true
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, f.Content, 0o644); err != nil {
			return err
		}
	}

	var stale, dirs []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if pass, verdict := passOverGitEntry(rel, d); pass {
			return verdict
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		if !want[filepath.ToSlash(rel)] {
			stale = append(stale, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, p := range stale {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	// Deepest first, so a folder emptied by removing its children goes too.
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, p := range dirs {
		entries, err := os.ReadDir(p)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			if err := os.Remove(p); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReadRepository reads the tree beneath dir as files, in lexical path order, so
// a caller can compare a committed copy of the repository, or a checkout of it,
// with RepositoryFiles. A .git directly beneath dir is not part of the tree and
// is not read, as WriteRepository leaves it alone.
func ReadRepository(dir string) ([]File, error) {
	var out []File
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if pass, verdict := passOverGitEntry(rel, d); pass {
			return verdict
		}
		if d.IsDir() {
			return nil
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, File{Path: filepath.ToSlash(rel), Content: content})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// DiffRepository reports every way got differs from want, one sentence per
// difference and in path order: a file missing, a file that should not be
// there, and a file whose bytes differ.
func DiffRepository(want, got []File) []string {
	wantBy := make(map[string][]byte, len(want))
	for _, f := range want {
		wantBy[f.Path] = f.Content
	}
	gotBy := make(map[string][]byte, len(got))
	for _, f := range got {
		gotBy[f.Path] = f.Content
	}
	var paths []string
	for p := range wantBy {
		paths = append(paths, p)
	}
	for p := range gotBy {
		if _, ok := wantBy[p]; !ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	var out []string
	for _, p := range paths {
		w, inWant := wantBy[p]
		g, inGot := gotBy[p]
		switch {
		case !inGot:
			out = append(out, p+" is missing")
		case !inWant:
			out = append(out, p+" is there and the generator does not write it")
		case !bytes.Equal(w, g):
			out = append(out, p+" differs from what the generator writes")
		}
	}
	return out
}

// moduleLicense returns the bytes of the LICENSE file at the root of the module
// holding the folder from.
//
// The root is the nearest folder at or above from that holds a go.mod, and that
// go.mod must declare the module this package belongs to: run from a folder of
// any other module, or from outside every module, it refuses rather than read a
// licence that is not this module's.
func moduleLicense(from string) ([]byte, error) {
	start, err := filepath.Abs(from)
	if err != nil {
		return nil, err
	}
	want, err := ownModule()
	if err != nil {
		return nil, err
	}
	for dir := start; ; {
		mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		switch {
		case err == nil:
			if got := declaredModule(mod); got != want {
				return nil, fmt.Errorf("the nearest go.mod at or above %s, in %s, declares the module %q; the skills "+
					"repository's licence is read from the root of %s, so run the generator from inside that module",
					start, dir, got, want)
			}
			license, err := os.ReadFile(filepath.Join(dir, licenseFile))
			if err != nil {
				return nil, fmt.Errorf("read the licence of %s: %w", want, err)
			}
			if len(bytes.TrimSpace(license)) == 0 {
				return nil, fmt.Errorf("the licence of %s, %s, is empty", want, filepath.Join(dir, licenseFile))
			}
			return license, nil
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, fmt.Errorf("no go.mod at or above %s; the skills repository's licence is read from the root "+
				"of %s, so run the generator from inside that module", start, want)
		}
		dir = parent
	}
}

// ownModule returns the path of the module this package belongs to, taken from
// the package's own import path rather than written out, so it cannot name a
// module other than the one the package is compiled from.
func ownModule() (string, error) {
	pkg := reflect.TypeOf(File{}).PkgPath()
	module, ok := strings.CutSuffix(pkg, "/internal/skills")
	if !ok {
		return "", fmt.Errorf("the skills package's import path %q does not end in /internal/skills, so its module "+
			"cannot be named", pkg)
	}
	return module, nil
}

// declaredModule returns the module path a go.mod file declares on its module
// line, or "" when it has none.
func declaredModule(mod []byte) string {
	for _, line := range strings.Split(string(mod), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "module" {
			continue
		}
		if unquoted, err := strconv.Unquote(fields[1]); err == nil {
			return unquoted
		}
		return fields[1]
	}
	return ""
}

// repositoryReadme renders the README of the public skills repository.
func repositoryReadme() ([]byte, error) {
	var rows []string
	for _, name := range skillNames {
		meta, err := Describe(name)
		if err != nil {
			return nil, err
		}
		if strings.ContainsAny(meta.Description, "|\n") {
			return nil, errors.New("the " + name + " skill's description carries a character a Markdown table cell cannot hold")
		}
		rows = append(rows, fmt.Sprintf("| [`%s`](%s/%s/SKILL.md) | %s |", name, repositorySkillsDir, name, meta.Description))
	}

	binary, _ := MCPServerCommand()
	serverCommandLine := mcpServerCommandLine()
	setupPage := SetupPage()

	var b strings.Builder
	b.WriteString(Banner + "\n\n")
	b.WriteString("# Aggrega skills\n\n")
	b.WriteString("Skills that teach an AI assistant to install and use [Aggrega](" + haltref.PublishedRoot() + "), the\n")
	b.WriteString("ahead-of-time compiler that turns declarative Aggregate, Component, Recipe, Manifest and Release\n")
	b.WriteString("files into provider-native artifacts. Each skill follows the open\n")
	b.WriteString("[Agent Skills](https://agentskills.io/specification) format, so any assistant that reads that\n")
	b.WriteString("format can load it.\n\n")
	b.WriteString("| Skill | What it does |\n")
	b.WriteString("| --- | --- |\n")
	for _, row := range rows {
		b.WriteString(row + "\n")
	}
	b.WriteString("\n## Install the plugin\n\n")
	b.WriteString("This repository is also a plugin for the assistants that install plugins. Installing it adds the\n")
	b.WriteString("`" + InstalledSkill + "` skill and connects the assistant to the `" + mcpServerName + "` MCP server, " +
		"which the assistant starts\nas `" + serverCommandLine + "`. ")
	b.WriteString("The plugin does not install the `" + binary + "` binary: install it first, as\n")
	b.WriteString("[the install page](" + setupPage + ") describes, so the assistant finds\n")
	b.WriteString("`" + binary + "` on the `PATH`.\n\n")
	b.WriteString("Claude Code:\n\n")
	fmt.Fprintf(&b, "```bash\nclaude plugin install %s --marketplace %s\n```\n\n", pluginName, repositorySlug)
	fmt.Fprintf(&b, "Inside Claude Code, the same is `/plugin install %s --marketplace %s`.\n\n", pluginName, repositorySlug)
	b.WriteString("Codex:\n\n")
	fmt.Fprintf(&b, "```bash\ncodex plugin marketplace add %s\ncodex plugin add %s@%s\n```\n\n",
		repositorySlug, pluginName, marketplaceName)
	b.WriteString("GitHub Copilot CLI:\n\n")
	fmt.Fprintf(&b, "```bash\ncopilot plugin marketplace add %s\ncopilot plugin install %s@%s\n```\n\n",
		repositorySlug, pluginName, marketplaceName)
	b.WriteString("Gemini CLI:\n\n")
	fmt.Fprintf(&b, "```bash\ngemini extensions install %s\n```\n\n", repositoryURL)
	b.WriteString("Antigravity:\n\n")
	fmt.Fprintf(&b, "```bash\nagy plugin install %s\n```\n\n", repositoryURL)
	b.WriteString("Cursor loads a plugin from a folder beneath `~/.cursor/plugins/local/`. Put the repository there,\n")
	b.WriteString("then run **Developer: Reload Window** from the command palette:\n\n")
	fmt.Fprintf(&b, "```bash\ngit clone %s ~/.cursor/plugins/local/%s\n```\n\n", repositoryURL, pluginName)
	b.WriteString("## Install a skill\n\n")
	b.WriteString("To install the skill alone and connect the MCP server yourself, with the open skills\n")
	b.WriteString("command-line tool, from your project:\n\n")
	fmt.Fprintf(&b, "```bash\nnpx skills add %s\n```\n\n", repositorySlug)
	b.WriteString("With Aggrega itself, which writes the copy built into the binary you have installed, so the\n")
	b.WriteString("skill matches that version:\n\n")
	b.WriteString("```bash\naggrega skills install --agent <assistant>\n```\n\n")
	b.WriteString("`--agent` takes an assistant's identifier and may be repeated; `--global` installs into the\n")
	b.WriteString("assistant's per-user skill folder instead of the current project's. The assistants it knows are:\n\n")
	b.WriteString("| `--agent` | Assistant | Project folder | Per-user folder |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, a := range agents {
		fmt.Fprintf(&b, "| `%s` | %s | `%s` | `~/%s` |\n", a.ID, a.Name, a.ProjectDir, a.UserDir)
	}
	b.WriteString("\nAn assistant that is not listed can still use Aggrega through its MCP server: connect it to\n")
	b.WriteString("`" + serverCommandLine + "` as [the install page](" + setupPage + ") describes.\n\n")
	b.WriteString("## Where these files come from\n\n")
	b.WriteString("Every file in this repository — the skills and the plugin manifests beside them — is generated\n")
	b.WriteString("from the source of the `aggrega` binary,\n")
	b.WriteString("[aggrega-ai/aggrega-cli](https://github.com/aggrega-ai/aggrega-cli), and a tag here names the\n")
	b.WriteString("Aggrega release the files were generated from. A change to a skill or a manifest is made there;\n")
	b.WriteString("an edit made here is replaced the next time the files are generated.\n\n")
	b.WriteString("## License\n\n")
	b.WriteString("The Aggrega skills and plugin are released under " + pluginLicenseName + "; the full text\n")
	b.WriteString("is in the [`LICENSE`](" + licenseFile + ") file.\n")
	return []byte(b.String()), nil
}

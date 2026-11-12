// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package skills

import (
	"bytes"
	"encoding/json"
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
// as missing; change one byte of a committed plugin manifest such as
// repository/gemini-extension.json, and it reddens naming that manifest.
func TestTheCommittedRepositoryCopyMatchesTheEmbeddedSkills(t *testing.T) {
	want, err := RepositoryFiles(".")
	if err != nil {
		t.Fatalf("generate the repository tree: %v", err)
	}
	// The floor is the README, the licence and one skill file: below it the
	// comparison would prove nothing. The plugin manifests are held by name in
	// TestThePluginManifestsCarryTheOneSourcedValues rather than counted here.
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

// TestTheDeclaredLicenseIsTheModulesLicense holds the licence the manifests
// declare by SPDX identifier, and the name the README gives it, to the text of
// the module's LICENSE, which is what the published tree actually carries. Each
// row pairs an identifier with its name and with lines only that licence's text
// opens with; the identifier in force must have a row, so a changed identifier
// cannot pass without someone stating what its text looks like.
//
// 🔴 PLANTED DEFECT: change pluginLicense to "MIT", and the test reddens on the
// missing row; change pluginLicenseName, and it reddens on the name; replace the
// module's LICENSE with another licence's text, and it reddens on the first
// marker that text lacks.
func TestTheDeclaredLicenseIsTheModulesLicense(t *testing.T) {
	known := []struct {
		spdx    string
		name    string
		markers []string
	}{
		{"Apache-2.0", "the Apache License, Version 2.0",
			[]string{"Apache License", "Version 2.0, January 2004", "http://www.apache.org/licenses/"}},
	}
	text, err := os.ReadFile(moduleRootLicense)
	if err != nil {
		t.Fatalf("read the module's licence at %s: %v", moduleRootLicense, err)
	}
	for _, row := range known {
		if row.spdx != pluginLicense {
			continue
		}
		if pluginLicenseName != row.name {
			t.Errorf("the licence %s is named %q to a reader, want %q", row.spdx, pluginLicenseName, row.name)
		}
		for _, marker := range row.markers {
			if !bytes.Contains(text, []byte(marker)) {
				t.Errorf("the manifests declare %s, and the module's licence at %s does not carry %q",
					row.spdx, moduleRootLicense, marker)
			}
		}
		return
	}
	t.Fatalf("the manifests declare the licence %q, which this test has no row for; add one naming the licence "+
		"and the lines its text opens with", pluginLicense)
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

// TestWriteRepositoryLeavesAGitEntryAlone writes the tree into a scratch
// checkout whose root holds a .git — a repository folder, or the pointer file a
// submodule's checkout holds — beside a file the generator does not write, and
// requires the .git to come through byte for byte and the stale file to go.
// It then reads the checkout back and requires it to be exactly the generated
// tree while the .git is still there, so the reader and the writer agree that
// the .git is not part of the tree.
//
// 🔴 PLANTED DEFECT: remove the .git skip from WriteRepository's walk, and the
// "a .git folder" row reddens on its .git/HEAD and the "a .git file" row on its
// .git, each reported as removed; remove it from ReadRepository's walk, and both
// rows redden naming the .git files as there and not written by the generator.
func TestWriteRepositoryLeavesAGitEntryAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		git  map[string]string // the files beneath the scratch root that make up its .git
	}{
		{"a .git folder", map[string]string{".git/HEAD": "ref: refs/heads/main\n", ".git/config": "[core]\n"}},
		{"a .git file", map[string]string{".git": "gitdir: ../.git/modules/aggrega-skills\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for p, content := range tc.git {
				writeScratch(t, filepath.Join(dir, filepath.FromSlash(p)), content)
			}
			stale := filepath.Join(dir, "skills", "retired", "SKILL.md")
			writeScratch(t, stale, "a skill the generator no longer writes\n")

			if err := WriteRepository(dir, "."); err != nil {
				t.Fatalf("WriteRepository: %v", err)
			}
			for p, content := range tc.git {
				got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
				if err != nil {
					t.Errorf("%s was removed: %v", p, err)
					continue
				}
				if string(got) != content {
					t.Errorf("%s = %q, want it left as %q", p, got, content)
				}
			}
			if _, err := os.Stat(stale); err == nil {
				t.Errorf("the stale %s is still there", stale)
			}
			if _, err := os.Stat(filepath.Dir(stale)); err == nil {
				t.Errorf("the folder %s, emptied of its stale file, is still there", filepath.Dir(stale))
			}
			if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
				t.Errorf("the generated README was not written: %v", err)
			}

			want, err := RepositoryFiles(".")
			if err != nil {
				t.Fatalf("generate the repository tree: %v", err)
			}
			got, err := ReadRepository(dir)
			if err != nil {
				t.Fatalf("ReadRepository: %v", err)
			}
			if diffs := DiffRepository(want, got); len(diffs) != 0 {
				t.Errorf("the checkout read back differs from the generated tree while its .git is there:\n  %s",
					strings.Join(diffs, "\n  "))
			}
		})
	}
}

// releaseWorkflow is the workflow whose publishing job pushes the committed
// tree to the public skills repository, as this package's folder reaches it:
// two folders up is the module root. It is inside the module, so the test cache
// tracks it.
var releaseWorkflow = filepath.Join("..", "..", ".github", "workflows", "publish-plugins-sign.yml")

// TestTheReleaseWorkflowPublishesToTheRepositoryTheManifestsName ties the
// repository the release workflow writes the tree to with the one the manifests
// and the README name. The workflow names that repository twice: in the
// SKILLS_REMOTE address the tree is pushed to, and in the repositories the token
// it pushes with is minted for — the step whose id is skills-token. The
// repository part of repositorySlug must be the repository in both. The owner in
// the address is the owner of the repository the workflow runs in, which no file
// states, so only the repository's name is compared.
//
// 🔴 PLANTED DEFECT: change the repository after the slash in repositorySlug, and
// both halves redden; change it in the workflow's SKILLS_REMOTE line, or in the
// skills-token step's repositories line, and that half reddens naming both
// values; rename the SKILLS_REMOTE variable, give it a second line, or rename the
// step's id, and the test reddens on what it could not find.
func TestTheReleaseWorkflowPublishesToTheRepositoryTheManifestsName(t *testing.T) {
	content, err := os.ReadFile(releaseWorkflow)
	if err != nil {
		t.Fatalf("read the release workflow at %s: %v", releaseWorkflow, err)
	}
	lines := strings.Split(string(content), "\n")
	_, want, found := strings.Cut(repositorySlug, "/")
	if !found || want == "" {
		t.Fatalf("repositorySlug %q names no repository after its owner", repositorySlug)
	}

	t.Run("the address the tree is pushed to", func(t *testing.T) {
		const (
			key    = "SKILLS_REMOTE:"
			prefix = "https://github.com/${{ github.repository_owner }}/"
			suffix = ".git"
		)
		var values []string
		for _, line := range lines {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), key); ok {
				values = append(values, strings.TrimSpace(v))
			}
		}
		if len(values) != 1 {
			t.Fatalf("the release workflow at %s carries %d %s line(s), want exactly one", releaseWorkflow, len(values), key)
		}
		repo, ok := strings.CutPrefix(values[0], prefix)
		if ok {
			repo, ok = strings.CutSuffix(repo, suffix)
		}
		if !ok || repo == "" || strings.Contains(repo, "/") {
			t.Fatalf("the release workflow's %s is %q, not an address of the form %s<repository>%s",
				key, values[0], prefix, suffix)
		}
		if repo != want {
			t.Errorf("the release workflow publishes to the repository %q, and the manifests and README name %q "+
				"(repositorySlug %q)", repo, want, repositorySlug)
		}
	})

	t.Run("the repository the token is minted for", func(t *testing.T) {
		const (
			stepID = "id: skills-token"
			key    = "repositories:"
			step   = "- "
		)
		// The step is the run of lines from the list item holding the id to the
		// next list item at the same indentation.
		at := -1
		for i, line := range lines {
			if strings.TrimSpace(line) == stepID {
				if at >= 0 {
					t.Fatalf("the release workflow at %s carries more than one %q", releaseWorkflow, stepID)
				}
				at = i
			}
		}
		if at < 0 {
			t.Fatalf("the release workflow at %s carries no %q step", releaseWorkflow, stepID)
		}
		start := at
		for start >= 0 && !strings.HasPrefix(strings.TrimSpace(lines[start]), step) {
			start--
		}
		if start < 0 {
			t.Fatalf("the %q line of %s belongs to no step", stepID, releaseWorkflow)
		}
		indent := len(lines[start]) - len(strings.TrimLeft(lines[start], " "))
		var values []string
		for _, line := range lines[start+1:] {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, step) && len(line)-len(strings.TrimLeft(line, " ")) == indent {
				break
			}
			if v, ok := strings.CutPrefix(trimmed, key); ok {
				values = append(values, strings.TrimSpace(v))
			}
		}
		if len(values) != 1 {
			t.Fatalf("the %q step of %s carries %d %s line(s), want exactly one", stepID, releaseWorkflow, len(values), key)
		}
		if values[0] != want {
			t.Errorf("the release workflow mints its token for the repositories %q, and the manifests and README "+
				"name %q (repositorySlug %q)", values[0], want, repositorySlug)
		}
	})
}

// decodedManifest is any of the plugin manifests as this test reads it back.
// Every key a manifest may carry is a pointer, so a key that is absent stays
// nil and one that is present is told apart from it.
type decodedManifest struct {
	Schema      *string                       `json:"$schema"`
	Name        *string                       `json:"name"`
	Version     *string                       `json:"version"`
	DisplayName *string                       `json:"displayName"`
	Description *string                       `json:"description"`
	Author      *pluginPerson                 `json:"author"`
	Owner       *pluginPerson                 `json:"owner"`
	Homepage    *string                       `json:"homepage"`
	Repository  *string                       `json:"repository"`
	License     *string                       `json:"license"`
	Interface   *struct{ DisplayName string } `json:"interface"`
	Plugins     []decodedPluginEntry          `json:"plugins"`
	MCPServers  map[string]decodedServer      `json:"mcpServers"`
}

type decodedPluginEntry struct {
	Name        string          `json:"name"`
	Source      json.RawMessage `json:"source"`
	Description *string         `json:"description"`
	Policy      json.RawMessage `json:"policy"`
	Category    *string         `json:"category"`
}

type decodedServer struct {
	Type    *string  `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// TestThePluginManifestsCarryTheOneSourcedValues decodes every plugin manifest
// the generator writes and holds each to the values its format carries: the
// plugin's name, description, homepage, repository, licence and author, and the
// marketplace's owner, where the format has the field, the aggrega MCP server started as `aggrega mcp` where it
// has a server list, and the exact $schema identifiers of the formats that
// publish one. A key the row does not name is refused, so a manifest cannot
// grow a field — a release version, say — without this test saying so. The
// expected values are written out rather than read from the generator's
// declarations, except the description, which is one sentence and is compared
// with the one the generator declares.
//
// Writing them out is deliberate, and reading them from the declarations instead
// would disable the check: these are the values a user's assistant installs
// from, so a change to one must be a change made twice, here and in the
// declaration. Compared with the declarations themselves, an edited constant
// would move the expectation with it and the test would pass on whatever the
// constant had become.
//
// 🔴 PLANTED DEFECT: give the Claude Code manifest a version field, drop
// "type": "stdio" from mcp.json's server, or change one $schema identifier, and
// the row for that file reddens naming the key; add a manifest the table has no
// row for, or stop writing one it has, and the set check reddens naming the path.
func TestThePluginManifestsCarryTheOneSourcedValues(t *testing.T) {
	const (
		name       = "aggrega"
		homepage   = "https://aggrega.ai/docs/ai-assistants/skill/"
		repository = "https://github.com/aggrega-ai/aggrega-skills"
		license    = "Apache-2.0"
	)
	// The author names who writes the plugin and the owner names who publishes
	// the marketplace, so they are two expected values, not one.
	author := pluginPerson{Name: "Cognitio s.r.l.", URL: "https://aggrega.ai"}
	owner := pluginPerson{Name: "aggrega-ai", URL: "https://github.com/aggrega-ai"}
	if strings.TrimSpace(pluginDescription) == "" {
		t.Fatal("the plugin's description is empty, so comparing every manifest with it proves nothing")
	}

	rows := []struct {
		path   string
		schema string // the exact $schema, or "" where the format names none
		keys   string // every top-level key the manifest carries besides $schema, space-separated
		stdio  bool   // the server entry states its transport
	}{
		{path: "plugin.json", schema: "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
			keys: "name description author homepage repository license"},
		{path: "mcp.json", schema: "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
			keys: "mcpServers", stdio: true},
		{path: ".claude-plugin/plugin.json", keys: "name description author homepage repository license mcpServers"},
		{path: ".claude-plugin/marketplace.json", keys: "name description owner plugins"},
		{path: ".agents/plugins/marketplace.json", keys: "name interface plugins"},
		{path: "gemini-extension.json", keys: "name version description mcpServers"},
		{path: ".antigravity-plugin/plugin.json", schema: "https://antigravity.google/schemas/v1/plugin.json",
			keys: "name displayName description author homepage repository license"},
		{path: "mcp_config.json", keys: "mcpServers"},
	}

	files, err := RepositoryFiles(".")
	if err != nil {
		t.Fatalf("generate the repository tree: %v", err)
	}
	byPath := map[string][]byte{}
	for _, f := range files {
		if path.Ext(f.Path) == ".json" {
			byPath[f.Path] = f.Content
		}
	}
	listed := map[string]bool{}
	for _, row := range rows {
		listed[row.path] = true
		if _, ok := byPath[row.path]; !ok {
			t.Errorf("the generated tree carries no %s", row.path)
		}
	}
	var unlisted []string
	for p := range byPath {
		if !listed[p] {
			unlisted = append(unlisted, p)
		}
	}
	sort.Strings(unlisted)
	for _, p := range unlisted {
		t.Errorf("the generated tree carries %s, which this test has no row for", p)
	}

	for _, row := range rows {
		content, ok := byPath[row.path]
		if !ok {
			continue
		}
		t.Run(row.path, func(t *testing.T) {
			if !bytes.HasSuffix(content, []byte("}\n")) || bytes.HasSuffix(content, []byte("\n\n")) {
				t.Errorf("the file does not end in its closing brace and one newline")
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(content, &raw); err != nil {
				t.Fatalf("not a JSON object: %v", err)
			}
			want := map[string]bool{}
			for _, k := range strings.Fields(row.keys) {
				want[k] = true
			}
			if row.schema != "" {
				want["$schema"] = true
			}
			for k := range raw {
				if !want[k] {
					t.Errorf("carries the key %q, which its format is not given", k)
				}
			}
			for k := range want {
				if _, ok := raw[k]; !ok {
					t.Errorf("carries no %q", k)
				}
			}

			var m decodedManifest
			dec := json.NewDecoder(bytes.NewReader(content))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&m); err != nil {
				t.Fatalf("decode: %v", err)
			}
			eq := func(key string, got *string, want string) {
				t.Helper()
				if got != nil && *got != want {
					t.Errorf("%s = %q, want %q", key, *got, want)
				}
			}
			eq("$schema", m.Schema, row.schema)
			eq("name", m.Name, name)
			eq("description", m.Description, pluginDescription)
			eq("homepage", m.Homepage, homepage)
			eq("repository", m.Repository, repository)
			eq("license", m.License, license)
			eq("displayName", m.DisplayName, "Aggrega")
			if m.Version != nil && *m.Version == "" {
				t.Error("version is empty")
			}
			if m.Author != nil && *m.Author != author {
				t.Errorf("author = %+v, want %+v", *m.Author, author)
			}
			if m.Owner != nil && *m.Owner != owner {
				t.Errorf("owner = %+v, want %+v", *m.Owner, owner)
			}
			if m.Interface != nil && m.Interface.DisplayName != "Aggrega" {
				t.Errorf("interface.displayName = %q, want %q", m.Interface.DisplayName, "Aggrega")
			}
			for _, p := range m.Plugins {
				if p.Name != name {
					t.Errorf("a plugins entry is named %q, want %q", p.Name, name)
				}
				eq("plugins[].description", p.Description, pluginDescription)
			}
			if want["plugins"] && len(m.Plugins) != 1 {
				t.Errorf("lists %d plugins, want the one", len(m.Plugins))
			}

			if !want["mcpServers"] {
				return
			}
			if len(m.MCPServers) != 1 {
				t.Fatalf("lists %d MCP servers, want the one named %q", len(m.MCPServers), name)
			}
			server, ok := m.MCPServers[name]
			if !ok {
				t.Fatalf("lists no MCP server named %q", name)
			}
			if server.Command != "aggrega" || strings.Join(server.Args, " ") != "mcp" || len(server.Args) != 1 {
				t.Errorf("the server is started as %q %q, want \"aggrega\" [\"mcp\"]", server.Command, server.Args)
			}
			switch {
			case row.stdio && (server.Type == nil || *server.Type != "stdio"):
				t.Errorf("the server's type is %v, want \"stdio\"", server.Type)
			case !row.stdio && server.Type != nil:
				t.Errorf("the server carries a type, %q, its format is not given", *server.Type)
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
		for _, want := range []string{`"no-such-assistant"`, "aggrega mcp", SetupPage(), "claude-code"} {
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

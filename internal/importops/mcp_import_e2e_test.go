// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs of the reference MCP importer through the real Import, over a
// workspace whose catalog folder holds the reference importers and a JSON parser
// beside their entities:
//
//   - each MCP client's configuration, laid down under the name its client reads
//     it by, and each registry server description is recognised as the mcp
//     format with no format named and drafted as an Aggregate holding one
//     core/ai-tool/mcp instance per server and a Release beside it;
//   - by default every value of a server's configuration is carried as written
//     into the Release — a header's value, and each part of an address
//     infra.endpoint leaves out, in the comment above its server — and named in
//     a warning, and the empty_credentials setting at marked or all writes each
//     empty; what the draft never writes — a static OAuth client — reaches no
//     draft whatever the setting says;
//   - a directory holding a project's client files is imported as one Aggregate;
//   - a commented Visual Studio Code configuration is recognised naming no format
//     and drafted exactly as its copy without comments, while a commented
//     configuration of a client reading JSON halts with the JSON parser's reason;
//   - two imports of one configuration are byte-identical;
//   - a source the parser refuses halts the import with the parser's reason, for
//     every importer.
//
// The fixtures are authored here to the shapes their clients and the MCP
// registry document (their README says so); none is copied.

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// mcpFixtureDir holds the committed MCP fixtures, relative to this package.
const mcpFixtureDir = "testdata/mcp"

// mcpFixtures are the committed MCP fixtures, each with the name its client reads
// it by, which an import is handed it under, and the instance keys its draft
// holds, in the order the draft writes them: the order the fixture lists its
// servers, and name order for Visual Studio Code's configuration, read through
// the parser for JSON with comments.
var mcpFixtures = []struct {
	fixture, name string
	keys          []string
}{
	{"claude-code.mcp.json", ".mcp.json", []string{"api-server", "github"}},
	{"claude_desktop_config.json", "claude_desktop_config.json", []string{"brave-search", "postgres"}},
	{"vscode.mcp.json", ".vscode/mcp.json", []string{"docs", "perplexity"}},
	{"cursor.mcp.json", ".cursor/mcp.json", []string{"remote-search", "local-files", "oauth-server"}},
	{"registry-npm.server.json", "server.json", []string{"io-github-example-weather"}},
	{"registry-remote.server.json", "server.json", []string{"com-example-tickets"}},
	{"registry-oci.server.json", "server.json", []string{"io-github-example-notes"}},
}

// readMCPFixture reads one committed MCP fixture.
func readMCPFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(mcpFixtureDir, name))
	if err != nil {
		t.Fatalf("read the committed fixture %s: %v", name, err)
	}
	return body
}

// mcpDrafts reads the Aggregate and the Release an MCP import wrote under dir as
// the compiler reads YAML, and returns the instance keys of each, in the order
// each document writes them, and the Release's text.
func mcpDrafts(t *testing.T, dir string) (aggregateKeys, releaseKeys []string, aggregate string, release string) {
	t.Helper()
	keysOf := func(path string) ([]string, string, *yaml.Node) {
		content, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			t.Fatalf("read the drafted %s: %v", path, err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(content, &doc); err != nil {
			t.Fatalf("the drafted %s does not read as YAML: %v", path, err)
		}
		components := yamlMember(yamlMember(doc.Content[0], "spec"), "components")
		var keys []string
		for i := 0; components != nil && i+1 < len(components.Content); i += 2 {
			keys = append(keys, components.Content[i].Value)
		}
		return keys, string(content), doc.Content[0]
	}
	var aggregateDoc, releaseDoc *yaml.Node
	aggregateKeys, aggregate, aggregateDoc = keysOf("aggregate.yaml")
	releaseKeys, release, releaseDoc = keysOf("release.yaml")
	name := yamlMember(yamlMember(aggregateDoc, "metadata"), "name")
	if got := yamlMember(yamlMember(releaseDoc, "spec"), "aggregate"); name == nil || got == nil || got.Value != name.Value {
		t.Errorf("the drafted Release does not name the drafted Aggregate:\n%s\n%s", aggregate, release)
	}
	return aggregateKeys, releaseKeys, aggregate, release
}

// yamlMember returns the value of the member named key of the mapping node, nil
// where it holds none.
func yamlMember(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// TestEveryMCPFileIsRecognisedAndDraftedAsAnAggregateAndARelease imports each
// committed fixture under the name its client reads it by, naming no format, and
// requires it read as the mcp format and drafted as aggregate.yaml and
// release.yaml alone: the Aggregate an instance of core/ai-tool/mcp per server,
// and the Release naming the Aggregate and holding an entry for each instance,
// both in the order mcpFixtures gives.
//
// 🔴 PLANTED DEFECT: in detect (plugins/mcp-importer/main.go), write
// `{Path: "mcpServers", Is: "array"}` in the .mcp.json signature: the Claude Code
// row is refused asking for the format, and the case fails on it.
func TestEveryMCPFileIsRecognisedAndDraftedAsAnAggregateAndARelease(t *testing.T) {
	t.Parallel()
	for _, f := range mcpFixtures {
		t.Run(f.fixture, func(t *testing.T) {
			t.Parallel()
			rw, _ := catalogFolderImportWorkspace(t)
			res, dir := writtenImport(t, rw, f.name, readMCPFixture(t, f.fixture))
			if res.Format != "mcp" {
				t.Fatalf("the source was read as the %q format, want mcp", res.Format)
			}
			if res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}
			if got := draftPaths(res); !slices.Equal(got, []string{"aggregate.yaml", "release.yaml"}) {
				t.Errorf("the import drafted %v, want the Aggregate and the Release", got)
			}
			aggregateKeys, releaseKeys, aggregate, _ := mcpDrafts(t, dir)
			if !slices.Equal(aggregateKeys, f.keys) || !slices.Equal(releaseKeys, f.keys) {
				t.Errorf("the Aggregate holds %v and the Release %v, want %v in both", aggregateKeys, releaseKeys, f.keys)
			}
			if got := strings.Count(aggregate, "      type: core/ai-tool/mcp\n"); got != len(f.keys) {
				t.Errorf("the Aggregate holds %d core/ai-tool/mcp instances, want %d:\n%s", got, len(f.keys), aggregate)
			}
		})
	}
}

// The key-shaped values the MCP fixtures plant: the key the Claude Desktop
// fixture's search server holds in its environment and the password its
// database server's connection string holds; and, in the Cursor fixture, the
// keys a remote server's address carries in its user part, its query and its
// fragment, the key it is sent in a header and the static OAuth client another
// server signs in with. The Cursor fixture's remote server is sent a variable
// reference in a second header.
const (
	plantedMCPEnvKey     = "BSA-planted-key-shaped-needle-7f3a9c2e"
	plantedMCPArgument   = "planted-password-needle-91b4"
	plantedMCPUserPart   = "planted-userpart-needle-3e6a"
	plantedMCPQueryKey   = "planted-query-needle-5d81"
	plantedMCPFragment   = "planted-fragment-needle-c08f"
	plantedMCPHeaderKey  = "planted-header-needle-b902"
	plantedMCPClientID   = "planted-client-id-needle-0a17"
	plantedMCPClientCode = "planted-client-secret-needle-4e1c"
	mcpHeaderReference   = "${env:SEARCH_TOKEN}"
)

// TestTheEmptyCredentialsSettingDecidesWhatAnMCPImportCarries imports the Claude
// Desktop and the Cursor fixtures with the empty_credentials setting absent,
// none, marked and all, after a needle proves each planted value is in its
// fixture:
//
//   - where the setting is absent or none, the environment's key, the
//     connection string's password, the header's key and the keys the remote
//     server's address carries in its user part, its query and its fragment are
//     carried as written into release.yaml — the header's key and the address's
//     parts in the comment above their server, beside the header and the part's
//     name, infra.endpoint holding the address without them — and reach nothing
//     else — no other file, no decision, no diagnostic — and each is named in a
//     credential-carried warning by its server and its place;
//   - where it is marked or all, none reaches anything, the comment names the
//     header and each part alone, and each is named in a credential-emptied
//     warning by its server and its place;
//   - the header holding a variable reference is written as the reference it
//     reads from, and the OAuth client reaches nothing, under any value of the
//     setting.
//
// 🔴 PLANTED DEFECT: in located (plugins/internal/mcpimport/mcpimport.go), give
// every value the class importer.CredentialUnmarked: the marked rows fail, the
// key and the password carried as written.
func TestTheEmptyCredentialsSettingDecidesWhatAnMCPImportCarries(t *testing.T) {
	t.Parallel()
	desktop := readMCPFixture(t, "claude_desktop_config.json")
	cursor := readMCPFixture(t, "cursor.mcp.json")
	for _, setting := range []string{"absent", "none", "marked", "all"} {
		t.Run(setting, func(t *testing.T) {
			t.Parallel()
			var settings map[string]any
			if setting != "absent" {
				settings = map[string]any{"empty_credentials": setting}
			}
			emptied := setting == "marked" || setting == "all"

			res, dir := writtenImport(t, mcpWorkspace(t, settings),
				"claude_desktop_config.json", desktop)
			planted := []string{plantedMCPEnvKey, plantedMCPArgument}
			if emptied {
				assertNoneReaches(t, planted, desktop, res, dir)
			} else {
				assertCarriedOnlyInTheRelease(t, planted, desktop, res, dir)
			}
			code := "credential-carried"
			if emptied {
				code = "credential-emptied"
			}
			named := strings.Join(diagnosticsCoded(res.Diagnostics, code), "\n")
			for _, field := range []string{"server_env.BRAVE_API_KEY of node brave-search",
				"infra.stdio_command.3 of node postgres"} {
				if !strings.Contains(named, field) {
					t.Errorf("no %s names %q:\n%s", code, field, named)
				}
			}

			res, dir = writtenImport(t, mcpWorkspace(t, settings), ".cursor/mcp.json", cursor)
			address := []string{plantedMCPUserPart, plantedMCPQueryKey, plantedMCPFragment}
			header := "    #   - x-api-key: \"" + plantedMCPHeaderKey + "\"\n"
			parts := []string{"    #   - user part: \"reader:" + plantedMCPUserPart + "\"\n",
				"    #   - query: \"api_key=" + plantedMCPQueryKey + "\"\n",
				"    #   - fragment: \"" + plantedMCPFragment + "\"\n",
				"    remote-search:\n      infra:\n        endpoint: \"https://search.example.com/mcp\"\n"}
			partReport := func(part string) string { return "url of node remote-search is carried with its " + part }
			if emptied {
				header = "    #   - x-api-key, its value left out, as the empty_credentials setting asks\n"
				parts = []string{"    #   - user part, its value left out, as the empty_credentials setting asks\n",
					"    #   - query, its value left out, as the empty_credentials setting asks\n",
					"    #   - fragment, its value left out, as the empty_credentials setting asks\n",
					"    remote-search:\n      infra:\n        # Left for the Release, since the source cannot supply it: " +
						"infra.endpoint\n        endpoint: \"\"\n"}
				partReport = func(part string) string { return "url of node remote-search is written with its " + part }
				assertNoneReaches(t, append([]string{plantedMCPHeaderKey, plantedMCPClientID, plantedMCPClientCode},
					address...), cursor, res, dir)
			} else {
				assertCarriedOnlyInTheRelease(t, append([]string{plantedMCPHeaderKey}, address...), cursor, res, dir)
				assertNoneReaches(t, []string{plantedMCPClientID, plantedMCPClientCode}, cursor, res, dir)
			}
			release, err := os.ReadFile(filepath.Join(dir, "release.yaml"))
			if err != nil {
				t.Fatalf("read the drafted Release: %v", err)
			}
			for _, want := range append([]string{header,
				"    #   - Authorization, its value read from " + mcpHeaderReference + "\n"}, parts...) {
				if !strings.Contains(string(release), want) {
					t.Errorf("the drafted Release does not hold %q:\n%s", want, release)
				}
			}
			for _, needle := range address {
				for _, line := range strings.Split(string(release), "\n") {
					if strings.Contains(line, needle) && !strings.HasPrefix(line, "    #   - ") {
						t.Errorf("the planted %q is on a line other than its part's in the comment: %q", needle, line)
					}
				}
			}
			named = strings.Join(diagnosticsCoded(res.Diagnostics, code), "\n")
			for _, field := range []string{"headers.x-api-key of node remote-search", partReport("user part"),
				partReport("query"), partReport("fragment")} {
				if !strings.Contains(named, field) {
					t.Errorf("no %s names %q:\n%s", code, field, named)
				}
			}
			if strings.Contains(strings.Join(diagnosticsCoded(res.Diagnostics, "credential-carried"), "\n")+
				strings.Join(diagnosticsCoded(res.Diagnostics, "credential-emptied"), "\n"), "headers.Authorization") {
				t.Errorf("a report names the header holding a variable reference, which holds no value")
			}
		})
	}
}

// mcpWorkspace is catalogFolderImportWorkspaceAttaching returning the workspace
// alone.
func mcpWorkspace(t *testing.T, settings map[string]any) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, _ := catalogFolderImportWorkspaceAttaching(t, settings)
	return rw
}

// assertCarriedOnlyInTheRelease requires that each of planted is in the drafted
// release.yaml and in nothing else the import wrote or reported, after the
// needle: each is in the source.
func assertCarriedOnlyInTheRelease(t *testing.T, planted []string, source []byte, res Result, outputDir string) {
	t.Helper()
	release, err := os.ReadFile(filepath.Join(outputDir, "release.yaml"))
	if err != nil {
		t.Fatalf("read the drafted Release: %v", err)
	}
	for _, p := range planted {
		if !strings.Contains(string(release), p) {
			t.Errorf("the drafted Release does not carry the planted %q as written", p)
		}
	}
	others := res
	others.Written = slices.DeleteFunc(slices.Clone(res.Written), func(p string) bool { return p == "release.yaml" })
	assertNoneReaches(t, planted, source, others, outputDir)
}

// TestAProjectDirectoryOfClientFilesImportsAsOneAggregate imports a directory
// holding a project's Claude Code, Visual Studio Code and Cursor configurations,
// naming no format: the files the importer's patterns select are read, by name,
// and one Aggregate and one Release compose every server of the three, in the
// order of the files' names and of each file's servers as its draft writes them.
func TestAProjectDirectoryOfClientFilesImportsAsOneAggregate(t *testing.T) {
	t.Parallel()
	rw, _ := catalogFolderImportWorkspace(t)
	dir := filepath.Join(t.TempDir(), "project")
	for _, f := range mcpFixtures[:4] {
		if f.name == "claude_desktop_config.json" {
			continue
		}
		importopstest.WriteSource(t, filepath.Join(dir, filepath.FromSlash(f.name)), string(readMCPFixture(t, f.fixture)))
	}
	importopstest.WriteSource(t, filepath.Join(dir, "README.md"), "a file no pattern selects\n")
	res, err := importDirectory(t, rw, dir, "project", "")
	if err != nil {
		t.Fatalf("the directory was refused: %v", err)
	}
	if res.Format != "mcp" || res.Halted() {
		t.Fatalf("the directory was read as %q, halted %v: %+v", res.Format, res.Halted(), res.Diagnostics)
	}
	var aggregate string
	for _, f := range res.Files {
		if f.Path == "aggregate.yaml" {
			aggregate = f.Content
		}
	}
	var keys []string
	for _, line := range strings.Split(aggregate, "\n") {
		if strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "     ") && strings.HasSuffix(line, ":") {
			keys = append(keys, strings.TrimSuffix(strings.TrimSpace(line), ":"))
		}
	}
	want := []string{"remote-search", "local-files", "oauth-server", "api-server", "github", "docs", "perplexity"}
	if !slices.Equal(keys, want) {
		t.Errorf("the Aggregate composes %v, want %v:\n%s", keys, want, aggregate)
	}
}

// TestACommentedVSCodeConfigurationIsRecognisedAndDraftedAsItsCopyWithout
// imports a .vscode/mcp.json written as JSON with comments — a line comment, a
// block comment and trailing commas — and the same configuration with them
// removed, each naming no format and naming mcp: each is recognised as the mcp
// format, and the commented one drafts exactly the files its copy drafts, byte
// for byte, its strings holding // kept. A .mcp.json holding a comment, which
// Claude Code reads as JSON, is recognised by no signature, and naming its
// format halts under parse-failed with the JSON parser's reason.
//
// 🔴 PLANTED DEFECT: in detect (plugins/mcp-importer/main.go), read the
// .vscode/mcp.json signature as mcpimport.Syntax: the commented file naming no
// format is refused as unrecognised, and the case fails on it.
func TestACommentedVSCodeConfigurationIsRecognisedAndDraftedAsItsCopyWithout(t *testing.T) {
	t.Parallel()
	commented := readMCPFixture(t, "vscode-commented.mcp.json")
	for _, needle := range []string{"//", "/*", ",\n    },"} {
		if !strings.Contains(string(commented), needle) {
			t.Fatalf("the needle: %q is not in the commented fixture, so it proves nothing", needle)
		}
	}
	rw, _ := catalogFolderImportWorkspace(t)
	// drafts imports fixture as .vscode/mcp.json naming format and returns each
	// drafted file's path and content, in the importer's order.
	drafts := func(fixture, format string) []string {
		t.Helper()
		path := importopstest.WriteSource(t, filepath.Join(t.TempDir(), ".vscode", "mcp.json"),
			string(readMCPFixture(t, fixture)))
		res, err := importDirectory(t, rw, path, ".vscode/mcp.json", format)
		if err != nil || res.Halted() || res.Format != "mcp" {
			t.Fatalf("%s naming the format %q was not drafted as mcp: %v %q %+v", fixture, format, err, res.Format,
				res.Diagnostics)
		}
		var files []string
		for _, f := range res.Files {
			files = append(files, f.Path+"\n"+f.Content)
		}
		return files
	}
	want := drafts("vscode-uncommented.mcp.json", "")
	for _, format := range []string{"", "mcp"} {
		if got := drafts("vscode-commented.mcp.json", format); !slices.Equal(got, want) {
			t.Errorf("naming the format %q, the commented configuration drafts\n%s\nand its copy without comments\n%s",
				format, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
	release := strings.Join(want, "\n")
	fetch := "    fetch:\n      infra:\n        stdio_command:\n          - \"uvx\"\n          - \"mcp-server-fetch\"\n" +
		"          - \"--user-agent=https://example.com//agent\"\n        stdio_runtime: pypi\n"
	if !strings.Contains(release, fetch) {
		t.Errorf("the drafted Release does not hold\n%s\nin\n%s", fetch, release)
	}

	claude := importopstest.WriteSource(t, filepath.Join(t.TempDir(), ".mcp.json"),
		"{\"mcpServers\": {\n  // a comment\n  \"x\": {\"command\": \"npx\"}}}\n")
	_, err := importDirectory(t, rw, claude, ".mcp.json", "")
	var undetected *UndetectedFormatError
	if !errors.As(err, &undetected) {
		t.Errorf("a commented .mcp.json naming no format was not refused as unrecognised: %v", err)
	}
	res, err := importDirectory(t, rw, claude, ".mcp.json", "mcp")
	if err != nil {
		t.Fatalf("a commented .mcp.json naming its format was refused before the importer ran: %v", err)
	}
	failed := diagnosticsCoded(res.Diagnostics, "parse-failed")
	if !res.Halted() || len(failed) != 1 || !strings.Contains(failed[0], "as mcp: json parse: invalid character '/'") {
		t.Errorf("a commented .mcp.json naming its format reported parse-failed %q, want the JSON parser's reason",
			failed)
	}
}

// TestTwoImportsOfAnMCPConfigurationAreByteIdentical imports the Cursor fixture
// twice, each into a workspace of its own, and requires the two to draft the same
// files, byte for byte.
func TestTwoImportsOfAnMCPConfigurationAreByteIdentical(t *testing.T) {
	t.Parallel()
	body := readMCPFixture(t, "cursor.mcp.json")
	first, _ := writtenImport(t, mcpWorkspace(t, nil), ".cursor/mcp.json", body)
	second, _ := writtenImport(t, mcpWorkspace(t, nil), ".cursor/mcp.json", body)
	if len(first.Files) == 0 || len(first.Files) != len(second.Files) {
		t.Fatalf("the two imports drafted %d and %d files", len(first.Files), len(second.Files))
	}
	for i := range first.Files {
		if first.Files[i].Path != second.Files[i].Path || first.Files[i].Content != second.Files[i].Content {
			t.Errorf("the two imports differ at %s", first.Files[i].Path)
		}
	}
}

// TestASourceTheParserRefusesHaltsTheImportWithTheParsersReason imports a JSON
// source cut short through each reference importer, naming its format: the parser
// refuses it, and the import halts under parse-failed carrying the parser's
// reason — never warning that the source held no node.
//
// 🔴 PLANTED DEFECT: in ImportArtifact
// (internal/compiler/importersocket/importersocket.go), drop the ParseRefusal
// check in the parse callback: each importer walks a null tree and warns
// no-nodes, and every row fails.
func TestASourceTheParserRefusesHaltsTheImportWithTheParsersReason(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"langflow", "flowise", "n8n", "mcp"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			rw, _ := catalogFolderImportWorkspace(t)
			path := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "cut.json"), `{"nodes": [`)
			res, err := importDirectory(t, rw, path, "cut.json", format)
			if err != nil {
				t.Fatalf("the import was refused before the importer ran: %v", err)
			}
			if !res.Halted() {
				t.Errorf("the import did not halt: %+v", res.Diagnostics)
			}
			failed := diagnosticsCoded(res.Diagnostics, "parse-failed")
			if len(failed) != 1 || !strings.Contains(failed[0], "as "+format+": json parse: unexpected EOF") {
				t.Errorf("the import reported parse-failed %q, want one carrying the parser's reason", failed)
			}
			if empty := diagnosticsCoded(res.Diagnostics, "no-nodes"); len(empty) != 0 {
				t.Errorf("the import reported the refused source as one holding no node: %q", empty)
			}
		})
	}
}

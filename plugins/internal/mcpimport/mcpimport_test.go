// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcpimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// The MCP importer on the host toolchain: each case hands Import a request and
// the parse the host would run — the compiler's own JSON parser, its refusal
// read as the import socket reads it — and reads what it drafts.

// hostParse parses source as the host's JSON parser does and refuses it as the
// import socket hands an importer a parser's refusal.
func hostParse(syntax string, source []byte) (json.RawMessage, error) {
	out := parsercore.ParseJSON(source)
	if err := importersocket.ParseRefusal(out); err != nil {
		return nil, err
	}
	return out.Tree, nil
}

// fixture reads one committed MCP fixture (internal/importops/testdata/mcp).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "internal", "importops", "testdata", "mcp", name))
	if err != nil {
		t.Fatalf("read the fixture %s: %v", name, err)
	}
	return body
}

// importOne imports body as the one file of a request, with settings.
func importOne(body []byte, settings string) aggrega.ImportOutput {
	in := aggrega.ImportInput{Format: Format, Bytes: body}
	if settings != "" {
		in.Settings = json.RawMessage(settings)
	}
	return Import(in, hostParse)
}

// fileContent returns the content of the file out holds at path, failing the
// test where it holds none.
func fileContent(t *testing.T, out aggrega.ImportOutput, path string) string {
	t.Helper()
	for _, f := range out.Files {
		if f.Path == path {
			return f.Content
		}
	}
	t.Fatalf("the import wrote no %s; it wrote %+v (diagnostics %+v)", path, out.Files, out.Diagnostics)
	return ""
}

// codes returns the messages of the diagnostics coded code, in order.
func codes(out aggrega.ImportOutput, code string) []string {
	var messages []string
	for _, d := range out.Diagnostics {
		if d.Code == code {
			messages = append(messages, d.Message)
		}
	}
	return messages
}

// The key-shaped values the Claude Desktop fixture plants: the key its search
// server's environment holds, where the client's documented example writes one,
// and the password its database server's connection string, an argument, holds.
const (
	plantedKey      = "BSA-planted-key-shaped-needle-7f3a9c2e"
	plantedPassword = "planted-password-needle-91b4"
)

// desktopAggregate and desktopRelease are what the Claude Desktop fixture is
// drafted as with no setting: one instance per server, and each server's command
// line, launcher and environment carried as written.
const desktopAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: imported/flow
  version: 0.1.0
  description: Draft Aggregate imported from a source in the mcp format.
spec:
  components:
    brave-search:
      type: core/ai-tool/mcp
    postgres:
      type: core/ai-tool/mcp
`

const desktopRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: imported/flow
  # environment: left for the operator, since no source names the environment this Release deploys to.
  version: 0.1.0
  description: Draft Release imported from a source in the mcp format.
spec:
  aggregate: imported/flow
  # manifest: left for the operator, since the import drafts no Manifest, which names the target the servers are built for.
  components:
    brave-search:
      infra:
        stdio_command:
          - "npx"
          - "-y"
          - "@modelcontextprotocol/server-brave-search"
        stdio_runtime: npm
      features:
        server_env:
          type: core/referenced-secret-set
          infra:
            entries:
              - key: "APPDATA"
                value: "C:\\Users\\user\\AppData\\Roaming\\"
              - key: "BRAVE_API_KEY"
                value: "` + plantedKey + `"
    postgres:
      infra:
        stdio_command:
          - "npx"
          - "-y"
          - "@modelcontextprotocol/server-postgres"
          - "postgresql://reader:` + plantedPassword + `@localhost/mydb"
        stdio_runtime: npm
`

// TestAClientConfigurationIsDraftedAsAnAggregateAndARelease imports the Claude
// Desktop fixture with no setting and requires the two drafts, byte for byte,
// and the entities naming them: a referenced core/ai-tool/mcp per server, the
// drafted Aggregate and the drafted Release.
//
// 🔴 PLANTED DEFECT: in renderRelease (render.go), drop the call to writeServer:
// the Release holds no component, and the case fails comparing it.
func TestAClientConfigurationIsDraftedAsAnAggregateAndARelease(t *testing.T) {
	t.Parallel()
	out := importOne(fixture(t, "claude_desktop_config.json"), "")
	if got := fileContent(t, out, "aggregate.yaml"); got != desktopAggregate {
		t.Errorf("the drafted Aggregate reads\n%s\nwant\n%s", got, desktopAggregate)
	}
	if got := fileContent(t, out, "release.yaml"); got != desktopRelease {
		t.Errorf("the drafted Release reads\n%s\nwant\n%s", got, desktopRelease)
	}
	var kinds []string
	for _, e := range out.Entities {
		kinds = append(kinds, e.Kind+" "+e.Name+" "+e.Mode+" "+e.SourceRef)
	}
	want := []string{
		"Component core/ai-tool/mcp reference brave-search",
		"Component core/ai-tool/mcp reference postgres",
		"Aggregate imported/flow draft ",
		"Release imported/flow draft ",
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("the import emitted %q, want %q", kinds, want)
	}
}

// TestTheEmptyCredentialsSettingDecidesWhatTheReleaseCarries imports the Claude
// Desktop fixture under every value of the empty_credentials setting:
//
//   - absent or none: the planted key and the planted argument are carried as
//     written into the Release, and each value of an MCP server's configuration is
//     named in a credential-carried warning by its server and its place — the
//     key as server_env.BRAVE_API_KEY, the argument by its position — never by
//     its value;
//   - marked or all: neither planted value is in any file, entity or diagnostic,
//     the environment value is left unset and the argument written empty, each
//     reported under credential-emptied by server and place;
//   - a value the setting does not take halts the import, which drafts nothing.
//
// 🔴 PLANTED DEFECT: in located (mcpimport.go), skip the environment's values:
// the key is reported by no warning, and under marked or all it is carried as
// written, so the case fails on both.
func TestTheEmptyCredentialsSettingDecidesWhatTheReleaseCarries(t *testing.T) {
	t.Parallel()
	body := fixture(t, "claude_desktop_config.json")
	for _, needle := range []string{plantedKey, plantedPassword} {
		if !strings.Contains(string(body), needle) {
			t.Fatalf("the needle: %q is not in the fixture, so the search finds nothing", needle)
		}
	}
	named := []string{"server_env.BRAVE_API_KEY of node brave-search", "infra.stdio_command.3 of node postgres"}
	for _, row := range []struct {
		settings string
		emptied  bool
	}{
		{"", false}, {`{"empty_credentials":"none"}`, false},
		{`{"empty_credentials":"marked"}`, true}, {`{"empty_credentials":"all"}`, true},
	} {
		t.Run(row.settings, func(t *testing.T) {
			t.Parallel()
			out := importOne(body, row.settings)
			release := fileContent(t, out, "release.yaml")
			everything, _ := json.Marshal(out)
			code, other := "credential-carried", "credential-emptied"
			if row.emptied {
				code, other = other, code
			}
			reported := strings.Join(codes(out, code), "\n")
			for _, field := range named {
				if !strings.Contains(reported, field) {
					t.Errorf("no %s names %q:\n%s", code, field, reported)
				}
			}
			if stray := codes(out, other); len(stray) != 0 {
				t.Errorf("the import reports %s as well: %q", other, stray)
			}
			diagnostics, _ := json.Marshal(out.Diagnostics)
			for _, needle := range []string{plantedKey, plantedPassword} {
				if strings.Contains(string(diagnostics), needle) {
					t.Errorf("a diagnostic holds the planted %q", needle)
				}
				if row.emptied && strings.Contains(string(everything), needle) {
					t.Errorf("the import's output holds the planted %q", needle)
				}
				if !row.emptied && !strings.Contains(release, needle) {
					t.Errorf("the Release does not carry the planted %q as written", needle)
				}
			}
			if row.emptied {
				for _, want := range []string{"              - key: \"BRAVE_API_KEY\"\n    # ",
					"    postgres:\n      infra:\n        stdio_command:\n          - \"\"\n          - \"\"\n          - \"\"\n" +
						"          - \"\"\n",
					"    #   - the value of server_env.BRAVE_API_KEY\n", "    #   - infra.stdio_command.3, written empty\n"} {
					if !strings.Contains(release, want) {
						t.Errorf("the Release does not hold %q:\n%s", want, release)
					}
				}
			}
		})
	}
	out := importOne(body, `{"empty_credentials":"some"}`)
	if len(out.Files) != 0 || len(out.Entities) != 0 || len(out.Diagnostics) != 1 ||
		out.Diagnostics[0].Code != "unknown-empty-credentials" || out.Diagnostics[0].Severity != "halt" {
		t.Errorf("a value the setting does not take drafted %+v and reported %+v, want nothing and one halt", out.Files,
			out.Diagnostics)
	}
}

// TestAVariableReferenceIsWrittenAsTheTextItIs imports a server whose command
// line, address and environment hold variable references, under no setting and
// under marked: a value that is one reference and nothing else holds no value,
// so it is written as written, located by no credential report, and an
// environment variable whose value is one is left for the Release, its reference
// named; a reference carrying a default, or text beside one, holds a value and
// is written empty under marked like any other.
func TestAVariableReferenceIsWrittenAsTheTextItIs(t *testing.T) {
	t.Parallel()
	body := []byte(`{"mcpServers":{"tool":{"command":"node","args":["${input:script}","--token=${TOKEN}","${TOKEN}",` +
		`"${MODE:-fast}"],"env":{"KEY":"${env:KEY}","PLAIN":"plain-value"}}}}`)
	for _, row := range []struct {
		settings string
		command  []string
	}{
		{"", []string{"node", "${input:script}", "--token=${TOKEN}", "${TOKEN}", "${MODE:-fast}"}},
		{`{"empty_credentials":"marked"}`, []string{"", "${input:script}", "", "${TOKEN}", ""}},
	} {
		out := importOne(body, row.settings)
		release := fileContent(t, out, "release.yaml")
		var want strings.Builder
		want.WriteString("        stdio_command:\n")
		for _, c := range row.command {
			want.WriteString("          - " + yamlString(c) + "\n")
		}
		if !strings.Contains(release, want.String()) {
			t.Errorf("under %q the Release does not write the command line\n%s\nin\n%s", row.settings, want.String(),
				release)
		}
		if !strings.Contains(release, "    #   - the value of server_env.KEY, which the source reads from ${env:KEY}\n") ||
			!strings.Contains(release, "              - key: \"KEY\"\n              - key: \"PLAIN\"\n") {
			t.Errorf("under %q the Release does not leave KEY for the Release naming its reference:\n%s", row.settings,
				release)
		}
		reported := strings.Join(append(codes(out, "credential-carried"), codes(out, "credential-emptied")...), "\n")
		for _, position := range []string{"infra.stdio_command.1 ", "infra.stdio_command.3 ", "server_env.KEY "} {
			if strings.Contains(reported, position) {
				t.Errorf("under %q a report names %s, which holds a reference alone:\n%s", row.settings, position, reported)
			}
		}
		for _, position := range []string{"infra.stdio_command.2 ", "infra.stdio_command.4 ", "server_env.PLAIN "} {
			if !strings.Contains(reported, position) {
				t.Errorf("under %q no report names %s:\n%s", row.settings, position, reported)
			}
		}
	}
}

// TestEachShapeIsReadAlike imports one fixture of each shape and requires what
// each server is drafted as in the Release.
func TestEachShapeIsReadAlike(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		fixture string
		want    []string
	}{
		{"claude-code.mcp.json", []string{
			"    api-server:\n      infra:\n        endpoint: \"${API_BASE_URL:-https://api.example.com}/mcp\"\n",
			"    # The source sends this server the header Authorization. Its value is not written",
			"    #   - the value of server_env.GITHUB_PERSONAL_ACCESS_TOKEN, which the source reads from ${GITHUB_TOKEN}\n",
			"    github:\n      infra:\n        stdio_command:\n          - \"npx\"\n          - \"-y\"\n" +
				"          - \"@modelcontextprotocol/server-github\"\n        stdio_runtime: npm\n",
		}},
		{"vscode.mcp.json", []string{
			"    # Not carried, since no infrastructure key of core/ai-tool/mcp takes it: envFile.\n",
			"    perplexity:\n      infra:\n        stdio_command:\n          - \"uvx\"\n          - \"mcp-server-perplexity\"\n" +
				"        stdio_runtime: pypi\n",
			"    docs:\n      infra:\n        endpoint: \"https://docs.example.com/mcp\"\n",
		}},
		{"vscode-commented.mcp.json", []string{
			"    fetch:\n      infra:\n        stdio_command:\n          - \"uvx\"\n          - \"mcp-server-fetch\"\n" +
				"          - \"--user-agent=https://example.com//agent\"\n        stdio_runtime: pypi\n",
		}},
		{"cursor.mcp.json", []string{
			"    # The address's query is not written",
			"    remote-search:\n      infra:\n        endpoint: \"https://search.example.com/mcp\"\n",
			"    local-files:\n      infra:\n        stdio_command:\n          - \"node\"\n" +
				"          - \"${workspaceFolder}/tools/files-server.js\"\n        stdio_runtime: npm\n",
			"    # Not carried, since no infrastructure key of core/ai-tool/mcp takes it: auth.\n    oauth-server:\n",
		}},
		{"registry-npm.server.json", []string{
			"    #   - infra.stdio_command.3, written empty, since the description leaves cache_dir for its user to supply\n",
			"    #   - the value of server_env.WEATHER_API_KEY, since the description leaves it for the user to supply\n",
			"    # The description also lists the oci package ghcr.io/example/weather-mcp:1.2.0; this instance runs " +
				"the first it lists.\n",
			"        stdio_command:\n          - \"npx\"\n          - \"@example/weather-mcp@1.2.0\"\n" +
				"          - \"--units=metric\"\n          - \"\"\n        stdio_runtime: npm\n",
			"              - key: \"WEATHER_API_KEY\"\n              - key: \"WEATHER_REGION\"\n" +
				"                value: \"eu-west\"\n",
		}},
		{"registry-remote.server.json", []string{
			"    # The source sends this server the header Authorization. Its value is not written",
			"    com-example-tickets:\n      infra:\n        endpoint: \"https://mcp.example.com/tickets\"\n",
		}},
		{"registry-oci.server.json", []string{
			"    io-github-example-notes:\n      infra:\n        image: \"ghcr.io/example/notes-mcp:0.4.1\"\n",
		}},
	} {
		t.Run(row.fixture, func(t *testing.T) {
			t.Parallel()
			out := importOne(fixture(t, row.fixture), "")
			release := fileContent(t, out, "release.yaml")
			for _, want := range row.want {
				if !strings.Contains(release, want) {
					t.Errorf("the Release does not hold %q:\n%s", want, release)
				}
			}
			for _, needle := range []string{"planted-query-needle", "planted-header-needle", "planted-client-secret",
				"planted-client-id"} {
				everything, _ := json.Marshal(out)
				if strings.Contains(string(everything), needle) {
					t.Errorf("the import's output holds %q, which nothing carries", needle)
				}
			}
		})
	}
}

// TestSeveralSourcesDraftOneAggregateInTheirOrder imports two client files as
// the named sources of one request, the first listing its servers out of name
// order and both listing a server named alike: one Aggregate and one Release
// compose every server, in the order the sources and each source write them, the
// second server named alike keyed apart, and each named by its source.
func TestSeveralSourcesDraftOneAggregateInTheirOrder(t *testing.T) {
	t.Parallel()
	first := []byte(`{"mcpServers":{"zeta":{"command":"npx"},"alpha":{"url":"https://alpha.example.com/mcp"}}}`)
	second := []byte(`{"servers":{"zeta":{"type":"stdio","command":"uvx"}}}`)
	out := Import(aggrega.ImportInput{Format: Format, Sources: []aggrega.ImportSource{
		{Name: ".mcp.json", Bytes: first}, {Name: ".vscode/mcp.json", Bytes: second},
	}}, hostParse)
	agg := fileContent(t, out, "aggregate.yaml")
	want := "  components:\n" +
		"    # The MCP server .mcp.json#zeta.\n    zeta:\n      type: core/ai-tool/mcp\n" +
		"    # The MCP server .mcp.json#alpha.\n    alpha:\n      type: core/ai-tool/mcp\n" +
		"    # The MCP server .vscode/mcp.json#zeta.\n    zeta-2:\n      type: core/ai-tool/mcp\n"
	if !strings.HasSuffix(agg, want) {
		t.Errorf("the drafted Aggregate reads\n%s\nwant it to end\n%s", agg, want)
	}
}

// TestASourceTheParserRefusesHaltsAndAnEmptyOneWarns hands Import a source the
// host's parser refuses, one listing no server, and one that is neither a
// client's configuration nor a registry description: the first is reported
// under parse-failed with the parser's reason, which halts, and the other two
// under no-nodes; nothing is drafted.
func TestASourceTheParserRefusesHaltsAndAnEmptyOneWarns(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, body, code, severity, message string
	}{
		{"truncated", `{"mcpServers": {"x": {"command": "npx",`, "parse-failed", "halt",
			`could not parse source "" as mcp: json parse: unexpected EOF`},
		{"no server", `{"mcpServers": {}}`, "no-nodes", "warn", `source "" carried no recognisable mcp nodes`},
		{"another shape", `{"nodes": [], "edges": []}`, "no-nodes", "warn", `source "" carried no recognisable mcp nodes`},
	} {
		out := importOne([]byte(row.body), "")
		want := []aggrega.Diagnostic{{Severity: row.severity, Code: row.code, Message: row.message}}
		if !reflect.DeepEqual(out.Diagnostics, want) || len(out.Files) != 0 {
			t.Errorf("%s: the import reported %+v and drafted %+v, want %+v and nothing", row.name, out.Diagnostics,
				out.Files, want)
		}
	}
}

// TestTwoImportsAreByteIdentical imports every fixture twice and requires the
// two outputs to be identical.
func TestTwoImportsAreByteIdentical(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"claude-code.mcp.json", "claude_desktop_config.json", "vscode.mcp.json",
		"vscode-commented.mcp.json", "cursor.mcp.json", "registry-npm.server.json", "registry-remote.server.json",
		"registry-oci.server.json"} {
		first, _ := json.Marshal(importOne(fixture(t, name), ""))
		second, _ := json.Marshal(importOne(fixture(t, name), ""))
		if string(first) != string(second) {
			t.Errorf("two imports of %s differ", name)
		}
	}
}

// TestStripJSONC writes what JSON with comments admits and JSON does not as
// spaces, keeping every line break and every other byte where it is.
func TestStripJSONC(t *testing.T) {
	t.Parallel()
	for _, row := range []struct{ name, in, want string }{
		{"json already", `{"a": [1, 2], "b": "c"}`, `{"a": [1, 2], "b": "c"}`},
		{"line comment", "{\n// note\n\"a\": 1\n}", "{\n       \n\"a\": 1\n}"},
		{"block comment", "{/* a\nb */\"a\": 1}", "{    \n    \"a\": 1}"},
		{"comment markers inside a string", `{"a": "x // y /* z */"}`, `{"a": "x // y /* z */"}`},
		{"escaped quote before a comment marker", `{"a": "q\" // r"}`, `{"a": "q\" // r"}`},
		{"trailing commas", "{\"a\": [1, 2,], \"b\": 3,\n}", "{\"a\": [1, 2 ], \"b\": 3 \n}"},
		{"trailing comma before a comment", "{\"a\": 1, // c\n}", "{\"a\": 1      \n}"},
		{"a comma inside a string", `{"a": "1,]"}`, `{"a": "1,]"}`},
		{"unterminated block comment", `{"a": 1} /* open`, `{"a": 1} /* open`},
	} {
		if got := string(StripJSONC([]byte(row.in))); got != row.want {
			t.Errorf("%s: StripJSONC(%q) = %q, want %q", row.name, row.in, got, row.want)
		}
	}
}

// TestEndpointOf writes an address without its user part, its query and its
// fragment, and names each part it left out.
func TestEndpointOf(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		in, want string
		dropped  []string
	}{
		{"https://mcp.example.com/mcp", "https://mcp.example.com/mcp", nil},
		{"https://user:pw@mcp.example.com/mcp", "https://mcp.example.com/mcp", []string{"user part"}},
		{"https://mcp.example.com/mcp?key=k#top", "https://mcp.example.com/mcp", []string{"query", "fragment"}},
		{"https://mcp.example.com/mcp#top", "https://mcp.example.com/mcp", []string{"fragment"}},
		{"https://mcp.example.com/a@b", "https://mcp.example.com/a@b", nil},
		{"${BASE:-https://api.example.com}/mcp", "${BASE:-https://api.example.com}/mcp", nil},
	} {
		got, dropped := endpointOf(row.in)
		if got != row.want || !reflect.DeepEqual(dropped, row.dropped) {
			t.Errorf("endpointOf(%q) = %q, %q; want %q, %q", row.in, got, dropped, row.want, row.dropped)
		}
	}
}

// TestIsReference tells a value that is one variable reference and nothing else
// from one holding a value.
func TestIsReference(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		in   string
		want bool
	}{
		{"${TOKEN}", true}, {"${env:TOKEN}", true}, {"${input:api-key}", true}, {"${workspaceFolder}", true},
		{"${TOKEN:-default}", false}, {"Bearer ${TOKEN}", false}, {"${A}${B}", false}, {"${}", false},
		{"$TOKEN", false}, {"plain", false},
	} {
		if got := isReference(row.in); got != row.want {
			t.Errorf("isReference(%q) = %v, want %v", row.in, got, row.want)
		}
	}
}

// TestYAMLStringReadsBackAsItsText writes text holding quotes, backslashes, a
// line break, a character a YAML reader could take for one and characters
// outside the Basic Multilingual Plane, and requires each written scalar to read
// back, through the YAML reader, as the text itself.
func TestYAMLStringReadsBackAsItsText(t *testing.T) {
	t.Parallel()
	for _, text := range []string{`plain`, `say "hi"`, `C:\path\`, "two\nlines", "caf\u00e9 \u0085 \u2028 \U0001F600", ""} {
		var back string
		if err := yaml.Unmarshal([]byte("value: "+yamlString(text)+"\n"), &struct {
			Value *string `yaml:"value"`
		}{&back}); err != nil || back != text {
			t.Errorf("yamlString(%q) = %s, which reads back as %q (%v)", text, yamlString(text), back, err)
		}
	}
}

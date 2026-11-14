// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcpimport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// The MCP importer on the host toolchain: each case hands Import a request and
// the parse the host would run — the compiler's own parsers for JSON and for
// JSON with comments, a refusal read as the import socket reads it — and reads
// what it drafts.

// hostParse parses source as the host's parser for syntax does and refuses it as
// the import socket hands an importer a parser's refusal. A syntax the importer
// does not read fails the call, so a request for one is caught.
func hostParse(syntax string, source []byte) (json.RawMessage, error) {
	var out abi.ParseOutput
	switch syntax {
	case Syntax:
		out = parsercore.ParseJSON(source)
	case SyntaxJSONC:
		out = parsercore.ParseJSONC(source)
	default:
		return nil, fmt.Errorf("the importer asked for the %q syntax, which it does not read", syntax)
	}
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
const desktopAggregate = `apiVersion: aggrega.ai/v1beta1
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

const desktopRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: imported/flow
  # environment is a drafted default, since no source names the environment this Release deploys to; change it to name the one it deploys to.
  environment: development
  version: 0.1.0
  description: Draft Release imported from a source in the mcp format.
spec:
  aggregate: imported/flow
  # manifest: left empty, since the import was given no target; an import given a target drafts the Manifest and names it here.
  manifest: ""
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
            # Attached by reference wherever the source names a variable: the environment brave-search is started with, one entry per variable, each written as the source writes it, so a variable reference stays one, which a target substituting variable references, as Docker Compose does when the stack starts, reads from the environment, and an empty value is one still owed.
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
// 🔴 PLANTED DEFECT: in mcpRelease (render.go), drop the call to serverEntry: the
// Release holds no component, and the case fails comparing it.
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
				for _, want := range []string{"              - key: \"BRAVE_API_KEY\"\n                value: \"\"\n    # ",
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
// so it is written as written, located by no credential report — an environment
// variable whose value is one written as that reference, its entry's value; a
// reference carrying a default, or text beside one, holds a value and is written
// empty under marked like any other.
func TestAVariableReferenceIsWrittenAsTheTextItIs(t *testing.T) {
	t.Parallel()
	body := []byte(`{"mcpServers":{"tool":{"command":"node","args":["${input:script}","--token=${TOKEN}","${TOKEN}",` +
		`"${MODE:-fast}"],"env":{"KEY":"${env:KEY}","PLAIN":"plain-value"}}}}`)
	for _, row := range []struct {
		settings, plain string
		command         []string
	}{
		{"", "plain-value", []string{"node", "${input:script}", "--token=${TOKEN}", "${TOKEN}", "${MODE:-fast}"}},
		{`{"empty_credentials":"marked"}`, "", []string{"", "${input:script}", "", "${TOKEN}", ""}},
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
		if want := "              - key: \"KEY\"\n                value: \"${env:KEY}\"\n" +
			"              - key: \"PLAIN\"\n                value: \"" + row.plain + "\"\n"; !strings.Contains(release, want) {
			t.Errorf("under %q the Release does not write KEY as the reference it reads from:\n%s\nin\n%s", row.settings,
				want, release)
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

// TestEachServerIsMappedByNameToTheRealizationOfHowItRuns imports a client's
// configuration listing a server run from its command line with an environment
// variable read from a variable reference, one reached at an address, and a
// registry description's server published as an image, for compose, for helm and
// for no target: given a target, the drafted Manifest maps each server by its key
// to that target's realization of how it runs — the bridge, the remote, the image
// — and, for helm, core/referenced-secret to helm/k8s-existing-secret; given
// none, no Manifest is drafted. The environment is attached for compose and for
// none as core/referenced-secret-set, its entry's value the reference as written,
// so the Release passes its own schema, and for helm as core/referenced-secret
// named by a Secret holding the variable under its own name, its value nowhere.
//
// 🔴 PLANTED DEFECT: in realization (render.go), return bridgeRealization for
// every server: the remote and image rows fail on the Manifest.
func TestEachServerIsMappedByNameToTheRealizationOfHowItRuns(t *testing.T) {
	t.Parallel()
	client := []byte(`{"mcpServers":{"github":{"command":"npx","args":["-y","@modelcontextprotocol/server-github"],` +
		`"env":{"GITHUB_PERSONAL_ACCESS_TOKEN":"${GITHUB_TOKEN}"}},"docs":{"type":"http",` +
		`"url":"https://mcp.example.com/docs/mcp"}}}`)
	image := fixture(t, "registry-oci.server.json")
	for _, target := range []string{"compose", "helm", ""} {
		t.Run("target "+target, func(t *testing.T) {
			t.Parallel()
			out := Import(aggrega.ImportInput{Format: Format, Bytes: client, Target: target}, hostParse)
			imageOut := Import(aggrega.ImportInput{Format: Format, Bytes: image, Target: target}, hostParse)
			release := fileContent(t, out, "release.yaml")
			var manifest, imageManifest string
			for _, f := range out.Files {
				if f.Path == "manifest.yaml" {
					manifest = f.Content
				}
			}
			for _, f := range imageOut.Files {
				if f.Path == "manifest.yaml" {
					imageManifest = f.Content
				}
			}
			if target == "" {
				if manifest != "" || imageManifest != "" {
					t.Errorf("with no target a Manifest is drafted:\n%s\n%s", manifest, imageManifest)
				}
			} else {
				for _, want := range []string{"    components:\n      name:\n        github: " + target + "/mcp-stdio-bridge\n" +
					"        docs: " + target + "/mcp-remote\n"} {
					if !strings.Contains(manifest, want) {
						t.Errorf("the Manifest does not map\n%s\nin\n%s", want, manifest)
					}
				}
				if want := "        io-github-example-notes: " + target + "/mcp\n"; !strings.Contains(imageManifest, want) {
					t.Errorf("the image server's Manifest does not map\n%s\nin\n%s", want, imageManifest)
				}
			}
			if target == "helm" {
				if !strings.Contains(release, "        server_env:\n          type: core/referenced-secret\n") ||
					!strings.Contains(release, "each named as the variable it holds: GITHUB_PERSONAL_ACCESS_TOKEN.\n"+
						"            origin: \"\"\n") || strings.Contains(release, "${GITHUB_TOKEN}") ||
					!strings.Contains(manifest, "core/referenced-secret: helm/k8s-existing-secret\n") {
					t.Errorf("for helm the environment is not named by its Secret:\n%s\n%s", release, manifest)
				}
				return
			}
			if !strings.Contains(release, "        server_env:\n          type: core/referenced-secret-set\n") ||
				!strings.Contains(release, "              - key: \"GITHUB_PERSONAL_ACCESS_TOKEN\"\n"+
					"                value: \"${GITHUB_TOKEN}\"\n") {
				t.Errorf("the environment is not attached as its entries:\n%s", release)
			}
		})
	}
}

// The header values TestAHeaderValueIsCarriedInTheCommentOrLeftOut plants: one a
// client sends a remote server, and one a registry description's remote header
// writes, unmarked.
const (
	plantedHeader         = "planted-header-value-needle-c41d"
	plantedRegistryHeader = "planted-registry-header-needle-e7a0"
)

// TestAHeaderValueIsCarriedInTheCommentOrLeftOut imports a client's remote server
// sent a literal header, a header that is one variable reference and a header
// written as no text, and a registry description's remote sent an unmarked
// header with a value, one marked secret with a default and one writing neither,
// under every value of the empty_credentials setting, after a needle proves each
// planted value is in its source:
//
//   - absent or none: each literal value is written as written in the comment
//     above its server, beside its header, and named in a credential-carried
//     warning as headers.<name> of its server, never by its value;
//   - marked or all: no literal value is in any file, entity or diagnostic, the
//     comment names each header alone, and each is reported under
//     credential-emptied as headers.<name> of its server, in the form the kit
//     gives a value of its class — a marked one as a value its server marks as a
//     credential;
//   - under every value, the reference is written as the reference it reads
//     from and reported by nothing, and a header with no value is named as such.
//
// 🔴 PLANTED DEFECT: in located (mcpimport.go), skip the headers: no report names
// a header, and under marked or all the planted values are carried, so the case
// fails on both.
func TestAHeaderValueIsCarriedInTheCommentOrLeftOut(t *testing.T) {
	t.Parallel()
	client := []byte(`{"mcpServers":{"remote":{"url":"https://mcp.example.com/mcp","headers":{` +
		`"X-Api-Key":"` + plantedHeader + `","Authorization":"${env:REMOTE_TOKEN}","X-Flags":["a"]}}}}`)
	registry := []byte(`{"$schema":"` + SchemaPrefix + `2025-12-11/server.schema.json","name":"com.example/hosted",` +
		`"remotes":[{"type":"streamable-http","url":"https://hosted.example.com/mcp","headers":[` +
		`{"name":"X-Tenant","value":"` + plantedRegistryHeader + `"},` +
		`{"name":"Authorization","isSecret":true,"default":"Bearer ` + plantedRegistryHeader + `-default"},` +
		`{"name":"X-Region","isRequired":true}]}]}`)
	for _, row := range []struct {
		name   string
		body   []byte
		key    string
		values map[string]string
		// marked names the headers the source marks as a credential, which the
		// kit reports written empty in the form it reports such a value in.
		marked map[string]bool
		fixed  []string
	}{
		{"client", client, "remote", map[string]string{"X-Api-Key": plantedHeader}, nil,
			[]string{"    #   - Authorization, its value read from ${env:REMOTE_TOKEN}\n",
				"    #   - X-Flags, with no value, since the source writes no text for it\n"}},
		{"registry", registry, "com.example/hosted", map[string]string{"X-Tenant": plantedRegistryHeader,
			"Authorization": "Bearer " + plantedRegistryHeader + "-default"}, map[string]bool{"Authorization": true},
			[]string{"    #   - X-Region, with no value, since the description leaves it for the user to supply\n"}},
	} {
		for header, value := range row.values {
			if !strings.Contains(string(row.body), value) {
				t.Fatalf("%s: the needle: %q, the value of %s, is not in the source", row.name, value, header)
			}
		}
		for _, settings := range []string{"", `{"empty_credentials":"none"}`, `{"empty_credentials":"marked"}`,
			`{"empty_credentials":"all"}`} {
			t.Run(row.name+" "+settings, func(t *testing.T) {
				t.Parallel()
				emptied := strings.Contains(settings, "marked") || strings.Contains(settings, "all")
				out := importOne(row.body, settings)
				release := fileContent(t, out, "release.yaml")
				everything, _ := json.Marshal(out)
				diagnostics, _ := json.Marshal(out.Diagnostics)
				code, other := "credential-carried", "credential-emptied"
				if emptied {
					code, other = other, code
				}
				reported := strings.Join(codes(out, code), "\n")
				for header, value := range row.values {
					line := "    #   - " + header + ": " + yamlString(value) + "\n"
					if emptied {
						line = "    #   - " + header + ", its value left out, as the empty_credentials setting asks\n"
					}
					if !strings.Contains(release, line) {
						t.Errorf("the Release does not hold %q:\n%s", line, release)
					}
					// The kit names a value it carries, and an MCP server's member it
					// writes empty, as "<field> of node <ref>", and a value its source
					// marks as a credential that it writes empty as "<field>, which node
					// <ref> marks as a credential"; each header is held to the one form
					// its class and the setting give.
					field := "the value of headers." + header + " of node " + row.key + " is carried as written "
					switch {
					case emptied && row.marked[header]:
						field = "the value of headers." + header + ", which node " + row.key +
							" marks as a credential, is written empty "
					case emptied:
						field = "the whole value of headers." + header + " of node " + row.key + " is written empty "
					}
					if !strings.Contains(reported, field) {
						t.Errorf("no %s names %q:\n%s", code, field, reported)
					}
					if strings.Contains(string(diagnostics), value) {
						t.Errorf("a diagnostic holds the planted value of %s", header)
					}
					if emptied && strings.Contains(string(everything), value) {
						t.Errorf("the import's output holds the planted value of %s", header)
					}
				}
				for _, line := range row.fixed {
					if !strings.Contains(release, line) {
						t.Errorf("the Release does not hold %q:\n%s", line, release)
					}
				}
				for _, stray := range codes(out, other) {
					if strings.Contains(stray, "headers.") {
						t.Errorf("the import reports a header under %s as well: %q", other, stray)
					}
				}
				for _, unreported := range []string{"headers.Authorization of node remote", "headers.X-Flags",
					"headers.X-Region"} {
					if strings.Contains(string(diagnostics), unreported) {
						t.Errorf("a report names %s, which holds no value to carry", unreported)
					}
				}
			})
		}
	}
}

// TestAConfigurationWithCommentsIsReadAsItsCopyWithout imports Visual Studio
// Code's configuration written as JSON with comments — a line comment, a block
// comment and trailing commas — and the same configuration with them removed,
// and requires the two outputs to be identical, byte for byte; it requires a
// configuration listing its servers under servers to be drafted in the order of
// its parsed tree, which is name order, whether or not it holds a comment; and it
// requires a configuration listing them under mcpServers, which its clients read
// as JSON, to be refused under parse-failed with the JSON parser's reason where
// it holds a comment.
//
// 🔴 PLANTED DEFECT: in sourceText (mcpimport.go), read every source as Syntax:
// the commented configuration is refused under parse-failed, and the case fails
// comparing it with its copy.
func TestAConfigurationWithCommentsIsReadAsItsCopyWithout(t *testing.T) {
	t.Parallel()
	commented := fixture(t, "vscode-commented.mcp.json")
	for _, needle := range []string{"//", "/*", ",\n    },"} {
		if !strings.Contains(string(commented), needle) {
			t.Fatalf("the needle: %q is not in the commented fixture, so it proves nothing", needle)
		}
	}
	out := importOne(commented, "")
	first, _ := json.Marshal(out)
	second, _ := json.Marshal(importOne(fixture(t, "vscode-uncommented.mcp.json"), ""))
	if string(first) != string(second) {
		t.Errorf("the commented configuration drafts\n%s\nand its copy without comments\n%s", first, second)
	}
	// Two outputs drafting nothing are alike too, so the comparison above holds
	// only where the commented configuration drafts its Release; fileContent fails
	// the case where it does not.
	fileContent(t, out, "release.yaml")

	for _, body := range []string{
		`{"servers":{"zeta":{"command":"npx","env":{"Z":"1","A":"2"}},"alpha":{"url":"https://alpha.example.com/mcp"}}}`,
		"{\"servers\":{\"zeta\":{\"command\":\"npx\",\"env\":{\"Z\":\"1\",\"A\":\"2\",},}, // a comment\n" +
			"\"alpha\":{\"url\":\"https://alpha.example.com/mcp\"}}}",
	} {
		out := importOne([]byte(body), "")
		agg := fileContent(t, out, "aggregate.yaml")
		if want := "    alpha:\n      type: core/ai-tool/mcp\n    zeta:\n"; !strings.Contains(agg, want) {
			t.Errorf("the servers of %s are not drafted in name order:\n%s", body, agg)
		}
		if release := fileContent(t, out, "release.yaml"); !strings.Contains(release,
			"              - key: \"A\"\n                value: \"2\"\n              - key: \"Z\"\n") {
			t.Errorf("the environment of %s is not drafted in name order:\n%s", body, release)
		}
	}

	out = importOne([]byte("{\"mcpServers\": {\n  // a comment\n  \"x\": {\"command\": \"npx\"}}}"), "")
	want := []aggrega.Diagnostic{{Severity: "halt", Code: "parse-failed",
		Message: `could not parse source "" as mcp: json parse: invalid character '/' looking for beginning of object key string`}}
	if !reflect.DeepEqual(out.Diagnostics, want) || len(out.Files) != 0 {
		t.Errorf("a commented mcpServers configuration reported %+v and drafted %+v, want %+v and nothing",
			out.Diagnostics, out.Files, want)
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
			"    # The headers the source sends this server, which no infrastructure key of core/ai-tool/mcp takes, " +
				"since a remote server's credential is held by each consumer, on its connection to the tool:\n" +
				"    #   - Authorization: \"Bearer ${API_KEY}\"\n",
			"              - key: \"GITHUB_PERSONAL_ACCESS_TOKEN\"\n                value: \"${GITHUB_TOKEN}\"\n",
			"    github:\n      infra:\n        stdio_command:\n          - \"npx\"\n          - \"-y\"\n" +
				"          - \"@modelcontextprotocol/server-github\"\n        stdio_runtime: npm\n",
		}},
		{"vscode.mcp.json", []string{
			"    # Not carried, since no infrastructure key of core/ai-tool/mcp takes it: envFile.\n",
			"    perplexity:\n      infra:\n        stdio_command:\n          - \"uvx\"\n          - \"mcp-server-perplexity\"\n" +
				"        stdio_runtime: pypi\n",
			"    docs:\n      infra:\n        endpoint: \"https://docs.example.com/mcp\"\n",
			"    #   - Authorization: \"Bearer ${input:docs-token}\"\n",
		}},
		{"vscode-commented.mcp.json", []string{
			"    fetch:\n      infra:\n        stdio_command:\n          - \"uvx\"\n          - \"mcp-server-fetch\"\n" +
				"          - \"--user-agent=https://example.com//agent\"\n        stdio_runtime: pypi\n",
		}},
		{"cursor.mcp.json", []string{
			"    # The address's user part, query and fragment are not written in infra.endpoint, which holds the " +
				"address without any part that carries a key; a consumer reading its key from the address supplies the " +
				"whole address on its own connection to the tool:\n" +
				"    #   - user part: \"reader:planted-userpart-needle-3e6a\"\n" +
				"    #   - query: \"api_key=planted-query-needle-5d81\"\n" +
				"    #   - fragment: \"planted-fragment-needle-c08f\"\n" +
				"    remote-search:\n",
			"    #   - x-api-key: \"planted-header-needle-b902\"\n" +
				"    #   - Authorization, its value read from ${env:SEARCH_TOKEN}\n",
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
			"              - key: \"WEATHER_API_KEY\"\n                value: \"\"\n              - key: \"WEATHER_REGION\"\n" +
				"                value: \"eu-west\"\n",
		}},
		{"registry-remote.server.json", []string{
			"    #   - Authorization, with no value, since the description leaves it for the user to supply\n",
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
			for _, needle := range []string{"planted-client-secret", "planted-client-id"} {
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
// under no-nodes; nothing is drafted. A request naming the file it was handed on
// its own has each reported under that name; one naming none, from a host that
// sends none, reports an empty name.
func TestASourceTheParserRefusesHaltsAndAnEmptyOneWarns(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, file, body, code, severity, message string
	}{
		{"truncated", "", `{"mcpServers": {"x": {"command": "npx",`, "parse-failed", "halt",
			`could not parse source "" as mcp: json parse: unexpected EOF`},
		{"no server", "", `{"mcpServers": {}}`, "no-nodes", "warn", `source "" carried no recognisable mcp nodes`},
		{"another shape", "", `{"nodes": [], "edges": []}`, "no-nodes", "warn",
			`source "" carried no recognisable mcp nodes`},
		{"truncated, named", ".mcp.json", `{"mcpServers": {"x": {"command": "npx",`, "parse-failed", "halt",
			`could not parse source ".mcp.json" as mcp: json parse: unexpected EOF`},
		{"no server, named", ".mcp.json", `{"mcpServers": {}}`, "no-nodes", "warn",
			`source ".mcp.json" carried no recognisable mcp nodes`},
	} {
		out := Import(aggrega.ImportInput{Format: Format, Bytes: []byte(row.body), Name: row.file}, hostParse)
		want := []aggrega.Diagnostic{{Severity: row.severity, Code: row.code, Message: row.message, SourceRef: row.file}}
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
		"vscode-commented.mcp.json", "vscode-uncommented.mcp.json", "cursor.mcp.json", "registry-npm.server.json",
		"registry-remote.server.json",
		"registry-oci.server.json"} {
		first, _ := json.Marshal(importOne(fixture(t, name), ""))
		second, _ := json.Marshal(importOne(fixture(t, name), ""))
		if string(first) != string(second) {
			t.Errorf("two imports of %s differ", name)
		}
	}
}

// TestRemoteEndpointWritesTheAddressAndThePartsItLeavesOut writes a remote
// server's address as infra.endpoint takes it — without its user part, its query
// and its fragment — and returns each part it leaves out, by name and as the
// address writes it, in the order the address writes them: a ? inside the
// fragment is the fragment's, and an @ past the authority is the path's.
//
// 🔴 PLANTED DEFECT: in RemoteEndpoint (servers.go), cut the query at the first ?
// before the fragment is found: the "a ? inside the fragment" row keeps "#top"
// in its endpoint and the case fails on it.
func TestRemoteEndpointWritesTheAddressAndThePartsItLeavesOut(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, in, want string
		parts          []AddressPart
	}{
		{"no part", "https://mcp.example.com/mcp", "https://mcp.example.com/mcp", nil},
		{"a user part", "https://user:pw@mcp.example.com/mcp", "https://mcp.example.com/mcp",
			[]AddressPart{{Name: "user part", Text: "user:pw"}}},
		{"a query and a fragment", "https://mcp.example.com/mcp?key=k#top", "https://mcp.example.com/mcp",
			[]AddressPart{{Name: "query", Text: "key=k"}, {Name: "fragment", Text: "top"}}},
		{"every part", "https://u:p@mcp.example.com:8443/mcp?key=k&x=1#top", "https://mcp.example.com:8443/mcp",
			[]AddressPart{{Name: "user part", Text: "u:p"}, {Name: "query", Text: "key=k&x=1"},
				{Name: "fragment", Text: "top"}}},
		{"a user part with no path", "https://u:p@mcp.example.com?key=k", "https://mcp.example.com",
			[]AddressPart{{Name: "user part", Text: "u:p"}, {Name: "query", Text: "key=k"}}},
		{"a fragment alone", "https://mcp.example.com/mcp#top", "https://mcp.example.com/mcp",
			[]AddressPart{{Name: "fragment", Text: "top"}}},
		{"a ? inside the fragment", "https://mcp.example.com/mcp#top?key=k", "https://mcp.example.com/mcp",
			[]AddressPart{{Name: "fragment", Text: "top?key=k"}}},
		{"an @ in the path", "https://mcp.example.com/a@b", "https://mcp.example.com/a@b", nil},
		{"an empty query", "https://mcp.example.com/mcp?", "https://mcp.example.com/mcp",
			[]AddressPart{{Name: "query", Text: ""}}},
		{"a reference with a default", "${BASE:-https://api.example.com}/mcp", "${BASE:-https://api.example.com}/mcp",
			nil},
	} {
		got, parts := RemoteEndpoint(row.in)
		if got != row.want || !reflect.DeepEqual(parts, row.parts) {
			t.Errorf("%s: RemoteEndpoint(%q) = %q, %+v; want %q, %+v", row.name, row.in, got, parts, row.want, row.parts)
		}
	}
}

// addressLead is the line opening the comment above a server whose address
// leaves out parts, for the parts named.
func addressLead(names, verb string) string {
	return "The address's " + names + " " + verb + " not written in infra.endpoint, which holds the address without " +
		"any part that carries a key; a consumer reading its key from the address supplies the whole address on its " +
		"own connection to the tool:"
}

// TestAddressCommentNamesEachPart writes the comment for one part, two and
// three — the verb agreeing with however many it names — and for a part carried,
// one that is wholly a variable reference and one the setting leaves out; and no
// comment for an address that leaves out nothing.
func TestAddressCommentNamesEachPart(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name  string
		parts []AddressPart
		want  []string
	}{
		{"none", nil, nil},
		{"one", []AddressPart{{Name: "query", Text: `k="v"`}},
			[]string{addressLead("query", "is"), `  - query: "k=\"v\""`}},
		{"two", []AddressPart{{Name: "user part", Text: "u:p"}, {Name: "query", Text: "${Q}", Reference: true}},
			[]string{addressLead("user part and query", "are"), `  - user part: "u:p"`,
				"  - query, its value read from ${Q}"}},
		{"three", []AddressPart{{Name: "user part", Text: "u:p", Emptied: true}, {Name: "query", Text: "k=v"},
			{Name: "fragment", Text: "top", Emptied: true}},
			[]string{addressLead("user part, query and fragment", "are"),
				"  - user part, its value left out, as the empty_credentials setting asks", `  - query: "k=v"`,
				"  - fragment, its value left out, as the empty_credentials setting asks"}},
	} {
		if got := AddressComment(row.parts); !reflect.DeepEqual(got, row.want) {
			t.Errorf("%s: AddressComment(%+v) = %q, want %q", row.name, row.parts, got, row.want)
		}
	}
}

// The key-shaped values TestTheAddressCarriesItsLeftOutPartsInTheComment plants
// in a remote server's address: one in its user part, one in its query and one
// in its fragment.
const (
	plantedUserPart = "planted-userpart-needle-5a17"
	plantedQuery    = "planted-query-needle-2c90"
	plantedFragment = "planted-fragment-needle-e4b3"
)

// TestTheAddressCarriesItsLeftOutPartsInTheComment imports a client's remote
// server whose address carries a planted user part, query and fragment, and a
// second server whose query is wholly a variable reference, under every value
// of the empty_credentials setting, after a needle proves each planted value is
// in the source:
//
//   - absent or none: infra.endpoint is the address without the three parts;
//     each planted value is in the Release on one line alone, the line naming its
//     part in the comment above its server; each part is named in a
//     credential-carried warning by its server and its name — "url of node
//     <server> is carried with its <part> as written" — and no planted value is
//     in any diagnostic, entity or other draft;
//   - marked or all: no planted value is in any file, entity or diagnostic,
//     infra.endpoint is left for the Release, the comment names each part alone,
//     and each part is reported under credential-emptied by its server and its
//     name;
//   - under every value, the query that is a reference is written as the
//     reference it reads from and reported by nothing.
//
// 🔴 PLANTED DEFECT: in located (mcpimport.go), skip the address's parts: no
// report names a part, and under marked or all the planted values are carried in
// the comment, so the case fails on both.
func TestTheAddressCarriesItsLeftOutPartsInTheComment(t *testing.T) {
	t.Parallel()
	body := []byte(`{"mcpServers":{"alpha-api":{"type":"http","url":"https://user:` + plantedUserPart +
		`@api.example.com/mcp?k=` + plantedQuery + `#` + plantedFragment + `"},` +
		`"beta-api":{"type":"http","url":"https://beta.example.com/mcp?${env:BETA_QUERY}"}}}`)
	parts := map[string]string{"user part": "user:" + plantedUserPart, "query": "k=" + plantedQuery,
		"fragment": plantedFragment}
	for _, needle := range []string{plantedUserPart, plantedQuery, plantedFragment} {
		if !strings.Contains(string(body), needle) {
			t.Fatalf("the needle: %q is not in the source, so the search finds nothing", needle)
		}
	}
	for _, settings := range []string{"", `{"empty_credentials":"none"}`, `{"empty_credentials":"marked"}`,
		`{"empty_credentials":"all"}`} {
		t.Run(settings, func(t *testing.T) {
			t.Parallel()
			emptied := strings.Contains(settings, "marked") || strings.Contains(settings, "all")
			out := importOne(body, settings)
			release := fileContent(t, out, "release.yaml")
			diagnostics, _ := json.Marshal(out.Diagnostics)
			decisions, _ := json.Marshal(out.Decisions)
			// others is everything the import wrote but the Release: every other file,
			// and every entity's body but the Release's, which is the Release itself.
			var others strings.Builder
			for _, f := range out.Files {
				if f.Path != "release.yaml" {
					others.WriteString(f.Content)
				}
			}
			for _, e := range out.Entities {
				if e.Kind != "Release" {
					others.WriteString(e.Name + e.Body + e.SourceRef)
				} else if e.Body != release {
					t.Errorf("the Release entity's body is not the drafted release.yaml")
				}
			}

			lead := "    # " + addressLead("user part, query and fragment", "are") + "\n"
			if !strings.Contains(release, lead) {
				t.Errorf("the Release does not open the comment with %q:\n%s", lead, release)
			}
			// The entry's key follows the comment directly; it holds infra.endpoint, or,
			// where the address is left for the Release, infra.endpoint written empty.
			if !strings.Contains(release, "    #   - query, its value read from ${env:BETA_QUERY}\n    beta-api:") {
				t.Errorf("the Release does not write the reference the second server's query reads from:\n%s", release)
			}
			endpoint := "    alpha-api:\n      infra:\n        endpoint: \"https://api.example.com/mcp\"\n"
			if emptied {
				endpoint = "    alpha-api:\n      infra:\n        # Left for the Release, since the source cannot supply " +
					"it: infra.endpoint\n        endpoint: \"\"\n"
			}
			if !strings.Contains(release, endpoint) {
				t.Errorf("the Release does not write %q:\n%s", endpoint, release)
			}

			code, other := "credential-carried", "credential-emptied"
			if emptied {
				code, other = other, code
			}
			reported := strings.Join(codes(out, code), "\n")
			for name, text := range parts {
				line := "    #   - " + name + ": " + yamlString(text) + "\n"
				report := "url of node alpha-api is carried with its " + name + " as written in the Release the draft " +
					"writes: the field belongs to an MCP server's configuration, which can carry a key, and setting " +
					"empty_credentials to marked or all writes its " + name + " empty"
				if emptied {
					line = "    #   - " + name + ", its value left out, as the empty_credentials setting asks\n"
					report = "url of node alpha-api is written with its " + name + " empty in the Release the draft writes"
				}
				if !strings.Contains(release, line) {
					t.Errorf("the Release does not hold %q:\n%s", line, release)
				}
				if !strings.Contains(reported, report) {
					t.Errorf("no %s says %q:\n%s", code, report, reported)
				}
			}
			for _, stray := range codes(out, other) {
				if strings.Contains(stray, "url of node") {
					t.Errorf("the import reports a part of the address under %s as well: %q", other, stray)
				}
			}
			if strings.Contains(string(diagnostics), "url of node beta-api") {
				t.Errorf("a report names the query that is a reference, which holds no value: %s", diagnostics)
			}
			for _, needle := range []string{plantedUserPart, plantedQuery, plantedFragment} {
				if strings.Contains(string(diagnostics), needle) || strings.Contains(string(decisions), needle) ||
					strings.Contains(others.String(), needle) {
					t.Errorf("the planted %q reaches a diagnostic, a decision, an entity or another draft", needle)
				}
				lines := 0
				for _, l := range strings.Split(release, "\n") {
					if strings.Contains(l, needle) {
						lines++
						if !strings.HasPrefix(l, "    #   - ") {
							t.Errorf("the planted %q is on a line other than its part's in the comment: %q", needle, l)
						}
					}
				}
				want := 1
				if emptied {
					want = 0
				}
				if lines != want {
					t.Errorf("the Release holds the planted %q on %d lines, want %d", needle, lines, want)
				}
			}
		})
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

// TestANameHoldingDoubleBracesReadsBackInTheAggregatesComments imports a client
// configuration whose server's name holds "{{", written into a comment of the
// Aggregate, and requires the Aggregate to run as a template — as the compiler
// evaluates its whole text, comments included — and the comment to read back
// with the name as the configuration writes it.
//
// 🔴 PLANTED DEFECT: in importer.CommentLine
// (aggrega-pdk/go/pkg/importer/definition.go), return the line unescaped: the
// Aggregate no longer parses as a template, and the case fails on it.
func TestANameHoldingDoubleBracesReadsBackInTheAggregatesComments(t *testing.T) {
	out := importOne([]byte(`{"mcpServers":{"srv{{x}}":{"command":"npx"}}}`), "")
	text := fileContent(t, out, "aggregate.yaml")
	if got := evaluatedAggregate(t, text); !strings.Contains(got, "# The MCP server srv{{x}}.\n") {
		t.Errorf("the evaluated Aggregate does not name the server srv{{x}}:\n%s", got)
	}
}

// evaluatedAggregate evaluates a drafted Aggregate's text as a template, as the
// compiler does before it reads the Aggregate as YAML, failing the test where it
// does not parse or run as one, and returns the evaluated text.
func evaluatedAggregate(t *testing.T, text string) string {
	t.Helper()
	tmpl, err := template.New("aggregate").Parse(text)
	if err != nil {
		t.Fatalf("the drafted Aggregate does not parse as a template: %v\n%s", err, text)
	}
	var evaluated strings.Builder
	if err := tmpl.Execute(&evaluated, nil); err != nil {
		t.Fatalf("the drafted Aggregate does not run as a template: %v\n%s", err, text)
	}
	return evaluated.String()
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proof, through the real Import over a workspace whose catalog folder
// holds the reference importers, that a value which is wholly a variable
// reference holds no value in any format: one MCP server command line — npx -y
// @example/srv ${TOKEN} --key=<a key> — written into a Langflow MCP Tools node's
// server configuration, into a Flowise Custom MCP tool's server configuration,
// and into an MCP client's configuration file, each imported with the
// empty_credentials setting absent and at marked; and that a reference carrying
// text after an operator, ${TOKEN-sk-planted} beside ${TOKEN} among them, holds
// that text in every format, under each value of the setting. The flows are
// committed real exports with the authored nodes added, since no export
// Langflow or Flowise publish carries such a node; the MCP files are authored
// here.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
)

// The values the variable-reference sources plant: a key an argument carries, a
// key a reference carries as its default, and the reference itself.
const (
	referenceArgKey     = "--key=sk-plantedREFargKEY0123456789abcd"
	referenceDefaultKey = "sk-plantedREFdefault0123456789abcde"
	variableReference   = "${TOKEN}"
)

// referenceArgs is the command line every format writes for the first server,
// after its command: the reference stands alone as its third argument.
var referenceArgs = []any{"-y", "@example/srv", variableReference, referenceArgKey}

// valuedReferenceArgs is the second server's: a reference carrying a default and
// text beside a reference, each of which holds a value.
var valuedReferenceArgs = []any{"${TOKEN:-" + referenceDefaultKey + "}", "--key=" + variableReference}

// credentialFields returns, for each credential-emptied or credential-carried
// diagnostic res raised naming node, its code and the field it names, in order.
func credentialFields(res Result, node string) []string {
	var out []string
	for _, d := range res.Diagnostics {
		if d.SourceRef != node || (d.Code != "credential-emptied" && d.Code != "credential-carried") {
			continue
		}
		field := d.Message
		if i := strings.Index(field, " of node "); i >= 0 {
			field = field[:i]
		}
		field = strings.TrimPrefix(strings.TrimPrefix(field, "the whole value of "), "the value of ")
		out = append(out, d.Code+" "+field)
	}
	return out
}

// assertNoReferenceReported requires that no diagnostic or decision res raised,
// and no comment line of any file it wrote under outputDir, holds the variable
// reference, or any text beginning one; source holds it, which is what says the
// search fires.
func assertNoReferenceReported(t *testing.T, source []byte, res Result, outputDir string) {
	t.Helper()
	if !strings.Contains(string(source), variableReference) {
		t.Fatalf("the needle: %q is not in the source, so the search finds nothing", variableReference)
	}
	for _, part := range []struct {
		what  string
		value any
	}{{"decisions", res.Decisions}, {"diagnostics", res.Diagnostics}} {
		raw, _ := json.Marshal(part.value)
		if strings.Contains(string(raw), "${TOKEN") {
			t.Errorf("the import's %s name the variable reference:\n%s", part.what, raw)
		}
	}
	for _, rel := range res.Written {
		content, err := os.ReadFile(filepath.Join(outputDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read the written %s: %v", rel, err)
		}
		for _, line := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") && strings.Contains(line, "${TOKEN") {
				t.Errorf("a comment of the written %s names the variable reference: %q", rel, line)
			}
		}
	}
}

// writtenFiles returns the bytes of every file res wrote under outputDir, by path.
func writtenFiles(t *testing.T, res Result, outputDir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, rel := range res.Written {
		content, err := os.ReadFile(filepath.Join(outputDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read the written %s: %v", rel, err)
		}
		out[rel] = string(content)
	}
	return out
}

// importedTwice imports body twice, each through a workspace of its own made by
// workspace, and requires the two imports to write the same bytes and raise the
// same diagnostics; it returns the first.
func importedTwice(t *testing.T, name string, body []byte, settings map[string]any,
	workspace func(*testing.T, map[string]any) *workspaceconfig.ResolvedWorkspace) (Result, string) {
	t.Helper()
	res, dir := writtenImport(t, workspace(t, settings), name, body)
	again, againDir := writtenImport(t, workspace(t, settings), name, body)
	if a, b := writtenFiles(t, res, dir), writtenFiles(t, again, againDir); !reflect.DeepEqual(a, b) {
		t.Errorf("two imports of %s wrote different files", name)
	}
	if !reflect.DeepEqual(res.Diagnostics, again.Diagnostics) {
		t.Errorf("two imports of %s raised different diagnostics:\n%+v\n%+v", name, res.Diagnostics, again.Diagnostics)
	}
	return res, dir
}

// TestAValueThatIsWhollyAVariableReferenceIsLeftAsWrittenInEveryFormat imports
// the one command line through each format, with empty_credentials absent and
// at marked, each twice:
//
//   - the argument that is wholly ${TOKEN} is carried, or drafted, as written
//     under both, and named in no diagnostic, no decision and no drafted
//     comment;
//   - absent, every other member of each server's configuration is carried as
//     written and named in a credential-carried warning; at marked each is
//     written empty and named in a credential-emptied warning, as today — the
//     reference carrying a default and the text beside a reference among them,
//     since each holds a value;
//   - a Flowise configuration whose only value is the reference is located not
//     at all, and is carried as written under both;
//   - the two imports of each write the same bytes and raise the same
//     diagnostics.
//
// 🔴 PLANTED DEFECT: in carryCredentials (aggrega-pdk/go/pkg/importer/credentials.go),
// replace `case references && wholeReference(tree, c):` with `case false:`, rebuilt
// into the importer fixtures: the Langflow rows fail, the reference named in a
// credential-carried warning by default and written empty under marked. And in
// serverConfigEdits (plugins/flowise-importer/walk.go), replace
// `if mcpconfig.Reference(m.Value) {` with `if false {`: the Flowise rows fail the
// same way.
func TestAValueThatIsWhollyAVariableReferenceIsLeftAsWrittenInEveryFormat(t *testing.T) {
	embeddedentities.Require(t)
	t.Parallel()
	flow := decoded(t, readRealExport(t, langflowPromptingProject))
	data := asObject(flow["data"])
	data["nodes"] = append(data["nodes"].([]any),
		mcpToolsNode("MCPTools-ref", map[string]any{"name": "ref", "config": map[string]any{"command": "npx",
			"args": referenceArgs}}),
		mcpToolsNode("MCPTools-valued", map[string]any{"name": "valued", "config": map[string]any{"command": "npx",
			"args": valuedReferenceArgs}}))
	langflowSource := encoded(t, flow)

	customMCP := func(id string, config map[string]any) map[string]any {
		text, _ := json.MarshalIndent(config, "", "    ")
		return map[string]any{"id": id, "type": "customNode", "position": map[string]any{"x": 0, "y": 0},
			"data": map[string]any{"id": id, "name": "customMCP", "label": "Custom MCP", "type": "Custom MCP Tool",
				"inputParams":  []any{map[string]any{"label": "MCP Server Config", "name": "mcpServerConfig", "type": "code"}},
				"inputAnchors": []any{}, "inputs": map[string]any{"mcpServerConfig": string(text), "mcpActions": `["search"]`}}}
	}
	chatflow := decoded(t, readRealExport(t, flowiseChatflow))
	chatflow["nodes"] = append(chatflow["nodes"].([]any),
		customMCP("customMCP_ref", map[string]any{"command": "npx", "args": referenceArgs}),
		customMCP("customMCP_valued", map[string]any{"command": "npx", "args": valuedReferenceArgs}),
		customMCP("customMCP_only", map[string]any{"args": []any{variableReference}}))
	flowiseSource := encoded(t, chatflow)

	mcpSourceFile, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{
		"ref":    map[string]any{"command": "npx", "args": referenceArgs},
		"valued": map[string]any{"command": "npx", "args": valuedReferenceArgs},
	}}, "", "  ")

	for _, setting := range []string{"absent", "marked"} {
		var settings map[string]any
		code, emptied := "credential-carried", false
		if setting == "marked" {
			settings, code, emptied = map[string]any{"empty_credentials": "marked"}, "credential-emptied", true
		}

		t.Run("a Langflow MCP server configuration, "+setting, func(t *testing.T) {
			t.Parallel()
			res, dir := importedTwice(t, "reference-flow.json", langflowSource, settings, wiringWorkspace)
			assertNoReferenceReported(t, langflowSource, res, dir)
			carried, _ := carriedFlow(t, dir, "langflow")
			config := func(id string) map[string]any {
				return asObject(asObject(asObject(langflowNode(t, carried, id)["mcp_server"])["value"])["config"])
			}
			wantRef := map[string]any{"command": "npx", "args": referenceArgs}
			wantValued := map[string]any{"command": "npx", "args": valuedReferenceArgs}
			if emptied {
				wantRef = map[string]any{"command": "", "args": []any{"", "", variableReference, ""}}
				wantValued = map[string]any{"command": "", "args": []any{"", ""}}
			}
			if got := config("MCPTools-ref"); !reflect.DeepEqual(got, wantRef) {
				t.Errorf("the reference's server is carried as %v, want %v", got, wantRef)
			}
			if got := config("MCPTools-valued"); !reflect.DeepEqual(got, wantValued) {
				t.Errorf("the second server is carried as %v, want %v", got, wantValued)
			}
			if got, want := credentialFields(res, "MCPTools-ref"), []string{code + " mcp_server.config.command",
				code + " mcp_server.config.args.0", code + " mcp_server.config.args.1",
				code + " mcp_server.config.args.3"}; !slices.Equal(got, want) {
				t.Errorf("the reference's server is reported as %q, want %q", got, want)
			}
			if got, want := credentialFields(res, "MCPTools-valued"), []string{code + " mcp_server.config.command",
				code + " mcp_server.config.args.0", code + " mcp_server.config.args.1"}; !slices.Equal(got, want) {
				t.Errorf("the second server is reported as %q, want %q", got, want)
			}
		})

		t.Run("a Flowise Custom MCP tool's configuration, "+setting, func(t *testing.T) {
			t.Parallel()
			res, dir := importedTwice(t, "reference-chatflow.json", flowiseSource, settings, wiringWorkspace)
			assertNoReferenceReported(t, flowiseSource, res, dir)
			carried, _ := carriedFlow(t, dir, "flowise")
			config := func(id string) map[string]any {
				var got map[string]any
				text, _ := asObject(flowiseNode(t, carried, id)["inputs"])["mcpServerConfig"].(string)
				if err := json.Unmarshal([]byte(text), &got); err != nil {
					t.Fatalf("the carried configuration of %s does not read as JSON: %v\n%s", id, err, text)
				}
				return got
			}
			wantRef := map[string]any{"command": "npx", "args": referenceArgs}
			wantValued := map[string]any{"command": "npx", "args": valuedReferenceArgs}
			if emptied {
				wantRef = map[string]any{"command": "", "args": []any{"", "", variableReference, ""}}
				wantValued = map[string]any{"command": "", "args": []any{"", ""}}
			}
			for id, want := range map[string]map[string]any{"customMCP_ref": wantRef, "customMCP_valued": wantValued,
				"customMCP_only": {"args": []any{variableReference}}} {
				if got := config(id); !reflect.DeepEqual(got, want) {
					t.Errorf("the configuration of %s is carried as %v, want %v", id, got, want)
				}
			}
			for _, id := range []string{"customMCP_ref", "customMCP_valued"} {
				if got := credentialFields(res, id); !slices.Equal(got, []string{code + " mcpServerConfig"}) {
					t.Errorf("the configuration of %s is reported as %q, want one %s", id, got, code)
				}
			}
			if got := credentialFields(res, "customMCP_only"); len(got) != 0 {
				t.Errorf("the configuration holding only the reference is reported as %q, want nothing", got)
			}
		})

		t.Run("an MCP client file, "+setting, func(t *testing.T) {
			t.Parallel()
			res, dir := importedTwice(t, ".mcp.json", mcpSourceFile, settings, mcpWorkspace)
			assertNoReferenceReported(t, mcpSourceFile, res, dir)
			content, err := os.ReadFile(filepath.Join(dir, "release.yaml"))
			if err != nil {
				t.Fatalf("read the drafted Release: %v", err)
			}
			var doc yaml.Node
			if err := yaml.Unmarshal(content, &doc); err != nil {
				t.Fatalf("the drafted Release does not read as YAML: %v", err)
			}
			command := func(key string) []string {
				node := yamlMember(yamlMember(yamlMember(yamlMember(yamlMember(doc.Content[0], "spec"), "components"),
					key), "infra"), "stdio_command")
				if node == nil {
					t.Fatalf("the drafted Release writes no infra.stdio_command for %s:\n%s", key, content)
				}
				var out []string
				for _, item := range node.Content {
					out = append(out, item.Value)
				}
				return out
			}
			wantRef := []string{"npx", "-y", "@example/srv", variableReference, referenceArgKey}
			wantValued := []string{"npx", valuedReferenceArgs[0].(string), valuedReferenceArgs[1].(string)}
			if emptied {
				wantRef, wantValued = []string{"", "", "", variableReference, ""}, []string{"", "", ""}
			}
			if got := command("ref"); !slices.Equal(got, wantRef) {
				t.Errorf("the reference's server is drafted with %q, want %q", got, wantRef)
			}
			if got := command("valued"); !slices.Equal(got, wantValued) {
				t.Errorf("the second server is drafted with %q, want %q", got, wantValued)
			}
			if got, want := credentialFields(res, "ref"), []string{code + " infra.stdio_command.0",
				code + " infra.stdio_command.1", code + " infra.stdio_command.2",
				code + " infra.stdio_command.4"}; !slices.Equal(got, want) {
				t.Errorf("the reference's server is reported as %q, want %q", got, want)
			}
			if got, want := credentialFields(res, "valued"), []string{code + " infra.stdio_command.0",
				code + " infra.stdio_command.1", code + " infra.stdio_command.2"}; !slices.Equal(got, want) {
				t.Errorf("the second server is reported as %q, want %q", got, want)
			}
		})
	}
}

// The text a reference carrying a value after an operator plants, and the
// arguments the operator sources write: the first server beside the bare
// reference, the second every other operator with no colon before it.
const operatorDefault = "sk-planted"

var (
	operatorArgs      = []any{"-y", "@example/srv", variableReference, "${TOKEN-" + operatorDefault + "}"}
	otherOperatorArgs = []any{"${TOKEN?" + operatorDefault + "}", "${TOKEN:+" + operatorDefault + "}",
		"${TOKEN=" + operatorDefault + "}"}
)

// assertTextNotReported requires that no diagnostic or decision res raised, and
// no comment line of any file it wrote under outputDir, holds text; source
// holds it, which is what says the search fires.
func assertTextNotReported(t *testing.T, source []byte, res Result, outputDir, text string) {
	t.Helper()
	if !strings.Contains(string(source), text) {
		t.Fatalf("the needle: %q is not in the source, so the search finds nothing", text)
	}
	for _, part := range []struct {
		what  string
		value any
	}{{"decisions", res.Decisions}, {"diagnostics", res.Diagnostics}} {
		raw, _ := json.Marshal(part.value)
		if strings.Contains(string(raw), text) {
			t.Errorf("the import's %s name %q:\n%s", part.what, text, raw)
		}
	}
	for rel, content := range writtenFiles(t, res, outputDir) {
		for _, line := range strings.Split(content, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") && strings.Contains(line, text) {
				t.Errorf("a comment of the written %s names %q: %q", rel, text, line)
			}
		}
	}
}

// TestAReferenceCarryingAValueAfterAnOperatorIsLocatedInEveryFormat imports an
// MCP server started with ${TOKEN-sk-planted} beside ${TOKEN}, and a second
// started with ${TOKEN?…}, ${TOKEN:+…} and ${TOKEN=…}, written into a Langflow
// MCP Tools node's server configuration, a Flowise Custom MCP tool's and an MCP
// client's configuration file, with empty_credentials absent, at marked and at
// all, each twice:
//
//   - absent, each reference carrying text after its operator is carried, or
//     drafted, as written and named in a credential-carried warning, as every
//     other member of the server's configuration is, while ${TOKEN} is named in
//     none;
//   - at marked and at all, each is written empty and named in a
//     credential-emptied warning, while ${TOKEN} is left as written and named in
//     none;
//   - neither the reference nor the planted text is named in a diagnostic, a
//     decision or a drafted comment;
//   - the two imports of each write the same bytes and raise the same
//     diagnostics.
//
// 🔴 PLANTED DEFECT: in IsVariableReference (aggrega-pdk/go/pkg/importer/credentials.go),
// replace `return variableName(inner)` with
// `return !strings.ContainsAny(inner, "${}\n\r") && !strings.Contains(inner, ":-")`,
// rebuilt into the importer fixtures — the grammar refusing only a colon and a
// hyphen: every row fails, each operator's argument carried as written under
// every setting and named in no warning.
func TestAReferenceCarryingAValueAfterAnOperatorIsLocatedInEveryFormat(t *testing.T) {
	embeddedentities.Require(t)
	t.Parallel()
	flow := decoded(t, readRealExport(t, langflowPromptingProject))
	data := asObject(flow["data"])
	data["nodes"] = append(data["nodes"].([]any),
		mcpToolsNode("MCPTools-operator", map[string]any{"name": "operator", "config": map[string]any{
			"command": "npx", "args": operatorArgs}}),
		mcpToolsNode("MCPTools-others", map[string]any{"name": "others", "config": map[string]any{
			"command": "npx", "args": otherOperatorArgs}}))
	langflowSource := encoded(t, flow)

	customMCP := func(id string, config map[string]any) map[string]any {
		text, _ := json.MarshalIndent(config, "", "    ")
		return map[string]any{"id": id, "type": "customNode", "position": map[string]any{"x": 0, "y": 0},
			"data": map[string]any{"id": id, "name": "customMCP", "label": "Custom MCP", "type": "Custom MCP Tool",
				"inputParams":  []any{map[string]any{"label": "MCP Server Config", "name": "mcpServerConfig", "type": "code"}},
				"inputAnchors": []any{}, "inputs": map[string]any{"mcpServerConfig": string(text), "mcpActions": `["search"]`}}}
	}
	chatflow := decoded(t, readRealExport(t, flowiseChatflow))
	chatflow["nodes"] = append(chatflow["nodes"].([]any),
		customMCP("customMCP_operator", map[string]any{"command": "npx", "args": operatorArgs}),
		customMCP("customMCP_others", map[string]any{"command": "npx", "args": otherOperatorArgs}))
	flowiseSource := encoded(t, chatflow)

	mcpSourceFile, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{
		"operator": map[string]any{"command": "npx", "args": operatorArgs},
		"others":   map[string]any{"command": "npx", "args": otherOperatorArgs},
	}}, "", "  ")

	for _, setting := range []string{"absent", "marked", "all"} {
		var settings map[string]any
		code, emptied := "credential-carried", false
		if setting != "absent" {
			settings, code, emptied = map[string]any{"empty_credentials": setting}, "credential-emptied", true
		}
		assertNothingReported := func(t *testing.T, source []byte, res Result, dir string) {
			t.Helper()
			assertTextNotReported(t, source, res, dir, "${TOKEN")
			assertTextNotReported(t, source, res, dir, operatorDefault)
		}

		t.Run("a Langflow MCP server configuration, "+setting, func(t *testing.T) {
			t.Parallel()
			res, dir := importedTwice(t, "operator-flow.json", langflowSource, settings, wiringWorkspace)
			assertNothingReported(t, langflowSource, res, dir)
			carried, _ := carriedFlow(t, dir, "langflow")
			config := func(id string) map[string]any {
				return asObject(asObject(asObject(langflowNode(t, carried, id)["mcp_server"])["value"])["config"])
			}
			wantOperator := map[string]any{"command": "npx", "args": operatorArgs}
			wantOthers := map[string]any{"command": "npx", "args": otherOperatorArgs}
			if emptied {
				wantOperator = map[string]any{"command": "", "args": []any{"", "", variableReference, ""}}
				wantOthers = map[string]any{"command": "", "args": []any{"", "", ""}}
			}
			if got := config("MCPTools-operator"); !reflect.DeepEqual(got, wantOperator) {
				t.Errorf("the first server is carried as %v, want %v", got, wantOperator)
			}
			if got := config("MCPTools-others"); !reflect.DeepEqual(got, wantOthers) {
				t.Errorf("the second server is carried as %v, want %v", got, wantOthers)
			}
			if got, want := credentialFields(res, "MCPTools-operator"), []string{code + " mcp_server.config.command",
				code + " mcp_server.config.args.0", code + " mcp_server.config.args.1",
				code + " mcp_server.config.args.3"}; !slices.Equal(got, want) {
				t.Errorf("the first server is reported as %q, want %q", got, want)
			}
			if got, want := credentialFields(res, "MCPTools-others"), []string{code + " mcp_server.config.command",
				code + " mcp_server.config.args.0", code + " mcp_server.config.args.1",
				code + " mcp_server.config.args.2"}; !slices.Equal(got, want) {
				t.Errorf("the second server is reported as %q, want %q", got, want)
			}
		})

		t.Run("a Flowise Custom MCP tool's configuration, "+setting, func(t *testing.T) {
			t.Parallel()
			res, dir := importedTwice(t, "operator-chatflow.json", flowiseSource, settings, wiringWorkspace)
			assertNothingReported(t, flowiseSource, res, dir)
			carried, _ := carriedFlow(t, dir, "flowise")
			config := func(id string) map[string]any {
				var got map[string]any
				text, _ := asObject(flowiseNode(t, carried, id)["inputs"])["mcpServerConfig"].(string)
				if err := json.Unmarshal([]byte(text), &got); err != nil {
					t.Fatalf("the carried configuration of %s does not read as JSON: %v\n%s", id, err, text)
				}
				return got
			}
			wantOperator := map[string]any{"command": "npx", "args": operatorArgs}
			wantOthers := map[string]any{"command": "npx", "args": otherOperatorArgs}
			if emptied {
				wantOperator = map[string]any{"command": "", "args": []any{"", "", variableReference, ""}}
				wantOthers = map[string]any{"command": "", "args": []any{"", "", ""}}
			}
			for id, want := range map[string]map[string]any{"customMCP_operator": wantOperator,
				"customMCP_others": wantOthers} {
				if got := config(id); !reflect.DeepEqual(got, want) {
					t.Errorf("the configuration of %s is carried as %v, want %v", id, got, want)
				}
				if got := credentialFields(res, id); !slices.Equal(got, []string{code + " mcpServerConfig"}) {
					t.Errorf("the configuration of %s is reported as %q, want one %s", id, got, code)
				}
			}
			for _, d := range res.Diagnostics {
				if (d.SourceRef == "customMCP_operator" || d.SourceRef == "customMCP_others") &&
					!strings.Contains(d.Message, "its command and arguments") {
					t.Errorf("the report of %s does not name its arguments: %q", d.SourceRef, d.Message)
				}
			}
		})

		t.Run("an MCP client file, "+setting, func(t *testing.T) {
			t.Parallel()
			res, dir := importedTwice(t, ".mcp.json", mcpSourceFile, settings, mcpWorkspace)
			assertNothingReported(t, mcpSourceFile, res, dir)
			content, err := os.ReadFile(filepath.Join(dir, "release.yaml"))
			if err != nil {
				t.Fatalf("read the drafted Release: %v", err)
			}
			var doc yaml.Node
			if err := yaml.Unmarshal(content, &doc); err != nil {
				t.Fatalf("the drafted Release does not read as YAML: %v", err)
			}
			command := func(key string) []string {
				node := yamlMember(yamlMember(yamlMember(yamlMember(yamlMember(doc.Content[0], "spec"), "components"),
					key), "infra"), "stdio_command")
				if node == nil {
					t.Fatalf("the drafted Release writes no infra.stdio_command for %s:\n%s", key, content)
				}
				var out []string
				for _, item := range node.Content {
					out = append(out, item.Value)
				}
				return out
			}
			wantOperator := []string{"npx", "-y", "@example/srv", variableReference, operatorArgs[3].(string)}
			wantOthers := []string{"npx", otherOperatorArgs[0].(string), otherOperatorArgs[1].(string),
				otherOperatorArgs[2].(string)}
			if emptied {
				wantOperator, wantOthers = []string{"", "", "", variableReference, ""}, []string{"", "", "", ""}
			}
			if got := command("operator"); !slices.Equal(got, wantOperator) {
				t.Errorf("the first server is drafted with %q, want %q", got, wantOperator)
			}
			if got := command("others"); !slices.Equal(got, wantOthers) {
				t.Errorf("the second server is drafted with %q, want %q", got, wantOthers)
			}
			if got, want := credentialFields(res, "operator"), []string{code + " infra.stdio_command.0",
				code + " infra.stdio_command.1", code + " infra.stdio_command.2",
				code + " infra.stdio_command.4"}; !slices.Equal(got, want) {
				t.Errorf("the first server is reported as %q, want %q", got, want)
			}
			if got, want := credentialFields(res, "others"), []string{code + " infra.stdio_command.0",
				code + " infra.stdio_command.1", code + " infra.stdio_command.2",
				code + " infra.stdio_command.3"}; !slices.Equal(got, want) {
				t.Errorf("the second server is reported as %q, want %q", got, want)
			}
		})
	}
}

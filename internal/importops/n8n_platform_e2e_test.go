// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

// End-to-end proofs of the reference n8n importer through the real Import, naming
// no format, over a workspace whose catalog folder holds the reference importers
// and a JSON parser beside their entities, together with the standard library's
// n8n platform and the Components the workflow's parts are referenced as, copied
// in from the aggrega-hub repository checked out beside this one:
//
//   - an exported workflow is drafted as core/ai-agent/n8n carrying it, its model,
//     MCP client and vector store wired to the platform's sockets, each reported
//     as reached other than through its connection;
//   - the values n8n marks as credentials, the parameters it marks as passwords
//     and an MCP client's address are written empty whatever the
//     empty_unmarked_credentials setting says, and a request's headers and query
//     parameters only where it is true;
//   - two imports of one workflow are byte-identical;
//   - the draft builds on helm and on compose with a Release supplying the image
//     and the model's credential, and the platform is seeded with the workflow as
//     exported, double braces included, the emptied values apart.
//
// The workflows are authored here to the shape n8n exports a workflow in (their
// README says so); none is copied from n8n.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The committed n8n workflows, relative to this package.
const (
	n8nAgentWorkflow = "testdata/n8n/agent-workflow.json"
	n8nHTTPWorkflow  = "testdata/n8n/http-workflow.json"
)

// n8nImportEntities are the hub's entities the n8n import's catalog view holds,
// by their path beneath platformHub: the platform, the Components the agent
// workflow's parts resemble, and the schema library declaring their values.
var n8nImportEntities = []string{
	"component/core/ai-agent/n8n/1.0.0/component.yaml",
	"component/core/ai-model/openai/1.0.0/component.yaml",
	"component/core/ai-model/openai-embedding/1.0.0/component.yaml",
	"component/core/ai-tool/mcp/1.0.0/component.yaml",
	"component/core/ai-tool/http-request/1.0.0/component.yaml",
	"component/core/datastore/qdrant/1.0.0/component.yaml",
	"schemalibrary/base-components/1.0.0/library.yaml",
}

// n8nWorkspace is catalogFolderImportWorkspaceAttaching with n8nImportEntities
// copied into the catalog folder at the paths the hub keeps them under.
func n8nWorkspace(t *testing.T, settings map[string]any) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, folder := catalogFolderImportWorkspaceAttaching(t, settings)
	for _, rel := range n8nImportEntities {
		body, err := os.ReadFile(filepath.Join(platformHub, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read the hub's %s: %v", rel, err)
		}
		dst := filepath.Join(folder.Dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatalf("create the directory for %s: %v", rel, err)
		}
		if err := os.WriteFile(dst, body, 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return rw
}

// readN8nWorkflow reads one committed n8n workflow.
func readN8nWorkflow(t *testing.T, rel string) []byte {
	t.Helper()
	body, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("read the committed workflow %s: %v", rel, err)
	}
	return body
}

// The values the committed workflows plant where a workflow can keep a secret.
// Each is in its workflow exactly where its comment says.
const (
	// The identifiers of the stored credentials the agent workflow's nodes select.
	plantedOpenAICredential = "plantedCredIdOpenAi01"
	plantedBearerCredential = "plantedCredIdBearer02"
	plantedQdrantCredential = "plantedCredIdQdrant03"
	// The key the agent workflow's MCP client node carries in its endpoint's query.
	plantedMCPKey = "plantedMcpKey0123"
	// The query parameter and the header the agent workflow's HTTP request tool sends.
	plantedToolQueryKey   = "plantedQueryKey0456"
	plantedToolHeaderKey  = "plantedHeaderToken0789"
	plantedHTTPQueryKey   = "plantedQueryKey1111"
	plantedHTTPHeaderKey  = "plantedHeaderToken2222"
	plantedHMACSecret     = "plantedHmacSecret3333"
	plantedJSONHeaderKey  = "plantedJsonHeaderKey4444"
	n8nMCPEndpointWritten = `"endpointUrl": "https://mcp.example.test/mcp?key=` + plantedMCPKey + `"`
)

// agentWorkflowAsCarried is the agent workflow as the draft carries it: its own
// bytes, with the identifier of every stored credential its nodes select and the
// address its MCP client node calls written empty — what n8n marks as a
// credential, and an MCP server's address, written empty whatever the settings.
func agentWorkflowAsCarried(t *testing.T) string {
	t.Helper()
	carried := string(readN8nWorkflow(t, n8nAgentWorkflow))
	for _, from := range []string{plantedOpenAICredential, plantedBearerCredential, plantedQdrantCredential} {
		if !strings.Contains(carried, `"id": "`+from+`"`) {
			t.Fatalf("the agent workflow does not select the credential %s", from)
		}
		carried = strings.ReplaceAll(carried, `"id": "`+from+`"`, `"id": ""`)
	}
	if !strings.Contains(carried, n8nMCPEndpointWritten) {
		t.Fatal("the agent workflow's MCP client node does not call the address it is authored with")
	}
	return strings.Replace(carried, n8nMCPEndpointWritten, `"endpointUrl": ""`, 1)
}

// TestAnN8nWorkflowIsDraftedAsThePlatformCarryingIt imports the agent workflow,
// naming no format and referencing every HIGH match, and reads the draft as the
// compiler reads an Aggregate:
//
//   - one instance of core/ai-agent/n8n under the key n8n, named by the
//     workflow's own id and carrying the workflow as exported, the values
//     written empty whatever the settings apart, double braces included;
//   - the chat model referenced as core/ai-model/openai, written with the model
//     its node selects, and wired to the model socket; the MCP client wired to the
//     tools socket as core/ai-tool/mcp; the Qdrant store wired to the vector_store
//     socket as core/datastore/qdrant; the embeddings kept in the workflow, since
//     the model socket draws a single connection, and the HTTP request tool kept
//     in it, since no socket takes it — each reported;
//   - each wired part reported as reached other than through its connection, none
//     as reached through it, since no n8n node reads the variables the platform
//     delivers while the draft leaves the platform's block on them;
//   - the image and the MCP server's address left for the Release.
//
// 🔴 PLANTED DEFECTS, each rebuilt into the importer fixture from its source:
//
//   - in modelOf (plugins/n8n-importer/model.go), delete the resource-locator
//     branch reading its value member: values.model is left for the author, and
//     the case fails on it;
//   - in credentialsOf (plugins/n8n-importer/credentials.go), delete the loop over
//     the node's credentials: the carried workflow holds the credential ids, and
//     the case fails comparing it.
func TestAnN8nWorkflowIsDraftedAsThePlatformCarryingIt(t *testing.T) {
	t.Parallel()
	rw := n8nWorkspace(t, referencing())
	res, dir := writtenImport(t, rw, "agent-workflow.json", readN8nWorkflow(t, n8nAgentWorkflow))
	if res.Halted() {
		t.Fatalf("the import halted: %+v", res.Diagnostics)
	}
	text := readDraftedAggregate(t, dir)
	agg := readAsTheCompilerDoes(t, text)

	platform, ok := agg.Spec.Components["n8n"]
	if !ok || platform.Type != "core/ai-agent/n8n" {
		t.Fatalf("the draft holds no core/ai-agent/n8n under n8n:\n%s", text)
	}
	if got := platform.Values["workflow_id"]; got != "Hx2pQ7rT4mN8vB1c" {
		t.Errorf("workflow_id is %#v, want the workflow's own id", got)
	}
	carried, _ := platform.Values["flow_definition"].(string)
	if want := agentWorkflowAsCarried(t); carried != want {
		t.Errorf("the draft carries a workflow of %d bytes that is not the export with its credential ids and MCP "+
			"address written empty (%d bytes)", len(carried), len(want))
	}
	if !strings.Contains(carried, "{{ $json.chatInput }}") || !strings.Contains(carried, "{{ $json.ticket }}") {
		t.Error("the carried workflow, read as the compiler reads it, lost the export's double braces")
	}

	for _, w := range []struct{ socket, key, component string }{
		{"model", "openai-chat-model", "core/ai-model/openai"},
		{"tools", "docs-mcp", "core/ai-tool/mcp"},
		{"vector_store", "knowledge-base", "core/datastore/qdrant"},
	} {
		if got := platform.Uses[w.socket][w.key].Target; got != w.key {
			t.Errorf("the %s connection %s reaches %q, want the part %s", w.socket, w.key, got, w.key)
		}
		if got := agg.Spec.Components[w.key].Type; got != w.component {
			t.Errorf("the part %s is %q, want the referenced %s", w.key, got, w.component)
		}
	}
	if got := agg.Spec.Components["openai-chat-model"].Values["model"]; got != "gpt-4o-mini" {
		t.Errorf("the model part is written with values.model %#v, want gpt-4o-mini, the model its node selects", got)
	}
	for _, kept := range []string{"embeddings-openai", "ticket-lookup", "ai-agent", "simple-memory", "sticky-note"} {
		if _, drafted := agg.Spec.Components[kept]; drafted {
			t.Errorf("the draft gives %s a Component of its own; it stays in the workflow", kept)
		}
	}

	notPlaced := diagnosticsCoded(res.Diagnostics, "part-not-placed")
	for _, node := range []string{"node Embeddings OpenAI ", "node Ticket lookup "} {
		if !slices.ContainsFunc(notPlaced, func(m string) bool { return strings.HasPrefix(m, node) }) {
			t.Errorf("no part-not-placed reports %q; got %q", node, notPlaced)
		}
	}
	if reads := diagnosticsCoded(res.Diagnostics, "part-reads-connection"); len(reads) != 0 {
		t.Errorf("a part is reported as reached through its connection: %q", reads)
	}
	literal := diagnosticsCoded(res.Diagnostics, "part-address-in-flow")
	if len(literal) != 3 {
		t.Fatalf("the import reports %d part(s) reached other than through their connection, want the three wired: %q",
			len(literal), literal)
	}
	for _, m := range literal {
		if !strings.Contains(m, "values.allow_env_access_in_nodes is false") {
			t.Errorf("the report does not say why the workflow does not read its connection: %q", m)
		}
	}

	for _, left := range []string{
		"#   - infra.image, the container image running n8n\n",
		"#   - infra.endpoint of docs-mcp, the address of the MCP server the node calls\n",
	} {
		if !strings.Contains(text, left) {
			t.Errorf("the draft does not leave %q for the Release:\n%s", strings.TrimSpace(left), text)
		}
	}
	if strings.Contains(text, "allow_env_access_in_nodes") {
		t.Error("the draft writes allow_env_access_in_nodes; it leaves the platform's default")
	}
}

// TestN8nCredentialValuesAreWrittenEmptyAsTheSettingSays imports each committed
// workflow under empty_unmarked_credentials true, false and absent, and holds the
// drafts, decisions and diagnostics to the split the setting draws:
//
//   - the stored credentials' identifiers, the MCP client's key and the Crypto
//     node's secret — what n8n marks as a credential or a password, and an MCP
//     server's address — reach nothing whatever the setting says, after a needle
//     proves each is in its source;
//   - the headers and query parameters a request is sent with reach the drafted
//     Aggregate where the setting is false or absent, and nothing where it is
//     true, each reported by its node and its name and never by its value.
//
// 🔴 PLANTED DEFECTS, each rebuilt into the importer fixture from its source:
//
//   - in credentialsOf (plugins/n8n-importer/credentials.go), replace
//     `if emptyUnmarked {` with `if true {`: the false and absent rows fail, the
//     request values written empty;
//   - in passwordParameters (plugins/n8n-importer/credentials.go), delete the
//     crypto row: every row importing the HTTP workflow fails, the Crypto node's
//     secret reaching the draft, and the agent workflow's rows pass.
func TestN8nCredentialValuesAreWrittenEmptyAsTheSettingSays(t *testing.T) {
	t.Parallel()
	always := map[string][]string{
		n8nAgentWorkflow: {plantedOpenAICredential, plantedBearerCredential, plantedQdrantCredential, plantedMCPKey},
		n8nHTTPWorkflow:  {plantedHMACSecret},
	}
	gated := map[string][]string{
		n8nAgentWorkflow: {plantedToolQueryKey, plantedToolHeaderKey},
		n8nHTTPWorkflow:  {plantedHTTPQueryKey, plantedHTTPHeaderKey, plantedJSONHeaderKey},
	}
	for _, setting := range []struct {
		name     string
		settings map[string]any
		empties  bool
	}{
		{name: "true", settings: map[string]any{"empty_unmarked_credentials": true}, empties: true},
		{name: "false", settings: map[string]any{"empty_unmarked_credentials": false}},
		{name: "absent", settings: nil},
	} {
		for _, source := range []string{n8nAgentWorkflow, n8nHTTPWorkflow} {
			t.Run(setting.name+"/"+filepath.Base(source), func(t *testing.T) {
				t.Parallel()
				body := readN8nWorkflow(t, source)
				res, dir := writtenImport(t, n8nWorkspace(t, setting.settings), filepath.Base(source), body)
				if res.Halted() {
					t.Fatalf("the import halted: %+v", res.Diagnostics)
				}
				assertNoneReaches(t, always[source], body, res, dir)
				if setting.empties {
					assertNoneReaches(t, gated[source], body, res, dir)
					emptied := strings.Join(diagnosticsCoded(res.Diagnostics, "credential-emptied"), "\n")
					for _, name := range []string{"Authorization", "api_key", "apikey"} {
						if strings.Contains(string(body), `"name": "`+name+`"`) && !strings.Contains(emptied, "."+name+" of node") {
							t.Errorf("no credential-emptied names the request value %s:\n%s", name, emptied)
						}
					}
					return
				}
				text := readDraftedAggregate(t, dir)
				for _, p := range gated[source] {
					if !strings.Contains(text, p) {
						t.Errorf("the draft does not carry %q as written, which the setting %s leaves alone", p, setting.name)
					}
				}
			})
		}
	}
}

// TestTwoImportsOfAnN8nWorkflowAreByteIdentical imports the agent workflow twice,
// each into a workspace of its own, and requires the two to draft the same files,
// byte for byte.
func TestTwoImportsOfAnN8nWorkflowAreByteIdentical(t *testing.T) {
	t.Parallel()
	body := readN8nWorkflow(t, n8nAgentWorkflow)
	first, _ := writtenImport(t, n8nWorkspace(t, referencing()), "agent-workflow.json", body)
	second, _ := writtenImport(t, n8nWorkspace(t, referencing()), "agent-workflow.json", body)
	if len(first.Files) == 0 || len(first.Files) != len(second.Files) {
		t.Fatalf("the two imports drafted %d and %d files", len(first.Files), len(second.Files))
	}
	for i := range first.Files {
		if first.Files[i].Path != second.Files[i].Path || first.Files[i].Content != second.Files[i].Content {
			t.Errorf("the two imports differ at %s", first.Files[i].Path)
		}
	}
}

// n8nBuildPaths are the catalog paths a drafted n8n platform is built against:
// the provider Plugin entities, the platform and its parts' Components, the
// Kubernetes and compose Recipe of each, the schema library, and the two secret
// Features and their realizations.
func n8nBuildPaths() []string {
	paths := []string{"../compiler/testdata/flow-definition-escape/catalog"}
	for _, rel := range []string{
		"component/core/ai-agent/n8n/1.0.0/component.yaml",
		"component/core/ai-model/openai/1.0.0/component.yaml",
		"component/core/ai-tool/mcp/1.0.0/component.yaml",
		"component/core/datastore/qdrant/1.0.0/component.yaml",
		"recipe/helm/n8n/1.0.0/recipe.yaml",
		"recipe/compose/n8n/1.0.0/recipe.yaml",
		"recipe/helm/openai/1.0.0/recipe.yaml",
		"recipe/compose/openai/1.0.0/recipe.yaml",
		"recipe/helm/mcp-remote/1.0.0/recipe.yaml",
		"recipe/compose/mcp-remote/1.0.0/recipe.yaml",
		"recipe/helm/qdrant/1.0.0/recipe.yaml",
		"recipe/compose/qdrant/1.0.0/recipe.yaml",
		"schemalibrary/base-components/1.0.0/library.yaml",
		"feature/core/referenced-secret/1.0.0/feature.yaml",
		"feature/core/secret-value/1.0.0/feature.yaml",
		"feature-recipe/helm/k8s-existing-secret/1.0.0/recipe.yaml",
		"feature-recipe/compose/dotenv-secret/1.0.0/recipe.yaml",
	} {
		paths = append(paths, platformHub+"/"+rel)
	}
	return paths
}

// writeN8nRelease writes, beside the drafted Aggregate in dir, a Manifest
// realizing every Component on target and a Release supplying only what the
// draft leaves for it: the image, the MCP server's address, and a credential on
// the model connection — a Secret the cluster holds on helm, a variable Docker
// Compose substitutes on compose. It returns the Release's path.
func writeN8nRelease(t *testing.T, dir, target string) string {
	t.Helper()
	name := "imported-n8n-" + target
	secret, realization, environment, origin := "core/referenced-secret", "helm/k8s-existing-secret", "production",
		"openai-key"
	if target == "compose" {
		secret, realization, environment, origin = "core/secret-value", "compose/dotenv-secret", "development",
			"${OPENAI_API_KEY}"
	}
	manifest := "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: " + name + "\n  version: \"1.0.0\"\n" +
		"spec:\n  mappings:\n    components:\n      type:\n" +
		"        core/ai-agent/n8n:1.0.0: " + target + "/n8n:1.0.0\n" +
		"        core/ai-model/openai:1.0.0: " + target + "/openai:1.0.0\n" +
		"        core/ai-tool/mcp:1.0.0: " + target + "/mcp-remote:1.0.0\n" +
		"        core/datastore/qdrant:1.0.0: " + target + "/qdrant:1.0.0\n" +
		"    features:\n      type:\n        " + secret + ":1.0.0: " + realization + ":1.0.0\n"
	release := "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: " + name + "\n  environment: " +
		environment + "\n  version: \"1.0.0\"\nspec:\n  aggregate: imported/flow\n  manifest: " + name + "\n" +
		"  boundaries:\n    main:\n      directory: ./dist/" + target + "\n      components:\n" +
		"        openai-chat-model: {}\n        knowledge-base: {}\n" +
		"        docs-mcp:\n          infra:\n            endpoint: https://docs-mcp.release.example/mcp\n" +
		"        n8n:\n          infra:\n            image: n8nio/n8n:1.110.1\n" +
		"          uses:\n            model:\n              openai-chat-model:\n                features:\n" +
		"                  credential:\n                    type: " + secret + "\n                    infra:\n" +
		"                      origin: " + origin + "\n"
	for file, body := range map[string]string{"manifest.yaml": manifest, "release.yaml": release} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatalf("write the %s: %v", file, err)
		}
	}
	return filepath.Join(dir, "release.yaml")
}

// n8nSeededWorkflow reads, out of what a build rendered, the workflow the n8n
// platform is seeded with: on helm the workflow.json data of the ConfigMap its
// Recipe mounts for the init step, and on compose the content of the config the
// init service mounts, as Docker Compose reads it — every $$ a $.
func n8nSeededWorkflow(t *testing.T, artifacts map[string][]byte, target string) string {
	t.Helper()
	if target == "helm" {
		var cm struct {
			Kind string            `yaml:"kind"`
			Data map[string]string `yaml:"data"`
		}
		doc := renderedEnding(t, artifacts, "/n8n/n8n-flow.configmap.yaml")
		if err := yaml.Unmarshal(doc, &cm); err != nil || cm.Kind != "ConfigMap" {
			t.Fatalf("the rendered workflow is not a ConfigMap (%v):\n%s", err, doc)
		}
		workflow, ok := cm.Data["workflow.json"]
		if !ok {
			t.Fatalf("the rendered ConfigMap carries no workflow.json:\n%s", doc)
		}
		return workflow
	}
	content, ok := renderedCompose(t, artifacts).Configs["n8n-flow"]
	if !ok || content.Content == nil {
		t.Fatal("the rendered compose file carries no config n8n-flow with inline content")
	}
	return strings.ReplaceAll(*content.Content, "$$", "$")
}

// TestAnN8nDraftBuildsAndSeedsTheWorkflowAsExported imports the agent workflow,
// referencing every HIGH match, and builds the draft on helm and on compose with
// a Release supplying only what the draft leaves for it. The build succeeds on
// both, the platform's init step is seeded with the workflow the draft carries —
// the export byte for byte, its double braces included, the values written empty
// whatever the settings apart, ending in one line feed — and the platform is
// handed the wired model's address as OPENAI_BASE_URL.
//
// 🔴 PLANTED DEFECT, rebuilt into the importer fixture from its source: in
// draftedText (aggrega-pdk/go/pkg/importer/definition.go), replace
// `literalBlock(escapeTemplateText(text), keyIndent)` with
// `literalBlock(text, keyIndent)`: both rows fail, the draft writing no double
// brace escaped — and a build of it would halt on the workflow's first one.
func TestAnN8nDraftBuildsAndSeedsTheWorkflowAsExported(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"helm", "compose"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			rw := n8nWorkspace(t, referencing())
			res, dir := writtenImport(t, rw, "agent-workflow.json", readN8nWorkflow(t, n8nAgentWorkflow))
			if res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}
			if !strings.Contains(readDraftedAggregate(t, dir), `{{"{{"}}`) {
				t.Fatal("the draft writes no double brace escaped")
			}
			result := pipelinetest.Build(t, pipelinetest.Input{
				ReleasePath:  writeN8nRelease(t, dir, target),
				CatalogPaths: n8nBuildPaths(),
			})
			seeded := n8nSeededWorkflow(t, result.Artifacts, target)
			if want := asSeeded(agentWorkflowAsCarried(t)); seeded != want {
				t.Errorf("the platform is seeded with a workflow of %d bytes that is not the %d the draft carries",
					len(seeded), len(want))
			}
			if got := platformVariable(t, result.Artifacts, "n8n", target, "OPENAI_BASE_URL"); got != openAIEndpoint {
				t.Errorf("OPENAI_BASE_URL is %q, want the wired model's address %q", got, openAIEndpoint)
			}
		})
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end coverage over flows exported by Langflow and by Flowise themselves,
// committed byte for byte under ../compiler/importersocket/testdata/real-exports
// with the attribution their repositories carry. Each is run through the real
// Import, naming no format, over a workspace whose catalog folder holds the
// reference importer of each format and a JSON parser beside their entities — the
// folder importopstest lays down, and the path every surface reaches. Nothing here
// stands in for the importers, the parser or the catalog.
//
// The cases here read how the importers draft each node, so the attachments hand
// the importers the node-by-node layout; what the default layout drafts from the
// same exports is covered beside this file.

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
)

// realExportsDir is where the committed real exports sit, relative to this
// package.
const realExportsDir = "../compiler/importersocket/testdata/real-exports"

// The committed real exports, by their path beneath realExportsDir.
const (
	langflowStarterProject = "langflow/Research Translation Loop.json"
	// langflowPromptingProject carries a language model node whose template holds
	// a max_tokens field and an api_key field.
	langflowPromptingProject = "langflow/Basic Prompting.json"
	// langflowRemixProject carries an MCP Tools node whose mcp_server field has no
	// server chosen, the one MCP Tools node the Langflow repository's own flows
	// carry.
	langflowRemixProject = "langflow/Nvidia Remix.json"
	// langflowSelectingProject is Langflow 1.6.0's Basic Prompting, whose language
	// model node selects a model, gpt-4.1, where langflowPromptingProject selects
	// none; its api_key field, marked password, names the global variable
	// OPENAI_API_KEY.
	langflowSelectingProject = "langflow/Basic Prompting 1.6.0.json"
	flowiseChatflow          = "flowise/chatflows/Multiple Documents QnA.json"
	flowiseAgentflow         = "flowise/agentflowsv2/Agents Handoff.json"
)

// realExports are the committed exports and what the reference importer drafts
// from each under the node-by-node layout, in which every node is drafted.
//
// drafts is every path the import reports, sorted. A path is reported once per
// node that drafts it, so two nodes of one type list the component they share
// once each.
var realExports = []struct {
	name   string
	source string
	format string
	drafts []string
}{
	{
		name:   "a Langflow starter project",
		source: langflowStarterProject,
		format: "langflow",
		drafts: []string{
			"aggregate.yaml",
			"components/imported-ai-model-chatinput.yaml",
			"components/imported-ai-model-chatoutput.yaml",
			"components/imported-ai-model-languagemodelcomponent.yaml",
			"components/imported-component-arxivcomponent.yaml",
			"components/imported-component-loopcomponent.yaml",
			"components/imported-component-parsercomponent.yaml",
			"release.yaml",
		},
	},
	{
		name:   "a Langflow starter project prompting a language model",
		source: langflowPromptingProject,
		format: "langflow",
		drafts: []string{
			"aggregate.yaml",
			"components/imported-ai-model-chatinput.yaml",
			"components/imported-ai-model-chatoutput.yaml",
			"components/imported-ai-model-languagemodelcomponent.yaml",
			"components/imported-component-prompt.yaml",
			"release.yaml",
		},
	},
	{
		name:   "a Langflow starter project whose language model selects a model",
		source: langflowSelectingProject,
		format: "langflow",
		drafts: []string{
			"aggregate.yaml",
			"components/imported-ai-model-chatinput.yaml",
			"components/imported-ai-model-chatoutput.yaml",
			"components/imported-ai-model-languagemodelcomponent.yaml",
			"components/imported-component-prompt.yaml",
			"release.yaml",
		},
	},
	{
		name:   "a Langflow starter project with an MCP Tools node naming no server",
		source: langflowRemixProject,
		format: "langflow",
		drafts: []string{
			"aggregate.yaml",
			"components/imported-ai-agent-agent.yaml",
			"components/imported-ai-model-chatinput.yaml",
			"components/imported-ai-model-chatoutput.yaml",
			"components/imported-ai-model-embeddingmodel.yaml",
			"components/imported-ai-tool-mcptools.yaml",
			"components/imported-component-faiss.yaml",
			"components/imported-component-prompt.yaml",
			"components/imported-component-remixdocumentation.yaml",
			"release.yaml",
		},
	},
	{
		name:   "a Flowise chatflow",
		source: flowiseChatflow,
		format: "flowise",
		drafts: []string{
			"aggregate.yaml",
			"components/imported-ai-model-chatopenai.yaml",
			"components/imported-ai-model-openaiembeddings.yaml",
			"components/imported-ai-model-openaiembeddings.yaml",
			"components/imported-ai-tool-retrievertool.yaml",
			"components/imported-ai-tool-retrievertool.yaml",
			"components/imported-ai-tool-toolagent.yaml",
			"components/imported-component-buffermemory.yaml",
			"components/imported-component-pinecone.yaml",
			"components/imported-component-pinecone.yaml",
			"release.yaml",
		},
	},
	{
		name:   "a Flowise agentflow",
		source: flowiseAgentflow,
		format: "flowise",
		drafts: []string{
			"aggregate.yaml",
			"components/imported-ai-agent-agentagentflow.yaml",
			"components/imported-ai-agent-agentagentflow.yaml",
			"components/imported-ai-agent-agentagentflow.yaml",
			"components/imported-ai-agent-conditionagentagentflow.yaml",
			"components/imported-ai-agent-startagentflow.yaml",
			"release.yaml",
		},
	},
}

// realExportPath is where the committed real export at rel sits.
func realExportPath(rel string) string {
	return filepath.Join(realExportsDir, filepath.FromSlash(rel))
}

// nodesLayoutWorkspace is catalogFolderImportWorkspace whose attachments hand
// each importer the node-by-node layout, the draft the cases in this file read.
func nodesLayoutWorkspace(t *testing.T) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, _ := catalogFolderImportWorkspaceAttaching(t, importopstest.NodesLayout())
	return rw
}

// readRealExport reads the committed real export at rel.
func readRealExport(t *testing.T, rel string) []byte {
	t.Helper()
	source, err := os.ReadFile(realExportPath(rel))
	if err != nil {
		t.Fatalf("read the committed real export: %v", err)
	}
	return source
}

// importRealExport runs Import over the committed real export at rel, naming no
// format, through the workspace rw, with the delivery and output directory given.
// The plugin bytes come from the cascade built from the directories the seam
// hands over and nothing else, as in the catalog-folder cases beside this file.
// What the importer is handed is what rw's own importer attachments hand it.
//
// The export is read where it is committed, so the answers file is named inside
// the workspace rw, which the test owns: a writing import never writes beside a
// fixture. It is one path for every import over rw, so two imports of one export
// are handed the same input and their records name the same answers file.
func importRealExport(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, rel string,
	delivery authoredcontent.Delivery, outputDir string) Result {
	t.Helper()
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    realExportPath(rel),
		SourceName:    filepath.Base(rel),
		OutputDir:     outputDir,
		Delivery:      delivery,
		Answers:       filepath.Join(rw.Root(), "answers", AnswersFileName),
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
	if err != nil {
		t.Fatalf("the import of %s was refused: %v", rel, err)
	}
	return res
}

// TestImportDraftsRealLangflowAndFlowiseExports runs each committed real export
// through Import, naming no format, and asserts four things: the format the
// export's own shape is read as; that the importer reports no diagnostic at all;
// the exact list of paths it drafts; and that a second import of the same bytes
// reports a byte-identical result, files, diagnostics and all.
//
// It fails if detection stops recognising an export as its tool writes it (the
// import is refused asking for a format, or reads it as the other one), if the
// importer halts or warns on one, if a node the export does not run is lifted or
// one it runs is lost — the path list moves — or if anything the import reports
// depends on more than the input bytes.
func TestImportDraftsRealLangflowAndFlowiseExports(t *testing.T) {
	embeddedentities.Require(t)
	t.Parallel()

	for _, ex := range realExports {
		t.Run(ex.name, func(t *testing.T) {
			t.Parallel()
			rw := nodesLayoutWorkspace(t)

			first := importRealExport(t, rw, ex.source, authoredcontent.DeliveryFor(true), "")
			if first.Format != ex.format {
				t.Errorf("the export was read as the %q format, want %q", first.Format, ex.format)
			}
			if got := diagnosticsBesideTheOwed(t, first); len(got) != 0 {
				t.Errorf("the importer reported %+v; this export reads with no diagnostic but the values its "+
					"drafts owe", got)
			}
			if got := draftPaths(first); !slices.Equal(got, ex.drafts) {
				t.Errorf("the importer drafted\n  %v\nwant\n  %v", got, ex.drafts)
			}

			second := importRealExport(t, rw, ex.source, authoredcontent.DeliveryFor(true), "")
			a, err := json.Marshal(first)
			if err != nil {
				t.Fatalf("encode the first result: %v", err)
			}
			b, err := json.Marshal(second)
			if err != nil {
				t.Fatalf("encode the second result: %v", err)
			}
			if !bytes.Equal(a, b) {
				t.Errorf("two imports of one export differ:\n first: %s\nsecond: %s", a, b)
			}
		})
	}
}

// draftedDocument is the part of a drafted document the cases below read.
type draftedDocument struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Components     map[string]any `yaml:"components"`
		FeaturesSchema map[string]any `yaml:"featuresSchema"`
		Schema         struct {
			Values map[string]any `yaml:"values"`
		} `yaml:"schema"`
	} `yaml:"spec"`
}

// valueFields returns the value fields a drafted Component declares — the keys
// of the properties block its values carry — sorted.
func (d draftedDocument) valueFields() []string {
	props, _ := d.Spec.Schema.Values["properties"].(map[string]any)
	return slices.Sorted(maps.Keys(props))
}

// credentialSlots returns the credential slots a drafted Component declares —
// the keys of its featuresSchema — sorted.
func (d draftedDocument) credentialSlots() []string {
	return slices.Sorted(maps.Keys(d.Spec.FeaturesSchema))
}

// draftedDocuments reads every drafted file a result carries but the Release, by
// metadata.name: the Release is named as the Aggregate it deploys. Two nodes of
// one type draft one document, reported once each, so a name read twice must
// carry the same content both times.
func draftedDocuments(t *testing.T, res Result) map[string]draftedDocument {
	t.Helper()
	docs := map[string]draftedDocument{}
	contents := map[string]string{}
	for _, f := range res.Files {
		if f.Path == RecordFileName {
			continue
		}
		var doc draftedDocument
		if err := yaml.Unmarshal([]byte(f.Content), &doc); err != nil {
			t.Fatalf("the drafted %s is not YAML: %v", f.Path, err)
		}
		if doc.Kind == "Release" {
			continue
		}
		if prior, seen := contents[doc.Metadata.Name]; seen && prior != f.Content {
			t.Fatalf("two drafted files are both named %s and differ", doc.Metadata.Name)
		}
		contents[doc.Metadata.Name] = f.Content
		docs[doc.Metadata.Name] = doc
	}
	return docs
}

// draftedDocumentNamed returns the drafted document whose metadata.name is name.
func draftedDocumentNamed(t *testing.T, res Result, name string) draftedDocument {
	t.Helper()
	doc, ok := draftedDocuments(t, res)[name]
	if !ok {
		t.Fatalf("no drafted document is named %s", name)
	}
	return doc
}

// TestTheReferenceImporterReadsTheShapesRealExportsCarry pins, one row per shape,
// how the reference importer reads a node shape real Langflow and Flowise exports
// carry and the hand-written fixtures do not. Each row reads one drafted document
// of a committed real export and compares a set of its keys exactly — the
// Aggregate's components, or a drafted Component's value fields — so a row fails
// naming what appeared or went missing.
//
//   - A note an author pins to the canvas — a node whose outer type is langflow's
//     noteNode or flowise's stickyNote — runs nothing, so it is not lifted as a
//     component; no edge reaches one in these exports.
//   - A langflow template carries entries that are not parameter fields: a string
//     naming the template's kind (_type), a boolean refresh flag (is_refresh), and
//     objects holding only a value the editor keeps (_frontend_node_flow_id,
//     _frontend_node_folder_id). Only an entry whose value is an object carrying a
//     type is drafted as a field.
//   - A flowise node's inputs also carry its anchors — the inputs another node
//     connects into, holding a reference to that node — which the edge already
//     records. An input named by the node's inputAnchors is not drafted as a value.
//
// 🔴 PLANTED DEFECTS, one per shape, each in the walker of the importer
// serving the row's format (plugins/langflow-importer/walk.go for Langflow,
// plugins/flowise-importer/walk.go for Flowise); the fixtures the tests run are
// rebuilt from those sources when they change.
//
//   - In liftNode, delete the three lines `if isCanvasNote(outerType) { return
//     importer.Node{}, false }`: in the Langflow walker the Langflow note row
//     fails, its Aggregate gaining note-hiyq7; in the Flowise walker the two
//     Flowise note rows fail, gaining stickynote-0, stickynote-1, stickynote-2 and
//     stickynoteagentflow-0.
//   - In the Langflow walker's templateFields, replace `err != nil || field.Type
//     == nil` with `err != nil`: the template row fails, the model component
//     gaining _frontend_node_flow_id and _frontend_node_folder_id.
//   - In the Flowise walker's liftNode, replace `subtract(orderedKeys(data["inputs"]),
//     anchorNames(data["inputAnchors"]))` with `orderedKeys(data["inputs"])`: the
//     anchor row fails, the agent component gaining inputModeration, memory, model
//     and tools.
//
// The first also fails TestImportDraftsRealLangflowAndFlowiseExports, whose path
// lists then gain each note's draft; the other two leave it passing, since they
// change what a drafted document carries rather than which documents are drafted.
func TestTheReferenceImporterReadsTheShapesRealExportsCarry(t *testing.T) {
	embeddedentities.Require(t)
	t.Parallel()

	components := func(d draftedDocument) []string { return slices.Sorted(maps.Keys(d.Spec.Components)) }
	values := draftedDocument.valueFields

	for _, tc := range []struct {
		shape    string
		source   string
		document string
		keys     func(draftedDocument) []string
		want     []string
	}{
		{
			shape:    "a langflow noteNode on the canvas is not lifted as a component",
			source:   langflowStarterProject,
			document: "imported/research-translation-loop",
			keys:     components,
			want: []string{
				"arxivcomponent-zgqwh", "chatinput-mblfd", "chatoutput-scvyg",
				"languagemodelcomponent-ps5yd", "loopcomponent-e7ur0", "parsercomponent-v2o48",
			},
		},
		{
			shape:    "a flowise stickyNote on a chatflow's canvas is not lifted as a component",
			source:   flowiseChatflow,
			document: "imported/multiple-documents-qna",
			keys:     components,
			want: []string{
				"buffermemory-0", "chatopenai-0", "openaiembeddings-0", "openaiembeddings-1", "pinecone-0",
				"pinecone-1", "retrievertool-1", "retrievertool-2", "toolagent-0",
			},
		},
		{
			shape:    "a flowise stickyNote on an agentflow's canvas is not lifted as a component",
			source:   flowiseAgentflow,
			document: "imported/agents-handoff",
			keys:     components,
			want: []string{
				"agentagentflow-0", "agentagentflow-1", "agentagentflow-2", "conditionagentagentflow-0",
				"startagentflow-0",
			},
		},
		{
			shape:    "a langflow template entry that is not a parameter field is not drafted as a value",
			source:   langflowStarterProject,
			document: "imported/ai-model/languagemodelcomponent",
			keys:     values,
			want: []string{
				"base_url_ibm_watsonx", "code", "input_value", "model", "model_name", "ollama_base_url",
				"project_id", "provider", "stream", "system_message", "temperature",
			},
		},
		{
			shape:    "a flowise input anchor is a connection and is not drafted as a value",
			source:   flowiseChatflow,
			document: "imported/ai-tool/toolagent",
			keys:     values,
			want:     []string{"maxIterations", "systemMessage"},
		},
	} {
		t.Run(tc.shape, func(t *testing.T) {
			t.Parallel()
			rw := nodesLayoutWorkspace(t)
			res := importRealExport(t, rw, tc.source, authoredcontent.DeliveryFor(true), "")
			got := tc.keys(draftedDocumentNamed(t, res, tc.document))
			if !slices.Equal(got, tc.want) {
				extra, missing := setDifference(got, tc.want)
				t.Errorf("%s from %s carries %v; unexpected %v, missing %v", tc.document, tc.source, got, extra,
					missing)
			}
		})
	}
}

// setDifference returns the elements of got absent from want, and those of want
// absent from got.
func setDifference(got, want []string) (extra, missing []string) {
	for _, k := range got {
		if !slices.Contains(want, k) {
			extra = append(extra, k)
		}
	}
	for _, k := range want {
		if !slices.Contains(got, k) {
			missing = append(missing, k)
		}
	}
	return extra, missing
}

// markedCredentials reads, from the export itself, every field its tool marks as
// a credential, by the id of the node that carries it, each node's names sorted:
// for langflow a template field whose object carries password set to true; for
// flowise an input parameter of type credential.
//
// It is read here independently of the importer on purpose: the case below
// compares what the export marks with what the importer drafted, and an oracle
// that shared the importer's reading would compare the importer with itself.
func markedCredentials(t *testing.T, format string, source []byte) map[string][]string {
	t.Helper()
	marked := map[string][]string{}
	switch format {
	case "langflow":
		var flow struct {
			Data struct {
				Nodes []struct {
					ID   string `json:"id"`
					Data struct {
						Node struct {
							Template map[string]json.RawMessage `json:"template"`
						} `json:"node"`
					} `json:"data"`
				} `json:"nodes"`
			} `json:"data"`
		}
		if err := json.Unmarshal(source, &flow); err != nil {
			t.Fatalf("read the langflow export: %v", err)
		}
		for _, n := range flow.Data.Nodes {
			for name, raw := range n.Data.Node.Template {
				var field struct {
					Password bool `json:"password"`
				}
				if json.Unmarshal(raw, &field) == nil && field.Password {
					marked[n.ID] = append(marked[n.ID], name)
				}
			}
		}
	case "flowise":
		var flow struct {
			Nodes []struct {
				ID   string `json:"id"`
				Data struct {
					InputParams []struct {
						Name string `json:"name"`
						Type string `json:"type"`
					} `json:"inputParams"`
				} `json:"data"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal(source, &flow); err != nil {
			t.Fatalf("read the flowise export: %v", err)
		}
		for _, n := range flow.Nodes {
			for _, p := range n.Data.InputParams {
				if p.Type == "credential" {
					marked[n.ID] = append(marked[n.ID], p.Name)
				}
			}
		}
	default:
		t.Fatalf("no credential marker is read for the %q format", format)
	}
	for id := range marked {
		slices.Sort(marked[id])
	}
	return marked
}

// draftedTargets imports the export at rel with the importer's decision record
// rendered in place of the drafts — the settings an importer attachment naming
// the importer hands it to have one — and returns the Component each source node
// was drafted as, by the node's id in the export.
func draftedTargets(t *testing.T, rel string) map[string]string {
	t.Helper()
	rw, _ := catalogFolderImportWorkspaceAttaching(t, map[string]any{"render_mode": "llm", "layout": "nodes"})
	res := importRealExport(t, rw, rel, authoredcontent.DeliveryFor(true), "")
	if !slices.Equal(draftPaths(res), []string{"import-decisions.json"}) || res.Files[0].Path != "import-decisions.json" {
		t.Fatalf("the decision record was not the one file the importer returned: %+v", draftPaths(res))
	}
	var decisions []abi.ImportDecision
	if err := json.Unmarshal([]byte(res.Files[0].Content), &decisions); err != nil {
		t.Fatalf("read the decision record: %v", err)
	}
	targets := map[string]string{}
	for _, d := range decisions {
		if d.Chosen != nil && d.Chosen.Mode == "draft" {
			targets[d.SourceNode.ID] = d.Chosen.Target
		}
	}
	return targets
}

// TestTheReferenceImporterDraftsACredentialFromTheSourceFormatsOwnMarker pins,
// one row per format, that a field is drafted as a credential slot exactly when
// the export's own tool marks it as one — Langflow's password: true on a template
// field, Flowise's input parameter of type credential — and that a field the tool
// does not mark is drafted as a value whatever its name.
//
// Each row lists every field the export marks, by the node carrying it, and the
// case first checks that list against the export itself, so it cannot leave one
// out. Then, for every source node the import drafted, the drafted Component's
// credential slots are exactly the fields the export marks on that node — none
// for a node it marks nothing on — and none of those fields is also a value field.
// A row naming a token-count field, which reads like a credential and is not one,
// checks that it is drafted as a value and not as a slot.
//
// 🔴 PLANTED DEFECT — the field name restored as the classifier, in the node-list
// reader both importers' walkers share (aggrega-pdk/go/pkg/importer/tree.go).
// Add
//
//	func isSecretName(name string) bool {
//		for _, needle := range []string{"api_key", "apikey", "api-key", "token", "password", "secret", "credential"} {
//			if containsFold(name, needle) {
//				return true
//			}
//		}
//		return false
//	}
//
// and in NodeList, immediately before the line that appends the lifted node,
// re-split the node's parameters by name:
//
//	all := append(append([]string{}, node.Fields...), node.Secrets...)
//	node.Fields, node.Secrets = nil, nil
//	for _, f := range all {
//		if isSecretName(f) {
//			node.Secrets = append(node.Secrets, f)
//		} else {
//			node.Fields = append(node.Fields, f)
//		}
//	}
//
// The Langflow max_tokens row then fails with the language model's slots reading
// [api_key max_tokens] and its max_tokens no longer a value; the Flowise row fails
// with the chat model's slots reading [credential maxTokens] and its maxTokens no
// longer a value.
func TestTheReferenceImporterDraftsACredentialFromTheSourceFormatsOwnMarker(t *testing.T) {
	embeddedentities.Require(t)
	t.Parallel()

	type valueField struct{ document, field string }
	for _, tc := range []struct {
		name        string
		source      string
		format      string
		credentials map[string][]string
		tokenCount  *valueField
	}{
		{
			name:   "langflow marks a credential with password true, and max_tokens carries no mark",
			source: langflowPromptingProject,
			format: "langflow",
			credentials: map[string][]string{
				"LanguageModelComponent-FLeYF": {"api_key"},
			},
			tokenCount: &valueField{"imported/ai-model/languagemodelcomponent", "max_tokens"},
		},
		{
			name:   "flowise marks a credential with an input parameter of type credential, and maxTokens carries no mark",
			source: flowiseChatflow,
			format: "flowise",
			credentials: map[string][]string{
				"chatOpenAI_0":       {"credential"},
				"openAIEmbeddings_0": {"credential"},
				"openAIEmbeddings_1": {"credential"},
				"pinecone_0":         {"credential"},
				"pinecone_1":         {"credential"},
			},
			tokenCount: &valueField{"imported/ai-model/chatopenai", "maxTokens"},
		},
		{
			name:   "a langflow starter project marking only its model's api_key",
			source: langflowStarterProject,
			format: "langflow",
			credentials: map[string][]string{
				"LanguageModelComponent-ps5yd": {"api_key"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if marked := markedCredentials(t, tc.format, readRealExport(t, tc.source)); !maps.EqualFunc(marked,
				tc.credentials, slices.Equal[[]string]) {
				t.Fatalf("the export marks %v as credentials; the row lists %v", marked, tc.credentials)
			}

			targets := draftedTargets(t, tc.source)
			for id := range tc.credentials {
				if _, drafted := targets[id]; !drafted {
					t.Fatalf("the node %s the export marks a credential on was not drafted; drafted %v", id, targets)
				}
			}

			rw := nodesLayoutWorkspace(t)
			docs := draftedDocuments(t, importRealExport(t, rw, tc.source, authoredcontent.DeliveryFor(true), ""))
			for _, id := range slices.Sorted(maps.Keys(targets)) {
				doc, ok := docs[targets[id]]
				if !ok {
					t.Fatalf("the node %s was decided into %s, which no drafted file is named", id, targets[id])
				}
				want := tc.credentials[id]
				if got := doc.credentialSlots(); !slices.Equal(got, want) {
					t.Errorf("%s, drafted from the node %s, declares the credential slots %v; the export marks %v",
						targets[id], id, got, want)
				}
				for _, field := range want {
					if slices.Contains(doc.valueFields(), field) {
						t.Errorf("%s, drafted from the node %s, declares the marked credential %s as a value as well",
							targets[id], id, field)
					}
				}
			}

			if tc.tokenCount != nil {
				doc, ok := docs[tc.tokenCount.document]
				if !ok {
					t.Fatalf("no drafted document is named %s", tc.tokenCount.document)
				}
				if !slices.Contains(doc.valueFields(), tc.tokenCount.field) {
					t.Errorf("%s does not declare %s as a value; its value fields are %v", tc.tokenCount.document,
						tc.tokenCount.field, doc.valueFields())
				}
				if slices.Contains(doc.credentialSlots(), tc.tokenCount.field) {
					t.Errorf("%s declares %s, which the export does not mark, as a credential slot",
						tc.tokenCount.document, tc.tokenCount.field)
				}
			}
		})
	}
}

// TestEveryDocumentDraftedFromARealExportValidates writes what each committed real
// export drafts into the catalog folder of a second workspace and validates it as
// `aggrega validate` does: each drafted Component through the single-entity check
// the command runs on an entity path, and the whole catalog — which is where an
// Aggregate is checked — through the check the command runs with no argument, over
// the directories the command reads in a workspace naming no Release.
//
// Before a drafted Component is validated, the shape of its values is read: a
// closed object whose fields sit under an explicit properties block — type object,
// properties, additionalProperties false, and nothing else. Under properties every
// key is a field name, so a source field named like a schema keyword (the Langflow
// parser's pattern, the Flowise retriever tool's description) compiles like any
// other; the same fields written directly under values are read as keywords and
// refused.
//
// It fails if a drafted Component's values take another shape, if one stops
// passing the schema, placeholder or pillar checks, or if the drafted Aggregate —
// or anything else in the catalog — stops passing the whole-catalog check.
//
// 🔴 PLANTED DEFECT — the fields written directly under values, in the emitter
// both importers share (aggrega-pdk/go/pkg/importer/emit.go,
// draftComponentBody): delete the lines writing `type: object`, `properties:` and
// `additionalProperties: false`, and indent each field two spaces less. Every
// drafted Component carrying values then fails the shape check, and the Langflow
// parser and the Flowise retriever tool fail validation as well, with the compact
// form's unknown-keyword refusal.
func TestEveryDocumentDraftedFromARealExportValidates(t *testing.T) {
	embeddedentities.Require(t)
	t.Parallel()

	for _, ex := range realExports {
		t.Run(ex.name, func(t *testing.T) {
			t.Parallel()
			rw := nodesLayoutWorkspace(t)
			target := aWorkspace(t)
			catalogDir := filepath.Join(target.Root(), workspaceconfig.ConventionalCatalogDir)

			res := importRealExport(t, rw, ex.source, authoredcontent.DeliveryFor(false), catalogDir)
			written := slices.Compact(slices.Sorted(slices.Values(res.Written)))
			if len(written) == 0 {
				t.Fatal("the import wrote nothing to validate")
			}

			// The stack is read only now, once the drafts are on disk: the catalog
			// folder joins it only when it exists.
			passes, err := target.WholeCatalogPasses(nil, nil)
			if err != nil {
				t.Fatalf("the whole-catalog passes were refused: %v", err)
			}
			pass := passes[0]
			cacheDir := target.Config().ResolvedArtifactCacheDir()

			for _, path := range written {
				t.Run(path, func(t *testing.T) {
					file := filepath.Join(catalogDir, filepath.FromSlash(path))
					data, err := os.ReadFile(file)
					if err != nil {
						t.Fatalf("read the drafted document: %v", err)
					}
					var doc draftedDocument
					if err := yaml.Unmarshal(data, &doc); err != nil {
						t.Fatalf("the drafted document is not YAML: %v", err)
					}
					if doc.Kind != "Component" {
						return
					}
					if values := doc.Spec.Schema.Values; values != nil {
						keys := slices.Sorted(maps.Keys(values))
						if !slices.Equal(keys, []string{"additionalProperties", "properties", "type"}) ||
							values["type"] != "object" || values["additionalProperties"] != false {
							t.Errorf("the drafted values are not a closed object with its fields under "+
								"properties: they carry %v\n%s", values, data)
						}
					}
					vr, err := validateops.ValidateEntityContent(context.Background(), target.Root(), cacheDir,
						pass.Dirs, pass.Named, file, data, false)
					if err != nil {
						t.Fatalf("the drafted Component did not validate: %v\n%s", err, data)
					}
					if !vr.Valid {
						t.Errorf("the drafted Component did not validate: %v\n%s", vr.Diagnostics, data)
					}
				})
			}

			t.Run("the whole catalog, the drafted Aggregate included", func(t *testing.T) {
				cr, err := validateops.ValidateCatalog(t.Context(), target.Root(), cacheDir, pass.Dirs, pass.Named, nil)
				if err != nil {
					t.Fatalf("the catalog holding the drafts did not load: %v", err)
				}
				if !cr.Valid {
					t.Errorf("the catalog holding the drafts did not validate: extends %v, location %v, "+
						"input kinds %v", cr.ExtendsErrs, cr.OCICollision, cr.InputKindErrs)
				}
			})
		})
	}
}

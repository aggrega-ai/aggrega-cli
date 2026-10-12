// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

// End-to-end coverage over flows exported by Langflow and by Flowise themselves,
// committed byte for byte under ../compiler/importersocket/testdata/real-exports
// with the attribution their repositories carry. Each is run through the real
// Import, naming no format, over a workspace whose catalog folder holds the
// reference importer and a JSON parser beside their entities — the folder
// importopstest lays down, and the path every surface reaches. Nothing here stands
// in for the importer, the parser or the catalog.

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// realExportsDir is where the committed real exports sit, relative to this
// package.
const realExportsDir = "../compiler/importersocket/testdata/real-exports"

// The committed real exports, by their path beneath realExportsDir.
const (
	langflowStarterProject = "langflow/Research Translation Loop.json"
	flowiseChatflow        = "flowise/chatflows/Multiple Documents QnA.json"
	flowiseAgentflow       = "flowise/agentflowsv2/Agents Handoff.json"
)

// realExports are the committed exports and what the reference importer drafts
// from each under its default posture, in which every node is drafted.
//
// drafts is every path the import reports, sorted. A path is reported once per
// node that drafts it, so two nodes of one type list the component they share
// once each.
//
// held names a drafted document whose validation this suite skips, with the
// reason; see TestEveryDocumentDraftedFromARealExportValidates.
var realExports = []struct {
	name   string
	source string
	format string
	drafts []string
	held   map[string]string
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
		},
		held: map[string]string{
			"components/imported-component-parsercomponent.yaml": "held: the parser node's source field pattern " +
				"is drafted as a value named pattern, which the compact form reads as a constraint keyword, so " +
				"the drafted values do not compile",
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
		},
		held: map[string]string{
			"components/imported-ai-tool-retrievertool.yaml": "held: the retriever tool node's source field " +
				"description is drafted as a value named description, which the compact form reads as a " +
				"keyword, so the drafted values do not compile",
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
		},
	},
}

// importRealExport runs Import over the committed real export at rel, naming no
// format, through the workspace rw, with the delivery and output directory given.
// The plugin bytes come from the cascade built from the directories the seam
// hands over and nothing else, as in the catalog-folder cases beside this file.
func importRealExport(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, rel string,
	delivery authoredcontent.Delivery, outputDir string) Result {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(realExportsDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read the committed real export: %v", err)
	}
	res, err := Import(context.Background(), rw, Options{
		Source:        source,
		SourceName:    filepath.Base(rel),
		OutputDir:     outputDir,
		Delivery:      delivery,
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
	t.Parallel()

	for _, ex := range realExports {
		t.Run(ex.name, func(t *testing.T) {
			t.Parallel()
			rw, _ := catalogFolderImportWorkspace(t)

			first := importRealExport(t, rw, ex.source, authoredcontent.DeliveryFor(true), "")
			if first.Format != ex.format {
				t.Errorf("the export was read as the %q format, want %q", first.Format, ex.format)
			}
			if len(first.Diagnostics) != 0 {
				t.Errorf("the importer reported %+v; this export reads with no diagnostic", first.Diagnostics)
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

// draftedDocument is the part of a drafted document the shape cases read.
type draftedDocument struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Components map[string]any `yaml:"components"`
		Schema     struct {
			Values map[string]any `yaml:"values"`
		} `yaml:"schema"`
	} `yaml:"spec"`
}

// draftedDocumentNamed returns the drafted document whose metadata.name is name.
func draftedDocumentNamed(t *testing.T, res Result, name string) draftedDocument {
	t.Helper()
	for _, f := range res.Files {
		var doc draftedDocument
		if err := yaml.Unmarshal([]byte(f.Content), &doc); err != nil {
			t.Fatalf("the drafted %s is not YAML: %v", f.Path, err)
		}
		if doc.Metadata.Name == name {
			return doc
		}
	}
	t.Fatalf("no drafted document is named %s", name)
	return draftedDocument{}
}

// TestTheReferenceImporterReadsTheShapesRealExportsCarry pins, one row per shape,
// how the reference importer reads a node shape real Langflow and Flowise exports
// carry and the hand-written fixtures do not. Each row reads one drafted document
// of a committed real export and compares a set of its keys exactly — the
// Aggregate's components, or a drafted Component's values — so a row fails naming
// what appeared or went missing.
//
//   - A note an author pins to the canvas — a node whose outer type is langflow's
//     noteNode or flowise's stickyNote — runs nothing, so it is not lifted as a
//     component; no edge reaches one in these exports.
//   - A langflow template carries entries that are not parameter fields: a string
//     naming the template's kind (_type), a boolean refresh flag (is_refresh), and
//     objects holding only a value the editor keeps (_frontend_node_flow_id,
//     _frontend_node_folder_id). Only an entry whose value is an object carrying a
//     type is drafted as a value.
//   - A flowise node's inputs also carry its anchors — the inputs another node
//     connects into, holding a reference to that node — which the edge already
//     records. An input named by the node's inputAnchors is not drafted as a value.
//
// 🔴 PLANTED DEFECTS, one per shape, each in the importer example's walker
// (aggrega-pdk/go/examples/import-langflow/walk.go, parseNodes); the fixture the
// tests run is rebuilt from that source when it changes.
//
//   - Delete the three lines `if isCanvasNote(format, outer) { continue }`: the three
//     note rows fail, each Aggregate gaining its notes (note-hiyq7; stickynote-0,
//     stickynote-1, stickynote-2; stickynoteagentflow-0).
//   - Replace `templateFields(nestedTemplate(data["node"]))` with
//     `orderedKeys(nestedTemplate(data["node"]))`: the template row fails, the model
//     component gaining _frontend_node_flow_id, _frontend_node_folder_id, _type and
//     is_refresh.
//   - Replace `subtract(orderedKeys(data["inputs"]), anchorNames(data["inputAnchors"]))`
//     with `orderedKeys(data["inputs"])`: the anchor row fails, the agent component
//     gaining inputModeration, memory, model and tools.
//
// The first also fails TestImportDraftsRealLangflowAndFlowiseExports, whose path
// lists then gain each note's draft; the other two leave it passing, since they
// change what a drafted document carries rather than which documents are drafted.
func TestTheReferenceImporterReadsTheShapesRealExportsCarry(t *testing.T) {
	t.Parallel()

	components := func(d draftedDocument) []string { return slices.Sorted(maps.Keys(d.Spec.Components)) }
	values := func(d draftedDocument) []string { return slices.Sorted(maps.Keys(d.Spec.Schema.Values)) }

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
			document: "imported/flow",
			keys:     components,
			want: []string{
				"arxivcomponent-zgqwh", "chatinput-mblfd", "chatoutput-scvyg",
				"languagemodelcomponent-ps5yd", "loopcomponent-e7ur0", "parsercomponent-v2o48",
			},
		},
		{
			shape:    "a flowise stickyNote on a chatflow's canvas is not lifted as a component",
			source:   flowiseChatflow,
			document: "imported/flow",
			keys:     components,
			want: []string{
				"buffermemory-0", "chatopenai-0", "openaiembeddings-0", "openaiembeddings-1", "pinecone-0",
				"pinecone-1", "retrievertool-1", "retrievertool-2", "toolagent-0",
			},
		},
		{
			shape:    "a flowise stickyNote on an agentflow's canvas is not lifted as a component",
			source:   flowiseAgentflow,
			document: "imported/flow",
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
			rw, _ := catalogFolderImportWorkspace(t)
			res := importRealExport(t, rw, tc.source, authoredcontent.DeliveryFor(true), "")
			got := tc.keys(draftedDocumentNamed(t, res, tc.document))
			if !slices.Equal(got, tc.want) {
				var extra, missing []string
				for _, k := range got {
					if !slices.Contains(tc.want, k) {
						extra = append(extra, k)
					}
				}
				for _, k := range tc.want {
					if !slices.Contains(got, k) {
						missing = append(missing, k)
					}
				}
				t.Errorf("%s from %s carries %v; unexpected %v, missing %v", tc.document, tc.source, got, extra,
					missing)
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
// A drafted document named in its export's held map is skipped with the reason and
// left out of the catalog the whole-catalog check reads: its values carry a source
// field whose name the compact form reads as a keyword, so they do not compile.
// Removing an entry makes the document checked like every other one, and it then
// fails with the compact form's unknown-keyword refusal for as long as the draft
// writes its values in that form.
//
// It fails if a drafted Component stops passing the schema, placeholder or pillar
// checks, or if the drafted Aggregate — or anything else in the catalog — stops
// passing the whole-catalog check.
func TestEveryDocumentDraftedFromARealExportValidates(t *testing.T) {
	t.Parallel()

	for _, ex := range realExports {
		t.Run(ex.name, func(t *testing.T) {
			t.Parallel()
			rw, _ := catalogFolderImportWorkspace(t)
			target := aWorkspace(t)
			catalogDir := filepath.Join(target.Root(), workspaceconfig.ConventionalCatalogDir)

			res := importRealExport(t, rw, ex.source, authoredcontent.DeliveryFor(false), catalogDir)
			written := slices.Compact(slices.Sorted(slices.Values(res.Written)))
			if len(written) == 0 {
				t.Fatal("the import wrote nothing to validate")
			}
			for path := range ex.held {
				if !slices.Contains(written, path) {
					t.Fatalf("%s is held but was not drafted; the held list names a document this export no "+
						"longer drafts", path)
				}
				if err := os.Remove(filepath.Join(catalogDir, filepath.FromSlash(path))); err != nil {
					t.Fatalf("leave the held %s out of the catalog: %v", path, err)
				}
			}

			// The stack is read only now, once the drafts are on disk: the catalog
			// folder joins it only when it exists.
			pass := target.WholeCatalogPasses(nil, nil)[0]
			cacheDir := target.Config().ResolvedArtifactCacheDir()

			for _, path := range written {
				t.Run(path, func(t *testing.T) {
					if why, held := ex.held[path]; held {
						t.Skip(why)
					}
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
				cr, err := validateops.ValidateCatalog(target.Root(), cacheDir, pass.Dirs, pass.Named)
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

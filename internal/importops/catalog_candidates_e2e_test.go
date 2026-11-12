// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end coverage of what the reference importer scores a node against: the
// Components of the workspace's own catalog, as the host projects them onto the
// import input. Each case runs the real Import over a workspace whose catalog
// folder holds the reference importers and a JSON parser — the folder importopstest
// lays down — together with the Components the case writes beside them, and reads
// the importer's decision record: the configuration attaches both importers with
// render_mode llm, so the record is the one file the importer returns, and with the
// node-by-node layout, so every node is decided as a Component of its own.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// langflowFlow is the committed Langflow fixture the cases import: an OpenAI model
// node, a retrieval tool node and a custom Python node.
const langflowFlow = "../compiler/importersocket/testdata/langflow-flow.json"

// catalogComponent is one Component document a case writes into the workspace's
// catalog folder, at the per-version path a catalog lays a Component out at.
type catalogComponent struct {
	name, version, body string
}

// enterpriseModel is a model Component no importer carries: authored in the
// workspace's own catalog under its own namespace, carrying the labels a model
// Component carries and value keys a language model node shares one of.
var enterpriseModel = catalogComponent{
	name:    "acme/ai-model/internal-llm",
	version: "1.2.0",
	body: `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/ai-model/internal-llm
  version: 1.2.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: ai-model
    model.aggrega.ai/capability: generative
    model.aggrega.ai/api: openai
    component.aggrega.ai/hosting: self-hosted
spec:
  schema:
    values:
      model: { type: string }
      temperature: { type: number }
      max_tokens: { type: integer }
    provides:
      endpoint: { type: string }
      model: { type: string }
`,
}

// enterpriseStore is a second Component of the same catalog, sorted after the
// model by name, which the model node resembles on no signal: another category,
// no label at a key the node implies a trait for, no value key it shares, and a
// name sharing no letter with the node's vendor token.
var enterpriseStore = catalogComponent{
	name:    "acme/datastore/kv",
	version: "0.3.0",
	body: `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/datastore/kv
  version: 0.3.0
  labels:
    component.aggrega.ai/type: resource
    component.aggrega.ai/category: datastore
spec:
  schema:
    values:
      ttl: { type: integer }
      size: { type: string }
`,
}

// writeCatalogComponents writes each Component into the catalog directory dir.
func writeCatalogComponents(t *testing.T, dir string, components ...catalogComponent) {
	t.Helper()
	for _, c := range components {
		path := filepath.Join(dir, "component", filepath.FromSlash(c.name), c.version, "component.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create the directory for %s: %v", c.name, err)
		}
		if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
			t.Fatalf("write %s: %v", c.name, err)
		}
	}
}

// importDecisionRecord runs Import over the committed Langflow fixture through the
// workspace rw, naming no format, and returns the decision record the importer
// rendered as its one file.
func importDecisionRecord(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) []abi.ImportDecision {
	t.Helper()
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    langflowFlow,
		SourceName:    filepath.Base(langflowFlow),
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
	if err != nil {
		t.Fatalf("the import was refused: %v", err)
	}
	if !slices.Equal(draftPaths(res), []string{"import-decisions.json"}) || res.Files[0].Path != "import-decisions.json" {
		t.Fatalf("the decision record was not the one file the importer returned: %v", draftPaths(res))
	}
	var decisions []abi.ImportDecision
	if err := json.Unmarshal([]byte(res.Files[0].Content), &decisions); err != nil {
		t.Fatalf("read the decision record: %v", err)
	}
	if len(decisions) == 0 {
		t.Fatal("the decision record holds no decision")
	}
	return decisions
}

// TestImportScoresTheWorkspaceCatalogsOwnComponents imports one Langflow flow under
// auto-apply three ways and reads the decision for its OpenAI model node:
//
//   - against a catalog holding an enterprise model Component no importer carries,
//     the importer scores that Component as a candidate from what the catalog view
//     says of it alone — the category its label carries, its capability and
//     hosting labels against the traits the node's mapping implies, its value keys
//     against the node's fields, its name against the node's vendor token — and
//     drafts the node, the match being LOW; the other Component, which the node
//     resembles on no signal, is not a candidate;
//   - against the same catalog with a mapping override naming that Component, it
//     references the Component, a HIGH match;
//   - against a catalog holding no Component, no node is scored against anything:
//     every node is drafted at the NONE tier and none is referenced.
//
// 🔴 PLANTED DEFECT — one entry dropped from the view the host hands the importer,
// in ProjectCatalog (internal/compiler/importersocket/catalog_projection.go): replace
// `for _, name := range slices.Sorted(maps.Keys(names))` with
// `for _, name := range slices.Sorted(maps.Keys(names))[1:]`. The first entry by
// name, the enterprise model, is then missing from the view while the second is
// still in it: the first row's model node is drafted with no candidate at the NONE
// tier, and the second row's is drafted where it was referenced. The third row's
// catalog holds no Component, so the projection returns before the loop and the row
// passes either way.
func TestImportScoresTheWorkspaceCatalogsOwnComponents(t *testing.T) {
	t.Parallel()

	const autoApply = "auto-apply-above-threshold"
	override := map[string]any{"langflow": map[string]any{"OpenAIModel": map[string]any{
		"component": enterpriseModel.name,
		"category":  "ai-model",
		"traits": map[string]any{
			"model.aggrega.ai/capability":  "generative",
			"component.aggrega.ai/hosting": "self-hosted",
		},
		"vendor": "llm",
	}}}

	for _, tc := range []struct {
		name       string
		components []catalogComponent
		settings   map[string]any
		// want is the decision for the OpenAI model node; nil asks that every node
		// be drafted at the NONE tier with no candidate.
		want *abi.ImportDecision
	}{
		{
			name:       "a catalog holding an enterprise model Component scores it as a candidate",
			components: []catalogComponent{enterpriseModel, enterpriseStore},
			settings:   map[string]any{"render_mode": "llm", "layout": "nodes", "reuse_mode": autoApply},
			want: &abi.ImportDecision{
				Candidates: []abi.ImportCandidate{{
					ComponentName: enterpriseModel.name, Score: 0.3138, Tier: "LOW",
					Signals: abi.CandidateSignals{Category: 0.2, DomainLabels: 0.075, Shape: 0.02, Name: 0.0188},
				}},
				Chosen:    &abi.DecisionChoice{Target: "imported/ai-model/openaimodel", Mode: "draft", Tier: "LOW"},
				Rationale: "drafted a new component; the closest candidate acme/ai-model/internal-llm scored only 0.3138",
			},
		},
		{
			name:       "a mapping override naming the enterprise Component references it",
			components: []catalogComponent{enterpriseModel, enterpriseStore},
			settings:   map[string]any{"render_mode": "llm", "layout": "nodes", "reuse_mode": autoApply, "mapping_overrides": override},
			want: &abi.ImportDecision{
				Candidates: []abi.ImportCandidate{{
					ComponentName: enterpriseModel.name, Score: 0.92, Tier: "HIGH",
					Signals: abi.CandidateSignals{Table: 0.5, Category: 0.2, DomainLabels: 0.15, Shape: 0.02, Name: 0.05},
				}},
				Chosen:    &abi.DecisionChoice{Target: enterpriseModel.name, Mode: "reference", Tier: "HIGH"},
				Rationale: "referenced acme/ai-model/internal-llm at score 0.9200 (HIGH) under auto-apply",
				SwapHint:  "no alternative candidate scored above zero",
			},
		},
		{
			name:     "a catalog holding no Component drafts every node and references none",
			settings: map[string]any{"render_mode": "llm", "layout": "nodes", "reuse_mode": autoApply},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rw, folder := catalogFolderImportWorkspaceAttaching(t, tc.settings)
			writeCatalogComponents(t, folder.Dir, tc.components...)
			decisions := importDecisionRecord(t, rw)

			if tc.want == nil {
				for _, d := range decisions {
					if len(d.Candidates) != 0 {
						t.Errorf("%s was scored against %+v; the catalog holds no Component", d.SourceNode.ID, d.Candidates)
					}
					if d.Chosen == nil || d.Chosen.Mode != "draft" || d.Chosen.Tier != "NONE" {
						t.Errorf("%s was decided %+v; want a draft at the NONE tier", d.SourceNode.ID, d.Chosen)
					}
				}
				return
			}

			var model *abi.ImportDecision
			for i := range decisions {
				if decisions[i].SourceNode.Type == "OpenAIModel" {
					model = &decisions[i]
				}
			}
			if model == nil {
				t.Fatalf("the decision record holds no decision for the OpenAI model node: %+v", decisions)
			}
			if !reflect.DeepEqual(model.Candidates, tc.want.Candidates) {
				t.Errorf("the model node's candidates are\n%+v\nwant\n%+v", model.Candidates, tc.want.Candidates)
			}
			if !reflect.DeepEqual(model.Chosen, tc.want.Chosen) {
				t.Errorf("the model node was decided %+v, want %+v", model.Chosen, tc.want.Chosen)
			}
			if model.Rationale != tc.want.Rationale {
				t.Errorf("the rationale reads %q, want %q", model.Rationale, tc.want.Rationale)
			}
			if model.SwapHint != tc.want.SwapHint {
				t.Errorf("the hint reads %q, want %q", model.SwapHint, tc.want.SwapHint)
			}
		})
	}
}

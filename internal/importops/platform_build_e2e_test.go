// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

// End-to-end proofs that a Langflow or Flowise source drafted as the platform
// carrying its flow builds, and renders the flow the platform seeds, with a
// Release supplying only what the draft leaves for the Release.
//
// Each case drafts through the real Import, naming no format, over a workspace
// whose catalog folder holds the reference importers and a JSON parser beside
// their entities — the folder importopstest lays down, and the path every
// surface reaches — together with the standard library's platform, model and tool
// Components, copied in from the aggrega-hub repository checked out beside this
// one. It then writes a Manifest and a Release beside the drafted Aggregate and
// builds it through the build-faithful harness, which loads those Components with
// their Recipes, the libraries the Recipes import and the secret Features from the
// same checkout, and the provider Plugin entities the compiler's flow-definition
// proofs build with, whose guests resolve from the binary's embedded set.
//
// These tests read catalog entities from the aggrega-hub repository checked out
// beside this one (the paths leave this repository for aggrega-hub). Without that
// checkout they fail with a catalog-load error rather than skipping, so check out
// aggrega-hub beside this repository first.
//
// The Langflow draft built as drafted is Langflow 1.6.0's Basic Prompting, a
// committed real export whose Language Model node selects a model. The other flows
// the Langflow cases import are the committed real export Basic Prompting with
// authored values added — a model selected on its Language Model node, which that
// export leaves unselected, a model named in the other ways a node can name one,
// and an MCP Tools node naming a server, which no flow Langflow publishes carries.
// Each says so where it is built; none is presented as an export.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// platformHub is the catalog of the aggrega-hub repository checked out beside
// this one.
const platformHub = "../../../aggrega-hub/aggrega-catalog"

// platformImportEntities are the hub's entities the import's catalog view
// holds, by their path beneath platformHub: the two flow platforms, the model and
// tool Components their parts are referenced as, and the schema library whose
// shared schemas declare those Components' values — without which the view
// would carry no value key of theirs.
var platformImportEntities = []string{
	"component/core/ai-agent/langflow/1.0.0/component.yaml",
	"component/core/ai-agent/flowise/1.0.0/component.yaml",
	"component/core/ai-model/openai/1.0.0/component.yaml",
	"component/core/ai-model/openai-embedding/1.0.0/component.yaml",
	"component/core/ai-tool/mcp/1.0.0/component.yaml",
	"component/core/ai-tool/retrieval/1.0.0/component.yaml",
	"schemalibrary/base-components/1.0.0/library.yaml",
}

// platformBuildPaths are the catalog paths a drafted platform is built against:
// the platforms, the model and the MCP server Components, the Kubernetes and
// compose Recipe of each, the bundles the platforms' Recipes import to seed a
// flow, the schema library, the two secret Features and their realizations, and
// the provider Plugin entities.
func platformBuildPaths() []string {
	paths := []string{"../compiler/testdata/flow-definition-escape/catalog"}
	for _, rel := range []string{
		"component/core/ai-agent/langflow/1.0.0/component.yaml",
		"component/core/ai-agent/flowise/1.0.0/component.yaml",
		"component/core/ai-model/openai/1.0.0/component.yaml",
		"component/core/ai-tool/mcp/1.0.0/component.yaml",
		"recipe/helm/langflow/1.0.0/recipe.yaml",
		"recipe/compose/langflow/1.0.0/recipe.yaml",
		"recipe/helm/flowise/1.0.0/recipe.yaml",
		"recipe/compose/flowise/1.0.0/recipe.yaml",
		"recipe/helm/openai/1.0.0/recipe.yaml",
		"recipe/compose/openai/1.0.0/recipe.yaml",
		"recipe/helm/mcp-remote/1.0.0/recipe.yaml",
		"recipe/compose/mcp-remote/1.0.0/recipe.yaml",
		"artifactlibrary/langflow-flow/1.0.0/library.yaml",
		"artifactlibrary/flowise-flow-seed/1.0.0/library.yaml",
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

// hubPlatformWorkspace is catalogFolderImportWorkspaceAttaching with the hub's
// platformImportEntities copied into the catalog folder at the paths the hub
// keeps them under.
func hubPlatformWorkspace(t *testing.T, settings map[string]any) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, folder := catalogFolderImportWorkspaceAttaching(t, settings)
	layHubEntities(t, folder.Dir, platformImportEntities)
	return rw
}

// layHubEntities copies each of the hub's entities named by its path beneath
// platformHub into dir, at the same path.
func layHubEntities(t *testing.T, dir string, rels []string) {
	t.Helper()
	for _, rel := range rels {
		body, err := os.ReadFile(filepath.Join(platformHub, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read the hub's %s: %v", rel, err)
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatalf("create the directory for %s: %v", rel, err)
		}
		if err := os.WriteFile(dst, body, 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

// referencing are the settings referencing every part whose best candidate is
// a HIGH match.
func referencing() map[string]any {
	return map[string]any{"reuse_mode": "auto-apply-above-threshold"}
}

// referencingLanguageModel is referencing with a mapping override mapping
// Langflow's Language Model component to core/ai-model/openai, which no row of
// the importer's own mapping arm does.
func referencingLanguageModel() map[string]any {
	return map[string]any{
		"reuse_mode": "auto-apply-above-threshold",
		"mapping_overrides": map[string]any{"langflow": map[string]any{"LanguageModelComponent": map[string]any{
			"component": "core/ai-model/openai",
			"category":  "ai-model",
			"traits":    map[string]any{"model.aggrega.ai/capability": "generative", "component.aggrega.ai/hosting": "managed"},
			"vendor":    "openai",
		}}},
	}
}

// promptingNodeID is the id of the Language Model node of the committed real
// export Basic Prompting.
const promptingNodeID = "LanguageModelComponent-FLeYF"

// promptingWith is the committed real export Basic Prompting with members set on
// fields of its Language Model node's template — fields, then member, then value
// — an authored flow.
func promptingWith(t *testing.T, fields map[string]map[string]any) []byte {
	t.Helper()
	flow := decoded(t, readRealExport(t, langflowPromptingProject))
	template := langflowNode(t, flow, promptingNodeID)
	for name, members := range fields {
		field := asObject(template[name])
		if field == nil {
			t.Fatalf("the Language Model node holds no field %s", name)
		}
		for member, value := range members {
			field[member] = value
		}
	}
	return encoded(t, flow)
}

// aSelectedModel is the model the authored flows select on their Language Model
// node, written in the shape Langflow keeps a selection in: a list holding the
// model's name and provider.
var aSelectedModel = map[string]map[string]any{
	"model": {"value": []any{map[string]any{"name": "gpt-4o-mini", "provider": "OpenAI"}}},
}

// TestAReferencedModelPartIsWrittenWithTheModelItsNodeSelects imports, under the
// default layout and over the hub's Components, sources whose model part the draft
// references as core/ai-model/openai, which declares the model value every model
// Component of the standard library requires and a Release cannot write:
//
//   - the model the node selects is written into the part's values.model, read
//     back as the compiler reads the Aggregate, and its decision says so — a
//     Flowise chat model's modelName, a Langflow Language Model's selection and,
//     where it is set, the override that replaces it;
//   - a node naming no model a draft can write — the committed Basic Prompting,
//     which selects none, one naming its model through a global variable, and one
//     taking it from another node — leaves values.model unwritten, with a comment
//     in the part saying it is left for the author and why, a model-not-named
//     warning naming the node, and its decision saying so;
//   - a part the draft drafts rather than references is written with no value
//     and nothing is reported of a model.
//
// 🔴 PLANTED DEFECTS, each rebuilt into the importer fixtures from its source:
//
//   - in partModel (aggrega-pdk/go/pkg/importer/platform.go), replace
//     `return true, name, ""` with `return true, "", noModelField`: the three rows
//     writing a model fail, values.model absent;
//   - in modelFields (plugins/langflow-importer/model.go), replace
//     `"LanguageModelComponent": {"model_name", "model"},` with
//     `"LanguageModelComponent": {"model"},`: the override row fails, writing
//     gpt-4o-mini, and the global-variable row fails, writing gpt-4o-mini;
//   - in modelOf (plugins/flowise-importer/model.go), replace
//     `if e.Key == "modelName" {` with `if e.Key == "modelname" {`: the Flowise
//     row fails, values.model left for the author.
func TestAReferencedModelPartIsWrittenWithTheModelItsNodeSelects(t *testing.T) {
	t.Parallel()
	const (
		flowiseKey  = "chatopenai-0"
		langflowKey = "languagemodelcomponent-fleyf"
	)
	cases := []struct {
		name     string
		settings map[string]any
		// source is a committed real export by its path beneath realExportsDir, or,
		// where it is empty, the authored flow build writes.
		source string
		build  func(t *testing.T) []byte
		key    string
		node   string
		// model is the model written into values.model; where it is empty, why is
		// the reason the part's comment and the warning give for leaving it.
		model, why string
		// drafted marks a part the draft drafts rather than references.
		drafted bool
	}{
		{name: "a Flowise chat model's modelName", settings: referencing(), source: flowiseChatflow,
			key: flowiseKey, node: "chatOpenAI_0", model: "gpt-3.5-turbo"},
		{name: "a Langflow Language Model's selection (authored)", settings: referencingLanguageModel(),
			build: func(t *testing.T) []byte { return promptingWith(t, aSelectedModel) },
			key:   langflowKey, node: promptingNodeID, model: "gpt-4o-mini"},
		{name: "the override replacing a Language Model's selection (authored)", settings: referencingLanguageModel(),
			build: func(t *testing.T) []byte {
				return promptingWith(t, map[string]map[string]any{"model": aSelectedModel["model"],
					"model_name": {"value": "  gpt-4.1-mini  "}})
			},
			key: langflowKey, node: promptingNodeID, model: "gpt-4.1-mini"},
		{name: "a Language Model selecting none, as the committed export does", settings: referencingLanguageModel(),
			source: langflowPromptingProject, key: langflowKey, node: promptingNodeID, why: "selects no model"},
		{name: "a Language Model naming its model through a global variable (authored)",
			settings: referencingLanguageModel(),
			build: func(t *testing.T) []byte {
				return promptingWith(t, map[string]map[string]any{"model": aSelectedModel["model"],
					"model_name": {"value": "TEAM_MODEL", "load_from_db": true}})
			},
			key: langflowKey, node: promptingNodeID,
			why: "names its model through a global variable, which Langflow reads only when the flow runs"},
		{name: "a Language Model taking its model from another node (authored)", settings: referencingLanguageModel(),
			build: func(t *testing.T) []byte {
				return promptingWith(t, map[string]map[string]any{"model": {"value": "connect_other_models"}})
			},
			key: langflowKey, node: promptingNodeID, why: "takes its model from another node connected to it"},
		{name: "a drafted part", settings: nil, source: flowiseChatflow, key: flowiseKey, node: "chatOpenAI_0",
			drafted: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rw := hubPlatformWorkspace(t, tc.settings)
			var res Result
			var outputDir string
			if tc.source != "" {
				outputDir = filepath.Join(rw.Root(), "drafts")
				res = importRealExport(t, rw, tc.source, authoredcontent.DeliveryFor(false), outputDir)
			} else {
				res, outputDir = writtenImport(t, rw, "authored-flow.json", tc.build(t))
			}
			if res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}
			text := readDraftedAggregate(t, outputDir)
			part, ok := readAsTheCompilerDoes(t, text).Spec.Components[tc.key]
			if !ok {
				t.Fatalf("the draft holds no part %s:\n%s", tc.key, text)
			}
			got, written := part.Values["model"]
			decision := decisionNamed(t, res.Decisions, tc.node)
			reported := diagnosticsCoded(res.Diagnostics, "model-not-named")

			switch {
			case tc.drafted:
				if !strings.HasPrefix(part.Type, "imported/") {
					t.Fatalf("the part is %s, want one the draft drafts", part.Type)
				}
				if written || len(part.Values) > 0 {
					t.Errorf("a drafted part is written with values %v", part.Values)
				}
				if len(reported) > 0 || strings.Contains(decision.Rationale, "values.model") {
					t.Errorf("a drafted part is reported of a model: %v; %q", reported, decision.Rationale)
				}
			case tc.model != "":
				if part.Type != "core/ai-model/openai" {
					t.Fatalf("the part is %s, want the referenced core/ai-model/openai", part.Type)
				}
				if got != tc.model {
					t.Errorf("values.model reads back as %#v, want %q", got, tc.model)
				}
				if want := "      values:\n        model: |-\n          " + tc.model + "\n"; !strings.Contains(text, want) {
					t.Errorf("the part is not written with %q:\n%s", want, text)
				}
				if want := `; values.model is written as "` + tc.model + `", the model the node selects`; !strings.Contains(decision.Rationale, want) {
					t.Errorf("the decision says %q, want it to carry %q", decision.Rationale, want)
				}
				if len(reported) > 0 {
					t.Errorf("a written model is reported as left: %v", reported)
				}
			default:
				if written {
					t.Errorf("values.model is written as %#v; the node names none", got)
				}
				comment := "      # values.model, the name of the model core/ai-model/openai serves, is left for the author, " +
					"since the node this part stands for " + tc.why + ".\n"
				if !strings.Contains(text, "    "+tc.key+":\n      type: core/ai-model/openai\n"+comment) {
					t.Errorf("the part does not carry the comment %q:\n%s", comment, text)
				}
				want := "values.model of " + tc.key + ", the name of the model core/ai-model/openai serves, is left for the " +
					"author, since node " + tc.node + " (LanguageModelComponent) " + tc.why + "; a Release cannot write it"
				if !slices.Equal(reported, []string{want}) {
					t.Errorf("the import reported %q, want exactly %q", reported, want)
				}
				if suffix := "; values.model is left for the author, since the node " + tc.why; !strings.Contains(decision.Rationale, suffix) {
					t.Errorf("the decision says %q, want it to carry %q", decision.Rationale, suffix)
				}
			}
		})
	}
}

// platformRelease is what a Release written beside a drafted platform supplies
// it with on one target, which is only what the draft leaves for the Release:
// the platform's image, its owner's address where it takes one, its
// administrator's secret, a credential on the model connection, and the address
// of each MCP server the flow names. Every part the draft wires is placed in the
// Release's one boundary with it.
type platformRelease struct {
	// platform is the platform instance's key, langflow or flowise.
	platform string
	// target is helm or compose.
	target string
	// parts are the keys of the parts the draft wires.
	parts []string
	// model is the alias of the model connection a credential is attached on.
	model string
	// servers are, by an MCP part's key, the address the Release gives it.
	servers map[string]string
}

// writePlatformRelease writes the Manifest and the Release r describes beside the
// drafted Aggregate in dir, and returns the Release's path. The Manifest realizes
// each Component on the target through its Recipe there, and the secrets through
// core/referenced-secret on helm — a Secret the cluster holds — and through
// core/secret-value, a variable Docker Compose substitutes, on compose.
func writePlatformRelease(t *testing.T, dir string, r platformRelease) string {
	t.Helper()
	name := "imported-" + r.platform + "-" + r.target
	secret, realization, environment := "core/referenced-secret", "helm/k8s-existing-secret", "production"
	origin := func(onHelm, onCompose string) string { return onHelm }
	if r.target == "compose" {
		secret, realization, environment = "core/secret-value", "compose/dotenv-secret", "development"
		origin = func(_, onCompose string) string { return "${" + onCompose + "}" }
	}
	manifest := "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: " + name + "\n  version: \"1.0.0\"\n" +
		"spec:\n  mappings:\n    components:\n      type:\n" +
		"        core/ai-agent/" + r.platform + ":1.0.0: " + r.target + "/" + r.platform + ":1.0.0\n" +
		"        core/ai-model/openai:1.0.0: " + r.target + "/openai:1.0.0\n" +
		"        core/ai-tool/mcp:1.0.0: " + r.target + "/mcp-remote:1.0.0\n" +
		"    features:\n      type:\n        " + secret + ":1.0.0: " + realization + ":1.0.0\n"

	var b strings.Builder
	b.WriteString("apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: " + name + "\n  environment: " +
		environment + "\n  version: \"1.0.0\"\nspec:\n  aggregate: imported/flow\n  manifest: " + name + "\n" +
		"  boundaries:\n    main:\n      directory: ./dist/" + r.target + "\n      components:\n")
	for _, part := range r.parts {
		if address, ok := r.servers[part]; ok {
			b.WriteString("        " + part + ":\n          infra:\n            endpoint: " + address + "\n")
			continue
		}
		b.WriteString("        " + part + ": {}\n")
	}
	b.WriteString("        " + r.platform + ":\n          infra:\n")
	slot := "admin_password"
	switch r.platform {
	case "langflow":
		b.WriteString("            image: langflowai/langflow:1.6.0\n")
	case "flowise":
		slot = "owner_password"
		b.WriteString("            image: flowiseai/flowise:3.1.3\n            owner_email: owner@example.com\n")
	}
	adminOrigin := origin("langflow-admin", "LANGFLOW_SUPERUSER_PASSWORD")
	if r.platform == "flowise" {
		adminOrigin = origin("flowise-owner", "FLOWISE_OWNER_PASSWORD")
	}
	b.WriteString("          features:\n            " + slot + ":\n              type: " + secret + "\n" +
		"              infra:\n                origin: " + adminOrigin + "\n")
	b.WriteString("          uses:\n            model:\n              " + r.model + ":\n                features:\n" +
		"                  credential:\n                    type: " + secret + "\n                    infra:\n" +
		"                      origin: " + origin("openai-key", "OPENAI_API_KEY") + "\n")

	for file, body := range map[string]string{"manifest.yaml": manifest, "release.yaml": b.String()} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatalf("write the %s: %v", file, err)
		}
	}
	return filepath.Join(dir, "release.yaml")
}

// composeLines are the lines the comment inside a drafted platform's instance
// shows for the compose target, as an author uncomments them: the platform's
// own secret declared with the key the compose Recipe reads it under.
func composeLines(platform string) []string {
	slot, key := "admin_password", "LANGFLOW_SUPERUSER_PASSWORD"
	if platform == "flowise" {
		slot, key = "owner_password", "OWNER_PASSWORD"
	}
	return []string{"      features:", "        " + slot + ":", "          type: core/secret-value", "          values:",
		"            key: " + key}
}

// uncommentComposeLines returns the drafted Aggregate text with the lines the
// comment inside the platform's instance shows for the compose target
// uncommented where they stand — exactly what an author building for compose
// does — and the lines it uncommented. It fails the test where the draft shows
// no such lines.
func uncommentComposeLines(t *testing.T, text string) (string, []string) {
	t.Helper()
	const opening = "      # For the compose target only, uncomment the lines below, "
	lines := strings.Split(text, "\n")
	var uncommented []string
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], opening) {
			continue
		}
		for j := i + 1; j < len(lines) && strings.HasPrefix(lines[j], "      # "); j++ {
			lines[j] = "      " + strings.TrimPrefix(lines[j], "      # ")
			uncommented = append(uncommented, lines[j])
		}
		break
	}
	if len(uncommented) == 0 {
		t.Fatalf("the draft shows no lines for the compose target:\n%s", text)
	}
	return strings.Join(lines, "\n"), uncommented
}

// buildDraftOn writes r's Manifest and Release beside the drafted Aggregate in
// dir — on compose with the lines the draft shows for compose uncommented first,
// which must be composeLines — and builds it through the build-faithful harness,
// returning what it rendered.
func buildDraftOn(t *testing.T, dir string, r platformRelease) map[string][]byte {
	t.Helper()
	if r.target == "compose" {
		path := filepath.Join(dir, "aggregate.yaml")
		text, uncommented := uncommentComposeLines(t, readDraftedAggregate(t, dir))
		if want := composeLines(r.platform); !slices.Equal(uncommented, want) {
			t.Fatalf("the draft shows the compose lines\n%s\nwant\n%s", strings.Join(uncommented, "\n"),
				strings.Join(want, "\n"))
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatalf("write the Aggregate with the compose lines: %v", err)
		}
	}
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  writePlatformRelease(t, dir, r),
		CatalogPaths: platformBuildPaths(),
	})
	return result.Artifacts
}

// seededFlow reads, out of what a build rendered, the flow the platform keyed
// platform is seeded with: on helm the flow.json data of the ConfigMap the
// platform's Recipe mounts, and on compose the content of the config it mounts,
// as Docker Compose reads it — every $$ a $.
func seededFlow(t *testing.T, artifacts map[string][]byte, platform, target string) string {
	t.Helper()
	if target == "helm" {
		var cm struct {
			Kind string            `yaml:"kind"`
			Data map[string]string `yaml:"data"`
		}
		doc := renderedEnding(t, artifacts, "/helm/"+platform+"/"+platform+"-flow/"+platform+"-flow.yaml")
		if err := yaml.Unmarshal(doc, &cm); err != nil || cm.Kind != "ConfigMap" {
			t.Fatalf("the rendered flow is not a ConfigMap (%v):\n%s", err, doc)
		}
		flow, ok := cm.Data["flow.json"]
		if !ok {
			t.Fatalf("the rendered ConfigMap carries no flow.json:\n%s", doc)
		}
		return flow
	}
	content, ok := renderedCompose(t, artifacts).Configs[platform+"-flow"]
	if !ok || content.Content == nil {
		t.Fatalf("the rendered compose file carries no config %s-flow with inline content", platform)
	}
	return strings.ReplaceAll(*content.Content, "$$", "$")
}

// renderedComposeFile is the part of a rendered compose file the cases read.
type renderedComposeFile struct {
	Services map[string]struct {
		Environment map[string]string `yaml:"environment"`
	} `yaml:"services"`
	Configs map[string]struct {
		Content *string `yaml:"content"`
	} `yaml:"configs"`
}

// renderedCompose reads the rendered compose file.
func renderedCompose(t *testing.T, artifacts map[string][]byte) renderedComposeFile {
	t.Helper()
	var f renderedComposeFile
	doc := renderedEnding(t, artifacts, "/compose/docker-compose.yaml")
	if err := yaml.Unmarshal(doc, &f); err != nil {
		t.Fatalf("the rendered compose file does not parse: %v\n%s", err, doc)
	}
	return f
}

// platformVariable reads the value the platform keyed platform is given for the
// environment variable name, as a literal: on helm from its Deployment's first
// container, on compose from its service.
func platformVariable(t *testing.T, artifacts map[string][]byte, platform, target, name string) string {
	t.Helper()
	if target == "compose" {
		value, ok := renderedCompose(t, artifacts).Services[platform].Environment[name]
		if !ok {
			t.Fatalf("the rendered service %s sets no %s", platform, name)
		}
		return value
	}
	var d struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Env []struct {
							Name  string  `yaml:"name"`
							Value *string `yaml:"value"`
						} `yaml:"env"`
					} `yaml:"containers"`
				} `yaml:"spec"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}
	doc := renderedEnding(t, artifacts, "/helm/"+platform+"-deployment.yaml")
	if err := yaml.Unmarshal(doc, &d); err != nil || len(d.Spec.Template.Spec.Containers) == 0 {
		t.Fatalf("the rendered Deployment does not read (%v):\n%s", err, doc)
	}
	for _, e := range d.Spec.Template.Spec.Containers[0].Env {
		if e.Name == name && e.Value != nil {
			return *e.Value
		}
	}
	t.Fatalf("the rendered Deployment sets no literal %s:\n%s", name, doc)
	return ""
}

// renderedEnding returns the one rendered file whose path ends with suffix. The
// directory a Release names leads every path, so the end of the path is what
// identifies the file a Recipe rendered.
func renderedEnding(t *testing.T, artifacts map[string][]byte, suffix string) []byte {
	t.Helper()
	var matched []string
	for path := range artifacts {
		if strings.HasSuffix(path, suffix) {
			matched = append(matched, path)
		}
	}
	if len(matched) != 1 {
		all := make([]string, 0, len(artifacts))
		for path := range artifacts {
			all = append(all, path)
		}
		sort.Strings(all)
		t.Fatalf("want one rendered file ending %q, found %d among %v", suffix, len(matched), all)
	}
	return artifacts[matched[0]]
}

// readDraftedAggregate reads the Aggregate an import wrote under dir.
func readDraftedAggregate(t *testing.T, dir string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, "aggregate.yaml"))
	if err != nil {
		t.Fatalf("read the drafted Aggregate: %v", err)
	}
	return string(content)
}

// decisionNamed returns the decision recorded for the node whose id is id.
func decisionNamed(t *testing.T, decisions []abi.ImportDecision, id string) abi.ImportDecision {
	t.Helper()
	for _, d := range decisions {
		if d.SourceNode.ID == id {
			return d
		}
	}
	t.Fatalf("no decision was recorded for %s", id)
	return abi.ImportDecision{}
}

// diagnosticsCoded returns the message of every diagnostic carrying code, in
// order.
func diagnosticsCoded(diagnostics []abi.Diagnostic, code string) []string {
	var out []string
	for _, d := range diagnostics {
		if d.Code == code {
			out = append(out, d.Message)
		}
	}
	return out
}

// openAIEndpoint is the address core/ai-model/openai publishes to the
// Components wired to it where the Release repoints it nowhere: its own
// default, OpenAI's base URL.
const openAIEndpoint = "https://api.openai.com/v1"

// asSeeded is the flow a platform is seeded with from the flow its draft
// carries. The bundle each platform's Recipes import to seed a flow writes it as
// a YAML literal block, which ends it in exactly one line feed: the carried flow
// with its trailing line feeds — none included — written as one.
func asSeeded(carried string) string {
	return strings.TrimRight(carried, "\n") + "\n"
}

// TestADraftedPlatformBuildsWithTheReleaseSupplyingWhatTheDraftLeavesIt imports
// one committed real export of each platform under the default layout,
// referencing every HIGH match — the Flowise export Multiple Documents QnA, and
// Langflow 1.6.0's Basic Prompting with a mapping override mapping Langflow's
// Language Model component to core/ai-model/openai, which no row of the
// importer's own mapping arm does — and builds each draft with a Release
// supplying only what the draft leaves for the Release: on helm as drafted, and
// on compose with the lines the draft's comment shows for compose uncommented,
// which must be exactly the platform's own secret — the Flowise owner's password,
// the Langflow administrator's password — declared with its own key. The build
// succeeds on both, the model part the platform's model socket draws is written
// with the model its node selects, the draft carries the export byte for byte,
// the flow the platform is seeded with is the flow the draft carries, and the
// platform reaches the wired model at the address the model publishes. The
// Langflow export's Language Model node names its key as the global variable
// OPENAI_API_KEY, the one the platform delivers for the model connection, and the
// seeded flow's node names it still.
//
// 🔴 PLANTED DEFECTS, each rebuilt into the importer fixtures from its source:
//
//   - in partModel (aggrega-pdk/go/pkg/importer/platform.go), replace
//     `return true, name, ""` with `return true, "", noModelField`: every row
//     fails, the build halting on the model part's missing values.model;
//   - in writeTargetAttachment (aggrega-pdk/go/pkg/importer/platform.go), replace
//     `"      key: " + a.Key,` with `"      key: value",`: both compose rows fail,
//     the uncommented lines differing from the platform's secret's own key;
//   - in modelFields (plugins/langflow-importer/model.go), replace
//     `"LanguageModelComponent": {"model_name", "model"},` with
//     `"LanguageModelComponent": {"model"},`: both Langflow rows fail — the
//     export's Language Model node names its model in model_name alone, so
//     values.model is left for the author and the build halts on it — and both
//     Flowise rows pass.
func TestADraftedPlatformBuildsWithTheReleaseSupplyingWhatTheDraftLeavesIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		platform, source string
		settings         map[string]any
		// part is the key of the model part the platform's model socket draws,
		// and model the model its node selects.
		part, model string
		// keyNode is the node of a Langflow export naming its key as the global
		// variable OPENAI_API_KEY in its api_key field.
		keyNode string
	}{
		{platform: "flowise", source: flowiseChatflow, settings: referencing(), part: "chatopenai-0",
			model: "gpt-3.5-turbo"},
		{platform: "langflow", source: langflowSelectingProject, settings: referencingLanguageModel(),
			part: "languagemodelcomponent-kboja", model: "gpt-4.1", keyNode: selectingNodeID},
	} {
		for _, target := range []string{"helm", "compose"} {
			t.Run(tc.platform+"/"+target, func(t *testing.T) {
				t.Parallel()
				rw := hubPlatformWorkspace(t, tc.settings)
				dir := filepath.Join(rw.Root(), "drafts")
				res := importRealExport(t, rw, tc.source, authoredcontent.DeliveryFor(false), dir)
				if res.Halted() {
					t.Fatalf("the import halted: %+v", res.Diagnostics)
				}
				agg := readAsTheCompilerDoes(t, readDraftedAggregate(t, dir))
				part := agg.Spec.Components[tc.part]
				if part.Type != "core/ai-model/openai" {
					t.Fatalf("the model part %s is %q, want the referenced core/ai-model/openai", tc.part, part.Type)
				}
				if got := agg.Spec.Components[tc.platform].Uses["model"][tc.part].Target; got != tc.part {
					t.Fatalf("the model connection %s reaches %q, want the model part %s", tc.part, got, tc.part)
				}
				if got := part.Values["model"]; got != tc.model {
					t.Errorf("the model part is written with values.model %#v, want %q, the one its node selects", got,
						tc.model)
				}
				carried, _ := agg.Spec.Components[tc.platform].Values["flow_definition"].(string)
				if carried == "" {
					t.Fatal("the draft carries no flow")
				}
				if export := string(readRealExport(t, tc.source)); carried != export {
					t.Errorf("the draft does not carry the export byte for byte (%d bytes carried, %d exported)",
						len(carried), len(export))
				}

				artifacts := buildDraftOn(t, dir, platformRelease{platform: tc.platform, target: target,
					parts: []string{tc.part}, model: tc.part})
				seeded := seededFlow(t, artifacts, tc.platform, target)
				if want := asSeeded(carried); seeded != want {
					t.Errorf("the platform is seeded with a flow of %d bytes that is not the %d the draft carries, ending "+
						"in one line feed", len(seeded), len(want))
				}
				if tc.keyNode != "" {
					var flow map[string]any
					if err := json.Unmarshal([]byte(seeded), &flow); err != nil {
						t.Fatalf("the seeded flow does not read as JSON: %v", err)
					}
					key := asObject(langflowNode(t, flow, tc.keyNode)["api_key"])
					if key["value"] != "OPENAI_API_KEY" || key["load_from_db"] != true {
						t.Errorf("the seeded node %s's api_key carries %#v (load_from_db %#v), want the variable "+
							"OPENAI_API_KEY read from the global variables", tc.keyNode, key["value"], key["load_from_db"])
					}
				}
				if got := platformVariable(t, artifacts, tc.platform, target, "OPENAI_BASE_URL"); got != openAIEndpoint {
					t.Errorf("OPENAI_BASE_URL is %q, want the wired model's address %q", got, openAIEndpoint)
				}
			})
		}
	}
}

// TestAFlowiseDefinitionDraftedFromAnExportRendersAsTheExport imports the
// committed real Flowise export Multiple Documents QnA, whose chatflow writes a
// double brace for every connection between its nodes and holds no value the
// import writes anew, and builds the draft as the case above does: the flow the
// platform is seeded with is the export byte for byte — on helm the ConfigMap's
// data, on compose the config's content with every $$ read back as $.
//
// 🔴 PLANTED DEFECT, rebuilt into the importer fixtures from its source: in
// draftedText (aggrega-pdk/go/pkg/importer/definition.go), replace
// `literalBlock(escapeTemplateText(text), keyIndent)` with
// `literalBlock(text, keyIndent)`: both rows fail, the draft writing no double
// brace escaped — and a build of it would halt on the first one.
func TestAFlowiseDefinitionDraftedFromAnExportRendersAsTheExport(t *testing.T) {
	t.Parallel()
	export := string(readRealExport(t, flowiseChatflow))
	if !strings.Contains(export, "{{") {
		t.Fatal("the export carries no double brace, so it proves nothing about the escape")
	}
	for _, target := range []string{"helm", "compose"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			rw := hubPlatformWorkspace(t, referencing())
			dir := filepath.Join(rw.Root(), "drafts")
			if res := importRealExport(t, rw, flowiseChatflow, authoredcontent.DeliveryFor(false), dir); res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}
			if !strings.Contains(readDraftedAggregate(t, dir), `{{"{{"}}`) {
				t.Fatal("the draft writes no double brace escaped")
			}
			artifacts := buildDraftOn(t, dir, platformRelease{platform: "flowise", target: target,
				parts: []string{"chatopenai-0"}, model: "chatopenai-0"})
			if got := seededFlow(t, artifacts, "flowise", target); got != export {
				t.Errorf("the platform is seeded with a flow that is not the export byte for byte (%d bytes, %d exported)",
					len(got), len(export))
			}
		})
	}
}

// The values the authored MCP Tools node carries: the address of the server it
// names, with a key in its query, a request header carrying a key, and a header
// carrying a key in the headers field the node sends its server over those of
// the server's configuration.
const (
	sourceServerHost    = "docs-mcp.source-address.example"
	sourceServerKey     = "sk-plantedSOURCEurl0123456789"
	sourceServerAddress = "https://" + sourceServerHost + "/mcp?key=" + sourceServerKey
	sourceServerHeader  = "Bearer sk-plantedSOURCEheader0123456789ab"
	sourceNodeHeader    = "Bearer sk-plantedSOURCEnodeHEADERS0123456"
	// releaseServerAddress is the address the Release gives the MCP server.
	releaseServerAddress = "https://docs.tools.example/mcp"
)

// mcpBuildSource is the committed real export Basic Prompting with a model
// selected on its Language Model node and one MCP Tools node added, naming the
// server docs at sourceServerAddress with sourceServerHeader, and carrying
// sourceNodeHeader in its own headers field — an authored flow.
func mcpBuildSource(t *testing.T) []byte {
	t.Helper()
	flow := decoded(t, promptingWith(t, aSelectedModel))
	data := asObject(flow["data"])
	node := mcpToolsNode("MCPTools-docs1", map[string]any{"name": "docs",
		"config": map[string]any{"url": sourceServerAddress, "headers": map[string]any{"Authorization": sourceServerHeader}}})
	asObject(asObject(asObject(node["data"])["node"])["template"])["headers"] = map[string]any{"_input_type": "DictInput",
		"name": "headers", "type": "dict", "list": true, "value": map[string]any{"Authorization": sourceNodeHeader}}
	data["nodes"] = append(data["nodes"].([]any), node)
	return encoded(t, flow)
}

// TestTheBuiltFlowReachesAnMCPServerAtTheAddressTheReleaseGives imports
// mcpBuildSource under the default layout, referencing the model through a
// mapping override and the MCP server as core/ai-tool/mcp, and builds the draft
// with a Release giving the server's Component its infra.endpoint: on helm as
// drafted, and on compose with the lines the draft shows for compose uncommented.
//
//   - under the default empty_credentials setting, nothing the import wrote,
//     decided or reported holds the source's address, its host, the key in its
//     query or the header in the node's own headers field — the wiring writes
//     them empty, as how the node reaches its connection — while the request
//     header of the server's configuration, which the setting leaves as written,
//     is in the flow the draft carries and nowhere else; each is found in the
//     source, which says the search fires; and the draft wires the server's
//     Component to the platform's tools socket under the name the flow gives it;
//   - in the flow the platform is seeded with, the MCP Tools node's server
//     configuration holds the address the Release gave, its own headers field
//     holds an empty object, which overrides no header of the server's
//     configuration, and the flow holds no copy of the source's host.
//
// 🔴 PLANTED DEFECTS, each rebuilt into the importer fixtures from its source —
// each a wiring value moved back behind the setting, which writes nothing
// empty by default:
//
//   - in wireServer (plugins/langflow-importer/wire.go), replace
//     `if server.hasURL {` with `if server.hasURL && false {`: both rows fail,
//     the source's address in the drafted aggregate.yaml;
//   - in ownHeaderEdits (plugins/langflow-importer/wire.go), replace
//     `for _, f := range ownHeaders[typ] {` with
//     `for _, f := range ownHeaders[typ+"-"] {`: both rows fail, the header in the
//     node's own headers field in the drafted aggregate.yaml and the seeded
//     node's headers field not empty.
func TestTheBuiltFlowReachesAnMCPServerAtTheAddressTheReleaseGives(t *testing.T) {
	t.Parallel()
	source := mcpBuildSource(t)
	planted := []string{sourceServerAddress, sourceServerHost, sourceServerKey, sourceNodeHeader}
	for _, target := range []string{"helm", "compose"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			rw := hubPlatformWorkspace(t, referencingLanguageModel())
			res, dir := writtenImport(t, rw, "authored-mcp-flow.json", source)
			if res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}
			assertNoneReaches(t, planted, source, res, dir)
			assertCarriedOnlyInTheFlow(t, []string{sourceServerHeader}, source, res, dir)
			agg := readAsTheCompilerDoes(t, readDraftedAggregate(t, dir))
			if c := agg.Spec.Components["docs"]; c.Type != "core/ai-tool/mcp" || len(c.Values) > 0 {
				t.Errorf("the server is drafted as %s with values %v, want core/ai-tool/mcp referenced bare", c.Type, c.Values)
			}
			if got := agg.Spec.Components["langflow"].Uses["tools"]["docs"].Target; got != "docs" {
				t.Errorf("the tools connection docs reaches %q, want the server's Component docs", got)
			}

			artifacts := buildDraftOn(t, dir, platformRelease{platform: "langflow", target: target,
				parts: []string{"languagemodelcomponent-fleyf", "docs"}, model: "languagemodelcomponent-fleyf",
				servers: map[string]string{"docs": releaseServerAddress}})
			seeded := seededFlow(t, artifacts, "langflow", target)
			if strings.Contains(seeded, sourceServerHost) {
				t.Errorf("the seeded flow holds the source's server host %s", sourceServerHost)
			}
			var flow map[string]any
			if err := json.Unmarshal([]byte(seeded), &flow); err != nil {
				t.Fatalf("the seeded flow does not read as JSON: %v", err)
			}
			server := asObject(asObject(langflowNode(t, flow, "MCPTools-docs1")["mcp_server"])["value"])
			if got := asObject(server["config"])["url"]; got != releaseServerAddress {
				t.Errorf("the MCP Tools node reaches its server at %#v, want the address the Release gave, %q", got,
					releaseServerAddress)
			}
			headers := asObject(langflowNode(t, flow, "MCPTools-docs1")["headers"])["value"]
			if h, ok := headers.(map[string]any); !ok || len(h) != 0 {
				t.Errorf("the MCP Tools node's own headers field holds %#v, want an empty object", headers)
			}
		})
	}
}

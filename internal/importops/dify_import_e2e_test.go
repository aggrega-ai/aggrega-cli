// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs of the reference Dify importer through the real Import, over
// a workspace whose catalog folder holds the reference importers and a JSON
// parser beside their entities, and the hub's Dify API server with the schema
// library its values read — the YAML a Dify DSL is written in is parsed by the
// parser the binary carries, since the folder serves none:
//
//   - the authored workflow and chat applications, under names of their author's
//     choosing, are recognised as the dify format with no format named, and
//     drafted as an Aggregate composing core/dify carrying the application;
//   - by default a secret holding a value is carried as written and named in a
//     warning, never by value; with empty_credentials marked it reaches nothing,
//     and is reported as written empty; a source that would read back otherwise
//     is not carried;
//   - two imports of one application are byte-identical.
//
// The fixtures and their provenance are described in testdata/dify/README.md.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// difyFixtureDir holds the committed Dify fixtures, relative to this package.
const difyFixtureDir = "testdata/dify"

// difyHubEntities are the hub's entities the import's catalog view holds, by
// their path beneath platformHub: the Dify API server and the schema library its
// values read.
var difyHubEntities = []string{
	"component/core/ai-agent/dify/1.0.0/component.yaml",
	"schemalibrary/base-components/1.0.0/library.yaml",
}

// The installation module a Dify draft composes and its parameter carrying the
// application, as the catalog names them.
const (
	difyModule    = "core/dify"
	difyParameter = "app_definition"
)

// The key-shaped values the authored workflow application plants: in a secret
// variable, and in the key an HTTP request node authenticates with.
const (
	plantedDifySecret  = "sk-planted-dify-secret-needle-4f9c2a7e"
	plantedDifyRequest = "sk-planted-dify-request-needle-8b3d61f0"
)

// difyWorkspace is catalogFolderImportWorkspaceAttaching with difyHubEntities
// copied into the catalog folder at the paths the hub keeps them under.
func difyWorkspace(t *testing.T, settings map[string]any) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, folder := catalogFolderImportWorkspaceAttaching(t, settings)
	for _, rel := range difyHubEntities {
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

// readDifyFixture reads one committed Dify fixture.
func readDifyFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(difyFixtureDir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read the committed fixture %s: %v", name, err)
	}
	return body
}

// carriedApplication reads the drafted Aggregate written under outputDir as YAML
// and returns the application its dify entry carries in app_definition, as an
// Aggregate's template reading gives it back, and the Aggregate's text; ok is
// false where it carries none.
func carriedApplication(t *testing.T, outputDir string) (app string, ok bool, text string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(outputDir, "aggregate.yaml"))
	if err != nil {
		t.Fatalf("read the drafted Aggregate: %v", err)
	}
	var doc struct {
		Spec struct {
			Aggregates map[string]struct {
				Type       string         `yaml:"type"`
				Parameters map[string]any `yaml:"parameters"`
			} `yaml:"aggregates"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(content, &doc); err != nil {
		t.Fatalf("the drafted Aggregate does not read as YAML: %v\n%s", err, content)
	}
	entry, found := doc.Spec.Aggregates["dify"]
	if !found || entry.Type != difyModule {
		t.Fatalf("the draft composes no %s entry keyed dify:\n%s", difyModule, content)
	}
	definition, ok := entry.Parameters[difyParameter].(string)
	return strings.ReplaceAll(definition, `{{"{{"}}`, "{{"), ok, string(content)
}

// TestEveryDifyApplicationIsRecognisedAndDraftedAsTheInstallationCarryingIt
// imports each authored application under a name of its author's choosing,
// naming no format, and requires it read as the dify format alone — no other
// importer's signature matching it, which would halt as ambiguous — and drafted
// as aggregate.yaml composing core/dify with the application carried byte for
// byte, every node reported or kept in the flow, and nothing halting.
//
// 🔴 PLANTED DEFECT: in detect (plugins/dify-importer/main.go), write
// `{Path: "kind", Is: "string", Equals: "application"}`: every row is refused
// asking for the format.
func TestEveryDifyApplicationIsRecognisedAndDraftedAsTheInstallationCarryingIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture, name string
		reported      int
	}{
		{"authored/workflow.yml", "support-answers.yml", 5},
		{"authored/chat.yml", "policy-assistant.yaml", 3},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			source := readDifyFixture(t, tc.fixture)
			res, dir := writtenImport(t, difyWorkspace(t, nil), tc.name, source)
			if res.Format != "dify" {
				t.Fatalf("the source was read as the %q format, want dify", res.Format)
			}
			if res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}
			app, ok, text := carriedApplication(t, dir)
			if !ok || app != string(source) {
				t.Errorf("the carried application differs from the source:\n%s", text)
			}
			if got := len(diagnosticsCoded(res.Diagnostics, "part-configured-in-platform")); got != tc.reported {
				t.Errorf("the import reported %d parts configured inside Dify, want %d: %+v", got, tc.reported,
					res.Diagnostics)
			}
			for _, d := range res.Decisions {
				if d.Chosen != nil {
					t.Errorf("node %s chose %+v; every node stays in the application", d.SourceNode.ID, d.Chosen)
				}
			}
		})
	}
}

// TestTheNewerDSLWarningNamesTheFileImportedOnItsOwn imports the authored chat
// application at a version of the DSL newer than the engine's, as a file of its
// own reached through a path naming a directory, through the real Import and the
// Dify importer: the one dsl-version-newer warning names the file by its base
// name — the name the import request carries beside the file's bytes — and
// references it, wherever the path to it led.
//
// 🔴 PLANTED DEFECT: in Import (internal/importops/import.go), delete
// `primaryName = src.primaryName()`: the warning names `source ""` and references
// nothing, and the case fails.
func TestTheNewerDSLWarningNamesTheFileImportedOnItsOwn(t *testing.T) {
	t.Parallel()
	authored := readDifyFixture(t, "authored/chat.yml")
	source := bytes.Replace(authored, []byte("\nversion: 0.7.0\n"), []byte("\nversion: 0.8.0\n"), 1)
	if bytes.Equal(source, authored) {
		t.Fatal("the fixture's version line was not rewritten, so no warning is owed")
	}
	res, _ := writtenImport(t, difyWorkspace(t, nil), "apps/policy-assistant.yml", source)
	var newer []string
	for _, d := range res.Diagnostics {
		if d.Code == "dsl-version-newer" {
			newer = append(newer, d.SourceRef+" | "+d.Message)
		}
	}
	want := `policy-assistant.yml | source "policy-assistant.yml" is written in version "0.8.0" of the Dify DSL, `
	if len(newer) != 1 || !strings.HasPrefix(newer[0], want) {
		t.Errorf("the import warned %q, want one warning opening %q", newer, want)
	}
}

// TestTheEmptyCredentialsSettingDecidesWhatADifyImportCarries imports the authored
// workflow application with the empty_credentials setting absent and marked,
// after a needle proves each planted value is in the fixture: absent, each is
// carried as written in the drafted application, named in a credential-carried
// warning by its node and field, and in no diagnostic or decision; marked, none
// reaches anything the import writes or reports, each is named in a
// credential-emptied warning, and the rest of the application is carried byte
// for byte as exported. Then the application with an alias reading the secret's
// value, so that, written empty, it reads back as a tree differing beyond the
// values written: under marked it is not carried, a definition-not-carried
// warning says why, and no planted value reaches anything.
//
// 🔴 PLANTED DEFECT: in emptyInSource (plugins/internal/difyimport/yamledit.go),
// write `emptyScalar` after the scalar instead of in its place
// (`out = append(out, source[w.start:w.end]...)` before the append): the marked
// row's application does not read back as asked and is not carried, and the
// case fails.
func TestTheEmptyCredentialsSettingDecidesWhatADifyImportCarries(t *testing.T) {
	t.Parallel()
	source := readDifyFixture(t, "authored/workflow.yml")
	planted := []string{plantedDifySecret, plantedDifyRequest}
	fields := []string{"environment_variables.ticketing_api_key of node workflow",
		"authorization.config.api_key of node 1760000000005"}

	t.Run("absent", func(t *testing.T) {
		t.Parallel()
		res, dir := writtenImport(t, difyWorkspace(t, nil), "support-answers.yml", source)
		app, ok, _ := carriedApplication(t, dir)
		if !ok || app != string(source) {
			t.Errorf("the application is not carried as written")
		}
		named := strings.Join(diagnosticsCoded(res.Diagnostics, "credential-carried"), "\n")
		for _, f := range fields {
			if !strings.Contains(named, f) {
				t.Errorf("no credential-carried names %q:\n%s", f, named)
			}
		}
		assertNoneReachesTheRecord(t, planted, source, res)
	})
	t.Run("marked", func(t *testing.T) {
		t.Parallel()
		res, dir := writtenImport(t, difyWorkspace(t, map[string]any{"empty_credentials": "marked"}),
			"support-answers.yml", source)
		assertNoneReaches(t, planted, source, res, dir)
		app, ok, _ := carriedApplication(t, dir)
		want := strings.Replace(strings.Replace(string(source), "value: "+plantedDifySecret, "value: ''", 1),
			"api_key: "+plantedDifyRequest, "api_key: ''", 1)
		if !ok || app != want {
			t.Errorf("the application carried under marked is not the export with exactly its secrets emptied:\n%s", app)
		}
		named := strings.Join(diagnosticsCoded(res.Diagnostics, "credential-emptied"), "\n")
		for _, f := range []string{"environment_variables.ticketing_api_key, which node workflow",
			"authorization.config.api_key, which node 1760000000005"} {
			if !strings.Contains(named, f) {
				t.Errorf("no credential-emptied names %q:\n%s", f, named)
			}
		}
	})
	t.Run("marked, reading back otherwise", func(t *testing.T) {
		t.Parallel()
		aliased := []byte(strings.Replace(strings.Replace(string(source), "value: "+plantedDifySecret,
			"value: &ticketing "+plantedDifySecret, 1), "value: Hello", "value: *ticketing", 1))
		res, dir := writtenImport(t, difyWorkspace(t, map[string]any{"empty_credentials": "marked"}),
			"support-answers.yml", aliased)
		assertNoneReaches(t, planted, aliased, res, dir)
		if _, ok, text := carriedApplication(t, dir); ok {
			t.Errorf("the application is carried though it does not read back as asked:\n%s", text)
		}
		refused := strings.Join(diagnosticsCoded(res.Diagnostics, "definition-not-carried"), "\n")
		if !strings.Contains(refused, "do not read back as its tree with exactly those values emptied") {
			t.Errorf("no definition-not-carried says why: %q", refused)
		}
	})
}

// assertNoneReachesTheRecord requires that none of planted is in the import's
// decisions or diagnostics, after the needle: each is in the source.
func assertNoneReachesTheRecord(t *testing.T, planted []string, source []byte, res Result) {
	t.Helper()
	for _, p := range planted {
		if !strings.Contains(string(source), p) {
			t.Fatalf("the needle: %q is not in the source, so the search finds nothing", p)
		}
		for _, d := range res.Diagnostics {
			if strings.Contains(d.Message, p) {
				t.Errorf("a diagnostic names the planted %q: %q", p, d.Message)
			}
		}
		for _, d := range res.Decisions {
			if strings.Contains(d.Rationale+d.SourceNode.ParamsSummary+d.SwapHint, p) {
				t.Errorf("the decision of %s names the planted %q", d.SourceNode.ID, p)
			}
		}
	}
}

// difyBuildPaths are the catalog paths a drafted Dify installation is built
// against: core/dify, its ten Components and the Helm Recipe of each, the schema
// library, the referenced-secret Feature and its existing-Secret realization, and
// the provider Plugin entities the compiler's flow-definition proofs build with.
func difyBuildPaths() []string {
	paths := []string{"../compiler/testdata/flow-definition-escape/catalog"}
	for _, rel := range []string{
		"aggregate/core/dify/1.0.0/aggregate.yaml",
		"schemalibrary/base-components/1.0.0/library.yaml",
		"feature/core/referenced-secret/1.0.0/feature.yaml",
		"feature-recipe/helm/k8s-existing-secret/1.0.0/recipe.yaml",
	} {
		paths = append(paths, platformHub+"/"+rel)
	}
	for _, c := range difyBuildComponents {
		paths = append(paths, platformHub+"/component/"+c.component+"/1.0.0/component.yaml",
			platformHub+"/recipe/helm/"+c.recipe+"/1.0.0/recipe.yaml")
	}
	return paths
}

// difyBuildComponents are the Components core/dify composes and the Helm Recipe
// the build's Manifest maps each to.
var difyBuildComponents = []struct{ component, recipe string }{
	{"core/ai-agent/dify", "dify"},
	{"core/ai-agent/dify-worker", "dify-worker"},
	{"core/ai-agent/dify-worker-beat", "dify-worker-beat"},
	{"core/ai-agent/dify-web", "dify-web"},
	{"core/ai-agent/dify-sandbox", "dify-sandbox"},
	{"core/ai-agent/dify-plugin-daemon", "dify-plugin-daemon"},
	{"core/ai-agent/dify-ssrf-proxy", "dify-ssrf-proxy"},
	{"core/datastore/postgres", "postgres"},
	{"core/datastore/redis", "redis"},
	{"core/datastore/weaviate", "weaviate"},
}

// difySecretName is the Secret the build's Release names for the application
// secret, under the existing-Secret realization the build's Manifest binds.
const difySecretName = "dify-test-credentials"

// writeDifyBuild writes, beside the drafted Aggregate in dir, a Manifest mapping
// each Component of core/dify to its Helm Recipe and core/referenced-secret to an
// existing Secret, and a Release placing the entry in a boundary, naming the
// owner account the application is imported under, and holding parameters, the
// lines of its spec.parameters block — none where empty. It returns the
// Release's path.
func writeDifyBuild(t *testing.T, dir, parameters string) string {
	t.Helper()
	var manifest strings.Builder
	manifest.WriteString("apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  name: imported/flow\n" +
		"  version: 0.1.0\nspec:\n  mappings:\n    components:\n      type:\n")
	for _, c := range difyBuildComponents {
		manifest.WriteString("        " + c.component + ": helm/" + c.recipe + "\n")
	}
	manifest.WriteString("    features:\n      type:\n        core/referenced-secret: helm/k8s-existing-secret\n")
	release := "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: imported/flow\n" +
		"  environment: development\n  version: 0.1.0\nspec:\n  aggregate: " + draftedAggregateName(t, dir) + "\n" +
		"  manifest: imported/flow\n"
	if parameters != "" {
		release += "  parameters:\n" + parameters
	}
	release += "  boundaries:\n    platform:\n      aggregates:\n        dify:\n          components:\n            api:\n" +
		"              infra:\n                owner_email: owner@example.com\n              features:\n" +
		"                owner_password:\n                  type: core/referenced-secret\n                  infra:\n" +
		"                    origin: dify-test-owner\n"
	for file, body := range map[string]string{"manifest.yaml": manifest.String(), "release.yaml": release} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatalf("write the %s: %v", file, err)
		}
	}
	return filepath.Join(dir, "release.yaml")
}

// secretKeyVariables reads every rendered Deployment's containers, through
// deploymentEnvs, and returns the SECRET_KEY variable each hands one, by the
// artifact's path. A rendered .yaml file that does not read fails the test.
func secretKeyVariables(t *testing.T, artifacts map[string][]byte) map[string]helmVariable {
	t.Helper()
	got := map[string]helmVariable{}
	for path, body := range artifacts {
		envs, err := deploymentEnvs(body)
		if err != nil && strings.HasSuffix(path, ".yaml") {
			t.Fatalf("the rendered %s does not read as YAML: %v", path, err)
		}
		for _, env := range envs {
			for _, e := range env {
				if e.Name == "SECRET_KEY" {
					got[path] = e
				}
			}
		}
	}
	return got
}

// TestADifyDraftBuildsOnlyWithTheReleaseFulfillingItsSecretKey imports the
// authored chat application through the real Import and builds the draft, with a
// Manifest mapping core/dify's Components to their Helm Recipes and the
// credential to an existing Secret, through the build-faithful harness, under
// three Releases, each placing the installation and naming its owner:
//
//   - fulfilling secret_key with a Secret's name: the build completes, and the
//     API server and the task worker each read SECRET_KEY from that Secret under
//     the data key secret-key — no literal is rendered for it anywhere;
//   - leaving secret_key unset: the build halts with the required parameter not
//     supplied, naming secret_key;
//   - fulfilling it with the empty string: the build halts on the parameter
//     contract, naming secret_key, since the draft declares it at least one
//     character long.
//
// 🔴 PLANTED DEFECT: in moduleEntity (aggrega-pdk/go/pkg/importer/module.go),
// pass each parameter down as the empty string (`": \"\"\n"` in place of the
// template): the fulfilled row fails, core/dify's own secret_key halting on the
// empty value it is handed.
func TestADifyDraftBuildsOnlyWithTheReleaseFulfillingItsSecretKey(t *testing.T) {
	t.Parallel()
	source := readDifyFixture(t, "authored/chat.yml")
	for _, tc := range []struct {
		name       string
		parameters string
		// halts is the condition the build halts with, nil where it builds.
		halts error
	}{
		{name: "fulfilled", parameters: "    secret_key: " + difySecretName + "\n"},
		{name: "unset", halts: resolver.ErrParameterMissing()},
		{name: "empty", parameters: "    secret_key: \"\"\n", halts: resolver.ErrParameterContractViolation()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, dir := writtenImport(t, difyWorkspace(t, nil), "support-assistant.yml", source)
			if res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}
			result, err := pipelinetest.BuildE(t, pipelinetest.Input{
				ReleasePath:  writeDifyBuild(t, dir, tc.parameters),
				CatalogPaths: difyBuildPaths(),
			})
			if tc.halts != nil {
				if err == nil {
					t.Fatalf("the build completed; want it halted with %v", tc.halts)
				}
				if !errors.Is(err, tc.halts) {
					t.Errorf("the build halted with %s, want %v", cerrors.CauseChain(err), tc.halts)
				}
				if chain := cerrors.CauseChain(err); !strings.Contains(chain, "secret_key") {
					t.Errorf("the halt does not name secret_key: %s", chain)
				}
				return
			}
			if err != nil {
				t.Fatalf("the build halted with secret_key fulfilled: %s", cerrors.CauseChain(err))
			}
			got := secretKeyVariables(t, result.Artifacts)
			if len(got) != 2 {
				t.Errorf("SECRET_KEY is handed to %d Deployments, want the API server's and the task worker's: %v",
					len(got), got)
			}
			for path, v := range got {
				if v.Value != nil || v.ValueFrom == nil || v.ValueFrom.SecretKeyRef == nil ||
					v.ValueFrom.SecretKeyRef.Name != difySecretName || v.ValueFrom.SecretKeyRef.Key != "secret-key" {
					t.Errorf("%s hands SECRET_KEY %+v, want it read from the Secret %s under the data key secret-key",
						path, v, difySecretName)
				}
			}
		})
	}
}

// TestADifyImportForHelmMapsTheTypesOfTheInstallation imports the authored chat
// application for the helm target through the real Import, over a catalog folder
// holding core/dify, its ten Components with the Helm Recipe of each, and the
// referenced-secret Feature with its existing-Secret realization. The drafted
// Aggregate declares no component of its own and composes core/dify, so every
// type the drafted Manifest maps is one the installation declares: it maps each of
// the ten, in the order core/dify declares them, and the Feature every one of its
// credentials is attached as, read from the catalog the import loaded; and no
// warning calls a type unmapped or the installation unread.
//
// 🔴 PLANTED DEFECT: in addAggregate (internal/importops/draft_needs.go) drop the loop
// over the composed Aggregates: the Manifest maps nothing and the case fails.
func TestADifyImportForHelmMapsTheTypesOfTheInstallation(t *testing.T) {
	t.Parallel()
	rw, folder := catalogFolderImportWorkspaceAttaching(t, nil)
	entities := []string{
		"aggregate/core/dify/1.0.0/aggregate.yaml",
		"schemalibrary/base-components/1.0.0/library.yaml",
		"feature/core/referenced-secret/1.0.0/feature.yaml",
		"feature-recipe/helm/k8s-existing-secret/1.0.0/recipe.yaml",
	}
	want := "    components:\n      type:\n"
	for _, c := range difyInstallationOrder {
		entities = append(entities, "component/"+c.component+"/1.0.0/component.yaml",
			"recipe/helm/"+c.recipe+"/1.0.0/recipe.yaml")
		want += "        " + c.component + ": helm/" + c.recipe + "\n"
	}
	want += "    features:\n      type:\n        core/referenced-secret: helm/k8s-existing-secret\n"
	layHubEntities(t, folder.Dir, entities)

	res, _ := targetedImport(t, rw, "support-assistant.yml", readDifyFixture(t, "authored/chat.yml"), "helm",
		authoredcontent.DeliveryReport)
	if res.Format != "dify" || res.Halted() {
		t.Fatalf("the source was read as %q, halted %v: %+v", res.Format, res.Halted(), res.Diagnostics)
	}
	if manifest := fileNamed(t, res, ManifestFileName); !strings.Contains(manifest, "\n  mappings:\n"+want) {
		t.Errorf("the drafted Manifest does not map the installation's types as\n%s\nit is\n%s", want, manifest)
	}
	for _, d := range res.Diagnostics {
		if strings.HasPrefix(d.Message, "the drafted Manifest ") {
			t.Errorf("the import warned: %s", d.Message)
		}
	}
}

// difyInstallationOrder is difyBuildComponents in the order core/dify declares
// its components: the datastores, the egress proxy, the code sandbox, the plugin
// runtime, the API server, the worker, the scheduler and the web front end.
var difyInstallationOrder = []struct{ component, recipe string }{
	{"core/datastore/postgres", "postgres"},
	{"core/datastore/redis", "redis"},
	{"core/datastore/weaviate", "weaviate"},
	{"core/ai-agent/dify-ssrf-proxy", "dify-ssrf-proxy"},
	{"core/ai-agent/dify-sandbox", "dify-sandbox"},
	{"core/ai-agent/dify-plugin-daemon", "dify-plugin-daemon"},
	{"core/ai-agent/dify", "dify"},
	{"core/ai-agent/dify-worker", "dify-worker"},
	{"core/ai-agent/dify-worker-beat", "dify-worker-beat"},
	{"core/ai-agent/dify-web", "dify-web"},
}

// TestTwoImportsOfADifyApplicationAreByteIdentical imports the workflow
// application twice, each into a workspace of its own, and requires the two to
// draft the same files, byte for byte.
func TestTwoImportsOfADifyApplicationAreByteIdentical(t *testing.T) {
	t.Parallel()
	body := readDifyFixture(t, "authored/workflow.yml")
	first, _ := writtenImport(t, difyWorkspace(t, nil), "support-answers.yml", body)
	second, _ := writtenImport(t, difyWorkspace(t, nil), "support-answers.yml", body)
	if len(first.Files) == 0 || len(first.Files) != len(second.Files) {
		t.Fatalf("the two imports drafted %d and %d files", len(first.Files), len(second.Files))
	}
	for i := range first.Files {
		if first.Files[i].Path != second.Files[i].Path || first.Files[i].Content != second.Files[i].Content {
			t.Errorf("the two imports differ at %s", first.Files[i].Path)
		}
	}
}

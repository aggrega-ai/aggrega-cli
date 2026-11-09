// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

// End-to-end proofs that an import given a target maps a component type its
// importer drafts with no version, as every core importer drafts one, to the
// Recipe of that provider implementing the version a build resolves the type to.
//
// Each case imports through the real Import, over a workspace whose catalog
// folder holds the reference importers and a JSON parser beside their entities —
// the folder importopstest lays down — together with the standard library's
// PostgreSQL and MCP server Components and the Recipes implementing them, copied
// in from the aggrega-hub repository checked out beside this one. Every one of
// those Recipes declares the version it implements; the drafts name none.
//
// These tests read catalog entities from the aggrega-hub repository checked out
// beside this one. Without that checkout they fail with a catalog-load error
// rather than skipping, so check out aggrega-hub beside this repository first.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// unversionedTargetHubEntities are the hub's entities the import's catalog view
// holds, by their path beneath platformHub: the PostgreSQL and MCP server
// Components, the schema library the MCP server's values read, and the Recipes
// implementing the two types — for PostgreSQL one compose and one helm Recipe,
// for the MCP server two compose and three helm Recipes of different names.
var unversionedTargetHubEntities = []string{
	"component/core/datastore/postgres/1.0.0/component.yaml",
	"component/core/ai-tool/mcp/1.0.0/component.yaml",
	"schemalibrary/base-components/1.0.0/library.yaml",
	"recipe/compose/postgres/1.0.0/recipe.yaml",
	"recipe/helm/postgres/1.0.0/recipe.yaml",
	"recipe/compose/mcp-stdio-bridge/1.0.0/recipe.yaml",
	"recipe/compose/mcp-remote/1.0.0/recipe.yaml",
	"recipe/helm/mcp-stdio-bridge/1.0.0/recipe.yaml",
	"recipe/helm/mcp-remote/1.0.0/recipe.yaml",
	"recipe/helm/mcp/1.0.0/recipe.yaml",
}

// unversionedTargetWorkspace is catalogFolderImportWorkspaceAttaching with
// unversionedTargetHubEntities copied into the catalog folder.
func unversionedTargetWorkspace(t *testing.T, settings map[string]any) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, folder := catalogFolderImportWorkspaceAttaching(t, settings)
	layHubEntities(t, folder.Dir, unversionedTargetHubEntities)
	return rw
}

// postgresComposeSource is a Compose application of one PostgreSQL service,
// which the Compose importer, handed the referencing settings, drafts as an
// instance of core/datastore/postgres — the type written with no version.
const postgresComposeSource = `services:
  db:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: example
`

// targetedImport imports body, laid down under name, over rw for target into the
// workspace's drafts directory, writing what it drafts, and returns the result and
// that directory.
func targetedImport(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, name string, body []byte, target string,
	delivery authoredcontent.Delivery) (Result, string) {
	t.Helper()
	path := importopstest.WriteSource(t, filepath.Join(t.TempDir(), name), string(body))
	outputDir := filepath.Join(rw.Root(), "drafts")
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    path,
		SourceName:    name,
		Target:        target,
		OutputDir:     outputDir,
		Delivery:      delivery,
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
	if err != nil {
		t.Fatalf("the import of %s for the target %q was refused: %v", name, target, err)
	}
	return res, outputDir
}

// wantPostgresManifest is the Manifest the compose target drafts for the
// PostgreSQL application: the drafted type, written with no version, mapped to the
// one compose Recipe implementing the version of it the catalog holds.
const wantPostgresManifest = `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: imported/flow
  version: 0.1.0
spec:
  mappings:
    components:
      type:
        core/datastore/postgres: compose/postgres
`

// unmappedWarnings returns the message of every warning a result carries that
// says the drafted Manifest leaves a type unmapped.
func unmappedWarnings(res Result) []string {
	var out []string
	for _, d := range res.Diagnostics {
		if d.Severity == abi.SeverityWarn && strings.HasPrefix(d.Message, "the drafted Manifest leaves the component type ") {
			out = append(out, d.Message)
		}
	}
	return out
}

// TestATargetMapsATypeDraftedWithNoVersion imports the PostgreSQL application for
// the compose target and requires the drafted Aggregate to write the type with no
// version, the drafted Manifest to map it to compose/postgres — the one compose
// Recipe, which declares that it implements core/datastore/postgres:1.0.0 — and no
// warning to call the type unmapped. It then fills the drafted Release's
// metadata.environment, as the comment the importer wrote in its place asks, adds
// nothing else, and builds the draft set through the build-faithful harness over
// the hub's Component and Recipe: it renders through the compose provider.
//
// 🔴 PLANTED DEFECT: in implementsRequest (pkg/catalog/manifest_lookup.go), make the
// first statement `return componentType`: the drafted Manifest maps nothing, the
// Manifest row reddens, and the warning row names core/datastore/postgres.
func TestATargetMapsATypeDraftedWithNoVersion(t *testing.T) {
	t.Parallel()
	rw := unversionedTargetWorkspace(t, referencing())
	res, dir := targetedImport(t, rw, "compose.yaml", []byte(postgresComposeSource), "compose",
		authoredcontent.DeliveryFor(false))
	if res.Format != "compose" || res.Halted() {
		t.Fatalf("the source was read as %q, halted %v: %+v", res.Format, res.Halted(), res.Diagnostics)
	}
	aggregate, err := os.ReadFile(filepath.Join(dir, "aggregate.yaml"))
	if err != nil {
		t.Fatalf("read the drafted Aggregate: %v", err)
	}
	// The needle that makes the case about a type with no version: a draft naming
	// the version would be matched as written and prove nothing here.
	if !strings.Contains(string(aggregate), "    db:\n      type: core/datastore/postgres\n") {
		t.Fatalf("the drafted Aggregate does not reference core/datastore/postgres with no version:\n%s", aggregate)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, ManifestFileName))
	if err != nil {
		t.Fatalf("read the drafted Manifest: %v", err)
	}
	if string(manifest) != wantPostgresManifest {
		t.Errorf("the drafted Manifest is\n%s\nwant\n%s", manifest, wantPostgresManifest)
	}
	if warned := unmappedWarnings(res); len(warned) != 0 {
		t.Errorf("the import warned of unmapped types: %q", warned)
	}

	release := filepath.Join(dir, "release.yaml")
	body, err := os.ReadFile(release)
	if err != nil {
		t.Fatalf("read the drafted Release: %v", err)
	}
	lines := strings.Split(string(body), "\n")
	filled := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "  # environment:") {
			lines[i] = "  environment: testing"
			filled++
		}
	}
	if filled != 1 {
		t.Fatalf("the drafted Release holds %d comment lines where its environment goes; want one:\n%s", filled, body)
	}
	if err := os.WriteFile(release, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("fill the drafted Release's environment: %v", err)
	}
	rendered := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath: release,
		CatalogPaths: []string{
			"../compiler/testdata/flow-definition-escape/catalog",
			platformHub + "/component/core/datastore/postgres/1.0.0/component.yaml",
			platformHub + "/recipe/compose/postgres/1.0.0/recipe.yaml",
		},
	}).Artifacts
	var files []string
	for path := range rendered {
		files = append(files, path)
	}
	sort.Strings(files)
	if want := []string{"compose/docker-compose.yaml"}; !slices.Equal(files, want) {
		t.Errorf("the drafts rendered %v; want %v", files, want)
	}
}

// TestTheBuildOffersAnUnmappedInstanceOfATypeWithNoVersionItsRecipes writes the
// drafts the compose target composes for the PostgreSQL application, empties the
// drafted Manifest's mapping and fills the Release's metadata.environment, and
// builds them through the build-faithful harness over the hub's Component and its
// compose and helm Recipes: the build halts on the unmapped-instance condition
// naming the db instance, and offers both Recipes as the ones implementing its
// type — which the drafted Aggregate writes with no version, and each Recipe
// declares at the version the catalog holds — so the halt names the Recipes the
// drafted Manifest chose among.
//
// The harness builds over an eagerly-loaded catalog; the lazy lookup a command
// builds through is held to the same candidates by the catalog's own tests.
//
// 🔴 PLANTED DEFECT: in the instance resolution (*Catalog).CatalogLookup serves
// (pkg/catalog/catalog.go), pass nil in place of c.componentOfType: the halt
// offers no Recipe and the candidate row reddens.
func TestTheBuildOffersAnUnmappedInstanceOfATypeWithNoVersionItsRecipes(t *testing.T) {
	t.Parallel()
	rw := unversionedTargetWorkspace(t, referencing())
	_, dir := targetedImport(t, rw, "compose.yaml", []byte(postgresComposeSource), "compose",
		authoredcontent.DeliveryFor(false))
	manifest := filepath.Join(dir, ManifestFileName)
	body, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read the drafted Manifest: %v", err)
	}
	emptied := strings.Replace(string(body), "      type:\n        core/datastore/postgres: compose/postgres\n",
		"      type: {}\n", 1)
	if emptied == string(body) {
		t.Fatalf("the drafted Manifest does not hold the mapping this case removes:\n%s", body)
	}
	if err := os.WriteFile(manifest, []byte(emptied), 0o644); err != nil {
		t.Fatalf("empty the drafted Manifest's mapping: %v", err)
	}
	release := filepath.Join(dir, "release.yaml")
	text, err := os.ReadFile(release)
	if err != nil {
		t.Fatalf("read the drafted Release: %v", err)
	}
	lines := strings.Split(string(text), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "  # environment:") {
			lines[i] = "  environment: testing"
		}
	}
	if err := os.WriteFile(release, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("fill the drafted Release's environment: %v", err)
	}

	_, err = pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath: release,
		CatalogPaths: []string{
			"../compiler/testdata/flow-definition-escape/catalog",
			platformHub + "/component/core/datastore/postgres/1.0.0/component.yaml",
			platformHub + "/recipe/compose/postgres/1.0.0/recipe.yaml",
			platformHub + "/recipe/helm/postgres/1.0.0/recipe.yaml",
		},
	})
	if !errors.Is(err, catalog.ErrNoRecipeResolved()) {
		t.Fatalf("the drafts with no mapping built with %v; want the unmapped-instance halt", err)
	}
	if halted := haltedInstances(err); !slices.Equal(halted, []string{".spec.components.db"}) {
		t.Fatalf("the build halted naming %q; want exactly the db instance", halted)
	}
	// The finding is read off the group's own violation rather than off the frame
	// the build wraps it in, as haltedInstances reads its place.
	var group *resolver.MultiResolverError
	if !errors.As(err, &group) {
		t.Fatalf("the halt carries no group of findings: %v", err)
	}
	var finding string
	for _, v := range group.Violations {
		if errors.Is(v, catalog.ErrNoRecipeResolved()) && v.Field == ".spec.components.db" {
			finding = v.Error()
		}
	}
	for _, want := range []string{"compose/postgres:1.0.0, helm/postgres:1.0.0", "pin one of them in the Manifest"} {
		if !strings.Contains(finding, want) {
			t.Errorf("the finding for the db instance does not carry %q:\n%s", want, finding)
		}
	}
}

// TestATargetLeavesATypeWithRecipesOfSeveralNamesUnmapped imports a Visual Studio
// Code configuration holding a server started from a command line and one reached
// at an address — two instances of core/ai-tool/mcp, written with no version — for
// the compose target and for the helm target, and requires the one warning each
// draws for the type to give the reason the catalog offers Recipes of more than
// one name for that provider, naming each, rather than the reason that it offers
// none.
func TestATargetLeavesATypeWithRecipesOfSeveralNamesUnmapped(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		target     string
		references string
	}{
		{target: "compose", references: `("compose/mcp-remote", "compose/mcp-stdio-bridge")`},
		{target: "helm", references: `("helm/mcp-remote", "helm/mcp-stdio-bridge", "helm/mcp")`},
	} {
		t.Run(tc.target, func(t *testing.T) {
			t.Parallel()
			res, _ := targetedImport(t, unversionedTargetWorkspace(t, nil), ".vscode/mcp.json",
				readMCPFixture(t, "vscode.mcp.json"), tc.target, authoredcontent.DeliveryReport)
			if res.Format != "mcp" || res.Halted() {
				t.Fatalf("the source was read as %q, halted %v: %+v", res.Format, res.Halted(), res.Diagnostics)
			}
			want := []string{`the drafted Manifest leaves the component type "core/ai-tool/mcp" unmapped: the ` +
				`workspace's catalog holds more than one Recipe binding to the target "` + tc.target + `" that ` +
				`implements it ` + tc.references + `, and the import does not choose between them, so a build of the ` +
				`drafts halts naming each instance of it until the one wanted is mapped`}
			if got := unmappedWarnings(res); !slices.Equal(got, want) {
				t.Errorf("the import warned %q\nwant %q", got, want)
			}
		})
	}
}

// TestATargetMapsATypeDraftedWithNoVersionTheSameOnEveryRun imports the PostgreSQL
// application for the compose target twice, each over its own fresh workspace,
// and holds the two results to byte-identical files and diagnostics.
func TestATargetMapsATypeDraftedWithNoVersionTheSameOnEveryRun(t *testing.T) {
	t.Parallel()
	var runs [2]Result
	for i := range runs {
		runs[i], _ = targetedImport(t, unversionedTargetWorkspace(t, referencing()), "compose.yaml",
			[]byte(postgresComposeSource), "compose", authoredcontent.DeliveryReport)
	}
	if !slices.Equal(runs[0].Files, runs[1].Files) {
		t.Errorf("two runs drafted different files:\n%+v\n%+v", runs[0].Files, runs[1].Files)
	}
	if !slices.Equal(runs[0].Diagnostics, runs[1].Diagnostics) {
		t.Errorf("two runs reported different diagnostics:\n%+v\n%+v", runs[0].Diagnostics, runs[1].Diagnostics)
	}
}

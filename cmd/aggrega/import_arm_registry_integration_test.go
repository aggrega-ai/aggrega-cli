// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

// End-to-end proof, through the real command tree, that the Recipe an arm import
// drafts for an Azure Verified Modules module no row maps builds: the module's
// compiled template is pushed to a registry on loopback as Microsoft's registry
// serves it, the drafted Recipe's source is pointed at that registry, and
// `aggrega pin`, `aggrega pull` and `aggrega build` run over the drafts.

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

const (
	// armWrappedFixture is the committed template the case imports.
	armWrappedFixture = "../../internal/importops/testdata/arm/wrapped-module"
	// armModulesDir holds the compiled templates of the modules the fixtures
	// deploy, as the registry serves them.
	armModulesDir = "../../plugins/internal/armimport/testdata/avm/"
	// armWrappedModule and armWrappedVersion name the module the fixture deploys.
	armWrappedModule  = "kusto/cluster"
	armWrappedVersion = "0.11.1"
)

// avmMember is a member standing for the compiled template of a module the
// fixture deploys, written {"$avm": "<module>/<version>"}. It deliberately
// restates the member the importer's own tests expand, which a test of another
// module cannot import.
var avmMember = regexp.MustCompile(`\{\s*"\$avm":\s*"([^"]+)"\s*\}`)

// TestArmWrappedModuleBuildsFromALoopbackRegistry imports a template deploying
// the Azure Verified Modules module avm/res/kusto/cluster, whose resource type no
// row maps, and builds the drafts: the drafted Recipe sources the module from the
// registry the case serves it from, the build nests the pulled template with its
// telemetry off, and every output the Recipe provides is one the pinned module
// declares.
func TestArmWrappedModuleBuildsFromALoopbackRegistry(t *testing.T) {
	embeddedentities.Require(t)
	withCLIRegistrations(t, nil)
	importopstest.CarryImporters(t, armImporterURI)
	isolateFromThisMachine(t)

	module, err := os.ReadFile(filepath.Join(armModulesDir, armWrappedModule, armWrappedVersion, "main.json"))
	if err != nil {
		t.Fatalf("read the module's compiled template: %v", err)
	}
	var declared struct {
		Outputs map[string]json.RawMessage `json:"outputs"`
	}
	if err := json.Unmarshal(module, &declared); err != nil {
		t.Fatalf("decode the module's compiled template: %v", err)
	}

	host := startRegistry(t, nil)
	repository := "bicep/avm/res/" + armWrappedModule
	pushOCIModule(t, host, repository, armWrappedVersion, bicepModuleArtifactType,
		&ociLayer{MediaType: bicepConfigMediaType, Content: []byte("{}")},
		ociLayer{MediaType: bicepTemplateMediaType, Content: module},
		ociLayer{MediaType: bicepSourceMediaType, Content: []byte("source layer bytes that no output carries")})

	fixture, err := os.ReadFile(filepath.Join(armWrappedFixture, "main.json"))
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}
	template := avmMember.ReplaceAllLiteral(fixture, module)
	source := filepath.Join(t.TempDir(), "stack")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "main.json"), template, 0o644); err != nil {
		t.Fatal(err)
	}

	hub := siblingCatalogRootForExamples(t)
	root := armBuildWorkspace(t, hub)
	if code, stdout, commentary, process := runImportAtTheRoot(t, root, source); code != 0 {
		t.Fatalf("the import exited %d:\n%s\n%s\n%s", code, stdout, commentary, process)
	}
	t.Chdir(root)
	// The registry the module is pulled from is on loopback, which an offline
	// workspace refuses: the workspace is set online, naming no registry of its
	// own.
	importopstest.WriteConfiguration(t, root)

	// The drafted Recipe names the module at the registry Microsoft serves it
	// from; the case serves it from the loopback registry instead.
	recipe := filepath.Join(root, "catalog", "recipe", "arm", "microsoft-kusto-clusters", "0.1.0", "recipe.yaml")
	body := string(mustReadFile(t, recipe))
	const authoredURL = "oci://mcr.microsoft.com/bicep/avm/res/" + armWrappedModule
	if !strings.Contains(body, authoredURL) {
		t.Fatalf("the drafted Recipe does not name %s:\n%s", authoredURL, body)
	}
	writeFixture(t, recipe, strings.Replace(body, authoredURL, "oci://"+host+"/"+repository, 1))

	// Every output the Recipe provides, read off its expression, is one the
	// pinned module declares.
	provided := regexp.MustCompile(`outputs\.(\w+)\.value`).FindAllStringSubmatch(body, -1)
	if len(provided) == 0 {
		t.Fatalf("the drafted Recipe provides no output of the module:\n%s", body)
	}
	for _, m := range provided {
		if _, ok := declared.Outputs[m[1]]; !ok {
			t.Errorf("the Recipe provides the output %s, which the module at %s declares no such output of", m[1],
				armWrappedVersion)
		}
	}

	if err := pinRun(context.Background(), io.Discard, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), recipe, false); err != nil {
		t.Fatalf("pin the drafted Recipe: %s", cerrors.CauseChain(err))
	}
	mustRunDriftCommand(t, "the pull", "pull", "--release", "release.yaml")
	mustRunDriftCommand(t, "the validation of the Release", "validate", "--release", "release.yaml")
	mustRunDriftCommand(t, "the build", "build", "--release", "release.yaml", "--verify-determinism")

	built := mustReadFile(t, filepath.Join(root, "dist/arm/arm-template.json"))
	resources := armResources(t, built)
	if got, want := slices.Sorted(func(yield func(string) bool) {
		for k := range resources {
			if !yield(k) {
				return
			}
		}
	}), []string{"Cluster", "SiteDocs"}; !slices.Equal(got, want) {
		t.Fatalf("the template deploys %v, want %v", got, want)
	}
	wrapper := resources["Cluster"]
	if wrapper["type"] != "Microsoft.Resources/deployments" {
		t.Errorf("the module is deployed as %v, want a nested template resource", wrapper["type"])
	}
	nested := objectAt(t, wrapper, "properties", "template")
	var wantTemplate map[string]any
	if err := json.Unmarshal(module, &wantTemplate); err != nil {
		t.Fatal(err)
	}
	if nestedParameters, _ := nested["parameters"].(map[string]any); len(nestedParameters) != len(objectAt(t, wantTemplate, "parameters")) {
		t.Errorf("the nested template is not the module's compiled template")
	}
	parameters := objectAt(t, wrapper, "properties", "parameters")
	if got := objectAt(t, parameters, "enableTelemetry")["value"]; got != false {
		t.Errorf("the module's telemetry parameter is %v, want false", got)
	}
	if got := objectAt(t, parameters, "name")["value"]; got != "adx-core" {
		t.Errorf("the module's name parameter is %v, want adx-core", got)
	}
	site := objectAt(t, resources["SiteDocs"], "properties")
	if got, want := site["repositoryToken"], "[reference('Cluster').outputs.resourceId.value]"; got != want {
		t.Errorf("the consumer reads %v, want %v", got, want)
	}
}

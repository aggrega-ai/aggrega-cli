// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end coverage of the published arm catalog entities that build offline,
// read from the aggrega-hub repository checked out beside this one and built
// through the real pipeline and the real compiled Wasm guest.
//
// The Recipes that wrap a module of the Azure Verified Modules programme source
// a template from a pinned git repository, which a build without a lockfile
// cannot fetch; the renderer's tests hold those against a seeded copy of each
// module's parameter declarations. What builds here are the entities that carry
// their own template: the Key Vault secret FeatureRecipe and the API Management
// Recipe, whose output is read.
//
// These tests skip when the sibling catalog is absent.

package compiler_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// armPublishedDir is the fixture workspace: an Aggregate per case, one Manifest
// mapping the arm Recipes and the Key Vault secret FeatureRecipe, and a Release
// per case.
const armPublishedDir = "testdata/arm-published"

// armPublishedHub names a path inside the sibling catalog.
func armPublishedHub(p string) string { return flowEscapeHub + "/" + p }

// armPublishedCatalogPaths are the entities the cases resolve from the sibling
// catalog: the gateway and PostgreSQL Components with their arm Recipes, the
// secret Features with the Key Vault FeatureRecipe, the cloudformation
// FeatureRecipes that realize the two generating Features on another capability
// (so a halt on arm names what exists elsewhere), and the schema library their
// values reference. The rendering Plugin is the shared fixture's, whose guest
// resolves from the binary's embedded set.
func armPublishedCatalogPaths() []string {
	return []string{
		"testdata/arm-catalog/arm-renderer.plugin.yaml",
		armPublishedHub("component/core/gateway/azure-api-management/1.0.0/component.yaml"),
		armPublishedHub("component/core/datastore/postgres/1.0.0/component.yaml"),
		armPublishedHub("recipe/arm/azure-api-management/1.0.0/recipe.yaml"),
		armPublishedHub("recipe/arm/postgres-flexible-server/1.0.0/recipe.yaml"),
		armPublishedHub("feature/core/referenced-secret/1.0.0/feature.yaml"),
		armPublishedHub("feature/core/generated-secret/1.0.0/feature.yaml"),
		armPublishedHub("feature/core/random-password/1.0.0/feature.yaml"),
		armPublishedHub("feature-recipe/arm/key-vault-secret/1.0.0/recipe.yaml"),
		armPublishedHub("feature-recipe/cloudformation/generated-secret/1.0.0/recipe.yaml"),
		armPublishedHub("feature-recipe/cloudformation/random-password/1.0.0/recipe.yaml"),
		armPublishedHub("schemalibrary/base-components/1.0.0/library.yaml"),
	}
}

func armPublishedInput(t *testing.T, release string) pipelinetest.Input {
	t.Helper()
	wasmtest.SkipWithoutSiblings(t, flowEscapeHub)
	return pipelinetest.Input{
		ReleasePath:  filepath.Join(armPublishedDir, release),
		CatalogPaths: armPublishedCatalogPaths(),
	}
}

// TestArmGeneratingSecretFormsHaltByName builds a PostgreSQL database whose
// password slot takes each Feature that generates a secret. No arm FeatureRecipe
// realizes either: the template a Release deploys cannot hold a generated value
// without writing it, so the build halts at recipe resolution, with the halt
// naming the capability.
func TestArmGeneratingSecretFormsHaltByName(t *testing.T) {
	t.Parallel()

	for _, feature := range []string{"generated-secret", "random-password"} {
		t.Run(feature, func(t *testing.T) {
			t.Parallel()
			_, err := pipelinetest.BuildE(t, armPublishedInput(t, "release-postgres-"+feature+".yaml"))
			if err == nil {
				t.Fatalf("a build attaching core/%s on arm succeeded, want a halt at recipe resolution", feature)
			}
			if !errors.Is(err, catalog.ErrNoRecipeResolved()) {
				t.Fatalf("the build failed with %s, want a halt wrapping the no-recipe-resolved condition", cerrors.CauseChain(err))
			}
			chain := cerrors.CauseChain(err)
			for _, want := range []string{"core/" + feature, `"arm"`} {
				if !strings.Contains(chain, want) {
					t.Errorf("the halt does not name %s:\n%s", want, chain)
				}
			}
		})
	}
}

// armGatewayTemplate builds one gateway Release and returns the resources of the
// template it assembled.
func armGatewayTemplate(t *testing.T, release string) map[string]any {
	t.Helper()
	result := pipelinetest.Build(t, armPublishedInput(t, release))
	template, _ := findArmTemplate(t, result.Artifacts, "stack")
	return armSection(t, template, "resources")
}

// armResource returns one resource of a decoded template section by symbolic
// name, failing the test when it is absent.
func armResource(t *testing.T, resources map[string]any, symbol string) map[string]any {
	t.Helper()
	body, ok := resources[symbol].(map[string]any)
	if !ok {
		t.Fatalf("the template declares no resource %q; it declares %v", symbol, mapKeys(resources))
	}
	return body
}

// TestArmFragmentEntitiesBuild builds the API Management Recipe with and
// without a provider key and reads what it wrote. With the key attached through
// the Key Vault secret FeatureRecipe, the template writes a named value the
// instance reads from that secret and the backend sends it; without, it writes
// neither and the policy authenticates with the instance's managed identity. The
// instance itself is declared as an existing resource and never deployed.
func TestArmFragmentEntitiesBuild(t *testing.T) {
	t.Parallel()

	t.Run("provider key by Key Vault reference", func(t *testing.T) {
		t.Parallel()
		resources := armGatewayTemplate(t, "release-gateway-key.yaml")

		instance := armResource(t, resources, "GatewayInstance")
		if instance["existing"] != true {
			t.Errorf("the instance is declared %v, want an existing resource", instance["existing"])
		}
		named := armResource(t, resources, "GatewayNamedValue")
		props, _ := named["properties"].(map[string]any)
		keyVault, _ := props["keyVault"].(map[string]any)
		identifier, _ := keyVault["secretIdentifier"].(string)
		for _, want := range []string{"kv-models", "openai-key", "environment().suffixes.keyvaultDns"} {
			if !strings.Contains(identifier, want) {
				t.Errorf("the named value's secret identifier %q does not carry %q", identifier, want)
			}
		}
		if props["secret"] != true {
			t.Errorf("the named value is not marked secret: %v", props["secret"])
		}
		backend := armResource(t, resources, "GatewayBackend")
		backendProps, _ := backend["properties"].(map[string]any)
		credentials, _ := backendProps["credentials"].(map[string]any)
		header, _ := credentials["header"].(map[string]any)
		if _, sent := header["api-key"]; !sent {
			t.Errorf("the backend sends no api-key header; its credentials are %v", credentials)
		}
		policy := armResource(t, resources, "GatewayPolicy")
		policyProps, _ := policy["properties"].(map[string]any)
		if value, _ := policyProps["value"].(string); strings.Contains(value, "authentication-managed-identity") {
			t.Errorf("the policy authenticates with the managed identity while a provider key is attached:\n%s", value)
		}
		for _, symbol := range []string{"GatewayApi", "GatewayOperation"} {
			armResource(t, resources, symbol)
		}
	})

	t.Run("managed identity", func(t *testing.T) {
		t.Parallel()
		resources := armGatewayTemplate(t, "release-gateway-identity.yaml")

		for _, absent := range []string{"GatewayNamedValue"} {
			if _, present := resources[absent]; present {
				t.Errorf("the template declares %q with no provider key attached", absent)
			}
		}
		backend := armResource(t, resources, "GatewayBackend")
		backendProps, _ := backend["properties"].(map[string]any)
		if _, carries := backendProps["credentials"]; carries {
			t.Errorf("the backend carries credentials with no provider key attached: %v", backendProps["credentials"])
		}
		policy := armResource(t, resources, "GatewayPolicy")
		policyProps, _ := policy["properties"].(map[string]any)
		if value, _ := policyProps["value"].(string); !strings.Contains(value, "authentication-managed-identity") {
			t.Errorf("the policy does not authenticate with the managed identity:\n%s", value)
		}
	})
}

// armFeaturesReadPattern matches a read of `.features` that is not an exact leaf:
// any `.features` not continued by `.<name>`, which covers ranging over it,
// defaulting it to a dictionary and handing it whole to a function.
var armFeaturesReadPattern = regexp.MustCompile(`\.features(?:[^.A-Za-z_]|$)`)

// armImportedMapping resolves a `<Library>[:<version>]#/mappings/<Entry>` import
// to the template text the compiler renders for it, following an entry whose
// whole body is another field-root `$import:` until a terminal template. The
// library is the version a bare reference selects, from the catalog's own
// selection. problem is empty on success and otherwise says why the text cannot
// be read, so the caller reports it. The chain is followed here because the
// compiler's resolver is unexported in the renderer; it ends at the first
// repeated reference.
func armImportedMapping(libs map[string]types.MappingLibrary, ref string) (text, entryID, problem string) {
	seen := map[string]bool{}
	for {
		library, entry, ok := types.IdentityRefParts(ref, "/mappings/")
		if !ok || entry == "" {
			return "", entryID, fmt.Sprintf("imports %q, which is not a <Library>#/mappings/<Entry> reference", ref)
		}
		library, _, _ = strings.Cut(library, ":")
		entryID = library + "#/mappings/" + entry
		if seen[entryID] {
			return "", entryID, "imports a chain of entries that returns to " + entryID
		}
		seen[entryID] = true
		lib, found := libs[library]
		if !found {
			return "", entryID, "imports " + entryID + ", and no MappingLibrary named " + library + " is in the catalog"
		}
		mapping, found := lib.Spec.Mappings.Get(entry)
		if !found {
			return "", entryID, "imports " + entryID + ", which the library does not declare"
		}
		trimmed := strings.TrimSpace(mapping.Body)
		if !strings.HasPrefix(trimmed, "$import:") {
			return mapping.Body, entryID, ""
		}
		rest, _, _ := strings.Cut(strings.TrimPrefix(trimmed, "$import:"), "\n")
		ref = strings.TrimSpace(rest)
	}
}

// armWholeFeatureReads returns, for every Recipe and FeatureRecipe file under
// the arm directories of the catalog at hub, the files whose valuesMapping reads
// `.features` other than by exact leaf. A valuesMapping that imports a
// MappingLibrary entry is scanned as that entry's template text, and a finding
// names the Recipe and the entry. A file that does not parse, whose
// valuesMapping is neither a string nor such an import, or whose import names a
// library or entry that is absent is reported as a finding as well, so a file the
// scan cannot read never reads as clean.
func armWholeFeatureReads(hub string) (findings []string, scanned int, err error) {
	// Entries that fail to index are passed over; an import that needed one is
	// then reported as unresolvable.
	cat, _, err := catalog.LoadPassOneTolerant(hub, "", hub)
	if err != nil {
		return nil, 0, err
	}
	libraries := cat.DefaultLibraries().Mappings
	for _, dir := range []string{"recipe/arm", "feature-recipe/arm"} {
		root := filepath.Join(hub, dir)
		if _, statErr := os.Stat(root); errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || d.Name() != "recipe.yaml" {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			scanned++
			var doc struct {
				Spec struct {
					ValuesMapping any `yaml:"valuesMapping"`
				} `yaml:"spec"`
			}
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				findings = append(findings, fmt.Sprintf("%s does not parse: %v", path, err))
				return nil
			}
			switch mapping := doc.Spec.ValuesMapping.(type) {
			case nil:
			case string:
				if loc := armFeaturesReadPattern.FindStringIndex(mapping); loc != nil {
					findings = append(findings, fmt.Sprintf("%s: valuesMapping reads .features other than by exact leaf, at %q", path, mapping[loc[0]:min(len(mapping), loc[1]+24)]))
				}
			case map[string]any:
				ref, _ := mapping["$import"].(string)
				if len(mapping) != 1 || ref == "" {
					findings = append(findings, fmt.Sprintf("%s: valuesMapping is a map the scan cannot read: %v", path, mapping))
					return nil
				}
				text, entryID, problem := armImportedMapping(libraries, ref)
				if problem != "" {
					findings = append(findings, fmt.Sprintf("%s: valuesMapping %s", path, problem))
					return nil
				}
				if loc := armFeaturesReadPattern.FindStringIndex(text); loc != nil {
					findings = append(findings, fmt.Sprintf("%s: valuesMapping imports %s, which reads .features other than by exact leaf, at %q", path, entryID, text[loc[0]:min(len(text), loc[1]+24)]))
				}
			default:
				findings = append(findings, fmt.Sprintf("%s: valuesMapping is a %T, which the scan cannot read", path, mapping))
			}
			return nil
		})
		if walkErr != nil {
			return nil, scanned, walkErr
		}
	}
	sort.Strings(findings)
	return findings, scanned, nil
}

// TestArmRecipesReadAttachmentsByExactLeaf holds that no arm Recipe's
// valuesMapping ranges over, defaults or passes on `.features` whole. A mapping
// that reads the attachments as a set reaches the leaves their Features declare
// sensitive and is then refused every literal it produces, so each Recipe reads
// the attachment it needs by exact leaf. A fragment Recipe's artifact is not a
// valuesMapping and may test an attachment's presence.
//
// The scan proves itself first: it runs over a copy of the tree holding a Recipe
// whose mapping ranges over `.features`, and must name that file, and over a copy
// holding a Recipe that reads one leaf, and must not. The same holds for a
// Recipe importing a MappingLibrary entry that ranges over `.features`, one that
// reads a leaf, one that chains to a ranging entry, and one naming an entry no library declares.
func TestArmRecipesReadAttachmentsByExactLeaf(t *testing.T) {
	t.Parallel()

	planted := t.TempDir()
	write := func(rel, mapping string) string {
		path := filepath.Join(planted, rel, "recipe.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating the planted directory: %v", err)
		}
		body := "spec:\n  valuesMapping: |\n    " + strings.ReplaceAll(mapping, "\n", "\n    ") + "\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("writing the planted Recipe: %v", err)
		}
		return path
	}
	ranged := write("recipe/arm/ranged/1.0.0", "{{- range $name, $_ := (default dict .features) }}\nkind: {{ $name }}\n{{- end }}")
	defaulted := write("recipe/arm/defaulted/1.0.0", "{{- $f := default dict .features }}\nkind: x")
	exact := write("recipe/arm/exact/1.0.0", "kind: \"{{ .features.password.type }}\"\nsecret: \"{{ .features.password.provides.name }}\"")
	writeImport := func(rel, ref string) string {
		path := filepath.Join(planted, rel, "recipe.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating the planted directory: %v", err)
		}
		body := "spec:\n  valuesMapping:\n    $import: " + ref + "\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("writing the planted Recipe: %v", err)
		}
		return path
	}
	libraryPath := filepath.Join(planted, "mappinglibrary", "planted-mappings", "1.0.0", "library.yaml")
	if err := os.MkdirAll(filepath.Dir(libraryPath), 0o755); err != nil {
		t.Fatalf("creating the planted directory: %v", err)
	}
	library := "apiVersion: aggrega.ai/v1beta1\nkind: MappingLibrary\nmetadata:\n  name: PlantedMappings\n  version: 1.0.0\nspec:\n  mappings:\n" +
		"    RangesFeatures: |\n      {{- range $name, $_ := .features }}\n      kind: {{ $name }}\n      {{- end }}\n" +
		"    ReadsLeaf: |\n      kind: \"{{ .features.password.type }}\"\n" +
		"    ChainsToRange: |\n      $import: PlantedMappings#/mappings/RangesFeatures\n"
	if err := os.WriteFile(libraryPath, []byte(library), 0o644); err != nil {
		t.Fatalf("writing the planted MappingLibrary: %v", err)
	}
	importRanged := writeImport("recipe/arm/import-ranged/1.0.0", "PlantedMappings#/mappings/RangesFeatures")
	importExact := writeImport("recipe/arm/import-exact/1.0.0", "PlantedMappings#/mappings/ReadsLeaf")
	importChained := writeImport("recipe/arm/import-chained/1.0.0", "PlantedMappings#/mappings/ChainsToRange")
	importMissing := writeImport("recipe/arm/import-missing/1.0.0", "PlantedMappings#/mappings/NoSuchEntry")
	findings, scanned, err := armWholeFeatureReads(planted)
	if err != nil {
		t.Fatalf("scanning the planted tree: %v", err)
	}
	if scanned != 7 {
		t.Fatalf("the scan read %d of the 7 planted files", scanned)
	}
	text := strings.Join(findings, "\n")
	for _, plantedFile := range []string{ranged, defaulted, importRanged, importChained, importMissing} {
		if !strings.Contains(text, plantedFile) {
			t.Fatalf("the scan does not name the planted match %s, so a clean result proves nothing:\n%s", plantedFile, text)
		}
	}
	for _, cleanFile := range []string{exact, importExact} {
		if strings.Contains(text, cleanFile) {
			t.Fatalf("the scan names a mapping that reads one leaf by exact path, %s:\n%s", cleanFile, text)
		}
	}

	wasmtest.SkipWithoutSiblings(t, flowEscapeHub)
	hub := wasmtest.SiblingPath(t, "aggrega-hub", "aggrega-catalog")
	findings, scanned, err = armWholeFeatureReads(hub)
	if err != nil {
		t.Fatalf("scanning the sibling catalog: %v", err)
	}
	if scanned == 0 {
		t.Fatalf("the scan found no arm Recipe under %s", hub)
	}
	for _, finding := range findings {
		t.Error(finding)
	}
}

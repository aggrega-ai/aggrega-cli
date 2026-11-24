// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer_test

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// These tests render each arm Recipe of the sibling catalog that wraps a module of
// the Azure Verified Modules programme, sourced from a git repository or a Bicep
// module in an OCI registry, through RenderAll and the embedded arm
// guest. The Recipe is loaded from its own YAML, so its locals, assertions and
// valuesMapping are the published ones; only its source is replaced, by a git
// repository seeded in the cache as a pull leaves it, holding the fixture under
// testdata/arm-published.
//
// Each fixture is a deliberate near-duplicate of the parameter declarations of
// the module its Recipe pins, kept so these tests run offline. It is not the
// module: the real module is proven by building the example whose output is
// committed. The fixture exists so that every key a Recipe binds is checked
// against a declaration of the module at the tag the Recipe pins.

// armPublishedKeyVaultSecret is the Key Vault secret an attachment on the
// password slot names, written as the resource ID the Aggregate writes.
const armPublishedKeyVaultSecret = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-secrets/providers/Microsoft.KeyVault/vaults/kv-app/secrets/db-password"

// armPublishedCase is one published Recipe and the node that renders it.
type armPublishedCase struct {
	// recipe is the Recipe's directory under recipe/arm of the catalog.
	recipe string
	// fixture is the directory under testdata/arm-published that holds the
	// module's declarations the Recipe is rendered against.
	fixture string
	// infra and values are the node's resolved infra and values, as JSON, written
	// the way the Component's defaults and the Release leave them.
	infra, values string
	// password attaches core/referenced-secret on a password slot, realized by
	// the published Key Vault secret FeatureRecipe.
	password bool
	// passwordSlot names the slot the attachment fills; the empty string is
	// "password".
	passwordSlot string
	// mappingLibrary is the directory under mappinglibrary of the catalog that
	// holds the MappingLibrary the Recipe's valuesMapping imports; empty when the
	// Recipe's mapping is its own.
	mappingLibrary string
	// node names the component the Recipe renders; the empty string is "api".
	node string
}

// armPublishedCases lists every arm Recipe that sources an Azure Verified Modules
// module. TestRenderAll_ArmPublishedRecipesAreAllCovered holds that the list
// names every such Recipe the catalog carries.
func armPublishedCases() []armPublishedCase {
	const environment = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-apps/providers/Microsoft.App/managedEnvironments/env-shared"
	return []armPublishedCase{
		{
			recipe:   "postgres-flexible-server",
			fixture:  "flexible-server",
			password: true,
			infra: `{"engine_version":"16","port":5432,"storage":{"size":"32Gi"},"ha":{"enabled":false,"read_replicas":0},
				"credentials":"generated","credentials_secret":"",
				"overrides":{"delegated_subnet_id":"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-net/providers/Microsoft.Network/virtualNetworks/vnet/subnets/db",
				"private_dns_zone_id":"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-net/providers/Microsoft.Network/privateDnsZones/db.postgres.database.azure.com",
				"sku_name":"GP_Standard_D2ds_v5","zone":"1","tags":{"env":"test"}}}`,
			values: `{"user":"appadmin","database":"orders","encoding":"UTF8","locale":"en_US.utf8"}`,
		},
		{
			recipe:       "mysql-flexible-server",
			fixture:      "mysql-flexible-server",
			password:     true,
			passwordSlot: "root_password",
			infra: `{"engine_version":"8.4","port":3306,"storage":{"size":"32Gi"},"ha":{"enabled":false,"read_replicas":0},
				"overrides":{"delegated_subnet_id":"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-net/providers/Microsoft.Network/virtualNetworks/vnet/subnets/db",
				"private_dns_zone_id":"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-net/providers/Microsoft.Network/privateDnsZones/db.mysql.database.azure.com",
				"sku_name":"GP_Standard_D2ds_v4","zone":"1","tags":{"env":"test"}}}`,
			values: `{"user":"appadmin","database":"orders","charset":"utf8mb4","collation":"utf8mb4_general_ci"}`,
		},
		{
			recipe:         "task-container-apps",
			fixture:        "container-app-job",
			mappingLibrary: "container-app-parameters",
			infra: `{"image":"registry.example.com/orders-migrate:1.0.0","compute":{"cpu":"250m","memory":"512Mi"},
				"overrides":{"container_app_environment_id":"` + environment + `"}}`,
			values: `{"schedule":"30 2 * * 1-5","timeout":"10m"}`,
		},
		{
			recipe:  "server-container-apps",
			fixture: "container-app",
			infra: `{"image":"registry.example.com/orders:1.0.0","port":8080,"replicas":2,"compute":{"cpu":"500m","memory":"1Gi"},
				"overrides":{"container_app_environment_id":"` + environment + `"}}`,
			values: `{}`,
		},
		{
			recipe:  "worker-container-apps",
			fixture: "container-app",
			infra: `{"image":"registry.example.com/orders-worker:1.0.0","replicas":1,"compute":{"cpu":"250m","memory":"512Mi"},
				"overrides":{"container_app_environment_id":"` + environment + `"}}`,
			values: `{"concurrency":4}`,
		},
		{
			recipe:  "redis-azure-managed",
			fixture: "redis-enterprise",
			infra:   `{"port":6379,"client_tls":true,"overrides":{"sku_name":"Balanced_B5","high_availability_enabled":true}}`,
			values:  `{"maxmemory_policy":"allkeys-lru","appendonly":false,"persistence":{"enabled":false}}`,
		},
		{
			recipe:   "sql-server-azure-sql-database",
			fixture:  "sql-server-azure-sql-database",
			password: true,
			infra:    `{"port":1433,"engine_version":"2022","storage":{"size":"32Gi"},"ha":{"enabled":false},"overrides":{}}`,
			values:   `{"database":"orders","user":"appadmin"}`,
		},
		{
			recipe:  "cosmos-db",
			fixture: "cosmos-db",
			infra:   `{"ha":{"enabled":false},"overrides":{}}`,
			values:  `{"database":"orders","containers":{"items":{"partition_key_paths":["/tenantId"]}},"consistency_level":"Session"}`,
		},
		{
			recipe:  "azure-ai-search",
			fixture: "azure-ai-search",
			infra:   `{"overrides":{}}`,
			values:  `{"semantic_search":"free"}`,
		},
		{
			recipe:  "azure-application-insights",
			fixture: "azure-application-insights",
			infra:   `{"overrides":{"workspace_id":"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-logs/providers/Microsoft.OperationalInsights/workspaces/logs-shared"}}`,
			values:  `{}`,
		},
		{
			recipe:  "azure-container-registry",
			fixture: "azure-container-registry",
			node:    "ordersregistry",
			infra:   `{"overrides":{}}`,
			values:  `{}`,
		},
		{
			recipe:  "azure-databricks",
			fixture: "azure-databricks",
			infra:   `{"overrides":{}}`,
			values:  `{}`,
		},
		{
			recipe:  "azure-event-grid",
			fixture: "azure-event-grid",
			infra:   `{"overrides":{}}`,
			values:  `{"input_schema":"EventGridSchema"}`,
		},
		{
			recipe:  "azure-key-vault",
			fixture: "azure-key-vault",
			infra:   `{"overrides":{}}`,
			values:  `{}`,
		},
		{
			recipe:  "azure-logic-apps",
			fixture: "azure-logic-apps",
			infra:   `{"overrides":{}}`,
			values:  `{"definition":{}}`,
		},
		{
			recipe:  "azure-service-bus",
			fixture: "azure-service-bus",
			node:    "ordersbus",
			infra:   `{"overrides":{}}`,
			values:  `{"queues":{"orders":{"max_delivery_count":5}},"topics":{"events":{"subscriptions":{"audit":{}}}}}`,
		},
		{
			recipe:  "azure-openai",
			fixture: "cognitive-services-account",
			infra:   `{"overrides":{"custom_subdomain_name":"orders-openai","sku_name":"Standard","sku_capacity":10,"model_version":"2024-11-20","public_network_access_enabled":false}}`,
			values:  `{"model":"gpt-4o","deployment_name":"chat"}`,
		},
		{
			recipe:  "azure-openai-embedding",
			fixture: "cognitive-services-account",
			infra:   `{"overrides":{"custom_subdomain_name":"orders-embeddings","sku_name":"Standard","sku_capacity":5}}`,
			values:  `{"model":"text-embedding-3-small","deployment_name":"embed"}`,
		},
	}
}

// armPublishedRecipe loads one arm Recipe from the sibling catalog, with its
// source, a git repository or a Bicep module in an OCI registry, replaced by the
// seeded repository. It fails the test when the Recipe
// does not source a module, since a Recipe without a source would render nothing
// of the fixture.
func armPublishedRecipe(t *testing.T, c armPublishedCase) *types.Recipe {
	t.Helper()
	dir := wasmtest.SiblingPath(t, "aggrega-hub", "aggrega-catalog", "recipe", "arm", c.recipe, "1.0.0")
	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("loading the Recipe %s: %v", c.recipe, err)
	}
	if len(cat.Recipes) != 1 {
		t.Fatalf("the directory of the Recipe %s holds %d Recipes, want one", c.recipe, len(cat.Recipes))
	}
	var recipe types.Recipe
	for _, r := range cat.Recipes {
		recipe = r
	}
	source, ok := recipe.Spec.Source.(map[string]any)
	if !ok || (source["kind"] != "git-repository" && source["kind"] != "bicep-module-oci") {
		t.Fatalf("the Recipe %s sources neither a git repository nor a Bicep module in an OCI registry: %v", c.recipe, recipe.Spec.Source)
	}
	recipe.Spec.Source = map[string]any{"kind": "git-repository", "url": armSourcedURL, "ref": armSourcedRef}
	return &recipe
}

// armPublishedFixture reads one fixture's main.json.
func armPublishedFixture(t *testing.T, fixture string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "arm-published", fixture, "main.json"))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	return string(body)
}

// armPublishedMappingLibraries loads the published MappingLibrary in the given
// directory of the catalog, keyed by name as a valuesMapping import names it.
func armPublishedMappingLibraries(t *testing.T, dir string) map[string]types.MappingLibrary {
	t.Helper()
	cat, err := catalog.LoadAll("", wasmtest.SiblingPath(t, "aggrega-hub", "aggrega-catalog", "mappinglibrary", dir, "1.0.0"))
	if err != nil {
		t.Fatalf("loading the MappingLibrary %s: %v", dir, err)
	}
	libraries := map[string]types.MappingLibrary{}
	for _, library := range cat.MappingLibraryVersions {
		libraries[library.Metadata.Name] = library
	}
	if len(libraries) == 0 {
		t.Fatalf("the directory of the MappingLibrary %s holds none", dir)
	}
	return libraries
}

// armPublishedPassword attaches core/referenced-secret to the node "api" on the
// case's password slot, realized by the published Key Vault secret FeatureRecipe, and
// returns the FeatureRecipe lookup.
func armPublishedPassword(t *testing.T, slot string, input *renderer.RendererInput) func(string, string) (*types.FeatureRecipe, bool) {
	t.Helper()
	feature := wasmtest.SiblingPath(t, "aggrega-hub", "aggrega-catalog", "feature", "core", "referenced-secret", "1.0.0")
	realization := wasmtest.SiblingPath(t, "aggrega-hub", "aggrega-catalog", "feature-recipe", "arm", "key-vault-secret", "1.0.0")
	cat, err := catalog.LoadAll("", feature, realization)
	if err != nil {
		t.Fatalf("loading the Feature and its FeatureRecipe: %v", err)
	}
	manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{
		Features: &types.MappingTier{},
	}}}
	manifest.Spec.Mappings.Features.Type.Set("core/referenced-secret:1.0.0", "key-vault-secret:1.0.0")
	lookup := cat.RecipeLookupWithManifest(manifest).FeatureRecipe

	values := types.OrderedMap[any]{}
	values.Set("origin", armPublishedKeyVaultSecret)
	values.Set("key", "value")
	instances := types.OrderedMap[types.IRFeatureEntry]{}
	instances.Set(slot, types.IRFeatureEntry{Type: "core/referenced-secret:1.0.0", Sensitive: true, Values: values})
	input.Graph.Spec.FeatureValuesMapping.Set("api", instances)
	input.FeatureOrder = ir.FeatureOrder{"api": {slot}}
	input.CatalogLookup = cat.CatalogLookup()
	return lookup
}

// renderArmPublished renders the case's Recipe as the node "api" and returns what
// the build assembled.
func renderArmPublished(t *testing.T, c armPublishedCase) (map[string]renderer.AssembledFile, error) {
	t.Helper()
	recipe := armPublishedRecipe(t, c)
	node := cmp.Or(c.node, "api")
	componentType, _, _ := strings.Cut(recipe.Spec.Implements, ":")

	root := t.TempDir()
	blob := buildIntegrationTGZ(t, map[string]string{"main.json": armPublishedFixture(t, c.fixture)})
	digest := integrationDigest(blob)
	seedIntegrationCacheSlot(t, root, "git-repository", armSourcedURL, armSourcedCommit, blob, digest)

	input := integrationInput(t, root, componentType, node, "arm", digest, armSourcedURL, armSourcedCommit)
	row := &input.Lockfile.SourceArtifacts[0]
	row.Kind = "git-repository"
	row.Mediatype = "application/gzip"
	row.ReferencedBy[0].RequestedRef = armSourcedRef
	input.GraphView = derivedKeyGraphFacts{}
	input.Graph.Spec.ValuesMapping.Set(node, json.RawMessage(c.values))
	input.Graph.Spec.InfraMapping.Set(node, json.RawMessage(c.infra))

	lookup := renderer.RecipeLookup{
		Recipe: func(_, typeURI string) (*types.Recipe, bool) {
			if typeURI != componentType {
				return nil, false
			}
			return recipe, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
	if c.mappingLibrary != "" {
		input.MappingLibraries = armPublishedMappingLibraries(t, c.mappingLibrary)
	}
	if c.password {
		lookup.FeatureRecipe = armPublishedPassword(t, cmp.Or(c.passwordSlot, "password"), &input)
	}
	result, err := renderer.RenderAll(context.Background(), input, lookup, wasmEngineFactory(t))
	if err != nil {
		return nil, err
	}
	return result.Files, nil
}

// armFixtureParameters returns the parameter names a fixture declares, as the
// module spells them.
func armFixtureParameters(t *testing.T, fixture string) []string {
	t.Helper()
	var doc struct {
		Parameters map[string]json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(armPublishedFixture(t, fixture)), &doc); err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	names := make([]string, 0, len(doc.Parameters))
	for name := range doc.Parameters {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// TestRenderAll_ArmPublishedRecipesRender renders each published AVM-wrapping
// Recipe against its fixture. The build assembles one nested deployment, every
// key the Recipe's valuesMapping binds names a parameter the fixture declares
// (read case-insensitively, as ARM reads parameter names), and no parameter the
// Recipe binds is left out of the nested deployment's parameters.
func TestRenderAll_ArmPublishedRecipesRender(t *testing.T) {
	t.Parallel()
	wasmtest.SkipWithoutSiblings(t, "../../../../aggrega-hub/aggrega-catalog")

	for _, c := range armPublishedCases() {
		t.Run(c.recipe, func(t *testing.T) {
			t.Parallel()
			files, err := renderArmPublished(t, c)
			if err != nil {
				t.Fatalf("rendering %s: %s", c.recipe, cerrors.CauseChain(err))
			}
			declared := map[string]bool{}
			for _, name := range armFixtureParameters(t, c.fixture) {
				declared[strings.ToLower(name)] = true
			}
			node := cmp.Or(c.node, "api")
			bound := armParametersOfAs(t, files, strings.ToUpper(node[:1])+node[1:])
			if len(bound) == 0 {
				t.Fatalf("the nested deployment of %s binds no parameter", c.recipe)
			}
			for key := range bound {
				if !declared[strings.ToLower(key)] {
					t.Errorf("%s binds %q, which the module's declarations at the tag it pins do not name", c.recipe, key)
				}
			}
			if _, bound := bound["name"]; !bound {
				t.Errorf("%s binds no name for the resource it deploys", c.recipe)
			}
		})
	}
}

// TestRenderAll_ArmPublishedRecipesAreAllCovered holds that armPublishedCases
// names every arm Recipe of the catalog that sources a module, so a Recipe added
// without a case here fails instead of going unrendered.
func TestRenderAll_ArmPublishedRecipesAreAllCovered(t *testing.T) {
	t.Parallel()
	root := wasmtest.SiblingPath(t, "aggrega-hub", "aggrega-catalog", "recipe", "arm")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("listing the arm Recipes: %v", err)
	}
	listed := map[string]bool{}
	for _, c := range armPublishedCases() {
		listed[c.recipe] = true
	}
	sourcing := 0
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(root, entry.Name(), "1.0.0", "recipe.yaml"))
		if err != nil {
			t.Fatalf("reading the Recipe %s: %v", entry.Name(), err)
		}
		if !strings.Contains(string(raw), "\n  source:\n") {
			continue
		}
		sourcing++
		if !listed[entry.Name()] {
			t.Errorf("the arm Recipe %s sources a module and has no case in armPublishedCases", entry.Name())
		}
	}
	if sourcing == 0 {
		t.Fatalf("no arm Recipe under %s sources a module, so the coverage check read nothing", root)
	}
	for name := range listed {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("armPublishedCases names %s, which the catalog does not carry: %v", name, err)
		}
	}
}

// TestArmPublishedRecipesPinTelemetryOff holds that every nested deployment a published
// Recipe renders passes enableTelemetry false to its module, which would
// otherwise deploy a resource that reports the module and its version to
// Microsoft. The fixture declares the parameter true by default, so a Recipe that
// left it out would render a template that turned telemetry on.
func TestArmPublishedRecipesPinTelemetryOff(t *testing.T) {
	t.Parallel()
	wasmtest.SkipWithoutSiblings(t, "../../../../aggrega-hub/aggrega-catalog")

	for _, c := range armPublishedCases() {
		t.Run(c.recipe, func(t *testing.T) {
			t.Parallel()
			declared := armFixtureParameters(t, c.fixture)
			if !slices.Contains(declared, "enableTelemetry") {
				t.Fatalf("the fixture for %s declares no enableTelemetry, so the check would pass on a module that has none", c.recipe)
			}
			files, err := renderArmPublished(t, c)
			if err != nil {
				t.Fatalf("rendering %s: %s", c.recipe, cerrors.CauseChain(err))
			}
			resources, _ := armAssembledTemplate(t, files)["resources"].(map[string]any)
			nested := 0
			for symbol, raw := range resources {
				body, _ := raw.(map[string]any)
				if body["type"] != "Microsoft.Resources/deployments" {
					continue
				}
				nested++
				properties, _ := body["properties"].(map[string]any)
				parameters, _ := properties["parameters"].(map[string]any)
				telemetry, present := parameters["enableTelemetry"].(map[string]any)
				if !present {
					t.Errorf("the nested deployment %s of %s binds no enableTelemetry", symbol, c.recipe)
					continue
				}
				if telemetry["value"] != false {
					t.Errorf("the nested deployment %s of %s binds enableTelemetry %v, want false", symbol, c.recipe, telemetry["value"])
				}
			}
			if nested == 0 {
				t.Fatalf("%s rendered no nested deployment", c.recipe)
			}
		})
	}
}

// TestRenderAll_ArmRedisInstanceNameLimit holds the instance-name rule of the
// Azure Managed Redis Recipe: the service checks the name together with the
// region's name against 63 characters, so a name of 45 characters is refused
// and one of 44 renders.
func TestRenderAll_ArmRedisInstanceNameLimit(t *testing.T) {
	t.Parallel()
	wasmtest.SkipWithoutSiblings(t, "../../../../aggrega-hub/aggrega-catalog")

	var base armPublishedCase
	for _, c := range armPublishedCases() {
		if c.recipe == "redis-azure-managed" {
			base = c
		}
	}
	if base.recipe == "" {
		t.Fatal("armPublishedCases has no case for redis-azure-managed")
	}

	t.Run("45 characters is refused", func(t *testing.T) {
		t.Parallel()
		c := base
		c.node = strings.Repeat("a", 45)
		_, err := renderArmPublished(t, c)
		if err == nil {
			t.Fatal("a 45-character instance name rendered, want it refused")
		}
		chain := cerrors.CauseChain(err)
		t.Logf("refusal: %s", chain)
		if !strings.Contains(chain, "an instance name holds up to 44 letters, digits and hyphens") {
			t.Fatalf("the refusal does not name the 44-character limit: %s", chain)
		}
	})
	t.Run("44 characters renders", func(t *testing.T) {
		t.Parallel()
		c := base
		c.node = strings.Repeat("a", 44)
		if _, err := renderArmPublished(t, c); err != nil {
			t.Fatalf("a 44-character instance name was refused: %s", cerrors.CauseChain(err))
		}
	})
}

// terraformRedisVersions are the versions of the terraform Azure Managed Redis
// Recipe whose instance-name rule TestRenderAll_TerraformRedisInstanceNameLimit
// holds; every one carries the same rule.
var terraformRedisVersions = []string{"1.0.1", "1.0.2"}

// renderTerraformRedis renders the published terraform Azure Managed Redis
// Recipe at the given version as a component named node and returns what the build assembled.
func renderTerraformRedis(t *testing.T, version, node string) (map[string]renderer.AssembledFile, error) {
	t.Helper()
	dir := wasmtest.SiblingPath(t, "aggrega-hub", "aggrega-catalog", "recipe", "terraform", "redis-azure-managed", version)
	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("loading the Recipe: %v", err)
	}
	if len(cat.Recipes) != 1 {
		t.Fatalf("the directory of the Recipe holds %d Recipes, want one", len(cat.Recipes))
	}
	var recipe types.Recipe
	for _, r := range cat.Recipes {
		recipe = r
	}
	componentType, _, _ := strings.Cut(recipe.Spec.Implements, ":")

	input := integrationInput(t, t.TempDir(), componentType, node, "terraform", "", "", "")
	input.Lockfile.SourceArtifacts = nil
	input.GraphView = derivedKeyGraphFacts{}
	input.Graph.Spec.ValuesMapping.Set(node, json.RawMessage(`{"maxmemory_policy":"allkeys-lru","appendonly":false,"persistence":{"enabled":false}}`))
	input.Graph.Spec.InfraMapping.Set(node, json.RawMessage(`{"port":6379,"client_tls":true,"overrides":{"resource_group_name":"rg-cache","location":"westeurope","sku_name":"Balanced_B5"}}`))

	lookup := renderer.RecipeLookup{
		Recipe: func(_, typeURI string) (*types.Recipe, bool) {
			if typeURI != componentType {
				return nil, false
			}
			return &recipe, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
	result, err := renderer.RenderAll(context.Background(), input, lookup, wasmEngineFactory(t))
	if err != nil {
		return nil, err
	}
	return result.Files, nil
}

// TestRenderAll_TerraformRedisInstanceNameLimit holds the instance-name rule of
// the terraform Azure Managed Redis Recipe: the service checks the name together
// with the region's name against 63 characters, so a name of 45 characters is
// refused and one of 44 renders, as with the arm Recipe.
func TestRenderAll_TerraformRedisInstanceNameLimit(t *testing.T) {
	t.Parallel()
	wasmtest.SkipWithoutSiblings(t, "../../../../aggrega-hub/aggrega-catalog")

	for _, version := range terraformRedisVersions {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			t.Run("45 characters is refused", func(t *testing.T) {
				t.Parallel()
				_, err := renderTerraformRedis(t, version, strings.Repeat("a", 45))
				if err == nil {
					t.Fatal("a 45-character instance name rendered, want it refused")
				}
				chain := cerrors.CauseChain(err)
				t.Logf("refusal: %s", chain)
				if !strings.Contains(chain, "an instance name holds up to 44 letters, digits and hyphens") {
					t.Fatalf("the refusal does not name the 44-character limit: %s", chain)
				}
			})
			t.Run("44 characters renders", func(t *testing.T) {
				t.Parallel()
				files, err := renderTerraformRedis(t, version, strings.Repeat("a", 44))
				if err != nil {
					t.Fatalf("a 44-character instance name was refused: %s", cerrors.CauseChain(err))
				}
				if len(files) == 0 {
					t.Fatal("a 44-character instance name rendered no file")
				}
			})
		})
	}
}

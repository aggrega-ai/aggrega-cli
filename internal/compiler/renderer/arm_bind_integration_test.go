// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// These tests drive an arm Recipe that sources a compiled ARM template fetched from
// a git repository, seeded in the cache as a pull leaves it, through RenderAll and
// the embedded arm guest. The guest wraps the template as one nested deployment and
// binds the Recipe's valuesMapping to that deployment's parameters; the build
// assembles the resource group template a Release deploys.

const (
	armSourcedURL    = "https://example.com/arm/templates.git"
	armSourcedRef    = "v1.0.0"
	armSourcedCommit = "fedcba9876543210fedcba9876543210fedcba98"

	// armSourcedFile is the file the build writes the assembled template under.
	armSourcedFile = "arm-template.json"
	// armSourcedSymbol is the nested deployment's symbolic name for the node "api".
	armSourcedSymbol = "Api"
)

// armBindTemplate declares one parameter of each kind the binding converts, a
// parameter with a default no key names, a securestring and a nullable one.
const armBindTemplate = `{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
  "languageVersion": "2.0",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "sku": {"type": "string", "allowedValues": ["Standard_B1ms", "Standard_B2s"]},
    "capacity": {"type": "int", "minValue": 1, "maxValue": 8},
    "zoneRedundant": {"type": "bool"},
    "tags": {"type": "object"},
    "adminPassword": {"type": "securestring"},
    "location": {"type": "string", "defaultValue": "[resourceGroup().location]"},
    "comment": {"type": "string", "nullable": true}
  },
  "resources": {
    "server": {
      "type": "Microsoft.DBforPostgreSQL/flexibleServers",
      "apiVersion": "2024-08-01",
      "name": "[format('db-{0}', uniqueString(resourceGroup().id))]",
      "location": "[parameters('location')]",
      "sku": {"name": "[parameters('sku')]"}
    }
  }
}
`

// armSecretTemplate declares a parameter for each value a Feature attachment
// supplies and one every mapping binds as a literal.
const armSecretTemplate = `{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
  "languageVersion": "2.0",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "kind": {"type": "string"},
    "vaultSecret": {"type": "string"},
    "enableTelemetry": {"type": "bool"}
  },
  "resources": {
    "server": {
      "type": "Microsoft.DBforPostgreSQL/flexibleServers",
      "apiVersion": "2024-08-01",
      "name": "db",
      "location": "[resourceGroup().location]"
    }
  }
}
`

// armSourcedCase is one render of the node "api" whose arm Recipe sources a
// template from the seeded git repository.
type armSourcedCase struct {
	// files are the repository's files, by path; the template is main.json.
	files map[string]string
	// mapping is the Recipe's valuesMapping, empty for none.
	mapping string
	// omits are the Recipe's omits entries.
	omits []types.OmitEntry
	// secret attaches a core/referenced-secret Feature named "password" to the
	// node, whose arm FeatureRecipe is the fixture's, so the node's context
	// carries an attachment whose origin its Feature declares sensitive.
	secret bool
}

// armSourcedRecipe is the node's Recipe.
func armSourcedRecipe(c armSourcedCase) *types.Recipe {
	return &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Capability:    "arm",
			Implements:    "core/compute/api-server",
			Source:        map[string]any{"kind": "git-repository", "url": armSourcedURL, "ref": armSourcedRef},
			ValuesMapping: types.ValuesMapping{Inline: c.mapping},
			Omits:         c.omits,
		},
	}
}

// renderArmSourced renders the case through the embedded arm guest and returns
// what the build assembled.
func renderArmSourced(t *testing.T, c armSourcedCase) (map[string]renderer.AssembledFile, error) {
	t.Helper()
	root := t.TempDir()
	blob := buildIntegrationTGZ(t, c.files)
	digest := integrationDigest(blob)
	seedIntegrationCacheSlot(t, root, "git-repository", armSourcedURL, armSourcedCommit, blob, digest)

	input := integrationInput(t, root, "core/compute/api-server", "api", "arm", digest, armSourcedURL, armSourcedCommit)
	row := &input.Lockfile.SourceArtifacts[0]
	row.Kind = "git-repository"
	row.Mediatype = "application/gzip"
	row.ReferencedBy[0].RequestedRef = armSourcedRef
	input.GraphView = derivedKeyGraphFacts{}
	input.Graph.Spec.ValuesMapping.Set("api", json.RawMessage(`{}`))

	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/compute/api-server" {
				return nil, false
			}
			return armSourcedRecipe(c), true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
	if c.secret {
		lookup.FeatureRecipe = armSecretAttachment(t, &input)
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, wasmEngineFactory(t))
	if err != nil {
		return nil, err
	}
	return result.Files, nil
}

// armSecretAttachment attaches the real core/referenced-secret Feature to the node
// as "password", written the way a developer writes it in an Aggregate, and
// returns the lookup of the arm FeatureRecipe that realizes it. The Feature and
// the FeatureRecipe are read from their own files, so the attachment declares
// sensitive exactly the leaves the catalog's Feature declares.
func armSecretAttachment(t *testing.T, input *renderer.RendererInput) func(string, string) (*types.FeatureRecipe, bool) {
	t.Helper()
	feature := wasmtest.SiblingPath(t, "aggrega-hub", "aggrega-catalog", "feature", "core", "referenced-secret", "1.0.0")
	fixture, err := filepath.Abs(filepath.Join("..", "testdata", "arm-sourced", "feature-recipes"))
	if err != nil {
		t.Fatalf("locating the FeatureRecipe fixture: %v", err)
	}
	cat, err := catalog.LoadAll("", feature, fixture)
	if err != nil {
		t.Fatalf("loading the Feature and its FeatureRecipe: %v", err)
	}
	manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{
		Features: &types.MappingTier{},
	}}}
	manifest.Spec.Mappings.Features.Type.Set("core/referenced-secret:1.0.0", "arm-referenced-secret:1.0.0")
	frLookup := cat.RecipeLookupWithManifest(manifest).FeatureRecipe

	values := types.OrderedMap[any]{}
	values.Set("origin", "vault-secret-ref")
	values.Set("key", "value")
	instances := types.OrderedMap[types.IRFeatureEntry]{}
	instances.Set("password", types.IRFeatureEntry{Type: "core/referenced-secret:1.0.0", Sensitive: true, Values: values})
	input.Graph.Spec.FeatureValuesMapping.Set("api", instances)
	input.FeatureOrder = ir.FeatureOrder{"api": {"password"}}
	input.CatalogLookup = cat.CatalogLookup()
	return frLookup
}

// armAssembledTemplate returns the assembled template a build wrote, decoded.
func armAssembledTemplate(t *testing.T, files map[string]renderer.AssembledFile) map[string]any {
	t.Helper()
	for _, path := range slices.Sorted(maps.Keys(files)) {
		if !strings.HasSuffix(path, armSourcedFile) {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(files[path].Content, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		return doc
	}
	t.Fatalf("the build assembled no %s; files: %v", armSourcedFile, slices.Sorted(maps.Keys(files)))
	return nil
}

// armNested returns the nested deployment's properties object of the assembled
// template.
func armNested(t *testing.T, files map[string]renderer.AssembledFile) map[string]any {
	t.Helper()
	doc := armAssembledTemplate(t, files)
	resources, _ := doc["resources"].(map[string]any)
	body, ok := resources[armSourcedSymbol].(map[string]any)
	if !ok {
		t.Fatalf("the template declares no nested deployment %q; it declares %v", armSourcedSymbol, slices.Sorted(maps.Keys(resources)))
	}
	properties, ok := body["properties"].(map[string]any)
	if !ok {
		t.Fatalf("the nested deployment %q has no properties: %v", armSourcedSymbol, body)
	}
	return properties
}

// armParametersOf returns the properties.parameters block of the nested
// deployment.
func armParametersOf(t *testing.T, files map[string]renderer.AssembledFile) map[string]any {
	t.Helper()
	parameters, ok := armNested(t, files)["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("the nested deployment carries no parameters block")
	}
	return parameters
}

// requireArmHalt fails the test unless err wraps the one plugin condition given,
// and returns the chain of causes the halt carries.
func requireArmHalt(t *testing.T, err, condition error) string {
	t.Helper()
	if err == nil {
		t.Fatalf("the build succeeded, want a halt wrapping %v", condition)
	}
	if !errors.Is(err, condition) {
		t.Fatalf("the build failed with %s, want a halt wrapping %v", cerrors.CauseChain(err), condition)
	}
	return cerrors.CauseChain(err)
}

// TestRenderAll_ArmBindsASourcedTemplatesParameters is the binding read through the
// build: a key binds the parameter it names without regard to case under the
// declared spelling, a number or boolean literal is written typed, an expression is
// written whole, a Key Vault reference object reaches the securestring parameter as
// written, a parameter with a default that no key names is left to its default, and
// a null reaches the nullable parameter. Two builds assemble the same bytes.
func TestRenderAll_ArmBindsASourcedTemplatesParameters(t *testing.T) {
	t.Parallel()

	const mapping = `SKU: Standard_B2s
capacity: 3
zoneRedundant: true
tags:
  env: prod
  tier: "[resourceGroup().location]"
adminPassword:
  reference:
    keyVault:
      id: /subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv
    secretName: admin-password
comment: null
`
	c := armSourcedCase{files: map[string]string{"main.json": armBindTemplate}, mapping: mapping}
	files, err := renderArmSourced(t, c)
	if err != nil {
		t.Fatalf("the build binding the template's parameters: %s", cerrors.CauseChain(err))
	}

	want := map[string]any{
		"sku":           map[string]any{"value": "Standard_B2s"},
		"capacity":      map[string]any{"value": json.Number("3")},
		"zoneRedundant": map[string]any{"value": true},
		"tags":          map[string]any{"value": map[string]any{"env": "prod", "tier": "[resourceGroup().location]"}},
		"adminPassword": map[string]any{"reference": map[string]any{
			"keyVault":   map[string]any{"id": "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv"},
			"secretName": "admin-password",
		}},
		"comment": map[string]any{"value": nil},
	}
	got := decodeUsingNumbers(t, armParametersOf(t, files))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("properties.parameters is\n%s\nwant\n%s", mustJSON(t, got), mustJSON(t, want))
	}
	if _, bound := got["location"]; bound {
		t.Errorf("properties.parameters binds location, which no key names and whose default stands")
	}

	// The inner template keeps every declaration: ARM supplies the values when the
	// nested deployment runs.
	inner, _ := armNested(t, files)["template"].(map[string]any)
	declared, _ := inner["parameters"].(map[string]any)
	if got := slices.Sorted(maps.Keys(declared)); !slices.Equal(got, []string{"adminPassword", "capacity", "comment", "location", "sku", "tags", "zoneRedundant"}) {
		t.Errorf("the inner template declares the parameters %v, want all seven it was written with", got)
	}

	again, err := renderArmSourced(t, c)
	if err != nil {
		t.Fatalf("the second build: %s", cerrors.CauseChain(err))
	}
	if !reflect.DeepEqual(files, again) {
		t.Errorf("two builds binding the same values assembled different files")
	}
}

// armValidMapping is a mapping that binds every parameter of armBindTemplate with
// a value it admits; the cases below each replace one line of it.
const armValidMapping = `sku: Standard_B1ms
capacity: 2
zoneRedundant: true
tags: {}
adminPassword:
  reference:
    keyVault:
      id: /vaults/kv
    secretName: pw
`

// TestRenderAll_ArmBindingChecksALiteralAgainstItsParameter holds the constraint
// halts: a literal outside allowedValues and one past maxValue each halt wrapping
// the condition for a value the provider cannot write, naming the parameter and
// the constraint and never the value.
func TestRenderAll_ArmBindingChecksALiteralAgainstItsParameter(t *testing.T) {
	t.Parallel()

	if _, err := renderArmSourced(t, armSourcedCase{files: map[string]string{"main.json": armBindTemplate}, mapping: armValidMapping}); err != nil {
		t.Fatalf("the control mapping every parameter a valid value: %s", cerrors.CauseChain(err))
	}
	for _, tc := range []struct {
		name, was, now, parameter, constraint, literal string
	}{
		{"allowedValues", "sku: Standard_B1ms", "sku: Premium_Unlisted_77", `"sku"`, "allowedValues", "Premium_Unlisted_77"},
		{"maxValue", "capacity: 2", "capacity: 987654", `"capacity"`, "maxValue", "987654"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mapping := strings.Replace(armValidMapping, tc.was, tc.now, 1)
			_, err := renderArmSourced(t, armSourcedCase{files: map[string]string{"main.json": armBindTemplate}, mapping: mapping})
			chain := requireArmHalt(t, err, plugincondition.ErrPluginValueNotRepresentable())
			for _, want := range []string{tc.parameter, tc.constraint} {
				if !strings.Contains(chain, want) {
					t.Errorf("the halt does not name %s:\n%s", want, chain)
				}
			}
			if strings.Contains(chain, tc.literal) {
				t.Errorf("the halt quotes the literal %q:\n%s", tc.literal, chain)
			}
		})
	}
}

// TestRenderAll_ArmLiteralIntoSecureParameterHalts holds the halt on a literal
// mapped to a securestring parameter, which would write the secret into the
// template as text: the condition is a value the provider cannot write, the
// sentence names the parameter and the route (a Key Vault reference or an
// expression), and the literal appears nowhere in it.
func TestRenderAll_ArmLiteralIntoSecureParameterHalts(t *testing.T) {
	t.Parallel()

	const literal = "hunter2-correct-horse"
	mapping := "sku: Standard_B1ms\ncapacity: 2\nzoneRedundant: false\ntags: {}\nadminPassword: " + literal + "\n"
	_, err := renderArmSourced(t, armSourcedCase{files: map[string]string{"main.json": armBindTemplate}, mapping: mapping})
	chain := requireArmHalt(t, err, plugincondition.ErrPluginValueNotRepresentable())
	for _, want := range []string{`"adminPassword"`, "Key Vault reference", "expression"} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not carry %q:\n%s", want, chain)
		}
	}
	if strings.Contains(chain, literal) {
		t.Errorf("the halt quotes the secret literal:\n%s", chain)
	}

	// The control writes the Key Vault reference for the same parameter and builds,
	// so the halt above is the literal's.
	control := "sku: Standard_B1ms\ncapacity: 2\nzoneRedundant: false\ntags: {}\nadminPassword:\n  reference:\n    keyVault:\n      id: /vaults/kv\n    secretName: pw\n"
	if _, err := renderArmSourced(t, armSourcedCase{files: map[string]string{"main.json": armBindTemplate}, mapping: control}); err != nil {
		t.Fatalf("the control binding a Key Vault reference: %s", cerrors.CauseChain(err))
	}
}

// TestRenderAll_ArmRequiredParameterUnboundHalts holds the halt on a parameter that
// declares no default and admits no null when no key names it: the condition is a
// required input left unresolved, and the sentence names the parameter and the
// route of reading a top-level parameter the Recipe's artifacts declare.
func TestRenderAll_ArmRequiredParameterUnboundHalts(t *testing.T) {
	t.Parallel()

	mapping := "sku: Standard_B1ms\ncapacity: 2\ntags: {}\nadminPassword:\n  reference:\n    keyVault:\n      id: /vaults/kv\n    secretName: pw\n"
	_, err := renderArmSourced(t, armSourcedCase{files: map[string]string{"main.json": armBindTemplate}, mapping: mapping})
	chain := requireArmHalt(t, err, plugincondition.ErrPluginRequiredInputUnresolved())
	for _, want := range []string{`"zoneRedundant"`, "[parameters('zoneRedundant')]"} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not carry %q:\n%s", want, chain)
		}
	}
}

// armExactLeafMapping reads the attachment's type and the name it published by
// exact leaf, the only reads of `.features` an arm Recipe makes.
const armExactLeafMapping = `kind: "{{ .features.password.type }}"
vaultSecret: "{{ .features.password.provides.name }}"
enableTelemetry: false
`

// armRangedMapping reads every attachment, which reaches the leaves its Feature
// declares sensitive.
const armRangedMapping = `{{- range $name, $_ := (default dict .features) }}
# attached: {{ $name }}
{{- end }}
kind: standard
vaultSecret: plain
enableTelemetry: false
`

// TestRenderAll_ArmExactLeafAttachmentReadLeavesTheMappingUnmarked proves the premise
// the sourced path rests on: a mapping that reads an attachment's type and the name
// its FeatureRecipe published, by exact leaf, is not marked as reading a sensitive
// value although the attachment's Feature declares its origin sensitive, so the
// literals it produces bind. The name published is the origin as written, so the
// value reaching the parameter is the attachment's own locator.
func TestRenderAll_ArmExactLeafAttachmentReadLeavesTheMappingUnmarked(t *testing.T) {
	t.Parallel()

	files, err := renderArmSourced(t, armSourcedCase{
		files:   map[string]string{"main.json": armSecretTemplate},
		mapping: armExactLeafMapping,
		secret:  true,
	})
	if err != nil {
		t.Fatalf("the exact-leaf mapping was refused, so the build marked it sensitive: %s", cerrors.CauseChain(err))
	}
	got := decodeUsingNumbers(t, armParametersOf(t, files))
	want := map[string]any{
		"kind":            map[string]any{"value": "core/referenced-secret:1.0.0"},
		"vaultSecret":     map[string]any{"value": "vault-secret-ref"},
		"enableTelemetry": map[string]any{"value": false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("properties.parameters is\n%s\nwant\n%s", mustJSON(t, got), mustJSON(t, want))
	}
}

// TestRenderAll_ArmRangedFeaturesMarkTheMappingAndRefuseLiterals is the other side of
// the premise: a mapping that ranges over `.features` reaches the attachment's
// sensitive leaves, is marked, and so every literal it produces halts naming the
// parameters and the routes, never a value. The attachment, the template and the
// literals are the exact-leaf test's, so the one difference is the read.
func TestRenderAll_ArmRangedFeaturesMarkTheMappingAndRefuseLiterals(t *testing.T) {
	t.Parallel()

	_, err := renderArmSourced(t, armSourcedCase{
		files:   map[string]string{"main.json": armSecretTemplate},
		mapping: armRangedMapping,
		secret:  true,
	})
	chain := requireArmHalt(t, err, plugincondition.ErrPluginValueNotRepresentable())
	for _, want := range []string{`"enableTelemetry"`, `"kind"`, `"vaultSecret"`, "Key Vault reference", "expression"} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not carry %q:\n%s", want, chain)
		}
	}
	if strings.Contains(chain, "vault-secret-ref") {
		t.Errorf("the halt quotes the attachment's origin:\n%s", chain)
	}
}

// decodeUsingNumbers returns a decoded JSON tree with its numbers kept as written,
// so a comparison reads the digits a template carries.
func decodeUsingNumbers(t *testing.T, tree map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(tree)
	if err != nil {
		t.Fatalf("encoding the parameters: %v", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decoding the parameters: %v", err)
	}
	return out
}

// mustJSON writes a tree for a failure message.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("encoding for the message: %v", err)
	}
	return string(raw)
}

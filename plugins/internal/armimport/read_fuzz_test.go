// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// expressionCorpus seeds the fuzzer with the expressions the armexpr package's
// own fuzzer starts from. DELIBERATE NEAR-DUPLICATE of FuzzParse's seeds in
// plugins/internal/armcore/armexpr: a test of another package cannot read
// them, and the reader is to be fuzzed from the same expressions the parser
// is, well formed and not.
var expressionCorpus = []string{
	"[concat('a', parameters('p'))]",
	"[reference('storageAccount').primaryEndpoints.blob]",
	"[ reference('storageAccount') ]",
	"[parameters('arr')[0]]",
	"[format('{0}''s', 'x')]",
	"[my.fn(1)]",
	"[createArray(1, 2, -3)]",
	"[lambda('x', lambdaVariables('x'))]",
	"[if(true(), 'a', 'b')]",
	"[prod]", "[[x]", "[", "[]", "['open]", "[f(]", "[f(a,)]", "[f()]]",
}

// FuzzReadTemplate reads a template holding a fuzzed expression at every
// place a template reads one — a variable, a parameter's default, a
// resource's name, condition, copy count, scope, dependsOn, a property and an
// output — in both language versions; a template nesting templates inline,
// evaluated in either scope, deployed by a copy loop into a fuzzed resource
// group, given fuzzed parameters, reading a fuzzed output, and nesting a
// module whose telemetry resource's name is fuzzed; a fuzzed document whole,
// as the root and as a template the root links. No input panics, the
// instances of a repeated resource are built, and two reads of one input give
// one result.
func FuzzReadTemplate(f *testing.F) {
	var docs [][]byte
	for _, name := range []string{"topology/one.json", "topology/two.json", "linked/main.json",
		"refused/multiline.json", "parameters/one.parameters.json", "avm/maps/account/0.2.2/main.json"} {
		raw, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(name)))
		if err != nil {
			f.Fatal(err)
		}
		docs = append(docs, raw)
	}
	for i, expr := range expressionCorpus {
		f.Add(expr, docs[i%len(docs)])
	}
	f.Fuzz(func(t *testing.T, expr string, doc []byte) {
		for _, sources := range fuzzSources(expr, doc) {
			g1, d1 := Read(aggrega.ImportInput{Sources: sources}, cloudimport.Settings{}, parse)
			g2, d2 := Read(aggrega.ImportInput{Sources: sources}, cloudimport.Settings{}, parse)
			if dump(g1) != dump(g2) || !reflect.DeepEqual(d1, d2) {
				t.Fatalf("two reads of one input differ")
			}
			if g1 == nil {
				continue
			}
			for _, n := range g1.Nodes {
				if n.Repeat != nil && n.Repeat.Instance != nil {
					for _, key := range n.Repeat.Keys {
						n.Repeat.Instance(key)
					}
				}
			}
			cloudimport.Expand(g1)
			cloudimport.Edges(g1)
		}
	})
}

// fuzzSources returns the requests FuzzReadTemplate reads for the fuzzed
// expression expr and the fuzzed document doc.
func fuzzSources(expr string, doc []byte) [][]aggrega.ImportSource {
	e := string(importer.String(expr))
	one := []byte(`{"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		"parameters": {"p": {"type": "string", "defaultValue": ` + e + `}},
		"variables": {"v": ` + e + `, "w": "[variables('v')]"},
		"resources": [
			{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": ` + e + `,
				"condition": ` + e + `, "copy": {"name": "loop", "count": 2}, "dependsOn": [` + e + `],
				"properties": {"a": ` + e + `, "b": "[variables('w')]", "c": "[concat(variables('v'), ` +
		`copyIndex())]", "copy": [{"name": "items", "count": 2, "input": ` + e + `}]},
				"resources": [{"type": "slots", "apiVersion": "2023-01-01", "name": ` + e + `}]},
			{"type": "Microsoft.Authorization/locks", "apiVersion": "2020-05-01", "name": "lock", "scope": ` + e + `}],
		"outputs": {"o": {"type": "string", "value": ` + e + `}}}`)
	two := []byte(`{"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		"languageVersion": "2.0",
		"resources": {"x": {"type": "Microsoft.Web/sites/slots", "apiVersion": "2023-01-01", "name": ` + e + `,
			"dependsOn": [` + e + `], "properties": {"a": ` + e + `}}}}`)
	telemetryName := string(importer.String("[format('46d3xbcp.res.web-site.{0}.{1}', replace('" +
		strings.ReplaceAll(expr, "'", "''") + "', '.', '-'), substring(uniqueString(parameters('name')), 0, 4))]"))
	module := `{"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		"languageVersion": "2.0", "metadata": {"_generator": {"name": "bicep"}, "name": ` + e + `},
		"parameters": {"name": {"type": "string"}, "lock": {"type": "object", "defaultValue": {}}},
		"resources": {
			"avmTelemetry": {"type": "Microsoft.Resources/deployments", "apiVersion": "2024-03-01",
				"name": ` + telemetryName + `, "properties": {"template": {"resources": []}}},
			"site": {"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": ` + e + `}}}`
	nested := []byte(`{"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		"parameters": {"p": {"type": "string", "defaultValue": ` + e + `}},
		"resources": [
			{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "inner",
				"copy": {"name": "n", "count": 2}, "condition": ` + e + `, "resourceGroup": ` + e + `,
				"dependsOn": [` + e + `],
				"properties": {"expressionEvaluationOptions": {"scope": "inner"},
					"parameters": {"p": {"value": ` + e + `}, "q": {"reference": {"keyVault": {"id": ` + e + `},
						"secretName": ` + e + `}}},
					"template": {"parameters": {"p": {"type": "string"}, "q": {"type": "securestring"},
							"r": {"type": "string", "defaultValue": ` + e + `}},
						"variables": {"v": ` + e + `},
						"resources": [
							{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": ` + e + `,
								"condition": ` + e + `, "dependsOn": [` + e + `],
								"properties": {"a": "[parameters('p')]", "b": "[parameters('q')]", "c": ` + e + `,
									"d": "[parameters('r')]", "e": "[variables('v')]"}},
							{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "site", "existing": true},
							{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "module",
								"properties": {"expressionEvaluationOptions": {"scope": "inner"},
									"parameters": {"name": {"value": ` + e + `}, "lock": {"value": ` + e + `}},
									"template": ` + module + `}}],
						"outputs": {"o": {"type": "string", "value": ` + e + `}}}}},
			{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "outer",
				"properties": {"template": {"resources": [{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01",
					"name": "[parameters('p')]", "properties": {"a": ` + e + `}}]}}},
			{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "linked",
				"properties": {"templateLink": {"relativePath": "linked.json"}, "parameters": {"p": {"value": ` + e + `}}}},
			{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "site", "dependsOn": ["inner", "linked"],
				"properties": {"o": "[reference('inner').outputs.o.value]", "e": ` + e + `,
					"l": "[reference('linked').outputs.o.value.x]"}}],
		"outputs": {"o": {"type": "string", "value": "[reference('inner').outputs.o.value]"}}}`)
	return [][]aggrega.ImportSource{
		{{Name: "main.json", Bytes: one}},
		{{Name: "main.json", Bytes: two}},
		{{Name: "main.json", Bytes: doc}},
		{{Name: "main.json", Bytes: one}, {Name: "main.parameters.json", Bytes: doc}},
		{{Name: "main.json", Bytes: nested}, {Name: "linked.json", Bytes: doc}},
	}
}

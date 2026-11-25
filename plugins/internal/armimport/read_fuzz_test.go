// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"os"
	"path/filepath"
	"reflect"
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
// output — in both language versions, and a fuzzed document whole. No input
// panics, the instances of a repeated resource are built, and two reads of
// one input give one result.
func FuzzReadTemplate(f *testing.F) {
	var docs [][]byte
	for _, name := range []string{"topology/one.json", "topology/two.json", "linked/main.json",
		"refused/multiline.json", "parameters/one.parameters.json"} {
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
		for _, sources := range [][]aggrega.ImportSource{
			{{Name: "main.json", Bytes: one}},
			{{Name: "main.json", Bytes: two}},
			{{Name: "main.json", Bytes: doc}},
			{{Name: "main.json", Bytes: one}, {Name: "main.parameters.json", Bytes: doc}},
		} {
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

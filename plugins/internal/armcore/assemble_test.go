// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
)

// contributes is one node's single fragment at a wave.
func contributes(node string, wave int, fragment string) Contribution {
	return Contribution{NodeID: node, Files: [][]byte{[]byte(fragment)}, Paths: []string{"arm.yaml"}, SyncWave: wave}
}

// assembled assembles contribs and decodes the one template file.
func assembled(t *testing.T, contribs ...Contribution) (map[string]any, []byte) {
	t.Helper()
	out, err := Assemble(contribs)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(out.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(out.Files))
	}
	var m map[string]any
	if err := json.Unmarshal(out.Files[0].Content, &m); err != nil {
		t.Fatalf("template is not JSON: %v", err)
	}
	return m, out.Files[0].Content
}

// storage is a fragment declaring one resource under symbol.
func storage(symbol string) string {
	return "resources:\n  " + symbol + ":\n    type: Microsoft.Storage/storageAccounts\n    apiVersion: '2023-01-01'\n    name: " + strings.ToLower(symbol) + "\n"
}

func TestAssembleMergesBySymbolicName(t *testing.T) {
	t.Parallel()
	m, _ := assembled(t, contributes("payments-db", 0, storage("Db")), contributes("orders-api", 0, storage("Api")))
	resources := m["resources"].(map[string]any)
	if _, ok := resources["Db"]; !ok {
		t.Errorf("Db missing: %v", resources)
	}
	if _, ok := resources["Api"]; !ok {
		t.Errorf("Api missing: %v", resources)
	}
	if m["$schema"] != SchemaResourceGroup || m["languageVersion"] != LanguageVersion || m["contentVersion"] != ContentVersion {
		t.Errorf("stamped top level = %v %v %v", m["$schema"], m["languageVersion"], m["contentVersion"])
	}
}

func TestAssembleRestatedIdenticalBodyMergesOnce(t *testing.T) {
	t.Parallel()
	shared := storage("Shared") + "parameters:\n  location:\n    type: string\nvariables:\n  v: x\ndefinitions:\n  d:\n    type: string\nmetadata:\n  m: x\nfunctions:\n  - namespace: ns\n    members:\n      f:\n        output:\n          type: string\n          value: x\n"
	m, _ := assembled(t, contributes("a", 0, shared), contributes("b", 0, shared))
	if n := len(m["resources"].(map[string]any)); n != 1 {
		t.Errorf("resources = %d, want 1", n)
	}
	if n := len(m["functions"].([]any)); n != 1 {
		t.Errorf("namespaces = %d, want 1", n)
	}
}

func TestAssembleCollisionNamesBothContributors(t *testing.T) {
	t.Parallel()
	fn := func(v string) string {
		return "functions:\n  - namespace: ns\n    members:\n      f:\n        output:\n          type: string\n          value: " + v + "\n"
	}
	tests := []struct {
		section string
		a, b    string
	}{
		{"resources", storage("Db"), strings.Replace(storage("Db"), "2023-01-01", "2024-01-01", 1)},
		{"parameters", "parameters:\n  p:\n    type: string\n", "parameters:\n  p:\n    type: int\n"},
		{"variables", "variables:\n  v: one\n", "variables:\n  v: two\n"},
		{"definitions", "definitions:\n  d:\n    type: string\n", "definitions:\n  d:\n    type: int\n"},
		{"functions", fn("one"), fn("two")},
		{"metadata", "metadata:\n  m: one\n", "metadata:\n  m: two\n"},
	}
	for _, tt := range tests {
		t.Run(tt.section, func(t *testing.T) {
			t.Parallel()
			_, err := Assemble([]Contribution{contributes("first-node", 0, tt.a), contributes("second-node", 0, tt.b)})
			if !errors.Is(err, plugincondition.ErrPluginOutputNameCollision()) {
				t.Fatalf("err = %v, want an output name collision", err)
			}
			for _, want := range []string{"first-node", "second-node", "in " + tt.section + ","} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message %q does not contain %q", err, want)
				}
			}
		})
	}
	t.Run("outputs", func(t *testing.T) {
		t.Parallel()
		one := "outputs:\n  host:\n    type: string\n    value: one\n"
		two := "outputs:\n  host:\n    type: string\n    value: two\n"
		_, err := Assemble([]Contribution{contributes("node", 0, one), contributes("node", 0, two)})
		if !errors.Is(err, plugincondition.ErrPluginOutputNameCollision()) {
			t.Fatalf("err = %v, want an output name collision", err)
		}
		if !strings.Contains(err.Error(), "in outputs,") || !strings.Contains(err.Error(), `"node"`) {
			t.Errorf("message %q does not name the space and contributor", err)
		}
	})
}

func TestAssembleCaseOnlyCollisionHalts(t *testing.T) {
	t.Parallel()
	_, err := Assemble([]Contribution{
		contributes("a", 0, "parameters:\n  location:\n    type: string\n"),
		contributes("b", 0, "parameters:\n  Location:\n    type: string\n"),
	})
	if !errors.Is(err, plugincondition.ErrPluginOutputNameCollision()) {
		t.Fatalf("err = %v, want an output name collision", err)
	}
}

func TestAssembleCopyVariablesFromTwoContributorsHalt(t *testing.T) {
	t.Parallel()
	copyOf := func(name string) string {
		return "variables:\n  copy:\n    - name: " + name + "\n      count: 2\n      input: x\n"
	}
	_, err := Assemble([]Contribution{contributes("a", 0, copyOf("one")), contributes("b", 0, copyOf("two"))})
	if !errors.Is(err, plugincondition.ErrPluginOutputNameCollision()) {
		t.Fatalf("err = %v, want an output name collision", err)
	}
	if _, err := Assemble([]Contribution{contributes("a", 0, copyOf("one")), contributes("b", 0, copyOf("one"))}); err != nil {
		t.Fatalf("an identical copy variable is a merge: %v", err)
	}
}

func TestAssembleOutputsAreNodeScoped(t *testing.T) {
	t.Parallel()
	out := func(v string) string { return "outputs:\n  host:\n    type: string\n    value: " + v + "\n" }
	m, _ := assembled(t, contributes("payments-db", 0, out("db")), contributes("orders-api", 0, out("api")))
	outputs := m["outputs"].(map[string]any)
	for name, want := range map[string]string{"payments_zhdb__host": "db", "orders_zhapi__host": "api"} {
		o, ok := outputs[name].(map[string]any)
		if !ok {
			t.Fatalf("output %s missing: %v", name, outputs)
		}
		if o["type"] != "string" || o["value"] != want {
			t.Errorf("%s = %v", name, o)
		}
	}
	if len(outputs) != 2 {
		t.Errorf("outputs = %v", outputs)
	}
}

func TestAssembleOutputsUseTheNameBasis(t *testing.T) {
	t.Parallel()
	c := contributes("node-1", 0, "outputs:\n  host:\n    type: string\n    value: x\n")
	c.NameBasis = "payments-db"
	m, _ := assembled(t, c)
	if _, ok := m["outputs"].(map[string]any)["payments_zhdb__host"]; !ok {
		t.Errorf("outputs = %v", m["outputs"])
	}
}

func TestAssembleWaveOrdering(t *testing.T) {
	t.Parallel()
	wave0 := storage("Zeta") + "  Alpha:\n    type: Microsoft.Storage/storageAccounts\n    apiVersion: '2023-01-01'\n    name: alpha\n  Old:\n    existing: true\n    type: Microsoft.Storage/storageAccounts\n    apiVersion: '2023-01-01'\n    name: old\n"
	wave1 := storage("Own") + "  Late:\n    type: Microsoft.Storage/storageAccounts\n    apiVersion: '2023-01-01'\n    name: late\n    dependsOn:\n      - Own\n  Seen:\n    existing: true\n    type: Microsoft.Storage/storageAccounts\n    apiVersion: '2023-01-01'\n    name: seen\n"
	m, _ := assembled(t, contributes("early", 0, wave0), contributes("late", 1, wave1))
	resources := m["resources"].(map[string]any)
	depends := func(symbol string) any { return resources[symbol].(map[string]any)["dependsOn"] }
	want := []any{"Own", "Alpha", "Zeta"}
	if got := depends("Late"); !jsonEqual(got, want) {
		t.Errorf("Late dependsOn = %v, want %v", got, want)
	}
	if got := depends("Own"); !jsonEqual(got, []any{"Alpha", "Zeta"}) {
		t.Errorf("Own dependsOn = %v", got)
	}
	for _, symbol := range []string{"Alpha", "Zeta", "Old", "Seen"} {
		if got := depends(symbol); got != nil {
			t.Errorf("%s dependsOn = %v, want none", symbol, got)
		}
	}
	_, oneWave := assembled(t, contributes("early", 1, wave0), contributes("late", 1, wave1))
	_, plain := assembled(t, contributes("early", 0, wave0), contributes("late", 0, wave1))
	if !bytes.Equal(oneWave, plain) {
		t.Error("one wave must assemble the bytes the no-wave assembly gives")
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func TestAssembleIsDeterministic(t *testing.T) {
	t.Parallel()
	frag := storage("Db") + "parameters:\n  b:\n    type: string\n  a:\n    type: string\nvariables:\n  y: 1\n  x: 2\nfunctions:\n  - namespace: zz\n    members:\n      g:\n        output: {type: string, value: x}\n      f:\n        output: {type: string, value: x}\n  - namespace: aa\n    members:\n      h:\n        output: {type: string, value: x}\noutputs:\n  host: {type: string, value: x}\n  port: {type: int, value: 1}\n"
	contribs := []Contribution{contributes("one", 0, frag), contributes("two", 1, storage("Api"))}
	_, first := assembled(t, contribs...)
	for i := 0; i < 20; i++ {
		if _, again := assembled(t, contribs...); !bytes.Equal(first, again) {
			t.Fatalf("run %d differs", i)
		}
	}
}

func TestAssembleWritesFunctionsSortedByNamespaceThenMember(t *testing.T) {
	t.Parallel()
	frag := "functions:\n  - namespace: zz\n    members:\n      b: {output: {type: string, value: x}}\n      a: {output: {type: string, value: x}}\n  - namespace: aa\n    members:\n      c: {output: {type: string, value: x}}\n"
	m, content := assembled(t, contributes("n", 0, frag))
	list := m["functions"].([]any)
	if list[0].(map[string]any)["namespace"] != "aa" || list[1].(map[string]any)["namespace"] != "zz" {
		t.Errorf("namespaces not sorted: %v", list)
	}
	if bytes.Index(content, []byte(`"a"`)) > bytes.Index(content, []byte(`"b"`)) {
		t.Error("members not sorted")
	}
}

func TestAssembleRefusesWiderContentScope(t *testing.T) {
	t.Parallel()
	frag := "$schema: https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#\n" + storage("Db")
	_, err := Assemble([]Contribution{contributes("payments-db", 0, frag)})
	if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
		t.Fatalf("err = %v, want cannot express input", err)
	}
	for _, want := range []string{"arm-provider:", "payments-db", "assembles a boundary at resource group scope", "resource group template"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not contain %q", err, want)
		}
	}
	for _, banned := range []string{"yet", "not available"} {
		if strings.Contains(err.Error(), banned) {
			t.Errorf("message %q contains %q", err, banned)
		}
	}
}

func TestAssembleWritesCanonicalJSON(t *testing.T) {
	t.Parallel()
	frag := "resources:\n  Db:\n    type: T\n    name: 'a<b>&c'\n"
	out, err := Assemble([]Contribution{contributes("n", 0, frag)})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Files[0]; got.Path != "arm-template.json" || got.Format != "json" {
		t.Errorf("file = %s %s", got.Path, got.Format)
	}
	want := "{\n  \"$schema\": \"" + SchemaResourceGroup + "\",\n  \"contentVersion\": \"1.0.0.0\",\n  \"languageVersion\": \"2.0\",\n  \"resources\": {\n    \"Db\": {\n      \"name\": \"a<b>&c\",\n      \"type\": \"T\"\n    }\n  }\n}\n"
	if got := string(out.Files[0].Content); got != want {
		t.Errorf("template:\n%s\nwant:\n%s", got, want)
	}
}

func TestAssembleWritesAnEmptyResourcesObject(t *testing.T) {
	t.Parallel()
	_, content := assembled(t, contributes("n", 0, "parameters:\n  p:\n    type: string\n"))
	if !strings.Contains(string(content), `"resources": {}`) {
		t.Errorf("template:\n%s", content)
	}
	if strings.Contains(string(content), `"outputs"`) || strings.Contains(string(content), `"functions"`) {
		t.Errorf("an empty section is written:\n%s", content)
	}
}

func TestAssembleRunsLimitsAfterComposition(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("parameters:\n")
	for i := 0; i < 257; i++ {
		b.WriteString("  p" + strings.Repeat("a", i%7) + string(rune('a'+i/26%26)) + string(rune('a'+i%26)) + string(rune('a'+i/676)) + ":\n    type: string\n")
	}
	_, err := Assemble([]Contribution{contributes("n", 0, b.String())})
	if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) || !strings.Contains(err.Error(), "split the boundary") {
		t.Fatalf("err = %v", err)
	}
}

func TestAssembleKeepsNamespaceAndMemberApart(t *testing.T) {
	t.Parallel()
	frag := "functions:\n  - namespace: my.ns\n    members:\n      f: {output: {type: string, value: x}}\n"
	m, _ := assembled(t, contributes("n", 0, frag))
	got := m["functions"].([]any)[0].(map[string]any)
	if got["namespace"] != "my.ns" {
		t.Errorf("namespace = %v", got["namespace"])
	}
	if _, ok := got["members"].(map[string]any)["f"]; !ok {
		t.Errorf("members = %v", got["members"])
	}
}

func TestAssembleFunctionCollisionNamesTheFunctionPointer(t *testing.T) {
	t.Parallel()
	fn := func(v string) string {
		return "functions:\n  - namespace: other\n    members:\n      g: {output: {type: string, value: x}}\n  - namespace: ns\n    members:\n      f: {output: {type: string, value: " + v + "}}\n"
	}
	_, err := Assemble([]Contribution{contributes("a", 0, fn("one")), contributes("b", 0, fn("two"))})
	if !errors.Is(err, plugincondition.ErrPluginOutputNameCollision()) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "/functions/1/members/f") {
		t.Errorf("message %q does not carry the function's pointer", err)
	}
}

func TestAssembleRefusesADependsOnWavesCannotExtend(t *testing.T) {
	t.Parallel()
	late := "resources:\n  Late:\n    type: T\n    name: late\n    dependsOn: 7\n"
	_, err := Assemble([]Contribution{contributes("early", 0, storage("Db")), contributes("late", 1, late)})
	if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
		t.Fatalf("err = %v, want cannot express input", err)
	}
	for _, want := range []string{"arm-provider:", "/resources/Late/dependsOn", `"late"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not contain %q", err, want)
		}
	}
	if _, err := Assemble([]Contribution{contributes("early", 0, storage("Db")), contributes("late", 0, late)}); err != nil {
		t.Errorf("one wave writes nothing and so refuses nothing: %v", err)
	}
}

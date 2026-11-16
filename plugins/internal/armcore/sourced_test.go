// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const schemaRG = "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#"

func dirListing(files ...string) rendercore.SourceListing {
	l := rendercore.SourceListing{Root: "/src/module", RootIsDir: true}
	for _, f := range files {
		l.Entries = append(l.Entries, rendercore.SourceEntry{Path: f})
	}
	return l
}

func TestSelectTemplate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		listing rendercore.SourceListing
		want    string
	}{
		{"single file whatever its name", rendercore.SourceListing{Root: "/src/anything.tpl"}, ""},
		{"main.json", dirListing("main.json", "README.md"), "main.json"},
		{"azuredeploy.json", dirListing("azuredeploy.json", "README.md"), "azuredeploy.json"},
		{"both prefer main.json", dirListing("azuredeploy.json", "main.json"), "main.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := SelectTemplate(c.listing)
			if err != nil || got != c.want {
				t.Fatalf("SelectTemplate = %q, %v; want %q", got, err, c.want)
			}
		})
	}

	t.Run("directory entry named main.json is not a file", func(t *testing.T) {
		t.Parallel()
		l := dirListing("azuredeploy.json")
		l.Entries = append(l.Entries, rendercore.SourceEntry{Path: "main.json", IsDir: true})
		got, err := SelectTemplate(l)
		if err != nil || got != "azuredeploy.json" {
			t.Fatalf("SelectTemplate = %q, %v", got, err)
		}
	})

	t.Run("neither", func(t *testing.T) {
		t.Parallel()
		_, err := SelectTemplate(dirListing("main.bicep", "sub/main.json"))
		if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
			t.Fatalf("want E0289, got %v", err)
		}
		for _, want := range []string{"arm-provider:", "main.json", "azuredeploy.json", "bicep build"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message %q lacks %q", err, want)
			}
		}
	})
}

func shapeDiag(t *testing.T, content string) []string {
	t.Helper()
	var out []string
	for _, d := range ValidateTemplate("main.json", []byte(content)) {
		if d.Severity != abi.SeverityHalt {
			t.Errorf("severity = %v, want halt", d.Severity)
		}
		out = append(out, d.Code+": "+d.Message)
	}
	return out
}

func TestValidateTemplate(t *testing.T) {
	t.Parallel()
	good := `{"$schema": "` + schemaRG + `", "contentVersion": "1.0.0.0", "resources": []}`

	if got := shapeDiag(t, good); len(got) != 0 {
		t.Fatalf("valid template: %v", got)
	}
	// Saved by Windows tools, or written with comments.
	for name, content := range map[string]string{
		"byte-order mark": "\xef\xbb\xbf" + good,
		"CRLF":            strings.ReplaceAll("{\n\"$schema\": \""+schemaRG+"\",\n\"resources\": []\n}\n", "\n", "\r\n"),
		"comments":        "// header\n{\n/* note */ \"$schema\": \"" + schemaRG + "\", // inline\n\"resources\": [],\n}\n",
	} {
		if got := shapeDiag(t, content); len(got) != 0 {
			t.Errorf("%s: want a clean parse, got %v", name, got)
		}
	}

	cases := []struct {
		name, content, code string
	}{
		{"unparsable", `{"$schema": `, ValidationCodeTemplateParse},
		{"trailing data", good + `{}`, ValidationCodeTemplateParse},
		{"not an object", `["a"]`, ValidationCodeTemplateShape},
		{"no schema", `{"resources": []}`, ValidationCodeTemplateShape},
		{"schema not text", `{"$schema": 3, "resources": []}`, ValidationCodeTemplateShape},
		{"tenant scope", `{"$schema": "https://schema.management.azure.com/schemas/2019-08-01/tenantDeploymentTemplate.json#", "resources": []}`, ValidationCodeTemplateShape},
		{"resources list under 2.0", `{"$schema": "` + schemaRG + `", "languageVersion": "2.0", "resources": []}`, ValidationCodeTemplateShape},
		{"resources list under 2.x", `{"$schema": "` + schemaRG + `", "languageVersion": "2.1-experimental", "resources": []}`, ValidationCodeTemplateShape},
		{"resources object without languageVersion", `{"$schema": "` + schemaRG + `", "resources": {}}`, ValidationCodeTemplateShape},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			diags := ValidateTemplate("main.json", []byte(c.content))
			if len(diags) != 1 {
				t.Fatalf("want one diagnostic, got %v", diags)
			}
			d := diags[0]
			if d.Code != c.code || d.Severity != abi.SeverityHalt {
				t.Errorf("got %q severity %v, want %q halt", d.Code, d.Severity, c.code)
			}
			if !strings.Contains(d.Message, "main.json") || !strings.HasPrefix(d.Message, "arm-provider:") {
				t.Errorf("message %q must open arm-provider: and name the file", d.Message)
			}
		})
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/sourced/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func renderFixture(t *testing.T, name string, node SourcedNode) (map[string]any, []byte, string) {
	t.Helper()
	node.Content = fixture(t, name)
	out, symbol, _, err := RenderSourced(node)
	if err != nil {
		t.Fatalf("RenderSourced: %v", err)
	}
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	return doc, out, symbol
}

func wrapperOf(t *testing.T, doc map[string]any, symbol string) map[string]any {
	t.Helper()
	resources, _ := doc["resources"].(map[string]any)
	if len(resources) != 1 {
		t.Fatalf("want one resource, got %v", resources)
	}
	w, _ := resources[symbol].(map[string]any)
	if w == nil {
		t.Fatalf("no resource %q in %v", symbol, resources)
	}
	return w
}

func checkWrapper(t *testing.T, name string) {
	t.Helper()
	values := types.OrderedMap[any]{}
	values.Set("name", "storeone")
	node := SourcedNode{Identity: "payments-db", Path: "main.json", Values: values, ValuesMapped: true}
	doc, out, symbol := renderFixture(t, name, node)
	if symbol != "PaymentsDb" {
		t.Fatalf("symbol = %q", symbol)
	}
	w := wrapperOf(t, doc, symbol)
	if w["type"] != "Microsoft.Resources/deployments" || w["apiVersion"] != DeploymentsAPIVersion {
		t.Errorf("type/apiVersion = %v / %v", w["type"], w["apiVersion"])
	}
	if want := NestedDeploymentHistoryName(symbol); w["name"] != want {
		t.Errorf("name = %v, want %v", w["name"], want)
	}
	props, _ := w["properties"].(map[string]any)
	if props["mode"] != "Incremental" {
		t.Errorf("mode = %v", props["mode"])
	}
	if eval, _ := props["expressionEvaluationOptions"].(map[string]any); eval["scope"] != "inner" {
		t.Errorf("expressionEvaluationOptions = %v", props["expressionEvaluationOptions"])
	}
	if p, _ := props["parameters"].(map[string]any); p["name"].(map[string]any)["value"] != "storeone" {
		t.Errorf("parameters = %v", props["parameters"])
	}
	tpl, _ := props["template"].(map[string]any)
	var src map[string]any
	if err := json.Unmarshal(fixture(t, name), &src); err != nil {
		t.Fatal(err)
	}
	if got, want := mustJSON(t, tpl), mustJSON(t, src); got != want {
		t.Errorf("template changed:\n got %s\nwant %s", got, want)
	}
	// Bytes are stable and end in one newline.
	_, again, _ := renderFixture(t, name, node)
	if !bytes.Equal(out, again) {
		t.Errorf("two runs differ")
	}
	if !bytes.HasSuffix(out, []byte("}\n")) || bytes.HasSuffix(out, []byte("\n\n")) {
		t.Errorf("want one trailing newline")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRenderSourcedWrapsA20Template(t *testing.T) {
	t.Parallel()
	checkWrapper(t, "module-2.0.json")
}

func TestRenderSourcedWrapsA10Template(t *testing.T) {
	t.Parallel()
	checkWrapper(t, "module-1.0.json")
}

func TestRenderSourcedDropsByteOrderMark(t *testing.T) {
	t.Parallel()
	content := append([]byte("\xef\xbb\xbf"), fixture(t, "module-1.0.json")...)
	values := types.OrderedMap[any]{}
	values.Set("name", "storeone")
	_, _, _, err := RenderSourced(SourcedNode{Identity: "a", Content: content, Values: values, ValuesMapped: true})
	if err != nil {
		t.Fatalf("RenderSourced: %v", err)
	}
}

func TestRenderSourcedStampsProvenanceOnTheWrapperOnly(t *testing.T) {
	t.Parallel()
	set := map[string]string{"component": "payments-db", "release": "orders"}
	values := types.OrderedMap[any]{}
	values.Set("name", "storeone")
	doc, out, symbol := renderFixture(t, "module-2.0.json", SourcedNode{Identity: "payments-db", Values: values, ValuesMapped: true, Provenance: set})
	w := wrapperOf(t, doc, symbol)
	md, _ := w["metadata"].(map[string]any)
	block, _ := md[ProvenanceMetadataKey].(map[string]any)
	if block["component"] != "payments-db" || block["release"] != "orders" {
		t.Fatalf("wrapper metadata = %v", w["metadata"])
	}
	if got := strings.Count(string(out), ProvenanceMetadataKey); got != 1 {
		t.Errorf("provenance key appears %d times, want once, on the wrapper", got)
	}
	// The inner template bytes are the ones an unstamped render carries.
	plain, _, plainSymbol := renderFixture(t, "module-2.0.json", SourcedNode{Identity: "payments-db", Values: values, ValuesMapped: true})
	inner := func(d map[string]any, s string) string {
		return mustJSON(t, wrapperOf(t, d, s)["properties"].(map[string]any)["template"])
	}
	if inner(doc, symbol) != inner(plain, plainSymbol) {
		t.Errorf("the vendored template changed under provenance")
	}
}

func TestRenderSourcedRefusesUnnameableIdentity(t *testing.T) {
	t.Parallel()
	_, _, _, err := RenderSourced(SourcedNode{Identity: "9lives", Content: fixture(t, "module-1.0.json")})
	if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
		t.Fatalf("want E0291, got %v", err)
	}
}

func TestRenderSourcedWithoutMappingBindsNothing(t *testing.T) {
	t.Parallel()
	doc, _, symbol := renderFixture(t, "module-1.0.json", SourcedNode{Identity: "a"})
	props, _ := wrapperOf(t, doc, symbol)["properties"].(map[string]any)
	if _, present := props["parameters"]; present {
		t.Errorf("parameters written without a values mapping: %v", props["parameters"])
	}
}

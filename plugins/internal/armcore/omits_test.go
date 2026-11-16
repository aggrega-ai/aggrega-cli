// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
)

const (
	typeStorage = "Microsoft.Storage/storageAccounts"
	typeLock    = "Microsoft.Authorization/locks"
)

// parsedTemplate reads a template written as JSON text.
func parsedTemplate(t *testing.T, text string) map[string]any {
	t.Helper()
	doc, err := readTemplate([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return doc.(map[string]any)
}

func omit(kind, name string) rendercore.ResolvedOmit {
	return rendercore.ResolvedOmit{Entry: rendercore.OmitEntry{Kind: kind, Name: name}}
}

// template20 is a languageVersion 2.0 template holding resources and the extra
// top-level JSON members given.
func template20(resources, extra string) string {
	if extra != "" {
		extra = "," + extra
	}
	return `{"$schema":"` + schemaRG + `","languageVersion":"2.0","contentVersion":"1.0.0.0","resources":{` + resources + `}` + extra + `}`
}

// template10 is a template with no languageVersion, holding resources as a list.
func template10(resources, extra string) string {
	if extra != "" {
		extra = "," + extra
	}
	return `{"$schema":"` + schemaRG + `","contentVersion":"1.0.0.0","resources":[` + resources + `]` + extra + `}`
}

const (
	storage20 = `"store":{"type":"` + typeStorage + `","apiVersion":"2023-01-01","name":"s1"}`
	lock20    = `"lock":{"type":"` + typeLock + `","apiVersion":"2020-05-01","name":"l1"}`
)

func TestOmitsRemoveA20Resource(t *testing.T) {
	t.Parallel()
	tpl := parsedTemplate(t, template20(storage20+","+lock20, ""))
	counts, err := applyOmits(tpl, []rendercore.ResolvedOmit{omit(typeLock, "lock")})
	if err != nil {
		t.Fatal(err)
	}
	resources := tpl["resources"].(map[string]any)
	if _, gone := resources["lock"]; gone || len(resources) != 1 {
		t.Fatalf("resources = %v", resources)
	}
	if !slices.Equal(counts, []int{1}) {
		t.Fatalf("counts = %v", counts)
	}
}

func TestOmitsRemoveA20ResourceMatchesKindAndNameExactly(t *testing.T) {
	t.Parallel()
	tpl := parsedTemplate(t, template20(lock20, ""))
	counts, err := applyOmits(tpl, []rendercore.ResolvedOmit{omit(typeStorage, "lock"), omit(typeLock, "Lock")})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(counts, []int{0, 0}) || len(tpl["resources"].(map[string]any)) != 1 {
		t.Fatalf("counts = %v, resources = %v", counts, tpl["resources"])
	}
}

func TestOmitsRemoveA10ResourceByName(t *testing.T) {
	t.Parallel()
	tpl := parsedTemplate(t, template10(
		`{"type":"`+typeStorage+`","apiVersion":"2023-01-01","name":"s1"},`+
			`{"type":"`+typeLock+`","apiVersion":"2020-05-01","name":"[concat(parameters('n'), '-lock')]"},`+
			`{"type":"`+typeLock+`","apiVersion":"2020-05-01","name":"other"}`, ""))
	counts, err := applyOmits(tpl, []rendercore.ResolvedOmit{omit(typeLock, "[concat(parameters('n'), '-lock')]")})
	if err != nil {
		t.Fatal(err)
	}
	resources := tpl["resources"].([]any)
	if len(resources) != 2 || resources[0].(map[string]any)["name"] != "s1" || resources[1].(map[string]any)["name"] != "other" {
		t.Fatalf("resources = %v", resources)
	}
	if !slices.Equal(counts, []int{1}) {
		t.Fatalf("counts = %v", counts)
	}
}

func TestOmitsCountsPerEntry(t *testing.T) {
	t.Parallel()
	tpl := parsedTemplate(t, template10(
		`{"type":"`+typeLock+`","name":"a"},{"type":"`+typeLock+`","name":"a"},{"type":"`+typeStorage+`","name":"s1"}`, ""))
	counts, err := applyOmits(tpl, []rendercore.ResolvedOmit{
		omit(typeLock, "a"), omit(typeLock, "missing"), omit(typeLock, "a"), omit(typeStorage, "s1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(counts, []int{2, 0, 2, 1}) {
		t.Fatalf("counts = %v", counts)
	}
	if got := tpl["resources"].([]any); len(got) != 0 {
		t.Fatalf("resources = %v", got)
	}
}

func TestOmitsNoEntriesChangesNothing(t *testing.T) {
	t.Parallel()
	tpl := parsedTemplate(t, template20(storage20, `"variables":{"x":"[prod]"}`))
	counts, err := applyOmits(tpl, nil)
	if err != nil || len(counts) != 0 {
		t.Fatalf("counts = %v, err = %v", counts, err)
	}
}

func TestOmitsNamespaceEntryHalts(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		entry rendercore.ResolvedOmit
	}{
		{"namespace", rendercore.ResolvedOmit{Entry: rendercore.OmitEntry{Kind: typeLock, Name: "lock", Namespace: "ns"}}},
		{"subchart", rendercore.ResolvedOmit{Entry: rendercore.OmitEntry{Kind: typeLock, Name: "lock", Subchart: "sub"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			tpl := parsedTemplate(t, template20(lock20, ""))
			_, err := applyOmits(tpl, []rendercore.ResolvedOmit{omit(typeLock, "lock"), c.entry})
			if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
				t.Fatalf("want E0288, got %v", err)
			}
			if !strings.HasPrefix(err.Error(), "arm-provider:") && !strings.Contains(err.Error(), "arm-provider:") {
				t.Errorf("message lacks the provider prefix: %v", err)
			}
			if len(tpl["resources"].(map[string]any)) != 1 {
				t.Errorf("a refused entry list removed a resource")
			}
		})
	}
}

func TestOmitsReferenceToRemovedHalts(t *testing.T) {
	t.Parallel()
	prop := func(expr string) string { // a 2.0 resource whose property holds expr
		return `"app":{"type":"Microsoft.Web/sites","name":"w","properties":{"p":"` + expr + `"}}`
	}
	cases := []struct {
		name     string
		template string
		entry    rendercore.ResolvedOmit
		form     string
		pointer  string
	}{
		{"2.0 dependsOn", template20(lock20+`,"app":{"type":"Microsoft.Web/sites","name":"w","dependsOn":["lock"]}`, ""),
			omit(typeLock, "lock"), "dependsOn", "/resources/app/dependsOn/0"},
		{"2.0 dependsOn by head", template20(lock20+`,"app":{"type":"Microsoft.Web/sites","name":"w","dependsOn":["lock::child"]}`, ""),
			omit(typeLock, "lock"), "dependsOn", "/resources/app/dependsOn/0"},
		{"2.0 reference", template20(lock20+","+prop("[reference('lock').id]"), ""),
			omit(typeLock, "lock"), "reference", "/resources/app/properties/p"},
		{"2.0 references", template20(lock20+","+prop("[references('lock')]"), ""),
			omit(typeLock, "lock"), "references", "/resources/app/properties/p"},
		{"2.0 resourceInfo", template20(lock20+","+prop("[resourceInfo('lock').id]"), ""),
			omit(typeLock, "lock"), "resourceInfo", "/resources/app/properties/p"},
		{"2.0 list function", template20(lock20+","+prop("[listKeys('lock', '2020-05-01').keys[0].value]"), ""),
			omit(typeLock, "lock"), "listKeys", "/resources/app/properties/p"},
		{"2.0 output", template20(lock20, `"outputs":{"o":{"type":"string","value":"[reference('lock').id]"}}`),
			omit(typeLock, "lock"), "reference", "/outputs/o/value"},
		{"1.0 dependsOn", template10(`{"type":"`+typeLock+`","name":"l1"},{"type":"Microsoft.Web/sites","name":"w","dependsOn":["l1"]}`, ""),
			omit(typeLock, "l1"), "dependsOn", "/resources/1/dependsOn/0"},
		{"1.0 reference", template10(`{"type":"`+typeLock+`","name":"l1"},{"type":"Microsoft.Web/sites","name":"w","properties":{"p":"[reference('l1')]"}}`, ""),
			omit(typeLock, "l1"), "reference", "/resources/1/properties/p"},
		{"1.0 resourceId", template10(`{"type":"`+typeLock+`","name":"l1"},{"type":"Microsoft.Web/sites","name":"w","dependsOn":["[resourceId('`+typeLock+`', 'l1')]"]}`, ""),
			omit(typeLock, "l1"), "resourceId", "/resources/1/dependsOn/0"},
		{"1.0 resourceId with a resource group", template10(`{"type":"`+typeLock+`","name":"l1"}`, `"outputs":{"o":{"type":"string","value":"[resourceId('rg', '`+typeLock+`', 'l1')]"}}`),
			omit(typeLock, "l1"), "resourceId", "/outputs/o/value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := applyOmits(parsedTemplate(t, c.template), []rendercore.ResolvedOmit{c.entry})
			if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
				t.Fatalf("want E0289, got %v", err)
			}
			msg := err.Error()
			for _, want := range []string{"arm-provider:", "the sourced template", c.entry.Entry.Name, c.form, c.pointer} {
				if !strings.Contains(msg, want) {
					t.Errorf("message lacks %q: %s", want, msg)
				}
			}
		})
	}
}

func TestOmitsReferenceToAnotherResourceIsKept(t *testing.T) {
	t.Parallel()
	tpl := parsedTemplate(t, template20(lock20+","+storage20+
		`,"app":{"type":"Microsoft.Web/sites","name":"w","dependsOn":["store"],"properties":{"p":"[reference('store').id]","q":"[resourceId('`+typeStorage+`', 's1')]"}}`, ""))
	if _, err := applyOmits(tpl, []rendercore.ResolvedOmit{omit(typeLock, "lock")}); err != nil {
		t.Fatal(err)
	}
}

func TestOmitsWithheldNameIsNotQuoted(t *testing.T) {
	t.Parallel()
	tpl := parsedTemplate(t, template20(`"secretlock":{"type":"`+typeLock+`","name":"l"},"app":{"type":"Microsoft.Web/sites","name":"w","dependsOn":["secretlock"]}`, ""))
	e := omit(typeLock, "secretlock")
	e.Withheld.Name = true
	_, err := applyOmits(tpl, []rendercore.ResolvedOmit{e})
	if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
		t.Fatalf("want E0289, got %v", err)
	}
	if strings.Contains(err.Error(), "secretlock") {
		t.Errorf("message quotes a withheld name: %v", err)
	}
}

func TestOmitsUnparsableExpressionHaltsWhereAnEntryMatched(t *testing.T) {
	t.Parallel()
	extra := `"variables":{"v":"[prod]"}`
	t.Run("matched", func(t *testing.T) {
		t.Parallel()
		_, err := applyOmits(parsedTemplate(t, template20(lock20, extra)), []rendercore.ResolvedOmit{omit(typeLock, "lock")})
		if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
			t.Fatalf("want E0291, got %v", err)
		}
		if !strings.Contains(err.Error(), "/variables/v") {
			t.Errorf("message lacks the pointer: %v", err)
		}
	})
	t.Run("no entry matched", func(t *testing.T) {
		t.Parallel()
		tpl := parsedTemplate(t, template20(lock20, extra))
		counts, err := applyOmits(tpl, []rendercore.ResolvedOmit{omit(typeLock, "other")})
		if err != nil || !slices.Equal(counts, []int{0}) {
			t.Fatalf("counts = %v, err = %v", counts, err)
		}
		if tpl["variables"].(map[string]any)["v"] != "[prod]" {
			t.Errorf("template changed")
		}
	})
}

func TestOmitsFirstHaltIsStable(t *testing.T) {
	t.Parallel()
	text := template20(lock20+`,"b":{"type":"x/y","name":"b","dependsOn":["lock"]},"a":{"type":"x/y","name":"a","dependsOn":["lock"]}`, "")
	first := ""
	for i := range 20 {
		_, err := applyOmits(parsedTemplate(t, text), []rendercore.ResolvedOmit{omit(typeLock, "lock")})
		if err == nil {
			t.Fatal("want a halt")
		}
		if i == 0 {
			first = err.Error()
		} else if err.Error() != first {
			t.Fatalf("halt differs:\n%s\n%s", first, err.Error())
		}
	}
	if !strings.Contains(first, "/resources/a/") {
		t.Errorf("first halt is not the first resource in key order: %s", first)
	}
}

func TestRenderSourcedLeavesOutResourcesBeforeProvenance(t *testing.T) {
	t.Parallel()
	node := SourcedNode{
		Identity:   "a",
		Content:    []byte(template20(storage20+","+lock20, "")),
		Omits:      []rendercore.ResolvedOmit{omit(typeLock, "lock"), omit(typeLock, "none")},
		Provenance: map[string]string{"component": "c"},
	}
	out, _, omitted, err := RenderSourced(node)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(omitted, []int{1, 0}) {
		t.Fatalf("omitted = %v", omitted)
	}
	if strings.Contains(string(out), typeLock) {
		t.Errorf("the left-out resource is still in the fragment")
	}
	if !strings.Contains(string(out), typeStorage) {
		t.Errorf("the kept resource is missing")
	}
}

func TestRenderSourcedHaltsWhereARemainingResourceNamesAnOmittedOne(t *testing.T) {
	t.Parallel()
	_, _, _, err := RenderSourced(SourcedNode{
		Identity: "a",
		Content:  []byte(template20(lock20+`,"app":{"type":"Microsoft.Web/sites","name":"w","dependsOn":["lock"]}`, "")),
		Omits:    []rendercore.ResolvedOmit{omit(typeLock, "lock")},
	})
	if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
		t.Fatalf("want E0289, got %v", err)
	}
}

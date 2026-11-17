// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

const fxQueueOnlyRecipe = "catalog/recipe/sample/queue-sample-store-queue/0.1.0/recipe.yaml"

// fxCoreQueue is a catalog Component the sample queue type can realize: one
// values key matches a property by name and one matches none, one provides key
// matches an attribute read back and one matches none.
func fxCoreQueue() aggrega.CatalogComponent {
	return aggrega.CatalogComponent{Name: "core/messaging/queue", Version: "1.2.0",
		Values: []string{"flavour", "queue_name"}, Provides: []string{"arn", "endpoint"},
		Labels: map[string]string{"component.aggrega.ai/category": "messaging"}}
}

func fxRecipeOnly(t *testing.T) (aggrega.ImportOutput, Resource) {
	t.Helper()
	in := fxInput(t, `{"resource_types": [{"type": "Sample::Store::Queue", "component": "core/messaging/queue"}]}`)
	in.Catalog = fxView(fxCoreQueue())
	f, got := fxCapturing(fxFormat())
	out := Import(in, f, fxParse)
	fxNoHalt(t, out)
	if len(*got) != 1 {
		t.Fatalf("the writer was handed %d resources", len(*got))
	}
	return out, (*got)[0]
}

func TestRecipeOnlyMapsValuesAndRequiresUnmatched(t *testing.T) {
	out, r := fxRecipeOnly(t)
	if len(out.Entities) != 1 || out.Entities[0].Kind != "Recipe" || out.Entities[0].Name != "queue-sample-store-queue" ||
		out.Entities[0].Mode != "draft" {
		t.Fatalf("entities %+v, want the Recipe alone", out.Entities)
	}
	recipe := fxYAML(t, fxFile(t, out, fxQueueOnlyRecipe))
	if got := at(recipe, "spec", "implements"); got == nil || got.Value != "core/messaging/queue:1.2.0" {
		t.Errorf("implements %v, want the version the catalog holds", got)
	}
	for _, f := range out.Files {
		if strings.HasPrefix(f.Path, "catalog/component/") {
			t.Errorf("a Recipe-only import drafted %s", f.Path)
		}
	}

	var sources []string
	for _, in := range r.Inputs {
		sources = append(sources, fmt.Sprintf("%s %d %s %v", in.Property.Name, in.Source.Pillar, in.Source.Key, in.Required))
	}
	want := []string{
		fmt.Sprintf("QueueName %d queue_name true", PillarValues),
		fmt.Sprintf("Region %d Region true", PillarOverrides),
		fmt.Sprintf("RetentionDays %d RetentionDays true", PillarOverrides),
		fmt.Sprintf("DelaySeconds %d DelaySeconds false", PillarOverrides),
		fmt.Sprintf("Labels %d Labels false", PillarOverrides),
		fmt.Sprintf("OldMode %d OldMode false", PillarOverrides),
		fmt.Sprintf("AccessToken %d access_token false", PillarSecret),
	}
	if !reflect.DeepEqual(sources, want) {
		t.Fatalf("inputs\n%s\nwant\n%s", strings.Join(sources, "\n"), strings.Join(want, "\n"))
	}

	assertions := fxRecipeAssertions(t, fxFile(t, out, fxQueueOnlyRecipe))
	required, ok := assertions["required_overrides_set"]
	if !ok {
		t.Fatalf("no required_overrides_set among %v", assertions)
	}
	err := fxAssertion(required, fxAssertionContext(nil, nil))
	if err == nil || !strings.Contains(err.Error(), "infra.overrides.Region is not set") {
		t.Errorf("no override set: %v", err)
	}
	err = fxAssertion(required, fxAssertionContext(map[string]any{"Region": "north"}, nil))
	if err == nil || !strings.Contains(err.Error(), "infra.overrides.RetentionDays is not set") {
		t.Errorf("one override set: %v", err)
	}
	if err := fxAssertion(required, fxAssertionContext(map[string]any{"Region": "north", "RetentionDays": 0}, nil)); err != nil {
		t.Errorf("both overrides set: %v", err)
	}
	reads := assertions["overrides_this_recipe_reads"]
	all := map[string]any{"Region": "north", "RetentionDays": 1, "DelaySeconds": 5, "Labels": map[string]any{}, "OldMode": true}
	if err := fxAssertion(reads, fxAssertionContext(all, nil)); err != nil {
		t.Errorf("every override the Recipe reads: %v", err)
	}
	if err := fxAssertion(reads, fxAssertionContext(map[string]any{"QueueName": "x"}, nil)); err == nil {
		t.Errorf("a property the Recipe reads from values is admitted under infra.overrides")
	}

	body := fxFile(t, out, fxQueueOnlyRecipe)
	if !strings.Contains(body, "# values.flavour of core/messaging/queue matches no property of Sample::Store::Queue") {
		t.Errorf("the header does not list the values key matched to nothing:\n%s", body)
	}
	if len(out.Decisions) != 1 {
		t.Fatalf("decisions %+v", out.Decisions)
	}
	d := out.Decisions[0]
	if d.Chosen == nil || d.Chosen.Target != "core/messaging/queue" || d.Chosen.Mode != "draft" {
		t.Errorf("chosen %+v", d.Chosen)
	}
	if want := "1 values, 0 infra, 5 overrides, 1 secrets, 1 provides"; d.SourceNode.ParamsSummary != want {
		t.Errorf("params summary %q, want %q", d.SourceNode.ParamsSummary, want)
	}
	for _, want := range []string{"flavour", "Region and RetentionDays", "endpoint"} {
		if !strings.Contains(d.Rationale, want) {
			t.Errorf("rationale %q does not name %q", d.Rationale, want)
		}
	}
}

func TestRecipeOnlyWritesFailForUnmappedProvides(t *testing.T) {
	out, r := fxRecipeOnly(t)
	if want := []Provide{{Key: "arn", Attribute: []string{"Arn"}}}; !reflect.DeepEqual(r.Provides, want) {
		t.Errorf("provides %+v, want %+v", r.Provides, want)
	}
	if want := []Unmapped{{Key: "endpoint", Candidates: []string{"Url"}}}; !reflect.DeepEqual(r.Unmapped, want) {
		t.Fatalf("unmapped %+v, want %+v", r.Unmapped, want)
	}
	value := `{{ fail "provides.endpoint is not mapped to an attribute of Sample::Store::Queue: Url" }}`
	if got := r.Unmapped[0].Value(r.Type); got != value {
		t.Errorf("value %s, want %s", got, value)
	}
	recipe := fxYAML(t, fxFile(t, out, fxQueueOnlyRecipe))
	if got := at(recipe, "spec", "provides", "endpoint"); got == nil || got.Value != value {
		t.Errorf("provides.endpoint %v, want %s", got, value)
	}
	d := fxDiagnostic(t, out, "warn", codeProvidesUnmapped)
	for _, want := range []string{"endpoint", "core/messaging/queue", "Url"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("message %q does not name %q", d.Message, want)
		}
	}

	// The value halts as a provides value evaluates it, naming the key.
	funcs := gotmpl.FuncMap()
	funcs["fail"] = func(msg string) (string, error) { return "", errors.New(msg) }
	tmpl, err := template.New("provides").Funcs(funcs).Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	err = tmpl.Execute(new(strings.Builder), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "provides.endpoint is not mapped") {
		t.Errorf("the value evaluates to %v", err)
	}
}

func TestComponentDraftedNewWarns(t *testing.T) {
	in := fxInput(t, `{"resource_types": [{"type": "Sample::Store::Queue", "component": "acme/messaging/queue"}]}`)
	in.Catalog = fxView(fxCoreQueue())
	out := Import(in, fxFormat(), fxParse)
	fxNoHalt(t, out)
	d := fxDiagnostic(t, out, "warn", codeComponentDraftedNew)
	if !strings.Contains(d.Message, "acme/messaging/queue") || !strings.Contains(d.Message, fxQueue) {
		t.Errorf("message %q", d.Message)
	}
	fxFile(t, out, "catalog/component/acme/messaging/queue/0.1.0/component.yaml")
	recipe := fxYAML(t, fxFile(t, out, "catalog/recipe/sample/queue/0.1.0/recipe.yaml"))
	if got := at(recipe, "spec", "implements"); got == nil || got.Value != "acme/messaging/queue:0.1.0" {
		t.Errorf("implements %v", got)
	}
	if _, ok := fxRecipeAssertions(t, fxFile(t, out, "catalog/recipe/sample/queue/0.1.0/recipe.yaml"))["required_overrides_set"]; ok {
		t.Errorf("a drafted Component's Recipe requires overrides")
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"text/template"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// The format under test reads its own JSON: a list of types, each with its
// properties in order, every flag the model carries written out. Its writer
// echoes each input it is handed, so a test reads what the core passed it.
const (
	fxFormatName   = "sample-schema"
	fxCapability   = "sample"
	fxQueue        = "Sample::Store::Queue"
	fxBucket       = "Sample::Store::Bucket"
	codeUnreadable = "resource-schema-unreadable"
	codeRefused    = "resource-type-refused"
	codeAmbiguous  = "resource-type-ambiguous"
)

func fxParse(syntax string, source []byte) (json.RawMessage, error) {
	if syntax != "json" {
		return nil, fmt.Errorf("no parser for %s", syntax)
	}
	if !json.Valid(source) {
		return nil, errors.New("not valid JSON")
	}
	return json.RawMessage(source), nil
}

func fxUnreadable(source, member string) aggrega.Diagnostic {
	return aggrega.Diagnostic{Severity: "halt", Code: codeUnreadable, SourceRef: source,
		Message: fmt.Sprintf("the source %q holds no readable %s", source, member)}
}

func fxList(in aggrega.ImportInput, parse ParseFunc) ([]TypeRef, []aggrega.Diagnostic) {
	var refs []TypeRef
	var diags []aggrega.Diagnostic
	for _, src := range importer.Sources(in) {
		named := importer.Named(in, src)
		tree, err := parse("json", src.Bytes)
		if err != nil {
			diags = append(diags, importer.ParseFailed(named, fxFormatName, err))
			continue
		}
		var doc struct {
			Types []json.RawMessage `json:"types"`
		}
		if json.Unmarshal(tree, &doc) != nil || doc.Types == nil {
			diags = append(diags, fxUnreadable(named.Name, "types"))
			continue
		}
		for _, raw := range doc.Types {
			var entry struct {
				Name *string `json:"name"`
			}
			if json.Unmarshal(raw, &entry) != nil || entry.Name == nil {
				diags = append(diags, fxUnreadable(named.Name, "name"))
				continue
			}
			refs = append(refs, TypeRef{Name: *entry.Name, Source: named.Name, Body: raw})
		}
	}
	return refs, diags
}

func fxLift(ref TypeRef) (*Type, []aggrega.Diagnostic) {
	var body struct {
		Description string            `json:"description"`
		Identifier  string            `json:"identifier"`
		Properties  []json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(ref.Body, &body) != nil {
		return nil, []aggrega.Diagnostic{fxUnreadable(ref.Source, "properties")}
	}
	t := &Type{Name: ref.Name, Description: body.Description, Source: ref.Source}
	if body.Identifier != "" {
		t.PrimaryIdentifier = [][]string{{body.Identifier}}
	}
	for _, raw := range body.Properties {
		var p struct {
			Name                                                        *string
			Kind                                                        Kind
			Description                                                 string
			Required, ReadOnly, Sensitive, CreateOnly, Deprecated, Open bool
		}
		if json.Unmarshal(raw, &p) != nil || p.Name == nil {
			return nil, []aggrega.Diagnostic{fxUnreadable(ref.Source, "properties[].name")}
		}
		if !slices.Contains([]Kind{KindString, KindInteger, KindNumber, KindBoolean, KindObject, KindArray, KindAny}, p.Kind) {
			return nil, []aggrega.Diagnostic{fxUnreadable(ref.Source, "properties[].kind")}
		}
		t.Properties = append(t.Properties, &Property{Name: *p.Name, Path: []string{*p.Name}, Kinds: []Kind{p.Kind},
			Description: p.Description, Required: p.Required, ReadOnly: p.ReadOnly, Sensitive: p.Sensitive,
			CreateOnly: p.CreateOnly, Deprecated: p.Deprecated, Open: p.Open})
	}
	return t, nil
}

func fxRefuse(t *Type) *aggrega.Diagnostic {
	if !strings.HasSuffix(t.Name, "::Module") {
		return nil
	}
	return &aggrega.Diagnostic{Severity: "halt", Code: codeRefused, SourceRef: t.Source,
		Message: fmt.Sprintf("the provider cannot realize %s", t.Name)}
}

// fxWrite echoes each input as a line of its one artifact, gated by its
// presence where it is optional, each provide naming its attribute, and each
// unmapped provides key as the core writes it.
func fxWrite(r Resource) (importer.RecipeDraft, []aggrega.Diagnostic) {
	var lines, names []string
	for _, in := range r.Inputs {
		line := in.Property.Name + ": {{ toJson " + in.Source.Template() + " }}"
		if !in.Required {
			line = "{{ if " + in.Source.Present() + " }}" + line + "{{ end }}"
		}
		lines = append(lines, line)
		names = append(names, in.Property.Name)
	}
	d := importer.RecipeDraft{
		Header:     []string{"Written by the sample format's writer."},
		Locals:     []importer.Entry{{Key: "name", Value: "{{ .context.instance_id }}"}},
		Assertions: []importer.Entry{{Key: "sample_rule", Value: "{{- /* the target's own rule */ -}}"}},
		Artifacts:  []importer.Entry{{Key: "resource.txt", Value: strings.Join(lines, "\n") + "\n"}},
		Files:      []importer.RecipeFile{{Path: "notes/inputs.txt", Content: strings.Join(names, "\n") + "\n"}},
	}
	for _, p := range r.Provides {
		value := "attribute " + strings.Join(p.Attribute, ".")
		if p.Identifier {
			value = "identifier " + strings.Join(p.Attribute, ".")
		}
		d.Provides = append(d.Provides, importer.Entry{Key: p.Key, Value: value})
	}
	for _, u := range r.Unmapped {
		d.Provides = append(d.Provides, importer.Entry{Key: u.Key, Value: u.Value(r.Type)})
	}
	return d, nil
}

func fxFormat() Format {
	return Format{
		Name: fxFormatName, Capability: fxCapability, Syntax: "json",
		List: fxList, Lift: fxLift, Refuse: fxRefuse,
		Rules: Rules{
			Placement: func(_ *Type, p *Property) bool { return p.Name == "Region" },
			Identity:  func(_ *Type, p *Property) bool { return p.Name == "QueueName" || p.Name == "BucketName" },
			Secret:    func(_ *Type, p *Property) bool { return p.Sensitive },
		},
		Category: func(t *Type) string {
			if strings.HasSuffix(t.Name, "::Queue") {
				return "messaging"
			}
			return ""
		},
		Write: fxWrite,
	}
}

// fxCapturing returns f writing through its own writer, recording each
// Resource it is handed.
func fxCapturing(f Format) (Format, *[]Resource) {
	var got []Resource
	write := f.Write
	f.Write = func(r Resource) (importer.RecipeDraft, []aggrega.Diagnostic) {
		got = append(got, r)
		return write(r)
	}
	return f, &got
}

func fxTypes(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "sample-types.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// fxInput is a request to import the sample types, under the settings given
// as JSON, empty for none.
func fxInput(t *testing.T, settings string) aggrega.ImportInput {
	t.Helper()
	in := aggrega.ImportInput{Format: fxFormatName, Name: "sample-types.json", Bytes: fxTypes(t)}
	if settings != "" {
		in.Settings = json.RawMessage(settings)
	}
	return in
}

func fxView(components ...aggrega.CatalogComponent) *aggrega.CatalogProjection {
	return &aggrega.CatalogProjection{Components: components}
}

func fxHalts(out aggrega.ImportOutput) []aggrega.Diagnostic {
	var halts []aggrega.Diagnostic
	for _, d := range out.Diagnostics {
		if d.Severity == "halt" {
			halts = append(halts, d)
		}
	}
	return halts
}

func fxNoHalt(t *testing.T, out aggrega.ImportOutput) {
	t.Helper()
	if h := fxHalts(out); len(h) > 0 {
		t.Fatalf("the import halted: %+v", h)
	}
}

func fxDiagnostic(t *testing.T, out aggrega.ImportOutput, severity, code string) aggrega.Diagnostic {
	t.Helper()
	for _, d := range out.Diagnostics {
		if d.Code == code && d.Severity == severity {
			return d
		}
	}
	t.Fatalf("no %s %s among %+v", severity, code, out.Diagnostics)
	return aggrega.Diagnostic{}
}

func fxFile(t *testing.T, out aggrega.ImportOutput, path string) string {
	t.Helper()
	for _, f := range out.Files {
		if f.Path == path {
			return f.Content
		}
	}
	t.Fatalf("no file %s among %v", path, fxPaths(out))
	return ""
}

func fxPaths(out aggrega.ImportOutput) []string {
	var paths []string
	for _, f := range out.Files {
		paths = append(paths, f.Path)
	}
	return paths
}

func fxYAML(t *testing.T, body string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("%v\n%s", err, body)
	}
	return doc.Content[0]
}

// fxAssertion evaluates an assertion body as the compiler's assertion surface
// does: its function set, a fail that halts with the message, and a key the
// context does not hold halting.
func fxAssertion(body string, ctx map[string]any) error {
	funcs := gotmpl.FuncMap()
	for name, fn := range gotmpl.AssertionComparisons() {
		funcs[name] = fn
	}
	funcs["fail"] = func(msg string) (string, error) { return "", errors.New(msg) }
	tmpl, err := template.New("assertion").Option("missingkey=error").Funcs(funcs).Parse(body)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, ctx); err != nil {
		return err
	}
	if strings.TrimSpace(b.String()) != "" {
		return fmt.Errorf("the assertion wrote %q", b.String())
	}
	return nil
}

// fxAssertionContext is an assertion's context: overrides under infra, and
// attachments under features, for the instance orders-queue.
func fxAssertionContext(overrides, features map[string]any) map[string]any {
	infra := map[string]any{}
	if overrides != nil {
		infra["overrides"] = overrides
	}
	if features == nil {
		features = map[string]any{}
	}
	return map[string]any{"values": map[string]any{}, "infra": infra, "features": features,
		"metadata": map[string]any{"instance_id": "orders-queue"}, "locals": map[string]any{}}
}

func fxRecipeAssertions(t *testing.T, recipe string) map[string]string {
	t.Helper()
	n := at(fxYAML(t, recipe), "spec", "assertions")
	out := map[string]string{}
	for i := 0; n != nil && i+1 < len(n.Content); i += 2 {
		out[n.Content[i].Value] = n.Content[i+1].Value
	}
	return out
}

const (
	fxQueueComponent = "catalog/component/imported/messaging/sample-store-queue/0.1.0/component.yaml"
	fxQueueRecipe    = "catalog/recipe/sample/sample-store-queue/0.1.0/recipe.yaml"
)

func TestImportDraftsComponentAndRecipe(t *testing.T) {
	in := fxInput(t, `{"resource_types": ["Sample::Store::Queue"]}`)
	in.Target = "terraform"
	out := Import(in, fxFormat(), fxParse)
	fxNoHalt(t, out)

	wantEntities := []string{"Component imported/messaging/sample-store-queue draft", "Recipe sample-store-queue draft"}
	var entities []string
	for _, e := range out.Entities {
		entities = append(entities, e.Kind+" "+e.Name+" "+e.Mode)
	}
	if !reflect.DeepEqual(entities, wantEntities) {
		t.Fatalf("entities %v, want %v", entities, wantEntities)
	}
	wantPaths := []string{fxQueueComponent, fxQueueRecipe,
		"catalog/recipe/sample/sample-store-queue/0.1.0/notes/inputs.txt"}
	if !reflect.DeepEqual(fxPaths(out), wantPaths) {
		t.Fatalf("files %v, want %v", fxPaths(out), wantPaths)
	}

	recipe := fxYAML(t, fxFile(t, out, fxQueueRecipe))
	for path, want := range map[string]string{
		"metadata.name":    "sample-store-queue",
		"metadata.version": "0.1.0",
		"spec.capability":  fxCapability,
		"spec.implements":  "imported/messaging/sample-store-queue:0.1.0",
	} {
		if got := at(recipe, strings.Split(path, ".")...); got == nil || got.Value != want {
			t.Errorf("%s = %v, want %s", path, got, want)
		}
	}
	if got := keysOf(at(recipe, "spec", "provides")); !reflect.DeepEqual(got, []string{"arn", "url"}) {
		t.Errorf("provides %v, want [arn url]", got)
	}
	if v := at(recipe, "spec", "provides", "url"); v == nil || v.Value != "identifier Url" {
		t.Errorf("provides.url %v, want the identifier", v)
	}

	// The drafts load as a catalog: the Component, and the Recipe implementing it.
	dir := t.TempDir()
	for _, f := range out.Files {
		path := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(f.Content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cat, err := catalog.LoadAll("", filepath.Join(dir, "catalog"))
	if err != nil {
		t.Fatalf("the catalog refuses the drafts: %v", err)
	}
	if _, ok := cat.Components["imported/messaging/sample-store-queue:0.1.0"]; !ok {
		t.Errorf("the catalog holds no drafted Component")
	}

	// A type no category of the format covers takes the kit's segment.
	out = Import(fxInput(t, `{"resource_types": ["Sample::Store::Bucket"]}`), fxFormat(), fxParse)
	fxNoHalt(t, out)
	fxFile(t, out, "catalog/component/imported/component/sample-store-bucket/0.1.0/component.yaml")
	recipe = fxYAML(t, fxFile(t, out, "catalog/recipe/sample/sample-store-bucket/0.1.0/recipe.yaml"))
	if got := at(recipe, "spec", "implements"); got == nil || got.Value != "imported/component/sample-store-bucket:0.1.0" {
		t.Errorf("implements %v", got)
	}
	if len(out.Decisions) != 1 || !strings.Contains(out.Decisions[0].Rationale, "category segment component") {
		t.Errorf("the decision does not say the category fell back: %+v", out.Decisions)
	}
}

func TestImportUnderTargetNamespace(t *testing.T) {
	out := Import(fxInput(t, `{"resource_types": ["Sample::Store::Queue"], "target_namespace": "acme"}`),
		fxFormat(), fxParse)
	fxNoHalt(t, out)
	fxFile(t, out, "catalog/component/acme/messaging/sample-store-queue/0.1.0/component.yaml")
	recipe := fxYAML(t, fxFile(t, out, fxQueueRecipe))
	if got := at(recipe, "spec", "implements"); got == nil || got.Value != "acme/messaging/sample-store-queue:0.1.0" {
		t.Errorf("implements %v", got)
	}
}

func TestImportSharedAssertions(t *testing.T) {
	out := Import(fxInput(t, `{"resource_types": ["Sample::Store::Queue"]}`), fxFormat(), fxParse)
	fxNoHalt(t, out)
	assertions := fxRecipeAssertions(t, fxFile(t, out, fxQueueRecipe))
	names := slices.Sorted(func(yield func(string) bool) {
		for k := range assertions {
			if !yield(k) {
				return
			}
		}
	})
	want := []string{"overrides_this_recipe_reads", "sample_rule", "secrets_are_parameters"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("assertions %v, want %v", names, want)
	}

	reads := assertions["overrides_this_recipe_reads"]
	if err := fxAssertion(reads, fxAssertionContext(nil, nil)); err != nil {
		t.Errorf("no overrides: %v", err)
	}
	set := map[string]any{"DelaySeconds": 5, "Labels": map[string]any{"team": "a"}, "OldMode": false}
	if err := fxAssertion(reads, fxAssertionContext(set, nil)); err != nil {
		t.Errorf("the drafted overrides: %v", err)
	}
	err := fxAssertion(reads, fxAssertionContext(map[string]any{"DelaySeconds": 5, "Region": "x"}, nil))
	if err == nil || !strings.Contains(err.Error(), "infra.overrides.Region is not a setting this Recipe reads") ||
		!strings.Contains(err.Error(), "DelaySeconds, Labels and OldMode") {
		t.Errorf("an override the Recipe does not read: %v", err)
	}

	secrets := assertions["secrets_are_parameters"]
	if err := fxAssertion(secrets, fxAssertionContext(nil, nil)); err != nil {
		t.Errorf("no attachment: %v", err)
	}
	attached := map[string]any{"access_token": map[string]any{"type": "core/secret-value"}}
	err = fxAssertion(secrets, fxAssertionContext(nil, attached))
	if err == nil || !strings.Contains(err.Error(), "access_token") ||
		!strings.Contains(err.Error(), "OrdersQueueAccessToken") {
		t.Errorf("an attachment under a secret's key: %v", err)
	}

	// Secrets drafted as slots are attached, so no assertion refuses one.
	slots := fxFormat()
	slots.Rules.SecretSlots = true
	out = Import(fxInput(t, `{"resource_types": ["Sample::Store::Queue"]}`), slots, fxParse)
	fxNoHalt(t, out)
	if _, ok := fxRecipeAssertions(t, fxFile(t, out, fxQueueRecipe))["secrets_are_parameters"]; ok {
		t.Errorf("a draft with secret slots refuses their attachment")
	}
}

func TestImportHeaderListsEveryKeyRead(t *testing.T) {
	out := Import(fxInput(t, `{"resource_types": ["Sample::Store::Queue"]}`), fxFormat(), fxParse)
	fxNoHalt(t, out)
	body := fxFile(t, out, fxQueueRecipe)
	var comments []string
	for _, line := range strings.Split(body, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "#") {
			comments = append(comments, strings.TrimPrefix(trimmed, "# "))
		}
	}
	for _, want := range [][2]string{
		{"values.queue_name: string, required; identity", Marked("The name of the queue.")},
		{"infra.region: string, required; placement", Marked("Where the queue is placed.")},
		{"infra.retention_days: integer, required; required", Marked("How long a message is kept.")},
		{"infra.overrides.DelaySeconds: integer; override", Marked("How long a message waits.")},
		{"infra.overrides.Labels: object; override", "The vendor schema gives no description of Labels."},
		{"infra.overrides.OldMode: boolean; override", Marked("A mode kept for older readers.")},
		{"parameter <instance>AccessToken: string; secret", Marked("The token a client presents.")},
	} {
		i := slices.IndexFunc(comments, func(c string) bool { return strings.HasPrefix(c, want[0]) })
		if i < 0 || i+1 >= len(comments) || comments[i+1] != want[1] {
			t.Errorf("no header line %q followed by %q in\n%s", want[0], want[1], strings.Join(comments, "\n"))
		}
	}
	for _, c := range comments {
		if strings.HasPrefix(c, "infra.overrides.DelaySeconds") && !strings.Contains(c, "replaced on change") {
			t.Errorf("a create-only override is not marked replaced on change: %q", c)
		}
		if strings.HasPrefix(c, "infra.overrides.OldMode") && !strings.Contains(c, "deprecated") {
			t.Errorf("a deprecated override is not marked: %q", c)
		}
		if strings.Contains(c, VendorTextMarker) && !strings.HasPrefix(c, VendorTextMarker) {
			t.Errorf("vendor text does not open its comment line: %q", c)
		}
	}
	if !slices.Contains(comments, "Written by the sample format's writer.") {
		t.Errorf("the writer's own header line is lost")
	}
}

func TestImportInputTemplatesReadTheirPillars(t *testing.T) {
	f, got := fxCapturing(fxFormat())
	fxNoHalt(t, Import(fxInput(t, `{"resource_types": ["Sample::Store::Queue"]}`), f, fxParse))
	if len(*got) != 1 {
		t.Fatalf("the writer was handed %d resources", len(*got))
	}
	r := (*got)[0]
	var sources []string
	for _, in := range r.Inputs {
		sources = append(sources, fmt.Sprintf("%s %d %s %v %v", in.Property.Name, in.Source.Pillar, in.Source.Key,
			in.Required, in.Secret))
	}
	want := []string{
		fmt.Sprintf("QueueName %d queue_name true false", PillarValues),
		fmt.Sprintf("Region %d region true false", PillarInfra),
		fmt.Sprintf("RetentionDays %d retention_days true false", PillarInfra),
		fmt.Sprintf("DelaySeconds %d DelaySeconds false false", PillarOverrides),
		fmt.Sprintf("Labels %d Labels false false", PillarOverrides),
		fmt.Sprintf("OldMode %d OldMode false false", PillarOverrides),
		fmt.Sprintf("AccessToken %d access_token false true", PillarSecret),
	}
	if !reflect.DeepEqual(sources, want) {
		t.Fatalf("inputs\n%s\nwant\n%s", strings.Join(sources, "\n"), strings.Join(want, "\n"))
	}
	wantProvides := []Provide{{Key: "arn", Attribute: []string{"Arn"}}, {Key: "url", Attribute: []string{"Url"}, Identifier: true}}
	if !reflect.DeepEqual(r.Provides, wantProvides) || r.Unmapped != nil {
		t.Fatalf("provides %+v, unmapped %+v", r.Provides, r.Unmapped)
	}

	ctx := map[string]any{
		"values":   map[string]any{"queue_name": "orders"},
		"infra":    map[string]any{"region": "north", "overrides": map[string]any{"DelaySeconds": 0}},
		"features": map[string]any{"access_token": map[string]any{"values": map[string]any{"key": "TOKEN"}}},
		"context": map[string]any{"inject": map[string]any{
			"env":  map[string]any{"TOKEN": "s3cr3t"},
			"pipe": map[string]any{"upstream_url": "https://u"},
		}},
	}
	eval := func(text string) (string, error) {
		tmpl, err := template.New("t").Option("missingkey=error").Funcs(gotmpl.FuncMap()).Parse(text)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		err = tmpl.Execute(&b, ctx)
		return b.String(), err
	}
	inject := InputSource{Pillar: PillarInject, Key: "upstream_url", Mechanism: "pipe"}
	for _, c := range []struct {
		source  InputSource
		value   string
		present bool
	}{
		{r.Inputs[0].Source, `"orders"`, true},
		{r.Inputs[1].Source, `"north"`, true},
		{r.Inputs[3].Source, `0`, true},
		{r.Inputs[4].Source, "", false},
		{r.Inputs[6].Source, `"s3cr3t"`, true},
		{inject, `"https://u"`, true},
		{InputSource{Pillar: PillarInject, Key: "other", Mechanism: "pipe"}, "", false},
		{InputSource{Pillar: PillarInject, Key: "other", Mechanism: "none"}, "", false},
	} {
		present, err := eval("{{ " + c.source.Present() + " }}")
		if err != nil || present != fmt.Sprint(c.present) {
			t.Errorf("%+v: Present %q, %v; want %v", c.source, present, err, c.present)
		}
		if !strings.Contains(c.source.Template(), "index") {
			t.Errorf("%+v: Template %q does not read through index", c.source, c.source.Template())
		}
		value, err := eval("{{ toJson " + c.source.Template() + " }}")
		if c.present && (err != nil || value != c.value) {
			t.Errorf("%+v: Template gives %q, %v; want %s", c.source, value, err, c.value)
		}
		if !c.present && err == nil {
			t.Errorf("%+v: Template of an absent input gives %q, want a halt", c.source, value)
		}
	}
}

func TestImportHaltDraftsNothing(t *testing.T) {
	both := `{"resource_types": ["Sample::Store::Queue", "Sample::Store::Bucket"]}`
	broken := aggrega.ImportSource{Name: "b.json", Bytes: []byte(`{"types": [`)}
	unshaped := aggrega.ImportSource{Name: "b.json", Bytes: []byte(`{"kinds": []}`)}
	module := aggrega.ImportSource{Name: "b.json", Bytes: []byte(`{"types": [{"name": "Sample::Store::Module"}]}`)}
	again := aggrega.ImportSource{Name: "b.json", Bytes: []byte(`{"types": [{"name": "Sample::Store::Queue"}]}`)}
	ambiguous := fxFormat()
	ambiguous.Resolve = func(name string, refs []TypeRef) (TypeRef, []aggrega.Diagnostic) {
		return TypeRef{}, []aggrega.Diagnostic{{Severity: "halt", Code: codeAmbiguous,
			Message: fmt.Sprintf("%d sources hold %s", len(refs), name)}}
	}
	halting := fxFormat()
	halting.Write = func(r Resource) (importer.RecipeDraft, []aggrega.Diagnostic) {
		d, _ := fxWrite(r)
		if r.Type.Name != fxBucket {
			return d, nil
		}
		return d, []aggrega.Diagnostic{{Severity: "halt", Code: codeRefused, Message: "the target cannot write it"}}
	}
	for _, c := range []struct {
		name, code, settings string
		sources              []aggrega.ImportSource
		view                 *aggrega.CatalogProjection
		format               *Format
	}{
		{name: "unreadable setting", code: codeResourceTypesUnreadable, settings: `{"resource_types": "Sample::Store::Queue"}`},
		{name: "several types, none named", code: codeResourceTypeNotNamed},
		{name: "a type not held", code: codeResourceTypeNotFound,
			settings: `{"resource_types": ["Sample::Store::Queue", "Sample::Store::Qeue"]}`},
		{name: "a source that does not parse", code: "parse-failed", settings: both,
			sources: []aggrega.ImportSource{{Name: "a.json"}, broken}},
		{name: "a source of another shape", code: codeUnreadable, settings: both,
			sources: []aggrega.ImportSource{{Name: "a.json"}, unshaped}},
		{name: "a refused type", code: codeRefused,
			settings: `{"resource_types": ["Sample::Store::Queue", "Sample::Store::Module"]}`,
			sources:  []aggrega.ImportSource{{Name: "a.json"}, module}},
		{name: "an ambiguous type", code: codeAmbiguous, settings: both, format: &ambiguous,
			sources: []aggrega.ImportSource{{Name: "a.json"}, again}},
		{name: "a Component held with other keys", code: codeComponentExists, settings: both,
			view: fxView(aggrega.CatalogComponent{Name: "imported/component/sample-store-bucket", Version: "0.1.0",
				Values: []string{"name"}})},
		{name: "a writer's halt", code: codeRefused, settings: both, format: &halting},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := fxInput(t, c.settings)
			for i, src := range c.sources {
				if src.Bytes == nil {
					src.Bytes = fxTypes(t)
				}
				c.sources[i] = src
			}
			in.Sources, in.Catalog = c.sources, c.view
			f := fxFormat()
			if c.format != nil {
				f = *c.format
			}
			out := Import(in, f, fxParse)
			halts := fxHalts(out)
			if !slices.ContainsFunc(halts, func(d aggrega.Diagnostic) bool { return d.Code == c.code }) {
				t.Fatalf("halts %+v, want %s", halts, c.code)
			}
			if out.Entities != nil || out.Files != nil || out.Decisions != nil {
				t.Errorf("a halted import drafted entities %+v, files %v, decisions %+v",
					out.Entities, fxPaths(out), out.Decisions)
			}
		})
	}
}

func TestImportIsDeterministic(t *testing.T) {
	settings := `{"resource_types": ["Sample::Store::Queue", "Sample::Store::Bucket",
		{"type": "Sample::Store::Queue", "component": "core/messaging/queue"}]}`
	view := fxView(
		aggrega.CatalogComponent{Name: "core/messaging/queue", Version: "1.2.0", Values: []string{"flavour", "queue_name"},
			Provides: []string{"arn", "endpoint"}, Labels: map[string]string{"component.aggrega.ai/category": "messaging"}},
		aggrega.CatalogComponent{Name: "core/storage/bucket", Version: "1.0.0", Values: []string{"bucket_name"}})
	run := func() []byte {
		in := fxInput(t, settings)
		in.Catalog = view
		out := Import(in, fxFormat(), fxParse)
		fxNoHalt(t, out)
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	first := run()
	for range 5 {
		if again := run(); string(again) != string(first) {
			t.Fatalf("two runs differ:\n%s\n%s", first, again)
		}
	}
}

func TestImportWarnsNoNodesWhereTheSourcesHoldNoType(t *testing.T) {
	in := fxInput(t, "")
	in.Bytes = []byte(`{"types": []}`)
	out := Import(in, fxFormat(), fxParse)
	d := fxDiagnostic(t, out, "warn", "no-nodes")
	if d.SourceRef != "sample-types.json" || len(out.Diagnostics) != 1 || out.Entities != nil || out.Files != nil {
		t.Fatalf("output %+v", out)
	}
}

func TestImportDraftsAnIdenticalRequestOnce(t *testing.T) {
	out := Import(fxInput(t, `{"resource_types": ["Sample::Store::Queue", "Sample::Store::Queue"]}`),
		fxFormat(), fxParse)
	fxNoHalt(t, out)
	if len(out.Entities) != 2 || len(out.Decisions) != 1 || len(out.Files) != 3 {
		t.Fatalf("entities %+v, decisions %d, files %v", out.Entities, len(out.Decisions), fxPaths(out))
	}

	folding := fxFormat()
	folding.FoldCase = true
	out = Import(fxInput(t, `{"resource_types": ["Sample::Store::Queue", "sample::store::QUEUE"]}`), folding, fxParse)
	fxNoHalt(t, out)
	if len(out.Entities) != 2 || len(out.Decisions) != 1 {
		t.Fatalf("two spellings of one type: entities %+v, decisions %d", out.Entities, len(out.Decisions))
	}
}

func TestImportDraftsATypeOncePerComponent(t *testing.T) {
	out := Import(fxInput(t, `{"resource_types": [{"type": "Sample::Store::Queue", "component": "acme/messaging/orders"},
		{"type": "Sample::Store::Queue", "component": "acme/messaging/events"}]}`), fxFormat(), fxParse)
	fxNoHalt(t, out)
	var names, ids []string
	for _, e := range out.Entities {
		names = append(names, e.Kind+" "+e.Name)
	}
	for _, d := range out.Decisions {
		ids = append(ids, d.SourceNode.ID)
	}
	want := []string{"Component acme/messaging/orders", "Recipe orders", "Component acme/messaging/events", "Recipe events"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("entities %v, want %v", names, want)
	}
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("decision ids %v, want two distinct", ids)
	}
}

func TestImportNeverPanicsOnMalformedInput(t *testing.T) {
	good := fxTypes(t)
	settings := `{"resource_types": ["Sample::Store::Queue"]}`
	type tree struct{ name, bytes, settings, code string }
	cases := []tree{
		{"empty", "", settings, "parse-failed"},
		{"null", "null", settings, codeUnreadable},
		{"a list", "[]", settings, codeUnreadable},
		{"a string", `"types"`, settings, codeUnreadable},
		{"types a string", `{"types": "x"}`, settings, codeUnreadable},
		{"types a number list", `{"types": [1, 2]}`, settings, codeUnreadable},
		{"a type with a number name", `{"types": [{"name": 3}]}`, settings, codeUnreadable},
		{"a type with null name", `{"types": [{"name": null}]}`, settings, codeUnreadable},
		{"properties an object", `{"types": [{"name": "Sample::Store::Queue", "properties": {}}]}`, settings, codeUnreadable},
		{"a null property", `{"types": [{"name": "Sample::Store::Queue", "properties": [null]}]}`, settings, codeUnreadable},
		{"a property of no kind", `{"types": [{"name": "Sample::Store::Queue", "properties": [{"name": "A", "kind": 7}]}]}`,
			settings, codeUnreadable},
		{"settings a list", string(good), `[1]`, codeResourceTypeNotNamed},
		{"resource_types an object", string(good), `{"resource_types": {"type": "x"}}`, codeResourceTypesUnreadable},
		{"an entry of a number type", string(good), `{"resource_types": [{"type": 1}]}`, codeResourceTypesUnreadable},
		{"an entry of a list", string(good), `{"resource_types": [["x"]]}`, codeResourceTypesUnreadable},
	}
	// Every truncation of the sample types short of its closing brace is not JSON.
	end := strings.LastIndex(string(good), "}")
	for i := 0; i < end; i += 7 {
		cases = append(cases, tree{fmt.Sprintf("truncated at %d", i), string(good[:i]), settings, "parse-failed"})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("the import panicked: %v", r)
				}
			}()
			in := fxInput(t, c.settings)
			in.Bytes = []byte(c.bytes)
			out := Import(in, fxFormat(), fxParse)
			halts := fxHalts(out)
			if !slices.ContainsFunc(halts, func(d aggrega.Diagnostic) bool { return d.Code == c.code }) {
				t.Fatalf("halts %+v, want %s", halts, c.code)
			}
			if out.Entities != nil || out.Files != nil || out.Decisions != nil {
				t.Errorf("a halted import drafted %+v", out)
			}
		})
	}
}

func TestComponentExistsHaltsOnOtherValuesKeys(t *testing.T) {
	in := fxInput(t, `{"resource_types": ["Sample::Store::Queue"]}`)
	in.Catalog = fxView(aggrega.CatalogComponent{Name: "imported/messaging/sample-store-queue", Version: "0.1.0",
		Values: []string{"name", "tier"}})
	out := Import(in, fxFormat(), fxParse)
	d := fxDiagnostic(t, out, "halt", codeComponentExists)
	for _, want := range []string{"imported/messaging/sample-store-queue", "name and tier", "queue_name", "component",
		fxQueue} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("message %q does not name %q", d.Message, want)
		}
	}
	if out.Entities != nil || out.Files != nil {
		t.Errorf("a halted import drafted %+v", out)
	}
}

func TestComponentExistsRefreshesOnEqualKeys(t *testing.T) {
	in := fxInput(t, `{"resource_types": ["Sample::Store::Queue"]}`)
	in.Catalog = fxView(aggrega.CatalogComponent{Name: "imported/messaging/sample-store-queue", Version: "0.1.0",
		Values: []string{"queue_name"}})
	out := Import(in, fxFormat(), fxParse)
	fxNoHalt(t, out)
	fxFile(t, out, fxQueueComponent)
	fxFile(t, out, fxQueueRecipe)
	if len(out.Decisions) != 1 {
		t.Fatalf("decisions %+v", out.Decisions)
	}
	d := out.Decisions[0]
	if !strings.Contains(d.Rationale, "same values keys") || strings.Contains(d.SwapHint, "sample-store-queue") {
		t.Errorf("rationale %q, swap hint %q", d.Rationale, d.SwapHint)
	}
}

func TestComponentExistsHaltsOnTwoRequestsDraftingOnePath(t *testing.T) {
	out := Import(fxInput(t, `{"resource_types": [{"type": "Sample::Store::Queue", "component": "acme/messaging/store"},
		{"type": "Sample::Store::Bucket", "component": "acme/storage/store"}]}`), fxFormat(), fxParse)
	d := fxDiagnostic(t, out, "halt", codeComponentExists)
	for _, want := range []string{"acme/messaging/store", "acme/storage/store", fxQueue, fxBucket,
		"catalog/recipe/sample/store"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("message %q does not name %q", d.Message, want)
		}
	}
	if out.Entities != nil || out.Files != nil {
		t.Errorf("a halted import drafted %+v", out)
	}
}

func TestDecisionIsNeverAReference(t *testing.T) {
	// The settings map the type to the catalog Component as strongly as the kit
	// scores anything, and ask for a strong match to be referenced; the
	// Component's last segment is the type's slug.
	in := fxInput(t, `{"resource_types": ["Sample::Store::Queue"], "reuse_mode": "auto-apply-above-threshold",
		"reuse_threshold": 0.01, "mapping_overrides": {"sample-schema": {"Sample::Store::Queue": {
		"component": "core/messaging/sample-store-queue", "category": "messaging", "vendor": "queue",
		"traits": {"component.aggrega.ai/hosting": "managed"}}}}}`)
	in.Catalog = fxView(aggrega.CatalogComponent{Name: "core/messaging/sample-store-queue", Version: "1.0.0",
		Values: []string{"queue_name"}, Labels: map[string]string{"component.aggrega.ai/category": "messaging",
			"component.aggrega.ai/hosting": "managed"}})
	out := Import(in, fxFormat(), fxParse)
	fxNoHalt(t, out)
	for _, e := range out.Entities {
		if e.Mode != "draft" {
			t.Errorf("entity %s %s is a %s", e.Kind, e.Name, e.Mode)
		}
	}
	if len(out.Decisions) != 1 {
		t.Fatalf("decisions %+v", out.Decisions)
	}
	d := out.Decisions[0]
	if d.Chosen == nil || d.Chosen.Mode != "draft" || d.Chosen.Target != "imported/messaging/sample-store-queue" {
		t.Errorf("chosen %+v, want the draft", d.Chosen)
	}
	if want := "resembles core/messaging/sample-store-queue; draft a Recipe implementing it with `component`"; d.SwapHint != want {
		t.Errorf("swap hint %q, want %q", d.SwapHint, want)
	}
	for _, c := range d.Candidates {
		if c.Signals.Table != 0 {
			t.Errorf("candidate %s carries a mapping signal %v", c.ComponentName, c.Signals.Table)
		}
	}
	if d.SourceNode.Type != fxQueue {
		t.Errorf("source node %+v", d.SourceNode)
	}
}

func TestDecisionCountsKeysByRule(t *testing.T) {
	out := Import(fxInput(t, `{"resource_types": ["Sample::Store::Queue", "Sample::Store::Bucket"]}`),
		fxFormat(), fxParse)
	fxNoHalt(t, out)
	if len(out.Decisions) != 2 {
		t.Fatalf("decisions %+v", out.Decisions)
	}
	queue, bucket := out.Decisions[0], out.Decisions[1]
	if want := "1 values, 2 infra, 3 overrides, 1 secrets, 2 provides"; queue.SourceNode.ParamsSummary != want {
		t.Errorf("params summary %q, want %q", queue.SourceNode.ParamsSummary, want)
	}
	for _, want := range []string{"1 identity key", "1 placement key", "1 required key", "3 overrides", "1 secret",
		"2 read-back attributes", "1 sensitive read-back attribute withheld", "1 read-back attribute not provided"} {
		if !strings.Contains(queue.Rationale, want) {
			t.Errorf("rationale %q does not count %q", queue.Rationale, want)
		}
	}
	if want := "1 values, 2 infra, 1 overrides, 0 secrets, 1 provides"; bucket.SourceNode.ParamsSummary != want {
		t.Errorf("params summary %q, want %q", bucket.SourceNode.ParamsSummary, want)
	}
	if !strings.Contains(bucket.Rationale, "SIZE is written size_2, since Size takes size") {
		t.Errorf("rationale %q does not name both properties of one key", bucket.Rationale)
	}
	for _, d := range out.Decisions {
		if d.Chosen == nil || d.Chosen.Mode != "draft" || d.SwapHint != "" {
			t.Errorf("decision %+v", d)
		}
	}
}

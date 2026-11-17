// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudformation

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

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// The codes the tests read, written out rather than taken from the core, so a
// renamed code fails here as it would for a reader of the diagnostics.
const (
	codeUnreadable  = "resource-schema-unreadable"
	codeRefused     = "resource-type-refused"
	codeParseFailed = "parse-failed"
	codeDropped     = "constraint-dropped"
	ledger          = "AWS::Example::Ledger"
)

// parse reads JSON source as the host's json parser does, into the canonical
// tree whose object keys are sorted.
func parse(syntax string, source []byte) (json.RawMessage, error) {
	if syntax != "json" {
		return nil, fmt.Errorf("no parser for %s", syntax)
	}
	out := parsercore.ParseJSON(source)
	for _, d := range out.Diagnostics {
		return nil, errors.New(d.Message)
	}
	return out.Tree, nil
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// fileInput is the import of the one fixture name, handed as a file.
func fileInput(t *testing.T, name string) aggrega.ImportInput {
	t.Helper()
	return aggrega.ImportInput{Format: Format.Name, Name: name, Bytes: fixture(t, name)}
}

// dirInput is the import of a directory holding the fixtures names.
func dirInput(t *testing.T, names ...string) aggrega.ImportInput {
	t.Helper()
	in := aggrega.ImportInput{Format: Format.Name, Name: "schemas"}
	for _, n := range names {
		in.Sources = append(in.Sources, aggrega.ImportSource{Name: n, Bytes: fixture(t, n)})
	}
	return in
}

// bytesInput is the import of one file named name holding body.
func bytesInput(name, body string) aggrega.ImportInput {
	return aggrega.ImportInput{Format: Format.Name, Name: name, Bytes: []byte(body)}
}

func haltsOf(ds []aggrega.Diagnostic) []aggrega.Diagnostic {
	var out []aggrega.Diagnostic
	for _, d := range ds {
		if d.Severity == "halt" {
			out = append(out, d)
		}
	}
	return out
}

func noHalt(t *testing.T, ds []aggrega.Diagnostic) {
	t.Helper()
	if h := haltsOf(ds); len(h) > 0 {
		t.Fatalf("halted: %+v", h)
	}
}

// onlyHalt returns the one halt ds holds, failing unless it is the one halt and
// carries code.
func onlyHalt(t *testing.T, ds []aggrega.Diagnostic, code string) aggrega.Diagnostic {
	t.Helper()
	h := haltsOf(ds)
	if len(h) != 1 || h[0].Code != code {
		t.Fatalf("halts %+v, want one %s", h, code)
	}
	return h[0]
}

// lifted lists and lifts the one type the fixture name holds.
func lifted(t *testing.T, name string) *schemadraft.Type {
	t.Helper()
	refs, ds := Format.List(fileInput(t, name), parse)
	noHalt(t, ds)
	if len(refs) != 1 {
		t.Fatalf("listed %+v, want one type", refs)
	}
	typ, ds := Format.Lift(refs[0])
	noHalt(t, ds)
	return typ
}

// liftBody lifts the provider schema body, handed as a file named name.
func liftBody(t *testing.T, body string) (*schemadraft.Type, []aggrega.Diagnostic) {
	t.Helper()
	refs, ds := Format.List(bytesInput("schema.json", body), parse)
	noHalt(t, ds)
	if len(refs) != 1 {
		t.Fatalf("listed %+v, want one type", refs)
	}
	return Format.Lift(refs[0])
}

// propertyAt returns the property of typ at the vendor path, dotted, the
// members of an array's items read as the array's own.
func propertyAt(t *testing.T, typ *schemadraft.Type, path string) *schemadraft.Property {
	t.Helper()
	props := typ.Properties
	var found *schemadraft.Property
	for _, name := range strings.Split(path, ".") {
		found = nil
		for _, p := range props {
			if p.Name == name {
				found = p
			}
		}
		if found == nil {
			t.Fatalf("no property %s in %s", path, typ.Name)
		}
		props = found.Properties
		if found.Items != nil {
			props = found.Items.Properties
		}
	}
	return found
}

func names(props []*schemadraft.Property) []string {
	var out []string
	for _, p := range props {
		out = append(out, p.Name)
	}
	return out
}

func TestCloudFormationListsBothShapes(t *testing.T) {
	var calls []string
	counting := func(syntax string, source []byte) (json.RawMessage, error) {
		calls = append(calls, syntax)
		return parse(syntax, source)
	}
	refs, ds := Format.List(dirInput(t, "ledger.json", "ledger-describe-type.json", "unprovisionable-describe-type.json"),
		counting)
	noHalt(t, ds)
	var got []string
	for _, r := range refs {
		got = append(got, r.Name+" "+r.Source)
	}
	want := []string{ledger + " ledger.json", ledger + " ledger-describe-type.json",
		"AWS::Example::Archive unprovisionable-describe-type.json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
	// The provider schema is parsed once; the describe-type output once, and its
	// Schema again as a second source; the refused output's Schema is not read.
	if want := []string{"json", "json", "json", "json"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("parse called for %v, want %v", calls, want)
	}

	// A file handed on its own is named by the request's name.
	refs, ds = Format.List(fileInput(t, "ledger.json"), parse)
	noHalt(t, ds)
	if len(refs) != 1 || refs[0].Source != "ledger.json" || refs[0].Name != ledger {
		t.Fatalf("listed %+v", refs)
	}

	// Both forms lift to one type and draft the same Component and Recipe.
	schema, described := lifted(t, "ledger.json"), lifted(t, "ledger-describe-type.json")
	schema.Source, described.Source = "", ""
	if !reflect.DeepEqual(schema, described) {
		t.Errorf("the two forms lift to different types:\n%+v\n%+v", schema, described)
	}
	a := schemadraft.Import(fileInput(t, "ledger.json"), Format, parse)
	b := schemadraft.Import(fileInput(t, "ledger-describe-type.json"), Format, parse)
	noHalt(t, a.Diagnostics)
	noHalt(t, b.Diagnostics)
	if len(a.Files) == 0 || !reflect.DeepEqual(a.Files, b.Files) {
		t.Errorf("the two forms draft different files:\n%+v\n%+v", a.Files, b.Files)
	}
}

func TestCloudFormationRefusesUnprovisionable(t *testing.T) {
	for _, c := range []struct{ file, typ, why string }{
		{"module-describe-type.json", "Example::Ledger::Bundle::MODULE", "MODULE"},
		{"unprovisionable-describe-type.json", "AWS::Example::Archive", "NON_PROVISIONABLE"},
		{"module-schema.json", "Example::Ledger::Bundle::MODULE", "MODULE"},
	} {
		t.Run(c.file, func(t *testing.T) {
			out := schemadraft.Import(fileInput(t, c.file), Format, parse)
			d := onlyHalt(t, out.Diagnostics, codeRefused)
			if d.SourceRef != c.file || !strings.Contains(d.Message, c.typ) || !strings.Contains(d.Message, c.why) {
				t.Errorf("refusal %+v does not name the source, the type and %s", d, c.why)
			}
			if out.Entities != nil || out.Files != nil || out.Decisions != nil {
				t.Errorf("a refused type drafted %+v", out)
			}
		})
	}

	// A type the provider refuses halts only where it is requested.
	in := dirInput(t, "ledger.json", "module-describe-type.json", "unprovisionable-describe-type.json")
	in.Settings = json.RawMessage(`{"resource_types": ["AWS::Example::Ledger"]}`)
	out := schemadraft.Import(in, Format, parse)
	noHalt(t, out.Diagnostics)
	if len(out.Entities) != 2 {
		t.Errorf("entities %+v, want the Ledger's Component and Recipe", out.Entities)
	}
}

func TestCloudFormationUnreadableNamesMember(t *testing.T) {
	describe := func(schema string) string {
		quoted, _ := json.Marshal(schema)
		return `{"Type": "RESOURCE", "TypeName": "AWS::Example::Ledger", "ProvisioningType": "FULLY_MUTABLE", ` +
			`"Schema": ` + string(quoted) + `}`
	}
	for _, c := range []struct{ name, body, code, member string }{
		{"no properties", string(fixture(t, "no-properties.json")), codeUnreadable, "no properties object"},
		{"properties a list", `{"typeName": "AWS::Example::Ledger", "properties": []}`, codeUnreadable,
			"no properties object"},
		{"typeName a number", `{"typeName": 7, "properties": {}}`, codeUnreadable, "no typeName string"},
		{"neither shape", `{"description": "a document of another kind"}`, codeUnreadable, "no typeName string"},
		{"a list", `[]`, codeUnreadable, "no typeName string"},
		{"no Schema", `{"Type": "RESOURCE", "TypeName": "AWS::Example::Ledger"}`, codeUnreadable, "no Schema string"},
		{"Schema an object", `{"TypeName": "AWS::Example::Ledger", "Schema": {}}`, codeUnreadable,
			"no Schema string"},
		{"TypeName a list", `{"TypeName": [], "Schema": "{}"}`, codeUnreadable, "no TypeName string"},
		{"Schema without properties", describe(`{"typeName": "AWS::Example::Ledger"}`), codeUnreadable,
			"Schema holds no properties object"},
		{"Schema without typeName", describe(`{"properties": {}}`), codeUnreadable, "Schema holds no typeName string"},
		{"Schema not JSON", describe(`{"typeName": `), codeParseFailed, "Schema"},
		{"a property not a schema", `{"typeName": "AWS::Example::Ledger", "properties": {"Size": 7}}`,
			codeUnreadable, "Size"},
		{"required not a list", `{"typeName": "AWS::Example::Ledger", "properties": {"Size": {"type": "integer"}}, ` +
			`"required": "Size"}`, codeUnreadable, "required"},
		{"a pointer list of numbers", `{"typeName": "AWS::Example::Ledger", "properties": {}, ` +
			`"readOnlyProperties": [1]}`, codeUnreadable, "readOnlyProperties"},
		{"a reference outside definitions", `{"typeName": "AWS::Example::Ledger", "properties": {"Size": ` +
			`{"$ref": "other.json#/Size"}}}`, codeUnreadable, "other.json#/Size"},
		{"a reference to no definition", `{"typeName": "AWS::Example::Ledger", "properties": {"Size": ` +
			`{"$ref": "#/definitions/Size"}}}`, codeUnreadable, "#/definitions/Size"},
		{"a type of no kind", `{"typeName": "AWS::Example::Ledger", "properties": {"Size": {"type": "decimal"}}}`,
			codeUnreadable, "decimal"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := schemadraft.Import(bytesInput("schema.json", c.body), Format, parse)
			d := onlyHalt(t, out.Diagnostics, c.code)
			if !strings.Contains(d.Message, c.member) || d.SourceRef != "schema.json" {
				t.Errorf("halt %+v does not name %q and the source", d, c.member)
			}
			if out.Entities != nil || out.Files != nil {
				t.Errorf("an unreadable source drafted %+v", out)
			}
		})
	}
}

// TestCloudFormationListsWithoutLiftingProperties holds List to reading a
// source's own members alone: a property no reader could lift halts only once
// its type is selected, and a source that is not selected never halts.
func TestCloudFormationListsWithoutLiftingProperties(t *testing.T) {
	broken := `{"typeName": "AWS::Example::Broken", "properties": {"Size": 7}}`
	refs, ds := Format.List(bytesInput("broken.json", broken), parse)
	noHalt(t, ds)
	if len(refs) != 1 || refs[0].Name != "AWS::Example::Broken" {
		t.Fatalf("listed %+v", refs)
	}
	in := dirInput(t, "ledger.json")
	in.Sources = append(in.Sources, aggrega.ImportSource{Name: "broken.json", Bytes: []byte(broken)})
	in.Settings = json.RawMessage(`{"resource_types": ["AWS::Example::Ledger"]}`)
	noHalt(t, schemadraft.Import(in, Format, parse).Diagnostics)
}

func TestCloudFormationLift(t *testing.T) {
	typ := lifted(t, "ledger.json")
	if typ.Name != ledger || typ.Source != "ledger.json" || typ.APIVersion != "" {
		t.Errorf("type %s from %s at %q", typ.Name, typ.Source, typ.APIVersion)
	}
	if want := "A ledger the example service keeps, written for this test. It records entries for its readers."; typ.Description != want {
		t.Errorf("description %q, want its first paragraph", typ.Description)
	}
	if want := [][]string{{"Identifier"}}; !reflect.DeepEqual(typ.PrimaryIdentifier, want) {
		t.Errorf("primary identifier %v, want %v", typ.PrimaryIdentifier, want)
	}
	if want := []string{"Arn", "Capacity", "DeletionProtection", "Endpoint", "Identifier", "KmsKeyId", "LedgerName",
		"LegacyMode", "MasterPassword", "Name", "Rules", "SubnetGroup"}; !reflect.DeepEqual(names(typ.Properties), want) {
		t.Errorf("properties %v, want %v", names(typ.Properties), want)
	}

	flags := func(p *schemadraft.Property) string {
		var on []string
		for _, f := range []struct {
			name string
			set  bool
		}{{"required", p.Required}, {"identifier", p.Identifier}, {"createOnly", p.CreateOnly},
			{"readOnly", p.ReadOnly}, {"writeOnly", p.WriteOnly}, {"sensitive", p.Sensitive},
			{"deprecated", p.Deprecated}} {
			if f.set {
				on = append(on, f.name)
			}
		}
		return strings.Join(on, " ")
	}
	for path, want := range map[string]string{
		"Arn":                "readOnly",
		"Capacity":           "required",
		"Capacity.ReadUnits": "required",
		"Capacity.Mode":      "",
		"DeletionProtection": "",
		"Endpoint.Address":   "readOnly",
		"Endpoint.Port":      "readOnly",
		"Endpoint.Token":     "readOnly sensitive",
		"Identifier":         "required identifier createOnly",
		"KmsKeyId":           "createOnly",
		"LedgerName":         "createOnly",
		"LegacyMode":         "deprecated",
		"MasterPassword":     "writeOnly sensitive",
		"Name":               "",
		"SubnetGroup":        "required createOnly",
		"Rules":              "",
		"Endpoint":           "",
	} {
		if got := flags(propertyAt(t, typ, path)); got != want {
			t.Errorf("%s flags %q, want %q", path, got, want)
		}
	}

	capacity := propertyAt(t, typ, "Capacity")
	if capacity.Description != "How much the ledger serves. Its members are read at creation." ||
		!reflect.DeepEqual(capacity.Kinds, []schemadraft.Kind{schemadraft.KindObject}) || capacity.Open ||
		!reflect.DeepEqual(names(capacity.Properties), []string{"Mode", "ReadUnits"}) {
		t.Errorf("Capacity %+v: want the referring description and the definition's closed members", capacity)
	}
	if mode := propertyAt(t, typ, "Capacity.Mode"); len(mode.Enum) != 2 || string(mode.Enum[1]) != `"BURST"` ||
		!reflect.DeepEqual(mode.Path, []string{"Capacity", "Mode"}) {
		t.Errorf("Capacity.Mode %+v", mode)
	}
	units := propertyAt(t, typ, "Capacity.ReadUnits")
	if units.Min == nil || units.Min.String() != "1" || units.Max == nil || units.Max.String() != "400" {
		t.Errorf("Capacity.ReadUnits bounds %v %v", units.Min, units.Max)
	}
	name := propertyAt(t, typ, "LedgerName")
	if name.Pattern != "^(?!reserved-)[a-z][a-z0-9-]*$" || name.MinLength == nil || *name.MinLength != 3 ||
		name.MaxLength == nil || *name.MaxLength != 63 {
		t.Errorf("LedgerName constraints %+v", name)
	}
	if endpoint := propertyAt(t, typ, "Endpoint"); endpoint.Description != "" {
		t.Errorf("Endpoint described %q, though neither it nor its definition has a description", endpoint.Description)
	}

	// The reference back to Rule is cut where it re-enters it.
	rules := propertyAt(t, typ, "Rules")
	if rules.Items == nil || !reflect.DeepEqual(names(rules.Items.Properties), []string{"Action", "Children"}) {
		t.Fatalf("Rules items %+v", rules.Items)
	}
	children := propertyAt(t, typ, "Rules.Children")
	if children.Items == nil || children.Items.Cut != "cycle" || !children.Items.Open ||
		!reflect.DeepEqual(children.Items.Kinds, []schemadraft.Kind{schemadraft.KindObject}) {
		t.Errorf("Rules.Children items %+v, want the cycle cut to an open object", children.Items)
	}
	if !slices.ContainsFunc(rules.Dropped, func(s string) bool { return strings.HasPrefix(s, "uniqueItems: ") }) {
		t.Errorf("Rules dropped %v, want uniqueItems", rules.Dropped)
	}
	for _, p := range []*schemadraft.Property{rules, children, propertyAt(t, typ, "SubnetGroup")} {
		for _, d := range p.Dropped {
			if strings.HasPrefix(d, "insertionOrder") || strings.HasPrefix(d, "relationshipRef") {
				t.Errorf("%s drops the annotation %q", p.Name, d)
			}
		}
	}
}

func TestCloudFormationLiftShapes(t *testing.T) {
	nest := func(depth int) string {
		body := `{"type": "string"}`
		for i := depth; i > 0; i-- {
			body = fmt.Sprintf(`{"type": "object", "additionalProperties": false, "properties": {"L%d": %s}}`, i, body)
		}
		return body
	}
	typ, ds := liftBody(t, `{"typeName": "AWS::Example::Shapes", "properties": {
		"Deep": `+nest(7)+`,
		"Either": {"type": ["string", "integer"]},
		"OneOf": {"oneOf": [{"type": "string"}, {"$ref": "#/definitions/Count"}]},
		"Exactly": {"type": "object", "additionalProperties": false,
			"properties": {"A": {"type": "string"}, "B": {"type": "string"}},
			"oneOf": [{"required": ["A"]}, {"required": ["B"]}]},
		"Free": {"type": "object"},
		"Patterned": {"type": "object", "additionalProperties": false,
			"patternProperties": {"^[a-z]+$": {"type": "string"}}},
		"Anything": {},
		"Stepped": {"type": "number", "multipleOf": 5}
	}, "definitions": {"Count": {"type": "integer"}}}`)
	noHalt(t, ds)

	// Deep's own level is one, so the property at its seventh level is cut.
	sixth := propertyAt(t, typ, "Deep.L1.L2.L3.L4.L5")
	if sixth.Cut != "" || len(sixth.Properties) != 1 {
		t.Fatalf("the sixth level %+v, want it lifted", sixth)
	}
	if seventh := sixth.Properties[0]; seventh.Cut != "depth" || !seventh.Open ||
		!reflect.DeepEqual(seventh.Path, []string{"Deep", "L1", "L2", "L3", "L4", "L5", "L6"}) {
		t.Errorf("the seventh level %+v, want it cut to an open object", seventh)
	}

	kinds := func(path string) []schemadraft.Kind { return propertyAt(t, typ, path).Kinds }
	if want := []schemadraft.Kind{"string", "integer"}; !reflect.DeepEqual(kinds("Either"), want) ||
		!reflect.DeepEqual(kinds("OneOf"), want) {
		t.Errorf("Either %v, OneOf %v, want %v", kinds("Either"), kinds("OneOf"), want)
	}
	exactly := propertyAt(t, typ, "Exactly")
	if !reflect.DeepEqual(exactly.Kinds, []schemadraft.Kind{"object"}) || exactly.Open ||
		!slices.ContainsFunc(exactly.Dropped, func(s string) bool { return strings.HasPrefix(s, "oneOf: ") }) {
		t.Errorf("Exactly %+v, want a closed object with its oneOf dropped", exactly)
	}
	if free := propertyAt(t, typ, "Free"); !free.Open {
		t.Errorf("an object stating no additionalProperties is closed")
	}
	patterned := propertyAt(t, typ, "Patterned")
	if !patterned.Open || !slices.ContainsFunc(patterned.Dropped, func(s string) bool {
		return strings.HasPrefix(s, "patternProperties: ")
	}) {
		t.Errorf("Patterned %+v, want an open object with its patternProperties dropped", patterned)
	}
	if want := []schemadraft.Kind{"any"}; !reflect.DeepEqual(kinds("Anything"), want) {
		t.Errorf("Anything %v, want %v", kinds("Anything"), want)
	}
	if stepped := propertyAt(t, typ, "Stepped"); !slices.ContainsFunc(stepped.Dropped, func(s string) bool {
		return strings.HasPrefix(s, "multipleOf: ")
	}) {
		t.Errorf("Stepped dropped %v, want multipleOf", stepped.Dropped)
	}
}

func TestCloudFormationIdentity(t *testing.T) {
	typ := lifted(t, "ledger.json")
	rules := Format.Rules
	if rules.Placement != nil || rules.SecretSlots || !rules.NestedProvides {
		t.Errorf("rules %+v: want no placement, secrets as parameters and nested provides", rules)
	}
	for _, c := range []struct {
		name string
		want bool
	}{{"Identifier", true}, {"LedgerName", true}, {"Name", true}, {"SubnetGroup", false}, {"KmsKeyId", false},
		{"DeletionProtection", false}} {
		if got := rules.Identity(typ, propertyAt(t, typ, c.name)); got != c.want {
			t.Errorf("identity of %s = %v, want %v", c.name, got, c.want)
		}
	}
	queue := &schemadraft.Type{Name: "AWS::SQS::Queue"}
	for _, c := range []struct {
		name string
		want bool
	}{{"QueueName", true}, {"Name", true}, {"QueueUrl", false}, {"LedgerName", false}} {
		p := &schemadraft.Property{Name: c.name, Path: []string{c.name}, Kinds: []schemadraft.Kind{"string"}}
		if got := rules.Identity(queue, p); got != c.want {
			t.Errorf("identity of %s on %s = %v, want %v", c.name, queue.Name, got, c.want)
		}
	}

	// The create-only properties outside identity are placed by whether the
	// vendor requires them, each replaced on change.
	out := schemadraft.Import(fileInput(t, "ledger.json"), Format, parse)
	noHalt(t, out.Diagnostics)
	component := fileOf(t, out, ledgerComponent)
	for _, want := range []string{
		"# identity: the resource's own name or identifier\n        # [vendor schema text] The ledger's own " +
			"identifier.\n        identifier:\n",
		"# required: the vendor schema requires it; replaced on change\n        # [vendor schema text] The subnet group",
	} {
		if !strings.Contains(component, want) {
			t.Errorf("the Component does not hold %q:\n%s", want, component)
		}
	}
	if got := pillarKeys(t, component, "values"); !reflect.DeepEqual(got, []string{"identifier", "ledger_name", "name"}) {
		t.Errorf("values keys %v", got)
	}
	if got := pillarKeys(t, component, "infra"); !reflect.DeepEqual(got, []string{"capacity", "subnet_group", "overrides"}) {
		t.Errorf("infra keys %v", got)
	}
	if !strings.Contains(fileOf(t, out, ledgerRecipe), "# infra.overrides.KmsKeyId: string; override: writable, set "+
		"under infra.overrides in the vendor's spelling; replaced on change") {
		t.Errorf("the Recipe does not list KmsKeyId as an override replaced on change")
	}
}

func TestCloudFormationSecretWords(t *testing.T) {
	str := []schemadraft.Kind{schemadraft.KindString}
	for _, c := range []struct {
		name  string
		kinds []schemadraft.Kind
		want  bool
	}{
		{"MasterPassword", str, true},
		{"DatabaseSecret", str, true},
		{"AuthToken", str, true},
		{"ServiceCredentials", str, true},
		{"SshPrivateKey", str, true},
		{"ApiKey", str, true},
		{"KeyPassphrase", str, true},
		{"SecretArn", str, false},
		{"TokenId", str, false},
		{"PasswordName", str, false},
		{"CredentialRef", str, false},
		{"MasterPassword", []schemadraft.Kind{schemadraft.KindObject}, false},
		{"Password", []schemadraft.Kind{schemadraft.KindString, schemadraft.KindInteger}, false},
		{"Description", str, false},
	} {
		p := &schemadraft.Property{Name: c.name, Path: []string{c.name}, Kinds: c.kinds}
		if got := Format.Rules.Secret(&schemadraft.Type{Name: ledger}, p); got != c.want {
			t.Errorf("secret %s %v = %v, want %v", c.name, c.kinds, got, c.want)
		}
	}

	out := schemadraft.Import(fileInput(t, "ledger.json"), Format, parse)
	noHalt(t, out.Diagnostics)
	if len(out.Decisions) != 1 {
		t.Fatalf("decisions %+v", out.Decisions)
	}
	rationale := out.Decisions[0].Rationale
	for _, want := range []string{"1 secret as a deploy-time parameter (MasterPassword)",
		"1 sensitive read-back attribute withheld"} {
		if !strings.Contains(rationale, want) {
			t.Errorf("rationale %q does not hold %q", rationale, want)
		}
	}
}

func TestCloudFormationCategories(t *testing.T) {
	for _, c := range []struct{ typ, want string }{
		{"AWS::SQS::Queue", "messaging"},
		{"AWS::DynamoDB::Table", "datastore"},
		{"AWS::S3::Bucket", "storage"},
		{"AWS::Lambda::Function", "compute"},
		{"AWS::StepFunctions::StateMachine", "ai-pipeline"},
		{"AWS::Bedrock::Agent", "ai-agent"},
		{"AWS::Bedrock::KnowledgeBase", "datastore"},
		{"AWS::Bedrock::Guardrail", "security"},
		{"AWS::Bedrock::Prompt", "ai-model"},
		{"AWS::Example::Ledger", ""},
		{"Example::Ledger::Bundle::MODULE", ""},
		{"Ledger", ""},
	} {
		if got := Format.Category(&schemadraft.Type{Name: c.typ}); got != c.want {
			t.Errorf("category of %s = %q, want %q", c.typ, got, c.want)
		}
	}
	for _, table := range []map[string]string{serviceCategories, typeCategories} {
		for key, category := range table {
			if !slices.Contains(schemadraft.Categories, category) {
				t.Errorf("%s takes the category %q, which is not one the catalog names", key, category)
			}
		}
	}
}

func TestCloudFormationNeverPanicsOnMalformedInput(t *testing.T) {
	good := string(fixture(t, "ledger.json"))
	described := string(fixture(t, "ledger-describe-type.json"))
	cases := map[string]string{
		"empty":                 "",
		"null":                  "null",
		"a string":              `"typeName"`,
		"properties null":       `{"typeName": "A::B::C", "properties": null}`,
		"a property null":       `{"typeName": "A::B::C", "properties": {"A": null}}`,
		"items a list":          `{"typeName": "A::B::C", "properties": {"A": {"type": "array", "items": [1]}}}`,
		"definitions a list":    `{"typeName": "A::B::C", "properties": {"A": {"$ref": "#/definitions/X"}}, "definitions": []}`,
		"a self reference":      `{"typeName": "A::B::C", "properties": {"A": {"$ref": "#/definitions/X"}}, "definitions": {"X": {"$ref": "#/definitions/X"}}}`,
		"enum an object":        `{"typeName": "A::B::C", "properties": {"A": {"type": "string", "enum": {}}}}`,
		"minimum a string":      `{"typeName": "A::B::C", "properties": {"A": {"type": "integer", "minimum": "1"}}}`,
		"minLength a fraction":  `{"typeName": "A::B::C", "properties": {"A": {"type": "string", "minLength": 1.5}}}`,
		"oneOf an object":       `{"typeName": "A::B::C", "properties": {"A": {"oneOf": {}}}}`,
		"a pointer to nothing":  `{"typeName": "A::B::C", "properties": {}, "readOnlyProperties": ["/properties/X/Y"]}`,
		"a pointer of no root":  `{"typeName": "A::B::C", "properties": {}, "primaryIdentifier": ["X"]}`,
		"Type a number":         `{"TypeName": "A::B::C", "Type": 1, "Schema": "{}"}`,
		"Schema a list":         `{"TypeName": "A::B::C", "Schema": "[]"}`,
		"description an object": `{"typeName": "A::B::C", "description": {}, "properties": {}}`,
	}
	for _, src := range []string{good, described} {
		for i := 0; i < len(src); i += 97 {
			cases[fmt.Sprintf("truncated %d at %d", len(src), i)] = src[:i]
		}
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("the import panicked: %v", r)
				}
			}()
			out := schemadraft.Import(bytesInput("schema.json", body), Format, parse)
			if len(haltsOf(out.Diagnostics)) > 0 && (out.Entities != nil || out.Files != nil) {
				t.Errorf("a halted import drafted %+v", out)
			}
		})
	}
}

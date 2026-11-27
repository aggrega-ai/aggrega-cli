// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// base is a template declaring a parameter of each kind the lowering reads, a
// mapping, two conditions and three resources; the YAML value a test lowers is
// written as the property P of the resource Api.
const base = `AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  Env: {Type: String, Default: dev, AllowedValues: [dev, prod]}
  Name: {Type: String}
  Password: {Type: String, NoEcho: true}
  Port: {Type: Number, Default: 5432}
  Subnets: {Type: CommaDelimitedList, Default: 'subnet-a, subnet-b'}
  Ami: {Type: 'AWS::SSM::Parameter::Value<AWS::EC2::Image::Id>', Default: /images/web}
  Zones: {Type: 'AWS::SSM::Parameter::Value<CommaDelimitedList>', Default: /zones}
Mappings:
  Sizes:
    dev: {Class: db.t3.micro}
    prod: {Class: db.r6g.large}
Conditions:
  IsProd: !Equals [!Ref Env, prod]
  InEast: !Equals [!Ref 'AWS::Region', us-east-1]
Resources:
  Db:
    Type: AWS::RDS::DBInstance
    Properties: {Engine: postgres}
  DbSecret:
    Type: AWS::SecretsManager::Secret
  Api:
    Type: AWS::ECS::Service
    Properties:
      P: %s
`

// atP is the JSON pointer of the property P in base.
const atP = "/Resources/Api/Properties/P"

// lowered reads and lowers the template text, beside the parameters file
// params where it is not empty.
func lowered(text, params string) (*cloudimport.Graph, []aggrega.Diagnostic) {
	sources := []aggrega.ImportSource{src("template.yaml", []byte(text))}
	if params != "" {
		sources = append(sources, src("params.json", []byte(params)))
	}
	return Read(aggrega.ImportInput{Format: formatName, Sources: sources}, cloudimport.Settings{}, parse)
}

// lowerP lowers value written as the property P of base, beside the
// parameters file params where one is given: the property, whether it is
// present, and what the read reports.
func lowerP(t *testing.T, value string, params ...string) (cloudimport.Prop, bool, []aggrega.Diagnostic) {
	t.Helper()
	file := ""
	if len(params) > 0 {
		file = params[0]
	}
	g, diags := lowered(fmt.Sprintf(base, value), file)
	for _, p := range node(t, g, "Api").Body {
		if p.Name != "Properties" {
			continue
		}
		for _, m := range p.Props {
			if m.Name == "P" {
				return m, true, diags
			}
		}
	}
	return cloudimport.Prop{}, false, diags
}

// valueOfP lowers value as lowerP does and returns the value P holds, failing
// where P is absent or holds an object or a list.
func valueOfP(t *testing.T, value string, params ...string) (cloudimport.Value, []aggrega.Diagnostic) {
	t.Helper()
	p, present, diags := lowerP(t, value, params...)
	if !present || len(p.Props) > 0 || len(p.Items) > 0 {
		t.Fatalf("%s lowers to %+v, present %v; want one value", value, p, present)
	}
	return p.Value, diags
}

// notCarriedAtP is the value-not-carried warning on the property P of base.
func notCarriedAtP(message string) aggrega.Diagnostic {
	return aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "value-not-carried", Message: message,
		SourceRef: "template.yaml#" + atP}
}

// strLit is the literal text s, as a leaf holds it.
func strLit(s string) cloudimport.Literal { return cloudimport.Literal{JSON: importer.String(s)} }

// cf is deploy-time text for the cloudformation target reading nothing the
// draft owes.
func cf(text string) cloudimport.Opaque {
	return cloudimport.Opaque{Text: text, Target: "cloudformation"}
}

// deployTime is a value of base's property P, the deploy-time text it lowers
// to, and the long form that text writes, as JSON.
type deployTime struct {
	value string
	want  cloudimport.Opaque
	long  string
}

// checkDeployTime lowers each case's value and checks it is the deploy-time
// text the case names, reported on nothing.
func checkDeployTime(t *testing.T, cases []deployTime) {
	t.Helper()
	for _, c := range cases {
		got, diags := valueOfP(t, c.value)
		noDiagnostics(t, diags)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s lowers to\n%#v\nwant\n%#v", c.value, got, c.want)
		}
	}
}

// owes is the Owed of deploy-time text reading what.
func owes(what string) string {
	return "it is text CloudFormation evaluates when the stack deploys, and it reads " + what
}

var (
	pseudoCases = []deployTime{
		{"!Ref AWS::Region", cf("!Ref AWS::Region"), `{"Ref": "AWS::Region"}`},
		{"!Ref AWS::AccountId", cf("!Ref AWS::AccountId"), `{"Ref": "AWS::AccountId"}`},
		{"!Ref AWS::Partition", cf("!Ref AWS::Partition"), `{"Ref": "AWS::Partition"}`},
		{"!Ref AWS::StackName", cf("!Ref AWS::StackName"), `{"Ref": "AWS::StackName"}`},
		{"!Ref AWS::URLSuffix", cf("!Ref AWS::URLSuffix"), `{"Ref": "AWS::URLSuffix"}`},
	}
	importValueCases = []deployTime{
		{"!ImportValue shared-vpc", cf("!ImportValue shared-vpc"), `{"Fn::ImportValue": "shared-vpc"}`},
		{"{'Fn::ImportValue': !Sub '${Env}-vpc'}", cloudimport.Opaque{
			Text: "!ImportValue {Fn::Sub: ['${Env}-vpc', {Env: dev}]}", Target: "cloudformation", Params: []string{"Env"}},
			`{"Fn::ImportValue": {"Fn::Sub": ["${Env}-vpc", {"Env": "dev"}]}}`},
	}
	functionCases = []deployTime{
		{"!Select [0, !GetAZs '']", cf("!Select [0, !GetAZs '']"), `{"Fn::Select": [0, {"Fn::GetAZs": ""}]}`},
		{`!Select [0, !GetAZs ""]`, cf("!Select [0, !GetAZs '']"), `{"Fn::Select": [0, {"Fn::GetAZs": ""}]}`},
		{"!Split [',', 'a,b']", cf("!Split [',', 'a,b']"), `{"Fn::Split": [",", "a,b"]}`},
		{"!GetAZs ''", cf("!GetAZs ''"), `{"Fn::GetAZs": ""}`},
		{"!Base64 'echo hello'", cf("!Base64 'echo hello'"), `{"Fn::Base64": "echo hello"}`},
		{"!Cidr ['10.0.0.0/16', 6, 5]", cf("!Cidr ['10.0.0.0/16', 6, 5]"), `{"Fn::Cidr": ["10.0.0.0/16", 6, 5]}`},
	}
	placedCases = []deployTime{
		{"!Select [0, !Ref Subnets]", cloudimport.Opaque{Text: "!Select [0, [subnet-a, subnet-b]]",
			Target: "cloudformation", Params: []string{"Subnets"}}, `{"Fn::Select": [0, ["subnet-a", "subnet-b"]]}`},
		{"!Join [':', [!Ref AWS::Region, !Ref Port]]", cloudimport.Opaque{
			Text: "!Join [':', [!Ref AWS::Region, '5432']]", Target: "cloudformation", Params: []string{"Port"}},
			`{"Fn::Join": [":", [{"Ref": "AWS::Region"}, "5432"]]}`},
		{"!Sub '${AWS::Region}-${Env}'", cloudimport.Opaque{Text: "!Sub ['${AWS::Region}-${Env}', {Env: dev}]",
			Target: "cloudformation", Params: []string{"Env"}}, `{"Fn::Sub": ["${AWS::Region}-${Env}", {"Env": "dev"}]}`},
	}
	dynamicCases = []deployTime{
		{"'{{resolve:ssm:/orders/db-host}}'", cf("{{resolve:ssm:/orders/db-host}}"),
			`"{{resolve:ssm:/orders/db-host}}"`},
	}
	ssmParameterCases = []deployTime{
		{"!Ref Ami", cloudimport.Opaque{Text: "{{resolve:ssm:/images/web}}", Target: "cloudformation",
			Params: []string{"Ami"}}, `"{{resolve:ssm:/images/web}}"`},
	}
	joinCases = []deployTime{
		{"!Join [',', !Ref Subnets]", cloudimport.Opaque{Text: "!Join [',', [subnet-a, subnet-b]]",
			Target: "cloudformation", Params: []string{"Subnets"}}, `{"Fn::Join": [",", ["subnet-a", "subnet-b"]]}`},
		{"!Join [!Ref Env, [a, b]]", cloudimport.Opaque{Text: "!Join [dev, [a, b]]", Target: "cloudformation",
			Params: []string{"Env"}}, `{"Fn::Join": ["dev", ["a", "b"]]}`},
		{"!Join ['', [!Ref Db, !Select [0, [x, z]]]]", cloudimport.Opaque{
			Text: "!Join ['', [!Ref Db, !Select [0, [x, z]]]]", Target: "cloudformation", Owed: owes("the resource " +
				"Db, which the drafted stack may declare under another logical ID, or not at all")},
			`{"Fn::Join": ["", [{"Ref": "Db"}, {"Fn::Select": [0, ["x", "z"]]}]]}`},
	}
	mappingCases = []deployTime{
		{"!FindInMap [Sizes, test, Class]", cloudimport.Opaque{Text: "!FindInMap [Sizes, test, Class]",
			Target: "cloudformation", Owed: owes("the mapping Sizes, which no template of the draft declares")},
			`{"Fn::FindInMap": ["Sizes", "test", "Class"]}`},
	}
	owedCases = []deployTime{
		{"!Select [0, !Split [',', !Ref Name]]", cloudimport.Opaque{Text: "!Select [0, !Split [',', !Ref Name]]",
			Target: "cloudformation", Owed: owes("the template parameter Name, which neither the parameters file nor " +
				"a literal default gives a value")}, `{"Fn::Select": [0, {"Fn::Split": [",", {"Ref": "Name"}]}]}`},
		{"{'Fn::Base64': !Sub 'pw=${Password}'}", cloudimport.Opaque{Text: "!Base64 {Fn::Sub: 'pw=${Password}'}",
			Target: "cloudformation", Owed: owes("the NoEcho template parameter Password, whose value is never " +
				"written")}, `{"Fn::Base64": {"Fn::Sub": "pw=${Password}"}}`},
		{"!Select [0, !Split [',', !Ref Ami]]", cloudimport.Opaque{Text: "!Select [0, !Split [',', !Ref Ami]]",
			Target: "cloudformation", Owed: owes("the template parameter Ami, whose value CloudFormation reads from " +
				"the Systems Manager parameter it names")}, `{"Fn::Select": [0, {"Fn::Split": [",", {"Ref": "Ami"}]}]}`},
		{"!Select [0, !GetAtt Db.SecurityGroups]", cloudimport.Opaque{Text: "!Select [0, !GetAtt Db.SecurityGroups]",
			Target: "cloudformation", Owed: owes("the resource Db, which the drafted stack may declare under another " +
				"logical ID, or not at all")}, `{"Fn::Select": [0, {"Fn::GetAtt": "Db.SecurityGroups"}]}`},
		{"!Sub '${AWS::StackName}-${Db}'", cloudimport.Opaque{Text: "!Sub '${AWS::StackName}-${Db}'",
			Target: "cloudformation", Owed: owes("the resource Db, which the drafted stack may declare under another " +
				"logical ID, or not at all")}, `{"Fn::Sub": "${AWS::StackName}-${Db}"}`},
		{"!Select [0, !Ref Missing]", cloudimport.Opaque{Text: "!Select [0, !Ref Missing]", Target: "cloudformation",
			Owed: owes("Missing, which the template declares no parameter or resource of")},
			`{"Fn::Select": [0, {"Ref": "Missing"}]}`},
		{"!FindInMap [Sizes, !Ref Env, Class]", cloudimport.Opaque{Text: "!FindInMap [Sizes, dev, Class]",
			Target: "cloudformation", Params: []string{"Env"}, Owed: owes("the mapping Sizes, which no template of " +
				"the draft declares")}, `{"Fn::FindInMap": ["Sizes", "dev", "Class"]}`},
		{"!If [InEast, a, b]", cloudimport.Opaque{Text: "!If [InEast, a, b]", Target: "cloudformation",
			Owed: owes("the condition InEast, which no template of the draft declares")},
			`{"Fn::If": ["InEast", "a", "b"]}`},
		{"!Join ['-', [!Ref AWS::Region, !GetAtt Db.Endpoint.Address, !Ref Name]]", cloudimport.Opaque{
			Text: "!Join ['-', [!Ref AWS::Region, !GetAtt Db.Endpoint.Address, !Ref Name]]", Target: "cloudformation",
			Owed: owes("the resource Db, which the drafted stack may declare under another logical ID, or not at " +
				"all, and the template parameter Name, which neither the parameters file nor a literal default " +
				"gives a value")},
			`{"Fn::Join": ["-", [{"Ref": "AWS::Region"}, {"Fn::GetAtt": "Db.Endpoint.Address"}, {"Ref": "Name"}]]}`},
	}
)

// TestLowerRefToParameterIsParam lowers a Ref to a template parameter,
// NoEcho too, to that parameter.
func TestLowerRefToParameterIsParam(t *testing.T) {
	t.Parallel()
	for name, value := range map[string]string{"Env": "!Ref Env", "Password": "!Ref Password", "Name": "!Ref Name"} {
		got, diags := valueOfP(t, value)
		noDiagnostics(t, diags)
		if want := (cloudimport.Param{Name: name}); !reflect.DeepEqual(got, want) {
			t.Errorf("%s lowers to %#v, want %#v", value, got, want)
		}
	}
}

// TestLowerRefToResourceIsItsIdentifier lowers a Ref to a resource to a
// reference to its identifier.
func TestLowerRefToResourceIsItsIdentifier(t *testing.T) {
	t.Parallel()
	got, diags := valueOfP(t, "!Ref Db")
	noDiagnostics(t, diags)
	if want := (cloudimport.Ref{Node: "Db"}); !reflect.DeepEqual(got, want) {
		t.Errorf("!Ref Db lowers to %#v, want %#v", got, want)
	}
}

// TestLowerPseudoParameterIsDeployTimeText lowers a Ref to a pseudo parameter
// to its short form, deploy-time text for the cloudformation target.
func TestLowerPseudoParameterIsDeployTimeText(t *testing.T) {
	t.Parallel()
	checkDeployTime(t, pseudoCases)
}

// TestGetAttStringAndListFormsDrawOneEdge lowers Fn::GetAtt written as one
// string, short or long, and as a list to one reference, the attribute's name
// split at each dot, and each draws the one edge the other does.
func TestGetAttStringAndListFormsDrawOneEdge(t *testing.T) {
	t.Parallel()
	want := []cloudimport.Edge{{From: "Api", To: "Db", Kind: cloudimport.EdgeRef, Path: []string{"Properties", "P"},
		Attr: []string{"Endpoint", "Address"}}}
	for _, value := range []string{"!GetAtt Db.Endpoint.Address", "!GetAtt [Db, Endpoint.Address]",
		"{'Fn::GetAtt': [Db, Endpoint.Address]}", "{'Fn::GetAtt': Db.Endpoint.Address}"} {
		g, diags := lowered(fmt.Sprintf(base, value), "")
		noDiagnostics(t, diags)
		if got := cloudimport.Edges(g); !reflect.DeepEqual(got, want) {
			t.Errorf("%s draws %+v, want %+v", value, got, want)
		}
	}
}

// TestLowerGetAttPathSplitsAtEachDot lowers A.B.C to the reference to A at
// [B, C], and a one-word attribute to a one-word path.
func TestLowerGetAttPathSplitsAtEachDot(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]cloudimport.Value{
		"!GetAtt Db.Endpoint.Port": cloudimport.Ref{Node: "Db", Attr: []string{"Endpoint", "Port"}},
		"!GetAtt [Db, Arn]":        cloudimport.Ref{Node: "Db", Attr: []string{"Arn"}},
	} {
		got, diags := valueOfP(t, value)
		noDiagnostics(t, diags)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s lowers to %#v, want %#v", value, got, want)
		}
	}
}

// TestLowerSubComposesLiteralsParametersAndReferences lowers Fn::Sub in its
// string and its list forms to composed text: ${!X} the literal text ${X},
// ${X.A.B} a reference to the attribute, a variable the list form binds read
// before the template's own, and text with no variable a literal. A part that
// is none of literal text, a parameter or a reference makes the whole
// deploy-time text.
func TestLowerSubComposesLiteralsParametersAndReferences(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]cloudimport.Value{
		"!Sub 'arn:aws:s3:::${Db}/${Env}-${!Literal}'": cloudimport.Compose{Parts: []cloudimport.Value{
			strLit("arn:aws:s3:::"), cloudimport.Ref{Node: "Db"}, strLit("/"), cloudimport.Param{Name: "Env"},
			strLit("-${Literal}")}},
		"!Sub ['postgres://${Host}:${Port}/${Db.Endpoint.Port}', {Host: !GetAtt Db.Endpoint.Address}]": cloudimport.
			Compose{Parts: []cloudimport.Value{strLit("postgres://"),
			cloudimport.Ref{Node: "Db", Attr: []string{"Endpoint", "Address"}}, strLit(":"),
			cloudimport.Param{Name: "Port"}, strLit("/"), cloudimport.Ref{Node: "Db", Attr: []string{"Endpoint", "Port"}}}},
		"!Sub ['${Db}-${Env}', {Db: orders, Env: !Ref Name}]": cloudimport.Compose{Parts: []cloudimport.Value{
			strLit("orders-"), cloudimport.Param{Name: "Name"}}},
		"!Sub ['${Count} items', {Count: 3}]": strLit("3 items"),
		"!Sub 'plain ${!Text}'":               strLit("plain ${Text}"),
		"!Sub '${AWS::Region}-${Env}'": cloudimport.Opaque{Text: "!Sub ['${AWS::Region}-${Env}', {Env: dev}]",
			Target: "cloudformation", Params: []string{"Env"}},
	} {
		got, diags := valueOfP(t, value)
		noDiagnostics(t, diags)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s lowers to\n%#v\nwant\n%#v", value, got, want)
		}
	}
}

// TestLowerJoinWithALiteralDelimiterComposes lowers Fn::Join with a literal
// delimiter and a literal list to composed text, a number or a boolean as its
// text and AWS::NoValue left out of the list; a delimiter or a list the
// template computes, or an item that is none of literal text, a parameter or
// a reference, makes it deploy-time text.
func TestLowerJoinWithALiteralDelimiterComposes(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]cloudimport.Value{
		"!Join ['', ['postgres://', !GetAtt Db.Endpoint.Address, ':', !Ref Port]]": cloudimport.Compose{
			Parts: []cloudimport.Value{strLit("postgres://"), cloudimport.Ref{Node: "Db",
				Attr: []string{"Endpoint", "Address"}}, strLit(":"), cloudimport.Param{Name: "Port"}}},
		"!Join [',', [a, 1, true]]":              strLit("a,1,true"),
		"!Join [',', [a, !Ref AWS::NoValue, b]]": strLit("a,b"),
		"!Join ['-', []]":                        strLit(""),
	} {
		got, diags := valueOfP(t, value)
		noDiagnostics(t, diags)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s lowers to\n%#v\nwant\n%#v", value, got, want)
		}
	}
	checkDeployTime(t, joinCases)
}

// TestLowerFindInMapWithLiteralKeysIsTheMappingsValue reads Fn::FindInMap
// with literal keys as the value the mapping holds there; keys the template
// computes, or keys the mapping holds nothing at, make it deploy-time text
// reading the mapping, which no template of the draft declares.
func TestLowerFindInMapWithLiteralKeysIsTheMappingsValue(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]cloudimport.Value{
		"!FindInMap [Sizes, prod, Class]": strLit("db.r6g.large"),
	} {
		got, diags := valueOfP(t, value)
		noDiagnostics(t, diags)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s lowers to\n%#v\nwant\n%#v", value, got, want)
		}
	}
	checkDeployTime(t, mappingCases)
}

// TestLowerIfOnAParameterEqualityIsTheBranchItsValueSelects lowers Fn::If on a
// condition comparing one template parameter with a literal to the branch the
// parameter's value selects — the value the parameters file supplies, else its
// default — and names the other branch in a warning; a condition reading
// anything else makes it deploy-time text.
func TestLowerIfOnAParameterEqualityIsTheBranchItsValueSelects(t *testing.T) {
	t.Parallel()
	value := "!If [IsProd, db.r6g.large, db.t3.micro]"
	got, diags := valueOfP(t, value)
	onlyDiagnostic(t, diags, notCarriedAtP("the template template.yaml writes Fn::If on the condition IsProd at "+
		atP+", which the value the template parameter Env is given makes false, so the draft carries its branch at "+
		atP+"/Fn::If/2 and not the one at "+atP+"/Fn::If/1: where the stack deploys with a value of Env that "+
		"makes IsProd true, write that branch by hand"))
	if want := strLit("db.t3.micro"); !reflect.DeepEqual(got, want) {
		t.Errorf("%s lowers to %#v, want %#v", value, got, want)
	}

	got, diags = valueOfP(t, value, `[{"ParameterKey": "Env", "ParameterValue": "prod"}]`)
	onlyDiagnostic(t, diags, notCarriedAtP("the template template.yaml writes Fn::If on the condition IsProd at "+
		atP+", which the value the template parameter Env is given makes true, so the draft carries its branch at "+
		atP+"/Fn::If/1 and not the one at "+atP+"/Fn::If/2: where the stack deploys with a value of Env that "+
		"makes IsProd false, write that branch by hand"))
	if want := strLit("db.r6g.large"); !reflect.DeepEqual(got, want) {
		t.Errorf("%s with Env prod lowers to %#v, want %#v", value, got, want)
	}

	got, diags = valueOfP(t, "!If [InEast, a, b]")
	noDiagnostics(t, diags)
	if want := (cloudimport.Opaque{Text: "!If [InEast, a, b]", Target: "cloudformation",
		Owed: owes("the condition InEast, which no template of the draft declares")}); !reflect.DeepEqual(got, want) {
		t.Errorf("!If [InEast, a, b] lowers to %#v, want %#v", got, want)
	}
}

// TestLowerImportValueIsDeployTimeText lowers Fn::ImportValue to its short
// form, a template parameter it reads written in place.
func TestLowerImportValueIsDeployTimeText(t *testing.T) {
	t.Parallel()
	checkDeployTime(t, importValueCases)
}

// TestLowerSelectSplitGetAZsBase64CidrAreDeployTimeText lowers each of those
// functions to its one-line short form, its strings single-quoted where YAML
// needs them quoted.
func TestLowerSelectSplitGetAZsBase64CidrAreDeployTimeText(t *testing.T) {
	t.Parallel()
	checkDeployTime(t, functionCases)
}

// TestLowerDeployTimeTextWritesParametersInPlace writes each template
// parameter deploy-time text reads as the value it is given, a list
// parameter's as the list of its comma-separated members, and names it among
// the text's Params.
func TestLowerDeployTimeTextWritesParametersInPlace(t *testing.T) {
	t.Parallel()
	checkDeployTime(t, placedCases)
}

// TestLowerDeployTimeTextOwesWhatTheDraftCannotWrite names, in the text's
// Owed, each thing deploy-time text reads that the draft can neither write in
// its place nor declare: a parameter given no value, a NoEcho parameter, a
// Systems Manager parameter type, a resource, a name the template declares
// nothing of, a mapping and a condition.
func TestLowerDeployTimeTextOwesWhatTheDraftCannotWrite(t *testing.T) {
	t.Parallel()
	checkDeployTime(t, owedCases)
}

// TestLowerGetStackOutputIsNotCarried leaves Fn::GetStackOutput, which takes a
// mapping, with no value, naming the property; a function with no one-line
// short form is not carried either.
func TestLowerGetStackOutputIsNotCarried(t *testing.T) {
	t.Parallel()
	got, diags := valueOfP(t, "{'Fn::GetStackOutput': {StackName: shared, OutputName: VpcId}}")
	onlyDiagnostic(t, diags, notCarriedAtP("the template template.yaml reads another stack's output with "+
		"Fn::GetStackOutput at "+atP+", which takes a mapping, and a catalog Recipe placing its one-line text "+
		"writes it as text, so it is not carried: export the output and read it with Fn::ImportValue, or write the "+
		"value by hand"))
	if got != nil {
		t.Errorf("Fn::GetStackOutput lowers to %#v, want no value", got)
	}

	got, diags = valueOfP(t, "{'Fn::Length': [a, b]}")
	onlyDiagnostic(t, diags, notCarriedAtP("the template template.yaml writes Fn::Length at "+atP+", which has no "+
		"one-line YAML short form a catalog Recipe reads back as the function, so it is not carried: write the "+
		"value by hand"))
	if got != nil {
		t.Errorf("Fn::Length lowers to %#v, want no value", got)
	}
}

// TestLowerNoValueLeavesTheMemberAbsent drops a member or an item that is
// AWS::NoValue, as CloudFormation does, and the branch of Fn::If selecting it.
func TestLowerNoValueLeavesTheMemberAbsent(t *testing.T) {
	t.Parallel()
	if p, present, diags := lowerP(t, "!Ref AWS::NoValue"); present {
		t.Errorf("!Ref AWS::NoValue lowers to %+v, want P absent", p)
	} else {
		noDiagnostics(t, diags)
	}

	p, present, diags := lowerP(t, "[a, !Ref AWS::NoValue]")
	noDiagnostics(t, diags)
	if want := (cloudimport.Prop{Name: "P", Items: []cloudimport.Prop{{Value: lit(`"a"`)}}}); !present ||
		!reflect.DeepEqual(p, want) {
		t.Errorf("[a, !Ref AWS::NoValue] lowers to %+v, want %+v", p, want)
	}

	p, present, diags = lowerP(t, "!If [IsProd, x, !Ref AWS::NoValue]")
	if present {
		t.Errorf("!If selecting AWS::NoValue lowers to %+v, want P absent", p)
	}
	onlyDiagnostic(t, diags, notCarriedAtP("the template template.yaml writes Fn::If on the condition IsProd at "+
		atP+", which the value the template parameter Env is given makes false, so the draft carries its branch at "+
		atP+"/Fn::If/2 and not the one at "+atP+"/Fn::If/1: where the stack deploys with a value of Env that "+
		"makes IsProd true, write that branch by hand"))
}

// TestLowerDynamicReferences lowers a Secrets Manager and a Systems Manager
// SecureString dynamic reference to the secret its store holds, the locator a
// secret's ARN with its colons kept, and a plain Systems Manager reference to
// deploy-time text; an Fn::Sub or Fn::Join wholly a Secrets Manager reference
// to a template resource is the credential of that resource it reads.
func TestLowerDynamicReferences(t *testing.T) {
	t.Parallel()
	arn := "arn:aws:secretsmanager:eu-west-1:123456789012:secret:orders-db-AbCdEf"
	for value, want := range map[string]cloudimport.Value{
		"'{{resolve:secretsmanager:" + arn + ":SecretString:password}}'": cloudimport.Stored{Store: "secretsmanager",
			Locator: arn, Text: "{{resolve:secretsmanager:" + arn + ":SecretString:password}}"},
		"'{{resolve:secretsmanager:orders-db:SecretString:password}}'": cloudimport.Stored{Store: "secretsmanager",
			Locator: "orders-db", Text: "{{resolve:secretsmanager:orders-db:SecretString:password}}"},
		"'{{resolve:ssm-secure:/orders/db-password:3}}'": cloudimport.Stored{Store: "ssm-secure",
			Locator: "/orders/db-password:3", Text: "{{resolve:ssm-secure:/orders/db-password:3}}"},
		"'{{resolve:ssm:/orders/db-host}}'": cf("{{resolve:ssm:/orders/db-host}}"),
		"!Sub '{{resolve:secretsmanager:${DbSecret}:SecretString:password}}'": cloudimport.Credential{
			Node: "DbSecret", What: "SecretString:password"},
		"!Join ['', ['{{resolve:secretsmanager:', !Ref DbSecret, ':SecretString:password}}']]": cloudimport.Credential{
			Node: "DbSecret", What: "SecretString:password"},
		"!Sub '{{resolve:secretsmanager:${Env}:SecretString:password}}'": cloudimport.Compose{
			Parts: []cloudimport.Value{strLit("{{resolve:secretsmanager:"), cloudimport.Param{Name: "Env"},
				strLit(":SecretString:password}}")}},
	} {
		got, diags := valueOfP(t, value)
		noDiagnostics(t, diags)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s lowers to\n%#v\nwant\n%#v", value, got, want)
		}
	}
}

// TestLowerSsmParameterTypeIsADynamicReference lowers a Ref to a parameter of
// a Systems Manager parameter type given a literal value to the dynamic
// reference resolving that Systems Manager parameter; one whose type reads a
// list, which a dynamic reference reads as one text, stays the parameter.
func TestLowerSsmParameterTypeIsADynamicReference(t *testing.T) {
	t.Parallel()
	checkDeployTime(t, ssmParameterCases)
	got, diags := valueOfP(t, "!Ref Zones")
	noDiagnostics(t, diags)
	if want := (cloudimport.Param{Name: "Zones"}); !reflect.DeepEqual(got, want) {
		t.Errorf("!Ref Zones lowers to %#v, want %#v", got, want)
	}
}

// TestLowerOutputsLikeProperties lowers each output's value as a property's
// value is lowered.
func TestLowerOutputsLikeProperties(t *testing.T) {
	t.Parallel()
	text := fmt.Sprintf(base, "x") + "Outputs:\n  Address:\n    Value: !GetAtt Db.Endpoint.Address\n" +
		"  Region:\n    Value: !Ref AWS::Region\n"
	g, diags := lowered(text, "")
	noDiagnostics(t, diags)
	want := []cloudimport.Output{
		{Name: "Address", Value: cloudimport.Ref{Node: "Db", Attr: []string{"Endpoint", "Address"}}},
		{Name: "Region", Value: cf("!Ref AWS::Region")},
	}
	if !reflect.DeepEqual(g.Outputs, want) {
		t.Errorf("the outputs lower to %+v, want %+v", g.Outputs, want)
	}
}

// TestQuotedShortFormLiteralIsNotCarried leaves with no value a literal that
// is wholly a short form — CloudFormation reads it as text, and a catalog
// Recipe placing it would read it as the intrinsic function — wherever the
// template writes it: a property, an item of a list, a mapping's value read by
// Fn::FindInMap, the text an Fn::Sub with no variable writes, and a
// parameter's default, each warned value-not-carried naming where it is
// written.
func TestQuotedShortFormLiteralIsNotCarried(t *testing.T) {
	t.Parallel()
	message := func(at string) string {
		return "the template template.yaml writes text at " + at + " that is wholly a CloudFormation short form, " +
			"which CloudFormation reads as text and a catalog Recipe placing it would read as an intrinsic function, " +
			"so it is not carried: write the value by hand where the stack deploys"
	}
	for _, value := range []string{"'!Ref Db'", `"!GetAtt Db.Endpoint.Address"`, "!Sub '!Ref Db'"} {
		got, diags := valueOfP(t, value)
		onlyDiagnostic(t, diags, notCarriedAtP(message(atP)))
		if got != nil {
			t.Errorf("%s lowers to %#v, want no value", value, got)
		}
	}

	p, _, diags := lowerP(t, "[a, '!Ref Db']")
	onlyDiagnostic(t, diags, aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "value-not-carried",
		Message: message(atP + "/1"), SourceRef: "template.yaml#" + atP + "/1"})
	if want := []cloudimport.Prop{{Value: lit(`"a"`)}, {}}; !reflect.DeepEqual(p.Items, want) {
		t.Errorf("the list lowers to %+v, want %+v", p.Items, want)
	}

	mapped := fmt.Sprintf(base, "!FindInMap [Sizes, dev, Class]")
	mapped = replaceOnce(t, mapped, "dev: {Class: db.t3.micro}", "dev: {Class: '!Ref Db'}")
	g, diags := lowered(mapped, "")
	onlyDiagnostic(t, diags, notCarriedAtP(message(atP)))
	if got := valueAt(t, node(t, g, "Api").Body, "Properties", "P"); got != nil {
		t.Errorf("the mapping's value lowers to %#v, want no value", got)
	}

	defaulted := replaceOnce(t, fmt.Sprintf(base, "!Ref Env"), "Default: dev,", "Default: '!Ref Db',")
	g, diags = lowered(defaulted, "")
	onlyDiagnostic(t, diags, aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "value-not-carried",
		Message: "the template template.yaml gives the parameter Env a default that is wholly a CloudFormation " +
			"short form, which CloudFormation reads as text and a catalog Recipe placing it would read as an " +
			"intrinsic function, so the default is not carried: write the value by hand where the stack deploys",
		SourceRef: "template.yaml#/Parameters/Env/Default"})
	for _, p := range g.Params {
		if p.Name == "Env" && p.Default != nil {
			t.Errorf("the parameter Env reads %+v, want it with no default", p)
		}
	}
}

// replaceOnce returns s with old, which it holds once, replaced by new.
func replaceOnce(t *testing.T, s, old, new string) string {
	t.Helper()
	if n := strings.Count(s, old); n != 1 {
		t.Fatalf("the template holds %q %d times, want once", old, n)
	}
	return strings.Replace(s, old, new, 1)
}

// FuzzLowerTemplate reads and lowers a fuzzed document as the template the
// import is handed, seeded with every file of the testdata and with base: no
// input panics, and two reads of one input give one result.
func FuzzLowerTemplate(f *testing.F) {
	err := filepath.WalkDir("testdata", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel("testdata", p)
		if err != nil {
			return err
		}
		f.Add(fixture(f, filepath.ToSlash(rel)))
		return nil
	})
	if err != nil {
		f.Fatal(err)
	}
	for _, c := range owedCases {
		f.Add([]byte(fmt.Sprintf(base, c.value)))
	}
	f.Fuzz(func(t *testing.T, doc []byte) {
		in := aggrega.ImportInput{Format: formatName, Sources: []aggrega.ImportSource{src("template.yaml", doc)}}
		g1, d1 := Read(in, cloudimport.Settings{}, parse)
		g2, d2 := Read(in, cloudimport.Settings{}, parse)
		if !reflect.DeepEqual(g1, g2) || !reflect.DeepEqual(d1, d2) {
			t.Fatalf("two reads of one input differ")
		}
		if g1 != nil {
			cloudimport.Edges(g1)
		}
	})
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// readSections reads the template of every section, alone.
func readSections(t *testing.T) (*cloudimport.Graph, []aggrega.Diagnostic) {
	t.Helper()
	return readAll("", src("template.yaml", fixture(t, "sections/template.yaml")))
}

// brokenResource is the warning on the resource of the sections template that
// is no object.
var brokenResource = aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "resource-not-carried",
	Message: "the template template.yaml writes the resource Broken at /Resources/Broken, which is no object " +
		"declaring its Type as text, so it is not drafted: write it as an object whose Type names its resource type",
	SourceRef: "template.yaml#/Resources/Broken"}

// TestSectionsParametersAreRead reads each parameter's type, description,
// allowed values, bounds and default; a type naming an existing resource of
// the cloud, AWS:: or List<AWS::, marks it a pointer.
func TestSectionsParametersAreRead(t *testing.T) {
	t.Parallel()
	g, diags := readSections(t)
	onlyDiagnostic(t, diags, brokenResource)
	num := func(s string) *json.Number { n := json.Number(s); return &n }
	length := func(n int) *int { return &n }
	want := []cloudimport.Parameter{
		{Name: "DbName", Type: "String", Default: lit(`"orders"`), MinLength: length(1), MaxLength: length(63)},
		{Name: "DbPassword", Type: "String", Secure: true},
		{Name: "DbPort", Type: "Number", Default: lit(`5432`), Min: num("1024"), Max: num("65535")},
		{Name: "Env", Type: "String", Description: "The environment the stack runs in.", Default: lit(`"dev"`),
			Allowed: []json.RawMessage{json.RawMessage(`"dev"`), json.RawMessage(`"prod"`)}},
		{Name: "SubnetIds", Type: "List<AWS::EC2::Subnet::Id>", Pointer: true},
		{Name: "VpcId", Type: "AWS::EC2::VPC::Id", Pointer: true},
	}
	if !reflect.DeepEqual(g.Params, want) {
		t.Errorf("the parameters read are\n%+v\nwant\n%+v", g.Params, want)
	}
}

// TestSectionsNoEchoDefaultIsNeverRead marks a NoEcho parameter secure and
// drops its default, so no secret a template writes there reaches the graph,
// and a note says the draft never writes it.
func TestSectionsNoEchoDefaultIsNeverRead(t *testing.T) {
	t.Parallel()
	g, diags := readSections(t)
	onlyDiagnostic(t, diags, brokenResource)
	dump, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(dump), "placeholder-not-a-secret") {
		t.Errorf("the graph holds the NoEcho parameter's default")
	}
	if want := "The template parameter DbPassword is NoEcho, so the draft never writes its default."; !slices.Contains(
		g.Notes, want) {
		t.Errorf("the notes are %q, want them to hold %q", g.Notes, want)
	}
}

// TestSectionsResourcesBecomeNodes reads each resource into a node keyed by its
// logical ID, ordered by ID: its type, its condition's name, its dependencies,
// written as one name or a list, the file and JSON pointer it is read from, no
// name, and a body of every other member but its Metadata, each value the long
// form the template reader writes. A resource that is no object is not
// carried, with a warning naming where it is written.
func TestSectionsResourcesBecomeNodes(t *testing.T) {
	t.Parallel()
	g, diags := readSections(t)
	onlyDiagnostic(t, diags, brokenResource)
	if got, want := nodeIDs(g), []string{"DbSecurityGroup", "OrdersDb", "OrdersQueue"}; !slices.Equal(got, want) {
		t.Fatalf("the graph holds %q, want %q", got, want)
	}
	want := &cloudimport.Node{ID: "OrdersDb", Type: "AWS::RDS::DBInstance", Form: cloudimport.FormRaw,
		Condition: lit(`"IsProd"`), DependsOn: []string{"OrdersQueue"}, Source: "template.yaml#/Resources/OrdersDb",
		Body: []cloudimport.Prop{
			{Name: "DeletionPolicy", Value: lit(`"Snapshot"`)},
			{Name: "Properties", Props: []cloudimport.Prop{
				{Name: "DBInstanceClass", Value: lit(`{"Fn::FindInMap": ["Sizes", {"Ref": "Env"}, "Class"]}`)},
				{Name: "DBName", Value: lit(`{"Ref": "DbName"}`)},
				{Name: "Engine", Value: lit(`"postgres"`)},
				{Name: "MasterUserPassword", Value: lit(`{"Ref": "DbPassword"}`)},
				{Name: "Port", Value: lit(`{"Ref": "DbPort"}`)},
				{Name: "Tags", Value: lit(`[]`)},
				{Name: "VPCSecurityGroups", Items: []cloudimport.Prop{
					{Value: lit(`{"Fn::GetAtt": "DbSecurityGroup.GroupId"}`)},
				}},
			}},
		}}
	if got := node(t, g, "OrdersDb"); !reflect.DeepEqual(got, want) {
		t.Errorf("OrdersDb reads\n%+v\nwant\n%+v", got, want)
	}
	queue := &cloudimport.Node{ID: "OrdersQueue", Type: "AWS::SQS::Queue", Form: cloudimport.FormRaw,
		DependsOn: []string{"DbSecurityGroup", "OrdersDb"}, Source: "template.yaml#/Resources/OrdersQueue",
		Body: []cloudimport.Prop{{Name: "Properties", Value: lit(`{}`)}}}
	if got := node(t, g, "OrdersQueue"); !reflect.DeepEqual(got, queue) {
		t.Errorf("OrdersQueue reads\n%+v\nwant\n%+v", got, queue)
	}
}

// TestSectionsOutputsAndNotes reads each output's value, and notes, in order,
// the transform the template declares, its Metadata, its Rules, the allowed
// pattern of a parameter, the default of a NoEcho parameter and the name each
// output is exported under, none of which the draft carries.
func TestSectionsOutputsAndNotes(t *testing.T) {
	t.Parallel()
	g, diags := readSections(t)
	onlyDiagnostic(t, diags, brokenResource)
	outputs := []cloudimport.Output{
		{Name: "DbAddress", Value: lit(`{"Fn::GetAtt": "OrdersDb.Endpoint.Address"}`)},
		{Name: "Plain", Value: lit(`"plain text"`)},
		{Name: "QueueUrl", Value: lit(`{"Ref": "OrdersQueue"}`)},
	}
	if !reflect.DeepEqual(g.Outputs, outputs) {
		t.Errorf("the outputs read are %+v, want %+v", g.Outputs, outputs)
	}
	notes := []string{
		"The template declares the transform AWS::Serverless-2016-10-31.",
		"The template's Metadata, which the draft does not carry, holds AWS::CloudFormation::Interface, Owner.",
		"The template's Rules, which the draft does not carry, check its parameters when the stack deploys: " +
			"ProdNeedsSubnets.",
		`The template parameter DbName allows only values matching its AllowedPattern, "[a-z][a-z0-9]*", which ` +
			`the draft does not carry.`,
		"The template parameter DbPassword is NoEcho, so the draft never writes its default.",
		"The output DbAddress is exported as orders-db-address, which the draft does not export.",
		"The output QueueUrl is exported under a name the template computes when the stack deploys, which the " +
			"draft does not export.",
	}
	if !slices.Equal(g.Notes, notes) {
		t.Errorf("the notes are\n%q\nwant\n%q", g.Notes, notes)
	}
}

// TestSectionsMappingsAndConditionsAreKept keeps the root template's Mappings
// and Conditions, each the long form the template reader writes, for the
// values that read them.
func TestSectionsMappingsAndConditionsAreKept(t *testing.T) {
	t.Parallel()
	r := &reader{in: aggrega.ImportInput{Format: formatName, Sources: []aggrega.ImportSource{
		src("template.yaml", fixture(t, "sections/template.yaml"))}}, parse: parse}
	if g := r.read(cloudimport.Settings{}); g == nil {
		t.Fatalf("the read halts: %+v", r.diags)
	}
	if got, want := compact(t, r.root.mappings.get("Sizes").json()), `{"dev":{"Class":"db.t3.micro"},"prod":{"Class":`+
		`"db.r6g.large"}}`; got != want {
		t.Errorf("the mapping Sizes is kept as %s, want %s", got, want)
	}
	if got, want := compact(t, r.root.conditions.get("IsProd").json()), `{"Fn::Equals":[{"Ref":"Env"},`+
		`"prod"]}`; got != want {
		t.Errorf("the condition IsProd is kept as %s, want %s", got, want)
	}
}

// compact returns raw with no insignificant space.
func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	if raw == nil {
		t.Fatalf("nothing is kept")
	}
	return string(lit(string(raw)).JSON)
}

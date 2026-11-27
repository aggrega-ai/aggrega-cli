// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// readNested reads and lowers sources with the root setting root.
func readNested(root string, sources ...aggrega.ImportSource) (*cloudimport.Graph, []aggrega.Diagnostic) {
	return Read(aggrega.ImportInput{Format: formatName, Sources: sources}, cloudimport.Settings{Root: root}, parse)
}

// flat reads the nested/flat fixture: main.yaml deploying network.yaml as the
// nested stack Network, which deploys subnets.yaml as the nested stack
// Subnets.
func flat(t *testing.T) (*cloudimport.Graph, []aggrega.Diagnostic) {
	t.Helper()
	g, diags := readNested("", fixtures(t, "nested/flat", "main.yaml", "network.yaml", "subnets.yaml")...)
	if g == nil {
		t.Fatalf("the read halts: %+v", diags)
	}
	return g, diags
}

// folded is the record of the nested stack at, of the template file, read as
// the resources of its template child, which contribute ids.
func folded(file, at, typ, child, ids string) aggrega.Diagnostic {
	return aggrega.Diagnostic{Severity: aggrega.SeverityInfo, Code: "resource-folded", Message: at + " (" + typ +
		") of " + file + " is read as the resources of its template " + child + ", which join the draft as " + ids +
		"; nothing is drafted for it", SourceRef: file + "#/Resources/" + at}
}

// propsOf returns the members of the Properties of n's body.
func propsOf(t *testing.T, n *cloudimport.Node) []cloudimport.Prop {
	t.Helper()
	for _, p := range n.Body {
		if p.Name == "Properties" {
			return p.Props
		}
	}
	t.Fatalf("%s writes no Properties", n.ID)
	return nil
}

// TestNestedStackIsFlattened reads a nested stack deploying a template the
// import holds as that template's resources, each a node whose ID is the
// stack's logical ID before its own: each Ref to a parameter of the nested
// template reads the value the stack's Parameters bind it to, lowered where the
// stack is written, so a parameter of the root bound whole is that parameter;
// one bound to nothing reads its default, and one declaring none is a
// parameter of the graph named by the stack and the parameter, named in the
// notes; the nested template's mappings and conditions are its own.
func TestNestedStackIsFlattened(t *testing.T) {
	t.Parallel()
	g, _ := flat(t)
	if got, want := nodeIDs(g), []string{"Api", "NetworkSubnetsSubnet", "NetworkVpc"}; !slices.Equal(got, want) {
		t.Fatalf("the graph holds %q, want %q", got, want)
	}
	vpc := node(t, g, "NetworkVpc")
	if vpc.Type != "AWS::EC2::VPC" || vpc.Source != "network.yaml#/Resources/Vpc" {
		t.Errorf("NetworkVpc is %s read from %s", vpc.Type, vpc.Source)
	}
	want := []cloudimport.Prop{
		{Name: "CidrBlock", Value: cloudimport.Text("10.0.0.0/16")},
		{Name: "Name", Value: cloudimport.Compose{Parts: []cloudimport.Value{cloudimport.Param{Name: "Env"},
			cloudimport.Text("-net")}}},
		{Name: "Size", Value: cloudimport.Text("small")},
		{Name: "Tier", Value: cloudimport.Text("standard")},
		{Name: "Zone", Value: cloudimport.Param{Name: "NetworkZone"}},
	}
	if got := propsOf(t, vpc); !reflect.DeepEqual(got, want) {
		t.Errorf("NetworkVpc's properties read\n%#v\nwant\n%#v", got, want)
	}
	if !reflect.DeepEqual(vpc.Condition, cloudimport.Param{Name: "Enabled"}) {
		t.Errorf("NetworkVpc's condition reads %#v, want the root's parameter Enabled its template's condition reads",
			vpc.Condition)
	}
	if got := propsOf(t, node(t, g, "NetworkSubnetsSubnet")); !reflect.DeepEqual(got, []cloudimport.Prop{{Name: "VpcId",
		Value: cloudimport.Ref{Node: "NetworkVpc"}}}) {
		t.Errorf("the subnet's properties read %#v, want its VpcId the VPC's identifier", got)
	}
	wantParams := []cloudimport.Parameter{
		{Name: "Enabled", Type: "boolean", Default: lit(`true`),
			Allowed: []json.RawMessage{json.RawMessage(`true`), json.RawMessage(`false`)}},
		{Name: "Env", Type: "String", Default: lit(`"dev"`)},
		{Name: "NetworkZone", Type: "String"},
	}
	if !reflect.DeepEqual(g.Params, wantParams) {
		t.Errorf("the parameters read\n%+v\nwant\n%+v", g.Params, wantParams)
	}
	wantNotes := []string{
		"The nested stack Network of main.yaml is read as the resources of its template network.yaml, each drafted " +
			"with Network before its logical ID.",
		"The nested stack Network binds no value to the parameter Zone of network.yaml, which declares no default " +
			"for it, so the draft reads it as the template parameter NetworkZone.",
		"The nested stack Subnets of network.yaml is read as the resources of its template subnets.yaml, each " +
			"drafted with NetworkSubnets before its logical ID.",
	}
	if !slices.Equal(g.Notes, wantNotes) {
		t.Errorf("the notes read\n%q\nwant\n%q", g.Notes, wantNotes)
	}
}

// TestNestedStackOutputResolvesToTheChildValue reads an output of a nested
// stack, by Fn::GetAtt in its string and its list form and as a variable of
// Fn::Sub, as the value the output holds, lowered in the nested template: a
// reference to a resource of it is a Ref to its node, through two levels of
// nesting, and text it composes of its parameters is composed of the values
// they are bound to.
func TestNestedStackOutputResolvesToTheChildValue(t *testing.T) {
	t.Parallel()
	g, _ := flat(t)
	want := []cloudimport.Prop{
		{Name: "Label", Value: cloudimport.Compose{Parts: []cloudimport.Value{cloudimport.Param{Name: "Env"},
			cloudimport.Text("-net-standard")}}},
		{Name: "SubnetId", Value: cloudimport.Ref{Node: "NetworkSubnetsSubnet"}},
		{Name: "VpcId", Value: cloudimport.Ref{Node: "NetworkVpc"}},
	}
	if got := propsOf(t, node(t, g, "Api")); !reflect.DeepEqual(got, want) {
		t.Errorf("Api's properties read\n%#v\nwant\n%#v", got, want)
	}
	wantOutputs := []cloudimport.Output{{Name: "Vpc", Value: cloudimport.Ref{Node: "NetworkVpc"}}}
	if !reflect.DeepEqual(g.Outputs, wantOutputs) {
		t.Errorf("the outputs read %#v, want %#v", g.Outputs, wantOutputs)
	}
}

// TestNestedStackDependsOnNamesWhatItContributed reads a dependency on a
// flattened nested stack as one on each node its template contributes,
// through two levels of nesting; the nested stack is no node, and its record
// names the nodes it contributes.
func TestNestedStackDependsOnNamesWhatItContributed(t *testing.T) {
	t.Parallel()
	g, diags := flat(t)
	if got, want := node(t, g, "Api").DependsOn, []string{"NetworkSubnetsSubnet", "NetworkVpc"}; !slices.Equal(got,
		want) {
		t.Errorf("Api depends on %q, want %q", got, want)
	}
	want := []aggrega.Diagnostic{
		folded("main.yaml", "Network", stackType, "network.yaml", "NetworkSubnetsSubnet, NetworkVpc"),
		folded("network.yaml", "Subnets", stackType, "subnets.yaml", "NetworkSubnetsSubnet"),
	}
	if !reflect.DeepEqual(diags, want) {
		t.Errorf("the read reports\n%+v\nwant\n%+v", diags, want)
	}
	var edges []string
	for _, e := range cloudimport.Edges(g) {
		edges = append(edges, e.From+">"+e.To)
	}
	want2 := []string{"Api>NetworkSubnetsSubnet", "Api>NetworkVpc", "Api>NetworkSubnetsSubnet", "Api>NetworkVpc",
		"NetworkSubnetsSubnet>NetworkVpc"}
	if !slices.Equal(edges, want2) {
		t.Errorf("the edges read %q, want %q", edges, want2)
	}
}

// TestNestedApplicationWithALocalLocationIsFlattened reads a serverless
// application whose Location is a path inside the imported directory as the
// resources of the template it names, as a nested stack's.
func TestNestedApplicationWithALocalLocationIsFlattened(t *testing.T) {
	t.Parallel()
	g, diags := readNested("", fixtures(t, "nested/application", "template.yaml", "apps/orders.yaml")...)
	onlyDiagnostic(t, diags, folded("template.yaml", "Orders", applicationType, "apps/orders.yaml", "OrdersQueue"))
	if got, want := nodeIDs(g), []string{"OrdersQueue", "Reader"}; !slices.Equal(got, want) {
		t.Fatalf("the graph holds %q, want %q", got, want)
	}
	name := cloudimport.Compose{Parts: []cloudimport.Value{cloudimport.Text("orders-"), cloudimport.Param{Name: "Stage"}}}
	if got := valueAt(t, node(t, g, "OrdersQueue").Body, "Properties", "QueueName"); !reflect.DeepEqual(got, name) {
		t.Errorf("the queue's name reads %#v, want %#v", got, name)
	}
	queue := valueAt(t, node(t, g, "Reader").Body, "Properties", "Environment", "Variables", "QUEUE")
	if got := queue; !reflect.DeepEqual(got, cloudimport.Ref{Node: "OrdersQueue"}) {
		t.Errorf("the function's QUEUE reads %#v, want the queue's identifier", got)
	}
	wantNotes := []string{
		"The template declares the transform AWS::Serverless-2016-10-31.",
		"The serverless application Orders of template.yaml is read as the resources of its template " +
			"apps/orders.yaml, each drafted with Orders before its logical ID.",
		"The template apps/orders.yaml declares the transform AWS::Serverless-2016-10-31.",
	}
	if !slices.Equal(g.Notes, wantNotes) {
		t.Errorf("the notes read\n%q\nwant\n%q", g.Notes, wantNotes)
	}
}

// notReadLink is the warning that the nested stack at of main.yaml, of type
// typ, deploys a template how, which the import does not read; property names
// the property linking it.
func notReadLink(at, typ, how, property string) aggrega.Diagnostic {
	return aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "linked-source-not-read", Message: at + " (" + typ +
		") of main.yaml deploys a template " + how + ", which the import does not read: no resource of that " +
		"template is drafted, " + at + " is drafted as written, and each output of it the template reads is carried " +
		"as text CloudFormation evaluates when the stack deploys; to draft its resources, place the template inside " +
		"the imported directory and write its " + property + " as a relative path to it",
		SourceRef: "main.yaml#/Resources/" + at}
}

// TestRemoteNestedStackIsNotRead keeps as a node each nested stack whose
// template is linked by a URL of Amazon S3 or the web, or by a value the
// template computes, and each serverless application located by its ID, each
// with a linked-source-not-read warning; an output of one is read as
// deploy-time text.
func TestRemoteNestedStackIsNotRead(t *testing.T) {
	t.Parallel()
	g, diags := readNested("", fixtures(t, "nested/remote", "main.yaml")...)
	want := []aggrega.Diagnostic{
		notReadLink("Computed", stackType, "located by a value the template computes when the stack deploys",
			"TemplateURL"),
		notReadLink("Published", applicationType, "located by the ID of an application the AWS Serverless "+
			"Application Repository publishes", "Location"),
		notReadLink("Remote", stackType, "linked by a URL", "TemplateURL"),
		notReadLink("Stored", stackType, "linked by a URL", "TemplateURL"),
	}
	if !reflect.DeepEqual(diags, want) {
		t.Fatalf("the read reports\n%+v\nwant\n%+v", diags, want)
	}
	if got, want := nodeIDs(g), []string{"Api", "Computed", "Published", "Remote", "Stored"}; !slices.Equal(got, want) {
		t.Fatalf("the graph holds %q, want %q", got, want)
	}
	api := node(t, g, "Api")
	for _, c := range []struct {
		prop string
		want cloudimport.Opaque
	}{
		{"Endpoint", cloudimport.Opaque{Text: "!GetAtt Remote.Outputs.Endpoint", Target: "cloudformation",
			Owed: owes("the resource Remote, which the drafted stack may declare under another logical ID, or not at " +
				"all"), Reads: []string{"Remote"}}},
		{"Url", cloudimport.Opaque{Text: "!Sub 'https://${Published.Outputs.Host}/'", Target: "cloudformation",
			Owed: owes("the resource Published, which the drafted stack may declare under another logical ID, or not " +
				"at all"), Reads: []string{"Published"}}},
	} {
		if got := valueAt(t, api.Body, "Properties", c.prop); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Api's %s reads %#v, want %#v", c.prop, got, c.want)
		}
	}
}

// TestChildOutsideTheDirectoryNotRead keeps as a node a nested stack linking
// its template by a path leaving the imported directory, with a
// linked-source-not-read warning.
func TestChildOutsideTheDirectoryNotRead(t *testing.T) {
	t.Parallel()
	g, diags := readNested("", fixtures(t, "nested/outside", "main.yaml")...)
	onlyDiagnostic(t, diags, notReadLink("Shared", stackType, "linked by a path leaving the imported directory",
		"TemplateURL"))
	if got, want := nodeIDs(g), []string{"Shared"}; !slices.Equal(got, want) {
		t.Errorf("the graph holds %q, want %q", got, want)
	}
}

// TestNestedStackLinkingATemplateItDeploysIsNotFlattenedAgain reads two
// templates linking each other from the one the root setting names: the
// nested stack deploying the root's template again is a node, with a
// linked-source-not-read warning, so the read ends.
func TestNestedStackLinkingATemplateItDeploysIsNotFlattenedAgain(t *testing.T) {
	t.Parallel()
	g, diags := readNested("a.yaml", fixtures(t, "cycle", "a.yaml", "b.yaml")...)
	want := []aggrega.Diagnostic{
		{Severity: aggrega.SeverityWarn, Code: "linked-source-not-read", Message: "A (" + stackType + ") of b.yaml " +
			"links its template a.yaml, which deploys the template linking it, directly or through others, so it is " +
			"not read again: no resource of it is drafted a second time, and A is drafted as written; link each " +
			"template once", SourceRef: "b.yaml#/Resources/A"},
		folded("a.yaml", "B", stackType, "b.yaml", "BA"),
	}
	if !reflect.DeepEqual(diags, want) {
		t.Fatalf("the read reports\n%+v\nwant\n%+v", diags, want)
	}
	if got, want := nodeIDs(g), []string{"BA"}; !slices.Equal(got, want) {
		t.Errorf("the graph holds %q, want %q", got, want)
	}
}

// TestNestedNoEchoParameterValueIsNeverWritten reads the NoEcho parameters of
// a nested template: one bound to a NoEcho parameter of the root reads that
// parameter, one bound to text the template writes is not carried, and one
// bound to nothing is a parameter of the graph, secure, its default never
// read; neither text is held anywhere in the graph or the diagnostics.
func TestNestedNoEchoParameterValueIsNeverWritten(t *testing.T) {
	t.Parallel()
	main := `AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  DbPassword: {Type: String, NoEcho: true}
Resources:
  Db:
    Type: AWS::CloudFormation::Stack
    Properties:
      TemplateURL: db.yaml
      Parameters:
        Password: !Ref DbPassword
        Token: hunter2-token
`
	db := `Parameters:
  Password: {Type: String, NoEcho: true}
  Token: {Type: String, NoEcho: true}
  Key: {Type: String, NoEcho: true, Default: kept-secret}
Resources:
  Instance:
    Type: AWS::RDS::DBInstance
    Properties:
      MasterUserPassword: !Ref Password
      Token: !Ref Token
      Key: !Ref Key
`
	g, diags := readNested("", src("main.yaml", []byte(main)), src("db.yaml", []byte(db)))
	notCarried := aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "value-not-carried", Message: "the " +
		"template main.yaml binds the NoEcho parameter Token of the nested stack Db at " +
		"/Resources/Db/Properties/Parameters/Token to a value written in the template, which the draft never " +
		"writes, so it is not carried: pass it through a NoEcho parameter of main.yaml",
		SourceRef: "main.yaml#/Resources/Db/Properties/Parameters/Token"}
	want := []aggrega.Diagnostic{folded("main.yaml", "Db", stackType, "db.yaml", "DbInstance"), notCarried}
	if !reflect.DeepEqual(diags, want) {
		t.Fatalf("the read reports\n%+v\nwant\n%+v", diags, want)
	}
	wantProps := []cloudimport.Prop{
		{Name: "Key", Value: cloudimport.Param{Name: "DbKey"}},
		{Name: "MasterUserPassword", Value: cloudimport.Param{Name: "DbPassword"}},
		{Name: "Token"},
	}
	if got := propsOf(t, node(t, g, "DbInstance")); !reflect.DeepEqual(got, wantProps) {
		t.Errorf("the instance's properties read\n%#v\nwant\n%#v", got, wantProps)
	}
	wantParams := []cloudimport.Parameter{{Name: "DbKey", Type: "String", Secure: true},
		{Name: "DbPassword", Type: "String", Secure: true}}
	if !reflect.DeepEqual(g.Params, wantParams) {
		t.Errorf("the parameters read\n%+v\nwant\n%+v", g.Params, wantParams)
	}
	if note := "The nested stack Db binds no value to the parameter Key of db.yaml, whose default the draft never " +
		"writes, as it is NoEcho, so the draft reads it as the template parameter DbKey."; !slices.Contains(g.Notes,
		note) {
		t.Errorf("the notes %q do not hold %q", g.Notes, note)
	}
	dump, err := json.Marshal(struct {
		G *cloudimport.Graph
		D []aggrega.Diagnostic
	}{g, diags})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"kept-secret", "hunter2-token"} {
		if strings.Contains(string(dump), secret) {
			t.Errorf("the read holds the NoEcho value %s", secret)
		}
	}
}

// TestNestedParameterValuesReadAsTheirTypes reads the parameters of a nested
// template as CloudFormation reads each type: a list parameter bound to text
// is the list of its comma-separated members, written whole or in place in
// deploy-time text; a parameter reading the Systems Manager parameter its
// value names, given its default, is the dynamic reference resolving it; and
// Fn::If on a condition comparing a parameter bound to text takes the branch
// that text selects.
func TestNestedParameterValuesReadAsTheirTypes(t *testing.T) {
	t.Parallel()
	main := `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Network:
    Type: AWS::CloudFormation::Stack
    Properties:
      TemplateURL: net.yaml
      Parameters: {Zones: 'a, b', Mode: quiet}
`
	net := `Parameters:
  Zones: {Type: CommaDelimitedList}
  Mode: {Type: String}
  Ami: {Type: 'AWS::SSM::Parameter::Value<AWS::EC2::Image::Id>', Default: /images/web}
Conditions:
  IsQuiet: !Equals [!Ref Mode, quiet]
Resources:
  Vpc:
    Type: AWS::EC2::VPC
    Properties:
      Zones: !Ref Zones
      First: !Select [0, !Ref Zones]
      Image: !Ref Ami
      Level: !If [IsQuiet, low, high]
`
	g, diags := readNested("", src("main.yaml", []byte(main)), src("net.yaml", []byte(net)))
	want := []aggrega.Diagnostic{
		folded("main.yaml", "Network", stackType, "net.yaml", "NetworkVpc"),
		{Severity: aggrega.SeverityWarn, Code: "value-not-carried", Message: "the template net.yaml writes Fn::If on " +
			"the condition IsQuiet at /Resources/Vpc/Properties/Level, which the value the template parameter Mode " +
			"is given makes true, so the draft carries its branch at /Resources/Vpc/Properties/Level/Fn::If/1 and not " +
			"the one at /Resources/Vpc/Properties/Level/Fn::If/2: where the stack deploys with a value of Mode that " +
			"makes IsQuiet false, write that branch by hand", SourceRef: "net.yaml#/Resources/Vpc/Properties/Level"},
	}
	if !reflect.DeepEqual(diags, want) {
		t.Fatalf("the read reports\n%+v\nwant\n%+v", diags, want)
	}
	wantProps := []cloudimport.Prop{
		{Name: "First", Value: cf("!Select [0, [a, b]]")},
		{Name: "Image", Value: cf("{{resolve:ssm:/images/web}}")},
		{Name: "Level", Value: cloudimport.Text("low")},
		{Name: "Zones", Value: lit(`["a", "b"]`)},
	}
	if got := propsOf(t, node(t, g, "NetworkVpc")); !reflect.DeepEqual(got, wantProps) {
		t.Errorf("the VPC's properties read\n%#v\nwant\n%#v", got, wantProps)
	}
}

// TestNestedValueMeasuresAsItsText carries a parameter of a nested template
// bound to text of 12,000 angle brackets: the value measures as its text,
// which the template reader writes escaped for HTML, and is within the size
// the draft carries of one value.
func TestNestedValueMeasuresAsItsText(t *testing.T) {
	t.Parallel()
	brackets := strings.Repeat("<", 12000)
	main := "AWSTemplateFormatVersion: \"2010-09-09\"\nResources:\n  Network:\n    Type: AWS::CloudFormation::Stack\n" +
		"    Properties:\n      TemplateURL: net.yaml\n      Parameters: {Name: '" + brackets + "'}\n"
	net := "Parameters:\n  Name: {Type: String}\nResources:\n  Vpc:\n    Type: AWS::EC2::VPC\n" +
		"    Properties: {Name: !Ref Name}\n"
	g, diags := readNested("", src("main.yaml", []byte(main)), src("net.yaml", []byte(net)))
	onlyDiagnostic(t, diags, folded("main.yaml", "Network", stackType, "net.yaml", "NetworkVpc"))
	if got := valueAt(t, node(t, g, "NetworkVpc").Body, "Properties", "Name"); !reflect.DeepEqual(got,
		cloudimport.Text(brackets)) {
		t.Errorf("the VPC's name reads %d bytes of value, want the text bound", cloudimport.Size(got))
	}
}

// TestNestedNoValueBindingReadsTheDefault reads a parameter of a nested
// template bound to Fn::If whose branch the template's values select is
// AWS::NoValue as CloudFormation does: as its default, lowered in the nested
// template; one declaring no default is not carried, CloudFormation requiring
// a value.
func TestNestedNoValueBindingReadsTheDefault(t *testing.T) {
	t.Parallel()
	main := `AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  Env: {Type: String, Default: dev}
Conditions:
  IsProd: !Equals [!Ref Env, prod]
Resources:
  Network:
    Type: AWS::CloudFormation::Stack
    Properties:
      TemplateURL: net.yaml
      Parameters:
        Size: !If [IsProd, large, !Ref AWS::NoValue]
`
	branch := aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "value-not-carried", Message: "the template " +
		"main.yaml writes Fn::If on the condition IsProd at /Resources/Network/Properties/Parameters/Size, which the " +
		"value the template parameter Env is given makes false, so the draft carries its branch at " +
		"/Resources/Network/Properties/Parameters/Size/Fn::If/2 and not the one at " +
		"/Resources/Network/Properties/Parameters/Size/Fn::If/1: where the stack deploys with a value of Env that " +
		"makes IsProd true, write that branch by hand",
		SourceRef: "main.yaml#/Resources/Network/Properties/Parameters/Size"}
	net := func(size string) []byte {
		return []byte("Parameters:\n  Size: " + size + "\nResources:\n  Vpc:\n    Type: AWS::EC2::VPC\n" +
			"    Properties: {Name: !Ref Size}\n")
	}
	t.Run("a default", func(t *testing.T) {
		t.Parallel()
		g, diags := readNested("", src("main.yaml", []byte(main)), src("net.yaml", net("{Type: String, Default: small}")))
		want := []aggrega.Diagnostic{folded("main.yaml", "Network", stackType, "net.yaml", "NetworkVpc"), branch}
		if !reflect.DeepEqual(diags, want) {
			t.Fatalf("the read reports\n%+v\nwant\n%+v", diags, want)
		}
		if got := valueAt(t, node(t, g, "NetworkVpc").Body, "Properties", "Name"); !reflect.DeepEqual(got,
			cloudimport.Text("small")) {
			t.Errorf("the VPC's name reads %#v, want the default small", got)
		}
	})
	t.Run("no default", func(t *testing.T) {
		t.Parallel()
		g, diags := readNested("", src("main.yaml", []byte(main)), src("net.yaml", net("{Type: String}")))
		want := []aggrega.Diagnostic{folded("main.yaml", "Network", stackType, "net.yaml", "NetworkVpc"), branch,
			{Severity: aggrega.SeverityWarn, Code: "value-not-carried", Message: "the template main.yaml binds the " +
				"parameter Size of the nested stack Network at /Resources/Network/Properties/Parameters/Size to no " +
				"value, as the branch of Fn::If it carries is AWS::NoValue, and its template net.yaml declares no " +
				"default for it, while CloudFormation requires a value, so it is not carried: bind it to a value, or " +
				"declare a default for it in net.yaml",
				SourceRef: "main.yaml#/Resources/Network/Properties/Parameters/Size"}}
		if !reflect.DeepEqual(diags, want) {
			t.Fatalf("the read reports\n%+v\nwant\n%+v", diags, want)
		}
		if got := propsOf(t, node(t, g, "NetworkVpc")); !reflect.DeepEqual(got, []cloudimport.Prop{{Name: "Name"}}) {
			t.Errorf("the VPC's properties read %#v, want its name with no value", got)
		}
	})
}

// TestNestedTemplateNotesNameWhatItDeclares names in the notes what a nested
// template declares that no resource carries, as the root template's are
// named: its Metadata, its Rules, the name an output is exported under, and
// the AllowedPattern of a parameter the graph takes for it.
func TestNestedTemplateNotesNameWhatItDeclares(t *testing.T) {
	t.Parallel()
	main := `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Network:
    Type: AWS::CloudFormation::Stack
    Properties: {TemplateURL: net.yaml}
`
	net := `Metadata:
  Owner: platform
Parameters:
  Zone: {Type: String, AllowedPattern: '[a-z]+'}
Rules:
  CheckZone:
    Assertions:
      - Assert: !Not [!Equals [!Ref Zone, '']]
Resources:
  Vpc: {Type: AWS::EC2::VPC}
Outputs:
  VpcId:
    Value: !Ref Vpc
    Export: {Name: shared-vpc}
`
	g, _ := readNested("", src("main.yaml", []byte(main)), src("net.yaml", []byte(net)))
	want := []string{
		"The nested stack Network of main.yaml is read as the resources of its template net.yaml, each drafted " +
			"with Network before its logical ID.",
		"The template net.yaml's Metadata, which the draft does not carry, holds Owner.",
		"The template net.yaml's Rules, which the draft does not carry, check its parameters when the stack " +
			"deploys: CheckZone.",
		"The output VpcId of the template net.yaml is exported as shared-vpc, which the draft does not export.",
		"The nested stack Network binds no value to the parameter Zone of net.yaml, which declares no default for " +
			"it, so the draft reads it as the template parameter NetworkZone.",
		"The template parameter NetworkZone allows only values matching its AllowedPattern, \"[a-z]+\", which the " +
			"draft does not carry.",
	}
	if !slices.Equal(g.Notes, want) {
		t.Errorf("the notes read\n%q\nwant\n%q", g.Notes, want)
	}
}

// TestNestedParameterNotCarriedIsOwedWithItsReason owes, in deploy-time text,
// a parameter of a nested template whose value the draft does not carry,
// saying why: a NoEcho value the template writes, and a binding to a list.
func TestNestedParameterNotCarriedIsOwedWithItsReason(t *testing.T) {
	t.Parallel()
	main := `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Network:
    Type: AWS::CloudFormation::Stack
    Properties:
      TemplateURL: net.yaml
      Parameters: {Token: written, Zones: [a, b]}
`
	net := `Parameters:
  Token: {Type: String, NoEcho: true}
  Zones: {Type: String}
Resources:
  Vpc:
    Type: AWS::EC2::VPC
    Properties:
      Token: {'Fn::Base64': !Sub '${Token}'}
      Zones: {'Fn::Base64': !Sub '${Zones}'}
`
	g, _ := readNested("", src("main.yaml", []byte(main)), src("net.yaml", []byte(net)))
	vpc := node(t, g, "NetworkVpc")
	for prop, why := range map[string]string{
		"Token": "whose value the template writes, which the draft never writes, as it is NoEcho",
		"Zones": "which its stack binds to an object or a list",
	} {
		got, _ := valueAt(t, vpc.Body, "Properties", prop).(cloudimport.Opaque)
		if want := owes("the parameter " + prop + " of the nested stack Network, " + why); got.Owed != want {
			t.Errorf("%s owes %q, want %q", prop, got.Owed, want)
		}
	}
}

// TestNestedStackConditionAndDependenciesReachItsResources reads a nested
// stack deployed under a condition and after another resource: each resource
// of its template is deployed under that condition and after that resource,
// beside its own dependencies.
func TestNestedStackConditionAndDependenciesReachItsResources(t *testing.T) {
	t.Parallel()
	main := `AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  Enabled: {Type: String, Default: 'true', AllowedValues: ['true', 'false']}
Conditions:
  IsOn: !Equals [!Ref Enabled, 'true']
Resources:
  Store: {Type: AWS::S3::Bucket}
  Network:
    Type: AWS::CloudFormation::Stack
    Condition: IsOn
    DependsOn: Store
    Properties: {TemplateURL: net.yaml}
`
	net := `Resources:
  Vpc: {Type: AWS::EC2::VPC}
  Gateway: {Type: AWS::EC2::InternetGateway, DependsOn: Vpc}
`
	g, diags := readNested("", src("main.yaml", []byte(main)), src("net.yaml", []byte(net)))
	onlyDiagnostic(t, diags, folded("main.yaml", "Network", stackType, "net.yaml", "NetworkGateway, NetworkVpc"))
	for id, deps := range map[string][]string{"NetworkVpc": {"Store"}, "NetworkGateway": {"NetworkVpc", "Store"}} {
		n := node(t, g, id)
		if !slices.Equal(n.DependsOn, deps) {
			t.Errorf("%s depends on %q, want %q", id, n.DependsOn, deps)
		}
		if !reflect.DeepEqual(n.Condition, cloudimport.Param{Name: "Enabled"}) {
			t.Errorf("%s's condition reads %#v, want the stack's", id, n.Condition)
		}
	}
}

// TestNestedIDsAreMadeUnique gives a resource of a nested template whose ID,
// the stack's logical ID before its own, a resource of the root takes, that ID
// and the least number from 2 no node takes.
func TestNestedIDsAreMadeUnique(t *testing.T) {
	t.Parallel()
	main := `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  NetworkVpc: {Type: AWS::EC2::VPC}
  Network:
    Type: AWS::CloudFormation::Stack
    Properties: {TemplateURL: net.yaml}
`
	net := "Resources:\n  Vpc: {Type: AWS::EC2::VPC}\n"
	g, diags := readNested("", src("main.yaml", []byte(main)), src("net.yaml", []byte(net)))
	onlyDiagnostic(t, diags, folded("main.yaml", "Network", stackType, "net.yaml", "NetworkVpc2"))
	if got, want := nodeIDs(g), []string{"NetworkVpc", "NetworkVpc2"}; !slices.Equal(got, want) {
		t.Errorf("the graph holds %q, want %q", got, want)
	}
	if got := node(t, g, "NetworkVpc2").Source; got != "net.yaml#/Resources/Vpc" {
		t.Errorf("NetworkVpc2 is read from %s", got)
	}
}

// TestNestedStackIdentifierIsDeployTimeTextItOwes reads the identifier of a
// flattened nested stack, which the draft declares no stack of, as
// deploy-time text owing it; a binding of a parameter the nested template
// does not declare, and what the nested stack writes beside its template and
// its parameters, are not carried.
func TestNestedStackIdentifierIsDeployTimeTextItOwes(t *testing.T) {
	t.Parallel()
	main := `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Network:
    Type: AWS::CloudFormation::Stack
    DeletionPolicy: Retain
    Properties:
      TemplateURL: net.yaml
      TimeoutInMinutes: 5
      Parameters: {Extra: x}
  Api:
    Type: AWS::ECS::Service
    Properties: {Stack: !Ref Network}
`
	net := "Resources:\n  Vpc: {Type: AWS::EC2::VPC}\n"
	g, diags := readNested("", src("main.yaml", []byte(main)), src("net.yaml", []byte(net)))
	want := []aggrega.Diagnostic{
		{Severity: aggrega.SeverityWarn, Code: "value-not-carried", Message: "the template main.yaml writes " +
			"DeletionPolicy, Properties.TimeoutInMinutes on the nested stack Network, which the draft reads as the " +
			"resources of its template net.yaml and carries none of: apply them to those resources by hand",
			SourceRef: "main.yaml#/Resources/Network"},
		{Severity: aggrega.SeverityWarn, Code: "value-not-carried", Message: "the template main.yaml binds the " +
			"parameter Extra of the nested stack Network at /Resources/Network/Properties/Parameters/Extra, which its " +
			"template net.yaml declares no parameter of, so it is not read: remove it from the stack's Parameters, or " +
			"declare it in net.yaml", SourceRef: "main.yaml#/Resources/Network/Properties/Parameters/Extra"},
		folded("main.yaml", "Network", stackType, "net.yaml", "NetworkVpc"),
	}
	if !reflect.DeepEqual(diags, want) {
		t.Fatalf("the read reports\n%+v\nwant\n%+v", diags, want)
	}
	owed := owes("the nested stack Network, which the draft reads as the resources of its template, and declares " +
		"no stack of")
	wantValue := cloudimport.Opaque{Text: "!Ref Network", Target: "cloudformation", Owed: owed, OwedBesideReads: owed}
	if got := valueAt(t, node(t, g, "Api").Body, "Properties", "Stack"); !reflect.DeepEqual(got, wantValue) {
		t.Errorf("the stack's identifier reads %#v, want %#v", got, wantValue)
	}
}

// TestNestedStackNameReadsTheStackTheDraftDoesNotDeploy reads the pseudo
// parameter naming the stack in a nested template as deploy-time text owing
// it: there it names the nested stack, which the draft does not deploy.
func TestNestedStackNameReadsTheStackTheDraftDoesNotDeploy(t *testing.T) {
	t.Parallel()
	main := `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Network:
    Type: AWS::CloudFormation::Stack
    Properties: {TemplateURL: net.yaml}
`
	net := `Resources:
  Vpc:
    Type: AWS::EC2::VPC
    Properties:
      Name: !Ref AWS::StackName
      Region: !Ref AWS::Region
`
	g, _ := readNested("", src("main.yaml", []byte(main)), src("net.yaml", []byte(net)))
	want := []cloudimport.Prop{
		{Name: "Name", Value: cloudimport.Opaque{Text: "!Ref AWS::StackName", Target: "cloudformation",
			Owed: owes("the pseudo parameter AWS::StackName, which in the nested stack Network names that stack, " +
				"which the draft reads as the resources of its template and does not deploy"),
			OwedBesideReads: owes("the pseudo parameter AWS::StackName, which in the nested stack Network names " +
				"that stack, which the draft reads as the resources of its template and does not deploy")}},
		{Name: "Region", Value: cf("!Ref AWS::Region")},
	}
	if got := propsOf(t, node(t, g, "NetworkVpc")); !reflect.DeepEqual(got, want) {
		t.Errorf("the VPC's properties read\n%#v\nwant\n%#v", got, want)
	}
}

// TestNestedOutputsComposingEachOtherAreBounded reads twenty nested stacks of
// one template, each binding its parameter to the output of the one before,
// which the template composes of that parameter twice: an output past the
// size the draft carries of one value is not carried, and what reads it is
// deploy-time text owing it. Two stacks binding each other's output read
// nothing of themselves.
func TestNestedOutputsComposingEachOtherAreBounded(t *testing.T) {
	t.Parallel()
	child := []byte(`Parameters:
  In: {Type: String}
Resources:
  Queue: {Type: AWS::SQS::Queue}
Outputs:
  Out:
    Value: !Sub '${In}${In}'
`)
	stack := func(id, in string) string {
		return fmt.Sprintf("  %s:\n    Type: AWS::CloudFormation::Stack\n    Properties:\n      TemplateURL: c.yaml\n"+
			"      Parameters: {In: %s}\n", id, in)
	}
	t.Run("a chain", func(t *testing.T) {
		t.Parallel()
		main := "AWSTemplateFormatVersion: \"2010-09-09\"\nResources:\n" + stack("S01", strings.Repeat("a", 64))
		for i := 2; i <= 20; i++ {
			main += stack(fmt.Sprintf("S%02d", i), fmt.Sprintf("!GetAtt S%02d.Outputs.Out", i-1))
		}
		main += "  Api:\n    Type: AWS::ECS::Service\n    Properties: {P: !GetAtt S20.Outputs.Out}\n"
		g, diags := readNested("", src("main.yaml", []byte(main)), src("c.yaml", child))
		var large []aggrega.Diagnostic
		for _, d := range diags {
			if d.Code == "value-not-carried" {
				large = append(large, d)
			}
		}
		onlyDiagnostic(t, large, aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "value-not-carried",
			Message: "the output Out of c.yaml, read for the nested stack S10, is larger than the 65536 bytes the " +
				"draft carries of one value, its parts composing one another, so what reads it is not carried",
			SourceRef: "c.yaml#/Outputs/Out/Value"})
		got, ok := valueAt(t, node(t, g, "Api").Body, "Properties", "P").(cloudimport.Opaque)
		if !ok || got.Owed == "" || len(got.Text) > 64 {
			t.Errorf("the last output reads %#v, want short deploy-time text owing what it reads", got)
		}
	})
	t.Run("a cycle", func(t *testing.T) {
		t.Parallel()
		main := "AWSTemplateFormatVersion: \"2010-09-09\"\nResources:\n" + stack("Sa", "!GetAtt Sb.Outputs.Out") +
			stack("Sb", "!GetAtt Sa.Outputs.Out") +
			"  Api:\n    Type: AWS::ECS::Service\n    Properties: {P: !GetAtt Sa.Outputs.Out}\n"
		g, diags := readNested("", src("main.yaml", []byte(main)), src("c.yaml", child))
		var cycles []aggrega.Diagnostic
		for _, d := range diags {
			if d.Code == "value-not-carried" {
				cycles = append(cycles, d)
			}
		}
		onlyDiagnostic(t, cycles, aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "value-not-carried",
			Message: "the output Out of c.yaml, read for the nested stack Sa, reads itself through the parameters " +
				"and outputs of nested stacks, so it is not carried: bind each parameter to a value that does not " +
				"read its own stack's outputs", SourceRef: "c.yaml#/Outputs/Out/Value"})
		if _, ok := valueAt(t, node(t, g, "Api").Body, "Properties", "P").(cloudimport.Opaque); !ok {
			t.Errorf("the output reading itself reads %#v, want deploy-time text owing it",
				valueAt(t, node(t, g, "Api").Body, "Properties", "P"))
		}
	})
}

// TestNestedResourcesPastTheBoundAreNotRead reads nested stacks deploying
// more resources than the import reads of nested templates: the stack past
// the bound is a node, its template not read, with a resource-not-carried
// warning.
func TestNestedResourcesPastTheBoundAreNotRead(t *testing.T) {
	t.Parallel()
	var child, main strings.Builder
	child.WriteString("Resources:\n")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&child, "  Q%03d: {Type: AWS::SQS::Queue}\n", i)
	}
	main.WriteString("AWSTemplateFormatVersion: \"2010-09-09\"\nResources:\n")
	for i := 0; i <= 100; i++ {
		fmt.Fprintf(&main, "  S%03d:\n    Type: AWS::CloudFormation::Stack\n    Properties: {TemplateURL: c.yaml}\n", i)
	}
	g, diags := readNested("", src("main.yaml", []byte(main.String())), src("c.yaml", []byte(child.String())))
	var notCarried []aggrega.Diagnostic
	for _, d := range diags {
		if d.Code != "resource-folded" {
			notCarried = append(notCarried, d)
		}
	}
	onlyDiagnostic(t, notCarried, aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "resource-not-carried",
		Message: "S100 (" + stackType + ") of main.yaml deploys the template c.yaml, which is not read, as the " +
			"nested stacks of the import deploy more than the 10000 resources it reads of them: no resource of that " +
			"template is drafted, and S100 is drafted as written", SourceRef: "main.yaml#/Resources/S100"})
	if len(g.Nodes) != 10001 || node(t, g, "S100").Type != stackType {
		t.Errorf("the graph holds %d nodes, want the 10000 resources read and the stack past the bound", len(g.Nodes))
	}
}

// FuzzReadNestedStack reads and lowers a fuzzed document as the template two
// nested stacks of a root deploy, binding its parameters and reading its
// outputs, and as a root deploying the nested stacks of the testdata; it is
// seeded with every file of the testdata. No input panics, two reads of one
// input give one result, and the graph read gives its edges and its keys.
func FuzzReadNestedStack(f *testing.F) {
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
	linking := []byte(`AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  Env: {Type: String, Default: dev}
Resources:
  One:
    Type: AWS::CloudFormation::Stack
    Properties:
      TemplateURL: ./child.yaml
      Parameters: {Env: !Ref Env, Cidr: 10.0.0.0/16, In: !GetAtt Two.Outputs.Out}
  Two:
    Type: AWS::Serverless::Application
    Properties:
      Location: child.yaml
      Parameters: {In: !Sub '${One.Outputs.Out}-${Env}'}
  Api:
    Type: AWS::ECS::Service
    DependsOn: [One, Two]
    Properties: {A: !GetAtt One.Outputs.VpcId, B: !Sub '${Two.Outputs.Label}', C: !Ref One}
`)
	network := fixture(f, "nested/flat/network.yaml")
	subnets := fixture(f, "nested/flat/subnets.yaml")
	f.Fuzz(func(t *testing.T, doc []byte) {
		requests := [][]aggrega.ImportSource{
			{src("main.yaml", linking), src("child.yaml", doc)},
			{src("main.yaml", doc), src("network.yaml", network), src("subnets.yaml", subnets)},
		}
		for _, sources := range requests {
			in := aggrega.ImportInput{Format: formatName, Sources: sources}
			g1, d1 := Read(in, cloudimport.Settings{Root: "main.yaml"}, parse)
			g2, d2 := Read(in, cloudimport.Settings{Root: "main.yaml"}, parse)
			if !reflect.DeepEqual(g1, g2) || !reflect.DeepEqual(d1, d2) {
				t.Fatalf("two reads of one input differ")
			}
			if g1 == nil {
				continue
			}
			cloudimport.Edges(g1)
			cloudimport.Format{Key: Key}.Keys(g1.Nodes)
		}
	})
}

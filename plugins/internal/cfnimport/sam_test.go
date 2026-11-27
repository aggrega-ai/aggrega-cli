// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"reflect"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// readSam reads and lowers the template testdata/sam/<dir>/template.yaml
// handed alone.
func readSam(t *testing.T, dir string) (*cloudimport.Graph, []aggrega.Diagnostic) {
	t.Helper()
	return Read(aggrega.ImportInput{Format: formatName, Sources: fixtures(t, "sam/"+dir, "template.yaml")},
		cloudimport.Settings{}, parse)
}

// readText reads and lowers the template text handed alone.
func readText(text string) (*cloudimport.Graph, []aggrega.Diagnostic) {
	return Read(aggrega.ImportInput{Format: formatName, Sources: []aggrega.ImportSource{src("template.yaml",
		[]byte(text))}}, cloudimport.Settings{}, parse)
}

// objectProp is the property name of an object whose members are props.
func objectProp(name string, props ...cloudimport.Prop) cloudimport.Prop {
	return cloudimport.Prop{Name: name, Props: props}
}

// listProp is the property name of an array whose items are the literal JSON
// texts items.
func listProp(name string, items ...string) cloudimport.Prop {
	p := cloudimport.Prop{Name: name}
	for _, item := range items {
		p.Items = append(p.Items, cloudimport.Prop{Value: lit(item)})
	}
	return p
}

// leafProp is the property name holding the literal JSON text value.
func leafProp(name, value string) cloudimport.Prop {
	return cloudimport.Prop{Name: name, Value: lit(value)}
}

// TestGlobalsPrimitiveMapAndListRules merges a SAM template's Globals into
// each resource of the type each names, as SAM merges them: a primitive the
// resource writes replaces the global one, Timeout 10 under Globals and 30 on
// the resource reading 30; a map is merged key by key, the resource's entry
// winning, Environment.Variables holding both maps' variables; a list is the
// global entries followed by the resource's, Layers reading the shared layer
// first; an intrinsic function is a primitive, so a list the resource writes
// replaces a global Ref; Architectures, which SAM replaces rather than
// prepends, reads the resource's alone. A resource writing nothing of a
// property takes the global value whole, a resource writing no Properties
// takes the Globals of its type, and a resource of a type Globals names none
// of is left as it is.
func TestGlobalsPrimitiveMapAndListRules(t *testing.T) {
	t.Parallel()
	g, diags := readSam(t, "globals")
	noDiagnostics(t, diags)
	layers := func(own ...string) cloudimport.Prop {
		return listProp("Layers", append([]string{`"arn:aws:lambda:eu-west-1:123456789012:layer:shared:1"`}, own...)...)
	}
	want := map[string][]cloudimport.Prop{
		"Orders": {
			listProp("Architectures", `"arm64"`),
			objectProp("Environment", objectProp("Variables", leafProp("QUEUE", `"orders-queue"`),
				leafProp("STAGE", `"production"`), leafProp("TABLE", `"orders-table"`))),
			leafProp("ImageUri", `"123456789012.dkr.ecr.eu-west-1.amazonaws.com/orders:1.0"`),
			layers(`"arn:aws:lambda:eu-west-1:123456789012:layer:orders:3"`),
			leafProp("MemorySize", "512"),
			leafProp("PackageType", `"Image"`),
			leafProp("Timeout", "30"),
			objectProp("VpcConfig", listProp("SecurityGroupIds", `"sg-global"`),
				listProp("SubnetIds", `"subnet-orders"`)),
		},
		"Billing": {
			listProp("Architectures", `"x86_64"`),
			objectProp("Environment", objectProp("Variables", leafProp("STAGE", `"production"`),
				leafProp("TABLE", `"global-table"`))),
			leafProp("ImageUri", `"123456789012.dkr.ecr.eu-west-1.amazonaws.com/billing:1.0"`),
			layers(),
			leafProp("MemorySize", "512"),
			leafProp("PackageType", `"Image"`),
			leafProp("Timeout", "10"),
			objectProp("VpcConfig", listProp("SecurityGroupIds", `"sg-global"`),
				cloudimport.Prop{Name: "SubnetIds", Value: cloudimport.Param{Name: "Subnets"}}),
		},
		"Ledger": {objectProp("SSESpecification", leafProp("SSEEnabled", "true"))},
		"Queue":  {leafProp("VisibilityTimeout", "60")},
	}
	for _, id := range []string{"Billing", "Ledger", "Orders", "Queue"} {
		if got := propsOf(t, node(t, g, id)); !reflect.DeepEqual(got, want[id]) {
			t.Errorf("%s's properties read\n%#v\nwant\n%#v", id, got, want[id])
		}
	}
}

// TestGlobalsPropertyTheTransformDoesNotReadIsNotMerged writes under the
// Globals of Function one property SAM supports there, Timeout, and one it
// does not, Events, Policies or Role, which SAM refuses: the supported one is
// merged, and the other is reported value-not-carried, naming it where it is
// written, and merged into no resource.
func TestGlobalsPropertyTheTransformDoesNotReadIsNotMerged(t *testing.T) {
	t.Parallel()
	for _, property := range []string{"Events", "Policies", "Role"} {
		t.Run(property, func(t *testing.T) {
			t.Parallel()
			g, diags := readText(`AWSTemplateFormatVersion: "2010-09-09"
Transform: AWS::Serverless-2016-10-31
Globals:
  Function:
    Timeout: 5
    ` + property + `: {}
Resources:
  Fn:
    Type: AWS::Serverless::Function
    Properties:
      PackageType: Image
`)
			at := "/Globals/Function/" + property
			onlyDiagnostic(t, diags, aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "value-not-carried",
				Message: "the template template.yaml writes " + property + " under Globals for Function at " + at +
					", which the transform AWS::Serverless-2016-10-31 does not read from Globals for an " +
					"AWS::Serverless::Function and refuses when the stack deploys, so no resource takes it: write it " +
					"on each resource it is meant for",
				SourceRef: "template.yaml#" + at})
			want := []cloudimport.Prop{leafProp("PackageType", `"Image"`), leafProp("Timeout", "5")}
			if got := propsOf(t, node(t, g, "Fn")); !reflect.DeepEqual(got, want) {
				t.Errorf("Fn's properties read\n%#v\nwant\n%#v", got, want)
			}
		})
	}
}

// TestGlobalsOfATypeTheTransformDoesNotReadAreNotMerged writes Globals for a
// key naming no type SAM reads Globals for: they are reported
// value-not-carried, naming the key, and merged into no resource.
func TestGlobalsOfATypeTheTransformDoesNotReadAreNotMerged(t *testing.T) {
	t.Parallel()
	g, diags := readText(`AWSTemplateFormatVersion: "2010-09-09"
Transform: AWS::Serverless-2016-10-31
Globals:
  LayerVersion:
    RetentionPolicy: Retain
Resources:
  Layer:
    Type: AWS::Serverless::LayerVersion
    Properties:
      ContentUri: layer/
`)
	onlyDiagnostic(t, diags, aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "value-not-carried",
		Message: "the template template.yaml writes Globals for LayerVersion at /Globals/LayerVersion, which names no " +
			"resource type the transform AWS::Serverless-2016-10-31 reads Globals for and is refused when the stack " +
			"deploys, so no resource takes them: write them on each resource they are meant for",
		SourceRef: "template.yaml#/Globals/LayerVersion"})
	want := []cloudimport.Prop{leafProp("ContentUri", `"layer/"`)}
	if got := propsOf(t, node(t, g, "Layer")); !reflect.DeepEqual(got, want) {
		t.Errorf("Layer's properties read\n%#v\nwant\n%#v", got, want)
	}
}

// TestGlobalsWithoutTheServerlessTransformAreNamedInTheNotes reads a
// template declaring Globals and no transform: they are named in the notes
// and merged into no resource, and nothing is reported.
func TestGlobalsWithoutTheServerlessTransformAreNamedInTheNotes(t *testing.T) {
	t.Parallel()
	g, diags := readText(`AWSTemplateFormatVersion: "2010-09-09"
Globals:
  Function:
    Timeout: 5
Resources:
  Fn:
    Type: AWS::Serverless::Function
    Properties:
      PackageType: Image
`)
	noDiagnostics(t, diags)
	if want := []string{"The template declares Globals, which the transform AWS::Serverless-2016-10-31 alone reads, " +
		"and not that transform, so no resource takes them."}; !slices.Equal(g.Notes, want) {
		t.Errorf("the notes read %q, want %q", g.Notes, want)
	}
	want := []cloudimport.Prop{leafProp("PackageType", `"Image"`)}
	if got := propsOf(t, node(t, g, "Fn")); !reflect.DeepEqual(got, want) {
		t.Errorf("Fn's properties read\n%#v\nwant\n%#v", got, want)
	}
}

// TestServerlessFunctionKeepsItsOwnProperties reads two SAM functions
// writing, or taking from the Globals, the properties from which SAM writes
// what no Recipe deploying a function takes — AutoPublishAlias,
// DeploymentPreference, Policies, CodeUri and InlineCode —: each is kept in
// the function's body as written, and nothing is reported, as what the
// draft does with them depends on whether a row maps the function.
func TestServerlessFunctionKeepsItsOwnProperties(t *testing.T) {
	t.Parallel()
	g, diags := readSam(t, "properties")
	noDiagnostics(t, diags)
	want := map[string][]cloudimport.Prop{
		"Api": {
			leafProp("AutoPublishAlias", `"live"`),
			leafProp("CodeUri", `"src/"`),
			objectProp("DeploymentPreference", leafProp("Type", `"Canary10Percent5Minutes"`)),
			leafProp("Handler", `"app.handler"`),
			listProp("Policies", `"AWSLambdaBasicExecutionRole"`),
			leafProp("Runtime", `"python3.12"`),
		},
		"Hook": {
			leafProp("AutoPublishAlias", `"live"`),
			leafProp("Handler", `"index.handler"`),
			leafProp("InlineCode", `"def handler(event, context): return event"`),
			leafProp("Runtime", `"python3.12"`),
		},
	}
	for _, id := range []string{"Api", "Hook"} {
		if got := propsOf(t, node(t, g, id)); !reflect.DeepEqual(got, want[id]) {
			t.Errorf("%s's properties read\n%#v\nwant\n%#v", id, got, want[id])
		}
	}
}

// TestGlobalsLeaveNullPropertiesAsTheyAre reads a SAM function writing its
// Properties as null beside Globals for functions: the transform reads null
// as written, so the function takes none of the Globals, and nothing is
// reported.
func TestGlobalsLeaveNullPropertiesAsTheyAre(t *testing.T) {
	t.Parallel()
	g, diags := readText(`AWSTemplateFormatVersion: "2010-09-09"
Transform: AWS::Serverless-2016-10-31
Globals:
  Function:
    Timeout: 5
Resources:
  Fn:
    Type: AWS::Serverless::Function
    Properties: null
`)
	noDiagnostics(t, diags)
	want := []cloudimport.Prop{{Name: "Properties", Value: lit("null")}}
	if got := node(t, g, "Fn").Body; !reflect.DeepEqual(got, want) {
		t.Errorf("Fn's body reads\n%#v\nwant\n%#v", got, want)
	}
}

// TestGlobalsCapacityProviderRules merges the Globals of CapacityProvider by
// the transform's own rules there: the architectures nested in
// InstanceRequirements are the resource's list, replacing the global one,
// while a list beside them is the global items followed by the resource's;
// ManagedResourceTags keeps only the members the resource writes, where it
// writes any, each merged — so a member only the Globals write is dropped
// while the Tags map is merged, a global tag the resource does not write
// kept — and is taken whole from the Globals by a resource writing it empty
// or not at all.
func TestGlobalsCapacityProviderRules(t *testing.T) {
	t.Parallel()
	g, diags := readSam(t, "capacity")
	noDiagnostics(t, diags)
	globalTags := objectProp("ManagedResourceTags", objectProp("Labels", leafProp("Fleet", `"shared"`)),
		objectProp("Tags", leafProp("CostCenter", `"cc-0001"`), leafProp("Team", `"platform"`)))
	globalRequirements := objectProp("InstanceRequirements", listProp("Architectures", `"x86_64"`),
		listProp("ExcludedTypes", `"t2.micro"`))
	want := map[string][]cloudimport.Prop{
		"Tagged": {
			objectProp("InstanceRequirements", listProp("Architectures", `"arm64"`),
				listProp("ExcludedTypes", `"t2.micro"`, `"t3.nano"`)),
			objectProp("ManagedResourceTags", objectProp("Tags", leafProp("CostCenter", `"cc-1234"`),
				leafProp("Environment", `"production"`), leafProp("Team", `"platform"`))),
		},
		"EmptyTags": {globalRequirements, globalTags},
		"Untagged":  {globalRequirements, globalTags},
	}
	for _, id := range []string{"EmptyTags", "Tagged", "Untagged"} {
		if got := propsOf(t, node(t, g, id)); !reflect.DeepEqual(got, want[id]) {
			t.Errorf("%s's properties read\n%#v\nwant\n%#v", id, got, want[id])
		}
	}
}

// TestServerlessApplicationTemplateReadsItsOwnGlobalsAndEvents reads a SAM
// template deploying a serverless application whose template declares
// Globals and an event of its own: each template's Globals are merged into
// its own functions alone, and the application's event is read in its
// template, the dependency drawn between the nodes its resources are, each
// named with the application's logical ID before its own.
func TestServerlessApplicationTemplateReadsItsOwnGlobalsAndEvents(t *testing.T) {
	t.Parallel()
	main := `AWSTemplateFormatVersion: "2010-09-09"
Transform: AWS::Serverless-2016-10-31
Globals:
  Function:
    Timeout: 9
Resources:
  Orders:
    Type: AWS::Serverless::Application
    Properties:
      Location: orders.yaml
  Api:
    Type: AWS::Serverless::Function
    Properties:
      PackageType: Image
`
	orders := `Transform: AWS::Serverless-2016-10-31
Globals:
  Function:
    MemorySize: 256
Resources:
  Worker:
    Type: AWS::Serverless::Function
    Properties:
      PackageType: Image
      Events:
        FromQueue:
          Type: SQS
          Properties:
            Queue: !GetAtt Queue.Arn
  Queue:
    Type: AWS::SQS::Queue
`
	g, diags := readNested("", src("main.yaml", []byte(main)), src("orders.yaml", []byte(orders)))
	at := "/Resources/Worker/Properties/Events/FromQueue"
	want := []aggrega.Diagnostic{
		folded("main.yaml", "Orders", applicationType, "orders.yaml", "OrdersQueue, OrdersWorker"),
		{Severity: aggrega.SeverityWarn, Code: "resource-not-carried", Message: "the template orders.yaml writes the " +
			"event FromQueue of Worker (AWS::Serverless::Function) at " + at + ", an SQS event, for which SAM writes " +
			"an event source mapping polling the queue when the stack deploys, which no instance of the draft writes, " +
			"so the event is not carried; the draft connects OrdersWorker to OrdersQueue: write what invokes " +
			"OrdersWorker by hand where the stack deploys", SourceRef: "orders.yaml#" + at},
	}
	if !reflect.DeepEqual(diags, want) {
		t.Fatalf("the read reports\n%+v\nwant\n%+v", diags, want)
	}
	if got, want := nodeIDs(g), []string{"Api", "OrdersQueue", "OrdersWorker"}; !slices.Equal(got, want) {
		t.Fatalf("the graph holds %q, want %q", got, want)
	}
	wantProps := map[string][]cloudimport.Prop{
		"Api":          {leafProp("PackageType", `"Image"`), leafProp("Timeout", "9")},
		"OrdersWorker": {leafProp("MemorySize", "256"), leafProp("PackageType", `"Image"`)},
	}
	for _, id := range []string{"Api", "OrdersWorker"} {
		if got := propsOf(t, node(t, g, id)); !reflect.DeepEqual(got, wantProps[id]) {
			t.Errorf("%s's properties read\n%#v\nwant\n%#v", id, got, wantProps[id])
		}
	}
	if got, want := cloudimport.Edges(g), []cloudimport.Edge{depends("OrdersWorker", "OrdersQueue")}; !reflect.DeepEqual(
		got, want) {
		t.Errorf("the graph's edges read\n%+v\nwant\n%+v", got, want)
	}
}

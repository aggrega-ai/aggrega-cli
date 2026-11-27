// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	cfndraft "github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft/cloudformation"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// eventNotCarried is the resource-not-carried warning naming the event of
// Worker, an AWS::Serverless::Function of template.yaml, that writes, with the
// edge the draft draws for it, empty for none, and the fix it says.
func eventNotCarried(event, writes, edge, fix string) aggrega.Diagnostic {
	at := "/Resources/Worker/Properties/Events/" + event
	if edge != "" {
		edge = "; the draft connects " + edge
	}
	return aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "resource-not-carried", Message: "the template " +
		"template.yaml writes the event " + event + " of Worker (AWS::Serverless::Function) at " + at + ", " + writes +
		" when the stack deploys, which no instance of the draft writes, so the event is not carried" + edge + ": " +
		fix, SourceRef: "template.yaml#" + at}
}

// dependsEdges returns the dependencies among the edges of g, in order.
func dependsEdges(g *cloudimport.Graph) []cloudimport.Edge {
	var out []cloudimport.Edge
	for _, e := range cloudimport.Edges(g) {
		if e.Kind == cloudimport.EdgeDepends {
			out = append(out, e)
		}
	}
	return out
}

// depends is the dependency of the node from on the node to.
func depends(from, to string) cloudimport.Edge {
	return cloudimport.Edge{From: from, To: to, Kind: cloudimport.EdgeDepends}
}

// TestSamEventsBecomeEdges reads a SAM function writing an event of each type
// SAM reads: an event reading a resource of the template — an SQS queue by
// !GetAtt, a Kinesis stream, a DynamoDB table's stream, a bucket by !Ref, a
// topic, a custom event bus — is a dependency of the function on that
// resource; an Api or HttpApi event naming the API it routes through by its
// RestApiId or ApiId is a dependency of that API on the function; every event
// is reported resource-not-carried, naming it, and left out of the function's
// body, as no instance of the draft writes what SAM writes for it — an event
// naming no API saying that SAM creates one, and a schedule, an EventBridge
// rule on the default bus, a log group's subscription, a Cognito trigger, an
// IoT rule and an Alexa skill drawing no edge.
func TestSamEventsBecomeEdges(t *testing.T) {
	t.Parallel()
	g, diags := readSam(t, "events-edges")
	invoke := "write what invokes Worker by hand where the stack deploys"
	route := "write the route, and the permission letting the API invoke Worker, in the API's definition by hand"
	want := []aggrega.Diagnostic{
		eventNotCarried("CustomBus", "an EventBridgeRule event, for which SAM writes an EventBridge rule targeting it",
			"Worker to Bus", invoke),
		eventNotCarried("DefaultBus", "an EventBridgeRule event, for which SAM writes an EventBridge rule targeting it",
			"", invoke),
		eventNotCarried("FromBucket", "an S3 event, for which SAM writes a notification into the bucket and the "+
			"permission letting the bucket invoke it", "Worker to Bucket", invoke),
		eventNotCarried("FromQueue", "an SQS event, for which SAM writes an event source mapping polling the queue",
			"Worker to Queue", invoke),
		eventNotCarried("FromStream", "a Kinesis event, for which SAM writes an event source mapping reading the "+
			"stream", "Worker to Stream", invoke),
		eventNotCarried("FromTable", "a DynamoDB event, for which SAM writes an event source mapping reading the "+
			"table's stream", "Worker to Table", invoke),
		eventNotCarried("FromTopic", "an SNS event, for which SAM writes the topic's subscription and the permission "+
			"letting the topic invoke it", "Worker to Topic", invoke),
		eventNotCarried("Hourly", "a ScheduleV2 event, for which SAM writes an EventBridge Scheduler schedule "+
			"invoking it", "", invoke),
		eventNotCarried("Http", "an HttpApi event, for which SAM writes its route into the API and the permission "+
			"letting the API invoke it", "HttpApi to Worker", route),
		eventNotCarried("ImplicitHttp", "an HttpApi event naming no ApiId, for which SAM creates an HTTP API of its "+
			"own, ServerlessHttpApi, with the event's route", "", "declare the API as a resource of the template, "+
			"name it in ApiId, and write the route in its definition"),
		eventNotCarried("ImplicitRest", "an Api event naming no RestApiId, for which SAM creates an API of its own, "+
			"ServerlessRestApi, with the event's route", "", "declare the API as a resource of the template, name "+
			"it in RestApiId, and write the route in its definition"),
		eventNotCarried("Logs", "a CloudWatchLogs event, for which SAM writes a subscription filter on the log group",
			"", invoke),
		eventNotCarried("Nightly", "a Schedule event, for which SAM writes an EventBridge rule invoking it on a "+
			"schedule", "", invoke),
		eventNotCarried("Rest", "an Api event, for which SAM writes its route into the API and the permission "+
			"letting the API invoke it", "RestApi to Worker", route),
		eventNotCarried("SignUp", "a Cognito event, for which SAM writes it into the user pool's triggers", "", invoke),
		eventNotCarried("Skill", "an AlexaSkill event, for which SAM writes the permission letting an Alexa skill "+
			"invoke it", "", invoke),
		eventNotCarried("Things", "an IoTRule event, for which SAM writes an AWS IoT topic rule invoking it", "",
			invoke),
	}
	if !reflect.DeepEqual(diags, want) {
		t.Fatalf("the read reports\n%+v\nwant\n%+v", diags, want)
	}
	wantEdges := []cloudimport.Edge{depends("HttpApi", "Worker"), depends("RestApi", "Worker"),
		depends("Worker", "Bus"), depends("Worker", "Bucket"), depends("Worker", "Queue"), depends("Worker", "Stream"),
		depends("Worker", "Table"), depends("Worker", "Topic")}
	if got := cloudimport.Edges(g); !reflect.DeepEqual(got, wantEdges) {
		t.Errorf("the graph's edges read\n%+v\nwant\n%+v", got, wantEdges)
	}
	wantProps := []cloudimport.Prop{leafProp("ImageUri", `"123456789012.dkr.ecr.eu-west-1.amazonaws.com/worker:1.0"`),
		leafProp("PackageType", `"Image"`)}
	if got := propsOf(t, node(t, g, "Worker")); !reflect.DeepEqual(got, wantProps) {
		t.Errorf("Worker's properties read\n%#v\nwant\n%#v", got, wantProps)
	}
}

// TestServerlessConnectorBecomesAnEdge reads a SAM connector written as a
// resource, naming its source by Source.Id and its destinations as a list of
// Ids, and one written on a resource under its Connectors, naming its
// destination by Destination.Id: each is a dependency of its source on each
// destination, nothing is drafted for it — the resource is no node, a
// dependency on it is dropped, and the Connectors are left out of the body
// they are written on — and each is reported resource-not-carried, naming its
// permissions, which SAM writes into IAM policies.
func TestServerlessConnectorBecomesAnEdge(t *testing.T) {
	t.Parallel()
	g, diags := readSam(t, "connectors")
	grant := ", whose permissions SAM writes into IAM policies when the stack deploys, which the draft does not " +
		"carry, and nothing is drafted for it; the draft connects "
	fix := ": grant the permissions in the policies the operator supplies"
	want := []aggrega.Diagnostic{
		{Severity: aggrega.SeverityWarn, Code: "resource-not-carried", Message: "the template template.yaml writes the " +
			"connector ReaderConnector (AWS::Serverless::Connector) at /Resources/ReaderConnector" + grant +
			"Reader to Table, Queue" + fix, SourceRef: "template.yaml#/Resources/ReaderConnector"},
		{Severity: aggrega.SeverityWarn, Code: "resource-not-carried", Message: "the template template.yaml writes the " +
			"connector ToTable of Writer at /Resources/Writer/Connectors/ToTable" + grant + "Writer to Table" + fix,
			SourceRef: "template.yaml#/Resources/Writer/Connectors/ToTable"},
	}
	if !reflect.DeepEqual(diags, want) {
		t.Fatalf("the read reports\n%+v\nwant\n%+v", diags, want)
	}
	if want := []string{"Queue", "Reader", "Table", "Writer"}; !slices.Equal(nodeIDs(g), want) {
		t.Errorf("the nodes read %v, want %v", nodeIDs(g), want)
	}
	wantEdges := []cloudimport.Edge{depends("Reader", "Table"), depends("Reader", "Queue"), depends("Writer", "Table")}
	if got := cloudimport.Edges(g); !reflect.DeepEqual(got, wantEdges) {
		t.Errorf("the graph's edges read\n%+v\nwant\n%+v", got, wantEdges)
	}
	for _, p := range node(t, g, "Writer").Body {
		if p.Name != "Properties" {
			t.Errorf("Writer's body holds %s", p.Name)
		}
	}
}

// TestServerlessConnectorNamingNoResourceDrawsNoEdge reads a connector naming
// its destination by its ARN: nothing is drafted for it and no edge is drawn,
// and the warning naming it says so.
func TestServerlessConnectorNamingNoResourceDrawsNoEdge(t *testing.T) {
	t.Parallel()
	g, diags := readText(`AWSTemplateFormatVersion: "2010-09-09"
Transform: AWS::Serverless-2016-10-31
Resources:
  Fn:
    Type: AWS::Serverless::Function
    Properties:
      PackageType: Image
  Link:
    Type: AWS::Serverless::Connector
    Properties:
      Source: {Id: Fn}
      Destination: {Arn: 'arn:aws:sqs:eu-west-1:123456789012:orders', Type: AWS::SQS::Queue}
      Permissions: [Write]
`)
	onlyDiagnostic(t, diags, aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "resource-not-carried",
		Message: "the template template.yaml writes the connector Link (AWS::Serverless::Connector) at /Resources/Link, " +
			"whose permissions SAM writes into IAM policies when the stack deploys, which the draft does not carry, " +
			"and nothing is drafted for it; the draft connects nothing for it, as it names no resource of the " +
			"template as both its source and a destination: grant the permissions in the policies the operator " +
			"supplies",
		SourceRef: "template.yaml#/Resources/Link"})
	if want := []string{"Fn"}; !slices.Equal(nodeIDs(g), want) {
		t.Errorf("the nodes read %v, want %v", nodeIDs(g), want)
	}
	if got := cloudimport.Edges(g); len(got) > 0 {
		t.Errorf("the graph's edges read %+v, want none", got)
	}
}

// drafted is a SAM function and a SAM state machine no row maps, each
// writing an event.
const drafted = `AWSTemplateFormatVersion: "2010-09-09"
Transform: AWS::Serverless-2016-10-31
Resources:
  Worker:
    Type: AWS::Serverless::Function
    Properties:
      PackageType: Image
      ImageUri: 123456789012.dkr.ecr.eu-west-1.amazonaws.com/worker:1.0
      Events:
        FromQueue:
          Type: SQS
          Properties:
            Queue: !GetAtt Queue.Arn
  Flow:
    Type: AWS::Serverless::StateMachine
    Properties:
      DefinitionUri: flow.asl.json
      Events:
        Nightly:
          Type: Schedule
          Properties:
            Schedule: rate(1 day)
  Queue:
    Type: AWS::SQS::Queue
`

// TestDraftedServerlessResourceEventsAreNotCarried imports a SAM function
// and a SAM state machine no row maps through the core: each is drafted from
// the source with no Events in its drafted Recipe or its Release entry, each
// event is reported resource-not-carried, and the function's instance uses
// the queue its event reads.
func TestDraftedServerlessResourceEventsAreNotCarried(t *testing.T) {
	t.Parallel()
	f := cloudimport.Format{Name: formatName, Target: target, Syntax: "yaml", Read: Read, Key: Key,
		Draft: cfndraft.Format}
	out := cloudimport.Import(aggrega.ImportInput{Format: formatName, Target: target,
		Sources: []aggrega.ImportSource{src("template.yaml", []byte(drafted))}}, f, parse)
	var notCarried []string
	for _, d := range out.Diagnostics {
		if d.Severity == aggrega.SeverityHalt {
			t.Fatalf("the import halts: %+v", d)
		}
		if d.Code == "resource-not-carried" {
			notCarried = append(notCarried, d.SourceRef)
		}
	}
	if want := []string{"template.yaml#/Resources/Flow/Properties/Events/Nightly",
		"template.yaml#/Resources/Worker/Properties/Events/FromQueue"}; !slices.Equal(notCarried, want) {
		t.Errorf("the import reports resource-not-carried at %q, want %q", notCarried, want)
	}
	files := map[string]string{}
	for _, file := range out.Files {
		files[file.Path] = string(file.Content)
	}
	for _, path := range []string{"catalog/recipe/cloudformation/aws-serverless-function/0.1.0/recipe.yaml",
		"catalog/recipe/cloudformation/aws-serverless-statemachine/0.1.0/recipe.yaml", "release.yaml"} {
		content, drafted := files[path]
		switch {
		case !drafted:
			t.Errorf("the import writes no %s", path)
		case strings.Contains(content, "Events"):
			t.Errorf("%s holds Events:\n%s", path, content)
		}
	}
	worker := "    worker:\n      type: imported/component/aws-serverless-function\n      uses:\n        queue:\n" +
		"          queue:\n            target: queue\n"
	if !strings.Contains(files["aggregate.yaml"], worker) {
		t.Errorf("the Aggregate holds no\n%s\nin\n%s", worker, files["aggregate.yaml"])
	}
}

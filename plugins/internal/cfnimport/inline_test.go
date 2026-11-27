// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// readOK reads and lowers the template text handed alone, failing where
// nothing is read.
func readOK(t *testing.T, text string) (*cloudimport.Graph, []aggrega.Diagnostic) {
	t.Helper()
	g, diags := readText(text)
	if g == nil {
		t.Fatalf("the template is not read: %+v", diags)
	}
	return g, diags
}

// holdsNode reports whether g holds a node of the ID id.
func holdsNode(g *cloudimport.Graph, id string) bool {
	return slices.ContainsFunc(g.Nodes, func(n *cloudimport.Node) bool { return n.ID == id })
}

// sharedTask is two Amazon ECS services running one task definition.
const sharedTask = `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Shared:
    Type: AWS::ECS::TaskDefinition
    DependsOn: Logs
    Properties:
      Family: shared
      ContainerDefinitions:
        - Name: app
          Image: registry.example/app:1.0.0
          PortMappings:
            - ContainerPort: 8080
  Logs:
    Type: AWS::Logs::LogGroup
  Api:
    Type: AWS::ECS::Service
    Properties:
      DesiredCount: 2
      TaskDefinition: !Ref Shared
  Admin:
    Type: AWS::ECS::Service
    Properties:
      TaskDefinition: !Ref Shared
`

// TestInlinedChildIsReadInPlaceInEachParent reads the task definition two
// services reference in place of each reference, each service holding a copy
// of its own and taking its dependencies, and keeping its body as written:
// the task definition is folded into the first service, drafting nothing, a
// resource-folded record names both services, and the decision on each lists
// it.
func TestInlinedChildIsReadInPlaceInEachParent(t *testing.T) {
	t.Parallel()
	g, _ := readOK(t, sharedTask)
	if got := node(t, g, "Shared").ReadIn; !slices.Equal(got, []string{"Admin", "Api"}) {
		t.Errorf("the task definition is read in %q, want Admin and Api", got)
	}
	for _, id := range []string{"Admin", "Api"} {
		n := node(t, g, id)
		if got := valueAt(t, n.Body, "Properties", "TaskDefinition", "Family"); !reflect.DeepEqual(got, lit(`"shared"`)) {
			t.Errorf("%s reads Family %#v, want the task definition's", id, got)
		}
		if got := valueAt(t, n.AsWritten, "Properties", "TaskDefinition"); !reflect.DeepEqual(got,
			cloudimport.Ref{Node: "Shared"}) {
			t.Errorf("%s keeps %#v as written, want its reference to Shared", id, got)
		}
		if !slices.Equal(n.DependsOn, []string{"Logs"}) {
			t.Errorf("%s depends on %q, want the task definition's dependency Logs", id, n.DependsOn)
		}
	}
	api, admin := node(t, g, "Api"), node(t, g, "Admin")
	defs := propAt(api.Body, []string{"Properties", "TaskDefinition", "ContainerDefinitions"})
	defs.Items[0].Props[0].Value = lit(`"changed"`)
	if name, _ := textAt(propAt(admin.Body, []string{"Properties", "TaskDefinition", "ContainerDefinitions",
		"0"}).Props, "Name"); name != "app" {
		t.Errorf("the services share one copy of the task definition: Admin's container is named %q", name)
	}

	out := importText(t, sharedTask)
	for _, id := range []string{"Admin", "Api"} {
		holds(t, "the decision on "+id, decisionOn(t, out, id).Rationale, "it references Shared "+
			"(AWS::ECS::TaskDefinition) at Properties.TaskDefinition, whose properties are read there in place of "+
			"the reference")
	}
	holds(t, "the decision on Shared", decisionOn(t, out, "Shared").Rationale, "folded: read in place of the "+
		"reference to it in the body of Admin and Api")
	folded := coded(out, cloudimport.CodeResourceFolded)
	if len(folded) != 1 || folded[0].Message != "Shared (AWS::ECS::TaskDefinition) is read in place of the "+
		"reference to it by Admin and Api, so nothing is drafted for it" {
		t.Errorf("the task definition is reported %+v", folded)
	}
	if strings.Contains(fileText(t, out, "aggregate.yaml"), "shared:") {
		t.Errorf("the task definition is an instance of its own")
	}
}

// TestDraftedParentKeepsItsBodyAsWritten drafts a cache cluster whose engine a
// parameter with no default gives from its body as written, its parameter
// group read by reference, and drafts the parameter group, a node of its own.
func TestDraftedParentKeepsItsBodyAsWritten(t *testing.T) {
	t.Parallel()
	out := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  Engine:
    Type: String
Resources:
  CacheParams:
    Type: AWS::ElastiCache::ParameterGroup
    Properties:
      CacheParameterGroupFamily: redis7
      Description: cache parameters
  Cache:
    Type: AWS::ElastiCache::CacheCluster
    Properties:
      Engine: !Ref Engine
      CacheNodeType: cache.t3.micro
      NumCacheNodes: 1
      CacheParameterGroupName: !Ref CacheParams
`)
	for _, id := range []string{"Cache", "CacheParams"} {
		if d := decisionOn(t, out, id); d.Chosen == nil || d.Chosen.Mode != "draft" {
			t.Errorf("%s is decided %+v, want drafted from the source", id, d.Chosen)
		}
	}
	lacks(t, "the decision on Cache", decisionOn(t, out, "Cache").Rationale, "in place of the reference")
	holds(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), "    cache:\n      type: "+
		"imported/datastore/aws-elasticache-cachecluster\n", "target: cache-params")
	holds(t, "the Release", fileText(t, out, "release.yaml"), "    cache-params:\n      infra:\n"+
		"        overrides:\n          CacheParameterGroupFamily: \"redis7\"\n")
	lacks(t, "the drafted cache cluster's Recipe", fileText(t, out,
		"catalog/recipe/cloudformation/aws-elasticache-cachecluster/0.1.0/recipe.yaml"), "CacheParameterGroupFamily")
}

// TestInlinedChildAnotherResourceReadsStaysANode reads a task definition a
// service references in place of the reference and keeps it a node of its
// own, as a schedule starts runs of it too: the schedule folds into it, an
// instance of the task.
func TestInlinedChildAnotherResourceReadsStaysANode(t *testing.T) {
	t.Parallel()
	text := `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Report:
    Type: AWS::ECS::TaskDefinition
    Properties:
      ContainerDefinitions:
        - Name: report
          Image: registry.example/report:1.0.0
  Api:
    Type: AWS::ECS::Service
    Properties:
      TaskDefinition: !Ref Report
  Nightly:
    Type: AWS::Scheduler::Schedule
    Properties:
      ScheduleExpression: cron(0 2 * * ? *)
      ScheduleExpressionTimezone: Europe/Rome
      Target:
        Arn: arn:aws:ecs:eu-south-1:123456789012:cluster/main
        RoleArn: arn:aws:iam::123456789012:role/scheduler
        EcsParameters:
          TaskDefinitionArn: !Ref Report
`
	g, _ := readOK(t, text)
	if report := node(t, g, "Report"); report.ReadIn != nil || node(t, g, "Nightly").Parent != "Report" {
		t.Fatalf("the task definition is read in %q, or the schedule's parent is %q", report.ReadIn,
			node(t, g, "Nightly").Parent)
	}
	out := importText(t, text)
	holds(t, "the decision on Api", decisionOn(t, out, "Api").Rationale, "Report staying a resource of its own, "+
		"as another resource reads it too")
	if d := decisionOn(t, out, "Report"); d.Chosen == nil || d.Chosen.Target != "core/compute/task" {
		t.Errorf("the task definition is decided %+v, want the task Component", d)
	}
	holds(t, "the decision on Nightly", decisionOn(t, out, "Nightly").Rationale, "folded: written into the "+
		"instance of Report")
	holds(t, "the Release", fileText(t, out, "release.yaml"), `ScheduleExpressionTimezone: "Europe/Rome"`,
		`RoleArn: "arn:aws:iam::123456789012:role/scheduler"`)
}

// TestParentLinkReadsAChildAsPartOfTheOneResourceItNames sets the parent of a
// child whose link names one resource of the template, and leaves one whose
// link names several, or none the template declares, a node of its own, its
// decision saying why.
func TestParentLinkReadsAChildAsPartOfTheOneResourceItNames(t *testing.T) {
	t.Parallel()
	text := `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Orders:
    Type: AWS::SQS::Queue
  Refunds:
    Type: AWS::SQS::Queue
  OrdersPolicy:
    Type: AWS::SQS::QueuePolicy
    Properties:
      Queues: [!Ref Orders]
      PolicyDocument: {}
  BothPolicy:
    Type: AWS::SQS::QueuePolicy
    Properties:
      Queues: [!Ref Orders, !Ref Refunds]
      PolicyDocument: {}
  OutsidePolicy:
    Type: AWS::S3::BucketPolicy
    Properties:
      Bucket: shared-bucket
      PolicyDocument: {}
`
	g, _ := readOK(t, text)
	if got := node(t, g, "OrdersPolicy").Parent; got != "Orders" {
		t.Errorf("the policy of one queue is read as part of %q, want Orders", got)
	}
	for id, says := range map[string]string{
		"BothPolicy":    "its Properties.Queues.* names Orders and Refunds, so it is read as part of none of them",
		"OutsidePolicy": "its Properties.Bucket names no resource the template declares, so it is read as part of none",
	} {
		n := node(t, g, id)
		if n.Parent != "" || !slices.Equal(n.Says, []string{says}) {
			t.Errorf("%s is read as part of %q, saying %q, want none, saying %q", id, n.Parent, n.Says, says)
		}
	}
	out := importText(t, text)
	holds(t, "the decision on BothPolicy", decisionOn(t, out, "BothPolicy").Rationale,
		"its Properties.Queues.* names Orders and Refunds, so it is read as part of none of them")
	holds(t, "the decision on OrdersPolicy", decisionOn(t, out, "OrdersPolicy").Rationale,
		"not carried: the fold of AWS::SQS::QueuePolicy in the row of AWS::SQS::Queue")
}

// TestMainContainerIsFirst reads, of the containers of a task definition, the
// one a service's load balancer names, else the first essential one, else the
// first, alone, and reports each other not carried at its index in the
// template; the task definition keeps its containers as written.
func TestMainContainerIsFirst(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		case_, balancer, essential, main string
		others, at                       []string
	}{
		{"named by the load balancer", "api", "true", "api", []string{"sidecar", "agent"}, []string{"0", "2"}},
		{"the first essential", "", "true", "api", []string{"sidecar", "agent"}, []string{"0", "2"}},
		{"the first", "", "false", "sidecar", []string{"api", "agent"}, []string{"1", "2"}},
	} {
		t.Run(c.case_, func(t *testing.T) {
			lb := ""
			if c.balancer != "" {
				lb = "      LoadBalancers:\n        - ContainerName: " + c.balancer + "\n          ContainerPort: 80\n"
			}
			text := `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Api:
    Type: AWS::ECS::Service
    Properties:
      TaskDefinition: !Ref Task
` + lb + `  Task:
    Type: AWS::ECS::TaskDefinition
    Properties:
      ContainerDefinitions:
        - Name: sidecar
          Image: registry.example/sidecar:1
          Essential: false
        - Name: api
          Image: registry.example/api:1
          Essential: ` + c.essential + `
        - Name: agent
          Image: registry.example/agent:1
`
			if c.balancer != "" {
				text = strings.Replace(text, "        - Name: sidecar\n          Image: registry.example/sidecar:1\n"+
					"          Essential: false\n", "        - Name: sidecar\n          Image: registry.example/sidecar:1\n"+
					"          Essential: true\n", 1)
			}
			g, _ := readOK(t, text)
			names := func(body []cloudimport.Prop, path ...string) []string {
				var out []string
				for _, d := range propAt(body, path).Items {
					name, _ := textAt(d.Props, "Name")
					out = append(out, name)
				}
				return out
			}
			got := names(node(t, g, "Api").Body, "Properties", "TaskDefinition", "ContainerDefinitions")
			if !slices.Equal(got, []string{c.main}) {
				t.Errorf("the service reads the containers %q, want %s alone", got, c.main)
			}
			got = names(node(t, g, "Task").AsWritten, "Properties", "ContainerDefinitions")
			if !slices.Equal(got, []string{"sidecar", "api", "agent"}) {
				t.Errorf("the task definition keeps the containers %q as written", got)
			}
			out := importText(t, text)
			var notCarried []string
			for _, d := range coded(out, cloudimport.CodeResourceNotCarried) {
				notCarried = append(notCarried, d.SourceRef)
			}
			const at = "template.yaml#/Resources/Task/Properties/ContainerDefinitions/"
			if want := []string{at + c.at[0], at + c.at[1]}; !slices.Equal(notCarried, want) {
				t.Errorf("the containers not carried are reported at %q, want %q", notCarried, want)
			}
			holds(t, "the decision on Api", decisionOn(t, out, "Api").Rationale,
				"its main container is "+c.main+", which the instance runs, and "+c.others[0]+" and "+c.others[1]+
					" are not carried")
			lacks(t, "the Release", fileText(t, out, "release.yaml"), "ContainerDefinitions.1")
		})
	}
}

// TestParameterGroupIsInlinedNowhere drafts from the source the parameter
// group an RDS instance and an Aurora cluster name, each a resource of its
// own, and leaves the name each reads of it for the operator.
func TestParameterGroupIsInlinedNowhere(t *testing.T) {
	t.Parallel()
	text := `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  DbParams:
    Type: AWS::RDS::DBParameterGroup
    Properties:
      Family: postgres16
      Description: instance parameters
  ClusterParams:
    Type: AWS::RDS::DBClusterParameterGroup
    Properties:
      Family: aurora-postgresql16
      Description: cluster parameters
  Db:
    Type: AWS::RDS::DBInstance
    Properties:
      Engine: postgres
      DBParameterGroupName: !Ref DbParams
  Cluster:
    Type: AWS::RDS::DBCluster
    Properties:
      Engine: aurora-postgresql
      DBClusterParameterGroupName: !Ref ClusterParams
`
	out := importText(t, text)
	for _, id := range []string{"DbParams", "ClusterParams"} {
		d := decisionOn(t, out, id)
		if d.Chosen == nil || d.Chosen.Mode != "draft" {
			t.Errorf("%s is decided %+v, want drafted from the source", id, d)
		}
		holds(t, "the decision on "+id, d.Rationale, "the RDS and Aurora Recipes take a parameter group by name "+
			"under infra.overrides and create none")
	}
	var left []string
	for _, d := range coded(out, cloudimport.CodeValueNotCarried) {
		left = append(left, d.Message)
	}
	for _, want := range []string{"Db writes Properties.DBParameterGroupName, which the instance db takes as " +
		"infra.overrides.DBParameterGroupName", "Cluster writes Properties.DBClusterParameterGroupName, which the " +
		"instance cluster takes as infra.overrides.DBClusterParameterGroupName"} {
		if !slices.ContainsFunc(left, func(m string) bool { return strings.HasPrefix(m, want) }) {
			t.Errorf("no value-not-carried leaves %q for the operator: %q", want, left)
		}
	}
}

// TestEventSourceMappingConnectsTheFunctionToItsSource folds an event source
// mapping into the function it names, not carried, and draws the function's
// dependency on the resource whose records invoke it.
func TestEventSourceMappingConnectsTheFunctionToItsSource(t *testing.T) {
	t.Parallel()
	text := `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Orders:
    Type: AWS::SQS::Queue
  Handler:
    Type: AWS::Lambda::Function
    Properties:
      PackageType: Image
      Code: {ImageUri: 123456789012.dkr.ecr.us-east-1.amazonaws.com/handler:1}
  OrdersMapping:
    Type: AWS::Lambda::EventSourceMapping
    Properties:
      FunctionName: !Ref Handler
      EventSourceArn: !GetAtt Orders.Arn
`
	g, _ := readOK(t, text)
	if got := node(t, g, "Handler").DependsOn; !slices.Equal(got, []string{"Orders"}) {
		t.Errorf("the function depends on %q, want the queue", got)
	}
	out := importText(t, text)
	holds(t, "the decision on OrdersMapping", decisionOn(t, out, "OrdersMapping").Rationale,
		"not carried: the fold of AWS::Lambda::EventSourceMapping in the row of AWS::Lambda::Function")
	holds(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), "    handler:\n      type: core/compute/function\n"+
		"      values:\n        # Left for the author, since the source does not give it: values.runtime, which its "+
		"Component core/compute/function requires.\n        runtime: \"\"\n"+
		"      uses:\n        orders:\n          orders:\n            target: orders\n")
}

// TestTopicSubscriptionConnectsTheEndpointToTheTopic folds a subscription
// into its topic, not carried, and draws its endpoint's dependency on the
// topic.
func TestTopicSubscriptionConnectsTheEndpointToTheTopic(t *testing.T) {
	t.Parallel()
	text := `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Events:
    Type: AWS::SNS::Topic
  Inbox:
    Type: AWS::SQS::Queue
  InboxSubscription:
    Type: AWS::SNS::Subscription
    Properties:
      TopicArn: !Ref Events
      Protocol: sqs
      Endpoint: !GetAtt Inbox.Arn
`
	g, _ := readOK(t, text)
	if got := node(t, g, "Inbox").DependsOn; !slices.Equal(got, []string{"Events"}) {
		t.Errorf("the queue depends on %q, want the topic", got)
	}
	out := importText(t, text)
	holds(t, "the decision on InboxSubscription", decisionOn(t, out, "InboxSubscription").Rationale,
		"not carried: the fold of AWS::SNS::Subscription in the row of AWS::SNS::Topic")
	holds(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), "    inbox:\n      type: core/messaging/sqs\n"+
		"      uses:\n        events:\n")
}

// onlyCodeOf returns the diagnostics of diags of code.
func onlyCodeOf(t *testing.T, diags []aggrega.Diagnostic, code cloudimport.Code) []aggrega.Diagnostic {
	t.Helper()
	var out []aggrega.Diagnostic
	for _, d := range diags {
		if d.Code == string(code) {
			out = append(out, d)
		}
	}
	return out
}

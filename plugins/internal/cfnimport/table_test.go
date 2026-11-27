// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// mappedTo fails unless the decision on id references component.
func mappedTo(t *testing.T, out aggrega.ImportOutput, id, component string) aggrega.ImportDecision {
	t.Helper()
	d := decisionOn(t, out, id)
	if d.Chosen == nil || d.Chosen.Target != component || d.Chosen.Mode != "reference" {
		t.Errorf("%s is decided %+v, want a reference to %s", id, d.Chosen, component)
	}
	return d
}

// rowOf returns the row of the merged table keyed key whose Recipe is recipe,
// failing where there is none.
func rowOf(t *testing.T, key, recipe string) cloudimport.Row {
	t.Helper()
	rows, err := Table().Rows()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Key == key && r.Recipe == recipe {
			return r
		}
	}
	t.Fatalf("the table holds no row of %s by %s", key, recipe)
	return cloudimport.Row{}
}

// TestRdsEngineMatchesWhateverItsCase maps an RDS instance whose engine the
// template writes in any case to the Component of that engine.
func TestRdsEngineMatchesWhateverItsCase(t *testing.T) {
	t.Parallel()
	out := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Mixed:
    Type: AWS::RDS::DBInstance
    Properties: {Engine: MySQL}
  Upper:
    Type: AWS::RDS::DBInstance
    Properties: {Engine: Postgres}
  Lower:
    Type: AWS::RDS::DBInstance
    Properties: {Engine: postgres}
`)
	mappedTo(t, out, "Mixed", "core/datastore/mysql")
	mappedTo(t, out, "Upper", "core/datastore/postgres")
	mappedTo(t, out, "Lower", "core/datastore/postgres")
}

// TestRdsEngineFromAParameter maps an RDS instance whose engine a template
// parameter gives by the parameter's default, and drafts one whose parameter
// has no default from the source, reporting the discriminator unresolved.
func TestRdsEngineFromAParameter(t *testing.T) {
	t.Parallel()
	const template = `AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  Engine:
    Type: String%s
Resources:
  Db:
    Type: AWS::RDS::DBInstance
    Properties: {Engine: !Ref Engine}
`
	out := importText(t, strings.Replace(template, "%s", "\n    Default: mariadb", 1))
	mappedTo(t, out, "Db", "core/datastore/mariadb")
	if n := len(coded(out, cloudimport.CodeDiscriminatorUnresolved)); n != 0 {
		t.Errorf("the engine with a default is reported unresolved %d times", n)
	}

	out = importText(t, strings.Replace(template, "%s", "", 1))
	if d := decisionOn(t, out, "Db"); d.Chosen == nil || d.Chosen.Mode != "draft" {
		t.Errorf("the instance whose engine has no default is decided %+v, want drafted", d.Chosen)
	}
	unresolved := coded(out, cloudimport.CodeDiscriminatorUnresolved)
	if len(unresolved) != 1 || !strings.HasPrefix(unresolved[0].Message, "Db (AWS::RDS::DBInstance): which row "+
		"maps it depends on a value the source sets only when it deploys") {
		t.Errorf("the engine with no default is reported %+v", unresolved)
	}
}

// TestRdsMasterPasswordIsNamedNeverAttached names, in the decision on a
// mapped RDS instance, the master password it writes, which is never
// attached, and ManageMasterUserPassword, the Recipe's own; nothing is
// attached at the instance's slot, and no value is written.
func TestRdsMasterPasswordIsNamedNeverAttached(t *testing.T) {
	t.Parallel()
	text := `AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  MasterPassword:
    Type: String
    NoEcho: true
    Default: never-written-default
Resources:
  Written:
    Type: AWS::RDS::DBInstance
    Properties:
      Engine: postgres
      MasterUsername: app
      MasterUserPassword: !Ref MasterPassword
  Managed:
    Type: AWS::RDS::DBInstance
    Properties:
      Engine: postgres
      MasterUsername: app
      ManageMasterUserPassword: true
`
	out := importText(t, text)
	holds(t, "the decision on Written", mappedTo(t, out, "Written", "core/datastore/postgres").Rationale,
		"its master password, written at Properties.MasterUserPassword, is never attached: the Recipe has RDS "+
			"generate the master password and keep it in AWS Secrets Manager")
	holds(t, "the decision on Managed", mappedTo(t, out, "Managed", "core/datastore/postgres").Rationale,
		"it writes Properties.ManageMasterUserPassword, which the Recipe writes true itself")
	for _, path := range []string{"aggregate.yaml", "release.yaml"} {
		lacks(t, path, fileText(t, out, path), "features:", "never-written-default")
	}
}

// TestRdsStorageIsPlacedInGibibytes places an RDS instance's allocated
// storage as infra.storage.size, in gibibytes, and the engine of SQL Server
// of any edition maps to its Recipe, which no other engine reaches.
func TestRdsStorageIsPlacedInGibibytes(t *testing.T) {
	t.Parallel()
	out := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Ledger:
    Type: AWS::RDS::DBInstance
    Properties: {Engine: sqlserver-se, AllocatedStorage: 100}
  Archive:
    Type: AWS::RDS::DBInstance
    Properties: {Engine: oracle-ee}
`)
	mappedTo(t, out, "Ledger", "core/datastore/sql-server")
	if d := decisionOn(t, out, "Archive"); d.Chosen == nil || d.Chosen.Mode != "draft" {
		t.Errorf("an Oracle instance is decided %+v, want drafted from the source", d.Chosen)
	}
	holds(t, "the Release", fileText(t, out, "release.yaml"), "    ledger:\n      infra:\n        storage:\n"+
		"          size: \"100Gi\"\n")
}

// TestRedisCacheClusterMapsToTheReplicationGroupRecipe maps a cache cluster
// running Redis to the Recipe deploying a Redis replication group, its
// decision saying so, and a cache cluster running Memcached to its own.
func TestRedisCacheClusterMapsToTheReplicationGroupRecipe(t *testing.T) {
	t.Parallel()
	out := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Sessions:
    Type: AWS::ElastiCache::CacheCluster
    Properties: {Engine: redis, CacheNodeType: cache.t3.micro, NumCacheNodes: 1, VpcSecurityGroupIds: [sg-1]}
  Pages:
    Type: AWS::ElastiCache::CacheCluster
    Properties: {Engine: memcached, CacheNodeType: cache.t3.micro, NumCacheNodes: 2}
`)
	holds(t, "the decision on Sessions", mappedTo(t, out, "Sessions", "core/datastore/redis").Rationale,
		"realized on cloudformation by the Recipe redis-elasticache", "the Recipe deploys a replication group of one "+
			"node in place of the cache cluster")
	mappedTo(t, out, "Pages", "core/datastore/memcached")
	holds(t, "the Release", fileText(t, out, "release.yaml"), "    sessions:\n      infra:\n        overrides:\n"+
		"          CacheNodeType: \"cache.t3.micro\"\n          SecurityGroupIds: [\"sg-1\"]\n")
}

// TestEcsServerWorkerAndTask maps an Amazon ECS service registering its tasks
// with a load balancer, and one whose main container maps a port, to the
// server; one doing neither to the worker; and a task definition no service
// runs, with the schedule and the EventBridge rule starting it folded into
// it, to the task, the first of each kind written and each other not carried.
func TestEcsServerWorkerAndTask(t *testing.T) {
	t.Parallel()
	out := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Web:
    Type: AWS::ECS::Service
    Properties:
      DesiredCount: 3
      LoadBalancers: [{ContainerName: web, ContainerPort: 8080, TargetGroupArn: arn:aws:elasticloadbalancing:eu-south-1:123456789012:targetgroup/web/1}]
      TaskDefinition: !Ref WebTask
  WebTask:
    Type: AWS::ECS::TaskDefinition
    Properties:
      ContainerDefinitions: [{Name: web, Image: registry.example/web:1}]
  Api:
    Type: AWS::ECS::Service
    Properties:
      TaskDefinition: !Ref ApiTask
  ApiTask:
    Type: AWS::ECS::TaskDefinition
    Properties:
      ContainerDefinitions:
        - Name: api
          Image: registry.example/api:1
          PortMappings: [{ContainerPort: 9000}]
          Environment: [{Name: MODE, Value: prod}]
  Consumer:
    Type: AWS::ECS::Service
    Properties:
      TaskDefinition: !Ref ConsumerTask
  ConsumerTask:
    Type: AWS::ECS::TaskDefinition
    Properties:
      ContainerDefinitions: [{Name: consumer, Image: registry.example/consumer:1}]
  Report:
    Type: AWS::ECS::TaskDefinition
    Properties:
      ContainerDefinitions: [{Name: report, Image: registry.example/report:1}]
  Nightly:
    Type: AWS::Scheduler::Schedule
    Properties:
      ScheduleExpression: cron(0 2 * * ? *)
      ScheduleExpressionTimezone: Europe/Rome
      Target: {Arn: arn:aws:ecs:eu-south-1:123456789012:cluster/main, EcsParameters: {TaskDefinitionArn: !Ref Report}}
  Weekly:
    Type: AWS::Scheduler::Schedule
    Properties:
      ScheduleExpression: cron(0 3 ? * 1 *)
      ScheduleExpressionTimezone: UTC
      Target: {Arn: arn:aws:ecs:eu-south-1:123456789012:cluster/main, EcsParameters: {TaskDefinitionArn: !Ref Report}}
  Purge:
    Type: AWS::ECS::TaskDefinition
    Properties:
      ContainerDefinitions: [{Name: purge, Image: registry.example/purge:1}]
  PurgeRule:
    Type: AWS::Events::Rule
    Properties:
      ScheduleExpression: rate(1 day)
      Targets:
        - Id: purge
          Arn: arn:aws:ecs:eu-south-1:123456789012:cluster/main
          RoleArn: arn:aws:iam::123456789012:role/events
          EcsParameters:
            TaskDefinitionArn: !Ref Purge
            NetworkConfiguration: {AwsVpcConfiguration: {Subnets: [subnet-1]}}
`)
	mappedTo(t, out, "Web", "core/compute/server")
	mappedTo(t, out, "Api", "core/compute/server")
	mappedTo(t, out, "Consumer", "core/compute/worker")
	mappedTo(t, out, "Report", "core/compute/task")
	mappedTo(t, out, "Purge", "core/compute/task")
	holds(t, "the decision on Nightly", decisionOn(t, out, "Nightly").Rationale, "folded: written into the instance "+
		"of Report")
	holds(t, "the decision on Weekly", decisionOn(t, out, "Weekly").Rationale, "not carried: the fold of "+
		"AWS::Scheduler::Schedule in the row of AWS::ECS::TaskDefinition takes one value, the first by ID, Nightly")
	holds(t, "the decision on PurgeRule", decisionOn(t, out, "PurgeRule").Rationale, "folded: written into the "+
		"instance of Purge")
	release := fileText(t, out, "release.yaml")
	holds(t, "the Release", release, "    web:\n      infra:\n        image: \"registry.example/web:1\"\n"+
		"        replicas: 3\n", "        port: 9000\n", "    report:\n      infra:\n        image: "+
		"\"registry.example/report:1\"\n        overrides:\n          ScheduleExpressionTimezone: \"Europe/Rome\"\n"+
		"          Cluster: \"arn:aws:ecs:eu-south-1:123456789012:cluster/main\"\n", "    purge:\n      infra:\n"+
		"        image: \"registry.example/purge:1\"\n        overrides:\n          Cluster: "+
		"\"arn:aws:ecs:eu-south-1:123456789012:cluster/main\"\n          RoleArn: "+
		"\"arn:aws:iam::123456789012:role/events\"\n          Subnets: [\"subnet-1\"]\n", "MODE = prod")
}

// TestEcsServiceOfAProductImageMapsToItsProductRecipe maps an Amazon ECS
// service whose main container runs the image a container product's Recipe
// names by default to that product's Recipe, the image written under the key
// the Recipe reads it at, and one running another image to the server.
func TestEcsServiceOfAProductImageMapsToItsProductRecipe(t *testing.T) {
	t.Parallel()
	f := Format
	f.Table = Table()
	f.Table.Images = []cloudimport.ImageRow{{Repository: "ghcr.io/acme/board", Component: "core/devtool/acme-board",
		Recipe: "acme-board-ecs", ImageKey: "image"}}
	view := goldenView(t)
	view.Components = append(view.Components, aggrega.CatalogComponent{Name: "core/devtool/acme-board",
		Version: "1.0.0", Labels: map[string]string{"component.aggrega.ai/category": "compute",
			"component.aggrega.ai/type": "server", "component.aggrega.ai/upstream-licensing": "not-applicable"}})
	out := cloudimport.Import(aggrega.ImportInput{Format: formatName, Target: target, Catalog: view,
		Sources: []aggrega.ImportSource{src("template.yaml", []byte(`AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Board:
    Type: AWS::ECS::Service
    Properties:
      TaskDefinition: !Ref BoardTask
  BoardTask:
    Type: AWS::ECS::TaskDefinition
    Properties:
      ContainerDefinitions: [{Name: board, Image: "ghcr.io/acme/board:2.4.0", PortMappings: [{ContainerPort: 3000}]}]
  Api:
    Type: AWS::ECS::Service
    Properties:
      TaskDefinition: !Ref ApiTask
  ApiTask:
    Type: AWS::ECS::TaskDefinition
    Properties:
      ContainerDefinitions: [{Name: api, Image: "ghcr.io/acme/api:1.0.0", PortMappings: [{ContainerPort: 8080}]}]
`))}}, f, parse)
	holds(t, "the decision on Board", mappedTo(t, out, "Board", "core/devtool/acme-board").Rationale,
		"realized on cloudformation by the Recipe acme-board-ecs")
	mappedTo(t, out, "Api", "core/compute/server")
	holds(t, "the Release", fileText(t, out, "release.yaml"), "    board:\n      infra:\n        image: "+
		"\"ghcr.io/acme/board:2.4.0\"\n")
	holds(t, "the Manifest", fileText(t, out, "manifest.yaml"), "board: cloudformation/acme-board-ecs",
		"api: cloudformation/server-ecs")
}

// TestZipPackagedSamFunctionLeavesTheImageToTheOperator maps a SAM function
// whose code is a .zip package to the function, its decision saying the
// Recipe runs a container image built from its CodeUri, infra.image left in
// its Release entry for the answers file, and the CodeUri reported not
// carried, beside the execution role SAM writes for a function writing no
// role.
func TestZipPackagedSamFunctionLeavesTheImageToTheOperator(t *testing.T) {
	t.Parallel()
	out := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Transform: AWS::Serverless-2016-10-31
Resources:
  Orders:
    Type: AWS::Serverless::Function
    Properties: {CodeUri: ./src, Handler: app.handler, Runtime: nodejs20.x}
`)
	holds(t, "the decision on Orders", mappedTo(t, out, "Orders", "core/compute/function").Rationale,
		"the Recipe deploying a function runs a container image, infra.image, which is left for the operator: "+
			"build the code at ./src, its CodeUri, into an image in Amazon ECR and set infra.image to it")
	notCarried := coded(out, cloudimport.CodeValueNotCarried)
	refs := []string{"template.yaml#/Resources/Orders", "template.yaml#/Resources/Orders/Properties/CodeUri"}
	if len(notCarried) != 2 || notCarried[0].SourceRef != refs[0] || notCarried[1].SourceRef != refs[1] ||
		!strings.Contains(notCarried[1].Message, "the function's code as a .zip package, so it is not carried") {
		t.Errorf("the role and the CodeUri are reported %+v", notCarried)
	}
	holds(t, "the Release", fileText(t, out, "release.yaml"), "    orders:\n      infra:\n        # Left for the "+
		"Release, since the source cannot supply it: infra.image, "+functionImage+"\n        image: \"\"\n")
}

// TestSamFunctionWritingNoRoleLeavesTheRoleToTheOperator maps three SAM
// functions to the function: one writing neither Role nor Policies, for which
// SAM writes an execution role, is reported value-not-carried at the function
// and its decision says the Recipe takes an existing role at
// infra.overrides.Role, to set in the Release; one writing its Role, and one
// writing Policies, which is reported as that property, carry no such report
// and no such decision.
func TestSamFunctionWritingNoRoleLeavesTheRoleToTheOperator(t *testing.T) {
	t.Parallel()
	out := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Transform: AWS::Serverless-2016-10-31
Resources:
  Orders:
    Type: AWS::Serverless::Function
    Properties: {PackageType: Image, ImageUri: "123456789012.dkr.ecr.eu-west-1.amazonaws.com/orders:1.0.0"}
  Billing:
    Type: AWS::Serverless::Function
    Properties: {PackageType: Image, ImageUri: "123456789012.dkr.ecr.eu-west-1.amazonaws.com/billing:1.0.0",
      Role: "arn:aws:iam::123456789012:role/billing"}
  Mailer:
    Type: AWS::Serverless::Function
    Properties: {PackageType: Image, ImageUri: "123456789012.dkr.ecr.eu-west-1.amazonaws.com/mailer:1.0.0",
      Policies: [AWSLambdaBasicExecutionRole]}
`)
	const decided = "the function writes neither Role nor Policies, so SAM writes an execution role for it, and " +
		"the Recipe deploying a function takes an existing one, infra.overrides.Role, which is left for the " +
		"operator: set infra.overrides.Role in the Release to the ARN of a role the function runs under"
	holds(t, "the decision on Orders", mappedTo(t, out, "Orders", "core/compute/function").Rationale, decided)
	for _, id := range []string{"Billing", "Mailer"} {
		lacks(t, "the decision on "+id, mappedTo(t, out, id, "core/compute/function").Rationale, decided)
	}
	want := []aggrega.Diagnostic{
		{Severity: aggrega.SeverityWarn, Code: string(cloudimport.CodeValueNotCarried), Message: "the template " +
			"template.yaml writes Policies on the function Mailer at /Resources/Mailer/Properties/Policies, from " +
			"which SAM writes an execution role for the function, so it is not carried: the Recipe takes an " +
			"existing role: infra.overrides.Role, so grant what the policies allow in the role the operator " +
			"supplies there", SourceRef: "template.yaml#/Resources/Mailer/Properties/Policies"},
		{Severity: aggrega.SeverityWarn, Code: string(cloudimport.CodeValueNotCarried), Message: "the template " +
			"template.yaml writes neither Role nor Policies on the function Orders at /Resources/Orders, so SAM " +
			"writes an execution role for the function, which is not carried: the Recipe deploying a function " +
			"takes an existing role, infra.overrides.Role, so set infra.overrides.Role in the Release to the ARN " +
			"of a role the function runs under", SourceRef: "template.yaml#/Resources/Orders"},
	}
	if got := coded(out, cloudimport.CodeValueNotCarried); !slices.EqualFunc(got, want,
		func(a, b aggrega.Diagnostic) bool {
			return a.Severity == b.Severity && a.Code == b.Code && a.Message == b.Message && a.SourceRef == b.SourceRef
		}) {
		t.Errorf("value-not-carried reports\n%+v\nwant\n%+v", got, want)
	}
}

// TestFunctionRuntimeIsTheFunctionsRuntimeValue places the runtime a Lambda
// function or a SAM function writes as the function's runtime value, which
// the Component requires; a function whose source writes none, run from an
// image, has its runtime written empty in the Aggregate, left for the author.
func TestFunctionRuntimeIsTheFunctionsRuntimeValue(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"AWS::Lambda::Function", "AWS::Serverless::Function"} {
		r := rowOf(t, key, "function-lambda")
		if i := slices.IndexFunc(r.Values, func(p cloudimport.Placement) bool { return p.Key == "runtime" }); i < 0 ||
			!slices.Equal(r.Values[i].From, []string{"Properties", "Runtime"}) {
			t.Errorf("the row of %s places its values %+v, want runtime from Properties.Runtime", key, r.Values)
		}
		want := []cloudimport.Requirement{{Key: "values.runtime", Type: "string"},
			{Key: "infra.image", Why: functionImage}}
		if !slices.Equal(r.Requires, want) {
			t.Errorf("the row of %s requires %+v, want %+v", key, r.Requires, want)
		}
	}
	zip := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Transform: AWS::Serverless-2016-10-31
Globals:
  Function: {Runtime: python3.12}
Resources:
  Orders:
    Type: AWS::Serverless::Function
    Properties: {CodeUri: ./src, Handler: app.handler}
`)
	holds(t, "the Aggregate of a SAM function", fileText(t, zip, "aggregate.yaml"), `      type: core/compute/function
      values:
        runtime: "python3.12"
`)
	lacks(t, "the Aggregate of a SAM function", fileText(t, zip, "aggregate.yaml"), "parameters:")
	image := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Orders:
    Type: AWS::Lambda::Function
    Properties:
      PackageType: Image
      Code: {ImageUri: 123456789012.dkr.ecr.eu-west-1.amazonaws.com/orders:1.0.0}
      Role: arn:aws:iam::123456789012:role/orders
`)
	holds(t, "the Aggregate of an image function", fileText(t, image, "aggregate.yaml"), `    orders:
      type: core/compute/function
      values:
        # Left for the author, since the source does not give it: values.runtime, which its Component core/compute/function requires.
        runtime: ""
`)
	lacks(t, "the Aggregate of an image function", fileText(t, image, "aggregate.yaml"), "parameters:")
	release := fileText(t, image, "release.yaml")
	holds(t, "the Release of an image function", release, `        image: "123456789012.dkr.ecr.eu-west-1.amazonaws.com/orders:1.0.0"
`)
	lacks(t, "the Release of an image function", release, "parameters:")
	lacks(t, "the Release of an image function", release, `image: ""`)
}

// TestBucketRegionIsLeftForTheAnswersFile leaves the Region a bucket's Recipe
// holds its stack to in the bucket's Release entry for the answers file, as no
// template states the Region its stack deploys to.
func TestBucketRegionIsLeftForTheAnswersFile(t *testing.T) {
	t.Parallel()
	out := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  OrderArchive:
    Type: AWS::S3::Bucket
    Properties: {BucketName: orders-archive-example}
`)
	holds(t, "the Release", fileText(t, out, "release.yaml"), `    order-archive:
      infra:
        # Left for the Release, since the source cannot supply it: infra.region, `+bucketRegion+`
        region: ""
`)
	lacks(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), "parameters:")
}

// TestSamFunctionMapsThroughTheDerivedRow derives the row of a SAM function
// from the Lambda function's: its Component, Recipe and labels are that row's,
// and its placements read the paths SAM writes, the image at ImageUri and the
// provisioned concurrency on the function itself.
func TestSamFunctionMapsThroughTheDerivedRow(t *testing.T) {
	t.Parallel()
	lambda := rowOf(t, "AWS::Lambda::Function", "function-lambda")
	sam := rowOf(t, "AWS::Serverless::Function", "function-lambda")
	if sam.Component != lambda.Component || sam.Category != lambda.Category || !maps.Equal(sam.Traits, lambda.Traits) {
		t.Errorf("the SAM function's row maps to %s (%s, %v), want the Lambda row's %s (%s, %v)", sam.Component,
			sam.Category, sam.Traits, lambda.Component, lambda.Category, lambda.Traits)
	}
	froms := map[string][]string{}
	for _, p := range sam.Infra {
		froms[p.Key] = p.From
	}
	if !slices.Equal(froms["image"], []string{"Properties", "ImageUri"}) ||
		!slices.Equal(froms["min_replicas"], []string{"Properties", "ProvisionedConcurrencyConfig",
			"ProvisionedConcurrentExecutions"}) {
		t.Errorf("the SAM function's infra reads %v, want image at Properties.ImageUri and min_replicas at its "+
			"provisioned concurrency", froms)
	}
	if sam.Env == nil || !slices.Equal(sam.Env.Path, []string{"Properties", "Environment", "Variables"}) {
		t.Errorf("the SAM function's environment is read at %+v", sam.Env)
	}
	if !strings.HasPrefix(sam.Origin, "derived for AWS::Serverless::Function from the row of AWS::Lambda::Function") {
		t.Errorf("the SAM function's row comes from %q", sam.Origin)
	}
	for _, key := range []string{"AWS::Serverless::SimpleTable", "AWS::Serverless::StateMachine",
		"AWS::Serverless::HttpApi"} {
		rows, _ := Table().Rows()
		if !slices.ContainsFunc(rows, func(r cloudimport.Row) bool { return r.Key == key }) {
			t.Errorf("no row is derived for %s", key)
		}
	}
	state := rowOf(t, "AWS::Serverless::StateMachine", "step-functions")
	if i := slices.IndexFunc(state.Values, func(p cloudimport.Placement) bool { return p.Key == "state_machine_type" }); i < 0 ||
		!slices.Equal(state.Values[i].From, []string{"Properties", "Type"}) {
		t.Errorf("the SAM state machine's type is read at %+v", state.Values)
	}
	rows, _ := Table().Rows()
	if slices.ContainsFunc(rows, func(r cloudimport.Row) bool { return r.Key == "AWS::Serverless::Api" }) {
		t.Errorf("a row is derived for SAM's Api, which no Recipe deploys")
	}
}

// TestTypesLeftToTheOperator classes the substrate a template creates as
// substrate, each key reading it left for the answers file, the comment
// naming the resource, and an alarm as not carried.
func TestTypesLeftToTheOperator(t *testing.T) {
	t.Parallel()
	out := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Vpc: {Type: AWS::EC2::VPC}
  Role: {Type: AWS::IAM::Role}
  Cluster: {Type: AWS::ECS::Cluster}
  Logs: {Type: AWS::Logs::LogGroup}
  DbSubnets: {Type: AWS::RDS::DBSubnetGroup}
  CacheSubnets: {Type: AWS::ElastiCache::SubnetGroup}
  Balancer: {Type: AWS::ElasticLoadBalancingV2::LoadBalancer}
  Listener: {Type: AWS::ElasticLoadBalancingV2::Listener}
  Scaling: {Type: AWS::ApplicationAutoScaling::ScalableTarget}
  Invoke: {Type: AWS::Lambda::Permission}
  Zone: {Type: AWS::Route53::HostedZone}
  Alarm: {Type: AWS::CloudWatch::Alarm}
  Worker:
    Type: AWS::ECS::Service
    Properties:
      Cluster: !Ref Cluster
      TaskDefinition: !Ref WorkerTask
  WorkerTask:
    Type: AWS::ECS::TaskDefinition
    Properties:
      ExecutionRoleArn: !GetAtt Role.Arn
      ContainerDefinitions: [{Name: worker, Image: registry.example/worker:1}]
`)
	for _, id := range []string{"Vpc", "Role", "Cluster", "Logs", "DbSubnets", "CacheSubnets", "Balancer", "Listener",
		"Scaling", "Invoke", "Zone"} {
		if d := decisionOn(t, out, id); !strings.HasPrefix(d.Rationale, "substrate: ") {
			t.Errorf("%s is decided %q, want substrate", id, d.Rationale)
		}
	}
	if d := decisionOn(t, out, "Alarm"); !strings.HasPrefix(d.Rationale, "not carried: ") {
		t.Errorf("the alarm is decided %q, want not carried", d.Rationale)
	}
	holds(t, "the Release", fileText(t, out, "release.yaml"), "          # Left for the Release, since the source "+
		"cannot supply it: infra.overrides.Cluster, the pointer to the resource Cluster (AWS::ECS::Cluster)",
		"infra.overrides.ExecutionRoleArn, the pointer to the resource Role (AWS::IAM::Role)")
}

// TestNotImportedNamesEveryEntityNoRowReaches lists, of the hub's
// cloudformation Recipes and FeatureRecipes, those no row reaches with the
// reason, each FeatureRecipe, the Recipe of an example Component, the log
// group and each reference realization among them, and no older version.
func TestNotImportedNamesEveryEntityNoRowReaches(t *testing.T) {
	t.Parallel()
	var names []string
	for _, n := range Table().NotImported {
		if strings.TrimSpace(n.Reason) == "" {
			t.Errorf("%s is listed with no reason", n.Recipe)
		}
		names = append(names, n.Recipe)
	}
	want := []string{"backup-aws-backup", "backup-native", "bedrock", "bedrock-agent", "bedrock-embedding",
		"bedrock-guardrails", "cloudwatch", "cognito", "db-reader", "generated-secret", "lex", "manual-trigger",
		"random-password", "sagemaker", "shared-config", "storage-alarm"}
	if !slices.Equal(names, want) {
		t.Errorf("notImported lists %q, want %q", names, want)
	}
}

// TestFoldsWritingKeysTakeOneChild writes, of two integrations of one HTTP
// API, which would each write the same keys, the first by ID, and names the
// other not carried.
func TestFoldsWritingKeysTakeOneChild(t *testing.T) {
	t.Parallel()
	out := importText(t, `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Api:
    Type: AWS::ApiGatewayV2::Api
    Properties: {Name: orders, ProtocolType: HTTP}
  ToOrders:
    Type: AWS::ApiGatewayV2::Integration
    Properties: {ApiId: !Ref Api, IntegrationType: HTTP_PROXY, IntegrationUri: https://orders.example}
  ToRefunds:
    Type: AWS::ApiGatewayV2::Integration
    Properties: {ApiId: !Ref Api, IntegrationType: HTTP_PROXY, IntegrationUri: https://refunds.example}
`)
	holds(t, "the decision on ToOrders", decisionOn(t, out, "ToOrders").Rationale, "folded: written into the "+
		"instance of Api")
	holds(t, "the decision on ToRefunds", decisionOn(t, out, "ToRefunds").Rationale, "not carried: the fold of "+
		"AWS::ApiGatewayV2::Integration in the row of AWS::ApiGatewayV2::Api takes one value, the first by ID, "+
		"ToOrders")
	release := fileText(t, out, "release.yaml")
	holds(t, "the Release", release, `IntegrationUri: "https://orders.example"`)
	lacks(t, "the Release", release, "refunds.example")
}

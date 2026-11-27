// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// propPath is a property or attribute path, its segments given in order.
func propPath(segs ...string) []string { return segs }

// The paths the Whens of the Amazon ECS services read: the target groups a
// service registers its tasks with, and the port mappings of the main
// container of the task definition a service references, read in place of the
// reference.
var (
	loadBalancers = propPath("Properties", "LoadBalancers")
	portMappings  = propPath("Properties", "TaskDefinition", "ContainerDefinitions", "0", "PortMappings")
)

// container returns the path of the main container's member member of the
// task definition whose properties begin at the path at.
func container(at []string, member ...string) []string {
	return append(append(append([]string(nil), at...), "ContainerDefinitions", "0"), member...)
}

// The paths at which a service and a task read their task definition's
// properties: a service's in place of its reference to it, a task's its own.
var (
	serviceTaskDefinition = propPath("Properties", "TaskDefinition")
	ownTaskDefinition     = propPath("Properties")
)

// ecsRow returns the hand row merged into the generated row of the Amazon ECS
// resource keyed key and of the When given, whose task definition's
// properties begin at the path at: its main container's image and
// environment, and for a service its count of tasks and, where port is set,
// the port its main container maps.
func ecsRow(key string, when []cloudimport.Pred, at []string, service, port bool) cloudimport.Row {
	r := cloudimport.Row{Key: key, When: when,
		Infra: []cloudimport.Placement{{From: container(at, "Image"), Key: "image"}},
		Env:   &cloudimport.EnvList{Path: container(at, "Environment"), Name: "Name", Value: "Value"},
	}
	if service {
		r.Infra = append(r.Infra, cloudimport.Placement{From: propPath("Properties", "DesiredCount"), Key: "replicas"})
	}
	if port {
		r.Infra = append(r.Infra, cloudimport.Placement{From: container(at, "PortMappings", "0", "ContainerPort"),
			Key: "port"})
	}
	return r
}

// rdsInstance returns the hand row merged into the generated row of an RDS
// instance of the When given: its allocated storage, in gibibytes, as
// infra.storage.size.
func rdsInstance(when cloudimport.Pred) cloudimport.Row {
	return cloudimport.Row{Key: "AWS::RDS::DBInstance", When: []cloudimport.Pred{when},
		Infra: []cloudimport.Placement{{From: propPath("Properties", "AllocatedStorage"), Key: "storage.size",
			Conv: "unit:Gi"}}}
}

// engine is the predicate an RDS instance's engine of the name given meets.
func engine(op cloudimport.PredOp, name string) cloudimport.Pred {
	return cloudimport.Pred{Path: propPath("Properties", "Engine"), Op: op, Value: name}
}

// sqlServer is the When of the rows of an RDS instance running SQL Server, of
// any of its editions.
var sqlServer = engine(cloudimport.OpPrefix, "sqlserver-")

// eventsRuleType is the type of an EventBridge rule, and eventsRule the path
// of its first target.
const eventsRuleType = "AWS::Events::Rule"

var eventsRule = propPath("Properties", "Targets", "0")

// takeOne is why a fold writing a child's properties takes one child, the
// first by ID, where the generated row's fold takes every child: the Recipe
// writes one resource of the child's type, so each child would write the same
// keys, and each other child is reported not carried, naming the one written.
const takeOne = "the Recipe writes one resource of the child's type, so of several children each writing the " +
	"same keys the first by ID is written and each other is not carried"

// oneChild returns the hand row merged into the generated row of key and when
// whose first fold of the child type typ writes the property at from under
// the key at, taking one child, the first by ID, for the reason takeOne gives.
func oneChild(key string, when []cloudimport.Pred, typ string, at, from []string) cloudimport.Row {
	return cloudimport.Row{Key: key, When: when,
		Folds:      []cloudimport.Fold{{Type: typ, Key: at, From: from, One: true}},
		Supersedes: map[string]string{"Folds." + typ + " at " + strings.Join(at, "."): takeOne}}
}

// overrides returns the path of the infra.overrides key key.
func overrides(key string) []string {
	return propPath("infra", "overrides", key)
}

// functionImage and bucketRegion say what the infra keys the Recipes of a
// function and of a bucket require are, where a template gives none: a
// function's container image, which a template running the function from a
// .zip package or from code written in it names none of, and a bucket's AWS
// Region, which a template never states, a stack creating its resources in
// the Region it is deployed to.
const (
	functionImage = "the container image in Amazon ECR the function runs, which the Recipe requires and the " +
		"source names none of"
	bucketRegion = "the AWS Region the stack is deployed to, which the Recipe holds the stack to and a template " +
		"never states"
)

// handRows are the mapping table's rows written by hand, each keyed like a
// generated row: a row of a generated row's key and When merges into it, and
// any other stands beside the generated rows. A fold writing a child's
// properties takes one child, as takeOne says.
var handRows = []cloudimport.Row{
	ecsRow(serviceType, []cloudimport.Pred{{Path: loadBalancers, Op: cloudimport.OpPresent}}, serviceTaskDefinition,
		true, true),
	ecsRow(serviceType, []cloudimport.Pred{{Path: portMappings, Op: cloudimport.OpPresent}}, serviceTaskDefinition,
		true, true),
	ecsRow(serviceType, []cloudimport.Pred{{Path: loadBalancers, Op: cloudimport.OpAbsent},
		{Path: portMappings, Op: cloudimport.OpAbsent}}, serviceTaskDefinition, true, false),
	func() cloudimport.Row {
		r := ecsRow(taskDefinition, nil, ownTaskDefinition, false, false)
		schedule := oneChild(taskDefinition, nil, "AWS::Scheduler::Schedule", overrides("ScheduleExpressionTimezone"),
			propPath("Properties", "ScheduleExpressionTimezone"))
		network := append(slices.Clone(eventsRule), "EcsParameters", "NetworkConfiguration", "AwsVpcConfiguration")
		r.Folds = append(schedule.Folds,
			cloudimport.Fold{Type: eventsRuleType, Key: overrides("Cluster"), From: append(slices.Clone(eventsRule),
				"Arn"), One: true},
			cloudimport.Fold{Type: eventsRuleType, Key: overrides("RoleArn"), From: append(slices.Clone(eventsRule),
				"RoleArn")},
			cloudimport.Fold{Type: eventsRuleType, Key: overrides("Subnets"), From: append(slices.Clone(network),
				"Subnets")},
			cloudimport.Fold{Type: eventsRuleType, Key: overrides("SecurityGroups"), From: append(slices.Clone(network),
				"SecurityGroups")},
			cloudimport.Fold{Type: eventsRuleType, Key: overrides("AssignPublicIp"), From: append(slices.Clone(network),
				"AssignPublicIp")},
		)
		r.Supersedes = schedule.Supersedes
		return r
	}(),
	oneChild("AWS::ApiGatewayV2::Api", []cloudimport.Pred{{Path: propPath("Properties", "ProtocolType"),
		Op: cloudimport.OpEquals, Value: "HTTP"}}, "AWS::ApiGatewayV2::Integration", overrides("ConnectionId"),
		propPath("Properties", "ConnectionId")),
	oneChild("AWS::Bedrock::KnowledgeBase", nil, "AWS::Bedrock::DataSource", overrides("BucketARN"),
		propPath("Properties", "DataSourceConfiguration", "S3Configuration", "BucketArn")),
	func() cloudimport.Row {
		r := oneChild("AWS::S3::Bucket", nil, "AWS::S3::BucketPolicy", overrides("policy"),
			propPath("Properties", "PolicyDocument"))
		r.Requires = []cloudimport.Requirement{{Key: "infra.region", Why: bucketRegion}}
		return r
	}(),
	rdsInstance(engine(cloudimport.OpEquals, "postgres")),
	rdsInstance(engine(cloudimport.OpEquals, "mysql")),
	rdsInstance(engine(cloudimport.OpEquals, "mariadb")),
	rdsInstance(sqlServer),
	{
		Key:    "AWS::Lambda::Function",
		Values: []cloudimport.Placement{{From: propPath("Properties", "Runtime"), Key: "runtime"}},
		Env:    &cloudimport.EnvList{Path: propPath("Properties", "Environment", "Variables")},
		Folds: []cloudimport.Fold{
			{Type: "AWS::Lambda::Version", Key: propPath("infra", "min_replicas"),
				From: propPath("Properties", "ProvisionedConcurrencyConfig", "ProvisionedConcurrentExecutions"),
				One:  true},
			{Type: eventSourceType, NotCarried: "the Recipe writes no event source mapping, and the import draws " +
				"the function's connection to the resource whose records invoke it, so map that resource's " +
				"records to the function outside the draft"},
		},
		Requires:   []cloudimport.Requirement{{Key: "infra.image", Why: functionImage}},
		Supersedes: map[string]string{"Folds.AWS::Lambda::Version at infra.min_replicas": takeOne},
	},
	{
		Key: "AWS::SNS::Topic",
		Folds: []cloudimport.Fold{
			{Type: subscriptionType, NotCarried: "the Recipe writes the topic alone, and the import draws a " +
				"subscription's endpoint's connection to the topic, so subscribe the endpoint to the topic outside " +
				"the draft"},
			{Type: "AWS::SNS::TopicPolicy", NotCarried: "the Recipe writes no topic policy, so grant access to " +
				"the topic outside the draft"},
		},
	},
	{
		Key: "AWS::SQS::Queue",
		Folds: []cloudimport.Fold{
			{Type: "AWS::SQS::QueuePolicy", NotCarried: "the Recipe writes no queue policy, so grant access to " +
				"the queue outside the draft"},
		},
	},
	{
		Key: "AWS::Route53::RecordSet",
		Values: []cloudimport.Placement{
			{From: propPath("Properties", "Name"), Key: "name", Conv: "string"},
			{From: propPath("Properties", "TTL"), Key: "ttl", Conv: "integer"},
			{From: propPath("Properties", "ResourceRecords"), Key: "records"},
		},
	},
}

// recipeWhens are the Whens written by hand for the generated rows of the
// Recipes whose text gives one key and no When telling their resources apart:
// the generator writes, of each Recipe named, one row for each When listed,
// its text's When followed by that one. The server and the worker on Amazon
// ECS render one shared fragment, so a service is a server where it registers
// its tasks with a load balancer or its main container maps a port, and a
// worker where it does neither; the Recipe of SQL Server on RDS names its
// engine through a local, so its row maps an engine of SQL Server of any
// edition.
var recipeWhens = map[string][][]cloudimport.Pred{
	"sql-server-rds": {{sqlServer}},
	"server-ecs": {
		{{Path: loadBalancers, Op: cloudimport.OpPresent}},
		{{Path: portMappings, Op: cloudimport.OpPresent}},
	},
	"worker-ecs": {
		{{Path: loadBalancers, Op: cloudimport.OpAbsent}, {Path: portMappings, Op: cloudimport.OpAbsent}},
	},
}

// notImported are the hub's cloudformation Recipes and FeatureRecipes no row
// reaches, each with the reason. A Recipe listed here generates no row.
var notImported = []cloudimport.NotImported{
	{Recipe: "backup-aws-backup", Reason: "a FeatureRecipe, realizing core/backup with an AWS Backup vault, plan " +
		"and selection: a Feature is attached to an instance, so no resource of a template is an instance of it"},
	{Recipe: "backup-native", Reason: "a FeatureRecipe, realizing core/backup as the automated backups the " +
		"database's own Recipe configures: it emits nothing, so no resource of a template is an instance of it"},
	{Recipe: "bedrock", Reason: "a reference realization: it writes no resource and publishes the endpoint " +
		"infra.endpoint names, so no resource of a template is an instance of it"},
	{Recipe: "bedrock-agent", Reason: "a reference realization: it writes no resource and publishes the agent " +
		"runtime endpoint infra.endpoint names, so no resource of a template is an instance of it"},
	{Recipe: "bedrock-embedding", Reason: "a reference realization: it writes no resource and publishes the " +
		"endpoint infra.endpoint names, so no resource of a template is an instance of it"},
	{Recipe: "bedrock-guardrails", Reason: "its provides name two resources, the guardrail and its version, so no " +
		"one resource stands for the instance, and a template's guardrail and version are drafted from the source"},
	{Recipe: "cloudwatch", Reason: "a log group is substrate in an import, since the compute Recipes read one by " +
		"name as an override, awslogs-group and LogGroup, so an instance of it could not be wired to them"},
	{Recipe: "cognito", Reason: "its provides name two resources, the user pool and its client, so no one " +
		"resource stands for the instance, and a template's user pool and client are drafted from the source"},
	{Recipe: "db-reader", Reason: "realizes an example Component"},
	{Recipe: "generated-secret", Reason: "a FeatureRecipe, realizing core/generated-secret: it emits nothing, the " +
		"service owning its credential, so no resource of a template is an instance of it"},
	{Recipe: "lex", Reason: "its provides name two resources, the bot and its alias, so no one resource stands " +
		"for the instance, and a template's bot, version and alias are drafted from the source"},
	{Recipe: "manual-trigger", Reason: "a FeatureRecipe, realizing core/manual-trigger: it emits nothing, and the " +
		"Recipe realizing the task deploys its task definition unstarted, so no resource of a template is an " +
		"instance of it"},
	{Recipe: "random-password", Reason: "a FeatureRecipe, realizing core/random-password as a Secrets Manager " +
		"secret the stack generates: a Feature is attached to an instance, so no resource of a template is an " +
		"instance of it"},
	{Recipe: "sagemaker", Reason: "a reference realization: it writes no resource and publishes the runtime " +
		"endpoint infra.endpoint names, so no resource of a template is an instance of it"},
	{Recipe: "shared-config", Reason: "a FeatureRecipe, realizing core/shared-config as one SSM parameter of the " +
		"boundary holding the boundary's tier: the parameter it writes is none a template writes, so no resource " +
		"of a template is an instance of it"},
	{Recipe: "storage-alarm", Reason: "a FeatureRecipe, realizing the example Feature example/storage-alarm: a " +
		"Feature is attached to an instance, and an import maps to no example"},
}

// SlotFeature is the one feature type a Recipe's assertions admit at a
// credential slot of its Component, any other attachment there halting the
// build.
type SlotFeature struct {
	// Recipe is the Recipe's name, Slot the slot and Feature the feature type.
	Recipe, Slot, Feature string
}

// RecipeLogicalID is how a Recipe names the resource that is its instance in
// the stack the build writes: the capitalised words of the instance's key,
// followed by Suffix.
type RecipeLogicalID struct {
	// Recipe is the Recipe's name, and Suffix the literal text it writes after
	// those words, empty where it writes none.
	Recipe, Suffix string
}

// logicalIDSuffix returns the text the Recipe named recipe writes after the
// capitalised words of an instance's key in the logical ID of the resource
// that is its instance, as the hub held it at TableRevision; false where its
// text names the resource otherwise.
func logicalIDSuffix(recipe string) (string, bool) {
	for _, id := range generatedLogicalIDs {
		if id.Recipe == recipe {
			return id.Suffix, true
		}
	}
	return "", false
}

// derived are the rows derived from the generated rows, the hand rows merged
// into them ([derivations]); none where those rows cannot be merged, which
// the core then refuses by name.
var derived = func() []cloudimport.Row {
	rows, err := cloudimport.Table{Generated: generatedRows, Hand: handRows}.Rows()
	if err != nil {
		return nil
	}
	return derivedRows(rows)
}()

// Table returns the cloudformation format's mapping table: the rows generated
// from the hub's cloudformation Recipes at TableRevision followed by the rows
// derived from them for the types whose resources deploy as another type's —
// AWS SAM's serverless types and a cache cluster running Redis — the hand
// rows, the container product rows, and the Recipes and FeatureRecipes no row
// reaches. Its rows are read and never changed.
func Table() cloudimport.Table {
	return cloudimport.Table{Revision: TableRevision, Generated: append(slices.Clone(generatedRows), derived...),
		Hand: handRows, Images: generatedImages, NotImported: notImported}
}

// RecipeWhens returns the Whens written by hand for the generated rows of the
// Recipes named, each Recipe giving one row for each When listed. The map is
// read and never changed.
func RecipeWhens() map[string][][]cloudimport.Pred {
	return recipeWhens
}

// AdmittedFeature returns the one feature type the assertions of the Recipe
// named recipe admit at slot, as the hub held them at TableRevision, and
// false where they admit any or none.
func AdmittedFeature(recipe, slot string) (string, bool) {
	for _, s := range generatedSlotFeatures {
		if s.Recipe == recipe && s.Slot == slot {
			return s.Feature, true
		}
	}
	return "", false
}

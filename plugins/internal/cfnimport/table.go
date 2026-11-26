// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import "github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"

// handRows are the mapping table's rows written by hand, each keyed like a
// generated row: a row of a generated row's key and When merges into it, and
// any other stands beside the generated rows.
var handRows []cloudimport.Row

// The paths the Whens of the Amazon ECS services read: the target groups a
// service registers its tasks with, and the port mappings of the main
// container of the task definition a service references, read in place of the
// reference.
var (
	loadBalancers = []string{"Properties", "LoadBalancers"}
	portMappings  = []string{"Properties", "TaskDefinition", "ContainerDefinitions", "0", "PortMappings"}
)

// recipeWhens are the Whens written by hand for the generated rows of the
// Recipes whose text gives one key and no When telling their resources apart:
// the generator writes, of each Recipe named, one row for each When listed,
// its text's When followed by that one. The server and the worker on Amazon
// ECS render one shared fragment, so a service is a server where it registers
// its tasks with a load balancer or its main container maps a port, and a
// worker where it does neither.
var recipeWhens = map[string][][]cloudimport.Pred{
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

// Table returns the cloudformation format's mapping table: the rows generated
// from the hub's cloudformation Recipes at TableRevision, the hand rows, the
// container product rows, and the Recipes and FeatureRecipes no row reaches.
// Its rows are read and never changed.
func Table() cloudimport.Table {
	return cloudimport.Table{Revision: TableRevision, Generated: generatedRows, Hand: handRows,
		Images: generatedImages, NotImported: notImported}
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

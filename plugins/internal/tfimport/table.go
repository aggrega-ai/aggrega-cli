// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package tfimport

import "github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"

// handRows are the mapping table's rows written by hand, each keyed like a
// generated row: a row of a generated row's key and When merges into it, and
// any other stands beside the generated rows.
var handRows []cloudimport.Row

// recipeWhens are the Whens written by hand for the generated rows of the
// Recipes whose text gives one key and no When telling their resources apart:
// the generator writes, of each Recipe named, one row for each When listed,
// its text's When followed by that one.
var recipeWhens = map[string][][]cloudimport.Pred{}

// referenceRealization is why a reference realization gives no row: it
// publishes an endpoint the operator names and writes no resource.
const referenceRealization = "a reference realization: it writes no resource and publishes the endpoint " +
	"infra.endpoint names, so no resource of a configuration is an instance of it"

// chained is why a Recipe gives no generated row whose resource of the type
// unreferenced, which no other resource of it references, references a
// resource of the type owner written once beside it, through no read-in rule.
func chained(unreferenced, owner string) string {
	return "its " + unreferenced + ", which no other resource of the Recipe references, references the " + owner +
		" written once beside it, so its text does not say which resource stands for the instance" + handKey
}

// handKey ends the reason of a Recipe whose text names no key the generator
// reads.
const handKey = "; its key is left to a hand row"

// attachedFeature is why a FeatureRecipe writing resources of its own gives
// no row, after what it writes.
const attachedFeature = ": a Feature is attached to an instance, so no resource of a configuration is an " +
	"instance of it"

// notImported are the hub's terraform Recipes and FeatureRecipes no row
// reaches, each with the reason. A Recipe listed here generates no row.
var notImported = []cloudimport.NotImported{
	{Recipe: "amazon-api-gateway", Reason: "its configuration writes a route and a stage, each referenced by no " +
		"other resource of the Recipe, so its text does not say which resource stands for the instance" + handKey},
	{Recipe: "audit-logging-azure-api-management", Reason: "a FeatureRecipe, binding core/audit-logging on the " +
		"API Management gateway's Recipe to a halt: it emits nothing, so no resource of a configuration is an " +
		"instance of it"},
	{Recipe: "azure-api-management", Reason: "its configuration writes an API operation and an API policy, each " +
		"referenced by no other resource of the Recipe, so its text does not say which resource stands for the " +
		"instance" + handKey},
	{Recipe: "azure-front-door", Reason: chained("azurerm_cdn_frontdoor_route", "azurerm_cdn_frontdoor_endpoint")},
	{Recipe: "azure-managed", Reason: referenceRealization},
	{Recipe: "azure-openai", Reason: chained("azurerm_cognitive_deployment", "azurerm_cognitive_account")},
	{Recipe: "azure-openai-embedding", Reason: chained("azurerm_cognitive_deployment", "azurerm_cognitive_account")},
	{Recipe: "backup-aws-backup", Reason: "a FeatureRecipe, realizing core/backup with an AWS Backup vault, plan " +
		"and selection" + attachedFeature},
	{Recipe: "backup-native", Reason: "a FeatureRecipe, realizing core/backup as the automated backups the " +
		"database's own Recipe configures: it emits nothing, so no resource of a configuration is an instance " +
		"of it"},
	{Recipe: "bedrock", Reason: referenceRealization},
	{Recipe: "bedrock-agent", Reason: referenceRealization},
	{Recipe: "bedrock-embedding", Reason: referenceRealization},
	{Recipe: "bedrock-guardrails", Reason: chained("aws_bedrock_guardrail_version", "aws_bedrock_guardrail")},
	{Recipe: "bedrock-knowledge-bases", Reason: chained("aws_bedrockagent_data_source",
		"aws_bedrockagent_knowledge_base")},
	{Recipe: "cloud-vision", Reason: referenceRealization},
	{Recipe: "cloudwatch", Reason: "a log group is substrate in an import, since the compute Recipes read one by " +
		"name as an override, awslogs_group, so an instance of it could not be wired to them"},
	{Recipe: "cognito", Reason: chained("aws_cognito_user_pool_client", "aws_cognito_user_pool")},
	{Recipe: "dns-record-azure-dns", Reason: "its configuration writes one record set of each record type, each " +
		"under a count, so none stands for one instance" + handKey},
	{Recipe: "external-object-store", Reason: "realizes an example Component, and an import maps to no example"},
	{Recipe: "firestore", Reason: chained("google_firestore_backup_schedule", "google_firestore_database")},
	{Recipe: "foundry-agent-service", Reason: referenceRealization},
	{Recipe: "function-cloud-run", Reason: "renders the Cloud Run service body server-cloud-run renders, and " +
		"nothing a configuration writes tells a function from a server, so a service maps to server-cloud-run; " +
		"the importer's mapping_overrides setting maps one to this Recipe"},
	{Recipe: "generated-secret", Reason: "a FeatureRecipe, realizing core/generated-secret: it emits nothing, the " +
		"service owning its credential, so no resource of a configuration is an instance of it"},
	{Recipe: "google-cloud-observability", Reason: referenceRealization},
	{Recipe: "lex", Reason: chained("awscc_lex_bot_alias", "awscc_lex_bot")},
	{Recipe: "manual-trigger", Reason: "a FeatureRecipe, realizing core/manual-trigger: it emits nothing, and the " +
		"Recipe realizing the task deploys a task it starts without starting it, so no resource of a " +
		"configuration is an instance of it"},
	{Recipe: "mariadb-rds-module", Reason: "renders the RDS instance of the literal engine mariadb that " +
		"mariadb-rds renders for the same Component, so such an instance maps to mariadb-rds"},
	{Recipe: "mysql-flexible-server", Reason: chained("azurerm_mysql_flexible_database",
		"azurerm_mysql_flexible_server")},
	{Recipe: "mysql-rds-module", Reason: "renders the RDS instance of the literal engine mysql that mysql-rds " +
		"renders for the same Component, so such an instance maps to mysql-rds"},
	{Recipe: "period-budget-azure-api-management", Reason: "a FeatureRecipe, realizing core/period-budget as a " +
		"token limit in the API Management API's policy" + attachedFeature},
	{Recipe: "period-budget-cloudflare-ai-gateway", Reason: "a FeatureRecipe, realizing core/period-budget as a " +
		"spend-limit rule on the Cloudflare AI Gateway's own resource" + attachedFeature},
	{Recipe: "prompt-injection-shield-azure-api-management", Reason: "a FeatureRecipe, realizing " +
		"core/prompt-injection-shield as a content safety check in the API Management API's policy" +
		attachedFeature},
	{Recipe: "public-ingress-native", Reason: "a FeatureRecipe, realizing core/public-ingress as the ingress the " +
		"server's own Recipe configures: it emits nothing, so no resource of a configuration is an instance of it"},
	{Recipe: "random-password", Reason: "a FeatureRecipe, realizing core/random-password as a password the " +
		"configuration draws when it is applied" + attachedFeature},
	{Recipe: "redshift", Reason: chained("aws_redshiftserverless_workgroup", "aws_redshiftserverless_namespace")},
	{Recipe: "retry-policy-azure-api-management", Reason: "a FeatureRecipe, realizing core/retry-policy as a " +
		"retry in the API Management API's policy" + attachedFeature},
	{Recipe: "retry-policy-cloudflare-ai-gateway", Reason: "a FeatureRecipe, realizing core/retry-policy as the " +
		"Cloudflare AI Gateway's own retry count" + attachedFeature},
	{Recipe: "s3-object-store-gcs", Reason: "sources the Cloud Storage module gcs sources, through the same " +
		"mapping, and nothing a configuration writes tells a bucket serving an S3-compatible object store from " +
		"one serving core/storage/gcs, so a call of the module maps to gcs; the importer's mapping_overrides " +
		"setting maps one to this Recipe"},
	{Recipe: "sagemaker", Reason: referenceRealization},
	{Recipe: "semantic-caching-azure-api-management", Reason: "a FeatureRecipe, realizing core/semantic-caching " +
		"as a cache lookup and store in the API Management API's policy" + attachedFeature},
	{Recipe: "shared-config-app-configuration", Reason: "a FeatureRecipe, realizing core/shared-config as one " +
		"App Configuration key-value of the boundary holding the boundary's tier: the key-value it writes is " +
		"none a configuration writes, so no resource of a configuration is an instance of it"},
	{Recipe: "shared-config-ssm-parameter", Reason: "a FeatureRecipe, realizing core/shared-config as one SSM " +
		"parameter of the boundary holding the boundary's tier: the parameter it writes is none a configuration " +
		"writes, so no resource of a configuration is an instance of it"},
	{Recipe: "sql-server-azure-sql-managed-instance", Reason: chained("azurerm_mssql_managed_database",
		"azurerm_mssql_managed_instance")},
	{Recipe: "sql-server-rds", Reason: "its configuration writes the engine through a local, as no literal, so its " +
		"text does not say which engine its row maps" + handKey},
	{Recipe: "variable-secret", Reason: "a FeatureRecipe, realizing core/secret-value by delivering a value the " +
		"Aggregate or the Release supplies: it emits nothing, so no resource of a configuration is an instance " +
		"of it"},
	{Recipe: "vertex-ai", Reason: referenceRealization},
	{Recipe: "vertex-ai-agent", Reason: referenceRealization},
	{Recipe: "vertex-ai-embedding", Reason: referenceRealization},
	{Recipe: "vertex-ai-vector-search", Reason: chained("google_vertex_ai_index_endpoint_deployed_index",
		"google_vertex_ai_index_endpoint")},
	{Recipe: "worker-container-apps", Reason: "renders the Container Apps body server-container-apps renders, " +
		"and its text gives no literal telling a worker's app from a server's, so an app maps to " +
		"server-container-apps"},
	{Recipe: "worker-ecs", Reason: "renders the Amazon ECS service body server-ecs renders, and its text gives " +
		"no literal telling a worker's service from a server's, so a service maps to server-ecs"},
}

// SlotFeature is the one feature type a Recipe's assertions admit at a
// credential slot of its Component, any other attachment there halting the
// build.
type SlotFeature struct {
	// Recipe is the Recipe's name, Slot the slot and Feature the feature type.
	Recipe, Slot, Feature string
}

// Table returns the terraform format's mapping table: the rows generated from
// the hub's terraform Recipes at TableRevision, the hand rows, the container
// product rows, and the Recipes and FeatureRecipes no row reaches. Its rows
// are read and never changed.
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

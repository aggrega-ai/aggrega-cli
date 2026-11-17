// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraform

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

// categoryPrefixes name the category of a resource type by the start of its
// name: the services of AWS, Azure and Google the catalog realizes, or plans
// to, each under the category of the Component realizing it. The first prefix a
// name starts with decides, so a prefix precedes any shorter one it overlaps.
var categoryPrefixes = []struct{ prefix, category string }{
	{"aws_bedrockagent_", "ai-agent"},
	{"aws_bedrock_", "ai-model"},
	{"aws_sagemaker_", "ai-model"},
	{"aws_lex", "ai-agent"},
	{"aws_sfn_", "ai-pipeline"},
	{"aws_lambda_", "compute"},
	{"aws_ecs_", "compute"},
	{"aws_db_", "datastore"},
	{"aws_rds_", "datastore"},
	{"aws_dynamodb_", "datastore"},
	{"aws_elasticache_", "datastore"},
	{"aws_opensearch_", "datastore"},
	{"aws_kendra_", "datastore"},
	{"aws_msk_", "messaging"},
	{"aws_mq_", "messaging"},
	{"aws_sqs_", "messaging"},
	{"aws_sns_", "messaging"},
	{"aws_kinesis", "messaging"},
	{"aws_cloudwatch_event_", "messaging"},
	{"aws_redshift", "bigdata"},
	{"aws_athena_", "bigdata"},
	{"aws_glue_", "bigdata"},
	{"aws_api_gateway_", "gateway"},
	{"aws_apigatewayv2_", "gateway"},
	{"aws_s3_", "storage"},
	{"aws_efs_", "storage"},
	{"aws_kms_", "security"},
	{"aws_cognito_", "security"},
	{"aws_acm_", "security"},
	{"aws_route53_", "network"},
	{"aws_cloudfront_", "network"},
	{"aws_cloudwatch_", "observability"},
	{"aws_ecr_", "devtool"},

	{"azurerm_container_app", "compute"},
	{"azurerm_linux_function_app", "compute"},
	{"azurerm_windows_function_app", "compute"},
	{"azurerm_cosmosdb_", "datastore"},
	{"azurerm_mssql_", "datastore"},
	{"azurerm_postgresql_", "datastore"},
	{"azurerm_mysql_", "datastore"},
	{"azurerm_redis_", "datastore"},
	{"azurerm_search_", "datastore"},
	{"azurerm_storage_", "storage"},
	{"azurerm_servicebus_", "messaging"},
	{"azurerm_eventhub", "messaging"},
	{"azurerm_eventgrid_", "messaging"},
	{"azurerm_key_vault", "security"},
	{"azurerm_api_management", "gateway"},
	{"azurerm_cdn_", "network"},
	{"azurerm_frontdoor", "network"},
	{"azurerm_dns_", "network"},
	{"azurerm_application_insights", "observability"},

	{"google_cloud_run", "compute"},
	{"google_cloudfunctions", "compute"},
	{"google_sql_", "datastore"},
	{"google_redis_", "datastore"},
	{"google_spanner_", "datastore"},
	{"google_bigtable_", "datastore"},
	{"google_firestore_", "datastore"},
	{"google_pubsub_", "messaging"},
	{"google_managed_kafka_", "messaging"},
	{"google_storage_", "storage"},
	{"google_bigquery_", "bigdata"},
	{"google_vertex_ai_", "ai-model"},
	{"google_kms_", "security"},
	{"google_dns_", "network"},
	{"google_monitoring_", "observability"},
	{"google_logging_", "observability"},
	{"google_artifact_registry_", "devtool"},
}

// category guesses the category of t by the start of its name; "" for a type no
// prefix names.
func category(t *schemadraft.Type) string {
	for _, e := range categoryPrefixes {
		if strings.HasPrefix(t.Name, e.prefix) {
			return e.category
		}
	}
	return ""
}

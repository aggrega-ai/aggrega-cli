// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudformation

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

// awsOrganization is the first segment of the name of a type AWS publishes.
const awsOrganization = "AWS"

// serviceCategories names the category of a type AWS publishes by its service,
// the second segment of its name: the AWS services the catalog realizes, or
// plans to, each under the category of the Component realizing it.
var serviceCategories = map[string]string{
	"Lambda":             "compute",
	"ECS":                "compute",
	"StepFunctions":      "ai-pipeline",
	"DynamoDB":           "datastore",
	"RDS":                "datastore",
	"ElastiCache":        "datastore",
	"OpenSearchService":  "datastore",
	"Kendra":             "datastore",
	"MSK":                "messaging",
	"AmazonMQ":           "messaging",
	"SQS":                "messaging",
	"SNS":                "messaging",
	"Events":             "messaging",
	"Kinesis":            "messaging",
	"KinesisFirehose":    "messaging",
	"Redshift":           "bigdata",
	"RedshiftServerless": "bigdata",
	"Athena":             "bigdata",
	"Glue":               "bigdata",
	"Bedrock":            "ai-model",
	"SageMaker":          "ai-model",
	"Lex":                "ai-agent",
	"ApiGateway":         "gateway",
	"ApiGatewayV2":       "gateway",
	"S3":                 "storage",
	"EFS":                "storage",
	"KMS":                "security",
	"Cognito":            "security",
	"CertificateManager": "security",
	"Route53":            "network",
	"CloudFront":         "network",
	"CloudWatch":         "observability",
	"Logs":               "observability",
	"ECR":                "devtool",
}

// typeCategories names the category of a type whose service holds types of
// several categories, ahead of its service's.
var typeCategories = map[string]string{
	"AWS::Bedrock::Agent":         "ai-agent",
	"AWS::Bedrock::KnowledgeBase": "datastore",
	"AWS::Bedrock::Guardrail":     "security",
}

// category guesses the category of t by its name: its own entry, or its
// service's; "" for a type of another organization or a service the tables do
// not name.
func category(t *schemadraft.Type) string {
	if c, ok := typeCategories[t.Name]; ok {
		return c
	}
	segments := strings.Split(t.Name, "::")
	if len(segments) != 3 || segments[0] != awsOrganization {
		return ""
	}
	return serviceCategories[segments[1]]
}

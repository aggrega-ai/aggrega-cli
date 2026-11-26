// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnrules

import "slices"

// Inline is a parent's property referencing, with Ref, a child the parent
// owns, whose Properties are read in place of the reference.
type Inline struct {
	// Parent is the parent's resource type, such as AWS::ECS::Service.
	Parent string
	// Path is the parent's property, its segments joined by dots, such as
	// Properties.TaskDefinition.
	Path string
	// Child is the type of the resource the property references, such as
	// AWS::ECS::TaskDefinition.
	Child string
}

// ParentLink is a child's property naming its parent, with Ref or with
// GetAtt, by which the child is read as the parent's.
type ParentLink struct {
	// Child is the child's resource type, such as AWS::S3::BucketPolicy.
	Child string
	// Path is the child's property, its segments joined by dots, such as
	// Properties.Bucket; a segment * stands for any item of a list.
	Path string
}

var inlines = []Inline{
	{Parent: "AWS::ECS::Service", Path: "Properties.TaskDefinition", Child: "AWS::ECS::TaskDefinition"},
	{Parent: "AWS::ElastiCache::ReplicationGroup", Path: "Properties.CacheParameterGroupName",
		Child: "AWS::ElastiCache::ParameterGroup"},
	{Parent: "AWS::ElastiCache::CacheCluster", Path: "Properties.CacheParameterGroupName",
		Child: "AWS::ElastiCache::ParameterGroup"},
	{Parent: "AWS::RedshiftServerless::Workgroup", Path: "Properties.NamespaceName",
		Child: "AWS::RedshiftServerless::Namespace"},
}

var parentLinks = []ParentLink{
	{Child: "AWS::S3::BucketPolicy", Path: "Properties.Bucket"},
	{Child: "AWS::Lambda::Version", Path: "Properties.FunctionName"},
	{Child: "AWS::Lambda::EventSourceMapping", Path: "Properties.FunctionName"},
	{Child: "AWS::Scheduler::Schedule", Path: "Properties.Target.EcsParameters.TaskDefinitionArn"},
	{Child: "AWS::Events::Rule", Path: "Properties.Targets.*.EcsParameters.TaskDefinitionArn"},
	{Child: "AWS::RDS::DBInstance", Path: "Properties.DBClusterIdentifier"},
	{Child: "AWS::SQS::QueuePolicy", Path: "Properties.Queues.*"},
	{Child: "AWS::SNS::Subscription", Path: "Properties.TopicArn"},
	{Child: "AWS::SNS::TopicPolicy", Path: "Properties.Topics.*"},
	{Child: "AWS::ApiGateway::Stage", Path: "Properties.RestApiId"},
	{Child: "AWS::ApiGateway::Deployment", Path: "Properties.RestApiId"},
	{Child: "AWS::ApiGateway::Resource", Path: "Properties.RestApiId"},
	{Child: "AWS::ApiGateway::Method", Path: "Properties.RestApiId"},
	{Child: "AWS::ApiGateway::Authorizer", Path: "Properties.RestApiId"},
	{Child: "AWS::ApiGatewayV2::Stage", Path: "Properties.ApiId"},
	{Child: "AWS::ApiGatewayV2::Route", Path: "Properties.ApiId"},
	{Child: "AWS::ApiGatewayV2::Integration", Path: "Properties.ApiId"},
	{Child: "AWS::ApiGatewayV2::Deployment", Path: "Properties.ApiId"},
	{Child: "AWS::ApiGatewayV2::ApiMapping", Path: "Properties.ApiId"},
	{Child: "AWS::KMS::Alias", Path: "Properties.TargetKeyId"},
	{Child: "AWS::EFS::MountTarget", Path: "Properties.FileSystemId"},
	{Child: "AWS::Bedrock::DataSource", Path: "Properties.KnowledgeBaseId"},
	{Child: "AWS::Bedrock::GuardrailVersion", Path: "Properties.GuardrailIdentifier"},
	{Child: "AWS::Cognito::UserPoolClient", Path: "Properties.UserPoolId"},
	{Child: "AWS::Lex::BotVersion", Path: "Properties.BotId"},
	{Child: "AWS::Lex::BotAlias", Path: "Properties.BotId"},
}

// Inlines returns the inline rules: each parent property whose referenced
// child is read in place of the reference. A parameter group an RDS instance
// or an Aurora cluster references is inlined by none, so it stays a resource
// of its own and the reference to it stays a reference. The list is the
// caller's own.
func Inlines() []Inline {
	return slices.Clone(inlines)
}

// ParentLinks returns the parent links: each child property naming the parent
// the child is read as part of. The list is the caller's own.
func ParentLinks() []ParentLink {
	return slices.Clone(parentLinks)
}

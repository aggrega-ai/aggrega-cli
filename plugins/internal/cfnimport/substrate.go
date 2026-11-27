// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import "github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"

// What the decision on a resource of each group of types says beside its
// class. A resource the draft leaves to the operator is no resource of the
// stack the build writes, so no Ref or Fn::GetAtt can name it there: the key
// of an instance reading it is left for the Release's answers file, the
// comment above it naming the resource.
const (
	leftForAnswers = ", which the Release leaves for its answers file, the comment above the key naming the " +
		"resource"
	networkSays = "the network a component runs in is the operator's: a Recipe of the catalog takes a VPC's " +
		"subnets and security groups by their IDs under infra.overrides" + leftForAnswers
	hostSays = "an instance, a cluster or a group of instances is a host the components run on, not a component " +
		"of an Aggregate"
	imageSays    = "a machine image and the pipeline building it serve the hosts the operator runs"
	identitySays = "identity and access are the operator's: a Recipe of the catalog takes a role by its ARN under " +
		"infra.overrides" + leftForAnswers + ", and the operator grants it its permissions"
	clusterSays = "the Amazon ECS cluster a service runs in is the operator's: the Amazon ECS Recipes take it by " +
		"its name or ARN under infra.overrides.Cluster" + leftForAnswers
	logGroupSays = "a log group is the operator's: the compute Recipes read one by name under infra.overrides, " +
		"as awslogs-group and LogGroup" + leftForAnswers
	dbSubnetGroupSays = "the subnet group a database is placed in is the operator's: the RDS and Aurora Recipes " +
		"take it by name under infra.overrides.DBSubnetGroupName" + leftForAnswers
	cacheSubnetGroupSays = "the subnet group a cache is placed in is the operator's: the ElastiCache Recipes take " +
		"it by name under infra.overrides.CacheSubnetGroupName" + leftForAnswers
	loadBalancerSays = "a load balancer and its listeners are the operator's: the server Recipe on Amazon ECS " +
		"registers its tasks with a target group of it, which the operator names under " +
		"infra.overrides.TargetGroupArn"
	scalingSays = "scaling a service is the operator's: the Recipes of the catalog keep the count of tasks or " +
		"the concurrency their infra names, and a scaling target and its policies are applied to the deployed " +
		"service outside the draft"
	permissionSays = "a permission to invoke a function is the operator's: the function Recipe grants none, and " +
		"each caller the function serves is granted it outside the draft"
	hostedZoneSays = "the hosted zone a record is written in is the operator's: the Route 53 record Recipe takes " +
		"it by its ID under infra.overrides.HostedZoneId" + leftForAnswers
	targetGroupSays = "a target group is the operator's load balancer's: the server Recipe on Amazon ECS takes " +
		"one by its ARN under infra.overrides.TargetGroupArn, and the hub holds no Feature realizing public " +
		"ingress for Amazon ECS that would carry it, so a service's TargetGroupArn reading it is not written and " +
		"is reported value-not-carried: set infra.overrides.TargetGroupArn in the Release to the ARN of a target " +
		"group of the operator's load balancer"
	listenerRuleSays = "a listener rule forwarding to a target group is the operator's load balancer's, as the " +
		"target group is, and the hub holds no Feature realizing public ingress for Amazon ECS that would carry it"
	alarmSays = "the catalog's alarm on cloudformation realizes an example Feature, to which an import maps " +
		"nothing, so an alarm is applied to the deployed resource outside the draft"
	ssmParameterSays = "core/shared-config on cloudformation writes one parameter of the boundary, " +
		"/<boundary>/shared-config holding tier=<infra.tier>, and realizes no parameter a template writes, so a " +
		"Systems Manager parameter is drafted from the source"
	parameterGroupSays = "the RDS and Aurora Recipes take a parameter group by name under infra.overrides and " +
		"create none, so the group is drafted from the source and the name an instance reads of it is left for " +
		"the operator"
	restAPISays = "the catalog's Recipe of core/gateway/amazon-api-gateway deploys an HTTP API, and none deploys " +
		"a REST API, so a REST API is drafted from the source"
)

// typeRules are the cloudformation format's type rules: the substrate a
// template may create, which the draft leaves to the operator — the network,
// the hosts and their images, identity and access, the Amazon ECS cluster, log
// groups, subnet groups, load balancers and their listeners, scaling, a
// function's invoke permissions and the hosted zone a record is written in —
// the resources no Recipe carries, a target group and a listener rule while no
// Feature realizes public ingress for Amazon ECS and an alarm, and what the
// decision on a resource drafted from the source says of why: a Systems
// Manager parameter, an RDS parameter group and a REST API. A resource of
// every other type is classed as any other is.
var typeRules = []cloudimport.TypeRule{
	{Type: "AWS::EC2::Instance", Class: cloudimport.ClassSubstrate, Says: hostSays},
	{Type: "AWS::EC2::LaunchTemplate", Class: cloudimport.ClassSubstrate, Says: hostSays},
	{Type: "AWS::EC2::*", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "AWS::AutoScaling::*", Class: cloudimport.ClassSubstrate, Says: hostSays},
	{Type: "AWS::EKS::*", Class: cloudimport.ClassSubstrate, Says: hostSays},
	{Type: "AWS::ImageBuilder::*", Class: cloudimport.ClassSubstrate, Says: imageSays},
	{Type: "AWS::IAM::*", Class: cloudimport.ClassSubstrate, Says: identitySays},
	{Type: "AWS::ECS::Cluster", Class: cloudimport.ClassSubstrate, Says: clusterSays},
	{Type: "AWS::Logs::LogGroup", Class: cloudimport.ClassSubstrate, Says: logGroupSays},
	{Type: "AWS::RDS::DBSubnetGroup", Class: cloudimport.ClassSubstrate, Says: dbSubnetGroupSays},
	{Type: "AWS::ElastiCache::SubnetGroup", Class: cloudimport.ClassSubstrate, Says: cacheSubnetGroupSays},
	{Type: "AWS::ElasticLoadBalancingV2::LoadBalancer", Class: cloudimport.ClassSubstrate, Says: loadBalancerSays},
	{Type: "AWS::ElasticLoadBalancingV2::Listener", Class: cloudimport.ClassSubstrate, Says: loadBalancerSays},
	{Type: "AWS::ApplicationAutoScaling::*", Class: cloudimport.ClassSubstrate, Says: scalingSays},
	{Type: "AWS::Lambda::Permission", Class: cloudimport.ClassSubstrate, Says: permissionSays},
	{Type: "AWS::Route53::HostedZone", Class: cloudimport.ClassSubstrate, Says: hostedZoneSays},
	{Type: "AWS::ElasticLoadBalancingV2::TargetGroup", Class: cloudimport.ClassNotCarried, Says: targetGroupSays},
	{Type: "AWS::ElasticLoadBalancingV2::ListenerRule", Class: cloudimport.ClassNotCarried,
		Says: listenerRuleSays},
	{Type: "AWS::CloudWatch::Alarm", Class: cloudimport.ClassNotCarried, Says: alarmSays},
	{Type: "AWS::SSM::Parameter", Class: cloudimport.ClassDrafted, Says: ssmParameterSays},
	{Type: "AWS::RDS::DBParameterGroup", Class: cloudimport.ClassDrafted, Says: parameterGroupSays},
	{Type: "AWS::RDS::DBClusterParameterGroup", Class: cloudimport.ClassDrafted, Says: parameterGroupSays},
	{Type: "AWS::ApiGateway::RestApi", Class: cloudimport.ClassDrafted, Says: restAPISays},
	{Type: "AWS::Serverless::Api", Class: cloudimport.ClassDrafted, Says: restAPISays},
}

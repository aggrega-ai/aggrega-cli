// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// alias is one property path a type a row is derived for writes, Derived,
// holding what the row it is derived from reads at From, or an attribute it
// reads back under another path.
type alias struct {
	From, Derived []string
}

// derivation is a row derived for the type Key, whose resource deploys as the
// resource of the row keyed From and of the When FromWhen: that row's
// Component, Recipe, labels and requirements, its When where Key's resources
// hold it, and each of its placements, environment list and provides
// translated through Aliases, the first alias of the longest path each begins
// with; a placement whose path no alias covers is left out. Promoted are the
// folds of that row whose child the type writes as a property of its own, each
// read as an infra placement at the alias's Derived path. Why says why Key's
// resources deploy as From's, in the derived row's origin. Retypes reports
// that the Recipe deploys a resource of From's type where the stack the
// template deployed holds one of Key's, under the same logical ID, so the
// stack the build writes holds there a resource whose attributes are not the
// ones the template reads; the serverless types are not retyped, as the
// transform itself deploys each as From's type.
type derivation struct {
	Key, From string
	FromWhen  []cloudimport.Pred
	When      []cloudimport.Pred
	Why       string
	Aliases   []alias
	Promoted  []alias
	Retypes   bool
}

// same is an alias of a path both types write alike.
func same(segs ...string) alias {
	return alias{From: segs, Derived: segs}
}

// derivations are the rows derived for the types whose resources deploy as
// another type's: each serverless type the AWS SAM transform expands to the
// type its row is derived from, its properties read as the transform writes
// them, and an ElastiCache cache cluster running Redis, read as the Redis
// replication group the Recipe deploys in its place. SAM's Api derives none,
// as no Recipe deploys a REST API, and SAM's HttpApi, always an HTTP API,
// writes no protocol type.
var derivations = []derivation{
	{
		Key: "AWS::Serverless::Function", From: "AWS::Lambda::Function",
		Why: "the serverless transform writes the function as an AWS::Lambda::Function",
		Aliases: []alias{
			{From: propPath("Properties", "Code", "ImageUri"), Derived: propPath("Properties", "ImageUri")},
			same("Properties", "Environment"),
			same("Properties", "LoggingConfig"),
			same("Properties", "MemorySize"),
			same("Properties", "ReservedConcurrentExecutions"),
			same("Properties", "Role"),
			same("Properties", "Runtime"),
			same("Properties", "Timeout"),
			same("Properties", "VpcConfig"),
		},
		Promoted: []alias{
			{From: propPath("AWS::Lambda::Version"), Derived: propPath("Properties", "ProvisionedConcurrencyConfig",
				"ProvisionedConcurrentExecutions")},
		},
	},
	{
		Key: "AWS::Serverless::SimpleTable", From: "AWS::DynamoDB::Table",
		Why: "the serverless transform writes the table as an AWS::DynamoDB::Table",
		Aliases: []alias{
			same("Properties", "ProvisionedThroughput"),
		},
	},
	{
		Key: "AWS::Serverless::StateMachine", From: "AWS::StepFunctions::StateMachine",
		Why: "the serverless transform writes the state machine as an AWS::StepFunctions::StateMachine",
		Aliases: []alias{
			{From: propPath("Properties", "StateMachineType"), Derived: propPath("Properties", "Type")},
			same("Properties", "Definition"),
			{From: propPath("Properties", "RoleArn"), Derived: propPath("Properties", "Role")},
			{From: propPath("Properties", "LoggingConfiguration"), Derived: propPath("Properties", "Logging")},
		},
	},
	{
		Key: "AWS::Serverless::HttpApi", From: "AWS::ApiGatewayV2::Api",
		FromWhen: []cloudimport.Pred{{Path: propPath("Properties", "ProtocolType"), Op: cloudimport.OpEquals,
			Value: "HTTP"}},
		Why: "the serverless transform writes the API as an AWS::ApiGatewayV2::Api of the protocol HTTP",
	},
	{
		Key: "AWS::ElastiCache::CacheCluster", From: "AWS::ElastiCache::ReplicationGroup",
		FromWhen: []cloudimport.Pred{engine(cloudimport.OpEquals, "redis")},
		When:     []cloudimport.Pred{engine(cloudimport.OpEquals, "redis")},
		Why: "the Recipe deploys a replication group of one node in place of the cache cluster, so a stack the " +
			"template deployed takes a resource of another type",
		Retypes: true,
		Aliases: []alias{
			same("Properties", "CacheNodeType"),
			same("Properties", "CacheSubnetGroupName"),
			same("Properties", "Port"),
			same("Properties", "TransitEncryptionEnabled"),
			{From: propPath("Properties", "SecurityGroupIds"), Derived: propPath("Properties", "VpcSecurityGroupIds")},
			{From: propPath("PrimaryEndPoint"), Derived: propPath("RedisEndpoint")},
		},
	},
}

// derivedRows returns the rows derived from rows, the generated rows with the
// hand rows merged into them, in the order derivations lists them; a
// derivation whose row rows do not hold derives none.
func derivedRows(rows []cloudimport.Row) []cloudimport.Row {
	var out []cloudimport.Row
	for _, d := range derivations {
		i := slices.IndexFunc(rows, func(r cloudimport.Row) bool {
			return r.Identity() == (cloudimport.Row{Key: d.From, When: d.FromWhen}).Identity()
		})
		if i < 0 {
			continue
		}
		out = append(out, d.derive(rows[i]))
	}
	return out
}

// retyped reports whether r is the row a derivation that retypes its
// resource derives ([derivation.Retypes]).
func retyped(r *cloudimport.Row) bool {
	for _, d := range derivations {
		if d.Retypes && (cloudimport.Row{Key: d.Key, When: d.When}).Identity() == r.Identity() {
			return true
		}
	}
	return false
}

// derive returns the row d derives from r.
func (d derivation) derive(r cloudimport.Row) cloudimport.Row {
	out := cloudimport.Row{Key: d.Key, Component: r.Component, Category: r.Category, Recipe: r.Recipe,
		When: d.When, Traits: r.Traits, Form: r.Form, Requires: r.Requires,
		Origin: "derived for " + d.Key + " from the row of " + d.From + cloudimport.WhenText(d.FromWhen) + ", " +
			d.Why + "; that row " + r.Origin}
	out.Values = d.placements(r.Values)
	out.Infra = d.placements(r.Infra)
	out.Overrides = d.placements(r.Overrides)
	out.Secrets = d.placements(r.Secrets)
	for _, p := range r.Provides {
		if from, ok := d.translate(p.From, true); ok {
			p.From = from
			out.Provides = append(out.Provides, p)
		}
	}
	for _, f := range r.Folds {
		promoted := func(a alias) bool { return strings.EqualFold(a.From[0], f.Type) }
		if i := slices.IndexFunc(d.Promoted, promoted); i >= 0 && len(f.Key) == 2 && f.Key[0] == "infra" {
			out.Infra = append(out.Infra, cloudimport.Placement{From: d.Promoted[i].Derived, Key: f.Key[1]})
		}
		out.Folds = append(out.Folds, f)
	}
	if r.Env != nil {
		if p, ok := d.translate(r.Env.Path, false); ok {
			env := *r.Env
			env.Path = p
			out.Env = &env
		}
	}
	return out
}

// placements returns ps translated through d's aliases, each whose path no
// alias covers left out.
func (d derivation) placements(ps []cloudimport.Placement) []cloudimport.Placement {
	var out []cloudimport.Placement
	for _, p := range ps {
		if from, ok := d.translate(p.From, false); ok {
			p.From = from
			out = append(out, p)
		}
	}
	return out
}

// translate returns path as the derived type writes it, through the alias of
// the longest path it begins with, the rest of it kept; false where no alias
// covers it. An attribute, read where attr is set, that no alias covers is
// read back as written, as the derived resource keeps its logical ID and its
// attributes.
func (d derivation) translate(path []string, attr bool) ([]string, bool) {
	best := -1
	for i, a := range d.Aliases {
		if len(a.From) <= len(path) && slices.EqualFunc(a.From, path[:len(a.From)], strings.EqualFold) &&
			(best < 0 || len(a.From) > len(d.Aliases[best].From)) {
			best = i
		}
	}
	if best < 0 {
		return slices.Clone(path), attr
	}
	a := d.Aliases[best]
	return append(slices.Clone(a.Derived), path[len(a.From):]...), true
}

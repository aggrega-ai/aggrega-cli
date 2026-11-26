// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnrules

import (
	"slices"
	"strings"
	"testing"
)

// shapeFindings returns a line for each inline rule or parent link whose path
// does not begin with Properties., holds an empty segment or a wildcard
// outside a parent link's list item, or whose types are not written whole as
// AWS::<service>::<resource>; for each type a child is both inlined and
// linked by; and for each rule or link written twice.
func shapeFindings(inlines []Inline, links []ParentLink) []string {
	var out []string
	path := func(what, p string, wildcard bool) {
		segs := strings.Split(p, ".")
		if segs[0] != "Properties" || len(segs) < 2 {
			out = append(out, what+" reads "+p+", which is no path beneath Properties")
		}
		for _, s := range segs {
			if s == "" || s == "*" && !wildcard {
				out = append(out, what+" reads "+p+", whose segment "+`"`+s+`"`+" names no member")
			}
		}
	}
	typ := func(what, t string) {
		parts := strings.Split(t, "::")
		if len(parts) != 3 || parts[0] != "AWS" || parts[1] == "" || parts[2] == "" {
			out = append(out, what+" names the type "+t+", which is no AWS::<service>::<resource>")
		}
	}
	inlined := map[string]bool{}
	seen := map[string]bool{}
	for _, in := range inlines {
		what := "the inline rule of " + in.Parent + " at " + in.Path
		path(what, in.Path, false)
		typ(what, in.Parent)
		typ(what, in.Child)
		inlined[strings.ToLower(in.Child)] = true
		if id := strings.ToLower(in.Parent + " " + in.Path); seen[id] {
			out = append(out, what+" is written twice")
		} else {
			seen[id] = true
		}
	}
	seen = map[string]bool{}
	for _, l := range links {
		what := "the parent link of " + l.Child + " at " + l.Path
		path(what, l.Path, true)
		typ(what, l.Child)
		if inlined[strings.ToLower(l.Child)] {
			out = append(out, l.Child+" is both inlined into its parent and linked to it")
		}
		if id := strings.ToLower(l.Child + " " + l.Path); seen[id] {
			out = append(out, what+" is written twice")
		} else {
			seen[id] = true
		}
	}
	return out
}

// TestEveryParentLinkAndInlineIsCloudFormationShaped requires each inline
// rule's and parent link's path to name a property beneath Properties, every
// type to be written whole, and a child to be inlined or linked, never both,
// since a reader taking it both ways would write its body into its parent and
// fold it besides.
//
// 🔴 PLANTED DEFECTS: a link whose path does not begin with Properties., and a
// type both inlined and linked — the cases below add each to a copy of the
// lists — and the check names it.
func TestEveryParentLinkAndInlineIsCloudFormationShaped(t *testing.T) {
	t.Parallel()
	inlines, links := Inlines(), ParentLinks()
	if len(inlines) == 0 || len(links) == 0 {
		t.Fatalf("the lists hold %d inline rules and %d parent links, and each holds at least one", len(inlines),
			len(links))
	}
	for _, f := range shapeFindings(inlines, links) {
		t.Errorf("%s: correct plugins/internal/cfnimport/cfnrules/rules.go", f)
	}

	for _, c := range []struct {
		name    string
		inlines []Inline
		links   []ParentLink
		want    string
	}{
		{"a path outside Properties", inlines,
			append(slices.Clone(links), ParentLink{Child: "AWS::Planted::Needle", Path: "Metadata.Parent"}),
			"Metadata.Parent, which is no path beneath Properties"},
		{"a child both inlined and linked", inlines,
			append(slices.Clone(links), ParentLink{Child: inlines[0].Child, Path: "Properties.Planted"}),
			inlines[0].Child + " is both inlined into its parent and linked to it"},
	} {
		if found := shapeFindings(c.inlines, c.links); !slices.ContainsFunc(found, func(f string) bool {
			return strings.Contains(f, c.want)
		}) {
			t.Errorf("lists with %s pass the shape check: %v", c.name, found)
		}
	}
}

// TestTheListsHoldTheRulesTheExtractorReads pins the rules the hub's Recipes
// are read by: the task definition a service references and the parameter
// group a cache or a replication group references are inlined, and so is the
// namespace a Redshift Serverless workgroup references; a bucket policy, a
// function version and a schedule starting a task definition are linked to
// the parent they name, and no RDS parameter group is inlined, since every
// RDS Recipe takes its parameter group by name.
func TestTheListsHoldTheRulesTheExtractorReads(t *testing.T) {
	t.Parallel()
	for _, want := range []Inline{
		{Parent: "AWS::ECS::Service", Path: "Properties.TaskDefinition", Child: "AWS::ECS::TaskDefinition"},
		{Parent: "AWS::ElastiCache::ReplicationGroup", Path: "Properties.CacheParameterGroupName",
			Child: "AWS::ElastiCache::ParameterGroup"},
		{Parent: "AWS::ElastiCache::CacheCluster", Path: "Properties.CacheParameterGroupName",
			Child: "AWS::ElastiCache::ParameterGroup"},
		{Parent: "AWS::RedshiftServerless::Workgroup", Path: "Properties.NamespaceName",
			Child: "AWS::RedshiftServerless::Namespace"},
	} {
		if !slices.Contains(Inlines(), want) {
			t.Errorf("Inlines holds no %+v", want)
		}
	}
	for _, in := range Inlines() {
		if strings.HasPrefix(in.Parent, "AWS::RDS::") {
			t.Errorf("Inlines holds %+v, and every RDS Recipe takes its parameter group by name", in)
		}
	}
	for _, want := range []ParentLink{
		{Child: "AWS::S3::BucketPolicy", Path: "Properties.Bucket"},
		{Child: "AWS::Lambda::Version", Path: "Properties.FunctionName"},
		{Child: "AWS::Scheduler::Schedule", Path: "Properties.Target.EcsParameters.TaskDefinitionArn"},
		{Child: "AWS::Events::Rule", Path: "Properties.Targets.*.EcsParameters.TaskDefinitionArn"},
		{Child: "AWS::SQS::QueuePolicy", Path: "Properties.Queues.*"},
		{Child: "AWS::ApiGatewayV2::Stage", Path: "Properties.ApiId"},
		{Child: "AWS::Lex::BotAlias", Path: "Properties.BotId"},
	} {
		if !slices.Contains(ParentLinks(), want) {
			t.Errorf("ParentLinks holds no %+v", want)
		}
	}
}

// TestTheListsAreCopies requires each call to return a list of its own, so a
// reader changing what it was handed changes no other reader's rules.
func TestTheListsAreCopies(t *testing.T) {
	t.Parallel()
	a, b := Inlines(), Inlines()
	a[0].Path = "Properties.Planted"
	if b[0].Path == "Properties.Planted" || Inlines()[0].Path == "Properties.Planted" {
		t.Errorf("Inlines hands out its own list")
	}
	c, d := ParentLinks(), ParentLinks()
	c[0].Path = "Properties.Planted"
	if d[0].Path == "Properties.Planted" || ParentLinks()[0].Path == "Properties.Planted" {
		t.Errorf("ParentLinks hands out its own list")
	}
}

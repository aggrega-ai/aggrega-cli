// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gotmpl

import "text/template/parse"

// WalkPipelines visits every pipeline in a parsed template, wherever it sits: the
// pipeline of an action, of an if, a range or a with, and of a template
// invocation, and every pipeline written inside a command's arguments — in
// parentheses, or as the receiver a chain of fields hangs off. Each is visited
// once, before the pipelines inside its own arguments, in the order the template
// spells them.
//
// visit is told, with each pipeline, whether the dot there is still the data the
// template was handed: true from the root, through an if and every else branch,
// and in the pipeline a range or a with evaluates; false in a range or with body,
// where the dot is rebound. A pipeline inside another's arguments has the dot of
// the pipeline it sits in.
//
// It is the walk of a template's structure that the readers needing every pipeline
// share — the field-access census in this package, and a reader looking for the
// command a pipeline opens with — so they cannot come to disagree about which
// pipelines a template has. A reader that threads an environment through the
// template in order, binding variables as it goes, walks the structure itself; see
// walkRoots, which states why. The recipe-source descent's walkParseNode in
// pkg/closure walks it too, stopping at the first reference it refuses.
func WalkPipelines(root parse.Node, visit func(p *parse.PipeNode, dotIsData bool)) {
	walkPipelinesFrom(root, true, visit)
}

// walkPipelinesFrom walks the structure under n with the dot's standing at n.
func walkPipelinesFrom(n parse.Node, dotIsData bool, visit func(*parse.PipeNode, bool)) {
	if n == nil {
		return
	}
	switch v := n.(type) {
	case *parse.ListNode:
		if v == nil {
			return
		}
		for _, child := range v.Nodes {
			walkPipelinesFrom(child, dotIsData, visit)
		}
	case *parse.ActionNode:
		walkPipeline(v.Pipe, dotIsData, visit)
	case *parse.IfNode:
		walkPipeline(v.Pipe, dotIsData, visit)
		walkPipelinesFrom(v.List, dotIsData, visit)
		walkPipelinesFrom(v.ElseList, dotIsData, visit)
	case *parse.RangeNode:
		walkPipeline(v.Pipe, dotIsData, visit)
		walkPipelinesFrom(v.List, false, visit)
		walkPipelinesFrom(v.ElseList, dotIsData, visit)
	case *parse.WithNode:
		walkPipeline(v.Pipe, dotIsData, visit)
		walkPipelinesFrom(v.List, false, visit)
		walkPipelinesFrom(v.ElseList, dotIsData, visit)
	case *parse.TemplateNode:
		walkPipeline(v.Pipe, dotIsData, visit)
	}
}

// walkPipeline visits p and then every pipeline inside its commands' arguments.
func walkPipeline(p *parse.PipeNode, dotIsData bool, visit func(*parse.PipeNode, bool)) {
	if p == nil {
		return
	}
	visit(p, dotIsData)
	for _, cmd := range p.Cmds {
		if cmd == nil {
			continue
		}
		for _, arg := range cmd.Args {
			walkPipelineArg(arg, dotIsData, visit)
		}
	}
}

// walkPipelineArg follows one argument to the pipelines it holds: itself, where it
// is a parenthesized pipeline, or its receiver, where it is a chain of fields.
func walkPipelineArg(arg parse.Node, dotIsData bool, visit func(*parse.PipeNode, bool)) {
	switch a := arg.(type) {
	case *parse.PipeNode:
		walkPipeline(a, dotIsData, visit)
	case *parse.ChainNode:
		walkPipelineArg(a.Node, dotIsData, visit)
	}
}

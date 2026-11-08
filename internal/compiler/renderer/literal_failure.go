// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"reflect"
	"strings"
	"text/template/parse"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
)

// failFunction is the name a template calls to halt with a sentence of its own.
const failFunction = "fail"

// definedTemplate is one template a prepared template carries — its body or one
// it defines — as the evaluator reads it from the set: the name it is defined
// under and its parse tree. The name is carried beside the tree rather than read
// from it, because the engine names a failing template by the name it was defined
// under, which a tree added to a set under another name does not record.
type definedTemplate struct {
	name string
	tree *parse.Tree
}

// literalFailure reports whether err, a failed execution of a template carrying
// the set defined, was raised by a call of fail whose sentence is written out in
// the template: a fail call whose every argument is a string literal and which
// opens its pipeline, so nothing a pipeline hands on reaches it. Such a failure's
// text is the failing position and the author's own sentence, both template
// source, so it cannot carry anything the template read — whichever branch the
// template took to reach the call.
//
// The call is found in the tree, never in the failure's text alone. An execution
// failure opens with the position the template engine composes from the node it
// was evaluating — the template, the line and column of the node in the template
// source, and the node as the source spells it — and nothing in that position is
// data. For every such call in every template of defined, the position the engine
// would compose for it is composed here the same way and compared with the whole
// position err opens with. A command starts at an offset no other command in one
// parse shares, so an equal position names that call, or the pipeline it alone
// makes up, and every failure raised there was raised while evaluating that call.
//
// Anything that does not match keeps the comparison [templateEval.withholdFailure]
// makes: a fail whose sentence is composed — printf, a field, a variable, a piped
// value — and a position the comparison cannot reproduce exactly, such as an
// action whose own text carries the separator that closes a position, or a
// template name carrying a percent sign the engine formats unescaped. Missing a
// call costs the author's sentence and never prints a value.
//
// The release also requires the function bound under the name to compose its text
// from its message alone; see [templateEval.failComposesFromMessage].
//
// The candidates are the set the evaluator read from the template after it ran
// ([definedTemplates]), which is the set it was parsed with: text/template defines
// templates only when it parses — `define` and `block` are parse-time — and no
// function a surface binds is handed the template to define more into, so nothing
// derived from the data can join the set while the template runs. A surface
// binding a function that parses text into the template would break that, and its
// failures would then have to be located against the set as parsed.
func (e templateEval) literalFailure(defined []definedTemplate, err error) bool {
	if !e.failComposesFromMessage() {
		return false
	}
	position := executionPosition(err)
	if position == "" {
		return false
	}
	for _, d := range defined {
		if strings.Contains(d.tree.ParseName, "%") || strings.Contains(d.name, "%") {
			continue
		}
		for _, cmd := range pipelineHeads(d.tree.Root) {
			if !literalCallOf(cmd, failFunction) {
				continue
			}
			location, context := d.tree.ErrorContext(cmd)
			if fmt.Sprintf("template: %s: executing %q at <%s>", location, d.name, context) == position {
				return true
			}
		}
	}
	return false
}

// failComposesFromMessage reports whether the function this evaluation binds
// under fail composes its failure text from its message argument alone, which is
// what makes a literal message a literal failure.
//
// Two bindings are known to: the shared function set's, which an evaluation
// adding no fail of its own takes, and the assertion surface's, [assertionFail].
// Any other function a surface binds under the name is not trusted, because what
// it reads is its own business, so its failures keep the comparison.
func (e templateEval) failComposesFromMessage() bool {
	bound, own := e.extraFuncs[failFunction]
	if !own {
		return true
	}
	return reflect.ValueOf(bound).Pointer() == reflect.ValueOf(assertionFail).Pointer()
}

// literalCallOf reports whether cmd calls the function named name with at least
// one argument and every argument a string literal.
func literalCallOf(cmd *parse.CommandNode, name string) bool {
	if len(cmd.Args) < 2 {
		return false
	}
	ident, ok := cmd.Args[0].(*parse.IdentifierNode)
	if !ok || ident.Ident != name {
		return false
	}
	for _, arg := range cmd.Args[1:] {
		if _, literal := arg.(*parse.StringNode); !literal {
			return false
		}
	}
	return true
}

// pipelineHeads returns every command that opens a pipeline anywhere in the tree
// under root, through the one walk of a template's pipelines,
// [gotmpl.WalkPipelines]. A command after the first in its pipeline is not
// returned, because the value before it is handed to it as its last argument.
//
// A pipeline the walk does not reach contributes no command, and that is the safe
// direction for its one reader: a call it does not return is a failure whose text
// stays withheld.
func pipelineHeads(root parse.Node) []*parse.CommandNode {
	var out []*parse.CommandNode
	gotmpl.WalkPipelines(root, func(p *parse.PipeNode, _ bool) {
		if len(p.Cmds) > 0 && p.Cmds[0] != nil {
			out = append(out, p.Cmds[0])
		}
	})
	return out
}

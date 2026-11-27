// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"encoding/json"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

// condition lowers the condition the node n, read from the resource of the
// logical ID id, is deployed under, which the reader holds as the condition's
// name. A condition comparing, with Fn::Equals either way round, one template
// parameter that allows exactly the values true and false with true, as text
// or as a boolean, is that parameter, which is read as a boolean
// ([lowerer.boolean]): the core writes the Aggregate
// instance's active from a boolean parameter. Any other condition — composed,
// comparing with false, reading a parameter allowing other values, a NoEcho
// parameter or a pseudo parameter, declared nowhere or named by no text — is
// not carried: condition-not-carried names it, and says that switching the
// instance off by hand takes switching off with it every instance whose
// connection reaches it, and n is read as deployed unconditionally.
func (l *lowerer) condition(n *cloudimport.Node, id string) {
	lit, ok := n.Condition.(cloudimport.Literal)
	if !ok {
		return
	}
	name, isText := schemadraft.JSONText(lit.JSON)
	if p := l.switchParam(name); isText && p != nil {
		l.boolean(p)
		n.Condition = cloudimport.Param{Name: p.Name}
		return
	}
	n.Condition = nil
	named := "the condition " + name
	if !isText {
		named = "a condition it names by no text"
	}
	at := jsonPointer("Resources", id, "Condition")
	l.warn(cloudimport.CodeConditionNotCarried, at, "the template "+l.t.file+" deploys "+id+" under "+named+
		", which is not one template parameter allowing only true and false compared with true, so the "+
		"condition is not carried and the resource is drafted as deployed whatever the condition decides: write "+
		"active on its instance by hand, switching off with it every instance whose connection reaches it, as a "+
		"switched-off target halts the build")
}

// switchParam returns the graph's parameter the condition named name switches
// on: the one a template parameter reads ([lowerer.param]) that is not
// NoEcho, allowing exactly the values true and false, which the condition
// compares with true; nil where it is none.
func (l *lowerer) switchParam(name string) *cloudimport.Parameter {
	param, compared, ok := l.equality(name)
	if !ok || compared != "true" {
		return nil
	}
	p := l.param(param)
	if p == nil || p.Secure || len(p.Allowed) != 2 {
		return nil
	}
	seen := map[string]bool{}
	for _, a := range p.Allowed {
		text, _ := draftyaml.ScalarText(a)
		seen[text] = true
	}
	if !seen["true"] || !seen["false"] {
		return nil
	}
	return p
}

// equality returns the template parameter the condition named name compares,
// and the text of the literal it compares it with: the condition is Fn::Equals
// of a Ref to the parameter and a literal scalar, either way round, a number
// or a boolean as written; false for any other condition.
func (l *lowerer) equality(name string) (param, compared string, ok bool) {
	o, _ := objectOf(l.t.conditions.get(name))
	if len(o) != 1 || o[0].Key != "Fn::Equals" {
		return "", "", false
	}
	args, _ := itemsOf(o[0].Value)
	if len(args) != 2 {
		return "", "", false
	}
	for i := range args {
		ref, _ := objectOf(args[i])
		if len(ref) != 1 || ref[0].Key != "Ref" {
			continue
		}
		param, isText := schemadraft.JSONText(ref[0].Value)
		compared, isScalar := draftyaml.ScalarText(args[1-i])
		if !isText || !isScalar || !l.declares(param) {
			return "", "", false
		}
		return param, compared, true
	}
	return "", "", false
}

// decides returns whether the condition named name holds, and the template
// parameter that decides it: the condition compares one parameter with a
// literal ([lowerer.equality]), and the parameter is given a literal value —
// the value the parameters file supplies, else its default, or for a
// parameter of a nested template the literal value it reads — which is no
// NoEcho value and no Systems Manager parameter's name; false otherwise.
func (l *lowerer) decides(name string) (holds bool, param string, ok bool) {
	param, compared, ok := l.equality(name)
	if !ok {
		return false, "", false
	}
	p := l.binding(param).parameter()
	if p == nil {
		return false, "", false
	}
	value, isGiven := given(p)
	if p.Secure || isSSMValue(p.Type) || !isGiven {
		return false, "", false
	}
	return value == compared, param, true
}

// boolean reads the template parameter p, which allows only true and false,
// as a boolean parameter: its type boolean, and its default, the value the
// parameters file supplies and its allowed values the JSON booleans their
// text writes.
func (l *lowerer) boolean(p *cloudimport.Parameter) {
	p.Type = "boolean"
	p.Default = asBoolean(p.Default)
	p.Supplied = asBoolean(p.Supplied)
	for i, a := range p.Allowed {
		if b, ok := booleanJSON(a); ok {
			p.Allowed[i] = b
		}
	}
}

// asBoolean returns the literal v as the JSON boolean its text writes, v
// itself where it is no literal writing true or false.
func asBoolean(v cloudimport.Value) cloudimport.Value {
	lit, ok := v.(cloudimport.Literal)
	if !ok {
		return v
	}
	if b, ok := booleanJSON(lit.JSON); ok {
		return cloudimport.Literal{JSON: b}
	}
	return v
}

// booleanJSON returns the JSON boolean raw writes as true or false, as text
// or as a boolean; false where it writes neither.
func booleanJSON(raw json.RawMessage) (json.RawMessage, bool) {
	text, _ := draftyaml.ScalarText(raw)
	if text != "true" && text != "false" {
		return nil, false
	}
	return json.RawMessage(text), true
}

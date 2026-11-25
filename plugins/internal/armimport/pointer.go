// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"encoding/json"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// pointer returns the pointer the arm target holds for v, a value of the
// template read into g reading a resource the Release points at: a resourceId
// expression naming the resource by its type and its name, which Azure
// Resource Manager resolves when the Release's template deploys, written
// whole. v is a reference to the resource's identifier — a resourceId call, or
// reference(…).id — and its resource is either one the template reads and
// does not declare, named by the resourceId call it is written as, or a node
// of g, named by its type and name. Each argument naming the resource is
// literal text or a parameter given a value — by the parameters file, else by
// a literal default — written as that value. Any other value has no pointer:
// an attribute of a resource is read when the template deploys, and a name
// the template computes when it deploys, or a resource it deploys into another
// scope, names no resource deterministically.
func pointer(v cloudimport.Value, g *cloudimport.Graph) (string, bool) {
	ref, ok := v.(cloudimport.Ref)
	if !ok || len(ref.Attr) > 0 || ref.Node == "" {
		return "", false
	}
	if n := nodeOf(g, ref.Node); n != nil {
		return declaredPointer(g, n)
	}
	call, err := armexpr.Parse("[" + ref.Node + "]")
	c, isCall := call.(armexpr.Call)
	if err != nil || !isCall || c.Namespace != "" || !strings.EqualFold(c.Name, "resourceId") || len(c.Args) < 2 {
		return "", false
	}
	args := make([]armexpr.Node, len(c.Args))
	for i, arg := range c.Args {
		s, ok := argumentText(g, arg)
		if !ok {
			return "", false
		}
		args[i] = armexpr.String{Value: s}
	}
	return armexpr.Expression(armexpr.Call{Name: "resourceId", Args: args}), true
}

// nodeOf returns the node of g whose ID is id, nil where g holds none.
func nodeOf(g *cloudimport.Graph, id string) *cloudimport.Node {
	if g == nil {
		return nil
	}
	for _, n := range g.Nodes {
		if n != nil && n.ID == id {
			return n
		}
	}
	return nil
}

// declaredPointer returns the resourceId expression naming n, a resource of
// the template, by its type and the segments of its name.
func declaredPointer(g *cloudimport.Graph, n *cloudimport.Node) (string, bool) {
	if n.Scope != "" || n.Type == "" || n.Name == nil {
		return "", false
	}
	name, ok := valueText(g, n.Name)
	if !ok {
		return "", false
	}
	segs := strings.Split(name, "/")
	if len(segs) != strings.Count(n.Type, "/") {
		return "", false
	}
	args := []armexpr.Node{armexpr.String{Value: n.Type}}
	for _, seg := range segs {
		if seg == "" {
			return "", false
		}
		args = append(args, armexpr.String{Value: seg})
	}
	return armexpr.Expression(armexpr.Call{Name: "resourceId", Args: args}), true
}

// argumentText returns the text an argument of a resourceId call names: a
// string literal's text, or a parameter's value.
func argumentText(g *cloudimport.Graph, arg armexpr.Node) (string, bool) {
	switch a := arg.(type) {
	case armexpr.String:
		return a.Value, true
	case armexpr.Call:
		if name, ok := oneTextArgument(a, "parameters"); ok {
			return paramText(g, name)
		}
	}
	return "", false
}

// valueText returns v as text where it is literal text, a parameter given a
// value, or a composition of these; false for any other value.
func valueText(g *cloudimport.Graph, v cloudimport.Value) (string, bool) {
	switch v := v.(type) {
	case cloudimport.Literal:
		return text(v.JSON)
	case cloudimport.Param:
		return paramText(g, v.Name)
	case cloudimport.Compose:
		var b strings.Builder
		for _, part := range v.Parts {
			s, ok := valueText(g, part)
			if !ok {
				return "", false
			}
			b.WriteString(s)
		}
		return b.String(), true
	}
	return "", false
}

// paramText returns the text a parameter of g named name is given: by the
// parameters file, else by a literal default; false where it is given none,
// or is secure.
func paramText(g *cloudimport.Graph, name string) (string, bool) {
	if g == nil {
		return "", false
	}
	for _, p := range g.Params {
		if !strings.EqualFold(p.Name, name) || p.Secure {
			continue
		}
		for _, v := range []cloudimport.Value{p.Supplied, p.Default} {
			if lit, ok := v.(cloudimport.Literal); ok {
				return text(json.RawMessage(lit.JSON))
			}
		}
		return "", false
	}
	return "", false
}

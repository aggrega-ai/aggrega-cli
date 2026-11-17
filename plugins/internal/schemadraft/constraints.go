// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/stringformat"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// codeConstraintDropped warns of a constraint of the vendor schema a draft
// does not carry.
const codeConstraintDropped = "constraint-dropped"

// MaxDepth is the deepest level of a property tree a reader lifts; a reader cuts
// a subtree deeper than it to an open object.
const MaxDepth = 6

// Carry describes p as the pillar key named key: its type (the open union of
// its kinds, any value where one of them is any), its description marked as
// vendor text, whether it is required or sensitive, and the constraints the
// compact form checks as the vendor schema states them — enum; pattern where it
// compiles as an RE2 regular expression, as the catalog reads it; min and max
// on a number or an array; minLength and maxLength on a string; and format
// where it is one the compiler checks. A closed object's input members are
// described under their snake_case keys, an array's items as their own node; an
// open object is passed to the vendor whole, so its members are not described.
// A cut is described as an open object. No default is carried.
//
// Each constraint read and not carried gives one constraint-dropped warning
// naming the property, the constraint and why: every entry of p.Dropped, read
// as "<constraint>: <why>", and each Carry refuses itself, through every
// member and item described.
func Carry(p *Property, key string) (importer.SchemaNode, []aggrega.Diagnostic) {
	var diags []aggrega.Diagnostic
	n := carry(p, key, vendorPath(p), &diags)
	return n, diags
}

func carry(p *Property, key, path string, diags *[]aggrega.Diagnostic) importer.SchemaNode {
	n := importer.SchemaNode{Name: key, Type: schemaTypes(p.Kinds), Description: Marked(p.Description),
		Required: p.Required, Sensitive: p.Sensitive}
	for _, entry := range p.Dropped {
		what, why, ok := strings.Cut(entry, ": ")
		if !ok {
			why = "the draft does not carry it"
		}
		*diags = append(*diags, constraintDropped(path, "the vendor schema's "+what, why))
	}
	if p.Cut != "" {
		n.Type, n.Open = []string{string(KindObject)}, true
		*diags = append(*diags, cutDropped(path, p.Cut))
		return n
	}
	n.Enum = p.Enum
	if p.Pattern != "" {
		if _, err := regexp.Compile(p.Pattern); err != nil {
			*diags = append(*diags, constraintDropped(path, fmt.Sprintf("the vendor schema's pattern %q", p.Pattern),
				"the catalog reads a pattern as an RE2 regular expression, which refuses it: "+err.Error()))
		} else {
			n.Pattern = p.Pattern
		}
	}
	carryBounds(&n, p, path, diags)
	if p.Format != "" {
		if stringformat.Supported(p.Format) {
			n.Format = p.Format
		} else {
			*diags = append(*diags, constraintDropped(path, fmt.Sprintf("the vendor schema's format %q", p.Format),
				"the compiler checks the formats "+strings.Join(stringformat.Names(), ", ")+
					" and refuses any other when the catalog loads"))
		}
	}
	if p.Items != nil && slices.Contains(p.Kinds, KindArray) {
		items := carry(p.Items, "", path+"[]", diags)
		n.Items = &items
	}
	switch {
	case describesMembers(p):
		for _, m := range memberKeys(p) {
			child := carry(m.Prop, m.Key, path+"."+m.Prop.Name, diags)
			if m.Renamed != "" {
				child.Comment = append(child.Comment, m.Renamed)
			}
			n.Properties = append(n.Properties, child)
		}
	case len(memberKeys(p)) > 0:
		why := "it is an open object, passed to the vendor whole in the vendor's own spelling"
		if !kindAlone(p, KindObject) {
			why = "it takes several types, and the draft describes the members of an object alone"
		}
		*diags = append(*diags, constraintDropped(path, "the vendor schema's properties", why))
	}
	if p.Open && slices.Contains(p.Kinds, KindObject) {
		n.Open = true
	}
	return n
}

// carryBounds carries p's bounds the compact form reads as p's type: min and
// max on a number's value or an array's items, minLength and maxLength on a
// string, and reports each it cannot. An array's bounds are a Terraform
// block's item counts where the vendor states no other.
func carryBounds(n *importer.SchemaNode, p *Property, path string, diags *[]aggrega.Diagnostic) {
	lo, hi := p.Min, p.Max
	if kindAlone(p, KindArray) && p.Block != nil && lo == nil && hi == nil {
		if p.Block.MinItems > 0 {
			lo = jsonNumber(p.Block.MinItems)
		}
		if p.Block.MaxItems > 0 {
			hi = jsonNumber(p.Block.MaxItems)
		}
	}
	if lo != nil || hi != nil {
		if kindAlone(p, KindInteger) || kindAlone(p, KindNumber) || kindAlone(p, KindArray) {
			n.Min, n.Max = lo, hi
		} else {
			why := boundsWhy(p)
			if lo != nil {
				*diags = append(*diags, constraintDropped(path, "the vendor schema's minimum "+lo.String(), why))
			}
			if hi != nil {
				*diags = append(*diags, constraintDropped(path, "the vendor schema's maximum "+hi.String(), why))
			}
		}
	}
	if p.MinLength != nil || p.MaxLength != nil {
		if slices.Contains(p.Kinds, KindString) {
			n.MinLength, n.MaxLength = p.MinLength, p.MaxLength
		} else {
			why := "a length bounds a string, and the property is " + kindsText(p.Kinds)
			if p.MinLength != nil {
				*diags = append(*diags, constraintDropped(path,
					fmt.Sprintf("the vendor schema's minimum length %d", *p.MinLength), why))
			}
			if p.MaxLength != nil {
				*diags = append(*diags, constraintDropped(path,
					fmt.Sprintf("the vendor schema's maximum length %d", *p.MaxLength), why))
			}
		}
	}
}

func jsonNumber(n int) *json.Number {
	v := json.Number(strconv.Itoa(n))
	return &v
}

// boundsWhy says why the compact form's min and max cannot carry p's bounds.
func boundsWhy(p *Property) string {
	switch {
	case len(p.Kinds) > 1:
		return "its type is a union of " + kindsText(p.Kinds) + ", and the compact form's min and max bound one type"
	case kindAlone(p, KindString):
		return "the compact form reads min and max on a string as its length"
	}
	return "a minimum and a maximum bound a number or an array's items, and the property is " + kindsText(p.Kinds)
}

// kindsText names kinds for a message.
func kindsText(kinds []Kind) string {
	if len(kinds) == 0 {
		return "of any type"
	}
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = string(k)
	}
	if len(names) == 1 {
		return "a " + names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// schemaTypes writes kinds as the compact form's types; none where a kind is
// any, which the compact form writes as no type.
func schemaTypes(kinds []Kind) []string {
	var out []string
	for _, k := range kinds {
		if k == KindAny {
			return nil
		}
		out = append(out, string(k))
	}
	return out
}

// cuts returns one constraint-dropped warning for each cut in p, its members
// and its items, at path.
func cuts(p *Property, path string) []aggrega.Diagnostic {
	if p.Cut != "" {
		return []aggrega.Diagnostic{cutDropped(path, p.Cut)}
	}
	var out []aggrega.Diagnostic
	if p.Items != nil {
		out = append(out, cuts(p.Items, path+"[]")...)
	}
	for _, m := range p.Properties {
		out = append(out, cuts(m, path+"."+m.Name)...)
	}
	return out
}

// cutDropped warns of the shape below path, which a reader cut.
func cutDropped(path, cut string) aggrega.Diagnostic {
	why := "the vendor schema's shape is cut there"
	switch cut {
	case "cycle":
		why = "the vendor schema's definitions refer back to themselves there"
	case "depth":
		why = fmt.Sprintf("the vendor schema nests deeper than %d levels there", MaxDepth)
	}
	return constraintDropped(path, "the vendor schema's shape below it", why+", so it is drafted as an open object")
}

// constraintDropped warns that the property at path does not carry what, and why.
func constraintDropped(path, what, why string) aggrega.Diagnostic {
	return aggrega.Diagnostic{
		Severity:  "warn",
		Code:      codeConstraintDropped,
		Message:   fmt.Sprintf("the property %q does not carry %s: %s", path, what, why),
		SourceRef: path,
	}
}

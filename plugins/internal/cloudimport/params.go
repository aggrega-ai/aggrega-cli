// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

// Where a template parameter is written depends on the key that reads it. A
// parameter an instance reads into a values key is a parameter of the
// Aggregate, declared under spec.parameters with the type, description,
// allowed values, bounds and literal default the template gives it, and the
// value reads {{ .parameters.<key> }}, the key the snake_case of the
// parameter's name, numbered where two names take one. A default the parameters
// file supplies takes the place of the template's, and a default that is an
// expression is not carried and is named in the parameter's comment. A
// parameter that feeds an infra or infra.overrides key is no parameter of the
// Aggregate: each Release entry writes its value — the parameters file's, else
// the template's literal default — or leaves the key for the answers file, a
// comment naming the parameter. A secure parameter is never written as a value
// in either place, and nor is text that names one. A parameter naming an
// existing resource, whose value differs by environment, is read into a values
// key as any parameter is, but the Aggregate parameter standing for it
// declares no default, whatever the template or the parameters file gives it:
// the Release fulfils it, the comment above it naming the value the parameters
// file supplies.

// paramRef is the text standing for an Aggregate parameter in a value being
// written, resolved to its key once the value is written whole.
var paramRef = regexp.MustCompile(`@@param:(\d+)@@`)

// param returns the parameter of the graph named name, nil where it declares
// none.
func (w *aggregateWriter) param(name string) *Parameter {
	if w.g == nil {
		return nil
	}
	for i := range w.g.Params {
		if w.g.Params[i].Name == name {
			return &w.g.Params[i]
		}
	}
	return nil
}

// aggType returns the type of the Aggregate parameter standing for p: string,
// integer, number, boolean, object or array.
func aggType(p Parameter) string {
	t := strings.ToLower(p.Type)
	switch {
	case t == "int" || t == "integer":
		return "integer"
	case t == "number" || t == "float":
		return "number"
	case t == "bool" || t == "boolean":
		return "boolean"
	case t == "object" || t == "secureobject" || t == "map":
		return "object"
	case t == "array" || strings.HasPrefix(t, "list") || strings.HasPrefix(t, "commadelimited"):
		return "array"
	}
	return "string"
}

// placeholder returns the text standing for the Aggregate parameter p in a
// value being written.
func (w *aggregateWriter) placeholder(p *Parameter) string {
	for i, name := range w.pending {
		if name == p.Name {
			return "@@param:" + strconv.Itoa(i) + "@@"
		}
	}
	w.pending = append(w.pending, p.Name)
	return "@@param:" + strconv.Itoa(len(w.pending)-1) + "@@"
}

// commit returns text with each parameter it reads declared as a parameter of
// the Aggregate, in the order read, and written by its key.
func (w *aggregateWriter) commit(text string) string {
	names := w.pending
	w.pending = nil
	return paramRef.ReplaceAllStringFunc(text, func(m string) string {
		i, _ := strconv.Atoi(paramRef.FindStringSubmatch(m)[1])
		name := names[i]
		key, ok := w.hoisted[name]
		if !ok {
			key = schemadraft.UniqueSnakeKey(w.keys, name)
			w.hoisted[name] = key
		}
		return key
	})
}

// paramText writes the value of the parameter v reads: a reference to its
// Aggregate parameter, or in the Release its value.
func (w *aggregateWriter) paramText(v Param, conv Conv) (string, outcome) {
	p := w.param(v.Name)
	switch {
	case p == nil:
		return "", outcome{why: "the template declares no parameter " + v.Name}
	case p.Secure:
		return "", outcome{why: "the template parameter " + p.Name + " is secure, and a secure value is never written " +
			"as a value", warn: true}
	}
	if w.release {
		lit, o := w.paramLiteral(p)
		if !o.ok {
			return "", o
		}
		return w.leafText(lit, conv)
	}
	typ := aggType(*p)
	if typ == "object" || typ == "array" {
		return "", outcome{why: "the template parameter " + p.Name + " is an " + typ + ", which an Aggregate reads " +
			"field by field"}
	}
	ref := "{{ .parameters." + w.placeholder(p) + " }}"
	switch {
	case conv == "" && typ == "string", conv == "string":
		return draftyaml.String(ref), written()
	case conv == "":
		return ref, written()
	case conv == "integer" && (typ == "integer" || typ == "number"):
		return ref, written()
	case conv == "integer":
		return "", outcome{why: "the converter integer needs an integer, and the template parameter " + p.Name +
			" is a " + typ}
	case strings.HasPrefix(string(conv), "unit:"):
		return draftyaml.String(ref + strings.TrimPrefix(string(conv), "unit:")), written()
	case strings.HasPrefix(string(conv), convMap):
		return "", outcome{why: "the converter " + string(conv) + " maps a literal the source writes, and the " +
			"template parameter " + p.Name + " is read when the Aggregate is built, where no map applies"}
	}
	return "", outcome{why: "the row names the converter " + string(conv) + ", which the core does not know"}
}

// composeText writes the concatenation v as one string: its literals and
// parameters, or in the Release the pointer it holds where it reads a resource
// the Release points at.
func (w *aggregateWriter) composeText(v Compose, conv Conv) (string, outcome) {
	if conv != "" && conv != "string" {
		return "", outcome{why: "the converter " + string(conv) + " converts a scalar, not text composed of several"}
	}
	if w.release && readsPointer(v, w.cs) {
		return w.pointerText(v)
	}
	var b strings.Builder
	for _, part := range v.Parts {
		switch part := part.(type) {
		case Literal:
			scalar, st := literalText(part.JSON)
			if st != stateRead {
				return "", outcome{why: "it is composed with a value that is no text"}
			}
			b.WriteString(w.escape(scalar))
		case Param:
			p := w.param(part.Name)
			switch {
			case p == nil:
				return "", outcome{why: "the template declares no parameter " + part.Name}
			case p.Secure:
				return "", outcome{why: "it is composed with the secure template parameter " + p.Name + ", which is " +
					"never written as a value", warn: true}
			case w.release:
				lit, o := w.paramLiteral(p)
				if !o.ok {
					return "", o
				}
				scalar, st := literalText(lit.(Literal).JSON)
				if st != stateRead {
					return "", outcome{why: "it is composed with the template parameter " + p.Name + ", which is no text"}
				}
				b.WriteString(scalar)
			case aggType(*p) == "object" || aggType(*p) == "array":
				return "", outcome{why: "it is composed with the template parameter " + p.Name + ", an " + aggType(*p)}
			default:
				b.WriteString("{{ .parameters." + w.placeholder(p) + " }}")
			}
		default:
			return "", outcome{why: "it is composed with a reference, a credential or text the source evaluates when it " +
				"deploys"}
		}
	}
	return draftyaml.String(b.String()), written()
}

// paramLiteral returns the literal the Release writes for the parameter p: the
// value the parameters file supplies, else the template's literal default.
func (w *aggregateWriter) paramLiteral(p *Parameter) (Value, outcome) {
	if p.Secure {
		return nil, outcome{why: "the template parameter " + p.Name + " is secure, and a secure value is never " +
			"written as a value", warn: true}
	}
	if l, ok := p.Supplied.(Literal); ok {
		return l, written()
	}
	if l, ok := p.Default.(Literal); ok {
		return l, written()
	}
	why := "the template parameter " + p.Name + " has no value in the parameters file and no literal default"
	if p.Default != nil {
		why = "the template parameter " + p.Name + " has no value in the parameters file, and its default is an " +
			"expression"
	}
	return nil, outcome{why: why, leave: true}
}

// paramSource says where the value of the parameter p a Release entry writes
// comes from.
func paramSource(p *Parameter) string {
	if _, ok := p.Supplied.(Literal); ok {
		return "the value the parameters file supplies"
	}
	return "its literal default"
}

// secureReads returns the names of the secure parameters text names, in the
// order the template declares them. A name is read where it stands apart from
// other letters, digits and underscores, so a parameter's name inside a longer
// word is not, and a name inside a string the text holds is.
func (w *aggregateWriter) secureReads(text string) []string {
	if w.g == nil {
		return nil
	}
	var out []string
	for _, p := range w.g.Params {
		if !p.Secure || p.Name == "" {
			continue
		}
		re := w.secure[p.Name]
		if re == nil {
			re = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(p.Name) + `($|[^A-Za-z0-9_])`)
			w.secure[p.Name] = re
		}
		if re.MatchString(text) {
			out = append(out, p.Name)
		}
	}
	return out
}

// conditionParam returns the parameter the Aggregate's active reads for the
// instance cl: the one boolean parameter its node's condition is, where no
// connection of the Aggregate reaches the instance; nil otherwise.
func (w *aggregateWriter) conditionParam(cl *Classified) *Parameter {
	pr, ok := cl.Node.Condition.(Param)
	if !ok || w.targeted(cl) {
		return nil
	}
	p := w.param(pr.Name)
	if p == nil || p.Secure || aggType(*p) != "boolean" {
		return nil
	}
	return p
}

// targeted reports whether a connection of another instance reaches cl.
func (w *aggregateWriter) targeted(cl *Classified) bool {
	for _, other := range w.out {
		if other == cl || !isInstance(other) {
			continue
		}
		for _, u := range other.Uses {
			if u.Target == cl.Key {
				return true
			}
		}
	}
	return false
}

// instanceParts returns the active and the values the instance cl is written
// with. A node with a condition that active cannot carry is written active, and
// condition-not-carried warns why.
func (w *aggregateWriter) instanceParts(cl *Classified) (string, []*valueTree) {
	active := ""
	if cl.Node.Condition != nil {
		if p := w.conditionParam(cl); p != nil {
			active = w.commit(draftyaml.String("{{ .parameters." + w.placeholder(p) + " }}"))
		} else if w.targeted(cl) {
			w.diags = append(w.diags, Report(CodeConditionNotCarried, fmt.Sprintf("%s is deployed under a condition "+
				"in the source, and the instance %s is drafted active because another instance's edge points at it, "+
				"and a switched-off target halts the build: write active on the instance once the instances reaching "+
				"it are switched off with it", cl.Node.ID, cl.Key)))
		} else {
			w.diags = append(w.diags, Report(CodeConditionNotCarried, fmt.Sprintf("%s is deployed under a condition "+
				"that is not one boolean parameter, so the instance %s is drafted active: write active on it by hand",
				cl.Node.ID, cl.Key)))
		}
	}
	return active, w.values(cl)
}

// parametersBlock writes the spec.parameters of the Aggregate: each template
// parameter an instance reads into a key, in the order the template declares
// them; nothing where none is.
func (w *aggregateWriter) parametersBlock() string {
	if len(w.hoisted) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("  parameters:\n")
	done := map[string]bool{}
	for _, p := range w.g.Params {
		key, ok := w.hoisted[p.Name]
		if !ok || done[p.Name] {
			continue
		}
		done[p.Name] = true
		if key != p.Name {
			b.WriteString(draftyaml.CommentLine("    ", "The template parameter "+draftyaml.Comment(p.Name)+"."))
		}
		def, has := w.defaultOf(p)
		if p.Pointer {
			b.WriteString(draftyaml.CommentLine("    ", "It names an existing resource, so it declares no default: the "+
				"Release fulfils it."))
		}
		if p.Default != nil && !isLiteral(p.Default) && !has && !p.Pointer {
			b.WriteString(draftyaml.CommentLine("    ", "Its default in the template is an expression, which an "+
				"Aggregate parameter cannot hold, so it is not carried."))
		}
		if _, supplied := p.Supplied.(Literal); supplied && has {
			b.WriteString(draftyaml.CommentLine("    ", "Its default is the value the parameters file supplies; a value that differs per environment belongs in the Release, which fulfils the parameter."))
		}
		b.WriteString("    " + key + ":\n")
		b.WriteString("      type: " + aggType(p) + "\n")
		if p.Description != "" {
			b.WriteString("      description: " + draftyaml.String(w.escape(p.Description)) + "\n")
		}
		if has {
			b.WriteString("      default: " + def + "\n")
		}
		if len(p.Allowed) > 0 {
			var items []string
			for _, a := range p.Allowed {
				if text, ok := w.jsonText(a); ok {
					items = append(items, text)
				}
			}
			if len(items) == len(p.Allowed) {
				b.WriteString("      enum: [" + strings.Join(items, ", ") + "]\n")
			}
		}
		if p.Min != nil {
			b.WriteString("      min: " + p.Min.String() + "\n")
		}
		if p.Max != nil {
			b.WriteString("      max: " + p.Max.String() + "\n")
		}
		if p.MinLength != nil {
			b.WriteString("      minLength: " + strconv.Itoa(*p.MinLength) + "\n")
		}
		if p.MaxLength != nil {
			b.WriteString("      maxLength: " + strconv.Itoa(*p.MaxLength) + "\n")
		}
	}
	return b.String()
}

// defaultOf returns the default the Aggregate parameter standing for p
// declares: the value the parameters file supplies, else the template's literal
// default; none for a parameter naming an existing resource.
func (w *aggregateWriter) defaultOf(p Parameter) (string, bool) {
	if p.Pointer {
		return "", false
	}
	for _, v := range []Value{p.Supplied, p.Default} {
		if l, ok := v.(Literal); ok {
			return w.jsonText(l.JSON)
		}
	}
	return "", false
}

// isLiteral reports whether v is a literal.
func isLiteral(v Value) bool {
	_, ok := v.(Literal)
	return ok
}

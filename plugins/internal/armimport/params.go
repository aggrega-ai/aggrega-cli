// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// maxTypeHops bounds how many type definitions a parameter's type is followed
// through, so definitions referring to each other end.
const maxTypeHops = 16

// parameters returns the parameters t declares, in the order the parser
// writes them: each with its type — a secure string or object read as a
// string or an object marked secure — its description, the values it allows,
// its bounds and its default, lowered.
func (r *reader) parameters(t *template) []cloudimport.Parameter {
	l := r.lowerer(t, nil)
	var out []cloudimport.Parameter
	for _, e := range t.params {
		o, _ := objectOf(e.Value)
		at := t.at + importer.Pointer("parameters", e.Key)
		p := cloudimport.Parameter{Name: e.Key}
		p.Type, p.Secure = t.declaredType(o)
		meta, _ := objectOf(o.get("metadata"))
		p.Description, _ = text(meta.get("description"))
		allowed, _ := arrayOf(o.get("allowedValues"))
		p.Allowed = allowed
		p.Min, p.Max = number(o.get("minValue")), number(o.get("maxValue"))
		p.MinLength, p.MaxLength = length(o.get("minLength")), length(o.get("maxLength"))
		if raw := o.get("defaultValue"); raw != nil {
			p.Default = l.leaf(raw, at+importer.Pointer("defaultValue"))
		}
		out = append(out, p)
	}
	return out
}

// paramType returns the type of the template's parameter name, false where
// the template declares none.
func (t *template) paramType(name string) (string, bool) {
	declared, ok := t.param(name)
	if !ok {
		return "", false
	}
	o, _ := objectOf(t.params.exact(declared))
	typ, _ := t.declaredType(o)
	return typ, true
}

// declaredType returns the type a parameter declares, lowercased, read through
// the type definition its $ref names where it names one, and whether the type
// is a secure one: securestring is the string type marked secure, and
// secureobject the object type.
func (t *template) declaredType(o obj) (string, bool) {
	for hop := 0; hop < maxTypeHops; hop++ {
		if typ, ok := text(o.get("type")); ok {
			typ = strings.ToLower(typ)
			switch typ {
			case "securestring":
				return "string", true
			case "secureobject":
				return "object", true
			}
			return typ, false
		}
		ref, ok := text(o.get("$ref"))
		if !ok || !strings.HasPrefix(ref, "#/definitions/") {
			return "", false
		}
		o, _ = objectOf(t.defs.exact(strings.TrimPrefix(ref, "#/definitions/")))
	}
	return "", false
}

// number returns the number raw holds, nil where it holds none.
func number(raw json.RawMessage) *json.Number {
	n := json.Number(strings.TrimSpace(string(raw)))
	if _, err := n.Float64(); err != nil || jsonKind(raw) == '"' {
		return nil
	}
	return &n
}

// length returns the integer raw holds, nil where it holds none.
func length(raw json.RawMessage) *int {
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil
	}
	return &n
}

// outputs returns the outputs t declares, each with its value lowered; an
// output a copy loop computes has none.
func (r *reader) outputs(t *template) []cloudimport.Output {
	l := r.lowerer(t, nil)
	var out []cloudimport.Output
	for _, e := range t.outputs {
		o, _ := objectOf(e.Value)
		output := cloudimport.Output{Name: e.Key}
		if raw := o.get("value"); raw != nil && o.get("copy") == nil {
			output.Value = l.leaf(raw, t.at+importer.Pointer("outputs", e.Key, "value"))
		}
		out = append(out, output)
	}
	return out
}

// fill fills params from the parameters file h: a parameter it gives a value
// is supplied that value, as the file writes it; one it gives a Key Vault
// reference holds the secret's resource ID, the vault's resource ID followed
// by /secrets/ and the secret's name, as its store reference. A parameter the
// template does not declare, or one the file gives neither, is not read, and a
// pinned secret version is not carried; each is reported value-not-carried.
func (r *reader) fill(params []cloudimport.Parameter, h *handed) {
	entries, _ := objectOf(h.top.exact("parameters"))
	for _, e := range entries {
		at := importer.Pointer("parameters", e.Key)
		i := declared(params, e.Key)
		if i < 0 {
			r.notRead(h, at, "the parameters file "+h.name+" sets the parameter "+e.Key+", which the template "+
				"declares no parameter of, so it is not read: remove it from the file, or declare it in the template")
			continue
		}
		o, _ := objectOf(e.Value)
		if v := o.get("value"); v != nil {
			params[i].Supplied = cloudimport.Literal{JSON: v}
			continue
		}
		ref, _ := objectOf(o.get("reference"))
		vault, _ := objectOf(ref.get("keyVault"))
		id, _ := text(vault.get("id"))
		secret, _ := text(ref.get("secretName"))
		if id == "" || secret == "" {
			r.notRead(h, at, "the parameters file "+h.name+" sets the parameter "+e.Key+" to neither a value nor a "+
				"Key Vault reference naming a vault's ID and a secret's name, so it is not read: write its value, "+
				"or the reference")
			continue
		}
		params[i].StoreRef = strings.TrimRight(id, "/") + "/secrets/" + secret
		if ref.get("secretVersion") != nil {
			r.notRead(h, at, "the parameters file "+h.name+" reads the parameter "+e.Key+" from a version of a "+
				"Key Vault secret it pins, and the draft names the secret alone, whose current version is read "+
				"when the template deploys: keep the version wanted current in the vault")
		}
	}
}

// declared returns the index of the parameter of params named name, matched
// as Azure Resource Manager matches a parameter's name; -1 where none is.
func declared(params []cloudimport.Parameter, name string) int {
	for i, p := range params {
		if p.Name == name {
			return i
		}
	}
	for i, p := range params {
		if strings.EqualFold(p.Name, name) {
			return i
		}
	}
	return -1
}

// notRead warns value-not-carried of the member of the parameters file h at
// the JSON pointer at.
func (r *reader) notRead(h *handed, at, message string) {
	d := cloudimport.Report(cloudimport.CodeValueNotCarried, message)
	d.SourceRef = h.name + "#" + at
	r.note(d)
}

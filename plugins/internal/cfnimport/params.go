// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// paramsForm is the form a parameters file writes its values in: one of the
// forms the AWS CLI reads parameters from a file in.
type paramsForm int

// The forms of a parameters file.
const (
	// formNone is a source that is no parameters file.
	formNone paramsForm = iota
	// formList is a list of objects, each naming a parameter by ParameterKey
	// and giving its ParameterValue, or UsePreviousValue: the form
	// create-stack --parameters reads, and deploy --parameter-overrides too.
	formList
	// formPairs is a list of Key=Value strings, each split at its first =:
	// a form deploy --parameter-overrides reads.
	formPairs
	// formObject is an object holding a Parameters object of each parameter's
	// value by its name, and no Resources: the form deploy
	// --parameter-overrides reads as CodePipeline's template configuration
	// writes it.
	formObject
)

// formOf returns the form of the parameters file whose tree, as the host's
// JSON parser reads it, is tree; formNone where it is no parameters file: a
// list is one where it holds items and each is an object holding a
// ParameterKey written as text, or each is text holding =.
func formOf(tree json.RawMessage) paramsForm {
	if o, ok := objectOf(tree); ok {
		if kind(o.get("Parameters")) == '{' && o.get("Resources") == nil {
			return formObject
		}
		return formNone
	}
	items, ok := itemsOf(tree)
	if !ok || len(items) == 0 {
		return formNone
	}
	list, pairs := true, true
	for _, item := range items {
		o, _ := objectOf(item)
		if _, keyed := schemadraft.JSONText(o.get("ParameterKey")); !keyed {
			list = false
		}
		if s, text := schemadraft.JSONText(item); !text || !strings.Contains(s, "=") {
			pairs = false
		}
	}
	switch {
	case list:
		return formList
	case pairs:
		return formPairs
	}
	return formNone
}

// fill fills params from the parameters file h: a parameter it gives a value
// is supplied that value, as the file writes it, and a Key=Value string's
// value as text. A parameter the file keeps at the value the deployed stack
// holds, with UsePreviousValue, is supplied none; one it names with no value,
// a value that is no text, number or boolean, a parameter the template does
// not declare and any member of a Parameters object's file other than
// Parameters are not read; each is reported value-not-carried.
func (r *reader) fill(params []cloudimport.Parameter, h *handed) {
	switch h.form {
	case formList:
		items, _ := itemsOf(h.tree)
		for i, item := range items {
			at := jsonPointer(strconv.Itoa(i))
			o, _ := objectOf(item)
			key, _ := schemadraft.JSONText(o.get("ParameterKey"))
			if isTrue(o.get("UsePreviousValue")) {
				r.notRead(h, at, "the parameters file "+h.name()+" keeps the parameter "+key+" at the value the "+
					"deployed stack holds, with UsePreviousValue, which the import cannot read, so no value is "+
					"supplied for it: write its ParameterValue in the file")
				continue
			}
			value := o.get("ParameterValue")
			if value == nil {
				r.notRead(h, at, "the parameters file "+h.name()+" names the parameter "+key+" and gives it no "+
					"ParameterValue, so it is not read: write its ParameterValue in the file")
				continue
			}
			r.supply(params, h, at, key, value)
		}
	case formPairs:
		items, _ := itemsOf(h.tree)
		for i, item := range items {
			pair, _ := schemadraft.JSONText(item)
			key, value, _ := strings.Cut(pair, "=")
			r.supply(params, h, jsonPointer(strconv.Itoa(i)), key, importer.String(value))
		}
	case formObject:
		top, _ := objectOf(h.tree)
		for _, e := range top {
			if e.Key != "Parameters" {
				r.notRead(h, jsonPointer(e.Key), "the parameters file "+h.name()+" holds "+e.Key+" beside its "+
					"Parameters, and the import reads its Parameters alone, so "+e.Key+" is not carried: set it where "+
					"the stack is deployed")
				continue
			}
			values, _ := objectOf(e.Value)
			for _, v := range values {
				r.supply(params, h, jsonPointer("Parameters", v.Key), v.Key, v.Value)
			}
		}
	}
}

// supply supplies the parameter of params named key the value raw the
// parameters file h gives it at the JSON pointer at.
func (r *reader) supply(params []cloudimport.Parameter, h *handed, at, key string, raw json.RawMessage) {
	i := -1
	for j, p := range params {
		if p.Name == key {
			i = j
			break
		}
	}
	switch {
	case i < 0:
		r.notRead(h, at, "the parameters file "+h.name()+" sets the parameter "+key+", which the template declares "+
			"no parameter of, so it is not read: remove it from the file, or declare it in the template")
	case kind(raw) == '{' || kind(raw) == '[' || kind(raw) == 0 || isNull(raw):
		r.notRead(h, at, "the parameters file "+h.name()+" gives the parameter "+key+" a value that is no text, "+
			"number or boolean, so it is not read: write its value as text")
	default:
		params[i].Supplied = listedLiteral(&params[i], raw)
	}
}

// notRead warns value-not-carried of the member of the parameters file h at
// the JSON pointer at.
func (r *reader) notRead(h *handed, at, message string) {
	d := cloudimport.Report(cloudimport.CodeValueNotCarried, message)
	d.SourceRef = h.name() + "#" + at
	r.note(d)
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lintops

import (
	"reflect"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// withoutParameterValues removes, in place, every parameter value a parsed entity
// document carries, so that no parameter value reaches a linter plugin. A
// parameter's value can be the credential itself — a realization that delivers a
// value reads it straight from a parameter — and a plugin attached at the linter
// socket may be code the workspace did not write.
//
// It is called from one place, the conversion every document handed to a linter
// plugin goes through (see yamlToLinterJSON), so the rule is stated here once and
// holds on every lint scope alike.
//
// What is removed depends on the kind the document declares, because the same key
// holds different things in different kinds:
//
//   - In an Aggregate, spec.parameters holds the parameter declarations. Each
//     declaration keeps its contract and nothing else: the keys of the
//     declaration vocabulary (see declarationKeys) less its default, which is a
//     value, with every declaration nested under its properties, items and
//     valuesType held to the same. A key outside the vocabulary — which only a
//     draft the Aggregate schema refuses carries at a parameter's own level — is
//     removed with whatever it holds. The declarations are kept because a rule
//     reads them: comparing an Aggregate with its previously published version
//     compares its parameter declarations. An entry that is not a declaration at
//     all is removed whole, since nothing says what it holds, and so is a
//     parameters value that is not a mapping of declarations.
//   - In every other document, spec.parameters is removed whole. In a Release it
//     holds the values that fulfil the Aggregate's declarations, inline or as the
//     path of the file supplying them. No other kind declares one, so a document
//     carrying it under any other kind is held to the stricter rule rather than
//     read as declarations.
//   - Every module listed under spec.aggregates loses its parameters block, which
//     holds the values passed to that module.
//
// A default is deleted rather than replaced, so a plugin cannot tell a declaration
// that gives one from a declaration that does not.
//
// Template text that reads a parameter is not a value and is kept, so a rule still
// sees which parameter an authored value reads. A document with no spec mapping is
// left as it is.
func withoutParameterValues(tree any, kind string) {
	spec, ok := specOf(tree)
	if !ok {
		return
	}
	if kind == string(entitykind.Aggregate) {
		withOnlyDeclarations(spec)
	} else {
		delete(spec, "parameters")
	}
	modules, ok := spec["aggregates"].(map[string]any)
	if !ok {
		return
	}
	for _, m := range modules {
		if module, ok := m.(map[string]any); ok {
			delete(module, "parameters")
		}
	}
}

// declarationKeys is the declaration vocabulary: the keys a parameter
// declaration may carry, read from the field tags of the type every declaration
// decodes into. A declaration nested under properties, items or valuesType
// decodes into the same type, so the one vocabulary holds at every level.
//
// It is derived rather than written out, so there is no second list of the
// vocabulary to fall out of step with the type that reads a declaration.
var declarationKeys = decodedKeys(reflect.TypeFor[types.ParameterContract]())

// The declaration keys the conversion treats by name. declarationValueKey holds a
// value and is withheld; declarationFieldsKey holds a mapping of named nested
// declarations; each of nestedDeclarationKeys holds one nested declaration.
const (
	declarationValueKey  = "default"
	declarationFieldsKey = "properties"
)

var nestedDeclarationKeys = []string{"items", "valuesType"}

// decodedKeys returns the mapping keys the YAML decoder reads into the fields of
// struct type t: the name each exported field's tag gives it. A field the decoder
// names any other way — one whose tag gives no name, or one inlined into its
// parent — contributes no key, so the set can be narrower than what the decoder
// reads and never wider: a key it lacks is withheld from a plugin rather than
// handed to one.
func decodedKeys(t reflect.Type) map[string]bool {
	keys := map[string]bool{}
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		keys[name] = true
	}
	return keys
}

// withOnlyDeclarations holds the parameter declarations under an Aggregate spec's
// parameters key to their contracts, in place.
func withOnlyDeclarations(spec map[string]any) {
	params, present := spec["parameters"]
	if !present {
		return
	}
	declarations, ok := params.(map[string]any)
	if !ok {
		delete(spec, "parameters")
		return
	}
	withOnlyContractsIn(declarations)
}

// withOnlyContractsIn holds each declaration of a mapping keyed by name to its
// contract, and removes an entry that is not a declaration. The keys are names, so
// a parameter or a nested field called "default", or called like no key of the
// vocabulary, keeps its place.
func withOnlyContractsIn(declarations map[string]any) {
	for name, d := range declarations {
		if !withOnlyContract(d) {
			delete(declarations, name)
		}
	}
}

// withOnlyContract holds one declaration, and every declaration nested in it, to
// the keys of the declaration vocabulary less the default, and reports whether d
// is a declaration at all. A nested position holding something other than a
// declaration is removed.
func withOnlyContract(d any) bool {
	declaration, ok := d.(map[string]any)
	if !ok {
		return false
	}
	for key := range declaration {
		if key == declarationValueKey || !declarationKeys[key] {
			delete(declaration, key)
		}
	}
	if properties, present := declaration[declarationFieldsKey]; present {
		if fields, ok := properties.(map[string]any); ok {
			withOnlyContractsIn(fields)
		} else {
			delete(declaration, declarationFieldsKey)
		}
	}
	for _, key := range nestedDeclarationKeys {
		if nested, present := declaration[key]; present && !withOnlyContract(nested) {
			delete(declaration, key)
		}
	}
	return true
}

// specOf returns a parsed document's spec mapping, when it has one.
func specOf(tree any) (map[string]any, bool) {
	root, ok := tree.(map[string]any)
	if !ok {
		return nil, false
	}
	spec, ok := root["spec"].(map[string]any)
	return spec, ok
}

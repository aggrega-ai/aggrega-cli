// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
)

// The parameter attributes the binding reads, and the parameter types it
// converts a literal to.
const (
	paramType          = "type"
	paramRef           = "$ref"
	paramDefault       = "defaultValue"
	paramNullable      = "nullable"
	paramAllowedValues = "allowedValues"
	paramMinLength     = "minLength"
	paramMaxLength     = "maxLength"
	paramMinValue      = "minValue"
	paramMaxValue      = "maxValue"

	typeString       = "string"
	typeInt          = "int"
	typeBool         = "bool"
	typeArray        = "array"
	typeObject       = "object"
	typeSecureString = "securestring"
	typeSecureObject = "secureobject"

	// definitionsPrefix is the start of a $ref into the template's definitions.
	definitionsPrefix = "#/" + sectionDefinitions + "/"
)

// bindParameters reads a node's values mapping as the parameters of the nested
// deployment that carries template, and returns the properties.parameters block.
//
// Each key names a parameter the template declares, without regard to case, as
// ARM reads parameter names, and is written under the declared spelling. A
// string that is wholly an expression is written as the parameter's value and
// evaluated by ARM in the parent template's scope. An object that is exactly a
// Key Vault secret reference is written as the parameter's entry, for a
// securestring or secureObject parameter only. Any other value is a literal: it
// is converted to the parameter's declared type and checked against its
// allowedValues, length and value limits, except that a parameter typed by a
// $ref into definitions takes it as written for ARM to check. Every string of a
// value is ARM text at any depth: one inside a list or object that ARM reads as
// an expression must parse and is kept as written, and text the binding itself
// produces by converting a number or boolean to a string is written with
// ArmLiteral. A literal is refused for a secure
// parameter, and for every parameter when the mapping read a sensitive value. A
// parameter with no default that admits no null and has no key is refused,
// because a nested deployment's parameters cannot be supplied when it runs.
func bindParameters(n SourcedNode, template map[string]any) (map[string]any, error) {
	where := fmt.Sprintf("the template sourced for instance %q", n.Identity)
	declared, _ := template[sectionParameters].(map[string]any)
	definitions, _ := template[sectionDefinitions].(map[string]any)
	supplied, err := suppliedValues(n)
	if err != nil {
		return nil, err
	}

	declaredByFold := map[string]string{}
	for _, name := range sortedKeys(declared) {
		if _, taken := declaredByFold[strings.ToLower(name)]; !taken {
			declaredByFold[strings.ToLower(name)] = name
		}
	}
	keyByDeclared := map[string]string{}
	keyByFold := map[string]string{}
	var unknown []string
	for _, key := range sortedKeys(supplied) {
		fold := strings.ToLower(key)
		if other, dup := keyByFold[fold]; dup {
			return nil, refuse(plugincondition.ErrPluginOutputNameCollision,
				fmt.Sprintf("the values mapping of %s writes the keys %q and %q, which ARM reads as one parameter name without regard to case; write one of them", where, other, key))
		}
		keyByFold[fold] = key
		name, ok := declaredByFold[fold]
		if !ok {
			unknown = append(unknown, key)
			continue
		}
		keyByDeclared[name] = key
	}
	if len(unknown) > 0 {
		return nil, refuse(plugincondition.ErrPluginRequiredInputUnresolved,
			fmt.Sprintf("the values mapping of %s names %s, which the template does not declare as a parameter; %s; correct the key",
				where, quoted(unknown), declares(sortedKeys(declared))))
	}

	bound := map[string]any{}
	var sensitiveLiterals, unbound []string
	for _, name := range sortedKeys(declared) {
		decl, _ := declared[name].(map[string]any)
		key, mapped := keyByDeclared[name]
		if !mapped {
			if _, hasDefault := decl[paramDefault]; !hasDefault && decl[paramNullable] != true {
				unbound = append(unbound, name)
			}
			continue
		}
		entry, literal, err := bindOne(where, name, decl, definitions, supplied[key])
		if err != nil {
			return nil, err
		}
		if literal && n.SensitiveValues {
			sensitiveLiterals = append(sensitiveLiterals, name)
		}
		bound[name] = entry
	}
	if len(sensitiveLiterals) > 0 {
		return nil, refuse(plugincondition.ErrPluginValueNotRepresentable,
			fmt.Sprintf("the values mapping of %s reads a value declared sensitive, so a literal it produces is refused, and %s would write a literal into the template as it stands; bind each to an expression or a Key Vault reference",
				where, parameterList(sensitiveLiterals)))
	}
	if len(unbound) > 0 {
		return nil, refuse(plugincondition.ErrPluginRequiredInputUnresolved,
			fmt.Sprintf("%s has no value mapped to %s, which declares no default and does not admit null; a nested deployment's parameters cannot be supplied when it runs, so map each one, writing %s to read a top-level parameter the Recipe's artifacts fragment declares",
				where, parameterList(unbound), parameterReads(unbound)))
	}
	return bound, nil
}

// suppliedValues returns the values mapping as a tree of plain JSON values:
// objects, lists, strings, booleans and numbers kept as written.
func suppliedValues(n SourcedNode) (map[string]any, error) {
	raw, err := json.Marshal(n.Values.TemplateAlphabeticalMapDeep())
	if err != nil {
		return nil, valueNotRepresentable(fmt.Sprintf("arm-provider: the values mapping of the template sourced for instance %q cannot be written as JSON: %v", n.Identity, err))
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, valueNotRepresentable(fmt.Sprintf("arm-provider: the values mapping of the template sourced for instance %q cannot be read back as JSON: %v", n.Identity, err))
	}
	return out, nil
}

// bindOne returns the properties.parameters entry for one value and whether the
// value was a literal.
func bindOne(where, name string, decl, definitions map[string]any, value any) (entry any, literal bool, err error) {
	notRepresentable := func(format string, args ...any) error {
		return refuse(plugincondition.ErrPluginValueNotRepresentable,
			fmt.Sprintf("the value mapped to parameter %q of %s ", name, where)+fmt.Sprintf(format, args...))
	}
	typ, byRef := parameterType(decl, definitions)
	secure := typ == typeSecureString || typ == typeSecureObject

	if text, ok := value.(string); ok && armexpr.IsExpression(text) {
		if _, err := armexpr.Parse(text); err != nil {
			reason, offset := err.Error(), 0
			if se, ok := err.(*armexpr.SyntaxError); ok {
				reason, offset = se.Reason, se.Offset
			}
			return nil, false, notRepresentable("is read by ARM as an expression but does not parse (offset %d: %s); correct the expression, or write [[ at its start to keep it literal text", offset, reason)
		}
		return map[string]any{"value": text}, false, nil
	}
	if isKeyVaultReference(value) {
		if !secure {
			return nil, false, notRepresentable("is a Key Vault reference, which ARM accepts only for a securestring or secureObject parameter; declare the parameter as one, or map an expression")
		}
		return value, false, nil
	}
	if secure {
		return nil, true, notRepresentable("is a literal for a %s parameter, which would write the secret into the template as text; map a Key Vault reference or an expression", typeName(decl, definitions))
	}
	if err := checkNestedExpressions(value, "", notRepresentable); err != nil {
		return nil, true, err
	}
	if byRef || typ == "" {
		return map[string]any{"value": value}, true, nil
	}

	converted, ok := convertLiteral(typ, value)
	if !ok {
		return nil, true, notRepresentable("is not %s, the declared type %q of the parameter; map a value of that type", article(typ), typ)
	}
	if constraint := violatedConstraint(decl, converted); constraint != "" {
		return nil, true, notRepresentable("does not satisfy the %s of the parameter, which ARM would refuse when the deployment runs; map a value the parameter admits", constraint)
	}
	return map[string]any{"value": converted}, true, nil
}

// parameterType returns the lower-cased type a parameter declares and whether it
// declares it through a $ref into the template's definitions. A $ref is followed
// one hop; a $ref that resolves to nothing has no type.
func parameterType(decl, definitions map[string]any) (typ string, byRef bool) {
	if t, ok := decl[paramType].(string); ok {
		return strings.ToLower(t), false
	}
	ref, ok := decl[paramRef].(string)
	if !ok {
		return "", false
	}
	if name, ok := strings.CutPrefix(ref, definitionsPrefix); ok {
		if d, ok := definitions[name].(map[string]any); ok {
			t, _ := d[paramType].(string)
			return strings.ToLower(t), true
		}
	}
	return "", true
}

// typeName returns the type a parameter declares, as written, for a sentence.
func typeName(decl, definitions map[string]any) string {
	if t, ok := decl[paramType].(string); ok {
		return t
	}
	if ref, ok := decl[paramRef].(string); ok {
		if name, ok := strings.CutPrefix(ref, definitionsPrefix); ok {
			if d, ok := definitions[name].(map[string]any); ok {
				if t, ok := d[paramType].(string); ok {
					return t
				}
			}
		}
	}
	return "secure"
}

// isKeyVaultReference reports whether v is exactly an object of the form
// {reference: {keyVault: {id}, secretName[, secretVersion]}}, the shape ARM reads
// as a Key Vault secret in place of a parameter's value.
func isKeyVaultReference(v any) bool {
	top, ok := v.(map[string]any)
	if !ok || len(top) != 1 {
		return false
	}
	ref, ok := top["reference"].(map[string]any)
	if !ok {
		return false
	}
	for k := range ref {
		if k != "keyVault" && k != "secretName" && k != "secretVersion" {
			return false
		}
	}
	vault, ok := ref["keyVault"].(map[string]any)
	if !ok || len(vault) != 1 {
		return false
	}
	if _, ok := vault["id"].(string); !ok {
		return false
	}
	if _, ok := ref["secretName"].(string); !ok {
		return false
	}
	if version, present := ref["secretVersion"]; present {
		if _, ok := version.(string); !ok {
			return false
		}
	}
	return true
}

// convertLiteral converts a literal to the declared type: text for a string, a
// whole number for an int, true or false for a bool, a list for an array and an
// object for an object. A scalar of another kind is read as ARM reads the same
// scalar written in a parameters file: a number or boolean given to a string
// parameter becomes its text, and text given to an int or bool parameter must
// read as one. A type the binding does not convert is returned as written.
func convertLiteral(typ string, v any) (any, bool) {
	switch typ {
	case typeString:
		switch x := v.(type) {
		case string:
			return x, true
		case json.Number:
			return ArmLiteral(x.String()), true
		case bool:
			return ArmLiteral(strconv.FormatBool(x)), true
		}
	case typeInt:
		switch x := v.(type) {
		case json.Number:
			if _, err := strconv.ParseInt(x.String(), 10, 64); err == nil {
				return x, true
			}
		case string:
			if _, err := strconv.ParseInt(x, 10, 64); err == nil {
				return json.Number(x), true
			}
		}
	case typeBool:
		switch x := v.(type) {
		case bool:
			return x, true
		case string:
			if b, err := strconv.ParseBool(strings.ToLower(x)); err == nil && (strings.EqualFold(x, "true") || strings.EqualFold(x, "false")) {
				return b, true
			}
		}
	case typeArray:
		if x, ok := v.([]any); ok {
			return x, true
		}
	case typeObject:
		if x, ok := v.(map[string]any); ok {
			return x, true
		}
	default:
		return v, true
	}
	return nil, false
}

// violatedConstraint returns the name of the first constraint of a parameter that
// a converted literal does not satisfy, or "" when it satisfies them all.
func violatedConstraint(decl map[string]any, v any) string {
	if allowed, ok := decl[paramAllowedValues].([]any); ok {
		got := canonical(v)
		admitted := false
		for _, a := range allowed {
			if canonical(a) == got {
				admitted = true
				break
			}
		}
		if !admitted {
			return paramAllowedValues
		}
	}
	size := -1
	switch x := v.(type) {
	case string:
		size = utf8.RuneCountInString(x)
	case []any:
		size = len(x)
	}
	if size >= 0 {
		if limit, ok := limitOf(decl[paramMinLength]); ok && float64(size) < limit {
			return paramMinLength
		}
		if limit, ok := limitOf(decl[paramMaxLength]); ok && float64(size) > limit {
			return paramMaxLength
		}
	}
	if num, ok := v.(json.Number); ok {
		n, err := num.Float64()
		if err == nil {
			if limit, ok := limitOf(decl[paramMinValue]); ok && n < limit {
				return paramMinValue
			}
			if limit, ok := limitOf(decl[paramMaxValue]); ok && n > limit {
				return paramMaxValue
			}
		}
	}
	return ""
}

// limitOf reads a constraint's number.
func limitOf(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case float64:
		return x, true
	}
	return 0, false
}

// canonical writes a value as JSON with sorted keys, so two values compare equal
// exactly when they hold the same data.
func canonical(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// checkNestedExpressions walks a literal's lists and objects in sorted key
// order and refuses the first string inside one that ARM reads as an expression
// and that does not parse. Every string of a bound value is ARM text at any
// depth: an expression is kept as written and evaluated in the parent scope, a
// string opening with a doubled bracket and plain text are kept as written.
func checkNestedExpressions(v any, pointer string, notRepresentable func(string, ...any) error) error {
	switch x := v.(type) {
	case string:
		if pointer == "" || !armexpr.IsExpression(x) {
			return nil
		}
		if _, err := armexpr.Parse(x); err != nil {
			reason, offset := err.Error(), 0
			if se, ok := err.(*armexpr.SyntaxError); ok {
				reason, offset = se.Reason, se.Offset
			}
			return notRepresentable("holds, at JSON pointer %s inside the value, text ARM reads as an expression that does not parse (offset %d: %s); correct the expression, or write [[ at its start to keep it literal text", pointer, offset, reason)
		}
	case []any:
		for i, e := range x {
			if err := checkNestedExpressions(e, fmt.Sprintf("%s/%d", pointer, i), notRepresentable); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, k := range sortedKeys(x) {
			if err := checkNestedExpressions(x[k], pointer+"/"+escapePointer(k), notRepresentable); err != nil {
				return err
			}
		}
	}
	return nil
}

// article returns a type name with the article a sentence needs.
func article(typ string) string {
	if typ == typeInt || typ == typeArray || typ == typeObject {
		return "an " + typ
	}
	return "a " + typ
}

// quoted writes names as a quoted, comma-separated list.
func quoted(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = strconv.Quote(n)
	}
	return strings.Join(q, ", ")
}

// parameterList writes names as "parameter "a"" or "parameters "a", "b"".
func parameterList(names []string) string {
	if len(names) == 1 {
		return "parameter " + quoted(names)
	}
	return "parameters " + quoted(names)
}

// declares says which parameters a template declares.
func declares(names []string) string {
	if len(names) == 0 {
		return "the template declares no parameters"
	}
	return "the template declares " + quoted(names)
}

// parameterReads writes the expression that reads each named top-level
// parameter, as a comma-separated list.
func parameterReads(names []string) string {
	reads := make([]string, len(names))
	for i, n := range names {
		reads[i] = "[parameters(" + StringLiteral(n) + ")]"
	}
	return strings.Join(reads, ", ")
}

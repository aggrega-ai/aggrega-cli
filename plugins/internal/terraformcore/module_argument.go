// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package terraformcore

import (
	"encoding/json"
	"strings"
)

// hclDirectiveIntroducer is the two-byte sequence that opens a template
// directive inside a quoted Terraform template, and hclDirectiveEscape is
// Terraform's own escape for it: writing the escape produces the two bytes
// literally instead of opening a directive.
//
// The escape matters because a module-block argument is an expression rather
// than a datum. Its bytes are parsed as Terraform template syntax, so a value
// that happens to contain the introducer would be read as the start of a
// directive and the configuration would not parse at all. Escaping it is what
// makes such a value render as itself.
//
// The doubled percent sign is an escape only immediately before the brace: a
// doubled percent anywhere else is two literal characters. So this exact pair is
// the only sequence that may be rewritten, and a value carrying percent signs
// alone must be left alone.
const (
	hclDirectiveIntroducer = "%{"
	hclDirectiveEscape     = "%%{"
)

// ModuleArgumentExpression encodes one of a node's rendered values as the
// Terraform expression a `module` block argument carries.
//
// A module-block argument is an expression, not a datum, and the JSON encoding
// of a value is already valid expression syntax for every shape a rendered value
// takes: a string is a quoted template, a number and a boolean are themselves,
// an array is a tuple constructor, and an object is an object constructor —
// Terraform accepts a colon between an object key and its value as readily as an
// equals sign. Every JSON string escape is also a Terraform string escape, so a
// quote, a backslash, a newline, a tab and a `\uXXXX` sequence all carry through
// unchanged and no second encoder is needed for them.
//
// The one sequence JSON leaves alone and Terraform does not is the template
// directive introducer, which is escaped here — see [hclDirectiveIntroducer].
// Rewriting it across the whole encoded document rather than inside each string
// separately is equivalent rather than merely convenient: those two bytes cannot
// occur in JSON outside a string literal, because a percent sign appears only
// inside a string and a string always closes with a quote before the next brace
// can open.
//
// An interpolation is deliberately NOT escaped, and that is the point of
// emitting values here at all. A value written as an interpolation is a
// deterministic ahead-of-time pointer the Recipe supplies: nothing resolves it
// while the configuration is being written, and a module-block argument is a
// position the engine evaluates, so the pointer arrives somewhere the engine can
// resolve it. A value carrying no interpolation renders as itself.
func ModuleArgumentExpression(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(encoded), hclDirectiveIntroducer, hclDirectiveEscape), nil
}

// nameLeadByte reports whether c may open a Terraform name, and nameByte
// whether c may appear later in one: a name begins with a letter or an
// underscore and may then carry letters, digits, underscores and dashes.
//
// The grammar has one home here because two callers need it for opposite
// purposes — [ModuleName] maps an identifier into it, and
// [ValidModuleArgumentName] refuses one that falls outside it — and a second
// copy that agreed today would be the thing that hid a later divergence.
func nameLeadByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func nameByte(c byte) bool {
	return nameLeadByte(c) || c >= '0' && c <= '9' || c == '-'
}

// ValidModuleArgumentName reports whether name can be written as a `module`
// block argument.
//
// An argument's name must match the input variable it binds, so unlike a module
// name it cannot be mapped into the grammar — mapping it would bind a variable
// the module never declared. A name outside the grammar is therefore refused by
// the caller rather than corrected, because the alternative is emitting a
// configuration that does not parse: a syntax error in generated output points a
// reader at a file nobody wrote.
//
// A module input variable's name is subject to the same grammar, so no name a
// module can declare is refused here.
func ValidModuleArgumentName(name string) bool {
	if name == "" || !nameLeadByte(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !nameByte(name[i]) {
			return false
		}
	}
	return true
}

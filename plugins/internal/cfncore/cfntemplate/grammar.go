// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfntemplate

import (
	"regexp"
	"strings"
)

// shortFormTags are the YAML short forms CloudFormation defines for its intrinsic
// functions, each written as the tag an author types, without the "!".
var shortFormTags = map[string]bool{
	"Ref": true, "Condition": true, "GetAtt": true, "Base64": true, "Cidr": true,
	"FindInMap": true, "GetAZs": true, "ImportValue": true, "Join": true, "Select": true,
	"Split": true, "Sub": true, "Transform": true, "And": true, "Equals": true,
	"If": true, "Not": true, "Or": true,
}

// IsShortFormTag reports whether tag, written without its "!", is one of the YAML
// short forms CloudFormation defines for an intrinsic function that a bound value
// may open with.
func IsShortFormTag(tag string) bool {
	return shortFormTags[tag]
}

// LooksLikeShortForm reports whether s is wholly one short-form intrinsic as an
// author writes it in a value: "!", a short-form tag, then a space or the end of
// the string. A "!!" tag, an unknown tag, text that merely contains a short form
// and a string with a carriage return or a line feed are not.
func LooksLikeShortForm(s string) bool {
	if !isBangTag(s) || strings.ContainsAny(s, "\r\n") {
		return false
	}
	tag, _, _ := strings.Cut(s[1:], " ")
	return IsShortFormTag(tag)
}

// IsIntrinsicKey reports whether key, as the one member of a mapping, makes the
// mapping one intrinsic function in its long form: Ref, Condition, or a name
// opening Fn::.
func IsIntrinsicKey(key string) bool {
	return key == "Ref" || key == "Condition" || strings.HasPrefix(key, "Fn::")
}

// isBangTag reports whether s opens with a single "!", the way a custom tag does, as
// opposed to a "!!" standard tag.
func isBangTag(s string) bool {
	return strings.HasPrefix(s, "!") && !strings.HasPrefix(s, "!!")
}

// dynamicReference matches a value that is wholly one CloudFormation dynamic
// reference: {{resolve:ssm:…}}, {{resolve:ssm-secure:…}} or
// {{resolve:secretsmanager:…}}.
var dynamicReference = regexp.MustCompile(`^\{\{resolve:(ssm|ssm-secure|secretsmanager):[^{}]+\}\}$`)

// IsDynamicReference reports whether s is wholly one dynamic reference, the form
// CloudFormation resolves when the stack is created or updated, so the value it
// names is never written into the template.
func IsDynamicReference(s string) bool {
	return dynamicReference.MatchString(s)
}

// SubParts splits an Fn::Sub argument into its string and its variable map. The
// string form has an empty map; the list form carries the string first and the map
// second; any other shape is an empty string and an empty map.
func SubParts(arg any) (text string, vars map[string]any) {
	switch a := arg.(type) {
	case string:
		return a, map[string]any{}
	case []any:
		if len(a) == 0 {
			return "", map[string]any{}
		}
		text, _ = a[0].(string)
		if len(a) > 1 {
			if vars, ok := a[1].(map[string]any); ok {
				return text, vars
			}
		}
		return text, map[string]any{}
	default:
		return "", map[string]any{}
	}
}

// SubPiece is one run of an Fn::Sub string: literal text, or the name between
// "${" and "}" of a variable.
type SubPiece struct {
	// Text is the literal text, with each ${!x} written as the text ${x}; for a
	// variable it is the name as written, such as "Foo" or "Foo.Arn".
	Text string
	// Variable reports that Text names a variable to substitute.
	Variable bool
}

// SubPieces splits an Fn::Sub string into its literal runs and its variables in the
// order written. A ${!x} is literal text and reads as ${x}; a "${" with no closing
// "}" and an empty ${} are literal text. Adjacent literal text is one piece.
func SubPieces(text string) []SubPiece {
	var pieces []SubPiece
	var literal strings.Builder
	flush := func() {
		if literal.Len() > 0 {
			pieces = append(pieces, SubPiece{Text: literal.String()})
			literal.Reset()
		}
	}
	for rest := text; rest != ""; {
		open := strings.Index(rest, "${")
		if open < 0 {
			literal.WriteString(rest)
			break
		}
		end := strings.IndexByte(rest[open+2:], '}')
		if end < 0 {
			literal.WriteString(rest)
			break
		}
		name := rest[open+2 : open+2+end]
		literal.WriteString(rest[:open])
		rest = rest[open+2+end+1:]
		switch {
		case name == "":
			literal.WriteString("${}")
		case strings.HasPrefix(name, "!"):
			literal.WriteString("${" + name[1:] + "}")
		default:
			flush()
			pieces = append(pieces, SubPiece{Text: name, Variable: true})
		}
	}
	flush()
	return pieces
}

// SubVariables returns the variables an Fn::Sub string names, each once, in the
// order they first appear. A ${!x} is literal text and is not returned.
func SubVariables(text string) []string {
	var vars []string
	seen := map[string]bool{}
	for _, p := range SubPieces(text) {
		if p.Variable && !seen[p.Text] {
			seen[p.Text] = true
			vars = append(vars, p.Text)
		}
	}
	return vars
}

// GetAttLongForm shapes the argument of an Fn::GetAtt into the long-form list: a
// string splits at its first dot into [logicalId, attribute], a string with no dot
// is the one-element list [logicalId], and any other value passes through.
func GetAttLongForm(arg any) any {
	s, ok := arg.(string)
	if !ok {
		return arg
	}
	if id, attribute, found := strings.Cut(s, "."); found {
		return []any{id, attribute}
	}
	return []any{s}
}

// GetAttParts reads an Fn::GetAtt argument, in its list form or as one string split
// at its first dot, into the logical id and the attribute. The attribute is empty
// when the argument names none or names one that is not text. ok is false when no
// logical id is named.
func GetAttParts(arg any) (logicalID, attribute string, ok bool) {
	parts, isList := GetAttLongForm(arg).([]any)
	if !isList || len(parts) == 0 {
		return "", "", false
	}
	logicalID, _ = parts[0].(string)
	if logicalID == "" {
		return "", "", false
	}
	if len(parts) > 1 {
		attribute, _ = parts[1].(string)
	}
	return logicalID, attribute, true
}

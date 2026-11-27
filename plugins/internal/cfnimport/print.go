// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfncore/cfntemplate"
)

// Deploy-time text for the cloudformation target is written as one line of
// YAML short form. A catalog Recipe places a value bare, through toJson, or
// inside a double-quoted scalar, and the provider's fragment reader reads a
// string that is wholly one short form as that intrinsic function, so the one
// line reads back as the function in each placement where it holds no double
// quote, no backslash and no line break, which a double-quoted scalar would
// read otherwise.

// shortForm prints a long-form intrinsic as one line of YAML short form, its
// strings single-quoted, that cfncore's fragment reader reads back to the same
// object whether a Recipe places it bare, through toJson or inside a
// double-quoted scalar; false where none exists (a string holding a double
// quote, a backslash or a line break, a non-intrinsic object, an
// Fn::GetStackOutput, which takes a mapping).
//
// v is a long form as encoding/json decodes it, its numbers json.Number or
// float64. A string is written plain where a flow scalar reads it back as the
// same text, and single-quoted otherwise, a single quote doubled. A function
// directly under a short-form tag is written in its long form, a flow mapping,
// as YAML gives a node one tag; anywhere else a function is written in its
// short form where it has one. A mapping's members are written in key order,
// the order the template reader writes a long form's members in. A string
// that is itself wholly a short form, which the fragment reader would read as
// the function, and any control character are refused, as is a scalar other
// than text directly under a tag, which the tag would read as text.
func shortForm(v any) (string, bool) {
	tag, arg, ok := shortFormOf(v)
	if !ok {
		return "", false
	}
	var w flowWriter
	w.tagged(tag, arg)
	if w.refused {
		return "", false
	}
	return w.b.String(), true
}

// shortFormOf returns the tag and the argument of the short form v is
// written in: v is one intrinsic function, a mapping of one member named Ref,
// Condition or Fn:: and the name of a function whose short form cfntemplate
// knows; false for any other value, and for Fn::Transform, which takes a
// mapping.
func shortFormOf(v any) (tag string, arg any, ok bool) {
	m, isMap := v.(map[string]any)
	if !isMap || len(m) != 1 {
		return "", nil, false
	}
	for key, value := range m {
		switch {
		case key == "Ref" || key == "Condition":
			return key, value, true
		case strings.HasPrefix(key, "Fn::"):
			name := strings.TrimPrefix(key, "Fn::")
			if cfntemplate.IsShortFormTag(name) && name != "Ref" && name != "Condition" && name != "Transform" {
				return name, value, true
			}
		}
	}
	return "", nil, false
}

// flowWriter writes a long form as YAML flow text on one line, refused where
// a part of it has no such text.
type flowWriter struct {
	b       strings.Builder
	refused bool
}

// tagged writes the short form of the function tag reading arg.
func (w *flowWriter) tagged(tag string, arg any) {
	w.b.WriteString("!" + tag + " ")
	switch a := arg.(type) {
	case string:
		w.text(a)
	case []any:
		w.list(a)
	case map[string]any:
		w.mapping(a)
	default:
		w.refused = true
	}
}

// node writes v as an item of a flow sequence or the value of a flow
// mapping's member, where a tag may open it.
func (w *flowWriter) node(v any) {
	switch v := v.(type) {
	case map[string]any:
		if tag, arg, ok := shortFormOf(v); ok {
			w.tagged(tag, arg)
			return
		}
		w.mapping(v)
	case []any:
		w.list(v)
	case string:
		w.text(v)
	case json.Number:
		w.b.WriteString(v.String())
	case float64:
		w.b.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	case bool:
		w.b.WriteString(strconv.FormatBool(v))
	case nil:
		w.b.WriteString("null")
	default:
		w.refused = true
	}
}

// list writes items as a flow sequence.
func (w *flowWriter) list(items []any) {
	w.b.WriteString("[")
	for i, item := range items {
		if i > 0 {
			w.b.WriteString(", ")
		}
		w.node(item)
	}
	w.b.WriteString("]")
}

// mapping writes m as a flow mapping, its members in key order.
func (w *flowWriter) mapping(m map[string]any) {
	w.b.WriteString("{")
	for i, key := range slices.Sorted(maps.Keys(m)) {
		if i > 0 {
			w.b.WriteString(", ")
		}
		w.text(key)
		w.b.WriteString(": ")
		w.node(m[key])
	}
	w.b.WriteString("}")
}

// text writes s as a flow scalar: plain where it reads back as itself,
// single-quoted otherwise.
func (w *flowWriter) text(s string) {
	switch {
	case !writable(s):
		w.refused = true
	case plain(s):
		w.b.WriteString(s)
	default:
		w.b.WriteString("'" + strings.ReplaceAll(s, "'", "''") + "'")
	}
}

// writable reports whether deploy-time text can hold s as written wherever a
// Recipe places it: s holds no double quote, no backslash and no control
// character — a line break among them — nor a character YAML reads as a line
// break or does not admit in a document, and is not itself wholly a short
// form, which the fragment reader would read as the function.
func writable(s string) bool {
	for _, r := range s {
		if r == '"' || r == '\\' || unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || r == '\ufeff' {
			return false
		}
	}
	return !cfntemplate.LooksLikeShortForm(s)
}

// plainText matches text a flow scalar writes unquoted and reads back as
// itself: a word of letters, digits and _ . : / -, opening with a letter and
// not closing with a colon, so YAML reads it as text and no colon in it
// introduces a value.
var plainText = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:/-]*[A-Za-z0-9_./-]$|^[A-Za-z]$`)

// yamlWords are the plain words YAML reads as a boolean or as null, matched
// regardless of case.
var yamlWords = []string{"true", "false", "null", "yes", "no", "on", "off", "y", "n"}

// plain reports whether s is written as a plain scalar.
func plain(s string) bool {
	return plainText.MatchString(s) && !slices.Contains(yamlWords, strings.ToLower(s))
}

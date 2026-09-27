// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package selector

import (
	"fmt"
	"strings"
	"unicode"
)

// Operator is the closed set of label-selector operators this package
// recognises in a parsed Requirement. The six exported constants below are
// the only valid values; callers compare against the constants and never
// against the underlying string. The string-typed backing exists only so the
// constants double as the literal tokens emitted by the string-selector
// parser ("=", "!=", "in", "notin"); the Exists / DoesNotExist pair has no
// surface token of its own (a bare key and a "!key" prefix produce them).
type Operator string

const (
	OpEquals       Operator = "="
	OpNotEquals    Operator = "!="
	OpIn           Operator = "in"
	OpNotIn        Operator = "notin"
	OpExists       Operator = "exists"
	OpDoesNotExist Operator = "doesnotexist"
)

// Requirement is one parsed label-selector clause: a key, an operator, and
// (for value-bearing operators) one or more values. Exists / DoesNotExist
// requirements carry an empty Values slice — the operator alone determines
// the predicate. Equals / NotEquals carry exactly one Values entry; In /
// NotIn carry one or more.
//
// Requirement is the internal currency of the string-selector path
// (Parse → matchRequirement in selector.go). Callers never construct
// Requirement directly; every selector surface — `usesSchema.selector`,
// attachment-slot `selector`, Feature `appliesTo`, and the string-form items
// of `appliesToAnyOf` — reaches it through Parse. The flat-map arm of an `appliesToAnyOf` item
// bypasses Requirement entirely via MatchLabels.
type Requirement struct {
	Key      string
	Operator Operator
	Values   []string // empty for Exists/DoesNotExist
}

// SyntaxError reports a requirement the selector grammar does not define. It is
// the one error Parse returns, so a caller telling a malformed selector apart
// from any other failure matches it with errors.As rather than by its text.
//
// Position is the requirement's 1-based place among the selector's
// comma-separated requirements and Requirement is its text with surrounding
// whitespace removed, which is empty for an empty requirement. Reason says,
// in words, what the requirement carries or lacks.
type SyntaxError struct {
	Position    int
	Requirement string
	Reason      string
}

// Error names the requirement that could not be classified — by its text, or
// by its position when it is empty — and why.
func (e *SyntaxError) Error() string {
	if e.Requirement == "" {
		return fmt.Sprintf("selector: requirement %d is empty: %s", e.Position, e.Reason)
	}
	return fmt.Sprintf("selector: requirement %d, %q: %s", e.Position, e.Requirement, e.Reason)
}

// Parse parses a Kubernetes-style label-selector string into a slice of
// Requirement values. Comma-separated requirements combine with AND
// semantics (every requirement must hold for the selector to match);
// parentheses around In / NotIn value sets are not split. An empty or
// whitespace-only selector returns (nil, nil), which downstream callers
// treat as "no constraint" — see MatchString in selector.go.
//
// Supported forms (one per requirement):
//   - key=value      (Equals)
//   - key!=value     (NotEquals)
//   - key in (v1,v2) (In)
//   - key notin (v1,v2) (NotIn)
//   - key            (Exists)
//   - !key           (DoesNotExist)
//
// A selector that is not a sequence of those forms is refused with a
// *SyntaxError naming the requirement it could not classify, and no
// requirement of it is returned. The refusals are:
//
//   - an empty requirement — a leading, trailing or doubled comma, or a
//     selector made of separators alone;
//   - a key that is empty, or that carries whitespace or any of the
//     characters the grammar itself uses: = ! ( ) and the comma. This is
//     what refuses "!tier=frontend" (a "!" requirement names a key and
//     nothing else), "tier in frontend" (a set without its parentheses) and
//     "tier: frontend";
//   - " in (" or " notin (" not followed by a value list closed by ")".
//
// Values are not held to any character set. A label value is whatever the
// author wrote, so a value is read as the text after the operator — in
// "tier==frontend" that is "=frontend", which matches only a label whose
// value is exactly that. Surrounding whitespace is trimmed from every key and
// value.
func Parse(selector string) ([]Requirement, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, nil
	}

	var reqs []Requirement
	parts := splitRequirements(selector)
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, &SyntaxError{
				Position: i + 1,
				Reason:   "requirements are separated by single commas, so a leading, trailing or doubled comma leaves an empty one",
			}
		}
		req, reason := parseOne(part)
		if reason != "" {
			return nil, &SyntaxError{Position: i + 1, Requirement: part, Reason: reason}
		}
		reqs = append(reqs, req)
	}
	return reqs, nil
}

// splitRequirements splits a selector string on commas that are not inside parentheses.
func splitRequirements(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i, ch := range s {
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// parseOne classifies one trimmed, non-empty requirement into one of the six
// forms. The second return is empty on success and otherwise says why the
// requirement fits none of them.
//
// Each form's key is checked by the same rule, and the rule is about the
// grammar rather than about labels: a key may not carry the characters the
// grammar uses to tell one form from another, because a requirement whose key
// carried one would be read as a different form, or as a key it does not name.
// Nothing else about a key or a value is judged.
func parseOne(s string) (Requirement, string) {
	// !key (does not exist)
	if strings.HasPrefix(s, "!") {
		key := strings.TrimSpace(s[1:])
		if key == "" {
			return Requirement{}, `"!" is followed by no label key; "!key" tests that the label key is absent`
		}
		if reason := keyDefect(key); reason != "" {
			return Requirement{}, notAbsenceKey(key, reason)
		}
		return Requirement{Key: key, Operator: OpDoesNotExist}, ""
	}

	// key in (v1,v2) or key notin (v1,v2)
	if idx := strings.Index(s, " in ("); idx > 0 {
		return parseSetOperator(strings.TrimSpace(s[:idx]), OpIn, s[idx+4:])
	}
	if idx := strings.Index(s, " notin ("); idx > 0 {
		return parseSetOperator(strings.TrimSpace(s[:idx]), OpNotIn, s[idx+7:])
	}

	// key!=value
	if idx := strings.Index(s, "!="); idx > 0 {
		key := strings.TrimSpace(s[:idx])
		if reason := keyDefect(key); reason != "" {
			return Requirement{}, reason
		}
		val := strings.TrimSpace(s[idx+2:])
		return Requirement{Key: key, Operator: OpNotEquals, Values: []string{val}}, ""
	}

	// key=value
	if idx := strings.Index(s, "="); idx >= 0 {
		key := strings.TrimSpace(s[:idx])
		if reason := keyDefect(key); reason != "" {
			return Requirement{}, reason
		}
		val := strings.TrimSpace(s[idx+1:])
		return Requirement{Key: key, Operator: OpEquals, Values: []string{val}}, ""
	}

	// key (exists)
	if reason := keyDefect(s); reason != "" {
		return Requirement{}, withSetHint(s, reason)
	}
	return Requirement{Key: s, Operator: OpExists}, ""
}

func parseSetOperator(key string, op Operator, rest string) (Requirement, string) {
	if reason := keyDefect(key); reason != "" {
		return Requirement{}, reason
	}
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, "(") || !strings.HasSuffix(rest, ")") {
		return Requirement{}, fmt.Sprintf("expected (values) after %s for key %q, got %q", op, key, rest)
	}
	inner := rest[1 : len(rest)-1]
	var values []string
	for _, v := range strings.Split(inner, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			values = append(values, v)
		}
	}
	return Requirement{Key: key, Operator: op, Values: values}, ""
}

// grammarCharacters are the characters the selector grammar uses to tell one
// requirement form from another, which is why a key may not carry them.
const grammarCharacters = "=!(),"

// keyDefect reports why key cannot be a key in a selector requirement, or the
// empty string when it can: a key is non-empty and carries neither whitespace
// nor any of the grammar's own characters.
func keyDefect(key string) string {
	if key == "" {
		return "the requirement names no label key before its operator"
	}
	for _, r := range key {
		if unicode.IsSpace(r) || strings.ContainsRune(grammarCharacters, r) {
			return fmt.Sprintf("the label key %q carries %s, and a key carries no whitespace and none of = ! ( ) or a comma",
				key, describeRune(r))
		}
	}
	return ""
}

// notAbsenceKey explains a "!" requirement whose remainder is not a bare key,
// and names the form the author most likely meant when the remainder reads as
// key=value.
func notAbsenceKey(remainder, reason string) string {
	msg := `"!" must be followed by a label key alone; ` + reason
	if k, v, found := strings.Cut(remainder, "="); found && keyDefect(strings.TrimSpace(k)) == "" {
		msg += fmt.Sprintf("; to exclude a value, write %q", strings.TrimSpace(k)+"!="+strings.TrimSpace(v))
	}
	return msg
}

// withSetHint adds the set form to the reason a bare requirement was refused
// when the requirement reads as a set written without its parentheses.
func withSetHint(s, reason string) string {
	for _, op := range []string{" notin ", " in "} {
		if k, v, found := strings.Cut(s, op); found && keyDefect(strings.TrimSpace(k)) == "" {
			return reason + fmt.Sprintf("; a set is written in parentheses, as in %q",
				strings.TrimSpace(k)+op+"("+strings.TrimSpace(v)+")")
		}
	}
	return reason
}

// describeRune names a character a key may not carry, in the words a reader
// recognises for the invisible ones.
func describeRune(r rune) string {
	switch {
	case r == ' ':
		return "a space"
	case r == '\t':
		return "a tab"
	case r == '\n' || r == '\r':
		return "a line break"
	case unicode.IsSpace(r):
		return fmt.Sprintf("the whitespace character %U", r)
	default:
		return fmt.Sprintf("%q", string(r))
	}
}

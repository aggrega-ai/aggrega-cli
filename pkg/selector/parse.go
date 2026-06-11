// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package selector

import (
	"fmt"
	"strings"
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
// Feature `appliesTo`, and the string-form items of `appliesToAnyOf` —
// reaches it through Parse. The flat-map arm of an `appliesToAnyOf` item
// bypasses Requirement entirely via MatchLabels.
type Requirement struct {
	Key      string
	Operator Operator
	Values   []string // empty for Exists/DoesNotExist
}

// Parse parses a Kubernetes-style label-selector string into a slice of
// Requirement values. Comma-separated clauses combine with AND semantics
// (every clause must hold for the selector to match); parentheses around
// In / NotIn value sets are not split. An empty or whitespace-only selector
// returns (nil, nil), which downstream callers treat as "no constraint" —
// see MatchString in selector.go.
//
// Supported forms (one per requirement):
//   - key=value      (Equals)
//   - key!=value     (NotEquals)
//   - key in (v1,v2) (In)
//   - key notin (v1,v2) (NotIn)
//   - key            (Exists)
//   - !key           (DoesNotExist)
//
// Errors surface as a wrapped "selector: ..." message and only fire when a
// clause cannot be classified into one of the six forms — specifically
// (a) "!" prefix with an empty key, and (b) " in (" or " notin (" present
// without a closing ")". Bare-key clauses always succeed as Exists, so most
// malformed inputs fail downstream at match time (selector references a key
// that does not exist on any node) rather than at parse time.
func Parse(selector string) ([]Requirement, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, nil
	}

	var reqs []Requirement
	parts := splitRequirements(selector)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		req, err := parseOne(part)
		if err != nil {
			return nil, fmt.Errorf("selector: %w", err)
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

func parseOne(s string) (Requirement, error) {
	s = strings.TrimSpace(s)

	// !key (does not exist)
	if strings.HasPrefix(s, "!") {
		key := strings.TrimSpace(s[1:])
		if key == "" {
			return Requirement{}, fmt.Errorf("empty key in %q", s)
		}
		return Requirement{Key: key, Operator: OpDoesNotExist}, nil
	}

	// key in (v1,v2) or key notin (v1,v2)
	if idx := strings.Index(s, " in ("); idx > 0 {
		key := strings.TrimSpace(s[:idx])
		return parseSetOperator(key, OpIn, s[idx+4:])
	}
	if idx := strings.Index(s, " notin ("); idx > 0 {
		key := strings.TrimSpace(s[:idx])
		return parseSetOperator(key, OpNotIn, s[idx+7:])
	}

	// key!=value
	if idx := strings.Index(s, "!="); idx > 0 {
		key := strings.TrimSpace(s[:idx])
		val := strings.TrimSpace(s[idx+2:])
		return Requirement{Key: key, Operator: OpNotEquals, Values: []string{val}}, nil
	}

	// key=value
	if idx := strings.Index(s, "="); idx > 0 {
		key := strings.TrimSpace(s[:idx])
		val := strings.TrimSpace(s[idx+1:])
		return Requirement{Key: key, Operator: OpEquals, Values: []string{val}}, nil
	}

	// key (exists)
	return Requirement{Key: s, Operator: OpExists}, nil
}

func parseSetOperator(key string, op Operator, rest string) (Requirement, error) {
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, "(") || !strings.HasSuffix(rest, ")") {
		return Requirement{}, fmt.Errorf("expected (values) after %s for key %q, got %q", op, key, rest)
	}
	inner := rest[1 : len(rest)-1]
	var values []string
	for _, v := range strings.Split(inner, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			values = append(values, v)
		}
	}
	return Requirement{Key: key, Operator: op, Values: values}, nil
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package selector

import (
	"fmt"
	"strings"
)

// Operator is a label selector operator.
type Operator string

const (
	OpEquals       Operator = "="
	OpNotEquals    Operator = "!="
	OpIn           Operator = "in"
	OpNotIn        Operator = "notin"
	OpExists       Operator = "exists"
	OpDoesNotExist Operator = "doesnotexist"
)

// Requirement is one parsed label selector requirement.
type Requirement struct {
	Key      string
	Operator Operator
	Values   []string // empty for Exists/DoesNotExist
}

// Parse parses a Kubernetes-style label selector string into requirements.
// Requirements are separated by commas (AND logic).
// Supported forms:
//   - key=value
//   - key!=value
//   - key in (v1,v2)
//   - key notin (v1,v2)
//   - key          (exists)
//   - !key         (does not exist)
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

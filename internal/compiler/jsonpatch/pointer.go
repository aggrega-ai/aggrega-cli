// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonpatch

import (
	"fmt"
	"strconv"
	"strings"
)

// parsePointer splits an RFC 6901 JSON Pointer into its reference
// tokens. The empty string is the whole-document pointer and yields
// zero tokens. A non-empty pointer MUST begin with '/'. Each token is
// unescaped: "~1" becomes "/" and "~0" becomes "~", applied in that
// order (per RFC 6901 §4, "~1" first so a literal "~01" round-trips to
// "~1" rather than being mangled into "/").
func parsePointer(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if pointer[0] != '/' {
		return nil, fmt.Errorf("invalid JSON pointer %q: must be empty or begin with %q", pointer, "/")
	}
	parts := strings.Split(pointer[1:], "/")
	tokens := make([]string, len(parts))
	for i, part := range parts {
		part = strings.ReplaceAll(part, "~1", "/")
		part = strings.ReplaceAll(part, "~0", "~")
		tokens[i] = part
	}
	return tokens, nil
}

// arrayIndex parses an array reference token against a slice of the
// given length. The "-" token names the position just past the last
// element; it is valid only where allowEnd is set (an add/append
// target) and resolves to length. A numeric token must be a
// non-negative base-10 integer with no leading zero. Range validation
// (idx < length for read/replace/remove, idx <= length for add) is the
// caller's responsibility; this function only decodes and rejects
// malformed tokens.
func arrayIndex(token string, length int, allowEnd bool) (idx int, isEnd bool, err error) {
	if token == "-" {
		if allowEnd {
			return length, true, nil
		}
		return 0, false, fmt.Errorf("array index %q is only valid for add", token)
	}
	if token == "" {
		return 0, false, fmt.Errorf("empty array index")
	}
	if len(token) > 1 && token[0] == '0' {
		return 0, false, fmt.Errorf("array index %q has a leading zero", token)
	}
	n, convErr := strconv.Atoi(token)
	if convErr != nil || n < 0 {
		return 0, false, fmt.Errorf("invalid array index %q", token)
	}
	return n, false, nil
}

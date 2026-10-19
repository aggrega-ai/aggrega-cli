// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composeimport

import (
	"slices"
	"strings"
)

// The parser's tree writes the members of a mapping in sorted order, so the
// order a Compose file lists its services, its models and a service's
// environment in is read from the file's text, and from nothing else there: the
// tree, which the parser alone produces, is what every value is read from. The
// reading takes the keys of a block mapping as the lines indented one level
// beneath its key write them; a mapping written in flow style, or a key the
// reading does not find there, keeps the tree's sorted order, after the keys it
// does find.

// keyOrder returns names, the members of the mapping at path, in the order the
// text writes them: those the text writes beneath path, in that order, followed
// by any other, sorted.
func keyOrder(text []byte, path []string, names []string) []string {
	found := blockKeys(text, path...)
	present := map[string]bool{}
	for _, n := range names {
		present[n] = true
	}
	var out []string
	taken := map[string]bool{}
	for _, k := range found {
		if present[k] && !taken[k] {
			out = append(out, k)
			taken[k] = true
		}
	}
	var rest []string
	for _, n := range names {
		if !taken[n] {
			rest = append(rest, n)
		}
	}
	slices.Sort(rest)
	return append(out, rest...)
}

// blockKeys returns the keys of the block mapping at path — the top-level key
// path[0], its key path[1], and so on — in the order the text writes them: the
// keys of the lines indented as deep as the first line beneath the mapping's
// key, up to the next line indented no deeper than that key. It returns none
// where a key of path is not written as a block mapping's key.
func blockKeys(text []byte, path ...string) []string {
	lines := strings.Split(strings.TrimPrefix(string(text), "\ufeff"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	start, end, keyIndent := 0, len(lines), -1
	for _, want := range path {
		at := -1
		child := -1
		for i := start; i < end; i++ {
			if skippable(lines[i]) || documentMarker(lines[i]) {
				continue
			}
			indent := indentOf(lines[i])
			if indent <= keyIndent {
				break
			}
			if child < 0 {
				child = indent
			}
			if indent != child {
				continue
			}
			key, rest, ok := keyOf(lines[i][indent:])
			if ok && key == want && emptyRest(rest) {
				at = i
				break
			}
		}
		if at < 0 {
			return nil
		}
		keyIndent = indentOf(lines[at])
		start = at + 1
		for end = start; end < len(lines); end++ {
			if !skippable(lines[end]) && indentOf(lines[end]) <= keyIndent {
				break
			}
		}
	}
	var keys []string
	child := -1
	for i := start; i < end; i++ {
		line := lines[i]
		if skippable(line) {
			continue
		}
		indent := indentOf(line)
		if child < 0 {
			child = indent
		}
		if indent != child {
			continue
		}
		key, _, ok := keyOf(line[indent:])
		if !ok {
			return nil
		}
		keys = append(keys, key)
	}
	return keys
}

// documentMarker reports whether a line opens or ends a YAML document.
func documentMarker(line string) bool {
	return strings.HasPrefix(line, "---") || strings.HasPrefix(line, "...")
}

// indentOf returns the number of spaces a line opens with.
func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// skippable reports whether a line holds nothing a mapping key is read from: it
// is blank, or holds a comment alone.
func skippable(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

// emptyRest reports whether what follows a key's colon on its line holds no
// value: nothing, a comment, or an anchor.
func emptyRest(rest string) bool {
	t := strings.TrimSpace(rest)
	if t == "" || strings.HasPrefix(t, "#") {
		return true
	}
	if strings.HasPrefix(t, "&") {
		_, after, _ := strings.Cut(t, " ")
		return emptyRest(after)
	}
	return false
}

// keyOf reads the mapping key a line opens with — plain, or quoted in double or
// single quotes — and returns it and what follows its colon. It reports false for
// a line that does not open with a key followed by a colon and a space or the end
// of the line, such as a sequence item.
func keyOf(line string) (string, string, bool) {
	if line == "" || line[0] == '-' || line[0] == '[' || line[0] == '{' {
		return "", "", false
	}
	if line[0] == '"' || line[0] == '\'' {
		quote := line[0]
		end := strings.IndexByte(line[1:], quote)
		if end < 0 {
			return "", "", false
		}
		key, rest := line[1:end+1], line[end+2:]
		if !strings.HasPrefix(rest, ":") {
			return "", "", false
		}
		return key, rest[1:], true
	}
	for i := 0; i < len(line); i++ {
		if line[i] == ':' && (i+1 == len(line) || line[i+1] == ' ' || line[i+1] == '\t') {
			key := strings.TrimSpace(line[:i])
			if key == "" {
				return "", "", false
			}
			return key, line[i+1:], true
		}
		if line[i] == ' ' && i+1 < len(line) && line[i+1] == '#' {
			return "", "", false
		}
	}
	return "", "", false
}

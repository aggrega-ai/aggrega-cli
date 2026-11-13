// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package difyimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// This file writes values empty in a Dify DSL's own bytes, for the kit's
// credential decision where the empty_credentials setting asks for it. It reads
// the block layout Dify's export writes — a mapping's keys one per line at one
// indentation, a sequence's items each opening with a dash, a scalar on the line
// of its key, continued on lines indented further — and replaces each value's
// scalar with an empty single-quoted one, keeping every other byte. It is not a
// YAML parser and does not decide what is carried: the kit parses what it
// returns through the host's parser and carries it only where it reads back as
// the source's tree with exactly those values emptied. A layout it does not read
// is refused, and the source is then not carried.

// emptyScalar is the scalar a value is written as: the empty string.
const emptyScalar = "''"

// emptyInSource writes each credential's value as emptyScalar in source, every
// other byte kept. It refuses a credential whose Value is set to anything but
// the empty string, and a value it cannot locate in the block layout. Its
// errors name a pointer's position, never a value.
func emptyInSource(source []byte, credentials []importer.Credential) ([]byte, error) {
	doc := newBlockDocument(source)
	type write struct{ start, end int }
	var writes []write
	for _, c := range credentials {
		if c.Value != nil && string(bytes.TrimSpace(c.Value)) != `""` {
			return nil, fmt.Errorf("the value at %s is written as a value other than the empty string", c.Pointer)
		}
		tokens, err := pointerTokens(c.Pointer)
		if err != nil {
			return nil, err
		}
		start, end, err := doc.scalarSpan(tokens)
		if err != nil {
			return nil, fmt.Errorf("the value at %s: %w", c.Pointer, err)
		}
		writes = append(writes, write{start, end})
	}
	sort.Slice(writes, func(i, j int) bool { return writes[i].start < writes[j].start })
	var out []byte
	at := 0
	for _, w := range writes {
		if w.start < at {
			return nil, errors.New("two values to write empty overlap")
		}
		out = append(out, source[at:w.start]...)
		out = append(out, emptyScalar...)
		at = w.end
	}
	return append(out, source[at:]...), nil
}

// pointerTokens splits a JSON Pointer into its unescaped reference tokens.
func pointerTokens(pointer string) ([]string, error) {
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("%q is not a JSON Pointer to a member", pointer)
	}
	parts := strings.Split(pointer[1:], "/")
	for i, p := range parts {
		parts[i] = strings.NewReplacer("~1", "/", "~0", "~").Replace(p)
	}
	return parts, nil
}

// blockLine is one line of a block document: where its content opens, the
// column it opens at, and where the line ends, its line break excluded. A line
// opening a sequence item is read a second time as the item's first line, its
// content opening after the dash, at the column it opens at.
type blockLine struct {
	at, indent, end int
}

// blank reports whether the line holds nothing but spaces, or a comment.
func (l blockLine) blank(src []byte) bool {
	return l.at >= l.end || src[l.at] == '#'
}

// blockDocument is a source read as lines.
type blockDocument struct {
	src   []byte
	lines []blockLine
}

// newBlockDocument reads source as lines, its document-start marker passed over.
func newBlockDocument(source []byte) *blockDocument {
	d := &blockDocument{src: source}
	for start := 0; start < len(source); {
		end := bytes.IndexByte(source[start:], '\n')
		next := len(source)
		if end < 0 {
			end = len(source)
		} else {
			end += start
			next = end + 1
		}
		lineEnd := end
		if lineEnd > start && source[lineEnd-1] == '\r' {
			lineEnd--
		}
		at := start
		for at < lineEnd && source[at] == ' ' {
			at++
		}
		l := blockLine{at: at, indent: at - start, end: lineEnd}
		if !(l.indent == 0 && string(source[at:lineEnd]) == "---") {
			d.lines = append(d.lines, l)
		}
		start = next
	}
	return d
}

// scalarSpan returns the byte span of the scalar tokens address, each token a
// key of a block mapping or the position of an item of a block sequence, the
// last a key whose value is a scalar written on its line — an anchor or a tag
// before it kept outside the span.
func (d *blockDocument) scalarSpan(tokens []string) (int, int, error) {
	lines := d.lines
	for i, token := range tokens {
		last := i == len(tokens)-1
		first := d.firstContent(lines)
		if first < 0 {
			return 0, 0, errors.New("a member on its way holds nothing")
		}
		if d.src[lines[first].at] == '-' && d.isItem(lines[first]) {
			if last {
				return 0, 0, errors.New("it is an item of a sequence, which is not written empty here")
			}
			item, err := d.item(lines[first:], token)
			if err != nil {
				return 0, 0, err
			}
			lines = item
			continue
		}
		entry, err := d.entry(lines[first:], token)
		if err != nil {
			return 0, 0, err
		}
		if last {
			return d.inlineScalar(entry)
		}
		lines = d.blockValue(entry)
	}
	return 0, 0, errors.New("no member is named")
}

// firstContent returns the position of the first line of lines that is not
// blank, or -1.
func (d *blockDocument) firstContent(lines []blockLine) int {
	for i, l := range lines {
		if !l.blank(d.src) {
			return i
		}
	}
	return -1
}

// isItem reports whether a line opens a sequence item: a dash followed by a
// space or by the line's end.
func (d *blockDocument) isItem(l blockLine) bool {
	return d.src[l.at] == '-' && (l.at+1 == l.end || d.src[l.at+1] == ' ')
}

// item returns the lines of the item of the sequence lines open whose position
// token is: its first line read after the dash, then every line up to the next
// item at the sequence's column or a line opening before it.
func (d *blockDocument) item(lines []blockLine, token string) ([]blockLine, error) {
	want, err := strconv.Atoi(token)
	if err != nil || want < 0 {
		return nil, fmt.Errorf("a sequence is indexed by %q", token)
	}
	column := lines[0].indent
	index := -1
	var out []blockLine
	for _, l := range lines {
		if l.blank(d.src) {
			if index == want {
				out = append(out, l)
			}
			continue
		}
		if l.indent < column || (l.indent == column && !d.isItem(l)) {
			break
		}
		if l.indent == column {
			index++
			if index > want {
				break
			}
			if index == want {
				at := l.at + 1
				for at < l.end && d.src[at] == ' ' {
					at++
				}
				out = append(out, blockLine{at: at, indent: column + (at - l.at), end: l.end})
			}
			continue
		}
		if index == want {
			out = append(out, l)
		}
	}
	if index < want {
		return nil, fmt.Errorf("the sequence holds no item %d", want)
	}
	return out, nil
}

// mappingEntry is a key of a block mapping found on a line: the line's position
// among the mapping's lines, where its value opens on that line, and the lines
// following it up to the next key at the mapping's column or a line opening
// before it.
type mappingEntry struct {
	line   blockLine
	value  int
	column int
	rest   []blockLine
}

// entry returns the entry of the mapping lines open keyed token.
func (d *blockDocument) entry(lines []blockLine, token string) (mappingEntry, error) {
	column := lines[0].indent
	for i, l := range lines {
		if l.blank(d.src) || l.indent != column {
			if !l.blank(d.src) && l.indent < column {
				break
			}
			continue
		}
		key, value, ok := d.keyOf(l)
		if !ok {
			if d.isItem(l) {
				continue
			}
			return mappingEntry{}, errors.New("a line of a mapping on its way does not read as a key")
		}
		if key != token {
			continue
		}
		e := mappingEntry{line: l, value: value, column: column}
		for _, r := range lines[i+1:] {
			if !r.blank(d.src) && (r.indent < column || (r.indent == column && !d.isItem(r))) {
				break
			}
			e.rest = append(e.rest, r)
		}
		return e, nil
	}
	return mappingEntry{}, fmt.Errorf("no key %q is written in the mapping on its way", token)
}

// keyOf reads the key a mapping line opens with — plain, single-quoted or
// double-quoted — and where its value opens on the line, after the colon and
// the spaces following it.
func (d *blockDocument) keyOf(l blockLine) (string, int, bool) {
	text := d.src[l.at:l.end]
	var key string
	n := 0
	switch {
	case len(text) > 0 && text[0] == '\'':
		var b strings.Builder
		for n = 1; n < len(text); n++ {
			if text[n] == '\'' {
				if n+1 < len(text) && text[n+1] == '\'' {
					b.WriteByte('\'')
					n++
					continue
				}
				break
			}
			b.WriteByte(text[n])
		}
		if n >= len(text) {
			return "", 0, false
		}
		key, n = b.String(), n+1
	case len(text) > 0 && text[0] == '"':
		end := 1
		for ; end < len(text); end++ {
			if text[end] == '\\' {
				end++
				continue
			}
			if text[end] == '"' {
				break
			}
		}
		if end >= len(text) || json.Unmarshal(text[:end+1], &key) != nil {
			return "", 0, false
		}
		n = end + 1
	default:
		colon := bytes.Index(text, []byte(": "))
		if colon < 0 {
			if !bytes.HasSuffix(text, []byte(":")) {
				return "", 0, false
			}
			colon = len(text) - 1
		}
		key, n = string(bytes.TrimRight(text[:colon], " ")), colon
		if key == "" || strings.ContainsAny(key[:1], "-?[{&*!|>'\"%@`") {
			return "", 0, false
		}
	}
	if n >= len(text) || text[n] != ':' || (n+1 < len(text) && text[n+1] != ' ') {
		return "", 0, false
	}
	n++
	for n < len(text) && text[n] == ' ' {
		n++
	}
	return key, l.at + n, true
}

// blockValue returns the lines an entry's value is written on, where the value
// opens on the line after its key.
func (d *blockDocument) blockValue(e mappingEntry) []blockLine {
	if e.value < e.line.end && d.src[e.value] != '#' {
		return nil
	}
	return e.rest
}

// inlineScalar returns the span of the scalar an entry's value is written as,
// opening on its key's line — after an anchor or a tag, which stay — and
// running to the end of the last line following it that is indented further
// than its key, whatever that line holds, so a line of a block scalar opening
// with a hash is replaced with it rather than left behind as a comment; the
// empty lines after it stay. A value opening on the next line, or a flow
// collection, is refused.
func (d *blockDocument) inlineScalar(e mappingEntry) (int, int, error) {
	start := e.value
	for start < e.line.end && (d.src[start] == '&' || d.src[start] == '!') {
		space := bytes.IndexByte(d.src[start:e.line.end], ' ')
		if space < 0 {
			return 0, 0, errors.New("its value opens on the line after its key")
		}
		start += space + 1
		for start < e.line.end && d.src[start] == ' ' {
			start++
		}
	}
	if start >= e.line.end || d.src[start] == '#' {
		return 0, 0, errors.New("its value opens on the line after its key")
	}
	if c := d.src[start]; c == '[' || c == '{' {
		return 0, 0, errors.New("its value is a flow collection")
	}
	end := e.line.end
	for _, r := range e.rest {
		empty := r.at >= r.end
		if !empty && r.indent <= e.column {
			break
		}
		if !empty {
			end = r.end
		}
	}
	for end > start && d.src[end-1] == ' ' {
		end--
	}
	return start, end, nil
}

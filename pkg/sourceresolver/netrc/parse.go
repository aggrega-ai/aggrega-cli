// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package netrc

import (
	"strings"
)

// entry is one machine or default entry, in the order the file writes it.
type entry struct {
	machine   string // empty for the default entry
	isDefault bool
	login     string
	password  string
}

// token is one word of the file and the line it starts on.
type token struct {
	text string
	line int
}

// lexer splits a netrc file into words. A word is a run of characters other
// than white space, or a double-quoted string in which a backslash escapes the
// character after it.
type lexer struct {
	path string
	src  string
	pos  int
	line int
}

// next returns the next word, false at the end of the file, or the grammar
// error of a quoted word left open.
func (l *lexer) next() (token, bool, error) {
	l.skipSpace()
	if l.pos >= len(l.src) {
		return token{}, false, nil
	}
	start := l.line
	if l.src[l.pos] != '"' {
		begin := l.pos
		for l.pos < len(l.src) && !isSpace(l.src[l.pos]) {
			l.pos++
		}
		return token{text: l.src[begin:l.pos], line: start}, true, nil
	}
	l.pos++ // the opening quote
	var b strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == '"':
			l.pos++
			return token{text: b.String(), line: start}, true, nil
		case c == '\n':
			return token{}, false, grammarError(l.path, start, "a quoted word is not closed on the line it opens")
		case c == '\\' && l.pos+1 < len(l.src) && l.src[l.pos+1] != '\n':
			l.pos++
			b.WriteByte(unescape(l.src[l.pos]))
		default:
			b.WriteByte(c)
		}
		l.pos++
	}
	return token{}, false, grammarError(l.path, start, "a quoted word is not closed on the line it opens")
}

// unescape is the character a backslash followed by c stands for.
func unescape(c byte) byte {
	switch c {
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	default:
		return c
	}
}

func (l *lexer) skipSpace() {
	for l.pos < len(l.src) && isSpace(l.src[l.pos]) {
		if l.src[l.pos] == '\n' {
			l.line++
		}
		l.pos++
	}
}

// skipLine moves past the end of the current line.
func (l *lexer) skipLine() {
	for l.pos < len(l.src) && l.src[l.pos] != '\n' {
		l.pos++
	}
}

// skipMacro moves past a macro body: the lines after the one naming the macro,
// up to and including the first empty line, or to the end of the file.
func (l *lexer) skipMacro() {
	l.skipLine()
	for l.pos < len(l.src) {
		// At a newline: the next line starts after it.
		l.pos++
		l.line++
		end := strings.IndexByte(l.src[l.pos:], '\n')
		var lineText string
		if end < 0 {
			lineText = l.src[l.pos:]
		} else {
			lineText = l.src[l.pos : l.pos+end]
		}
		if strings.TrimSpace(lineText) == "" {
			l.pos += len(lineText)
			return
		}
		if end < 0 {
			l.pos = len(l.src)
			return
		}
		l.pos += end
	}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// keywords names the words the grammar admits where a keyword is expected. It is
// written into the fault a misplaced word produces, which is how the diagnostic
// says what was expected without repeating what was found.
const keywords = "machine, default, login, password, account or macdef"

// parse reads the netrc grammar: entries opened by `machine NAME` or `default`,
// each followed by `login`, `password` and `account` pairs that belong to it,
// and `macdef NAME` macro definitions whose bodies run to the next empty line.
// A word starting with `#` where a keyword is expected opens a comment that runs
// to the end of the line.
//
// It refuses what it cannot read rather than skipping it, because a word read in
// the wrong place shifts every word after it: a misspelt keyword would otherwise
// turn the password that follows into the next keyword, and the entry would be
// sent with no credential, or with the wrong one, while the file looked fine.
func parse(path, src string) ([]entry, error) {
	l := &lexer{path: path, src: src, line: 1}
	var entries []entry
	current := -1 // index into entries of the entry pairs belong to
	for {
		kw, ok, err := l.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return entries, nil
		}
		if strings.HasPrefix(kw.text, "#") {
			l.skipLine()
			continue
		}
		switch kw.text {
		case "default":
			entries = append(entries, entry{isDefault: true})
			current = len(entries) - 1
			continue
		case "machine", "login", "password", "account", "macdef":
		default:
			return nil, grammarError(path, kw.line, "a word stands where a keyword is expected ("+keywords+")")
		}
		value, ok, err := l.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, grammarError(path, kw.line, "the keyword "+kw.text+" is not followed by its value")
		}
		switch kw.text {
		case "machine":
			entries = append(entries, entry{machine: value.text})
			current = len(entries) - 1
		case "macdef":
			l.skipMacro()
		default:
			if current < 0 {
				return nil, grammarError(path, kw.line,
					"the keyword "+kw.text+" stands before any machine or default entry it could belong to")
			}
			switch kw.text {
			case "login":
				entries[current].login = value.text
			case "password":
				entries[current].password = value.text
			}
		}
	}
}

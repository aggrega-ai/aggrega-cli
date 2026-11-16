// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armexpr

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokString
	tokInt
	tokLParen
	tokRParen
	tokComma
	tokDot
	tokLBracket
	tokRBracket
)

type token struct {
	kind tokenKind
	// text is the identifier or integer as written, or the unescaped string.
	text string
	at   int
}

type lexer struct {
	src string
	pos int
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentPart(c byte) bool { return isIdentStart(c) || isDigit(c) }

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// next scans one token starting at l.pos, skipping whitespace before it.
func (l *lexer) next() (token, *SyntaxError) {
	for l.pos < len(l.src) && isSpace(l.src[l.pos]) {
		l.pos++
	}
	if l.pos >= len(l.src) {
		return token{kind: tokEOF, at: l.pos}, nil
	}
	start := l.pos
	c := l.src[start]
	switch {
	case isIdentStart(c):
		l.pos++
		for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
			l.pos++
		}
		return token{kind: tokIdent, text: l.src[start:l.pos], at: start}, nil
	case isDigit(c) || (c == '-' && start+1 < len(l.src) && isDigit(l.src[start+1])):
		l.pos++
		for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
			l.pos++
		}
		return token{kind: tokInt, text: l.src[start:l.pos], at: start}, nil
	case c == '\'':
		return l.scanString()
	}
	l.pos++
	switch c {
	case '(':
		return token{kind: tokLParen, at: start}, nil
	case ')':
		return token{kind: tokRParen, at: start}, nil
	case ',':
		return token{kind: tokComma, at: start}, nil
	case '.':
		return token{kind: tokDot, at: start}, nil
	case '[':
		return token{kind: tokLBracket, at: start}, nil
	case ']':
		return token{kind: tokRBracket, at: start}, nil
	}
	return token{}, &SyntaxError{Offset: start, Reason: "unexpected character"}
}

// scanString reads a single-quoted string whose opening quote is at l.pos.
func (l *lexer) scanString() (token, *SyntaxError) {
	start := l.pos
	l.pos++
	var b []byte
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c != '\'' {
			b = append(b, c)
			l.pos++
			continue
		}
		if l.pos+1 < len(l.src) && l.src[l.pos+1] == '\'' {
			b = append(b, '\'')
			l.pos += 2
			continue
		}
		l.pos++
		return token{kind: tokString, text: string(b), at: start}, nil
	}
	return token{}, &SyntaxError{Offset: start, Reason: "string is not closed"}
}

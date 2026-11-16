// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armexpr

// maxDepth bounds nesting so hostile input cannot exhaust the stack.
const maxDepth = 200

// IsExpression reports whether s is a string ARM reads as an expression: it
// starts with "[", does not start with "[[", and ends with "]".
func IsExpression(s string) bool {
	return len(s) >= 2 && s[0] == '[' && s[1] != '[' && s[len(s)-1] == ']'
}

// Parse reads s, the whole string value with its brackets, as one expression.
// A failure is a *SyntaxError whose offset is a byte offset in s.
func Parse(s string) (Node, error) {
	if !IsExpression(s) {
		return nil, &SyntaxError{Offset: 0, Reason: "text is not a bracketed expression"}
	}
	p := &parser{lex: lexer{src: s, pos: 1}}
	n, end, err := p.bracketed(false)
	if err != nil {
		return nil, err
	}
	if end != len(s) {
		return nil, &SyntaxError{Offset: end, Reason: "text after the closing bracket"}
	}
	return n, nil
}

// ParseSpan reads one bracketed expression whose opening bracket is text[open].
// It succeeds only when the expression is complete, closed by "]" and rooted in
// a function call (possibly under member and index accessors); end is the
// offset just past the closing bracket. Bracketed text that is not such an
// expression, such as array[0] or [a-z], yields ok false.
func ParseSpan(text string, open int) (n Node, end int, ok bool) {
	if open < 0 || open+1 >= len(text) || text[open] != '[' || text[open+1] == '[' {
		return nil, 0, false
	}
	p := &parser{lex: lexer{src: text, pos: open + 1}}
	n, end, err := p.bracketed(true)
	if err != nil {
		return nil, 0, false
	}
	return n, end, true
}

type parser struct {
	lex   lexer
	tok   token
	depth int
}

// bracketed parses an expression followed by "]" with the lexer just after the
// opening bracket. It returns the offset just past the closing bracket.
func (p *parser) bracketed(callRoot bool) (Node, int, *SyntaxError) {
	if err := p.advance(); err != nil {
		return nil, 0, err
	}
	n, err := p.expr()
	if err != nil {
		return nil, 0, err
	}
	if callRoot && !rootIsCall(n) {
		return nil, 0, &SyntaxError{Offset: n.Pos(), Reason: "expression is not rooted in a function call"}
	}
	if p.tok.kind != tokRBracket {
		return nil, 0, &SyntaxError{Offset: p.tok.at, Reason: "expected a closing bracket"}
	}
	return n, p.tok.at + 1, nil
}

func rootIsCall(n Node) bool {
	for {
		switch v := n.(type) {
		case Call:
			return true
		case Member:
			n = v.Target
		case Index:
			n = v.Target
		default:
			return false
		}
	}
}

func (p *parser) advance() *SyntaxError {
	t, err := p.lex.next()
	if err != nil {
		return err
	}
	p.tok = t
	return nil
}

// expr parses a primary followed by any number of accessors.
func (p *parser) expr() (Node, *SyntaxError) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxDepth {
		return nil, &SyntaxError{Offset: p.tok.at, Reason: "expression is nested too deeply"}
	}
	n, err := p.primary()
	if err != nil {
		return nil, err
	}
	for {
		switch p.tok.kind {
		case tokDot:
			at := p.tok.at
			if err := p.advance(); err != nil {
				return nil, err
			}
			if p.tok.kind != tokIdent {
				return nil, &SyntaxError{Offset: p.tok.at, Reason: "expected a property name after the dot"}
			}
			n = Member{Target: n, Name: p.tok.text, At: at}
			if err := p.advance(); err != nil {
				return nil, err
			}
		case tokLBracket:
			at := p.tok.at
			if err := p.advance(); err != nil {
				return nil, err
			}
			idx, err := p.expr()
			if err != nil {
				return nil, err
			}
			if p.tok.kind != tokRBracket {
				return nil, &SyntaxError{Offset: p.tok.at, Reason: "expected a closing bracket for the index"}
			}
			n = Index{Target: n, Index: idx, At: at}
			if err := p.advance(); err != nil {
				return nil, err
			}
		default:
			return n, nil
		}
	}
}

func (p *parser) primary() (Node, *SyntaxError) {
	t := p.tok
	switch t.kind {
	case tokString:
		if err := p.advance(); err != nil {
			return nil, err
		}
		return String{Value: t.text, At: t.at}, nil
	case tokInt:
		if err := p.advance(); err != nil {
			return nil, err
		}
		return Int{Text: t.text, At: t.at}, nil
	case tokIdent:
		return p.call()
	case tokEOF:
		return nil, &SyntaxError{Offset: t.at, Reason: "expression is incomplete"}
	}
	return nil, &SyntaxError{Offset: t.at, Reason: "expected a function call, a string or an integer"}
}

// call parses [namespace.]name(args) with the current token the first identifier.
func (p *parser) call() (Node, *SyntaxError) {
	first := p.tok
	if err := p.advance(); err != nil {
		return nil, err
	}
	c := Call{Name: first.text, At: first.at}
	if p.tok.kind == tokDot {
		if err := p.advance(); err != nil {
			return nil, err
		}
		if p.tok.kind != tokIdent {
			return nil, &SyntaxError{Offset: p.tok.at, Reason: "expected a function name after the namespace"}
		}
		c.Namespace, c.Name = first.text, p.tok.text
		if err := p.advance(); err != nil {
			return nil, err
		}
	}
	if p.tok.kind != tokLParen {
		return nil, &SyntaxError{Offset: p.tok.at, Reason: "a name that is not followed by an argument list is not a function call"}
	}
	if err := p.advance(); err != nil {
		return nil, err
	}
	if p.tok.kind == tokRParen {
		if err := p.advance(); err != nil {
			return nil, err
		}
		return c, nil
	}
	for {
		arg, err := p.expr()
		if err != nil {
			return nil, err
		}
		c.Args = append(c.Args, arg)
		switch p.tok.kind {
		case tokComma:
			if err := p.advance(); err != nil {
				return nil, err
			}
		case tokRParen:
			if err := p.advance(); err != nil {
				return nil, err
			}
			return c, nil
		default:
			return nil, &SyntaxError{Offset: p.tok.at, Reason: "expected a comma or a closing parenthesis"}
		}
	}
}

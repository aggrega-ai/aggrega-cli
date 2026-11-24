// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template/parse"
)

// A Recipe's fragments and valuesMapping are YAML written as Go templates. The
// readers below read such text as it is written and evaluate nothing: the
// structure the YAML lines give, the actions a value is written with, and the
// calls an action makes, parsed by the standard library's template parser with
// the functions a build supplies left unchecked.

// span is one action of a line: where it starts and ends, and its text less
// its delimiters and trim markers.
type span struct {
	start, end int
	inner      string
}

// scanActions returns the actions text writes, in order; a quoted string inside
// an action may hold braces of its own, and an action left open runs to the
// end of text. A comment action is passed over.
func scanActions(text string) []span {
	var out []span
	for i := 0; i < len(text); {
		open := strings.Index(text[i:], "{{")
		if open < 0 {
			break
		}
		open += i
		j := open + 2
		var quote byte
	scan:
		for j < len(text) {
			c := text[j]
			switch {
			case quote != 0 && c == '\\' && quote == '"':
				j += 2
				continue
			case quote != 0 && c == quote:
				quote = 0
			case quote == 0 && (c == '"' || c == '`'):
				quote = c
			case quote == 0 && c == '}' && j+1 < len(text) && text[j+1] == '}':
				break scan
			}
			j++
		}
		end := min(j+2, len(text))
		inner := strings.TrimSpace(text[open+2 : min(j, len(text))])
		inner = strings.TrimSpace(strings.TrimPrefix(inner, "-"))
		if strings.HasSuffix(inner, " -") {
			inner = strings.TrimSpace(strings.TrimSuffix(inner, "-"))
		}
		if !strings.HasPrefix(inner, "/*") {
			out = append(out, span{start: open, end: end, inner: inner})
		}
		i = end
	}
	return out
}

// normalize writes an action's text with each run of space as one space, so
// two spellings of one action compare equal.
func normalize(inner string) string {
	return strings.Join(strings.Fields(inner), " ")
}

// unquote returns value without the one pair of quotes enclosing it whole.
func unquote(value string) string {
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1]
	}
	return value
}

// leaf is one value a template writes at a path: each mapping key, and each
// list item by its index in decimal. conditional reports that an if or with
// branch writes it.
type leaf struct {
	path        []string
	value       string
	conditional bool
}

// templateDoc is what readTemplateYAML reads of a template: its leaves in the
// order written, and the text of each of its actions.
type templateDoc struct {
	leaves  []leaf
	actions []string
}

// blockScalar matches a YAML block scalar's indicator, after which the more
// indented lines are the scalar's text.
var blockScalar = regexp.MustCompile(`^[|>][-+0-9]*(\s+#.*)?$`)

// readTemplateYAML reads YAML written as a template line by line: a line
// holding actions alone opens or closes a branch, or does neither, and every
// other line is a mapping key or a list item placed by its indentation. A
// value written inside a range, a define or a block is not a leaf: which key
// or index it is written at is the template's to decide. A line within a block
// scalar is the scalar's text and nothing more.
func readTemplateYAML(text string) templateDoc {
	type frame struct {
		indent int
		seg    string
		item   bool
		id     int
	}
	var (
		doc      templateDoc
		stack    []frame
		control  []string
		counters = map[int]int{}
		ids      = 0
		scalar   = -1
	)
	pathOf := func(last string) []string {
		out := make([]string, 0, len(stack)+1)
		for _, f := range stack {
			out = append(out, f.seg)
		}
		return append(out, last)
	}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		spans := scanActions(line)
		for _, s := range spans {
			doc.actions = append(doc.actions, s.inner)
		}
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if scalar >= 0 {
			if trimmed == "" || indent > scalar {
				continue
			}
			scalar = -1
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if actionsAlone(line, spans) {
			for _, s := range spans {
				control = branch(control, s.inner)
			}
			continue
		}
		hidden := slices.ContainsFunc(control, func(c string) bool { return c != "if" && c != "with" })
		conditional := len(control) > 0
		rest := line[indent:]
		item := false
		for rest == "-" || strings.HasPrefix(rest, "- ") {
			for len(stack) > 0 && (stack[len(stack)-1].indent > indent ||
				stack[len(stack)-1].indent == indent && stack[len(stack)-1].item) {
				stack = stack[:len(stack)-1]
			}
			parent := -1
			if len(stack) > 0 {
				parent = stack[len(stack)-1].id
			}
			index := counters[parent]
			counters[parent]++
			ids++
			stack = append(stack, frame{indent: indent, seg: strconv.Itoa(index), item: true, id: ids})
			after := strings.TrimLeft(rest[1:], " ")
			indent += len(rest) - len(after)
			rest, item = after, true
		}
		if rest == "" {
			continue
		}
		key, value, isKey := splitKey(rest)
		if !isKey {
			if item && !hidden {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				doc.leaves = append(doc.leaves, leaf{path: pathOf(top.seg), value: rest, conditional: conditional})
				stack = append(stack, top)
			}
			continue
		}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		switch {
		case value == "":
			ids++
			stack = append(stack, frame{indent: indent, seg: key, id: ids})
		case blockScalar.MatchString(value):
			scalar = indent
		case !hidden:
			doc.leaves = append(doc.leaves, leaf{path: pathOf(key), value: value, conditional: conditional})
		}
	}
	return doc
}

// actionsAlone reports whether line holds actions and nothing else.
func actionsAlone(line string, spans []span) bool {
	if len(spans) == 0 {
		return false
	}
	at := 0
	for _, s := range spans {
		if strings.TrimSpace(line[at:s.start]) != "" {
			return false
		}
		at = s.end
	}
	return strings.TrimSpace(line[at:]) == ""
}

// branch returns the open branches after an action: an if, with, range,
// define or block opens one, and an end closes the innermost.
func branch(open []string, inner string) []string {
	word, _, _ := strings.Cut(normalize(inner), " ")
	switch word {
	case "if", "with", "range", "define", "block":
		return append(open, word)
	case "end":
		if len(open) > 0 {
			return open[:len(open)-1]
		}
	}
	return open
}

// splitKey splits a YAML line, less its indentation and any list dash, into a
// mapping key and its value at the first colon outside an action that a space
// or the line's end follows; a key written in quotes is read up to its closing
// quote. false where the line is a scalar.
func splitKey(rest string) (key, value string, ok bool) {
	spans := scanActions(rest)
	skip := func(i int) int {
		for _, s := range spans {
			if i >= s.start && i < s.end {
				return s.end
			}
		}
		return i
	}
	i := 0
	if rest[0] == '"' || rest[0] == '\'' {
		quote := rest[0]
		for i = 1; i < len(rest); i++ {
			if j := skip(i); j != i {
				i = j - 1
				continue
			}
			if rest[i] == quote {
				break
			}
		}
		i++
	}
	for ; i < len(rest); i++ {
		if j := skip(i); j != i {
			i = j - 1
			continue
		}
		if rest[i] == ':' && (i+1 == len(rest) || rest[i+1] == ' ') {
			return unquote(strings.TrimSpace(rest[:i])), strings.TrimSpace(rest[i+1:]), true
		}
	}
	return "", "", false
}

// parseTemplate parses text as a template, leaving the functions it calls
// unchecked, since the generator holds none of the functions a build supplies.
func parseTemplate(text string) (*parse.Tree, error) {
	t := parse.New("recipe")
	t.Mode = parse.SkipFuncCheck
	return t.Parse(text, "", "", map[string]*parse.Tree{})
}

// wholeField returns the field a value reads where it is one action reading
// that field whole and nothing else, quoted or not, such as .values.user.
func wholeField(value string) (string, bool) {
	tree, err := parseTemplate(unquote(strings.TrimSpace(value)))
	if err != nil || tree.Root == nil || len(tree.Root.Nodes) != 1 {
		return "", false
	}
	action, ok := tree.Root.Nodes[0].(*parse.ActionNode)
	if !ok || len(action.Pipe.Decl) != 0 || len(action.Pipe.Cmds) != 1 || len(action.Pipe.Cmds[0].Args) != 1 {
		return "", false
	}
	field, ok := action.Pipe.Cmds[0].Args[0].(*parse.FieldNode)
	if !ok {
		return "", false
	}
	return "." + strings.Join(field.Ident, "."), true
}

// shapeOf returns the text tree writes with each action writing a symbol
// spelling written as symbolMark and every other action as actionMark; false
// where the tree branches or ranges.
func shapeOf(tree *parse.Tree, symbols []string) (string, bool) {
	if tree.Root == nil {
		return "", false
	}
	var b strings.Builder
	for _, n := range tree.Root.Nodes {
		switch n := n.(type) {
		case *parse.TextNode:
			b.Write(n.Text)
		case *parse.ActionNode:
			if len(n.Pipe.Decl) == 0 && slices.Contains(symbols, normalize(n.Pipe.String())) {
				b.WriteString(symbolMark)
			} else {
				b.WriteString(actionMark)
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}

// argKind is what one argument of a command is written as.
type argKind int

// The kinds of argument the generator reads.
const (
	argOther argKind = iota
	argIdentifier
	argString
	argTrue
)

// argument is one argument of a command: a function's name, a string literal's
// text, true, or anything else.
type argument struct {
	text string
	kind argKind
}

// walkCommands calls visit with the arguments of every command node beneath n,
// a command's own before those of the commands its arguments hold, in the order
// the template writes them.
func walkCommands(n parse.Node, visit func([]argument)) {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			walkCommands(c, visit)
		}
	case *parse.ActionNode:
		walkCommands(n.Pipe, visit)
	case *parse.PipeNode:
		if n == nil {
			return
		}
		for _, c := range n.Cmds {
			walkCommands(c, visit)
		}
	case *parse.CommandNode:
		args := make([]argument, len(n.Args))
		for i, a := range n.Args {
			switch a := a.(type) {
			case *parse.IdentifierNode:
				args[i] = argument{text: a.Ident, kind: argIdentifier}
			case *parse.StringNode:
				args[i] = argument{text: a.Text, kind: argString}
			case *parse.BoolNode:
				if a.True {
					args[i] = argument{text: "true", kind: argTrue}
				}
			}
		}
		visit(args)
		for _, a := range n.Args {
			walkCommands(a, visit)
		}
	case *parse.ChainNode:
		walkCommands(n.Node, visit)
	case *parse.IfNode:
		walkCommands(n.Pipe, visit)
		walkCommands(n.List, visit)
		walkCommands(n.ElseList, visit)
	case *parse.RangeNode:
		walkCommands(n.Pipe, visit)
		walkCommands(n.List, visit)
		walkCommands(n.ElseList, visit)
	case *parse.WithNode:
		walkCommands(n.Pipe, visit)
		walkCommands(n.List, visit)
		walkCommands(n.ElseList, visit)
	case *parse.TemplateNode:
		walkCommands(n.Pipe, visit)
	}
}

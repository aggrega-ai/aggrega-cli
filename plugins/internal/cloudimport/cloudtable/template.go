// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template/parse"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
)

// A Recipe's fragments and valuesMapping are YAML written as Go templates. The
// readers below read such text as it is written and evaluate nothing: the
// structure the YAML lines give, the actions a value is written with, and the
// calls an action makes, parsed with the build's own function set and walked
// by the walk every reader of a template's pipelines shares.

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
// list item by its index in decimal. at is where the value starts in the
// template's text, and conditional reports that an if, with or range branch
// writes it.
type leaf struct {
	path        []string
	value       string
	at          int
	conditional bool
}

// templateDoc is what readTemplateYAML reads of a template: its leaves in the
// order written, the text of each of its actions, its parse tree's root, nil
// where the text does not parse, and, for each pipeline by where it starts,
// whether the dot there is the data the template is handed.
type templateDoc struct {
	leaves  []leaf
	actions []string
	root    parse.Node
	pipes   []pipeAt
}

// pipeAt is one pipeline of a template: where it starts in the text, and
// whether the dot there is the data the template is handed.
type pipeAt struct {
	pos       int
	dotIsData bool
}

// readsData reports whether every action l's value is written with sees the
// dot as the data the template is handed — outside the body of a with or a
// range, which rebind it — so a field the value reads is the field of that
// data. false where the template does not parse, or the value holds no action.
func (d templateDoc) readsData(l leaf) bool {
	seen := false
	for _, p := range d.pipes {
		if p.pos >= l.at && p.pos < l.at+len(l.value) {
			if !p.dotIsData {
				return false
			}
			seen = true
		}
	}
	return seen
}

// blockScalar matches a YAML block scalar's indicator, after which the more
// indented lines are the scalar's text.
var blockScalar = regexp.MustCompile(`^[|>][-+0-9]*(\s+#.*)?$`)

// readTemplateYAML reads YAML written as a template line by line: a line
// holding actions alone opens or closes a branch, or does neither, and every
// other line is a mapping key or a list item placed by its indentation. A
// value written inside a range, a define or a block is not a leaf: which key
// or index it is written at is the template's to decide. A line within a block
// scalar is the scalar's text and nothing more. The whole text is parsed with
// the build's function set, and each pipeline's dot read from the shared walk.
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
	offset := 0
	for _, raw := range strings.Split(text, "\n") {
		lineStart := offset
		offset += len(raw) + 1
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
		restAt := lineStart + indent
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
			restAt += len(rest) - len(after)
			rest, item = after, true
		}
		if rest == "" {
			continue
		}
		key, value, valueAt, isKey := splitKey(rest)
		if !isKey {
			if item && !hidden {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				doc.leaves = append(doc.leaves, leaf{path: pathOf(top.seg), value: rest, at: restAt,
					conditional: conditional})
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
			doc.leaves = append(doc.leaves, leaf{path: pathOf(key), value: value, at: restAt + valueAt,
				conditional: conditional})
		}
	}
	if root, err := gotmpl.ParseTree(text, "{{", "}}"); err == nil {
		doc.root = root
		gotmpl.WalkPipelines(root, func(p *parse.PipeNode, dotIsData bool) {
			doc.pipes = append(doc.pipes, pipeAt{pos: int(p.Pos), dotIsData: dotIsData})
		})
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
// quote. valueAt is where the value starts in rest. false where the line is a
// scalar.
func splitKey(rest string) (key, value string, valueAt int, ok bool) {
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
			after := rest[i+1:]
			value := strings.TrimSpace(after)
			return unquote(strings.TrimSpace(rest[:i])), value, i + 1 + len(after) - len(strings.TrimLeft(after, " ")),
				true
		}
	}
	return "", "", 0, false
}

// rootNodes returns the nodes of text's parse tree's root, parsed with the
// build's function set; false where text does not parse.
func rootNodes(text string) ([]parse.Node, bool) {
	root, err := gotmpl.ParseTree(text, "{{", "}}")
	if err != nil {
		return nil, false
	}
	list, ok := root.(*parse.ListNode)
	if !ok || list == nil {
		return nil, false
	}
	return list.Nodes, true
}

// wholeField returns the field a value reads where it is one action reading
// that field whole and nothing else, quoted or not, such as .values.user.
func wholeField(value string) (string, bool) {
	nodes, ok := rootNodes(unquote(strings.TrimSpace(value)))
	if !ok || len(nodes) != 1 {
		return "", false
	}
	action, ok := nodes[0].(*parse.ActionNode)
	if !ok || len(action.Pipe.Decl) != 0 || len(action.Pipe.Cmds) != 1 || len(action.Pipe.Cmds[0].Args) != 1 {
		return "", false
	}
	field, ok := action.Pipe.Cmds[0].Args[0].(*parse.FieldNode)
	if !ok {
		return "", false
	}
	return "." + strings.Join(field.Ident, "."), true
}

// shapeOf returns the text value writes with each action writing a symbol
// spelling written as symbolMark and every other action as actionMark; false
// where the value does not parse, or branches or ranges.
func shapeOf(value string, symbols []string) (string, bool) {
	nodes, ok := rootNodes(value)
	if !ok {
		return "", false
	}
	var b strings.Builder
	for _, n := range nodes {
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

// eachCommand calls visit with the arguments of every command of every
// pipeline beneath root, in the order the shared walk visits the pipelines.
func eachCommand(root parse.Node, visit func([]argument)) {
	gotmpl.WalkPipelines(root, func(p *parse.PipeNode, _ bool) {
		for _, cmd := range p.Cmds {
			if cmd == nil {
				continue
			}
			args := make([]argument, len(cmd.Args))
			for i, a := range cmd.Args {
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
		}
	})
}

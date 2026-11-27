// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template/parse"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// A Recipe's fragments and valuesMapping are YAML written as Go templates. The
// readers below read such text as it is written and evaluate nothing: the
// structure the YAML lines give, the actions a value is written with, and the
// calls an action makes, parsed with the build's own function set and walked
// by the walk every reader of a template's pipelines shares.

// span is one action of a line: where it starts and ends, its text less its
// delimiters and trim markers, and whether it is a comment.
type span struct {
	start, end int
	inner      string
	comment    bool
}

// scanActions returns the actions text writes, in order; a quoted string inside
// an action may hold braces of its own, and an action left open runs to the
// end of text. A comment action is passed over.
func scanActions(text string) []span {
	return slices.DeleteFunc(scanAllActions(text), func(s span) bool { return s.comment })
}

// scanAllActions returns the actions text writes as scanActions does, each
// comment action among them.
func scanAllActions(text string) []span {
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
		out = append(out, span{start: open, end: end, inner: inner, comment: strings.HasPrefix(inner, "/*")})
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
	return readsDataIn(d.pipes, l.at, l.at+len(l.value))
}

// readsDataIn reports whether every pipeline of pipes starting from start to
// end sees the dot as the data the template is handed; false where none
// starts there.
func readsDataIn(pipes []pipeAt, start, end int) bool {
	seen := false
	for _, p := range pipes {
		if p.pos >= start && p.pos < end {
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
	doc.root, doc.pipes, _ = pipelinesOf(text)
	return doc
}

// pipelinesOf parses text with the build's function set and returns its tree's
// root and each of its pipelines by where it starts, read from the shared
// walk; false, with neither, where text does not parse.
func pipelinesOf(text string) (parse.Node, []pipeAt, bool) {
	root, err := gotmpl.ParseTree(text, "{{", "}}")
	if err != nil {
		return nil, nil, false
	}
	var pipes []pipeAt
	gotmpl.WalkPipelines(root, func(p *parse.PipeNode, dotIsData bool) {
		pipes = append(pipes, pipeAt{pos: int(p.Pos), dotIsData: dotIsData})
	})
	return root, pipes, true
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
	argField
)

// argument is one argument of a command: a function's name, a string literal's
// text, true, a field read off the dot, such as .infra, or anything else.
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
				case *parse.FieldNode:
					args[i] = argument{text: "." + strings.Join(a.Ident, "."), kind: argField}
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

// symKind is what a reader of a template reads a pipeline to yield.
type symKind int

// The kinds of what a pipeline yields.
const (
	symOther symKind = iota
	symData
	symValues
	symValue
	symInfra
	symInfraKey
	symOverrides
	symOverride
	symFeatures
	symSlot
	symSlotPart
	symSlotType
	symLocals
	symLocal
	symContext
	symInstanceID
	symLogicalID
	symLiteral
)

// sym is what a pipeline yields: its kind and the key, slot or literal text
// it names, and whether a literal holds.
type sym struct {
	kind   symKind
	key    string
	truthy bool
}

// member returns what the member name of s yields.
func (s sym) member(name string) sym {
	switch s.kind {
	case symData:
		switch name {
		case "values":
			return sym{kind: symValues}
		case "infra":
			return sym{kind: symInfra}
		case "features":
			return sym{kind: symFeatures}
		case "locals":
			return sym{kind: symLocals}
		case "context":
			return sym{kind: symContext}
		}
	case symValues:
		return sym{kind: symValue, key: name}
	case symValue:
		return sym{kind: symValue, key: s.key + "." + name}
	case symInfra:
		if name == "overrides" {
			return sym{kind: symOverrides}
		}
		return sym{kind: symInfraKey, key: name}
	case symInfraKey:
		return sym{kind: symInfraKey, key: s.key + "." + name}
	case symOverrides:
		return sym{kind: symOverride, key: name}
	case symFeatures:
		return sym{kind: symSlot, key: name}
	case symSlot:
		if name == "type" {
			return sym{kind: symSlotType, key: s.key}
		}
		return sym{kind: symSlotPart, key: s.key}
	case symSlotPart:
		return s
	case symLocals:
		if name == "logical_id" {
			return sym{kind: symLogicalID}
		}
		return sym{kind: symLocal, key: name}
	case symContext:
		if name == "instance_id" {
			return sym{kind: symInstanceID}
		}
	}
	return sym{}
}

// members returns what the members names of s, in turn, yield.
func (s sym) members(names []string) sym {
	for _, n := range names {
		s = s.member(n)
	}
	return s
}

// readsSlot reports whether s is a credential slot's attachment or anything
// read off one.
func (s sym) readsSlot() bool {
	return s.kind == symSlot || s.kind == symSlotPart || s.kind == symSlotType
}

// passThrough are the functions whose result a reader reads as the value
// handed them last: a default taken where that value is empty, and the
// conversions of a value to its text, its JSON text or the first item of its
// text split.
var passThrough = []string{"default", "toJson", "toString", "first", "splitList"}

// scope is what the dot and each variable yield where an action is written.
type scope struct {
	dot  sym
	vars map[string]sym
}

// dataScope is the scope at a template's start: the dot is the data the
// template is handed, and no variable is bound.
func dataScope() scope {
	return scope{dot: sym{kind: symData}, vars: map[string]sym{}}
}

// declare returns sc with the variables p declares bound to what p yields: a
// variable p assigns, or one unknown names, yields nothing a reader
// reads.
func (sc scope) declare(p *parse.PipeNode, unknown map[string]bool) scope {
	if p == nil || len(p.Decl) == 0 {
		return sc
	}
	v := sym{}
	if !p.IsAssign && len(p.Decl) == 1 {
		v = sc.pipe(p)
	}
	vars := maps.Clone(sc.vars)
	for _, d := range p.Decl {
		if unknown[d.Ident[0]] {
			vars[d.Ident[0]] = sym{}
		} else {
			vars[d.Ident[0]] = v
		}
	}
	return scope{dot: sc.dot, vars: vars}
}

// pipe returns what p yields.
func (sc scope) pipe(p *parse.PipeNode) sym {
	if p == nil || len(p.Cmds) == 0 {
		return sym{}
	}
	s := sc.command(p.Cmds[0], nil)
	for _, c := range p.Cmds[1:] {
		s = sc.command(c, &s)
	}
	return s
}

// command returns what c yields, handed piped as its last argument where a
// pipeline's earlier command yields it.
func (sc scope) command(c *parse.CommandNode, piped *sym) sym {
	if c == nil || len(c.Args) == 0 {
		return sym{}
	}
	fn, ok := c.Args[0].(*parse.IdentifierNode)
	if !ok {
		if len(c.Args) == 1 && piped == nil {
			return sc.node(c.Args[0])
		}
		return sym{}
	}
	args := c.Args[1:]
	last := sym{}
	switch {
	case piped != nil:
		last = *piped
	case len(args) > 0:
		last = sc.node(args[len(args)-1])
	}
	switch {
	case fn.Ident == "optional" || fn.Ident == "index":
		if piped != nil || len(args) < 2 {
			return sym{}
		}
		key, ok := args[1].(*parse.StringNode)
		if !ok {
			return sym{}
		}
		return sc.node(args[0]).member(key.Text)
	case fn.Ident == "capitalizedWords":
		if last.kind == symInstanceID {
			return sym{kind: symLogicalID}
		}
	case slices.Contains(passThrough, fn.Ident):
		return last
	}
	return sym{}
}

// node returns what an argument yields.
func (sc scope) node(n parse.Node) sym {
	switch n := n.(type) {
	case *parse.DotNode:
		return sc.dot
	case *parse.FieldNode:
		return sc.dot.members(n.Ident)
	case *parse.VariableNode:
		if n.Ident[0] == "$" {
			return sym{kind: symData}.members(n.Ident[1:])
		}
		v, ok := sc.vars[n.Ident[0]]
		if !ok {
			return sym{}
		}
		return v.members(n.Ident[1:])
	case *parse.ChainNode:
		return sc.node(n.Node).members(n.Field)
	case *parse.PipeNode:
		return sc.pipe(n)
	case *parse.StringNode:
		return sym{kind: symLiteral, key: n.Text, truthy: n.Text != ""}
	case *parse.BoolNode:
		return sym{kind: symLiteral, key: strconv.FormatBool(n.True), truthy: n.True}
	case *parse.NumberNode:
		return sym{kind: symLiteral, key: n.Text, truthy: n.IsInt && n.Int64 != 0 || n.IsFloat && n.Float64 != 0}
	case *parse.NilNode:
		return sym{kind: symLiteral}
	}
	return sym{}
}

// wholeRead returns what one action reading a field yields — a field of the
// dot or of the data the template is handed, written bare or handed to
// toJson — and whether it is handed to toJson.
func wholeRead(p *parse.PipeNode, sc scope) (sym, bool, bool) {
	if p == nil || len(p.Decl) > 0 || len(p.Cmds) != 1 {
		return sym{}, false, false
	}
	args := p.Cmds[0].Args
	toJSON := false
	if len(args) == 2 {
		if fn, ok := args[0].(*parse.IdentifierNode); ok && fn.Ident == "toJson" {
			toJSON, args = true, args[1:]
		}
	}
	if len(args) != 1 {
		return sym{}, false, false
	}
	s, ok := fieldRead(args[0], sc)
	return s, toJSON, ok
}

// fieldRead returns what an argument reading a field yields — a field of the
// dot, or of the data the template is handed through $ — and false for any
// other argument.
func fieldRead(arg parse.Node, sc scope) (sym, bool) {
	switch a := arg.(type) {
	case *parse.FieldNode:
	case *parse.VariableNode:
		if a.Ident[0] != "$" || len(a.Ident) < 2 {
			return sym{}, false
		}
	default:
		return sym{}, false
	}
	return sc.node(arg), true
}

// pathIs reports whether path is the dotted rule path pattern, a segment *
// matching any segment; segments are compared regardless of case.
func pathIs(path []string, pattern string) bool {
	segs := strings.Split(pattern, ".")
	if len(segs) != len(path) {
		return false
	}
	for i, s := range segs {
		if s != "*" && !strings.EqualFold(s, path[i]) {
			return false
		}
	}
	return true
}

// controlWord returns the keyword opening an action that opens, divides or
// closes a branch, a range or a definition, empty for any other action.
func controlWord(inner string) string {
	word, _, _ := strings.Cut(normalize(inner), " ")
	switch word {
	case "if", "with", "range", "else", "end", "define", "block":
		return word
	}
	return ""
}

// symbolMark and actionMark stand, in a value's shape, for an action writing
// the instance's symbol and for any other action.
const (
	symbolMark = "\x00"
	actionMark = "\x01"
)

// overridesHeader is the comment line opening the block in which a Recipe
// lists the infra.overrides keys it reads.
const overridesHeader = "This Recipe reads from infra.overrides:"

// headerEntry matches one key of the overrides header and the description
// after its dash, which a key too long for its column writes on the next line.
// A key may hold a hyphen after its first character, as a log driver option's
// name does.
var headerEntry = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*)(?:\s+-\s+(.*))?$`)

// headerPath matches the path a description's parenthesis opens with: a
// member name, then any run of a list index in brackets or a dot and a member
// name; and headerSegment each of its segments, a member name or a bracketed
// index.
var (
	headerPath    = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*(?:\[[0-9]+\]|\.[A-Za-z_$][A-Za-z0-9_$]*)*`)
	headerSegment = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*|\[[0-9]+\]`)
)

// headerOverrides returns each key the document's overrides header names a
// path for, placed from that path, in the order listed. A key's path is what
// its description's first parenthesis opens with where a path ends it, or a
// semicolon or a colon follows it; a path a comma or words follow is a value
// translated, and gives nothing.
func headerOverrides(document string) []cloudimport.Placement {
	return placementsOf(headerItems(document))
}

// headerItems returns each key of the document's overrides header with the
// lines of its description, in the order listed. The block is the comment
// lines after its opening line, each key at one indentation, a key beneath a
// parent whose description ends in a colon two deeper, and every deeper line
// continuing the key before it; a comment line indented less, an empty one or
// a line of the document ends it.
func headerItems(document string) []*headerItem {
	lines := strings.Split(document, "\n")
	start := slices.IndexFunc(lines, func(l string) bool {
		body, ok := strings.CutPrefix(strings.TrimSpace(l), "#")
		return ok && strings.TrimSpace(body) == overridesHeader
	})
	if start < 0 {
		return nil
	}
	var keys []*headerItem
	var parent *headerItem
	indent := -1
	for _, l := range lines[start+1:] {
		body, ok := strings.CutPrefix(strings.TrimSpace(l), "#")
		text := strings.TrimSpace(body)
		if !ok || text == "" {
			break
		}
		at := len(body) - len(strings.TrimLeft(body, " "))
		m := headerEntry.FindStringSubmatch(text)
		switch {
		case indent < 0 && m == nil:
			return nil
		case indent < 0 || at == indent:
			if m == nil {
				return keys
			}
			indent = at
			k := &headerItem{key: m[1]}
			if m[2] != "" {
				k.desc = []string{m[2]}
			}
			keys, parent = append(keys, k), k
		case at < indent:
			return keys
		case at == indent+2 && m != nil && parent != nil &&
			strings.HasSuffix(strings.Join(parent.desc, " "), ":"):
			k := &headerItem{key: parent.key + "." + m[1]}
			if m[2] != "" {
				k.desc = []string{m[2]}
			}
			keys = append(keys, k)
		default:
			if len(keys) == 0 {
				continue
			}
			last := keys[len(keys)-1]
			if len(last.desc) == 0 {
				text = strings.TrimSpace(strings.TrimPrefix(text, "-"))
			}
			last.desc = append(last.desc, text)
		}
	}
	return keys
}

// headerItem is one key of the overrides header with the lines of its
// description.
type headerItem struct {
	key  string
	desc []string
}

// placementsOf returns each header key whose description names a path,
// placed from it.
func placementsOf(items []*headerItem) []cloudimport.Placement {
	var out []cloudimport.Placement
	for _, it := range items {
		if from := descriptionPath(strings.Join(it.desc, " ")); from != nil {
			out = append(out, cloudimport.Placement{From: from, Key: it.key})
		}
	}
	return out
}

// descriptionPath returns the path the description's first parenthesis opens
// with, where a path ends it or a semicolon or a colon follows it; nil
// otherwise.
func descriptionPath(desc string) []string {
	open := strings.IndexByte(desc, '(')
	if open < 0 {
		return nil
	}
	depth, end := 0, -1
	for i := open; i < len(desc) && end < 0; i++ {
		switch desc[i] {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				end = i
			}
		}
	}
	if end < 0 {
		return nil
	}
	inner := desc[open+1 : end]
	path := headerPath.FindString(inner)
	if path == "" {
		return nil
	}
	if rest := inner[len(path):]; rest != "" && rest[0] != ';' && rest[0] != ':' {
		return nil
	}
	// Each segment is read whole: a member name as written, and a bracketed
	// index as the index's digits, the segment a list item is placed at. A
	// member name never begins with a digit, so an index segment is never the
	// spelling of a member's.
	var out []string
	for _, seg := range headerSegment.FindAllString(path, -1) {
		if index, ok := strings.CutPrefix(seg, "["); ok {
			seg = strings.TrimSuffix(index, "]")
		}
		out = append(out, seg)
	}
	return out
}

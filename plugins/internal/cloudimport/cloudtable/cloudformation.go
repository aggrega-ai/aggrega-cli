// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/template/parse"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfnimport/cfnrules"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// A cloudformation Recipe's fragments are CloudFormation YAML written as a Go
// template. The extractor reads them as written and evaluates nothing: the
// YAML lines give each value's path, each action's pipeline is read for what
// it yields — a value, an infra key, an override, a credential slot, the
// instance's logical ID, a literal — and a branch is read as taken or absent
// only where the fragment decides it itself, by a variable bound to a literal
// that nothing assigns again.

// discriminators are the properties whose literal text at the instance's
// resource says which product a resource of its type runs: the engine of a
// database, cache or broker, the protocol of an API. Every other literal a
// Recipe writes is a setting the Recipe chooses, which a resource of another
// setting is still an instance of.
var discriminators = []string{"Engine", "EngineType", "ProtocolType"}

// otherClose ends an action written in canonical text, which actionMark opens.
const otherClose = "\x02"

// logicalRun matches a logical ID written in canonical text: letters, digits,
// the instance's logical ID and other actions.
const logicalRun = `((?:[A-Za-z0-9\x00]|\x01[^\x02]*\x02)+)`

// refWhole and getAttWhole match canonical text that is one Ref or one GetAtt
// written in short form and nothing else, capturing the logical ID and the
// attribute; refAny matches either anywhere, capturing the logical ID.
var (
	refWhole    = regexp.MustCompile(`^!Ref ` + logicalRun + `$`)
	getAttWhole = regexp.MustCompile(`^!GetAtt ` + logicalRun + `\.([A-Za-z0-9]+(?:\.[A-Za-z0-9]+)*)$`)
	refAny      = regexp.MustCompile(`!(?:Ref|GetAtt) ` + logicalRun)
)

// symKind is what the extractor reads a pipeline to yield.
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

// passThrough are the functions whose result the extractor reads as the value
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
// variable p assigns, or one unknown names, yields nothing the extractor
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

// tmpl is a template's text parsed with the build's function set, each
// action, branch and range of its tree by where it is written.
type tmpl struct {
	nodes []posNode
}

// posNode is one node of a template's tree and where it is written.
type posNode struct {
	pos  int
	node parse.Node
}

// parseTmpl parses text and indexes its nodes.
func parseTmpl(text string) (tmpl, error) {
	root, err := gotmpl.ParseTree(text, "{{", "}}")
	if err != nil {
		return tmpl{}, err
	}
	var t tmpl
	var walk func(parse.Node)
	walk = func(n parse.Node) {
		switch n := n.(type) {
		case *parse.ListNode:
			if n == nil {
				return
			}
			for _, c := range n.Nodes {
				walk(c)
			}
		case *parse.ActionNode:
			t.nodes = append(t.nodes, posNode{pos: int(n.Pos), node: n})
		case *parse.IfNode:
			t.nodes = append(t.nodes, posNode{pos: int(n.Pos), node: n})
			walk(n.List)
			walk(n.ElseList)
		case *parse.WithNode:
			t.nodes = append(t.nodes, posNode{pos: int(n.Pos), node: n})
			walk(n.List)
			walk(n.ElseList)
		case *parse.RangeNode:
			t.nodes = append(t.nodes, posNode{pos: int(n.Pos), node: n})
			walk(n.List)
			walk(n.ElseList)
		}
	}
	walk(root)
	sort.SliceStable(t.nodes, func(i, j int) bool { return t.nodes[i].pos < t.nodes[j].pos })
	return t, nil
}

// at returns the node the action written from start to end opens, nil where
// it opens none, as an else or an end does.
func (t tmpl) at(start, end int) parse.Node {
	i := sort.Search(len(t.nodes), func(i int) bool { return t.nodes[i].pos >= start })
	if i < len(t.nodes) && t.nodes[i].pos < end {
		return t.nodes[i].node
	}
	return nil
}

// action returns the pipeline of the plain action written from start to end,
// nil where none is written there.
func (t tmpl) action(start, end int) *parse.PipeNode {
	if a, ok := t.at(start, end).(*parse.ActionNode); ok {
		return a.Pipe
	}
	return nil
}

// canonical writes text, written at at in t's text where sc holds, with each
// action yielding the instance's logical ID as symbolMark and every other
// action as its normalized text between actionMark and otherClose, so two
// spellings of one logical ID compare equal.
func (t tmpl) canonical(text string, at int, sc scope) string {
	var b strings.Builder
	last := 0
	for _, s := range scanActions(text) {
		b.WriteString(text[last:s.start])
		last = s.end
		if p := t.action(at+s.start, at+s.end); p != nil && len(p.Decl) == 0 && sc.pipe(p).kind == symLogicalID {
			b.WriteString(symbolMark)
			continue
		}
		b.WriteString(actionMark + normalize(s.inner) + otherClose)
	}
	b.WriteString(text[last:])
	return b.String()
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

// decision is what the extractor reads of whether a branch is written.
type decision int

// The decisions on a branch.
const (
	// undecided is a branch the build decides when it renders.
	undecided decision = iota
	// taken is a branch the fragment always writes.
	taken
	// absent is a branch the fragment never writes.
	absent
)

// decide returns what a condition decides: a variable bound to a literal,
// or not applied to one, decides by the literal; any other condition leaves
// the branch undecided.
func decide(p *parse.PipeNode, sc scope) decision {
	if p == nil || len(p.Decl) > 0 || len(p.Cmds) != 1 {
		return undecided
	}
	args := p.Cmds[0].Args
	negate := false
	if fn, ok := args[0].(*parse.IdentifierNode); ok && fn.Ident == "not" && len(args) == 2 {
		negate, args = true, args[1:]
	}
	if len(args) != 1 {
		return undecided
	}
	v, ok := args[0].(*parse.VariableNode)
	if !ok || len(v.Ident) != 1 || v.Ident[0] == "$" {
		return undecided
	}
	s := sc.vars[v.Ident[0]]
	switch {
	case s.kind != symLiteral:
		return undecided
	case s.truthy != negate:
		return taken
	}
	return absent
}

// branchFrame is an open branch, range or definition of a fragment.
type branchFrame struct {
	kind     string
	state    decision
	anyTaken bool
	// rebinds reports that the dot is dot within the frame.
	rebinds bool
	dot     sym
}

// cfnLine is one line of a fragment's YAML: a mapping key or a list item, at
// its path, with the value it writes, empty for a key opening a mapping or a
// list. at is where the value starts in the fragment's text, keyAt where a
// mapping key does; conditional reports that a branch the fragment does not
// decide writes it, repeated that a range does; sc is the scope it is
// written in.
type cfnLine struct {
	path        []string
	key         string
	keyAt       int
	value       string
	at          int
	conditional bool
	repeated    bool
	sc          scope
}

// cfnFragment is a fragment read line by line.
type cfnFragment struct {
	t     tmpl
	lines []cfnLine
}

// readFragment reads a fragment: a line holding actions alone opens, divides
// or closes a branch, or binds a variable, and every other line is a mapping
// key or a list item placed by its indentation, as readTemplateYAML places
// it. A line inside a branch the fragment decides is absent is not read, nor
// one inside a definition; a line within a block scalar is the scalar's text.
func readFragment(text string) (*cfnFragment, error) {
	t, err := parseTmpl(text)
	if err != nil {
		return nil, err
	}
	assigned := map[string]bool{}
	for _, n := range t.nodes {
		if a, ok := n.node.(*parse.ActionNode); ok && a.Pipe.IsAssign {
			for _, d := range a.Pipe.Decl {
				assigned[d.Ident[0]] = true
			}
		}
	}
	type yamlFrame struct {
		indent int
		seg    string
		item   bool
		id     int
	}
	var (
		f        = &cfnFragment{t: t}
		stack    []yamlFrame
		branches []branchFrame
		counters = map[int]int{}
		ids      = 0
		scalar   = -1
		vars     = map[string]sym{}
	)
	now := func() scope {
		out := scope{dot: sym{kind: symData}, vars: vars}
		for _, b := range branches {
			if b.rebinds {
				out.dot = b.dot
			}
		}
		return out
	}
	state := func() (absentNow, conditional, repeated bool) {
		for _, b := range branches {
			absentNow = absentNow || b.state == absent
			conditional = conditional || b.state == undecided
			repeated = repeated || b.kind == "range"
		}
		return
	}
	control := func(s span, lineStart int) {
		node := t.at(lineStart+s.start, lineStart+s.end)
		cur := now()
		absentNow, _, _ := state()
		word := controlWord(s.inner)
		switch word {
		case "if":
			fr := branchFrame{kind: word}
			if n, ok := node.(*parse.IfNode); ok {
				fr.state = decide(n.Pipe, cur)
			}
			fr.anyTaken = fr.state == taken
			branches = append(branches, fr)
		case "with":
			fr := branchFrame{kind: word, rebinds: true}
			if n, ok := node.(*parse.WithNode); ok {
				fr.state, fr.dot = decide(n.Pipe, cur), cur.pipe(n.Pipe)
				if !absentNow {
					vars = cur.declare(n.Pipe, assigned).vars
				}
			}
			fr.anyTaken = fr.state == taken
			branches = append(branches, fr)
		case "range":
			if n, ok := node.(*parse.RangeNode); ok && n.Pipe != nil && len(n.Pipe.Decl) > 0 {
				vars = maps.Clone(vars)
				for _, d := range n.Pipe.Decl {
					vars[d.Ident[0]] = sym{}
				}
			}
			branches = append(branches, branchFrame{kind: word, rebinds: true})
		case "define", "block":
			branches = append(branches, branchFrame{kind: word, state: absent})
		case "else":
			if len(branches) == 0 {
				return
			}
			top := &branches[len(branches)-1]
			_, rest, _ := strings.Cut(normalize(s.inner), " ")
			sub, _, _ := strings.Cut(rest, " ")
			cond := undecided
			switch n := node.(type) {
			case *parse.IfNode:
				cond = decide(n.Pipe, cur)
			case *parse.WithNode:
				cond = decide(n.Pipe, cur)
			}
			switch {
			case top.kind == "define" || top.kind == "block":
			case top.anyTaken:
				top.state = absent
			case top.state == absent && sub == "":
				top.state = taken
			case top.state == absent:
				top.state = cond
			}
			top.anyTaken = top.anyTaken || top.state == taken
			top.rebinds = false
			if n, ok := node.(*parse.WithNode); ok && sub == "with" {
				top.rebinds, top.dot = true, cur.pipe(n.Pipe)
			}
		case "end":
			if len(branches) > 0 {
				branches = branches[:len(branches)-1]
			}
		default:
			if a, ok := node.(*parse.ActionNode); ok && !absentNow {
				vars = cur.declare(a.Pipe, assigned).vars
			}
		}
	}
	stackPath := func() []string {
		out := make([]string, 0, len(stack)+1)
		for _, fr := range stack {
			out = append(out, fr.seg)
		}
		return out
	}
	offset := 0
	for _, raw := range strings.Split(text, "\n") {
		lineStart := offset
		offset += len(raw) + 1
		line := strings.TrimRight(raw, " \t\r")
		spans := scanActions(line)
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
				control(s, lineStart)
			}
			continue
		}
		absentNow, conditional, repeated := state()
		if absentNow {
			continue
		}
		for _, s := range spans {
			if controlWord(s.inner) == "" {
				if p := t.action(lineStart+s.start, lineStart+s.end); p != nil {
					vars = now().declare(p, assigned).vars
				}
			}
		}
		cur := now()
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
			stack = append(stack, yamlFrame{indent: indent, seg: strconv.Itoa(index), item: true, id: ids})
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
			if item {
				f.lines = append(f.lines, cfnLine{path: stackPath(), value: rest, at: restAt,
					conditional: conditional, repeated: repeated, sc: cur})
			}
			continue
		}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		keyAt := restAt
		if rest[0] == '"' || rest[0] == '\'' {
			keyAt++
		}
		l := cfnLine{path: append(stackPath(), key), key: key, keyAt: keyAt, value: value, at: restAt + valueAt,
			conditional: conditional, repeated: repeated, sc: cur}
		switch {
		case value == "":
			ids++
			stack = append(stack, yamlFrame{indent: indent, seg: key, id: ids})
			f.lines = append(f.lines, l)
		case blockScalar.MatchString(value):
			scalar = indent
		default:
			f.lines = append(f.lines, l)
		}
	}
	return f, nil
}

// cfnResource is one resource a fragment's Resources block writes: its key
// as written and in canonical text, its literal type, whether a branch the
// fragment does not decide or a range writes it, and its lines, their paths
// beneath it.
type cfnResource struct {
	key, canon            string
	typ                   string
	conditional, repeated bool
	lines                 []cfnLine
	frag                  *cfnFragment
}

// outside reports whether r is written outside every branch the fragment
// does not decide.
func (r *cfnResource) outside() bool {
	return !r.conditional && !r.repeated
}

// String names r as the reader finds it in the Recipe: its key and its type.
func (r *cfnResource) String() string {
	typ := r.typ
	if typ == "" {
		typ = "of no literal type"
	}
	return r.key + " (" + typ + ")"
}

// resources returns the resources f's Resources block writes as lines, in the
// order written.
func (f *cfnFragment) resources() []*cfnResource {
	var out []*cfnResource
	var current *cfnResource
	for _, l := range f.lines {
		if len(l.path) < 2 || l.path[0] != "Resources" {
			continue
		}
		if len(l.path) == 2 {
			current = nil
			if l.value == "" {
				current = &cfnResource{key: l.key, canon: f.t.canonical(l.key, l.keyAt, l.sc),
					conditional: l.conditional, repeated: l.repeated, frag: f}
				out = append(out, current)
			}
			continue
		}
		if current == nil || l.path[1] != current.key {
			continue
		}
		l.path = slices.Clone(l.path[2:])
		if len(l.path) == 1 && l.path[0] == "Type" && len(scanActions(l.value)) == 0 {
			current.typ = unquote(l.value)
		}
		current.lines = append(current.lines, l)
	}
	return out
}

// reference returns the logical ID, in canonical text, that l's value
// references whole with Ref, or with GetAtt where getAtt is set.
func (f *cfnFragment) reference(l cfnLine, getAtt bool) (string, bool) {
	text := f.t.canonical(l.value, l.at, l.sc)
	if m := refWhole.FindStringSubmatch(text); m != nil {
		return m[1], true
	}
	if m := getAttWhole.FindStringSubmatch(text); getAtt && m != nil {
		return m[1], true
	}
	return "", false
}

// providesRef is what one provides key publishes of a resource: the logical
// ID, in canonical text, a branch of it reads whole, and the attribute,
// empty for a Ref.
type providesRef struct {
	key, logical string
	attr         []string
}

// readProvides returns each provides key a branch of which is one Ref or one
// GetAtt, where every such branch names one logical ID and attribute, and the
// logical ID of every resource any provides text references.
func readProvides(provides []entry) ([]providesRef, map[string]bool) {
	var whole []providesRef
	named := map[string]bool{}
	for _, e := range provides {
		t, err := parseTmpl(e.value)
		if err != nil {
			continue
		}
		var segments []string
		var b strings.Builder
		last := 0
		for _, s := range scanActions(e.value) {
			if controlWord(s.inner) == "" {
				continue
			}
			b.WriteString(t.canonical(e.value[last:s.start], last, dataScope()))
			segments, last = append(segments, b.String()), s.end
			b.Reset()
		}
		segments = append(segments, t.canonical(e.value[last:], last, dataScope()))
		var refs []providesRef
		for _, seg := range segments {
			seg = strings.TrimSpace(seg)
			for _, m := range refAny.FindAllStringSubmatch(seg, -1) {
				named[m[1]] = true
			}
			var ref providesRef
			if m := refWhole.FindStringSubmatch(seg); m != nil {
				ref = providesRef{key: e.key, logical: m[1]}
			} else if m := getAttWhole.FindStringSubmatch(seg); m != nil {
				ref = providesRef{key: e.key, logical: m[1], attr: strings.Split(m[2], ".")}
			} else {
				continue
			}
			if !slices.ContainsFunc(refs, func(r providesRef) bool {
				return r.logical == ref.logical && slices.Equal(r.attr, ref.attr)
			}) {
				refs = append(refs, ref)
			}
		}
		if len(refs) == 1 {
			whole = append(whole, refs[0])
		}
	}
	return whole, named
}

// resourceList names rs as the reader finds them in the Recipe.
func resourceList(rs []*cfnResource) string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.String()
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// primaryOf returns the resource that is the instance: the one keyed by the
// instance's logical ID alone; else the one written outside every branch the
// fragment does not decide; else, of those written outside every such
// branch, the one the Recipe's provides reference. It refuses a fragment
// whose text does not say which.
func primaryOf(resources []*cfnResource, named map[string]bool) (*cfnResource, error) {
	var exact, outside, picked []*cfnResource
	for _, r := range resources {
		if r.canon == symbolMark {
			exact = append(exact, r)
		}
		if r.outside() {
			outside = append(outside, r)
		}
		if r.outside() && named[r.canon] {
			picked = append(picked, r)
		}
	}
	const keyIt = "key the instance's resource by the logical ID alone"
	switch {
	case len(exact) == 1:
		return exact[0], nil
	case len(exact) > 1:
		return nil, fmt.Errorf("keys %s by the instance's logical ID alone, so its text does not say which is the "+
			"instance: key one resource by the logical ID alone", resourceList(exact))
	case len(outside) == 1:
		return outside[0], nil
	case len(outside) == 0:
		return nil, fmt.Errorf("writes %s, each inside a branch its text does not decide and none keyed by the "+
			"instance's logical ID alone, so its text does not say which is the instance: %s, or write it "+
			"outside every branch", resourceList(resources), keyIt)
	case len(picked) == 1:
		return picked[0], nil
	case len(picked) == 0:
		return nil, fmt.Errorf("writes %s outside every branch, none keyed by the instance's logical ID alone, "+
			"and its provides reference none of them, so its text does not say which is the instance: %s, or "+
			"publish an attribute of it in provides", resourceList(outside), keyIt)
	}
	return nil, fmt.Errorf("writes %s outside every branch, none keyed by the instance's logical ID alone, and its "+
		"provides reference each of them, so no one resource stands for the instance: %s, or list the Recipe in "+
		"notImported in the importer's table.go with why", resourceList(picked), keyIt)
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

// pillar is where a line's value goes in the instance.
type pillar int

// The pillars a line's value goes to.
const (
	pillarValues pillar = iota
	pillarInfra
	pillarOverrides
	pillarSecrets
)

// placed is one placement a line gives, and its pillar.
type placed struct {
	pillar pillar
	p      cloudimport.Placement
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
	switch a := args[0].(type) {
	case *parse.FieldNode:
	case *parse.VariableNode:
		if a.Ident[0] != "$" || len(a.Ident) < 2 {
			return sym{}, false, false
		}
	default:
		return sym{}, false, false
	}
	return sc.node(args[0]), toJSON, true
}

// overrideRead returns the infra.overrides key the value text, written at at
// in f's text, reads whole: every branch of it that reads an override is one
// action reading that one override, through a variable, optional, a default,
// the dot a with binds to it, or toJson.
func (f *cfnFragment) overrideRead(text string, at int, sc scope) (string, bool) {
	keys := map[string]bool{}
	composed := false
	spans := scanActions(text)
	segStart := 0
	for i, s := range spans {
		if controlWord(s.inner) != "" {
			segStart = s.end
			continue
		}
		p := f.t.action(at+s.start, at+s.end)
		if p == nil || len(p.Decl) > 0 {
			continue
		}
		v := sc.pipe(p)
		if v.kind != symOverride {
			continue
		}
		segEnd := len(text)
		for _, n := range spans[i+1:] {
			if controlWord(n.inner) != "" {
				segEnd = n.start
				break
			}
		}
		if strings.TrimSpace(text[segStart:segEnd]) != text[s.start:s.end] {
			composed = true
		}
		keys[v.key] = true
	}
	if composed || len(keys) != 1 {
		return "", false
	}
	for k := range keys {
		return k, true
	}
	return "", false
}

// secretPath returns the property a credential read at path is placed at:
// path cut before its first segment naming an intrinsic function.
func secretPath(path []string) []string {
	for i, s := range path {
		if s == "Ref" || strings.HasPrefix(s, "Fn::") {
			return slices.Clone(path[:i])
		}
	}
	return slices.Clone(path)
}

// placements returns what the line l of f gives, its value placed from the
// property path from: a value or an infra key one action reads whole, bare,
// quoted — read as text — or handed to toJson; the override every branch
// reading one reads whole, where header lists it; and each credential slot
// an action of a property's value reads, placed at the property. A line a
// range writes gives nothing.
func (f *cfnFragment) placements(l cfnLine, from []string, header map[string]bool) []placed {
	if l.repeated || l.value == "" {
		return nil
	}
	var out []placed
	unq := unquote(l.value)
	quoted := unq != l.value
	at := l.at
	if quoted {
		at++
	}
	if spans := scanActions(unq); len(spans) == 1 && spans[0].start == 0 && spans[0].end == len(unq) {
		if v, toJSON, ok := wholeRead(f.t.action(at, at+spans[0].end), l.sc); ok {
			var conv cloudimport.Conv
			if quoted && !toJSON {
				conv = "string"
			}
			switch v.kind {
			case symValue:
				out = append(out, placed{pillarValues, cloudimport.Placement{From: from, Key: v.key, Conv: conv}})
			case symInfraKey:
				out = append(out, placed{pillarInfra, cloudimport.Placement{From: from, Key: v.key, Conv: conv}})
			}
		}
	}
	if k, ok := f.overrideRead(unq, at, l.sc); ok && header[k] {
		out = append(out, placed{pillarOverrides, cloudimport.Placement{From: from, Key: k}})
	}
	if len(from) > 0 && from[0] == "Properties" {
		var slots []string
		for _, s := range scanActions(l.value) {
			if p := f.t.action(l.at+s.start, l.at+s.end); p != nil {
				if v := l.sc.pipe(p); v.readsSlot() && !slices.Contains(slots, v.key) {
					slots = append(slots, v.key)
				}
			}
		}
		for _, slot := range slots {
			out = append(out, placed{pillarSecrets, cloudimport.Placement{From: secretPath(from), Key: slot}})
		}
	}
	return out
}

// discriminator returns the predicate the line l of the instance's resource
// gives: a discriminator written as a literal at its own property outside
// every branch the fragment does not decide.
func discriminator(l cfnLine) (cloudimport.Pred, bool) {
	if len(l.path) != 2 || l.path[0] != "Properties" || !slices.Contains(discriminators, l.path[1]) ||
		l.conditional || l.repeated || l.value == "" || len(scanActions(l.value)) > 0 ||
		strings.ContainsAny(l.value[:1], "!{[&*|>") {
		return cloudimport.Pred{}, false
	}
	return cloudimport.Pred{Path: slices.Clone(l.path), Op: cloudimport.OpEquals, Value: unquote(l.value)}, true
}

// rowBuilder gathers the row of a Recipe's instance.
type rowBuilder struct {
	row    cloudimport.Row
	header map[string]bool
}

// add adds what the line l of f gives, placed from from, keeping the first
// placement of each key, and of each slot at each property.
func (b *rowBuilder) add(f *cfnFragment, l cfnLine, from []string) {
	for _, p := range f.placements(l, from, b.header) {
		list := map[pillar]*[]cloudimport.Placement{pillarValues: &b.row.Values, pillarInfra: &b.row.Infra,
			pillarOverrides: &b.row.Overrides, pillarSecrets: &b.row.Secrets}[p.pillar]
		if !slices.ContainsFunc(*list, func(o cloudimport.Placement) bool {
			return o.Key == p.p.Key && (p.pillar != pillarSecrets || slices.Equal(o.From, p.p.From))
		}) {
			*list = append(*list, p.p)
		}
	}
}

// fold adds the folds of the child c linked to the instance at link: one for
// each value, infra key and override its lines give, written under that key,
// and where they give none, one taking the child whole, read at its link.
func (b *rowBuilder) fold(c *cfnResource, link []string) {
	var folds []cloudimport.Fold
	for _, l := range c.lines {
		for _, p := range c.frag.placements(l, l.path, b.header) {
			var key []string
			switch p.pillar {
			case pillarValues:
				key = append([]string{"values"}, strings.Split(p.p.Key, ".")...)
			case pillarInfra:
				key = append([]string{"infra"}, strings.Split(p.p.Key, ".")...)
			case pillarOverrides:
				key = []string{"infra", "overrides", p.p.Key}
			default:
				continue
			}
			fold := cloudimport.Fold{Type: c.typ, Key: key, From: p.p.From}
			if !slices.ContainsFunc(folds, func(o cloudimport.Fold) bool { return slices.Equal(o.Key, key) }) {
				folds = append(folds, fold)
			}
		}
	}
	if len(folds) == 0 {
		folds = []cloudimport.Fold{{Type: c.typ, From: slices.Clone(link)}}
	}
	for _, fold := range folds {
		if !slices.ContainsFunc(b.row.Folds, func(o cloudimport.Fold) bool {
			return strings.EqualFold(o.Type, fold.Type) && slices.Equal(o.Key, fold.Key) &&
				slices.Equal(o.From, fold.From)
		}) {
			b.row.Folds = append(b.row.Folds, fold)
		}
	}
}

// linkOf returns where the child c references primary through a parent link
// of its type, nil where none does.
func linkOf(c, primary *cfnResource, links []cfnrules.ParentLink) []string {
	for _, link := range links {
		if !strings.EqualFold(link.Child, c.typ) {
			continue
		}
		for _, l := range c.lines {
			if !pathIs(l.path, link.Path) || l.value == "" {
				continue
			}
			if logical, ok := c.frag.reference(l, true); ok && logical == primary.canon {
				return l.path
			}
		}
	}
	return nil
}

// headerKeys returns the keys the document's overrides header lists.
func headerKeys(document string) map[string]bool {
	keys := map[string]bool{}
	for _, it := range headerItems(document) {
		keys[it.key] = true
	}
	return keys
}

// extractCloudFormation reads one cloudformation Recipe's row literally,
// evaluating no template; or returns why it gives none; or refuses it,
// saying what to fix.
//
// Of the resources its fragments' Resources blocks write, the instance's is
// the one primaryOf chooses, and its literal type is the row's key; its
// discriminators written as literals are the When. Each of its lines places
// what it reads, as placements says, and so does each line of the resources
// an inline rule of the cfnrules package reads in place of the instance's
// reference to them, beneath the referencing property. Each resource a parent
// link of the cfnrules package links to the instance gives folds of its type.
// Any other resource written outside every branch the fragment does not
// decide is refused, naming it; one written inside such a branch is left out,
// and the note says so. A provides key a branch of which is one GetAtt of the
// instance's resource, or one Ref of it, publishes that attribute, or its
// identifier.
func extractCloudFormation(r recipeSource) (cloudimport.Row, string, error) {
	if r.source != nil {
		return cloudimport.Row{}, "it sources a module, and a cloudformation row is read from a fragment", nil
	}
	if len(r.fragments) == 0 {
		return cloudimport.Row{}, "it writes no fragment", nil
	}
	resources, err := fragmentResources(r)
	if err != nil {
		return cloudimport.Row{}, "", err
	}
	if len(resources) == 0 {
		return cloudimport.Row{}, "its fragment writes no resource", nil
	}
	provides, named := readProvides(r.provides)
	primary, err := primaryOf(resources, named)
	if err != nil {
		return cloudimport.Row{}, "", err
	}
	if primary.typ == "" {
		return cloudimport.Row{}, "", fmt.Errorf("writes the instance's resource %s with no literal Type: write "+
			"its type as a literal", primary.key)
	}
	b := rowBuilder{row: cloudimport.Row{Key: primary.typ, Form: cloudimport.FormRaw},
		header: headerKeys(r.document)}

	inlined := map[*cfnResource][]string{}
	var inlineOrder []*cfnResource
	for _, l := range primary.lines {
		if p, ok := discriminator(l); ok {
			b.row.When = append(b.row.When, p)
		}
		b.add(primary.frag, l, l.path)
		for _, in := range cfnrules.Inlines() {
			if !strings.EqualFold(in.Parent, primary.typ) || !pathIs(l.path, in.Path) || l.value == "" {
				continue
			}
			logical, ok := primary.frag.reference(l, false)
			if !ok {
				continue
			}
			for _, c := range resources {
				if c != primary && c.canon == logical && strings.EqualFold(c.typ, in.Child) && inlined[c] == nil {
					inlined[c] = slices.Clone(l.path)
					inlineOrder = append(inlineOrder, c)
				}
			}
		}
	}
	for _, c := range inlineOrder {
		for _, l := range c.lines {
			if len(l.path) > 1 && l.path[0] == "Properties" {
				b.add(c.frag, l, slices.Concat(inlined[c], l.path[1:]))
			}
		}
	}

	var notes []string
	links := cfnrules.ParentLinks()
	for _, c := range resources {
		if c == primary || inlined[c] != nil {
			continue
		}
		if link := linkOf(c, primary, links); link != nil {
			b.fold(c, link)
			continue
		}
		if !c.outside() {
			notes = append(notes, "the resource "+c.String()+", written in a branch, is read as part of the "+
				"instance by no inline rule or parent link, so the row leaves it out")
			continue
		}
		return cloudimport.Row{}, "", fmt.Errorf("writes the resource %s outside every branch beside the "+
			"instance's %s, and no rule reads it as part of the instance — no inline rule of %s references it "+
			"and no parent link of its type names the instance: add the rule to the cfnrules package, or list "+
			"the Recipe in notImported in the importer's table.go with why", c, primary, primary.typ)
	}

	for _, p := range provides {
		if p.logical == primary.canon {
			b.row.Provides = append(b.row.Provides, cloudimport.Placement{From: p.attr, Key: p.key})
		}
	}
	return b.row, strings.Join(notes, "; "), nil
}

// fragmentResources returns the resources r's fragments write, in the order
// written.
func fragmentResources(r recipeSource) ([]*cfnResource, error) {
	var resources []*cfnResource
	for _, text := range r.fragments {
		f, err := readFragment(text)
		if err != nil {
			return nil, fmt.Errorf("writes a fragment that does not parse as a template (%v): correct the fragment",
				err)
		}
		resources = append(resources, f.resources()...)
	}
	return resources, nil
}

// slotFeature is the one feature type a Recipe's assertions admit at a
// credential slot.
type slotFeature struct {
	recipe, slot, feature string
}

// admittedFeatures returns, for each credential slot of r's Component at which
// r's assertions admit one feature type alone, that type, ordered by slot. An
// assertion admits a type at a slot where a branch fails the build unless the
// type of the slot's attachment, read as its text or as the text before its
// first colon, is that type's name.
func admittedFeatures(r recipeSource) []slotFeature {
	admits := map[string][]string{}
	var list func(*parse.ListNode, scope) scope
	list = func(n *parse.ListNode, sc scope) scope {
		if n == nil {
			return sc
		}
		for _, c := range n.Nodes {
			switch c := c.(type) {
			case *parse.ActionNode:
				sc = sc.declare(c.Pipe, nil)
			case *parse.IfNode:
				if slot, feature, ok := admission(c.Pipe, sc); ok && fails(c.List) &&
					!slices.Contains(admits[slot], feature) {
					admits[slot] = append(admits[slot], feature)
				}
				list(c.List, sc)
				list(c.ElseList, sc)
			case *parse.WithNode:
				inner := sc.declare(c.Pipe, nil)
				inner.dot = sc.pipe(c.Pipe)
				list(c.List, inner)
				list(c.ElseList, sc)
			case *parse.RangeNode:
				inner := scope{vars: maps.Clone(sc.vars)}
				if c.Pipe != nil {
					for _, d := range c.Pipe.Decl {
						inner.vars[d.Ident[0]] = sym{}
					}
				}
				list(c.List, inner)
				list(c.ElseList, sc)
			}
		}
		return sc
	}
	for _, text := range r.assertions {
		root, err := gotmpl.ParseTree(text, "{{", "}}")
		if err != nil {
			continue
		}
		if l, ok := root.(*parse.ListNode); ok {
			list(l, dataScope())
		}
	}
	var out []slotFeature
	for _, slot := range slices.Sorted(maps.Keys(admits)) {
		if len(admits[slot]) == 1 {
			out = append(out, slotFeature{recipe: r.name, slot: slot, feature: admits[slot][0]})
		}
	}
	return out
}

// admission returns the slot and the feature type a condition compares the
// type of the slot's attachment with, by ne.
func admission(p *parse.PipeNode, sc scope) (string, string, bool) {
	if p == nil || len(p.Decl) > 0 || len(p.Cmds) != 1 || len(p.Cmds[0].Args) != 3 {
		return "", "", false
	}
	args := p.Cmds[0].Args
	if fn, ok := args[0].(*parse.IdentifierNode); !ok || fn.Ident != "ne" {
		return "", "", false
	}
	for _, pair := range [][2]parse.Node{{args[1], args[2]}, {args[2], args[1]}} {
		lit, ok := pair[1].(*parse.StringNode)
		if v := sc.node(pair[0]); ok && v.kind == symSlotType {
			return v.key, lit.Text, true
		}
	}
	return "", "", false
}

// fails reports whether a branch calls fail, directly or in a branch of its
// own.
func fails(n *parse.ListNode) bool {
	if n == nil {
		return false
	}
	for _, c := range n.Nodes {
		switch c := c.(type) {
		case *parse.ActionNode:
			for _, cmd := range c.Pipe.Cmds {
				if fn, ok := cmd.Args[0].(*parse.IdentifierNode); ok && fn.Ident == "fail" {
					return true
				}
			}
		case *parse.IfNode:
			if fails(c.List) || fails(c.ElseList) {
				return true
			}
		case *parse.WithNode:
			if fails(c.List) || fails(c.ElseList) {
				return true
			}
		}
	}
	return false
}

// generateSlotFeatures returns the feature types the Recipes of h's
// capability admit at a credential slot, each Recipe read at the version a
// reference naming it without one resolves to, ordered by Recipe and slot; a
// Recipe the hand table lists in notImported gives none.
func generateSlotFeatures(h *hubCatalog, hand cloudimport.Table) ([]slotFeature, error) {
	notImported := map[string]bool{}
	for _, n := range hand.NotImported {
		notImported[n.Recipe] = true
	}
	var out []slotFeature
	for _, name := range h.recipeNames() {
		r, ok := h.recipe(name)
		if !ok || notImported[name] {
			continue
		}
		src, err := h.read(r)
		if err != nil {
			return nil, err
		}
		out = append(out, admittedFeatures(src)...)
	}
	return out, nil
}

// generateSlotFeaturesFromHub generates f's slot features from the checkout
// at hub.
func generateSlotFeaturesFromHub(hub string, f format, hand cloudimport.Table) ([]slotFeature, error) {
	h, err := loadHub(hub, f.capability)
	if err != nil {
		return nil, err
	}
	return generateSlotFeatures(h, hand)
}

// logicalID is how a Recipe names the resource that is its instance in the
// stack the build writes: the capitalised words of the instance's key,
// followed by suffix.
type logicalID struct {
	recipe, suffix string
}

// logicalIDOf returns the literal text r keys the resource that is its
// instance by after the instance's logical ID — the capitalised words of the
// instance's key, written as that action, as the local logical_id holding it
// alone or as a variable bound to either — and false where r keys it
// otherwise, declares a local logical_id holding anything else, or its text
// does not say which resource is the instance.
func logicalIDOf(r recipeSource) (string, bool) {
	if r.source != nil || len(r.fragments) == 0 {
		return "", false
	}
	if text, declared := r.locals["logical_id"]; declared && !isSymbol(text, []string{foldedName}) {
		return "", false
	}
	resources, err := fragmentResources(r)
	if err != nil || len(resources) == 0 {
		return "", false
	}
	_, named := readProvides(r.provides)
	primary, err := primaryOf(resources, named)
	if err != nil {
		return "", false
	}
	suffix, keyed := strings.CutPrefix(primary.canon, symbolMark)
	if !keyed || strings.ContainsAny(suffix, symbolMark+actionMark+otherClose) {
		return "", false
	}
	return suffix, true
}

// generateLogicalIDs returns how each Recipe of h's capability names the
// resource that is its instance, where its text says so as logicalIDOf
// reads it, each Recipe read at the version a reference naming it without one
// resolves to, ordered by Recipe; a Recipe the hand table lists in
// notImported gives none.
func generateLogicalIDs(h *hubCatalog, hand cloudimport.Table) ([]logicalID, error) {
	notImported := map[string]bool{}
	for _, n := range hand.NotImported {
		notImported[n.Recipe] = true
	}
	var out []logicalID
	for _, name := range h.recipeNames() {
		r, ok := h.recipe(name)
		if !ok || notImported[name] {
			continue
		}
		src, err := h.read(r)
		if err != nil {
			return nil, err
		}
		if suffix, ok := logicalIDOf(src); ok {
			out = append(out, logicalID{recipe: name, suffix: suffix})
		}
	}
	return out, nil
}

// recipeFacts are what a format's table lists of its Recipes beside the rows:
// the one feature type each Recipe admits at a credential slot, and how each
// names the resource that is its instance.
type recipeFacts struct {
	slots []slotFeature
	ids   []logicalID
}

// generateFactsFromHub generates the facts f's table lists of the Recipes of
// the checkout at hub, none where f lists none.
func generateFactsFromHub(hub string, f format, hand cloudimport.Table) (recipeFacts, error) {
	if !f.slots {
		return recipeFacts{}, nil
	}
	h, err := loadHub(hub, f.capability)
	if err != nil {
		return recipeFacts{}, err
	}
	slots, err := generateSlotFeatures(h, hand)
	if err != nil {
		return recipeFacts{}, err
	}
	ids, err := generateLogicalIDs(h, hand)
	if err != nil {
		return recipeFacts{}, err
	}
	return recipeFacts{slots: slots, ids: ids}, nil
}

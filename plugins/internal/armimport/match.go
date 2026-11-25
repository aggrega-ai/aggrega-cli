// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
)

// identity is a resource's type, name and scope, as a reference matches them.
type identity struct {
	typ string
	// segs are the segments of the resource's full name, one per segment of
	// its type past the namespace, each printed canonically.
	segs []string
	// sub and group are the subscription and the resource group the resource
	// is declared in, printed canonically; empty for the template's own.
	sub, group string
}

// param returns the declared name of the parameter name the template's
// expressions read — its own, or for a template evaluated in the outer scope
// the scope's — matched as Azure Resource Manager matches it: exactly, else
// regardless of case.
func (t *template) param(name string) (string, bool) {
	t = t.scope()
	if t.params.exact(name) != nil {
		return name, true
	}
	for _, e := range t.params {
		if strings.EqualFold(e.Key, name) {
			return e.Key, true
		}
	}
	return "", false
}

// variable returns the declared name and the value of the variable name the
// template's expressions read, matched as a parameter is; false where the
// template declares none, or a variable copy loop computes it.
func (t *template) variable(name string) (string, json.RawMessage, bool) {
	t = t.scope()
	for _, loop := range t.loopVars {
		if strings.EqualFold(loop, name) {
			return "", nil, false
		}
	}
	if raw := t.vars.exact(name); raw != nil {
		return name, raw, true
	}
	for _, e := range t.vars {
		if strings.EqualFold(e.Key, name) {
			return e.Key, e.Value, true
		}
	}
	return "", nil, false
}

// canonical returns n with every variable it reads written in place where the
// variable holds text or a scalar, and every parameter of a nested template
// the value it takes, so two expressions reading one value through a
// variable, a parameter and directly print alike.
func (t *template) canonical(n armexpr.Node) armexpr.Node {
	return t.inline(n, nil)
}

// inline returns n with every variable it reads written in place where the
// variable holds text or a scalar; every parameter of a nested template
// written as the text or scalar the resource deploying it gives it, else as
// its default, read in the template that writes it; and, where l is the
// lowerer of t, each copyIndex call whose index l knows written as that index.
// A variable or a parameter reading itself, through others or directly, is
// left as the call reading it, and so is a parameter of the root template.
func (t *template) inline(n armexpr.Node, l *lowerer) armexpr.Node {
	return t.inlineWith(n, l, &inlining{budget: maxInlined})
}

// inlining is the state of one expression being written with what it reads
// in place: the variables and parameters it may still write, and those being
// written.
type inlining struct {
	budget int
	inside []inlined
}

// inlined is a variable, or a parameter, of a template being written in
// place.
type inlined struct {
	t     *template
	param bool
	name  string
}

// enter records that k is being written in place, false where it is
// already: it reads itself.
func (st *inlining) enter(k inlined) bool {
	if st.budget == 0 || slices.Contains(st.inside, k) {
		return false
	}
	st.budget--
	st.inside = append(st.inside, k)
	return true
}

// leave records that the variable or parameter last entered is written.
func (st *inlining) leave() {
	st.inside = st.inside[:len(st.inside)-1]
}

// inlineWith returns n inlined as [template.inline] says, sharing st with the
// expressions it writes in place.
func (t *template) inlineWith(n armexpr.Node, l *lowerer, st *inlining) armexpr.Node {
	var walk func(n armexpr.Node) armexpr.Node
	walk = func(n armexpr.Node) armexpr.Node {
		switch v := n.(type) {
		case armexpr.Call:
			if name, ok := oneTextArgument(v, "variables"); ok {
				if replaced, ok := t.inlined(name, st, walk); ok {
					return replaced
				}
				return v
			}
			if name, ok := oneTextArgument(v, "parameters"); ok {
				if replaced, ok := t.bound(name, l, st); ok {
					return replaced
				}
				return v
			}
			if v.Namespace == "" && strings.EqualFold(v.Name, "copyIndex") && l != nil {
				if i, ok := l.copyIndex(v); ok {
					return armexpr.Int{Text: strconv.Itoa(i), At: v.At}
				}
			}
			args := make([]armexpr.Node, len(v.Args))
			for i, a := range v.Args {
				args[i] = walk(a)
			}
			v.Args = args
			return v
		case armexpr.Member:
			v.Target = walk(v.Target)
			return v
		case armexpr.Index:
			v.Target, v.Index = walk(v.Target), walk(v.Index)
			return v
		}
		return n
	}
	return walk(n)
}

// maxInlined bounds the variables and parameters one expression has written
// in place, so ones reading each other several times cannot grow it without
// bound; past it each is left as the call reading it.
const maxInlined = 1000

// inlined returns the expression the variable name holds, written in place of
// the call reading it, false where it holds an object, an array or text that
// does not parse, or is being written in place already.
func (t *template) inlined(name string, st *inlining, walk func(armexpr.Node) armexpr.Node) (armexpr.Node,
	bool) {
	key, raw, found := t.variable(name)
	if !found {
		return nil, false
	}
	n, ok := expression(raw)
	if !ok {
		return nil, false
	}
	if s, isText := text(raw); !isText || !armexpr.IsExpression(s) {
		return n, true
	}
	if !st.enter(inlined{t: t.scope(), name: key}) {
		return nil, false
	}
	defer st.leave()
	return walk(n), true
}

// expression returns the JSON scalar raw as an expression: text as the string
// it is or the expression it holds, an integer, true, false or null; false
// for an object, an array, a fraction, and text that does not parse.
func expression(raw json.RawMessage) (armexpr.Node, bool) {
	switch jsonKind(raw) {
	case '"':
		s, _ := text(raw)
		if !armexpr.IsExpression(s) {
			return armexpr.String{Value: armexpr.LiteralText(s)}, true
		}
		n, err := armexpr.Parse(s)
		return n, err == nil
	case 't', 'f', 'n':
		literal := strings.TrimSpace(string(raw))
		return armexpr.Call{Name: literal}, literal == "true" || literal == "false" || literal == "null"
	case '{', '[', 0:
		return nil, false
	}
	literal := strings.TrimSpace(string(raw))
	if _, err := strconv.ParseInt(literal, 10, 64); err != nil {
		return nil, false
	}
	return armexpr.Int{Text: literal}, true
}

// pieces returns the text n is made of, in order: the arguments of a concat,
// and the text and the arguments a format with a literal format text joins,
// each read the same way; adjacent literal text joined and empty text
// dropped. Any other expression is one piece.
func (t *template) pieces(n armexpr.Node, depth int) []armexpr.Node {
	var raw []armexpr.Node
	c, isCall := n.(armexpr.Call)
	switch {
	case depth > maxNesting || !isCall || c.Namespace != "":
		raw = []armexpr.Node{n}
	case strings.EqualFold(c.Name, "concat"):
		for _, a := range c.Args {
			raw = append(raw, t.pieces(a, depth+1)...)
		}
	case strings.EqualFold(c.Name, "format"):
		raw = t.formatted(c, depth)
	default:
		raw = []armexpr.Node{n}
	}
	var out []armexpr.Node
	for _, p := range raw {
		s, isText := p.(armexpr.String)
		if isText && s.Value == "" {
			continue
		}
		if prev, ok := lastText(out); ok && isText {
			out[len(out)-1] = armexpr.String{Value: prev + s.Value}
			continue
		}
		out = append(out, p)
	}
	return out
}

// formatted returns the pieces of a format call c, or c itself as one piece
// where its format text is not literal or does not split.
func (t *template) formatted(c armexpr.Call, depth int) []armexpr.Node {
	if len(c.Args) == 0 {
		return []armexpr.Node{c}
	}
	layout, ok := c.Args[0].(armexpr.String)
	if !ok {
		return []armexpr.Node{c}
	}
	pieces, ok := formatPieces(layout.Value)
	if !ok {
		return []armexpr.Node{c}
	}
	var out []armexpr.Node
	for _, p := range pieces {
		if p.arg < 0 {
			out = append(out, armexpr.String{Value: p.text})
			continue
		}
		if p.arg+1 >= len(c.Args) {
			return []armexpr.Node{c}
		}
		out = append(out, t.pieces(c.Args[p.arg+1], depth+1)...)
	}
	return out
}

// lastText returns the text of the last of nodes, false where it is no text.
func lastText(nodes []armexpr.Node) (string, bool) {
	if len(nodes) == 0 {
		return "", false
	}
	s, ok := nodes[len(nodes)-1].(armexpr.String)
	return s.Value, ok
}

// segments splits pieces into the segments of a resource name, at each slash
// of their literal text.
func segments(pieces []armexpr.Node) [][]armexpr.Node {
	segs := [][]armexpr.Node{nil}
	for _, p := range pieces {
		s, isText := p.(armexpr.String)
		if !isText {
			segs[len(segs)-1] = append(segs[len(segs)-1], p)
			continue
		}
		for i, part := range strings.Split(s.Value, "/") {
			if i > 0 {
				segs = append(segs, nil)
			}
			if part != "" {
				segs[len(segs)-1] = append(segs[len(segs)-1], armexpr.String{Value: part})
			}
		}
	}
	return segs
}

// segmentNode returns the expression of one name segment: its one piece, or
// the concat of its pieces.
func segmentNode(seg []armexpr.Node) armexpr.Node {
	switch len(seg) {
	case 0:
		return armexpr.String{}
	case 1:
		return seg[0]
	}
	return armexpr.Call{Name: "concat", Args: seg}
}

// printed returns each segment printed canonically.
func printed(segs [][]armexpr.Node) []string {
	out := make([]string, len(segs))
	for i, seg := range segs {
		out[i] = armexpr.Print(segmentNode(seg))
	}
	return out
}

// nameSegments returns the segments of the full name names write, each name
// read canonically; nil where one of them is absent or does not parse.
func (t *template) nameSegments(names []json.RawMessage) [][]armexpr.Node {
	var out [][]armexpr.Node
	for _, raw := range names {
		n, ok := t.node(raw)
		if !ok {
			return nil
		}
		out = append(out, segments(t.pieces(n, 0))...)
	}
	return out
}

// node returns the string raw holds as an expression read canonically:
// literal text as a string, an expression parsed; false where raw holds no
// string, or one that does not parse.
func (t *template) node(raw json.RawMessage) (armexpr.Node, bool) {
	s, ok := text(raw)
	switch {
	case !ok:
		return nil, false
	case !armexpr.IsExpression(s):
		return armexpr.String{Value: armexpr.LiteralText(s)}, true
	}
	n, err := armexpr.Parse(s)
	if err != nil {
		return nil, false
	}
	return t.canonical(n), true
}

// identify records the identity of each resource of the template.
func (t *template) identify() {
	for _, res := range t.resources {
		res.id = identity{typ: res.typ, segs: printed(t.nameSegments(res.names)),
			sub:   t.scopeArg(res.subscription, "subscription().subscriptionId"),
			group: t.scopeArg(res.group, "resourceGroup().name")}
	}
}

// scopeArg returns the subscription or resource group raw names, printed
// canonically; empty where raw is absent or names the template's own, own
// printed canonically.
func (t *template) scopeArg(raw json.RawMessage, own string) string {
	if raw == nil {
		return ""
	}
	n, ok := t.node(raw)
	if !ok {
		return strings.TrimSpace(string(raw))
	}
	return scopeText(n, own)
}

// scopeText returns n printed canonically, empty where it is own.
func scopeText(n armexpr.Node, own string) string {
	if text := armexpr.Print(n); !strings.EqualFold(text, own) {
		return text
	}
	return ""
}

// bySymbol returns the resource of a 2.0 template whose symbolic name is name,
// exactly, else regardless of case; nil where there is none, or the template
// keys no resource by symbolic name.
func (t *template) bySymbol(name string) *resource {
	if !t.v2 {
		return nil
	}
	return t.byLocal(name)
}

// byLocal returns the resource of the template whose local symbol is name,
// exactly, else regardless of case; nil where there is none.
func (t *template) byLocal(name string) *resource {
	for _, res := range t.resources {
		if res.local == name {
			return res
		}
	}
	for _, res := range t.resources {
		if strings.EqualFold(res.local, name) {
			return res
		}
	}
	return nil
}

// resourceOf returns the resource of the template whose symbol is id, nil
// where there is none.
func (t *template) resourceOf(id string) *resource {
	for _, res := range t.resources {
		if res.symbol == id {
			return res
		}
	}
	return nil
}

// match returns the symbol and type of the first resource, in the order the
// template writes them, else in the order the other templates of its family
// write them, of type typ, the name segs and the scope sub and group. Where
// none is, it returns the identifier of the resource the template reads and
// does not declare: the resourceId call naming it, with scope its
// subscription and resource group arguments, printed canonically.
func (t *template) match(typ string, segs [][]armexpr.Node, sub, group string, scope []armexpr.Node) (string,
	string) {
	want := printed(segs)
	if res := t.firstAcross(func(res *resource) bool {
		return strings.EqualFold(res.typ, typ) && sameText(res.id.segs, want) && strings.EqualFold(res.id.sub, sub) &&
			strings.EqualFold(res.id.group, group)
	}); res != nil {
		return res.symbol, res.typ
	}
	args := []armexpr.Node{}
	if sub != "" || group != "" {
		args = append(args, scope...)
	}
	args = append(args, armexpr.String{Value: typ})
	for _, seg := range segs {
		args = append(args, segmentNode(seg))
	}
	return armexpr.Print(armexpr.Call{Name: "resourceId", Args: args}), typ
}

// first returns the first resource, in the order the template writes them,
// that matches; where several match, their symbols are recorded as ambiguous
// for the lowerer to report where the reference is written. It is nil where
// none matches.
func (t *template) first(matches func(*resource) bool) *resource {
	var found []*resource
	for _, res := range t.resources {
		if matches(res) {
			found = append(found, res)
		}
	}
	if len(found) > 1 {
		ids := make([]string, len(found))
		for i, res := range found {
			ids[i] = res.symbol
		}
		t.ambiguous = append(t.ambiguous, ids)
	}
	if len(found) == 0 {
		return nil
	}
	return found[0]
}

// sameText reports whether a and b hold the same texts, regardless of case.
func sameText(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

// byResourceID returns the symbol and type of the resource a resourceId call
// c, read canonically, names. Its type is its first argument of literal text
// holding a slash; the arguments before it name the subscription and the
// resource group, and the call names a resource of the template only where
// they name the ones the resource is declared in — resourceGroup().name and
// subscription().subscriptionId for the template's own. The arguments after
// it name the resource, compared printed canonically. A call naming no
// resource of the template names the resource it reads and does not declare,
// by the call printed canonically.
func (t *template) byResourceID(c armexpr.Call) (string, string) {
	at := -1
	for i, a := range c.Args {
		if s, ok := a.(armexpr.String); ok && strings.Contains(s.Value, "/") {
			at = i
			break
		}
	}
	if at < 0 || at > 2 {
		return armexpr.Print(c), ""
	}
	var sub, group string
	switch at {
	case 1:
		group = scopeText(c.Args[0], "resourceGroup().name")
	case 2:
		sub = scopeText(c.Args[0], "subscription().subscriptionId")
		group = scopeText(c.Args[1], "resourceGroup().name")
	}
	var segs [][]armexpr.Node
	for _, a := range c.Args[at+1:] {
		segs = append(segs, segments(t.pieces(a, 0))...)
	}
	return t.match(c.Args[at].(armexpr.String).Value, segs, sub, group, c.Args[:at])
}

// typed returns the type and the name segments of segs where they spell a
// resource's type and name, as a dependsOn entry or a relative resource ID
// may: a namespace holding a dot, then each segment of the type, literal,
// followed by the name of that level.
func typed(segs [][]armexpr.Node) (string, [][]armexpr.Node, bool) {
	if len(segs) < 3 || len(segs)%2 == 0 {
		return "", nil, false
	}
	typ := make([]string, 0, len(segs)/2+1)
	var names [][]armexpr.Node
	for i, seg := range segs {
		if i > 0 && i%2 == 0 {
			names = append(names, seg)
			continue
		}
		s, ok := literalSegment(seg)
		if !ok || (i == 0 && !strings.Contains(s, ".")) {
			return "", nil, false
		}
		typ = append(typ, s)
	}
	return strings.Join(typ, "/"), names, true
}

// literalSegment returns the text of a segment of one literal piece.
func literalSegment(seg []armexpr.Node) (string, bool) {
	if len(seg) != 1 {
		return "", false
	}
	s, ok := seg[0].(armexpr.String)
	return s.Value, ok && s.Value != ""
}

// byLiteral returns the symbol and type of the resource the literal text s
// names: under 2.0 a symbolic name; where loops is set, the name of a
// resource's copy loop; the full name of a resource of the template's own
// scope, its own first and then its family's; or a type and a name spelled
// together. Where it names none, the
// identifier is s printed as a string literal, quoted, which no symbol of a
// node takes, or the resourceId call a type and name spelled together name;
// known reports that s names a resource of the template or spells a type and
// a name.
func (t *template) byLiteral(s string, loops bool) (id, typ string, known bool) {
	if res := t.bySymbol(s); res != nil {
		return res.symbol, res.typ, true
	}
	if loops {
		if res := t.first(func(res *resource) bool {
			loop, _ := objectOf(res.copy)
			name, ok := text(loop.get("name"))
			return ok && strings.EqualFold(name, s)
		}); res != nil {
			return res.symbol, res.typ, true
		}
	}
	segs := segments([]armexpr.Node{armexpr.String{Value: s}})
	want := printed(segs)
	if res := t.firstAcross(func(res *resource) bool {
		return res.id.sub == "" && res.id.group == "" && sameText(res.id.segs, want)
	}); res != nil {
		return res.symbol, res.typ, true
	}
	if typ, names, ok := typed(segs); ok {
		id, typ := t.match(typ, names, "", "", nil)
		return id, typ, true
	}
	return armexpr.Print(armexpr.String{Value: s}), "", false
}

// target returns the symbol and type of the resource the expression n names
// where it is read as a resource: literal text naming one, a resourceId call,
// or text spelling a type and a name; false where n names none of these.
func (t *template) target(n armexpr.Node) (string, string, bool) {
	canon := t.canonical(n)
	switch v := canon.(type) {
	case armexpr.String:
		id, typ, _ := t.byLiteral(v.Value, false)
		return id, typ, true
	case armexpr.Call:
		if v.Namespace == "" && strings.EqualFold(v.Name, "resourceId") {
			id, typ := t.byResourceID(v)
			return id, typ, true
		}
	}
	if typ, names, ok := typed(segments(t.pieces(canon, 0))); ok {
		id, typ := t.match(typ, names, "", "", nil)
		return id, typ, true
	}
	return "", "", false
}

// scopeTarget returns the symbol of the resource the scope of res names, an
// extension resource's scope, or the identifier of the resource it names and
// the template does not declare; false where the scope names no resource the
// import can tell.
func (t *template) scopeTarget(res *resource) (string, bool) {
	n, ok := t.node(res.scope)
	if !ok {
		return "", false
	}
	if s, isText := n.(armexpr.String); isText {
		id, _, known := t.byLiteral(s.Value, false)
		return id, known
	}
	id, _, ok := t.target(n)
	return id, ok
}

// parentOf returns the ID of the node res belongs to: the resource it is
// nested in or names as its parent, the resource its scope names, or the
// resource its type and name are a child of — the resource of the type with
// its last segment dropped and the name with its last segment dropped, in the
// same scope, or that resource's identifier where the template does not
// declare it. It is empty for a resource of none of these.
func (t *template) parentOf(res *resource) string {
	if res.parent != "" {
		if p := t.byLocal(res.parent); p != nil {
			return p.symbol
		}
		return t.prefix + res.parent
	}
	if res.scope != nil {
		if id, ok := t.scopeTarget(res); ok {
			return id
		}
	}
	levels := strings.Split(res.typ, "/")
	segs := t.nameSegments(res.names)
	if len(levels) < 3 || len(segs) != len(levels)-1 {
		return ""
	}
	var scope []armexpr.Node
	for _, raw := range []json.RawMessage{res.subscription, res.group} {
		if n, ok := t.node(raw); ok && (res.id.sub != "" || res.id.group != "") {
			scope = append(scope, n)
		}
	}
	id, _ := t.match(strings.Join(levels[:len(levels)-1], "/"), segs[:len(segs)-1], res.id.sub, res.id.group, scope)
	return id
}

// dependencies returns the IDs of the nodes the resource states it depends
// on, in the order written, each once: a dependsOn entry naming a resource of
// the template by symbolic name, copy loop, name or resourceId names its node,
// and one naming none names the resource the template reads and does not
// declare. An entry that reads as an expression and does not parse is not
// carried, with a value-not-carried warning.
func (l *lowerer) dependencies() []string {
	items, _ := arrayOf(l.res.dependsOn)
	var out []string
	for i, raw := range items {
		s, ok := text(raw)
		if !ok {
			continue
		}
		var id string
		switch {
		case !armexpr.IsExpression(s):
			id, _, _ = l.t.byLiteral(armexpr.LiteralText(s), true)
		default:
			n, err := armexpr.Parse(s)
			if err != nil {
				l.unparsed(l.res.at+"/dependsOn/"+strconv.Itoa(i), err)
				continue
			}
			canon := l.t.canonical(n)
			if lit, isText := canon.(armexpr.String); isText {
				id, _, _ = l.t.byLiteral(lit.Value, true)
			} else if target, _, ok := l.t.target(canon); ok {
				id = target
			} else {
				id = armexpr.Print(canon)
			}
		}
		l.settle(l.res.at + "/dependsOn/" + strconv.Itoa(i))
		if id != "" && !contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, have := range list {
		if have == s {
			return true
		}
	}
	return false
}

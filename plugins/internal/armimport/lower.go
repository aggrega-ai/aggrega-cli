// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// obj is a JSON object's members in the order the parser writes them, which
// sorts them by key.
type obj []importer.ObjectEntry

// objectOf returns the members of raw, false where raw is no object.
func objectOf(raw json.RawMessage) (obj, bool) {
	if jsonKind(raw) != '{' {
		return nil, false
	}
	return obj(importer.ObjectEntries(raw)), true
}

// arrayOf returns the items of raw, false where raw is no array.
func arrayOf(raw json.RawMessage) ([]json.RawMessage, bool) {
	if jsonKind(raw) != '[' {
		return nil, false
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, false
	}
	return items, true
}

// exact returns the member of o named key, nil where o has none.
func (o obj) exact(key string) json.RawMessage {
	for _, e := range o {
		if e.Key == key {
			return e.Value
		}
	}
	return nil
}

// get returns the member of o named key, matched as Azure Resource Manager
// matches a member's name: exactly, else regardless of case, the first in
// key order; nil where o has none.
func (o obj) get(key string) json.RawMessage {
	_, v := o.member(key)
	return v
}

// member returns the key and the value of the member of o named key, matched
// as get matches it; empty and nil where o has none.
func (o obj) member(key string) (string, json.RawMessage) {
	for _, e := range o {
		if e.Key == key {
			return e.Key, e.Value
		}
	}
	for _, e := range o {
		if strings.EqualFold(e.Key, key) {
			return e.Key, e.Value
		}
	}
	return "", nil
}

// jsonKind returns the first character of raw past any whitespace: '{', '[',
// '"', or the first character of a number, a boolean or null; 0 for nothing.
func jsonKind(raw json.RawMessage) byte {
	for _, c := range raw {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		}
		return c
	}
	return 0
}

// text returns the string raw holds, false where raw holds no string.
func text(raw json.RawMessage) (string, bool) {
	if jsonKind(raw) != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// isNull reports whether raw is the JSON null.
func isNull(raw json.RawMessage) bool {
	return jsonKind(raw) == 'n'
}

// str is the literal string s.
func str(s string) cloudimport.Literal {
	return cloudimport.Literal{JSON: importer.String(s)}
}

// hasExpression reports whether a string raw holds, at any depth, is read as
// an expression or escapes a bracket.
func hasExpression(raw json.RawMessage, depth int) bool {
	if depth > maxNesting {
		return true
	}
	switch jsonKind(raw) {
	case '"':
		s, _ := text(raw)
		return armexpr.IsExpression(s) || armexpr.LiteralText(s) != s
	case '{':
		o, _ := objectOf(raw)
		for _, e := range o {
			if hasExpression(e.Value, depth+1) {
				return true
			}
		}
	case '[':
		items, _ := arrayOf(raw)
		for _, item := range items {
			if hasExpression(item, depth+1) {
				return true
			}
		}
	}
	return false
}

// loopIndex is the index of one copy loop, known where an instance is
// lowered.
type loopIndex struct {
	// name is the loop's name; resource marks the loop of the resource
	// itself, which copyIndex() with no name reads.
	name     string
	index    int
	resource bool
}

// lowerer lowers the values of one template, or of one of its resources,
// into the core's values.
type lowerer struct {
	r *reader
	t *template
	// res is the resource whose values are lowered, nil for the template's
	// parameters and outputs.
	res *resource
	// loops are the copy loops whose index is known.
	loops []loopIndex
	// quiet discards what lowering reports: an instance of a repeated
	// resource is lowered from what its resource reported once already.
	quiet bool
	// inside names the variables and parameters being lowered, so one reading
	// itself through others is read as opaque rather than followed for ever.
	inside []string
	// spent counts the variables followed and the property copies written,
	// shared by every lowerer of one value, so a template whose variables or
	// loops multiply each other cannot grow the graph without bound.
	spent *int
	// via is the lowerer of the Microsoft.Resources/deployments resource
	// deploying the lowerer's template, through which the template's
	// parameters are read; nil for the root template.
	via *lowerer
}

// maxExpansion bounds the variables one lowerer follows and the property
// copies it writes; past it a variable is deploy-time text and a property
// copy loop is not carried.
const maxExpansion = 10000

// spend records one expansion, false where the lowerer has spent its bound.
func (l *lowerer) spend(n int) bool {
	if *l.spent+n > maxExpansion {
		return false
	}
	*l.spent += n
	return true
}

// lowerer returns the lowerer of res in t; res is nil for t's parameters and
// outputs.
func (r *reader) lowerer(t *template, res *resource) *lowerer {
	return r.lowererAt(t, res, -1, 0)
}

// withLoop returns l with the index of the property loop named name known.
func (l *lowerer) withLoop(name string, index int) *lowerer {
	out := *l
	out.loops = append([]loopIndex{{name: name, index: index}}, l.loops...)
	return &out
}

// warn records a value-not-carried warning about the value at the JSON
// pointer at in the lowerer's template.
func (l *lowerer) warn(at, message string) {
	if l.quiet {
		return
	}
	d := cloudimport.Report(cloudimport.CodeValueNotCarried, message)
	d.SourceRef = l.t.file + "#" + at
	l.r.note(d)
}

// unparsed warns that the string at the JSON pointer at reads as an
// expression and does not parse.
func (l *lowerer) unparsed(at string, err error) {
	reason := "it does not parse"
	var syntaxErr *armexpr.SyntaxError
	if errors.As(err, &syntaxErr) {
		reason = fmt.Sprintf("%s at offset %d", syntaxErr.Reason, syntaxErr.Offset)
	}
	l.warn(at, fmt.Sprintf("the template %s writes text at %s that Azure Resource Manager reads as an "+
		"expression, and it does not parse as one (%s), so it is not carried: write the expression so it parses, "+
		"or open the text with [[ to keep it as literal text", l.t.file, at, reason))
}

// name returns the resource's name: the names it and the resources it is
// nested in write, joined by slashes; nil where one of them is not carried.
func (l *lowerer) name() cloudimport.Value {
	var parts []cloudimport.Value
	for i, raw := range l.res.names {
		if raw == nil {
			return nil
		}
		if i > 0 {
			parts = append(parts, str("/"))
		}
		v := l.leaf(raw, l.res.nameAt[i], "the name of "+l.res.symbol)
		if v == nil {
			return nil
		}
		parts = append(parts, v)
	}
	return compose(parts)
}

// body returns the resource's body lowered. The template a
// Microsoft.Resources/deployments resource holds inline is kept as the
// template writes it: its expressions are its own, evaluated where it is
// deployed, and not read in the template holding it.
func (l *lowerer) body() []cloudimport.Prop {
	var out []cloudimport.Prop
	for _, e := range l.res.body {
		at := l.res.at + importer.Pointer(e.Key)
		var p cloudimport.Prop
		if strings.EqualFold(l.res.typ, deploymentsType) && strings.EqualFold(e.Key, "properties") {
			p = l.deploymentProperties(e.Value, at)
		} else {
			p = l.prop(e.Value, at)
		}
		p.Name = e.Key
		out = append(out, p)
	}
	return out
}

// deploymentProperties lowers the properties of a Microsoft.Resources/deployments
// resource, keeping its inline template as written.
func (l *lowerer) deploymentProperties(raw json.RawMessage, at string) cloudimport.Prop {
	o, ok := objectOf(raw)
	if !ok || len(o) == 0 {
		return l.prop(raw, at)
	}
	var p cloudimport.Prop
	for _, e := range o {
		var m cloudimport.Prop
		if strings.EqualFold(e.Key, "template") && jsonKind(e.Value) == '{' {
			m = cloudimport.Prop{Value: cloudimport.Literal{JSON: e.Value}}
		} else {
			m = l.prop(e.Value, at+importer.Pointer(e.Key))
		}
		m.Name = e.Key
		p.Props = append(p.Props, m)
	}
	return p
}

// condition returns the condition the resource is deployed under, nil where
// it is deployed unconditionally or its condition is not carried.
func (l *lowerer) condition() cloudimport.Value {
	raw := l.res.condition
	if raw == nil || strings.TrimSpace(string(raw)) == "true" {
		return nil
	}
	v := l.leaf(raw, l.res.at+importer.Pointer("condition"), "the condition of "+l.res.symbol)
	if lit, ok := v.(cloudimport.Literal); ok && string(lit.JSON) == "true" {
		return nil
	}
	return v
}

// leaf returns the value raw, written at the JSON pointer at, lowered as one
// value: an object or an array holding no expression — written in place, or
// held whole by a variable — is its literal. One holding an expression has no
// one value: it is nil, with a value-not-carried warning naming what, the
// value it is, and where it is written.
func (l *lowerer) leaf(raw json.RawMessage, at, what string) cloudimport.Value {
	p := l.prop(raw, at)
	if p.Value != nil || (len(p.Props) == 0 && len(p.Items) == 0) {
		return p.Value
	}
	source := raw
	if held, ok := l.t.heldWhole(raw); ok {
		source = held
	}
	if !hasExpression(source, 0) {
		return cloudimport.Literal{JSON: source}
	}
	l.warn(at, fmt.Sprintf("the template %s writes %s at %s as an object or a list holding an expression, "+
		"which the draft carries as no one value, so it is not carried: write it in the draft by hand", l.t.file,
		what, at))
	return nil
}

// heldWhole returns the value of the variable the string raw reads whole, as
// [variables('v')], false where raw is no such string.
func (t *template) heldWhole(raw json.RawMessage) (json.RawMessage, bool) {
	s, ok := text(raw)
	if !ok || !armexpr.IsExpression(s) {
		return nil, false
	}
	n, err := armexpr.Parse(s)
	if err != nil {
		return nil, false
	}
	name, ok := oneTextArgument(n, "variables")
	if !ok {
		return nil, false
	}
	_, held, found := t.variable(name)
	return held, found
}

// prop lowers the JSON value raw, written at the JSON pointer at, into a
// property: an object into its members, an array into its items, a string
// Azure Resource Manager reads as an expression into the value it lowers to,
// and any other value into its literal. An empty object or array is its
// literal. A member named copy holding an array is a property copy loop: each
// loop becomes the member it names, holding one item per index of a literal
// count.
func (l *lowerer) prop(raw json.RawMessage, at string) cloudimport.Prop {
	switch jsonKind(raw) {
	case 0:
		return cloudimport.Prop{}
	case '{':
		o, _ := objectOf(raw)
		if len(o) == 0 {
			return cloudimport.Prop{Value: cloudimport.Literal{JSON: json.RawMessage("{}")}}
		}
		var p cloudimport.Prop
		for _, e := range o {
			if e.Key == "copy" && jsonKind(e.Value) == '[' {
				p.Props = append(p.Props, l.propertyLoops(e.Value, at+importer.Pointer("copy"))...)
				continue
			}
			m := l.prop(e.Value, at+importer.Pointer(e.Key))
			m.Name = e.Key
			p.Props = append(p.Props, m)
		}
		return p
	case '[':
		items, _ := arrayOf(raw)
		if len(items) == 0 {
			return cloudimport.Prop{Value: cloudimport.Literal{JSON: json.RawMessage("[]")}}
		}
		var p cloudimport.Prop
		for i, item := range items {
			p.Items = append(p.Items, l.prop(item, at+importer.Pointer(strconv.Itoa(i))))
		}
		return p
	case '"':
		s, _ := text(raw)
		p := l.text(s, at)
		l.settle(at)
		return p
	}
	return cloudimport.Prop{Value: cloudimport.Literal{JSON: raw}}
}

// propertyLoops lowers the property copy loops raw lists, written at the
// JSON pointer at: each into the member its name names, one item per index of
// a literal count, the item lowered with copyIndex of the loop's name read as
// its index. A loop whose count the template computes when it deploys is not
// carried.
func (l *lowerer) propertyLoops(raw json.RawMessage, at string) []cloudimport.Prop {
	loops, _ := arrayOf(raw)
	var out []cloudimport.Prop
	for i, loop := range loops {
		loopAt := at + importer.Pointer(strconv.Itoa(i))
		o, _ := objectOf(loop)
		name, named := text(o.get("name"))
		if !named || name == "" {
			l.warn(loopAt, "the template "+l.t.file+" writes a property copy loop at "+loopAt+" naming no "+
				"property, so it is not carried: name the property the loop writes")
			continue
		}
		count, literal := literalCount(o.get("count"))
		if literal && !l.spend(count) {
			l.warn(loopAt, "the template "+l.t.file+" repeats the property "+name+" at "+loopAt+" more times "+
				"than the import writes out, its copy loops multiplying each other, so the property is not "+
				"carried: write its items in the draft by hand")
			out = append(out, cloudimport.Prop{Name: name})
			continue
		}
		if !literal {
			l.warn(loopAt, "the template "+l.t.file+" repeats the property "+name+" at "+loopAt+" as many "+
				"times as it computes when it deploys, so the property is not carried: write its items in the "+
				"draft by hand")
			out = append(out, cloudimport.Prop{Name: name})
			continue
		}
		p := cloudimport.Prop{Name: name}
		if count == 0 {
			p.Value = cloudimport.Literal{JSON: json.RawMessage("[]")}
		}
		for k := 0; k < count; k++ {
			p.Items = append(p.Items, l.withLoop(name, k).prop(o.get("input"), loopAt+importer.Pointer("input")))
		}
		out = append(out, p)
	}
	return out
}

// text lowers the string s, written at the JSON pointer at. A string opening
// with a doubled bracket is the literal text without the first bracket, a
// string Azure Resource Manager does not read as an expression is its
// literal, and an expression is lowered; one reading a variable that holds an
// object or an array is that object or array, and one reading a parameter of a
// nested template is the value the parameter takes, as [lowerer.parameter]
// reads it. An expression that does not parse is not carried, with a
// value-not-carried warning.
func (l *lowerer) text(s, at string) cloudimport.Prop {
	if !armexpr.IsExpression(s) {
		return cloudimport.Prop{Value: str(armexpr.LiteralText(s))}
	}
	n, err := armexpr.Parse(s)
	if err != nil {
		l.unparsed(at, err)
		return cloudimport.Prop{}
	}
	if name, ok := oneTextArgument(n, "variables"); ok {
		if key, raw, found := l.t.variable(name); found && (jsonKind(raw) == '{' || jsonKind(raw) == '[') &&
			l.spend(1) && l.enter(key) {
			defer l.leave()
			return l.prop(raw, l.t.scope().at+importer.Pointer("variables", key))
		}
	}
	if name, ok := oneTextArgument(n, "parameters"); ok {
		if p, _, handled := l.parameter(name, nil); handled {
			return p
		}
	}
	return cloudimport.Prop{Value: l.expr(n)}
}

// enter records that the variable key is being lowered, false where it is
// already: the variable reads itself.
func (l *lowerer) enter(key string) bool {
	for _, k := range l.inside {
		if k == key {
			return false
		}
	}
	l.inside = append(l.inside, key)
	return true
}

// leave records that the variable last entered is lowered.
func (l *lowerer) leave() {
	l.inside = l.inside[:len(l.inside)-1]
}

// oneTextArgument returns the text of the one literal argument of n, where n
// is a call of the built-in function name, matched regardless of case.
func oneTextArgument(n armexpr.Node, name string) (string, bool) {
	c, ok := n.(armexpr.Call)
	if !ok || c.Namespace != "" || !strings.EqualFold(c.Name, name) || len(c.Args) != 1 {
		return "", false
	}
	s, ok := c.Args[0].(armexpr.String)
	return s.Value, ok
}

// expr lowers the expression n. A value it reads that is not carried makes it
// nil.
func (l *lowerer) expr(n armexpr.Node) cloudimport.Value {
	switch v := n.(type) {
	case armexpr.String:
		return str(v.Value)
	case armexpr.Int:
		if i, err := strconv.ParseInt(v.Text, 10, 64); err == nil {
			return cloudimport.Literal{JSON: json.RawMessage(strconv.FormatInt(i, 10))}
		}
		return l.opaque(n)
	case armexpr.Call:
		return l.call(v)
	}
	return l.access(n)
}

// call lowers the function call c: parameters and variables to what they
// read, concat and format to composed text, reference, resourceInfo and
// resourceId to references, each list function to a credential, copyIndex to
// the index where it is known, and every other call to deploy-time text.
func (l *lowerer) call(c armexpr.Call) cloudimport.Value {
	if c.Namespace != "" {
		return l.opaque(c)
	}
	switch strings.ToLower(c.Name) {
	case "parameters":
		if name, ok := oneTextArgument(c, "parameters"); ok {
			if p, raw, handled := l.parameter(name, nil); handled {
				return l.single(p, raw, c)
			}
		}
		return l.opaque(c)
	case "variables":
		if name, ok := oneTextArgument(c, "variables"); ok {
			return l.variable(name, nil, c)
		}
		return l.opaque(c)
	case "concat":
		return l.concat(c)
	case "format":
		return l.format(c)
	case "reference", "resourceinfo", "resourceid":
		return l.refer(c, c, nil)
	case "copyindex":
		if index, ok := l.copyIndex(c); ok {
			return cloudimport.Literal{JSON: json.RawMessage(strconv.Itoa(index))}
		}
		return l.opaque(c)
	case "true", "false", "null":
		if len(c.Args) == 0 {
			return cloudimport.Literal{JSON: json.RawMessage(strings.ToLower(c.Name))}
		}
	}
	if armexpr.IsListFunction(c.Name) {
		return l.credential(c)
	}
	return l.opaque(c)
}

// access lowers an expression reading a member or an item of a function's
// result: of reference or resourceInfo, a reference to that attribute; of a
// list function, the credential; of variables, the value the variable holds
// there. Any other is deploy-time text.
func (l *lowerer) access(n armexpr.Node) cloudimport.Value {
	var path []string
	cur := n
	for {
		switch v := cur.(type) {
		case armexpr.Member:
			path = append(path, v.Name)
			cur = v.Target
			continue
		case armexpr.Index:
			switch i := v.Index.(type) {
			case armexpr.String:
				path = append(path, i.Value)
			case armexpr.Int:
				path = append(path, i.Text)
			default:
				return l.opaque(n)
			}
			cur = v.Target
			continue
		}
		break
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	root, ok := cur.(armexpr.Call)
	if !ok || root.Namespace != "" {
		return l.opaque(n)
	}
	switch strings.ToLower(root.Name) {
	case "reference", "resourceinfo", "resourceid":
		return l.refer(root, n, path)
	case "variables":
		if name, ok := oneTextArgument(root, "variables"); ok {
			return l.variable(name, path, n)
		}
	case "parameters":
		if name, ok := oneTextArgument(root, "parameters"); ok {
			if p, raw, handled := l.parameter(name, path); handled {
				return l.single(p, raw, n)
			}
		}
	}
	if armexpr.IsListFunction(root.Name) {
		return l.credential(root)
	}
	return l.opaque(n)
}

// variable lowers what the variable name holds at path, read by the
// expression whole: text is lowered as the template writes it, an object or
// an array holding no expression is its literal, and anything else — a
// variable the template does not declare, one a copy loop computes, one
// holding an expression in an object, one reading itself — is deploy-time
// text.
func (l *lowerer) variable(name string, path []string, whole armexpr.Node) cloudimport.Value {
	key, raw, found := l.t.variable(name)
	if !found || !l.spend(1) || !l.enter(key) {
		return l.opaque(whole)
	}
	defer l.leave()
	at := l.t.scope().at + importer.Pointer("variables", key)
	for _, seg := range path {
		raw = descend(raw, seg)
		at += importer.Pointer(seg)
		if raw == nil {
			return l.opaque(whole)
		}
	}
	switch jsonKind(raw) {
	case '"':
		p := l.text(mustText(raw), at)
		if p.Value == nil && (len(p.Props) > 0 || len(p.Items) > 0) {
			return l.opaque(whole)
		}
		return p.Value
	case '{', '[':
		if hasExpression(raw, 0) {
			return l.opaque(whole)
		}
	}
	return cloudimport.Literal{JSON: raw}
}

// mustText returns the string raw holds, empty where it holds none.
func mustText(raw json.RawMessage) string {
	s, _ := text(raw)
	return s
}

// descend returns the member or item of raw that seg names: a member by its
// name, matched as a member's name is, an item by its index; nil where raw
// holds none.
func descend(raw json.RawMessage, seg string) json.RawMessage {
	if o, ok := objectOf(raw); ok {
		return o.get(seg)
	}
	if items, ok := arrayOf(raw); ok {
		if i, err := strconv.Atoi(seg); err == nil && i >= 0 && i < len(items) {
			return items[i]
		}
	}
	return nil
}

// concat lowers a concat call of text to composed text. A call joining
// arrays, or anything that is not text, is deploy-time text.
func (l *lowerer) concat(c armexpr.Call) cloudimport.Value {
	parts := make([]cloudimport.Value, 0, len(c.Args))
	for _, arg := range c.Args {
		v := l.expr(arg)
		if v == nil {
			return nil
		}
		part, ok := l.textPart(v)
		if !ok {
			return l.opaque(c)
		}
		parts = append(parts, part)
	}
	return compose(parts)
}

// format lowers a format call whose format text is literal to composed text:
// the text between its placeholders, and the argument each placeholder names.
// A placeholder carrying an alignment or a format specifier, or naming no
// argument, makes the call deploy-time text.
func (l *lowerer) format(c armexpr.Call) cloudimport.Value {
	if len(c.Args) == 0 {
		return l.opaque(c)
	}
	f, ok := l.expr(c.Args[0]).(cloudimport.Literal)
	layout, isText := text(f.JSON)
	if !ok || !isText {
		return l.opaque(c)
	}
	pieces, ok := formatPieces(layout)
	if !ok {
		return l.opaque(c)
	}
	var parts []cloudimport.Value
	for _, p := range pieces {
		if p.arg < 0 {
			parts = append(parts, str(p.text))
			continue
		}
		if p.arg+1 >= len(c.Args) {
			return l.opaque(c)
		}
		v := l.expr(c.Args[p.arg+1])
		if v == nil {
			return nil
		}
		part, ok := l.textPart(v)
		if !ok {
			return l.opaque(c)
		}
		parts = append(parts, part)
	}
	return compose(parts)
}

// formatPiece is one piece of a format text: literal text, or the index of
// the argument a placeholder names, arg -1 for text.
type formatPiece struct {
	text string
	arg  int
}

// formatPieces splits a format text into its pieces, as the composite format
// format() reads: {n} names the argument n, a doubled brace is the brace. A
// placeholder with an alignment or a format specifier, or an unpaired brace,
// is not split, false.
func formatPieces(layout string) ([]formatPiece, bool) {
	var out []formatPiece
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, formatPiece{text: b.String(), arg: -1})
			b.Reset()
		}
	}
	for i := 0; i < len(layout); i++ {
		c := layout[i]
		switch {
		case c == '{' && i+1 < len(layout) && layout[i+1] == '{', c == '}' && i+1 < len(layout) && layout[i+1] == '}':
			b.WriteByte(c)
			i++
		case c == '{':
			end := strings.IndexByte(layout[i:], '}')
			if end < 0 {
				return nil, false
			}
			n, err := strconv.Atoi(layout[i+1 : i+end])
			if err != nil || n < 0 || strings.ContainsAny(layout[i+1:i+end], "+- ") {
				return nil, false
			}
			flush()
			out = append(out, formatPiece{arg: n})
			i += end
		case c == '}':
			return nil, false
		default:
			b.WriteByte(c)
		}
	}
	flush()
	return out, true
}

// arrayFunctions are the template functions whose result is an array or an
// object, never text: a concat or format reading one is not composed text.
var arrayFunctions = []string{"array", "createArray", "createObject", "distinct", "filter", "flatten", "intersection",
	"items", "json", "map", "objectKeys", "range", "references", "skip", "sort", "split", "take", "union"}

// textPart returns v as a piece of composed text, false where it is none: text,
// a number or a boolean written as its text, a parameter that is not an array
// or an object, a reference, a credential, composed text, or deploy-time text
// that is not an array function's result.
func (l *lowerer) textPart(v cloudimport.Value) (cloudimport.Value, bool) {
	switch v := v.(type) {
	case cloudimport.Literal:
		switch jsonKind(v.JSON) {
		case '"':
			return v, true
		case '{', '[', 'n', 0:
			return nil, false
		}
		return str(strings.TrimSpace(string(v.JSON))), true
	case cloudimport.Param:
		typ, _ := l.r.root.paramType(v.Name)
		return v, typ != "array" && typ != "object"
	case cloudimport.Opaque:
		n, err := armexpr.Parse(v.Text)
		if c, ok := n.(armexpr.Call); err == nil && ok && c.Namespace == "" {
			for _, name := range arrayFunctions {
				if strings.EqualFold(c.Name, name) {
					return nil, false
				}
			}
		}
	}
	return v, true
}

// compose returns the concatenation of parts: composed parts flattened,
// adjacent literal text joined and empty text dropped; one part is itself,
// and none is the empty text.
func compose(parts []cloudimport.Value) cloudimport.Value {
	var flat []cloudimport.Value
	var add func(v cloudimport.Value)
	add = func(v cloudimport.Value) {
		if c, ok := v.(cloudimport.Compose); ok {
			for _, p := range c.Parts {
				add(p)
			}
			return
		}
		lit, isLit := v.(cloudimport.Literal)
		s, isText := text(lit.JSON)
		if isLit && isText {
			if s == "" {
				return
			}
			if n := len(flat); n > 0 {
				if prev, ok := flat[n-1].(cloudimport.Literal); ok {
					if before, ok := text(prev.JSON); ok {
						flat[n-1] = str(before + s)
						return
					}
				}
			}
		}
		flat = append(flat, v)
	}
	for _, p := range parts {
		add(p)
	}
	switch len(flat) {
	case 0:
		return str("")
	case 1:
		return flat[0]
	}
	return cloudimport.Compose{Parts: flat}
}

// copyIndex returns the index copyIndex call c reads, false where it is not
// known: the loop it names, or the resource's own where it names none, plus
// its offset.
func (l *lowerer) copyIndex(c armexpr.Call) (int, bool) {
	name, offset := "", 0
	for i, arg := range c.Args {
		switch v := arg.(type) {
		case armexpr.String:
			if i != 0 {
				return 0, false
			}
			name = v.Value
		case armexpr.Int:
			n, err := strconv.Atoi(v.Text)
			if err != nil || i != len(c.Args)-1 {
				return 0, false
			}
			offset = n
		default:
			return 0, false
		}
	}
	if len(c.Args) > 2 {
		return 0, false
	}
	for _, loop := range l.loops {
		if (name == "" && loop.resource) || (name != "" && strings.EqualFold(loop.name, name)) {
			return loop.index + offset, true
		}
	}
	return 0, false
}

// refer lowers a call of reference, resourceInfo or resourceId, read at path
// by the expression whole, to a reference to the resource it names. A
// resourceId names the resource's identifier. A reference reads the attribute
// at path — its properties where it reads the resource's properties, as
// reference does unless asked for the full resource; the outputs of a
// Microsoft.Resources/deployments resource are read below its properties, and
// those of one whose template is flattened are the value the output holds —
// and its id names the identifier. A call naming no resource it can tell, a
// whole resource object, or an output of a template the import does not read
// is deploy-time text.
func (l *lowerer) refer(c armexpr.Call, whole armexpr.Node, path []string) cloudimport.Value {
	isID := len(path) == 1 && strings.EqualFold(path[0], "id")
	switch strings.ToLower(c.Name) {
	case "resourceid":
		canon, ok := l.t.canonical(c).(armexpr.Call)
		if len(path) > 0 || !ok {
			return l.opaque(whole)
		}
		id, _ := l.t.byResourceID(canon)
		return cloudimport.Ref{Node: id}
	case "resourceinfo":
		name, ok := oneTextArgument(c, "resourceInfo")
		res := l.t.bySymbol(name)
		switch {
		case !ok || res == nil || len(path) == 0:
			return l.opaque(whole)
		case isID:
			return cloudimport.Ref{Node: res.symbol}
		}
		return cloudimport.Ref{Node: res.symbol, Attr: append([]string(nil), path...)}
	}
	if len(c.Args) == 0 {
		return l.opaque(whole)
	}
	id, typ, ok := l.t.target(c.Args[0])
	if !ok || l.unread(id) {
		return l.opaque(whole)
	}
	if isID {
		return cloudimport.Ref{Node: id}
	}
	full := false
	if len(c.Args) >= 3 {
		mode, isText := l.t.canonical(c.Args[2]).(armexpr.String)
		full = isText && strings.EqualFold(mode.Value, "full")
	}
	var attr []string
	switch {
	case strings.EqualFold(typ, deploymentsType) && full && len(path) > 0 && strings.EqualFold(path[0], "properties"):
		attr = append(attr, path[1:]...)
	case strings.EqualFold(typ, deploymentsType), full:
		attr = append(attr, path...)
	default:
		attr = append([]string{"properties"}, path...)
	}
	if len(attr) == 0 {
		return l.opaque(whole)
	}
	if res := l.t.resourceOf(id); res != nil && res.nested != nil {
		return l.output(res, attr, whole)
	}
	return cloudimport.Ref{Node: id, Attr: attr}
}

// unread reports whether id is a Microsoft.Resources/deployments resource of
// the template linking a template the import does not read.
func (l *lowerer) unread(id string) bool {
	res := l.t.resourceOf(id)
	return res != nil && l.r.unreadLink(l.t, res) != ""
}

// credential lowers a call of a list function to the credential it reads from
// the resource its first argument names; a resource it cannot tell is named
// by that argument printed canonically, and a call naming none by the call.
func (l *lowerer) credential(c armexpr.Call) cloudimport.Value {
	if len(c.Args) == 0 {
		return cloudimport.Credential{Node: armexpr.Print(c), What: c.Name}
	}
	id, _, ok := l.t.target(c.Args[0])
	if !ok {
		id = armexpr.Print(l.t.canonical(c.Args[0]))
	}
	return cloudimport.Credential{Node: id, What: c.Name}
}

// opaque returns the expression n as deploy-time text for the arm target,
// printed canonically with every variable it reads written in place where it
// can be, and every copy index the lowerer knows. An expression reaching a
// list function — itself, through a variable written in place, or through a
// variable holding an object or a list — is the credential that function
// reads, so no credential is carried as text.
func (l *lowerer) opaque(n armexpr.Node) cloudimport.Value {
	inlined := l.t.inline(n, l)
	if list, ok := l.t.listCallIn(inlined); ok {
		return l.credential(list)
	}
	return cloudimport.Opaque{Text: armexpr.Expression(inlined), Target: target}
}

// listCallIn returns the first call of the list family n reaches: in n, or in
// an object or a list a variable n reads holds; false where it reaches none.
// Each variable is searched once.
func (t *template) listCallIn(n armexpr.Node) (armexpr.Call, bool) {
	return t.listCall(n, map[string]bool{})
}

// listCall returns the first list call n reaches, the variables in seen
// searched already.
func (t *template) listCall(n armexpr.Node, seen map[string]bool) (armexpr.Call, bool) {
	if c, ok := armexpr.ListCall(n); ok {
		return c, true
	}
	var found armexpr.Call
	ok := false
	armexpr.Walk(n, func(m armexpr.Node) bool {
		name, isVariable := oneTextArgument(m, "variables")
		if ok || !isVariable {
			return !ok
		}
		if key, raw, declared := t.variable(name); declared && !seen[key] {
			seen[key] = true
			found, ok = t.listCallInValue(raw, seen, 0)
		}
		return false
	})
	return found, ok
}

// listCallInValue returns the first list call an expression in the JSON value
// raw reaches, read canonically.
func (t *template) listCallInValue(raw json.RawMessage, seen map[string]bool, depth int) (armexpr.Call, bool) {
	if depth > maxNesting {
		return armexpr.Call{}, false
	}
	var values []json.RawMessage
	switch jsonKind(raw) {
	case '"':
		s, _ := text(raw)
		if !armexpr.IsExpression(s) {
			return armexpr.Call{}, false
		}
		n, err := armexpr.Parse(s)
		if err != nil {
			return armexpr.Call{}, false
		}
		return t.listCall(t.canonical(n), seen)
	case '{':
		o, _ := objectOf(raw)
		for _, e := range o {
			values = append(values, e.Value)
		}
	case '[':
		values, _ = arrayOf(raw)
	}
	for _, v := range values {
		if c, ok := t.listCallInValue(v, seen, depth+1); ok {
			return c, true
		}
	}
	return armexpr.Call{}, false
}

// settle reports each reference lowered at the JSON pointer at that several
// resources of the template matched, and forgets them: an instance of a
// repeated resource forgets them unreported, its resource having reported
// them once.
func (l *lowerer) settle(at string) {
	for _, ids := range l.t.ambiguous {
		if l.quiet {
			continue
		}
		d := cloudimport.Report(cloudimport.CodeReferenceNotWired, fmt.Sprintf("the template %s references at %s "+
			"a resource that %s each match, and the draft reads the first of them, %s, as the template writes "+
			"them: give each resource a name of its own, or reference the one meant by its symbolic name",
			l.t.file, at, andList(ids), ids[0]))
		d.SourceRef = l.t.file + "#" + at
		l.r.note(d)
	}
	l.t.ambiguous = nil
}

// andList joins names as a sentence lists them: "a", "a and b", "a, b and c".
func andList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// scopeText returns, as report text, the scope the resource is declared in
// where it is not the one the template deploys into: its subscriptionId, its
// resourceGroup, and a scope naming no resource of the template.
func (l *lowerer) scopeText() string {
	var parts []string
	if l.res.id.sub != "" {
		parts = append(parts, "subscription "+l.t.valueText(l.res.subscription))
	}
	if l.res.id.group != "" {
		parts = append(parts, "resource group "+l.t.valueText(l.res.group))
	}
	if l.res.scope != nil {
		if _, ok := l.t.scopeTarget(l.res); !ok {
			parts = append(parts, "scope "+l.t.valueText(l.res.scope))
		}
	}
	return strings.Join(parts, ", ")
}

// valueText returns raw as report text: text as written, an expression
// printed canonically, any other value as its JSON.
func (t *template) valueText(raw json.RawMessage) string {
	s, ok := text(raw)
	if !ok {
		return strings.TrimSpace(string(raw))
	}
	if !armexpr.IsExpression(s) {
		return s
	}
	n, err := armexpr.Parse(s)
	if err != nil {
		return s
	}
	return armexpr.Expression(t.canonical(n))
}

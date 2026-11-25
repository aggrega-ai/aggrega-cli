// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

const (
	// maxNestedResources bounds the resources of nested templates one read
	// takes into the graph, so templates linking one another many times over
	// cannot grow it without bound; past it a nested template is not read.
	maxNestedResources = 10000
	// maxValueSize bounds the size of the value one output or one parameter of
	// a nested template holds, in bytes of text and parts, so outputs or
	// parameters composing each other cannot grow it without bound; past it
	// the value is not carried.
	maxValueSize = 1 << 16
)

// frame is one Microsoft.Resources/deployments resource deploying a nested
// template, with the template holding it.
type frame struct {
	t   *template
	res *resource
}

// nesting is how a nested template is deployed.
type nesting struct {
	// up holds dep, the Microsoft.Resources/deployments resource deploying the
	// template.
	up  *template
	dep *resource
	// outer reports that the template's expressions are evaluated in the
	// scope of the template holding it.
	outer bool
	// family is the group of templates the template joins, nil for none.
	family *family
}

// family is the templates read into one scope: the root, and each template
// nested in a member and deployed into the scope that member deploys into. A
// reference matches a resource of its own template first, then one of the
// other members, and an existing resource one member reads and another
// declares is read as the declared one.
type family struct{ members []*template }

// nestUnder records how t is deployed.
func (t *template) nestUnder(n *nesting) {
	t.up, t.dep, t.outer, t.family = n.up, n.dep, n.outer, n.family
	t.frames = append(slices.Clip(n.up.frames), frame{t: n.up, res: n.dep})
	t.prefix = n.dep.symbol + "/"
}

// scope returns the template whose parameters and variables t's expressions
// read: t, or for a template evaluated in the outer scope the scope of the
// template holding it.
func (t *template) scope() *template {
	for t.outer && t.up != nil {
		t = t.up
	}
	return t
}

// scoped returns the lowerer of the template whose parameters and variables
// l's expressions read: l, or one l reads through.
func (l *lowerer) scoped() *lowerer {
	for l.t.outer && l.via != nil {
		l = l.via
	}
	return l
}

// lowererAt returns the lowerer of res in t — res nil for t's parameters and
// outputs — reading each parameter t's resources take from the
// Microsoft.Resources/deployments resources deploying t through lowerers of
// their own. Where level is not negative, the copy loop of the resource at
// that level — an index into t.frames, or len(t.frames) for res itself — is
// read at index, and the lowerers from that level down report nothing: an
// instance repeats what its resource reported once.
func (r *reader) lowererAt(t *template, res *resource, level, index int) *lowerer {
	var via *lowerer
	quiet := false
	loop := func(i int, l *lowerer) {
		if i != level || l.res == nil {
			return
		}
		o, _ := objectOf(l.res.copy)
		name, _ := text(o.get("name"))
		l.loops = []loopIndex{{name: name, index: index, resource: true}}
		quiet = true
	}
	for i, f := range t.frames {
		l := &lowerer{r: r, t: f.t, res: f.res, via: via, spent: new(int)}
		loop(i, l)
		l.quiet = quiet
		via = l
	}
	l := &lowerer{r: r, t: t, res: res, via: via, spent: new(int)}
	loop(len(t.frames), l)
	l.quiet = quiet
	return l
}

// firstAcross returns the first resource of the template that matches, else
// the first of the other members of its family, in the order they are read,
// leaving out a resource read as another and one sending a module's
// telemetry. Where several match, their symbols are recorded as ambiguous, as
// first records them. It is nil where none matches.
func (t *template) firstAcross(matches func(*resource) bool) *resource {
	if res := t.first(matches); res != nil || t.family == nil {
		return res
	}
	var found []*resource
	for _, m := range t.family.members {
		if m == t {
			continue
		}
		for _, res := range m.resources {
			if !res.aliased && !res.telemetry && matches(res) {
				found = append(found, res)
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	if len(found) > 1 {
		ids := make([]string, len(found))
		for i, res := range found {
			ids[i] = res.symbol
		}
		t.ambiguous = append(t.ambiguous, ids)
	}
	return found[0]
}

// arguments returns the properties.parameters of a
// Microsoft.Resources/deployments resource, the values it gives the
// parameters of the template it deploys, and the JSON pointer it is written
// at.
func (res *resource) arguments() (obj, string) {
	pk, rawProps := res.body.member("properties")
	props, _ := objectOf(rawProps)
	ak, rawArgs := props.member("parameters")
	args, _ := objectOf(rawArgs)
	return args, res.at + importer.Pointer(pk, ak)
}

// bound returns the expression the parameter name of a nested template is
// written as in place, false where it is not: the text or scalar the resource
// deploying the template gives it, read in the template holding that
// resource, else its default, read in its own template. A parameter of the
// root template, one given an object, an array or a Key Vault reference, and
// one reading itself are not.
func (t *template) bound(name string, l *lowerer, st *inlining) (armexpr.Node, bool) {
	s := t.scope()
	if s.up == nil {
		return nil, false
	}
	declared, ok := s.param(name)
	if !ok {
		return nil, false
	}
	var sl *lowerer
	if l != nil {
		sl = l.scoped()
	}
	var raw json.RawMessage
	args, _ := s.dep.arguments()
	_, given := args.member(declared)
	if given != nil {
		o, _ := objectOf(given)
		raw = o.get("value")
	} else {
		decl, _ := objectOf(s.params.exact(declared))
		raw = decl.get("defaultValue")
	}
	n, ok := expression(raw)
	if !ok || !st.enter(inlined{t: s, param: true, name: declared}) {
		return nil, false
	}
	defer st.leave()
	if given == nil {
		return s.inlineWith(n, sl, st), true
	}
	var vl *lowerer
	if sl != nil {
		vl = sl.via
	}
	return s.up.inlineWith(n, vl, st), true
}

// parameter returns what the parameter name the lowerer's expressions read
// holds at path, lowered, with the JSON it is written as where it is. A
// parameter of the root template is the parameter, read whole. A parameter of
// a nested template is the value the resource deploying the template gives it
// under properties.parameters, lowered where that resource is written, as
// [lowerer.argument] reads it; else its default, lowered in the nested
// template. handled is false where the value is deploy-time text the caller
// writes as the expression: a member of a root parameter, a parameter no
// template declares, a path the value holds nothing at, or a parameter
// reading itself. A parameter given no value and declaring no default is not
// carried, with a value-not-carried warning, and so is one whose value is
// larger than maxValueSize. A parameter read whole is lowered once for the
// lowerer of the resource deploying its template.
func (l *lowerer) parameter(name string, path []string) (cloudimport.Prop, json.RawMessage, bool) {
	sl := l.scoped()
	s := sl.t
	declared, ok := s.param(name)
	switch {
	case !ok:
		return cloudimport.Prop{}, nil, false
	case s.up == nil:
		if len(path) > 0 {
			return cloudimport.Prop{}, nil, false
		}
		return cloudimport.Prop{Value: cloudimport.Param{Name: declared}}, nil, true
	case sl.via == nil:
		return cloudimport.Prop{}, nil, false
	}
	if len(path) == 0 {
		if known, done := sl.via.known[declared]; done {
			return known.prop, known.raw, known.handled
		}
	}
	if !sl.spend(1) || !sl.enter(parameterKey(declared)) {
		return cloudimport.Prop{}, nil, false
	}
	defer sl.leave()
	p, raw, handled := sl.parameterValue(declared, path)
	if handled && propSize(p) > maxValueSize {
		sl.warn(s.at+importer.Pointer("parameters", declared), fmt.Sprintf("the template %s takes at %s a value of "+
			"its parameter %s larger than the %d bytes the draft carries of one value, its parts composing one "+
			"another, so what reads it is not carried", s.file, s.at+importer.Pointer("parameters", declared),
			declared, maxValueSize))
		p, raw = cloudimport.Prop{}, nil
	}
	if len(path) == 0 {
		if sl.via.known == nil {
			sl.via.known = map[string]taken{}
		}
		sl.via.known[declared] = taken{prop: p, raw: raw, handled: handled}
	}
	return p, raw, handled
}

// taken is what a parameter of a nested template takes, lowered once for
// the lowerer of the resource deploying the template.
type taken struct {
	prop    cloudimport.Prop
	raw     json.RawMessage
	handled bool
}

// parameterValue returns what the parameter declared of the nested template
// sl lowers holds at path, as [lowerer.parameter] says.
func (sl *lowerer) parameterValue(declared string, path []string) (cloudimport.Prop, json.RawMessage, bool) {
	s := sl.t
	args, at := s.dep.arguments()
	if key, given := args.member(declared); given != nil {
		return sl.via.argument(given, at+importer.Pointer(key), path)
	}
	declAt := s.at + importer.Pointer("parameters", declared)
	decl, _ := objectOf(s.params.exact(declared))
	if def := decl.get("defaultValue"); def != nil {
		raw, defAt, ok := descendAt(def, declAt+importer.Pointer("defaultValue"), path)
		if !ok {
			return cloudimport.Prop{}, nil, false
		}
		return sl.prop(raw, defAt), raw, true
	}
	sl.warn(declAt, fmt.Sprintf("the template %s declares at %s the parameter %s with no default, and %s, the %s "+
		"resource deploying that template, gives it no value, so what reads it is not carried: give it a value "+
		"under that resource's properties.parameters", s.file, declAt, declared, s.dep.symbol, deploymentsType))
	return cloudimport.Prop{}, nil, true
}

// parameterKey is the key under which the lowerer records the parameter
// declared being lowered, apart from the variables it records.
func parameterKey(declared string) string {
	return "\x00parameters\x00" + declared
}

// argument lowers raw, the member of a Microsoft.Resources/deployments
// resource's properties.parameters written at the JSON pointer at, read at
// path: the value it gives there, lowered with its JSON, or the credential a
// Key Vault reference names, read whole. handled is false where the value
// holds nothing at path, or a Key Vault reference is read into. A member
// giving neither, or a reference naming no vault's ID and secret's name, is
// not carried, with a value-not-carried warning.
func (l *lowerer) argument(raw json.RawMessage, at string, path []string) (cloudimport.Prop, json.RawMessage,
	bool) {
	o, _ := objectOf(raw)
	if key, v := o.member("value"); v != nil {
		inner, innerAt, ok := descendAt(v, at+importer.Pointer(key), path)
		if !ok {
			return cloudimport.Prop{}, nil, false
		}
		return l.prop(inner, innerAt), inner, true
	}
	if key, ref := o.member("reference"); ref != nil {
		if len(path) > 0 {
			return cloudimport.Prop{}, nil, false
		}
		c := l.secretReference(ref)
		l.settle(at)
		if c != nil {
			return cloudimport.Prop{Value: c}, nil, true
		}
		l.warn(at+importer.Pointer(key), fmt.Sprintf("the template %s gives a parameter at %s a Key Vault "+
			"reference naming no vault's ID and no secret's name, so it is not carried: name the vault by its "+
			"keyVault.id and the secret by its secretName", l.t.file, at))
		return cloudimport.Prop{}, nil, true
	}
	l.warn(at, fmt.Sprintf("the template %s gives a parameter at %s neither a value nor a Key Vault reference, "+
		"so it is not carried: write its value", l.t.file, at))
	return cloudimport.Prop{}, nil, true
}

// descendAt returns what raw, written at the JSON pointer at, holds at path,
// with the pointer it is written at; false where it holds nothing there.
func descendAt(raw json.RawMessage, at string, path []string) (json.RawMessage, string, bool) {
	for _, seg := range path {
		raw = descend(raw, seg)
		if raw == nil {
			return nil, "", false
		}
		at += importer.Pointer(seg)
	}
	return raw, at, true
}

// secretReference returns the credential the Key Vault reference ref names:
// the secret, by its name, read from the vault its keyVault.id names — a
// resource of the template, or one it reads and does not declare — nil where
// it names no vault's ID or no secret's name.
func (l *lowerer) secretReference(ref json.RawMessage) cloudimport.Value {
	o, _ := objectOf(ref)
	vault, _ := objectOf(o.get("keyVault"))
	secret, _ := text(o.get("secretName"))
	n, ok := l.t.node(vault.get("id"))
	if !ok || secret == "" {
		return nil
	}
	id, _, found := l.t.target(n)
	if !found {
		id = armexpr.Print(n)
	}
	return cloudimport.Credential{Node: id, What: "Key Vault secret " + secret}
}

// single returns the one value p holds, read where an expression reads it: a
// leaf's value; an object or an array holding no expression, as raw writes
// it, its literal; and any other the expression whole, deploy-time text.
func (l *lowerer) single(p cloudimport.Prop, raw json.RawMessage, whole armexpr.Node) cloudimport.Value {
	if p.Value != nil || (len(p.Props) == 0 && len(p.Items) == 0) {
		return p.Value
	}
	if raw != nil && !hasExpression(raw, 0) {
		return cloudimport.Literal{JSON: raw}
	}
	return l.opaque(whole)
}

// output returns what the output of the template dep deploys holds, read at
// attr — outputs, the output's name, value, and a path into the value — where
// that template is flattened. The output's value is lowered in that
// template; a path below it reads further into a reference or a literal. An
// output the template does not declare, one a copy loop computes or holding
// no value, and a path into any other value are not carried, with a
// value-not-carried warning; an output reading itself is deploy-time text.
func (l *lowerer) output(dep *resource, attr []string, whole armexpr.Node) cloudimport.Value {
	if len(attr) < 3 || !strings.EqualFold(attr[0], "outputs") || !strings.EqualFold(attr[2], "value") {
		return l.opaque(whole)
	}
	inner := dep.nested
	key, raw := inner.outputs.member(attr[1])
	o, _ := objectOf(raw)
	value := o.get("value")
	at := inner.at + importer.Pointer("outputs", key)
	switch {
	case raw == nil:
		l.warn(l.site(), fmt.Sprintf("the template %s reads at %s the output %s of %s, which the template %s "+
			"deploys does not declare, so it is not carried: read an output that template declares", l.t.file,
			l.site(), attr[1], dep.symbol, dep.symbol))
		return nil
	case o.get("copy") != nil || value == nil:
		l.outputNotCarried(inner, at, fmt.Sprintf("the template %s computes the output %s at %s with a copy "+
			"loop, or gives it no value, which the draft carries as no one value, so what reads it is not carried",
			inner.file, key, at))
		return nil
	case slices.Contains(l.r.resolving, dep.symbol):
		return l.opaque(whole)
	}
	v := l.r.outputValue(dep, key, value, at)
	rest := attr[3:]
	if len(rest) == 0 || v == nil {
		return v
	}
	switch v := v.(type) {
	case cloudimport.Ref:
		return cloudimport.Ref{Node: v.Node, Attr: append(slices.Clone(v.Attr), rest...)}
	case cloudimport.Literal:
		if held, _, ok := descendAt(v.JSON, "", rest); ok {
			return cloudimport.Literal{JSON: held}
		}
	}
	l.warn(l.site(), fmt.Sprintf("the template %s reads at %s into the output %s of %s a member its value holds "+
		"as no one value, so it is not carried: read the output whole", l.t.file, l.site(), key, dep.symbol))
	return nil
}

// outputValue returns the value the output key of the template dep deploys
// holds, value written at the JSON pointer at, lowered in that template once
// for the whole read: an output read again is the value lowered the first
// time. A value larger than maxValueSize is not carried, with a
// value-not-carried warning.
func (r *reader) outputValue(dep *resource, key string, value json.RawMessage, at string) cloudimport.Value {
	memo := dep.symbol + "\x00" + key
	if v, done := r.known[memo]; done {
		return v
	}
	r.resolving = append(r.resolving, dep.symbol)
	inner := dep.nested
	v := r.lowererAt(inner, nil, -1, 0).leaf(value, at+importer.Pointer("value"), "the output "+key)
	r.resolving = r.resolving[:len(r.resolving)-1]
	if size(v) > maxValueSize {
		d := cloudimport.Report(cloudimport.CodeValueNotCarried, fmt.Sprintf("the template %s computes the output "+
			"%s at %s as a value larger than the %d bytes the draft carries of one value, its parts composing "+
			"one another, so what reads it is not carried", inner.file, key, at, maxValueSize))
		d.SourceRef = inner.file + "#" + at
		r.note(d)
		v = nil
	}
	r.known[memo] = v
	return v
}

// size returns how large v is: the bytes of the text and JSON it holds, and
// one for each part of composed text.
func size(v cloudimport.Value) int {
	switch v := v.(type) {
	case cloudimport.Literal:
		return len(v.JSON)
	case cloudimport.Param:
		return len(v.Name)
	case cloudimport.Compose:
		n := 0
		for _, p := range v.Parts {
			n += 1 + size(p)
		}
		return n
	case cloudimport.Ref:
		n := len(v.Node)
		for _, a := range v.Attr {
			n += len(a)
		}
		return n
	case cloudimport.Credential:
		return len(v.Node) + len(v.What)
	case cloudimport.Opaque:
		return len(v.Text)
	}
	return 0
}

// propSize returns how large p is: the size of its value, and one for each
// member and item below it with theirs.
func propSize(p cloudimport.Prop) int {
	n := size(p.Value)
	for _, c := range p.Props {
		n += 1 + propSize(c)
	}
	for _, c := range p.Items {
		n += 1 + propSize(c)
	}
	return n
}

// resourceAcross returns the resource whose symbol is id, with the template
// holding it: of the template t, else of any template the read holds; nil
// where none is.
func (r *reader) resourceAcross(t *template, id string) (*template, *resource) {
	if res := t.resourceOf(id); res != nil {
		return t, res
	}
	for _, f := range r.families {
		for _, m := range f.members {
			if res := m.resourceOf(id); res != nil {
				return m, res
			}
		}
	}
	return nil, nil
}

// noNode reports whether res is no node of the graph: a resource whose
// template is flattened, or one by which a module sends its telemetry.
func (res *resource) noNode() bool {
	return res != nil && (res.nested != nil || res.telemetry)
}

// reference returns what the reference whole, naming the resource id and
// reading attr of it, lowers to: a Ref to the node id names; the value an
// output of the template id deploys holds, where that template is flattened
// and attr reads one of its outputs; and otherwise, where id names a resource
// that is no node, the expression whole as deploy-time text, with a
// value-not-carried warning, as the draft writes no such resource.
func (l *lowerer) reference(id string, attr []string, whole armexpr.Node) cloudimport.Value {
	_, res := l.r.resourceAcross(l.t, id)
	switch {
	case !res.noNode():
		return cloudimport.Ref{Node: id, Attr: attr}
	case res.nested != nil && len(attr) >= 3 && strings.EqualFold(attr[0], "outputs") &&
		strings.EqualFold(attr[2], "value"):
		return l.output(res, attr, whole)
	}
	l.warn(l.site(), fmt.Sprintf("the template %s reads at %s %s of %s, a %s resource the draft writes no "+
		"resource for, as it is read as the resources it deploys, or sends a module's telemetry; the expression "+
		"%s is kept as text evaluated when the template deploys, naming a resource the draft does not hold: "+
		"read an output of its template, or the resources it deploys", l.t.file, l.site(), readText(attr), id,
		deploymentsType, armexpr.Expression(whole)))
	return l.opaque(whole)
}

// readText says what attr reads of a resource, for a message.
func readText(attr []string) string {
	if len(attr) == 0 {
		return "the identifier"
	}
	return "the attribute " + strings.Join(attr, ".")
}

// site is the JSON pointer of what the lowerer lowers: its resource, else its
// template.
func (l *lowerer) site() string {
	if l.res != nil {
		return l.res.at
	}
	return l.t.at
}

// outputNotCarried warns value-not-carried of the output of the nested
// template inner at the JSON pointer at.
func (l *lowerer) outputNotCarried(inner *template, at, message string) {
	if l.quiet {
		return
	}
	d := cloudimport.Report(cloudimport.CodeValueNotCarried, message)
	d.SourceRef = inner.file + "#" + at
	l.r.note(d)
}

// plan decides, for each resource of t, what the graph takes of it, and for
// the templates nested in t in turn. A resource by which an Azure Verified
// Modules module sends its telemetry is never a node: where t is not the
// module's own template, already reported, it is not carried, with a
// resource-not-carried warning. A Microsoft.Resources/deployments resource
// deploying a resource module is one node where [reader.oneNode] keeps it one,
// unless t is, or is nested in, a resource module's template, whose child
// modules are read as their resources and never reported as modules. Every
// other holding its template inline, or linking by a relative path a template
// the import holds, has that template flattened: read as a template of its
// own, its resources joining the graph with their symbols behind the
// deploying resource's and a slash. Its parameters take the values the
// deploying resource gives them under properties.parameters, read where that
// resource is written, else their defaults; under the outer expression scope,
// as [evaluatedOuter] says, its expressions read the parameters and variables
// of the template holding it instead. A reference to an output of the
// template reads the value the output holds. A template, or a module kept one
// node, deployed into another resource group or subscription is read the same
// way, with a deployment-scope-merged warning naming the annotation a
// boundary of its own would take. A nested template the import refuses,
// read as a template it is handed is, halts source-refused; one nested deeper
// than the import follows, past the resources the import reads of nested
// templates, or linking a template that deploys it is not read, with a
// warning, and the resource deploying it is a node as written.
func (r *reader) plan(t *template) {
	for _, res := range t.resources {
		if r.halted {
			return
		}
		switch {
		case t.isTelemetry(res):
			res.telemetry = true
			if t.module == nil {
				r.telemetryNotCarried(t, res)
			}
		case strings.EqualFold(res.typ, deploymentsType):
			r.planNested(t, res)
		}
	}
}

// planNested decides what the graph takes of the
// Microsoft.Resources/deployments resource res of t, as [reader.plan] says.
func (r *reader) planNested(t *template, res *resource) {
	file, at, top, inline, ok := r.nestedTemplate(t, res)
	switch {
	case !ok:
		return
	case len(t.frames)+1 >= maxNesting:
		r.notFlattened(t, res, fmt.Sprintf("it is nested deeper than the %d levels the import follows", maxNesting))
		return
	case !inline && deploysItself(t, file):
		d := cloudimport.Report(cloudimport.CodeLinkedSourceNotRead, res.symbol+" is a "+deploymentsType+
			" resource of "+t.file+" linking the template "+file+", which deploys the template linking it, "+
			"directly or through others, so it is not read again: no resource of it is drafted a second time, and "+
			res.symbol+" is drafted as written; link each template once")
		d.SourceRef = t.file + "#" + res.at
		r.note(d)
		return
	}
	props, _ := objectOf(res.body.get("properties"))
	outer, scope := inline && evaluatedOuter(t, top, props), t.otherScope(res)
	rec := recognise(top)
	inModule := t.withinResourceModule()
	why := ""
	if rec != nil && rec.kind() == resourceModule && !inModule {
		var one bool
		if one, why = r.oneNode(t, res, file, at, top, rec, outer); one {
			r.scopeMerged(t, res, scope)
			return
		}
	}
	if refused := nestedRefusal(top); refused != "" {
		d := cloudimport.Report(cloudimport.CodeSourceRefused, "the template "+res.symbol+" of "+t.file+
			" deploys, written at "+file+"#"+at+", is refused: "+refused)
		d.SourceRef = file + "#" + at
		r.halt(d)
		return
	}
	count := len(resourceValues(top.exact("resources")))
	if r.nestedRead+count > maxNestedResources {
		r.notFlattened(t, res, fmt.Sprintf("the nested templates of the import deploy more than the %d "+
			"resources it reads of them", maxNestedResources))
		return
	}
	r.nestedRead += count
	fam := t.family
	if scope != "" {
		fam = &family{}
		r.families = append(r.families, fam)
	}
	inner := r.template(file, at, top, &nesting{up: t, dep: res, outer: outer, family: fam})
	inner.module = rec
	res.nested = inner
	if rec != nil && !inModule {
		r.recognisedFlattened(t, res, rec, why)
	}
	r.scopeMerged(t, res, scope)
	if props.get("parametersLink") != nil {
		d := cloudimport.Report(cloudimport.CodeValueNotCarried, res.symbol+" is a "+deploymentsType+
			" resource of "+t.file+" reading the values of its template's parameters from the parameters file "+
			"it links, which the import does not read: each parameter takes its default, and what reads one "+
			"declaring none is not carried; write their values under properties.parameters")
		d.SourceRef = t.file + "#" + res.at
		r.note(d)
	}
	r.plan(inner)
}

// scopeMerged warns deployment-scope-merged where res of t deploys into scope,
// another scope than t's own, naming the annotation a boundary of its own
// would take; it does nothing where scope is empty.
func (r *reader) scopeMerged(t *template, res *resource, scope string) {
	if scope == "" {
		return
	}
	d := cloudimport.Report(cloudimport.CodeDeploymentScopeMerged, fmt.Sprintf("%s is a %s resource of %s "+
		"deploying into %s, another scope than the template's own; what it deploys is drafted into the one "+
		"boundary the import writes, whose template deploys into one scope: to deploy it into %s, move the "+
		"Components drafted from it into a boundary of their own annotated %s", res.symbol, deploymentsType, t.file,
		scope, scope, armcore.AnnotationResourceGroup))
	d.SourceRef = t.file + "#" + res.at
	r.note(d)
}

// withinResourceModule reports whether t is, or is nested in, the template of
// an Azure Verified Modules resource module: the child modules of such a
// template are read as the resources they deploy, never as modules of their
// own.
func (t *template) withinResourceModule() bool {
	for x := t; x != nil; x = x.up {
		if x.module != nil && x.module.kind() == resourceModule {
			return true
		}
	}
	return false
}

// nestedTemplate returns the template res deploys where the import reads it
// — the template it holds inline under properties.template, or the one it
// links by a relative path the import holds — with the source holding it, the
// JSON pointer it is written at, and whether it is held inline; false where
// res deploys none the import reads.
func (r *reader) nestedTemplate(t *template, res *resource) (file, at string, top obj, inline, ok bool) {
	pk, rawProps := res.body.member("properties")
	props, _ := objectOf(rawProps)
	if tk, raw := props.member("template"); raw != nil {
		top, ok = objectOf(raw)
		return t.file, res.at + importer.Pointer(pk, tk), top, true, ok
	}
	link, _ := objectOf(props.get("templateLink"))
	rel, linked := text(link.get("relativePath"))
	if !linked || r.sel == nil || r.unreadLink(t, res) != "" {
		return "", "", nil, false, false
	}
	h := r.sel.linkedTemplate(t.file, rel)
	if h == nil {
		return "", "", nil, false, false
	}
	return h.name, "", h.top, false, true
}

// evaluatedOuter reports whether a template held inline — with the top-level
// object top, by a resource of t whose properties are props — is evaluated in
// the scope of t: where neither t nor the template is of language version 2.0
// and its properties.expressionEvaluationOptions.scope is not inner. Under
// language version 1.0 Azure Resource Manager's default is outer; under 2.0
// it is inner, and outer is not admitted.
func evaluatedOuter(t *template, top, props obj) bool {
	if t.v2 || isTwoZero(top) {
		return false
	}
	opts, _ := objectOf(props.get("expressionEvaluationOptions"))
	scope, ok := text(opts.get("scope"))
	return !ok || !strings.EqualFold(scope, "inner")
}

// deploysItself reports whether the template file, linked from t, is t or a
// template t is nested in: a template deploying itself, directly or through
// others.
func deploysItself(t *template, file string) bool {
	same := func(x *template) bool { return x.at == "" && path.Clean(x.file) == path.Clean(file) }
	if same(t) {
		return true
	}
	for _, f := range t.frames {
		if same(f.t) {
			return true
		}
	}
	return false
}

// nestedRefusal says why the import does not read a nested template whose
// top-level object is top, empty where it reads it: as a template the import
// is handed, except that one writing no resources holds none.
func nestedRefusal(top obj) string {
	if raw := top.exact("resources"); raw == nil || isNull(raw) {
		return refusedVersion(top)
	}
	return refusal(top)
}

// otherScope returns, as report text, the scope res is declared to deploy
// into where it is not the one t deploys into — its subscriptionId and its
// resourceGroup, each printed canonically — empty where both are t's own.
func (t *template) otherScope(res *resource) string {
	var parts []string
	if res.id.sub != "" {
		parts = append(parts, "the subscription "+res.id.sub)
	}
	if res.id.group != "" {
		parts = append(parts, "the resource group "+res.id.group)
	}
	return strings.Join(parts, " and ")
}

// notFlattened warns that the template res deploys is not read, and why: res
// is a node as written.
func (r *reader) notFlattened(t *template, res *resource, why string) {
	d := cloudimport.Report(cloudimport.CodeResourceNotCarried, res.symbol+" is a "+deploymentsType+" resource of "+
		t.file+" whose template is not read, as "+why+": no resource of that template is drafted on its own, and "+
		res.symbol+" is drafted as written")
	d.SourceRef = t.file + "#" + res.at
	r.note(d)
}

// alias reads each existing resource of a template that another template of
// its family declares — of the same type, name and scope, and created — as
// that resource: it takes that resource's symbol and is no node of its own.
func (r *reader) alias() {
	for _, f := range r.families {
		for _, t := range f.members {
			for _, res := range t.resources {
				if !res.existing || res.aliased || len(res.id.segs) == 0 {
					continue
				}
				if d := f.declares(t, res.id); d != nil {
					res.symbol, res.aliased = d.symbol, true
				}
			}
		}
	}
}

// declares returns the first resource a member of f other than own creates
// with the identity id, nil where none does.
func (f *family) declares(own *template, id identity) *resource {
	for _, t := range f.members {
		if t == own {
			continue
		}
		for _, res := range t.resources {
			if !res.existing && !res.aliased && !res.telemetry && res.nested == nil && res.module == nil &&
				sameIdentity(res.id, id) {
				return res
			}
		}
	}
	return nil
}

// sameIdentity reports whether a and b name one resource: one type, one name
// and one scope, regardless of case.
func sameIdentity(a, b identity) bool {
	return strings.EqualFold(a.typ, b.typ) && sameText(a.segs, b.segs) && strings.EqualFold(a.sub, b.sub) &&
		strings.EqualFold(a.group, b.group)
}

// yield records, for each resource of t whose template is flattened, the IDs
// of the nodes that template yields, and returns those t yields: each of its
// resources that is a node, and what each template flattened in it yields.
func (r *reader) yield(t *template) []string {
	var out []string
	for _, res := range t.resources {
		switch {
		case res.telemetry, res.aliased:
		case res.nested != nil:
			ids := r.yield(res.nested)
			r.yields[res.symbol] = ids
			out = append(out, ids...)
		default:
			out = append(out, res.symbol)
		}
	}
	return out
}

// build returns the nodes t yields, in the order it writes its resources:
// each resource that is a node, and the nodes of each template flattened in
// it.
func (r *reader) build(t *template) []*cloudimport.Node {
	var out []*cloudimport.Node
	for _, res := range t.resources {
		switch {
		case res.telemetry, res.aliased:
		case res.nested != nil:
			out = append(out, r.build(res.nested)...)
		default:
			out = append(out, r.node(t, res))
		}
	}
	return out
}

// flatDependencies returns deps, each once, a dependency on a resource whose
// template is flattened read as a dependency on each node that template
// yields other than self.
func (r *reader) flatDependencies(self string, deps []string) []string {
	var out []string
	add := func(id string) {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	for _, dep := range deps {
		ids, flattened := r.yields[dep]
		if !flattened {
			add(dep)
			continue
		}
		for _, id := range ids {
			if id != self {
				add(id)
			}
		}
	}
	return out
}

// conditions returns the condition the lowerer's resource is deployed under:
// its own, and that of each Microsoft.Resources/deployments resource it is
// read through. One condition is itself; several are deploy-time text, their
// and, outermost first; a condition known false makes the whole false.
func (l *lowerer) conditions() cloudimport.Value {
	var values []cloudimport.Value
	var exprs []armexpr.Node
	for v := l; v != nil; v = v.via {
		c := v.condition()
		if c == nil {
			continue
		}
		if lit, ok := c.(cloudimport.Literal); ok && strings.TrimSpace(string(lit.JSON)) == "false" {
			return lit
		}
		if e := v.conditionExpression(); e != nil {
			values, exprs = append(values, c), append(exprs, e)
		}
	}
	switch len(values) {
	case 0:
		return nil
	case 1:
		return values[0]
	}
	slices.Reverse(exprs)
	return cloudimport.Opaque{Text: armexpr.Expression(armexpr.Call{Name: "and", Args: exprs}), Target: target}
}

// conditionExpression returns the lowerer's resource's condition as an
// expression of the root template, every variable and parameter it reads
// written in place where it can be; nil where it reads as no expression.
func (l *lowerer) conditionExpression() armexpr.Node {
	n, ok := expression(l.res.condition)
	if !ok {
		return nil
	}
	return l.t.inline(n, l)
}

// telemetryNotCarried warns that res, by which an Azure Verified Modules
// module sends its telemetry, is not drafted.
func (r *reader) telemetryNotCarried(t *template, res *resource) {
	d := cloudimport.Report(cloudimport.CodeResourceNotCarried, fmt.Sprintf("the template %s writes %s, a %s "+
		"resource by which an Azure Verified Modules module sends its telemetry, its name opening %s; it is not "+
		"drafted, as whether a Recipe sends telemetry is the Recipe's to set", t.file, res.symbol, deploymentsType,
		telemetryPrefix))
	d.SourceRef = t.file + "#" + res.at
	r.note(d)
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfncore/cfntemplate"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// stack is a template as the graph holds it: the root template, or the
// template a nested stack or a serverless application deploys, read once for
// each resource deploying it.
type stack struct {
	t *template
	// up is the stack whose template declares the resource deploying t, nil
	// for the root; at is that resource's logical ID, and node the resource
	// as the reader read it.
	up   *stack
	at   string
	node *cloudimport.Node
	// prefix opens the ID of each node the stack's resources are: empty for
	// the root, up's prefix and at for any other.
	prefix string
	// ids holds the ID of the node each resource of t is, by its logical ID.
	ids map[string]string
	// nested holds the stack each resource of t deploying a template read
	// into the graph deploys, by the resource's logical ID.
	nested map[string]*stack
	// own are the nodes of t's resources, each with its logical ID.
	own []home
	// contributed are the IDs of the nodes the stack contributes, its own
	// and those of the stacks nested in it, in ID order; nil until known.
	contributed []string
	// deps are the IDs of the nodes every node of the stack depends on
	// beside its own dependencies: those the resources deploying it depend on.
	deps []string
	// args are what each parameter of t reads, and outputs each output of t,
	// by name.
	args    map[string]*argument
	outputs map[string]*outputRead
	// cond is the condition the resource deploying t is deployed under,
	// lowered where that resource is written, once condRead.
	cond     cloudimport.Value
	condRead bool
}

// home is a node with the stack holding it and its logical ID there.
type home struct {
	s  *stack
	n  *cloudimport.Node
	id string
}

// progress is how far a value read once is.
type progress int

// The steps of a value read once.
const (
	pending progress = iota
	inProgress
	settled
)

// argument is what a Ref to one parameter of a nested template reads.
type argument struct {
	// decl is the parameter as the nested template declares it.
	decl cloudimport.Parameter
	// bound is the value the resource deploying the template binds it to,
	// written in that resource's template at the JSON pointer at; def is its
	// default, written in the nested template at at, where nothing binds it;
	// file is the template either is written in.
	bound, def *value
	at, file   string
	// graph names the graph's parameter it reads: the one it is bound to
	// whole, or the one the graph takes for it where it is bound to nothing
	// and declares no default; value is the value it reads where graph is
	// empty, once state is settled, and why says why the draft carries none,
	// for a note, where it is not carried.
	graph string
	value cloudimport.Value
	why   string
	state progress
}

// outputRead is one output of a nested template, read once.
type outputRead struct {
	o     cloudimport.Output
	value cloudimport.Value
	state progress
}

// kind names what the resource deploying s is: a nested stack or a
// serverless application.
func (s *stack) kind() string {
	return linkKind(s.node.Type)
}

// linkKind names a resource of type typ deploying a template.
func linkKind(typ string) string {
	if typ == applicationType {
		return "serverless application"
	}
	return "nested stack"
}

// linkProperty is the property naming the template a resource of type typ
// deploys.
func linkProperty(typ string) string {
	if typ == applicationType {
		return applicationLink
	}
	return stackLink
}

// flatten reads each nested stack and each serverless application of the
// graph g, read from the root template, whose template the import reads, as
// that template's resources: each a node whose ID is the deploying
// resource's logical ID before its own — or, where a node takes that ID
// already, that ID and the least number from 2 none takes —, its Source the
// nested template's, the deploying resource itself no node, as [reader.descend]
// says. A dependency on such a resource is one on each node its template
// contributes, and each node a template contributes depends, beside its own
// dependencies, on what the resource deploying it depends on. A resource
// deploying a template the import does not read stays a node: one linked by a
// URL, located by a value the template computes or by a serverless
// application's ID, or by a path leaving the imported directory or naming no
// template the import holds, with a linked-source-not-read warning naming
// it; one deploying a template that deploys it, directly or through others,
// with the same warning; one past the resources the import reads of nested
// templates with a resource-not-carried warning. Each resource read as its
// template's resources is recorded resource-folded, naming the nodes its
// template contributes. The graph's nodes are ordered by ID again, and its
// parameters by name.
func (r *reader) flatten(g *cloudimport.Graph) {
	r.graph = g
	r.byFile = map[string]*template{}
	for _, t := range r.templates {
		r.byFile[path.Clean(t.file)] = t
	}
	r.taken, r.paramTaken = map[string]bool{}, map[string]bool{}
	for _, e := range r.root.resources {
		r.taken[e.Key] = true
	}
	for _, p := range g.Params {
		r.paramTaken[p.Name] = true
	}
	root := &stack{t: r.root, ids: map[string]string{}, nested: map[string]*stack{}}
	r.stacks = []*stack{root}
	r.build(root, g.Nodes)

	r.home = map[*cloudimport.Node]home{}
	r.nodeIDs = map[string]bool{}
	var nodes []*cloudimport.Node
	for _, s := range r.stacks {
		if s.up != nil {
			s.deps = addNew(r.dependencies(s.up, s.node.DependsOn), s.up.deps...)
		}
		for _, h := range s.own {
			if s.up != nil || slices.ContainsFunc(h.n.DependsOn, func(dep string) bool { return s.nested[dep] != nil }) {
				h.n.DependsOn = addNew(r.dependencies(s, h.n.DependsOn), s.deps...)
			}
			r.home[h.n] = h
			r.nodeIDs[h.n.ID] = true
			nodes = append(nodes, h.n)
		}
	}
	for _, s := range r.stacks[1:] {
		ids := "no resource"
		if c := s.contribution(); len(c) > 0 {
			ids = strings.Join(c, ", ")
		}
		d := cloudimport.Report(cloudimport.CodeResourceFolded, fmt.Sprintf("%s (%s) of %s is read as the resources "+
			"of its template %s, which join the draft as %s; nothing is drafted for it", s.at, s.node.Type, s.up.t.file,
			s.t.file, ids))
		d.SourceRef = s.node.Source
		r.note(d)
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	g.Nodes = nodes
	sort.SliceStable(g.Params, func(i, j int) bool { return g.Params[i].Name < g.Params[j].Name })
}

// build reads nodes, those of the resources of s's template, into s: each
// deploying a template the import reads into the graph is read as that
// template's resources, after every other has taken its ID, and every other
// is a node of s.
func (r *reader) build(s *stack, nodes []*cloudimport.Node) {
	type deploying struct {
		n *cloudimport.Node
		t *template
	}
	var later []deploying
	for _, n := range nodes {
		if t := r.deployed(s, n); t != nil {
			later = append(later, deploying{n, t})
			continue
		}
		r.keep(s, n)
	}
	for _, d := range later {
		if r.nestedRead+len(d.t.resources) > cloudimport.MaxNestedResources {
			r.warnNode(cloudimport.CodeResourceNotCarried, d.n, fmt.Sprintf("%s (%s) of %s deploys the template %s, "+
				"which is not read, as the nested stacks of the import deploy more than the %d resources it reads of "+
				"them: no resource of that template is drafted, and %s is drafted as written", d.n.ID, d.n.Type,
				s.t.file, d.t.file, cloudimport.MaxNestedResources, d.n.ID))
			r.keep(s, d.n)
			continue
		}
		r.descend(s, d.n, d.t)
	}
}

// keep makes n, read from the resource of s's template of its ID, a node of
// s, its ID made one no other node takes where s is nested.
func (r *reader) keep(s *stack, n *cloudimport.Node) {
	id := n.ID
	if s.up != nil {
		n.ID = unique(r.taken, s.prefix+id)
	}
	s.ids[id] = n.ID
	s.own = append(s.own, home{s: s, n: n, id: id})
}

// unique returns id where taken does not hold it, else id and the least
// number from 2 that taken does not hold, recording it in taken.
func unique(taken map[string]bool, id string) string {
	out := id
	for i := 2; taken[out]; i++ {
		out = id + strconv.Itoa(i)
	}
	taken[out] = true
	return out
}

// deployed returns the template n, read from the resource of s's template of
// its ID, deploys where the import reads it into the graph: n is a nested
// stack or a serverless application whose TemplateURL or Location is a path,
// inside the imported directory, of a template the import reads, which
// neither s's template nor one deploying it is. For any other nested stack or
// serverless application it warns, and returns nil.
func (r *reader) deployed(s *stack, n *cloudimport.Node) *template {
	res, _ := objectOf(s.t.resources.get(n.ID))
	link, typ, deploys := linkOf(res)
	if !deploys {
		return nil
	}
	t, how := r.linkedTemplate(s.t.file, link, typ)
	if t == nil {
		r.warnNode(cloudimport.CodeLinkedSourceNotRead, n, fmt.Sprintf("%s (%s) of %s deploys a template %s, which "+
			"the import does not read: no resource of that template is drafted, %s is drafted as written, and each "+
			"output of it the template reads is carried as text CloudFormation evaluates when the stack deploys; to "+
			"draft its resources, import the directory holding both templates, each named %s, and write its %s as "+
			"a relative path to it", n.ID, typ, s.t.file, how, n.ID, templateNames(), linkProperty(typ)))
		return nil
	}
	for x := s; x != nil; x = x.up {
		if x.t == t {
			r.warnNode(cloudimport.CodeLinkedSourceNotRead, n, fmt.Sprintf("%s (%s) of %s links its template %s, "+
				"which deploys the template linking it, directly or through others, so it is not read again: no "+
				"resource of it is drafted a second time, and %s is drafted as written; link each template once", n.ID,
				typ, s.t.file, t.file, n.ID))
			return nil
		}
	}
	return t
}

// linkedTemplate returns the template the import reads that v, the
// TemplateURL or Location of a resource of type typ written in the template
// at from, names; nil where it names none, with how it locates the template
// it deploys, for a warning.
func (r *reader) linkedTemplate(from string, v *value, typ string) (*template, string) {
	link, isText := schemadraft.JSONText(v.json())
	o, isObject := objectOf(v)
	switch {
	case v == nil || isNull(v):
		return nil, "it names by no " + linkProperty(typ)
	case isObject && typ == applicationType && o.get("ApplicationId") != nil:
		return nil, "located by the ID of an application the AWS Serverless Application Repository publishes"
	case isObject && len(o) == 1 && cfntemplate.IsIntrinsicKey(o[0].Key):
		return nil, "located by a value the template computes when the stack deploys"
	case !isText:
		return nil, "located by a value that is no path"
	}
	if why := notLocal(link); why != "" {
		return nil, "linked by " + why
	}
	target, inside := linked(from, link)
	if !inside {
		return nil, "linked by a path leaving the imported directory"
	}
	if t := r.byFile[target]; t != nil {
		return t, ""
	}
	return nil, "linked by a path naming no template the import holds"
}

// warnNode reports a diagnostic of code on n, a resource deploying a
// template.
func (r *reader) warnNode(code cloudimport.Code, n *cloudimport.Node, message string) {
	d := cloudimport.Report(code, message)
	d.SourceRef = n.Source
	r.note(d)
}

// descend reads t, the template n deploys, as a stack nested in s: what n
// writes beside its template and its parameters is not carried, and is
// reported value-not-carried; each parameter of t takes the value n's
// Parameters bind it to, as [reader.bind] says; and t's resources are read
// into the stack as s's are. The notes name n read as t's resources, and,
// once for each template, what t declares that no resource carries, as the
// root template's are named: its transforms, its Metadata, its Rules and the
// names its outputs are exported under.
func (r *reader) descend(s *stack, n *cloudimport.Node, t *template) {
	child := &stack{t: t, up: s, at: n.ID, node: n, prefix: s.prefix + n.ID, ids: map[string]string{},
		nested: map[string]*stack{}, args: map[string]*argument{}, outputs: map[string]*outputRead{}}
	s.nested[n.ID] = child
	r.stacks = append(r.stacks, child)
	r.nestedRead += len(t.resources)
	file := draftyaml.Comment(t.file)
	outputs, exports := t.outputsRead(" of the template " + file)
	for _, o := range outputs {
		child.outputs[o.Name] = &outputRead{o: o}
	}
	r.graph.Notes = append(r.graph.Notes, "The "+child.kind()+" "+draftyaml.Comment(n.ID)+" of "+
		draftyaml.Comment(s.t.file)+" is read as the resources of its template "+file+", each drafted with "+
		draftyaml.Comment(child.prefix)+" before its logical ID.")
	if !slices.Contains(r.noted, t.file) {
		r.noted = append(r.noted, t.file)
		r.graph.Notes = append(append(r.graph.Notes, t.declared("The template "+file)...), exports...)
	}
	r.leftOn(s, child)
	r.bind(s, child)
	r.build(child, r.nodes(t))
}

// leftOn warns value-not-carried of what the resource deploying child, of
// s's template, writes beside its template, its parameters and the members
// every node is read from: no draft deploys that resource.
func (r *reader) leftOn(s, child *stack) {
	res, _ := objectOf(s.t.resources.get(child.at))
	var left []string
	for _, e := range res {
		switch {
		case e.Key == "Properties":
			props, _ := objectOf(e.Value)
			for _, p := range props {
				if p.Key != linkProperty(child.node.Type) && p.Key != "Parameters" {
					left = append(left, "Properties."+p.Key)
				}
			}
		case !isConsumed(e.Key):
			left = append(left, e.Key)
		}
	}
	if len(left) == 0 {
		return
	}
	r.warnNode(cloudimport.CodeValueNotCarried, child.node, "the template "+s.t.file+" writes "+strings.Join(left, ", ")+
		" on the "+child.kind()+" "+child.at+", which the draft reads as the resources of its template "+child.t.file+
		" and carries none of: apply them to those resources by hand")
}

// bind records what each parameter of child's template reads: the value the
// resource deploying it, of s's template, binds it to under its Parameters —
// lowered where that resource is written, once something reads it —, else
// its default, else a parameter the graph takes for it, named by the
// stack's prefix and the parameter's name, named in the notes with the
// AllowedPattern it declares. A NoEcho parameter's default is never read, so
// one bound to nothing is such a parameter, secure. A binding of a parameter
// the template does not declare, and Parameters written as no object, are
// reported value-not-carried.
func (r *reader) bind(s, child *stack) {
	res, _ := objectOf(s.t.resources.get(child.at))
	props, _ := objectOf(res.get("Properties"))
	at := jsonPointer("Resources", child.at, "Properties", "Parameters")
	v := props.get("Parameters")
	binds, isObject := objectOf(v)
	if v != nil && !isNull(v) && (!isObject || len(binds) == 1 && cfntemplate.IsIntrinsicKey(binds[0].Key)) {
		r.warnAt(cloudimport.CodeValueNotCarried, s.t.file, at, "the template "+s.t.file+" writes the Parameters "+
			"of the "+child.kind()+" "+child.at+" at "+at+" as no object of each parameter's value by its name, so no "+
			"parameter of its template "+child.t.file+" is bound: write each parameter's value under Parameters")
		binds = nil
	}
	for _, e := range binds {
		if child.t.params.get(e.Key) == nil {
			here := at + jsonPointer(e.Key)
			r.warnAt(cloudimport.CodeValueNotCarried, s.t.file, here, "the template "+s.t.file+" binds the "+
				"parameter "+e.Key+" of the "+child.kind()+" "+child.at+" at "+here+", which its template "+
				child.t.file+" declares no parameter of, so it is not read: remove it from the stack's Parameters, or "+
				"declare it in "+child.t.file)
		}
	}
	declared, _ := child.t.parameters()
	for _, p := range declared {
		a := &argument{decl: p}
		child.args[p.Name] = a
		decl, _ := objectOf(child.t.params.get(p.Name))
		if p.Default != nil {
			a.def, a.file, a.at = decl.get("Default"), child.t.file, jsonPointer("Parameters", p.Name, "Default")
		}
		if b := binds.get(p.Name); b != nil && !isNoValue(b) {
			a.bound, a.file, a.at = b, s.t.file, at+jsonPointer(p.Name)
		}
		if a.bound != nil || a.def != nil {
			continue
		}
		q := p
		q.Name = unique(r.paramTaken, child.prefix+p.Name)
		a.graph = q.Name
		r.graph.Params = append(r.graph.Params, q)
		why := "which declares no default for it"
		if def := decl.get("Default"); def != nil && !isNull(def) {
			why = "whose default the draft never writes, as it is NoEcho"
		}
		r.graph.Notes = append(r.graph.Notes, "The "+child.kind()+" "+draftyaml.Comment(child.at)+" binds no value to "+
			"the parameter "+draftyaml.Comment(p.Name)+" of "+draftyaml.Comment(child.t.file)+", "+why+", so the "+
			"draft reads it as the template parameter "+draftyaml.Comment(q.Name)+".")
		if pattern, ok := schemadraft.JSONText(decl.get("AllowedPattern").json()); ok {
			r.graph.Notes = append(r.graph.Notes, patternNote(q.Name, pattern))
		}
	}
}

// isNoValue reports whether v is a Ref to AWS::NoValue: a parameter a stack
// binds to it is bound to nothing.
func isNoValue(v *value) bool {
	o, _ := objectOf(v)
	name, _ := schemadraft.JSONText(o.get("Ref").json())
	return len(o) == 1 && name == "AWS::NoValue"
}

// dependencies returns the IDs of the nodes the names deps of resources of
// s's template name, each once, in order: a resource read as its template's
// resources names each node that template contributes, a resource that is a
// node names its node, and a name s's template declares no resource of names
// the ID a resource of that name would take.
func (r *reader) dependencies(s *stack, deps []string) []string {
	var out []string
	for _, dep := range deps {
		switch child, id := s.nested[dep], s.ids[dep]; {
		case child != nil:
			out = addNew(out, child.contribution()...)
		case id != "":
			out = addNew(out, id)
		default:
			out = addNew(out, s.prefix+dep)
		}
	}
	return out
}

// addNew returns out with each of ids it does not hold appended, in order.
func addNew(out []string, ids ...string) []string {
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// contribution returns the IDs of the nodes s contributes: its own and those
// of each stack nested in it, in ID order.
func (s *stack) contribution() []string {
	if s.contributed != nil {
		return s.contributed
	}
	out := []string{}
	for _, h := range s.own {
		out = append(out, h.n.ID)
	}
	for _, e := range s.t.resources {
		if child := s.nested[e.Key]; child != nil {
			out = append(out, child.contribution()...)
		}
	}
	slices.Sort(out)
	s.contributed = out
	return out
}

// warnAt records a diagnostic of code on what the template file writes at
// the JSON pointer at: while the graph is lowered, among what lowering
// reports.
func (r *reader) warnAt(code cloudimport.Code, file, at, message string) {
	d := cloudimport.Report(code, message)
	d.SourceRef = file + "#" + at
	if r.lowering != nil {
		*r.lowering = append(*r.lowering, d)
		return
	}
	r.note(d)
}

// scope returns a lowerer of s's template, reporting what it lowers among
// what lowering the graph reports, with an expansion budget of its own.
func (r *reader) scope(s *stack) *lowerer {
	return &lowerer{r: r, t: s.t, s: s, params: r.graph.Params, notes: r.lowering, spent: new(cloudimport.Budget)}
}

// binding is what a Ref to a parameter of the lowerer's template reads.
type binding struct {
	// declared reports that the template declares the parameter.
	declared bool
	// param is the graph's parameter it reads: the root template's own, the
	// one a nested template's parameter is bound to whole, or the one the
	// graph takes for it; nil where it reads value.
	param *cloudimport.Parameter
	// value is the value it reads where param is nil, and decl the parameter
	// as its nested template declares it; nil for the root template's. why
	// says why the draft carries no value, where it carries none.
	value cloudimport.Value
	decl  *cloudimport.Parameter
	why   string
}

// parameter returns the parameter b reads as the graph holds one: the
// graph's own, or, for a parameter of a nested template reading a literal
// value, its declaration supplied that value; nil where it reads any other
// value.
func (b binding) parameter() *cloudimport.Parameter {
	if b.param != nil || b.decl == nil {
		return b.param
	}
	lit, ok := b.value.(cloudimport.Literal)
	if !ok {
		return nil
	}
	q := *b.decl
	q.Default, q.Supplied = nil, lit
	return &q
}

// declares reports whether the lowerer's template declares a parameter named
// name.
func (l *lowerer) declares(name string) bool {
	return l.t.params.get(name) != nil
}

// binding returns what a Ref to the parameter name of the lowerer's template
// reads: the root template's parameter is the graph's of that name; a nested
// template's reads what [reader.resolve] says.
func (l *lowerer) binding(name string) binding {
	if !l.declares(name) {
		return binding{}
	}
	if l.s.up == nil {
		return binding{declared: true, param: l.graphParam(name)}
	}
	a := l.s.args[name]
	l.r.resolve(l.s, a)
	if a.graph != "" {
		return binding{declared: true, param: l.graphParam(a.graph), decl: &a.decl}
	}
	return binding{declared: true, value: a.value, decl: &a.decl, why: a.why}
}

// resolve reads, once, the value the parameter a of the nested template of s
// reads. A parameter the resource deploying the template binds to a
// parameter of the graph whole reads that parameter, unless it reads the
// Systems Manager parameter its value names and that parameter is of another
// type. Any other reads the value the binding lowers to where the resource
// is written; one the binding gives no value — its Fn::If carrying the
// branch AWS::NoValue — or bound to nothing reads its default, lowered in
// the nested template, as CloudFormation reads it, and is not carried where
// it declares none, as CloudFormation requires a value. A parameter reading
// the Systems Manager parameter its value names reads the dynamic reference
// resolving the one its literal value names, and is not carried where its
// value is no literal name. A NoEcho parameter's value is carried only where
// it is a parameter, a reference to a resource or a secret; one written in
// the template is never written. A binding to an object or a list, a value
// reading itself through the parameters and outputs of nested stacks, and
// one larger than cloudimport.MaxValueSize are not carried. Each value not
// carried is reported value-not-carried, and why says why.
func (r *reader) resolve(s *stack, a *argument) {
	switch a.state {
	case settled:
		return
	case inProgress:
		r.warnAt(cloudimport.CodeValueNotCarried, a.file, a.at, "the value of the parameter "+a.decl.Name+" of the "+
			s.kind()+" "+s.at+" reads itself through the parameters and outputs of nested stacks, so it is not "+
			"carried: bind each parameter to a value that does not read its own stack's outputs")
		a.why = "whose value reads itself through the parameters and outputs of nested stacks"
		return
	}
	a.state = inProgress
	defer func() { a.state = settled }()
	if a.graph != "" {
		return
	}
	a.why = ""
	var v cloudimport.Value
	present := a.bound != nil
	if present {
		var p cloudimport.Prop
		p, present = r.scope(s.up).prop(a.bound, a.at)
		v = p.Value
		switch param, isParam := v.(cloudimport.Param); {
		case len(p.Props) > 0 || len(p.Items) > 0:
			r.warnAt(cloudimport.CodeValueNotCarried, a.file, a.at, "the template "+a.file+" binds the parameter "+
				a.decl.Name+" of the "+s.kind()+" "+s.at+" at "+a.at+" to an object or a list, which a nested "+
				"stack's parameter does not take, so it is not carried: bind it to text")
			a.why = "which its stack binds to an object or a list"
			return
		case !present:
			if a.def == nil {
				r.noValue(s, a)
				return
			}
		case isParam && sameType(&a.decl, r.scope(s.up).graphParam(param.Name)):
			a.graph = param.Name
			return
		case a.decl.Secure && !isReference(v):
			if v != nil {
				r.warnAt(cloudimport.CodeValueNotCarried, a.file, a.at, "the template "+a.file+" binds the NoEcho "+
					"parameter "+a.decl.Name+" of the "+s.kind()+" "+s.at+" at "+a.at+" to a value written in the "+
					"template, which the draft never writes, so it is not carried: pass it through a NoEcho parameter "+
					"of "+a.file)
				a.why = "whose value the template writes, which the draft never writes, as it is NoEcho"
			}
			return
		}
	}
	if !present {
		p, _ := r.scope(s).prop(a.def, jsonPointer("Parameters", a.decl.Name, "Default"))
		v = p.Value
	}
	v = r.ssmValue(s, a, v)
	if v != nil && cloudimport.Size(v) > cloudimport.MaxValueSize {
		r.warnAt(cloudimport.CodeValueNotCarried, a.file, a.at, "the value of the parameter "+a.decl.Name+" of the "+
			s.kind()+" "+s.at+" is "+cloudimport.TooLarge())
		a.why = "whose value is larger than the draft carries of one value"
		v = nil
	}
	a.value = v
}

// noValue warns value-not-carried of the parameter a of the nested template
// of s, which the resource deploying it binds to no value, its Fn::If
// carrying the branch AWS::NoValue, and whose default the draft cannot read:
// CloudFormation then requires one.
func (r *reader) noValue(s *stack, a *argument) {
	decl, _ := objectOf(s.t.params.get(a.decl.Name))
	why := "its template " + s.t.file + " declares no default for it, while CloudFormation requires a value, so it " +
		"is not carried: bind it to a value, or declare a default for it in " + s.t.file
	if def := decl.get("Default"); def != nil && !isNull(def) {
		why = "the default its template " + s.t.file + " declares for it is NoEcho, which the draft never writes, so " +
			"it is not carried: pass it through a NoEcho parameter of " + a.file
	}
	r.warnAt(cloudimport.CodeValueNotCarried, a.file, a.at, "the template "+a.file+" binds the parameter "+a.decl.Name+
		" of the "+s.kind()+" "+s.at+" at "+a.at+" to no value, as the branch of Fn::If it carries is AWS::NoValue, "+
		"and "+why)
	a.why = "which its stack binds to no value"
}

// sameType reports whether the parameter decl of a nested template reads
// the graph's parameter p whole: always, unless decl reads the Systems
// Manager parameter its value names and p is of another type.
func sameType(decl, p *cloudimport.Parameter) bool {
	return p != nil && (!isSSMValue(decl.Type) || p.Type == decl.Type)
}

// isReference reports whether v reads a value the template does not write:
// a parameter, a resource's attribute, a credential or a secret a store
// holds.
func isReference(v cloudimport.Value) bool {
	switch v.(type) {
	case cloudimport.Param, cloudimport.Ref, cloudimport.Credential, cloudimport.Stored:
		return true
	}
	return false
}

// ssmValue returns the value v the parameter a of the nested template of s
// is given as what a Ref to it reads: for a parameter reading the Systems
// Manager parameter its value names, the dynamic reference resolving the one
// literal name v writes, not carried where v writes none; v itself for any
// other.
func (r *reader) ssmValue(s *stack, a *argument, v cloudimport.Value) cloudimport.Value {
	if !isSSMValue(a.decl.Type) || v == nil {
		return v
	}
	lit, _ := v.(cloudimport.Literal)
	name, isText := schemadraft.JSONText(lit.JSON)
	text := "{{resolve:ssm:" + name + "}}"
	if isText && !isList(strings.TrimSuffix(strings.TrimPrefix(a.decl.Type, ssmValuePrefix), ">")) &&
		cfntemplate.IsDynamicReference(text) && writable(text) {
		return cloudimport.Opaque{Text: text, Target: target}
	}
	r.warnAt(cloudimport.CodeValueNotCarried, a.file, a.at, "the parameter "+a.decl.Name+" of the "+s.kind()+" "+
		s.at+" reads the Systems Manager parameter its value names, and the value it is given is no literal name of "+
		"one parameter, so it is not carried: give it the name of one Systems Manager parameter")
	a.why = "whose value names no one Systems Manager parameter"
	return nil
}

// listed returns v, the value a Ref to the parameter decl reads, as the
// property it is: the list of the comma-separated members of the literal text
// of a list parameter, each with the space around it trimmed, as
// CloudFormation reads a list parameter; v itself otherwise.
func listed(decl *cloudimport.Parameter, v cloudimport.Value) cloudimport.Value {
	lit, ok := v.(cloudimport.Literal)
	text, isText := schemadraft.JSONText(lit.JSON)
	if !ok || !isText || !isList(decl.Type) || isSSMValue(decl.Type) {
		return v
	}
	var items []json.RawMessage
	for _, item := range strings.Split(text, ",") {
		items = append(items, importer.String(strings.TrimSpace(item)))
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return v
	}
	return literal(raw)
}

// listedLiteral returns raw, a default or a supplied value of the parameter
// p, as the leaf the graph holds: a list parameter's the list of its members,
// as [listed] reads it, and any other's as written.
func listedLiteral(p *cloudimport.Parameter, raw json.RawMessage) cloudimport.Literal {
	lit, _ := listed(p, literal(raw)).(cloudimport.Literal)
	return lit
}

// reference returns what the template of the lowerer reads, at the JSON
// pointer at, of attr of id, a name it declares no parameter of: of a
// resource read as its template's resources, the value of the output
// Outputs.<name> reads, lowered in that template, not carried where it
// declares none, and false for anything else; of a resource deploying a
// template the import does not read, false for an output; of any other, a
// Ref to the node the name is. False is read as deploy-time text.
func (l *lowerer) reference(id string, attr []string, at string) (cloudimport.Value, bool) {
	if child := l.s.nested[id]; child != nil {
		if len(attr) != 2 || attr[0] != "Outputs" {
			return nil, false
		}
		v, found := l.r.outputOf(child, attr[1])
		if !found {
			l.warn(cloudimport.CodeValueNotCarried, at, "the template "+l.t.file+" reads at "+at+" the output "+
				attr[1]+" of the "+child.kind()+" "+id+", which its template "+child.t.file+" does not declare, so it is "+
				"not carried: read an output "+child.t.file+" declares")
		}
		return v, true
	}
	if res, _ := objectOf(l.t.resources.get(id)); len(attr) > 0 && attr[0] == "Outputs" {
		if _, _, deploys := linkOf(res); deploys {
			return nil, false
		}
	}
	switch node, ok := l.s.ids[id]; {
	case ok:
		return cloudimport.Ref{Node: node, Attr: attr}, true
	case l.s.up != nil:
		return cloudimport.Ref{Node: l.s.prefix + id, Attr: attr}, true
	}
	return cloudimport.Ref{Node: id, Attr: attr}, true
}

// outputOf returns, read once, the value of the output name of the nested
// template of s, lowered in it; false where the template declares no output
// of that name. An output reading itself through the parameters and outputs
// of nested stacks, and one larger than cloudimport.MaxValueSize, are not
// carried, and are reported value-not-carried.
func (r *reader) outputOf(s *stack, name string) (cloudimport.Value, bool) {
	o := s.outputs[name]
	if o == nil {
		return nil, false
	}
	at := jsonPointer("Outputs", name, "Value")
	switch o.state {
	case settled:
		return o.value, true
	case inProgress:
		r.warnAt(cloudimport.CodeValueNotCarried, s.t.file, at, "the output "+name+" of "+s.t.file+", read for the "+
			s.kind()+" "+s.at+", reads itself through the parameters and outputs of nested stacks, so it is not "+
			"carried: bind each parameter to a value that does not read its own stack's outputs")
		return nil, true
	}
	o.state = inProgress
	v := r.scope(s).output(o.o)
	if v != nil && cloudimport.Size(v) > cloudimport.MaxValueSize {
		r.warnAt(cloudimport.CodeValueNotCarried, s.t.file, at, "the output "+name+" of "+s.t.file+", read for the "+
			s.kind()+" "+s.at+", is "+cloudimport.TooLarge())
		v = nil
	}
	o.value, o.state = v, settled
	return v, true
}

// stackCondition returns, lowered once where the resource deploying s is
// written, the condition that resource is deployed under; nil where it
// declares none or the draft does not carry it.
func (r *reader) stackCondition(s *stack) cloudimport.Value {
	if !s.condRead {
		s.condRead = true
		r.scope(s.up).condition(s.node, s.at)
		s.cond = s.node.Condition
	}
	return s.cond
}

// inherit gives the node h, whose own condition the lowerer of its template
// lowered, the condition each resource deploying its template is deployed
// under: the one parameter they and its own switch it on by, where they
// switch it on by one; where they switch it on by several, which an instance
// cannot carry together, none, with a condition-not-carried warning.
func (r *reader) inherit(h home) {
	var params []string
	add := func(v cloudimport.Value) {
		if p, ok := v.(cloudimport.Param); ok && !slices.Contains(params, p.Name) {
			params = append(params, p.Name)
		}
	}
	add(h.n.Condition)
	for s := h.s; s.up != nil; s = s.up {
		add(r.stackCondition(s))
	}
	switch len(params) {
	case 0:
	case 1:
		h.n.Condition = cloudimport.Param{Name: params[0]}
	default:
		h.n.Condition = nil
		r.warnAt(cloudimport.CodeConditionNotCarried, h.s.t.file, jsonPointer("Resources", h.id), "the template "+
			h.s.t.file+" deploys "+h.id+", and the nested stacks deploying it, under conditions switched by the "+
			"template parameters "+strings.Join(params, ", ")+", which the draft cannot carry together, as one "+
			"parameter switches an instance on, so the resource is drafted as deployed whatever the conditions "+
			"decide: write active on its instance by hand, switching off with it every instance whose connection "+
			"reaches it, as a switched-off target halts the build")
	}
}

// isNode reports whether the graph holds a node of the ID id.
func (r *reader) isNode(id string) bool {
	return r.nodeIDs[id]
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

const (
	// formatName is the arm format's name, as the import request and the
	// parse-failed diagnostic name it.
	formatName = "arm"
	// syntax is the syntactic format a source is parsed as: JSON with
	// comments, which reads plain JSON too.
	syntax = "jsonc"
	// target is the target the format's own Recipes deploy on, the target an
	// expression of a template is carried for as written.
	target = "arm"
	// deploymentsType is the type of a resource that deploys another template,
	// written inline or linked; matched regardless of case.
	deploymentsType = "Microsoft.Resources/deployments"
	// maxNesting bounds how deep the reader follows child resources and the
	// templates resources hold, so a hostile source cannot exhaust the stack.
	maxNesting = 64
	// maxCopies is the most copies Azure Resource Manager deploys of one
	// resource or property; a literal count above it is read as a count the
	// template computes, and drafted once.
	maxCopies = 800
)

// Read reads the Azure Resource Manager templates a request carries into the
// core's graph, with what it reports of them. It is the arm format's reader.
//
// Each source is parsed as JSON with comments, so a template written with
// comments, or opening with a byte-order mark, is read. A source the parser
// refuses halts parse-failed where the import was handed it alone or its text
// names the template schema; any other is a file of the directory that is no
// template, named among the sources not read. A template is a source whose $schema begins
// with the Azure Resource Manager schema prefix and that holds resources; a
// parameters file has the prefix, a parameters object and no resources; every
// other source is named among those not read. The root template is chosen by
// the core's rule over the templates and the templateLink.relativePath links
// between them, and the root and every template it links are read; a request
// holding no template halts source-refused. A template the import reads is
// refused, halting source-refused and naming why, where its $schema is the
// tenant template schema, its languageVersion is neither absent, 1.0 nor 2.0,
// or its resources are not an array (1.0) or an object (2.0).
//
// The root template's resources become the graph's nodes, ordered by ID:
// under language version 2.0 each is keyed by its symbolic name; under 1.0
// each by a symbol derived from its name — the literal text its name ends
// with, else the last segment of its type — keyed as the importer kit keys a
// node, and a child resource nested in its parent's resources is read as a
// resource of its own, with its full type and name. Every string Azure
// Resource Manager reads as an expression is read with armexpr and lowered
// into the core's values; a reference naming a resource of the template names
// its node, and one naming none names a resource the template reads and does
// not create, keyed by the reference printed canonically. A string that reads
// as an expression and does not parse is not carried, with a value-not-carried
// warning; it never halts. The parameters the root template declares are the
// graph's parameters, filled from the one parameters file the request
// carries.
//
// A Microsoft.Resources/deployments resource holding its template inline, or
// linking by a relative path a template the import holds, is read as the
// resources that template deploys: they join the graph, each symbol behind the
// symbol of the resource deploying them and a slash, reading the values that
// resource gives the template's parameters — or, evaluated in the outer
// scope, the holding template's parameters and variables — and a reference to
// one of the template's outputs reads the value the output holds. A
// template deployed into another resource group or subscription is read the
// same way, with a deployment-scope-merged warning. One deploying an Azure
// Verified Modules resource module — a template Bicep generated, holding the
// resource by which the module sends its telemetry under a name opening
// 46d3xbcp.res. — is one node of the module's resource type instead, in the
// module form, its body the parameters given, where the module index lists
// its telemetry prefix or a row of the table maps that type; it is reported
// avm-module-recognised, and its lock, role assignments, diagnostic settings
// and private endpoints are left out and reported resource-not-carried unless
// the Recipe's override header lists them. Any other module — a pattern or a
// utility module, or a resource module kept no one node — is read as its
// resources. The resource by which a module sends its telemetry is never a
// node, whatever template holds it, and enableTelemetry is never carried.
func Read(in aggrega.ImportInput, s cloudimport.Settings, parse cloudimport.ParseFunc) (*cloudimport.Graph,
	[]aggrega.Diagnostic) {
	return read(in, s, parse, Table())
}

// read reads as [Read] does, a module of no path the module index lists
// staying one node where a row of table maps its resource type.
func read(in aggrega.ImportInput, s cloudimport.Settings, parse cloudimport.ParseFunc, table cloudimport.Table) (
	*cloudimport.Graph, []aggrega.Diagnostic) {
	r := &reader{in: in, parse: parse, table: table, yields: map[string][]string{}}
	srcs, unreadable := r.sources()
	if r.halted {
		return nil, r.diags
	}
	sel := r.selectSources(srcs, unreadable, s)
	if r.halted {
		return nil, r.diags
	}
	r.sel = sel
	for _, name := range sel.read {
		if h := sel.templates[path.Clean(name)]; h != nil {
			if why := refusal(h.top); why != "" {
				r.halt(cloudimport.Report(cloudimport.CodeSourceRefused, "the template "+h.name+" is refused: "+why))
			}
		}
	}
	if r.halted {
		return nil, r.diags
	}

	t := r.template(sel.root.name, "", sel.root.top, nil)
	r.root = t
	r.recogniseRoot(t, sel.root.top)
	r.defaults = r.quiet().parameters(t)
	r.plan(t)
	if r.halted {
		return nil, r.diags
	}
	r.alias()
	r.yield(t)
	g := &cloudimport.Graph{Format: formatName, Read: sel.read, NotRead: sel.notRead}
	g.Nodes = r.build(t)
	sort.SliceStable(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	g.Params = r.parameters(t)
	if sel.params != nil {
		r.fill(g.Params, sel.params)
	}
	g.Outputs = r.outputs(t)
	if r.halted {
		return nil, r.diags
	}
	return g, r.diags
}

// reader is one read of a request's templates.
type reader struct {
	in    aggrega.ImportInput
	parse cloudimport.ParseFunc
	sel   *selection
	// table is the mapping table whose rows tell whether a module of no path
	// the module index lists stays one node.
	table cloudimport.Table
	// root is the root template, and defaults its parameters as it declares
	// them, read for the table's rows.
	root     *template
	defaults []cloudimport.Parameter
	// families are the groups of templates deployed into one scope, in the
	// order they are read.
	families []*family
	// yields holds, for each Microsoft.Resources/deployments resource whose
	// template is flattened, the IDs of the nodes that template yields.
	yields map[string][]string
	// resolving names the resources whose template's outputs are being read,
	// so an output reading itself ends.
	resolving []string
	// nestedRead counts the resources of the nested templates read.
	nestedRead int
	diags      []aggrega.Diagnostic
	// halted reports a halting diagnostic among diags.
	halted bool
}

// quiet returns a reader of the same request whose diagnostics are
// discarded: what it reads is read again, and reported, by r.
func (r *reader) quiet() *reader {
	return &reader{in: r.in, parse: r.parse, sel: r.sel, table: r.table, root: r.root, defaults: r.defaults,
		yields: r.yields}
}

// halt records a diagnostic that halts the import.
func (r *reader) halt(d aggrega.Diagnostic) {
	r.diags = append(r.diags, d)
	r.halted = true
}

// note records a diagnostic, once: the same diagnostic raised again, as a
// variable read at several places raises it, is recorded where it is first
// raised.
func (r *reader) note(d aggrega.Diagnostic) {
	for _, have := range r.diags {
		if have == d {
			return
		}
	}
	r.diags = append(r.diags, d)
}

// refusal says why the import does not read the template whose top-level
// object is top, empty where it reads it.
func refusal(top obj) string {
	if why := refusedVersion(top); why != "" {
		return why
	}
	v2 := isTwoZero(top)
	resources := top.exact("resources")
	switch {
	case v2 && jsonKind(resources) != '{':
		return "its languageVersion is 2.0 and its resources are not an object keyed by symbolic name, as that " +
			"language version writes them"
	case !v2 && jsonKind(resources) != '[':
		return "its resources are not an array, as a template of language version 1.0 writes them; a template " +
			"keying its resources by symbolic name sets its languageVersion to 2.0"
	}
	return ""
}

// refusedVersion says why the import does not read the template whose
// top-level object is top, by its schema and its language version alone;
// empty where they are read.
func refusedVersion(top obj) string {
	schema, _ := text(top.exact("$schema"))
	if isTenantSchema(schema) {
		return "its $schema is the tenant template schema, and the import drafts the resources a template " +
			"deploys into a resource group, a subscription or a management group; import a template of one of " +
			"those scopes"
	}
	if raw := top.exact("languageVersion"); raw != nil && !isNull(raw) {
		version, ok := text(raw)
		if !ok || (version != "1.0" && version != "2.0") {
			return "its languageVersion is neither 1.0 nor 2.0, the language versions the import reads; an " +
				"experimental language version may change at any time, so build the template without the " +
				"experimental features that set it"
		}
	}
	return ""
}

// isTwoZero reports whether the template whose top-level object is top is of
// language version 2.0.
func isTwoZero(top obj) bool {
	version, _ := text(top.exact("languageVersion"))
	return version == "2.0"
}

// isTenantSchema reports whether schema is the template schema of the tenant
// scope.
func isTenantSchema(schema string) bool {
	file := schema
	if i := strings.LastIndex(file, "/"); i >= 0 {
		file = file[i+1:]
	}
	return strings.HasPrefix(strings.ToLower(file), "tenantdeploymenttemplate.")
}

// template is one template the import reads, with its sections.
type template struct {
	// file is the name of the source holding the template, and at the JSON
	// pointer of the template inside it: empty for the source's own template.
	file, at string
	// v2 reports language version 2.0: resources keyed by symbolic name.
	v2 bool
	// params, vars and defs are the template's parameters, variables and type
	// definitions; outputs its outputs.
	params, vars, defs, outputs obj
	// loopVars names the variables a variable copy loop computes.
	loopVars []string
	// resources are the template's resources, normalised, in the order the
	// template writes them: by symbolic name under 2.0, by position under 1.0,
	// a child after the resource it is nested in.
	resources []*resource
	// ambiguous holds, for each reference matched since the lowerer last
	// settled, that several resources matched, their symbols in the order the
	// template writes them.
	ambiguous [][]string
	// up is the template holding the Microsoft.Resources/deployments resource
	// dep that deploys this one, both nil for the root; frames are the
	// resources deploying it, outermost first, each with the template holding
	// it.
	up     *template
	dep    *resource
	frames []frame
	// outer reports that the template's expressions read the parameters and
	// variables of the template holding it, as a nested template evaluated in
	// the outer scope does.
	outer bool
	// prefix opens the symbol of each of the template's resources: the symbol
	// of dep and a slash, empty for the root.
	prefix string
	// family is the group of templates deployed into one scope the template
	// belongs to, nil for a module's template read for its name alone.
	family *family
	// module is the Azure Verified Modules module the template is, nil where
	// it is none.
	module *recognised
}

// resource is one resource of a template, normalised.
type resource struct {
	// symbol is the resource's key in the graph: its local symbol behind its
	// template's prefix, or, for an existing resource another template of its
	// scope declares, that resource's symbol.
	symbol string
	// local is the resource's symbol in its own template: its symbolic name
	// under 2.0, a symbol derived from its name under 1.0.
	local string
	// typ is the resource's full type and apiVersion the API version it is
	// written in.
	typ, apiVersion string
	// names are the name the template writes for the resource, the names of
	// the resources it is nested in first: a child nested in its parent's
	// resources writes its own name alone, and its full name joins them.
	names []json.RawMessage
	// nameAt are the JSON pointers of names, aligned with them.
	nameAt []string
	// at is the JSON pointer of the resource in its source.
	at string
	// body is the resource below the keys the reader consumes.
	body obj
	// dependsOn, condition, copy and scope are the members of those names, as
	// written; subscription and group the subscriptionId and resourceGroup
	// the resource is declared in.
	dependsOn, condition, copy, scope json.RawMessage
	subscription, group               json.RawMessage
	// existing reports that the template reads the resource and does not
	// create it.
	existing bool
	// parent is the local symbol of the resource the template nests it in, or
	// names as its parent; empty where only its type and name can say.
	parent string
	// id is the resource's identity, as a reference matches it.
	id identity
	// nested is the template a Microsoft.Resources/deployments resource
	// deploys, read as resources of the graph; module the Azure Verified
	// Modules module it deploys as one node. Each is nil where the resource
	// is neither.
	nested *template
	module *avmModule
	// telemetry reports the resource by which a module sends its telemetry,
	// which is no node; aliased an existing resource read as the resource
	// another template of its scope declares, which is no node of its own.
	telemetry, aliased bool
}

// consumed are the members of a resource the reader reads into its node's
// fields rather than its body, matched regardless of case.
var consumed = []string{"type", "apiVersion", "name", "condition", "copy", "dependsOn", "existing", "scope",
	"parent", "resources", "comments", "subscriptionId", "resourceGroup"}

// template reads the template whose top-level object is top, held in file at
// the JSON pointer at, normalising its resources. nest says how a nested
// template is deployed, nil for the root.
func (r *reader) template(file, at string, top obj, nest *nesting) *template {
	t := header(file, at, top)
	if nest != nil {
		t.nestUnder(nest)
	} else {
		t.family = &family{}
		r.families = append(r.families, t.family)
	}
	t.family.members = append(t.family.members, t)

	resources := top.exact("resources")
	used := map[string]bool{}
	if t.v2 {
		entries, _ := objectOf(resources)
		for _, e := range entries {
			used[e.Key] = true
		}
		for _, e := range entries {
			r.resource(t, e.Value, at+importer.Pointer("resources", e.Key), e.Key, nil, used, 0)
		}
	} else {
		items, _ := arrayOf(resources)
		for i, raw := range items {
			r.resource(t, raw, at+importer.Pointer("resources", strconv.Itoa(i)), "", nil, used, 0)
		}
	}
	t.identify()
	return t
}

// header returns the template whose top-level object is top, held in file at
// the JSON pointer at, with its parameters, variables, type definitions and
// outputs and none of its resources.
func header(file, at string, top obj) *template {
	t := &template{file: file, at: at, v2: isTwoZero(top)}
	t.params, _ = objectOf(top.exact("parameters"))
	t.defs, _ = objectOf(top.exact("definitions"))
	t.outputs, _ = objectOf(top.exact("outputs"))
	vars, _ := objectOf(top.exact("variables"))
	for _, e := range vars {
		if e.Key != "copy" {
			t.vars = append(t.vars, e)
			continue
		}
		loops, _ := arrayOf(e.Value)
		for _, loop := range loops {
			l, _ := objectOf(loop)
			if name, ok := text(l.get("name")); ok {
				t.loopVars = append(t.loopVars, name)
			}
		}
	}
	return t
}

// resource reads the resource raw, written at the JSON pointer at, into t,
// followed by each child nested in its resources. symbol is its symbolic name
// under 2.0, empty where one is derived; parent is the resource it is nested
// in, nil for a resource of the template's own resources. A resource that is
// not an object, or whose type is not literal text, is not carried, with a
// resource-not-carried warning naming where it is written.
func (r *reader) resource(t *template, raw json.RawMessage, at, symbol string, parent *resource,
	used map[string]bool, depth int) {
	o, ok := objectOf(raw)
	typ, typed := text(o.get("type"))
	if !ok || !typed || typ == "" || armexpr.IsExpression(typ) {
		d := cloudimport.Report(cloudimport.CodeResourceNotCarried, "the template "+t.file+" writes a resource "+
			"at "+at+" whose type is not written as literal text, so it is not drafted: write its type as "+
			"literal text")
		d.SourceRef = t.file + "#" + at
		r.note(d)
		return
	}
	res := &resource{typ: typ, at: at}
	res.apiVersion, _ = text(o.get("apiVersion"))
	res.names = []json.RawMessage{o.get("name")}
	res.nameAt = []string{at + importer.Pointer("name")}
	if parent != nil {
		res.names = append(append([]json.RawMessage(nil), parent.names...), o.get("name"))
		res.nameAt = append(append([]string(nil), parent.nameAt...), at+importer.Pointer("name"))
		res.parent = parent.local
		if !strings.HasPrefix(strings.ToLower(typ), strings.ToLower(parent.typ)+"/") {
			res.typ = parent.typ + "/" + typ
		}
	}
	res.dependsOn, res.condition, res.copy = o.get("dependsOn"), o.get("condition"), o.get("copy")
	res.scope, res.subscription, res.group = o.get("scope"), o.get("subscriptionId"), o.get("resourceGroup")
	res.existing = string(o.get("existing")) == "true"
	if name, ok := text(o.get("parent")); ok && t.v2 {
		res.parent = name
	} else if i := strings.LastIndex(symbol, "::"); i > 0 && t.v2 && res.parent == "" {
		res.parent = symbol[:i]
	}
	for _, e := range o {
		if !isConsumed(e.Key) {
			res.body = append(res.body, e)
		}
	}
	res.local = symbol
	if res.local == "" {
		res.local = importer.UniqueKey(used, t.derivedSymbol(res))
	}
	res.symbol = t.prefix + res.local
	t.resources = append(t.resources, res)

	nested := o.get("resources")
	if nested == nil || isNull(nested) {
		return
	}
	childrenAt := at + importer.Pointer("resources")
	children, listed := arrayOf(nested)
	switch {
	case !listed:
		r.childrenNotCarried(t, res, childrenAt, "the child resources are not written as an array")
	case depth >= maxNesting && len(children) > 0:
		r.childrenNotCarried(t, res, childrenAt, fmt.Sprintf("they are nested deeper than the %d levels the "+
			"import follows", maxNesting))
	default:
		for i, child := range children {
			r.resource(t, child, childrenAt+importer.Pointer(strconv.Itoa(i)), "", res, used, depth+1)
		}
	}
}

// childrenNotCarried warns that no child resource of res, written at the JSON
// pointer at, is drafted, and why.
func (r *reader) childrenNotCarried(t *template, res *resource, at, why string) {
	d := cloudimport.Report(cloudimport.CodeResourceNotCarried, "the template "+t.file+" writes the child "+
		"resources of "+res.symbol+" at "+at+", and "+why+", so none of them is drafted: write each child beside "+
		"its parent, with its full type and name")
	d.SourceRef = t.file + "#" + at
	r.note(d)
}

// isConsumed reports whether a resource's member named key is read into its
// node's fields.
func isConsumed(key string) bool {
	for _, c := range consumed {
		if strings.EqualFold(c, key) {
			return true
		}
	}
	return false
}

// derivedSymbol is the symbol a resource of a 1.0 template is keyed by before
// it is made unique: the literal text its name ends with, else the last
// segment of its type.
func (t *template) derivedSymbol(res *resource) string {
	segs := t.nameSegments(res.names)
	if len(segs) > 0 {
		last := segs[len(segs)-1]
		if len(last) > 0 {
			if s, ok := last[len(last)-1].(armexpr.String); ok && importer.Slug(s.Value) != "" {
				return s.Value
			}
		}
	}
	return res.typ[strings.LastIndex(res.typ, "/")+1:]
}

// node returns the graph node of res in t.
func (r *reader) node(t *template, res *resource) *cloudimport.Node {
	n := r.lowererAt(t, res, -1, 0).build()
	n.Repeat = r.repetition(t, res)
	r.linkedSource(t, res)
	return n
}

// build returns the node of the lowerer's resource, lowered at the copy
// indexes the lowerer and the lowerers it reads through hold. A node of a
// flattened template takes, beside its own, the condition, the dependencies
// and, where it states none of its own, the scope of each
// Microsoft.Resources/deployments resource deploying that template; a
// dependency on a resource whose template is flattened is a dependency on
// each node that template yields.
func (l *lowerer) build() *cloudimport.Node {
	res := l.res
	n := &cloudimport.Node{ID: res.symbol, Type: res.typ, APIVersion: res.apiVersion, Form: cloudimport.FormRaw,
		Existing: res.existing, Source: l.t.file + "#" + res.at}
	if res.module != nil {
		l.moduleNode(n)
	} else {
		n.Name = l.name()
		n.Body = l.body()
	}
	n.Parent = l.t.parentOf(res)
	n.Scope = l.scopeText()
	l.settle(res.at)
	n.Condition = l.conditions()
	n.DependsOn = l.dependencies()
	for v := l.via; v != nil; v = v.via {
		if n.Scope == "" {
			n.Scope = v.scopeText()
			v.settle(v.res.at)
		}
		n.DependsOn = append(n.DependsOn, v.dependencies()...)
	}
	n.DependsOn = l.r.flatDependencies(n.ID, n.DependsOn)
	return n
}

// repetition returns how res in t repeats: through its own copy loop, or
// through that of a Microsoft.Resources/deployments resource deploying t. One
// loop of a literal count repeats it once per index, the instance lowered
// with that loop's copyIndex() read as its index; a count the template
// computes when it deploys, or loops at more than one level, hold no keys.
func (r *reader) repetition(t *template, res *resource) *cloudimport.Repetition {
	level, loops := -1, 0
	for i, f := range t.frames {
		if f.res.copy != nil {
			level, loops = i, loops+1
		}
	}
	loopRes := res
	if res.copy != nil {
		level, loops = len(t.frames), loops+1
	} else if level >= 0 {
		loopRes = t.frames[level].res
	}
	switch {
	case loops == 0:
		return nil
	case loops > 1:
		return &cloudimport.Repetition{}
	}
	loop, _ := objectOf(loopRes.copy)
	count, ok := literalCount(loop.get("count"))
	if !ok {
		return &cloudimport.Repetition{}
	}
	keys := make([]string, count)
	for i := range keys {
		keys[i] = strconv.Itoa(i)
	}
	return &cloudimport.Repetition{Keys: keys, Instance: func(key string) *cloudimport.Node {
		index, err := strconv.Atoi(key)
		if err != nil {
			return nil
		}
		return r.lowererAt(t, res, level, index).build()
	}}
}

// literalCount returns the count a copy loop states literally, false where the
// template computes it when it deploys or it exceeds the copies Azure Resource
// Manager deploys.
func literalCount(raw json.RawMessage) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || n < 0 || n > maxCopies {
		return 0, false
	}
	return n, true
}

// linkedSource warns where res deploys a template linked from outside what
// the import holds: by URI, by template spec, or by a relative path leaving
// the imported directory or naming no file the import was handed.
func (r *reader) linkedSource(t *template, res *resource) {
	if why := r.unreadLink(t, res); why != "" {
		d := cloudimport.Report(cloudimport.CodeLinkedSourceNotRead, res.symbol+" is a "+deploymentsType+
			" resource of "+t.file+" whose template is linked by "+why+", which the import does not read: no "+
			"resource of that template is drafted, and each of its outputs the template reads is carried as text "+
			"evaluated when the template deploys; to draft its resources, place the linked template inside the "+
			"imported directory and link it by templateLink.relativePath")
		d.SourceRef = t.file + "#" + res.at
		r.note(d)
	}
}

// unreadLink says how res links a template the import does not read, empty
// where res links none or the import reads the one it links.
func (r *reader) unreadLink(t *template, res *resource) string {
	if !strings.EqualFold(res.typ, deploymentsType) {
		return ""
	}
	props, _ := objectOf(res.body.get("properties"))
	link, ok := objectOf(props.get("templateLink"))
	if !ok {
		return ""
	}
	if rel := link.get("relativePath"); rel != nil {
		path, literal := text(rel)
		switch {
		case !literal || armexpr.IsExpression(path):
			return "a relative path the template computes when it deploys"
		case strings.Contains(path, `\`):
			return "a relative path written with a backslash, which the import does not read as a path separator"
		case r.sel == nil || r.sel.linkedTemplate(t.file, path) == nil:
			target, inside := linked(t.file, path)
			switch {
			case !inside:
				return "a relative path leaving the imported directory"
			case r.sel != nil && r.sel.handed[target]:
				return "a relative path naming a file the import holds that is no template"
			}
			return "a relative path naming no file the import holds"
		}
		return ""
	}
	if link.get("id") != nil {
		return "its template spec ID"
	}
	return "its URI"
}

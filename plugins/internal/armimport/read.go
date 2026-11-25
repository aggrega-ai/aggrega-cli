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
func Read(in aggrega.ImportInput, s cloudimport.Settings, parse cloudimport.ParseFunc) (*cloudimport.Graph,
	[]aggrega.Diagnostic) {
	r := &reader{in: in, parse: parse}
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

	t := r.template(sel.root.name, "", sel.root.top)
	g := &cloudimport.Graph{Format: formatName, Read: sel.read, NotRead: sel.notRead}
	for _, res := range t.resources {
		g.Nodes = append(g.Nodes, r.node(t, res))
	}
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
	diags []aggrega.Diagnostic
	// halted reports a halting diagnostic among diags.
	halted bool
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
	schema, _ := text(top.exact("$schema"))
	if isTenantSchema(schema) {
		return "its $schema is the tenant template schema, and the import drafts the resources a template " +
			"deploys into a resource group, a subscription or a management group; import a template of one of " +
			"those scopes"
	}
	v2 := false
	if raw := top.exact("languageVersion"); raw != nil && !isNull(raw) {
		version, ok := text(raw)
		if !ok || (version != "1.0" && version != "2.0") {
			return "its languageVersion is neither 1.0 nor 2.0, the language versions the import reads; an " +
				"experimental language version may change at any time, so build the template without the " +
				"experimental features that set it"
		}
		v2 = version == "2.0"
	}
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
}

// resource is one resource of a template, normalised.
type resource struct {
	// symbol is the resource's key in the graph: its symbolic name under 2.0,
	// a symbol derived from its name under 1.0.
	symbol string
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
	// parent is the symbol of the resource the template nests it in, or names
	// as its parent; empty where only its type and name can say.
	parent string
	// id is the resource's identity, as a reference matches it.
	id identity
}

// consumed are the members of a resource the reader reads into its node's
// fields rather than its body, matched regardless of case.
var consumed = []string{"type", "apiVersion", "name", "condition", "copy", "dependsOn", "existing", "scope",
	"parent", "resources", "comments", "subscriptionId", "resourceGroup"}

// template reads the template whose top-level object is top, held in file at
// the JSON pointer at, normalising its resources.
func (r *reader) template(file, at string, top obj) *template {
	t := &template{file: file, at: at}
	version, _ := text(top.exact("languageVersion"))
	t.v2 = version == "2.0"
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
		res.parent = parent.symbol
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
	res.symbol = symbol
	if res.symbol == "" {
		res.symbol = importer.UniqueKey(used, t.derivedSymbol(res))
	}
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

// node returns the graph node of res.
func (r *reader) node(t *template, res *resource) *cloudimport.Node {
	l := r.lowerer(t, res)
	n := l.build()
	if res.copy != nil {
		n.Repeat = r.repetition(t, res, l)
	}
	r.linkedSource(t, res)
	return n
}

// build returns the node of the lowerer's resource, lowered at the copy
// index the lowerer holds.
func (l *lowerer) build() *cloudimport.Node {
	res := l.res
	n := &cloudimport.Node{ID: res.symbol, Type: res.typ, APIVersion: res.apiVersion, Form: cloudimport.FormRaw,
		Existing: res.existing, Source: l.t.file + "#" + res.at}
	n.Name = l.name()
	n.Body = l.body()
	n.Parent = l.t.parentOf(res)
	n.Scope = l.scopeText()
	l.settle(res.at)
	n.Condition = l.condition()
	n.DependsOn = l.dependencies()
	return n
}

// repetition returns how res repeats: one instance per index of a literal
// count, the instance lowered with copyIndex() read as its index; a count the
// template computes when it deploys holds no keys.
func (r *reader) repetition(t *template, res *resource, base *lowerer) *cloudimport.Repetition {
	loop, _ := objectOf(res.copy)
	name, _ := text(loop.get("name"))
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
		l := base.instance(name, index)
		return l.build()
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

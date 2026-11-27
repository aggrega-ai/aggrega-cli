// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

const (
	// telemetryPrefix opens the name of the Microsoft.Resources/deployments
	// resource by which an Azure Verified Modules module sends its telemetry.
	telemetryPrefix = "46d3xbcp."
	// AVMResourcePrefix opens the telemetry prefix of an Azure Verified
	// Modules resource module, as the module index lists it and as the name of
	// the resource by which the module sends its telemetry opens.
	AVMResourcePrefix = telemetryPrefix + "res."
	// telemetryParameter is the parameter by which a module's user turns the
	// module's telemetry on and off.
	telemetryParameter = "enableTelemetry"
)

// versionPattern matches a published module's version, as the replace call
// in its telemetry resource's name states it; the placeholder a module built
// from its source states instead does not match.
var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

// moduleKind is the kind of an Azure Verified Modules module, as its
// telemetry prefix names it.
type moduleKind int

// The kinds of a module.
const (
	// otherModule is a module whose prefix names none of the kinds below.
	otherModule moduleKind = iota
	// resourceModule deploys one resource, with what the module adds to it.
	resourceModule
	// patternModule deploys several resources and modules together.
	patternModule
	// utilityModule computes what other modules read.
	utilityModule
)

// recognised is what the import recognises of an Azure Verified Modules
// module from its template: its telemetry prefix, its version — empty where
// the template states none — and the name its metadata gives it.
type recognised struct {
	prefix, version, title string
}

// kind returns the kind of the module, as its prefix names it.
func (rec recognised) kind() moduleKind {
	switch {
	case strings.HasPrefix(rec.prefix, AVMResourcePrefix):
		return resourceModule
	case strings.HasPrefix(rec.prefix, telemetryPrefix+"ptn."):
		return patternModule
	case strings.HasPrefix(rec.prefix, telemetryPrefix+"utl."):
		return utilityModule
	}
	return otherModule
}

// describe names the module for a message: its kind; its path in the
// registry, where path is known, and its telemetry prefix; its version, or
// that it states none; and the name its metadata gives it.
func (rec recognised) describe(path string) string {
	var b strings.Builder
	b.WriteString("the Azure Verified Modules ")
	switch rec.kind() {
	case resourceModule:
		b.WriteString("resource module")
	case patternModule:
		b.WriteString("pattern module")
	case utilityModule:
		b.WriteString("utility module")
	default:
		b.WriteString("module")
	}
	if path != "" {
		fmt.Fprintf(&b, " %s, of telemetry prefix %s", path, rec.prefix)
	} else {
		fmt.Fprintf(&b, " of telemetry prefix %s, for which the module index the import holds lists no path",
			rec.prefix)
	}
	if rec.version != "" {
		fmt.Fprintf(&b, ", version %s", rec.version)
	} else {
		b.WriteString(", stating no version, as a module built from its source does")
	}
	if rec.title != "" {
		fmt.Fprintf(&b, ", named %s in its metadata", strconv.Quote(rec.title))
	}
	return b.String()
}

// recognise returns the Azure Verified Modules module whose template has the
// top-level object top: a template Bicep generated, its
// metadata._generator.name bicep, holding a Microsoft.Resources/deployments
// resource whose name is the one by which a module sends its telemetry, as
// [template.telemetryName] reads it. It is nil where top is no such module.
func recognise(top obj) *recognised {
	meta, _ := objectOf(top.exact("metadata"))
	generator, _ := objectOf(meta.exact("_generator"))
	if name, _ := text(generator.exact("name")); name != "bicep" {
		return nil
	}
	t := header("", "", top)
	for _, raw := range resourceValues(top.exact("resources")) {
		o, _ := objectOf(raw)
		if typ, _ := text(o.get("type")); !strings.EqualFold(typ, armcore.DeploymentsType) {
			continue
		}
		if prefix, version, ok := t.telemetryName(o.get("name")); ok {
			title, _ := text(meta.exact("name"))
			return &recognised{prefix: prefix, version: version, title: title}
		}
	}
	return nil
}

// telemetryName reads raw, the name of a Microsoft.Resources/deployments
// resource of t, as the name by which a module sends its telemetry: text
// whose first piece, read with t's variables written in place and a format
// call split into its pieces, opens telemetryPrefix. It returns the prefix,
// that piece without the dot closing it, and the version the literal first
// argument of the first replace call among the pieces states, empty where
// that argument is no version; false where raw is no such name.
func (t *template) telemetryName(raw json.RawMessage) (prefix, version string, ok bool) {
	n, ok := t.node(raw)
	if !ok {
		return "", "", false
	}
	pieces := t.pieces(n, 0)
	if len(pieces) == 0 {
		return "", "", false
	}
	first, isText := pieces[0].(armexpr.String)
	if !isText || !strings.HasPrefix(first.Value, telemetryPrefix) {
		return "", "", false
	}
	for _, p := range pieces[1:] {
		c, isCall := p.(armexpr.Call)
		if !isCall || c.Namespace != "" || !strings.EqualFold(c.Name, "replace") || len(c.Args) == 0 {
			continue
		}
		if v, isText := c.Args[0].(armexpr.String); isText && versionPattern.MatchString(v.Value) {
			version = v.Value
		}
		break
	}
	return strings.TrimSuffix(first.Value, "."), version, true
}

// isTelemetry reports whether res of t is the resource by which a module
// sends its telemetry.
func (t *template) isTelemetry(res *resource) bool {
	if !strings.EqualFold(res.typ, armcore.DeploymentsType) || len(res.names) == 0 {
		return false
	}
	_, _, ok := t.telemetryName(res.names[len(res.names)-1])
	return ok
}

// avmModule is an Azure Verified Modules resource module a
// Microsoft.Resources/deployments resource deploys as one node.
type avmModule struct {
	recognised
	// path is the module's path in the registry, where the module index lists
	// its prefix; typ is the type of the resource it deploys, and apiVersion
	// the API version its template writes that resource in.
	path, typ, apiVersion string
	// tmpl is the module's template, read without its resources and nested
	// under the resource deploying it; name is the name it writes for its
	// resource, at the JSON pointer nameAt, nil where it writes none.
	tmpl   *template
	name   json.RawMessage
	nameAt string
	// dropped are the parameters the resource deploying the module sets that
	// are left out of the node.
	dropped []string
}

// accessoryTypes are the types of the resources a resource module deploys
// beside the resource it is for.
var accessoryTypes = []string{"Microsoft.Authorization/locks", "Microsoft.Authorization/roleAssignments",
	"Microsoft.Insights/diagnosticSettings", armcore.DeploymentsType}

// ownResource is one resource a module's template creates of its own.
type ownResource struct {
	typ, apiVersion string
	name            json.RawMessage
	nameAt          string
}

// moduleResources returns the resources of its own the module template with
// the top-level object top, written at the JSON pointer at, creates, in the
// order it writes them: each written at its top, created rather than read as
// existing, of literal type, and of a type that is neither one of the
// accessoryTypes nor a child of another such resource's type.
func moduleResources(at string, top obj) []ownResource {
	var all []ownResource
	add := func(raw json.RawMessage, ptr string) {
		o, ok := objectOf(raw)
		typ, typed := text(o.get("type"))
		if !ok || !typed || typ == "" || armexpr.IsExpression(typ) || string(o.get("existing")) == "true" {
			return
		}
		for _, accessory := range accessoryTypes {
			if strings.EqualFold(typ, accessory) {
				return
			}
		}
		apiVersion, _ := text(o.get("apiVersion"))
		all = append(all, ownResource{typ: typ, apiVersion: apiVersion, name: o.get("name"),
			nameAt: ptr + importer.Pointer("name")})
	}
	resources := top.exact("resources")
	if entries, ok := objectOf(resources); ok {
		for _, e := range entries {
			add(e.Value, at+importer.Pointer("resources", e.Key))
		}
	} else if items, ok := arrayOf(resources); ok {
		for i, raw := range items {
			add(raw, at+importer.Pointer("resources", strconv.Itoa(i)))
		}
	}
	var out []ownResource
	for _, c := range all {
		child := slices.ContainsFunc(all, func(p ownResource) bool {
			return strings.HasPrefix(strings.ToLower(c.typ), strings.ToLower(p.typ)+"/")
		})
		if !child {
			out = append(out, c)
		}
	}
	return out
}

// oneNode decides whether res of t, deploying the Azure Verified Modules
// resource module rec — its template held in file at the JSON pointer at,
// with the top-level object top, evaluated in t's scope where outer is set —
// is one node, and says why not where it is not. The node's resource type is the one the module index gives rec's
// prefix; where the index lists no module of it, the type of the module's one
// resource of its own, as [moduleResources] reads them, and a module creating
// none or several is not one node. A module the index lists is one node; one
// it does not list is one node where a row of the table maps its resource
// type, and not otherwise. A module kept one node is reported
// avm-module-recognised; its parameters are as [reader.foldParameters] keeps
// them.
func (r *reader) oneNode(t *template, res *resource, file, at string, top obj, rec *recognised,
	outer bool) (bool, string) {
	m := &avmModule{recognised: *rec}
	if listed, ok := AVMModuleByPrefix(rec.prefix); ok {
		m.path, m.typ = listed.Module, listed.ResourceType()
	}
	own := moduleResources(at, top)
	if m.typ == "" {
		if len(own) != 1 {
			return false, fmt.Sprintf("the module index lists no module of its telemetry prefix, and the "+
				"module creates %s beside locks, role assignments, diagnostic settings and %s resources, so no "+
				"one resource type is its own", ownCount(len(own)), armcore.DeploymentsType)
		}
		m.typ = own[0].typ
	}
	for _, o := range own {
		if strings.EqualFold(o.typ, m.typ) {
			m.apiVersion, m.name, m.nameAt = o.apiVersion, o.name, o.nameAt
			break
		}
	}
	m.tmpl = header(file, at, top)
	m.tmpl.nestUnder(&nesting{up: t, dep: res, outer: outer})
	rows := r.rows(t, res, m)
	if m.path == "" && len(rows) == 0 {
		return false, "the module index lists no module of its telemetry prefix, so the import cannot name the " +
			"module to source, and no Recipe of the catalog maps " + m.typ
	}
	res.module = m
	d := cloudimport.Report(cloudimport.CodeAVMModuleRecognised, fmt.Sprintf("%s deploys %s: it is one node of "+
		"the resource type %s, the parameters it sets read as the module's, and the module's telemetry is not "+
		"carried, as whether a Recipe sends telemetry is the Recipe's to set", res.symbol, m.describe(m.path),
		m.typ))
	d.SourceRef = t.file + "#" + res.at
	r.note(d)
	m.dropped = r.foldParameters(t, res, m, rows)
	return true, ""
}

// ownCount says how many resources of its own a module creates.
func ownCount(n int) string {
	switch n {
	case 0:
		return "no resource of its own"
	case 1:
		return "one resource of its own"
	}
	return strconv.Itoa(n) + " resources of its own"
}

// rows returns the rows of the table the node of res, deploying m, may take:
// the row mapping it, else the rows a value set only when the template
// deploys chooses among; none where no row maps its resource type.
func (r *reader) rows(t *template, res *resource, m *avmModule) []cloudimport.Row {
	l := r.quiet().lowererAt(t, res, -1, 0)
	n := &cloudimport.Node{ID: res.symbol, Type: m.typ, Form: cloudimport.FormAVM, Body: l.moduleParameters(nil)}
	row, could, ok := r.table.Resolve(n, &cloudimport.Graph{Params: r.defaults})
	if ok {
		return []cloudimport.Row{row}
	}
	return could
}

// folded are the parameters by which an Azure Verified Modules resource
// module deploys, beside its resource, what a Recipe takes only where its
// override header lists the parameter, each with what it deploys.
var folded = []struct{ name, deploys string }{
	{"lock", "a management lock on its resource"},
	{"roleAssignments", "role assignments on its resource"},
	{"diagnosticSettings", "diagnostic settings sending its resource's logs and metrics"},
	{"privateEndpoints", "private endpoints reaching its resource"},
}

// foldParameters returns the parameters of folded that res sets and that no
// override header of rows lists, each left out of the node res deploys m as.
// Each by which the module deploys something is reported
// resource-not-carried, naming the parameter.
func (r *reader) foldParameters(t *template, res *resource, m *avmModule, rows []cloudimport.Row) []string {
	args, at := res.arguments()
	var dropped []string
	for _, f := range folded {
		key, raw := args.member(f.name)
		if raw == nil || listsOverride(rows, key) {
			continue
		}
		dropped = append(dropped, key)
		if setsNothing(f.name, raw) {
			continue
		}
		taken := "no Recipe it may map to lists it among the overrides it reads"
		if len(rows) == 0 {
			taken = "no Recipe of the catalog maps the module"
		}
		d := cloudimport.Report(cloudimport.CodeResourceNotCarried, fmt.Sprintf("%s sets the parameter %s of %s, "+
			"by which the module deploys %s; %s, so it is not carried: set it up beside the resource once the "+
			"Release has deployed it", res.symbol, key, m.describe(m.path), f.deploys, taken))
		d.SourceRef = t.file + "#" + at + importer.Pointer(key)
		r.note(d)
	}
	return dropped
}

// listsOverride reports whether the override header of one of rows, read in
// the module form, lists the parameter key.
func listsOverride(rows []cloudimport.Row, key string) bool {
	for _, row := range rows {
		for _, p := range row.For(cloudimport.FormAVM).Overrides {
			if len(p.From) > 0 && strings.EqualFold(p.From[0], key) {
				return true
			}
		}
	}
	return false
}

// setsNothing reports whether the member of properties.parameters raw gives
// the parameter name null, an empty object or an empty array, or, for the
// lock, a lock of the kind None, by which the module deploys no lock.
func setsNothing(name string, raw json.RawMessage) bool {
	o, _ := objectOf(raw)
	v := o.get("value")
	switch jsonKind(v) {
	case 'n':
		return true
	case '{':
		members, _ := objectOf(v)
		kind, _ := text(members.get("kind"))
		return len(members) == 0 || (name == "lock" && strings.EqualFold(kind, "None"))
	case '[':
		items, _ := arrayOf(v)
		return len(items) == 0
	}
	return false
}

// moduleNode writes into n the node of the lowerer's resource deploying an
// Azure Verified Modules resource module as one node: the module's resource
// type, the API version and the name the module's template writes for its
// resource — the name lowered there, its parameters read through the
// resource deploying it — the module it calls, and as its body the
// parameters the resource sets.
func (l *lowerer) moduleNode(n *cloudimport.Node) {
	m := l.res.module
	n.Type, n.APIVersion, n.Form = m.typ, m.apiVersion, cloudimport.FormAVM
	n.Module = &cloudimport.ModuleRef{Path: m.path, Version: m.version, Prefix: m.prefix}
	if m.name != nil {
		named := &resource{symbol: l.res.symbol, names: []json.RawMessage{m.name}, nameAt: []string{m.nameAt}}
		n.Name = (&lowerer{r: l.r, t: m.tmpl, res: named, via: l, quiet: true, spent: new(cloudimport.Budget)}).name()
	}
	n.Body = l.moduleParameters(m.dropped)
}

// moduleParameters returns the parameters the lowerer's resource sets under
// properties.parameters, in the order the parser writes them, each lowered as
// [lowerer.argument] reads it, but enableTelemetry and each of drop.
func (l *lowerer) moduleParameters(drop []string) []cloudimport.Prop {
	args, at := l.res.arguments()
	var out []cloudimport.Prop
	for _, e := range args {
		if strings.EqualFold(e.Key, telemetryParameter) || slices.Contains(drop, e.Key) {
			continue
		}
		p, _, _ := l.argument(e.Value, at+importer.Pointer(e.Key), nil)
		p.Name = e.Key
		out = append(out, p)
	}
	return out
}

// recogniseRoot reports the root template t, with the top-level object top,
// as the Azure Verified Modules module it is, where it is one: its resources
// are read as it writes them.
func (r *reader) recogniseRoot(t *template, top obj) {
	rec := recognise(top)
	if rec == nil {
		return
	}
	t.module = rec
	path := ""
	if listed, ok := AVMModuleByPrefix(rec.prefix); ok {
		path = listed.Module
	}
	d := cloudimport.Report(cloudimport.CodeAVMModuleRecognised, fmt.Sprintf("the template %s is %s: its "+
		"resources are read as it writes them, each on its own, and its telemetry is not carried, as whether a "+
		"Recipe sends telemetry is the Recipe's to set", t.file, rec.describe(path)))
	d.SourceRef = t.file
	r.note(d)
}

// recognisedFlattened reports res of t, deploying the Azure Verified Modules
// module rec, as a module read as the resources it deploys: a pattern or a
// utility module always, and a resource module where it is not one node,
// because of why.
func (r *reader) recognisedFlattened(t *template, res *resource, rec *recognised, why string) {
	path := ""
	if listed, ok := AVMModuleByPrefix(rec.prefix); ok {
		path = listed.Module
	}
	how := "it is read as the resources it deploys, each on its own"
	switch rec.kind() {
	case patternModule:
		how = "a pattern module is read as the resources and the modules it deploys, each on its own"
	case resourceModule:
		how = "it is read as the resources it deploys, each drafted on its own, as " + why
	}
	d := cloudimport.Report(cloudimport.CodeAVMModuleRecognised, fmt.Sprintf("%s deploys %s: %s; its telemetry "+
		"is not carried, as whether a Recipe sends telemetry is the Recipe's to set", res.symbol,
		rec.describe(path), how))
	d.SourceRef = t.file + "#" + res.at
	r.note(d)
}

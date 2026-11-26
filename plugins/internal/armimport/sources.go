// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"bytes"
	"encoding/json"
	"path"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// SchemaPrefix opens the $schema of every Azure Resource Manager template and
// parameters file.
const SchemaPrefix = "https://schema.management.azure.com/schemas/"

// sourceKind is what a source the import is handed holds.
type sourceKind int

// The kinds of a source.
const (
	// kindOther is a source that is neither a template nor a parameters file:
	// it is named among the sources not read.
	kindOther sourceKind = iota
	// kindTemplate is a template: a $schema of the prefix, and resources.
	kindTemplate
	// kindParameters is a parameters file: a $schema of the prefix, a
	// parameters object, and no resources.
	kindParameters
)

// handed is one source the import is handed, parsed.
type handed struct {
	// name is the source's slash-separated path inside the imported
	// directory, or the name of the file imported on its own.
	name string
	// top is the source's top-level object.
	top  obj
	kind sourceKind
}

// kindOf returns what a parsed source holds, with its top-level members.
func kindOf(tree json.RawMessage) (sourceKind, obj) {
	top, ok := objectOf(tree)
	if !ok {
		return kindOther, nil
	}
	schema, ok := text(top.exact("$schema"))
	if !ok || !strings.HasPrefix(schema, SchemaPrefix) {
		return kindOther, top
	}
	if top.exact("resources") != nil {
		return kindTemplate, top
	}
	if _, ok := objectOf(top.exact("parameters")); ok {
		return kindParameters, top
	}
	return kindOther, top
}

// sources parses each source the import is handed. A source the parser
// refuses halts parse-failed, naming the line and column where the parser
// names them, where the import was handed that source alone or the source
// names the template schema; any other source the parser refuses is named
// among the sources not read, as a file of the directory that is no template.
func (r *reader) sources() ([]*handed, []string) {
	all := importer.Sources(r.in)
	var out []*handed
	var unreadable []string
	for _, src := range all {
		named := importer.Named(r.in, src)
		tree, err := r.parse(syntax, src.Bytes)
		if err != nil {
			if len(all) == 1 || bytes.Contains(src.Bytes, []byte(SchemaPrefix)) {
				r.halt(importer.ParseFailed(named, formatName, err))
				continue
			}
			unreadable = append(unreadable, named.Name)
			continue
		}
		kind, top := kindOf(tree)
		out = append(out, &handed{name: named.Name, top: top, kind: kind})
	}
	return out, unreadable
}

// links returns the paths a template links by templateLink.relativePath, each
// relative to the template's own directory as written, in the order its
// Microsoft.Resources/deployments resources, and the templates they hold
// inline, are written.
func links(top obj) []string {
	var out []string
	var walk func(resources json.RawMessage, depth int)
	walk = func(resources json.RawMessage, depth int) {
		if depth > maxNesting {
			return
		}
		for _, raw := range resourceValues(resources) {
			res, _ := objectOf(raw)
			if typ, _ := text(res.get("type")); !strings.EqualFold(typ, armcore.DeploymentsType) {
				walk(res.get("resources"), depth+1)
				continue
			}
			props, _ := objectOf(res.get("properties"))
			link, _ := objectOf(props.get("templateLink"))
			if rel, ok := text(link.get("relativePath")); ok && rel != "" && !armexpr.IsExpression(rel) {
				out = append(out, rel)
			}
			nested, _ := objectOf(props.get("template"))
			walk(nested.get("resources"), depth+1)
		}
	}
	walk(top.exact("resources"), 0)
	return out
}

// resourceValues returns the resources a resources member holds: an array's
// items in order, or an object's members in key order.
func resourceValues(raw json.RawMessage) []json.RawMessage {
	if items, ok := arrayOf(raw); ok {
		return items
	}
	o, _ := objectOf(raw)
	out := make([]json.RawMessage, len(o))
	for i, e := range o {
		out[i] = e.Value
	}
	return out
}

// linked resolves a link written in the template at from to the path it names
// inside the imported directory, false where it leaves that directory or is
// written with a backslash.
func linked(from, link string) (string, bool) {
	if strings.HasPrefix(link, "/") || strings.Contains(link, `\`) {
		return "", false
	}
	target := path.Join(path.Dir(path.Clean(from)), link)
	if target == ".." || strings.HasPrefix(target, "../") {
		return "", false
	}
	return target, true
}

// selection is what an import reads of the sources it is handed.
type selection struct {
	// root is the template the import starts from, and templates each
	// template it reads by path: the root and every template the root links,
	// transitively, inside the imported directory.
	root      *handed
	templates map[string]*handed
	// params is the one parameters file read, nil where none is.
	params *handed
	// handed marks the path of each source the import is handed, whatever it
	// holds.
	handed map[string]bool
	// read and notRead name the sources read and those handed and not read,
	// in path order.
	read, notRead []string
}

// selectSources chooses what the import reads: the root template, through
// the core's rule over the templates and the links between them, every
// template it links, and the one parameters file handed. A request holding no
// template halts source-refused; several parameters files are none read, with
// a parameter-files-not-read warning naming them.
func (r *reader) selectSources(srcs []*handed, unreadable []string, s cloudimport.Settings) *selection {
	var templates, params []*handed
	notRead := slices.Clone(unreadable)
	for _, h := range srcs {
		switch h.kind {
		case kindTemplate:
			templates = append(templates, h)
		case kindParameters:
			params = append(params, h)
		default:
			notRead = append(notRead, h.name)
		}
	}
	if len(templates) == 0 {
		r.halt(cloudimport.Report(cloudimport.CodeSourceRefused, "the import holds no Azure Resource Manager "+
			"template: a template is a JSON file whose $schema begins "+SchemaPrefix+" and which holds resources; "+
			"import the template, or the directory holding it"))
		return nil
	}

	units := make([]cloudimport.Unit, len(templates))
	byPath := map[string]*handed{}
	for i, h := range templates {
		units[i] = cloudimport.Unit{Path: h.name, Links: links(h.top)}
		byPath[path.Clean(h.name)] = h
	}
	root, _, halt := cloudimport.SelectRoot(units, s.Root)
	if halt != nil {
		r.halt(*halt)
		return nil
	}

	sel := &selection{root: byPath[path.Clean(root.Path)], templates: map[string]*handed{}, handed: map[string]bool{}}
	for _, h := range srcs {
		sel.handed[path.Clean(h.name)] = true
	}
	for _, name := range unreadable {
		sel.handed[path.Clean(name)] = true
	}
	queue := []*handed{sel.root}
	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		if sel.templates[path.Clean(h.name)] != nil {
			continue
		}
		sel.templates[path.Clean(h.name)] = h
		sel.read = append(sel.read, h.name)
		for _, link := range links(h.top) {
			if target, inside := linked(h.name, link); inside && byPath[target] != nil {
				queue = append(queue, byPath[target])
			}
		}
	}
	for _, h := range templates {
		if sel.templates[path.Clean(h.name)] == nil {
			notRead = append(notRead, h.name)
		}
	}

	switch len(params) {
	case 0:
	case 1:
		sel.params = params[0]
		sel.read = append(sel.read, params[0].name)
	default:
		names := make([]string, len(params))
		for i, h := range params {
			names[i] = h.name
			notRead = append(notRead, h.name)
		}
		slices.Sort(names)
		r.note(cloudimport.Report(cloudimport.CodeParameterFilesNotRead, "the import holds several parameters "+
			"files, "+strings.Join(names, ", ")+", and reads none of them, as one import drafts one Release for "+
			"one environment: import the template with the one parameters file whose values the Release takes"))
	}
	slices.Sort(sel.read)
	slices.Sort(notRead)
	sel.notRead = notRead
	return sel
}

// linkedTemplate returns the template a templateLink.relativePath written in
// the template at from names, nil where the import holds none: the link leaves
// the imported directory, or names a file the import was not handed.
func (sel *selection) linkedTemplate(from, link string) *handed {
	target, inside := linked(from, link)
	if !inside {
		return nil
	}
	return sel.templates[target]
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"bytes"
	"encoding/json"
	"path"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfncore/cfntemplate"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// The resource types by which a template links another, each with the
// property naming the linked template's location.
const (
	stackType       = "AWS::CloudFormation::Stack"
	stackLink       = "TemplateURL"
	applicationType = "AWS::Serverless::Application"
	applicationLink = "Location"
)

// versionKey is the member declaring a template's format version.
const versionKey = "AWSTemplateFormatVersion"

// handed is one source the import is handed, classified.
type handed struct {
	// src is the source, named as a diagnostic names it: by its path inside
	// the imported directory, or by the name of the file imported on its own.
	src aggrega.ImportSource
	// top is the source's top-level object as the template reader writes it,
	// nil where the reader refuses the source or reads no object; refusal is
	// the reader's refusal, nil where it reads the source.
	top     obj
	refusal error
	// template reports a template: a source holding a format version written
	// as text, or Resources and a Transform, or one a template links.
	template bool
	// linked reports that a template links the source.
	linked bool
	// tree is the source as the host's JSON parser reads it, held where it is
	// a parameters file, and form the form the file writes its values in.
	tree json.RawMessage
	form paramsForm
}

// name is the source's name.
func (h *handed) name() string {
	return h.src.Name
}

// classify reads each source the import is handed with the template reader,
// and each the reader does not read as a template with the host's JSON parser,
// for a parameters file.
func (r *reader) classify() []*handed {
	var out []*handed
	for _, src := range importer.Sources(r.in) {
		h := &handed{src: importer.Named(r.in, src)}
		if tree, err := cfntemplate.Normalize(src.Bytes); err != nil {
			h.refusal = err
		} else {
			h.top, _ = objectOf(tree)
			h.template = holdsTemplate(h.top)
		}
		if !h.template && r.parse != nil {
			if tree, err := r.parse(paramsSyntax, src.Bytes); err == nil {
				if h.form = formOf(tree); h.form != formNone {
					h.tree = tree
				}
			}
		}
		out = append(out, h)
	}
	return out
}

// holdsTemplate reports whether the top-level object top is a template by
// what it holds: an AWSTemplateFormatVersion written as text, or a Resources
// object and a Transform.
func holdsTemplate(top obj) bool {
	if _, ok := schemadraft.JSONText(top.get(versionKey)); ok {
		return true
	}
	transform := top.get("Transform")
	return kind(top.get("Resources")) == '{' && transform != nil && !isNull(transform)
}

// links returns the local paths a template links: the TemplateURL of each
// nested stack and the Location of each serverless application, written as
// text, in the order of its resources, each as written, relative to the
// template's own directory. A URL, an application's ID and a value an
// intrinsic function computes link nothing the import holds.
func links(top obj) []string {
	resources, _ := objectOf(top.get("Resources"))
	var out []string
	for _, e := range resources {
		res, _ := objectOf(e.Value)
		props, _ := objectOf(res.get("Properties"))
		var raw json.RawMessage
		switch typ, _ := schemadraft.JSONText(res.get("Type")); typ {
		case stackType:
			raw = props.get(stackLink)
		case applicationType:
			raw = props.get(applicationLink)
		default:
			continue
		}
		if link, ok := schemadraft.JSONText(raw); ok && isLocal(link) {
			out = append(out, link)
		}
	}
	return out
}

// isLocal reports whether a template's location names a file by a relative
// path rather than by a URL.
func isLocal(link string) bool {
	return link != "" && !strings.Contains(link, "://") && !strings.HasPrefix(link, "/") &&
		!strings.Contains(link, `\`)
}

// linked resolves a local path written in the template at from to the path it
// names inside the imported directory, false where it leaves that directory.
func linked(from, link string) (string, bool) {
	if !isLocal(link) {
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
	// template it reads, in path order: the root and every template the root
	// links, transitively.
	root      *handed
	templates []*handed
	// params is the one parameters file read, nil where none is.
	params *handed
	// read and notRead name the sources read and those handed and not read,
	// in path order.
	read, notRead []string
	// notes say why each source the template reader refuses is not read, in
	// path order.
	notes []string
}

// selectSources chooses what the import reads of the sources srcs: the root
// template, through the core's rule over the templates and the local links
// between them, every template it links, transitively, and the one parameters
// file handed. A source a template links is a template too, whatever it
// holds. A source the template reader refuses halts parse-failed where the
// import was handed it alone, the root setting names it, its text holds
// AWSTemplateFormatVersion or a template links it; any other is named not
// read, with the reader's reason. A request holding no template halts
// source-refused; several parameters files are none read, with a
// parameter-files-not-read warning naming them.
func (r *reader) selectSources(srcs []*handed, s cloudimport.Settings) *selection {
	byPath := map[string]*handed{}
	for _, h := range srcs {
		byPath[path.Clean(h.name())] = h
	}
	var queue []*handed
	for _, h := range srcs {
		if h.template {
			queue = append(queue, h)
		}
	}
	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		for _, link := range links(h.top) {
			target, inside := linked(h.name(), link)
			t := byPath[target]
			if !inside || t == nil || t.linked {
				continue
			}
			t.linked = true
			if t.refusal == nil && !t.template {
				t.template = true
				queue = append(queue, t)
			}
		}
	}

	for _, h := range srcs {
		if h.refusal == nil || (h.form != formNone && !h.linked) {
			continue
		}
		named := s.Root != "" && path.Clean(s.Root) == path.Clean(h.name())
		if len(srcs) == 1 || named || h.linked || bytes.Contains(h.src.Bytes, []byte(versionKey)) {
			r.halt(importer.ParseFailed(h.src, formatName, h.refusal))
		}
	}
	if r.halted {
		return nil
	}

	var units []cloudimport.Unit
	for _, h := range srcs {
		if h.template {
			units = append(units, cloudimport.Unit{Path: h.name(), Links: links(h.top)})
		}
	}
	if len(units) == 0 {
		r.halt(cloudimport.Report(cloudimport.CodeSourceRefused, "the import holds no CloudFormation template: a "+
			"template is a YAML or JSON file holding an "+versionKey+" written as text, or holding Resources and a "+
			"Transform, or one that a template the import reads links by a local path; add "+versionKey+
			": \"2010-09-09\" to the template, or import the directory holding the template that links it"))
		return nil
	}
	root, _, halt := cloudimport.SelectRoot(units, s.Root)
	if halt != nil {
		r.halt(*halt)
		return nil
	}

	sel := &selection{root: byPath[path.Clean(root.Path)]}
	reads := map[*handed]bool{}
	queue = []*handed{sel.root}
	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		if reads[h] {
			continue
		}
		reads[h] = true
		for _, link := range links(h.top) {
			if target, inside := linked(h.name(), link); inside && byPath[target] != nil && byPath[target].template {
				queue = append(queue, byPath[target])
			}
		}
	}

	var params []*handed
	var refused []*handed
	for _, h := range srcs {
		switch {
		case reads[h]:
			sel.templates = append(sel.templates, h)
			sel.read = append(sel.read, h.name())
		case h.template:
			sel.notRead = append(sel.notRead, h.name())
		case h.form != formNone:
			params = append(params, h)
		case h.refusal != nil:
			refused = append(refused, h)
			sel.notRead = append(sel.notRead, h.name())
		default:
			sel.notRead = append(sel.notRead, h.name())
		}
	}
	slices.SortStableFunc(sel.templates, func(a, b *handed) int { return strings.Compare(a.name(), b.name()) })
	slices.SortStableFunc(refused, func(a, b *handed) int { return strings.Compare(a.name(), b.name()) })
	for _, h := range refused {
		sel.notes = append(sel.notes, draftyaml.Comment(h.name())+" is not read, as the template reader refuses it: "+
			draftyaml.Comment(h.refusal.Error())+".")
	}

	switch len(params) {
	case 0:
	case 1:
		sel.params = params[0]
		sel.read = append(sel.read, params[0].name())
	default:
		names := make([]string, len(params))
		for i, h := range params {
			names[i] = h.name()
		}
		slices.Sort(names)
		sel.notRead = append(sel.notRead, names...)
		r.note(cloudimport.Report(cloudimport.CodeParameterFilesNotRead, "the import holds several parameters "+
			"files, "+strings.Join(names, ", ")+", and reads none of them, as one import drafts one Release for "+
			"one environment: import the template with the one parameters file whose values the Release takes"))
	}
	slices.Sort(sel.read)
	slices.Sort(sel.notRead)
	return sel
}

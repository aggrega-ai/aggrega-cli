// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

const (
	// formatName is the cloudformation format's name, as the import request,
	// the graph and the parse-failed diagnostic name it.
	formatName = "cloudformation"
	// paramsSyntax is the syntactic format the host's parser reads a
	// parameters file as.
	paramsSyntax = "json"
)

// read reads the CloudFormation templates and the parameters file a request
// carries into the core's graph, with what it reports of them; [Read] lowers
// the values it reads.
//
// Each source is read with the template reader, cfntemplate.Normalize, which
// reads YAML with short forms and JSON as CloudFormation reads them, into the
// long form; parse, the host's parser, is never asked for a template, and
// reads as JSON only the sources the template reader does not read as one, for
// a parameters file. A template is a source holding an
// AWSTemplateFormatVersion written as text, or a Resources object and a
// Transform, or one a template links by a local path, or one holding a
// Resources object the root setting names; a parameters file is one of the
// forms the AWS CLI reads parameters from a file in — a list of ParameterKey
// and ParameterValue, a list of Key=Value strings one of whose keys the root
// template declares, or an object holding a Parameters object and no
// Resources; every other source is named among those not read. The root
// template is chosen by the core's rule over the templates and the local links
// between them — the TemplateURL of a nested stack and the Location of a
// serverless application, written as a path inside the imported directory —
// and the root and every template it links are read. A source the template
// reader refuses halts parse-failed where the import was handed it alone, the
// root setting names it or a template the import reads links it, and, where
// the root setting is not set, where its text holds AWSTemplateFormatVersion;
// any other is named not read, with the reader's reason in a note. A request
// holding no template halts source-refused; several parameters files are none
// read. A template the import reads under a transform it cannot read halts
// source-refused, as [reader.refuseTransforms] says.
//
// The root template's resources become the graph's nodes, keyed by their
// logical IDs and ordered by them; its parameters, filled from the one
// parameters file read, are the graph's parameters; its outputs are the
// graph's outputs; and what it declares that no resource carries is named in
// the notes, after the sources not read for the template reader's refusal.
// Every value is left the long-form JSON the template reader writes, an
// intrinsic function as one leaf.
func read(in aggrega.ImportInput, s cloudimport.Settings, parse cloudimport.ParseFunc) (*cloudimport.Graph,
	[]aggrega.Diagnostic) {
	r := &reader{in: in, parse: parse}
	return r.read(s), r.diags
}

// reader is one read of a request's templates.
type reader struct {
	in    aggrega.ImportInput
	parse cloudimport.ParseFunc
	// sel is what the import reads of the sources it is handed; root is the
	// root template and templates each template read, in path order.
	sel       *selection
	root      *template
	templates []*template
	diags     []aggrega.Diagnostic
	// halted reports a halting diagnostic among diags.
	halted bool

	// graph is the graph [Read] flattens and lowers; byFile holds each
	// template read by its path.
	graph  *cloudimport.Graph
	byFile map[string]*template
	// stacks are the root template and each template a nested stack or a
	// serverless application deploys into the graph, root first, each before
	// the templates it deploys; home holds the stack and the logical ID of
	// each node, and nodeIDs the ID of each.
	stacks  []*stack
	home    map[*cloudimport.Node]home
	nodeIDs map[string]bool
	// taken and paramTaken hold the IDs of the nodes and the names of the
	// parameters of the graph; nestedRead counts the resources of the nested
	// templates read; noted names the nested templates the notes name.
	taken, paramTaken map[string]bool
	nestedRead        int
	noted             []string
	// lowering collects what lowering the graph reports, nil before it.
	lowering *[]aggrega.Diagnostic
}

// read reads the request's sources into the graph, nil where the read halts.
func (r *reader) read(s cloudimport.Settings) *cloudimport.Graph {
	sel := r.selectSources(r.classify(), s)
	if r.halted {
		return nil
	}
	r.sel = sel
	for _, h := range sel.templates {
		t := sections(h)
		r.templates = append(r.templates, t)
		if h == sel.root {
			r.root = t
		}
	}
	if r.refuseTransforms(); r.halted {
		return nil
	}
	t := r.root
	g := &cloudimport.Graph{Format: formatName, Read: sel.read, NotRead: sel.notRead}
	params, paramNotes := t.parameters()
	if sel.params != nil {
		r.fill(params, sel.params)
	}
	g.Params = params
	g.Nodes = r.nodes(t)
	sort.SliceStable(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	outputs, outputNotes := t.outputsRead()
	g.Outputs = outputs
	g.Notes = append(append(append(append([]string(nil), sel.notes...), t.declared()...), paramNotes...),
		outputNotes...)
	return g
}

// halt records a diagnostic that halts the import.
func (r *reader) halt(d aggrega.Diagnostic) {
	r.diags = append(r.diags, d)
	r.halted = true
}

// note records a diagnostic, once.
func (r *reader) note(d aggrega.Diagnostic) {
	for _, have := range r.diags {
		if have == d {
			return
		}
	}
	r.diags = append(r.diags, d)
}

// obj is a JSON object's members in the order the reader is handed them: the
// template reader and the host's parser both write them sorted by key.
type obj []importer.ObjectEntry

// objectOf returns the members of raw, false where raw is no object.
func objectOf(raw json.RawMessage) (obj, bool) {
	if kind(raw) != '{' {
		return nil, false
	}
	return obj(importer.ObjectEntries(raw)), true
}

// get returns the member of o named key, matched exactly, as CloudFormation
// matches a key; nil where o has none.
func (o obj) get(key string) json.RawMessage {
	for _, e := range o {
		if e.Key == key {
			return e.Value
		}
	}
	return nil
}

// itemsOf returns the items of raw, false where raw is no array.
func itemsOf(raw json.RawMessage) ([]json.RawMessage, bool) {
	if kind(raw) != '[' {
		return nil, false
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, false
	}
	return items, true
}

// kind returns the first character of raw past any space: '{', '[', '"', or
// the first character of a number, a boolean or null; 0 for nothing.
func kind(raw json.RawMessage) byte {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 {
		return trimmed[0]
	}
	return 0
}

// isNull reports whether raw is the JSON null.
func isNull(raw json.RawMessage) bool {
	return kind(raw) == 'n'
}

// compacted returns raw with no insignificant space, as one leaf of the graph
// holds it.
func compacted(raw json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return raw
	}
	return b.Bytes()
}

// literal is the leaf holding raw.
func literal(raw json.RawMessage) cloudimport.Literal {
	return cloudimport.Literal{JSON: compacted(raw)}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"bytes"
	"encoding/json"
	"sort"
	"unicode/utf8"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
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
	outputs, outputNotes := t.outputsRead("")
	g.Outputs = outputs
	g.Notes = append(append(append(append([]string(nil), sel.notes...), t.declared("The template")...), paramNotes...),
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

// value is one JSON value the reader reads, decoded once: its text, as the
// document holding it writes it, and the members of an object or the items of
// an array, each a value of its own, so reading any part of a document decodes
// none of it again.
type value struct {
	// raw is the value's text, a slice of the document's.
	raw json.RawMessage
	// members are an object's members, in the order the document writes them,
	// and items an array's items; both nil for any other value.
	members obj
	items   []*value
}

// member is one member of a JSON object: its key and its value.
type member struct {
	Key   string
	Value *value
}

// obj is a JSON object's members in the order the reader is handed them: the
// template reader and the host's parser both write them sorted by key.
type obj []member

// decoded returns the JSON value raw writes, decoded once; nil where raw holds
// nothing. Text that is no JSON value is a value holding that text alone.
func decoded(raw json.RawMessage) *value {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	if !json.Valid(trimmed) {
		return &value{raw: trimmed}
	}
	d := decoder{doc: trimmed}
	return d.value()
}

// decoder walks a document holding one valid JSON value, at its offset at.
type decoder struct {
	doc []byte
	at  int
}

// space moves d past the space before the next token.
func (d *decoder) space() {
	for d.at < len(d.doc) {
		switch d.doc[d.at] {
		case ' ', '\t', '\n', '\r':
			d.at++
		default:
			return
		}
	}
}

// value decodes the value at d's offset, and moves d past it.
func (d *decoder) value() *value {
	d.space()
	start := d.at
	v := &value{}
	switch d.doc[d.at] {
	case '{':
		d.at++
		for d.space(); d.doc[d.at] != '}'; d.space() {
			key := d.key()
			d.space()
			d.at++ // the colon
			v.members = append(v.members, member{Key: key, Value: d.value()})
			if d.space(); d.doc[d.at] == ',' {
				d.at++
			}
		}
		d.at++
	case '[':
		d.at++
		for d.space(); d.doc[d.at] != ']'; d.space() {
			v.items = append(v.items, d.value())
			if d.space(); d.doc[d.at] == ',' {
				d.at++
			}
		}
		d.at++
	case '"':
		d.text()
	default:
		for d.at < len(d.doc) && !closes(d.doc[d.at]) {
			d.at++
		}
	}
	v.raw = d.doc[start:d.at:d.at]
	return v
}

// closes reports whether c ends a number, a boolean or null.
func closes(c byte) bool {
	switch c {
	case ',', ']', '}', ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

// text moves d past the string at its offset.
func (d *decoder) text() {
	for d.at++; d.doc[d.at] != '"'; d.at++ {
		if d.doc[d.at] == '\\' {
			d.at++
		}
	}
	d.at++
}

// key decodes the string at d's offset, a member's key, and moves d past it:
// text with no escape and in UTF-8 is its own key, as decoding it gives.
func (d *decoder) key() string {
	start := d.at
	d.text()
	quoted := d.doc[start:d.at]
	if inner := quoted[1 : len(quoted)-1]; bytes.IndexByte(inner, '\\') < 0 && utf8.Valid(inner) {
		return string(inner)
	}
	var key string
	_ = json.Unmarshal(quoted, &key)
	return key
}

// json returns v's text, nil where v is nil.
func (v *value) json() json.RawMessage {
	if v == nil {
		return nil
	}
	return v.raw
}

// objectOf returns the members of v, false where v is no object.
func objectOf(v *value) (obj, bool) {
	if kind(v) != '{' {
		return nil, false
	}
	return v.members, true
}

// get returns the member of o named key, matched exactly, as CloudFormation
// matches a key; nil where o has none.
func (o obj) get(key string) *value {
	for _, e := range o {
		if e.Key == key {
			return e.Value
		}
	}
	return nil
}

// itemsOf returns the items of v, false where v is no array.
func itemsOf(v *value) ([]*value, bool) {
	if kind(v) != '[' {
		return nil, false
	}
	return v.items, true
}

// kind returns the first character of v: '{', '[', '"', or the first
// character of a number, a boolean or null; 0 for nothing.
func kind(v *value) byte {
	if raw := v.json(); len(raw) > 0 {
		return raw[0]
	}
	return 0
}

// isNull reports whether v is the JSON null.
func isNull(v *value) bool {
	return kind(v) == 'n'
}

// compacted returns raw with no insignificant space, as one leaf of the graph
// holds it: <, > and &, which the template reader writes escaped for HTML,
// are written as themselves, so a leaf holds its text as written and measures
// as its text ([cloudimport.Size]).
func compacted(raw json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return raw
	}
	if !bytes.Contains(b.Bytes(), []byte(`\u00`)) {
		return b.Bytes()
	}
	dec := json.NewDecoder(bytes.NewReader(b.Bytes()))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return b.Bytes()
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return b.Bytes()
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n"))
}

// literal is the leaf holding raw.
func literal(raw json.RawMessage) cloudimport.Literal {
	return cloudimport.Literal{JSON: compacted(raw)}
}

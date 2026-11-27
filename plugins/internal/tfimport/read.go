// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package tfimport

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/terraformcore/tfconfig"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// formatName is the terraform format's name, as the import request, the
// graph and the parse-failed diagnostic name it.
const formatName = "terraform"

// commentKey is the member name the JSON syntax reads as a comment rather
// than as an argument or a block, as CDK for Terraform writes its metadata.
const commentKey = "//"

// block is one top-level block of a module directory's configuration.
type block struct {
	// Kind is the block's type: resource, data, module, variable, output,
	// provider, terraform or locals.
	Kind string
	// Labels are the block's labels: a resource's or a data source's type and
	// name, a module call's, a variable's, an output's or a provider's name.
	Labels []string
	// Body is the block's body, in the syntax of the file holding it; members
	// reads it alike from either.
	Body hcl.Body
	// File is the name of the file holding the block, as the import was
	// handed it, and Range where the block is written in it: its header in
	// the native syntax, the object or array holding its body in JSON.
	File  string
	Range hcl.Range
}

// config is what the reader reads of one module directory.
type config struct {
	// Dir is the directory's slash-separated path inside the imported
	// directory, "." for the imported directory itself.
	Dir string
	// Blocks are the directory's blocks of the kinds read: its files in name
	// order, each file's blocks in the order it writes them.
	Blocks []block
	// NotRead names what the directory holds that is not read, in the same
	// order: each moved, import, removed and check block, each backend and
	// cloud block of a terraform block, and each block of a kind the reader
	// does not know, as "<kind> at <file>:<line>"; then, once, the
	// directory's .terraform/, where the import was handed a file beneath it
	// other than the record of its installed modules.
	NotRead []string
}

// readKinds are the kinds of block the reader reads.
var readKinds = []string{"resource", "data", "module", "variable", "output", "provider", "terraform", "locals"}

// configSchema names every top-level block kind Terraform reads, with its
// labels; a kind it does not name is one the reader does not know.
var configSchema = &hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{
	{Type: "resource", LabelNames: []string{"type", "name"}},
	{Type: "data", LabelNames: []string{"type", "name"}},
	{Type: "module", LabelNames: []string{"name"}},
	{Type: "variable", LabelNames: []string{"name"}},
	{Type: "output", LabelNames: []string{"name"}},
	{Type: "provider", LabelNames: []string{"name"}},
	{Type: "terraform"},
	{Type: "locals"},
	{Type: "moved"},
	{Type: "import"},
	{Type: "removed"},
	{Type: "check", LabelNames: []string{"name"}},
}}

// known reports whether configSchema names the block kind.
func known(kind string) bool {
	return slices.ContainsFunc(configSchema.Blocks, func(s hcl.BlockHeaderSchema) bool { return s.Type == kind })
}

// readDir reads the configuration files directly in the directory dir — a
// .tf or .tf.json file outside .terraform/, never one beneath it — into its
// blocks, each parsed in the syntax its name declares, as Terraform reads it:
// a .tf file opening with a byte-order mark or ending its lines with CRLF
// reads as the same file without them, and a .tf.json file opening with a
// byte-order mark is refused. A file that
// does not parse, or that writes a block of a kind Terraform reads with the
// wrong labels or a body that is no object, halts with the importer kit's
// parse-failed naming the file, the line and the column, and no
// configuration is returned.
func readDir(dir string, files []file) (*config, []aggrega.Diagnostic) {
	dir = path.Clean(dir)
	cfg := &config{Dir: dir}
	var own []file
	initialised := false
	for _, f := range files {
		switch classify(f.Name) {
		case kindConfig:
			if path.Dir(f.Name) == dir {
				own = append(own, f)
			}
		case kindSkipped:
			if holder, _, _ := underDotTerraform(f.Name); holder == dir {
				initialised = true
			}
		}
	}
	slices.SortStableFunc(own, func(a, b file) int { return strings.Compare(a.Name, b.Name) })

	var halts []aggrega.Diagnostic
	for _, f := range own {
		read, diags := readFile(f)
		if errs := errorsOf(diags); len(errs) > 0 {
			halts = append(halts, importer.ParseFailed(aggrega.ImportSource{Name: f.Name}, formatName, errs))
			continue
		}
		cfg.Blocks = append(cfg.Blocks, read.blocks...)
		cfg.NotRead = append(cfg.NotRead, read.notRead...)
	}
	if len(halts) > 0 {
		return nil, halts
	}
	if initialised {
		cfg.NotRead = append(cfg.NotRead, path.Join(dir, dotTerraform)+"/")
	}
	return cfg, nil
}

// errorsOf returns the errors among diags.
func errorsOf(diags hcl.Diagnostics) hcl.Diagnostics {
	var out hcl.Diagnostics
	for _, d := range diags {
		if d.Severity == hcl.DiagError {
			out = append(out, d)
		}
	}
	return out
}

// fileRead is what the reader reads of one configuration file: its blocks of
// the kinds read, and what it holds that is not read, each in the order the
// file writes it.
type fileRead struct {
	blocks  []block
	notRead []string
}

// placed is one entry of what a file holds that is not read, at its byte
// offset in the file.
type placed struct {
	at   int
	text string
}

// notReadAt names a block of the kind given, written at rng, as not read.
func notReadAt(kind string, rng hcl.Range) placed {
	return placed{at: rng.Start.Byte, text: fmt.Sprintf("%s at %s:%d", kind, rng.Filename, rng.Start.Line)}
}

// readFile parses one configuration file and reads its top-level blocks:
// those of the kinds read, each with its body checked readable, and what is
// not read. It returns the parse's diagnostics and those of reading the
// blocks; nothing is read where the file does not parse.
func readFile(f file) (fileRead, hcl.Diagnostics) {
	parsed, diags, _ := tfconfig.ParseConfigFile(f.Name, f.Bytes)
	if parsed == nil || diags.HasErrors() {
		return fileRead{}, diags
	}
	content, remain, contentDiags := parsed.Body.PartialContent(configSchema)
	diags = append(diags, contentDiags...)

	var read fileRead
	var skipped []placed
	for _, b := range content.Blocks {
		if !slices.Contains(readKinds, b.Type) {
			skipped = append(skipped, notReadAt(b.Type, b.DefRange))
			continue
		}
		ms, bodyDiags := members(b.Body)
		diags = append(diags, bodyDiags...)
		if b.Type == "terraform" {
			for _, m := range ms {
				if m.Name == "backend" || m.Name == "cloud" {
					skipped = append(skipped, notReadAt(m.Name, m.Range))
				}
			}
		}
		read.blocks = append(read.blocks, block{Kind: b.Type, Labels: b.Labels, Body: b.Body, File: f.Name,
			Range: b.DefRange})
	}
	skipped = append(skipped, unknownKinds(parsed.Body, remain)...)

	slices.SortStableFunc(read.blocks, func(a, b block) int { return a.Range.Start.Byte - b.Range.Start.Byte })
	slices.SortStableFunc(skipped, func(a, b placed) int { return a.at - b.at })
	for _, s := range skipped {
		read.notRead = append(read.notRead, s.text)
	}
	return read, diags
}

// unknownKinds names, in the order a file's body writes them, its top-level
// blocks and arguments of no kind configSchema names: in the native syntax
// the body's own, in JSON the members of remain, what PartialContent left of
// it, less its comments.
func unknownKinds(body, remain hcl.Body) []placed {
	var out []placed
	if native, ok := body.(*hclsyntax.Body); ok {
		for _, b := range native.Blocks {
			if !known(b.Type) {
				out = append(out, notReadAt(b.Type, b.TypeRange))
			}
		}
		for _, a := range byOffset(hclAttributes(native.Attributes)) {
			out = append(out, notReadAt(a.Name, a.NameRange))
		}
		return out
	}
	// A JSON file whose top level is an array of objects names no member
	// here; what it holds that is read is read all the same.
	attrs, _ := remain.JustAttributes()
	for _, a := range byOffset(attrs) {
		out = append(out, notReadAt(a.Name, a.NameRange))
	}
	return out
}

// byOffset returns attrs in the order the file writes them.
func byOffset(attrs hcl.Attributes) []*hcl.Attribute {
	out := make([]*hcl.Attribute, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, a)
	}
	slices.SortFunc(out, func(a, b *hcl.Attribute) int {
		if a.NameRange.Start.Byte != b.NameRange.Start.Byte {
			return a.NameRange.Start.Byte - b.NameRange.Start.Byte
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// hclAttributes returns a native body's arguments as HCL's attributes.
func hclAttributes(attrs hclsyntax.Attributes) hcl.Attributes {
	out := make(hcl.Attributes, len(attrs))
	for name, a := range attrs {
		out[name] = a.AsHCLAttribute()
	}
	return out
}

// shape is what a member holds.
type shape int

// The shapes of a member.
const (
	// leafShape is one expression.
	leafShape shape = iota
	// objectShape is an object: a nested block written once in its body, an
	// object expression, a JSON object.
	objectShape
	// listShape is a list: the nested blocks of one type written several
	// times in one body, a list expression, a JSON array.
	listShape
)

// member is one member of a block's body, or of an object or a list it holds,
// read alike from either syntax.
type member struct {
	// Name is an argument's name, a nested block's type or label, or an
	// object's key; empty for an item of a list.
	Name  string
	Shape shape
	// Expr is a leaf's expression, nil for an object or a list.
	Expr hcl.Expression
	// Members are an object's members and Items a list's items, each in the
	// order the file writes them.
	Members, Items []member
	// Range is where the member is written: an argument's or a key's name, a
	// nested block's type or label, an item's value.
	Range hcl.Range
}

// members reads a block's body into its members, in the order the file
// writes them, alike from either syntax: in the native syntax an argument is
// its expression and the nested blocks of one type are one member, an object
// where the body writes one and a list of objects where it writes several,
// and a labelled nested block sits beneath its labels, as JSON writes it; in
// JSON every member of the body is an argument, its object an object and its
// array a list. In both, an object or a list expression is read into its
// members or items, and so is every object and list inside it, as long as
// each key is literal text; a "//" member of a JSON object is a comment and
// no member. It returns what reading a JSON body reports: one that is no
// object, a name written twice.
func members(body hcl.Body) ([]member, hcl.Diagnostics) {
	if native, ok := body.(*hclsyntax.Body); ok {
		return nativeMembers(native), nil
	}
	attrs, diags := body.JustAttributes()
	ordered := byOffset(attrs)
	out := make([]member, 0, len(ordered))
	for _, a := range ordered {
		out = append(out, named(valueOf(a.Expr), a.Name, a.NameRange))
	}
	return out, diags
}

// named returns m under the name written at rng.
func named(m member, name string, rng hcl.Range) member {
	m.Name, m.Range = name, rng
	return m
}

// nativeMembers reads a native body's arguments and nested blocks into its
// members, ordered by where each argument and the first block of each type is
// written.
func nativeMembers(body *hclsyntax.Body) []member {
	type at struct {
		offset int
		m      member
	}
	var out []at
	for _, a := range byOffset(hclAttributes(body.Attributes)) {
		out = append(out, at{a.NameRange.Start.Byte, named(valueOf(a.Expr), a.Name, a.NameRange)})
	}
	var nested blockGroup
	for _, b := range body.Blocks {
		nested.add(b, 0)
	}
	for _, g := range nested.children {
		out = append(out, at{g.rng.Start.Byte, g.member()})
	}
	slices.SortStableFunc(out, func(a, b at) int { return a.offset - b.offset })
	ms := make([]member, len(out))
	for i, e := range out {
		ms[i] = e.m
	}
	return ms
}

// blockGroup gathers the nested blocks of a native body under their type and
// then under each of their labels, each name where it is first written.
type blockGroup struct {
	name     string
	rng      hcl.Range
	blocks   []*hclsyntax.Block
	children []*blockGroup
}

// add places b beneath g, depth names of its type and labels already read.
func (g *blockGroup) add(b *hclsyntax.Block, depth int) {
	if depth > len(b.Labels) {
		g.blocks = append(g.blocks, b)
		return
	}
	name, rng := b.Type, b.TypeRange
	if depth > 0 {
		name, rng = b.Labels[depth-1], b.LabelRanges[depth-1]
	}
	for _, c := range g.children {
		if c.name == name {
			c.add(b, depth+1)
			return
		}
	}
	c := &blockGroup{name: name, rng: rng}
	g.children = append(g.children, c)
	c.add(b, depth+1)
}

// member returns what g holds: the body of the one block written under its
// name an object, those of several a list of objects, and its labels an
// object of what each holds.
func (g *blockGroup) member() member {
	bodies := make([]member, 0, len(g.blocks))
	for _, b := range g.blocks {
		bodies = append(bodies, member{Shape: objectShape, Members: nativeMembers(b.Body), Range: b.DefRange()})
	}
	labelled := member{Shape: objectShape, Range: g.rng}
	for _, c := range g.children {
		labelled.Members = append(labelled.Members, c.member())
	}
	switch {
	case len(g.children) == 0 && len(bodies) == 1:
		return named(bodies[0], g.name, g.rng)
	case len(g.children) == 0:
		return member{Name: g.name, Shape: listShape, Items: bodies, Range: g.rng}
	case len(bodies) == 0:
		return named(labelled, g.name, g.rng)
	}
	return member{Name: g.name, Shape: listShape, Items: append(bodies, labelled), Range: g.rng}
}

// valueOf reads an expression into a member: an object or a list expression
// into its members or items, any other expression a leaf. An object with a
// key that is no literal text is a leaf whole.
func valueOf(expr hcl.Expression) member {
	leaf := member{Shape: leafShape, Expr: expr, Range: expr.Range()}
	switch e := expr.(type) {
	case *hclsyntax.TupleConsExpr:
		out := member{Shape: listShape, Range: e.SrcRange}
		for _, item := range e.Exprs {
			out.Items = append(out.Items, valueOf(item))
		}
		return out
	case *hclsyntax.ObjectConsExpr:
		out := member{Shape: objectShape, Range: e.SrcRange}
		for _, item := range e.Items {
			key, ok := literalString(item.KeyExpr)
			if !ok {
				return leaf
			}
			out.Members = append(out.Members, named(valueOf(item.ValueExpr), key, item.KeyExpr.Range()))
		}
		return out
	case hclsyntax.Expression:
		return leaf
	}
	if pairs, diags := hcl.ExprMap(expr); !diags.HasErrors() {
		out := member{Shape: objectShape, Range: expr.Range()}
		for _, p := range pairs {
			key, ok := literalString(p.Key)
			if !ok {
				return leaf
			}
			if key != commentKey {
				out.Members = append(out.Members, named(valueOf(p.Value), key, p.Key.Range()))
			}
		}
		return out
	}
	if items, diags := hcl.ExprList(expr); !diags.HasErrors() {
		out := member{Shape: listShape, Range: expr.Range()}
		for _, item := range items {
			out.Items = append(out.Items, valueOf(item))
		}
		return out
	}
	return leaf
}

// literalString returns the text an expression writes as a literal string, in
// either syntax, read off its syntax and never evaluated: a native string
// whose every part is literal text, or a bare word written as an object key;
// a JSON string read as the template Terraform reads it as, so "$${x}" is the
// text ${x} and "${var.x}" no literal.
func literalString(expr hcl.Expression) (string, bool) {
	switch e := expr.(type) {
	case *hclsyntax.ObjectConsKeyExpr:
		if !e.ForceNonLiteral {
			if word := hcl.ExprAsKeyword(e.Wrapped); word != "" {
				return word, true
			}
		}
		return literalString(e.Wrapped)
	case *hclsyntax.TemplateExpr:
		var b strings.Builder
		for _, part := range e.Parts {
			text, ok := literalString(part)
			if !ok {
				return "", false
			}
			b.WriteString(text)
		}
		return b.String(), true
	case *hclsyntax.LiteralValueExpr:
		if e.Val.Type() != cty.String || e.Val.IsNull() || !e.Val.IsKnown() {
			return "", false
		}
		return e.Val.AsString(), true
	case hclsyntax.Expression:
		return "", false
	}
	// Past the native syntax's, the JSON syntax's expressions are the ones
	// reading an object's members by ExprMap; no other is read.
	if _, isJSON := expr.(interface{ ExprMap() []hcl.KeyValuePair }); !isJSON {
		return "", false
	}
	v, diags := expr.Value(nil)
	if diags.HasErrors() || v.Type() != cty.String || v.IsNull() || !v.IsKnown() {
		return "", false
	}
	rng := expr.Range()
	tmpl, diags := hclsyntax.ParseTemplate([]byte(v.AsString()), rng.Filename, rng.Start)
	if diags.HasErrors() {
		return "", false
	}
	return literalString(tmpl)
}

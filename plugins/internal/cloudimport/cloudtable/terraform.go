// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template/parse"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/terraformcore/tfconfig"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/tfimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/tfimport/tfaddr"
)

// A terraform Recipe's artifacts are Terraform configuration written as Go
// templates, and the files of the local directory it sources are
// configuration copied as they are. The extractor reads both as Terraform
// reads them and evaluates nothing: an artifact's template actions are masked
// first, so the text parses as the configuration it renders, each masked
// action standing for the action it replaces.

// passThroughFile is the hub's module through which a terraform Recipe wrapping
// an ArtifactLibrary body passes its input: its one variable, config, is
// handed whole to the body the Recipe imports at passThroughBody, and the
// body's outputs are its one output, named as that directory.
const passThroughFile = "scripts/container-pass-through/main.tf"

// passThroughBody is the directory the pass-through module calls its body at.
const passThroughBody = "service"

// terraformDiscriminators are the attributes, and the module inputs, whose
// literal text says which product a resource of its type runs: the engine of
// a database, cache or broker, and the kind of an account. Every other literal
// a Recipe writes is a setting the Recipe chooses, which a resource of another
// setting is still an instance of.
var terraformDiscriminators = []string{"engine", "engine_type", "kind"}

// maskPrefix and maskSuffix enclose the number of a template action masked as
// an identifier, and maskWord matches such an identifier, capturing the
// number.
const (
	maskPrefix = "__aggrega_action_"
	maskSuffix = "__"
)

var (
	maskWord  = regexp.MustCompile(maskPrefix + `([0-9]+)` + maskSuffix)
	maskWhole = regexp.MustCompile(`^` + maskPrefix + `([0-9]+)` + maskSuffix + `$`)
)

// maskedAction is one action a masked template writes as an identifier: where
// it is written in the template's text, and its text less its delimiters and
// trim markers.
type maskedAction struct {
	start, end int
	inner      string
}

// lineState is what template branches a line of a masked template is written
// in: one the build decides when it renders, and a range repeating it or a
// definition the template does not write in place.
type lineState struct {
	conditional, repeated bool
}

// maskedTemplate is a template's text with its actions masked: the masked
// text, each masked action by its number, and the branches each line is
// written in, line n at n-1.
type maskedTemplate struct {
	text    string
	actions []maskedAction
	lines   []lineState
}

// lineAt returns the branches the line numbered n is written in.
func (m maskedTemplate) lineAt(n int) lineState {
	if n < 1 || n > len(m.lines) {
		return lineState{}
	}
	return m.lines[n-1]
}

// maskTemplate masks the template actions of text so the text reads as the
// configuration it renders. A line holding only actions and whitespace
// becomes empty; elsewhere a comment action writes nothing, and every other
// action is written as the identifier __aggrega_action_<n>__, inside a string
// or outside one, n its number among the actions masked so, by which it is
// remembered. A newline written inside an action is kept after the line it
// ends, so every line keeps its number.
func maskTemplate(text string) maskedTemplate {
	spans := scanAllActions(text)
	var (
		m    maskedTemplate
		b    strings.Builder
		open []string
	)
	state := func() lineState {
		var s lineState
		for _, word := range open {
			s.conditional = true
			if word != "if" && word != "with" {
				s.repeated = true
			}
		}
		return s
	}
	for start, next := 0, 0; ; {
		// A line runs to the first newline written outside an action.
		end, k := len(text), next
		for from := start; ; {
			end = len(text)
			if nl := strings.IndexByte(text[from:], '\n'); nl >= 0 {
				end = from + nl
			}
			crossed := false
			for k < len(spans) && spans[k].start < end {
				k++
				if spans[k-1].end > end {
					from, crossed = spans[k-1].end, true
					break
				}
			}
			if !crossed {
				break
			}
		}
		line := spans[next:k]
		newlines := strings.Count(text[start:end], "\n")
		st := state()
		for range newlines + 1 {
			m.lines = append(m.lines, st)
		}
		alone := len(line) > 0
		at := start
		for _, s := range line {
			alone = alone && strings.TrimSpace(text[at:s.start]) == ""
			at = s.end
		}
		alone = alone && strings.TrimSpace(text[at:end]) == ""
		at = start
		for _, s := range line {
			if !alone {
				b.WriteString(text[at:s.start])
			}
			at = s.end
			if s.comment {
				continue
			}
			open = branch(open, s.inner)
			if !alone {
				b.WriteString(maskPrefix + strconv.Itoa(len(m.actions)) + maskSuffix)
				m.actions = append(m.actions, maskedAction{start: s.start, end: s.end, inner: s.inner})
			}
		}
		if !alone {
			b.WriteString(text[at:end])
		}
		b.WriteString(strings.Repeat("\n", newlines))
		next = k
		if end >= len(text) {
			break
		}
		b.WriteByte('\n')
		start = end + 1
	}
	m.text = b.String()
	return m
}

// canonicalAction writes an action's text as every spelling of it compares
// equal: each run of space as one space, and a field read off $ as the same
// field read off the dot, which outside a range or a with is the same data.
func canonicalAction(inner string) string {
	text := normalize(inner)
	if rest, ok := strings.CutPrefix(text, "$."); ok {
		return "." + rest
	}
	return text
}

// tfFile is one configuration file of a Recipe: the name it lands under in
// the module the Recipe renders, and the directory of that module it is in;
// its masked template, for an artifact, and where each pipeline of the
// template starts; and its body.
type tfFile struct {
	name, dir string
	tmpl      *maskedTemplate
	pipes     []pipeAt
	body      *hclsyntax.Body
}

// canonical writes text read from f with each masked action in it written as
// the action, in canonical text, so two spellings of one name compare equal.
func (f *tfFile) canonical(text string) string {
	if f.tmpl == nil {
		return text
	}
	return maskWord.ReplaceAllStringFunc(text, func(word string) string {
		n, _ := strconv.Atoi(maskWord.FindStringSubmatch(word)[1])
		if n >= len(f.tmpl.actions) {
			return word
		}
		return "{{ " + canonicalAction(f.tmpl.actions[n].inner) + " }}"
	})
}

// lineAt returns the template branches the line numbered n of f is written
// in, none for a file copied as it is.
func (f *tfFile) lineAt(n int) lineState {
	if f.tmpl == nil {
		return lineState{}
	}
	return f.tmpl.lineAt(n)
}

// masked returns the action an expression of f is written as whole, and
// whether it is written inside a string.
func (f *tfFile) masked(e hclsyntax.Expression) (maskedAction, bool, bool) {
	if f.tmpl == nil {
		return maskedAction{}, false, false
	}
	var word string
	quoted := false
	switch e := e.(type) {
	case *hclsyntax.TemplateExpr:
		if len(e.Parts) != 1 {
			return maskedAction{}, false, false
		}
		lit, ok := e.Parts[0].(*hclsyntax.LiteralValueExpr)
		if !ok || lit.Val.Type() != cty.String || lit.Val.IsNull() {
			return maskedAction{}, false, false
		}
		word, quoted = lit.Val.AsString(), true
	case *hclsyntax.ScopeTraversalExpr:
		if len(e.Traversal) != 1 {
			return maskedAction{}, false, false
		}
		word = e.Traversal.RootName()
	default:
		return maskedAction{}, false, false
	}
	m := maskWhole.FindStringSubmatch(word)
	if m == nil {
		return maskedAction{}, false, false
	}
	n, _ := strconv.Atoi(m[1])
	if n >= len(f.tmpl.actions) {
		return maskedAction{}, false, false
	}
	return f.tmpl.actions[n], quoted, true
}

// literalText returns the text of an expression of f written as a literal
// string, holding no masked action.
func (f *tfFile) literalText(e hclsyntax.Expression) (string, bool) {
	t, ok := e.(*hclsyntax.TemplateExpr)
	if !ok || !t.IsStringLiteral() {
		return "", false
	}
	v, diags := t.Value(nil)
	if diags.HasErrors() || v.Type() != cty.String || v.IsNull() {
		return "", false
	}
	if s := v.AsString(); !maskWord.MatchString(s) {
		return s, true
	}
	return "", false
}

// parseTerraformFile reads one file of a Recipe, masking its template actions
// first where it is an artifact, and parses it as Terraform parses a
// configuration file; of several values of one attribute it keeps the first,
// as a template writing an attribute in each branch of a decision writes one
// of them. It returns nil and no reason for a file Terraform does not load as
// configuration, and nil with why for one that does not parse.
func parseTerraformFile(f fragment, template bool) (*tfFile, string) {
	jsonSyntax, ok := tfconfig.Syntax(f.name)
	if !ok {
		return nil, ""
	}
	out := &tfFile{name: f.name, dir: path.Dir(f.name)}
	text := f.text
	if template {
		_, pipes, ok := pipelinesOf(f.text)
		if !ok {
			return nil, "its file " + f.name + " does not parse as a template"
		}
		m := maskTemplate(f.text)
		out.tmpl, out.pipes, text = &m, pipes, m.text
	}
	file, diags, _ := tfconfig.ParseConfigFile(f.name, []byte(text))
	for _, d := range diags {
		if d.Severity == hcl.DiagError && d.Summary != "Attribute redefined" {
			return nil, fmt.Sprintf("its file %s does not parse as a Terraform configuration once its template "+
				"actions are masked (%s)", f.name, d.Error())
		}
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if jsonSyntax || !ok {
		return nil, "its file " + f.name + " is written in JSON syntax, which the generator reads no resource of"
	}
	out.body = body
	return out, ""
}

// tfResource is one resource block of a Recipe's files: its type as written,
// its name in canonical text, its file and block, and whether it opens inside
// a template branch, and inside a range, which repeats it.
type tfResource struct {
	typ, name        string
	file             *tfFile
	block            *hclsyntax.Block
	branch, repeated bool
}

// String names r as the reader finds it in the Recipe.
func (r *tfResource) String() string {
	return r.typ + "." + r.name + " (" + r.file.name + ")"
}

// tfOutput is one output block of a Recipe's files: its value and its file.
type tfOutput struct {
	value hclsyntax.Expression
	file  *tfFile
}

// tfModule is what the extractor reads of a Recipe's files: each resource in
// the order written, each output by its directory and name, and the variables
// each directory declares.
type tfModule struct {
	resources []*tfResource
	outputs   map[string]tfOutput
	variables map[string]bool
}

// readTerraformModule parses the files of the local directory r sources,
// copied as they are, and its artifacts, written as templates, and reads
// their blocks; it returns why it reads none where a file does not parse or
// is written in JSON syntax.
func readTerraformModule(r recipeSource) (tfModule, string) {
	var files []*tfFile
	for _, group := range []struct {
		frags    []fragment
		template bool
	}{{r.module, false}, {r.fragments, true}} {
		for _, frag := range group.frags {
			f, why := parseTerraformFile(frag, group.template)
			if why != "" {
				return tfModule{}, why
			}
			if f != nil {
				files = append(files, f)
			}
		}
	}
	m := tfModule{outputs: map[string]tfOutput{}, variables: map[string]bool{}}
	for _, f := range files {
		for _, b := range f.body.Blocks {
			switch {
			case b.Type == "resource" && len(b.Labels) == 2:
				st := f.lineAt(b.TypeRange.Start.Line)
				m.resources = append(m.resources, &tfResource{typ: f.canonical(b.Labels[0]),
					name: f.canonical(b.Labels[1]), file: f, block: b, branch: st.conditional, repeated: st.repeated})
			case b.Type == "output" && len(b.Labels) == 1:
				key := f.dir + "\x00" + f.canonical(b.Labels[0])
				if a, ok := b.Body.Attributes["value"]; ok {
					if _, seen := m.outputs[key]; !seen {
						m.outputs[key] = tfOutput{value: a.Expr, file: f}
					}
				}
			case b.Type == "variable" && len(b.Labels) == 1:
				m.variables[f.dir+"\x00"+b.Labels[0]] = true
			}
		}
	}
	return m, ""
}

// resourceNamed returns the resource of the module directory dir of the type
// and name, in canonical text, given.
func (m tfModule) resourceNamed(dir, typ, name string) *tfResource {
	for _, r := range m.resources {
		if r.file.dir == dir && r.typ == typ && r.name == name {
			return r
		}
	}
	return nil
}

// referenced returns the resource of m a traversal written in f names, nil
// where it names none.
func (m tfModule) referenced(f *tfFile, t hcl.Traversal) *tfResource {
	if len(t) < 2 {
		return nil
	}
	attr, ok := t[1].(hcl.TraverseAttr)
	if !ok {
		return nil
	}
	return m.resourceNamed(f.dir, t.RootName(), f.canonical(attr.Name))
}

// traversalsOf returns every traversal the expressions of body and of its
// nested blocks read.
func traversalsOf(body *hclsyntax.Body) []hcl.Traversal {
	var out []hcl.Traversal
	for _, a := range attributesOf(body) {
		out = append(out, a.Expr.Variables()...)
	}
	for _, b := range body.Blocks {
		out = append(out, traversalsOf(b.Body)...)
	}
	return out
}

// instanceResource returns the resource of m that is the instance: of the
// resources no other resource of m references, which must all be of one type,
// the one opening outside every template branch, or the one alone. A resource
// a template range writes is one the template repeats, and neither is the
// instance nor makes another resource referenced. It returns why where m's
// text does not say which.
func (m tfModule) instanceResource() (*tfResource, string) {
	switch {
	case len(m.resources) == 0:
		return nil, "it writes no resource"
	case !slices.ContainsFunc(m.resources, func(r *tfResource) bool { return !r.repeated }):
		return nil, "it writes each of its resources inside a template range, which repeats it"
	}
	referenced := map[*tfResource]bool{}
	for _, r := range m.resources {
		if r.repeated {
			continue
		}
		for _, t := range traversalsOf(r.block.Body) {
			if s := m.referenced(r.file, t); s != nil && s != r {
				referenced[s] = true
			}
		}
	}
	var free, outside []*tfResource
	var types []string
	for _, r := range m.resources {
		if referenced[r] || r.repeated {
			continue
		}
		free = append(free, r)
		if !r.branch {
			outside = append(outside, r)
		}
		if !slices.Contains(types, r.typ) {
			types = append(types, r.typ)
		}
	}
	switch {
	case len(free) == 0:
		return nil, "each resource it writes is referenced by another, so its text does not say which is the instance"
	case len(types) > 1:
		return nil, "it writes " + tfResourceList(free) + ", of " + strconv.Itoa(len(types)) + " types no other " +
			"resource of the Recipe references, so its text does not say which is the instance"
	case len(outside) == 1:
		return outside[0], ""
	case len(free) == 1:
		return free[0], ""
	}
	return nil, "it writes " + tfResourceList(free) + ", each referenced by no other resource of the Recipe and " +
		"none alone outside every template branch, so its text does not say which is the instance"
}

// tfResourceList names rs as the reader finds them in the Recipe.
func tfResourceList(rs []*tfResource) string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.String()
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// readWhole returns what a value written as one action reads whole — a value,
// or an infra key other than an override, read off the data the template is
// handed — with the conversion the way it is written gives it: a number of
// gibibytes where the action trims the suffix Gi from it, its text where the
// action is quoted and does not write it as JSON, and the value as is
// otherwise.
func readWhole(value string) (sym, cloudimport.Conv, bool) {
	trimmed := strings.TrimSpace(value)
	unq := unquote(trimmed)
	nodes, ok := rootNodes(unq)
	if !ok || len(nodes) != 1 {
		return sym{}, "", false
	}
	action, ok := nodes[0].(*parse.ActionNode)
	if !ok {
		return sym{}, "", false
	}
	var (
		s    sym
		conv cloudimport.Conv
	)
	if arg, ok := trimsGibibytes(action.Pipe); ok {
		if s, ok = fieldRead(arg, dataScope()); !ok {
			return sym{}, "", false
		}
		conv = "unit:Gi"
	} else {
		var toJSON bool
		if s, toJSON, ok = wholeRead(action.Pipe, dataScope()); !ok {
			return sym{}, "", false
		}
		if unq != trimmed && !toJSON {
			conv = "string"
		}
	}
	if s.kind != symValue && s.kind != symInfraKey {
		return sym{}, "", false
	}
	return s, conv, true
}

// trimsGibibytes returns the argument a pipeline trims the suffix Gi from,
// written trimSuffix "Gi" X or X | trimSuffix "Gi".
func trimsGibibytes(p *parse.PipeNode) (parse.Node, bool) {
	trims := func(args []parse.Node) bool {
		fn, ok := args[0].(*parse.IdentifierNode)
		suffix, isString := args[1].(*parse.StringNode)
		return ok && fn.Ident == "trimSuffix" && isString && suffix.Text == "Gi"
	}
	switch {
	case p == nil || len(p.Decl) > 0:
	case len(p.Cmds) == 1 && len(p.Cmds[0].Args) == 3 && trims(p.Cmds[0].Args):
		return p.Cmds[0].Args[2], true
	case len(p.Cmds) == 2 && len(p.Cmds[0].Args) == 1 && len(p.Cmds[1].Args) == 2 && trims(p.Cmds[1].Args):
		return p.Cmds[0].Args[0], true
	}
	return nil, false
}

// varRead is a module variable, or a member of one, that an attribute reads
// whole, and the attribute's path.
type varRead struct {
	variable, at []string
}

// tfRowBuilder gathers the row of a Recipe's instance.
type tfRowBuilder struct {
	row  cloudimport.Row
	mod  tfModule
	vars []varRead
	// written are the paths the instance's resource, and each resource read
	// in place of a reference to it, write a value at.
	written [][]string
	// varDirs are the directories of the module whose variables are the ones
	// the Recipe's valuesMapping writes.
	varDirs []string
}

// place adds a placement of what s reads from the path from, keeping the
// first placement of each key.
func (b *tfRowBuilder) place(s sym, from []string, conv cloudimport.Conv) {
	list := &b.row.Values
	if s.kind == symInfraKey {
		list = &b.row.Infra
	}
	if !slices.ContainsFunc(*list, func(o cloudimport.Placement) bool { return o.Key == s.key }) {
		*list = append(*list, cloudimport.Placement{From: slices.Clone(from), Key: s.key, Conv: conv})
	}
}

// when adds the predicate a discriminator's literal at path gives.
func (b *tfRowBuilder) when(path []string, literal string) {
	if len(path) != 1 || !slices.Contains(terraformDiscriminators, path[0]) {
		return
	}
	if !slices.ContainsFunc(b.row.When, func(p cloudimport.Pred) bool { return slices.Equal(p.Path, path) }) {
		b.row.When = append(b.row.When, cloudimport.Pred{Path: slices.Clone(path), Op: cloudimport.OpEquals,
			Value: literal})
	}
}

// walkBody calls visit with each value body writes and the path it writes it
// at beneath prefix, in the order written: an attribute by its name, a nested
// block's by the block's type and its index among the blocks of that type,
// and a value inside an object, a tuple or a value jsonencode encodes by the
// member's key or the item's index. A dynamic block, which repeats its
// content, gives nothing.
func walkBody(body *hclsyntax.Body, prefix []string, visit func([]string, hclsyntax.Expression)) {
	type item struct {
		at    int
		attr  *hclsyntax.Attribute
		block *hclsyntax.Block
	}
	var items []item
	for _, a := range attributesOf(body) {
		items = append(items, item{at: a.SrcRange.Start.Byte, attr: a})
	}
	for _, b := range body.Blocks {
		items = append(items, item{at: b.TypeRange.Start.Byte, block: b})
	}
	slices.SortFunc(items, func(x, y item) int { return x.at - y.at })
	index := map[string]int{}
	for _, it := range items {
		if it.attr != nil {
			walkExpr(slices.Concat(prefix, []string{it.attr.Name}), it.attr.Expr, visit)
			continue
		}
		if it.block.Type == "dynamic" {
			continue
		}
		i := index[it.block.Type]
		index[it.block.Type]++
		walkBody(it.block.Body, slices.Concat(prefix, []string{it.block.Type, strconv.Itoa(i)}), visit)
	}
}

// attributesOf returns the attributes of body in the order written.
func attributesOf(body *hclsyntax.Body) []*hclsyntax.Attribute {
	out := make([]*hclsyntax.Attribute, 0, len(body.Attributes))
	for _, a := range body.Attributes {
		out = append(out, a)
	}
	slices.SortFunc(out, func(x, y *hclsyntax.Attribute) int { return x.SrcRange.Start.Byte - y.SrcRange.Start.Byte })
	return out
}

// walkExpr calls visit with e at path and with each value inside it.
func walkExpr(path []string, e hclsyntax.Expression, visit func([]string, hclsyntax.Expression)) {
	visit(path, e)
	switch e := e.(type) {
	case *hclsyntax.ObjectConsExpr:
		for _, item := range e.Items {
			key, diags := item.KeyExpr.Value(nil)
			if diags.HasErrors() || key.Type() != cty.String || key.IsNull() || maskWord.MatchString(key.AsString()) {
				continue
			}
			walkExpr(slices.Concat(path, []string{key.AsString()}), item.ValueExpr, visit)
		}
	case *hclsyntax.TupleConsExpr:
		for i, x := range e.Exprs {
			walkExpr(slices.Concat(path, []string{strconv.Itoa(i)}), x, visit)
		}
	case *hclsyntax.FunctionCallExpr:
		if e.Name == "jsonencode" && len(e.Args) == 1 && !e.ExpandFinal {
			walkExpr(path, e.Args[0], visit)
		}
	case *hclsyntax.ParenthesesExpr:
		walkExpr(path, e.Expression, visit)
	}
}

// variablePath returns the module variable, and the members of it, an
// expression reads whole.
func variablePath(e hclsyntax.Expression) ([]string, bool) {
	t, ok := e.(*hclsyntax.ScopeTraversalExpr)
	if !ok || len(t.Traversal) < 2 || t.Traversal.RootName() != "var" {
		return nil, false
	}
	var out []string
	for _, step := range t.Traversal[1:] {
		attr, ok := step.(hcl.TraverseAttr)
		if !ok {
			return nil, false
		}
		out = append(out, attr.Name)
	}
	return out, true
}

// readResource reads the values r writes beneath prefix: each one action
// reads a value or an infra key whole at, placed from its path; each module
// variable of the Recipe's input read whole; and, of the instance's own
// resource, each discriminator written as a literal outside every template
// branch. It returns each resource a read-in rule reads in place of r's
// reference to it, with the path of the reference, in the order written.
func (b *tfRowBuilder) readResource(r *tfResource, prefix []string, own bool) ([]*tfResource, [][]string) {
	var (
		children []*tfResource
		at       [][]string
	)
	rules := tfimport.ReadIns()
	inputs := slices.Contains(b.varDirs, r.file.dir)
	walkBody(r.block.Body, prefix, func(p []string, e hclsyntax.Expression) {
		b.written = append(b.written, p)
		st := r.file.lineAt(e.Range().Start.Line)
		if a, quoted, ok := r.file.masked(e); ok {
			if st.repeated || !readsDataIn(r.file.pipes, a.start, a.end) {
				return
			}
			text := "{{ " + a.inner + " }}"
			if quoted {
				text = `"` + text + `"`
			}
			if s, conv, ok := readWhole(text); ok {
				b.place(s, p, conv)
			}
			return
		}
		if v, ok := variablePath(e); ok {
			if inputs {
				b.vars = append(b.vars, varRead{variable: v, at: slices.Clone(p)})
			}
			return
		}
		if lit, ok := r.file.literalText(e); ok && own && !st.conditional {
			b.when(p, lit)
			return
		}
		t, ok := e.(*hclsyntax.ScopeTraversalExpr)
		if !ok || !own {
			return
		}
		c := b.mod.referenced(r.file, t.Traversal)
		if c == nil || c == r || slices.Contains(children, c) {
			return
		}
		local := p[len(prefix):]
		for _, rule := range rules {
			if strings.EqualFold(rule.Parent, r.typ) && strings.EqualFold(rule.Child, c.typ) &&
				pathIs(local, rule.Path) {
				children, at = append(children, c), append(at, slices.Clone(p))
				return
			}
		}
	})
	return children, at
}

// translate returns the path of the attribute an input of the Recipe's
// module, at the path in, is read at: through the longest variable path of
// the module an attribute reads whole that in begins with, the attribute
// named as the input chosen where several read it, else the first written.
func (b *tfRowBuilder) translate(in []string) ([]string, bool) {
	var (
		best  []string
		depth = -1
	)
	for _, v := range b.vars {
		if len(v.variable) > len(in) || !slices.Equal(v.variable, in[:len(v.variable)]) {
			continue
		}
		cand := slices.Concat(v.at, in[len(v.variable):])
		switch {
		case len(v.variable) > depth:
			best, depth = cand, len(v.variable)
		case len(v.variable) == depth && slices.Equal(cand, in) && !slices.Equal(best, in):
			best = cand
		}
	}
	return best, depth >= 0
}

// writes reports whether the instance's resources write a value at p or
// beneath it.
func (b *tfRowBuilder) writes(p []string) bool {
	return slices.ContainsFunc(b.written, func(w []string) bool {
		return len(w) >= len(p) && slices.Equal(w[:len(p)], p)
	})
}

// declares reports whether a directory of the Recipe's input declares the
// variable name.
func (b *tfRowBuilder) declares(name string) bool {
	return slices.ContainsFunc(b.varDirs, func(dir string) bool { return b.mod.variables[dir+"\x00"+name] })
}

// readValuesMapping reads the Recipe's valuesMapping, each input it writes
// read through at: one action reading a value or an infra key whole placed
// from the path at gives, and a discriminator written as a literal outside
// every branch at a path at gives one segment of a predicate.
func (b *tfRowBuilder) readValuesMapping(text string, at func([]string) ([]string, bool)) {
	doc := readTemplateYAML(text)
	for _, l := range doc.leaves {
		from, ok := at(l.path)
		if !ok {
			continue
		}
		if doc.readsData(l) {
			if s, conv, ok := readWhole(l.value); ok {
				b.place(s, from, conv)
			}
			continue
		}
		value := strings.TrimSpace(l.value)
		if l.conditional || value == "" || len(scanActions(value)) > 0 || strings.ContainsAny(value[:1], "!{[&*|>") {
			continue
		}
		b.when(from, unquote(value))
	}
}

// instanceOutput matches the shape of a reference to an output of the
// instance's module, the instance's key written as symbolMark: its output,
// and the output of the module it calls where it names one through it.
var instanceOutput = regexp.MustCompile(`^\$\{module\.\x00\.([A-Za-z_][A-Za-z0-9_-]*)` +
	`(?:\.([A-Za-z_][A-Za-z0-9_-]*))?\}$`)

// providedOutput returns the path of the output of the instance's module a
// provides value publishes: the one reference to it that every branch of the
// value naming one is, read where branches the value writes are each another
// text.
func providedOutput(value string) ([]string, bool) {
	var refs [][]string
	segment := func(text string) {
		var b strings.Builder
		last := 0
		for _, s := range scanActions(text) {
			b.WriteString(text[last:s.start])
			last = s.end
			if canonicalAction(s.inner) == ".context.instance_id" {
				b.WriteString(symbolMark)
			} else {
				b.WriteString(actionMark)
			}
		}
		b.WriteString(text[last:])
		m := instanceOutput.FindStringSubmatch(strings.TrimSpace(b.String()))
		if m == nil {
			return
		}
		ref := []string{m[1]}
		if m[2] != "" {
			ref = append(ref, m[2])
		}
		if !slices.ContainsFunc(refs, func(r []string) bool { return slices.Equal(r, ref) }) {
			refs = append(refs, ref)
		}
	}
	last := 0
	for _, s := range scanActions(value) {
		if controlWord(s.inner) == "" {
			continue
		}
		segment(value[last:s.start])
		last = s.end
	}
	segment(value[last:])
	if len(refs) != 1 {
		return nil, false
	}
	return refs[0], true
}

// attributePath returns the attribute of the resource r a traversal written
// in r's module reads, its steps after the resource's type and name: a
// member's name, or an index in decimal or a key.
func (m tfModule) attributePath(f *tfFile, t hcl.Traversal, r *tfResource) ([]string, bool) {
	if m.referenced(f, t) != r || len(t) < 3 {
		return nil, false
	}
	var out []string
	for _, step := range t[2:] {
		switch s := step.(type) {
		case hcl.TraverseAttr:
			out = append(out, s.Name)
		case hcl.TraverseIndex:
			switch {
			case s.Key.Type() == cty.String:
				out = append(out, s.Key.AsString())
			case s.Key.Type() == cty.Number:
				out = append(out, s.Key.AsBigFloat().Text('f', -1))
			default:
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return out, true
}

// extractTerraform reads one terraform Recipe's row literally, evaluating no
// template, or returns why it gives none.
//
// A Recipe sourcing a module by a git-repository or terraform-module-oci
// source gives a row of the module form keyed by the module's registry
// address, which the repository the source names gives
// ([tfaddr.FromGitURL]) with its module_subdir: each input its valuesMapping
// writes as one action reading a value or an infra key whole is placed from
// the input's path, a discriminator it writes as a literal outside every
// branch is the When, each key the overrides header names a path for is
// placed from it, and each provides key publishing one output of the
// instance's module whole is placed at that output.
//
// Any other Recipe is read from its configuration: the files of the local
// directory it sources, copied as they are, and its artifacts, the files of
// each ArtifactLibrary body it imports among them, written as templates whose
// actions are masked before they are parsed ([maskTemplate]). A local
// directory whose main.tf is the hub's pass-through module writes no resource
// and hands its input to the body it calls. Of the resources the files write,
// the one no other resource references is the instance, and its type the
// row's key: a resource a template range repeats is none, and of several of
// one type, the one written outside every template branch is. Each value the
// instance's resource writes as one action reading a value or an infra key
// whole, outside a range, is placed from its path, and so is each value of a
// resource a read-in rule ([tfimport.ReadIns]) reads in place of the
// instance's reference to it, beneath the referencing attribute; each other
// resource is left out, and the note says so. A discriminator the instance's
// resource writes as a literal outside every template branch is the When. A
// valuesMapping input the module reads whole at an attribute of those
// resources is placed, or gives the When, from that attribute; each key the
// overrides header names a path for is placed from it, a module input's path
// read at the attribute reading it; and each provides key publishing one
// output of the instance's module, or of the body the pass-through module
// calls, whose value is an attribute of the instance's resource, is placed at
// that attribute.
func extractTerraform(r recipeSource) (cloudimport.Row, string, error) {
	if r.source != nil {
		return terraformModuleRow(r)
	}
	mod, why := readTerraformModule(r)
	if why != "" {
		return cloudimport.Row{}, why, nil
	}
	key, why := mod.instanceResource()
	if key == nil {
		return cloudimport.Row{}, why, nil
	}
	if strings.Contains(key.typ, "{{") {
		return cloudimport.Row{}, "the resource " + key.String() + " that is its instance names no literal type", nil
	}
	passThrough := false
	if text, ok := r.hubFiles[passThroughFile]; ok {
		passThrough = slices.ContainsFunc(r.module, func(f fragment) bool { return f.name == "main.tf" && f.text == text })
	}
	b := tfRowBuilder{row: cloudimport.Row{Key: key.typ, Form: cloudimport.FormRaw}, mod: mod}
	if len(r.module) > 0 {
		b.varDirs = []string{"."}
		if passThrough {
			b.varDirs = append(b.varDirs, passThroughBody)
		}
	}
	children, at := b.readResource(key, nil, true)
	for i, c := range children {
		b.readResource(c, at[i], false)
	}

	var notes []string
	for _, res := range mod.resources {
		if res != key && !slices.Contains(children, res) {
			notes = append(notes, "the resource "+res.String()+" is read as part of the instance by no read-in "+
				"rule, so the row leaves it out")
		}
	}
	if len(r.module) > 0 {
		b.readValuesMapping(r.valuesMapping, b.translate)
	}
	for _, p := range headerOverrides(r.document) {
		if len(r.module) > 0 && !b.writes(p.From) && b.declares(p.From[0]) {
			from, ok := b.translate(p.From)
			if !ok {
				notes = append(notes, "the override "+p.Key+" passes the module input "+strings.Join(p.From, ".")+
					", which no attribute of the instance reads whole, so the row leaves it out")
				continue
			}
			p.From = from
		}
		b.row.Overrides = append(b.row.Overrides, p)
	}
	for _, e := range r.provides {
		ref, ok := providedOutput(e.value)
		if !ok {
			continue
		}
		dir := "."
		if len(ref) == 2 {
			if !passThrough || ref[0] != passThroughBody {
				continue
			}
			dir, ref = passThroughBody, ref[1:]
		}
		out, ok := mod.outputs[dir+"\x00"+ref[0]]
		if !ok {
			continue
		}
		value := out.value
		if w, ok := value.(*hclsyntax.TemplateWrapExpr); ok {
			value = w.Wrapped
		}
		t, ok := value.(*hclsyntax.ScopeTraversalExpr)
		if !ok {
			continue
		}
		if from, ok := mod.attributePath(out.file, t.Traversal, key); ok {
			b.row.Provides = append(b.row.Provides, cloudimport.Placement{From: from, Key: e.key})
		}
	}
	return b.row, strings.Join(notes, "; "), nil
}

// terraformModuleRow reads the row of a Recipe sourcing a module, as
// extractTerraform says.
func terraformModuleRow(r recipeSource) (cloudimport.Row, string, error) {
	kind, _ := r.source["kind"].(string)
	if kind != "git-repository" && kind != "terraform-module-oci" {
		return cloudimport.Row{}, fmt.Sprintf("it sources a %q source, which names no Terraform module", kind), nil
	}
	url, _ := r.source["url"].(string)
	subdir, _ := r.source["module_subdir"].(string)
	key, ok := tfaddr.FromGitURL(url, strings.Trim(subdir, "/"))
	if !ok {
		return cloudimport.Row{}, fmt.Sprintf("it sources %q, which is no GitHub repository named "+
			"terraform-<provider>-<name>, so no module address keys its row", url), nil
	}
	b := tfRowBuilder{row: cloudimport.Row{Key: key, Form: cloudimport.FormModule}}
	b.readValuesMapping(r.valuesMapping, func(p []string) ([]string, bool) { return p, true })
	b.row.Overrides = headerOverrides(r.document)
	for _, e := range r.provides {
		if ref, ok := providedOutput(e.value); ok && len(ref) == 1 {
			b.row.Provides = append(b.row.Provides, cloudimport.Placement{From: ref, Key: e.key})
		}
	}
	var notes []string
	if mod, why := readTerraformModule(r); why == "" && len(mod.resources) > 0 {
		notes = append(notes, "its artifacts write "+tfResourceList(mod.resources)+" beside the module it "+
			"sources, which the row, keyed by the module, leaves out")
	}
	return b.row, strings.Join(notes, "; "), nil
}

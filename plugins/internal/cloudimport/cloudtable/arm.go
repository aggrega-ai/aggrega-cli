// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// entry is one key of a Recipe's provides with the template text it publishes.
type entry struct{ key, value string }

// recipeSource is what the generator hands an extractor of one Recipe, every
// import already resolved: its name, its path beneath the hub's root, the
// Component it implements less its version, its source, its locals, its
// provides in the order written, its valuesMapping text, the text of each of
// its artifacts' files in the order written, and its document as written,
// comments included.
type recipeSource struct {
	name, path, component string
	source                map[string]any
	locals                map[string]string
	provides              []entry
	valuesMapping         string
	fragments             []string
	document              string
}

// avmRegistry is where the Azure Verified Modules programme publishes its
// Bicep modules, and avmResource the prefix of a resource module's path.
const (
	avmRegistry = "oci://mcr.microsoft.com/bicep/"
	avmResource = "avm/res/"
)

// nestedDeployment is the type of the resource that wraps a template inside
// another, and which the importer flattens into the resources it holds.
const nestedDeployment = "Microsoft.Resources/deployments"

// foldedName is the action writing the instance's symbol: its name folded into
// words, as the provider names the resource it wraps a sourced module in.
const foldedName = "capitalizedWords .context.instance_id"

// resourceType matches a resource type written whole: a provider namespace and
// at least one type segment.
var resourceType = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(\.[A-Za-z0-9]+)+(/[A-Za-z0-9]+)+$`)

// extractARM reads one arm Recipe's row literally, evaluating no template, or
// returns why it gives none.
//
// The row's key is the module the Recipe sources where it is a resource module
// of the Azure Verified Modules programme — the module_subdir under avm/res/ a
// git-repository source names, or the path under avm/res/ an oci reference to
// the programme's registry names — and the row is of the module form: the
// literal kind the valuesMapping writes outside any branch is its When; each
// parameter the valuesMapping writes as one action reading a value or an infra
// key whole is placed under that key; each key the overrides header names a
// parameter path for is placed from it; and each provides key publishing one
// module output read off the instance's symbol whole is placed at the output's
// path: outputs, the output's name, value.
//
// A Recipe sourcing nothing is read from its fragments, the files of its
// artifacts: the resource a fragment's resources block keys by the instance's
// symbol alone — its name folded into words, or the locals entry or variable
// holding that — is the instance. Written as lines, its literal type is the
// key, unless it is read as existing; the literal kind written beneath it
// outside any branch is the When, its properties written as one action reading
// a value or an infra key whole are placed under that key, the overrides
// header's paths are placed as for a module, and a provides key publishing the
// resource's identifier — resourceId of its own type, or the id a reference to
// its symbol reads — is placed at an empty path. Written with toJson, the
// resource is built with dict: the key is the one resource type the fragment's
// dict calls build that is not the resource wrapping a template, a child of another, a
// relative child type or a resource read as existing, and the literal kind
// those calls give it is the When; nothing else of it is read.
//
// A value wrapped in a branch, composed with other text or calls, or read off
// another resource is not one pointer form, and gives nothing; neither does a
// parameter or property written inside a range.
func extractARM(r recipeSource) (cloudimport.Row, string) {
	symbols := symbolSpellings(r.locals)
	if r.source != nil {
		module, why := avmModule(r.source)
		if module == "" {
			return cloudimport.Row{}, why
		}
		doc := readTemplateYAML(r.valuesMapping)
		row := cloudimport.Row{Key: module, Form: cloudimport.FormAVM, When: kindWhen(doc.leaves)}
		row.Values, row.Infra = wholeValues(doc, doc.leaves)
		row.Overrides = headerOverrides(r.document)
		row.Provides = moduleOutputs(r.provides, symbols)
		return row, ""
	}
	if len(r.fragments) == 0 {
		return cloudimport.Row{}, "it sources no module and writes no fragment"
	}
	var whys []string
	for _, text := range r.fragments {
		res := fragmentResource(text, symbols)
		if res.key == "" {
			whys = append(whys, res.why)
			continue
		}
		row := cloudimport.Row{Key: res.key, Form: cloudimport.FormRaw, When: res.when}
		if res.dict {
			return row, ""
		}
		row.Values, row.Infra = res.values, res.infra
		row.Overrides = headerOverrides(r.document)
		row.Provides = resourceIdentifiers(r.provides, symbols, row.Key)
		return row, ""
	}
	return cloudimport.Row{}, strings.Join(whys, "; ")
}

// avmModule returns the path of the Azure Verified Modules resource module
// source names, or why it names none.
func avmModule(source map[string]any) (string, string) {
	kind, _ := source["kind"].(string)
	switch kind {
	case "git-repository":
		sub, _ := source["module_subdir"].(string)
		sub = strings.TrimSuffix(sub, "/")
		if strings.HasPrefix(sub, avmResource) && len(sub) > len(avmResource) {
			return sub, ""
		}
		return "", fmt.Sprintf("it sources the module directory %q, which is not an Azure Verified Modules "+
			"resource module", sub)
	case "bicep-module-oci":
		url, _ := source["url"].(string)
		if rest, ok := strings.CutPrefix(url, avmRegistry); ok && strings.HasPrefix(rest, avmResource) &&
			len(rest) > len(avmResource) {
			return strings.TrimSuffix(rest, "/"), ""
		}
		return "", fmt.Sprintf("it sources %q, which is not an Azure Verified Modules resource module", url)
	}
	return "", fmt.Sprintf("it sources a %q source, which is not an Azure Verified Modules module", kind)
}

// symbolSpellings returns the normalized actions writing the instance's
// symbol: its name folded into words, and the locals entry symbol where it
// holds that action alone.
func symbolSpellings(locals map[string]string) []string {
	out := []string{foldedName}
	if isSymbol(locals["symbol"], out) {
		out = append(out, ".locals.symbol")
	}
	return out
}

// declaration matches an action declaring or assigning a variable, capturing
// the variable and what it is given.
var declaration = regexp.MustCompile(`^(\$[A-Za-z_][A-Za-z0-9_]*) :?= (.+)$`)

// withVariables returns symbols and each variable a fragment's actions give a
// symbol spelling, so the fragment's {{ $v }} writes the symbol too.
func withVariables(symbols, actions []string) []string {
	out := slices.Clone(symbols)
	for _, a := range actions {
		if m := declaration.FindStringSubmatch(normalize(a)); m != nil && slices.Contains(out, m[2]) &&
			!slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

// isSymbol reports whether text is one action writing a symbol spelling and
// nothing else.
func isSymbol(text string, symbols []string) bool {
	text = strings.TrimSpace(text)
	spans := scanActions(text)
	return len(spans) == 1 && spans[0].start == 0 && spans[0].end == len(text) &&
		slices.Contains(symbols, normalize(spans[0].inner))
}

// resource is what a fragment says of the resource it keys by the instance's
// symbol: its key, its When and its whole values and infra, and whether it is
// built with dict; or why the fragment says none.
type resource struct {
	key           string
	when          []cloudimport.Pred
	values, infra []cloudimport.Placement
	dict          bool
	why           string
}

// fragmentResource reads the resource a fragment keys by the instance's symbol
// alone.
func fragmentResource(text string, symbols []string) resource {
	doc := readTemplateYAML(text)
	symbols = withVariables(symbols, doc.actions)
	var beneath []leaf
	var whole *leaf
	empty := false
	for i, l := range doc.leaves {
		if len(l.path) == 1 && l.path[0] == "resources" && strings.TrimSpace(l.value) == "{}" {
			empty = true
		}
		if len(l.path) < 2 || l.path[0] != "resources" || !isSymbol(l.path[1], symbols) {
			continue
		}
		if len(l.path) == 2 {
			whole = &doc.leaves[i]
			continue
		}
		beneath = append(beneath, leaf{path: l.path[2:], value: l.value, at: l.at, conditional: l.conditional})
	}
	switch {
	case whole != nil:
		spans := scanActions(strings.TrimSpace(whole.value))
		if len(spans) != 1 || !strings.HasPrefix(normalize(spans[0].inner), "toJson ") {
			return resource{why: "its fragment writes the resource keyed by the instance's symbol as " +
				fmt.Sprintf("%q", whole.value) + ", neither lines nor a dict written with toJson"}
		}
		return dictResource(doc)
	case len(beneath) > 0:
		return lineResource(doc, beneath)
	case empty:
		return resource{why: "its fragment writes no resource"}
	}
	return resource{why: "its fragment keys no resource by the instance's symbol alone"}
}

// lineResource reads a resource of doc written as lines, its leaves' paths
// beneath the resource.
func lineResource(doc templateDoc, leaves []leaf) resource {
	var typ string
	for _, l := range leaves {
		switch {
		case len(l.path) == 1 && l.path[0] == "existing" && unquote(strings.TrimSpace(l.value)) == "true":
			return resource{why: "the resource its fragment keys by the instance's symbol is read as existing"}
		case len(l.path) == 1 && l.path[0] == "type" && !l.conditional:
			typ = unquote(strings.TrimSpace(l.value))
		}
	}
	if !resourceType.MatchString(typ) {
		return resource{why: fmt.Sprintf("the resource its fragment keys by the instance's symbol writes the "+
			"type %q, which is no literal resource type", typ)}
	}
	out := resource{key: typ, when: kindWhen(leaves)}
	out.values, out.infra = wholeValues(doc, leaves)
	return out
}

// dictCall is the literal arguments of one dict call: each key written as a
// string mapped to the string or true it is given.
type dictCall map[string]string

// dictResource reads the resource a fragment builds with dict calls.
func dictResource(doc templateDoc) resource {
	if doc.root == nil {
		return resource{why: "its fragment does not parse as a template"}
	}
	var calls []dictCall
	eachCommand(doc.root, func(args []argument) {
		if len(args) == 0 || args[0].kind != argIdentifier || args[0].text != "dict" {
			return
		}
		call := dictCall{}
		for i := 1; i+1 < len(args); i += 2 {
			if args[i].kind == argString && (args[i+1].kind == argString || args[i+1].kind == argTrue) {
				call[args[i].text] = args[i+1].text
			}
		}
		calls = append(calls, call)
	})
	var types []string
	for _, c := range calls {
		t := c["type"]
		if !resourceType.MatchString(t) || strings.EqualFold(t, nestedDeployment) || c["existing"] == "true" ||
			slices.ContainsFunc(types, func(s string) bool { return strings.EqualFold(s, t) }) {
			continue
		}
		types = append(types, t)
	}
	var kept []string
	for _, t := range types {
		child := slices.ContainsFunc(types, func(parent string) bool {
			return strings.HasPrefix(strings.ToLower(t), strings.ToLower(parent)+"/")
		})
		if !child {
			kept = append(kept, t)
		}
	}
	switch len(kept) {
	case 0:
		return resource{why: "its fragment builds no resource with a literal type of its own"}
	case 1:
	default:
		return resource{why: "its fragment builds " + strings.Join(kept, " and ") + " with dict, and its text " +
			"does not say which is the instance"}
	}
	key := kept[0]
	kind, agreed := "", true
	for _, c := range calls {
		if !strings.EqualFold(c["type"], key) || c["existing"] == "true" {
			continue
		}
		k, ok := c["kind"]
		if !ok || kind != "" && k != kind {
			agreed = false
		}
		kind = k
	}
	out := resource{key: key, dict: true}
	if agreed && kind != "" {
		out.when = []cloudimport.Pred{{Path: []string{"kind"}, Op: cloudimport.OpEquals, Value: kind}}
	}
	return out
}

// kindWhen returns the When a literal kind written outside any branch gives,
// none where no such kind is written.
func kindWhen(leaves []leaf) []cloudimport.Pred {
	for _, l := range leaves {
		if len(l.path) != 1 || l.path[0] != "kind" || l.conditional {
			continue
		}
		v := unquote(strings.TrimSpace(l.value))
		if v == "" || strings.Contains(v, "{{") {
			continue
		}
		return []cloudimport.Pred{{Path: []string{"kind"}, Op: cloudimport.OpEquals, Value: v}}
	}
	return nil
}

// wholeValues returns each leaf of doc written as one action reading a value
// of the Component whole, placed under that value's key, and each written as
// one action reading an infra key whole other than an override, placed under
// that key, both in the order written. A value written where the dot is not
// the data the template is handed — inside a with or a range — reads no value
// of the Component, and a path written as two keys gives neither.
func wholeValues(doc templateDoc, leaves []leaf) (values, infra []cloudimport.Placement) {
	var all []cloudimport.Placement
	var pillar []bool
	for _, l := range leaves {
		field, ok := wholeField(l.value)
		if !ok || !doc.readsData(l) {
			continue
		}
		var key string
		isValue := false
		switch {
		case strings.HasPrefix(field, ".values."):
			key, isValue = strings.TrimPrefix(field, ".values."), true
		case strings.HasPrefix(field, ".infra.") && !strings.HasPrefix(field, ".infra.overrides."):
			key = strings.TrimPrefix(field, ".infra.")
		default:
			continue
		}
		p := cloudimport.Placement{From: slices.Clone(l.path), Key: key}
		at := slices.IndexFunc(all, func(o cloudimport.Placement) bool { return slices.Equal(o.From, p.From) })
		switch {
		case at < 0:
			all, pillar = append(all, p), append(pillar, isValue)
		case all[at].Key != key || pillar[at] != isValue:
			all[at].Key = ""
		}
	}
	for i, p := range all {
		switch {
		case p.Key == "":
		case pillar[i]:
			values = append(values, p)
		default:
			infra = append(infra, p)
		}
	}
	return values, infra
}

// moduleOutput matches the one pointer form a module Recipe's provides key
// takes: a module output read whole off the instance's symbol, the symbol
// written as symbolMark.
var moduleOutput = regexp.MustCompile(`^\[reference\('\x00'\)\.outputs\.([A-Za-z_][A-Za-z0-9_]*)\.value\]$`)

// moduleOutputs returns each provides key publishing one module output read
// whole off the instance's symbol, placed at the output's path.
func moduleOutputs(provides []entry, symbols []string) []cloudimport.Placement {
	var out []cloudimport.Placement
	for _, e := range provides {
		shape, ok := shapeOf(e.value, symbols)
		if !ok {
			continue
		}
		if m := moduleOutput.FindStringSubmatch(shape); m != nil {
			out = append(out, cloudimport.Placement{From: []string{"outputs", m[1], "value"}, Key: e.key})
		}
	}
	return out
}

// resourceID and referenceID match the pointer forms publishing a resource's
// identifier: resourceId of a type, capturing it, and the id a reference to the
// instance's symbol, written as symbolMark, reads.
var (
	resourceID  = regexp.MustCompile(`^\[resourceId\('([^']*)'\s*(,.*)?\)\]$`)
	referenceID = regexp.MustCompile(`^\[reference\('\x00'(\s*,[^\x00\x01()]*)?\)\.id\]$`)
)

// resourceIdentifiers returns each provides key publishing the identifier of
// the resource of type key, placed at an empty path.
func resourceIdentifiers(provides []entry, symbols []string, key string) []cloudimport.Placement {
	var out []cloudimport.Placement
	for _, e := range provides {
		shape, ok := shapeOf(e.value, symbols)
		if !ok {
			continue
		}
		m := resourceID.FindStringSubmatch(shape)
		if m != nil && strings.EqualFold(m[1], key) || referenceID.MatchString(shape) {
			out = append(out, cloudimport.Placement{Key: e.key})
		}
	}
	return out
}

// symbolMark and actionMark stand, in a value's shape, for an action writing
// the instance's symbol and for any other action.
const (
	symbolMark = "\x00"
	actionMark = "\x01"
)

// overridesHeader is the comment line opening the block in which a Recipe
// lists the infra.overrides keys it reads.
const overridesHeader = "This Recipe reads from infra.overrides:"

// headerEntry matches one key of the overrides header and the description
// after its dash, which a key too long for its column writes on the next line.
var headerEntry = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)(?:\s+-\s+(.*))?$`)

// headerPath matches the path a description's parenthesis opens with.
var headerPath = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*(?:\[[0-9]+\]|\.[A-Za-z_$][A-Za-z0-9_$]*)*`)

// headerOverrides returns each key the document's overrides header names a
// path for, placed from that path, in the order listed. The block is the
// comment lines after its opening line, each key at one indentation, a key
// beneath a parent whose description ends in a colon two deeper, and every
// deeper line continuing the key before it; a comment line indented less, an
// empty one or a line of the document ends it. A key's path is what its
// description's first parenthesis opens with where a path ends it, or a
// semicolon or a colon follows it; a path a comma or words follow is a value
// translated, and gives nothing.
func headerOverrides(document string) []cloudimport.Placement {
	lines := strings.Split(document, "\n")
	start := slices.IndexFunc(lines, func(l string) bool {
		body, ok := strings.CutPrefix(strings.TrimSpace(l), "#")
		return ok && strings.TrimSpace(body) == overridesHeader
	})
	if start < 0 {
		return nil
	}
	var keys []*headerItem
	var parent *headerItem
	indent := -1
	for _, l := range lines[start+1:] {
		body, ok := strings.CutPrefix(strings.TrimSpace(l), "#")
		text := strings.TrimSpace(body)
		if !ok || text == "" {
			break
		}
		at := len(body) - len(strings.TrimLeft(body, " "))
		m := headerEntry.FindStringSubmatch(text)
		switch {
		case indent < 0 && m == nil:
			return nil
		case indent < 0 || at == indent:
			if m == nil {
				return placementsOf(keys)
			}
			indent = at
			k := &headerItem{key: m[1]}
			if m[2] != "" {
				k.desc = []string{m[2]}
			}
			keys, parent = append(keys, k), k
		case at < indent:
			return placementsOf(keys)
		case at == indent+2 && m != nil && parent != nil &&
			strings.HasSuffix(strings.Join(parent.desc, " "), ":"):
			k := &headerItem{key: parent.key + "." + m[1]}
			if m[2] != "" {
				k.desc = []string{m[2]}
			}
			keys = append(keys, k)
		default:
			if len(keys) == 0 {
				continue
			}
			last := keys[len(keys)-1]
			if len(last.desc) == 0 {
				text = strings.TrimSpace(strings.TrimPrefix(text, "-"))
			}
			last.desc = append(last.desc, text)
		}
	}
	return placementsOf(keys)
}

// headerItem is one key of the overrides header with the lines of its
// description.
type headerItem struct {
	key  string
	desc []string
}

// placementsOf returns each header key whose description names a path,
// placed from it.
func placementsOf(items []*headerItem) []cloudimport.Placement {
	var out []cloudimport.Placement
	for _, it := range items {
		if from := descriptionPath(strings.Join(it.desc, " ")); from != nil {
			out = append(out, cloudimport.Placement{From: from, Key: it.key})
		}
	}
	return out
}

// descriptionPath returns the path the description's first parenthesis opens
// with, where a path ends it or a semicolon or a colon follows it; nil
// otherwise.
func descriptionPath(desc string) []string {
	open := strings.IndexByte(desc, '(')
	if open < 0 {
		return nil
	}
	depth, end := 0, -1
	for i := open; i < len(desc) && end < 0; i++ {
		switch desc[i] {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				end = i
			}
		}
	}
	if end < 0 {
		return nil
	}
	inner := desc[open+1 : end]
	path := headerPath.FindString(inner)
	if path == "" {
		return nil
	}
	if rest := inner[len(path):]; rest != "" && rest[0] != ';' && rest[0] != ':' {
		return nil
	}
	path = strings.NewReplacer("[", ".", "]", "").Replace(path)
	return strings.Split(path, ".")
}

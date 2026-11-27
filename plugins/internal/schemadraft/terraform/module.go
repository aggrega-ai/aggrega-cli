// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

const (
	// moduleSource is the Recipe's source: the module directory beside it.
	moduleSource = "./module"
	// moduleFile is the module's one file, relative to the Recipe's directory.
	moduleFile = "module/main.tf.json"
	// thisResource names the module's one resource.
	thisResource = "this"
	// reservedSuffix follows a name Terraform reserves for a module argument.
	reservedSuffix = "_value"
	// iteratorPrefix opens the iterator of a dynamic block, followed by its depth.
	iteratorPrefix = "it"
	// secretMechanism is the inject mechanism a secret's attachment delivers
	// it on. It restates the mechanism the core declares on every secret slot
	// it drafts, which the core does not export, so the guard below reads
	// where the core's read does.
	secretMechanism = "env"

	azurermType     = "azurerm"
	azurermBoundary = "provider-azurerm.tf"
	// azurermFeatures is the provider block the azurerm provider requires in
	// the root configuration, with its features block, as the catalog's
	// azurerm Recipes write it.
	azurermFeatures = "provider \"azurerm\" {\n  features {}\n}\n"

	codeProviderVersionUnconstrained = "provider-version-unconstrained"
)

// shortSourceHosts are the registry hosts a provider source leaves out: the
// source hashicorp/aws names the same provider under Terraform and OpenTofu, so
// a draft from either composes in one root configuration with the catalog's
// Recipes.
var shortSourceHosts = []string{"registry.terraform.io", "registry.opentofu.org"}

// reservedVariables are the names Terraform reserves for the arguments of a
// module block itself, and provider, which OpenTofu reserves too: no module
// variable takes one.
var reservedVariables = []string{"count", "depends_on", "for_each", "lifecycle", "locals", "provider", "providers",
	"source", "version"}

// Module is one resource drafted as a local module.
type Module struct {
	// Provider is the provider the module requires; Version "" writes no
	// constraint and warns provider-version-unconstrained.
	Provider schemadraft.ProviderAddress
	Type     *schemadraft.Type
	// Inputs are top-level attributes and blocks of the resource, each pillar-
	// or inject-sourced and set from one module variable (VariableNames); a
	// member of a block is set from within its block's variable, and an input
	// at a deeper path from its own variable, inside the block an input at its
	// first segment writes.
	Inputs []schemadraft.Input
	// Outputs are attributes read back, one output each, at the top level of
	// the resource as the terraform-schema format drafts them. An output is
	// named after its attribute's path joined with underscores, so a path
	// naming a nested attribute is published too, numbered where the output of
	// another attribute holds that name (outputNames).
	Outputs []schemadraft.Provide
}

// WriteModule returns the Recipe's target parts for m: Source ./module; Files
// [module/main.tf.json]; ValuesMapping; Provides
// ${module.{{ .context.instance_id }}.<attr>}; for azurerm, BoundaryArtifacts
// provider-azurerm.tf, the provider block with its features block; and the
// Header lines saying how the module is set and pinned.
//
// module/main.tf.json is Terraform's JSON syntax with its keys sorted. Its
// terraform.required_providers names the provider by its type, from the source
// <namespace>/<type> for the Terraform and OpenTofu registries and from its
// full address for any other host, at Version where it is set. It declares one
// variable for each input, named by VariableName — taken first for the inputs
// whose names Terraform admits, so each of them keeps its own name — typed from
// the attribute (any for a block, a nested type, an object, a map, a tuple, a
// union or dynamic), null by default where the input is optional and sensitive
// for a secret. Its resource <type>.this sets each attribute to its variable
// and each block as a dynamic block over its variable: a map of blocks one for
// each key, labelled by it; any other nesting once for each item of a list and
// once for a single block, none for null. Each member of a block's content is
// read from the iterator of its depth with try(…, null), so an absent member
// leaves the argument unset; a member read back alone is not set, and a block
// the reader cut is left out and named in the Header. A resource whose name
// does not open with its provider's type is bound to that provider. An input at
// a path of more than one segment, such as a property a neighbour supplies, is
// set inside the dynamic block the input at its first segment writes, at its
// path, the Header naming its variable and the path: the segment after a block
// names the item of a list in decimal, the key of a map or the item 0 of a
// single block, in which alone the attribute is set, the other items keeping
// the value their block's variable gives it, or else a member of the block's
// content; the last segment names an attribute. An input of a type read from a
// source that writes the resource, whose properties are marked Written, is set
// at its path inside the object or list the source writes, each made where
// none is there, a segment of decimal digits naming an item of a list, and
// halts where the path or a prefix of it is set from one variable whole. Each output publishes an
// attribute read back, sensitive where the attribute is, under the name
// outputNames gives it.
//
// The valuesMapping sets one module argument for each input with a value: a
// required input always, an optional one where its source holds it, so one set
// to false, 0 or "" is written. An input at a deeper path halts where the
// optional input whose block it is set inside is not set, naming its path. A
// value is written with toJson, whole, a "${" in it written as "$${" but for a
// value a neighbour supplies; a values or infra input is rebuilt under the
// vendor's names, as schemadraft.Respeller writes it. A secret is the value
// its slot's attachment delivers on the env inject mechanism, under the key the
// attachment's values name; a slot whose attachment delivers nothing there
// halts naming the slot, and an optional slot holding no attachment leaves its
// variable unset. No value is written into the module.
//
// It warns provider-version-unconstrained where m names a provider at no
// version, naming the provider and where its version goes. A nested input is
// placed only inside a block another input writes: on one at a deeper path
// that no block of the module holds it halts with
// schemadraft.NestedInputRefused, drafting nothing, naming it and why.
func WriteModule(m Module) (importer.RecipeDraft, []aggrega.Diagnostic) {
	names, renamed := variableNames(m.Inputs)
	d := importer.RecipeDraft{
		Source: moduleSource,
		Header: []string{
			moduleFile + " beside this Recipe declares the resource as a Terraform module, with one variable for " +
				"each key above and one output for each attribute the provides below publish; valuesMapping sets " +
				"each variable whose key holds a value, and a variable left unset stays null, leaving its argument " +
				"unset.",
			"A pull admits this Recipe once aggrega pin has recorded the digest of " + moduleFile + " in the " +
				"entity.lock beside it: run aggrega pin on this Recipe after drafting it and after each change " +
				"to the module.",
		},
	}
	d.Header = append(d.Header, renamed...)

	w := &bodyWriter{}
	body := map[string]any{}
	variables := map[string]any{}
	var nested []int
	for i, in := range m.Inputs {
		variables[names[i]] = variable(in)
		if isNested(in) {
			nested = append(nested, i)
			continue
		}
		w.member(body, in.Property, "var."+names[i], 1)
	}
	var halts []aggrega.Diagnostic
	var placed []string
	for _, i := range nested {
		in := m.Inputs[i]
		if in.Property.Written {
			if why := setWritten(body, in.Property.Path, "var."+names[i]); why != "" {
				halts = append(halts, nestedRefused(m, in, why))
				continue
			}
			placed = append(placed, fmt.Sprintf("The module variable %s sets %s, inside %s.", names[i],
				dotted(in.Property.Path), in.Property.Path[0]))
			continue
		}
		if why := w.nested(body, m.Inputs, in, "var."+names[i]); why != "" {
			halts = append(halts, nestedRefused(m, in, why))
			continue
		}
		placed = append(placed, fmt.Sprintf("The module variable %s sets %s, inside the block %s.", names[i],
			dotted(in.Property.Path), in.Property.Path[0]))
	}
	if len(halts) > 0 {
		return importer.RecipeDraft{}, halts
	}
	for _, path := range w.omitted {
		d.Header = append(d.Header, fmt.Sprintf("The module does not set the block %s: it nests deeper than %d "+
			"levels, below which the draft does not read the vendor schema.", path, schemadraft.MaxDepth))
	}
	d.Header = append(d.Header, placed...)

	var diags []aggrega.Diagnostic
	doc := map[string]any{}
	a := m.Provider
	if a.Type != "" {
		required := map[string]any{"source": providerSource(a)}
		if a.Version != "" {
			required["version"] = a.Version
		} else {
			diags = append(diags, versionUnconstrained(m))
		}
		doc["terraform"] = map[string]any{"required_providers": map[string]any{a.Type: required}}
		if typePrefix(m.Type.Name) != a.Type {
			body["provider"] = a.Type
		}
		if a.Type == azurermType {
			d.BoundaryArtifacts = []importer.Entry{{Key: azurermBoundary, Value: azurermFeatures}}
		}
	}
	if len(variables) > 0 {
		doc["variable"] = variables
	}
	doc["resource"] = map[string]any{m.Type.Name: map[string]any{thisResource: body}}

	outputNamed, outputLines := outputNames(m.Outputs)
	d.Header = append(d.Header, outputLines...)
	outputs := map[string]any{}
	for i, o := range m.Outputs {
		name, path := outputNamed[i], strings.Join(attributeOf(o), ".")
		output := map[string]any{"value": "${" + m.Type.Name + "." + thisResource + "." + path + "}"}
		if sensitiveAt(m.Type.Properties, attributeOf(o)) {
			output["sensitive"] = true
		}
		outputs[name] = output
		d.Provides = append(d.Provides, importer.Entry{Key: o.Key,
			Value: "${module.{{ .context.instance_id }}." + name + "}"})
	}
	if len(outputs) > 0 {
		doc["output"] = outputs
	}
	if len(d.Provides) > 0 {
		d.Provides[0].Comment = []string{"Each value is the module's output of that name, which the root " +
			"configuration resolves where it declares the module; the String Pipe pipes it as written into a " +
			"consumer's context."}
	}

	d.Files = []importer.RecipeFile{{Path: moduleFile, Content: encode(doc)}}
	d.ValuesMapping = valuesMapping(m.Inputs, names)
	return d, diags
}

// VariableName is the module variable an input of the vendor name vendor
// takes, and so the module argument and the valuesMapping key setting it: the
// name as the kit writes a key, with underscores for its hyphens; where
// Terraform reserves that name for an argument of a module block (or OpenTofu
// does, as it does provider), the name followed by _value; then the kit's
// numeric suffix where a variable taken earlier holds it. used is the kit's
// record of the variables taken, in its own spelling, and records this one.
//
// The name an input takes depends on the order the inputs are named in:
// WriteModule names the inputs whose names Terraform admits first and the
// reserved ones after them, so with both source and source_value among the
// inputs, source takes source_value_2. A caller needing the variable of an
// input reads it from VariableNames rather than calling VariableName itself.
func VariableName(vendor string, used map[string]bool) string {
	base := underscored(importer.Slug(vendor))
	if slices.Contains(reservedVariables, base) {
		base += reservedSuffix
	}
	return underscored(importer.UniqueKey(used, base))
}

// VariableNames returns the module variable WriteModule declares for each of
// m.Inputs, in their order: the variable the module sets the input's attribute
// or block from, and the key the valuesMapping sets it under. An input at a path
// of more than one segment takes the variable VariableName gives its source's
// key, the property inject key of one a neighbour supplies, rather than its
// name.
func VariableNames(m Module) []string {
	names, _ := variableNames(m.Inputs)
	return names
}

// underscored writes the kit's hyphenated key with underscores, as a Terraform
// identifier.
func underscored(key string) string { return strings.ReplaceAll(key, "-", "_") }

// variableNames names the variable of each input, in the inputs' order: the
// inputs whose names Terraform admits take theirs first, so each keeps its own
// name, then the others. It returns beside them the Header line naming each
// variable that differs from its attribute's name, and why.
func variableNames(inputs []schemadraft.Input) ([]string, []string) {
	names := make([]string, len(inputs))
	used := map[string]bool{}
	reserved := func(in schemadraft.Input) bool {
		return slices.Contains(reservedVariables, underscored(importer.Slug(variableBase(in))))
	}
	for _, pass := range []bool{false, true} {
		for i, in := range inputs {
			if reserved(in) == pass {
				names[i] = VariableName(variableBase(in), used)
			}
		}
	}
	var lines []string
	for i, in := range inputs {
		if isNested(in) || names[i] == in.Property.Name {
			continue
		}
		why := "since another attribute's variable takes its name"
		if reserved(in) {
			why = "since Terraform reserves its name for an argument of a module block"
		}
		lines = append(lines, fmt.Sprintf("The module variable %s sets the attribute %s, %s.", names[i],
			in.Property.Name, why))
	}
	return names, lines
}

// variableBase is the name the variable of in is named after: the source's key
// of an input at a path of more than one segment, the property's name of any
// other.
func variableBase(in schemadraft.Input) string {
	if isNested(in) {
		return in.Source.Key
	}
	return in.Property.Name
}

// isNested reports whether in sits at a path of more than one segment, which
// the module sets inside a block another input writes.
func isNested(in schemadraft.Input) bool { return len(in.Property.Path) > 1 }

// dotted is path joined by dots.
func dotted(path []string) string { return strings.Join(path, ".") }

// variable is the module variable declaring in.
func variable(in schemadraft.Input) map[string]any {
	v := map[string]any{"type": variableType(in.Property)}
	if !in.Required {
		v["default"] = nil
	}
	if in.Secret || in.Property.Sensitive {
		v["sensitive"] = true
	}
	return v
}

// variableType is the type constraint of a variable taking p: a primitive, or a
// list of them, as p is one; any for every other shape, which Terraform converts
// where the resource reads it. An object is any too, since a typed object
// would require every member the vendor leaves optional.
func variableType(p *schemadraft.Property) string {
	if p.Block != nil || p.Cut != "" || len(p.Dropped) > 0 || len(p.Kinds) != 1 {
		return "any"
	}
	switch p.Kinds[0] {
	case schemadraft.KindString:
		return "string"
	case schemadraft.KindNumber, schemadraft.KindInteger:
		return "number"
	case schemadraft.KindBoolean:
		return "bool"
	case schemadraft.KindArray:
		if p.Items != nil {
			if item := variableType(p.Items); item != "any" {
				return "list(" + item + ")"
			}
		}
	}
	return "any"
}

// bodyWriter writes the body of the module's resource, recording each block it
// leaves out.
type bodyWriter struct {
	// omitted holds the dotted path of each block the reader cut.
	omitted []string
}

// member writes p into body, the resource's body or a block's content, read
// from the expression value: an attribute as the argument value, a block as a
// dynamic block over value whose iterator is named for depth, and a block the
// reader cut not at all.
func (w *bodyWriter) member(body map[string]any, p *schemadraft.Property, value string, depth int) {
	if p.Block == nil || p.Block.Attribute {
		body[p.Name] = "${" + value + "}"
		return
	}
	if p.Cut != "" {
		w.omitted = append(w.omitted, strings.Join(p.Path, "."))
		return
	}
	it := iteratorPrefix + strconv.Itoa(depth)
	block := map[string]any{"iterator": it}
	element := p
	switch p.Block.Nesting {
	case nestingMap:
		// merge passes a map whose blocks differ in shape, and reads null as
		// empty, where a conditional would have to unify the two.
		block["for_each"] = "${merge(" + value + ")}"
		block["labels"] = []any{"${" + it + ".key}"}
		element = p.Items
	case nestingList, nestingSet:
		element = p.Items
		block["for_each"] = "${" + value + "[*]}"
	default:
		// A splat reads a single value as a one-element list and null as an
		// empty one.
		block["for_each"] = "${" + value + "[*]}"
	}
	content := map[string]any{}
	if element != nil {
		for _, m := range element.Properties {
			if m.ReadOnly {
				continue
			}
			w.member(content, m, "try("+traverse(it+".value", m.Name)+", null)", depth+1)
		}
	}
	block["content"] = content
	dynamic, _ := body["dynamic"].(map[string]any)
	if dynamic == nil {
		dynamic = map[string]any{}
		body["dynamic"] = dynamic
	}
	dynamic[p.Name] = block
}

// setWritten sets the expression value at path inside body, the resource's
// body, for a property read from a source that writes the resource: the
// segments name the members of objects, and the items of lists by their index in
// decimal, each container made where none is there, so the object or list the
// source writes is written member by member. It returns "", or why it cannot:
// the path, or a prefix of it, is already set from one variable whole.
func setWritten(body map[string]any, path []string, value string) string {
	node, why := setAt(body, path, value)
	if why != "" {
		return why
	}
	_ = node // setAt changes body in place
	return ""
}

// setAt returns node with the expression value set at path below it, or why it
// cannot be. A nil node is made an object, or a list where path opens with an
// index; a list is extended with empty objects up to the index. A map node is
// changed in place.
func setAt(node any, path []string, value string) (any, string) {
	if len(path) == 0 {
		if m, ok := node.(map[string]any); node != nil && (!ok || len(m) > 0) {
			return nil, "it is set more than once"
		}
		return "${" + value + "}", ""
	}
	if index, err := strconv.Atoi(path[0]); err == nil && strings.Trim(path[0], "0123456789") == "" {
		list, ok := node.([]any)
		if node != nil && !ok {
			return nil, path[0] + " is an item of a list, and the module sets the container as an object or whole"
		}
		for len(list) <= index {
			list = append(list, map[string]any{})
		}
		item, why := setAt(list[index], path[1:], value)
		if why != "" {
			return nil, why
		}
		list[index] = item
		return list, ""
	}
	members, ok := node.(map[string]any)
	if node == nil {
		members, ok = map[string]any{}, true
	}
	if !ok {
		return nil, "the module sets the container of " + path[0] + " from one variable whole or as a list"
	}
	member, why := setAt(members[path[0]], path[1:], value)
	if why != "" {
		return nil, why
	}
	members[path[0]] = member
	return members, ""
}

// nested sets in, at a path of more than one segment, from the expression value
// inside the dynamic block the input at its first segment writes into body, as
// WriteModule describes, and returns "", or returns why it cannot.
func (w *bodyWriter) nested(body map[string]any, inputs []schemadraft.Input, in schemadraft.Input,
	value string) string {
	path := in.Property.Path
	i := slices.IndexFunc(inputs, func(c schemadraft.Input) bool { return !isNested(c) && c.Property.Name == path[0] })
	if i < 0 {
		return "no input of the module writes " + path[0]
	}
	p, content := inputs[i].Property, body
	var conditions []string
	for k := 0; ; {
		if p.Block == nil || p.Block.Attribute || p.Cut != "" {
			return dotted(path[:k+1]) + " is not a block the module writes"
		}
		dynamic, _ := content["dynamic"].(map[string]any)
		block, _ := dynamic[path[k]].(map[string]any)
		inner, _ := block["content"].(map[string]any)
		it, _ := block["iterator"].(string)
		if inner == nil {
			return dotted(path[:k+1]) + " is not a block the module writes"
		}
		element := p
		if p.Items != nil && p.Block.Nesting != nestingSingle && p.Block.Nesting != nestingGroup {
			element = p.Items
		}
		k++
		if condition, ok := itemCondition(p.Block.Nesting, it, path[k]); ok {
			conditions = append(conditions, condition)
			k++
		}
		if k == len(path) {
			return dotted(path) + " names an item of a block, not an attribute in it"
		}
		j := slices.IndexFunc(element.Properties, func(m *schemadraft.Property) bool {
			return m.Name == path[k] && !m.ReadOnly
		})
		if j < 0 {
			return fmt.Sprintf("the block %s holds no attribute %s the module sets", dotted(path[:k]), path[k])
		}
		m := element.Properties[j]
		if k < len(path)-1 {
			p, content = m, inner
			continue
		}
		if m.Block != nil && !m.Block.Attribute {
			return dotted(path) + " is a block, not an attribute"
		}
		expression := value
		if len(conditions) > 0 {
			kept := "null"
			if text, ok := inner[m.Name].(string); ok {
				kept = strings.TrimSuffix(strings.TrimPrefix(text, "${"), "}")
				if strings.Contains(kept, " ? ") {
					kept = "(" + kept + ")"
				}
			}
			expression = strings.Join(conditions, " && ") + " ? " + value + " : " + kept
		}
		inner[m.Name] = "${" + expression + "}"
		return ""
	}
}

// itemCondition is the condition holding for the item segment names of a
// dynamic block of the nesting given, whose iterator is it, where segment names
// an item: an index in decimal of a list, any key of a map, and 0 of a single
// block; ok is false where segment names no item.
func itemCondition(nesting, it, segment string) (string, bool) {
	index, err := strconv.Atoi(segment)
	isIndex := err == nil && strings.Trim(segment, "0123456789") == ""
	switch nesting {
	case nestingList:
		if isIndex {
			return it + ".key == " + strconv.Itoa(index), true
		}
	case nestingMap:
		return it + ".key == " + strconv.Quote(segment), true
	case nestingSingle, nestingGroup:
		if segment == "0" {
			return it + ".key == 0", true
		}
	}
	return "", false
}

// nestedRefused halts on the input in of m, at a path of more than one segment,
// which the module cannot set, saying why.
func nestedRefused(m Module, in schemadraft.Input, why string) aggrega.Diagnostic {
	return schemadraft.NestedInputRefused(m.Type, in, why+"; the module sets an input at a nested path only "+
		"inside a block another input of the module writes")
}

// identifier matches a name Terraform reads as an attribute after a dot.
var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// traverse is the expression reading the attribute name of expr.
func traverse(expr, name string) string {
	if identifier.MatchString(name) {
		return expr + "." + name
	}
	return expr + "[" + strconv.Quote(name) + "]"
}

// providerSource is the source a provider is required from: namespace/type for
// a provider of the Terraform or OpenTofu registry, its full address for any
// other host.
func providerSource(a schemadraft.ProviderAddress) string {
	var parts []string
	if !slices.Contains(shortSourceHosts, a.Host) {
		parts = append(parts, a.Host)
	}
	parts = append(parts, a.Namespace, a.Type)
	return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), "/")
}

// typePrefix is the provider Terraform takes a resource of the type name to
// belong to: its name up to the first underscore.
func typePrefix(name string) string {
	if i := strings.Index(name, "_"); i >= 0 {
		return name[:i]
	}
	return name
}

// versionUnconstrained warns that m requires its provider at no version.
func versionUnconstrained(m Module) aggrega.Diagnostic {
	return aggrega.Diagnostic{
		Severity: aggrega.SeverityWarn,
		Code:     codeProviderVersionUnconstrained,
		Message: fmt.Sprintf("no version of the provider %s is known, so the module drafted for %s requires it "+
			"without a version constraint: set terraform.required_providers.%s.version in the %s beside the "+
			"Recipe, then run aggrega pin on the Recipe", providerSource(m.Provider), m.Type.Name, m.Provider.Type,
			moduleFile),
		SourceRef: m.Type.Source,
	}
}

// attributeOf is the attribute path o publishes, or its key where it names none.
func attributeOf(o schemadraft.Provide) []string {
	if len(o.Attribute) == 0 {
		return []string{o.Key}
	}
	return o.Attribute
}

// outputName is the name of the output publishing o where no other output
// holds it: its attribute path joined by underscores.
func outputName(o schemadraft.Provide) string { return strings.Join(attributeOf(o), "_") }

// outputNames names the output of each of outputs, in their order. The outputs
// of top-level attributes are named first, each by outputName, so each keeps
// its attribute's own name; a nested path whose joined name an output named
// before it holds takes the first numeric suffix free, _2 onward, rather than
// overwriting that output. It returns beside them the Header line naming each
// output whose name is not its path's joined name, and why.
//
// The suffix deliberately repeats the kit's numeric suffix (importer.UniqueKey)
// rather than calling it: the kit rewrites a name into its own key spelling,
// lowercase and hyphenated, and an output keeps its attribute path's spelling.
func outputNames(outputs []schemadraft.Provide) ([]string, []string) {
	names := make([]string, len(outputs))
	used := map[string]bool{}
	for _, nested := range []bool{false, true} {
		for i, o := range outputs {
			if (len(attributeOf(o)) > 1) != nested {
				continue
			}
			base := outputName(o)
			name := base
			for n := 2; used[name]; n++ {
				name = fmt.Sprintf("%s_%d", base, n)
			}
			used[name] = true
			names[i] = name
		}
	}
	var lines []string
	for i, o := range outputs {
		if base := outputName(o); names[i] != base {
			lines = append(lines, fmt.Sprintf("The module output %s publishes the attribute %s, since the output "+
				"of another attribute takes the name %s.", names[i], strings.Join(attributeOf(o), "."), base))
		}
	}
	return names, lines
}

// sensitiveAt reports whether the property at path among props, through the
// members of objects, is sensitive.
func sensitiveAt(props []*schemadraft.Property, path []string) bool {
	for _, p := range props {
		if p.Name != path[0] {
			continue
		}
		if len(path) == 1 {
			return p.Sensitive
		}
		return sensitiveAt(p.Properties, path[1:])
	}
	return false
}

// encode writes doc as Terraform's JSON syntax: indented, its keys sorted, and
// no character escaped that JSON leaves plain.
func encode(doc map[string]any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(doc) // a document of maps, lists, strings, booleans and nulls always encodes
	return b.String()
}

// valuesMapping is the valuesMapping setting the variable names[i] from each
// input i.
func valuesMapping(inputs []schemadraft.Input, names []string) string {
	var lines []string
	for i, in := range inputs {
		key := yamlKey(names[i])
		guard := blockGuard(inputs, in)
		switch {
		case in.Source.Pillar == schemadraft.PillarSecret:
			lines = append(append(lines, guard...), secretLines(in, names[i], key)...)
		case in.Required:
			lines = append(append(lines, guard...), key+": "+inputValue(in))
		default:
			lines = append(append(append(lines, "{{- if "+in.Source.Present()+" }}"), guard...),
				key+": "+inputValue(in), "{{- end }}")
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// blockGuard is the lines halting where in, at a path of more than one segment,
// is set inside the block of an optional input that is not set, naming in's
// path; none for any other in.
func blockGuard(inputs []schemadraft.Input, in schemadraft.Input) []string {
	if !isNested(in) {
		return nil
	}
	i := slices.IndexFunc(inputs, func(c schemadraft.Input) bool {
		return !isNested(c) && c.Property.Name == in.Property.Path[0]
	})
	if i < 0 || inputs[i].Required {
		return nil
	}
	return []string{
		"{{- if not " + inputs[i].Source.Present() + " }}",
		"{{- fail " + strconv.Quote(fmt.Sprintf("the module sets %s inside the block %s, and %s is not set: set it, "+
			"holding the item the path names", dotted(in.Property.Path), in.Property.Path[0], in.Property.Path[0])) + " }}",
		"{{- end }}",
	}
}

// inputValue is the template text writing in's value as JSON: a values or infra
// input rebuilt under the vendor's names, any other as it is read, each with a
// `${` in it written as `$${` but for a value a neighbour supplies.
func inputValue(in schemadraft.Input) string {
	switch in.Source.Pillar {
	case schemadraft.PillarValues, schemadraft.PillarInfra:
		var r schemadraft.Respeller
		statements, value := r.Value(in.Property, in.Source.Template())
		return statements + "{{ toJson " + value + escapeInterpolation + " }}"
	case schemadraft.PillarInject:
		return "{{ toJson " + in.Source.Template() + " }}"
	}
	return "{{ toJson " + in.Source.Template() + escapeInterpolation + " }}"
}

// escapeInterpolation is the template pipeline step writing a dollar sign and
// brace of a value as two dollar signs and the brace, as the catalog's Recipes
// write the text they read: the root configuration then reads the value as the
// author wrote it, never as a reference. A value a neighbour supplies is not
// escaped, since its references stay references.
const escapeInterpolation = ` | replace "${" "$${"`

// secretLines set the variable v, written as key, to the secret in: the value
// the attachment on its slot delivers on the env mechanism under the key its
// values name, read as the core reads a secret. A slot whose attachment
// delivers nothing there halts naming it; an optional slot is read inside it,
// so one holding no attachment sets nothing.
func secretLines(in schemadraft.Input, v, key string) []string {
	slot := in.Source.Key
	fail := "{{- fail " + strconv.Quote(fmt.Sprintf("the attachment on the featuresSchema slot %s delivers no "+
		"value on the %s inject mechanism under the key its values name, and the module variable %s takes the "+
		"value delivered there: attach to the slot %s a secret Feature delivering its value on %s, such as "+
		"core/secret-value", slot, secretMechanism, v, slot, secretMechanism)) + " }}"
	if in.Required {
		delivered := "(optional (default dict .context.inject) " + strconv.Quote(secretMechanism) + " dict)"
		return []string{
			"{{- if not (hasKey " + delivered + " (index .features " + strconv.Quote(slot) + ` "values" "key")) }}`,
			fail,
			"{{- end }}",
			key + ": {{ toJson " + in.Source.Template() + " }}",
		}
	}
	delivered := "(optional (default dict $.context.inject) " + strconv.Quote(secretMechanism) + " dict)"
	return []string{
		`{{- with (optional . "features" dict).` + slot + " }}",
		"{{- if not (hasKey " + delivered + " .values.key) }}",
		fail,
		"{{- end }}",
		key + ": {{ toJson (index " + delivered + " .values.key) }}",
		"{{- end }}",
	}
}

// plainKey matches a variable name a YAML reader reads back as a string, but
// for the words yamlKey quotes.
var plainKey = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// yamlKey writes the variable name v as a key of the valuesMapping's output,
// which the compiler reads as YAML 1.2: plain, or quoted where the reader would
// take it for a boolean or a null.
func yamlKey(v string) string {
	if plainKey.MatchString(v) && v != "true" && v != "false" && v != "null" {
		return v
	}
	return importer.QuotedString(v)
}

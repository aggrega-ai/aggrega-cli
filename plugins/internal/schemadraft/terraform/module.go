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
	// Inputs are pillar- or inject-sourced; one module variable each.
	Inputs []schemadraft.Input
	// Outputs are the attributes read back; one output each.
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
// does not open with its provider's type is bound to that provider. Each
// output publishes an attribute read back, sensitive where the attribute is.
//
// The valuesMapping sets one module argument for each input with a value: a
// required input always, an optional one where its source holds it, so one set
// to false, 0 or "" is written. A value is written with toJson, whole; a values
// or infra input is rebuilt under the vendor's names, as schemadraft.Respell
// writes it. A secret is the value its slot's attachment delivers on the env
// inject mechanism, under the key the attachment's values name; a slot whose
// attachment delivers nothing there halts naming the slot, and an optional
// slot holding no attachment leaves its variable unset. No value is written
// into the module.
//
// It warns provider-version-unconstrained where m names a provider at no
// version, naming the provider and where its version goes.
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
	for i, in := range m.Inputs {
		variables[names[i]] = variable(in)
		w.member(body, in.Property, "var."+names[i], 1)
	}
	for _, path := range w.omitted {
		d.Header = append(d.Header, fmt.Sprintf("The module does not set the block %s: it nests deeper than %d "+
			"levels, below which the draft does not read the vendor schema.", path, schemadraft.MaxDepth))
	}

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

	outputs := map[string]any{}
	for _, o := range m.Outputs {
		name, path := outputName(o), strings.Join(attributeOf(o), ".")
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
// name as the kit writes a key, with
// underscores for its hyphens; where Terraform reserves that name for an
// argument of a module block (or OpenTofu does, as it does provider), the name
// followed by _value; then the kit's numeric suffix where a variable taken
// earlier holds it. used is the kit's record of the variables taken, in its own
// spelling, and records this one.
func VariableName(vendor string, used map[string]bool) string {
	base := underscored(importer.Slug(vendor))
	if slices.Contains(reservedVariables, base) {
		base += reservedSuffix
	}
	return underscored(importer.UniqueKey(used, base))
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
		return slices.Contains(reservedVariables, underscored(importer.Slug(in.Property.Name)))
	}
	for _, pass := range []bool{false, true} {
		for i, in := range inputs {
			if reserved(in) == pass {
				names[i] = VariableName(in.Property.Name, used)
			}
		}
	}
	var lines []string
	for i, in := range inputs {
		if names[i] == in.Property.Name {
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
		Severity: "warn",
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

// outputName is the output publishing o: its attribute path joined by
// underscores.
func outputName(o schemadraft.Provide) string { return strings.Join(attributeOf(o), "_") }

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
		switch {
		case in.Source.Pillar == schemadraft.PillarSecret:
			lines = append(lines, secretLines(in, names[i], key)...)
		case in.Required:
			lines = append(lines, key+": "+inputValue(in))
		default:
			lines = append(lines, "{{- if "+in.Source.Present()+" }}", key+": "+inputValue(in), "{{- end }}")
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// inputValue is the template text writing in's value as JSON: a values or infra
// input rebuilt under the vendor's names, any other as it is read.
func inputValue(in schemadraft.Input) string {
	if in.Source.Pillar == schemadraft.PillarValues || in.Source.Pillar == schemadraft.PillarInfra {
		return schemadraft.Respell(in.Property, in.Source.Template())
	}
	return "{{ toJson " + in.Source.Template() + " }}"
}

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

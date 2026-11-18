// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudformation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// The members of a provider schema the reader reads.
const (
	keyTypeName      = "typeName"
	keyDescription   = "description"
	keyProperties    = "properties"
	keyDefinitions   = "definitions"
	keyRequired      = "required"
	keyPrimary       = "primaryIdentifier"
	keyReadOnly      = "readOnlyProperties"
	keyWriteOnly     = "writeOnlyProperties"
	keyCreateOnly    = "createOnlyProperties"
	keyDeprecated    = "deprecatedProperties"
	pointerRoot      = "/properties/"
	definitionPrefix = "#/definitions/"
	itemsSegment     = "*"
	moduleSuffix     = "::MODULE"
)

// The members of a describe-type output the reader reads.
const (
	keyDescribedName = "TypeName"
	keySchema        = "Schema"
	keyKind          = "Type"
	keyProvisioning  = "ProvisioningType"
	kindResource     = "RESOURCE"
	nonProvisionable = "NON_PROVISIONABLE"
)

// list reads the one type each source of in describes: a provider schema, or
// a describe-type output whose Schema it parses as a second source. It reads a
// source's own members alone, leaving its properties to lift. A type the
// provider cannot realize is listed, and refused when it is lifted, so a
// directory holding one drafts the others.
func list(in aggrega.ImportInput, parse schemadraft.ParseFunc) ([]schemadraft.TypeRef, []aggrega.Diagnostic) {
	var refs []schemadraft.TypeRef
	var diags []aggrega.Diagnostic
	for _, src := range importer.Sources(in) {
		ref, halt := listSource(importer.Named(in, src), parse)
		if halt != nil {
			diags = append(diags, *halt)
			continue
		}
		refs = append(refs, ref)
	}
	return refs, diags
}

// listSource reads the type src describes. A provider schema is the body
// lifted; so is the provider schema a describe-type output holds, unless the
// output describes a type the provider refuses, whose body is the output.
func listSource(src aggrega.ImportSource, parse schemadraft.ParseFunc) (schemadraft.TypeRef, *aggrega.Diagnostic) {
	tree, err := parse(syntax, src.Bytes)
	if err != nil {
		d := importer.ParseFailed(src, formatName, err)
		return schemadraft.TypeRef{}, &d
	}
	top := members(tree)
	if _, ok := top[keyTypeName]; ok {
		name, missing := providerSchema(top)
		if missing != "" {
			return schemadraft.TypeRef{}, unreadable(src.Name, "it holds no "+missing)
		}
		return schemadraft.TypeRef{Name: name, Source: src.Name, Body: tree}, nil
	}
	if _, ok := top[keyDescribedName]; !ok {
		return schemadraft.TypeRef{}, unreadable(src.Name, "it holds no typeName string")
	}
	name, ok := schemadraft.JSONText(top[keyDescribedName])
	if !ok || name == "" {
		return schemadraft.TypeRef{}, unreadable(src.Name, "it holds no TypeName string")
	}
	if raw, ok := top[keyKind]; ok {
		if _, isText := schemadraft.JSONText(raw); !isText {
			return schemadraft.TypeRef{}, unreadable(src.Name, "it holds no Type string")
		}
	}
	if refusal(name, top) != "" {
		return schemadraft.TypeRef{Name: name, Source: src.Name, Body: tree}, nil
	}
	schema, ok := schemadraft.JSONText(top[keySchema])
	if !ok {
		return schemadraft.TypeRef{}, unreadable(src.Name, "it holds no Schema string")
	}
	inner, err := parse(syntax, []byte(schema))
	if err != nil {
		d := importer.ParseFailed(src, formatName, fmt.Errorf("its Schema member: %w", err))
		return schemadraft.TypeRef{}, &d
	}
	if _, missing := providerSchema(members(inner)); missing != "" {
		return schemadraft.TypeRef{}, unreadable(src.Name, "its Schema holds no "+missing)
	}
	return schemadraft.TypeRef{Name: name, Source: src.Name, Body: inner}, nil
}

// providerSchema returns the type name a provider schema's members give, or the
// member it lacks.
func providerSchema(top map[string]json.RawMessage) (name, missing string) {
	name, ok := schemadraft.JSONText(top[keyTypeName])
	if !ok || name == "" {
		return "", "typeName string"
	}
	if !schemadraft.IsJSONObject(top[keyProperties]) {
		return "", "properties object"
	}
	return name, ""
}

// refusal says why the provider cannot realize the type name, whose source's
// own members are top, or is empty where it can: a describe-type output of a
// Type other than RESOURCE or of the ProvisioningType NON_PROVISIONABLE, or a
// type whose name marks a module.
func refusal(name string, top map[string]json.RawMessage) string {
	if kind, ok := schemadraft.JSONText(top[keyKind]); ok && kind != kindResource {
		return "whose Type is " + kind + ", and the cloudformation provider realizes a RESOURCE type alone"
	}
	if provisioning, _ := schemadraft.JSONText(top[keyProvisioning]); provisioning == nonProvisionable {
		return "whose ProvisioningType is " + nonProvisionable + ", so CloudFormation cannot create it in a stack"
	}
	if strings.HasSuffix(name, moduleSuffix) {
		return "a MODULE type, and the cloudformation provider realizes a RESOURCE type alone"
	}
	return ""
}

// lift reads the type ref names whole: its description, its primary
// identifier, and its properties in the canonical tree's order, each with what
// the schema's pointer lists say of it.
func lift(ref schemadraft.TypeRef) (*schemadraft.Type, []aggrega.Diagnostic) {
	top := members(ref.Body)
	if why := refusal(ref.Name, top); why != "" {
		return nil, []aggrega.Diagnostic{{
			Severity: aggrega.SeverityHalt,
			Code:     schemadraft.CodeResourceTypeRefused,
			Message: fmt.Sprintf("the source %q describes %s, %s: import the schema of a resource type", ref.Source,
				ref.Name, why),
			SourceRef: ref.Source,
		}}
	}
	l := &lifter{definitionsRaw: top[keyDefinitions], flags: map[string]*pointerFlags{}}
	t := &schemadraft.Type{Name: ref.Name, Source: ref.Source}
	if description, ok := schemadraft.JSONText(top[keyDescription]); ok {
		t.Description = schemadraft.FirstParagraph(description)
	}
	for _, pointerList := range []struct {
		key string
		set func(*pointerFlags)
	}{
		{keyReadOnly, func(f *pointerFlags) { f.readOnly = true }},
		{keyWriteOnly, func(f *pointerFlags) { f.writeOnly = true }},
		{keyCreateOnly, func(f *pointerFlags) { f.createOnly = true }},
		{keyDeprecated, func(f *pointerFlags) { f.deprecated = true }},
	} {
		for _, path := range l.pointers(top[pointerList.key], pointerList.key) {
			key := pointerKey(path)
			if l.flags[key] == nil {
				l.flags[key] = &pointerFlags{}
			}
			pointerList.set(l.flags[key])
		}
	}
	for _, path := range l.pointers(top[keyPrimary], keyPrimary) {
		t.PrimaryIdentifier = append(t.PrimaryIdentifier, path)
	}
	required := l.names(top[keyRequired], keyRequired)
	if l.err == "" {
		t.Properties = l.members(top[keyProperties], nil, nil, required)
	}
	if l.err != "" {
		return nil, []aggrega.Diagnostic{*unreadableSchema(ref, l.err)}
	}
	for _, p := range t.Properties {
		p.Identifier = slices.ContainsFunc(t.PrimaryIdentifier, func(id []string) bool {
			return slices.Equal(id, p.Path)
		})
	}
	return t, nil
}

// pointerFlags is what the schema's pointer lists say of one property.
type pointerFlags struct{ readOnly, writeOnly, createOnly, deprecated bool }

// lifter lifts the properties of one provider schema.
type lifter struct {
	definitionsRaw json.RawMessage
	// definitions is read from definitionsRaw on the first reference, and only
	// looked up.
	definitions map[string]json.RawMessage
	// entered holds the definitions the property being lifted is nested in,
	// innermost last, so a reference back to one is a cycle.
	entered []string
	// flags holds what the pointer lists say, by pointerKey; only looked up.
	flags map[string]*pointerFlags
	// err is why the schema cannot be read, once a member is found unreadable.
	err string
}

// fail records why the schema cannot be read, keeping the first reason.
func (l *lifter) fail(format string, args ...any) {
	if l.err == "" {
		l.err = fmt.Sprintf(format, args...)
	}
}

// members lifts the properties raw holds, the members of the object at path,
// whose pointer segments are ptr; those named in required are required.
func (l *lifter) members(raw json.RawMessage, path, ptr, required []string) []*schemadraft.Property {
	if raw == nil {
		return nil
	}
	if !schemadraft.IsJSONObject(raw) {
		l.fail("the properties of %s are not an object", describePath(path))
		return nil
	}
	var out []*schemadraft.Property
	for _, e := range importer.ObjectEntries(raw) {
		p := l.property(e.Key, schemadraft.Appended(path, e.Key), schemadraft.Appended(ptr, e.Key), e.Value, slices.Contains(required, e.Key))
		if l.err != "" {
			return nil
		}
		out = append(out, p)
	}
	return out
}

// property lifts the property name at path, whose pointer segments are ptr,
// from its schema raw: a reference is resolved within the schema's definitions,
// a reference back into a definition it is nested in is cut, and so is a
// property nested deeper than schemadraft.MaxDepth.
func (l *lifter) property(name string, path, ptr []string, raw json.RawMessage, required bool) *schemadraft.Property {
	p := &schemadraft.Property{Name: name, Path: path, Required: required}
	if f := l.flags[pointerKey(ptr)]; f != nil {
		p.ReadOnly, p.WriteOnly, p.CreateOnly, p.Deprecated = f.readOnly, f.writeOnly, f.createOnly, f.deprecated
	}
	n := l.node(raw, path)
	if l.err != "" {
		return p
	}
	p.Description = n.description
	if len(path) > schemadraft.MaxDepth {
		return cut(p, "depth")
	}
	entered := 0
	defer func() { l.entered = l.entered[:len(l.entered)-entered] }()
	for n.ref != "" {
		def := l.definitionName(n.ref, path)
		if l.err != "" {
			return p
		}
		if slices.Contains(l.entered, def) {
			return cut(p, "cycle")
		}
		n = l.node(l.definition(def, n.ref, path), path)
		if l.err != "" {
			return p
		}
		if p.Description == "" {
			p.Description = n.description
		}
		l.entered = append(l.entered, def)
		entered++
	}

	p.Kinds = n.kinds
	p.Dropped = n.dropped
	branched := false
	if len(p.Kinds) == 0 {
		p.Kinds, branched = l.branchKinds(n, path), true
	}
	if len(p.Kinds) == 0 {
		switch {
		case n.properties != nil:
			p.Kinds = []schemadraft.Kind{schemadraft.KindObject}
		case n.items != nil:
			p.Kinds = []schemadraft.Kind{schemadraft.KindArray}
		default:
			p.Kinds = []schemadraft.Kind{schemadraft.KindAny}
		}
	}
	p.Enum, p.Pattern, p.Format, p.MinLength, p.MaxLength = n.enum, n.pattern, n.format, n.minLength, n.maxLength
	p.Min, p.Max = n.minimum, n.maximum
	if slices.Contains(p.Kinds, schemadraft.KindArray) {
		p.Min = bound(p, n.minimum, n.minItems, "minimum", "minItems")
		p.Max = bound(p, n.maximum, n.maxItems, "maximum", "maxItems")
	}
	if slices.Contains(p.Kinds, schemadraft.KindObject) {
		p.Properties = l.members(n.properties, path, ptr, n.required)
		p.Open = !n.closed || n.patterned || (branched && n.properties == nil)
	}
	if n.items != nil && slices.Contains(p.Kinds, schemadraft.KindArray) {
		p.Items = l.property(name, path, schemadraft.Appended(ptr, itemsSegment), n.items, false)
	}
	p.Sensitive = credentialNamed(p)
	return p
}

// bound is one bound of the array p: the value bound valueKey where the schema
// states one, as for a union that is a number too, and otherwise the item count
// itemsKey; an item count it does not carry is recorded as dropped.
func bound(p *schemadraft.Property, value, items *json.Number, valueKey, itemsKey string) *json.Number {
	if value == nil {
		return items
	}
	if items != nil {
		p.Dropped = append(p.Dropped, itemsKey+": the draft carries one bound of a kind, the schema's "+valueKey)
	}
	return value
}

// cut makes p an open object, cut for why.
func cut(p *schemadraft.Property, why string) *schemadraft.Property {
	p.Kinds, p.Open, p.Cut = []schemadraft.Kind{schemadraft.KindObject}, true, why
	return p
}

// node is the members of one property schema the reader reads.
type node struct {
	kinds                                []schemadraft.Kind
	description, ref, pattern, format    string
	properties, items                    json.RawMessage
	required                             []string
	enum                                 []json.RawMessage
	minimum, maximum, minItems, maxItems *json.Number
	minLength, maxLength                 *int
	oneOf, anyOf                         []json.RawMessage
	// closed is set where additionalProperties is false; patterned where
	// patternProperties admits members by a pattern.
	closed, patterned bool
	dropped           []string
}

// The constraints a draft does not carry, each with why; read in the canonical
// tree's order.
var droppedKeywords = map[string]string{
	"multipleOf":        "not a constraint a Component carries",
	"const":             "not a constraint a Component carries",
	"exclusiveMinimum":  "not a constraint a Component carries",
	"exclusiveMaximum":  "not a constraint a Component carries",
	"minProperties":     "not a constraint a Component carries",
	"maxProperties":     "not a constraint a Component carries",
	"dependencies":      "not a constraint a Component carries",
	"patternProperties": "the draft describes no member by a pattern, so the object is open",
	"allOf":             "the draft does not merge the schemas it combines",
	"oneOf":             "the draft carries the union of its branches' types, not a branch's own constraints",
	"anyOf":             "the draft carries the union of its branches' types, not a branch's own constraints",
}

// node reads the schema raw of the property at path.
func (l *lifter) node(raw json.RawMessage, path []string) node {
	var n node
	if !schemadraft.IsJSONObject(raw) {
		l.fail("the schema of %s is not an object", describePath(path))
		return n
	}
	for _, e := range importer.ObjectEntries(raw) {
		if why, ok := droppedKeywords[e.Key]; ok {
			n.dropped = append(n.dropped, e.Key+": "+why)
		}
		switch e.Key {
		case "type":
			n.kinds = l.kinds(e.Value, path)
		case keyDescription:
			if description, ok := schemadraft.JSONText(e.Value); ok {
				n.description = schemadraft.FirstParagraph(description)
			}
		case "$ref":
			l.decode(e.Value, &n.ref, path, "$ref")
		case keyProperties:
			n.properties = e.Value
		case "items":
			n.items = e.Value
		case keyRequired:
			n.required = l.names(e.Value, describePath(path)+"'s required")
		case "enum":
			l.decode(e.Value, &n.enum, path, "enum")
		case "pattern":
			l.decode(e.Value, &n.pattern, path, "pattern")
		case "format":
			l.decode(e.Value, &n.format, path, "format")
		case "minimum":
			n.minimum = l.number(e.Value, path, e.Key)
		case "maximum":
			n.maximum = l.number(e.Value, path, e.Key)
		case "minItems":
			n.minItems = l.number(e.Value, path, e.Key)
		case "maxItems":
			n.maxItems = l.number(e.Value, path, e.Key)
		case "minLength":
			l.decode(e.Value, &n.minLength, path, e.Key)
		case "maxLength":
			l.decode(e.Value, &n.maxLength, path, e.Key)
		case "oneOf":
			l.decode(e.Value, &n.oneOf, path, e.Key)
		case "anyOf":
			l.decode(e.Value, &n.anyOf, path, e.Key)
		case "additionalProperties":
			n.closed = bytes.Equal(bytes.TrimSpace(e.Value), []byte("false"))
		case "patternProperties":
			n.patterned = true
		case "uniqueItems":
			if bytes.Equal(bytes.TrimSpace(e.Value), []byte("true")) {
				n.dropped = append(n.dropped, "uniqueItems: not a constraint a Component carries")
			}
		}
	}
	return n
}

// kinds reads a type member: one type name or a list of them. A null type
// adds no kind.
func (l *lifter) kinds(raw json.RawMessage, path []string) []schemadraft.Kind {
	var names []string
	if one, ok := schemadraft.JSONText(raw); ok {
		names = []string{one}
	} else if json.Unmarshal(raw, &names) != nil {
		l.fail("the type of %s is neither a type name nor a list of them", describePath(path))
		return nil
	}
	var out []schemadraft.Kind
	for _, name := range names {
		switch k := schemadraft.Kind(name); k {
		case schemadraft.KindString, schemadraft.KindInteger, schemadraft.KindNumber, schemadraft.KindBoolean,
			schemadraft.KindObject, schemadraft.KindArray:
			if !slices.Contains(out, k) {
				out = append(out, k)
			}
		case "null":
		default:
			l.fail("the type of %s is %q, which is not a JSON Schema type", describePath(path), name)
			return nil
		}
	}
	return out
}

// branchKinds is the union of the kinds of the branches of n's oneOf and
// anyOf, in order: each branch's type, or that of the definition it refers to.
// A branch stating no type, such as one listing the members it requires, adds
// none.
func (l *lifter) branchKinds(n node, path []string) []schemadraft.Kind {
	var out []schemadraft.Kind
	for _, raw := range append(slices.Clone(n.oneOf), n.anyOf...) {
		b := l.node(raw, path)
		var seen []string
		for b.ref != "" && l.err == "" {
			def := l.definitionName(b.ref, path)
			if l.err != "" || slices.Contains(seen, def) {
				break
			}
			seen = append(seen, def)
			b = l.node(l.definition(def, b.ref, path), path)
		}
		if l.err != "" {
			return nil
		}
		kinds := b.kinds
		if len(kinds) == 0 && b.properties != nil {
			kinds = []schemadraft.Kind{schemadraft.KindObject}
		}
		for _, k := range kinds {
			if !slices.Contains(out, k) {
				out = append(out, k)
			}
		}
	}
	return out
}

// definitionName is the definition ref names, which must be one of the
// schema's own.
func (l *lifter) definitionName(ref string, path []string) string {
	name, ok := strings.CutPrefix(ref, definitionPrefix)
	if !ok || name == "" || strings.Contains(name, "/") {
		l.fail("%s refers to %q, and the import resolves a reference to the schema's own definitions alone",
			describePath(path), ref)
		return ""
	}
	return unescapePointer(name)
}

// definition returns the schema of the definition name, which ref refers to.
func (l *lifter) definition(name, ref string, path []string) json.RawMessage {
	if l.definitions == nil {
		l.definitions = members(l.definitionsRaw)
	}
	raw, ok := l.definitions[name]
	if !ok {
		l.fail("%s refers to %q, which the schema's definitions do not hold", describePath(path), ref)
	}
	return raw
}

// pointers reads the list of property pointers raw holds, the member key, each
// as the path of property names it points to, an item written *. A pointer
// outside the schema's properties points to no property and is passed over.
func (l *lifter) pointers(raw json.RawMessage, key string) [][]string {
	if raw == nil {
		return nil
	}
	var list []string
	if json.Unmarshal(raw, &list) != nil {
		l.fail("its %s is not a list of JSON pointers", key)
		return nil
	}
	var out [][]string
	for _, ptr := range list {
		rest, ok := strings.CutPrefix(ptr, pointerRoot)
		if !ok || rest == "" {
			continue
		}
		segments := strings.Split(rest, "/")
		for i, s := range segments {
			segments[i] = unescapePointer(s)
		}
		out = append(out, segments)
	}
	return out
}

// names reads a list of property names raw holds, what naming it for a message.
func (l *lifter) names(raw json.RawMessage, what string) []string {
	if raw == nil {
		return nil
	}
	var out []string
	if json.Unmarshal(raw, &out) != nil {
		l.fail("%s is not a list of property names", what)
	}
	return out
}

// decode reads raw into target, the member of the property at path.
func (l *lifter) decode(raw json.RawMessage, target any, path []string, member string) {
	if json.Unmarshal(raw, target) != nil {
		l.fail("the %s of %s is not what JSON Schema writes there", member, describePath(path))
	}
}

// number reads raw, the member of the property at path, as a number.
func (l *lifter) number(raw json.RawMessage, path []string, member string) *json.Number {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || (trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9')) {
		l.fail("the %s of %s is not a number", member, describePath(path))
		return nil
	}
	n := json.Number(trimmed)
	return &n
}

// pointerKey is the flags key of the property whose pointer segments are ptr.
func pointerKey(ptr []string) string { return strings.Join(ptr, "\x00") }

// unescapePointer reads one JSON pointer segment.
func unescapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
}

// describePath names the property at path for a message: the schema's
// properties at the top level.
func describePath(path []string) string {
	if len(path) == 0 {
		return "the schema"
	}
	return fmt.Sprintf("the property %q", strings.Join(path, "."))
}

// members returns the members of the JSON object raw by key; only looked up.
func members(raw json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for _, e := range importer.ObjectEntries(raw) {
		out[e.Key] = e.Value
	}
	return out
}

// unreadable halts on the source named source, which is neither shape the
// format reads: lack names the member it lacks.
func unreadable(source, lack string) *aggrega.Diagnostic {
	return &aggrega.Diagnostic{
		Severity: aggrega.SeverityHalt,
		Code:     schemadraft.CodeResourceSchemaUnreadable,
		Message: fmt.Sprintf("the source %q is not a CloudFormation resource schema: %s; import a provider "+
			"schema, which holds a typeName string and a properties object, or the output of aws cloudformation "+
			"describe-type, which holds a TypeName string and a Schema string holding a provider schema",
			source, lack),
		SourceRef: source,
	}
}

// unreadableSchema halts on the provider schema of ref, one of whose members
// the reader cannot read, for why.
func unreadableSchema(ref schemadraft.TypeRef, why string) *aggrega.Diagnostic {
	return &aggrega.Diagnostic{
		Severity: aggrega.SeverityHalt,
		Code:     schemadraft.CodeResourceSchemaUnreadable,
		Message: fmt.Sprintf("the source %q holds a provider schema of %s the import cannot read: %s; correct the "+
			"schema, or import the one CloudFormation publishes for the type", ref.Source, ref.Name, why),
		SourceRef: ref.Source,
	}
}

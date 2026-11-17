// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package arm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// The kinds of type a Bicep type file holds, by their $type.
const (
	kindResource       = "ResourceType"
	kindObject         = "ObjectType"
	kindDiscriminated  = "DiscriminatedObjectType"
	kindString         = "StringType"
	kindStringLiteral  = "StringLiteralType"
	kindInteger        = "IntegerType"
	kindIntegerLiteral = "IntegerLiteralType"
	kindBoolean        = "BooleanType"
	kindBooleanLiteral = "BooleanLiteralType"
	kindArray          = "ArrayType"
	kindUnion          = "UnionType"
	kindAny            = "AnyType"
	kindNull           = "NullType"
)

// The flags of a property of an object type.
const (
	flagRequired           = 1
	flagReadOnly           = 2
	flagWriteOnly          = 4
	flagDeployTimeConstant = 8
	flagIdentifier         = 16
	// inheritedFlags are the flags the members of a resource's properties take
	// from properties itself, which the draft lifts them in place of.
	inheritedFlags = flagReadOnly | flagWriteOnly | flagDeployTimeConstant
)

// refPrefix opens a reference to another entry of the same file, followed by
// the entry's index.
const refPrefix = "#/"

// typeMembers are the root members of a resource body that name the type
// itself, read back as constants. The draft does not lift them: the writer
// writes them from the type's own name and apiVersion.
var typeMembers = []string{"type", "apiVersion"}

// node is one entry of a Bicep type file, each member the reader reads left
// undecoded until it is read.
type node struct {
	Kind                 json.RawMessage `json:"$type"`
	Name                 json.RawMessage `json:"name"`
	Body                 json.RawMessage `json:"body"`
	WritableScopes       json.RawMessage `json:"writableScopes"`
	Value                json.RawMessage `json:"value"`
	Sensitive            json.RawMessage `json:"sensitive"`
	MinLength            json.RawMessage `json:"minLength"`
	MaxLength            json.RawMessage `json:"maxLength"`
	Pattern              json.RawMessage `json:"pattern"`
	MinValue             json.RawMessage `json:"minValue"`
	MaxValue             json.RawMessage `json:"maxValue"`
	ItemType             json.RawMessage `json:"itemType"`
	Elements             json.RawMessage `json:"elements"`
	Properties           json.RawMessage `json:"properties"`
	AdditionalProperties json.RawMessage `json:"additionalProperties"`
	Discriminator        json.RawMessage `json:"discriminator"`
	BaseProperties       json.RawMessage `json:"baseProperties"`
}

// member is one property of an object type, as the file writes it.
type member struct {
	Type        json.RawMessage `json:"type"`
	Flags       json.RawMessage `json:"flags"`
	Description json.RawMessage `json:"description"`
}

// readNode reads raw as a node and its $type; ok is false where raw is not an
// object holding a $type string.
func readNode(raw json.RawMessage) (n node, kind string, ok bool) {
	if json.Unmarshal(raw, &n) != nil {
		return node{}, "", false
	}
	if kind, ok = schemadraft.JSONText(n.Kind); !ok {
		return node{}, "", false
	}
	return n, kind, true
}

// present reports whether raw holds a value: it is neither absent nor null.
func present(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// decode reads raw into target, a number, a boolean or a list, reporting
// whether raw is present and of the type target takes. Text is read with
// schemadraft.JSONText.
func decode(raw json.RawMessage, target any) bool {
	return present(raw) && json.Unmarshal(raw, target) == nil
}

// list reads the resource types each source of in holds: every ResourceType
// entry of the file, named <type>@<apiVersion>, without reading its body. A
// source whose first entry holds no $type string is not a Bicep type file.
func list(in aggrega.ImportInput, parse schemadraft.ParseFunc) ([]schemadraft.TypeRef, []aggrega.Diagnostic) {
	var refs []schemadraft.TypeRef
	var diags []aggrega.Diagnostic
	for _, src := range importer.Sources(in) {
		found, halt := listSource(importer.Named(in, src), parse)
		if halt != nil {
			diags = append(diags, *halt)
			continue
		}
		refs = append(refs, found...)
	}
	return refs, diags
}

// listSource lists the types the file src holds. Each type's body is the whole
// file, which its entries refer into by index.
func listSource(src aggrega.ImportSource, parse schemadraft.ParseFunc) ([]schemadraft.TypeRef, *aggrega.Diagnostic) {
	tree, err := parse(syntax, src.Bytes)
	if err != nil {
		d := importer.ParseFailed(src, formatName, err)
		return nil, &d
	}
	var entries []json.RawMessage
	if json.Unmarshal(tree, &entries) != nil || len(entries) == 0 {
		return nil, unreadable(src.Name, "it holds no 0.$type string")
	}
	if _, _, ok := readNode(entries[0]); !ok {
		return nil, unreadable(src.Name, "it holds no 0.$type string")
	}
	var refs []schemadraft.TypeRef
	for i, raw := range entries {
		n, kind, ok := readNode(raw)
		if !ok || kind != kindResource {
			continue
		}
		full, named := schemadraft.JSONText(n.Name)
		name, version := "", ""
		if named {
			name, version, named = splitName(full)
		}
		if !named {
			return nil, unreadable(src.Name, fmt.Sprintf("its %d.name is not a resource type name followed by @ and "+
				"an apiVersion", i))
		}
		refs = append(refs, schemadraft.TypeRef{Name: name, APIVersion: version, Source: src.Name, Body: tree})
	}
	return refs, nil
}

// splitName splits the name of a ResourceType entry, <type>@<apiVersion>.
func splitName(full string) (name, version string, ok bool) {
	i := strings.LastIndex(full, "@")
	if i <= 0 || i == len(full)-1 {
		return "", "", false
	}
	return full[:i], full[i+1:], true
}

// lift reads the type ref names whole: the scopes it is writable at, and the
// members of its body, the root keys first in the file's order and then the
// members of properties, lifted in place of properties, each path opening with
// properties. A body that is a discriminated object is lifted from its base
// properties and the members its kinds declare. The resource ID, read back, is
// the type's identifier. A child type's name is its parent's and its own joined
// by a slash, so the constraints the file states for its own name alone are
// dropped.
func lift(ref schemadraft.TypeRef) (*schemadraft.Type, []aggrega.Diagnostic) {
	l := &lifter{}
	if json.Unmarshal(ref.Body, &l.entries) != nil {
		return nil, []aggrega.Diagnostic{*unreadableType(ref, "the file is not a list of types")}
	}
	at, res := l.resource(ref)
	if at < 0 {
		return nil, []aggrega.Diagnostic{*unreadableType(ref, "the file holds no ResourceType of that name")}
	}
	var bits int
	if !decode(res.WritableScopes, &bits) {
		l.fail("its %d.writableScopes is not a number", at)
	}
	t := &schemadraft.Type{Name: ref.Name, APIVersion: ref.APIVersion, Source: ref.Source, Scopes: scopesOf(bits)}
	if l.err == "" {
		where := fmt.Sprintf("its %d.body", at)
		i, body, kind := l.target(res.Body, where)
		switch {
		case l.err != "":
		case kind == kindObject:
			t.Properties = l.body(i, body.Properties)
		case kind == kindDiscriminated:
			t.Properties = l.discriminatedBody(i, body)
		default:
			l.fail("%s refers to a %s, where a resource body is an %s or a %s", where, kind, kindObject, kindDiscriminated)
		}
	}
	if l.err != "" {
		return nil, []aggrega.Diagnostic{*unreadableType(ref, l.err)}
	}
	for _, p := range t.Properties {
		if slices.Equal(p.Path, []string{keyID}) && p.ReadOnly {
			t.PrimaryIdentifier = [][]string{{keyID}}
		}
		if slices.Equal(p.Path, []string{keyName}) && strings.Count(t.Name, "/") > 1 {
			dropOwnNameConstraints(p)
		}
	}
	return t, nil
}

// childNameWhy says why a child type's name does not carry the constraints the
// file states for it.
const childNameWhy = "an ARM template names a child resource by its parent's name and its own, joined by a slash, " +
	"and the vendor schema states the constraint for the child's own name alone"

// dropOwnNameConstraints drops the constraints of the child's own name p that
// its whole name does not meet: its pattern, its maximum length and its enum.
func dropOwnNameConstraints(p *schemadraft.Property) {
	if p.Pattern != "" {
		p.Dropped = append(p.Dropped, "pattern: "+childNameWhy)
		p.Pattern = ""
	}
	if p.MaxLength != nil {
		p.Dropped = append(p.Dropped, fmt.Sprintf("maximum length %d: %s", *p.MaxLength, childNameWhy))
		p.MaxLength = nil
	}
	if p.Enum != nil {
		p.Dropped = append(p.Dropped, "enum: "+childNameWhy)
		p.Enum = nil
	}
}

// lifter lifts the body of one resource type.
type lifter struct {
	entries []json.RawMessage
	// entered holds the entries of the objects and arrays the property being
	// lifted is nested in, innermost last, so a reference back to one is a
	// cycle.
	entered []int
	// err is why the file cannot be read, once a member is found unreadable.
	err string
}

// fail records why the file cannot be read, keeping the first reason.
func (l *lifter) fail(format string, args ...any) {
	if l.err == "" {
		l.err = fmt.Sprintf(format, args...)
	}
}

// resource returns the index and the node of the first ResourceType entry
// named as ref names it, or -1.
func (l *lifter) resource(ref schemadraft.TypeRef) (int, node) {
	want := ref.Name + "@" + ref.APIVersion
	for i, raw := range l.entries {
		n, kind, ok := readNode(raw)
		if name, named := schemadraft.JSONText(n.Name); ok && kind == kindResource && named && name == want {
			return i, n
		}
	}
	return -1, node{}
}

// target resolves the reference raw holds, written at where, to the index of
// the entry it names, that entry and its $type.
func (l *lifter) target(raw json.RawMessage, where string) (int, node, string) {
	var r struct {
		Ref json.RawMessage `json:"$ref"`
	}
	if json.Unmarshal(raw, &r) != nil {
		r.Ref = nil
	}
	ref, isRef := schemadraft.JSONText(r.Ref)
	if !isRef {
		l.fail(`%s holds no reference such as {"$ref": "#/3"}`, where)
		return -1, node{}, ""
	}
	digits, local := strings.CutPrefix(ref, refPrefix)
	if !local {
		l.fail("%s refers to %q, and the import resolves a reference to an entry of the same file alone, written "+
			"#/<index>", where, ref)
		return -1, node{}, ""
	}
	i, err := strconv.Atoi(digits)
	if err != nil || i < 0 || i >= len(l.entries) {
		l.fail("%s refers to %q, which the file does not hold", where, ref)
		return -1, node{}, ""
	}
	n, kind, ok := readNode(l.entries[i])
	if !ok {
		l.fail("%s refers to %q, which holds no $type string", where, ref)
		return -1, node{}, ""
	}
	return i, n, kind
}

// body lifts the members of the resource body at entry i, whose members raw
// holds: the root keys, less the two naming the type, and then the members of
// properties where it is a closed object of members, lifted in its place.
func (l *lifter) body(i int, raw json.RawMessage) []*schemadraft.Property {
	l.entered = append(l.entered, i)
	defer func() { l.entered = l.entered[:len(l.entered)-1] }()
	if !l.isMembers(raw, "the resource body") {
		return nil
	}
	var root, inner []*schemadraft.Property
	for _, e := range importer.ObjectEntries(raw) {
		if slices.Contains(typeMembers, e.Key) && l.readOnly(e.Value) {
			continue
		}
		if e.Key == keyProperties {
			if members, ok := l.flattened(e.Value); ok {
				inner = members
				continue
			}
		}
		p := l.property(e.Key, []string{e.Key}, e.Value, 0)
		if l.err != "" {
			return nil
		}
		root = append(root, p)
	}
	return append(root, inner...)
}

// flattened lifts the members of the root properties member raw in its place,
// each taking the read-only, write-only and deploy-time constant flags of
// properties: where properties is a closed object of members, not one it is
// nested in already.
func (l *lifter) flattened(raw json.RawMessage) ([]*schemadraft.Property, bool) {
	path := []string{keyProperties}
	m, flags, _ := l.member(raw, path)
	if l.err != "" {
		return nil, false
	}
	i, n, kind := l.target(m.Type, describePath(path))
	if l.err != "" || kind != kindObject || present(n.AdditionalProperties) || slices.Contains(l.entered, i) {
		return nil, false
	}
	l.entered = append(l.entered, i)
	members := l.members(n.Properties, path, flags&inheritedFlags)
	l.entered = l.entered[:len(l.entered)-1]
	return members, l.err == "" && len(members) > 0
}

// readOnly reports whether the member raw is flagged read-only.
func (l *lifter) readOnly(raw json.RawMessage) bool {
	var m member
	var flags int
	return json.Unmarshal(raw, &m) == nil && decode(m.Flags, &flags) && flags&flagReadOnly != 0
}

// isMembers reports whether raw, the members of what, is an object or absent,
// failing where it is neither.
func (l *lifter) isMembers(raw json.RawMessage, what string) bool {
	if present(raw) && !schemadraft.IsJSONObject(raw) {
		l.fail("the members of %s are not an object", what)
		return false
	}
	return true
}

// members lifts the members raw holds of the object at path, in the file's
// order, each taking the flags inherit besides its own.
func (l *lifter) members(raw json.RawMessage, path []string, inherit int) []*schemadraft.Property {
	if !l.isMembers(raw, describePath(path)) {
		return nil
	}
	var out []*schemadraft.Property
	for _, e := range importer.ObjectEntries(raw) {
		p := l.property(e.Key, schemadraft.Appended(path, e.Key), e.Value, inherit)
		if l.err != "" {
			return nil
		}
		out = append(out, p)
	}
	return out
}

// member reads the member raw of the property at path: its type reference,
// its flags and the first paragraph of its description.
func (l *lifter) member(raw json.RawMessage, path []string) (m member, flags int, description string) {
	if json.Unmarshal(raw, &m) != nil {
		l.fail("%s is not an object", describePath(path))
		return member{}, 0, ""
	}
	if present(m.Flags) && !decode(m.Flags, &flags) {
		l.fail("the flags of %s are not a number", describePath(path))
	}
	description, _ = schemadraft.JSONText(m.Description)
	return m, flags, schemadraft.FirstParagraph(description)
}

// property lifts the property name at path from its member raw, with the
// flags inherit besides its own. A property nested deeper than
// schemadraft.MaxDepth is cut, its type not read.
func (l *lifter) property(name string, path []string, raw json.RawMessage, inherit int) *schemadraft.Property {
	m, flags, description := l.member(raw, path)
	p := &schemadraft.Property{Name: name, Path: path, Description: description}
	flags |= inherit
	p.Required = flags&flagRequired != 0
	p.ReadOnly = flags&flagReadOnly != 0
	p.WriteOnly = flags&flagWriteOnly != 0
	p.CreateOnly = flags&flagDeployTimeConstant != 0
	p.Identifier = flags&flagIdentifier != 0
	if l.err != "" {
		return p
	}
	if len(path) > schemadraft.MaxDepth {
		return cut(p, "depth")
	}
	l.shape(p, m.Type, describePath(path))
	return p
}

// cut makes p an open object, cut for why.
func cut(p *schemadraft.Property, why string) *schemadraft.Property {
	p.Kinds, p.Open, p.Cut = []schemadraft.Kind{schemadraft.KindObject}, true, why
	return p
}

// shape reads the type the reference raw of p names, written at where, into p:
// its kinds, its constraints, its members and its items. An object or an array
// referring back to one p is nested in is cut.
func (l *lifter) shape(p *schemadraft.Property, raw json.RawMessage, where string) {
	i, n, kind := l.target(raw, where)
	if l.err != "" {
		return
	}
	switch kind {
	case kindObject, kindArray:
		if slices.Contains(l.entered, i) {
			cut(p, "cycle")
			return
		}
		l.entered = append(l.entered, i)
		defer func() { l.entered = l.entered[:len(l.entered)-1] }()
	}
	switch kind {
	case kindString:
		p.Kinds = []schemadraft.Kind{schemadraft.KindString}
		p.Sensitive = l.sensitive(n)
		p.MinLength = l.length(n.MinLength, where, "minLength")
		p.MaxLength = l.length(n.MaxLength, where, "maxLength")
		if pattern, ok := schemadraft.JSONText(n.Pattern); ok {
			p.Pattern = pattern
		} else if present(n.Pattern) {
			l.fail("the pattern of %s is not text", where)
		}
	case kindInteger:
		p.Kinds = []schemadraft.Kind{schemadraft.KindInteger}
		p.Min, p.Max = l.number(n.MinValue, where, "minValue"), l.number(n.MaxValue, where, "maxValue")
	case kindBoolean:
		p.Kinds = []schemadraft.Kind{schemadraft.KindBoolean}
	case kindStringLiteral, kindIntegerLiteral, kindBooleanLiteral:
		k, value := l.literal(kind, n, where)
		p.Kinds, p.Enum = []schemadraft.Kind{k}, []json.RawMessage{value}
	case kindAny, kindNull:
		p.Kinds = []schemadraft.Kind{schemadraft.KindAny}
	case kindArray:
		p.Kinds = []schemadraft.Kind{schemadraft.KindArray}
		p.Min, p.Max = l.number(n.MinLength, where, "minLength"), l.number(n.MaxLength, where, "maxLength")
		items := &schemadraft.Property{Name: p.Name, Path: p.Path}
		l.shape(items, n.ItemType, where+"'s items")
		p.Items = items
	case kindObject:
		p.Kinds = []schemadraft.Kind{schemadraft.KindObject}
		p.Sensitive = l.sensitive(n)
		p.Properties = l.members(n.Properties, p.Path, 0)
		if present(n.AdditionalProperties) {
			p.Open = true
			if _, _, admitted := l.target(n.AdditionalProperties, where+"'s additionalProperties"); admitted != kindAny {
				p.Dropped = append(p.Dropped, "type of the members it admits beyond those it names: the draft "+
					"passes the object whole, so the type the vendor states for those members is not checked")
			}
		}
	case kindUnion:
		l.union(p, n, where)
	case kindDiscriminated:
		discriminator, named := schemadraft.JSONText(n.Discriminator)
		if !named {
			discriminator = "kind"
		}
		p.Kinds, p.Open = []schemadraft.Kind{schemadraft.KindObject}, true
		p.Dropped = append(p.Dropped, fmt.Sprintf("members of each %s: a discriminated object takes the members its "+
			"%s names, which the draft does not describe, so the object is open", discriminator, discriminator))
	default:
		p.Kinds = []schemadraft.Kind{schemadraft.KindAny}
		p.Dropped = append(p.Dropped, "type "+kind+": the reader does not read that kind of type, so the property "+
			"takes any value")
	}
}

// sensitive reads whether the type n is marked sensitive.
func (l *lifter) sensitive(n node) bool {
	var sensitive bool
	decode(n.Sensitive, &sensitive)
	return sensitive
}

// length reads the length bound raw, the member named of the type at where.
func (l *lifter) length(raw json.RawMessage, where, named string) *int {
	if !present(raw) {
		return nil
	}
	var n int
	if !decode(raw, &n) {
		l.fail("the %s of %s is not a whole number", named, where)
		return nil
	}
	return &n
}

// number reads the bound raw, the member named of the type at where.
func (l *lifter) number(raw json.RawMessage, where, named string) *json.Number {
	if !present(raw) {
		return nil
	}
	var n json.Number
	if !decode(raw, &n) {
		l.fail("the %s of %s is not a number", named, where)
		return nil
	}
	return &n
}

// literal reads the value of the literal type n of the $type kind, written at
// where, and the kind of the value.
func (l *lifter) literal(kind string, n node, where string) (schemadraft.Kind, json.RawMessage) {
	if kind == kindStringLiteral {
		text, ok := schemadraft.JSONText(n.Value)
		if !ok {
			l.fail("the value of the %s of %s is not one", kind, where)
			return schemadraft.KindString, nil
		}
		value, _ := json.Marshal(text)
		return schemadraft.KindString, value
	}
	var target any
	var k schemadraft.Kind
	switch kind {
	case kindIntegerLiteral:
		target, k = new(json.Number), schemadraft.KindInteger
	default:
		target, k = new(bool), schemadraft.KindBoolean
	}
	if !decode(n.Value, target) {
		l.fail("the value of the %s of %s is not one", kind, where)
		return k, nil
	}
	value, _ := json.Marshal(target)
	return k, value
}

// union reads the union n into p, written at where, nested unions read as
// their elements. A union of literals is an enum of their values. A union of
// string literals and a string admits any string, so it is a string carrying
// no enum. Any other union takes the kinds of its elements, an object among
// them open, and carries no element's constraints.
func (l *lifter) union(p *schemadraft.Property, n node, where string) {
	var kinds []schemadraft.Kind
	var literals []json.RawMessage
	var known []string
	openString, other := false, false
	add := func(k schemadraft.Kind) {
		if !slices.Contains(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	var visited []int
	var walk func(n node)
	walk = func(n node) {
		var elements []json.RawMessage
		if !decode(n.Elements, &elements) {
			l.fail("the union of %s holds no list of elements", where)
			return
		}
		for _, e := range elements {
			i, en, kind := l.target(e, where+"'s union")
			if l.err != "" {
				return
			}
			if slices.Contains(visited, i) {
				continue
			}
			visited = append(visited, i)
			switch kind {
			case kindStringLiteral, kindIntegerLiteral, kindBooleanLiteral:
				k, value := l.literal(kind, en, where)
				add(k)
				literals = append(literals, value)
				text, isText := schemadraft.JSONText(value)
				if !isText {
					text = string(value)
				}
				known = append(known, text)
			case kindUnion:
				walk(en)
			case kindString:
				add(schemadraft.KindString)
				openString = true
			case kindNull:
			default:
				add(unionKind(kind))
				other = true
			}
		}
	}
	walk(n)
	switch {
	case l.err != "":
	case len(kinds) == 0 || slices.Contains(kinds, schemadraft.KindAny):
		p.Kinds = []schemadraft.Kind{schemadraft.KindAny}
	case !openString && !other:
		p.Kinds, p.Enum = kinds, literals
	case !other && len(kinds) == 1:
		p.Kinds = kinds
		if len(known) > 0 {
			p.Dropped = append(p.Dropped, "known values "+strings.Join(known, ", ")+": the vendor schema admits "+
				"any other string beside them, so the draft carries no enum")
		}
	default:
		p.Kinds = kinds
		p.Open = slices.Contains(kinds, schemadraft.KindObject)
		p.Dropped = append(p.Dropped, "constraints of each type of its union: the draft carries the union of the "+
			"types, not the constraints of each")
	}
}

// unionKind is the kind an element of the $type kind gives a union.
func unionKind(kind string) schemadraft.Kind {
	switch kind {
	case kindInteger:
		return schemadraft.KindInteger
	case kindBoolean:
		return schemadraft.KindBoolean
	case kindObject, kindDiscriminated:
		return schemadraft.KindObject
	case kindArray:
		return schemadraft.KindArray
	}
	return schemadraft.KindAny
}

// occurrence is one declaration of a member by one kind of a discriminated
// body.
type occurrence struct {
	raw   json.RawMessage
	entry int
	kind  string
	value json.RawMessage
	flags int
}

// discriminatedBody lifts the members of the discriminated resource body n at
// entry i: its base properties as a body's, then each member its kinds
// declare, in name order. A member every kind declares with one type is lifted
// from that type; one whose every declaration is a string literal, as the
// discriminator's is, is a string of their values; any other is passed whole.
// It is required where every kind requires it. A discriminator no kind declares
// is a required string of the kinds' names.
func (l *lifter) discriminatedBody(i int, n node) []*schemadraft.Property {
	discriminator, _ := schemadraft.JSONText(n.Discriminator)
	base := l.body(i, n.BaseProperties)
	if l.err != "" || !l.isMembers(n.Elements, "the kinds of the resource body") {
		return nil
	}
	l.entered = append(l.entered, i)
	defer func() { l.entered = l.entered[:len(l.entered)-1] }()

	baseNames := func(name string) bool {
		return slices.ContainsFunc(base, func(p *schemadraft.Property) bool { return p.Name == name })
	}
	kinds := importer.ObjectEntries(n.Elements)
	var names, kindNames []string
	declared := map[string][]occurrence{}
	for _, k := range kinds {
		kindNames = append(kindNames, k.Key)
		where := fmt.Sprintf("the %s %q of the resource body", discriminator, k.Key)
		_, kn, kind := l.target(k.Value, where)
		if l.err != "" {
			return nil
		}
		if kind != kindObject {
			l.fail("%s refers to a %s, where a kind of a body is an %s", where, kind, kindObject)
			return nil
		}
		if !l.isMembers(kn.Properties, where) {
			return nil
		}
		for _, e := range importer.ObjectEntries(kn.Properties) {
			if baseNames(e.Key) || slices.Contains(typeMembers, e.Key) && l.readOnly(e.Value) {
				continue
			}
			m, flags, _ := l.member(e.Value, []string{e.Key})
			ti, tn, tkind := l.target(m.Type, describePath([]string{e.Key}))
			if l.err != "" {
				return nil
			}
			if _, seen := declared[e.Key]; !seen {
				names = append(names, e.Key)
			}
			declared[e.Key] = append(declared[e.Key], occurrence{raw: e.Value, entry: ti, kind: tkind, value: tn.Value,
				flags: flags})
		}
	}
	if discriminator != "" && !baseNames(discriminator) && declared[discriminator] == nil {
		names = append(names, discriminator)
	}
	slices.Sort(names)

	props := base
	for _, name := range names {
		occ := declared[name]
		if occ == nil {
			p := &schemadraft.Property{Name: name, Path: []string{name}, Kinds: []schemadraft.Kind{schemadraft.KindString},
				Required: true}
			for _, k := range kindNames {
				value, _ := json.Marshal(k)
				p.Enum = append(p.Enum, value)
			}
			props = append(props, p)
			continue
		}
		props = append(props, l.declaredMember(name, occ, len(kinds), discriminator))
		if l.err != "" {
			return nil
		}
	}
	return props
}

// declaredMember lifts the member name the kinds of a discriminated body
// declare in occ, of kinds kinds in all.
func (l *lifter) declaredMember(name string, occ []occurrence, kinds int, discriminator string) *schemadraft.Property {
	optional := func(o occurrence) bool { return o.flags&flagRequired == 0 }
	required := len(occ) == kinds && !slices.ContainsFunc(occ, optional)
	sameType := !slices.ContainsFunc(occ, func(o occurrence) bool { return o.entry != occ[0].entry })
	literals := !slices.ContainsFunc(occ, func(o occurrence) bool { return o.kind != kindStringLiteral })
	var p *schemadraft.Property
	switch {
	case sameType:
		p = l.property(name, []string{name}, occ[0].raw, 0)
	case literals:
		_, _, description := l.member(occ[0].raw, []string{name})
		p = &schemadraft.Property{Name: name, Path: []string{name}, Kinds: []schemadraft.Kind{schemadraft.KindString},
			Description: description}
		for _, o := range occ {
			value, ok := schemadraft.JSONText(o.value)
			if !ok {
				l.fail("the value of the %s %s refers to is not text", kindStringLiteral, describePath([]string{name}))
				return p
			}
			raw, _ := json.Marshal(value)
			p.Enum = append(p.Enum, raw)
		}
	default:
		_, _, description := l.member(occ[0].raw, []string{name})
		p = &schemadraft.Property{Name: name, Path: []string{name}, Kinds: []schemadraft.Kind{schemadraft.KindAny},
			Description: description}
		p.Dropped = append(p.Dropped, fmt.Sprintf("shape of each %s: the kinds of the resource body give it "+
			"different shapes, so the draft passes it whole", discriminator))
	}
	p.Required = required
	return p
}

// describePath names the property at path for a message.
func describePath(path []string) string {
	return fmt.Sprintf("the property %q", strings.Join(path, "."))
}

// unreadable halts on the source named source, which is not a Bicep type file:
// lack names the member it lacks.
func unreadable(source, lack string) *aggrega.Diagnostic {
	return &aggrega.Diagnostic{
		Severity: "halt",
		Code:     schemadraft.CodeResourceSchemaUnreadable,
		Message: fmt.Sprintf("the source %q is not a Bicep type file: %s; import a types.json from "+
			"Azure/bicep-types-az, a list of types whose first entry holds a $type string and whose resource types "+
			"are named <type>@<apiVersion>", source, lack),
		SourceRef: source,
	}
}

// unreadableType halts on the type ref names, a member of whose body the
// reader cannot read, for why.
func unreadableType(ref schemadraft.TypeRef, why string) *aggrega.Diagnostic {
	return &aggrega.Diagnostic{
		Severity: "halt",
		Code:     schemadraft.CodeResourceSchemaUnreadable,
		Message: fmt.Sprintf("the source %q holds a Bicep type file whose resource type %s@%s the import cannot "+
			"read: %s; correct the file, or import the types.json Azure/bicep-types-az publishes for the type's "+
			"namespace and apiVersion", ref.Source, ref.Name, ref.APIVersion, why),
		SourceRef: ref.Source,
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraform

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

// The members of an export the reader reads.
const (
	keyFormatVersion    = "format_version"
	keyProviderSchemas  = "provider_schemas"
	keyResourceSchemas  = "resource_schemas"
	betaSuffix          = "-beta"
	nestingList         = "list"
	nestingSet          = "set"
	nestingSingle       = "single"
	nestingGroup        = "group"
	nestingMap          = "map"
	fixAmbiguousExports = "export the schema again from a configuration requiring only one of them"
)

// list reads the resource types each provider of each source of in carries,
// from provider_schemas.<address>.resource_schemas, without decoding any type's
// body: a data source, an ephemeral resource or a function is not listed.
func list(in aggrega.ImportInput, parse schemadraft.ParseFunc) ([]schemadraft.TypeRef, []aggrega.Diagnostic) {
	var refs []schemadraft.TypeRef
	var diags []aggrega.Diagnostic
	for _, src := range importer.Sources(in) {
		named := importer.Named(in, src)
		found, halt := listSource(named, parse)
		if halt != nil {
			diags = append(diags, *halt)
			continue
		}
		refs = append(refs, found...)
	}
	return refs, diags
}

// listSource lists the types the export src holds.
func listSource(src aggrega.ImportSource, parse schemadraft.ParseFunc) ([]schemadraft.TypeRef, *aggrega.Diagnostic) {
	tree, err := parse(syntax, src.Bytes)
	if err != nil {
		d := importer.ParseFailed(src, formatName, err)
		return nil, &d
	}
	var version, providers json.RawMessage
	for _, e := range importer.ObjectEntries(tree) {
		switch e.Key {
		case keyFormatVersion:
			version = e.Value
		case keyProviderSchemas:
			providers = e.Value
		}
	}
	if _, ok := text(version); !ok {
		return nil, unreadable(src.Name, "it holds no "+keyFormatVersion+" string")
	}
	if !isObject(providers) {
		return nil, unreadable(src.Name, "it holds no "+keyProviderSchemas+" object")
	}
	var refs []schemadraft.TypeRef
	for _, p := range importer.ObjectEntries(providers) {
		if !isObject(p.Value) {
			return nil, unreadable(src.Name, "the provider "+p.Key+" is not an object")
		}
		address := parseAddress(p.Key)
		for _, member := range importer.ObjectEntries(p.Value) {
			if member.Key != keyResourceSchemas {
				continue
			}
			if !isObject(member.Value) {
				return nil, unreadable(src.Name, "the "+keyResourceSchemas+" of the provider "+p.Key+" is not an object")
			}
			for _, r := range importer.ObjectEntries(member.Value) {
				addr := address
				refs = append(refs, schemadraft.TypeRef{Name: r.Key, Source: src.Name, Provider: &addr, Body: r.Value})
			}
		}
	}
	return refs, nil
}

// parseAddress reads the registry address of a provider as an export keys it:
// host/namespace/type. An address of fewer segments leaves the leading ones
// empty; one of more takes the extra leading segments as the host.
func parseAddress(key string) schemadraft.ProviderAddress {
	segments := strings.Split(key, "/")
	var a schemadraft.ProviderAddress
	a.Type = segments[len(segments)-1]
	if len(segments) > 1 {
		a.Namespace = segments[len(segments)-2]
	}
	if len(segments) > 2 {
		a.Host = strings.Join(segments[:len(segments)-2], "/")
	}
	return a
}

// addressText is the address a provider is named by in a message.
func addressText(a *schemadraft.ProviderAddress) string {
	var parts []string
	for _, s := range []string{a.Host, a.Namespace, a.Type} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "/")
}

// resolve chooses among the providers carrying the type name: where exactly two
// do, at one host and in one namespace, and the type of one is the other's plus
// -beta, the other's type, reporting the beta provider passed over; otherwise it
// halts naming the providers and how to export one alone.
func resolve(name string, refs []schemadraft.TypeRef) (schemadraft.TypeRef, []aggrega.Diagnostic) {
	if len(refs) == 2 && refs[0].Provider != nil && refs[1].Provider != nil {
		a, b := refs[0].Provider, refs[1].Provider
		if a.Host == b.Host && a.Namespace == b.Namespace {
			switch {
			case b.Type == a.Type+betaSuffix:
				return refs[0], []aggrega.Diagnostic{schemadraft.BetaProviderPassedOver(name, addressText(a), addressText(b))}
			case a.Type == b.Type+betaSuffix:
				return refs[1], []aggrega.Diagnostic{schemadraft.BetaProviderPassedOver(name, addressText(b), addressText(a))}
			}
		}
	}
	var providers []string
	for _, r := range refs {
		if r.Provider != nil {
			providers = append(providers, addressText(r.Provider))
		}
	}
	return schemadraft.TypeRef{}, []aggrega.Diagnostic{schemadraft.ResourceTypeAmbiguous(name, providers, fixAmbiguousExports)}
}

// lift reads the type ref names whole: its description and its attributes and
// blocks, the properties of its body, in the order of the export's tree.
func lift(ref schemadraft.TypeRef) (*schemadraft.Type, []aggrega.Diagnostic) {
	l := &lifter{}
	var body struct {
		Block json.RawMessage `json:"block"`
	}
	if err := json.Unmarshal(ref.Body, &body); err != nil {
		l.fail("its schema is not an object")
	} else if !isObject(body.Block) {
		l.fail("its schema holds no block object")
	}
	t := &schemadraft.Type{Name: ref.Name, Source: ref.Source, Provider: ref.Provider}
	if l.err == "" {
		t.Properties, t.Description = l.block(body.Block, nil)
	}
	if l.err != "" {
		return nil, []aggrega.Diagnostic{*unreadableSchema(ref, l.err)}
	}
	return t, nil
}

// lifter lifts the properties of one resource schema.
type lifter struct {
	// err is why the schema cannot be read, once a member is found unreadable.
	err string
}

// fail records why the schema cannot be read, keeping the first reason.
func (l *lifter) fail(format string, args ...any) {
	if l.err == "" {
		l.err = fmt.Sprintf(format, args...)
	}
}

// block lifts the attributes and the nested blocks of the block raw, at path,
// as properties in name order, with the first paragraph of its description.
func (l *lifter) block(raw json.RawMessage, path []string) ([]*schemadraft.Property, string) {
	var b struct {
		Attributes  json.RawMessage `json:"attributes"`
		BlockTypes  json.RawMessage `json:"block_types"`
		Description string          `json:"description"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		l.fail("a block at %s is not readable: %v", describePath(path), err)
		return nil, ""
	}
	var props []*schemadraft.Property
	for _, name := range []struct {
		raw  json.RawMessage
		what string
		lift func(string, []string, json.RawMessage) *schemadraft.Property
	}{{b.Attributes, "attributes", l.attribute}, {b.BlockTypes, "block_types", l.blockType}} {
		if name.raw == nil {
			continue
		}
		if !isObject(name.raw) {
			l.fail("the %s of %s are not an object", name.what, describePath(path))
			return nil, ""
		}
		for _, e := range importer.ObjectEntries(name.raw) {
			p := name.lift(e.Key, appended(path, e.Key), e.Value)
			if l.err != "" {
				return nil, ""
			}
			props = append(props, p)
		}
	}
	slices.SortStableFunc(props, func(a, b *schemadraft.Property) int { return strings.Compare(a.Name, b.Name) })
	return props, schemadraft.FirstParagraph(b.Description)
}

// attribute lifts the attribute name at path from its schema raw: a cty type,
// or a nested type of attributes.
func (l *lifter) attribute(name string, path []string, raw json.RawMessage) *schemadraft.Property {
	var a struct {
		Type        json.RawMessage `json:"type"`
		NestedType  json.RawMessage `json:"nested_type"`
		Description string          `json:"description"`
		Required    bool            `json:"required"`
		Optional    bool            `json:"optional"`
		Computed    bool            `json:"computed"`
		Sensitive   bool            `json:"sensitive"`
		WriteOnly   bool            `json:"write_only"`
		Deprecated  bool            `json:"deprecated"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		l.fail("the attribute %s is not readable: %v", describePath(path), err)
		return &schemadraft.Property{Name: name, Path: path}
	}
	var p *schemadraft.Property
	switch {
	case len(path) > schemadraft.MaxDepth:
		p = cut(&schemadraft.Property{Name: name, Path: path})
	case a.Type != nil:
		p = l.cty(name, path, a.Type)
	case a.NestedType != nil:
		p = l.nestedType(name, path, a.NestedType)
	default:
		l.fail("the attribute %s holds neither a type nor a nested_type", describePath(path))
		return &schemadraft.Property{Name: name, Path: path}
	}
	p.Description = schemadraft.FirstParagraph(a.Description)
	p.Required = a.Required || (p.Block != nil && p.Block.MinItems > 0)
	p.ReadOnly = a.Computed && !a.Optional && !a.Required || legacyID(path, a.Optional, a.Computed)
	p.Sensitive, p.WriteOnly, p.Deprecated = a.Sensitive, a.WriteOnly, a.Deprecated
	return p
}

// legacyIDAttribute names the resource identifier a provider built on the
// Terraform plugin SDK declares at the top level of every resource, optional
// and computed, and refuses in configuration ("Invalid or unknown key").
const legacyIDAttribute = "id"

// legacyID reports whether the attribute at path, optional and computed as
// given, is that identifier: read back, since no configuration sets it.
func legacyID(path []string, optional, computed bool) bool {
	return len(path) == 1 && path[0] == legacyIDAttribute && optional && computed
}

// nestedType lifts the attribute name at path whose type is a nested type of
// attributes.
func (l *lifter) nestedType(name string, path []string, raw json.RawMessage) *schemadraft.Property {
	var n struct {
		Attributes json.RawMessage `json:"attributes"`
		Mode       string          `json:"nesting_mode"`
		MinItems   int             `json:"min_items"`
		MaxItems   int             `json:"max_items"`
	}
	if err := json.Unmarshal(raw, &n); err != nil || !isObject(n.Attributes) {
		l.fail("the nested type of %s is not readable", describePath(path))
		return &schemadraft.Property{Name: name, Path: path}
	}
	block, _ := json.Marshal(map[string]json.RawMessage{"attributes": n.Attributes})
	props, _ := l.block(block, path)
	p := l.nest(&schemadraft.Property{Name: name, Path: path}, n.Mode, n.MinItems, n.MaxItems, props)
	if p.Block != nil {
		p.Block.Attribute = true
	}
	return p
}

// blockType lifts the nested block name at path from its entry raw, which gives
// its nesting mode, the bounds on its item count and the block itself.
func (l *lifter) blockType(name string, path []string, raw json.RawMessage) *schemadraft.Property {
	var b struct {
		Mode     string          `json:"nesting_mode"`
		Block    json.RawMessage `json:"block"`
		MinItems int             `json:"min_items"`
		MaxItems int             `json:"max_items"`
	}
	if err := json.Unmarshal(raw, &b); err != nil || !isObject(b.Block) {
		l.fail("the block %s is not readable", describePath(path))
		return &schemadraft.Property{Name: name, Path: path}
	}
	p := &schemadraft.Property{Name: name, Path: path}
	if len(path) > schemadraft.MaxDepth {
		var described struct {
			Description string `json:"description"`
		}
		_ = json.Unmarshal(b.Block, &described) // only the description is read; the cut subtree is not lifted
		p.Description = schemadraft.FirstParagraph(described.Description)
		// A cut block stays known as a block, which Terraform sets as one.
		p.Block = &schemadraft.Block{Nesting: b.Mode, MinItems: b.MinItems, MaxItems: b.MaxItems}
		return cut(p)
	}
	props, description := l.block(b.Block, path)
	p.Description = description
	return l.nest(p, b.Mode, b.MinItems, b.MaxItems, props)
}

// nest shapes p, a block or a nested type holding props, by its nesting mode: a
// list or a set is an array of objects, a single or a group an object, a map an
// open object whose members the Component does not describe, the object each
// key holds kept as its Items for the module setting it. A bound above zero on
// the item count makes p required.
func (l *lifter) nest(p *schemadraft.Property, mode string, lo, hi int, props []*schemadraft.Property) *schemadraft.Property {
	p.Block = &schemadraft.Block{Nesting: mode, MinItems: lo, MaxItems: hi}
	p.Required = lo > 0
	switch mode {
	case nestingList, nestingSet:
		p.Kinds = []schemadraft.Kind{schemadraft.KindArray}
		p.Items = &schemadraft.Property{Name: p.Name, Path: p.Path, Kinds: []schemadraft.Kind{schemadraft.KindObject},
			Properties: props}
	case nestingSingle, nestingGroup:
		p.Kinds, p.Properties = []schemadraft.Kind{schemadraft.KindObject}, props
	case nestingMap:
		p.Kinds, p.Open = []schemadraft.Kind{schemadraft.KindObject}, true
		p.Items = &schemadraft.Property{Name: p.Name, Path: p.Path, Kinds: []schemadraft.Kind{schemadraft.KindObject},
			Properties: props}
		p.Dropped = append(p.Dropped, "nesting_mode map: the draft carries a map of blocks as an open object, "+
			"without the members of its blocks")
	default:
		l.fail("the block %s has the nesting_mode %q, which is not one of list, set, single, group or map",
			describePath(p.Path), mode)
	}
	return p
}

// cty lifts the cty type raw of the property name at path: a primitive named by
// a string, or a collection or structural type written as an array.
func (l *lifter) cty(name string, path []string, raw json.RawMessage) *schemadraft.Property {
	p := &schemadraft.Property{Name: name, Path: path}
	if s, ok := text(raw); ok {
		switch s {
		case "string":
			p.Kinds = []schemadraft.Kind{schemadraft.KindString}
		case "number":
			p.Kinds = []schemadraft.Kind{schemadraft.KindNumber}
		case "bool":
			p.Kinds = []schemadraft.Kind{schemadraft.KindBoolean}
		case "dynamic":
			p.Kinds = []schemadraft.Kind{schemadraft.KindAny}
		default:
			l.fail("the type %q of %s is not a Terraform type", s, describePath(path))
		}
		return p
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || len(parts) < 2 {
		l.fail("the type of %s is not a Terraform type", describePath(path))
		return p
	}
	constructor, _ := text(parts[0])
	switch constructor {
	case "list", "set":
		p.Kinds = []schemadraft.Kind{schemadraft.KindArray}
		p.Items = l.cty(name, path, parts[1])
	case "map":
		p.Kinds, p.Open = []schemadraft.Kind{schemadraft.KindObject}, true
		p.Dropped = append(p.Dropped, "map element type: the draft carries a map as an open object, without the "+
			"type of its values")
	case "object":
		p.Kinds = []schemadraft.Kind{schemadraft.KindObject}
		if !isObject(parts[1]) {
			l.fail("the object type of %s holds no attributes", describePath(path))
			return p
		}
		p.Dropped = append(p.Dropped, objectRequirement(parts))
		for _, e := range importer.ObjectEntries(parts[1]) {
			memberPath := appended(path, e.Key)
			if len(memberPath) > schemadraft.MaxDepth {
				p.Properties = append(p.Properties, cut(&schemadraft.Property{Name: e.Key, Path: memberPath}))
				continue
			}
			p.Properties = append(p.Properties, l.cty(e.Key, memberPath, e.Value))
		}
	case "tuple":
		p.Kinds = []schemadraft.Kind{schemadraft.KindArray}
		p.Dropped = append(p.Dropped, "tuple element types: the draft carries a tuple as an array, without the "+
			"type of each position")
	default:
		l.fail("the type %q of %s is not a Terraform type", constructor, describePath(path))
	}
	return p
}

// objectRequirement is the Dropped entry for an object type, whose members the
// draft never marks required: the type requires each member unless its optional
// list, the third element, names it.
func objectRequirement(parts []json.RawMessage) string {
	var optional []string
	if len(parts) > 2 {
		_ = json.Unmarshal(parts[2], &optional) // an unreadable list names none
	}
	if len(optional) > 0 {
		return "object optional attributes: the type marks " + strings.Join(optional, ", ") +
			" optional and requires its other members, and the draft marks no member required"
	}
	return "object required attributes: the type requires every member, and the draft marks no member required"
}

// cut makes p an open object, cut for its depth.
func cut(p *schemadraft.Property) *schemadraft.Property {
	p.Kinds, p.Open, p.Cut = []schemadraft.Kind{schemadraft.KindObject}, true, "depth"
	return p
}

// text reads raw as a JSON string.
func text(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(trimmed, &s) != nil {
		return "", false
	}
	return s, true
}

// isObject reports whether raw is a JSON object.
func isObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed)
}

// appended is path followed by name, sharing no storage with path.
func appended(path []string, name string) []string {
	return append(slices.Clone(path), name)
}

// describePath names the property at path in a message.
func describePath(path []string) string {
	if len(path) == 0 {
		return "the resource body"
	}
	return strings.Join(path, ".")
}

// unreadable halts on the source named source, which is not an export of
// providers schema -json: lack names the member it lacks.
func unreadable(source, lack string) *aggrega.Diagnostic {
	return &aggrega.Diagnostic{
		Severity: "halt",
		Code:     schemadraft.CodeResourceSchemaUnreadable,
		Message: fmt.Sprintf("the source %q is not a Terraform providers schema export: %s; import the output of "+
			"terraform providers schema -json or tofu providers schema -json, which holds a format_version string "+
			"and a provider_schemas object", source, lack),
		SourceRef: source,
	}
}

// unreadableSchema halts on the schema of ref, one of whose members the reader
// cannot read, for why.
func unreadableSchema(ref schemadraft.TypeRef, why string) *aggrega.Diagnostic {
	return &aggrega.Diagnostic{
		Severity: "halt",
		Code:     schemadraft.CodeResourceSchemaUnreadable,
		Message: fmt.Sprintf("the source %q holds a schema of %s the import cannot read: %s; export the schema again "+
			"with terraform providers schema -json or tofu providers schema -json", ref.Source, ref.Name, why),
		SourceRef: ref.Source,
	}
}

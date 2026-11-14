// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// Owed is one value the drafts still leave empty that a schema says something
// supplies: the drafted file it is in, the keys leading to it from the top of
// that document, what its schema says it takes and its whole description there,
// and whether that schema declares it sensitive.
//
// A sensitive value is a secret's, and its answer names where the secret comes
// from — a variable or a Secret — never the secret itself ([AnswerIsAName]).
//
// Comment is the comment the draft writes directly above the value's key, which
// describes the value where its schema gives no description of its own.
type Owed struct {
	File        string
	Keys        []string
	Takes       string
	Description string
	Sensitive   bool
	Comment     string

	// line is the line of the value's key in its drafted file, which orders the
	// owed values of one file.
	line int
}

// Key is the owed value's key path as the answers file writes it: the keys
// joined by full stops, a key holding one written quoted ([RenderKey]).
func (o Owed) Key() string { return RenderKey(o.Keys) }

// Question is the sentence an owed value is described by: its schema's whole
// description where the schema gives one; otherwise the comment the draft writes
// above its key, where there is one; and otherwise what the schema says it takes.
func (o Owed) Question() string {
	switch {
	case o.Description != "":
		return o.Description
	case o.Comment != "":
		return o.Comment
	}
	return o.Takes
}

// keyComment is the comment written directly above a key, its markers and line
// breaks folded into one line of text.
func keyComment(key *yaml.Node) string {
	if key == nil {
		return ""
	}
	var words []string
	for _, line := range strings.Split(key.HeadComment, "\n") {
		words = append(words, strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "#"))...)
	}
	return strings.Join(words, " ")
}

// owedCatalog is the part of the catalog the owed reading reads the schemas of
// the drafted instances' types from. The real seam's catalog serves it; a harness
// standing in for the catalog may not, and its drafts are then read against their
// kinds' own schemas alone.
type owedCatalog interface {
	CatalogLookup() resolver.CatalogLookup
}

// readOwed lists every value the drafts leave as an empty string where a schema
// says one is supplied, in the order the drafts are written and, within a
// document, in the order its keys are.
//
// Two schemas are read, by one reading ([validateops.PlaceholdersOf] and
// [validateops.PlaceholdersUnder], the reading `aggrega validate` reports an
// empty required value under):
//
//   - each drafted document's own kind schema, for every value it requires;
//   - the catalog schema each drafted instance or attachment is of — a drafted
//     Aggregate instance's values and infra against its Component, an attachment
//     against its Feature, a connection against its socket, a drafted Release
//     entry's infra against the Component the Aggregate names it as, and the
//     Release's parameters against the drafted Aggregate's own declarations —
//     for every value written empty under a key that schema declares, required
//     or not. Nothing supplied such a value, so the draft cannot have meant it.
//
// What is not a YAML document of a kind the binary carries a schema for is not
// read, and neither is the text of a block scalar — a flow or an application's
// own definition carried as a value.
func readOwed(files []abi.ScaffoldFile, recipes RecipeIndex) []Owed {
	drafted := draftedFiles(files)
	var owed []Owed
	seen := map[string]bool{}
	add := func(file string, found []validateops.Placeholder) {
		for _, p := range found {
			id := file + "\x00" + joinKey(p.Keys)
			if seen[id] {
				continue
			}
			seen[id] = true
			o := Owed{File: file, Keys: p.Keys, Takes: p.Takes, Description: p.Description,
				Sensitive: p.Sensitive, Comment: keyComment(p.Key)}
			if p.Key != nil {
				o.line = p.Key.Line
			}
			owed = append(owed, o)
		}
	}

	var aggregate, release *abi.ScaffoldFile
	for i := range drafted {
		f := drafted[i]
		kind := draftKind(f.content)
		if kind == "" {
			continue
		}
		scaffold := abi.ScaffoldFile{Path: f.path, Content: f.content}
		switch {
		case kind == entitykind.Aggregate && aggregate == nil:
			aggregate = &scaffold
		case kind == entitykind.Release && release == nil:
			release = &scaffold
		}
		if found, err := validateops.PlaceholdersOf(envelope.Kind(kind), []byte(f.content), f.path); err == nil {
			add(f.path, found)
		}
	}

	cat, ok := recipes.(owedCatalog)
	if !ok || aggregate == nil {
		return sortedOwed(drafted, owed)
	}
	needs, _, err := readDraftNeeds(*aggregate, release, recipes)
	if err != nil {
		return sortedOwed(drafted, owed)
	}
	r := owedReader{lookup: cat.CatalogLookup(), needs: needs}
	if top, err := readDraft(aggregate.Content); err == nil {
		add(aggregate.Path, r.aggregate(top, []byte(aggregate.Content)))
	}
	if release != nil {
		if top, err := readDraft(release.Content); err == nil {
			add(release.Path, r.release(top, []byte(release.Content), aggregate.Content))
		}
	}
	return sortedOwed(drafted, owed)
}

// sortedOwed orders owed values by the order the drafts are written and, within
// one file, by where each sits in it, so the list, the questions and the answers
// file read top to bottom whichever schema found a value.
func sortedOwed(drafted []draftedFile, owed []Owed) []Owed {
	order := make(map[string]int, len(drafted))
	for i, f := range drafted {
		order[f.path] = i
	}
	out := slices.Clone(owed)
	slices.SortStableFunc(out, func(a, b Owed) int {
		if c := cmp.Compare(order[a.File], order[b.File]); c != 0 {
			return c
		}
		return cmp.Compare(a.line, b.line)
	})
	return out
}

// owedReader reads the drafted Aggregate and Release against the catalog schemas
// of what they declare.
//
// 🔴 It reads the drafts' YAML nodes, and that is deliberate: what it reports is a
// key path inside the document as written — the path an answer is applied at and
// the answers file names — and a placeholder's position, which the decoded types
// do not carry. Which entries a Release's boundary routes, and at which key path,
// is the Release's own enumeration; which type each instance is of is read off
// the one reading the drafted Manifest is composed from ([readDraftNeeds]), keyed
// by the id a build gives each instance and the name it gives each attachment.
type owedReader struct {
	lookup resolver.CatalogLookup
	needs  draftNeeds
}

// aggregate reads the drafted Aggregate's instances: their own pillars against
// their Components, their attachments against their Features, and their
// connections against their sockets.
func (r owedReader) aggregate(top *yaml.Node, data []byte) []validateops.Placeholder {
	_, spec := mappingEntry(top, "spec")
	var out []validateops.Placeholder
	var subsystems func(n *yaml.Node, keys []string)
	instances := func(n *yaml.Node, keys []string) {
		eachEntry(n, func(name string, inst *yaml.Node) {
			path := append(append([]string(nil), keys...), name)
			typ := scalarValue(mappingValue(inst, "type"))
			out = append(out, r.instance(typ, inst, data, path, true)...)
		})
	}
	subsystems = func(n *yaml.Node, keys []string) {
		eachEntry(n, func(name string, sys *yaml.Node) {
			path := append(append([]string(nil), keys...), name)
			_, comps := mappingEntry(sys, "components")
			instances(comps, append(append([]string(nil), path...), "components"))
			_, feats := mappingEntry(sys, "features")
			out = append(out, r.attachments(feats, data, append(append([]string(nil), path...), "features"), "")...)
			_, nested := mappingEntry(sys, "subsystems")
			subsystems(nested, append(append([]string(nil), path...), "subsystems"))
		})
	}
	_, comps := mappingEntry(spec, "components")
	instances(comps, []string{"spec", "components"})
	_, subs := mappingEntry(spec, "subsystems")
	subsystems(subs, []string{"spec", "subsystems"})
	return out
}

// release reads the drafted Release: its parameters against the drafted
// Aggregate's declarations, and each entry it routes — named directly, routed in
// through a module, at any depth of boundaries — against the Component the
// Aggregate names it as, with its attachments and its connections.
//
// Which entries a boundary declares, the id each carries and the key path it is
// written at are the Release's own enumeration ([types.BoundaryComponentDeclarations]);
// this reads the entry's node off that key path. A Release declaring its
// components at the top level is read as the one boundary those components form,
// whose key paths open at spec.
func (r owedReader) release(top *yaml.Node, data []byte, aggregateContent string) []validateops.Placeholder {
	_, spec := mappingEntry(top, "spec")
	var out []validateops.Placeholder
	if _, params := mappingEntry(spec, "parameters"); params != nil {
		out = append(out, validateops.PlaceholdersUnder(parameterSchema(aggregateContent), params, data,
			[]string{"spec", "parameters"})...)
	}
	var rel types.Release
	if err := yaml.Unmarshal(data, &rel); err != nil {
		return out
	}
	out = append(out, r.boundary(types.Boundary{Components: rel.Spec.Components}, spec, data, []string{"spec"})...)
	_, bounds := mappingEntry(spec, "boundaries")
	for name, b := range rel.Spec.Boundaries.All() {
		out = append(out, r.boundary(b, mappingValue(bounds, name), data, []string{"spec", "boundaries", name})...)
	}
	return out
}

// boundary reads one boundary written at node, whose keys lead to it along path:
// each component entry it declares, its own attachments, and the boundaries
// nested inside it.
func (r owedReader) boundary(b types.Boundary, node *yaml.Node, data []byte, path []string) []validateops.Placeholder {
	var out []validateops.Placeholder
	for _, decl := range types.BoundaryComponentDeclarations(b) {
		entry := node
		for _, key := range decl.Locator {
			entry = mappingValue(entry, key)
		}
		out = append(out, r.instance(r.typeOf(decl.ID), entry, data, slices.Concat(path, decl.Locator), false)...)
	}
	out = append(out, r.attachments(mappingValue(node, "features"), data, slices.Concat(path, []string{"features"}), "")...)
	for name, nested := range b.Boundaries.All() {
		out = append(out, r.boundary(nested, mappingValue(mappingValue(node, "boundaries"), name), data,
			slices.Concat(path, []string{"boundaries", name}))...)
	}
	return out
}

// typeOf is the component type of the instance with the given id, where the
// drafts give it exactly one.
func (r owedReader) typeOf(id string) string {
	if typs := r.needs.components.typesOf(id); len(typs) == 1 {
		return typs[0]
	}
	return ""
}

// featureTypeOf is the type an attachment is of: the one it writes, or, where it
// writes none, the one the drafts give the attachment of that name, where they
// give exactly one.
func (r owedReader) featureTypeOf(written, name string) string {
	if written != "" {
		return written
	}
	if typs := r.needs.features.typesOf(name); len(typs) == 1 {
		return typs[0]
	}
	return ""
}

// instance reads one instance or Release entry of typ: its own pillars against
// its Component, its attachments against their Features, and its connections
// against its sockets and their attachments. A type the catalog holds no schema
// for is read for nothing of its own.
func (r owedReader) instance(typ string, n *yaml.Node, data []byte, path []string, aggregate bool) []validateops.Placeholder {
	var out []validateops.Placeholder
	if typ != "" {
		if schema, ok := resolver.CompileComponentSchema(r.lookup, typ); ok {
			out = append(out, pillarPlaceholders(schema, n, data, path)...)
		}
	}
	_, feats := mappingEntry(n, "features")
	out = append(out, r.attachments(feats, data, append(append([]string(nil), path...), "features"), "")...)
	_, uses := mappingEntry(n, "uses")
	eachEntry(uses, func(socket string, aliases *yaml.Node) {
		eachEntry(aliases, func(alias string, conn *yaml.Node) {
			connPath := append(append([]string(nil), path...), "uses", socket, alias)
			if typ != "" {
				for _, pillar := range []string{"values", "infra"} {
					if !aggregate && pillar == "values" {
						continue
					}
					if schema, ok := resolver.CompileSocketPillarSchema(r.lookup, typ, socket, pillar); ok {
						out = append(out, pillarPlaceholders(schema, conn, data, connPath)...)
					}
				}
			}
			_, connFeats := mappingEntry(conn, "features")
			out = append(out, r.attachments(connFeats, data, append(append([]string(nil), connPath...), "features"),
				alias)...)
		})
	})
	return out
}

// attachments reads each attachment of a features block against its Feature.
// alias is the connection the block sits on, empty for an instance's own.
func (r owedReader) attachments(n *yaml.Node, data []byte, path []string, alias string) []validateops.Placeholder {
	var out []validateops.Placeholder
	eachEntry(n, func(slot string, att *yaml.Node) {
		name := slot
		if alias != "" {
			name = types.JoinConnectionAttachmentName(alias, slot)
		}
		typ := r.featureTypeOf(scalarValue(mappingValue(att, "type")), name)
		if typ == "" {
			return
		}
		if schema, ok := resolver.CompileFeatureSchema(r.lookup, typ); ok {
			out = append(out, pillarPlaceholders(schema, att, data, append(append([]string(nil), path...), slot))...)
		}
	})
	return out
}

// pillarPlaceholders reads n — an instance, an attachment or a connection — against
// compiled, a contract compiled with each pillar it declares nested under the
// pillar's name: each pillar n writes, in the order it writes them, is read against
// that pillar's own schema, its key path continuing path with the pillar's name.
//
// 🔴 Reading each pillar against its own schema, rather than n against compiled, is
// what makes a reference inside a pillar resolve as the catalog resolves it: a
// pointer written in a pillar — "#/$defs/<name>" naming a definition the pillar
// declares, or "#/properties/<name>" naming another of its properties — is read
// from the root of that pillar's schema, and from the root of compiled it names
// nothing, so the value under it would go unread.
func pillarPlaceholders(compiled map[string]any, n *yaml.Node, data []byte, path []string) []validateops.Placeholder {
	pillars, _ := compiled["properties"].(map[string]any)
	var out []validateops.Placeholder
	read := map[string]bool{}
	eachEntry(n, func(pillar string, value *yaml.Node) {
		schema, ok := pillars[pillar].(map[string]any)
		if !ok || read[pillar] {
			return
		}
		read[pillar] = true
		out = append(out, validateops.PlaceholdersUnder(schema, value, data, slices.Concat(path, []string{pillar}))...)
	})
	return out
}

// parameterSchema is the drafted Aggregate's parameter declarations as the
// reading reads a schema: each parameter a property carrying its declared type
// and description, read through the one decoding of a declaration
// ([types.DecodeParameterContract]). A declaration that does not decode is left
// out; the build refuses it where it is written.
func parameterSchema(aggregateContent string) map[string]any {
	var agg types.Aggregate
	if err := yaml.Unmarshal([]byte(aggregateContent), &agg); err != nil {
		return nil
	}
	props := map[string]any{}
	for name, entry := range agg.Spec.Parameters.All() {
		pc, err := types.DecodeParameterContract(entry)
		if err != nil {
			continue
		}
		prop := map[string]any{}
		if pc.Type != "" {
			prop["type"] = pc.Type
		}
		if pc.Description != "" {
			prop["description"] = pc.Description
		}
		props[name] = prop
	}
	if len(props) == 0 {
		return nil
	}
	return map[string]any{"type": "object", "properties": props}
}

// eachEntry calls fn for each entry of a block mapping, in the order written; a
// node that is not a mapping has none.
func eachEntry(n *yaml.Node, fn func(key string, value *yaml.Node)) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		fn(n.Content[i].Value, n.Content[i+1])
	}
}

// owedDiagnostic is the warning an owed value is reported under on every
// surface: the drafted file, the key, and what the value is.
func owedDiagnostic(o Owed) abi.Diagnostic {
	return warnDiagnostic(fmt.Sprintf("%s: %s is still empty: %s", o.File, o.Key(), o.Question()))
}

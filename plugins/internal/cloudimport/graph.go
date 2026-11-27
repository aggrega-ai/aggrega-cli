// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"sort"
	"strconv"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// Form is the form a node is written in.
type Form string

// The forms of a node.
const (
	// FormRaw is a resource written by its own type and properties.
	FormRaw Form = "raw"
	// FormAVM is an Azure Verified Module: one node whose Body holds the
	// module's parameters.
	FormAVM Form = "avm"
	// FormModule is a module of the source's own language: one node whose Body
	// holds the module's parameters.
	FormModule Form = "module"
)

// Prop is one member of an object or one item of an array in a node's
// properties.
type Prop struct {
	// Name is the member's key, empty for an array item.
	Name string
	// Value is a leaf's value, nil where Props or Items hold the subtree.
	Value Value
	// Props are an object's members, in the order the reader read them.
	Props []Prop
	// Items are an array's items, in order.
	Items []Prop
}

// ModuleRef identifies the module a FormAVM or FormModule node calls.
type ModuleRef struct {
	// Path is the module's path.
	Path string
	// Version is the module's version, empty where the source does not state it.
	Version string
	// Prefix is the module's telemetry prefix, empty where it has none.
	Prefix string
}

// Repetition is how a node repeats: one instance per key.
type Repetition struct {
	// Keys holds one key per instance — a literal count's indices, a literal
	// map's keys. Nil is a repetition the source computes when it deploys,
	// drafted as one instance with a repetition-not-expanded warning; an empty
	// slice is a literal repetition of no instance.
	Keys []string
	// Instance returns the node one key deploys, the key written wherever the
	// source reads the current instance's index or key.
	Instance func(key string) *Node
}

// Node is one resource or module call of a source.
type Node struct {
	// ID is the source's identifier of the node: a symbolic name, a logical
	// id, an address. IDs are unique within a graph.
	ID string
	// Type is the vendor's type of the resource.
	Type string
	// APIVersion is the vendor API version the source writes the resource in,
	// empty where the format has none.
	APIVersion string
	// Name is the resource's name.
	Name Value
	// Composed reports that Name is the full name the source composes of the
	// name of the node's parent and the node's own, as some sources name a
	// child resource; where it is false, Name is the node's own name.
	Composed bool
	// Leaf is the node's own name within its parent where Composed is set,
	// which a fold or an instance made of a child reads as the child's name;
	// nil where the source composes the two names so the import cannot tell
	// the node's own part, and then no fold writes the child's name.
	Leaf Value
	// Form is the form the node is written in.
	Form Form
	// Module is the module a FormAVM or FormModule node calls.
	Module *ModuleRef
	// Body is the resource below the keys the reader consumed; a module's
	// parameters for FormAVM and FormModule.
	Body []Prop
	// Parent is the ID of the node a child or extension resource belongs to.
	Parent string
	// Condition is the condition the node is deployed under, nil where it is
	// deployed unconditionally.
	Condition Value
	// Repeat is how the node repeats, nil where it does not.
	Repeat *Repetition
	// Scope is the scope the node deploys into, as report text, where it is not
	// the one the source deploys into; empty where it is.
	Scope string
	// Existing reports that the source reads the resource and does not create it.
	Existing bool
	// DependsOn are the IDs of the nodes the source states this node depends
	// on, as written.
	DependsOn []string
	// Source is the file and JSON pointer the node is read from, named in
	// decisions and comments.
	Source string
}

// Parameter is one parameter the source declares.
type Parameter struct {
	Name, Type, Description string
	// Allowed are the values the parameter allows, empty where it allows any.
	Allowed []json.RawMessage
	// Min and Max bound a number, nil where unbounded.
	Min, Max *json.Number
	// MinLength and MaxLength bound a string's or an array's length, nil where
	// unbounded.
	MinLength, MaxLength *int
	// Default is the parameter's default, nil where it has none.
	Default Value
	// Generated names, as a warning names it, the call by which the format
	// reads the source generating the default afresh each time it deploys;
	// empty where the format reads the default as no such call.
	Generated string
	// Secure reports that the source declares the parameter's value secret.
	Secure bool
	// Supplied is the value a parameters file supplies, nil where none does.
	Supplied Value
	// StoreRef is a secret-store locator a parameters file names instead of a
	// value.
	StoreRef string
	// Pointer reports that the parameter names an existing resource of the
	// target's cloud, whose value differs by environment: a values key reading
	// it reads a parameter of the Aggregate that declares no default, which the
	// Release fulfils under its spec.parameters, left for the answers file, the
	// comment above it naming the value the parameters file supplies; an infra
	// or infra.overrides key reading it is written in the Release entry as any
	// parameter's value is.
	Pointer bool
}

// Given returns the literal value p is given: the value the parameters file
// supplies, else its literal default; false where it is given neither.
func (p Parameter) Given() (Literal, bool) {
	for _, v := range []Value{p.Supplied, p.Default} {
		if l, ok := v.(Literal); ok {
			return l, true
		}
	}
	return Literal{}, false
}

// Output is one output the source declares.
type Output struct {
	Name  string
	Value Value
}

// Graph is a source read into the core's model.
type Graph struct {
	// Format is the name of the source's format.
	Format string
	// Nodes are the source's nodes, ordered by ID.
	Nodes []*Node
	// Params are the source's parameters.
	Params []Parameter
	// Outputs are the source's outputs.
	Outputs []Output
	// Read and NotRead are the names of the sources the import read and of
	// those it was handed and did not read.
	Read, NotRead []string
	// Notes are the lines the format adds to the comment opening the drafted
	// Aggregate, after the sources read and not read, in order: what it read of
	// the source that no resource carries, such as a section it names and does
	// not read. Each line of a note is one comment line, written as it is, so a
	// format writes each text it reads from the source in a note through
	// draftyaml.Comment.
	Notes []string
	// Expanded holds, for each repeated node [Expand] replaced by its
	// instances or removed, the IDs of the nodes it deploys, in key order:
	// none for a node that deploys no instance. A Ref, Credential, dependency
	// or parent naming such a node names these.
	Expanded map[string][]string
}

// EdgeKind is what makes a node consume another.
type EdgeKind int

// The kinds of an edge.
const (
	// EdgeRef is a reference to an attribute of the producer.
	EdgeRef EdgeKind = iota
	// EdgeCredential is a credential read from the producer.
	EdgeCredential
	// EdgeDepends is a dependency the consumer states.
	EdgeDepends
)

// Edge is one thing node From consumes from node To.
type Edge struct {
	// From is the ID of the consuming node and To that of the producing node.
	From, To string
	// Kind is what makes From consume To.
	Kind EdgeKind
	// Path is the property path in From's Body where the reference or
	// credential sits: a member's key, or an array item's index in decimal. It
	// is empty for a dependency.
	Path []string
	// Attr is the attribute of To a reference reads, empty for the producer's
	// identifier, a credential and a dependency.
	Attr []string
}

// Edges returns the edges of g: for each node in ID order, every Ref and
// Credential of its Body — a leaf's value or a part of a composed one — with
// the path where it sits, in the order the Body holds them, then each of its
// DependsOn in the order written. A reference naming no node of g is an edge
// still.
func Edges(g *Graph) []Edge {
	return edges(byID(g))
}

// Keys returns the instance key of each of nodes, aligned with them: the key
// every importer gives a source item, [importer.UniqueKey] of the node's ID,
// so no two nodes share a key and none is empty. It is the keys of a format
// with no Key ([Format.Keys]).
func Keys(nodes []*Node) []string {
	return Format{}.Keys(nodes)
}

// Keys returns the instance key of each of nodes, aligned with them: the key
// f.Key gives the node's ID, or the ID itself where f has no Key, written as
// [importer.UniqueKey] writes a key, so no two nodes share a key and none is
// empty. Two nodes whose IDs f.Key gives one key take it and it numbered, in
// the order of nodes: db, then db-2.
func (f Format) Keys(nodes []*Node) []string {
	used := map[string]bool{}
	keys := make([]string, len(nodes))
	for i, n := range nodes {
		id := ""
		if n != nil {
			id = n.ID
			if f.Key != nil {
				id = f.Key(id)
			}
		}
		keys[i] = importer.UniqueKey(used, id)
	}
	return keys
}

// byID returns the nodes of g sorted by ID, leaving g as it is.
func byID(g *Graph) []*Node {
	if g == nil {
		return nil
	}
	nodes := make([]*Node, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		if n != nil {
			nodes = append(nodes, n)
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	return nodes
}

// edges returns the edges of nodes, in their order.
func edges(nodes []*Node) []Edge {
	var out []Edge
	for _, n := range nodes {
		for _, p := range n.Body {
			out = propEdges(out, n.ID, p, []string{p.Name})
		}
		for _, to := range n.DependsOn {
			out = append(out, Edge{From: n.ID, To: to, Kind: EdgeDepends})
		}
	}
	return out
}

// propEdges appends the edges of p, which sits at path in from's Body.
func propEdges(out []Edge, from string, p Prop, path []string) []Edge {
	out = valueEdges(out, from, p.Value, path)
	for _, m := range p.Props {
		out = propEdges(out, from, m, extend(path, m.Name))
	}
	for i, item := range p.Items {
		out = propEdges(out, from, item, extend(path, strconv.Itoa(i)))
	}
	return out
}

// valueEdges appends the edges of v, which sits at path in from's Body.
func valueEdges(out []Edge, from string, v Value, path []string) []Edge {
	switch v := v.(type) {
	case Ref:
		return append(out, Edge{From: from, To: v.Node, Kind: EdgeRef, Path: path, Attr: v.Attr})
	case Credential:
		return append(out, Edge{From: from, To: v.Node, Kind: EdgeCredential, Path: path})
	case Compose:
		for _, part := range v.Parts {
			out = valueEdges(out, from, part, path)
		}
	}
	return out
}

// extend returns path with seg appended, in a slice of its own, so edges never
// share a path's backing array.
func extend(path []string, seg string) []string {
	out := make([]string, len(path), len(path)+1)
	copy(out, path)
	return append(out, seg)
}

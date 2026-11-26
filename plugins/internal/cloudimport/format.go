// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// ParseFunc parses a source as the syntactic format named syntax into its
// canonical tree: the host's parse, which an importer's import export wires in.
type ParseFunc = schemadraft.ParseFunc

// Format is everything the core needs that depends on the format of the
// source it imports.
type Format struct {
	// Name is the format's name, the <format> of the import:<format>
	// capability the importer advertises. Target is the target the format's
	// own Recipes deploy on: the target a row's Recipe and a drafted Recipe
	// realize their Component on. Syntax is the syntactic format a source is
	// parsed as.
	Name, Target, Syntax string
	// Read reads the request's sources into the core's graph, with what it
	// reports of them; a halt among its diagnostics drafts nothing.
	Read func(in aggrega.ImportInput, s Settings, parse ParseFunc) (*Graph, []aggrega.Diagnostic)
	// Table is the format's mapping table.
	Table Table
	// Types are the format's type rules: the types it leaves to the operator
	// as substrate, the types no Recipe carries, and what the decision on a
	// resource of a type says beside its class.
	Types []TypeRule
	// Attach are the format's attach rules: the types whose nodes it writes as
	// a Feature attachment on another node's instance rather than as an
	// instance of their own, as [AttachRule] says.
	Attach []AttachRule
	// Key gives the instance key of a node from its ID, which [Format.Keys]
	// then makes unique as every importer's keys are; nil keys a node by its
	// ID alone.
	Key func(id string) string
	// Pointer returns the target's deterministic pointer text for a value
	// referencing a substrate or existing resource, read in g, the graph the
	// value is read from, its repetitions expanded; false where the target has
	// none for it.
	Pointer func(v Value, g *Graph) (string, bool)
	// Text writes a literal string as the target's text; nil writes it as is.
	Text func(s string) string
	// Draft is how the format drafts a Component and a Recipe for a resource
	// no row maps: its naming, its categories, the provider its Recipes
	// target, its placement rules and its Recipe writer.
	Draft schemadraft.Format
	// DraftProvides is what the Recipe drafted for a resource from the source
	// publishes before each attribute another node reads of it: nil publishes
	// the resource's identifier under id and its attribute name under name,
	// and a list given, empty included, publishes that list in its place. A
	// format sets it where the attribute name is not one its target reads back
	// of every resource, such as id alone.
	DraftProvides []schemadraft.Provide
	// Wrap writes the Recipe sourcing whole a module no row maps; nil where
	// the format's reader flattens such a module into its resources.
	Wrap schemadraft.ModuleWriter
	// Module locates the module a node of a module form calls, from the
	// ModuleRef the reader gives the node: the source kind and the address
	// the Recipe Wrap writes sources it from, and the parameters that Recipe
	// binds itself. The version the Recipe sources is the one the ModuleRef
	// states. A module whose ModuleSource has no kind or no address is not
	// located: its node is not carried, nothing is drafted for it, and a
	// linked-source-not-read warning names it. Nil where Wrap is nil; a format
	// setting Wrap and no Module halts on a module no row maps.
	Module func(ref ModuleRef) ModuleSource
}

// ModuleSource is where the Recipe drafted for a module no row maps sources
// the module from, and what that Recipe binds itself, as a format's Module
// gives it.
type ModuleSource struct {
	// Kind is the Recipe's source kind, and URL the module's address.
	Kind, URL string
	// Fixed are the parameters the Recipe binds itself, in order, which no
	// Release sets: a parameter of one of these names the source sets is not
	// carried, and is reported value-not-carried. Each Value is a single-line
	// YAML scalar, written as it is.
	Fixed []importer.Entry
}

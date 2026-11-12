// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package clisurface projects a live cobra command tree into a structured,
// deterministic catalog of command descriptors that other surfaces consume.
//
// The command tree remains the single source of the command surface: this
// package reads it and records what it finds, and holds no list of its own. A
// command exists in the catalog because it exists in the tree, so a command
// added to the tree appears in every consumer without a second place to update.
//
// The catalog is deliberately unfiltered. Hidden commands are present, carrying
// Hidden so a consumer can decide, and nothing is dropped by name — a command
// named help or completion is an ordinary member. Which commands to show, and in
// what order to show them, is presentation policy that belongs to each consumer:
// a documentation renderer wants a name-sorted, hidden-excluding view, while a
// completeness check wants everything. Filtering here would force one consumer's
// policy on all of them.
//
// One consequence of reading the tree rather than predicting it: cobra creates
// its own help and completion commands when a tree runs, not when it is built,
// so whether those appear in the catalog follows from whether they are in the
// tree the caller passes. The catalog reports the tree it is given.
//
// Every field except Parent is recorded verbatim from cobra; Parent carries the
// parent command's path, which is what keeps the tree relation readable once the
// tree is flattened into entries. Nothing is inferred or reformatted, so a
// descriptor cannot disagree with the tree it came from.
package clisurface

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Flag describes one flag as the command declares it.
type Flag struct {
	// Name is the flag's long name, without the leading dashes.
	Name string
	// Shorthand is the flag's single-letter form without the leading dash,
	// empty when the flag has none.
	Shorthand string
	// Type is the flag value's type name as pflag reports it, such as
	// "string", "bool", or "stringSlice".
	Type string
	// Usage is the flag's help text.
	Usage string
	// Default is the flag's default value in the string form pflag records for
	// the help text, which is the value's own rendering rather than an empty
	// string: a bool defaulting to false records "false". It is empty only
	// where the value type renders empty, as an unset string flag does.
	Default string
	// Hidden reports whether the flag is declared hidden, so a consumer that
	// omits hidden flags can filter on it.
	Hidden bool
}

// Descriptor describes one command in the tree.
type Descriptor struct {
	// Name is the command's own name, without any ancestry.
	Name string
	// Path is the command's full invocation path — the names of the command
	// and all its ancestors joined by spaces, starting at the root's name.
	// It is unique within a catalog and is the key other entries reference.
	Path string
	// Parent is the Path of the command's parent, empty for the root.
	Parent string
	// Use is the command's usage declaration, whose first token is the
	// command name and whose remaining tokens are the positional-argument
	// summary the command declares.
	Use string
	// Synopsis is the command's full usage line: the invocation path, the
	// positional-argument summary, and a flags marker when the command
	// accepts flags.
	Synopsis string
	// Short is the command's one-line help.
	Short string
	// Long is the command's long-form help, empty when it declares none.
	Long string
	// Hidden reports whether the command is declared hidden, so a consumer
	// that omits hidden commands can filter on it.
	Hidden bool
	// LocalFlags are the flags that belong to this command, excluding the
	// persistent flags it inherits from its ancestors.
	LocalFlags []Flag
	// InheritedFlags are the persistent flags this command's ancestors
	// declare, which apply to it without being declared on it.
	InheritedFlags []Flag
	// PersistentFlags are the flags this command declares persistently, which
	// its descendants inherit. They are a subset of LocalFlags.
	PersistentFlags []Flag
}

// Catalog returns one Descriptor per command in root's tree, root included.
//
// The walk is depth-first and pre-order: a command precedes its descendants,
// and children appear in the order cobra yields them, which under cobra's
// default command sorting is lexical by name. The first entry is always root
// itself. The order is a property of the tree alone, so identical trees produce
// identical catalogs.
//
// A nil root yields a nil catalog. Catalog reads the tree and adds nothing to
// it; note that asking cobra for a command's children normalizes their order in
// place, as rendering help does.
func Catalog(root *cobra.Command) []Descriptor {
	if root == nil {
		return nil
	}
	var out []Descriptor
	var walk func(cmd *cobra.Command, parent string)
	walk = func(cmd *cobra.Command, parent string) {
		out = append(out, describe(cmd, parent))
		path := cmd.CommandPath()
		for _, sub := range cmd.Commands() {
			walk(sub, path)
		}
	}
	walk(root, "")
	return out
}

// Children returns the entries of cat whose immediate parent is parentPath, in
// catalog order. It is the one home for the parent relation, so no consumer
// reconstructs it from path strings. Passing the empty string returns the root
// entry, whose Parent is empty.
func Children(cat []Descriptor, parentPath string) []Descriptor {
	var out []Descriptor
	for _, d := range cat {
		if d.Parent == parentPath {
			out = append(out, d)
		}
	}
	return out
}

func describe(cmd *cobra.Command, parent string) Descriptor {
	return Descriptor{
		Name:            cmd.Name(),
		Path:            cmd.CommandPath(),
		Parent:          parent,
		Use:             cmd.Use,
		Synopsis:        cmd.UseLine(),
		Short:           cmd.Short,
		Long:            cmd.Long,
		Hidden:          cmd.Hidden,
		LocalFlags:      describeFlags(cmd.LocalFlags()),
		InheritedFlags:  describeFlags(cmd.InheritedFlags()),
		PersistentFlags: describeFlags(cmd.PersistentFlags()),
	}
}

func describeFlags(fs *pflag.FlagSet) []Flag {
	if fs == nil {
		return nil
	}
	var out []Flag
	fs.VisitAll(func(f *pflag.Flag) {
		out = append(out, Flag{
			Name:      f.Name,
			Shorthand: f.Shorthand,
			Type:      f.Value.Type(),
			Usage:     f.Usage,
			Default:   f.DefValue,
			Hidden:    f.Hidden,
		})
	})
	return out
}

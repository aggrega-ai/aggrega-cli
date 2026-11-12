// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package nodeorigin records which document a YAML node was read from, for a node
// that sits in the tree of a different document.
//
// A schema a catalog entity imports through `$import:` is parsed from its own file,
// and its content then replaces the directive inside the entity. From there on the
// node's line is a line of the imported file while the node sits in the entity's
// tree, and nothing on a yaml.Node says which file that line belongs to. A
// diagnostic pairing the entity's path with that line would send its reader to a
// line of a file that does not hold the fault. The substitution states here where
// the content came from, and a diagnostic locating a node reads it here, so the path
// and the line a location names always come from one document.
//
// The record is keyed by the node itself, not by an entity or a field name. A node
// carries its record through every reader the entity reaches, whichever copy of the
// entity holds it, and two parses of one file — a saved document and an unsaved
// buffer of it, say — never read each other's record. The key is weak, so a record
// lives as long as its node does: a process that parses entities for as long as it
// runs keeps nothing for content it has let go.
package nodeorigin

import (
	"runtime"
	"sync"
	"weak"

	"go.yaml.in/yaml/v3"
)

// origins maps a weak reference to a node onto the document the node was read
// from. Two weak references compare equal only when they were made from the same
// node, so a key never answers for a node allocated later at a reclaimed address.
var origins sync.Map

// Record states that node, and every node beneath it, was read from document.
//
// Recording a node again replaces what was recorded for it. A chain of imports
// relies on that: a node whose imported content was itself an import, and was
// replaced again, was read from the last file the chain reached.
func Record(node *yaml.Node, document string) {
	if node == nil {
		return
	}
	key := weak.Make(node)
	if _, seen := origins.Swap(key, document); !seen {
		runtime.AddCleanup(node, forget, key)
	}
	for _, child := range node.Content {
		Record(child, document)
	}
}

// forget drops the record of a node that is no longer reachable.
func forget(key weak.Pointer[yaml.Node]) {
	origins.Delete(key)
}

// Document returns the document node was read from, and whether one is recorded.
//
// A mapping a Go struct holds by value rather than through a pointer — an entity's
// schema block, a socket's contract — is copied with the struct that holds it, so
// the copy is a node nothing recorded. Its keys are shared by every copy, and a
// mapping's keys are always read from the document the mapping was read from, so
// such a mapping is answered for by its first key.
func Document(node *yaml.Node) (string, bool) {
	if node == nil {
		return "", false
	}
	if document, ok := recorded(node); ok {
		return document, true
	}
	if node.Kind == yaml.MappingNode && len(node.Content) > 0 {
		return recorded(node.Content[0])
	}
	return "", false
}

// DocumentOr returns the document a position on node belongs to: the document the
// node was read from, or holder — the document whose tree the node sits in — where
// nothing records another.
func DocumentOr(node *yaml.Node, holder string) string {
	if document, ok := Document(node); ok {
		return document
	}
	return holder
}

// recorded reads the record of exactly node.
func recorded(node *yaml.Node) (string, bool) {
	document, ok := origins.Load(weak.Make(node))
	if !ok {
		return "", false
	}
	return document.(string), true
}

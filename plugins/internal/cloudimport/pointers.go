// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
)

// A value read from a substrate or existing resource is a pointer the Release
// holds. In the entry of a drafted instance, where the import's target is the
// format's own, it is the text the format's Pointer gives for the whole value,
// written as it is, which the drafted Recipe passes to the resource as the
// source does. In the entry of a mapped instance it is left for the answers
// file: a catalog Recipe takes the resource's literal identifier, which only
// the operator knows when the draft is written, so the comment names the
// resource, its type and the text the format's Pointer gives for it, from
// which the operator writes the identifier. On any other target, or where the
// format has no pointer for the value, the key is left for the answers file,
// its comment naming the resource and its type. Text the source evaluates when
// it deploys is written the same way where its target is the import's, and
// left otherwise.

// refsOf returns the references v reads, in order.
func refsOf(v Value) []Ref {
	switch v := v.(type) {
	case Ref:
		return []Ref{v}
	case Compose:
		var out []Ref
		for _, part := range v.Parts {
			out = append(out, refsOf(part)...)
		}
		return out
	}
	return nil
}

// readsPointer reports whether v reads a resource the Release points at.
func readsPointer(v Value, cs classLookup) bool {
	for _, r := range refsOf(v) {
		if cs.reach(r.Node).pointer {
			return true
		}
	}
	return false
}

// resourceText names the resource id in a comment: its ID and, where the node
// is declared, its type.
func (w *aggregateWriter) resourceText(id string) string {
	if cl := w.cs.byID[id]; cl != nil && cl.Node.Type != "" {
		return "the resource " + draftyaml.Comment(id) + " (" + draftyaml.Comment(cl.Node.Type) + ")"
	}
	return "the resource " + draftyaml.Comment(id)
}

// pointerText writes the pointer the Release holds for the value v, which reads
// resources the Release points at, and what became of writing it.
func (w *aggregateWriter) pointerText(v Value) (string, outcome) {
	var named []string
	for _, r := range refsOf(v) {
		reached := w.cs.reach(r.Node)
		if len(reached.instances) > 0 || len(reached.held) > 0 || len(reached.none) > 0 || !reached.pointer {
			return "", outcome{why: "it reads an attribute of " + r.Node + ", which a connection between instances " +
				"delivers and no Release key holds"}
		}
		named = append(named, w.resourceText(r.Node))
	}
	expression := ""
	if w.f.Pointer != nil {
		if text, ok := w.f.Pointer(v); ok {
			expression = text
		}
	}
	if w.mappedEntry {
		why := "the pointer to " + andList(named) + ", which the draft leaves to the operator"
		if expression != "" {
			why += " and the source names as " + draftyaml.Comment(expression)
		}
		return "", outcome{why: why + "; the catalog Recipe takes the resource's literal identifier: write it",
			leave: true}
	}
	if expression != "" && w.ownTarget() {
		return draftyaml.String(expression), written()
	}
	return "", outcome{why: "the pointer to " + andList(named) + ", which the draft leaves to the operator; write the " +
		"target's pointer to it", leave: true}
}

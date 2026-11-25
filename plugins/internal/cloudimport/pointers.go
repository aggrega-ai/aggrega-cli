// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
)

// A value read from a substrate or existing resource is a pointer the Release
// holds: where the import's target is the format's own, the text the format's
// Pointer gives for the whole value, written as it is; on any other target, or
// where the format has no pointer for the value, the key is left for the
// answers file, its comment naming the resource and its type. Text the source
// evaluates when it deploys is written the same way where its target is the
// import's, and left otherwise.

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
	if w.f.Pointer != nil && w.ownTarget() {
		if text, ok := w.f.Pointer(v); ok {
			return draftyaml.String(text), written()
		}
	}
	return "", outcome{why: "the pointer to " + andList(named) + ", which the draft leaves to the operator; write the " +
		"target's pointer to it", leave: true}
}

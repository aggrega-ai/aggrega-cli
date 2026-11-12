// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ownValuesRoot and ownProvidesRoot are the two own pillars a component-level
// inject entry can deliver from, spelled as a Recipe template reads them, so the
// key an entry maps to is the path that names the same pillar key in the Recipe's
// own context.
const (
	ownValuesRoot   = "values"
	ownProvidesRoot = "provides"
)

// selfInjectOwnKeys maps each delivery name the author wrote in a component's
// own inject block to the own key that entry delivers, per mechanism — the shape
// a connection's inject map has at .context.uses.<alias>.inject, where each local
// name maps to the provides key it reads.
//
// An entry delivering one of the component's own provides (from: <key>) maps to
// "provides.<key>". An entry whose value: template reads exactly one of the
// component's own values, and nothing else, maps to "values.<key>", where <key>
// is that value's top-level key — however deeply the template reads into the
// value, and whatever literal text or functions surround the read, because what
// is reported is the one own value the entry's template reads, not how it is
// spelled. Every other entry maps to the empty string: a literal, a template
// reading two values, a template reading anything beyond the values (infra,
// metadata, the node's identity), and a template whose read of the values is not
// attributed to one top-level key from its text — the values read whole, or
// handed to a function by their root. An empty answer is the honest one where the
// text does not decide the value; nothing here guesses.
//
// The entries are the delivery's own — read through normalizeSelfInjectEntries,
// with which half of an entry counts answered by [selfInjectEntry.providesKey] —
// so this map cannot name an entry the delivery skips or read the other half of
// one it delivers. Which values a template reads is the shared reading every
// reference check is built on — the keys of one root and whether that list is
// complete, then every reference the template makes at all — so this answer
// cannot come to disagree with what the template is admitted or refused for
// reading.
//
// Every mechanism the block names is present, custom ones included, in the order
// selfInjectMechanisms walks them for the delivery; within a mechanism the entries
// keep the order the author wrote them. A nil block yields an empty map, so the context always carries the key.
// Nothing is resolved and no value is read: the result is a function of the
// authored text alone.
func selfInjectOwnKeys(block *types.InjectBlock) types.OrderedMap[types.OrderedMap[string]] {
	out := types.OrderedMap[types.OrderedMap[string]]{}
	for mech, raw := range selfInjectMechanisms(block) {
		out.Set(mech, selfInjectMechanismOwnKeys(raw))
	}
	return out
}

// selfInjectMechanismOwnKeys maps one mechanism's entries, in the order the
// author wrote them.
func selfInjectMechanismOwnKeys(raw any) types.OrderedMap[string] {
	out := types.OrderedMap[string]{}
	entries := normalizeSelfInjectEntries(raw)
	for in, entry := range entries.All() {
		if from, ok := entry.providesKey(); ok {
			out.Set(in, ownProvidesRoot+"."+from)
			continue
		}
		out.Set(in, selfInjectValueOwnKey(entry.value))
	}
	return out
}

// selfInjectValueOwnKey is the own key a value: template delivers:
// "values.<key>" when every reference the template makes is a read of the one own
// value whose top-level key is <key>, and the empty string otherwise.
//
// Two readings decide it, and both are needed. The top-level keys of .values the
// template reads must be exactly one and the list complete — a template reading
// the values whole, or handing on the context itself, can reach any value, so no
// single one can be named. And every reference the template makes must sit under
// that one value, so an entry combining a value with infra, metadata or the node's
// identity is not read as delivering that value. A reference reaching deeper into
// the value — .values.cfg.port, or the value handed to a function as
// index .values.cfg "port" — is a read of that one value.
func selfInjectValueOwnKey(text string) string {
	keys, complete, err := gotmpl.RootKeysRead(text, "{{", "}}", ownValuesRoot)
	if err != nil || !complete || len(keys) != 1 {
		return ""
	}
	tree, err := gotmpl.ParseTree(text, "{{", "}}")
	if err != nil {
		return ""
	}
	for _, ref := range gotmpl.CollectRootRefs(tree) {
		if len(ref.Segs) < 2 || ref.Segs[0] != ownValuesRoot || ref.Segs[1] != keys[0] {
			return ""
		}
	}
	return ownValuesRoot + "." + keys[0]
}

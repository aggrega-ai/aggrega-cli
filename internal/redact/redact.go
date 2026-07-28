// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package redact holds the one replacement text every human-readable surface
// substitutes for a datum an author declared sensitive, and the appliers that
// perform the substitution without changing the shape of what is displayed.
//
// The boundary this package sits on is narrow and deliberate: it covers what
// the tool PRINTS for a human to read — the per-pass trace and the inspect
// layouts — and never what the tool WRITES. A rendered artifact is emitted
// verbatim, because it is the deployable and a placeholder inside it would be
// a broken deployment rather than a protected one.
//
// Nothing here decides WHAT is sensitive. The two facts it consumes are read
// from their own single homes upstream — the authored leaf declarations on an
// entity's schema, and the per-delivery-key marking the graph carries on an
// attachment — and arrive as a Keys set. No value is inspected and no meaning
// is read out of a key's name.
package redact

import "github.com/aggrega-ai/aggrega-cli/pkg/types"

// placeholder is the replacement text, declared exactly once in the whole
// module. Every surface reaches it through Placeholder; a surface that spelled
// its own would make "is this value redacted consistently?" unanswerable, and
// the two would drift the first time one gained a length hint or a type
// suffix. TestPlaceholderLiteralHasOneHome enforces the single spelling.
const placeholder = "<redacted>"

// Placeholder returns the fixed text that stands in for a declared-sensitive
// value on a human-readable surface. It is deliberately shape-free: it carries
// no length, no type and no hash of the value it replaces, so nothing about
// the hidden datum can be recovered from what is printed.
func Placeholder() string { return placeholder }

// Keys names, for one node, which data a surface must replace. It is a
// description of declarations, assembled by NodeKeys from the two upstream
// facts; it holds no values and answers no question about what a datum means.
//
// Values and Infra are keyed by the flat-dotted property path from the pillar
// root — the spelling types.FlattenLeaves produces and the authored leaf
// declarations use, so the two join by key. Inject is keyed by delivery
// mechanism and then by delivery key, which is the shape the attachment's
// marking and the delivery pipe both carry.
type Keys struct {
	Values map[string]bool
	Infra  map[string]bool
	Inject map[string]map[string]bool
}

// Empty reports whether the set names nothing. A caller holding an empty set
// can skip every applier: each one returns its input unchanged, so a node with
// no declared sensitivity produces byte-identical output to a build predating
// the axis.
func (k Keys) Empty() bool {
	return len(k.Values) == 0 && len(k.Infra) == 0 && len(k.Inject) == 0
}

// Leaf reports whether the named pillar leaf is declared sensitive. The path is
// the flat-dotted chain from the pillar root, without the pillar prefix. An
// unrecognised pillar name reports false rather than guessing.
func (k Keys) Leaf(pillar, path string) bool {
	switch pillar {
	case "values":
		return k.Values[path]
	case "infra":
		return k.Infra[path]
	}
	return false
}

// Delivery reports whether the named delivery key of the named mechanism
// carries material inherited from a sensitive declaration.
func (k Keys) Delivery(mechanism, key string) bool {
	return k.Inject[mechanism][key]
}

// Pillar returns a copy of a values-or-infra pillar with every declared
// sensitive leaf's value replaced by the placeholder. The key survives: a
// reader debugging a build must still see that the leaf exists and that
// something was withheld, because a display whose structure differs from the
// real one is a worse debugging surface than no display at all.
//
// The input is never mutated. The pillar this reads is the same map the render
// context carries into the artifacts, so an in-place substitution here would
// reach the deployable — the copy is what keeps redaction on the display side
// of the boundary rather than upstream of rendering.
//
// A path names a leaf, so a sensitive declaration on an intermediate object
// replaces that whole subtree with the placeholder rather than descending into
// it. When paths is empty the input is returned as-is.
func Pillar(in types.OrderedMap[any], paths map[string]bool) types.OrderedMap[any] {
	if len(paths) == 0 || in.Len() == 0 {
		return in
	}
	return replacePillar("", in, paths)
}

func replacePillar(prefix string, in types.OrderedMap[any], paths map[string]bool) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	for k, v := range in.All() {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if paths[path] {
			out.Set(k, placeholder)
			continue
		}
		// The descent mirrors types.FlattenLeaves exactly — same two nested
		// map shapes, same "empty map is a leaf" rule — so a declared path
		// that the flattener can produce is a path this walk can reach.
		switch n := v.(type) {
		case *types.OrderedMap[any]:
			if n == nil || n.Len() == 0 {
				out.Set(k, v)
				continue
			}
			nested := replacePillar(path, *n, paths)
			out.Set(k, &nested)
		case types.OrderedMap[any]:
			if n.Len() == 0 {
				out.Set(k, v)
				continue
			}
			out.Set(k, replacePillar(path, n, paths))
		default:
			out.Set(k, v)
		}
	}
	return out
}

// InjectPipe returns a copy of a delivery pipe with every marked delivery key's
// value replaced by the placeholder, preserving mechanism order, key order and
// every unmarked entry.
//
// The mapping onto a typed mechanism is exact rather than approximate: that
// mechanism's pillar is one flat merged map across self-inject, uses-inject and
// attachment-inject, and a delivery key already present when another source
// tries to set it halts the build, so a delivery key identifies exactly one
// entry within its mechanism on a node.
//
// The input is never mutated. When marked is empty the input is returned
// as-is.
func InjectPipe(in types.InjectPipe, marked map[string]map[string]bool) types.InjectPipe {
	if len(marked) == 0 {
		return in
	}
	out := in
	out.Env = replaceDelivery(in.Env, marked["env"])
	out.File = replaceDelivery(in.File, marked["file"])
	out.Arg = replaceDelivery(in.Arg, marked["arg"])
	// `all` is the host-built union of the typed mechanisms rather than an
	// authored pillar, so the same datum reappears here and the union must not
	// become the hole that re-prints what the typed pillar hid. The lookup is
	// the union of every mechanism's marking, deliberately wider than exact:
	// which mechanism won a same-named key in the merge is the merge's own
	// precedence rule, and reproducing it here would put that rule in a second
	// place. Withholding both is the safe side of that trade, and it costs only
	// the union view of one key authored under two mechanisms.
	out.All = replaceDelivery(in.All, unionMarked(marked))
	if in.Additional.Len() > 0 {
		additional := types.OrderedMap[types.OrderedMap[string]]{}
		for mechanism, deliveries := range in.Additional.All() {
			additional.Set(mechanism, replaceDelivery(deliveries, marked[mechanism]))
		}
		out.Additional = additional
	}
	return out
}

func replaceDelivery(in types.OrderedMap[string], keys map[string]bool) types.OrderedMap[string] {
	if len(keys) == 0 || in.Len() == 0 {
		return in
	}
	out := types.OrderedMap[string]{}
	for k, v := range in.All() {
		if keys[k] {
			out.Set(k, placeholder)
			continue
		}
		out.Set(k, v)
	}
	return out
}

func unionMarked(marked map[string]map[string]bool) map[string]bool {
	union := map[string]bool{}
	for _, deliveries := range marked {
		for key := range deliveries {
			union[key] = true
		}
	}
	return union
}

// NodeKeys assembles one node's redaction set from the two facts, each read
// from its own home and neither recomputed here.
//
// declaredValues and declaredInfra are the leaf paths the host Component's
// schema declares sensitive, read through the single authored-declaration
// reader. attachments supplies the per-delivery-key marking the graph already
// carries on each attachment, folded across the node's attachments into the
// mechanism-then-key shape the pipe is displayed in.
//
// The per-attachment sensitivity roll-up is deliberately NOT consulted. It says
// an attachment handles secret material; it does not say which of its leaves
// do, and treating it as if it did would print the placeholder over a
// referenceable credential's name and key — public locators an author left
// unmarked on purpose.
func NodeKeys(declaredValues, declaredInfra map[string]bool, attachments types.OrderedMap[types.IRFeatureEntry]) Keys {
	keys := Keys{}
	if len(declaredValues) > 0 {
		keys.Values = declaredValues
	}
	if len(declaredInfra) > 0 {
		keys.Infra = declaredInfra
	}
	for _, entry := range attachments.All() {
		for mechanism, deliveries := range entry.SensitiveInject.All() {
			for deliveryKey := range deliveries.All() {
				if keys.Inject == nil {
					keys.Inject = map[string]map[string]bool{}
				}
				if keys.Inject[mechanism] == nil {
					keys.Inject[mechanism] = map[string]bool{}
				}
				keys.Inject[mechanism][deliveryKey] = true
			}
		}
	}
	return keys
}

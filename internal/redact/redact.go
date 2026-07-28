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
// Nothing here decides WHAT is sensitive. The facts it consumes are read from
// their own single homes upstream — the authored leaf declarations on an
// entity's schema, the per-delivery-key marking the graph carries on an
// attachment, and the reading of which template outputs were computed from a
// declared leaf — and arrive as a Keys set. No value is inspected and no
// meaning is read out of a key's name.
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

// withheldProjection stands in for a WHOLE projection the display cannot show,
// rather than for one value inside it. Like placeholder it is declared exactly
// once in the module — two spellings would make "was this withheld, or simply
// absent?" unanswerable, which is the one question a reader of a shortened
// display has to be able to answer.
//
// It says what happened and how to see the data anyway. A projection that simply
// vanished would be a hole the reader cannot see, and a reader who cannot tell an
// omission from an empty result will read the display as complete.
// TestWithheldProjectionLiteralHasOneHome enforces the single spelling.
const withheldProjection = "<withheld: rendered from data an author declared sensitive, and could not be " +
	"re-rendered with that data substituted; re-run with `aggrega build --trace-unredacted` " +
	"to capture it in the clear>"

// WithheldProjection returns the fixed text that stands in for a whole
// projection a human-readable surface cannot show, naming the flag that shows it.
//
// It replaces a projection rather than a value because some displayed data is
// not addressable by key: a rendered document's fields carry no link back to the
// declarations the template read to produce them, so there is no key to
// substitute at. The first answer for such a projection is a re-render against a
// substituted context, which keeps the document's shape; this text is the fallback
// for the case where that re-render could not be produced at all.
func WithheldProjection() string { return withheldProjection }

// substitutedProjection announces a projection a display shows as a RE-RENDER
// rather than as a capture of what the build emitted. Like the two texts above it
// is declared exactly once — a second spelling would let one surface's
// announcement gain a qualifier the other's did not, and this text's whole job is
// to be precise about what the reader is looking at.
//
// The announcement is deliberately narrow about its own guarantee. It says which
// data was substituted (the data a declaration names) and which two ways the
// re-render can differ from the emitted document (a template that branches on a
// value's content, and data no declaration names) — never that no sensitive value
// remains, which is a promise the substitution cannot keep.
//
// The replacement text is composed from placeholder rather than spelled again, so
// the two cannot drift and the single-home census still finds exactly one
// spelling of each. TestSubstitutedProjectionLiteralHasOneHome enforces this one.
//
// It carries no apostrophe, and that is a constraint rather than a style choice.
// The text is emitted inside a YAML document, where a single-quoted scalar escapes
// an apostrophe by doubling it — so a reader searching a capture for this exact
// text would not find it, and neither would a test.
const substitutedProjection = "<re-rendered: the same template evaluated against a context whose " +
	"declared-sensitive data reads as `" + placeholder + "`, so this is not the document the build " +
	"emitted; a template that branches on the content of a value renders differently here, and data " +
	"no declaration names is carried through unchanged. Re-run with `aggrega build " +
	"--trace-unredacted` to capture the emitted document.>"

// SubstitutedProjection returns the fixed text a human-readable surface prints
// beside a projection it shows as a re-render with declared-sensitive data
// substituted, so a reader is never told that a re-render is the emitted document.
func SubstitutedProjection() string { return substitutedProjection }

// WorkloadDisplay records what a human-readable surface does with the rendered
// workload projection. It is the one display decision a Keys set carries, because
// a rendered workload is the one projection no key-addressed substitution can
// reach: its fields carry no link back to the declarations the template read to
// produce them.
//
// The zero value shows the document the build emitted, which is what a node
// declaring nothing gets — and what a capture with its withholding turned off
// gets, since that mode hands every surface an empty set.
type WorkloadDisplay uint8

const (
	// WorkloadVerbatim shows the document the build emitted.
	WorkloadVerbatim WorkloadDisplay = iota

	// WorkloadSubstituted shows a re-render of the same template against a
	// context whose declared-sensitive data reads as the placeholder, announced
	// by SubstitutedProjection so a reader cannot mistake it for the emitted
	// document.
	WorkloadSubstituted

	// WorkloadUnavailable shows WithheldProjection, for the case where the
	// re-render could not be produced.
	WorkloadUnavailable
)

// Keys names, for one node, which data a surface must replace. It is a
// description of declarations and of what was computed from them, assembled by
// NodeKeys from the upstream facts; it holds no values and answers no question
// about what a datum means.
//
// Values and Infra are keyed by the flat-dotted property path from the pillar
// root — the spelling types.FlattenLeaves produces and the authored leaf
// declarations use, so the two join by key. Inject is keyed by delivery
// mechanism and then by delivery key, which is the shape the attachment's
// marking and the delivery pipe both carry.
//
// Locals and Source name DERIVED withholding rather than declared: a template
// output computed from a declared-sensitive datum lands under a key nobody
// marked, so identifying it takes a reading of the template's references rather
// than a leaf declaration. Locals is keyed by the local's own name, which is how
// the resolved block is displayed. Source is keyed by the source field's name,
// with WholeSourceKey standing for a source declared as a single string.
//
// UsesProvides names ANOTHER entity's declarations. A uses edge carries its
// target's resolved provides into the consuming node's display, so what governs
// those values is the target Component's schema, not the consumer's. It is keyed
// by the edge's alias — the consumer's own local name for the connection, which
// is how the edge is displayed — and then by the target's provides key. Only the
// provides sub-map is covered; an edge's per-connection values and infra are a
// different pillar with a different owner.
//
// Workload is the one entry that names a whole projection rather than keys
// within it, and it records what the display DOES with that projection rather
// than which of its keys are withheld — a rendered workload is a parsed document
// whose fields carry no link back to the declarations the template read to
// produce them, so no key-addressed substitution can reach a datum interpolated
// into it.
type Keys struct {
	Values       map[string]bool
	Infra        map[string]bool
	Inject       map[string]map[string]bool
	Locals       map[string]bool
	Source       map[string]bool
	UsesProvides map[string]map[string]bool
	Workload     WorkloadDisplay
}

// AnyWithheld reports whether the set names any datum a template could READ. It
// is the input side of a whole-projection reading — a display re-renders or
// withholds a document because the document's template referenced something in
// here — and it is the guard that makes a substituted context worth building.
//
// Workload is deliberately excluded. It is the OUTPUT of such a reading, so a
// reading that consulted it would depend on its own result.
func (k Keys) AnyWithheld() bool {
	return len(k.Values) > 0 || len(k.Infra) > 0 || len(k.Inject) > 0 ||
		len(k.Locals) > 0 || len(k.Source) > 0 || len(k.UsesProvides) > 0
}

// Empty reports whether the set names nothing and no projection is displayed
// differently. A caller holding an empty set can skip every applier: each one
// returns its input unchanged, so a node with no declared sensitivity produces
// byte-identical output to a build predating the axis.
func (k Keys) Empty() bool {
	return !k.AnyWithheld() && k.Workload == WorkloadVerbatim
}

// WholeSourceKey is the Keys.Source entry that marks a source declared as a
// single string. Such a source is one unnamed field, so there is no field name to
// key it by.
//
// It cannot be confused with a structured source's field because the two shapes
// are mutually exclusive: a source is a string or a structured object, never
// both, so only one of the two lookups ever runs for a given source. The spelling
// is declared here rather than at the site that computes the set, so the producer
// and the applier cannot disagree about it.
const WholeSourceKey = ""

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
	out.Env = replaceFlat(in.Env, marked["env"])
	out.File = replaceFlat(in.File, marked["file"])
	out.Arg = replaceFlat(in.Arg, marked["arg"])
	// `all` is the host-built union of the typed mechanisms rather than an
	// authored pillar, so the same datum reappears here and the union must not
	// become the hole that re-prints what the typed pillar hid. The lookup is
	// the union of every mechanism's marking, deliberately wider than exact:
	// which mechanism won a same-named key in the merge is the merge's own
	// precedence rule, and reproducing it here would put that rule in a second
	// place. Withholding both is the safe side of that trade, and it costs only
	// the union view of one key authored under two mechanisms.
	out.All = replaceFlat(in.All, unionMarked(marked))
	if in.Additional.Len() > 0 {
		additional := types.OrderedMap[types.OrderedMap[string]]{}
		for mechanism, deliveries := range in.Additional.All() {
			additional.Set(mechanism, replaceFlat(deliveries, marked[mechanism]))
		}
		out.Additional = additional
	}
	return out
}

// Locals returns a copy of a node's resolved locals block with every withheld
// entry's value replaced by the placeholder, preserving author order and every
// entry that is not withheld.
//
// A local is withheld because its template READ a declared-sensitive datum, not
// because anything about the resolved string was examined: a local's value is
// arbitrary text that the compiler cannot and does not inspect, so the only way
// a computed secret is identifiable at all is through the reference the author
// wrote. The key survives for the same reason it survives in a pillar — a reader
// debugging a build must still see that the local exists.
//
// The input is never mutated. When keys is empty the input is returned as-is.
func Locals(in types.OrderedMap[string], keys map[string]bool) types.OrderedMap[string] {
	return replaceFlat(in, keys)
}

// Source returns a copy of a resolved Recipe source with every withheld field's
// value replaced by the placeholder, keeping every other field verbatim.
//
// Two shapes carry a template and so can carry a computed secret: a source
// declared as a single string, withheld under WholeSourceKey, and a structured
// source whose string-valued fields are each evaluated, withheld under their own
// field names. Any other shape reached this display having had no template
// evaluated against it, so nothing can have been computed from a declaration and
// it is returned untouched.
//
// The input is never mutated. When keys is empty the input is returned as-is.
func Source(in any, keys map[string]bool) any {
	if len(keys) == 0 || in == nil {
		return in
	}
	switch s := in.(type) {
	case string:
		if keys[WholeSourceKey] {
			return placeholder
		}
		return in
	case map[string]any:
		// The copy walks a Go map, whose iteration order is randomised; the result
		// is itself an unordered map, so that order cannot reach a displayed byte —
		// the display's serializer is what fixes key order.
		out := make(map[string]any, len(s))
		for k, v := range s {
			if keys[k] {
				out[k] = placeholder
				continue
			}
			out[k] = v
		}
		return out
	}
	return in
}

// Uses returns a copy of a node's enriched uses-edge map with every withheld
// provides value replaced by the placeholder, preserving alias order, each
// edge's own field order and every entry that is not withheld.
//
// It is the one applier whose input is governed by declarations an entity OTHER
// than the displayed node made. A uses edge publishes its target's resolved
// provides into the consumer's display, so a value the target declared sensitive
// would otherwise print in the clear on a surface belonging to a node that
// declared nothing. Withholding is keyed by alias, so two edges publishing the
// same provides key resolve independently — the edge whose target declared it is
// withheld and the other is not.
//
// Only the provides sub-map is touched. An edge's per-connection values and infra
// are authored on the edge rather than published by the target, so they are not
// what a target's provides declaration governs.
//
// The input is never mutated. When withheld is empty the input is returned as-is.
func Uses(in types.OrderedMap[types.EnrichedEdge], withheld map[string]map[string]bool) types.OrderedMap[types.EnrichedEdge] {
	if len(withheld) == 0 || in.Len() == 0 {
		return in
	}
	out := types.OrderedMap[types.EnrichedEdge]{}
	for alias, edge := range in.All() {
		keys := withheld[alias]
		if len(keys) == 0 || edge.Provides.Len() == 0 {
			out.Set(alias, edge)
			continue
		}
		edge.Provides = replaceFlat(edge.Provides, keys)
		out.Set(alias, edge)
	}
	return out
}

// TemplateContext returns a copy of a render context with every datum the set
// NAMES replaced by the placeholder, leaving the context's shape and every
// datum the set does not name exactly as the render sees them.
//
// It is the ONE substituted context in the module, and it is the answer for a
// projection that is not addressable by key. A rendered document's fields carry
// no link back to the declarations the template read to produce them, so nothing
// can be substituted inside the finished document — but the CONTEXT the template
// read is fully key-addressed, so evaluating the same template again against a
// substituted context yields a document of the same shape with the placeholder
// standing where the datum stood. Removing the keys instead would change which
// branches the template takes and the two documents would diverge structurally;
// substituting keeps a string where a string was.
//
// Two ways a document built this way still differs from the emitted one, both of
// which the display has to announce rather than paper over: a template that
// branches on a value's CONTENT — an equality test, a length, a hash — takes a
// different branch against the placeholder, and a datum reaching the template
// through a root this set cannot name is carried through unchanged.
//
// Every applier it composes returns a copy, so the input is never mutated. That
// is load-bearing rather than incidental: the context handed here is the live one
// the artifacts render from, and an in-place substitution would put the
// placeholder into the deployable.
//
// A set naming nothing readable returns the input pointer unchanged, so a node
// with no declared sensitivity pays nothing.
func TemplateContext(in *types.TemplateContext, keys Keys) *types.TemplateContext {
	if in == nil || !keys.AnyWithheld() {
		return in
	}
	out := *in
	out.Values = Pillar(in.Values, keys.Values)
	out.Infra = Pillar(in.Infra, keys.Infra)
	out.Locals = Locals(in.Locals, keys.Locals)
	out.Source = Source(in.Source, keys.Source)
	out.Context.Inject = InjectPipe(in.Context.Inject, keys.Inject)
	out.Context.Uses = Uses(in.Context.Uses, keys.UsesProvides)
	return &out
}

// replaceFlat is the ONE substitution over a flat authored-key map, shared by
// every surface displayed in that shape — a delivery pipe's per-mechanism
// entries and a resolved locals block. A second copy would let one surface gain
// a formatting nuance the other did not.
func replaceFlat(in types.OrderedMap[string], keys map[string]bool) types.OrderedMap[string] {
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

// Derived names the display data a surface must withhold because it was COMPUTED
// from a declared-sensitive datum rather than declared sensitive itself. A
// template that interpolates a withheld leaf produces a string under a key no
// author marked, so withholding it takes a reading of what the template
// references; the reading happens in one place upstream and arrives here as two
// key sets.
//
// Locals is keyed by the local's own name. Source is keyed by the source field's
// name, with WholeSourceKey standing for a source declared as a single string.
// Both are grouped in one argument rather than added as two more bare maps to
// NodeKeys, so a caller cannot pair the wrong set with the wrong pillar.
type Derived struct {
	Locals map[string]bool
	Source map[string]bool
}

// CrossNode names the display data governed by ANOTHER entity's declarations.
// A uses edge carries its target's resolved provides into the consuming node's
// display twice over — published under the edge, and delivered onto the
// consumer's own delivery keys where the edge's `inject` mapping routes it — and
// both are governed by the target Component's schema rather than the consumer's.
//
// Provides is keyed by the edge's alias, the consumer's own local name for the
// connection, and then by the target's provides key. Inject is keyed by delivery
// mechanism and then by delivery key, which is the shape the delivery pipe is
// displayed in and therefore not alias-keyed; a delivery key identifies exactly
// one entry within its mechanism on a node, because a key another source already
// set halts the build.
//
// The two are grouped rather than passed as two bare maps for the same reason
// Derived is: they are read from the same edges by the same declaration lookup,
// and a caller pairing one of them with the wrong pillar would withhold by alias
// what is displayed by mechanism.
type CrossNode struct {
	Provides map[string]map[string]bool
	Inject   map[string]map[string]bool
}

// NodeKeys assembles one node's redaction set from the upstream facts, each read
// from its own home and none of them recomputed here.
//
// declaredValues and declaredInfra are the leaf paths the host Component's
// schema declares sensitive, read through the single authored-declaration
// reader. attachments supplies the per-delivery-key marking the graph already
// carries on each attachment, folded across the node's attachments into the
// mechanism-then-key shape the pipe is displayed in. crossNode supplies what the
// TARGET of each uses edge declares — read through the same authored-declaration
// reader, against the target's type rather than the node's — both as published
// provides and as the consumer delivery keys the edge's `inject` mapping routes
// them onto. derived supplies the template outputs computed from a
// declared-sensitive datum, read from the authored template text by the one walk
// that performs that reading.
//
// A node's set therefore spans two owners: its own declarations, and the
// declarations of every node it consumes across an edge. That is not a widening
// of who may declare — it follows from the display carrying another node's
// published values, so the set that governs them has to be that node's.
//
// The two delivery-key sources fold into ONE marking, because the pipe is one
// flat merged map per mechanism and a reader of it cannot tell which source filled
// a key. An attachment's mapping and a uses edge's mapping are the same policy
// read from two wirings — a delivery key whose referenced provides key is declared
// sensitive — so a display that covered one and not the other would print in the
// clear exactly what it withheld one projection above.
//
// The per-attachment sensitivity roll-up is deliberately NOT consulted. It says
// an attachment handles secret material; it does not say which of its leaves
// do, and treating it as if it did would print the placeholder over a
// referenceable credential's name and key — public locators an author left
// unmarked on purpose.
func NodeKeys(
	declaredValues, declaredInfra map[string]bool,
	attachments types.OrderedMap[types.IRFeatureEntry],
	crossNode CrossNode,
	derived Derived,
) Keys {
	keys := Keys{}
	if len(declaredValues) > 0 {
		keys.Values = declaredValues
	}
	if len(declaredInfra) > 0 {
		keys.Infra = declaredInfra
	}
	if len(crossNode.Provides) > 0 {
		keys.UsesProvides = crossNode.Provides
	}
	if len(derived.Locals) > 0 {
		keys.Locals = derived.Locals
	}
	if len(derived.Source) > 0 {
		keys.Source = derived.Source
	}
	for _, entry := range attachments.All() {
		for mechanism, deliveries := range entry.SensitiveInject.All() {
			for deliveryKey := range deliveries.All() {
				keys.markDelivery(mechanism, deliveryKey)
			}
		}
	}
	for mechanism, deliveries := range crossNode.Inject {
		for deliveryKey := range deliveries {
			keys.markDelivery(mechanism, deliveryKey)
		}
	}
	return keys
}

// markDelivery records one delivery key of one mechanism, allocating as it goes.
// The set is built rather than aliased so a caller's own map is never captured,
// and both delivery-key sources reach it through this one method so neither can
// grow a nesting rule the other does not have.
//
// The receiver is a pointer because the maps it allocates have to survive the
// call; the walks that fill it iterate ordered inputs but the result is only ever
// read as a lookup, so no iteration order here can reach a displayed byte.
func (k *Keys) markDelivery(mechanism, deliveryKey string) {
	if k.Inject == nil {
		k.Inject = map[string]map[string]bool{}
	}
	if k.Inject[mechanism] == nil {
		k.Inject[mechanism] = map[string]bool{}
	}
	k.Inject[mechanism][deliveryKey] = true
}

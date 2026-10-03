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
// their own single homes upstream — the sensitive-leaf declarations on an
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
//
// The invocation it names is the WHOLE runnable one, not just the flag that
// selects the behaviour. This text is written into a file rather than printed
// beside a prompt, so its reader meets it long after the build and has nothing
// on screen to complete it from; an invocation short of a required flag halts,
// and a halt is what a reader of a shortened display reads as a broken tool.
const withheldProjection = "<withheld: rendered from data an author declared sensitive, and could not be " +
	"re-rendered with that data substituted; re-run with " +
	"`aggrega build --release <release> --trace --trace-unredacted` to capture it in the clear>"

// WithheldProjection returns the fixed text that stands in for a whole
// projection a human-readable surface cannot show, naming the invocation that
// shows it.
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
//
// Like the withholding statement above it names the whole runnable invocation
// rather than the flag alone, and for the same reason: it is read from a file,
// where there is nothing on screen to complete a partial command from.
const substitutedProjection = "<re-rendered: the same template evaluated against a context whose " +
	"declared-sensitive data reads as `" + placeholder + "`, so this is not the document the build " +
	"emitted; a template that branches on the content of a value renders differently here, and data " +
	"no declaration names is carried through unchanged. Re-run with `aggrega build --release <release> " +
	"--trace --trace-unredacted` to capture the emitted document.>"

// SubstitutedProjection returns the fixed text a human-readable surface prints
// beside a projection it shows as a re-render with declared-sensitive data
// substituted, so a reader is never told that a re-render is the emitted document.
func SubstitutedProjection() string { return substitutedProjection }

// withheldMarkerKey is the sole key of the object a STRUCTURED surface leaves
// where a value used to be. Like the three texts above it is declared exactly
// once in the module, and for the same reason: two spellings would make "was
// this withheld, or simply absent?" unanswerable, which is the one question a
// reader of a shortened display has to be able to answer.
// TestWithheldMarkerKeyHasOneHome enforces the single spelling.
//
// A structured surface cannot use the placeholder, and that is a property of
// the surface rather than a preference. The texts above replace a value a
// reader meets as text; a structured surface emits a document whose values
// carry types, so putting a string where an object or a number sat changes the
// shape of the document — and leaves a reader unable to tell the substitution
// from a value whose author happened to write that same text.
//
// A removed value needs SOME representation, because dropping the key would
// drop the field's path with it and the reader would no longer learn that the
// field exists at all. The representation may not be one an author could have
// written, which rules out every plain scalar. Null is the trap worth naming:
// a reader that renders it prints the readable word where the authored value
// used to be, which is a confident wrong answer in place of a correct refusal.
// A single-key object is a shape a reader recognises before it reads any value
// out of it.
//
// An author who writes this exact shape into their own configuration has that
// value withheld and is told it was withheld. That is over-withholding, which
// cannot leak.
const withheldMarkerKey = "aggrega_withheld"

// WithheldReason says WHY a structured surface withheld a value. It is a
// distinct type rather than a bare string so a surface reaches for one of the
// three below instead of inventing a fourth wording that a reader would not
// recognise and would have to fall back on.
//
// Each is a different statement about the graph, and a reader words them apart,
// because saying "this is a secret" about a leaf its author left readable would
// assert something the graph does not.
type WithheldReason string

const (
	// WithheldDeclared — the entity attached here declares this leaf, or one
	// above it, to carry sensitive material.
	WithheldDeclared WithheldReason = "declared"

	// WithheldAttachment — the sensitivity marking arrived in a shape this
	// surface cannot read, so no leaf under it can be judged and every one of
	// them is withheld.
	WithheldAttachment WithheldReason = "attachment"

	// WithheldUnreadable — the value is in no shape this surface can read.
	WithheldUnreadable WithheldReason = "unreadable"
)

// WithheldMarkerKey returns the sole key of the withheld marker, for a surface
// that has to recognise one rather than build one.
func WithheldMarkerKey() string { return withheldMarkerKey }

// WithheldMarker returns the object a structured surface leaves where a value
// was, carrying the reason so a reader can still say why a field is absent.
//
// A reason this package does not define falls back to WithheldAttachment,
// which is the reading that claims least: it says the marking could not be read
// rather than asserting anything about what the author declared at this path.
// The fallback is here, on the producing side, so a reader never has to decide
// what an unrecognised reason meant.
//
// A fresh map is built per call rather than a shared one being handed out.
// Two positions holding one map value would let a YAML encoder emit the second
// as an alias of the first, which is a shape a reader joining on the marker key
// would have to resolve before it could recognise the marker at all.
func WithheldMarker(reason WithheldReason) map[string]string {
	switch reason {
	case WithheldDeclared, WithheldUnreadable:
	default:
		reason = WithheldAttachment
	}
	return map[string]string{withheldMarkerKey: string(reason)}
}

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

// DeliveryKeys names delivery keys by mechanism and then by delivery key. It is
// the shape every delivery route reports in and the shape the delivery pipe is
// displayed in, so a route's marking and the pipe it covers join by key without
// either side reshaping the other.
type DeliveryKeys map[string]map[string]bool

// Keys names, for one node, which data a surface must replace. It is a
// description of declarations and of what was computed from them, assembled by
// NodeKeys from the upstream facts; it holds no values and answers no question
// about what a datum means.
//
// Values and Infra are keyed by the flat-dotted property path from the pillar
// root — the spelling types.FlattenLeaves produces and the authored leaf
// declarations use, so the two join by key. The delivery marking is keyed by
// mechanism and then by delivery key, which is the shape every delivery route
// reports in and the shape the delivery pipe is displayed in.
//
// The delivery marking is deliberately UNEXPORTED, and that is the boundary the
// whole package sits on rather than a style choice. Three withholding gaps of one
// family have been found by probing, each the same shape — a route that puts
// material on a node's delivery pipe and that the assembly did not read — so the
// marking has to be unreachable except through NodeKeys. Unexporting it makes the
// compiler, rather than a comment, the thing that stops a second assembler from
// existing: no package outside this one can mark a delivery key at all, so a
// route added later has to arrive as an argument to the one constructor. What that
// cannot force is the plumbing of a route into that constructor in the first
// place; the untraced argument is what covers a key no route accounted for.
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
// provides sub-map is covered by it; an edge's per-connection values and infra
// are a different pillar with a different owner.
//
// UsesAttachments names the declarations of the Features attached ON a
// connection, which the edge displays under its features entry. Each is governed
// by its own Feature's schema — the same declarations that govern an attachment
// the node holds itself — so it is keyed by alias, then by the slot the
// attachment fills on the connection's socket, which is how the edge displays it.
//
// Workload is the one entry that names a whole projection rather than keys
// within it, and it records what the display DOES with that projection rather
// than which of its keys are withheld — a rendered workload is a parsed document
// whose fields carry no link back to the declarations the template read to
// produce them, so no key-addressed substitution can reach a datum interpolated
// into it.
type Keys struct {
	Values          map[string]bool
	Infra           map[string]bool
	Locals          map[string]bool
	Source          map[string]bool
	UsesProvides    map[string]map[string]bool
	UsesAttachments map[string]map[string]AttachmentKeys
	Workload        WorkloadDisplay

	// inject is the delivery marking. See the type's own documentation for why
	// it is not exported.
	inject DeliveryKeys
}

// AttachmentKeys names, for one attachment, what its Feature's schema declares
// sensitive: the leaf paths of its values and infra pillars, spelled as Values and
// Infra are on Keys, and the keys of the provides it publishes. Each set is the
// declaration as read, with no intersection against what the attachment carries,
// so the same attachment is withheld the same way whichever build displays it.
type AttachmentKeys struct {
	Values   map[string]bool
	Infra    map[string]bool
	Provides map[string]bool
}

// Any reports whether the attachment's Feature declares anything sensitive in
// any of the three pillars an attachment is displayed with.
func (a AttachmentKeys) Any() bool {
	return len(a.Values) > 0 || len(a.Infra) > 0 || len(a.Provides) > 0
}

// AnyWithheld reports whether the set names any datum a template could READ. It
// is the input side of a whole-projection reading — a display re-renders or
// withholds a document because the document's template referenced something in
// here — and it is the guard that makes a substituted context worth building.
//
// Workload is deliberately excluded. It is the OUTPUT of such a reading, so a
// reading that consulted it would depend on its own result.
func (k Keys) AnyWithheld() bool {
	return len(k.Values) > 0 || len(k.Infra) > 0 || len(k.inject) > 0 ||
		len(k.Locals) > 0 || len(k.Source) > 0 || len(k.UsesProvides) > 0 ||
		len(k.UsesAttachments) > 0
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

// Leaf reports whether the named pillar leaf is withheld. The path is the
// flat-dotted chain from the pillar root, without the pillar prefix. An
// unrecognised pillar name reports false rather than guessing.
//
// A leaf is withheld when a declaration names it OR names an object containing
// it, which is asked through the one home for that rule rather than restated
// here. Only the pillar SELECTION is local, and it has to be: this package sits
// below the reader of authored declarations rather than beside it, so the
// selection cannot be shared without inverting that dependency. The rule
// itself — the part that could drift into a different answer — is shared.
func (k Keys) Leaf(pillar, path string) bool {
	switch pillar {
	case "values":
		return types.PathCoveredBy(path, k.Values)
	case "infra":
		return types.PathCoveredBy(path, k.Infra)
	}
	return false
}

// Delivery reports whether the named delivery key of the named mechanism
// carries material inherited from a sensitive declaration.
func (k Keys) Delivery(mechanism, key string) bool {
	return k.inject[mechanism][key]
}

// AnyDelivery reports whether any delivery key of any mechanism is withheld. It
// is what a read of the whole delivery pipe reaches, so a template referencing
// the pipe without naming a mechanism is answered by this.
func (k Keys) AnyDelivery() bool {
	for _, deliveries := range k.inject {
		if len(deliveries) > 0 {
			return true
		}
	}
	return false
}

// AnyDeliveryIn reports whether any delivery key of the named mechanism is
// withheld — what a read of one mechanism without naming a key reaches.
func (k Keys) AnyDeliveryIn(mechanism string) bool {
	return len(k.inject[mechanism]) > 0
}

// InjectPipe returns a copy of a delivery pipe with every delivery key this set
// marks replaced by the placeholder. It is a method rather than a free function
// because the marking it reads is not reachable from outside this package: the
// applier and the set that governs it travel together, so no surface can display
// the pipe against a marking it assembled itself.
func (k Keys) InjectPipe(in types.InjectPipe) types.InjectPipe {
	return injectPipe(in, k.inject)
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
	return replacePillar("", in, paths, func() any { return placeholder })
}

// PillarStructured is Pillar for a surface that emits a document rather than
// text: every declared sensitive leaf's value is replaced by the withheld
// marker instead of by the placeholder, so the substitution stays
// distinguishable from an authored string and the document keeps a shape a
// reader can walk.
//
// It shares the descent with Pillar rather than restating it. The two differ
// only in what they leave behind, and a second walk would be free to drift into
// reaching a different set of leaves than the text surface reaches — which is
// the one difference between the two surfaces that must never exist.
func PillarStructured(in types.OrderedMap[any], paths map[string]bool, reason WithheldReason) types.OrderedMap[any] {
	if len(paths) == 0 || in.Len() == 0 {
		return in
	}
	return replacePillar("", in, paths, func() any { return WithheldMarker(reason) })
}

// replacePillar walks a pillar and replaces every marked leaf with whatever
// replacement builds. The replacement is a constructor rather than a value so
// each marked leaf gets its own, which keeps a document encoder from emitting
// the second and later ones as aliases of the first.
func replacePillar(prefix string, in types.OrderedMap[any], paths map[string]bool, replacement func() any) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	for k, v := range in.All() {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if paths[path] {
			out.Set(k, replacement())
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
			nested := replacePillar(path, *n, paths, replacement)
			out.Set(k, &nested)
		case types.OrderedMap[any]:
			if n.Len() == 0 {
				out.Set(k, v)
				continue
			}
			out.Set(k, replacePillar(path, n, paths, replacement))
		default:
			out.Set(k, v)
		}
	}
	return out
}

// injectPipe returns a copy of a delivery pipe with every marked delivery key's
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
func injectPipe(in types.InjectPipe, marked DeliveryKeys) types.InjectPipe {
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
// datum the edges carry replaced by the placeholder, preserving alias order, each
// edge's own field order and every entry that is not withheld.
//
// It is the one applier whose input is governed by declarations entities OTHER
// than the displayed node made, and an edge carries two such kinds of data.
//
// Its target's resolved provides, governed by the target Component's schema. A
// value the target declared sensitive would otherwise print in the clear on a
// surface belonging to a node that declared nothing. Withholding is keyed by
// alias, so two edges publishing the same provides key resolve independently —
// the edge whose target declared it is withheld and the other is not.
//
// The attachments written on the connection, each governed by its own Feature's
// schema — the declarations that govern the same Feature attached to a node
// directly. A declared values or infra leaf is withheld the way a node's own
// pillar is, keeping its key, and so is a declared key of the provides the
// attachment publishes. The rest of an attachment — its type, version and labels,
// and every leaf and provides key its Feature left undeclared — is shown as it
// is. Withholding is keyed by alias and slot, so the same Feature written on two
// connections resolves per connection.
//
// It is a method rather than a free function so the two halves cannot be applied
// apart: every surface displaying the uses map reaches both through this one
// call, and none can withhold an edge's provides while showing its attachments.
//
// An edge's per-connection values and infra are authored on the edge rather than
// published by the target or declared by a Feature, so neither half governs them.
//
// The input is never mutated. When neither half names anything the input is
// returned as-is.
func (k Keys) Uses(in types.OrderedMap[types.EnrichedEdge]) types.OrderedMap[types.EnrichedEdge] {
	if (len(k.UsesProvides) == 0 && len(k.UsesAttachments) == 0) || in.Len() == 0 {
		return in
	}
	out := types.OrderedMap[types.EnrichedEdge]{}
	for alias, edge := range in.All() {
		if keys := k.UsesProvides[alias]; len(keys) > 0 && edge.Provides.Len() > 0 {
			edge.Provides = replaceFlat(edge.Provides, keys)
		}
		if slots := k.UsesAttachments[alias]; len(slots) > 0 && edge.Features.Len() > 0 {
			edge.Features = connectionAttachments(edge.Features, slots)
		}
		out.Set(alias, edge)
	}
	return out
}

// connectionAttachments returns a copy of one connection's attachments with every
// declared datum replaced, reaching each pillar through the applier that pillar
// already has: a values or infra pillar through Pillar, the provides bag through
// the flat substitution an edge's provides takes. An attachment its slot names
// nothing for is kept as it is.
func connectionAttachments(in types.OrderedMap[types.TemplateAttachment], slots map[string]AttachmentKeys) types.OrderedMap[types.TemplateAttachment] {
	out := types.OrderedMap[types.TemplateAttachment]{}
	for slot, attachment := range in.All() {
		if keys, ok := slots[slot]; ok {
			attachment.Values = Pillar(attachment.Values, keys.Values)
			attachment.Infra = Pillar(attachment.Infra, keys.Infra)
			attachment.Provides = replaceFlat(attachment.Provides, keys.Provides)
		}
		out.Set(slot, attachment)
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
	out.Context.Inject = keys.InjectPipe(in.Context.Inject)
	out.Context.Uses = keys.Uses(in.Context.Uses)
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

func unionMarked(marked DeliveryKeys) map[string]bool {
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
//
// Attachments is the third thing an edge carries into the display: the
// attachments written on the connection, each governed by its own Feature's
// declarations rather than by the target's. It is keyed by alias and then by the
// slot the attachment fills on the connection's socket, which is how the edge
// displays it. It travels here because it is read off the same edges, and a
// delivery key one of those attachments marks needs nothing from it: that
// marking is already on the attachment's graph entry, which the attachments
// argument of NodeKeys folds in with every other attachment the node holds.
type CrossNode struct {
	Provides    map[string]map[string]bool
	Inject      DeliveryKeys
	Attachments map[string]map[string]AttachmentKeys
}

// NodeKeys assembles one node's redaction set from the upstream facts, each read
// from its own home and none of them recomputed here. It is the ONE assembler:
// the set's delivery marking is unreachable from any other package, so a surface
// cannot apply a marking it assembled itself, and every route this assembly knows
// about arrives as an argument of its own.
//
// What the compiler enforces is that no argument may be silently dropped, not that
// every route in the pipeline is wired to one: a route added upstream and never
// plumbed here compiles cleanly, which is how the routes now named below were each
// found by probing rather than by a gate. The untraced argument is the standing
// answer to that residual — see its bullet.
//
// declaredValues and declaredInfra are the leaf paths the host Component's
// schema declares sensitive, read through the single authored-declaration
// reader. derived supplies the template outputs computed from a
// declared-sensitive datum, read from the authored template text by the one walk
// that performs that reading.
//
// FIVE arguments carry delivery routes, one per route, and that is why they are
// separate parameters rather than fields of one struct: a route left out of this
// signature is a compile error at the call site, where a field left unset on a
// struct would be an empty map nobody notices — which is the exact shape of every
// withholding gap this assembly has had.
//
//   - attachments supplies the per-delivery-key marking the graph already carries
//     on each attachment, from the attachment's own `inject` mapping, folded
//     across the node's attachments.
//   - crossNode supplies what the TARGET of each uses edge declares — read
//     through the same authored-declaration reader, against the target's type
//     rather than the node's — both as published provides and as the consumer
//     delivery keys the edge's `inject` mapping routes them onto; and what the
//     Feature of each attachment written on a connection declares, read through
//     the reader every attachment's declarations come from.
//   - injectMapping supplies the delivery keys a FeatureRecipe's own
//     `injectMapping` put on the pipe on behalf of a slot that declares no
//     delivery of its own. Such a slot has no `inject` mapping to read, so the
//     attachment route is structurally blind to it and this one is what covers it.
//   - selfInject supplies the delivery keys a node's own component-level
//     self-inject put on its own pipe. It is the one route where the material and
//     the declaration that governs it belong to the SAME node, which is why a
//     reading that only looked outward across edges and attachments left it in the
//     clear.
//   - untraced supplies delivery keys no route could account for. They are
//     withheld, which is the same direction the reading of a template takes when a
//     reference cannot be resolved: over-withholding costs a reader one value they
//     must find another way, while under-withholding prints a secret. It is the
//     assembly's fail-safe for a route whose provenance the host cannot see, since
//     no compiler mechanism can force a route that exists in the pipeline to be
//     plumbed to this call.
//
// A node's set therefore spans several owners: its own declarations, the
// declarations of every node it consumes across an edge, and those of every
// Feature attached on one of its connections. That is not a widening of who may
// declare — it follows from the display carrying data those entities govern, so
// the set that governs them has to be theirs.
//
// Every delivery-key source folds into ONE marking, because the pipe is one flat
// merged map per mechanism and a reader of it cannot tell which source filled a
// key. An attachment's mapping, a uses edge's mapping and a FeatureRecipe's
// injectMapping are the same policy read from three wirings — a delivery key
// carrying material a declaration governs — so a display that covered one and not
// the others would print in the clear exactly what it withheld one projection
// above.
//
// The per-attachment sensitivity roll-up is deliberately NOT consulted. It says
// an attachment handles secret material; it does not say which of its leaves
// do, and treating it as if it did would print the placeholder over a
// referenceable credential's name and key — public locators an author left
// unmarked on purpose. Where a route is hard to trace the untraced argument is the
// sanctioned conservatism, and it is per-key.
func NodeKeys(
	declaredValues, declaredInfra map[string]bool,
	attachments types.OrderedMap[types.IRFeatureEntry],
	crossNode CrossNode,
	injectMapping DeliveryKeys,
	selfInject DeliveryKeys,
	untraced DeliveryKeys,
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
	// Only a slot whose Feature declares something is kept, so a connection
	// carrying nothing declared leaves the set exactly as empty as a build without
	// any connection attachment — the guard a substituted context and a re-rendered
	// workload are built behind reads its emptiness.
	for alias, slots := range crossNode.Attachments {
		for slot, declared := range slots {
			if !declared.Any() {
				continue
			}
			if keys.UsesAttachments == nil {
				keys.UsesAttachments = map[string]map[string]AttachmentKeys{}
			}
			if keys.UsesAttachments[alias] == nil {
				keys.UsesAttachments[alias] = map[string]AttachmentKeys{}
			}
			keys.UsesAttachments[alias][slot] = declared
		}
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
	for _, route := range []DeliveryKeys{crossNode.Inject, injectMapping, selfInject, untraced} {
		for mechanism, deliveries := range route {
			for deliveryKey := range deliveries {
				keys.markDelivery(mechanism, deliveryKey)
			}
		}
	}
	return keys
}

// markDelivery records one delivery key of one mechanism, allocating as it goes.
// The set is built rather than aliased so a caller's own map is never captured,
// and every delivery-key source reaches it through this one method so none can
// grow a nesting rule the others do not have.
//
// The receiver is a pointer because the maps it allocates have to survive the
// call; the walks that fill it iterate ordered inputs but the result is only ever
// read as a lookup, so no iteration order here can reach a displayed byte.
func (k *Keys) markDelivery(mechanism, deliveryKey string) {
	if k.inject == nil {
		k.inject = DeliveryKeys{}
	}
	if k.inject[mechanism] == nil {
		k.inject[mechanism] = map[string]bool{}
	}
	k.inject[mechanism][deliveryKey] = true
}

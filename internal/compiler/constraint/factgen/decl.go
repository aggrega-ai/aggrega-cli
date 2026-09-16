// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command factgen generates the constraint framework's view stages, its derived
// subject-kind set, and the reviewable placement table, from the fact registry
// declared in registry.go.
//
// Nothing here reads a hand-maintained document. The registry is Go source, the
// derivations below are computed from it, and every generated artifact is a
// projection of it — so a placement change is a one-line registry edit whose
// consequences appear as a diff in committed output. One derivation reads the
// module's own source instead of the registry, and says so where it lives: which
// declared checks anything actually invokes is not a question the registry can
// answer.
//
// Usage, from this directory:
//
//	go run . <output-file>
//
// The output file's base name selects the target. Only the targets committed
// inside this module are named by a generate directive in generate.go; the ones
// committed above it are produced by passing their path, because a directive
// naming a path outside the module would fail outright in a standalone checkout
// of this repository. So `go generate` over this package regenerates SOME of the
// targets, and a clean working tree afterwards says nothing about the rest —
// their drift is gated by the workspace build, which has every tree present.
package main

// hostPower names one power the host offers, which a stage's fact production
// may exercise in order to make that stage's facts true.
//
// The set is closed, and closed rather than left as free text on purpose. A
// free-text list of power names is checkable only against a spelling: two stages
// would eventually spell one power differently, nothing could assert that a
// declared power is one the host actually offers, and no consumer could ever
// branch on one. Named values make a declaration reviewable against a known
// vocabulary and make adding a power a deliberate act in one place.
//
// A power is a reach outside the compiler's own computation, which is what
// admits the members below and refuses two things a reader reasonably expects to
// find among them. A catalog is a data source rather than a power: reaching one
// is a workspace read or an artifact fetch, both of which are here already. And
// rendering is an outcome rather than a power: producing artifact bytes is a
// call across the plugin boundary, which is here already too.
type hostPower string

const (
	// powerFilesystem is reading the workspace's own files.
	powerFilesystem hostPower = "filesystem"

	// powerPluginResolution is resolving a plugin reference to an entry in the
	// plugin cache.
	powerPluginResolution hostPower = "plugin-resolution"

	// powerArtifactFetch is fetching a published artifact.
	powerArtifactFetch hostPower = "artifact-fetch"

	// powerWorkspaceConfiguration is reading the workspace's configuration —
	// the sources a reference is resolved against, and the versions it is
	// pinned to.
	powerWorkspaceConfiguration hostPower = "workspace-configuration"

	// powerGuestCrossing is calling a guest across the plugin boundary.
	//
	// It is a power in its own right and not part of resolving the plugin that
	// precedes it: a reference can resolve and its guest still fail to load or
	// to answer, and the two are refused at different points for different
	// reasons.
	powerGuestCrossing hostPower = "guest-crossing"
)

// hostPowers is the closed set: a stage may declare a member of it and nothing
// else, which deriveChain enforces. Adding a power is a constant above plus an
// entry here, and those two edits are the whole of it — a constant absent from
// this slice is refused the first time a stage names it.
var hostPowers = []hostPower{
	powerFilesystem,
	powerPluginResolution,
	powerArtifactFetch,
	powerWorkspaceConfiguration,
	powerGuestCrossing,
}

// noHostPowers is what a stage declares when its own fact production exercises
// none.
//
// It has a name so that the empty case is stated rather than left blank. An
// omitted declaration and a deliberately empty one must not read the same,
// because a reviewer cannot tell an assertion from an oversight; deriveChain
// refuses a stage that omits the slot, so naming the empty case is the only way
// to say it.
var noHostPowers = []hostPower{}

// stageDecl declares one stage of the view chain: a point in the compilation at
// which some set of facts is complete.
//
// A stage exists if and only if some fact lands there. A stage declared here
// with no fact landing on it is refused rather than silently dropped, because a
// stage nothing occupies is vocabulary every future author must learn and no
// check uses.
type stageDecl struct {
	// Name is the stage's name. It names the state the stage holds, never a
	// position in a sequence, so the name survives a change to the order
	// things happen in.
	Name string

	// Extends names the stage this one contains: every fact complete at
	// Extends is still complete here. It is empty for the chain's root.
	//
	// The relation is declared rather than derived, and that is deliberate.
	// Facts accumulate, so deriving the order from the fact sets alone would
	// make "the chain is linear" true by construction and the assertion
	// worthless. Declared, the relation can be wrong — a second stage
	// extending an already-extended stage is a branch, which means two stages
	// no order relates, and the generator refuses it.
	Extends string

	// Holds describes what is complete at this stage, for the placement table.
	Holds string

	// HostPowers are the powers this stage's fact production exercises: what
	// the compiler reaches for, outside its own computation, in order to make
	// this stage's facts true.
	//
	// It describes the production and never a check. A check receives its view
	// and its subject and nothing else, so every value it reads was produced
	// for it, and the power belongs to whatever produced that value. A sentence
	// here about what a check at this stage may do has drifted off the slot's
	// subject.
	//
	// The declaration is per stage rather than per fact because the production
	// is per stage. Two facts complete at one point share one production, so a
	// slot on the fact would let them disagree about what that shared
	// production is allowed to do, and the disagreement would be invisible.
	//
	// A fact has no slot for one in any case: factDecl declares a stage, a
	// carrier, a subject key and a result, and there is nowhere in that to
	// record a filesystem read. So a power cannot arrive through a fact even by
	// accident. Declaring it here is not the preferred route to the axis; it is
	// the only one.
	//
	// Each stage states what its OWN production exercises, not what it adds to
	// the stage it extends. The two readings differ, and the accumulating one
	// is the weaker: under it the last stage reports needing nothing new, which
	// is a property of the chain rather than an answer about the stage. So a
	// power an earlier stage also needed is stated again here, where it is
	// needed again.
	//
	// A stage exercising none declares noHostPowers rather than omitting the
	// slot, and an omission halts generation.
	HostPowers []hostPower
}

// factDecl declares one fact: one distinct question about the pipeline product
// that a check reads to decide its predicate.
//
// One fact, one accessor, strictly one to one. There are no convenience
// variants: a fact exposed both as a predicate and as a set-returning form is
// two code paths for one question. An accessor may return a composite record
// where the fact itself is composite; that is still one fact.
type factDecl struct {
	// Name is the fact's name in kebab-case, and it is the single name for
	// the fact. The accessor's Go method name is derived from it rather than
	// declared beside it, which is what makes duplicate declaration a compile
	// error: two declarations of one name emit two same-named methods on one
	// interface, and that does not compile.
	Name string

	// Stage names the stage at which this fact is complete.
	Stage string

	// SourceField names the pipeline product field the fact reads, as a Go
	// structure and field. Where the product is carried as a function
	// parameter rather than as a field, it names that single carrier instead;
	// what matters is that exactly one carrier is named, because two facts
	// sharing a stage, a carrier and a subject are the mechanically flaggable
	// shape of same-semantics-under-a-different-name duplication.
	SourceField string

	// Carrier names the authored document kind that carries this fact — the
	// kind a user writes as the document's own kind, and never the Go
	// structure the value is read out of, which SourceField names. The two
	// slots both say "carrier" and mean different things: one is where the
	// value sits in the compiler, the other is what a user would have to open
	// in order to change it.
	//
	// It applies at the document stage alone. That stage presents one authored
	// document, so it has two axes — when the fact is complete, and which kind
	// of document carries it. Every later stage presents one compilation, which
	// has one carrier and so nothing for a second axis to distinguish; a
	// declaration at such a stage is refused rather than ignored, because a
	// value nothing reads is a value a reviewer trusts.
	//
	// It is declared only where the kind does not derive from SourceField.
	// Where the source carrier is one authored entity's spec type, the kind is
	// read from that type's own name, and declaring it again would give one
	// kind two homes — so a declaration repeating a successful derivation is
	// refused alongside one that contradicts it.
	Carrier string

	// Subject is the coordinate list the fact is keyed by, outermost first —
	// so {"node", "socket"} means the fact is asked once per socket per node.
	// The subject-kind set is derived from these lists and from nothing else.
	Subject []string

	// Params are the accessor's arguments. They are declared separately from
	// Subject and are usually related to it without being identical: a fact
	// keyed per node and socket may legitimately be read through the node's
	// component type, because the type is a projection of the node.
	Params []paramDecl

	// Returns is the accessor's result, emitted verbatim after the parameter
	// list — so a single type or a parenthesized list are both admissible.
	//
	// Four bounds hold over it, on two separate axes: two over what a check
	// could re-derive from the result, and two over what a check could DO with
	// it.
	//
	// A return type may not mention an authored-entity type, by pointer or by
	// value, inside a container or as a type argument; nor any diagnostic
	// shape. A view exposes named facts and never a raw document, and one
	// accessor handing back a document reopens re-derivation for every check.
	//
	// Nor may it mention a type whose field some fact names in its SourceField.
	// Reading a value out of a carrier is what every fact does; handing the
	// carrier itself back is what this refuses, because a check holding that
	// structure can read the very field another fact declares separately, at its
	// own stage and with its own placement, and so re-derive it. A compilation
	// product sits outside the authored-entity closure by design — facts over
	// products must remain declarable, and most of the later stages read one —
	// so this is the rule that closes the same hole for a product carrier.
	//
	// On the other axis, a result may not mention a function type, at any
	// nesting depth — bare, inside a container, or as a type argument. A fact is
	// a value already read out of the product; a callable is a power the caller
	// invokes, and the powers a check holds are declared on their own axis so
	// that they stay visible to the placement the declaration is computed from.
	//
	// Nor may a result mention a package-qualified type unless the admitted set
	// the guard over the generated views declares names that type. The set
	// admits rather than forbids: everything dangerous cannot be enumerated and
	// everything safe can, so a type nobody has classified is refused rather
	// than passed, and admitting one is an edit to that set with the reason
	// recorded beside the entry.
	//
	// Where a rule genuinely cannot be expressed within these bounds, declare
	// ReportedException rather than reshaping the rule around the guard: the
	// framework then reports the shape as open rather than admitting it
	// silently. The function-type bound is the one with no such escape, and
	// deliberately so — an exception records a shape as open, and a power is not
	// an open shape but a different kind of thing.
	Returns string

	// Doc is the accessor's godoc body, in declarative voice.
	Doc string

	// ReportedException, when non-empty, records that this fact's return shape
	// does not satisfy the no-raw-document rule, in the terms of what it
	// exposes. The framework reports such a fact rather than admitting it
	// silently: the guard names it, the placement table prints it, and the
	// exception is a standing question rather than a settled shape.
	ReportedException string
}

// paramDecl declares one accessor argument.
type paramDecl struct {
	Name string
	Type string
}

// recordDecl declares a composite record a composite fact returns. Its fields
// are scalars: a record exists to carry one fact's several coordinates, not to
// hand a check a structure it can walk into.
type recordDecl struct {
	Name   string
	Doc    string
	Fields []recordField
}

// recordField declares one field of a composite record.
type recordField struct {
	Name string
	Type string
	Doc  string
}

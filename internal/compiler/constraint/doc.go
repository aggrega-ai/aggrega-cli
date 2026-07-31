// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package constraint carries the mechanism a constraint check is written
// against: what a check hands back when an input violates a rule, and what
// that objection is attributed to.
//
// # A check raises an objection, never a diagnostic
//
// A check returns [Objection] values — a sentinel, the provenance of the
// subject the objection is about, and the check's own detail. It never
// constructs a diagnostic of any shape. Diagnostics differ by the boundary
// they cross: the plugin ABI carries a generated wire shape shared with
// non-Go guests, the editor surface carries a protocol shape with positions,
// the tolerant lockfile load carries an observation shape, and CLI output
// carries a categorised compiler error. A check cannot know which of those
// will read it, and none of them should be its concern; the framework builds
// whichever shape a caller needs from the objection.
//
// # A subject comes only from here
//
// [Subject], and every subject kind derived from it, carries an unexported
// method, so no type declared outside this package satisfies one by declaring
// its own methods: an unexported interface method is qualified by the package
// that declared it, so a same-named method written elsewhere is a different
// method. [Objection] carries only unexported fields and has no exported
// constructor, so no code outside this package can populate one.
//
// Both locks are needed, and the reason is a route the first one alone leaves
// open. A type declared elsewhere may embed Subject, which promotes the
// unexported method and does satisfy the interface — so the seal bounds who
// can declare a subject, not who can hold something the compiler accepts as
// one. What such a type still cannot do is populate an objection, because the
// fields are unexported: its Raise can only hand back the zero value. That is
// why Objection is a struct rather than an interface. As an interface the
// embedding route would compose into a complete forgery, with a fabricated
// sentinel and fabricated provenance; as a struct it composes into nothing
// that reads as raised.
//
// So the property the framework rests on is precisely this: an objection that
// carries a sentinel can only have come from a subject this package supplied,
// and this package supplies a subject only while it is invoking a check.
//
// The bound on that property is worth stating plainly rather than leaving a
// reader to assume more. It closes the forged-objection route and nothing
// wider. Go's error type is universal, so a function anywhere in the compiler
// can still return a plain error and halt a build; such a halt carries no
// provenance, appears in no coverage computation, and is reachable from no
// declaration — which makes checking outside this framework conspicuously
// second-class, not impossible. The zero [Objection] also exists, as the zero
// value of any Go struct does, and carries neither sentinel nor provenance;
// it is not a raised objection and reads as one nowhere.
//
// # Provenance is a file and a dotted authored locator
//
// A subject knows a source file and a dotted locator naming the authored
// element inside it — and no line and no column. Positions are not compile
// -time facts: the surface that needs them re-reads the authored document and
// walks the locator to recover a position, so recovering one here as well
// would be a second implementation of the same job.
//
// The framework does not invent a locator it cannot derive. A container the
// compiler synthesized rather than the author typing it contributes no
// segment, because the author typed none; a subject with no authored segment
// at all carries the empty locator, which routes a consumer to file-level
// positioning rather than to a plausible-looking path the source file does
// not contain.
//
// # Traversal is push as well as pull
//
// Most subject kinds are reached by the framework walking a collection. A
// document is not: documents are validated one at a time as they are read,
// and the catalog reads them lazily, so no point in the compilation holds a
// materialised document set to walk. [RunOnDocument] is therefore the
// push-model entry — the caller holding a document presents it, and the check
// runs once, against that one document.
package constraint

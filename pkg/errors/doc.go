// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package errors provides the compiler's categorised-error surface for
// CLI consumption.
//
// CompilerError carries a Category, a SourceLocation pointing at the
// authoring artifact at fault, a human-facing Message, an optional
// Expression (the failing template expression where applicable), and a
// Context map (the per-pass pillar snapshot at the failure point).
//
// Categories partition the error surface by origin. Some name the authoring
// artifact the operator must edit to fix the error; the rest name non-authoring
// faults. The list below is the statement of how many there are of each, and a
// number written beside it would read as current while lagging what it counts:
//   - DeveloperError: the Aggregate is at fault.
//   - OpsError: the Release or the Manifest is at fault.
//   - CatalogError: a Recipe, Component, Feature, FeatureRecipe, or
//     Plugin is at fault.
//   - TranslationError: the provider engine cannot express the requested
//     synthesized output (no authoring fix; the developer or operator
//     must change input shape).
//   - FetchError: a remote-acquisition fault (registry or source-artifact
//     fetch); the remediation is operational, not authoring.
//   - PolicyError: an operator-attached policy plugin rejected the
//     build; the fix is to satisfy the enforced policy or reconsider its
//     attachment, not to edit an authoring artifact.
//   - TrustError: the trust and signing layer refused the operation — a
//     signature that does not verify, an absent signature where one is
//     required, an untrusted signer, an unreadable trust policy or trust
//     root, or a plugin whose recorded trusted identity no longer matches.
//     It names a concern rather than an authoring surface, so one value
//     covers both "the trust configuration is wrong" and "this artifact
//     failed verification"; the condition's short code distinguishes them.
//
// A CompilerError may also carry NO category, and that is a state rather than
// an unset field: a failure whose cause lies in the compiler itself has no
// concern to route the reader to, since there is nothing they can go and look
// at. Being no fault of an authored artifact is not the test — the last three
// values above name concerns rather than authoring surfaces and are printed
// for failures that are nobody's authoring mistake. The rendered head omits an
// absent category rather than standing anything in for it, and the
// kind-to-Category mapping reports an uncovered reference rather than
// defaulting one — see ClassifyEntityKind and ClassifyEntityCategory.
//
// CompilerError implements the standard error interface plus Unwrap so
// errors.Is and errors.As work against the carried inner error chain.
//
// # Converting a domain error into the envelope
//
// A package that reports failures through its own structured error type
// carries a ToCompilerError method on that type, which builds the envelope
// with the Category the failure's authoring surface implies.
//
// The method is additive. The type keeps its own Error method, so callers
// inside the package still format and assert against the type's own shape,
// and the envelope is built only where the error is about to reach the CLI.
//
// Conversion preserves matching. The envelope holds the original
// domain-typed error in its wrap chain, so errors.Is against a sentinel
// declared inside that domain type resolves identically before and after
// conversion, and a test written against the sentinel needs no knowledge of
// which side of the conversion it is looking at.
//
// Each converting type documents its own Category routing beside its
// ToCompilerError method, because that routing is a property of the type's
// authoring surface rather than of this package. A type whose every instance
// belongs to one domain by construction names its Category directly; a type
// that can report against several kinds of entity reads it from the kind the
// failure carries, through ClassifyEntityKind.
//
// # Two families are matched rather than converted
//
// FetchError and TrustError have no converting type, and the asymmetry is
// structural rather than an omission. A remote-acquisition failure originates
// as a shared sentinel wrapped with transport context — a URL, a version, a
// remediation pointer — rather than as a structured value with fields to read,
// so there is no domain type on which a conversion method could sit. A trust
// or signing failure is the same shape for the same reason: it originates as a
// registered identity wrapped with the key, the reference or the recorded
// digest that the occurrence concerns. Both reach their Category by having the
// chain matched against those identities at a diagnostic surface, which then
// builds the envelope with the fixed Category. That match is the counterpart of
// a ToCompilerError: an identity test rather than a type assertion. A refusal
// to egress at all, raised when the workspace is offline, is matched by the
// fetch set, because from the user's position it is likewise an inability to
// fetch.
//
// The two differ in WHERE the match sits, and the difference follows from where
// each family's failures converge. Source-resolution fetch faults all cross one
// boundary inside the renderer, so the match sits there and the envelope
// reaches every surface, the one-line form included. Trust and signing faults
// are raised across the publish, pull, resolution and validation paths and
// converge only at the command line's own diagnostic formatter, so the match
// sits at that formatter. The consequence is worth stating plainly: a trust
// failure carries its Category on the printed terminal head and not on the
// one-line form this package's Error method produces, because the occurrence
// never crossed an envelope construction before reaching the terminal.
//
// # Identities and occurrences
//
// A halting condition has an identity and an occurrence, and this package
// carries both.
//
// The identity is a value declared by a Register call and published by an
// exported accessor standing beside it. It satisfies the error interface, it
// stays matchable under errors.Is through arbitrary wrapping, and one such
// value is shared by every occurrence of the condition it names. Its concrete
// type and that type's fields are unexported, and the interface it is handed
// back as — Identity — carries a method no importing package can declare, so an
// error written out by hand does not satisfy it. Embedding that interface
// promotes the method rather than declaring it, which no interface can prevent,
// so the constructors check that the value came from a Register call rather
// than resting on the type alone.
//
// The code the identity carries is a separate thing from the identity itself,
// and the separation is deliberate. The identity is what Go code matches; the
// code is the stable identifier a consumer outside Go reads, obtained from any
// error in the chain with CodeOf. It is an output, never an input to matching —
// writing the string back yields a string, not an identity.
//
// CompilerError is the occurrence: it carries the Category, the SourceLocation
// and the surrounding detail that differ between one failure and the next.
//
// The division follows from that sharing. Anything that differs between two
// occurrences of the same condition belongs on the instance, because a single
// shared value cannot express it; anything true of the condition itself belongs
// beside the identity, declared once.
//
// # Building an occurrence is a decision about identity, and it is forced
//
// A CompilerError holds no exported field, so there is no way to bring one into
// existence but through a constructor, and each constructor states what the
// occurrence names:
//
//   - Raise takes the condition as a typed, required argument and admits only a
//     value Register returned. An error written out by hand does not satisfy
//     Identity, so the program handing one over does not compile; one that
//     embeds Identity does satisfy it, because embedding promotes the method
//     rather than declaring it, and Raise refuses that value when it runs.
//   - Unidentified states that the failure names no registered condition. It is
//     an escape hatch and its name is the point: a diagnostic built there prints
//     a category with no short code beside it, which reads almost exactly like
//     one that names a condition, so the gap is made visible at the call site
//     instead of at the terminal.
//   - Wrap re-categorises an existing chain and takes whatever identity that
//     chain carries. Which one it carries is a property of the value rather than
//     of the call, so it is the one constructor whose identity is not decided
//     where it is written.
//
// The reason the choice is forced rather than encouraged is that a diagnostic
// naming no condition is indistinguishable, to the person reading it, from one
// that does — the bracketed category is there either way. A shape that let the
// identity be omitted would make the commonest failure the silent one.
//
// One form of occurrence is not an envelope. Occurrence builds a finding whose
// whole text its raise site composed and which travels inside a larger report,
// whose frames supply the category and the scope. It requires a registered
// condition, admitted exactly as Raise admits one, and renders its text and nothing
// beside it: the condition is matchable and its codes reach the head a surface
// prints, while the text stays the raise site's own. Its own documentation says
// why it reaches the condition through Is and As rather than through Unwrap.
package errors

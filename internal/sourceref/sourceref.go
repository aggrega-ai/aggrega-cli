// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package sourceref owns the single rule for reading a Recipe's source
// reference as local or remote, and the single statement of what a local one is
// resolved against. It is a leaf — it imports the standard library and two
// packages that import nothing of this module's, the containment predicate and
// the condition registry — and that is what makes it the one home reachable from
// every side that asks: the render imports the dependency descent and the
// catalog load precedes both, so a rule living inside any one of them would be
// reachable from that one and re-implemented in the others.
//
// # What a local path is bounded by
//
// A local path an entity declares for content it carries — its source, and every
// file an `$import:` names — is resolved against the directory holding the
// entity's document and must stay inside it; [AnchorContent] states both halves
// once, for every such path. Everything a publication carries for the entity is
// taken from that directory, so a path leaving it names content that would not
// travel with the entity. The bound
// is stated here rather than in the catalog load because the render resolves a
// templated source's value too, and the two must refuse the same paths.
//
// The locality rule is a heuristic over the reference's own text, and the whole
// of what it reads is that text: a reference carrying a recognised transport
// scheme prefix, or naming a repository, is fetched; anything else names a path
// the workspace already holds. Nothing outside the string decides it — no entity,
// no values, no infrastructure, no filesystem — which is why the rule can sit
// this low.
//
// The distinction is load-bearing rather than cosmetic: a reference the workspace
// already holds is never pinned in a Release's lockfile, so both the pass that
// writes the lockfile and the pass that renders against one have to draw the line
// in the same place. Two copies drawing it identically today would be exactly what
// diverges the next time a transport is added.
//
// # Which value the resolution is owed, and why that decides where it runs
//
// A reference written as a template names nothing until it has been evaluated, so
// the directory it is relative to cannot be joined to it while it is still text:
// joining first resolves the template's spelling and leaves the value the author
// actually wrote unresolved. Whether a reference carries a template is therefore
// read here too, beside the locality rule, because the two together decide the
// one question — resolve this reference now, or resolve what it produces later —
// and a surface that could answer one without the other would answer it wrongly
// for exactly the references that differ.
//
// # Two questions about templates, and reading one for the other gets both wrong
//
// A source field carrying a template raises two questions that look like one.
// Whether THIS FIELD is still text decides when it is resolved, and it is asked of
// one string. Whether THE SOURCE'S IDENTITY is still text decides whether its
// author already knows which artifact it names, and so whether that artifact can
// be pinned ahead of a Release; it is asked of a whole source, and only the
// fields the artifact-identity join is keyed on can answer it. A structured source
// whose provider-defined key carries a template answers yes to the first about
// that key and no to the second about the whole, which is why both readings live
// here, named apart, over one walk of the shapes a source takes.
package sourceref

import (
	"strings"
)

// templateOpen is the opening delimiter a source template is written with. The
// source surface fixes the pair rather than inheriting a configurable one, so a
// single spelling decides every reading of whether a reference is still text.
const templateOpen = "{{"

// identityFields names the structured-source keys whose authored text fixes WHICH
// artifact the source names, as opposed to how it is configured once obtained.
//
// The set is the terms the artifact-identity join is keyed on, read off that join
// rather than restated from any document: the location terms kind, url, chart and
// module_subdir, plus BOTH keys that can decide the version term. Which of those
// two decides it depends on the kind — a resolver that pins the authored version
// verbatim leaves version deciding, and one that resolves a reference to something
// immutable leaves ref deciding and pins a value the author never wrote — so a
// reading that took only one of them would call an artifact's identity knowable
// for a kind where it is not.
//
// A structured source is an open object and every other key it carries is
// provider-defined, reaching the entity's own templates rather than the fetch. Such
// a key decides nothing about which artifact is named, so a template in one leaves
// the identity knowable.
var identityFields = [...]string{"kind", "url", "chart", "module_subdir", "version", "ref"}

// IdentityFields returns the structured-source keys [IdentityTemplated] consults,
// which are the keys whose authored text fixes which artifact a source names.
//
// It is exported for one reason: the package that builds a source's fetch
// coordinates declares the same keys, and it declares them from a package this
// leaf cannot import, so the correspondence between the two sets can
// only be checked from that side. Nothing in the reading itself needs this, and a
// caller reaching for it to re-derive the reading is re-implementing
// [IdentityTemplated] rather than checking it.
//
// It returns a copy, so a caller cannot rewrite the set it is checking against.
func IdentityFields() []string {
	out := make([]string, len(identityFields))
	copy(out, identityFields[:])
	return out
}

// Reference returns the single reference a source value names, and whether the
// value is a shape a reference can be read out of at all.
//
// It is the one place the two shapes a source field takes are walked for that
// term. A scalar source IS its reference. A structured source names it at the url
// entry, which is the field naming what would be fetched; a structured source
// carrying no url, or one whose url is not text, is a readable shape that names
// nothing, which is a different answer from a shape this package cannot read.
//
// Any other shape reports unreadable. That includes a structured source that
// arrived through an order-preserving decode, which is an ordered container rather
// than a map — the same two-shape union the source-template walk covers, mirrored
// here deliberately so the two cannot come to disagree about which values they can
// read. A source that reaches lock-time descent in a shape neither covers is
// refused there by name, so reporting it unreadable here defers to that refusal
// rather than standing in for it.
//
// # Why this walk and the source-template walk stay apart
//
// The renderer's source-template walk classifies which PARTS of a source carry a
// template and what each part is called; this one projects the single reference a
// source names. Both cross the same union of shapes and neither is folded into the
// other, because they ask different questions and are reached from different
// sides: the catalog load and the dependency descent read a source long before any
// render and cannot import the renderer, which is what puts this projection in a
// leaf. Each site composes its own question over it — where a relative
// reference is anchored, what path a local one names, whether the author already
// knows which artifact is meant — and a single classifier folding those together
// would answer none of them cleanly. What must stay in step is only the union of
// shapes, which is why each walk names the other where it is declared.
func Reference(source any) (ref string, readable bool) {
	switch s := source.(type) {
	case string:
		return s, true
	case map[string]any:
		url, _ := s["url"].(string)
		return url, true
	default:
		return "", false
	}
}

// IsLocal reports whether a source value names a reference the workspace already
// holds rather than one fetched over a transport.
//
// It accepts the two shapes a source field takes, through the one walk that reads
// a reference out of either. A structured source carrying no url — or one whose
// url is not text — reaches the rule with nothing and is not local, the same
// answer a scalar naming nothing gets. Any other shape names nothing this rule can
// place and is not local either.
func IsLocal(source any) bool {
	ref, _ := Reference(source)
	return IsLocalURL(ref)
}

// IdentityTemplated reports whether a source value's identity is still text: that
// is, whether any field whose authored value fixes WHICH artifact the source names
// carries a template action.
//
// It is the question a pin turns on, and it is deliberately not the question
// [IsTemplated] answers. IsTemplated reads one field's text, which is what decides
// whether THAT field is anchored now or once it has a value. This reads a whole
// source and asks whether its author already knows what it resolves to: a source
// whose identity is written out in full is knowable to them, while one whose
// identity comes from a template is knowable only once a Release fills it in.
//
// The distinction between an identity-bearing field and a provider-defined one is
// the whole of what this adds over a substring test, and it runs both ways. A
// template anywhere in a scalar source makes the identity unknowable, because a
// scalar source IS its own identity. A template in a structured source's version,
// ref, chart, module_subdir, kind or url does the same. A template in any other
// key of a structured source does NOT: that key is the provider's, it reaches the
// entity's templates rather than the fetch, and the artifact named is the same one
// whatever it renders to.
//
// A shape no reference can be read out of reports false. Such a shape is refused
// by name where a source spec is built, so answering true here would report a
// templated identity for a value whose identity was never read.
func IdentityTemplated(source any) bool {
	switch s := source.(type) {
	case string:
		return IsTemplated(s)
	case map[string]any:
		for _, field := range identityFields {
			if text, ok := s[field].(string); ok && IsTemplated(text) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// Declared reports whether a source value names anything at all.
//
// A source that evaluated to an empty string leaves the entity with none just as
// an absent source does, and an author cannot tell the two apart from the
// outcome — so this is one condition rather than two. Any shape other than nil
// and string carries a source: a structured source is a container, and a
// container that exists names something.
func Declared(source any) bool {
	switch s := source.(type) {
	case nil:
		return false
	case string:
		return s != ""
	default:
		return true
	}
}

// NeedsFetch reports whether a source can only be obtained over a transport.
//
// It is the question a run asks about its own reach rather than about the
// entity: a reference the workspace already holds is usable from whatever is on
// disk, while one named by a transport reference exists nowhere until something
// retrieves it. A run holding no record of what to retrieve, and no store to
// retrieve it into, can satisfy the first and cannot begin the second.
//
// It sits beside the locality rule rather than above it because it is that rule
// plus the one thing a value has that a reference does not, which is that it may
// name nothing at all. Deriving it anywhere else would put the transport list in
// one package and the conclusion drawn from it in another, which is the split
// this package exists to prevent.
func NeedsFetch(source any) bool {
	return Declared(source) && !IsLocal(source)
}

// IsLocalURL reports whether a single source reference names something the
// workspace already holds.
//
// An empty reference is NOT local. It names nothing at all, which is a different
// answer from naming something near: reporting it local would hand the empty
// string onward as though it were a path, and the passes that ask this question
// treat a local reference as one they may use verbatim.
func IsLocalURL(ref string) bool {
	if ref == "" {
		return false
	}
	switch {
	case strings.HasPrefix(ref, "oci://"),
		strings.HasPrefix(ref, "https://"),
		strings.HasPrefix(ref, "http://"),
		strings.HasPrefix(ref, "git+ssh://"),
		strings.HasPrefix(ref, "git://"),
		strings.HasPrefix(ref, "ssh://"),
		strings.HasSuffix(ref, ".git"):
		return false
	default:
		return true
	}
}

// IsTemplated reports whether a source reference is still template text rather
// than a value.
//
// It is the question that decides WHEN a local reference is anchored, not
// whether: a reference carrying a template action names nothing yet, so the
// directory it is relative to belongs to the value it produces rather than to the
// spelling that produces it. Anchoring the spelling reads as harmless whenever
// the value happens to be relative, because a directory joined to a relative path
// is the same directory either way — and it silently mangles every value that is
// not, since an absolute path, a transport reference and an empty result are each
// decided by text the anchoring has already prefixed by the time they exist.
//
// The delimiter is the one the source surface evaluates with, so this reading and
// that evaluation cannot come to disagree about which references are text.
//
// It reads ONE field's text and answers about that field alone. Whether a whole
// source's identity is still text is [IdentityTemplated], which is a different
// question with a different answer: a structured source whose provider-defined key
// carries a template has a field that is text and an identity that is not.
func IsTemplated(ref string) bool {
	return strings.Contains(ref, templateOpen)
}

// AnchorToDocument resolves a source reference against the directory of the
// document that declared it, and is the one statement of what a relative source
// is relative TO.
//
// Two references come back exactly as given, each for its own reason. A
// reference that is not local names something fetched over a transport, and
// joining it to a directory would destroy the reference while leaving a value
// every later pass reads as local. An empty reference names nothing at all, which
// the locality reading already answers.
//
// Everything else is a path the author wrote beside their own document, and it is
// held to the bound every content path is held to: [AnchorContent] resolves it and
// refuses one that leaves the document's directory, an absolute path included.
// What it resolves to reads the same whatever directory the build runs from.
//
// An empty documentPath names no declaring document — the entity reached the
// caller with no file on disk its paths are relative to — so there is no
// directory for the reference to be relative to, and it comes back as given. It is still judged: the verdict is a property of the path rather than of
// the directory it is relative to, so a path that could leave any directory is
// refused here as it is everywhere else.
//
// It takes the document rather than the directory so that "the directory that
// declared it" is derived once here instead of at each call site, where a second
// spelling would be free to differ.
func AnchorToDocument(documentPath, ref string) (string, error) {
	if !IsLocalURL(ref) {
		return ref, nil
	}
	if documentPath == "" {
		if _, err := containInDocumentDirectory(documentPath, ref); err != nil {
			return "", err
		}
		return ref, nil
	}
	return AnchorContent(documentPath, ref)
}

// AnchorSource resolves a source VALUE against the directory of the document that
// declared it, whichever of the two shapes the value takes, and returns the source
// with nothing but its reference rewritten.
//
// It is [AnchorToDocument] applied at the level a source field is authored at, so
// the rule for what a relative source is relative to is stated once and reaches
// both shapes. A scalar source is its own reference and comes back as the resolved
// one. A structured source has its url entry resolved and every other key carried
// through untouched, including the keys the provider reads.
//
// 🔴 The template test here is over the REFERENCE and deliberately not over the
// identity. What is being rewritten is the reference, so what decides whether the
// rewrite is owed now is whether the reference names a path yet — a structured
// source whose version is a template still has a url an author wrote out in full,
// and deferring that url would leave it relative to whatever directory the build
// happens to run from. [IdentityTemplated] answers a different question, about
// whether the artifact can be pinned, and neither answer stands in for the other.
//
// A value whose reference needs no rewriting comes back as the same value rather
// than as a rebuilt copy of itself, and a shape no reference can be read out of
// comes back untouched.
func AnchorSource(documentPath string, source any) (any, error) {
	ref, readable := Reference(source)
	if !readable || IsTemplated(ref) {
		return source, nil
	}
	anchored, err := AnchorToDocument(documentPath, ref)
	if err != nil {
		return nil, err
	}
	if anchored == ref {
		return source, nil
	}
	return withReference(source, anchored), nil
}

// withReference returns a source value with its reference replaced and every other
// part carried verbatim.
//
// It is kept unexported so the rewrite has one entry point: a caller that could
// replace a reference without going through the anchoring above would be free to
// write a value the anchoring rule never sanctioned.
//
// The structured arm rebuilds the map rather than writing into the caller's, so a
// parsed document is not altered under a reader holding it. The rebuilt map's
// iteration order is Go's own, as the authored map's already is, and no rendered
// byte depends on either.
func withReference(source any, ref string) any {
	switch s := source.(type) {
	case string:
		return ref
	case map[string]any:
		out := make(map[string]any, len(s))
		for k, v := range s {
			out[k] = v
		}
		out["url"] = ref
		return out
	default:
		return source
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package entitykind declares the entity-kind vocabulary — the set of kinds an
// author may write in a document's top-level `kind:` field — and the per-kind
// facts that several packages otherwise each restated.
//
// It answers membership and vocabulary questions only: is this token one of the
// entity kinds, what is its path segment, does a given rule apply to it. It
// deliberately answers no per-kind *work* question: which struct a kind
// unmarshals into, which map it is indexed under, how it is bundled for
// transport, and which mediatype it publishes under all stay with the packages
// that do that work, because each of those carries information this vocabulary
// does not have.
//
// The distinction is worth stating because the two are easy to conflate. A
// package that needs to know *whether* a kind participates in something reads it
// here. A package that needs to know *what to do* for each kind keeps its own
// per-kind code and binds it to this set with a coverage assertion, so a kind
// added here fails that package's tests until it is handled rather than being
// silently skipped. A restated membership list, by contrast, agrees on the day
// it is written and disagrees silently the day a kind is added.
package entitykind

import "strings"

// Kind is the canonical name of an entity kind. It is the exact string an author
// writes in a document's top-level `kind:` field, and the value packages that
// dispatch on kind compare against.
type Kind string

const (
	// Component is the platform catalog's compute-or-resource definition.
	Component Kind = "Component"
	// Feature is a catalog capability attached to a component or a release.
	Feature Kind = "Feature"
	// Recipe is the infrastructure implementation behind a component.
	Recipe Kind = "Recipe"
	// FeatureRecipe is the infrastructure implementation behind a feature.
	FeatureRecipe Kind = "FeatureRecipe"
	// SchemaLibrary publishes reusable schema fragments for other entities to
	// import.
	SchemaLibrary Kind = "SchemaLibrary"
	// MappingLibrary publishes reusable mapping templates for other entities to
	// import.
	MappingLibrary Kind = "MappingLibrary"
	// WorkloadLibrary publishes reusable workload templates for other entities to
	// import.
	WorkloadLibrary Kind = "WorkloadLibrary"
	// ArtifactLibrary publishes reusable artifact bundles for other entities to
	// import.
	ArtifactLibrary Kind = "ArtifactLibrary"
	// Aggregate is the developer's application-intent document.
	Aggregate Kind = "Aggregate"
	// Plugin is the authoring surface that pins a WebAssembly plugin artifact by
	// digest.
	Plugin Kind = "Plugin"
	// Workload is the platform-owned workload document, written inside a Recipe
	// or a FeatureRecipe rather than addressed on its own.
	Workload Kind = "Workload"
	// Manifest is the operator's deployment-target document.
	Manifest Kind = "Manifest"
	// Release is the operator's environment-binding document.
	Release Kind = "Release"
)

// Entry is the per-kind record. Its fields are the facts a consumer cannot
// derive from the kind name alone; anything derivable is a function on this
// package instead of a stored field, so there is nothing to keep in agreement.
type Entry struct {
	// Kind is the canonical kind name.
	Kind Kind
	// ConventionsLinted reports whether the publishing-conventions rules apply to
	// documents of this kind. It is a separate fact from whether the kind can be
	// published: an Aggregate is publishable but is an authoring-side document
	// that the conventions rules do not examine, so neither answer implies the
	// other.
	ConventionsLinted bool
	// ReferencePage reports whether a language-reference page documents this kind.
	// A kind without one has its guidance pointers fall back to the
	// language-reference index rather than naming a page that does not exist.
	ReferencePage bool
	// SharesDirectory reports whether a document of this kind may sit in one
	// directory beside other entity documents. Every other kind has a directory
	// of its own, as a published entity has one coordinate per version.
	//
	// The rule is stated by exception on purpose: the zero value is the rule, so
	// a kind added to the vocabulary without this field set is one entity per
	// directory from the moment it exists, and nobody has to remember to opt it
	// in. The kinds that set it are the top-level documents a workspace root
	// holds side by side and that carry no files of their own to package.
	SharesDirectory bool
}

// entries is the vocabulary, in fixed author-defined order: catalog-authored
// kinds, then the developer and plugin authoring surfaces, then the
// deployment-side kinds. Enumeration hands out a copy of this slice so a
// consumer that iterates it emits deterministic output.
var entries = []Entry{
	{Kind: Component, ConventionsLinted: true, ReferencePage: true},
	{Kind: Feature, ConventionsLinted: true, ReferencePage: true},
	{Kind: Recipe, ConventionsLinted: true, ReferencePage: true},
	{Kind: FeatureRecipe, ConventionsLinted: true, ReferencePage: true},
	{Kind: SchemaLibrary, ConventionsLinted: true, ReferencePage: true},
	{Kind: MappingLibrary, ConventionsLinted: true, ReferencePage: true},
	{Kind: WorkloadLibrary, ConventionsLinted: true, ReferencePage: false},
	{Kind: ArtifactLibrary, ConventionsLinted: true, ReferencePage: false},
	{Kind: Aggregate, ConventionsLinted: false, ReferencePage: true, SharesDirectory: true},
	{Kind: Plugin, ConventionsLinted: true, ReferencePage: true},
	{Kind: Workload, ConventionsLinted: false, ReferencePage: true},
	{Kind: Manifest, ConventionsLinted: false, ReferencePage: true, SharesDirectory: true},
	{Kind: Release, ConventionsLinted: false, ReferencePage: true, SharesDirectory: true},
}

// Entries returns every entry in fixed author-defined order. The returned slice
// is a copy, so a caller may not change the vocabulary through it.
func Entries() []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	return out
}

// Kinds returns every entity kind in fixed author-defined order. The returned
// slice is freshly allocated, so a caller may reorder or filter it without
// changing the vocabulary.
func Kinds() []Kind {
	out := make([]Kind, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Kind)
	}
	return out
}

// Lookup returns the entry for a kind and whether the kind is one of the entity
// kinds. A caller holding an unvalidated token uses the second return value
// rather than treating a zero entry as an answer.
func Lookup(k Kind) (Entry, bool) {
	for _, e := range entries {
		if e.Kind == k {
			return e, true
		}
	}
	return Entry{}, false
}

// IsKind reports whether a token is one of the entity kinds. The comparison is
// exact, so it is the check for a value that is already a canonical kind name —
// a `kind:` field read from a document, for example. A lowercase token from a
// path or URI goes through BySegment instead.
func IsKind(k Kind) bool {
	_, ok := Lookup(k)
	return ok
}

// Segment returns the lowercase token that names a kind in a path, a URI, or
// any other lowercase-only address. Kind names are mixed-case and these
// addresses are not, so the segment is the kind name lowercased — there is no
// per-kind exception, and a test holds that so a future kind cannot quietly
// introduce one.
//
// Segment does not report whether its argument is an entity kind: it lowercases
// whatever it is given. A caller that must reject an unknown token checks
// membership first, or resolves in the other direction with BySegment.
func Segment(k Kind) string {
	return strings.ToLower(string(k))
}

// BySegment resolves a lowercase path or URI token back to its entity kind, and
// reports whether the token names one. It is the inverse of Segment and the
// single place that resolution happens, so an address surface and the kind it
// addresses cannot disagree about spelling.
func BySegment(segment string) (Kind, bool) {
	for _, e := range entries {
		if Segment(e.Kind) == segment {
			return e.Kind, true
		}
	}
	return "", false
}

// Segments returns the lowercase token for every entity kind, in fixed
// author-defined order — the enumeration counterpart to BySegment's lookup, for
// a caller that must present or iterate the whole address vocabulary rather
// than resolve one token. The returned slice is freshly allocated, so a caller
// may reorder it — into lexical order for a diagnostic, say — without changing
// the vocabulary.
func Segments() []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, Segment(e.Kind))
	}
	return out
}

// ConventionsLinted returns every kind the publishing-conventions rules apply
// to, in fixed author-defined order. The returned slice is freshly allocated.
func ConventionsLinted() []Kind {
	out := make([]Kind, 0, len(entries))
	for _, e := range entries {
		if e.ConventionsLinted {
			out = append(out, e.Kind)
		}
	}
	return out
}

// IsConventionsLinted reports whether the publishing-conventions rules apply to
// documents of the named kind. An unrecognised token reports false: a kind the
// vocabulary does not contain is not a kind the rules describe.
func IsConventionsLinted(k Kind) bool {
	e, ok := Lookup(k)
	return ok && e.ConventionsLinted
}

// SharesDirectory reports whether a document of the named kind may sit in one
// directory beside other entity documents. An unrecognised token reports false,
// so a kind the vocabulary does not contain is held to a directory of its own
// rather than admitted by default.
func SharesDirectory(k Kind) bool {
	e, ok := Lookup(k)
	return ok && e.SharesDirectory
}

// DirectorySharing returns every kind that may share a directory, in fixed
// author-defined order. The returned slice is freshly allocated.
func DirectorySharing() []Kind {
	out := make([]Kind, 0, len(entries))
	for _, e := range entries {
		if e.SharesDirectory {
			out = append(out, e.Kind)
		}
	}
	return out
}

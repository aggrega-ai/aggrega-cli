// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure

// This file is the single home for resolution provenance: the tags a Lookup
// stamps to record which source served an entity or artifact, and the words a
// user-facing surface prints for them. Both halves live here together on
// purpose. The tags are consumed as decision inputs — the pin predicates read
// them to decide what enters the lockfile — while the words are consumed only
// for display, and keeping the two beside each other is what stops a producer,
// a predicate and a printer from each inventing its own spelling.
//
// Provenance is transient. It describes what one run's lookup did, on one
// machine, and it is never written to the lockfile: a lockfile is byte-identical
// across machines that resolved the same closure from different local state, and
// a value that depends on local state would break that.

// ResolutionPathCatalog is the ResolutionPath a Lookup reports for an entity
// resolved from a local catalog directory. A pull-side descent pins an entity
// only when it resolves from the registry-backed cache or pull sources; an entity
// resolved this way stays catalog-resident and resolves at build through the same
// local catalog walk, never through a lockfile pin.
//
// It is also the path a plugin artifact carries when its bytes were read from the
// local filesystem — beside its entity in such a directory, or at a path the author
// named — which is how the artifact of an entity authored there is resolved.
const ResolutionPathCatalog = "tier-1-catalog"

// ResolutionPathCache is the ResolutionPath a Lookup reports for an entity read
// from the workspace content cache — a previously-pulled artifact still present
// locally, so this run performed no fetch.
const ResolutionPathCache = "tier-2-cache"

// ResolutionPathRegistry is the ResolutionPath a Lookup reports for an entity
// fetched from a registry during this run. It is the source whose signature a pull
// always verifies, which is why ResolvedFromRegistry exists rather than callers
// testing the value themselves.
const ResolutionPathRegistry = "tier-3-pull"

// ResolutionPathExternal is the ResolutionPath a Lookup reports for the Release's
// own Aggregate and Manifest. The caller resolves each from the Release's own
// reference before the walk — a file beside the Release or at the relative path
// the Release writes, or, for an Aggregate named by identity, an entry of the
// workspace's catalog view, pinned cache entries included — and registers it with
// the Lookup by the bytes it read, so the walk takes it as given instead of
// resolving it through the catalog sources. The one exception is a top-level
// Aggregate a pull fetched from a registry during the run, which is seeded as
// ResolutionPathRegistry instead.
const ResolutionPathExternal = "external"

// ResolutionPathEmbedded is the ResolutionPath a Lookup reports for a core plugin
// resolved from the copy that ships inside the compiler binary.
//
// The path states provenance only — where the bytes came from — never whether the
// reference is pinned. The pin decision belongs to the pin predicates and differs
// by plugin type, so a reader must not infer one from the other: a core provider
// served this way is left unpinned, while every other declared type is pinned even
// though the binary carries it.
const ResolutionPathEmbedded = "tier-0-embed"

// ResolutionSource is the word a user-facing surface prints for the source that
// served an entity or artifact. It is deliberately a separate type from the
// ResolutionPath tags above: a tag is an internal decision input whose spelling
// callers branch on, while a source is vocabulary a reader has to be able to
// reconduct to the CLI reference, and collapsing the two would put an internal
// tag on a terminal the moment a new tag appeared.
//
// Every surface that names a source reads these constants, so the vocabulary
// changes in one place and every surface follows.
type ResolutionSource string

const (
	// SourceFetchedFresh reports that this run fetched the bytes from a registry.
	SourceFetchedFresh ResolutionSource = "fetched-fresh"
	// SourceCacheReuse reports that this run reused a cache slot already holding
	// the bytes, and fetched nothing.
	SourceCacheReuse ResolutionSource = "cache-reuse"
	// SourceLocal reports that the bytes were found on the local filesystem — a
	// catalog directory, or a path the author named directly.
	SourceLocal ResolutionSource = "local"
	// SourceInBinary reports that the bytes came from the copy inside the
	// compiler binary, so no registry, cache or catalog was consulted for them.
	SourceInBinary ResolutionSource = "in-binary"
	// SourceExternal reports that the entry is the Release's own Aggregate or
	// Manifest, read from the file or entry the Release names rather than resolved
	// as a dependency through the catalog sources.
	SourceExternal ResolutionSource = "external"
)

// ResolutionSourceFor maps a ResolutionPath tag to the word a user-facing surface
// prints for it, reporting whether the tag was recognised. An unrecognised tag
// returns false rather than a guess, so a caller decides what to show instead of
// this mapper silently publishing an internal tag it does not have a word for.
func ResolutionSourceFor(resolutionPath string) (ResolutionSource, bool) {
	switch resolutionPath {
	case ResolutionPathRegistry:
		return SourceFetchedFresh, true
	case ResolutionPathCache:
		return SourceCacheReuse, true
	case ResolutionPathCatalog:
		return SourceLocal, true
	case ResolutionPathEmbedded:
		return SourceInBinary, true
	case ResolutionPathExternal:
		return SourceExternal, true
	}
	return "", false
}

// ReportedResolutionSource is the text a user-facing surface reports for where
// one run's resolution took a reference from, given the ResolutionPath tag the
// lookup stamped on it.
//
// It is the word ResolutionSourceFor maps the tag to where there is one, and the
// tag itself where there is none: a tag with no word yet is a gap worth seeing,
// so it is shown rather than dropped. An empty tag — a reference whose lookup
// recorded no source — yields the empty string, which a surface omits rather
// than reporting as a source.
//
// The pull detail column and the structured answers reporting one resolution
// both read this rather than composing the fallback themselves, so they name each
// source in one spelling.
func ReportedResolutionSource(resolutionPath string) string {
	if source, ok := ResolutionSourceFor(resolutionPath); ok {
		return string(source)
	}
	return resolutionPath
}

// ResolvedFromRegistry reports whether a resolution fetched from a registry
// during this run — the source whose signature a pull always verifies; a cache
// resolution is verified by a pull only where nothing records its fact. Callers
// match through this predicate rather than against the tag string,
// because a string comparison keeps compiling after the tag is respelled and
// then silently stops matching, with nothing failing and nothing logged.
func ResolvedFromRegistry(resolutionPath string) bool {
	return resolutionPath == ResolutionPathRegistry
}

// AcquiredFromRegistry reports whether the bytes a resolution served were
// acquired from a registry — fetched during this run, or read from the content
// cache an earlier fetch filled. Those are the resolutions a pull verified the
// signature of when it fetched the bytes, and so the ones a recorded signature can
// speak for. A catalog directory, the copy inside the compiler binary, and the
// Release's own documents are none of them: nothing ever verifies a registry
// signature for bytes that did not come from one.
//
// Like ResolvedFromRegistry it exists so a caller matches through a predicate
// rather than against the tags, which keep compiling after a respelling and then
// silently stop matching.
func AcquiredFromRegistry(resolutionPath string) bool {
	return resolutionPath == ResolutionPathRegistry || resolutionPath == ResolutionPathCache
}

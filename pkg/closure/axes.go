// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

// VerifiableRef is the common shape every closure axis projects its members onto
// for the registry-touching passes: the identity to report, the content pin, the
// manifest digest a signature commits to, and the resolution path that says whether
// the reference was freshly fetched from a registry at all.
//
// It is deliberately narrower than ResolvedRef and ResolvedLibrary: a verification
// or marker-discovery pass needs an identity, a subject digest and a tier, and
// nothing else. Projecting onto one shape is what lets those passes treat every
// axis alike instead of branching per kind.
type VerifiableRef struct {
	// URI is the reference's canonical identity — a bare catalog identity, a bare
	// library name, or a host-carrying artifact reference. It is the join key onto
	// the matching lockfile row.
	URI string
	// ResolvedVersion is the concrete version the resolver selected.
	ResolvedVersion string
	// Digest is the portable content digest used for the cache pin and the
	// integrity check.
	Digest string
	// ManifestDigest is the OCI manifest digest a signature commits to — the verify
	// subject. Empty for a reference that resolved below the registry tier.
	ManifestDigest string
	// ResolutionPath is the resolution-tier diagnostic tag, which tells a caller
	// whether this reference was freshly fetched from a registry.
	ResolutionPath string
	// KindHint is the collection-site kind hint the reference was recorded with,
	// carried only on an axis whose members mix kinds. Empty on a single-kind axis,
	// whose VerifiableAxis.Kind states the kind instead.
	KindHint string
}

// VerifiableAxis is one axis of a computed closure: the pinned references of a
// single section, paired with the catalog kind those references publish under.
//
// Kind and the per-reference KindHint are alternatives, never both: a section whose
// members all publish under one kind states it here once, and a section whose
// members mix kinds leaves it empty and carries a hint per reference. A caller
// resolves the kind by preferring Kind and falling back to the hint, so the kind
// vocabulary is spelled once per section rather than once per call site.
type VerifiableAxis struct {
	// Axis is the closure section this axis reads, named exactly as the field is
	// declared. It is the key the coverage assertion joins on, so it is the field
	// name rather than a prose label.
	Axis string
	// Kind is the catalog kind every reference on this axis publishes under. Empty
	// on a mixed-kind axis, whose references carry their own KindHint.
	Kind string
	// Refs are the axis's members projected onto the common shape. An axis with no
	// members is still returned, so the set of axes describes the closure's shape
	// rather than one closure's contents.
	Refs []VerifiableRef
}

// axesWithoutVerifiableRefs names each closure section deliberately left out of
// VerifiableAxes, mapped to the reason it carries no verifiable reference.
//
// A member must be a section whose contents can carry neither a signature nor a
// withdrawal marker at all. "Not wired yet" does not qualify, because that is
// exactly the state the coverage assertion beside this map exists to report: an
// exemption written for an unfinished section would silence the one signal that a
// section is uncovered.
var axesWithoutVerifiableRefs = map[string]string{
	"SourceArtifacts": "a source artifact is a chart, module or fragment fetched from its own " +
		"upstream — never an artifact this toolchain publishes, so nothing here ever signs one " +
		"or attaches a withdrawal marker to one, and the pin carries no slot to record either. " +
		"Its integrity rests instead on the content digest, which the cached read recomputes and " +
		"compares on every build",
}

// VerifiableAxes returns every closure section that carries references a
// registry-touching pass can verify or discover a withdrawal marker for — the
// catalog entities, the four library sections, and the Wasm plugins — each
// projected onto the common reference shape.
//
// It is the single enumeration of those sections. A pass that walked the sections
// itself would have to name each one, and the next section added would be covered
// by nobody: the section would simply not appear at the pass, with nothing failing
// to say so. Consuming this instead makes a new section a build-time question
// rather than a silent omission, because the coverage assertion beside this
// function requires every declared section to be either returned here or named in
// axesWithoutVerifiableRefs with its reason.
//
// Every covered axis is returned whether or not it holds members, so the returned
// set describes the closure's shape rather than one closure's contents.
func (c Closure) VerifiableAxes() []VerifiableAxis {
	return []VerifiableAxis{
		{Axis: "Entities", Refs: refsOf(c.Entities)},
		{Axis: "SchemaLibraries", Kind: "SchemaLibrary", Refs: libraryRefsOf(c.SchemaLibraries)},
		{Axis: "MappingLibraries", Kind: "MappingLibrary", Refs: libraryRefsOf(c.MappingLibraries)},
		{Axis: "WorkloadLibraries", Kind: "WorkloadLibrary", Refs: libraryRefsOf(c.WorkloadLibraries)},
		{Axis: "ArtifactsLibraries", Kind: "ArtifactsLibrary", Refs: libraryRefsOf(c.ArtifactsLibraries)},
		{Axis: "Plugins", Refs: refsOf(c.Plugins)},
	}
}

// refsOf projects a mixed-kind section onto the common shape, carrying each
// reference's own collection-site kind hint.
func refsOf(refs []ResolvedRef) []VerifiableRef {
	out := make([]VerifiableRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, VerifiableRef{
			URI:             r.URI,
			ResolvedVersion: r.ResolvedVersion,
			Digest:          r.Digest,
			ManifestDigest:  r.ManifestDigest,
			ResolutionPath:  r.ResolutionPath,
			KindHint:        r.KindHint,
		})
	}
	return out
}

// libraryRefsOf projects a library section onto the common shape. A library
// carries no collection-site kind hint — its section fixes its kind — so the hint
// is left empty and the axis states the kind.
func libraryRefsOf(libs []ResolvedLibrary) []VerifiableRef {
	out := make([]VerifiableRef, 0, len(libs))
	for _, l := range libs {
		out = append(out, VerifiableRef{
			URI:             l.URI,
			ResolvedVersion: l.ResolvedVersion,
			Digest:          l.Digest,
			ManifestDigest:  l.ManifestDigest,
			ResolutionPath:  l.ResolutionPath,
		})
	}
	return out
}

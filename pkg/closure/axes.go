// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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

// EntitiesAxis names the catalog-entity section, as VerifiableAxes names it and as
// the lockfile's Entities field is named. It is the one axis whose members are
// identified by their kind as well as their reference (see EntityKey), so a reader
// joining a reference to its lockfile row adds the kind on this axis alone.
const EntitiesAxis = "Entities"

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

// LibraryAxis describes one library axis of a closure: a section whose members
// are cross-document libraries selected by minimum version and pinned by the
// lockfile. The four axes differ only in the fragment surface they admit and the
// section they accumulate into, so every operation over them is written once and
// parameterised by this descriptor rather than copied per axis.
//
// The exported fields are the axis's identity and vocabulary; the unexported
// accessor binds that identity to the public Closure section it accumulates into.
type LibraryAxis struct {
	// Axis is the Closure section this axis reads, named exactly as the field is
	// declared, so it joins onto the coverage assertions the same way VerifiableAxis
	// does.
	Axis string
	// Kind is the catalog kind the axis's members publish under.
	Kind string
	// FragmentPrefix is the JSON Pointer fragment body that marks a reference on
	// this axis — the surface `<Library>[:<version>]#<FragmentPrefix><Entry>` admits.
	// All four axes share one host-less identity grammar and differ only here.
	FragmentPrefix string
	// Label is the axis's name in operator-facing diagnostics, where a lockfile row
	// is reported as drifted, missing or no longer used.
	Label string

	// libs binds the axis to its Closure section. The walk's own accumulator needs
	// no such binding: it is keyed by axis name, so it grows with this table.
	libs func(*Closure) *[]ResolvedLibrary
}

// libraryAxes is the single declared set of library axes. Every pass that treats
// the axes alike ranges over it, so an axis added here is picked up by the walk,
// the reference grammar, minimum-version resolution, materialisation,
// canonical ordering, the resolver interface, lockfile drift reporting, the pin
// projection and the convention lint without any of them naming it.
//
// Order is load-bearing: it fixes the order diagnostics are emitted in, so
// reordering these rows reorders operator-visible output.
var libraryAxes = []LibraryAxis{
	{
		Axis: "SchemaLibraries", Kind: "SchemaLibrary",
		FragmentPrefix: "/schemas/", Label: "schema-library",
		libs: func(c *Closure) *[]ResolvedLibrary { return &c.SchemaLibraries },
	},
	{
		Axis: "MappingLibraries", Kind: "MappingLibrary",
		FragmentPrefix: "/mappings/", Label: "mapping-library",
		libs: func(c *Closure) *[]ResolvedLibrary { return &c.MappingLibraries },
	},
	{
		Axis: "WorkloadLibraries", Kind: "WorkloadLibrary",
		FragmentPrefix: "/workloads/", Label: "workload-library",
		libs: func(c *Closure) *[]ResolvedLibrary { return &c.WorkloadLibraries },
	},
	{
		Axis: "ArtifactLibraries", Kind: "ArtifactLibrary",
		FragmentPrefix: "/artifacts/", Label: "artifact-library",
		libs: func(c *Closure) *[]ResolvedLibrary { return &c.ArtifactLibraries },
	},
}

// LibraryAxes returns the declared library axes in diagnostic order. A caller that
// must bind the axis set to its own types — a lockfile section, a catalog map —
// ranges over this rather than restating the set, so the binding can be asserted
// complete instead of silently covering a subset.
func LibraryAxes() []LibraryAxis { return libraryAxes }

// LibraryAxisNamed returns the axis whose Closure section is named section. It
// exists for the few call sites that are genuinely about one axis — a public
// entry point named for the surface it walks — rather than about the axes alike.
// A name no axis declares is a programming error at package scope, so it panics
// rather than returning a zero axis that would silently accumulate nowhere.
func LibraryAxisNamed(section string) LibraryAxis {
	for _, a := range libraryAxes {
		if a.Axis == section {
			return a
		}
	}
	panic("closure: no library axis named " + section)
}

// Libraries returns the axis's section of c.
func (a LibraryAxis) Libraries(c *Closure) []ResolvedLibrary { return *a.libs(c) }

// pendingLibs returns the axis's in-progress accumulator on s. The accumulator is
// keyed by axis name rather than held in a field per axis, so it needs no binding
// in the table and an axis added there is accumulated without further wiring.
func (a LibraryAxis) pendingLibs(s *walkState) map[string]*pendingLibrary {
	return s.libs[a.Axis]
}

// SetLibraries replaces the axis's section of c.
func (a LibraryAxis) SetLibraries(c *Closure, libs []ResolvedLibrary) { *a.libs(c) = libs }

// IsRef reports whether value is a host-less cross-document library reference on
// this axis, in the identity form `<Library>[:<version>]#<FragmentPrefix>…`.
// In-document anchors, host-bearing `oci://…` references and every other shape
// return false, so a discovery walk records only genuine cross-library references
// on the surface it is walking.
func (a LibraryAxis) IsRef(value string) bool {
	return isIdentityRefWithFragment(value, a.FragmentPrefix)
}

// ParseRef parses a host-less identity reference on this axis into its library
// name, optional version assertion and fragment body. See
// parseIdentityRefWithFragment for the grammar and the error shape.
func (a LibraryAxis) ParseRef(s string) (libraryName, version, fragment string, err error) {
	return parseIdentityRefWithFragment(s, a.FragmentPrefix)
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
	"ReleaseDocuments": "a Release document is the Release a resolution was asked for, or one it " +
		"extends, read as a file beside it — never an artifact this toolchain publishes and never " +
		"fetched from a registry, so nothing here ever signs one or attaches a withdrawal marker to " +
		"one, and the record carries no slot to hold either. Its integrity rests instead on the " +
		"content digest, which each load takes from the bytes it read and the drift comparison " +
		"checks against the recorded one whenever a lockfile written earlier is held to the " +
		"Release as it now stands",
	"Instances": "an instance is a place in the Release's composition that names a type, not an " +
		"artifact: the reference it names is a member of Entities, which this enumeration covers, " +
		"so whatever is signed or withdrawn is reached there, and the instance itself is never " +
		"published, fetched or written to a lockfile",
	"modules": "the walk's record of the modules the Aggregate includes, by the names they are " +
		"included under and the references naming them: each reference is a member of Entities, " +
		"which this enumeration covers, and the record is read by the sub-aggregate descent alone " +
		"and never written to a lockfile",
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
// The library sections are read from the declared axis set rather than listed
// here, so the two enumerations cannot disagree: an axis added to that set is
// verifiable by construction instead of needing to be remembered here as well.
func (c Closure) VerifiableAxes() []VerifiableAxis {
	axes := make([]VerifiableAxis, 0, len(libraryAxes)+2)
	axes = append(axes, VerifiableAxis{Axis: EntitiesAxis, Refs: refsOf(c.Entities)})
	for _, a := range libraryAxes {
		axes = append(axes, VerifiableAxis{
			Axis: a.Axis,
			Kind: a.Kind,
			Refs: libraryRefsOf(a.Libraries(&c)),
		})
	}
	return append(axes, VerifiableAxis{Axis: "Plugins", Refs: refsOf(c.Plugins)})
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

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import "go.yaml.in/yaml/v3"

// SchemaConsumer pairs a resolved Component/Feature URI with its schema body
// (a yaml.Node) so DiscoverSchemaLibraries can scan it for cross-document
// SchemaLibrary $ref: references. The caller (pull's pin flow or build's
// drift-verify flow) supplies these from the catalog after ComputeClosure has
// resolved the entity closure.
type SchemaConsumer struct {
	URI    string
	Schema *yaml.Node
}

// LibraryBodyFunc returns the entry bodies of a resolved SchemaLibrary so its own
// cross-document $ref: references can be descended transitively. The caller
// resolves the body from the catalog (a locally-authored library) or the content
// cache (a remote pinned library, via the lazy library walk's verified Tier-2 read).
// A false found return stops transitive descent into that library (its body is
// not available to scan); err is for a hard read/parse failure.
type LibraryBodyFunc func(lib ResolvedLibrary) (entryBodies []*yaml.Node, found bool, err error)

// MappingConsumer pairs a resolved Recipe/FeatureRecipe URI (or a MappingLibrary
// URI, during transitive descent) with a template body so DiscoverMappingLibraries
// can scan it for a field-root `$import:` MappingLibrary reference. For a Recipe /
// FeatureRecipe the Template is the valuesMapping body in `$import: <ref>`
// directive form; for a MappingLibrary entry it is the entry's own template body
// (which may itself carry a field-root `$import:`).
type MappingConsumer struct {
	URI      string
	Template string
}

// MappingBodyFunc returns the entry template bodies of a resolved MappingLibrary
// so its own cross-library `$import:` references can be descended transitively.
// The caller resolves the bodies from the catalog (a locally-authored or bridged
// library); a false found return stops transitive descent into that library.
type MappingBodyFunc func(lib ResolvedLibrary) (entryBodies []string, found bool, err error)

// WorkloadConsumer pairs a resolved Recipe/FeatureRecipe URI (or a WorkloadLibrary
// URI, during transitive descent) with a template body so DiscoverWorkloadLibraries
// can scan it for a field-root `$import:` WorkloadLibrary reference. For a Recipe /
// FeatureRecipe the Template is the workload body in `$import: <ref>` directive form;
// for a WorkloadLibrary entry it is the entry's own template body (which may itself
// carry a field-root `$import:`).
type WorkloadConsumer struct {
	URI      string
	Template string
}

// WorkloadBodyFunc returns the entry template bodies of a resolved WorkloadLibrary
// so its own cross-library `$import:` references can be descended transitively.
// The caller resolves the bodies from the catalog (a locally-authored or bridged
// library); a false found return stops transitive descent into that library.
type WorkloadBodyFunc func(lib ResolvedLibrary) (entryBodies []string, found bool, err error)

// ArtifactsConsumer pairs a resolved Recipe/FeatureRecipe URI (or an ArtifactsLibrary
// URI, during transitive descent) with a template body so DiscoverArtifactsLibraries
// can scan it for a field-root `$import:` ArtifactsLibrary reference. For a Recipe /
// FeatureRecipe the Template is one artifacts-map entry's body in `$import: <ref>`
// directive form (an artifacts map may contribute several consumers); for an
// ArtifactsLibrary bundle it is one bundle file body (which may itself carry a
// field-root `$import:`).
type ArtifactsConsumer struct {
	URI      string
	Template string
}

// ArtifactsBodyFunc returns the bundle file bodies of a resolved ArtifactsLibrary
// (flattened across every bundle entry) so its own cross-library `$import:`
// references can be descended transitively. The caller resolves the bodies from
// the catalog (a locally-authored or bridged library); a false found return stops
// transitive descent into that library.
type ArtifactsBodyFunc func(lib ResolvedLibrary) (entryBodies []string, found bool, err error)

// DiscoverMappingLibraries is the mapping-axis mirror of DiscoverSchemaLibraries:
// it discovers the MappingLibrary closure reachable from a set of
// Recipe/FeatureRecipe valuesMapping bodies via field-root `$import:` references,
// resolves each discovered library (minimum-version selection + lookup), and
// transitively descends each resolved library's own entry bodies to a fixpoint.
// The discovered + resolved libraries are merged into c.MappingLibraries.
//
// As on the schema axis, this is the single producer of the mapping-library axis
// of the closure, so pull's pin flow and build's drift-verify flow compute the
// identical closure. Termination is by a visited-set on the canonical library
// identity; a genuine library↔library cycle terminates discovery without error
// and surfaces as a cycle at `$import:` consumption (render time), not here.
func (c *Closure) DiscoverMappingLibraries(lookup Lookup, consumers []MappingConsumer, body MappingBodyFunc) error {
	descended := map[string]struct{}{}

	frontier := append([]MappingConsumer{}, consumers...)

	for len(frontier) > 0 {
		state := newWalkState()
		for _, lib := range c.MappingLibraries {
			state.mappingLibs[lib.URI] = libraryFromResolved(lib)
		}
		for _, mc := range frontier {
			if err := state.walkMappingImportInto(mc.Template, mc.URI); err != nil {
				return err
			}
		}
		if err := state.resolveLibraries(state.mappingLibs, lookup.ResolveMappingLibrary); err != nil {
			return err
		}
		c.MappingLibraries = nil
		for _, lib := range state.materialiseMappingLibs() {
			c.MappingLibraries = append(c.MappingLibraries, lib)
		}
		canonicaliseClosure(c)

		frontier = frontier[:0]
		for _, lib := range c.MappingLibraries {
			if _, seen := descended[lib.URI]; seen {
				continue
			}
			descended[lib.URI] = struct{}{}
			bodies, found, err := body(lib)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			for _, b := range bodies {
				frontier = append(frontier, MappingConsumer{URI: lib.URI, Template: b})
			}
		}
	}
	return nil
}

// DiscoverWorkloadLibraries is the workload-axis mirror of DiscoverMappingLibraries:
// it discovers the WorkloadLibrary closure reachable from a set of
// Recipe/FeatureRecipe workload bodies via field-root `$import:` references,
// resolves each discovered library (minimum-version selection + lookup), and
// transitively descends each resolved library's own entry bodies to a fixpoint.
// The discovered + resolved libraries are merged into c.WorkloadLibraries.
//
// As on the mapping axis, this is the single producer of the workload-library axis
// of the closure, so pull's pin flow and build's drift-verify flow compute the
// identical closure. Termination is by a visited-set on the canonical library
// identity; a genuine library↔library cycle terminates discovery without error
// and surfaces as a cycle at `$import:` consumption (render time), not here.
func (c *Closure) DiscoverWorkloadLibraries(lookup Lookup, consumers []WorkloadConsumer, body WorkloadBodyFunc) error {
	descended := map[string]struct{}{}

	frontier := append([]WorkloadConsumer{}, consumers...)

	for len(frontier) > 0 {
		state := newWalkState()
		for _, lib := range c.WorkloadLibraries {
			state.workloadLibs[lib.URI] = libraryFromResolved(lib)
		}
		for _, wc := range frontier {
			if err := state.walkWorkloadImportInto(wc.Template, wc.URI); err != nil {
				return err
			}
		}
		if err := state.resolveLibraries(state.workloadLibs, lookup.ResolveWorkloadLibrary); err != nil {
			return err
		}
		c.WorkloadLibraries = nil
		for _, lib := range state.materialiseWorkloadLibs() {
			c.WorkloadLibraries = append(c.WorkloadLibraries, lib)
		}
		canonicaliseClosure(c)

		frontier = frontier[:0]
		for _, lib := range c.WorkloadLibraries {
			if _, seen := descended[lib.URI]; seen {
				continue
			}
			descended[lib.URI] = struct{}{}
			bodies, found, err := body(lib)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			for _, b := range bodies {
				frontier = append(frontier, WorkloadConsumer{URI: lib.URI, Template: b})
			}
		}
	}
	return nil
}

// DiscoverArtifactsLibraries is the artifacts-axis mirror of DiscoverWorkloadLibraries:
// it discovers the ArtifactsLibrary closure reachable from a set of
// Recipe/FeatureRecipe artifacts bodies via field-root `$import:` references,
// resolves each discovered library (minimum-version selection + lookup), and
// transitively descends each resolved library's own bundle file bodies to a
// fixpoint. The discovered + resolved libraries are merged into c.ArtifactsLibraries.
//
// As on the workload axis, this is the single producer of the artifacts-library axis
// of the closure, so pull's pin flow and build's drift-verify flow compute the
// identical closure. Termination is by a visited-set on the canonical library
// identity; a genuine library↔library cycle terminates discovery without error
// and surfaces as a cycle at `$import:` consumption (render time), not here.
func (c *Closure) DiscoverArtifactsLibraries(lookup Lookup, consumers []ArtifactsConsumer, body ArtifactsBodyFunc) error {
	descended := map[string]struct{}{}

	frontier := append([]ArtifactsConsumer{}, consumers...)

	for len(frontier) > 0 {
		state := newWalkState()
		for _, lib := range c.ArtifactsLibraries {
			state.artifactsLibs[lib.URI] = libraryFromResolved(lib)
		}
		for _, ac := range frontier {
			if err := state.walkArtifactImportInto(ac.Template, ac.URI); err != nil {
				return err
			}
		}
		if err := state.resolveLibraries(state.artifactsLibs, lookup.ResolveArtifactsLibrary); err != nil {
			return err
		}
		c.ArtifactsLibraries = nil
		for _, lib := range state.materialiseArtifactsLibs() {
			c.ArtifactsLibraries = append(c.ArtifactsLibraries, lib)
		}
		canonicaliseClosure(c)

		frontier = frontier[:0]
		for _, lib := range c.ArtifactsLibraries {
			if _, seen := descended[lib.URI]; seen {
				continue
			}
			descended[lib.URI] = struct{}{}
			bodies, found, err := body(lib)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			for _, b := range bodies {
				frontier = append(frontier, ArtifactsConsumer{URI: lib.URI, Template: b})
			}
		}
	}
	return nil
}

// DiscoverSchemaLibraries discovers the SchemaLibrary closure reachable from a set
// of Component/Feature schema bodies via cross-document $ref: references,
// resolves each discovered library (minimum-version selection + lookup), and
// transitively descends each resolved library's own entry bodies to a fixpoint.
// The discovered + resolved libraries are merged into c.SchemaLibraries.
//
// This is the single producer of the library axis of the closure: both pull's
// pin flow and build's drift-verify flow call it with the same inputs, so they
// compute the identical library closure (the lockfile they write and the closure
// they verify against cannot diverge). The leaf $ref walker (WalkSchemaRefs /
// walkSchemaRefsInto) only RECORDS references; resolution (MVS + the injected
// Lookup) and the transitive descent live here, in the package that owns the
// walk state and the resolution machinery.
//
// Termination is by a visited-set keyed on the canonical library identity (the
// host-less name the lockfile pins), so each library's body is descended exactly
// once. A genuine library↔library cycle therefore terminates discovery without
// error; the cycle itself surfaces as ErrCircularRef at $ref expansion
// (compactform), where the recursive expander detects it — discovery does not
// duplicate that diagnostic.
func (c *Closure) DiscoverSchemaLibraries(lookup Lookup, consumers []SchemaConsumer, body LibraryBodyFunc) error {
	descended := map[string]struct{}{}

	frontier := make([]SchemaConsumer, 0, len(consumers))
	for _, sc := range consumers {
		if sc.Schema != nil {
			frontier = append(frontier, sc)
		}
	}

	for len(frontier) > 0 {
		// Re-seed a fresh walk state from the libraries discovered so far, walk
		// the current frontier (consumer schemas, then transitively library
		// bodies), and re-resolve the full set so minimum-version selection sees
		// every consumer of every library. Re-resolution is idempotent.
		state := newWalkState()
		for _, lib := range c.SchemaLibraries {
			state.schemaLibs[lib.URI] = libraryFromResolved(lib)
		}
		for _, sc := range frontier {
			if err := state.walkSchemaRefsInto(sc.Schema, sc.URI); err != nil {
				return err
			}
		}
		if err := state.resolveLibraries(state.schemaLibs, lookup.ResolveSchemaLibrary); err != nil {
			return err
		}
		c.SchemaLibraries = nil
		for _, lib := range state.materialiseSchemaLibs() {
			c.SchemaLibraries = append(c.SchemaLibraries, lib)
		}
		canonicaliseClosure(c)

		// Build the next frontier from libraries not yet descended: scan each
		// newly-resolved library's own entry bodies for further $ref:s.
		frontier = frontier[:0]
		for _, lib := range c.SchemaLibraries {
			if _, seen := descended[lib.URI]; seen {
				continue
			}
			descended[lib.URI] = struct{}{}
			bodies, found, err := body(lib)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			for _, n := range bodies {
				frontier = append(frontier, SchemaConsumer{URI: lib.URI, Schema: n})
			}
		}
	}
	return nil
}

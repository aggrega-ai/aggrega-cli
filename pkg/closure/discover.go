// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure

import "gopkg.in/yaml.v3"

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
// resolves the body from the catalog (a locally-authored or bridged library) or
// the content cache (a remote pinned library, via the ReadPinnedLibrary path).
// A false found return stops transitive descent into that library (its body is
// not available to scan); err is for a hard read/parse failure.
type LibraryBodyFunc func(lib ResolvedLibrary) (entryBodies []*yaml.Node, found bool, err error)

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
		if err := state.resolveLibraries(lookup, true /* schema */); err != nil {
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

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// LibrarySet is the name-keyed libraries one reader sees, one version per name.
type LibrarySet struct {
	// Schemas holds the SchemaLibrary entities by library name.
	Schemas map[string]types.SchemaLibrary
	// Mappings holds the MappingLibrary entities by library name.
	Mappings map[string]types.MappingLibrary
	// Workloads holds the WorkloadLibrary entities by library name.
	Workloads map[string]types.WorkloadLibrary
	// Artifacts holds the ArtifactLibrary entities by library name.
	Artifacts map[string]types.ArtifactLibrary
}

// DefaultLibraries returns, for every library name this catalog holds on each of
// the four library kinds, the one version a bare reference to that name selects
// (selectLibraryVersion). It is the reading for a surface no Release is in play
// on — a whole-catalog check, an inspection, an import projection — and is built
// afresh on each call, so a pass takes it once.
func (c *Catalog) DefaultLibraries() LibrarySet {
	return LibrarySet{
		Schemas:   c.defaultSchemaLibraries(),
		Mappings:  defaultLibrarySelection(c.MappingLibraryVersions, mappingLibraryMeta),
		Workloads: defaultLibrarySelection(c.WorkloadLibraryVersions, workloadLibraryMeta),
		Artifacts: defaultLibrarySelection(c.ArtifactLibraryVersions, artifactLibraryMeta),
	}
}

// defaultSchemaLibraries is the SchemaLibrary part of DefaultLibraries, for a
// pass that compiles schemas and reads no other library kind.
func (c *Catalog) defaultSchemaLibraries() map[string]types.SchemaLibrary {
	return defaultLibrarySelection(c.SchemaLibraryVersions, schemaLibraryMeta)
}

// SchemaLibrary returns the SchemaLibrary a reference selects among the versions
// this catalog holds: a bare name reads the highest version, a name with a
// held version reads that version, and any other version reads as a floor: the
// highest held version of its major at or above it. ok is false where the catalog
// holds the name at no version, or at none the floor admits.
func (c *Catalog) SchemaLibrary(ref string) (types.SchemaLibrary, bool) {
	lib, _, ok, _ := selectLibrary(c.SchemaLibraryVersions, ref, schemaLibraryMeta)
	return lib, ok
}

// MappingLibrary is SchemaLibrary for a MappingLibrary.
func (c *Catalog) MappingLibrary(ref string) (types.MappingLibrary, bool) {
	lib, _, ok, _ := selectLibrary(c.MappingLibraryVersions, ref, mappingLibraryMeta)
	return lib, ok
}

// WorkloadLibrary is SchemaLibrary for a WorkloadLibrary.
func (c *Catalog) WorkloadLibrary(ref string) (types.WorkloadLibrary, bool) {
	lib, _, ok, _ := selectLibrary(c.WorkloadLibraryVersions, ref, workloadLibraryMeta)
	return lib, ok
}

// ArtifactLibrary is SchemaLibrary for an ArtifactLibrary.
func (c *Catalog) ArtifactLibrary(ref string) (types.ArtifactLibrary, bool) {
	lib, _, ok, _ := selectLibrary(c.ArtifactLibraryVersions, ref, artifactLibraryMeta)
	return lib, ok
}

// selectLibrary returns the library ref names among the versions index holds,
// with the identity it is indexed under. It is the one read of a library by the
// identity a resolution recorded or a caller spells out: a reference naming a
// held version exactly (`Name:1.1.0` with 1.1.0 held) reads that version, so a
// recorded version reads back as itself however many later versions the index
// comes to hold; any other reference is selected as selectLibraryFloor selects
// it. The per-kind reads of both catalog backings and the by-reference byte read
// (Catalog.EntityBytes) read through it. A miss, and a reference no held version
// satisfies, both report ok false; err is the selection's account of the second
// (or of a floor that is no version), and nil for a plain miss.
func selectLibrary[V any](index map[string]V, ref string, meta func(V) (string, string)) (lib V, key string, ok bool, err error) {
	if key, ok := heldLibraryIdentity(index, ref, meta); ok {
		return index[key], key, true, nil
	}
	return selectLibraryFloor(index, ref, meta)
}

// selectLibraryFloor returns the library ref selects among the versions index
// holds (selectLibraryVersion), reading a version in ref as a floor: the highest
// held version every such floor admits. It is the dependency resolution's read,
// where a consumer's `@version` states what it needs and not what it holds.
func selectLibraryFloor[V any](index map[string]V, ref string, meta func(V) (string, string)) (lib V, key string, ok bool, err error) {
	key, ok, err = selectLibraryVersion(index, ref, meta)
	if !ok || err != nil {
		return lib, "", false, err
	}
	return index[key], key, true, nil
}

// heldLibraryIdentity reports the index key of the held version ref names
// exactly: the entry whose name and version are ref's base and version as
// written. Entries equal in both (differing in build metadata only are different
// versions as written) select the smaller key, so the read is deterministic.
func heldLibraryIdentity[V any](index map[string]V, ref string, meta func(V) (string, string)) (string, bool) {
	pin := types.ParseVersionPin(ref)
	version, hasVersion := pin.Pin()
	if !hasVersion || pin.IsCaret() {
		return "", false
	}
	found := ""
	for k, v := range index {
		if n, ver := meta(v); n == pin.Base() && ver == version && (found == "" || k < found) {
			found = k
		}
	}
	return found, found != ""
}

// defaultLibrarySelection derives a name-keyed map from an identity-keyed index:
// per library name, the version a bare reference selects. The index is grouped by
// name in one pass and each name selected among its own versions only, which is
// every version a bare reference to the name considers, so the index is read once
// rather than once per name.
func defaultLibrarySelection[V any](index map[string]V, meta func(V) (string, string)) map[string]V {
	byName := map[string]map[string]V{}
	for key, v := range index {
		name, _ := meta(v)
		versions := byName[name]
		if versions == nil {
			versions = map[string]V{}
			byName[name] = versions
		}
		versions[key] = v
	}
	out := make(map[string]V, len(byName))
	for name, versions := range byName {
		if key, ok, err := selectLibraryVersion(versions, name, meta); ok && err == nil {
			out[name] = versions[key]
		}
	}
	return out
}

// closureLibrarySelection derives a name-keyed map from an identity-keyed index:
// exactly the versions a Release's dependency closure resolved. A resolved
// library the index does not hold is omitted.
func closureLibrarySelection[V any](index map[string]V, libs []closure.ResolvedLibrary) map[string]V {
	out := map[string]V{}
	for _, lib := range libs {
		if v, ok := index[entityIndexKey(lib.URI, lib.ResolvedVersion)]; ok {
			out[lib.URI] = v
		}
	}
	return out
}

func schemaLibraryMeta(l types.SchemaLibrary) (string, string) {
	return l.Metadata.Name, l.Metadata.Version
}

func mappingLibraryMeta(l types.MappingLibrary) (string, string) {
	return l.Metadata.Name, l.Metadata.Version
}

func workloadLibraryMeta(l types.WorkloadLibrary) (string, string) {
	return l.Metadata.Name, l.Metadata.Version
}

func artifactLibraryMeta(l types.ArtifactLibrary) (string, string) {
	return l.Metadata.Name, l.Metadata.Version
}

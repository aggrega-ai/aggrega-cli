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

// selectLibrary returns the library ref selects among the versions index holds
// (selectLibraryVersion), with the identity it is indexed under. A miss, and a
// reference no held version satisfies, both report ok false.
func selectLibrary[V any](index map[string]V, ref string, meta func(V) (string, string)) (lib V, key string, ok bool) {
	key, ok, err := selectLibraryVersion(index, ref, meta)
	if !ok || err != nil {
		return lib, "", false
	}
	return index[key], key, true
}

// defaultLibrarySelection derives a name-keyed map from an identity-keyed index:
// per library name, the version a bare reference selects.
func defaultLibrarySelection[V any](index map[string]V, meta func(V) (string, string)) map[string]V {
	out := map[string]V{}
	for _, v := range index {
		name, _ := meta(v)
		if _, done := out[name]; done {
			continue
		}
		if key, ok, err := selectLibraryVersion(index, name, meta); ok && err == nil {
			out[name] = index[key]
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

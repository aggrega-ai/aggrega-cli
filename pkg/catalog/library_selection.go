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

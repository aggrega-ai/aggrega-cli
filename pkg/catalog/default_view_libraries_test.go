// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

// defaultViewLibraries is the version a bare reference selects for every library
// name a view holds, on each kind, for a test that inspects what a view enumerated
// with no Release in play. A Release's path reads ClosureLibraries instead.
func defaultViewLibraries(l *LazyLookup) LibrarySet {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.buildSchemaLibraries()
	l.buildRecipeIndex()
	return LibrarySet{
		Schemas:   l.defaultSchemaLibraries(),
		Mappings:  defaultLibrarySelection(l.mappingLibraryVersions, mappingLibraryMeta),
		Workloads: defaultLibrarySelection(l.workloadLibraryVersions, workloadLibraryMeta),
		Artifacts: defaultLibrarySelection(l.artifactLibraryVersions, artifactLibraryMeta),
	}
}

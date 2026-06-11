// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// discoverClosureLibraries is the single producer of the closure's SchemaLibrary
// axis, shared by pull's pin flow and build's drift-verify flow so both compute
// the identical library closure — the lockfile pull writes and the closure build
// verifies against it cannot diverge. It scans every Component and Feature schema
// in the loaded catalog for cross-document $ref:s, resolves the discovered
// libraries (minimum-version selection + the injected lookup), and descends them
// transitively to a fixpoint, merging the result into cl.SchemaLibraries.
//
// Scanning is CLOSURE-SCOPED: only the Components and Features the Release reaches
// (cl.Entities) are scanned for $ref:s, matching the closure-scoped Pass-2 gate —
// so a library referenced only by an out-of-closure entity is neither pinned nor
// resolution-fatal, and an out-of-closure entity with a dangling $ref never fails
// the build/pull. Transitive descent into a discovered library's own $ref:s is
// unscoped (those libraries are in-closure by construction). Iteration is
// sorted-name for deterministic output.
func discoverClosureLibraries(cl *closure.Closure, cat *catalog.Catalog, lookup closure.Lookup) error {
	inClosure := closureNameSet(cl)
	var consumers []closure.SchemaConsumer

	compNames := make([]string, 0, len(cat.Components))
	for name := range cat.Components {
		compNames = append(compNames, name)
	}
	sort.Strings(compNames)
	for _, name := range compNames {
		if _, ok := inClosure[name]; !ok {
			continue
		}
		comp := cat.Components[name]
		consumers = append(consumers, closure.SchemaConsumer{URI: name, Schema: &comp.Spec.Schema})
	}

	featNames := make([]string, 0, len(cat.Features))
	for name := range cat.Features {
		featNames = append(featNames, name)
	}
	sort.Strings(featNames)
	for _, name := range featNames {
		if _, ok := inClosure[name]; !ok {
			continue
		}
		feat := cat.Features[name]
		consumers = append(consumers, closure.SchemaConsumer{URI: name, Schema: &feat.Spec.Schema})
	}

	// Transitive body accessor: a discovered library's own entry bodies, for
	// descending its cross-document $ref:s. A local or bridged library is in the
	// catalog; a library present only in the cache (a remote pin not bridged into
	// THIS catalog) is not found here and its transitive descent lands with the
	// pull-side Tier-3 path.
	body := func(lib closure.ResolvedLibrary) ([]*yaml.Node, bool, error) {
		sl, ok := cat.SchemaLibraries[lib.URI]
		if !ok {
			return nil, false, nil
		}
		keys := sl.Spec.Schemas.Keys()
		bodies := make([]*yaml.Node, 0, len(keys))
		for _, k := range keys {
			entry, _ := sl.Spec.Schemas.Get(k)
			node := entry.Content
			bodies = append(bodies, &node)
		}
		return bodies, true, nil
	}

	return cl.DiscoverSchemaLibraries(lookup, consumers, body)
}

// discoverClosureMappingLibraries is the mapping-axis mirror of
// discoverClosureLibraries: the single producer of the closure's MappingLibrary
// axis, shared by pull's pin flow and build's drift-verify flow. It scans every
// Recipe and FeatureRecipe valuesMapping in the loaded catalog for a field-root
// `$import:` library reference, resolves the discovered libraries (minimum-version
// selection + the injected lookup), and descends them transitively to a fixpoint,
// merging the result into cl.MappingLibraries.
//
// A valuesMapping is presented to the walker in its authored `$import: <ref>`
// directive form; a Mode-1 inline values template and a Mode-2 local-file import
// carry no library reference and are skipped by the walker's identity gate.
// Iteration is sorted-name for deterministic output.
func discoverClosureMappingLibraries(cl *closure.Closure, cat *catalog.Catalog, lookup closure.Lookup) error {
	var consumers []closure.MappingConsumer

	inClosure := closureNameSet(cl)
	recipeNames := make([]string, 0, len(cat.Recipes))
	for name := range cat.Recipes {
		recipeNames = append(recipeNames, name)
	}
	sort.Strings(recipeNames)
	for _, name := range recipeNames {
		// Closure membership is by the entity's metadata.name (the form
		// closure entity URIs carry), not by the identity map key, which
		// additionally carries the provider segment.
		if _, ok := inClosure[cat.Recipes[name].Metadata.Name]; !ok {
			continue
		}
		if tmpl, ok := valuesMappingImportDirective(cat.Recipes[name].Spec.ValuesMapping); ok {
			consumers = append(consumers, closure.MappingConsumer{URI: name, Template: tmpl})
		}
	}

	frNames := make([]string, 0, len(cat.FeatureRecipes))
	for name := range cat.FeatureRecipes {
		frNames = append(frNames, name)
	}
	sort.Strings(frNames)
	for _, name := range frNames {
		if _, ok := inClosure[cat.FeatureRecipes[name].Metadata.Name]; !ok {
			continue
		}
		if tmpl, ok := valuesMappingImportDirective(cat.FeatureRecipes[name].Spec.ValuesMapping); ok {
			consumers = append(consumers, closure.MappingConsumer{URI: name, Template: tmpl})
		}
	}

	// Transitive body accessor: a discovered MappingLibrary's own entry bodies, for
	// descending their field-root `$import:`s. A library present only in the cache
	// (a remote pin not bridged into THIS catalog) is not found here and its
	// transitive descent lands with the pull-side Tier-3 path.
	body := func(lib closure.ResolvedLibrary) ([]string, bool, error) {
		ml, ok := cat.MappingLibraries[lib.URI]
		if !ok {
			return nil, false, nil
		}
		bodies := make([]string, 0, ml.Spec.Mappings.Len())
		for _, k := range ml.Spec.Mappings.Keys() {
			entry, _ := ml.Spec.Mappings.Get(k)
			bodies = append(bodies, entry.Body)
		}
		return bodies, true, nil
	}

	return cl.DiscoverMappingLibraries(lookup, consumers, body)
}

// closureNameSet is the set of catalog index names (version-stripped) the
// closure reaches, for filtering library discovery to closure entities.
func closureNameSet(cl *closure.Closure) map[string]struct{} {
	set := make(map[string]struct{}, len(cl.Entities))
	for _, e := range cl.Entities {
		set[catalog.EntityNameFromURI(e.URI)] = struct{}{}
	}
	return set
}

// valuesMappingImportDirective renders a valuesMapping that carries an `$import:`
// target back into its authored directive form (`$import: <target>`) for the
// discovery walker, which scans a template body for a field-root `$import:`. A
// Mode-1 inline values template carries no import and yields ok=false.
func valuesMappingImportDirective(vm types.ValuesMapping) (string, bool) {
	if vm.Import == "" {
		return "", false
	}
	return "$import: " + vm.Import, true
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// NewLazyLookup returns a resolver.CatalogLookup that lazily resolves
// Component and Feature URIs through the Tier-1 → Tier-2 → Tier-3
// cascade. Tier-1 walks the configured catalog directories once per
// session (header-only YAML parse) and builds an in-memory index
// keyed by metadata.name; subsequent Tier-1 calls hit the index in
// O(1). Tier-2 reads cached entity bytes from the workspace cache
// directory. Tier-3 pulls from the registry via the supplied
// OCIPuller and populates the cache for subsequent lookups.
//
// Extends chains are resolved recursively on demand. When a Component
// (or Feature) has a non-empty spec.extends, the lookup invokes
// validator.ResolveComponentExtends (or ResolveFeatureExtends) with
// itself as the closure, walking the chain through the same cascade.
// Cycle detection comes free via validator.ResolveInheritance's
// visited-set guard.
//
// The lookup is layout-agnostic: entities are indexed by authored
// metadata.name regardless of where the YAML lives under
// catalogDirs. Workspaces using flat layouts, generated catalog
// trees, or any other organisation work identically to the
// canonical kind-directory layout.
//
// The returned lookup is safe to reuse across many resolver +
// pipeline invocations within a session. The in-memory caches grow
// unbounded; the negative cache short-circuits repeated lookups of
// known-missing URIs.
//
// For the eager / one-shot build flow, callers should continue to
// use catalog.Load + (*Catalog).CatalogLookup, which loads every
// entity upfront. The lazy primitive is for long-running consumers
// where most catalog entities are never referenced.
func NewLazyLookup(workspaceRoot, cacheDir string, catalogDirs []string, puller OCIPuller) resolver.CatalogLookup {
	l := &lazyLookup{
		workspaceRoot:   workspaceRoot,
		cacheDir:        cacheDir,
		catalogDirs:     catalogDirs,
		puller:          puller,
		components:      map[string]types.Component{},
		features:        map[string]types.Feature{},
		componentMisses: map[string]struct{}{},
		featureMisses:   map[string]struct{}{},
		componentIndex:  map[string]string{},
		featureIndex:    map[string]string{},
	}
	return resolver.CatalogLookup{
		Component: l.Component,
		Feature:   l.Feature,
	}
}

type lazyLookup struct {
	workspaceRoot string
	cacheDir      string
	catalogDirs   []string
	puller        OCIPuller

	// Hot cache populated by successful resolutions. Holds merged
	// entities (extends chain already applied via validator
	// primitives).
	components map[string]types.Component
	features   map[string]types.Feature

	// Negative cache short-circuits repeated lookups of known-missing
	// URIs within the session.
	componentMisses map[string]struct{}
	featureMisses   map[string]struct{}

	// Per-kind Tier-1 indexes built on first access. Maps
	// metadata.name → filesystem path. Built once per session via
	// the catalog-tree walker; reused thereafter. No in-session
	// invalidation.
	componentIndex      map[string]string
	componentIndexBuilt bool
	featureIndex        map[string]string
	featureIndexBuilt   bool
}

// fileHeader is the partial unmarshal struct used by the catalog-tree
// walker to index entities by authored metadata.name without parsing
// the full entity body.
type fileHeader struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	} `yaml:"metadata"`
}

// Component resolves the URI through the Tier-1 → Tier-2 → Tier-3
// cascade and applies extends resolution via the validator primitive.
// Returns the merged Component on hit, (zero-value, false) on
// cascade exhaustion.
//
// Extends-resolution errors (cycle, missing parent, malformed chain)
// are emitted as one-line warnings to os.Stderr per the existing
// mid-pipeline warning convention. The lookup does NOT halt on
// extends errors — it caches and returns the unmerged entity so
// downstream consumers can proceed; the operator sees the warning
// in the daemon's stderr stream. This mirrors how dispatchBuild
// surfaces catalog deprecation + binding-lint warnings.
func (l *lazyLookup) Component(uri string) (types.Component, bool) {
	if c, ok := l.components[uri]; ok {
		return c, true
	}
	if _, ok := l.componentMisses[uri]; ok {
		return types.Component{}, false
	}
	comp, ok := l.resolveComponent(uri)
	if !ok {
		l.componentMisses[uri] = struct{}{}
		return types.Component{}, false
	}
	merged, vErrs := validator.ResolveComponentExtends(comp, l.Component)
	if len(vErrs) > 0 {
		fmt.Fprintf(os.Stderr, "  extends: Component %s: %s\n", uri, vErrs[0].Error())
	}
	l.components[uri] = merged
	return merged, true
}

// Feature mirrors Component for the Feature surface. Extends-resolution
// errors surface to os.Stderr per the same convention.
func (l *lazyLookup) Feature(uri string) (types.Feature, bool) {
	if f, ok := l.features[uri]; ok {
		return f, true
	}
	if _, ok := l.featureMisses[uri]; ok {
		return types.Feature{}, false
	}
	feat, ok := l.resolveFeature(uri)
	if !ok {
		l.featureMisses[uri] = struct{}{}
		return types.Feature{}, false
	}
	merged, vErrs := validator.ResolveFeatureExtends(feat, l.Feature)
	if len(vErrs) > 0 {
		fmt.Fprintf(os.Stderr, "  extends: Feature %s: %s\n", uri, vErrs[0].Error())
	}
	l.features[uri] = merged
	return merged, true
}

// resolveComponent walks the Tier-1/2/3 cascade and unmarshals the
// resolved bytes into a Component. Returns the unmerged entity;
// extends resolution is the caller's responsibility.
//
// Tier-1's walker already filters on kind=="Component" at index
// build time (only Component YAMLs land in componentIndex). Tier-2
// and Tier-3 do NOT pre-filter — the cache slot or registry artifact
// could in principle carry bytes of a different kind. Both unmarshal
// paths therefore include a kind check: a parsed entity whose
// Kind != "Component" is rejected, the cascade falls through to the
// next tier, and the lookup ultimately reports miss if no tier
// produces a Component-kind result. This matches catalog.Load's
// kind-routing semantic (indexFile dispatches by hdr.Kind).
func (l *lazyLookup) resolveComponent(uri string) (types.Component, bool) {
	if comp, ok := l.tier1ReadComponent(uri); ok {
		return comp, true
	}
	if bytes, _, ok := tier2ReadBytes(l.workspaceRoot, l.cacheDir, uri); ok {
		var comp types.Component
		if err := yaml.Unmarshal(bytes, &comp); err == nil && comp.Kind == "Component" {
			return comp, true
		}
	}
	bytes, _, err := tier3PullCatalogEntity(l.workspaceRoot, l.cacheDir, l.puller, uri)
	if err == nil && bytes != nil {
		var comp types.Component
		if uerr := yaml.Unmarshal(bytes, &comp); uerr == nil && comp.Kind == "Component" {
			return comp, true
		}
	}
	return types.Component{}, false
}

// resolveFeature mirrors resolveComponent for the Feature surface.
// The same kind-check guards Tier-2 + Tier-3 unmarshal paths against
// non-Feature bytes arriving from the cache or registry.
func (l *lazyLookup) resolveFeature(uri string) (types.Feature, bool) {
	if feat, ok := l.tier1ReadFeature(uri); ok {
		return feat, true
	}
	if bytes, _, ok := tier2ReadBytes(l.workspaceRoot, l.cacheDir, uri); ok {
		var feat types.Feature
		if err := yaml.Unmarshal(bytes, &feat); err == nil && feat.Kind == "Feature" {
			return feat, true
		}
	}
	bytes, _, err := tier3PullCatalogEntity(l.workspaceRoot, l.cacheDir, l.puller, uri)
	if err == nil && bytes != nil {
		var feat types.Feature
		if uerr := yaml.Unmarshal(bytes, &feat); uerr == nil && feat.Kind == "Feature" {
			return feat, true
		}
	}
	return types.Feature{}, false
}

// tier1ReadComponent looks up the URI in the (lazily-built) component
// index and reads + full-unmarshals the indexed file. Returns the
// unmerged Component on hit.
func (l *lazyLookup) tier1ReadComponent(uri string) (types.Component, bool) {
	l.buildComponentIndex()
	lookupName := lookupNameFromURI(uri)
	path, ok := l.componentIndex[lookupName]
	if !ok {
		return types.Component{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return types.Component{}, false
	}
	var comp types.Component
	if err := yaml.Unmarshal(data, &comp); err != nil {
		return types.Component{}, false
	}
	return comp, true
}

// tier1ReadFeature mirrors tier1ReadComponent for the Feature surface.
func (l *lazyLookup) tier1ReadFeature(uri string) (types.Feature, bool) {
	l.buildFeatureIndex()
	lookupName := lookupNameFromURI(uri)
	path, ok := l.featureIndex[lookupName]
	if !ok {
		return types.Feature{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return types.Feature{}, false
	}
	var feat types.Feature
	if err := yaml.Unmarshal(data, &feat); err != nil {
		return types.Feature{}, false
	}
	return feat, true
}

// buildComponentIndex walks the catalog directories once (header-only
// YAML parse) and populates l.componentIndex with metadata.name →
// filesystem path mappings for every Component-kind YAML found.
// Idempotent: a second call is a no-op once componentIndexBuilt is
// true.
func (l *lazyLookup) buildComponentIndex() {
	if l.componentIndexBuilt {
		return
	}
	l.componentIndexBuilt = true
	l.walkAndIndex("Component", l.componentIndex)
}

// buildFeatureIndex mirrors buildComponentIndex for the Feature surface.
func (l *lazyLookup) buildFeatureIndex() {
	if l.featureIndexBuilt {
		return
	}
	l.featureIndexBuilt = true
	l.walkAndIndex("Feature", l.featureIndex)
}

// walkAndIndex walks every catalog directory recursively, header-
// unmarshals each *.yaml / *.yml file, and records files whose kind
// matches the target kind into the supplied index. Files whose kind
// does not match are skipped without indexing.
//
// Walk errors and per-file parse failures are silently ignored —
// the lazy lookup's contract is "find what you can"; broken files
// surface at full-unmarshal time when a specific URI is requested,
// not during the index walk.
func (l *lazyLookup) walkAndIndex(targetKind string, index map[string]string) {
	for _, dir := range l.catalogDirs {
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			ext := filepath.Ext(path)
			if ext != ".yaml" && ext != ".yml" {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			var hdr fileHeader
			if uerr := yaml.Unmarshal(data, &hdr); uerr != nil {
				return nil
			}
			if hdr.Kind != targetKind {
				return nil
			}
			if hdr.Metadata.Name == "" {
				return nil
			}
			index[hdr.Metadata.Name] = path
			return nil
		})
	}
}

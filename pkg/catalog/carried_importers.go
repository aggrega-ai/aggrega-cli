// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"maps"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// carriedImporter is one importer an import's catalog view serves from what the
// binary carries rather than from a catalog directory: its generated Plugin
// entity and the content digest of the one module it accepts.
type carriedImporter struct {
	plugin types.Plugin
	digest string
}

// UseCarriedImporterResolution makes this view serve, beside the importers its
// catalog directories hold, every importer the binary carries the entity of
// without embedding its module — the compiler's own core importers, published
// with each compiler release. A workspace whose catalog holds no importer for a
// format one of them advertises can then resolve it, and recognise a source by
// the signatures its entity records.
//
// They compete under the rules every importer resolves by: within one name the
// highest version is chosen, or the version an attachment names; two importers of
// different names advertising one format halt as ambiguous unless an attachment
// names one of them. Where the catalog itself holds an importer at the same
// identity and version as a carried one, the catalog's entity keeps its place and
// the carried one is not served, because the catalog's entity is the one whose
// module sits beside it.
//
// It is the import's view alone that calls this. A build, a pull and a lookup of
// a plugin by its reference never see these entities, and nothing records them in
// a lockfile: an import pins nothing, and the carried entities serve only the
// choice of which importer runs. The binary's carried set is read when this is
// called, so a test that seeds that set first is served the set it seeded.
//
// An entity that does not decode, or that is not an importer's, is left out: it
// is a defect of the build that generated it, and the importer it names then
// resolves as one the binary does not carry.
func (c *Catalog) UseCarriedImporterResolution() {
	carried := map[string]carriedImporter{}
	for _, p := range embedded.PublishedPlugins() {
		var entity types.Plugin
		if err := yaml.Unmarshal(p.Entity, &entity); err != nil {
			continue
		}
		if entity.Metadata.PluginType != importerPluginType {
			continue
		}
		carried[entityIndexKey(entity.Coordinate(), entity.Metadata.Version)] = carriedImporter{plugin: entity, digest: p.Digest}
	}
	c.carriedImporters = carried
}

// CarriedImporterDigest reports the content digest of the one module p may run
// from, when p is an importer this view serves from what the binary carries, and
// false for every other entity — every importer and parser the view's catalog
// directories hold included.
//
// It is how the import's loader tells a carried importer apart from one of the
// catalog's own. A carried importer has no module beside an entity in a catalog
// directory, and must never be read from beside a catalog entity of the same
// identity at another version, because that module belongs to the other entity.
// Its bytes are held to the digest returned here instead.
//
// p is matched by identity and version. A catalog entity of the same identity and
// version as a carried one keeps its place, so for it this reports false, and its
// module is read from beside it as any catalog entity's is.
func (c *Catalog) CarriedImporterDigest(p types.Plugin) (string, bool) {
	key := entityIndexKey(p.Coordinate(), p.Metadata.Version)
	ci, ok := c.carriedImporters[key]
	if !ok {
		return "", false
	}
	if _, held := c.providerResolutionPlugins()[key]; held {
		return "", false
	}
	return ci.digest, true
}

// importerResolutionPlugins returns the plugin set importer resolution reads:
// the set providerResolutionPlugins returns, joined, where
// UseCarriedImporterResolution enabled it, by the importers the binary carries.
// A carried importer whose identity and version that set already holds is left
// out, so the catalog's own entity keeps its place; one at another version of an
// identity the set holds sits beside it, for the version select to choose between.
//
// The returned map is keyed exactly like Catalog.Plugins and is a fresh map
// whenever a carried importer joins it, so the catalog's own index is never
// written through it.
func (c *Catalog) importerResolutionPlugins() map[string]types.Plugin {
	set := c.providerResolutionPlugins()
	if len(c.carriedImporters) == 0 {
		return set
	}
	out := make(map[string]types.Plugin, len(set)+len(c.carriedImporters))
	maps.Copy(out, set)
	for key, ci := range c.carriedImporters {
		if _, held := set[key]; held {
			continue
		}
		out[key] = ci.plugin
	}
	return out
}

// resolutionPluginsFor returns the plugin set the shared capability select
// resolves a plugin of pluginType from: importerResolutionPlugins for an
// importer, and providerResolutionPlugins for every other type, so a carried
// importer is a candidate for importer resolution and for nothing else.
func (c *Catalog) resolutionPluginsFor(pluginType string) map[string]types.Plugin {
	if pluginType == importerPluginType {
		return c.importerResolutionPlugins()
	}
	return c.providerResolutionPlugins()
}

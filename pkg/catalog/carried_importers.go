// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"maps"
	"slices"

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
// identity and version as a carried one, with its module beside it, the catalog's
// entity keeps its place and the carried one is not served, because the
// catalog's entity is the one whose module sits beside it.
//
// A catalog entity of a carried importer's identity whose module does not sit
// beside it in its catalog folder is one no import could run: a catalog entity's
// module is read from beside it, and a carried importer's module never is. Such
// an entity — a checkout of a catalog that records the importers a release
// published, say — is no candidate for the choice of which importer runs, though
// recognition still reads the signatures it records (importerRecognitionPlugins).
// At the carried importer's own version the carried one is served in its place;
// at another version it is left out, save in the two cases
// importerResolutionPlugins sets out — an attachment naming exactly that
// version, or a requested format no importer of its identity that can run
// advertises — where the import stops naming where its module goes rather than
// running another importer in its place. Whether a module sits beside each such
// entity is read here, once.
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
	identities := map[string]struct{}{}
	for _, p := range embedded.PublishedPlugins() {
		var entity types.Plugin
		if err := yaml.Unmarshal(p.Entity, &entity); err != nil {
			continue
		}
		if entity.Metadata.PluginType != importerPluginType {
			continue
		}
		carried[entityIndexKey(entity.Coordinate(), entity.Metadata.Version)] = carriedImporter{plugin: entity, digest: p.Digest}
		identities[entity.Coordinate()] = struct{}{}
	}
	withoutModule := map[string]struct{}{}
	for key, p := range c.Plugins {
		if p.Metadata.PluginType != importerPluginType {
			continue
		}
		if _, carries := identities[p.Coordinate()]; !carries {
			continue
		}
		if !c.moduleBesideEntity(key, p) {
			withoutModule[key] = struct{}{}
		}
	}
	c.carriedImporters = carried
	c.importersWithoutModule = withoutModule
}

// moduleBesideEntity reports whether the module of p, the catalog's own Plugin
// entity indexed under key, sits beside it, judged as the co-located tier judges
// it (pluginpath.ColocatedDir.HoldsModule): a module there, or something there
// the tier would refuse rather than miss — a link leading out of the catalog
// folder's root — so the load that reads it refuses it rather than another
// module running in its place. An entity with no catalog folder of its own has
// nothing beside it.
func (c *Catalog) moduleBesideEntity(key string, p types.Plugin) bool {
	at, sourced := c.pluginColocatedDir(key, p)
	return sourced && at.HoldsModule(p.ArtifactReference())
}

// pinsExactly reports whether pinned, the version an attachment pins as written,
// names exactly p's version, as the version select judges an exact pin
// (pinNamesExactly), the pin read through the pin grammar's own split
// (types.ParseVersionPin). Empty, or a range, names no single version.
func pinsExactly(pinned string, p types.Plugin) bool {
	if pinned == "" {
		return false
	}
	return pinNamesExactly(types.ParseVersionPin(p.Metadata.Name+":"+pinned), p.Metadata.Version)
}

// catalogImporterRuns reports whether set, a resolution set, holds under key an
// importer of the catalog's own that runs from beside its entity — one it holds
// whose module sits beside it, or one of an identity the binary carries no
// importer of.
func (c *Catalog) catalogImporterRuns(set map[string]types.Plugin, key string) bool {
	if _, held := set[key]; !held {
		return false
	}
	_, idle := c.importersWithoutModule[key]
	return !idle
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
// version as a carried one, with its module beside it, keeps its place, so for it
// this reports false, and its module is read from beside it as any catalog
// entity's is. Where no module sits beside that entity, the carried importer is
// the one served at that identity and version, and this reports its digest.
func (c *Catalog) CarriedImporterDigest(p types.Plugin) (string, bool) {
	key := entityIndexKey(p.Coordinate(), p.Metadata.Version)
	ci, ok := c.carriedImporters[key]
	if !ok {
		return "", false
	}
	if c.catalogImporterRuns(c.providerResolutionPlugins(), key) {
		return "", false
	}
	return ci.digest, true
}

// importerRecognitionPlugins returns the plugin set an import recognising a
// source's format reads the signatures of: the set providerResolutionPlugins
// returns — every importer entity the catalog holds, every version, whether a
// module sits beside it or not — joined, where UseCarriedImporterResolution
// enabled it, by the importers the binary carries whose identity and version the
// catalog does not hold.
//
// Recognition says which format a source is, and an entity states the formats it
// advertises and the signatures it recognises them by whether or not its module
// sits beside it, so recognition reads it. Which importer then runs that format
// is the selection's question, which importerResolutionPlugins answers.
//
// The returned map is keyed exactly like Catalog.Plugins and is a fresh map
// wherever the view serves carried importers, so the catalog's own index is never
// written through it.
func (c *Catalog) importerRecognitionPlugins() map[string]types.Plugin {
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

// importerResolutionPlugins returns the plugin set importer selection reads:
// the set providerResolutionPlugins returns, joined, where
// UseCarriedImporterResolution enabled it, by the importers the binary carries.
//
// A carried importer whose identity and version that set holds with its module
// beside it is left out, so the catalog's own entity keeps its place; where no
// module sits beside the set's entity, the carried importer takes its key. One at
// another version of an identity the set holds sits beside it, for the version
// select to choose between.
//
// A catalog entity of a carried importer's identity at another version with no
// module beside it is left out too — no import could run it — unless one of two
// things holds, and then it stays, so that an import choosing it stops naming
// where its module goes rather than running another importer in its place:
//   - pinned names exactly its version (pinsExactly): an attachment naming that
//     version selects that entity;
//   - capability, the format requested, is one it advertises and no importer of
//     its identity that can run advertises: passing it over would leave the
//     format with no importer of that identity at all.
//
// pinned is the version an attachment pins, as written; empty, or a range, pins no
// entity. capability is the import:<format> capability the selection is for, and
// empty where the set is read for no one format.
//
// The returned map is keyed exactly like Catalog.Plugins and is a fresh map
// wherever the view serves carried importers, so the catalog's own index is never
// written through it.
func (c *Catalog) importerResolutionPlugins(capability, pinned string) map[string]types.Plugin {
	set := c.providerResolutionPlugins()
	if len(c.carriedImporters) == 0 {
		return set
	}
	served := c.runnableAdvertisers(set, capability)
	out := make(map[string]types.Plugin, len(set)+len(c.carriedImporters))
	for key, p := range set {
		if _, idle := c.importersWithoutModule[key]; idle {
			if _, carriedHere := c.carriedImporters[key]; carriedHere {
				continue
			}
			_, runnable := served[p.Coordinate()]
			advertises := capability != "" && slices.Contains(p.DeclaredCapabilities(), capability)
			if !pinsExactly(pinned, p) && (runnable || !advertises) {
				continue
			}
		}
		out[key] = p
	}
	for key, ci := range c.carriedImporters {
		if c.catalogImporterRuns(set, key) {
			continue
		}
		out[key] = ci.plugin
	}
	return out
}

// runnableAdvertisers returns the identities of the importers advertising
// capability that an import could run: the entities of set no module is missing
// beside, and the carried importers the selection serves — every one but one
// whose key the catalog holds with its module beside it (catalogImporterRuns),
// where the catalog's entity, counted with set's, is the one that runs. Empty
// capability advertises nothing.
func (c *Catalog) runnableAdvertisers(set map[string]types.Plugin, capability string) map[string]struct{} {
	served := map[string]struct{}{}
	if capability == "" {
		return served
	}
	for key, p := range set {
		if _, idle := c.importersWithoutModule[key]; idle {
			continue
		}
		if p.Metadata.PluginType == importerPluginType && slices.Contains(p.DeclaredCapabilities(), capability) {
			served[p.Coordinate()] = struct{}{}
		}
	}
	for key, ci := range c.carriedImporters {
		if c.catalogImporterRuns(set, key) {
			continue
		}
		if slices.Contains(ci.plugin.DeclaredCapabilities(), capability) {
			served[ci.plugin.Coordinate()] = struct{}{}
		}
	}
	return served
}

// resolutionPluginsFor returns the plugin set the shared capability select
// resolves a plugin of pluginType serving capability from:
// importerResolutionPlugins for an importer, handed capability and pinned, and
// providerResolutionPlugins for every other type, so a carried importer is a
// candidate for importer resolution and for nothing else.
func (c *Catalog) resolutionPluginsFor(pluginType, capability, pinned string) map[string]types.Plugin {
	if pluginType == importerPluginType {
		return c.importerResolutionPlugins(capability, pinned)
	}
	return c.providerResolutionPlugins()
}

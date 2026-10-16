// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Seam is the resolver-and-loader pair for an import run, plus the parser pair
// the importer draws on to obtain its parsed source tree, the view of the
// catalog's Components the importer is handed, and the importer entities whose
// recognition signatures settle a format the import does not name.
//
// They travel together because they are built from one catalog view: parser
// and importer resolution share it, so one loader closure serves both sockets,
// the Components the importer is shown are the ones held by the catalog its
// importer was selected from, and the importers a source is recognised by are
// the ones that resolution selects among. The recognition parses the source
// through the same parser pair the importer draws on.
//
// Catalog is nil when that catalog holds no Component, and a harness standing
// in for the catalog leaves it nil unless it supplies a view of its own.
// Importers is empty when neither the catalog nor the binary holds an importer,
// and a harness standing in for the catalog supplies the entities whose
// signatures it wants read, so one that supplies none recognises no source.
type Seam struct {
	ImporterRes  ImporterResolver
	ImporterLoad importersocket.ImporterLoader
	ParserRes    parsersocket.ParserResolver
	ParserLoad   parsersocket.ParserLoader
	Catalog      *abi.CatalogProjection
	Importers    []types.Plugin
}

// ImporterResolver resolves a requested source format to the importer that
// serves it, given the uri of each of the workspace's plugins.importer
// attachments in list order, and reports the position among them of the
// attachment that attaches the importer it resolved, or -1 when none does.
//
// The catalog is the implementation an import runs through, and its
// ImporterPluginForFormat is where an attachment selects an importer and where
// the attachment whose with-config that importer is handed is named, in one
// answer. A harness standing in for the catalog answers both halves itself.
type ImporterResolver interface {
	ImporterPluginForFormat(format string, attached []string) (types.Plugin, int, error)
}

// BuildSeam is how [Import] obtains its resolvers and loaders.
//
// It is a package-level variable rather than a direct call so a harness can
// substitute a resolver that does not need a catalog on disk. Resolution here
// loads the workspace's whole catalog to select an importer by the format it
// advertises, which a sandbox with no catalog cannot do — the same reason the
// plugin operations carry a pointable registry target.
//
// 🔴 It is read at call time, inside [Import], and no second spelling of it
// exists anywhere. A convenience alias initialised from it would copy the
// function value at package initialisation, so pointing this variable would not
// reach the alias — a seam that cannot be pointed, which fails silently rather
// than loudly. Do not add one.
var BuildSeam = buildSeam

// buildSeam builds the real resolver and loader for an import run.
//
// The resolver is the catalog view loaded here. An importer is chosen by the
// format it advertises (import:<format>) among the catalog's importer plugins,
// and where a plugins.importer attachment names one of the importers advertising
// the requested format, that one is chosen, at the version the attachment's uri
// names — so a workspace whose catalog holds several importers for one format
// says which of them runs. The resolved plugin's bytes come from the
// shared plugin cascade the caller supplied, which is the same byte-resolution
// path every workspace-level socket plugin uses, handed the directories beside
// the Plugin entities of the catalog view loaded here — the view that selected
// the importer. So an importer authored in a catalog directory, with its built
// module laid down beside its entity, is read from there as a build reads a
// plugin from beside its entity; the cascade's other tiers — a module embedded
// in the binary, an explicit local path, the content cache — answer as they do
// for every other plugin. Those directories are computed once per seam, from
// this one catalog view, so the place a module sits has a single answer.
//
// The view also serves the compiler's own core importers, which the binary
// carries the entities of without carrying their modules. They compete with the
// catalog's importers under the same rules: the highest version within one name,
// an ambiguity halt where importers of two names advertise one format and no
// attachment names one, and an attachment's choice where it names one. An
// importer the catalog holds at the same identity and version keeps its place.
// A core importer's recorded signatures recognise a source as the catalog's
// importers' do, so a source one of them matches is recognised with no format
// named. The view does not serve the binary's parsers: a parser is selected from
// the catalog's own entities alone.
//
// A core importer's module is never read from beside a catalog entity, since a
// module laid there belongs to that entity even where it shares the core
// importer's identity at another version, so its resolution is handed no
// directories. Whichever of the cascade's other tiers answers, the bytes are held
// to the digest the binary carries for it, and the load is cross-checked against
// that importer's own entity rather than against an entity of the same identity
// the plugin resolution may find in the workspace.
//
// A plugins.importer attachment's function field is currently inert: an importer
// has a single import export, so there is no alternate entry to dispatch to. The
// field is treated like the provider socket's — carried on the shared attachment
// shape but not wired to anything — until an importer surface with more than one
// entry exists.
//
// The parser is selected by the same format capability from the same catalog
// view, and its bytes come from the same cascade handed the same directories, so
// a parser plugin loads exactly like any other — beside its entity included.
//
// The importer's view of the catalog's Components is projected from this same
// view, once, so the import loads the catalog once and shows the importer
// exactly the catalog it was selected from — nothing a Release contributes, and
// nothing fetched.
//
// The importers whose signatures recognise a source are read from this same
// view too — every importer it resolves among, the binary's own core importers
// included — so a source is recognised only as a format an importer this view
// holds advertises.
func buildSeam(rw *workspaceconfig.ResolvedWorkspace, resolvePlugin PluginBytesResolver) (Seam, error) {
	wsCfg := rw.Config()
	// An import concerns no one Release, so it reads the workspace's own stack.
	dirs := rw.ResolveCatalogDirs("", nil)
	cat, err := catalog.LoadAllWithNamedDirs(rw.Root(), wsCfg.ResolvedArtifactCacheDir(), rw.NamedCatalogDirs("", nil), dirs...)
	if err != nil {
		return Seam{}, fmt.Errorf("load catalog for import resolution: %w", err)
	}
	cat.UseEmbeddedProviderResolution()
	cat.UseCarriedImporterResolution()
	colocated := cat.PluginColocatedDirs()

	load := func(p types.Plugin) (wasm.PluginSpec, error) {
		if accepted, carried := cat.CarriedImporterDigest(p); carried {
			return loadCarriedImporter(rw, resolvePlugin, p, accepted)
		}
		resolved, rerr := resolvePlugin(rw, p.ArtifactReference(), colocated)
		if rerr != nil {
			return wasm.PluginSpec{}, rerr
		}
		return resolver.PluginSpec(p.Coordinate(), resolved), nil
	}
	return Seam{
		ImporterRes:  cat,
		ImporterLoad: load,
		ParserRes:    cat,
		ParserLoad:   load,
		Catalog:      importersocket.ProjectCatalog(cat),
		Importers:    cat.ImporterPlugins(),
	}, nil
}

// carriedModuleIs is what the refusal of a core importer's module names the
// digest it was held to as.
const carriedModuleIs = "the digest this compiler release publishes"

// loadCarriedImporter reads the module of p, a core importer the import's view
// serves from what the binary carries, and holds it to accepted, the digest of
// the one module the binary accepts for it.
//
// The resolution is handed no directories beside catalog entities: p has no
// entity in a catalog directory, and a module laid beside a catalog entity of
// p's identity belongs to that entity at whatever version it is. The cascade's
// other tiers answer as they do for any plugin, and a module none of them holds
// is the cascade's own refusal, naming the plugin.
//
// The claim the load gate cross-checks the module against, and the limits it is
// run under, are p's own. The plugin resolution a surface supplies looks an
// entity up in the workspace by p's identity alone, which may find a catalog
// entity of that identity at another version, and neither of its statements is
// a statement about this module.
func loadCarriedImporter(rw *workspaceconfig.ResolvedWorkspace, resolvePlugin PluginBytesResolver, p types.Plugin,
	accepted string) (wasm.PluginSpec, error) {
	resolved, err := resolvePlugin(rw, p.ArtifactReference(), nil)
	if err != nil {
		return wasm.PluginSpec{}, err
	}
	if err := pluginpath.HoldToDigest(resolved, accepted, carriedModuleIs); err != nil {
		return wasm.PluginSpec{}, err
	}
	resolved.Contract = resolver.PluginContractClaim(p)
	resolved.Limits = resolver.PluginRuntimeLimits(p)
	resolved.Commands = resolver.PluginCommandsMirror(p)
	return resolver.PluginSpec(p.Coordinate(), resolved), nil
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"context"
	"fmt"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
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
// through the same parser pair the importer draws on, so a syntax the catalog
// serves no parser for, and the binary does, is parsed by the binary's parser
// for both.
//
// Catalog is nil when that catalog holds no Component, and a harness standing
// in for the catalog leaves it nil unless it supplies a view of its own.
// Importers is empty when neither the catalog nor the binary holds an importer,
// and a harness standing in for the catalog supplies the entities whose
// signatures it wants read, so one that supplies none recognises no source.
//
// ImporterLoad and ParserLoad are where a harness standing in for the seam
// supplies its loaders. The real seam leaves both nil and loads through its one
// loader, bound to the context of the import that runs it.
//
// Recipes is the same catalog view's Recipes, which an import given a target
// drafts its Manifest from. A nil Recipes is a catalog holding no Recipe, so a
// harness standing in for the catalog that supplies none refuses every target.
type Seam struct {
	ImporterRes  ImporterResolver
	ImporterLoad importersocket.ImporterLoader
	ParserRes    parsersocket.ParserResolver
	ParserLoad   parsersocket.ParserLoader
	Catalog      *abi.CatalogProjection
	Importers    []types.Plugin
	Recipes      RecipeIndex

	// loadIn is the one loader of the real seam, serving both sockets, bound to
	// the context of the import that runs it, so the one step of an import that
	// can reach a registry — acquiring a core importer the machine does not hold —
	// ends with the import that asked for it. A harness standing in for the seam
	// leaves it nil, and its ImporterLoad and ParserLoad are what run.
	loadIn func(ctx context.Context) func(types.Plugin) (wasm.PluginSpec, error)
}

// importerLoader returns the loader an import under ctx runs its importer
// through: the real seam's, bound to ctx, or a harness's ImporterLoad.
func (s Seam) importerLoader(ctx context.Context) importersocket.ImporterLoader {
	if s.loadIn == nil {
		return s.ImporterLoad
	}
	return s.loadIn(ctx)
}

// parserLoader returns the loader an import under ctx runs a parser through:
// the real seam's, bound to ctx, or a harness's ParserLoad.
func (s Seam) parserLoader(ctx context.Context) parsersocket.ParserLoader {
	if s.loadIn == nil {
		return s.ParserLoad
	}
	return s.loadIn(ctx)
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

// RecipeIndex is what an import given a target reads of the catalog's Recipes:
// the capabilities they bind to, and the Recipes implementing a component type.
//
// The catalog is the implementation an import runs through, and its answers are
// the catalog's own suggestion enumeration — the one the no-mapping diagnostic
// lists its pin candidates from, which resolves a type naming no version to the
// version a build resolves it to before matching — so a draft Manifest names
// exactly the Recipes a build halting on an unmapped instance would offer. A
// harness standing in for the catalog answers both itself.
type RecipeIndex interface {
	RecipeCapabilities() []string
	RecipesImplementing(componentType string) []types.Recipe
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
// named.
//
// A core importer's module is never read from beside a catalog entity, since a
// module laid there belongs to that entity even where it shares the core
// importer's identity at another version, so its resolution is handed no
// directories. It is read from the binary's own tier or the workspace's content
// cache, held to the digest the binary carries for it; where neither holds it,
// the import fetches it from the workspace's default registry, verifies it as
// every fetched plugin is verified, and writes it to the content cache, so every
// later import reads it from there and reaches no registry. The load is
// cross-checked against that importer's own entity rather than against an entity
// of the same identity the plugin resolution may find in the workspace.
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
// Where no parser in that view advertises a syntax, the parser the binary
// carries for it serves instead (importParsers), its module read from the
// binary's own tier of that cascade; where one does, it is the one used, and the
// binary's parsers play no part. The binary carries its parsers whatever the
// workspace holds, so nothing is fetched for one, and a workspace whose catalog
// holds no parser at all still recognises a source and imports it.
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
//
// So are the Recipes an import given a target drafts its Manifest from: the
// Recipes of the catalog directories this view loaded, and nothing a registry
// holds that the workspace has not pulled.
func buildSeam(rw *workspaceconfig.ResolvedWorkspace, resolvePlugin PluginBytesResolver) (Seam, error) {
	wsCfg := rw.Config()
	// An import concerns no one Release, so it reads the workspace's own stack.
	dirs, err := rw.ResolveCatalogDirs("", nil)
	if err != nil {
		return Seam{}, err
	}
	named, err := rw.NamedCatalogDirs("", nil)
	if err != nil {
		return Seam{}, err
	}
	cat, err := catalog.LoadAllWithNamedDirs(rw.Root(), wsCfg.ResolvedArtifactCacheDir(), named, dirs...)
	if err != nil {
		return Seam{}, fmt.Errorf("load catalog for import resolution: %w", err)
	}
	cat.UseEmbeddedProviderResolution()
	cat.UseCarriedImporterResolution()
	colocated := cat.PluginColocatedDirs()

	// One loader serves both sockets, and each import binds it to its own context
	// at the call that loads a plugin. A parser is never a core importer, so
	// loading one reaches no registry; a core importer the machine does not hold
	// is fetched under that context, so the fetch ends with the import.
	loadIn := func(ctx context.Context) func(types.Plugin) (wasm.PluginSpec, error) {
		return func(p types.Plugin) (wasm.PluginSpec, error) {
			if accepted, carried := cat.CarriedImporterDigest(p); carried {
				return loadCarriedImporter(ctx, rw, p, accepted)
			}
			resolved, rerr := resolvePlugin(rw, p.ArtifactReference(), colocated)
			if rerr != nil {
				return wasm.PluginSpec{}, rerr
			}
			return resolver.PluginSpec(p.Coordinate(), resolved), nil
		}
	}
	return Seam{
		ImporterRes: cat,
		ParserRes:   importParsers{workspace: cat, binary: binaryParsers()},
		Catalog:     importersocket.ProjectCatalog(cat),
		Importers:   cat.ImporterPlugins(),
		Recipes:     cat,
		loadIn:      loadIn,
	}, nil
}

// loadCarriedImporter reads the module of p, a core importer the import's view
// serves from what the binary carries, held to accepted, the digest of the one
// module the binary accepts for it — acquiring it under ctx where the machine
// does not hold it (resolveCarriedImporter).
//
// The claim the load gate cross-checks the module against, and the limits it is
// run under, are p's own: the module is the one the binary names for p, so
// nothing the workspace holds of p's identity — a catalog entity of it at
// another version, say — makes a statement about it.
func loadCarriedImporter(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, p types.Plugin,
	accepted string) (wasm.PluginSpec, error) {
	resolved, err := resolveCarriedImporter(ctx, rw, p, accepted)
	if err != nil {
		return wasm.PluginSpec{}, err
	}
	resolved.Contract = resolver.PluginContractClaim(p)
	resolved.Limits = resolver.PluginRuntimeLimits(p)
	resolved.Commands = resolver.PluginCommandsMirror(p)
	return resolver.PluginSpec(p.Coordinate(), resolved), nil
}

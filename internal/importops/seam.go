// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Seam is the resolver-and-loader pair for an import run, plus the parser pair
// the importer draws on to obtain its parsed source tree.
//
// The four travel together because they are built from one catalog view: parser
// and importer resolution share it and its embedded-resolution posture, so one
// loader closure serves both sockets and no separate enable is needed for the
// parser side.
type Seam struct {
	ImporterRes  importersocket.ImporterResolver
	ImporterLoad importersocket.ImporterLoader
	ParserRes    parsersocket.ParserResolver
	ParserLoad   parsersocket.ParserLoader
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
// Resolution is capability-select: the importer is chosen by the format it
// advertises (import:<format>) among the catalog's importer plugins, so a
// workspace attaching more than one importer resolves the right one per format
// rather than pinning a single one. The resolved plugin's bytes come from the
// shared plugin cascade the caller supplied, which is the same byte-resolution
// path every workspace-level socket plugin uses. The plugins.importer attachment
// surface still carries each importer's with-config, threaded consumer-scoped by
// [AttachmentInputs]; the catalog decides which importer runs.
//
// A plugins.importer attachment's function field is currently inert: an importer
// has a single import export, so there is no alternate entry to dispatch to. The
// field is treated like the provider socket's — carried on the shared attachment
// shape but not wired to anything — until an importer surface with more than one
// entry exists.
//
// The parser is selected by the same format capability from the same catalog
// view, and its bytes come from the same cascade, so a parser plugin loads
// exactly like any other.
func buildSeam(rw *workspaceconfig.ResolvedWorkspace, resolvePlugin PluginBytesResolver) (Seam, error) {
	wsCfg := rw.Config()
	dirs := workspaceconfig.ResolveCatalogDirs(nil, wsCfg, rw.Root())
	cat, err := catalog.LoadAllWithCacheDir(rw.Root(), wsCfg.ResolvedArtifactCacheDir(), dirs...)
	if err != nil {
		return Seam{}, fmt.Errorf("load catalog for import resolution: %w", err)
	}
	cat.UseEmbeddedProviderResolution()

	load := func(p types.Plugin) (wasm.PluginSpec, error) {
		resolved, rerr := resolvePlugin(rw, p.ArtifactReference())
		if rerr != nil {
			return wasm.PluginSpec{}, rerr
		}
		return resolver.PluginSpec(p.Coordinate(), resolved), nil
	}
	return Seam{
		ImporterRes:  capabilitySelectImporter{cat: cat},
		ImporterLoad: load,
		ParserRes:    cat,
		ParserLoad:   load,
	}, nil
}

// capabilitySelectImporter is the ImporterResolver backed by the catalog's
// capability resolution: it resolves the importer advertising import:<format>
// among the catalog's importer plugins, halting loudly on an unadvertised or
// ambiguous format. An importer is selected by the format it serves rather than
// by a single pinned attachment, which is the natural resolution for an import.
type capabilitySelectImporter struct {
	cat *catalog.Catalog
}

func (r capabilitySelectImporter) ImporterPluginForFormat(format string) (types.Plugin, error) {
	return r.cat.ImporterPluginForFormat(format)
}

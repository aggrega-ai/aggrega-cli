// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"errors"
	"maps"
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The view an import reads in a workspace naming catalog folders is two layers:
// the folders, and the published catalog snapshot behind them. Entities of one
// identity are settled by the load, the folders' winning; a plugin resolved by
// the capability it serves is settled here, layer by layer. Where a plugin of the
// folders serves a capability, the published catalog's plugins serving it are no
// candidates for it, so a published plugin of another name never makes a
// capability the folders serve ambiguous, and one of a later version never
// displaces the folders' own. The published catalog adds only the capabilities
// the folders serve none of. Nothing is read of a plugin but its type, its name,
// its version and the capabilities it declares.

// foldersFirstParsers resolves a syntax's parser over the two layers: from the
// folders alone where a parser of theirs advertises the syntax — an ambiguity
// among the folders' own parsers stays theirs — and from the whole view only
// where none does.
type foldersFirstParsers struct {
	folders, layered parsersocket.ParserResolver
}

// ParserPluginForFormat resolves format to the folders' parser for it, or, where
// the folders advertise none, to the whole view's.
func (r foldersFirstParsers) ParserPluginForFormat(format string) (types.Plugin, error) {
	p, err := r.folders.ParserPluginForFormat(format)
	if errors.Is(err, catalog.ErrNoParserForFormat()) {
		return r.layered.ParserPluginForFormat(format)
	}
	return p, err
}

// foldersFirstImporters resolves a format's importer over the two layers.
//
// Where an importer entity the folders hold advertises the format, the
// resolution is the folders' view's — the folders' importers and the importers
// the binary carries, under every rule that resolution applies, attachments and
// the core importers' included — and the published catalog's importers are no
// candidates, save one a plugins.importer attachment names: an attachment chooses
// the importer it names wherever that importer sits, so where none of the
// attachments names an importer of the folders' view and one names a published
// importer advertising the format, that one is chosen. Where no importer the
// folders hold advertises the format, the whole view resolves it.
type foldersFirstImporters struct {
	folders, layered *catalog.Catalog
}

// ImporterPluginForFormat resolves format over the two layers, given the uri of
// each plugins.importer attachment in list order, and reports the position of
// the attachment that attaches the importer resolved, or -1 when none does.
func (r foldersFirstImporters) ImporterPluginForFormat(format string, attached []string) (types.Plugin, int, error) {
	if !foldersAdvertiseImport(r.folders, format) {
		return r.layered.ImporterPluginForFormat(format, attached)
	}
	p, at, err := r.folders.ImporterPluginForFormat(format, attached)
	if err == nil && at >= 0 {
		return p, at, nil
	}
	if published, publishedAt, perr := r.layered.ImporterPluginForFormat(format, attached); perr == nil &&
		publishedAt >= 0 {
		return published, publishedAt, nil
	}
	return p, at, err
}

// foldersAdvertiseImport reports whether an importer Plugin entity the folders
// hold advertises format. The importers the binary carries are not the folders',
// so they do not count.
func foldersAdvertiseImport(folders *catalog.Catalog, format string) bool {
	for _, key := range slices.Sorted(maps.Keys(folders.Plugins)) {
		p := folders.Plugins[key]
		if p.Metadata.PluginType == string(wasm.PluginTypeImporter) &&
			slices.Contains(catalog.ImporterFormats(p), format) {
			return true
		}
	}
	return false
}

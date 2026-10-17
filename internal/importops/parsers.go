// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// importParsers is the one parser resolution of an import: the parser the
// workspace's catalog serves for a syntax, and, for a syntax the catalog serves
// no parser for, the parser the binary carries for it.
//
// The seam hands it to the importer's parse callback and to the recognition of a
// source no format is named for alike, so the parser a source is recognised
// through is the parser the importer then reads it through, and every surface
// reaching the import resolves its parsers the same way.
//
// 🔴 The binary's parser serves ONLY where no parser in the catalog advertises the
// syntax. Wherever one does, the catalog's resolution answers whatever it answers
// — its parser, or its refusal of parsers of two names advertising one syntax —
// exactly as it does with nothing behind it. So a catalog's own parser is never
// replaced, and a parser the binary carries never joins a choice the catalog
// makes: joining it would turn a catalog holding a JSON parser of another name
// into a choice between two parsers, which a parser has no attachment to settle.
// Where neither serves the syntax, the refusal is the catalog's own, unchanged.
//
// It is the import's alone. A build selects a parser from its catalog's own
// entities and never from the binary's, so what a build parses with does not
// depend on anything here.
type importParsers struct {
	workspace parsersocket.ParserResolver
	binary    parsersocket.ParserResolver
}

// ParserPluginForFormat resolves format to the workspace catalog's parser for it,
// or, where the catalog advertises none, to the binary's.
func (r importParsers) ParserPluginForFormat(format string) (types.Plugin, error) {
	p, err := r.workspace.ParserPluginForFormat(format)
	if err == nil || !errors.Is(err, catalog.ErrNoParserForFormat()) || r.binary == nil {
		return p, err
	}
	carried, carriedErr := r.binary.ParserPluginForFormat(format)
	if errors.Is(carriedErr, catalog.ErrNoParserForFormat()) {
		return types.Plugin{}, err
	}
	return carried, carriedErr
}

// binaryParsers returns the resolution of the parsers the binary carries: a
// catalog view holding the Plugin entities the binary embeds and nothing else, so
// a syntax resolves among them by the one rule every parser is resolved by. A
// binary built without its embedded plugins carries none, and resolves nothing.
//
// The entities are read through the catalog's own reading of the binary's entity
// set — a view holding no entity of its own, which therefore answers a reference
// from the binary's entities alone — rather than decoded here a second time.
func binaryParsers() parsersocket.ParserResolver {
	embeddedOnly := &catalog.Catalog{}
	embeddedOnly.UseEmbeddedProviderResolution()
	carried := map[string]types.Plugin{}
	for _, uri := range embedded.EntityURIs() {
		if p, ok := embeddedOnly.PluginByURI(uri); ok {
			carried[uri] = p
		}
	}
	return &catalog.Catalog{Plugins: carried}
}

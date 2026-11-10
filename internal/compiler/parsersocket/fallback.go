// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package parsersocket

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// WithBinaryParsers returns the one parser resolution every consumer of the
// parser socket resolves through: the parser the catalog view serves for a
// syntax, and, for a syntax that view serves no parser for, the parser the
// binary carries for it. A build's render-verdict parse and an import's parses
// alike take it, so a workspace whose catalog holds no parser reads a format the
// binary carries a parser for, with nothing fetched and no lockfile pin, and the
// two surfaces cannot come to resolve a syntax differently.
//
// 🔴 The binary's parser serves ONLY where no parser in the catalog advertises
// the syntax. Wherever one does, the catalog's resolution answers whatever it
// answers — its parser, or its refusal of parsers of two names advertising one
// syntax — exactly as it does with nothing behind it. So a catalog's own parser
// is never replaced, and a parser the binary carries never joins a choice the
// catalog makes: joining it would turn a catalog holding a parser of another name
// for the same syntax into a choice between two parsers, which a parser has no
// attachment to settle. Where neither serves the syntax, the refusal is the
// catalog's own, unchanged; and a catalog error of any other kind is returned as
// it is, the binary not consulted.
func WithBinaryParsers(view ParserResolver) ParserResolver {
	return fallbackParsers{catalog: view, binary: binaryParsers()}
}

// fallbackParsers is the resolution WithBinaryParsers returns: catalog first,
// and binary only behind a catalog that advertises no parser for the syntax.
type fallbackParsers struct {
	catalog ParserResolver
	binary  ParserResolver
}

// ParserPluginForFormat resolves format to the catalog's parser for it, or, where
// the catalog advertises none, to the binary's.
func (r fallbackParsers) ParserPluginForFormat(format string) (types.Plugin, error) {
	p, err := r.catalog.ParserPluginForFormat(format)
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
func binaryParsers() ParserResolver {
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

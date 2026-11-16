// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package parsersocket

// Coverage for the one parser resolution every consumer of the parser socket
// takes: the catalog view's parser for a syntax, and, for a syntax that view
// serves no parser for, the one the binary carries. The surfaces that reach it —
// an import and a build's render-verdict parse — are covered where each binds it.

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// parserAnswer is what a stand-in parser resolution answers for every syntax,
// counting how often it was asked.
type parserAnswer struct {
	plugin types.Plugin
	err    error
	asked  int
}

func (a *parserAnswer) ParserPluginForFormat(string) (types.Plugin, error) {
	a.asked++
	return a.plugin, a.err
}

// parserNamed is a parser entity of name advertising the json syntax.
func parserNamed(name string) types.Plugin {
	return types.Plugin{
		Metadata: types.PluginMetadata{Name: name, Version: "1.0.0", PluginType: "parser"},
		Spec:     types.PluginSpec{Parser: &types.PluginParser{Capabilities: []string{"parse:json"}}},
	}
}

// TestTheBinarysParserServesOnlyWhereTheCatalogServesNone drives the resolution
// over each answer the catalog's resolution can give, with the binary's
// resolution beside it, and reads which answer it takes and whether the binary
// was asked at all.
//
// The binary is asked only where the catalog answers that no parser advertises
// the syntax. A catalog parser is used; a catalog refusing two parsers of
// different names is refused as the catalog refuses it — the binary's parser
// never joins a choice the catalog makes, so it never makes the ambiguity, nor
// settles one; and where neither serves the syntax the refusal is the catalog's
// own value, unchanged.
//
// 🔴 PLANTED DEFECT: in fallbackParsers.ParserPluginForFormat
// (internal/compiler/parsersocket/fallback.go), consult the binary first, or on
// every catalog error rather than on the no-parser one — the catalog-serves and
// ambiguity rows fail, the binary having been asked or answered. Return the
// binary's refusal where neither serves, and the neither row fails on the
// refusal's identity.
func TestTheBinarysParserServesOnlyWhereTheCatalogServesNone(t *testing.T) {
	t.Parallel()
	noParser := fmt.Errorf("%w: no parser plugin advertises %q", catalog.ErrNoParserForFormat(), "parse:json")
	ambiguous := fmt.Errorf("%w: format %q is advertised by parser plugins [a b]", catalog.ErrAmbiguousParserPlugin(), "json")
	unrelated := errors.New("the catalog could not be read")
	binaryMiss := fmt.Errorf("%w: no parser plugin advertises %q", catalog.ErrNoParserForFormat(), "parse:json")
	binaryAmbiguous := fmt.Errorf("%w: format %q is advertised by parser plugins [x y]", catalog.ErrAmbiguousParserPlugin(), "json")

	for _, row := range []struct {
		name        string
		catalog     parserAnswer
		binary      parserAnswer
		wantPlugin  string
		wantErr     error
		binaryAsked bool
	}{
		{name: "the catalog serves the syntax", catalog: parserAnswer{plugin: parserNamed("catalog-json")},
			binary: parserAnswer{plugin: parserNamed("json")}, wantPlugin: "parser/catalog-json"},
		{name: "the catalog serves none and the binary does", catalog: parserAnswer{err: noParser},
			binary: parserAnswer{plugin: parserNamed("json")}, wantPlugin: "parser/json", binaryAsked: true},
		{name: "neither serves the syntax", catalog: parserAnswer{err: noParser},
			binary: parserAnswer{err: binaryMiss}, wantErr: noParser, binaryAsked: true},
		{name: "the catalog refuses two parsers of different names", catalog: parserAnswer{err: ambiguous},
			binary: parserAnswer{plugin: parserNamed("json")}, wantErr: ambiguous},
		{name: "the catalog fails otherwise", catalog: parserAnswer{err: unrelated},
			binary: parserAnswer{plugin: parserNamed("json")}, wantErr: unrelated},
		{name: "the catalog serves none and the binary refuses", catalog: parserAnswer{err: noParser},
			binary: parserAnswer{err: binaryAmbiguous}, wantErr: binaryAmbiguous, binaryAsked: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			view, binary := row.catalog, row.binary
			got, err := fallbackParsers{catalog: &view, binary: &binary}.ParserPluginForFormat("json")
			if row.wantErr != nil {
				if err != row.wantErr {
					t.Fatalf("the resolution answered %v; want the very refusal %v", err, row.wantErr)
				}
			} else {
				if err != nil {
					t.Fatalf("the resolution was refused: %v", err)
				}
				if got.Coordinate() != row.wantPlugin {
					t.Errorf("the resolution answered %s; want %s", got.Coordinate(), row.wantPlugin)
				}
			}
			if view.asked != 1 {
				t.Errorf("the catalog was asked %d time(s); want exactly once", view.asked)
			}
			if asked := binary.asked > 0; asked != row.binaryAsked {
				t.Errorf("the binary was asked = %v; want %v", asked, row.binaryAsked)
			}
		})
	}
}

// TestTheBinarysParsersAreTheOnesItEmbeds reads the resolution of the parsers the
// binary carries over the build of this binary: the json syntax every core
// importer's signature reads, and the hcl syntax a build's Terraform output is
// read in, each resolve to the parser the binary embeds — an entity whose module
// the binary holds, so it loads from the binary's own tier — and a syntax the
// binary embeds no parser for resolves to the no-parser refusal.
//
// It skips where the entities were never generated because aggrega-hub is not
// checked out beside this repository, and otherwise fails rather than skips
// where the binary embeds no entity: the claim is about the set a release ships,
// and a run that never saw it proves nothing.
func TestTheBinarysParsersAreTheOnesItEmbeds(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	if len(embedded.EntityURIs()) == 0 {
		t.Fatal("this binary embeds no plugin entity, so nothing here can show its parsers being served: build it " +
			"with the repository's build script, which embeds the core plugins' entities")
	}
	res := binaryParsers()

	for _, syntax := range []string{"json", "hcl"} {
		p, err := res.ParserPluginForFormat(syntax)
		if err != nil {
			t.Fatalf("the binary's parsers serve no %s: %v", syntax, err)
		}
		if p.Metadata.PluginType != "parser" || !slices.Contains(p.DeclaredCapabilities(), "parse:"+syntax) {
			t.Errorf("the binary's %s parser is %s of type %q advertising %v", syntax, p.Coordinate(),
				p.Metadata.PluginType, p.DeclaredCapabilities())
		}
		if _, held := embedded.Lookup(p.ArtifactReference()); !held {
			t.Errorf("the binary embeds no module for %s, so its %s parser could not be loaded from the binary",
				p.ArtifactReference(), syntax)
		}
	}

	if _, err := res.ParserPluginForFormat("no-such-syntax"); !errors.Is(err, catalog.ErrNoParserForFormat()) {
		t.Errorf("a syntax the binary embeds no parser for was not refused as such: %v", err)
	}
}

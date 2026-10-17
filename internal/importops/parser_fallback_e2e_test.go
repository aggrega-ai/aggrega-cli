// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

// Coverage for the parser an import reads a source through: the one the
// workspace's catalog serves for its syntax, and, for a syntax the catalog serves
// no parser for, the one the binary carries. The end-to-end cases run the REAL
// seam — Import builds it from the workspace's own catalog and configuration, and
// nothing below points that variable — so the resolution they observe is the one
// every surface reaches.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
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

// TestAnImportFallsBackToTheBinarysParserOnlyWhereTheCatalogServesNone drives the
// import's one parser resolution over each answer the catalog's resolution can
// give, with the binary's resolution beside it, and reads which answer the import
// takes and whether the binary was asked at all.
//
// The binary is asked only where the catalog answers that no parser advertises
// the syntax. A catalog parser is used; a catalog refusing two parsers of
// different names is refused as the catalog refuses it — the binary's parser
// never joins a choice the catalog makes, so it never makes the ambiguity, nor
// settles one; and where neither serves the syntax the refusal is the catalog's
// own value, unchanged.
//
// 🔴 PLANTED DEFECT: in importParsers.ParserPluginForFormat
// (internal/importops/parsers.go), consult the binary first, or on every catalog
// error rather than on the no-parser one — the catalog-serves and ambiguity rows
// fail, the binary having been asked or answered. Return the binary's refusal
// where neither serves, and the neither row fails on the refusal's identity.
func TestAnImportFallsBackToTheBinarysParserOnlyWhereTheCatalogServesNone(t *testing.T) {
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
			workspace, binary := row.catalog, row.binary
			got, err := importParsers{workspace: &workspace, binary: &binary}.ParserPluginForFormat("json")
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
			if workspace.asked != 1 {
				t.Errorf("the catalog was asked %d time(s); want exactly once", workspace.asked)
			}
			if asked := binary.asked > 0; asked != row.binaryAsked {
				t.Errorf("the binary was asked = %v; want %v", asked, row.binaryAsked)
			}
		})
	}
}

// TestTheBinarysParsersAreTheOnesItEmbeds reads the resolution of the parsers the
// binary carries over the build of this binary: the json syntax every core
// importer's signature reads resolves to the JSON parser the binary embeds — an
// entity whose module the binary holds, so the import loads it from the binary's
// own tier — and a syntax the binary embeds no parser for resolves to the
// no-parser refusal.
//
// It fails rather than skips where the binary embeds no entity: the claim is
// about the set a release ships, and a run that never saw it proves nothing.
func TestTheBinarysParsersAreTheOnesItEmbeds(t *testing.T) {
	t.Parallel()
	if len(embedded.EntityURIs()) == 0 {
		t.Fatal("this binary embeds no plugin entity, so nothing here can show its parsers being served: build it " +
			"with the repository's build script, which embeds the core plugins' entities")
	}
	res := binaryParsers()

	p, err := res.ParserPluginForFormat("json")
	if err != nil {
		t.Fatalf("the binary's parsers serve no json: %v", err)
	}
	if p.Metadata.PluginType != "parser" || !slices.Contains(p.DeclaredCapabilities(), "parse:json") {
		t.Errorf("the binary's json parser is %s of type %q advertising %v", p.Coordinate(), p.Metadata.PluginType,
			p.DeclaredCapabilities())
	}
	if _, held := embedded.Lookup(p.ArtifactReference()); !held {
		t.Errorf("the binary embeds no module for %s, so its json parser could not be loaded from the binary",
			p.ArtifactReference())
	}

	if _, err := res.ParserPluginForFormat("no-such-syntax"); !errors.Is(err, catalog.ErrNoParserForFormat()) {
		t.Errorf("a syntax the binary embeds no parser for was not refused as such: %v", err)
	}
}

// parserlessWorkspace resolves a scratch workspace whose catalog folder holds the
// reference importer of each format, attached, and no parser.
func parserlessWorkspace(t *testing.T) (*workspaceconfig.ResolvedWorkspace, importopstest.CatalogFolder) {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	folder := importopstest.LayAttachedImporterFolder(t, wsDir, filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir))
	return resolveWorkspaceAt(t, wsDir), folder
}

// parsersResolved returns the parser references a recorded cascade was asked for,
// in order, and the call for each.
func parsersResolved(cascade *recordingCascade) []cascadeResolution {
	var out []cascadeResolution
	for _, call := range cascade.resolutions() {
		if strings.HasPrefix(call.uri, "parser/") {
			out = append(out, call)
		}
	}
	return out
}

// TestAWorkspaceHoldingNoParserImportsThroughTheBinarysParser drives Import over a
// workspace whose catalog folder holds the reference importer of each format and
// no parser, for a Langflow and a Flowise source, with the format named and with
// it recognised.
//
// Each import completes and drafts what the importer drafts from the source's
// real tree — which only a parser that read the source can have produced — and
// the one parser the import loaded is the binary's JSON parser, read from the
// binary's own tier. With the format recognised, the recognition read the source
// through that parser as well, since nothing else could have told the formats
// apart.
//
// 🔴 PLANTED DEFECT: in buildSeam (internal/importops/seam.go), set ParserRes back
// to the catalog view alone, and every row fails: the recognising rows are refused
// naming the json syntax no parser serves, and the named rows halt with the
// importer's could-not-parse diagnostic.
func TestAWorkspaceHoldingNoParserImportsThroughTheBinarysParser(t *testing.T) {
	t.Parallel()

	for _, src := range catalogFolderSources {
		for _, named := range []bool{true, false} {
			label := src.format + ", the format recognised"
			format := ""
			if named {
				label, format = src.format+", the format named", src.format
			}
			t.Run(label, func(t *testing.T) {
				t.Parallel()
				rw, _ := parserlessWorkspace(t)
				cascade := &recordingCascade{}
				res, err := Import(context.Background(), rw, Options{
					SourcePath:    src.source,
					SourceName:    filepath.Base(src.source),
					Format:        format,
					Delivery:      authoredcontent.DeliveryFor(true),
					ResolvePlugin: cascade.resolve,
				})
				if err != nil {
					t.Fatalf("the import was refused: %v", err)
				}
				if res.Halted() {
					t.Fatalf("the importer halted: %+v", res.Diagnostics)
				}
				if res.Format != src.format {
					t.Errorf("the source was read as the %q format, want %q", res.Format, src.format)
				}
				if got := draftPaths(res); !slices.Equal(got, src.drafts) {
					t.Errorf("the importer drafted %v, want %v", got, src.drafts)
				}
				parsers := parsersResolved(cascade)
				if len(parsers) == 0 {
					t.Fatalf("the import loaded no parser; it resolved %+v", cascade.resolutions())
				}
				for _, p := range parsers {
					if p.uri != "parser/json" || p.tier != pluginpath.TierEmbed {
						t.Errorf("the import loaded the parser %s from the %q tier; want only the binary's parser/json "+
							"from the binary's own tier", p.uri, p.tier)
					}
				}
			})
		}
	}
}

// TestAWorkspaceHoldingNothingRecognisesAndImportsALoneFileThroughACoreImporter is
// the case as a user first meets it: an offline workspace whose catalog holds
// nothing at all — no importer and no parser — while the binary is made to carry
// importer/langflow, an entity generated from the reference Langflow importer
// held to that importer's own digest, and the importer's module is on this
// machine through the tier a module compiled into the binary is read from.
//
// A lone Langflow file imported with no format named is recognised as Langflow
// through the binary's JSON parser and imported through the core importer, which
// reads it through the same parser, and the drafts are the importer's.
//
// 🔴 PLANTED DEFECT: in buildSeam (internal/importops/seam.go), set ParserRes back
// to the catalog view alone, and the import is refused before any importer runs,
// naming the json syntax no parser serves.
func TestAWorkspaceHoldingNothingRecognisesAndImportsALoneFileThroughACoreImporter(t *testing.T) {
	// Not parallel: it seeds the process-wide carried set and the modules the
	// binary is read as carrying.
	src := catalogFolderSources[0]
	scratch := aWorkspaceDir(t)
	reference, _ := importopstest.LayImporters(t, scratch, filepath.Join(scratch, "catalog"),
		importopstest.Importer{Name: "langflow", Format: "langflow"})
	langflow := reference[0]
	carry(t, embedded.PublishedPlugin{
		URI:    "importer/langflow",
		Entity: entityAt(t, langflow),
		Digest: digest.ContentDigest(moduleAt(t, langflow)),
	})
	t.Cleanup(embedded.SeedForTest("importer/langflow", moduleAt(t, langflow)))

	wsDir := aWorkspaceDir(t)
	importopstest.WriteOfflineConfiguration(t, wsDir)
	rw := resolveWorkspaceAt(t, wsDir)
	if _, err := os.Stat(filepath.Join(rw.Root(), workspaceconfig.ConventionalCatalogDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the workspace holds a catalog folder (stat: %v), so it is not one holding nothing", err)
	}

	cascade := &recordingCascade{workspaceCache: true}
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    src.source,
		SourceName:    filepath.Base(src.source),
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: cascade.resolve,
	})
	if err != nil {
		t.Fatalf("the import was refused: %v", err)
	}
	if res.Halted() {
		t.Fatalf("the importer halted: %+v", res.Diagnostics)
	}
	if res.Format != src.format {
		t.Errorf("the source was read as the %q format, want %q", res.Format, src.format)
	}
	if got := draftPaths(res); !slices.Equal(got, src.drafts) {
		t.Errorf("the importer drafted %v, want %v", got, src.drafts)
	}
	for _, p := range parsersResolved(cascade) {
		if p.uri != "parser/json" || p.tier != pluginpath.TierEmbed {
			t.Errorf("the import loaded the parser %s from the %q tier; want only the binary's parser/json", p.uri, p.tier)
		}
	}
}

// TestAWorkspacesOwnParserIsTheOneAnImportReadsThrough drives Import over a
// workspace whose catalog folder holds the reference importers and the planted
// parser — a parser of the json syntax answering one fixed tree whatever it is
// handed — for a Langflow source.
//
// The catalog serves the json syntax, so its parser is the one read through, and
// the binary's is never loaded. What the import did shows it: recognised with no
// format named, the Langflow source is read as the format the planted tree's shape
// is — Flowise — and drafted as the one planted node; with the Langflow format
// named, the Langflow importer finds no Langflow node in the planted tree and
// drafts no component, warning so. A binary parser read in its place would have
// recognised and drafted the source's real Langflow nodes.
//
// 🔴 PLANTED DEFECT: in importParsers.ParserPluginForFormat
// (internal/importops/parsers.go), ask the binary before the catalog, and both
// rows fail: the source is recognised and drafted as Langflow, through parser/json.
func TestAWorkspacesOwnParserIsTheOneAnImportReadsThrough(t *testing.T) {
	t.Parallel()
	src := catalogFolderSources[0]

	for _, row := range []struct {
		name       string
		format     string
		wantFormat string
	}{
		{name: "the format recognised", wantFormat: "flowise"},
		{name: "the format named", format: "langflow", wantFormat: "langflow"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			rw, folder := parserlessWorkspace(t)
			planted := importopstest.LayPlantedJSONParser(t, rw.Root(), folder.Dir)

			cascade := &recordingCascade{}
			res, err := Import(context.Background(), rw, Options{
				SourcePath:    src.source,
				SourceName:    filepath.Base(src.source),
				Format:        row.format,
				Delivery:      authoredcontent.DeliveryFor(true),
				ResolvePlugin: cascade.resolve,
			})
			if err != nil {
				t.Fatalf("the import was refused: %v", err)
			}
			if res.Format != row.wantFormat {
				t.Errorf("the source was read as the %q format, want %q — the format the parser read it as decides it",
					res.Format, row.wantFormat)
			}
			plantedDraft := strings.ToLower(importopstest.PlantedParserNodeType)
			drafts := draftPaths(res)
			drafted := func(part string) bool {
				return slices.ContainsFunc(drafts, func(p string) bool { return strings.Contains(p, part) })
			}
			if drafted("openaimodel") {
				t.Errorf("the importer drafted the source's real Langflow nodes %v, so another parser read it", drafts)
			}
			if row.format == "" && !drafted(plantedDraft) {
				t.Errorf("the importer drafted %v; want a draft of the planted node %s", drafts, plantedDraft)
			}
			if row.format != "" {
				if drafted("components/") {
					t.Errorf("the importer drafted components %v from a tree carrying no Langflow node", drafts)
				}
				if !slices.ContainsFunc(res.Diagnostics, func(d abi.Diagnostic) bool { return d.Code == "no-nodes" }) {
					t.Errorf("the importer did not warn that the tree carried no node: %+v", res.Diagnostics)
				}
			}

			parsers := parsersResolved(cascade)
			if len(parsers) == 0 {
				t.Fatalf("the import loaded no parser; it resolved %+v", cascade.resolutions())
			}
			for _, p := range parsers {
				if p.uri != planted.Reference || p.tier != pluginpath.TierColocated {
					t.Errorf("the import loaded the parser %s from the %q tier; want only the workspace's %s from "+
						"beside its entity", p.uri, p.tier, planted.Reference)
				}
			}
		})
	}
}

// TestASyntaxNeitherTheWorkspaceNorTheBinaryParsesIsRefusedAsBefore drives Import
// over a workspace whose catalog folder holds an importer recognising its sources
// in a syntax for which neither the catalog nor the binary carries a parser, for a
// lone file with no format named.
//
// The recognition stops naming that importer and the syntax, and the refusal
// carried is exactly the one the catalog's own resolution gives for a syntax no
// parser advertises — the refusal the import gave before the binary's parsers
// served at all. The importer's name sorts ahead of the core importers' so its
// signature is the first the recognition tries, whatever this binary carries.
//
// 🔴 PLANTED DEFECT: in importParsers.ParserPluginForFormat
// (internal/importops/parsers.go), answer the binary's miss with an error of its
// own rather than the catalog's refusal, and the comparison fails.
func TestASyntaxNeitherTheWorkspaceNorTheBinaryParsesIsRefusedAsBefore(t *testing.T) {
	t.Parallel()
	const syntax = "no-such-syntax"
	wsDir := aWorkspaceDir(t)
	importopstest.WriteImporterEntity(t, filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir), "an-unread-probe",
		[]string{"unread-probe"}, []types.PluginDetectSignature{{
			Syntax: syntax,
			Match:  []types.PluginPathMatch{{Path: "root", Is: types.IsPresent}},
		}})
	importopstest.WriteConfiguration(t, wsDir)
	rw := resolveWorkspaceAt(t, wsDir)
	source := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "doc.unread"), "<root/>\n")

	_, err := Import(context.Background(), rw, Options{
		SourcePath:    source,
		SourceName:    "doc.unread",
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
	if !errors.Is(err, catalog.ErrNoParserForFormat()) {
		t.Fatalf("the import was not refused for want of a parser: %v", err)
	}
	_, catalogRefusal := (&catalog.Catalog{}).ParserPluginForFormat(syntax)
	want := fmt.Sprintf("recognise the source format of %q: importer %q declares a signature read in %s: "+
		"resolve parser for format %q: %s", "doc.unread", "importer/an-unread-probe", syntax, syntax, catalogRefusal.Error())
	if err.Error() != want {
		t.Errorf("the refusal reads\n  %s\nwant the catalog's own refusal, as before\n  %s", err.Error(), want)
	}
}

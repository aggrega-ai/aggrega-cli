// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// Coverage for the parser an import reads a source through: the one the
// workspace's catalog serves for its syntax, and, for a syntax the catalog serves
// no parser for, the one the binary carries. The end-to-end cases run the REAL
// seam — Import builds it from the workspace's own catalog and configuration, and
// nothing below points that variable — so the resolution they observe is the one
// every surface reaches. The resolution's own rows, over every answer a catalog
// can give, are the parser socket's (internal/compiler/parsersocket).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
)

// parserlessWorkspace resolves a scratch workspace whose catalog folder holds the
// reference importer of each format, attached, and no parser. The attachments
// hand each importer the node-by-node layout, so what a source is drafted as
// names the nodes of the tree the parser returned for it, one Component each.
func parserlessWorkspace(t *testing.T) (*workspaceconfig.ResolvedWorkspace, importopstest.CatalogFolder) {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	folder := importopstest.LayAttachedImporterFolderWith(t, wsDir, filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir),
		importopstest.NodesLayout())
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

// parserLoads returns each parser a recorded cascade was asked for, written as
// "<reference> from <tier>", once each and sorted — the set of parsers an import
// loaded, and from where, whatever their order or how often each was read.
func parserLoads(cascade *recordingCascade) []string {
	var out []string
	for _, p := range parsersResolved(cascade) {
		load := fmt.Sprintf("%s from %s", p.uri, p.tier)
		if !slices.Contains(out, load) {
			out = append(out, load)
		}
	}
	slices.Sort(out)
	return out
}

// binaryYAMLParser is the load of the binary's own YAML parser, from the binary's
// own tier. A recognition of a lone file reads it in YAML for the signature the
// reference Dify importer declares, which names no file and reads YAML, so the
// file is parsed through it wherever the workspace serves no YAML parser.
var binaryYAMLParser = fmt.Sprintf("parser/yaml from %s", pluginpath.TierEmbed)

// TestAWorkspaceHoldingNoParserImportsThroughTheBinarysParser drives Import over a
// workspace whose catalog folder holds the reference importer of each format and
// no parser, for a Langflow and a Flowise source, with the format named and with
// it recognised.
//
// Each import completes and drafts what the importer drafts from the source's
// real tree — which only a parser that read the source can have produced. With
// the format named, the one parser the import loaded is the binary's JSON parser,
// read from the binary's own tier. With the format recognised, the recognition
// read the source through that parser as well, since nothing else could have told
// the formats apart, and through the binary's YAML parser, from the same tier, for
// the one signature naming no file and reading YAML, the Dify importer's — and
// through no other parser.
//
// 🔴 PLANTED DEFECT: in buildSeam (internal/importops/seam.go), set ParserRes back
// to the catalog view alone, and every row fails: the recognising rows are refused
// naming the json syntax no parser serves, and the named rows halt with the
// importer's could-not-parse diagnostic.
func TestAWorkspaceHoldingNoParserImportsThroughTheBinarysParser(t *testing.T) {
	embeddedentities.Require(t)
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
				want := []string{fmt.Sprintf("parser/json from %s", pluginpath.TierEmbed)}
				if !named {
					want = append(want, binaryYAMLParser)
					slices.Sort(want)
				}
				if got := parserLoads(cascade); !slices.Equal(got, want) {
					t.Errorf("the import loaded the parsers %v; want exactly %v — the binary's parser/json from the "+
						"binary's own tier, and, with the format recognised, its parser/yaml from that tier for the one "+
						"signature naming no file and reading YAML", got, want)
				}
			})
		}
	}
}

// TestAWorkspaceHoldingNothingRecognisesAndImportsALoneFileThroughACoreImporter is
// the case as a user first meets it: an offline workspace whose catalog holds
// nothing at all — no importer and no parser. It names no catalog folder, so its
// catalog is the published catalog snapshot it holds, fetched earlier from a
// registry of the test's own: the snapshot of an empty catalog. Meanwhile the
// binary is made to carry
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
	embeddedentities.Require(t)
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
	holdSnapshotOf(t, wsDir, t.TempDir())
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
	assertTheDefaultDraft(t, res, src)
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
// the binary's JSON parser is never loaded; with no format named, the recognition
// reads the file in YAML too, for the Dify importer's signature, through the
// binary's YAML parser, since the catalog serves none. What the import did shows it: recognised with no
// format named, the Langflow source is read as the format the planted tree's shape
// is — Flowise — and drafted as the one planted node; with the Langflow format
// named, the Langflow importer finds no Langflow node in the planted tree and
// drafts no component, warning so. A binary parser read in its place would have
// recognised and drafted the source's real Langflow nodes.
//
// 🔴 PLANTED DEFECT: in fallbackParsers.ParserPluginForFormat
// (internal/compiler/parsersocket/fallback.go), ask the binary before the catalog, and both
// rows fail: the source is recognised and drafted as Langflow, through parser/json.
func TestAWorkspacesOwnParserIsTheOneAnImportReadsThrough(t *testing.T) {
	embeddedentities.Require(t)
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

			want := []string{fmt.Sprintf("%s from %s", planted.Reference, pluginpath.TierColocated)}
			if row.format == "" {
				want = append(want, binaryYAMLParser)
				slices.Sort(want)
			}
			if got := parserLoads(cascade); !slices.Equal(got, want) {
				t.Errorf("the import loaded the parsers %v; want exactly %v — the workspace's %s from beside its "+
					"entity, never the binary's parser/json, and, with the format recognised, the binary's parser/yaml "+
					"for the one signature naming no file and reading YAML, a syntax the workspace serves no parser for",
					got, want, planted.Reference)
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
// 🔴 PLANTED DEFECT: in fallbackParsers.ParserPluginForFormat
// (internal/compiler/parsersocket/fallback.go), answer the binary's miss with an error of its
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

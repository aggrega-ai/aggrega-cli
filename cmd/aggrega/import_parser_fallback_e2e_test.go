// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage, at every entry point that reaches the import operation, of
// the parser an import reads a source through: the one the workspace's catalog
// serves for its syntax, and, for a syntax the catalog serves none for, the one
// the binary carries. `aggrega import`, the assistant's import tool through
// `aggrega mcp`, and the session's aggrega/import method through `aggrega serve`
// are each driven at the real command tree, with the real import seam and the
// plugin resolution this package supplies — nothing here points the seam or
// stands in for the resolution. The operation's own coverage of the same
// resolution, which names the parser each import loaded and the tier it was read
// at, lives beside the operation.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// parserlessFolderWorkspace declares an isolated workspace whose conventional
// catalog folder holds the reference importer of each format, attached with the
// node-by-node layout — so what a source is drafted as is a component per node of
// the tree the parser returned — and no parser, and returns its root and what the
// folder holds. A case calls isolateMachineConfiguration first.
func parserlessFolderWorkspace(t *testing.T) (string, importopstest.CatalogFolder) {
	t.Helper()
	root := t.TempDir()
	folder := importopstest.LayAttachedImporterFolderWith(t, root, filepath.Join(root, workspaceconfig.ConventionalCatalogDir),
		importopstest.NodesLayout())
	return resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}).Root(), folder
}

// sharedParserlessImport is the expectation every surface is held to: the shared
// operation over a workspace laid out like the one under test — the importers and
// no parser — through the real seam and this package's own plugin resolution,
// handed no destination and asked to report rather than write. It refuses an
// expectation that would measure nothing: a halted import, or one that drafted no
// component.
func sharedParserlessImport(t *testing.T, source []byte, name string) importops.Result {
	t.Helper()
	root, _ := parserlessFolderWorkspace(t)
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace the shared operation runs in: %v", err)
	}
	res, err := importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    importopstest.WriteSource(t, filepath.Join(t.TempDir(), name), string(source)),
		SourceName:    name,
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: importPluginBytes,
	})
	if err != nil {
		t.Fatalf("the shared import over a workspace holding no parser was refused, so there is no expectation: %v", err)
	}
	components := 0
	for _, f := range res.Files {
		if strings.HasPrefix(f.Path, "components/") {
			components++
		}
	}
	if res.Halted() || components == 0 {
		t.Fatalf("the shared import over a workspace holding no parser halted or drafted no component, so the "+
			"comparison would measure nothing: %+v", res.Diagnostics)
	}
	return res
}

// TestEveryImportSurfaceReadsThroughTheBinarysParserWhereTheWorkspaceServesNone
// drives each surface over a workspace whose catalog folder holds the reference
// importers and no parser, with a Langflow source imported on its own and no
// format named. Each surface recognises it as Langflow and drafts what the shared
// operation drafts — the importer's drafts of the source's real tree — so the
// source was recognised and read through a parser the catalog does not hold: the
// one the binary carries.
//
// 🔴 PLANTED DEFECT: in buildSeam (internal/importops/seam.go), set ParserRes back
// to the catalog view alone, and the expectation is refused already — the shared
// operation is refused naming the json syntax no parser serves — and so is every
// surface.
func TestEveryImportSurfaceReadsThroughTheBinarysParserWhereTheWorkspaceServesNone(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	src := catalogFolderImportSources[0]
	body := readCatalogFolderSource(t, src.path)
	shared := sharedParserlessImport(t, body, "flow.json")

	t.Run("the command", func(t *testing.T) {
		root, _ := parserlessFolderWorkspace(t)
		external := filepath.Join(t.TempDir(), "flow.json")
		if err := os.WriteFile(external, body, 0o644); err != nil {
			t.Fatalf("write the source: %v", err)
		}
		assertImportCommandDraftsTheSharedResult(t, root, external, src.format, true, shared)
	})

	t.Run("the assistant's tool", func(t *testing.T) {
		root, _ := parserlessFolderWorkspace(t)
		seedSource(t, root, "flow.json", body)
		answer := callAssistantTool(t, root, "import", map[string]any{"source": "flow.json"})
		if answer.isError {
			t.Fatalf("the import was refused: %s", answer.text)
		}
		var payload struct {
			Format  string   `json:"format"`
			Written []string `json:"written"`
		}
		if err := json.Unmarshal(answer.structured, &payload); err != nil {
			t.Fatalf("decode the import answer's payload: %v (body %s)", err, answer.structured)
		}
		if payload.Format != src.format {
			t.Errorf("the tool read the source as %q; want %q", payload.Format, src.format)
		}
		assertDraftsMatchTheSharedImport(t, root, payload.Written, shared)
	})

	t.Run("the session", func(t *testing.T) {
		root, _ := parserlessFolderWorkspace(t)
		seedSource(t, root, "flow.json", body)
		frame, stderr := callSession(t, root, "aggrega/import", map[string]any{"source": "flow.json", "outputDir": "drafts"})
		if raw, refused := frame["error"]; refused {
			t.Fatalf("aggrega/import was refused: %s\nstderr:\n%s", raw, stderr)
		}
		var result struct {
			Format  string   `json:"format"`
			Written []string `json:"written"`
			Halted  bool     `json:"halted"`
		}
		if err := json.Unmarshal(frame["result"], &result); err != nil {
			t.Fatalf("decode the import answer: %v\n%s", err, frame["result"])
		}
		if result.Halted {
			t.Fatalf("the importer halted: %s", frame["result"])
		}
		if result.Format != src.format {
			t.Errorf("the session read the source as %q; want %q", result.Format, src.format)
		}
		assertDraftsMatchTheSharedImport(t, filepath.Join(root, "drafts"), result.Written, shared)
	})
}

// TestEveryImportSurfaceReadsThroughTheWorkspacesOwnParser drives each surface over
// a workspace whose catalog folder holds the reference importers and the planted
// parser — a parser of the json syntax answering one fixed tree, of the Flowise
// shape, whatever it is handed — with a Langflow source and no format named.
//
// Each surface reads the Langflow source as Flowise: the catalog serves the json
// syntax, so its planted parser read the source, and the binary's — which would
// have read it as the Langflow it is — played no part. The command's drafts are
// the one planted node's.
//
// 🔴 PLANTED DEFECT: in importParsers.ParserPluginForFormat
// (internal/importops/parsers.go), ask the binary before the catalog, and every
// surface reads the source as Langflow.
func TestEveryImportSurfaceReadsThroughTheWorkspacesOwnParser(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	// The planted parser is built before the machine's configuration is isolated,
	// as the importer fixtures are, because the toolchain building it reads those
	// locations.
	importopstest.PlantedJSONParserModule(t)
	isolateMachineConfiguration(t)
	body := readCatalogFolderSource(t, catalogFolderImportSources[0].path)
	planted := strings.ToLower(importopstest.PlantedParserNodeType)

	plantedWorkspace := func(t *testing.T) string {
		t.Helper()
		root, folder := parserlessFolderWorkspace(t)
		importopstest.LayPlantedJSONParser(t, root, folder.Dir)
		seedSource(t, root, "flow.json", body)
		return root
	}

	t.Run("the command", func(t *testing.T) {
		root := plantedWorkspace(t)
		code, stdout, commentary, process := runImportAtTheRoot(t, root, filepath.Join(root, "flow.json"), "--dry-run")
		if code != 0 {
			t.Fatalf("aggrega import exited %d\nstdout:\n%s\nprocess error stream:\n%s", code, stdout, process)
		}
		if got := commentaryFormat(commentary); got != "flowise" {
			t.Errorf("the command read the source as %q; want flowise, the shape of the planted parser's tree\n%s",
				got, commentary)
		}
		if !strings.Contains(stdout, planted) || strings.Contains(stdout, "openaimodel") {
			t.Errorf("the command's drafts are not the planted node's alone:\n%s", stdout)
		}
	})

	t.Run("the assistant's tool", func(t *testing.T) {
		root := plantedWorkspace(t)
		answer := importThroughTheTool(t, root, map[string]any{"source": "flow.json", "dry_run": true})
		if answer.refused {
			t.Fatalf("the import was refused: %s", answer.text)
		}
		if answer.format != "flowise" {
			t.Errorf("the tool read the source as %q; want flowise, the shape of the planted parser's tree", answer.format)
		}
	})

	t.Run("the session", func(t *testing.T) {
		root := plantedWorkspace(t)
		answer := importThroughTheSession(t, root, map[string]any{"source": "flow.json", "outputDir": "drafts", "dryRun": true})
		if answer.refusal != "" {
			t.Fatalf("aggrega/import was refused: %s", answer.refusal)
		}
		if answer.format != "flowise" {
			t.Errorf("the session read the source as %q; want flowise, the shape of the planted parser's tree", answer.format)
		}
	})
}

// TestEveryImportSurfaceRefusesASyntaxNoParserServesAsBefore drives each surface
// over a workspace whose catalog folder holds one importer, recognising its
// sources in a syntax for which neither the catalog nor the binary carries a
// parser, with a lone file and no format named. Every surface refuses it with the
// no-parser condition, carrying the catalog's own refusal for that syntax — the
// one every surface carried before the binary's parsers served — and the shared
// operation's refusal is that refusal, word for word.
//
// 🔴 PLANTED DEFECT: in importParsers.ParserPluginForFormat
// (internal/importops/parsers.go), answer the binary's miss with a nil error and
// an empty parser, and every surface reports a parser that could not be loaded
// rather than the no-parser condition.
func TestEveryImportSurfaceRefusesASyntaxNoParserServesAsBefore(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the
	// environment is set for the whole test.
	withCLIRegistrations(t, nil)
	isolateMachineConfiguration(t)
	const syntax = "no-such-syntax"
	short, ok := cerrors.ShortCodeOf(catalog.ErrNoParserForFormat())
	if !ok {
		t.Fatal("the no-parser condition carries no short code, so its refusal cannot be named")
	}
	_, catalogRefusal := (&catalog.Catalog{}).ParserPluginForFormat(syntax)
	if catalogRefusal == nil {
		t.Fatalf("an empty catalog serves the %s syntax, so nothing here measures a refusal", syntax)
	}
	_, sentence, _ := strings.Cut(catalogRefusal.Error(), ": ")

	unreadWorkspace := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		importopstest.WriteImporterEntity(t, filepath.Join(root, workspaceconfig.ConventionalCatalogDir),
			"an-unread-probe", []string{"unread-probe"}, []types.PluginDetectSignature{{
				Syntax: syntax,
				Match:  []types.PluginPathMatch{{Path: "root", Is: types.IsPresent}},
			}})
		root = resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}).Root()
		seedSource(t, root, "doc.unread", []byte("<root/>\n"))
		return root
	}

	rw, err := workspaceconfig.Resolve(unreadWorkspace(t), "")
	if err != nil {
		t.Fatalf("resolve the workspace the shared operation runs in: %v", err)
	}
	_, shared := importops.Import(context.Background(), rw, importops.Options{
		SourcePath:    filepath.Join(rw.Root(), "doc.unread"),
		SourceName:    "doc.unread",
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: importPluginBytes,
	})
	if !errors.Is(shared, catalog.ErrNoParserForFormat()) {
		t.Fatalf("the shared operation was not refused for want of a parser: %v", shared)
	}
	if !strings.HasSuffix(shared.Error(), "resolve parser for format \""+syntax+"\": "+catalogRefusal.Error()) {
		t.Errorf("the shared operation's refusal %q does not end in the catalog's own refusal %q", shared, catalogRefusal)
	}

	t.Run("the command", func(t *testing.T) {
		root := unreadWorkspace(t)
		code, stdout, _, process := runImportAtTheRoot(t, root, filepath.Join(root, "doc.unread"), "--dry-run")
		if code == 0 {
			t.Fatalf("a source no parser can read was imported:\n%s", stdout)
		}
		for _, want := range []string{"[" + short + "]", sentence} {
			if !strings.Contains(process, want) {
				t.Errorf("the command's refusal does not carry %q:\n%s", want, process)
			}
		}
	})

	t.Run("the assistant's tool", func(t *testing.T) {
		root := unreadWorkspace(t)
		answer := importThroughTheTool(t, root, map[string]any{"source": "doc.unread", "dry_run": true})
		if !answer.refused {
			t.Fatalf("the tool imported a source no parser can read: %s", answer.text)
		}
		for _, want := range []string{short, sentence} {
			if !strings.Contains(answer.text, want) {
				t.Errorf("the tool's refusal does not carry %q: %s", want, answer.text)
			}
		}
	})

	t.Run("the session", func(t *testing.T) {
		root := unreadWorkspace(t)
		answer := importThroughTheSession(t, root, map[string]any{"source": "doc.unread", "outputDir": "drafts", "dryRun": true})
		if !strings.Contains(answer.refusal, sentence) {
			t.Errorf("the session's refusal does not carry %q: %q", sentence, answer.refusal)
		}
	})
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package importopstest lays down, for a test, a catalog folder from which an
// import runs end to end: the reference agent-flow importer and a JSON parser,
// each as a real Plugin entity with its built module beside it — or several
// importers generated from that one module under names, versions and formats of
// the caller's choosing — and the workspace configuration attaching them.
//
// It exists so every surface that reaches the import operation — the operation
// itself, the command line, the assistant's tool and the editor session — is
// proven over one and the same folder, built one way. A folder each test
// package assembled for itself would be a copy that agreed with the others on
// the day it was written.
//
// Every entity is composed by the catalog generation the command line runs
// (`aggrega plugin gen-catalog`), from the built modules, so an entity here is
// exactly what an author generating one from their own build gets — save where a
// caller narrows the formats an importer advertises, which is an edit an author
// may make to an entity they own, made through the entity's own setter. Each
// module is laid down under the name the plugin cascade reads it by, derived from
// the entity's own artifact reference rather than spelled here.
//
// The parser is the JSON parser this binary carries, generated under a name the
// binary does not embed, so its module can be read only from beside its entity:
// an import over this folder therefore reads the parser the importer calls back
// to through the same tier it reads the importer through, and a resolution that
// silently fell back to the binary's own copy could not satisfy it.
package importopstest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ImporterFixture is the built reference agent-flow importer the folder's
// importer entity is generated from.
const ImporterFixture = "import-langflow-go.wasm"

// importerName is the metadata.name the folder's importer entity carries.
const importerName = "import-langflow"

// ParserName is the metadata.name the folder's parser entity carries —
// deliberately one the binary does not embed.
const ParserName = "catalog-json"

// embeddedJSONParser is the binary's own JSON parser, whose module the folder's
// parser entity is generated from.
const embeddedJSONParser = "parser/json"

// LaidPlugin is one Plugin entity the folder holds.
type LaidPlugin struct {
	// Reference is the entity's artifact reference: the key the catalog indexes
	// the directory beside the entity under, and the reference the cascade reads
	// its module by.
	Reference string
	// Identity is the entity's identity and version, <pluginType>/<name>:<version>
	// — the form a plugins.importer attachment's uri names it by, and the form
	// `aggrega plugin add` writes.
	Identity string
	// Name is the entity's metadata.name, the name a diagnostic lists it by.
	Name string
	// Version is the entity's metadata.version.
	Version string
	// EntityDir is the directory the entity's document sits in.
	EntityDir string
	// Module is where the entity's module sits beside it. It is laid down only
	// when the folder was asked for its modules.
	Module string
}

// Importer is one importer entity a folder is asked to hold, generated from the
// reference agent-flow importer.
type Importer struct {
	// Name is the entity's metadata.name.
	Name string
	// Version is the version the module the entity is generated from reports
	// about its own build, and so the entity's metadata.version. Empty is the
	// fixture's own version.
	Version string
	// Formats, when non-empty, narrows the formats the entity advertises to these,
	// each written as the bare format (langflow, flowise). The module itself still
	// handles every format it handles: the narrowing is the entity's claim, which
	// is what the catalog selects an importer by, so it is how two entities
	// generated from one module come to advertise different formats. Empty keeps
	// every format the module reports.
	Formats []string
}

// Attachment is one plugins.importer entry a workspace configuration carries.
type Attachment struct {
	// URI is the entry's uri, written as given.
	URI string
	// Settings is the entry's with.settings; nil writes none.
	Settings map[string]any
	// Sources is the entry's with.sources; empty writes none.
	Sources []string
}

// LayImporters lays each importer down at dir, inside the workspace rooted at
// workspaceRoot, generated from the reference agent-flow importer with its
// module beside it, together with the JSON parser the importer calls back to,
// and returns the importers in the order given and the parser.
//
// It builds the importer fixture, and a stamped build of it for each version
// asked for, so a caller that goes on to change the process environment the
// toolchain reads should call this first.
func LayImporters(t *testing.T, workspaceRoot, dir string, importers ...Importer) ([]LaidPlugin, LaidPlugin) {
	t.Helper()
	laid := make([]LaidPlugin, 0, len(importers))
	for _, imp := range importers {
		module := wasmtest.EnsureFixtureBuilt(t, ImporterFixture)
		if imp.Version != "" {
			module = wasmtest.EnsureStampedFixtureBuilt(t, ImporterFixture, imp.Version)
		}
		var capabilities []string
		for _, format := range imp.Formats {
			capabilities = append(capabilities, "import:"+format)
		}
		laid = append(laid, layPlugin(t, workspaceRoot, dir, module, imp.Name, capabilities, true))
	}
	return laid, layParser(t, workspaceRoot, dir, true)
}

// WriteConfiguration writes the workspace configuration file at root: a
// workspace block, which anchors the root at that directory, and, when
// attachments are given, a plugins.importer list carrying them in order.
//
// It is encoded through the YAML marshaller rather than assembled from strings,
// so a uri or a setting holding a character that has to be quoted is written as
// a value the loader reads back unchanged.
func WriteConfiguration(t *testing.T, root string, attachments ...Attachment) {
	t.Helper()
	doc := map[string]any{"workspace": map[string]any{}}
	if len(attachments) > 0 {
		entries := make([]map[string]any, 0, len(attachments))
		for _, a := range attachments {
			entry := map[string]any{"uri": a.URI}
			with := map[string]any{}
			if a.Settings != nil {
				with["settings"] = a.Settings
			}
			if len(a.Sources) > 0 {
				with["sources"] = a.Sources
			}
			if len(with) > 0 {
				entry["with"] = with
			}
			entries = append(entries, entry)
		}
		doc["plugins"] = map[string]any{"importer": entries}
	}
	body, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("encode the workspace configuration: %v", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create the workspace root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), body, 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
}

// CatalogFolder is what LayCatalogFolder put down.
type CatalogFolder struct {
	// Dir is the catalog directory.
	Dir      string
	Importer LaidPlugin
	Parser   LaidPlugin
}

// LayCatalogFolder lays the folder down at dir, inside the workspace rooted at
// workspaceRoot, and returns what it holds.
//
// withModules false writes both entities and leaves each without its module —
// the case of a module that is not beside its entity.
//
// It builds the importer fixture, so a caller that goes on to change the
// process environment the toolchain reads should call this first.
//
// There is no skip here beyond the one the fixture build itself carries, and
// that one fires only when the plugin kit's source or its toolchain is absent:
// a binary carrying no JSON parser, or a generation that refuses, fails the
// calling test.
func LayCatalogFolder(t *testing.T, workspaceRoot, dir string, withModules bool) CatalogFolder {
	t.Helper()

	importerModule := wasmtest.EnsureFixtureBuilt(t, ImporterFixture)
	return CatalogFolder{
		Dir:      dir,
		Importer: layPlugin(t, workspaceRoot, dir, importerModule, importerName, nil, withModules),
		Parser:   layParser(t, workspaceRoot, dir, withModules),
	}
}

// layParser lays the binary's own JSON parser down at dir under a name the binary
// does not embed, with its module beside it when withModule is set.
func layParser(t *testing.T, workspaceRoot, dir string, withModule bool) LaidPlugin {
	t.Helper()
	parserBytes, ok := embedded.Lookup(embeddedJSONParser)
	if !ok {
		t.Fatalf("this binary carries no %s, so no parser can be laid down beside an entity", embeddedJSONParser)
	}
	parserModule := filepath.Join(t.TempDir(), "json-parser.wasm")
	if err := os.WriteFile(parserModule, parserBytes, 0o644); err != nil {
		t.Fatalf("write the JSON parser module: %v", err)
	}
	return layPlugin(t, workspaceRoot, dir, parserModule, ParserName, nil, withModule)
}

// layPlugin generates one entity from the module at module under the identity
// name, writes it where the generation addresses it beneath dir, and lays the
// module beside it when withModule is set.
//
// The generation runs as a dry run and its one document is then written at the
// path it names, with the content it carries: the bytes and the place are both
// the generation's, and running it as a dry run is what hands them back to be
// read before they are written.
//
// capabilities, when non-empty, replaces the capabilities the generated entity
// declares before it is written — the one change made to what the generation
// produced, and made through the entity's own setter for its plugin type's block.
func layPlugin(t *testing.T, workspaceRoot, dir, module, name string, capabilities []string, withModule bool) LaidPlugin {
	t.Helper()

	overlay := filepath.Join(t.TempDir(), name+".yaml")
	body := "apiVersion: aggrega.ai/v1alpha1\nkind: Plugin\nmetadata:\n  name: " + name + "\n"
	if err := os.WriteFile(overlay, []byte(body), 0o644); err != nil {
		t.Fatalf("write the %s overlay: %v", name, err)
	}

	res, err := pluginops.GenCatalog(context.Background(), pluginops.GenCatalogOptions{
		PluginPath:    module,
		OutputDir:     dir,
		OverlayPath:   overlay,
		DryRun:        true,
		WorkspaceRoot: workspaceRoot,
	}, nil)
	if err != nil {
		t.Fatalf("generate the %s entity from %s: %v", name, module, err)
	}
	if len(res.Documents) != 1 {
		t.Fatalf("generating the %s entity produced %d documents, want exactly one", name, len(res.Documents))
	}
	doc := res.Documents[0]
	content := []byte(doc.Content)

	var entity types.Plugin
	if err := yaml.Unmarshal(content, &entity); err != nil {
		t.Fatalf("read back the generated %s entity: %v", name, err)
	}
	if len(capabilities) > 0 {
		entity.Spec.SetCapabilitiesFor(entity.Metadata.PluginType, capabilities)
		if content, err = yaml.Marshal(entity); err != nil {
			t.Fatalf("encode the %s entity with its capabilities narrowed: %v", name, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(doc.Path), 0o755); err != nil {
		t.Fatalf("create the %s entity's directory: %v", name, err)
	}
	if err := os.WriteFile(doc.Path, content, 0o644); err != nil {
		t.Fatalf("write the %s entity: %v", name, err)
	}

	laid := LaidPlugin{
		Reference: entity.ArtifactReference(),
		Identity:  entity.Coordinate() + ":" + entity.Metadata.Version,
		Name:      entity.Metadata.Name,
		Version:   entity.Metadata.Version,
		EntityDir: filepath.Dir(doc.Path),
	}
	laid.Module = filepath.Join(laid.EntityDir, pluginpath.PluginLeaf(laid.Reference))
	if !withModule {
		return laid
	}
	bytes, err := os.ReadFile(module)
	if err != nil {
		t.Fatalf("read the %s module: %v", name, err)
	}
	if err := os.WriteFile(laid.Module, bytes, 0o644); err != nil {
		t.Fatalf("lay the %s module beside its entity: %v", name, err)
	}
	return laid
}

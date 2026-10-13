// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package importopstest lays down, for a test, a catalog folder from which an
// import runs end to end: the reference importer of each agent-flow format and a
// JSON parser, each as a real Plugin entity with its built module beside it — or
// importers generated from those modules under names and versions of the
// caller's choosing — and the workspace configuration attaching them.
//
// It exists so every surface that reaches the import operation — the operation
// itself, the command line, the assistant's tool and the editor session — is
// proven over one and the same folder, built one way. A folder each test
// package assembled for itself would be a copy that agreed with the others on
// the day it was written.
//
// Every entity is composed by the catalog generation the command line runs
// (`aggrega plugin gen-catalog`), from the built modules, so an entity here is
// exactly what an author generating one from their own build gets. Each reference
// importer serves one format, so the format an entity advertises is the one its
// module reports. Each module is laid down under the name the plugin cascade
// reads it by, derived from the entity's own artifact reference rather than
// spelled here.
//
// The parser is the JSON parser this binary carries, generated under a name the
// binary does not embed, so its module can be read only from beside its entity:
// an import over this folder therefore reads the parser the importer calls back
// to through the same tier it reads the importer through, and a resolution that
// silently fell back to the binary's own copy could not satisfy it.
//
// Because every entity is generated from its module, each reference importer's
// entity records the recognition signatures its module declares, and an import
// naming no format over the folder recognises a source by those and nothing else.
// A case needing a YAML source recognised lays the demo importer and the binary's
// YAML parser beside them; a case needing importers whose signatures it chose
// writes their entities without modules, for an import refused before any
// importer runs.
package importopstest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// referenceImporter is the reference importer of one format: the format it
// serves, the built fixture its entities are generated from, and the
// metadata.name the folder's entity for it carries.
type referenceImporter struct {
	format, fixture, name string
}

// referenceImporters are the reference importers, one per format, in the order a
// folder lays them down.
var referenceImporters = []referenceImporter{
	{format: "langflow", fixture: "import-langflow-go.wasm", name: "import-langflow"},
	{format: "flowise", fixture: "import-flowise-go.wasm", name: "import-flowise"},
}

// referenceImporterFor returns the reference importer serving format, failing
// the test when no reference importer serves it.
func referenceImporterFor(t *testing.T, format string) referenceImporter {
	t.Helper()
	for _, r := range referenceImporters {
		if r.format == format {
			return r
		}
	}
	t.Fatalf("no reference importer serves the %q format", format)
	return referenceImporter{}
}

// BuildImporterFixtures builds the fixture of every reference importer. A caller
// that goes on to change the process environment the toolchain reads calls this
// first, so no fixture is built in the changed environment.
func BuildImporterFixtures(t *testing.T) {
	t.Helper()
	for _, r := range referenceImporters {
		wasmtest.EnsureFixtureBuilt(t, r.fixture)
	}
}

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
// reference importer of one format.
type Importer struct {
	// Name is the entity's metadata.name.
	Name string
	// Version is the version the module the entity is generated from reports
	// about its own build, and so the entity's metadata.version. Empty is the
	// fixture's own version.
	Version string
	// Format is the format whose reference importer the entity is generated from,
	// written as the bare format (langflow, flowise) — and so the one format the
	// entity advertises, which is what the catalog selects an importer by.
	Format string
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
// workspaceRoot, generated from the reference importer of its format with its
// module beside it, together with the JSON parser the importers call back to,
// and returns the importers in the order given and the parser.
//
// It builds the fixture of each format asked for, and a stamped build of it for
// each version asked for, so a caller that goes on to change the process
// environment the toolchain reads should call this first.
func LayImporters(t *testing.T, workspaceRoot, dir string, importers ...Importer) ([]LaidPlugin, LaidPlugin) {
	t.Helper()
	laid := make([]LaidPlugin, 0, len(importers))
	for _, imp := range importers {
		ref := referenceImporterFor(t, imp.Format)
		module := wasmtest.EnsureFixtureBuilt(t, ref.fixture)
		if imp.Version != "" {
			module = wasmtest.EnsureStampedFixtureBuilt(t, ref.fixture, imp.Version)
		}
		laid = append(laid, layPlugin(t, workspaceRoot, dir, module, imp.Name, true))
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
	Dir string
	// Importers are the folder's importers: the reference importer of each format,
	// one entity apiece, in the order a folder lays them down.
	Importers []LaidPlugin
	Parser    LaidPlugin
}

// ImporterFor returns the folder's importer for format, failing the test when no
// reference importer serves it.
func (f CatalogFolder) ImporterFor(t *testing.T, format string) LaidPlugin {
	t.Helper()
	for i, r := range referenceImporters {
		if r.format == format && i < len(f.Importers) {
			return f.Importers[i]
		}
	}
	t.Fatalf("the folder holds no importer for the %q format", format)
	return LaidPlugin{}
}

// Plugins returns every entity the folder holds: its importers, then its parser.
func (f CatalogFolder) Plugins() []LaidPlugin {
	return append(append([]LaidPlugin{}, f.Importers...), f.Parser)
}

// LayCatalogFolder lays the folder down at dir, inside the workspace rooted at
// workspaceRoot, and returns what it holds.
//
// withModules false writes every entity and leaves each without its module —
// the case of a module that is not beside its entity.
//
// It builds the importer fixtures, so a caller that goes on to change the
// process environment the toolchain reads should call this first.
//
// There is no skip here beyond the one the fixture build itself carries, and
// that one fires only when the plugin kit's source or its toolchain is absent:
// a binary carrying no JSON parser, or a generation that refuses, fails the
// calling test.
func LayCatalogFolder(t *testing.T, workspaceRoot, dir string, withModules bool) CatalogFolder {
	t.Helper()

	folder := CatalogFolder{Dir: dir}
	for _, r := range referenceImporters {
		module := wasmtest.EnsureFixtureBuilt(t, r.fixture)
		folder.Importers = append(folder.Importers, layPlugin(t, workspaceRoot, dir, module, r.name, withModules))
	}
	folder.Parser = layParser(t, workspaceRoot, dir, withModules)
	return folder
}

// layParser lays the binary's own JSON parser down at dir under a name the binary
// does not embed, with its module beside it when withModule is set.
func layParser(t *testing.T, workspaceRoot, dir string, withModule bool) LaidPlugin {
	t.Helper()
	return layEmbeddedParser(t, workspaceRoot, dir, embeddedJSONParser, ParserName, withModule)
}

// LayYAMLParser lays the binary's own YAML parser down at dir, inside the
// workspace rooted at workspaceRoot, under a name the binary does not embed and
// with its module beside it — so a folder that also holds an importer whose
// recognition signature reads a source as YAML can parse it there.
func LayYAMLParser(t *testing.T, workspaceRoot, dir string) LaidPlugin {
	t.Helper()
	return layEmbeddedParser(t, workspaceRoot, dir, "parser/yaml", "catalog-yaml", true)
}

// layEmbeddedParser lays the parser the binary embeds at coordinate down at dir
// under the entity name name, with its module beside it when withModule is set.
func layEmbeddedParser(t *testing.T, workspaceRoot, dir, coordinate, name string, withModule bool) LaidPlugin {
	t.Helper()
	parserBytes, ok := embedded.Lookup(coordinate)
	if !ok {
		t.Fatalf("this binary carries no %s, so no parser can be laid down beside an entity", coordinate)
	}
	parserModule := filepath.Join(t.TempDir(), name+".wasm")
	if err := os.WriteFile(parserModule, parserBytes, 0o644); err != nil {
		t.Fatalf("write the %s parser module: %v", coordinate, err)
	}
	return layPlugin(t, workspaceRoot, dir, parserModule, name, withModule)
}

// DemoImporterName is the metadata.name the demo importer's entity carries.
const DemoImporterName = "import-demo"

// LayDemoImporter lays the demo importer down at dir, inside the workspace rooted
// at workspaceRoot, generated from its built module with the module beside it.
// The demo importer advertises the demo format and declares one recognition
// signature, which reads a source as YAML and asks for the string DemoSource at
// its top-level kind — so a folder holding it and a YAML parser recognises such a
// source as the demo format with nothing else telling it so.
func LayDemoImporter(t *testing.T, workspaceRoot, dir string) LaidPlugin {
	t.Helper()
	return layPlugin(t, workspaceRoot, dir, wasmtest.EnsureFixtureBuilt(t, "import-go.wasm"), DemoImporterName, true)
}

// ReferenceImporterEntities returns the Plugin entity of the reference importer
// of each format, in the order a folder lays them down, generated from each
// built module exactly as a folder's are — so each carries the recognition
// signatures its module declares — and lays nothing down.
//
// It is for a harness standing in for the catalog: handed as the importers whose
// signatures an import reads, these are the declarations a real catalog folder
// holds rather than a copy of them written beside the harness.
func ReferenceImporterEntities(t *testing.T) []types.Plugin {
	t.Helper()
	out := make([]types.Plugin, 0, len(referenceImporters))
	for _, r := range referenceImporters {
		module := wasmtest.EnsureFixtureBuilt(t, r.fixture)
		entity, _ := generateEntity(t, t.TempDir(), filepath.Join(t.TempDir(), "catalog"), module, r.name)
		out = append(out, entity)
	}
	return out
}

// EmbeddedParsers returns a parser resolution and loader serving each syntax
// from the parser the binary embeds for it — json and yaml among them — read
// straight from the binary, so a harness standing in for the catalog parses
// through a real parser with no catalog on disk. A syntax the binary embeds no
// parser for is refused as a catalog refuses one no parser advertises.
func EmbeddedParsers(t *testing.T) (parsersocket.ParserResolver, parsersocket.ParserLoader) {
	t.Helper()
	res := embeddedParserResolver{}
	load := func(p types.Plugin) (wasm.PluginSpec, error) {
		raw, ok := embedded.Lookup(p.Coordinate())
		if !ok {
			return wasm.PluginSpec{}, fmt.Errorf("this binary embeds no %s", p.Coordinate())
		}
		return wasm.PluginSpec{Name: p.Coordinate(), Bytes: raw}, nil
	}
	return res, load
}

// embeddedParserResolver resolves a syntax to the parser the binary embeds for
// it, by the coordinate the binary keys its parsers under.
type embeddedParserResolver struct{}

func (embeddedParserResolver) ParserPluginForFormat(format string) (types.Plugin, error) {
	if _, ok := embedded.Lookup("parser/" + format); !ok {
		return types.Plugin{}, fmt.Errorf("%w: this binary embeds no parser for %q", catalog.ErrNoParserForFormat(), format)
	}
	return types.Plugin{
		Metadata: types.PluginMetadata{Name: format, Version: "1.0.0", PluginType: "parser"},
		Spec:     types.PluginSpec{Parser: &types.PluginParser{Capabilities: []string{"parse:" + format}}},
	}, nil
}

// WriteImporterEntity writes, at dir, a Plugin entity for an importer that
// advertises formats and records the recognition signatures detect, with no
// module beside it, and returns what it laid. It is the entity of an importer
// no module vouches for — what a recognition reads without ever loading the
// importer, so a case whose import is refused before any importer runs can hold
// importers whose signatures it chose.
func WriteImporterEntity(t *testing.T, dir, name string, formats []string, detect []types.PluginDetectSignature) LaidPlugin {
	t.Helper()
	capabilities := make([]string, 0, len(formats))
	for _, f := range formats {
		capabilities = append(capabilities, "import:"+f)
	}
	entity := types.Plugin{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Plugin",
		Metadata:   types.PluginMetadata{Name: name, Version: "1.0.0", PluginType: "importer"},
		Spec: types.PluginSpec{
			AbiVersion: "v1alpha1",
			Importer:   &types.PluginImporter{Capabilities: capabilities, Detect: detect},
		},
	}
	body, err := yaml.Marshal(entity)
	if err != nil {
		t.Fatalf("encode the %s entity: %v", name, err)
	}
	entityDir := filepath.Join(dir, "importer", name, entity.Metadata.Version)
	if err := os.MkdirAll(entityDir, 0o755); err != nil {
		t.Fatalf("create the %s entity's directory: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(entityDir, "plugin.yaml"), body, 0o644); err != nil {
		t.Fatalf("write the %s entity: %v", name, err)
	}
	return LaidPlugin{
		Reference: entity.ArtifactReference(),
		Identity:  entity.Coordinate() + ":" + entity.Metadata.Version,
		Name:      name,
		Version:   entity.Metadata.Version,
		EntityDir: entityDir,
		Module:    filepath.Join(entityDir, pluginpath.PluginLeaf(entity.ArtifactReference())),
	}
}

// generateEntity generates one entity from the module at module under the
// identity name, addressed beneath dir, and returns it and the document the
// generation produced, writing nothing.
func generateEntity(t *testing.T, workspaceRoot, dir, module, name string) (types.Plugin, authoredcontent.Document) {
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

	var entity types.Plugin
	if err := yaml.Unmarshal([]byte(doc.Content), &entity); err != nil {
		t.Fatalf("read back the generated %s entity: %v", name, err)
	}
	return entity, doc
}

// layPlugin generates one entity from the module at module under the identity
// name, writes it where the generation addresses it beneath dir, and lays the
// module beside it when withModule is set.
//
// The generation runs as a dry run and its one document is then written at the
// path it names, with the content it carries: the bytes and the place are both
// the generation's, and running it as a dry run is what hands them back to be
// read before they are written.
func layPlugin(t *testing.T, workspaceRoot, dir, module, name string, withModule bool) LaidPlugin {
	t.Helper()

	entity, doc := generateEntity(t, workspaceRoot, dir, module, name)
	content := []byte(doc.Content)

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

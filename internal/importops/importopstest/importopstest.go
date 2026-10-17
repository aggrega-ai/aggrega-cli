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
// importer runs. A case importing a directory lays the files-probe importer,
// which declares file patterns and reports back exactly what it was handed.
//
// A case about which parser an import reads through lays the importers with no
// parser beside them, so every syntax is one the catalog serves no parser for, or
// lays the planted parser: a parser of the json syntax that answers one fixed
// tree whatever it is handed, so what an import did with a source says whether
// that parser or another read it.
package importopstest

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

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
// folder lays them down. Each fixture is the compiler's own importer for its
// format, built from its source under the module's plugins/ tree.
var referenceImporters = []referenceImporter{
	{format: "langflow", fixture: "langflow-importer.wasm", name: "import-langflow"},
	{format: "flowise", fixture: "flowise-importer.wasm", name: "import-flowise"},
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
	writeConfiguration(t, root, false, attachments)
}

// WriteOfflineConfiguration is WriteConfiguration for a workspace under the
// offline posture, whose import reaches no registry: one that needs a core
// importer the machine does not hold is refused rather than fetching it.
func WriteOfflineConfiguration(t *testing.T, root string, attachments ...Attachment) {
	t.Helper()
	writeConfiguration(t, root, true, attachments)
}

// writeConfiguration writes the configuration WriteConfiguration and
// WriteOfflineConfiguration describe.
func writeConfiguration(t *testing.T, root string, offline bool, attachments []Attachment) {
	t.Helper()
	doc := map[string]any{"workspace": map[string]any{}}
	if offline {
		doc[workspaceconfig.OfflineSettingKey] = true
	}
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

// Plugins returns every entity the folder holds: its importers, then its parser
// where it holds one.
func (f CatalogFolder) Plugins() []LaidPlugin {
	out := append([]LaidPlugin{}, f.Importers...)
	if f.Parser.Reference != "" {
		out = append(out, f.Parser)
	}
	return out
}

// Attachments returns one plugins.importer entry per importer the folder holds,
// in the order the folder lays them down, each naming its importer by identity
// and version and handing it settings — none where settings is nil, so the entry
// carries no with and the importer it names is handed exactly what an importer
// no entry names is handed.
//
// The binary carries importers of its own that advertise the same formats as the
// folder's, so a workspace whose catalog holds the folder selects one of the
// folder's importers only where an entry names it: with none, an import of either
// format halts as ambiguous between the folder's importer and the binary's.
func (f CatalogFolder) Attachments(settings map[string]any) []Attachment {
	attachments := make([]Attachment, 0, len(f.Importers))
	for _, imp := range f.Importers {
		attachments = append(attachments, Attachment{URI: imp.Identity, Settings: settings})
	}
	return attachments
}

// LayAttachedCatalogFolder lays the folder down at dir, inside the workspace
// rooted at workspaceRoot, as LayCatalogFolder does, and writes the workspace's
// configuration at workspaceRoot with an entry attaching each of the folder's
// importers and handing it settings (see [CatalogFolder.Attachments]). It is the
// workspace in which an import of either format runs the folder's importer: what
// a user whose catalog holds an importer of a format the binary's own importers
// also serve writes to choose it.
//
// The configuration is written before the workspace is resolved, so a caller
// resolves it afterwards; it replaces any configuration already at
// workspaceRoot.
func LayAttachedCatalogFolder(t *testing.T, workspaceRoot, dir string, withModules bool,
	settings map[string]any) CatalogFolder {
	t.Helper()
	folder := LayCatalogFolder(t, workspaceRoot, dir, withModules)
	WriteConfiguration(t, workspaceRoot, folder.Attachments(settings)...)
	return folder
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
// There is no skip here beyond the one the fixture build carries when the
// plugin kit and ABI checkouts beside this repository are not found: the
// importer fixtures are built from this module's own sources with the Go
// toolchain the test already runs under, so a build that fails, a binary
// carrying no JSON parser, or a generation that refuses, fails the calling test.
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

// LayAttachedImporterFolder lays down at dir, inside the workspace rooted at
// workspaceRoot, the reference importer of each format with its module beside it
// and NO parser, and writes the workspace's configuration at workspaceRoot with
// an entry attaching each importer and handing it nothing. The folder's Parser is
// left empty.
//
// It is the workspace whose catalog serves no parser for any syntax: an import
// over it reads its source through whatever parser the import resolves when the
// catalog serves none, or is refused for want of one. A case that wants the
// catalog to serve one lays it beside the importers afterwards.
//
// The configuration is written before the workspace is resolved, so a caller
// resolves it afterwards; it replaces any configuration already at
// workspaceRoot.
func LayAttachedImporterFolder(t *testing.T, workspaceRoot, dir string) CatalogFolder {
	t.Helper()
	folder := CatalogFolder{Dir: dir}
	for _, r := range referenceImporters {
		module := wasmtest.EnsureFixtureBuilt(t, r.fixture)
		folder.Importers = append(folder.Importers, layPlugin(t, workspaceRoot, dir, module, r.name, true))
	}
	WriteConfiguration(t, workspaceRoot, folder.Attachments(nil)...)
	return folder
}

// PlantedParserName is the metadata.name the planted parser's entity carries.
const PlantedParserName = "planted-json"

// PlantedParserNodeType is the type of the one node the planted parser's tree
// carries, whatever source it was handed. A source the planted parser read is
// recognised as the format whose signature asks for arrays at nodes and edges and
// nothing at data.nodes — the Flowise format, among the reference importers — and
// a Flowise import of it drafts one component, whose name carries this type
// lowercased.
const PlantedParserNodeType = "plantedMarkerParser"

// plantedParserArtifact is the name the built planted parser is published under
// among the built fixtures. No other build layout owns it.
const plantedParserArtifact = "planted-json-parser.wasm"

// plantedParserSource is where the planted parser's source sits beneath the
// module root.
var plantedParserSource = filepath.Join("internal", "importops", "testdata", "planted-json-parser")

// PlantedJSONParserModule builds the planted parser and returns the built
// module's path.
//
// The planted parser is a guest kept beside the import operation's tests for one
// claim no real parser makes visible: which parser of a syntax read a source. It
// advertises the json syntax, and its parse export answers one fixed tree for any
// source — arrays at nodes and edges, nothing at data.nodes, and one node of the
// type PlantedParserNodeType — so an import that read a source through it
// recognises and drafts that tree rather than what the source holds.
func PlantedJSONParserModule(t *testing.T) string {
	t.Helper()
	return localTestGuest(t, plantedParserSource, plantedParserArtifact)
}

// LayPlantedJSONParser lays the planted parser ([PlantedJSONParserModule]) down at
// dir, inside the workspace rooted at workspaceRoot, generated from its built
// module with the module beside it, under the name PlantedParserName — a parser
// the workspace's catalog serves for the json syntax.
func LayPlantedJSONParser(t *testing.T, workspaceRoot, dir string) LaidPlugin {
	t.Helper()
	return layPlugin(t, workspaceRoot, dir, PlantedJSONParserModule(t), PlantedParserName, true)
}

// layParser lays the binary's own JSON parser down at dir under a name the binary
// does not embed, with its module beside it when withModule is set.
func layParser(t *testing.T, workspaceRoot, dir string, withModule bool) LaidPlugin {
	t.Helper()
	return layEmbeddedParser(t, workspaceRoot, dir, embeddedJSONParser, ParserName, withModule)
}

// LayJSONParser lays the binary's own JSON parser down at dir, inside the
// workspace rooted at workspaceRoot, under a name the binary does not embed and
// with its module beside it — the parser a folder holding importers that read a
// source as JSON parses it with.
func LayJSONParser(t *testing.T, workspaceRoot, dir string) LaidPlugin {
	t.Helper()
	return layParser(t, workspaceRoot, dir, true)
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

// FilesProbeFormat is the format the files-probe importer advertises.
const FilesProbeFormat = "files-probe"

// FilesProbeImporterName is the metadata.name the files-probe importer's entity
// carries.
const FilesProbeImporterName = "import-files-probe"

// FilesProbeKind is the top-level kind a files-probe source carries, which the
// probe's recognition signature asks for.
const FilesProbeKind = "FilesProbe"

// filesProbeArtifact is the name the built files-probe importer is published
// under among the built fixtures. No other build layout owns it.
const filesProbeArtifact = "files-probe-importer.wasm"

// filesProbeSource is where the files-probe importer's source sits beneath the
// module root.
var filesProbeSource = filepath.Join("internal", "importops", "testdata", "files-probe-importer")

// FilesProbeModule builds the files-probe importer and returns the built module's
// path.
//
// The files-probe importer is a guest kept beside the import operation's tests
// for one claim no reference importer makes: it declares file patterns, so a
// directory import has files to read for it. It advertises the files-probe format
// and one recognition signature — a JSON source named like *.probe.json, or a
// .json file in a directory named probe, whose top-level kind is the string
// FilesProbeKind — and it answers every import with information diagnostics
// reporting what it was handed, in order: "input <base64>" for the whole input
// payload, "primary <base64>" for the primary bytes, and "source <name> <base64>"
// for each named source.
//
// Its source is found by walking up from the directory the test runs in to the
// module root, so every package's tests build the one guest.
func FilesProbeModule(t *testing.T) string {
	t.Helper()
	return localTestGuest(t, filesProbeSource, filesProbeArtifact)
}

// localTestGuest builds the guest whose source sits at source beneath the module
// root and returns the built module's path, published among the built fixtures as
// artifact. The source is found by walking up from the directory the test runs in
// to the module root, so every package's tests build the one guest.
func localTestGuest(t *testing.T, source, artifact string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("read the test's working directory: %v", err)
	}
	for {
		src := filepath.Join(dir, source)
		if _, err := os.Stat(filepath.Join(src, "main.go")); err == nil {
			return wasmtest.EnsureLocalGuestBuilt(t, src, artifact)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no %s above the test's working directory: the guest's source is supposed to ship beside "+
				"the import operation's tests", source)
		}
		dir = parent
	}
}

// FilesProbeEntity returns the files-probe importer's Plugin entity, generated
// from its built module exactly as a folder's is — so it records the signature
// the module declares — and lays nothing down, for a harness standing in for the
// catalog.
func FilesProbeEntity(t *testing.T) types.Plugin {
	t.Helper()
	entity, _ := generateEntity(t, t.TempDir(), filepath.Join(t.TempDir(), "catalog"), FilesProbeModule(t),
		FilesProbeImporterName)
	return entity
}

// LayFilesProbeImporter lays the files-probe importer ([FilesProbeModule]) down at
// dir, inside the workspace rooted at workspaceRoot, generated from its built
// module with the module beside it, so its entity records the signature its
// describe() declares and an import reads the patterns from that record.
func LayFilesProbeImporter(t *testing.T, workspaceRoot, dir string) LaidPlugin {
	t.Helper()
	return layPlugin(t, workspaceRoot, dir, FilesProbeModule(t), FilesProbeImporterName, true)
}

// FilesProbeReport is what the files-probe importer reported it was handed.
type FilesProbeReport struct {
	// Input is the whole input payload, as the importer received it.
	Input []byte
	// Primary is the primary source bytes.
	Primary []byte
	// Sources is every named source, in the order received.
	Sources []abi.ImportSource
}

// ReadFilesProbeReport reads the files-probe importer's report out of the
// messages its diagnostics carry, in order. A message it does not recognise, a
// report missing its input or its primary bytes, or bytes that are not base64,
// fail the test.
func ReadFilesProbeReport(t *testing.T, messages []string) FilesProbeReport {
	t.Helper()
	var r FilesProbeReport
	var sawInput, sawPrimary bool
	decode := func(s string) []byte {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			t.Fatalf("the files-probe importer reported bytes that are not base64 (%q): %v", s, err)
		}
		return b
	}
	for _, m := range messages {
		switch {
		case strings.HasPrefix(m, "input "):
			r.Input, sawInput = decode(strings.TrimPrefix(m, "input ")), true
		case strings.HasPrefix(m, "primary "):
			r.Primary, sawPrimary = decode(strings.TrimPrefix(m, "primary ")), true
		case strings.HasPrefix(m, "source "):
			rest := strings.TrimPrefix(m, "source ")
			cut := strings.LastIndex(rest, " ")
			if cut < 0 {
				t.Fatalf("the files-probe importer reported a source with no bytes: %q", m)
			}
			r.Sources = append(r.Sources, abi.ImportSource{Name: rest[:cut], Bytes: decode(rest[cut+1:])})
		default:
			t.Fatalf("the files-probe importer reported a message it does not write: %q", m)
		}
	}
	if !sawInput || !sawPrimary {
		t.Fatalf("the files-probe importer's report is incomplete (input %v, primary %v): %q", sawInput, sawPrimary,
			messages)
	}
	return r
}

// FilesProbeBody is the content of a source the files-probe importer's signature
// recognises.
func FilesProbeBody(label string) string {
	return `{"kind":"` + FilesProbeKind + `","label":"` + label + `"}`
}

// WriteSource writes body at path, creating the directories holding it, and
// returns path — the place an import of that source is handed.
func WriteSource(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create the directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
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

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package importopstest lays down, for a test, a catalog folder from which an
// import runs end to end: the reference agent-flow importer and a JSON parser,
// each as a real Plugin entity with its built module beside it.
//
// It exists so every surface that reaches the import operation — the operation
// itself, the command line, the assistant's tool and the editor session — is
// proven over one and the same folder, built one way. A folder each test
// package assembled for itself would be a copy that agreed with the others on
// the day it was written.
//
// Both entities are composed by the catalog generation the command line runs
// (`aggrega plugin gen-catalog`), from the built modules, so an entity here is
// exactly what an author generating one from their own build gets. Each module is
// laid down under the name the plugin cascade reads it by, derived from the
// entity's own artifact reference rather than spelled here.
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
)

// ImporterFixture is the built reference agent-flow importer the folder's
// importer entity is generated from.
const ImporterFixture = "import-langflow-go.wasm"

// importerName and parserName are the metadata.name each generated entity
// carries. The parser's is deliberately one the binary does not embed.
const (
	importerName = "import-langflow"
	parserName   = "catalog-json"
)

// embeddedJSONParser is the binary's own JSON parser, whose module the folder's
// parser entity is generated from.
const embeddedJSONParser = "parser/json"

// LaidPlugin is one Plugin entity the folder holds.
type LaidPlugin struct {
	// Reference is the entity's artifact reference: the key the catalog indexes
	// the directory beside the entity under, and the reference the cascade reads
	// its module by.
	Reference string
	// EntityDir is the directory the entity's document sits in.
	EntityDir string
	// Module is where the entity's module sits beside it. It is laid down only
	// when the folder was asked for its modules.
	Module string
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

	parserBytes, ok := embedded.Lookup(embeddedJSONParser)
	if !ok {
		t.Fatalf("this binary carries no %s, so no parser can be laid down beside an entity", embeddedJSONParser)
	}
	parserModule := filepath.Join(t.TempDir(), "json-parser.wasm")
	if err := os.WriteFile(parserModule, parserBytes, 0o644); err != nil {
		t.Fatalf("write the JSON parser module: %v", err)
	}

	return CatalogFolder{
		Dir:      dir,
		Importer: layPlugin(t, workspaceRoot, dir, importerModule, importerName, withModules),
		Parser:   layPlugin(t, workspaceRoot, dir, parserModule, parserName, withModules),
	}
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
	if err := os.MkdirAll(filepath.Dir(doc.Path), 0o755); err != nil {
		t.Fatalf("create the %s entity's directory: %v", name, err)
	}
	if err := os.WriteFile(doc.Path, []byte(doc.Content), 0o644); err != nil {
		t.Fatalf("write the %s entity: %v", name, err)
	}

	var entity types.Plugin
	if err := yaml.Unmarshal([]byte(doc.Content), &entity); err != nil {
		t.Fatalf("read back the generated %s entity: %v", name, err)
	}
	laid := LaidPlugin{
		Reference: entity.ArtifactReference(),
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

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importopstest

import (
	"os"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// SchemaImporters are the identities of the resource-schema importers the
// compiler carries as core importers, one per format.
var SchemaImporters = []string{"importer/cloudformation-schema", "importer/terraform-schema", "importer/arm-schema"}

// CarrySchemaImporters makes the binary under test carry the resource-schema
// importers: each one's Plugin entity as this build generated it, and the digest
// of the module built here from the repository's sources, which is served from
// the binary's embedded tier and is the module the import then accepts and runs. It fails the test where
// the build carries no entity for one of them.
//
// The carried set is process-wide, so a test calling this must not be parallel;
// the set is restored when the test ends.
func CarrySchemaImporters(t *testing.T) {
	t.Helper()
	entities := map[string][]byte{}
	for _, p := range embedded.PublishedPlugins() {
		entities[p.URI] = p.Entity
	}
	var carried []embedded.PublishedPlugin
	for _, uri := range SchemaImporters {
		entity := entities[uri]
		if len(entity) == 0 {
			t.Fatalf("this binary carries no entity for %s: build the core plugins with the repository's build script", uri)
		}
		module, err := os.ReadFile(wasmtest.EnsureFixtureBuilt(t, uri[len("importer/"):]+"-importer.wasm"))
		if err != nil {
			t.Fatalf("read the module of %s: %v", uri, err)
		}
		t.Cleanup(embedded.SeedForTest(uri, module))
		carried = append(carried, embedded.PublishedPlugin{URI: uri, Entity: entity, Digest: digest.ContentDigest(module)})
	}
	t.Cleanup(embedded.SeedPublishedForTest(carried))
}

// SchemaImporterAttachment is the attachment of the resource-schema importer uri
// that hands it settings, at the version the binary under test carries it at. It
// fails the test where the binary carries no entity for uri.
func SchemaImporterAttachment(t *testing.T, uri string, settings map[string]any) Attachment {
	t.Helper()
	for _, p := range embedded.PublishedPlugins() {
		if p.URI != uri {
			continue
		}
		var entity types.Plugin
		if err := yaml.Unmarshal(p.Entity, &entity); err != nil {
			t.Fatalf("read the carried entity of %s: %v", uri, err)
		}
		return Attachment{URI: entity.Coordinate() + ":" + entity.Metadata.Version, Settings: settings}
	}
	t.Fatalf("this binary carries no entity for %s", uri)
	return Attachment{}
}

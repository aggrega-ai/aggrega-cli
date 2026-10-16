// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// carriedImporterEntity is an importer entity named name at version advertising
// import:langflow, described as description so a case can tell which copy of one
// identity and version it was handed.
func carriedImporterEntity(name, version, description string) types.Plugin {
	return types.Plugin{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Plugin",
		Metadata: types.PluginMetadata{
			Name: name, Version: version, PluginType: importerPluginType, Description: description,
		},
		Spec: types.PluginSpec{
			AbiVersion: "v1alpha1",
			Importer:   &types.PluginImporter{Capabilities: []string{"import:langflow"}},
		},
	}
}

// encodeEntity encodes p as the YAML document a catalog directory holds.
func encodeEntity(t *testing.T, p types.Plugin) []byte {
	t.Helper()
	b, err := yaml.Marshal(p)
	if err != nil {
		t.Fatalf("encode %s: %v", p.Coordinate(), err)
	}
	return b
}

// catalogHolding writes each entity into a fresh catalog directory and loads it
// as the import's view is loaded, serving the binary's embedded entities.
func catalogHolding(t *testing.T, entities ...types.Plugin) *Catalog {
	t.Helper()
	dir := t.TempDir()
	for _, p := range entities {
		path := filepath.Join(dir, p.Metadata.PluginType, p.Metadata.Name, p.Metadata.Version, "plugin.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create the directory for %s: %v", p.Coordinate(), err)
		}
		if err := os.WriteFile(path, encodeEntity(t, p), 0o644); err != nil {
			t.Fatalf("write %s: %v", p.Coordinate(), err)
		}
	}
	c, err := LoadAll(dir, dir)
	if err != nil {
		t.Fatalf("load the catalog: %v", err)
	}
	c.UseEmbeddedProviderResolution()
	return c
}

// carriedDigest is the digest the cases below make the binary carry for its
// importer/langflow.
const carriedDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// TestTheImportersTheBinaryCarriesCompeteUnderTheExistingRules makes the binary
// carry importer/langflow at 2.0.0 and resolves the Langflow format on an import's
// view over catalogs holding, in turn, nothing; that identity below, at and above
// the carried version; and an importer of another name.
//
//   - Nothing: the carried importer is chosen, and it is the one whose digest the
//     view reports.
//   - The same identity below: the highest version, the carried one, is chosen;
//     the catalog's entity is not taken for it, and an attachment naming the lower
//     version chooses the catalog's.
//   - The same identity and version: the catalog's entity keeps its place — it is
//     the one chosen and the one recognition reads — and no digest is reported for
//     it.
//   - The same identity above: the catalog's higher version is chosen.
//   - Another name: with no attachment the format is ambiguous between the two,
//     and an attachment naming the catalog's importer chooses it.
//
// 🔴 PLANTED DEFECT: in importerResolutionPlugins
// (pkg/catalog/carried_importers.go), let a carried importer replace the set's
// entity of the same key rather than be left out, and the same-version row fails:
// the carried copy is chosen, recognition reads it, and a digest is reported for
// what should be the catalog's own entity.
func TestTheImportersTheBinaryCarriesCompeteUnderTheExistingRules(t *testing.T) {
	// Not parallel: it seeds the process-wide carried set.
	t.Cleanup(embedded.SeedPublishedForTest([]embedded.PublishedPlugin{{
		URI:    "importer/langflow",
		Entity: encodeEntity(t, carriedImporterEntity("langflow", "2.0.0", "carried")),
		Digest: carriedDigest,
	}}))

	for _, row := range []struct {
		name     string
		holds    []types.Plugin
		attached []string
		// want is the description and version of the importer chosen, carried
		// whether it is the binary's, and ambiguous the halt expected instead.
		want      string
		version   string
		carried   bool
		ambiguous string
	}{
		{name: "a catalog holding none", want: "carried", version: "2.0.0", carried: true},
		{
			name:  "the same identity at a lower version",
			holds: []types.Plugin{carriedImporterEntity("langflow", "1.0.0", "catalog")},
			want:  "carried", version: "2.0.0", carried: true,
		},
		{
			name:     "the same identity at a lower version, attached",
			holds:    []types.Plugin{carriedImporterEntity("langflow", "1.0.0", "catalog")},
			attached: []string{"importer/langflow:1.0.0"},
			want:     "catalog", version: "1.0.0",
		},
		{
			name:  "the same identity and version",
			holds: []types.Plugin{carriedImporterEntity("langflow", "2.0.0", "catalog")},
			want:  "catalog", version: "2.0.0",
		},
		{
			name:  "the same identity at a higher version",
			holds: []types.Plugin{carriedImporterEntity("langflow", "3.0.0", "catalog")},
			want:  "catalog", version: "3.0.0",
		},
		{
			name:      "another name",
			holds:     []types.Plugin{carriedImporterEntity("import-langflow", "1.0.0", "catalog")},
			ambiguous: `format "langflow" is advertised by importer plugins [import-langflow langflow]`,
		},
		{
			name:     "another name, attached",
			holds:    []types.Plugin{carriedImporterEntity("import-langflow", "1.0.0", "catalog")},
			attached: []string{"importer/import-langflow:1.0.0"},
			want:     "catalog", version: "1.0.0",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			view := catalogHolding(t, row.holds...)
			view.UseCarriedImporterResolution()

			got, _, err := view.ImporterPluginForFormat("langflow", row.attached)
			if row.ambiguous != "" {
				if !errors.Is(err, ErrAmbiguousImporterPlugin()) {
					t.Fatalf("the format was not refused as ambiguous: %v", err)
				}
				if !strings.Contains(err.Error(), row.ambiguous) {
					t.Errorf("the halt reads %q; want it to carry %q", err.Error(), row.ambiguous)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve the Langflow importer: %v", err)
			}
			if got.Metadata.Description != row.want || got.Metadata.Version != row.version {
				t.Fatalf("chose the %s importer at %s; want the %s one at %s", got.Metadata.Description,
					got.Metadata.Version, row.want, row.version)
			}
			digest, carried := view.CarriedImporterDigest(got)
			if carried != row.carried || (carried && digest != carriedDigest) {
				t.Errorf("the view reports a carried digest %q (%v) for the chosen importer; want carried=%v",
					digest, carried, row.carried)
			}

			var read []types.Plugin
			for _, p := range view.ImporterPlugins() {
				if p.Coordinate() == got.Coordinate() && p.Metadata.Version == row.version {
					read = append(read, p)
				}
			}
			if len(read) != 1 || read[0].Metadata.Description != row.want {
				t.Errorf("recognition reads %d %s at %s (%+v); want exactly the %s one",
					len(read), got.Coordinate(), row.version, read, row.want)
			}
		})
	}
}

// TestOnlyTheImportsViewServesTheImportersTheBinaryCarries holds the importers
// this build of the binary carries to the import's own view. For each, over a
// catalog holding no plugin:
//
//   - a view as a build or a pull reads it — the whole catalog serving the
//     binary's embedded entities, and the lazy backing — neither resolves the
//     importer by its reference nor resolves its format, and the binary's embedded
//     entities do not include it;
//   - the import's view resolves its format to it and reports its digest, and
//     still does not resolve it by its reference.
//
// It runs over the set the build carries, and fails rather than passing over an
// empty one where the build carries no importer.
func TestOnlyTheImportsViewServesTheImportersTheBinaryCarries(t *testing.T) {
	var carried []types.Plugin
	digests := map[string]string{}
	for _, p := range embedded.PublishedPlugins() {
		var entity types.Plugin
		if err := yaml.Unmarshal(p.Entity, &entity); err != nil {
			t.Fatalf("the entity the binary carries for %s does not decode: %v", p.URI, err)
		}
		if entity.Metadata.PluginType == importerPluginType {
			carried = append(carried, entity)
			digests[entity.Coordinate()] = p.Digest
		}
	}
	if len(carried) == 0 {
		t.Fatal("this binary carries no importer, so nothing here is proven: build the core plugins with the " +
			"repository's build script, which writes what the binary carries of each core plugin it does not embed")
	}

	for _, imp := range carried {
		uri := imp.Coordinate()
		formats := ImporterFormats(imp)
		if len(formats) == 0 {
			t.Fatalf("the importer the binary carries as %s advertises no format", uri)
		}
		t.Run(uri, func(t *testing.T) {
			dir := t.TempDir()

			build := catalogHolding(t)
			if p, ok := build.PluginByURI(uri); ok {
				t.Errorf("a build's view resolves %s by its reference, to %+v", uri, p.Metadata)
			}
			if _, _, err := build.ImporterPluginForFormat(formats[0], nil); !errors.Is(err, ErrNoImporterForFormat()) {
				t.Errorf("a build's view resolves the %s format: %v", formats[0], err)
			}
			if got := build.ImporterPlugins(); len(got) != 0 {
				t.Errorf("a build's view lists importers %v", got)
			}
			lazy := NewLazyLookup(dir, "", []string{dir}, nil)
			if p, ok := lazy.PluginByURI(uri); ok {
				t.Errorf("the lazy backing a build and a pull resolve through resolves %s by its reference, to %+v",
					uri, p.Metadata)
			}
			if slices.Contains(embedded.EntityURIs(), uri) {
				t.Errorf("the binary's embedded entities include %s, whose module it does not carry", uri)
			}

			view := catalogHolding(t)
			view.UseCarriedImporterResolution()
			got, at, err := view.ImporterPluginForFormat(formats[0], nil)
			if err != nil {
				t.Fatalf("the import's view does not resolve the %s format: %v", formats[0], err)
			}
			if got.Coordinate() != uri || at != -1 {
				t.Errorf("the import's view resolved the %s format to %s, attached at %d; want %s, attached by none",
					formats[0], got.Coordinate(), at, uri)
			}
			if d, ok := view.CarriedImporterDigest(got); !ok || d != digests[uri] {
				t.Errorf("the import's view reports the digest %q (%v) for %s; the binary carries %q", d, ok, uri,
					digests[uri])
			}
			if p, ok := view.PluginByURI(uri); ok {
				t.Errorf("the import's view resolves %s by its reference, to %+v", uri, p.Metadata)
			}
		})
	}
}

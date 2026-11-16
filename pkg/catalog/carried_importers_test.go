// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// carriedImporterEntity is an importer entity named name at version advertising
// import:langflow, described as description so a case can tell which copy of one
// identity and version it was handed.
func carriedImporterEntity(name, version, description string) types.Plugin {
	return types.Plugin{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "Plugin",
		Metadata: types.PluginMetadata{
			Name: name, Version: version, PluginType: importerPluginType, Description: description,
		},
		Spec: types.PluginSpec{
			AbiVersion: "v1beta1",
			Importer:   &types.PluginImporter{Capabilities: []string{"import:langflow"}},
		},
	}
}

// alsoAdvertising returns p advertising, beside import:langflow, each format
// given.
func alsoAdvertising(p types.Plugin, formats ...string) types.Plugin {
	capabilities := []string{"import:langflow"}
	for _, format := range formats {
		capabilities = append(capabilities, "import:"+format)
	}
	p.Spec.Importer = &types.PluginImporter{Capabilities: capabilities}
	return p
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
	return catalogHoldingBeside(t, false, entities...)
}

// catalogHoldingBeside is catalogHolding with, where withModules is set, a
// module laid beside each entity under the name the co-located tier reads it by.
// The module's bytes are no guest: nothing here loads it, and only whether it is
// there is read.
func catalogHoldingBeside(t *testing.T, withModules bool, entities ...types.Plugin) *Catalog {
	t.Helper()
	if withModules {
		return catalogHoldingMixed(t, entities, nil)
	}
	return catalogHoldingMixed(t, nil, entities)
}

// catalogHoldingMixed is catalogHolding over beside, each with a module laid
// beside it as catalogHoldingBeside lays one, and bare, each with none.
func catalogHoldingMixed(t *testing.T, beside, bare []types.Plugin) *Catalog {
	t.Helper()
	dir := t.TempDir()
	type laid struct {
		p          types.Plugin
		withModule bool
	}
	var all []laid
	for _, p := range beside {
		all = append(all, laid{p: p, withModule: true})
	}
	for _, p := range bare {
		all = append(all, laid{p: p})
	}
	for _, l := range all {
		p := l.p
		path := filepath.Join(dir, p.Metadata.PluginType, p.Metadata.Name, p.Metadata.Version, "plugin.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create the directory for %s: %v", p.Coordinate(), err)
		}
		if err := os.WriteFile(path, encodeEntity(t, p), 0o644); err != nil {
			t.Fatalf("write %s: %v", p.Coordinate(), err)
		}
		if l.withModule {
			module := filepath.Join(filepath.Dir(path), pluginpath.PluginLeaf(p.ArtifactReference()))
			if err := os.WriteFile(module, []byte("\x00asm"), 0o644); err != nil {
				t.Fatalf("lay the module of %s: %v", p.Coordinate(), err)
			}
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
// carry importer/langflow — at 2.0.0, or at a pre-release where a row says so —
// and resolves the Langflow format on an import's view over catalogs holding, in
// turn, nothing; that identity below, at and above the carried version, each with
// its module beside it and without; and an importer of another name.
//
//   - Nothing: the carried importer is chosen, and it is the one whose digest the
//     view reports.
//   - The same identity below: the highest version, the carried one, is chosen;
//     the catalog's entity is not taken for it, and an attachment naming the lower
//     version chooses the catalog's, with its module beside it or not; a range
//     names no single version, so it does not reach a module-less entity, and
//     with nothing else in it the attachment is refused as unsatisfiable.
//   - The same identity and version, its module beside it: the catalog's entity
//     keeps its place — it is the one chosen and the one recognition reads — and
//     no digest is reported for it.
//   - The same identity and version, no module beside it: the carried importer is
//     served in its place, attached or not, and its digest is reported.
//   - The same identity above: the catalog's higher version is chosen where its
//     module is beside it; where none is, it is no candidate, and the carried one
//     is chosen.
//   - A pre-release carried beside a release: a release with its module beside it
//     is preferred, as any unversioned choice prefers one; a release with no module
//     beside it is no candidate, and the pre-release is chosen.
//   - A higher version with no module beside it that advertises a format the
//     carried importer does not, that format requested: nothing of the identity
//     that can run serves it, so the entity stays the candidate, unattached or
//     attached at exactly its version — the attachment attaching it — and a load
//     refuses it naming its folder rather than the format having no importer.
//     So too where the carried importer advertises that format but a catalog
//     entity with its module, which does not, runs at its key in its place.
//   - Another name: with no attachment the format is ambiguous between the two,
//     and an attachment naming the catalog's importer chooses it.
//
// The attachment chosen is the first one where a row attaches one, and none
// otherwise. Recognition is a separate question from the choice: it reads every
// entity the catalog holds, its module beside it or not, and each carried
// importer whose identity and version the catalog does not hold, in key order,
// whatever is chosen.
//
// 🔴 PLANTED DEFECTS, in pkg/catalog/carried_importers.go and importer_lookup.go:
//   - in importerResolutionPlugins, let a carried importer replace the set's
//     entity of the same key whatever is beside it, and the same-version row with
//     the module beside it fails: the carried copy is chosen, and a digest is
//     reported for what should be the catalog's own entity;
//   - in importerResolutionPlugins, keep every entity importersWithoutModule names,
//     and the rows choosing the carried importer over a module-less entity fail;
//   - in importerResolutionPlugins, pass over a module-less entity whatever the
//     requested format, and the unattached row requesting the format only that
//     entity advertises fails: the format has no importer;
//   - in runnableAdvertisers, count every carried importer's formats, the one a
//     catalog entity with its module runs in place of included, and the row whose
//     held entity lacks the format the carried one adds fails: the module-less
//     entity advertising it is passed over and the format has no importer;
//   - in importerAdvertising, read importerResolutionPlugins("", "") rather than
//     the set for the attachment's capability and version, and the attached row
//     requesting that format fails: the attachment is not taken as attaching it;
//   - in ImporterPlugins, read importerResolutionPlugins("", "") rather than
//     importerRecognitionPlugins(), and every row whose catalog entity has no
//     module beside it fails: recognition loses that entity, or reads the carried
//     one in its place.
func TestTheImportersTheBinaryCarriesCompeteUnderTheExistingRules(t *testing.T) {
	// Not parallel: it seeds the process-wide carried set.
	const prerelease = "2.1.0-dev.3"
	langflowAt := func(version string) []types.Plugin {
		return []types.Plugin{carriedImporterEntity("langflow", version, "catalog")}
	}

	for _, row := range []struct {
		name     string
		holds    []types.Plugin
		modules  bool
		attached []string
		// bare are entities held beside holds with no module, whatever modules
		// says.
		bare []types.Plugin
		// carriedAt is the version the binary carries, 2.0.0 where empty;
		// carriedAlso the formats it advertises beside langflow; and format the
		// format resolved, langflow where empty.
		carriedAt   string
		carriedAlso []string
		format      string
		// want is the description and version of the importer chosen, carried
		// whether it is the binary's, and ambiguous or unsatisfiable the halt
		// expected instead; reads is what recognition reads, in order, each
		// written <name>@<version>=<description>.
		want          string
		version       string
		carried       bool
		ambiguous     string
		unsatisfiable bool
		reads         []string
	}{
		{
			name: "a catalog holding none", want: "carried", version: "2.0.0", carried: true,
			reads: []string{"langflow@2.0.0=carried"},
		},
		{
			name:  "the same identity at a lower version, its module beside it",
			holds: langflowAt("1.0.0"), modules: true,
			want: "carried", version: "2.0.0", carried: true,
			reads: []string{"langflow@1.0.0=catalog", "langflow@2.0.0=carried"},
		},
		{
			name:  "the same identity at a lower version, no module beside it",
			holds: langflowAt("1.0.0"),
			want:  "carried", version: "2.0.0", carried: true,
			reads: []string{"langflow@1.0.0=catalog", "langflow@2.0.0=carried"},
		},
		{
			name:  "the same identity at a lower version, its module beside it, attached",
			holds: langflowAt("1.0.0"), modules: true,
			attached: []string{"importer/langflow:1.0.0"},
			want:     "catalog", version: "1.0.0",
			reads: []string{"langflow@1.0.0=catalog", "langflow@2.0.0=carried"},
		},
		{
			name:     "the same identity at a lower version, no module beside it, attached",
			holds:    langflowAt("1.0.0"),
			attached: []string{"importer/langflow:1.0.0"},
			want:     "catalog", version: "1.0.0",
			reads: []string{"langflow@1.0.0=catalog", "langflow@2.0.0=carried"},
		},
		{
			name:     "the same identity at a lower version, no module beside it, attached by a range",
			holds:    langflowAt("1.0.0"),
			attached: []string{"importer/langflow:^1.0.0"},
			// A range pins no entity, so the module-less 1.0.0 is no candidate and
			// the carried 2.0.0 falls outside the range.
			unsatisfiable: true,
			reads:         []string{"langflow@1.0.0=catalog", "langflow@2.0.0=carried"},
		},
		{
			name:  "the same identity and version, its module beside it",
			holds: langflowAt("2.0.0"), modules: true,
			want: "catalog", version: "2.0.0",
			reads: []string{"langflow@2.0.0=catalog"},
		},
		{
			name:  "the same identity and version, no module beside it",
			holds: langflowAt("2.0.0"),
			want:  "carried", version: "2.0.0", carried: true,
			reads: []string{"langflow@2.0.0=catalog"},
		},
		{
			name:     "the same identity and version, no module beside it, attached",
			holds:    langflowAt("2.0.0"),
			attached: []string{"importer/langflow:2.0.0"},
			want:     "carried", version: "2.0.0", carried: true,
			reads: []string{"langflow@2.0.0=catalog"},
		},
		{
			name:  "the same identity at a higher version, its module beside it",
			holds: langflowAt("3.0.0"), modules: true,
			want: "catalog", version: "3.0.0",
			reads: []string{"langflow@2.0.0=carried", "langflow@3.0.0=catalog"},
		},
		{
			name:  "the same identity at a higher version, no module beside it",
			holds: langflowAt("3.0.0"),
			want:  "carried", version: "2.0.0", carried: true,
			reads: []string{"langflow@2.0.0=carried", "langflow@3.0.0=catalog"},
		},
		{
			name:  "a pre-release carried, a release with its module beside it",
			holds: langflowAt("1.0.0"), modules: true, carriedAt: prerelease,
			want: "catalog", version: "1.0.0",
			reads: []string{"langflow@1.0.0=catalog", "langflow@" + prerelease + "=carried"},
		},
		{
			name:  "a pre-release carried, a release with no module beside it",
			holds: langflowAt("1.0.0"), carriedAt: prerelease,
			want: "carried", version: prerelease, carried: true,
			reads: []string{"langflow@1.0.0=catalog", "langflow@" + prerelease + "=carried"},
		},
		{
			name:   "a higher version, no module beside it, adding a format the carried one lacks, that format requested",
			holds:  []types.Plugin{alsoAdvertising(carriedImporterEntity("langflow", "3.0.0", "catalog"), "flowx")},
			format: "flowx",
			want:   "catalog", version: "3.0.0",
			reads: []string{"langflow@2.0.0=carried", "langflow@3.0.0=catalog"},
		},
		{
			name:     "a higher version, no module beside it, adding a format the carried one lacks, attached",
			holds:    []types.Plugin{alsoAdvertising(carriedImporterEntity("langflow", "3.0.0", "catalog"), "flowx")},
			attached: []string{"importer/langflow:3.0.0"},
			format:   "flowx",
			want:     "catalog", version: "3.0.0",
			reads: []string{"langflow@2.0.0=carried", "langflow@3.0.0=catalog"},
		},
		{
			name:  "a higher version, no module beside it, adding a format the carried one lacks, the shared format requested",
			holds: []types.Plugin{alsoAdvertising(carriedImporterEntity("langflow", "3.0.0", "catalog"), "flowx")},
			want:  "carried", version: "2.0.0", carried: true,
			reads: []string{"langflow@2.0.0=carried", "langflow@3.0.0=catalog"},
		},
		{
			// The carried importer advertises flowx, but the catalog's entity at its
			// key, with its module beside it, runs in its place and does not; so
			// nothing of the identity that can run serves flowx, and the module-less
			// higher version that advertises it stays the candidate.
			name:        "the carried importer's key held with its module, a module-less higher version adding a format the held one lacks",
			holds:       langflowAt("2.0.0"),
			modules:     true,
			bare:        []types.Plugin{alsoAdvertising(carriedImporterEntity("langflow", "3.0.0", "catalog"), "flowx")},
			carriedAlso: []string{"flowx"},
			format:      "flowx",
			want:        "catalog", version: "3.0.0",
			reads: []string{"langflow@2.0.0=catalog", "langflow@3.0.0=catalog"},
		},
		{
			name:      "another name",
			holds:     []types.Plugin{carriedImporterEntity("import-langflow", "1.0.0", "catalog")},
			ambiguous: `format "langflow" is advertised by importer plugins [import-langflow langflow]`,
			reads:     []string{"import-langflow@1.0.0=catalog", "langflow@2.0.0=carried"},
		},
		{
			name:     "another name, attached",
			holds:    []types.Plugin{carriedImporterEntity("import-langflow", "1.0.0", "catalog")},
			attached: []string{"importer/import-langflow:1.0.0"},
			want:     "catalog", version: "1.0.0",
			reads: []string{"import-langflow@1.0.0=catalog", "langflow@2.0.0=carried"},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			carriedAt := row.carriedAt
			if carriedAt == "" {
				carriedAt = "2.0.0"
			}
			t.Cleanup(embedded.SeedPublishedForTest([]embedded.PublishedPlugin{{
				URI:    "importer/langflow",
				Entity: encodeEntity(t, alsoAdvertising(carriedImporterEntity("langflow", carriedAt, "carried"), row.carriedAlso...)),
				Digest: carriedDigest,
			}}))
			beside, bare := row.holds, row.bare
			if !row.modules {
				beside, bare = nil, append(slices.Clone(row.holds), row.bare...)
			}
			view := catalogHoldingMixed(t, beside, bare)
			view.UseCarriedImporterResolution()

			format := row.format
			if format == "" {
				format = "langflow"
			}
			got, at, err := view.ImporterPluginForFormat(format, row.attached)
			if row.ambiguous != "" {
				if !errors.Is(err, ErrAmbiguousImporterPlugin()) {
					t.Fatalf("the format was not refused as ambiguous: %v", err)
				}
				if !strings.Contains(err.Error(), row.ambiguous) {
					t.Errorf("the halt reads %q; want it to carry %q", err.Error(), row.ambiguous)
				}
				assertRecognitionReads(t, view, row.reads)
				return
			}
			if row.unsatisfiable {
				if !errors.Is(err, ErrImporterVersionUnsatisfiable()) {
					t.Fatalf("the attachment's range was not refused as one no version satisfies: %v (chose %s at %s)",
						err, got.Metadata.Description, got.Metadata.Version)
				}
				assertRecognitionReads(t, view, row.reads)
				return
			}
			if err != nil {
				t.Fatalf("resolve the %s importer: %v", format, err)
			}
			if got.Metadata.Description != row.want || got.Metadata.Version != row.version {
				t.Fatalf("chose the %s importer at %s; want the %s one at %s", got.Metadata.Description,
					got.Metadata.Version, row.want, row.version)
			}
			wantAt := -1
			if len(row.attached) > 0 {
				wantAt = 0
			}
			if at != wantAt {
				t.Errorf("the importer is attached by position %d; want %d", at, wantAt)
			}
			digest, carried := view.CarriedImporterDigest(got)
			if carried != row.carried || (carried && digest != carriedDigest) {
				t.Errorf("the view reports a carried digest %q (%v) for the chosen importer; want carried=%v",
					digest, carried, row.carried)
			}

			assertRecognitionReads(t, view, row.reads)
		})
	}
}

// assertRecognitionReads fails the test unless the importers view's recognition
// reads are exactly want, in order, each written <name>@<version>=<description>.
func assertRecognitionReads(t *testing.T, view *Catalog, want []string) {
	t.Helper()
	var read []string
	for _, p := range view.ImporterPlugins() {
		read = append(read, p.Metadata.Name+"@"+p.Metadata.Version+"="+p.Metadata.Description)
	}
	if !slices.Equal(read, want) {
		t.Errorf("recognition reads %v; want %v", read, want)
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
	wasmtest.RequireEmbeddedEntities(t)
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

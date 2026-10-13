// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func importerPlugin(name, version string, capabilities ...string) types.Plugin {
	return types.Plugin{
		Metadata: types.PluginMetadata{Name: name, Version: version, PluginType: "importer"},
		Spec:     types.PluginSpec{Importer: &types.PluginImporter{Capabilities: capabilities}},
	}
}

// TestImporterPluginForFormat_ResolvesAdvertisedFormat pins the happy path: an
// importer plugin advertising import:demo resolves for the "demo" format, and one
// advertising several formats resolves for each, with no attachment attaching it.
func TestImporterPluginForFormat_ResolvesAdvertisedFormat(t *testing.T) {
	t.Parallel()
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"demo-importer:1.0.0":  importerPlugin("demo-importer", "1.0.0", "import:demo"),
		"multi-importer:1.0.0": importerPlugin("multi-importer", "1.0.0", "import:langflow", "import:flowise"),
	}}

	got, at, err := c.ImporterPluginForFormat("demo", nil)
	if err != nil {
		t.Fatalf("resolve demo: %v", err)
	}
	if got.Metadata.Name != "demo-importer" || at != -1 {
		t.Fatalf("resolved %q attached by %d; want demo-importer attached by none (-1)", got.Metadata.Name, at)
	}

	for _, format := range []string{"langflow", "flowise"} {
		got, _, err := c.ImporterPluginForFormat(format, nil)
		if err != nil {
			t.Fatalf("resolve %s: %v", format, err)
		}
		if got.Metadata.Name != "multi-importer" {
			t.Fatalf("format %s resolved %q; want multi-importer", format, got.Metadata.Name)
		}
	}
}

// TestImporterPluginForFormat_UnknownFormatHaltsLoud pins the loud
// no-importer-for-format halt: an unadvertised format returns
// ErrNoImporterForFormat rather than a silent miss.
func TestImporterPluginForFormat_UnknownFormatHaltsLoud(t *testing.T) {
	t.Parallel()
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"demo-importer:1.0.0": importerPlugin("demo-importer", "1.0.0", "import:demo"),
	}}

	_, _, err := c.ImporterPluginForFormat("nonexistent", nil)
	if !errors.Is(err, catalog.ErrNoImporterForFormat()) {
		t.Fatalf("err = %v; want ErrNoImporterForFormat", err)
	}
}

// TestImporterPluginForFormat_AmbiguousHaltsLoud pins the ambiguity halt: two
// distinct importer plugins advertising the same format, with no attachment
// naming either, return ErrAmbiguousImporterPlugin, naming both candidates.
func TestImporterPluginForFormat_AmbiguousHaltsLoud(t *testing.T) {
	t.Parallel()
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"demo-a:1.0.0": importerPlugin("demo-a", "1.0.0", "import:demo"),
		"demo-b:1.0.0": importerPlugin("demo-b", "1.0.0", "import:demo"),
	}}

	_, _, err := c.ImporterPluginForFormat("demo", nil)
	if !errors.Is(err, catalog.ErrAmbiguousImporterPlugin()) {
		t.Fatalf("err = %v; want ErrAmbiguousImporterPlugin", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "demo-a") || !strings.Contains(msg, "demo-b") {
		t.Fatalf("ambiguity error must name both candidates; got: %s", msg)
	}
}

// TestImporterPluginForFormat_SelectsHighestVersion pins version selection
// through the shared select: two versions of one importer plugin resolve to the
// highest when no attachment pins one.
func TestImporterPluginForFormat_SelectsHighestVersion(t *testing.T) {
	t.Parallel()
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"demo-importer:1.0.0": importerPlugin("demo-importer", "1.0.0", "import:demo"),
		"demo-importer:2.1.0": importerPlugin("demo-importer", "2.1.0", "import:demo"),
	}}

	got, _, err := c.ImporterPluginForFormat("demo", nil)
	if err != nil {
		t.Fatalf("resolve demo: %v", err)
	}
	if got.Metadata.Version != "2.1.0" {
		t.Fatalf("resolved version %q; want 2.1.0", got.Metadata.Version)
	}
}

// TestImporterPluginForFormat_IgnoresNonImporterTypes pins the plugin-type
// filter, both directions: a provider plugin whose capabilities happen to list an
// import:<format> string is not matched by the importer resolver, and the
// importer resolver never resolves a bare provider capability. The provider
// resolution path is a separate diagnostic vocabulary over the same core and
// stays untouched.
func TestImporterPluginForFormat_IgnoresNonImporterTypes(t *testing.T) {
	t.Parallel()
	provider := types.Plugin{
		Metadata: types.PluginMetadata{Name: "odd-provider", Version: "1.0.0", PluginType: "provider"},
		Spec:     types.PluginSpec{Provider: types.PluginProvider{Capabilities: []string{"import:demo", "helm"}}},
	}
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"odd-provider:1.0.0":  provider,
		"demo-importer:1.0.0": importerPlugin("demo-importer", "1.0.0", "import:demo"),
	}}

	got, _, err := c.ImporterPluginForFormat("demo", nil)
	if err != nil {
		t.Fatalf("resolve demo: %v", err)
	}
	if got.Metadata.Name != "demo-importer" {
		t.Fatalf("resolved %q; want demo-importer (the importer, not the provider)", got.Metadata.Name)
	}

	// The importer resolver never resolves a bare provider capability like "helm".
	if _, _, err := c.ImporterPluginForFormat("helm", nil); !errors.Is(err, catalog.ErrNoImporterForFormat()) {
		t.Fatalf("resolving provider capability as a format: err = %v; want ErrNoImporterForFormat", err)
	}
}

// TestImporterPluginForFormat_AnAttachmentNamingAnAdvertiserAttachesIt pins what
// the attachments' uris do to the resolution, one row per shape, over one
// catalog: three importers advertising the demo format — one of them at two
// versions — an importer advertising another format, a provider whose
// capabilities list the demo format, and a parser.
//
// An attachment attaches an importer when its uri, with any version split off,
// is that importer's coordinate and the importer advertises the format asked
// for: it is then chosen however many importers advertise the format, a version
// on its uri pins the version, and its position is the one reported. Any other
// attachment attaches nothing: with the demo importers' ambiguity left standing,
// it changes nothing about the halt, and next to a single advertiser it changes
// nothing about the resolution.
func TestImporterPluginForFormat_AnAttachmentNamingAnAdvertiserAttachesIt(t *testing.T) {
	t.Parallel()
	shared := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"demo-a:1.0.0":       importerPlugin("demo-a", "1.0.0", "import:demo"),
		"demo-b:1.0.0":       importerPlugin("demo-b", "1.0.0", "import:demo"),
		"demo-b:2.0.0":       importerPlugin("demo-b", "2.0.0", "import:demo"),
		"demo-c:1.0.0":       importerPlugin("demo-c", "1.0.0", "import:demo"),
		"other-format:1.0.0": importerPlugin("other-format", "1.0.0", "import:other"),
		"odd-provider:1.0.0": {
			Metadata: types.PluginMetadata{Name: "odd-provider", Version: "1.0.0", PluginType: "provider"},
			Spec:     types.PluginSpec{Provider: types.PluginProvider{Capabilities: []string{"import:demo"}}},
		},
		"json:1.0.0": {
			Metadata: types.PluginMetadata{Name: "json", Version: "1.0.0", PluginType: "parser"},
			Spec:     types.PluginSpec{Parser: &types.PluginParser{Capabilities: []string{"parse:json"}}},
		},
	}}
	single := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"demo-a:1.0.0":       importerPlugin("demo-a", "1.0.0", "import:demo"),
		"other-format:1.0.0": importerPlugin("other-format", "1.0.0", "import:other"),
	}}

	for _, tc := range []struct {
		name     string
		cat      *catalog.Catalog
		attached []string
		// wantName and wantVersion name the importer resolved; wantAmbiguous,
		// when set, is the candidate list the ambiguity halt must name instead.
		wantName, wantVersion string
		wantAt                int
		wantAmbiguous         string
	}{
		{name: "one attachment naming one of three advertisers", cat: shared,
			attached: []string{"importer/demo-c:1.0.0"}, wantName: "demo-c", wantVersion: "1.0.0", wantAt: 0},
		{name: "the advertiser named after an attachment naming nothing it holds", cat: shared,
			attached: []string{"importer/elsewhere:1.0.0", "importer/demo-a:1.0.0"},
			wantName: "demo-a", wantVersion: "1.0.0", wantAt: 1},
		{name: "a version on the uri pins that version", cat: shared,
			attached: []string{"importer/demo-b:1.0.0"}, wantName: "demo-b", wantVersion: "1.0.0", wantAt: 0},
		{name: "a range on the uri chooses the highest in it", cat: shared,
			attached: []string{"importer/demo-b:^1.0.0"}, wantName: "demo-b", wantVersion: "1.0.0", wantAt: 0},
		{name: "a uri naming no version chooses the highest", cat: shared,
			attached: []string{"importer/demo-b"}, wantName: "demo-b", wantVersion: "2.0.0", wantAt: 0},
		{name: "attachments naming two advertisers", cat: shared,
			attached:      []string{"importer/demo-c:1.0.0", "importer/demo-a:1.0.0"},
			wantAmbiguous: "[demo-a demo-c]"},
		{name: "an attachment naming the importer of another format", cat: shared,
			attached: []string{"importer/other-format:1.0.0"}, wantAmbiguous: "[demo-a demo-b demo-c]"},
		{name: "an attachment naming the provider listing the format", cat: shared,
			attached: []string{"provider/odd-provider:1.0.0"}, wantAmbiguous: "[demo-a demo-b demo-c]"},
		{name: "an attachment naming the parser", cat: shared,
			attached: []string{"parser/json:1.0.0"}, wantAmbiguous: "[demo-a demo-b demo-c]"},
		{name: "the sole attachment naming the importer of another format", cat: single,
			attached: []string{"importer/other-format:1.0.0"}, wantName: "demo-a", wantVersion: "1.0.0", wantAt: -1},
		{name: "the sole attachment naming nothing the catalog holds", cat: single,
			attached: []string{"importer/elsewhere:1.0.0"}, wantName: "demo-a", wantVersion: "1.0.0", wantAt: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, at, err := tc.cat.ImporterPluginForFormat("demo", tc.attached)
			if tc.wantAmbiguous != "" {
				if !errors.Is(err, catalog.ErrAmbiguousImporterPlugin()) {
					t.Fatalf("err = %v; want ErrAmbiguousImporterPlugin", err)
				}
				if want := `format "demo" is advertised by importer plugins ` + tc.wantAmbiguous; !strings.Contains(err.Error(), want) {
					t.Fatalf("the ambiguity halt reads %q; want it to carry %q", err.Error(), want)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve demo: %v", err)
			}
			if got.Metadata.Name != tc.wantName || got.Metadata.Version != tc.wantVersion || at != tc.wantAt {
				t.Fatalf("resolved %s at %s attached by position %d; want %s at %s attached by position %d",
					got.Metadata.Name, got.Metadata.Version, at, tc.wantName, tc.wantVersion, tc.wantAt)
			}
		})
	}
}

// TestImporterPluginForFormat_AnAttachmentPinningAVersionTheCatalogDoesNotHoldHalts
// pins that an attachment naming an advertiser at a version no advertised version
// of it satisfies halts rather than resolving to some other version, naming the
// version it pins, under the importer's own unsatisfiable-pin condition — and not
// under the no-advertiser one, whose sentence would be false here: the importer
// does advertise the format, at other versions.
func TestImporterPluginForFormat_AnAttachmentPinningAVersionTheCatalogDoesNotHoldHalts(t *testing.T) {
	t.Parallel()
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"demo-importer:1.0.0": importerPlugin("demo-importer", "1.0.0", "import:demo"),
		"demo-importer:2.1.0": importerPlugin("demo-importer", "2.1.0", "import:demo"),
	}}

	for _, pin := range []string{"9.9.9", "^3.0.0"} {
		t.Run(pin, func(t *testing.T) {
			t.Parallel()
			got, at, err := c.ImporterPluginForFormat("demo", []string{"importer/demo-importer:" + pin})
			if err == nil {
				t.Fatalf("resolved %s at %s attached by position %d; want a halt", got.Metadata.Name,
					got.Metadata.Version, at)
			}
			if !errors.Is(err, catalog.ErrImporterVersionUnsatisfiable()) {
				t.Errorf("err = %v; want it reported under ErrImporterVersionUnsatisfiable", err)
			}
			if errors.Is(err, catalog.ErrNoImporterForFormat()) {
				t.Errorf("err = %v; it is reported under ErrNoImporterForFormat too, whose sentence is false for an advertised importer", err)
			}
			if !strings.Contains(err.Error(), `pins "`+pin+`"`) {
				t.Errorf("the halt does not name the version pinned: %v", err)
			}
		})
	}
}

// TestImporterPluginsListsEveryImporterInKeyOrder holds the set an import reads
// recognition signatures from: every importer the view resolves among, every
// version included and no plugin of another type, ordered by the key the catalog
// indexes each under rather than by the map holding them — so one catalog lists
// them in one order on every read. ImporterFormats reads each one's formats with
// the import prefix taken off, in the order the entity lists them.
func TestImporterPluginsListsEveryImporterInKeyOrder(t *testing.T) {
	t.Parallel()
	parser := types.Plugin{
		Metadata: types.PluginMetadata{Name: "json", Version: "1.0.0", PluginType: "parser"},
		Spec:     types.PluginSpec{Parser: &types.PluginParser{Capabilities: []string{"parse:json"}}},
	}
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"importer/zeta:1.0.0":  importerPlugin("zeta", "1.0.0", "import:z"),
		"importer/alpha:2.0.0": importerPlugin("alpha", "2.0.0", "import:b", "import:a"),
		"importer/alpha:1.0.0": importerPlugin("alpha", "1.0.0", "import:b"),
		"parser/json:1.0.0":    parser,
	}}
	for range 3 {
		var got []string
		for _, p := range c.ImporterPlugins() {
			got = append(got, p.Metadata.Name+":"+p.Metadata.Version)
		}
		if want := []string{"alpha:1.0.0", "alpha:2.0.0", "zeta:1.0.0"}; strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("ImporterPlugins listed %v; want %v", got, want)
		}
	}
	if got := catalog.ImporterFormats(importerPlugin("alpha", "2.0.0", "import:b", "import:a")); strings.Join(got, ",") != "b,a" {
		t.Errorf("ImporterFormats = %v; want [b a]", got)
	}
	if got := catalog.ImporterFormats(parser); got != nil {
		t.Errorf("a parser's formats were read as import formats: %v", got)
	}
}

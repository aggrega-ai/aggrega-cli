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
		Spec:     types.PluginSpec{Capabilities: capabilities},
	}
}

// TestImporterPluginForFormat_ResolvesAdvertisedFormat pins the happy path: an
// importer plugin advertising import:demo resolves for the "demo" format, and one
// advertising several formats resolves for each.
func TestImporterPluginForFormat_ResolvesAdvertisedFormat(t *testing.T) {
	t.Parallel()
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"demo-importer:1.0.0":  importerPlugin("demo-importer", "1.0.0", "import:demo"),
		"multi-importer:1.0.0": importerPlugin("multi-importer", "1.0.0", "import:langflow", "import:flowise"),
	}}

	got, err := c.ImporterPluginForFormat("demo")
	if err != nil {
		t.Fatalf("resolve demo: %v", err)
	}
	if got.Metadata.Name != "demo-importer" {
		t.Fatalf("resolved %q; want demo-importer", got.Metadata.Name)
	}

	for _, format := range []string{"langflow", "flowise"} {
		got, err := c.ImporterPluginForFormat(format)
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

	_, err := c.ImporterPluginForFormat("nonexistent")
	if !errors.Is(err, catalog.ErrNoImporterForFormat()) {
		t.Fatalf("err = %v; want ErrNoImporterForFormat", err)
	}
}

// TestImporterPluginForFormat_AmbiguousHaltsLoud pins the ambiguity halt: two
// distinct importer plugins advertising the same format return
// ErrAmbiguousImporterPlugin, naming both candidates.
func TestImporterPluginForFormat_AmbiguousHaltsLoud(t *testing.T) {
	t.Parallel()
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"demo-a:1.0.0": importerPlugin("demo-a", "1.0.0", "import:demo"),
		"demo-b:1.0.0": importerPlugin("demo-b", "1.0.0", "import:demo"),
	}}

	_, err := c.ImporterPluginForFormat("demo")
	if !errors.Is(err, catalog.ErrAmbiguousImporterPlugin()) {
		t.Fatalf("err = %v; want ErrAmbiguousImporterPlugin", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "demo-a") || !strings.Contains(msg, "demo-b") {
		t.Fatalf("ambiguity error must name both candidates; got: %s", msg)
	}
}

// TestImporterPluginForFormat_SelectsHighestVersion pins version selection
// through the shared select: two versions of one importer plugin resolve to the
// highest.
func TestImporterPluginForFormat_SelectsHighestVersion(t *testing.T) {
	t.Parallel()
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"demo-importer:1.0.0": importerPlugin("demo-importer", "1.0.0", "import:demo"),
		"demo-importer:2.1.0": importerPlugin("demo-importer", "2.1.0", "import:demo"),
	}}

	got, err := c.ImporterPluginForFormat("demo")
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
		Spec:     types.PluginSpec{Capabilities: []string{"import:demo", "helm"}},
	}
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"odd-provider:1.0.0":  provider,
		"demo-importer:1.0.0": importerPlugin("demo-importer", "1.0.0", "import:demo"),
	}}

	got, err := c.ImporterPluginForFormat("demo")
	if err != nil {
		t.Fatalf("resolve demo: %v", err)
	}
	if got.Metadata.Name != "demo-importer" {
		t.Fatalf("resolved %q; want demo-importer (the importer, not the provider)", got.Metadata.Name)
	}

	// The importer resolver never resolves a bare provider capability like "helm".
	if _, err := c.ImporterPluginForFormat("helm"); !errors.Is(err, catalog.ErrNoImporterForFormat()) {
		t.Fatalf("resolving provider capability as a format: err = %v; want ErrNoImporterForFormat", err)
	}
}

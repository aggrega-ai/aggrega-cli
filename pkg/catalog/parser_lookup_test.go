// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func parserPlugin(name, version string, capabilities ...string) types.Plugin {
	return types.Plugin{
		Metadata: types.PluginMetadata{Name: name, Version: version, PluginType: "parser"},
		Spec:     types.PluginSpec{Capabilities: capabilities},
	}
}

// TestParserPluginForFormat_ResolvesAdvertisedFormat pins the happy path: a
// parser plugin advertising parse:hcl resolves for the "hcl" format, and one
// advertising several formats resolves for each.
func TestParserPluginForFormat_ResolvesAdvertisedFormat(t *testing.T) {
	t.Parallel()
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"hcl-parser:1.0.0":   parserPlugin("hcl-parser", "1.0.0", "parse:hcl"),
		"multi-parser:1.0.0": parserPlugin("multi-parser", "1.0.0", "parse:toml", "parse:yaml"),
	}}

	got, err := c.ParserPluginForFormat("hcl")
	if err != nil {
		t.Fatalf("resolve hcl: %v", err)
	}
	if got.Metadata.Name != "hcl-parser" {
		t.Fatalf("resolved %q; want hcl-parser", got.Metadata.Name)
	}

	for _, format := range []string{"toml", "yaml"} {
		got, err := c.ParserPluginForFormat(format)
		if err != nil {
			t.Fatalf("resolve %s: %v", format, err)
		}
		if got.Metadata.Name != "multi-parser" {
			t.Fatalf("format %s resolved %q; want multi-parser", format, got.Metadata.Name)
		}
	}
}

// TestParserPluginForFormat_UnknownFormatHaltsLoud pins the loud
// no-parser-for-format halt: an unadvertised format returns ErrNoParserForFormat
// rather than a silent miss.
func TestParserPluginForFormat_UnknownFormatHaltsLoud(t *testing.T) {
	t.Parallel()
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"hcl-parser:1.0.0": parserPlugin("hcl-parser", "1.0.0", "parse:hcl"),
	}}

	_, err := c.ParserPluginForFormat("nonexistent")
	if !errors.Is(err, catalog.ErrNoParserForFormat) {
		t.Fatalf("err = %v; want ErrNoParserForFormat", err)
	}
}

// TestParserPluginForFormat_AmbiguousHaltsLoud pins the ambiguity halt: two
// distinct parser plugins advertising the same format return
// ErrAmbiguousParserPlugin.
func TestParserPluginForFormat_AmbiguousHaltsLoud(t *testing.T) {
	t.Parallel()
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"hcl-a:1.0.0": parserPlugin("hcl-a", "1.0.0", "parse:hcl"),
		"hcl-b:1.0.0": parserPlugin("hcl-b", "1.0.0", "parse:hcl"),
	}}

	_, err := c.ParserPluginForFormat("hcl")
	if !errors.Is(err, catalog.ErrAmbiguousParserPlugin) {
		t.Fatalf("err = %v; want ErrAmbiguousParserPlugin", err)
	}
}

// TestParserPluginForFormat_SelectsHighestVersion pins version selection: two
// versions of one parser plugin resolve to the highest.
func TestParserPluginForFormat_SelectsHighestVersion(t *testing.T) {
	t.Parallel()
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"hcl-parser:1.0.0": parserPlugin("hcl-parser", "1.0.0", "parse:hcl"),
		"hcl-parser:2.1.0": parserPlugin("hcl-parser", "2.1.0", "parse:hcl"),
	}}

	got, err := c.ParserPluginForFormat("hcl")
	if err != nil {
		t.Fatalf("resolve hcl: %v", err)
	}
	if got.Metadata.Version != "2.1.0" {
		t.Fatalf("resolved version %q; want 2.1.0", got.Metadata.Version)
	}
}

// TestParserPluginForFormat_IgnoresNonParserTypes pins the plugin-type filter,
// both directions: a provider plugin whose capabilities happen to list a
// parse:<format> string is not matched by the parser resolver, and the parser
// resolver never resolves a bare provider capability. The provider resolution
// path is a separate sibling and stays untouched.
func TestParserPluginForFormat_IgnoresNonParserTypes(t *testing.T) {
	t.Parallel()
	// A provider plugin whose capabilities list a parse:<format> string.
	provider := types.Plugin{
		Metadata: types.PluginMetadata{Name: "odd-provider", Version: "1.0.0", PluginType: "provider"},
		Spec:     types.PluginSpec{Capabilities: []string{"parse:hcl", "helm"}},
	}
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"odd-provider:1.0.0": provider,
		"hcl-parser:1.0.0":   parserPlugin("hcl-parser", "1.0.0", "parse:hcl"),
	}}

	got, err := c.ParserPluginForFormat("hcl")
	if err != nil {
		t.Fatalf("resolve hcl: %v", err)
	}
	if got.Metadata.Name != "hcl-parser" {
		t.Fatalf("resolved %q; want hcl-parser (the parser, not the provider)", got.Metadata.Name)
	}

	// The parser resolver never resolves a bare provider capability like "helm".
	if _, err := c.ParserPluginForFormat("helm"); !errors.Is(err, catalog.ErrNoParserForFormat) {
		t.Fatalf("resolving provider capability as a format: err = %v; want ErrNoParserForFormat", err)
	}
}

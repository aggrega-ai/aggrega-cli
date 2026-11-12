// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package parsersocket_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// demoParserCatalog builds a catalog holding the demo parser fixture as a
// pluginType: parser Plugin entity advertising parse:demo, plus a loader that
// maps that entity to the built fixture .wasm. This is a test stand-in for the
// byte source a core (embedded) or pulled parser supplies in production.
func demoParserCatalog(fixturePath string) (*catalog.Catalog, parsersocket.ParserLoader) {
	c := &catalog.Catalog{Plugins: map[string]types.Plugin{
		"demo-parser:1.0.0": {
			Metadata: types.PluginMetadata{Name: "demo-parser", Version: "1.0.0", PluginType: "parser"},
			Spec:     types.PluginSpec{Parser: &types.PluginParser{Capabilities: []string{"parse:demo"}}},
		},
	}}
	load := func(p types.Plugin) (wasm.PluginSpec, error) {
		return wasm.PluginSpec{Name: p.Coordinate(), Path: fixturePath}, nil
	}
	return c, load
}

// TestParseArtifact_ResolvesAndRuns exercises the whole host orchestration
// end-to-end: the real catalog resolver matches parse:demo, the host loads the
// parser guest hermetically, invokes parse with a ParseInput, and decodes the
// ParseOutput tree. The demo parser echoes {format, source}, so the tree reflects
// the request — proving the resolve → load → run → decode path returns a correct
// tree.
func TestParseArtifact_ResolvesAndRuns(t *testing.T) {
	t.Parallel()
	fixture := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")
	c, load := demoParserCatalog(fixture)

	out, err := parsersocket.ParseArtifact(context.Background(), c, load, "demo", []byte("hello world"), nil)
	if err != nil {
		t.Fatalf("ParseArtifact: %v", err)
	}

	var tree struct {
		Format string `json:"format"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(out.Tree, &tree); err != nil {
		t.Fatalf("decode tree %s: %v", out.Tree, err)
	}
	if tree.Format != "demo" || tree.Source != "hello world" {
		t.Fatalf("tree = %+v; want {demo, hello world}", tree)
	}
}

// TestParseArtifact_Deterministic pins the determinism invariant: the hermetic
// parser invocation is byte-deterministic, so the same source and format yield a
// byte-identical tree across two independent invocations.
func TestParseArtifact_Deterministic(t *testing.T) {
	t.Parallel()
	fixture := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")
	c, load := demoParserCatalog(fixture)

	first, err := parsersocket.ParseArtifact(context.Background(), c, load, "demo", []byte("repeatable"), nil)
	if err != nil {
		t.Fatalf("first ParseArtifact: %v", err)
	}
	second, err := parsersocket.ParseArtifact(context.Background(), c, load, "demo", []byte("repeatable"), nil)
	if err != nil {
		t.Fatalf("second ParseArtifact: %v", err)
	}
	if !bytes.Equal(first.Tree, second.Tree) {
		t.Fatalf("non-deterministic tree:\n first: %s\nsecond: %s", first.Tree, second.Tree)
	}
}

// TestParseArtifact_UnknownFormatHaltsLoud pins the loud-halt contract: an
// unadvertised format halts through the resolver's no-parser sentinel rather than
// silently passing the source through unparsed. The loader is never reached.
func TestParseArtifact_UnknownFormatHaltsLoud(t *testing.T) {
	t.Parallel()
	c, _ := demoParserCatalog("unused.wasm")
	loaderCalled := false
	load := func(p types.Plugin) (wasm.PluginSpec, error) {
		loaderCalled = true
		return wasm.PluginSpec{}, nil
	}

	_, err := parsersocket.ParseArtifact(context.Background(), c, load, "unknown", []byte("x"), nil)
	if !errors.Is(err, catalog.ErrNoParserForFormat()) {
		t.Fatalf("err = %v; want ErrNoParserForFormat", err)
	}
	if loaderCalled {
		t.Fatal("loader must not be reached for an unresolved format")
	}
}

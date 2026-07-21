// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importersocket_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// errNoImporterForFormat is the stub resolver's not-found halt. The real
// catalog resolver arrives in a later increment with its own sentinel; this
// test depends only on the loud-halt contract — an unresolved format returns an
// error and the loader is never reached — not on any specific sentinel.
var errNoImporterForFormat = errors.New("no importer plugin advertises the requested format")

// stubImporterCatalog resolves the format "demo" to the demo importer fixture
// as a pluginType: importer Plugin entity advertising import:demo, plus a loader
// that maps that entity to the built fixture .wasm. Any other format returns the
// not-found halt. This is a test stand-in for the byte source a core (embedded)
// or pulled importer supplies in production, since the real catalog resolver
// does not exist yet.
type stubImporterCatalog struct{}

func (stubImporterCatalog) ImporterPluginForFormat(format string) (types.Plugin, error) {
	if format != "demo" {
		return types.Plugin{}, errNoImporterForFormat
	}
	return types.Plugin{
		Metadata: types.PluginMetadata{Name: "demo-importer", Version: "1.0.0", PluginType: "importer"},
		Spec:     types.PluginSpec{Capabilities: []string{"import:demo"}},
	}, nil
}

func demoImporterLoader(fixturePath string) importersocket.ImporterLoader {
	return func(p types.Plugin) (wasm.PluginSpec, error) {
		return wasm.PluginSpec{Name: p.Coordinate(), Path: fixturePath}, nil
	}
}

// parserResolverFunc adapts a plain function to the ParserResolver interface so
// a test can record the format the importer's host-mediated parse callback asks
// for.
type parserResolverFunc func(format string) (types.Plugin, error)

func (f parserResolverFunc) ParserPluginForFormat(format string) (types.Plugin, error) {
	return f(format)
}

// parserSeamProbe records what the injected parse callback drove through the
// parser socket: the syntactic formats the resolver was consulted for and the
// parser coordinates the loader was asked to load. A test reads it to confirm
// the importer obtained its tree host-mediated rather than parsing in-guest.
type parserSeamProbe struct {
	resolvedFormats []string
	loadedCoords    []string
}

// demoParserSeam resolves the format "json" to the demo parser fixture and maps
// it to the built parse fixture .wasm, recording every resolve and load into the
// returned probe. The probe importer fixture requests "json" through the parse
// callback, so this seam stands in for the parser socket a consumer wires in.
func demoParserSeam(fixturePath string) (parsersocket.ParserResolver, parsersocket.ParserLoader, *parserSeamProbe) {
	probe := &parserSeamProbe{}
	res := parserResolverFunc(func(format string) (types.Plugin, error) {
		probe.resolvedFormats = append(probe.resolvedFormats, format)
		if format != "json" {
			return types.Plugin{}, errors.New("no parser plugin advertises the requested format")
		}
		return types.Plugin{
			Metadata: types.PluginMetadata{Name: "demo-parser", Version: "1.0.0", PluginType: "parser"},
			Spec:     types.PluginSpec{Capabilities: []string{"parse:json"}},
		}, nil
	})
	load := func(p types.Plugin) (wasm.PluginSpec, error) {
		probe.loadedCoords = append(probe.loadedCoords, p.Coordinate())
		return wasm.PluginSpec{Name: p.Coordinate(), Path: fixturePath}, nil
	}
	return res, load, probe
}

// TestImportArtifact_ResolvesAndRuns exercises the whole host orchestration
// end-to-end: the stub resolver matches import:demo, the host loads the importer
// guest hermetically, invokes import with an ImportInput, and decodes the
// ImportOutput. The demo importer echoes the request format into one drafted
// entity and a matching scaffold file, so the result reflects the request —
// proving the resolve → load → run → decode path returns a correct result. The
// demo importer does not consume host-mediated parsing, so the parser seams are
// left nil here: the callback is injected but never invoked.
func TestImportArtifact_ResolvesAndRuns(t *testing.T) {
	t.Parallel()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-go.wasm")
	load := demoImporterLoader(fixture)

	out, err := importersocket.ImportArtifact(context.Background(), stubImporterCatalog{}, load, nil, nil, "demo", []byte("hello world"), nil, nil)
	if err != nil {
		t.Fatalf("ImportArtifact: %v", err)
	}

	if len(out.Entities) != 1 {
		t.Fatalf("entities = %+v; want exactly one", out.Entities)
	}
	if out.Entities[0].Name != "demo" || out.Entities[0].Kind != "Component" || out.Entities[0].Mode != "draft" {
		t.Fatalf("entity = %+v; want {Component, demo, draft}", out.Entities[0])
	}
	if len(out.Files) != 1 {
		t.Fatalf("files = %+v; want exactly one", out.Files)
	}
	if out.Files[0].Path != "demo.yaml" || out.Files[0].Content != out.Entities[0].Body {
		t.Fatalf("file = %+v; want path demo.yaml with content echoing the entity body", out.Files[0])
	}
}

// TestImportArtifact_Deterministic pins the determinism invariant: the hermetic
// importer invocation is byte-deterministic, so the same source and format yield
// a byte-identical result across two independent invocations.
func TestImportArtifact_Deterministic(t *testing.T) {
	t.Parallel()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-go.wasm")
	load := demoImporterLoader(fixture)

	first, err := importersocket.ImportArtifact(context.Background(), stubImporterCatalog{}, load, nil, nil, "demo", []byte("repeatable"), nil, nil)
	if err != nil {
		t.Fatalf("first ImportArtifact: %v", err)
	}
	second, err := importersocket.ImportArtifact(context.Background(), stubImporterCatalog{}, load, nil, nil, "demo", []byte("repeatable"), nil, nil)
	if err != nil {
		t.Fatalf("second ImportArtifact: %v", err)
	}

	firstRaw, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	secondRaw, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	if !bytes.Equal(firstRaw, secondRaw) {
		t.Fatalf("non-deterministic output:\n first: %s\nsecond: %s", firstRaw, secondRaw)
	}
}

// TestImportArtifact_UnknownFormatHaltsLoud pins the loud-halt contract: an
// unadvertised importer format halts through the resolver's not-found halt
// rather than silently passing the source through unimported. The loader is
// never reached.
func TestImportArtifact_UnknownFormatHaltsLoud(t *testing.T) {
	t.Parallel()
	loaderCalled := false
	load := func(p types.Plugin) (wasm.PluginSpec, error) {
		loaderCalled = true
		return wasm.PluginSpec{}, nil
	}

	_, err := importersocket.ImportArtifact(context.Background(), stubImporterCatalog{}, load, nil, nil, "unknown", []byte("x"), nil, nil)
	if !errors.Is(err, errNoImporterForFormat) {
		t.Fatalf("err = %v; want errNoImporterForFormat", err)
	}
	if loaderCalled {
		t.Fatal("loader must not be reached for an unresolved format")
	}
}

// TestImportArtifact_HostMediatedParse proves the parser seam is wired through
// the dispatcher: an importer guest that asks the host to parse its source
// obtains a real tree host-mediated. The probe importer hands its source to the
// aggrega_parse_source host function for the "json" format; the dispatcher's
// injected callback resolves and runs the parser through the parser socket and
// hands the tree back. The guest echoes the tree it received, so a clean decode
// (no in-band failure) plus a recorded parser resolve+load proves the callback
// fired, delegated to the parser socket, and delivered a genuine parsed tree —
// not the tree passing through the import input payload.
func TestImportArtifact_HostMediatedParse(t *testing.T) {
	t.Parallel()
	importerFixture := wasmtest.EnsureFixtureBuilt(t, "parse-source-probe-go.wasm")
	parserFixture := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")

	importerLoad := func(p types.Plugin) (wasm.PluginSpec, error) {
		return wasm.PluginSpec{Name: p.Coordinate(), Path: importerFixture}, nil
	}
	parserRes, parserLoad, probe := demoParserSeam(parserFixture)

	_, err := importersocket.ImportArtifact(context.Background(), stubImporterCatalog{}, importerLoad, parserRes, parserLoad, "demo", []byte(`{"hello":"world"}`), nil, nil)
	if err != nil {
		t.Fatalf("ImportArtifact: %v", err)
	}

	// The guest asked the host to parse for "json"; a genuine host-mediated
	// parse reached the parser socket and loaded the parser.
	if len(probe.resolvedFormats) != 1 || probe.resolvedFormats[0] != "json" {
		t.Fatalf("parser resolved formats = %v; want exactly [json]", probe.resolvedFormats)
	}
	wantCoord := types.PluginCoordinate("parser", "demo-parser")
	if len(probe.loadedCoords) != 1 || probe.loadedCoords[0] != wantCoord {
		t.Fatalf("parser loaded coords = %v; want exactly [%s]", probe.loadedCoords, wantCoord)
	}
}

// TestImportArtifact_HostMediatedParseDeterministic pins the determinism
// invariant across the nested parse: two host-mediated import runs over the same
// source yield a byte-identical result, so the extra producer-consumption hop
// introduces no nondeterminism.
func TestImportArtifact_HostMediatedParseDeterministic(t *testing.T) {
	t.Parallel()
	importerFixture := wasmtest.EnsureFixtureBuilt(t, "parse-source-probe-go.wasm")
	parserFixture := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")

	importerLoad := func(p types.Plugin) (wasm.PluginSpec, error) {
		return wasm.PluginSpec{Name: p.Coordinate(), Path: importerFixture}, nil
	}

	run := func() []byte {
		parserRes, parserLoad, _ := demoParserSeam(parserFixture)
		out, err := importersocket.ImportArtifact(context.Background(), stubImporterCatalog{}, importerLoad, parserRes, parserLoad, "demo", []byte(`{"hello":"world"}`), nil, nil)
		if err != nil {
			t.Fatalf("ImportArtifact: %v", err)
		}
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("marshal import output: %v", err)
		}
		return raw
	}

	if first, second := run(), run(); !bytes.Equal(first, second) {
		t.Fatalf("non-deterministic host-mediated import output:\n first: %s\nsecond: %s", first, second)
	}
}

// TestImportArtifact_NoParserForFormatHaltsLoud proves an unresolvable syntactic
// format halts loudly through the parser socket's ErrNoParserForFormat rather
// than the source passing through unparsed. The importer asks the host to parse
// for "json", but the parser resolver — a real, empty catalog — advertises no
// parser, so the reused ErrNoParserForFormat fires inside the injected callback.
// The halt surfaces (a non-nil result) and no parser is loaded, proving the
// dispatcher delegates to the parser socket's resolution path rather than
// swallowing the failure.
func TestImportArtifact_NoParserForFormatHaltsLoud(t *testing.T) {
	t.Parallel()
	importerFixture := wasmtest.EnsureFixtureBuilt(t, "parse-source-probe-go.wasm")

	importerLoad := func(p types.Plugin) (wasm.PluginSpec, error) {
		return wasm.PluginSpec{Name: p.Coordinate(), Path: importerFixture}, nil
	}

	// A real, empty catalog resolves no parser for "json"; its
	// ParserPluginForFormat returns the reused ErrNoParserForFormat.
	parserRes := &catalog.Catalog{}
	parserLoaderCalled := false
	parserLoad := func(p types.Plugin) (wasm.PluginSpec, error) {
		parserLoaderCalled = true
		return wasm.PluginSpec{}, nil
	}

	// Sanity: the resolver the callback delegates to fails with the reused
	// sentinel for the format the probe requests.
	if _, err := parserRes.ParserPluginForFormat("json"); !errors.Is(err, catalog.ErrNoParserForFormat) {
		t.Fatalf("parser resolver err = %v; want ErrNoParserForFormat", err)
	}

	_, err := importersocket.ImportArtifact(context.Background(), stubImporterCatalog{}, importerLoad, parserRes, parserLoad, "demo", []byte(`{"x":1}`), nil, nil)
	if err == nil {
		t.Fatal("ImportArtifact: want a loud halt for an unresolvable syntactic format, got nil error")
	}
	if parserLoaderCalled {
		t.Fatal("parser loader must not be reached when no parser advertises the format")
	}
}

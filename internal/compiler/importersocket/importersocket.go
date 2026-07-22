// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package importersocket is the host orchestration for the importer socket: it
// resolves an importer plugin for a requested source format, runs it
// hermetically, and returns the entities, decisions, diagnostics, and scaffold
// files the importer produced. An importer turns a foreign infrastructure
// description into Aggrega entities so a consumer can adopt existing
// infrastructure without hand-authoring the entities from scratch. The host
// mediates the whole flow — the consumer declares the format it needs, the host
// resolves and runs the importer and hands the result back — so an importer is
// never invoked guest-to-guest and the host adds no new dispatcher re-entry.
//
// The import wire types (ImportInput, ImportOutput, and their nested source,
// entity, decision, diagnostic, and scaffold shapes) are the single generated
// ABI codec the guests speak; the host reads and writes that one codec directly
// rather than a duplicated host-side copy, so the wire shape stays
// single-sourced from the schema.
package importersocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ImporterResolver resolves a requested source format to the importer plugin
// entity that advertises it. It is the capability-resolution seam the
// orchestrator depends on; the real catalog implementation halts loudly on an
// unadvertised format or an ambiguous one.
type ImporterResolver interface {
	ImporterPluginForFormat(format string) (types.Plugin, error)
}

// ImporterLoader turns a resolved importer plugin entity into a
// hermetically-loadable wasm spec — the byte source for the guest. Obtaining a
// plugin's bytes is the consuming site's concern, not this orchestrator's: a
// core importer resolves to its bytes embedded in the binary, and a pulled
// importer resolves to a verified cache slot. The seam is injected so the
// orchestrator stays agnostic to where the bytes come from.
type ImporterLoader func(types.Plugin) (wasm.PluginSpec, error)

// ImportArtifact resolves the importer advertising the requested format, loads
// it hermetically, invokes its import export with the source bytes, and returns
// the decoded result. An unknown format halts loudly — the resolver's
// no-importer-for-format halt propagates rather than the source passing through
// unimported.
//
// The invocation is hermetic and byte-deterministic: the loader carries a nil
// puller and the platform's deterministic module config (no network, no
// filesystem), so the same source bytes and format yield a byte-identical
// result across runs. An importer is a pure sources→entities function; it
// introduces no reproducibility posture of its own but inherits its consuming
// socket's — a core importer embedded in the binary carries no lockfile pin, and
// a future pulled importer would follow the pin discipline of the socket that
// consumes its output.
//
// sources carries the named sources for a multi-file import, and settings is the
// opaque importer-specific options document the consumer supplies; the host
// pipes both through without interpreting them. A nil settings is omitted from
// the wire payload.
//
// parserRes and parserLoad are the parser-socket seams the host wires into the
// importer for host-mediated parsing. An importer that needs a source's
// canonical structured tree does not carry its own language parser: it asks the
// host, and the host resolves and runs a parser for the requested syntactic
// format and returns the tree. The dispatcher wires that path by injecting a
// callback onto the loaded importer before invoking its import export; the
// callback resolves and runs the parser through the parser socket. The callback
// threads the invocation context, so the host's producer-consumption depth guard
// crosses the boundary and a nested producer-consumption call is denied. An
// unresolvable syntactic format halts loudly through the parser resolver rather
// than the source passing through unparsed. The tree arrives through this
// callback, never through the import input payload.
func ImportArtifact(ctx context.Context, res ImporterResolver, load ImporterLoader, parserRes parsersocket.ParserResolver, parserLoad parsersocket.ParserLoader, format string, source []byte, sources []abi.ImportSource, settings json.RawMessage) (abi.ImportOutput, error) {
	plugin, err := res.ImporterPluginForFormat(format)
	if err != nil {
		return abi.ImportOutput{}, fmt.Errorf("resolve importer for format %q: %w", format, err)
	}

	spec, err := load(plugin)
	if err != nil {
		return abi.ImportOutput{}, fmt.Errorf("load importer %q: %w", plugin.Coordinate(), err)
	}
	spec.PluginType = wasm.PluginTypeImporter

	payload, err := json.Marshal(abi.ImportInput{Format: format, Bytes: source, Sources: sources, Settings: settings})
	if err != nil {
		return abi.ImportOutput{}, fmt.Errorf("marshal import input for %q: %w", format, err)
	}

	loader := wasm.NewCachingLoaderFromContext(ctx, nil)
	guest, desc, err := loader.LoadAndDescribe(ctx, spec)
	if err != nil {
		return abi.ImportOutput{}, fmt.Errorf("load importer %q: %w", plugin.Coordinate(), err)
	}
	defer guest.Close(ctx)

	// Wire host-mediated parsing onto the importer for the next export
	// invocation, the same way an acquired source location is set immediately
	// before a sourced export. The callback runs a parser through the parser
	// socket for the syntactic format the importer requests — which need not be
	// the importer's own format — and returns the canonical tree. It threads the
	// callback's context, not the outer one, so the host's producer-consumption
	// depth guard carried on that context crosses into the parser run.
	guest.SetSourceParser(func(ctx context.Context, format string, source []byte, options json.RawMessage) (json.RawMessage, error) {
		parsed, err := parsersocket.ParseArtifact(ctx, parserRes, parserLoad, format, source, options)
		if err != nil {
			// Map the parser-resolution sentinel — whose home is pkg/catalog, a
			// package the host boundary must not import — to its public wire code
			// here, at the layer that owns the catalog dependency. The boundary
			// reads the code off the CodedHostError; any other seam error stays
			// uncoded and the boundary treats it as a parse failure.
			if errors.Is(err, catalog.ErrNoParserForFormat) {
				return nil, &wasm.CodedHostError{Code: abi.CodeNoParserForFormat, Err: err}
			}
			return nil, err
		}
		return parsed.Tree, nil
	})

	out, err := wasm.InvokeExport(ctx, guest, desc, wasm.ImportExport, payload)
	if err != nil {
		return abi.ImportOutput{}, fmt.Errorf("call %q on importer %q: %w", wasm.ImportExport, plugin.Coordinate(), err)
	}

	var io abi.ImportOutput
	if err := io.UnmarshalJSON(out); err != nil {
		return abi.ImportOutput{}, fmt.Errorf("decode import output from importer %q: %w", plugin.Coordinate(), err)
	}
	return io, nil
}

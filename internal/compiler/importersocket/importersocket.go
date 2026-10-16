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
// catalog-view, entity, decision, diagnostic, and scaffold shapes) are the single generated
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
// plugin's bytes is the consuming site's concern, not this orchestrator's: the
// import operation reads them through the shared plugin cascade, which serves a
// module embedded in the binary, one laid down beside its Plugin entity in a
// catalog directory, one at an explicit local path and one in the workspace
// content cache, and fetches nothing — except that a core importer of the
// compiler's release no tier holds is fetched once, verified and written to the
// content cache before it loads. The seam is injected so the orchestrator stays
// agnostic to where the bytes come from.
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
// introduces no reproducibility posture of its own, and an import records no
// lockfile pin for the importer it runs. Which bytes run is the loader's answer
// alone — the import operation's loader reads them through the shared plugin
// cascade, from the binary, from beside the importer's Plugin entity in a
// catalog directory, from an explicit local path or from the workspace content
// cache, a core importer of the compiler's release held to the digest the binary
// carries for it — so this orchestrator holds no pin discipline of its own.
//
// The socket carries the canonical CBOR encoding in both directions: the import
// input is written by the generated codec's EncodeCBOR, and the guest's output is
// read back through the binary encoding's strict decode entry point, which
// refuses an output that is not an ImportOutput — a top-level field the shape
// does not declare, or bytes that are not a CBOR map at all — naming the export.
// The encoding is fixed by the socket, so an importer decodes its input and
// encodes its output with the CBOR entry points its kit's ImportInput and
// ImportOutput carry, and reports a failure it can describe in the output's
// error field.
//
// sources carries the named sources for a multi-file import, and settings is the
// opaque importer-specific options document the consumer supplies; the host
// pipes both through without interpreting them — settings crosses as the bytes
// the consumer supplied. A nil settings is omitted from the wire payload.
//
// catalogView is the view of the workspace catalog's Components the importer is
// handed, as [ProjectCatalog] builds it from the catalog the import resolved
// against. It is copied onto the payload as it is; a nil view is omitted from the
// wire, so a payload built for a catalog holding no Component is byte-identical
// to one built with no view at all.
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
func ImportArtifact(ctx context.Context, res ImporterResolver, load ImporterLoader, parserRes parsersocket.ParserResolver, parserLoad parsersocket.ParserLoader, format string, source []byte, sources []abi.ImportSource, settings json.RawMessage, catalogView *abi.CatalogProjection) (abi.ImportOutput, error) {
	plugin, err := res.ImporterPluginForFormat(format)
	if err != nil {
		return abi.ImportOutput{}, fmt.Errorf("resolve importer for format %q: %w", format, err)
	}

	spec, err := load(plugin)
	if err != nil {
		return abi.ImportOutput{}, fmt.Errorf("load importer %q: %w", plugin.Coordinate(), err)
	}
	spec.PluginType = wasm.PluginTypeImporter

	payload, err := abi.ImportInput{Format: format, Bytes: source, Sources: sources, Settings: settings, Catalog: catalogView}.EncodeCBOR()
	if err != nil {
		return abi.ImportOutput{}, fmt.Errorf("encode import input for %q: %w", format, err)
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
			if errors.Is(err, catalog.ErrNoParserForFormat()) {
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
	return readImportOutput(plugin.Coordinate(), out)
}

// readImportOutput reads an importer's answer through the socket's three
// guest-envelope checks, in order: the bytes must be an ImportOutput in the
// canonical CBOR the socket carries, its error field must be empty, and every
// diagnostic it carries must have a declared severity. coordinate names the
// importer in each refusal.
func readImportOutput(coordinate string, out []byte) (abi.ImportOutput, error) {
	var io abi.ImportOutput
	if err := wasm.DecodeGuestOutputCBOR(wasm.ImportExport.Name(), out, &io); err != nil {
		return abi.ImportOutput{}, fmt.Errorf("importer %q: %w", coordinate, err)
	}
	if err := wasm.GuestReturnedError(coordinate, wasm.ImportExport.Name(), wasm.GuestFailure{Message: io.Error}); err != nil {
		return abi.ImportOutput{}, fmt.Errorf("importer %q: %w", coordinate, err)
	}
	if err := wasm.GuestDiagnosticSeverities(coordinate, wasm.ImportExport.Name(), io.Diagnostics); err != nil {
		return abi.ImportOutput{}, err
	}
	return io, nil
}

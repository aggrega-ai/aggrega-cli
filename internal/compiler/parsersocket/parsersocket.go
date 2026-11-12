// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package parsersocket is the host orchestration for the parser socket: it
// resolves a parser plugin for a requested source format, runs it hermetically,
// and returns the canonical structured tree the parser produced. A parser turns
// a foreign source text into a structured tree so any consumer can read the tree
// without carrying its own language parser. The host mediates the whole flow —
// the consumer declares the format it needs, the host resolves and runs the
// parser and hands the tree back — so a parser is never invoked guest-to-guest
// and the host adds no new dispatcher re-entry.
//
// The parse wire types (ParseInput, ParseOutput, and their nested span and
// diagnostic shapes) are the single generated ABI codec the guests speak; the
// host reads and writes that one codec directly rather than a duplicated
// host-side copy, so the wire shape stays single-sourced from the schema. The
// socket carries that codec's canonical CBOR in both directions, the encoding a
// guest's own request to the host for a parse already arrives in, so one
// ParseInput has one encoding whichever side builds it, and its source bytes
// cross as a raw byte string rather than as base64 text.
package parsersocket

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ParserResolver resolves a requested source format to the parser plugin entity
// that advertises it. It is the capability-resolution seam the orchestrator
// depends on; *catalog.Catalog satisfies it through ParserPluginForFormat, which
// halts loudly on an unadvertised format or an ambiguous one.
type ParserResolver interface {
	ParserPluginForFormat(format string) (types.Plugin, error)
}

// ParserLoader turns a resolved parser plugin entity into a hermetically-loadable
// wasm spec — the byte source for the guest. Obtaining a plugin's bytes is the
// consuming site's concern, not this orchestrator's: a core parser resolves to
// its bytes embedded in the binary, and a pulled parser resolves to a verified
// cache slot. The seam is injected so the orchestrator stays agnostic to where
// the bytes come from.
type ParserLoader func(types.Plugin) (wasm.PluginSpec, error)

// ParseArtifact resolves the parser advertising the requested format, loads it
// hermetically, invokes its parse export with the source bytes, and returns the
// decoded tree. An unknown format halts loudly — the resolver's
// no-parser-for-format halt propagates rather than the source passing through
// unparsed.
//
// The invocation is hermetic and byte-deterministic: the loader carries a nil
// puller and the platform's deterministic module config (no network, no
// filesystem), so the same source bytes and format yield a byte-identical tree
// across runs. A parser is a pure bytes→tree function; it introduces no
// reproducibility posture of its own but inherits its consuming socket's — a core
// parser embedded in the binary carries no lockfile pin, and a future pulled
// parser would follow the pin discipline of the socket that consumes its tree.
//
// options is the opaque parser-specific options document the consumer supplies;
// the host pipes it through without interpreting it. A nil options is omitted
// from the wire payload.
//
// The parser's answer is read through the strict binary decode: an answer that
// is not a ParseOutput in canonical CBOR, or that carries a top-level field the
// shape does not declare, halts naming the parser and its parse export. The
// export is fixed by the parser type, so no configuration can misdirect it; the
// decode is strict so that the plugin-wire registry's call-site guard sees the
// binary encoding named here, which is what keeps the direct-invocation command
// converting an authored payload for this export.
func ParseArtifact(ctx context.Context, res ParserResolver, load ParserLoader, format string, source []byte, options json.RawMessage) (abi.ParseOutput, error) {
	plugin, err := res.ParserPluginForFormat(format)
	if err != nil {
		return abi.ParseOutput{}, fmt.Errorf("resolve parser for format %q: %w", format, err)
	}

	spec, err := load(plugin)
	if err != nil {
		return abi.ParseOutput{}, fmt.Errorf("load parser %q: %w", plugin.Coordinate(), err)
	}
	spec.PluginType = wasm.PluginTypeParser

	payload, err := abi.ParseInput{Format: format, Bytes: source, Options: options}.EncodeCBOR()
	if err != nil {
		return abi.ParseOutput{}, fmt.Errorf("encode parse input for %q: %w", format, err)
	}

	loader := wasm.NewCachingLoaderFromContext(ctx, nil)
	guest, desc, err := loader.LoadAndDescribe(ctx, spec)
	if err != nil {
		return abi.ParseOutput{}, fmt.Errorf("load parser %q: %w", plugin.Coordinate(), err)
	}
	defer guest.Close(ctx)

	out, err := wasm.InvokeExport(ctx, guest, desc, wasm.ParseExport, payload)
	if err != nil {
		return abi.ParseOutput{}, fmt.Errorf("call %q on parser %q: %w", wasm.ParseExport, plugin.Coordinate(), err)
	}

	var po abi.ParseOutput
	if err := wasm.DecodeGuestOutputCBOR(wasm.ParseExport.Name(), out, &po); err != nil {
		return abi.ParseOutput{}, fmt.Errorf("parser %q: %w", plugin.Coordinate(), err)
	}
	if err := wasm.GuestReturnedError(plugin.Coordinate(), wasm.ParseExport.Name(), wasm.GuestFailure{Message: po.Error}); err != nil {
		return abi.ParseOutput{}, fmt.Errorf("parser %q: %w", plugin.Coordinate(), err)
	}
	if err := wasm.GuestDiagnosticSeverities(plugin.Coordinate(), wasm.ParseExport.Name(), po.Diagnostics); err != nil {
		return abi.ParseOutput{}, err
	}
	return po, nil
}

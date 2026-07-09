// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine

import (
	"context"
	"fmt"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// CarveExport and ReassembleExport are the optional guest exports this adapter
// drives to carve a provider's emitted output into addressable units and to
// reassemble it byte-faithfully after a pipeline step edits some of them. They
// are a paired contract — a provider advertises both or neither. A provider
// whose output the stock YAML codec cannot carve directly (a templated chart,
// for instance) advertises them so its own output-shape knowledge (what is a
// PATCH target versus opaque) lives in its guest; a provider that omits them is
// never routed here and borrows the host's stock YAML codec.
const (
	CarveExport      = "carve"
	ReassembleExport = "reassemble"
)

// PipelineCodec reports the provider's pipeline codec, driven through its guest
// when the guest advertises both the carve and reassemble exports. It satisfies
// renderer.CodecProvider: the pipeline executor asks the engine rendering a
// scope "do you supply a codec?" and, when the guest advertises the exports,
// carves and reassembles that scope across the guest ABI so the provider's
// output-shape policy stays in the guest. A guest that does not advertise both
// exports (or cannot be described) reports (nil, false), and the executor
// borrows the stock YAML codec.
func (e *Engine) PipelineCodec(yamlIndent int) (frp.Codec, bool) {
	desc, err := e.describe()
	if err != nil {
		return nil, false
	}
	if !advertisesExport(desc, CarveExport) || !advertisesExport(desc, ReassembleExport) {
		return nil, false
	}
	return &guestCodec{engine: e, yamlIndent: yamlIndent}, true
}

// guestCodec is the host-side frp.Codec that carves and reassembles through a
// provider guest's carve and reassemble exports. It carries the render-wide
// indent so the guest's carving and any op-synthesised text match the render's
// indentation, and it holds the Engine whose loader, lookup, and memoized
// descriptor drive the crossings. Segment and Reassemble use a background
// context: the frp.Codec contract carries none, and the crossing is a
// self-contained guest call with no cancellation surface.
type guestCodec struct {
	engine     *Engine
	yamlIndent int
}

var _ frp.Codec = (*guestCodec)(nil)

// Segment carves one named file into ordered addressable units through the
// guest's carve export.
func (c *guestCodec) Segment(name string, body []byte) ([]frp.Unit, error) {
	in := renderer.CarveInput{Name: name, Body: body, YamlIndent: c.yamlIndent}
	out, err := invokeCarve(context.Background(), c.engine, in)
	if err != nil {
		return nil, err
	}
	return out.Units, nil
}

// Reassemble rebuilds one named file from its carved units and the patched units
// a pipeline step produced, through the guest's reassemble export. A guest owns
// its own reassembly correctness (and any self-heal), so this host-side adapter
// raises no advisory warnings across the ABI: the reassemble crossing carries a
// body and an error, not diagnostics.
func (c *guestCodec) Reassemble(name string, body []byte, segs []frp.Unit, patched []frp.PatchedUnit) ([]byte, []string, error) {
	in := renderer.ReassembleInput{Name: name, Body: body, Segs: segs, Patched: patched, YamlIndent: c.yamlIndent}
	out, err := invokeReassemble(context.Background(), c.engine, in)
	if err != nil {
		return nil, nil, err
	}
	return out.Body, nil, nil
}

// invokeCarve loads a fresh guest instance through the shared loader, calls the
// carve export against the Engine's memoized descriptor, and decodes the
// CarveOutput. It mirrors invoke's memoized-descriptor, fresh-instance-per-
// crossing lifecycle; the codec surface needs no source root (it carves bytes
// already in the payload). The plugin is closed before invokeCarve returns.
func invokeCarve(ctx context.Context, e *Engine, input renderer.CarveInput) (renderer.CarveOutput, error) {
	desc, err := e.describe()
	if err != nil {
		return renderer.CarveOutput{}, err
	}
	resolved, err := e.lookup(e.pluginURI)
	if err != nil {
		return renderer.CarveOutput{}, fmt.Errorf("wasmengine: resolve plugin %q: %w", e.pluginURI, err)
	}
	plugin, err := e.loader.Load(ctx, resolver.PluginSpec(e.pluginURI, resolved))
	if err != nil {
		return renderer.CarveOutput{}, err
	}
	defer plugin.Close(ctx)

	payload, err := input.MarshalJSON()
	if err != nil {
		return renderer.CarveOutput{}, fmt.Errorf("wasmengine: marshal carve input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, *desc, CarveExport, payload)
	if err != nil {
		return renderer.CarveOutput{}, err
	}
	var out renderer.CarveOutput
	if err := out.UnmarshalJSON(raw); err != nil {
		return renderer.CarveOutput{}, fmt.Errorf("wasmengine: decode carve output: %w", err)
	}
	return out, nil
}

// invokeReassemble loads a fresh guest instance through the shared loader, calls
// the reassemble export against the Engine's memoized descriptor, and decodes the
// ReassembleOutput. It mirrors invokeCarve's lifecycle. The plugin is closed
// before invokeReassemble returns.
func invokeReassemble(ctx context.Context, e *Engine, input renderer.ReassembleInput) (renderer.ReassembleOutput, error) {
	desc, err := e.describe()
	if err != nil {
		return renderer.ReassembleOutput{}, err
	}
	resolved, err := e.lookup(e.pluginURI)
	if err != nil {
		return renderer.ReassembleOutput{}, fmt.Errorf("wasmengine: resolve plugin %q: %w", e.pluginURI, err)
	}
	plugin, err := e.loader.Load(ctx, resolver.PluginSpec(e.pluginURI, resolved))
	if err != nil {
		return renderer.ReassembleOutput{}, err
	}
	defer plugin.Close(ctx)

	payload, err := input.MarshalJSON()
	if err != nil {
		return renderer.ReassembleOutput{}, fmt.Errorf("wasmengine: marshal reassemble input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, *desc, ReassembleExport, payload)
	if err != nil {
		return renderer.ReassembleOutput{}, err
	}
	var out renderer.ReassembleOutput
	if err := out.UnmarshalJSON(raw); err != nil {
		return renderer.ReassembleOutput{}, fmt.Errorf("wasmengine: decode reassemble output: %w", err)
	}
	return out, nil
}

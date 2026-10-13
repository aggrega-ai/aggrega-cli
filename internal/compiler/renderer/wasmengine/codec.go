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

// PipelineCodec reports the provider's pipeline codec, driven through its guest
// when the guest advertises both the carve and reassemble exports — the optional
// exports that carve a provider's emitted output into addressable units and
// reassemble it byte-faithfully after a pipeline step edits some of them. They
// are a paired contract: a provider advertises both or neither. A provider whose
// output the stock YAML codec cannot carve directly (a templated chart, for
// instance) advertises them so its own output-shape knowledge (what is a PATCH
// target versus opaque) lives in its guest. It satisfies
// renderer.CodecProvider: the pipeline executor asks the engine rendering a
// scope "do you supply a codec?" and, when the guest advertises the exports,
// carves and reassembles that scope across the guest ABI so the provider's
// output-shape policy stays in the guest. A guest that does not advertise both
// exports (or cannot be described) reports (nil, false), and the executor
// borrows the stock YAML codec.
//
// ctx is the render's own. It is handed to the returned codec rather than to
// each crossing because the carve-and-reassemble contract is the guest export
// contract unchanged, and a guest carries no host context — so that contract
// cannot take one without asking every implementation, and one implementation
// compiles into a guest. The codec is built here, used for the one pipeline run
// that asked for it, and discarded, so the context it carries is that run's by
// construction.
func (e *Engine) PipelineCodec(ctx context.Context, yamlIndent int) (frp.Codec, bool) {
	desc, err := e.describeContext(ctx)
	if err != nil {
		return nil, false
	}
	if !advertisesExport(desc, wasm.CarveExport) || !advertisesExport(desc, wasm.ReassembleExport) {
		return nil, false
	}
	return &guestCodec{engine: e, yamlIndent: yamlIndent, ctx: ctx}, true
}

// guestCodec is the host-side frp.Codec that carves and reassembles through a
// provider guest's carve and reassemble exports. It carries the render-wide
// indent so the guest's carving and any op-synthesized text match the render's
// indentation, and it holds the Engine whose loader, lookup, and memoized
// descriptor drive the crossings.
type guestCodec struct {
	engine     *Engine
	yamlIndent int
	// ctx is the render's context, carried on the value because the carve and
	// reassemble contract takes none and cannot: it is the guest export contract
	// unchanged, and one implementation of it compiles into a guest, where a host
	// context does not exist. The value is built inside the pipeline run that
	// holds the context, used for that run's crossings, and discarded — so its
	// lifetime is the run's, which is what makes carrying it here the
	// request-scoped exception rather than a stored context. PipelineCodec above
	// is the only thing that builds one and always sets this, so it is never nil.
	ctx context.Context
}

var _ frp.Codec = (*guestCodec)(nil)

// Segment carves one named file into ordered addressable units through the
// guest's carve export.
func (c *guestCodec) Segment(name string, body []byte) ([]frp.Unit, error) {
	in := renderer.CarveInput{Name: name, Body: body, YamlIndent: c.yamlIndent}
	out, err := invokeCarve(c.ctx, c.engine, in)
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
	out, err := invokeReassemble(c.ctx, c.engine, in)
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
	export := wasm.CarveExport
	desc, err := e.describeContext(ctx)
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

	payload, err := input.EncodeCBOR()
	if err != nil {
		return renderer.CarveOutput{}, fmt.Errorf("wasmengine: encode carve input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, *desc, export, payload)
	if err != nil {
		return renderer.CarveOutput{}, err
	}
	var out renderer.CarveOutput
	if err := wasm.DecodeGuestOutputCBOR(export.Name(), raw, &out); err != nil {
		return renderer.CarveOutput{}, fmt.Errorf("wasmengine: %w", err)
	}
	return out, nil
}

// invokeReassemble loads a fresh guest instance through the shared loader, calls
// the reassemble export against the Engine's memoized descriptor, and decodes the
// ReassembleOutput. It mirrors invokeCarve's lifecycle. The plugin is closed
// before invokeReassemble returns.
func invokeReassemble(ctx context.Context, e *Engine, input renderer.ReassembleInput) (renderer.ReassembleOutput, error) {
	export := wasm.ReassembleExport
	desc, err := e.describeContext(ctx)
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

	payload, err := input.EncodeCBOR()
	if err != nil {
		return renderer.ReassembleOutput{}, fmt.Errorf("wasmengine: encode reassemble input: %w", err)
	}
	raw, err := wasm.InvokeExport(ctx, plugin, *desc, export, payload)
	if err != nil {
		return renderer.ReassembleOutput{}, err
	}
	var out renderer.ReassembleOutput
	if err := wasm.DecodeGuestOutputCBOR(export.Name(), raw, &out); err != nil {
		return renderer.ReassembleOutput{}, fmt.Errorf("wasmengine: %w", err)
	}
	return out, nil
}

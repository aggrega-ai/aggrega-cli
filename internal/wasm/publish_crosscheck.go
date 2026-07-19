// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
)

// DescribePluginBytes loads the given .wasm bytes, invokes and validates their
// describe() self-report, closes the plugin, and returns the validated
// descriptor. It is the single home of the load-describe-close sequence over
// standalone bytes with no entity front — the publish path's cross-check, the
// command mirror, and the under-declaration advisory all read one load's
// descriptor through it, so the guest is instantiated once per publish.
//
// The bytes load under the describe-only profile — inert host-function stubs and
// the union import allowlist — so a guest of any plugin type, including a CLI
// plugin that imports the terminal streaming emitters, instantiates purely to
// have its describe() read; the guest reaches only no-op stubs and never any real
// host capability.
//
// The Contract on the spec is left nil, so the load gate runs no entity capability
// cross-check here; the caller runs whatever entity comparison it needs against the
// returned descriptor. A non-empty pluginType flows onto the spec as the expected
// type, so the load gate performs the declared-vs-reported plugin-type check as part
// of the describe-load and halts on disagreement; an empty pluginType is the
// introspection path and skips that check. This makes the gate the type verdict for
// every caller that loads bytes directly through this primitive, not only those that
// go through ValidatePlugin. maxMemoryPages caps the guest's linear memory for the
// load; the plugin is Closed before returning so no instance leaks.
func DescribePluginBytes(ctx context.Context, identity string, wasmBytes []byte, maxMemoryPages uint32, pluginType string) (PluginDescriptor, error) {
	// Build through the shared constructor so this loader carries the same
	// process-wide compilation cache every other loader uses, rather than a
	// bare cache-less loader that would recompile the guest in isolation.
	loader := NewCachingLoader(nil)
	spec := PluginSpec{
		Name:           identity,
		PluginType:     PluginType(pluginType),
		Bytes:          wasmBytes,
		MaxMemoryPages: maxMemoryPages,
	}

	plugin, desc, err := loader.LoadForDescribe(ctx, spec)
	if err != nil {
		return PluginDescriptor{}, err
	}
	_ = plugin.Close(ctx)
	return desc, nil
}

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
// The Contract on the spec is left nil, so the load gate runs no entity
// cross-check here — this primitive owns only the load and the descriptor; the
// caller runs whatever comparison it needs against the returned descriptor.
// maxMemoryPages caps the guest's linear memory for the load; the plugin is
// Closed before returning so no instance leaks.
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

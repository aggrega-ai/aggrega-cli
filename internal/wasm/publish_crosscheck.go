// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
)

// CrossCheckPublishedPlugin verifies, at publish time, that a Plugin
// entity's authored capability claim agrees with the .wasm bytes about to be
// published. It is the single home for the publish-side describe() check: the
// entity's authored per-export capability claim is cross-checked against the
// plugin's own self-report, so a published entity can never promise a capability
// the binary does not actually provide.
//
// The host-function surface is not cross-checked here: it is not authored. The
// publish flow derives the host functions from the .wasm import section and
// stamps them into the entity (machine-owned), so there is no authored claim to
// disagree with — the binary is the sole source.
//
// The check loads the bytes and invokes describe() with a nil capability, so the
// load gate's own cross-check is skipped and this function owns the comparison,
// then compares the authored export claim against the descriptor via
// CrossCheckContract — the same primitive the load gate uses, so the publish and
// load surfaces share one comparison.
//
// The bytes load under the describe-only profile: inert host-function stubs and
// the union import allowlist, so a guest of any plugin type — including a CLI
// plugin that imports the terminal streaming emitters — instantiates purely to be
// introspected, and its describe() reaches only no-op stubs rather than any real
// host capability.
//
// maxMemoryPages caps the guest's linear memory for the describe() load. A large
// guest whose working set exceeds the platform default cap would fail to
// instantiate under the default; the caller supplies the entity's declared
// runtime cap (or a describe-time floor when the entity declares none) so the
// load matches the guest's authored footprint. The cap governs only the load;
// the capability comparison is independent of it.
//
// A disagreement halts with ErrWasmContractMismatch. On success it returns the
// plugin's validated describe() descriptor so the caller can reuse the one load's
// self-report — the command mirror and the under-declaration advisory read it
// rather than loading the guest a second time. The plugin is Closed before
// returning either way so a rejected or completed publish check leaks no
// instance. entityIdentity names the entity in any rejection message.
func CrossCheckPublishedPlugin(ctx context.Context, entityIdentity string, claim EntityContractClaim, wasmBytes []byte, maxMemoryPages uint32, pluginType string) (PluginDescriptor, error) {
	desc, err := DescribePluginBytes(ctx, entityIdentity, wasmBytes, maxMemoryPages, pluginType)
	if err != nil {
		return PluginDescriptor{}, err
	}
	if err := CrossCheckContract(entityIdentity, claim, desc); err != nil {
		return PluginDescriptor{}, err
	}
	return desc, nil
}

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

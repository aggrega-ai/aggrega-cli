// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"io"
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
// The load profile is selected by pluginType. A CLI plugin imports the terminal
// streaming emitters, which the build profile's shared allowlist rejects at load
// — before describe() can run — so a CLI plugin loads under the runtime CLI
// profile (LoadCLIAndDescribe), whose union allowlist admits the streaming
// imports; terminal output is discarded because only describe() is needed. Every
// other plugin type loads under the build profile (LoadAndDescribe).
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
// The load profile is selected by pluginType, exactly as the entity-fronted load
// gate selects it: a CLI plugin imports the terminal streaming emitters, which
// the build profile's shared allowlist rejects at load before describe() can run,
// so a CLI plugin loads under the runtime CLI profile whose union allowlist
// admits the streaming imports (its terminal output is discarded because only
// describe() is needed). Every other plugin type loads under the build profile.
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

	var (
		plugin *Plugin
		desc   PluginDescriptor
		err    error
	)
	// A CLI plugin imports the terminal streaming emitters, which the build
	// profile rejects at load before describe() can run; the CLI profile admits
	// them. Terminal output is discarded — only describe() is needed here.
	if pluginType == string(PluginTypeCLI) {
		plugin, desc, err = loader.LoadCLIAndDescribe(ctx, spec, io.Discard, io.Discard)
	} else {
		plugin, desc, err = loader.LoadAndDescribe(ctx, spec)
	}
	if err != nil {
		return PluginDescriptor{}, err
	}
	_ = plugin.Close(ctx)
	return desc, nil
}

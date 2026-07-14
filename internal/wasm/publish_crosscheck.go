// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import "context"

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
// The check loads the bytes and invokes describe() (LoadAndDescribe with a nil
// capability, so the load gate's own cross-check is skipped and this function
// owns the comparison), then compares the authored export claim against the
// descriptor via CrossCheckContract — the same primitive the load gate uses,
// so the publish and load surfaces share one comparison.
//
// maxMemoryPages caps the guest's linear memory for the describe() load. A large
// guest whose working set exceeds the platform default cap would fail to
// instantiate under the default; the caller supplies the entity's declared
// runtime cap (or a describe-time floor when the entity declares none) so the
// load matches the guest's authored footprint. The cap governs only the load;
// the capability comparison is independent of it.
//
// A disagreement halts with ErrWasmContractMismatch. The plugin is Closed
// before returning so a rejected publish leaks no instance. entityIdentity names
// the entity in any rejection message.
func CrossCheckPublishedPlugin(ctx context.Context, entityIdentity string, claim EntityContractClaim, wasmBytes []byte, maxMemoryPages uint32) error {
	// Build through the shared constructor so this loader carries the same
	// process-wide compilation cache every other loader uses, rather than a
	// bare cache-less loader that would recompile the guest in isolation.
	loader := NewCachingLoader(nil)
	plugin, desc, err := loader.LoadAndDescribe(ctx, PluginSpec{
		Name:           entityIdentity,
		Bytes:          wasmBytes,
		MaxMemoryPages: maxMemoryPages,
		// Contract nil: this function owns the cross-check, rather than
		// running it inside the load gate.
	})
	if err != nil {
		return err
	}
	defer func() { _ = plugin.Close(ctx) }()

	return CrossCheckContract(entityIdentity, claim, desc)
}

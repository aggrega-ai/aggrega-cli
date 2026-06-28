// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import "context"

// CrossCheckPublishedPlugin verifies, at publish time, that a WasmPlugin
// entity's authored claims agree with the .wasm bytes about to be published.
// It is the single home for the publish-side checks: the entity's authored
// capability claim and host-function declaration are cross-checked against the
// plugin's own self-report, so a published entity can never promise a
// capability or host surface the binary does not actually provide.
//
// The check loads the bytes and invokes describe() (LoadAndDescribe with a nil
// capability, so the load gate's own cross-check is skipped and this function
// owns the comparison), then runs two checks against the same loaded plugin:
//
//   - the authored export claim against the descriptor, via
//     CrossCheckCapabilities — the same primitive the load gate uses, so the
//     publish and load surfaces share one comparison;
//   - the authored host-function declaration (when non-empty) against the set
//     derived from the .wasm import section, via the same derive-and-compare the
//     loader performs, so a declaration that drifts from the binary halts.
//
// A disagreement halts with the existing Loud Guardrail sentinels
// (ErrWasmCapabilityMismatch / ErrWasmHostFunctionsDeclarationMismatch). The
// plugin is Closed before returning so a rejected publish leaks no instance.
// entityIdentity names the entity in any rejection message.
func CrossCheckPublishedPlugin(ctx context.Context, entityIdentity string, claim EntityCapabilityClaim, authoredHostFunctions []string, wasmBytes []byte) error {
	loader := &Loader{}
	plugin, desc, err := loader.LoadAndDescribe(ctx, PluginSpec{
		Name:  entityIdentity,
		Bytes: wasmBytes,
		// Capability nil: this function owns the cross-check, rather than
		// running it inside the load gate, so both the capability check and the
		// host-function check share one entrypoint here.
	})
	if err != nil {
		return err
	}
	defer func() { _ = plugin.Close(ctx) }()

	if err := CrossCheckCapabilities(entityIdentity, claim, desc); err != nil {
		return err
	}

	if len(authoredHostFunctions) > 0 {
		derived, err := deriveDeclaredHostFunctions(entityIdentity, wasmBytes)
		if err != nil {
			return err
		}
		if err := crossCheckAuthoredHostFunctions(entityIdentity, authoredHostFunctions, derived); err != nil {
			return err
		}
	}
	return nil
}

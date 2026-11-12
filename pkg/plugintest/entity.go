// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package plugintest

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ValidateEntity validates .wasm bytes against an authored Plugin entity in
// process: beyond the binary-only declared-vs-reported type check, it
// cross-checks the entity's authored capability claim, its machine-owned
// command mirror, and its machine-owned host-function record against the
// binary's describe() self-report and imports — the full gate the publish flow
// applies. On success it returns the projected report, whose Notices list the
// surfaces the binary offers that the entity does not curate (under-declaration
// is legitimate and never fails validation). On failure it returns the halting
// error, which a caller may errors.Is against this package's sentinels.
//
// The declared type, memory cap, and identity all come from the entity, so a
// caller supplies only the parsed entity and the bytes.
func ValidateEntity(bytes []byte, entity types.Plugin) (Report, error) {
	report, err := wasm.ValidatePlugin(context.Background(), bytes, wasm.ValidateOptions{
		DeclaredType:   entity.Metadata.PluginType,
		MaxMemoryPages: entityMaxMemoryPages(entity),
		Identity:       entity.Metadata.Name,
		Entity:         resolver.PluginEntityInputs(entity),
	})
	if err != nil {
		return Report{}, err
	}
	return projectReport(report), nil
}

// RequireValidEntity fails t when bytes do not validate against the authored
// Plugin entity. It is the entity-mode companion to RequireValid and applies
// the full publish gate — type, capability claim, command mirror, and
// host-function record — against the binary.
func RequireValidEntity(t testing.TB, bytes []byte, entity types.Plugin) {
	t.Helper()
	if _, err := ValidateEntity(bytes, entity); err != nil {
		t.Fatalf("plugintest: Plugin %q did not validate against its .wasm: %v", entity.Metadata.Name, err)
	}
}

// entityMaxMemoryPages returns the entity's declared describe-load memory cap,
// or zero (the platform default cap) when the entity declares no runtime block.
func entityMaxMemoryPages(entity types.Plugin) uint32 {
	if entity.Spec.Runtime != nil {
		return entity.Spec.Runtime.MaxMemoryPages
	}
	return 0
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"errors"
	"strings"
	"testing"
)

// TestCrossCheckHostFunctions exercises the check that makes describe()'s
// declared host_functions authoritative: the union of host functions declared
// across a descriptor's exports must equal, exactly, the set the guest imports
// from its .wasm. The imported set is ground truth; a declared-but-not-imported
// name or an imported-but-not-declared name is a Loud Guardrail halt.
func TestCrossCheckHostFunctions(t *testing.T) {
	t.Parallel()

	descWith := func(perExport ...[]string) PluginDescriptor {
		d := PluginDescriptor{
			AbiVersion: ABIVersionV1Alpha1,
			PluginType: string(PluginTypeProvider),
		}
		for i, hf := range perExport {
			name := providerRenderExportName
			if i > 0 {
				name = "assemble_boundary"
			}
			d.Exports = append(d.Exports, PluginExport{Name: name, HostFunctions: hf})
		}
		return d
	}

	cases := []struct {
		name     string
		desc     PluginDescriptor
		imported []string
		wantErr  bool
		wantSub  string
	}{
		{
			name:     "both empty agrees",
			desc:     descWith(nil),
			imported: nil,
		},
		{
			name:     "matching single import agrees",
			desc:     descWith([]string{"aggrega_fetch_external_input"}),
			imported: []string{"aggrega_fetch_external_input"},
		},
		{
			name:     "declared but not imported halts",
			desc:     descWith([]string{"aggrega_fetch_external_input"}),
			imported: nil,
			wantErr:  true,
			wantSub:  "does not import",
		},
		{
			name:     "imported but not declared halts",
			desc:     descWith(nil),
			imported: []string{"aggrega_fetch_external_input"},
			wantErr:  true,
			wantSub:  "does not declare",
		},
		{
			name: "union across exports matches module-level import set",
			desc: descWith(
				[]string{"aggrega_fetch_external_input"},
				[]string{"aggrega_log"},
			),
			imported: []string{"aggrega_fetch_external_input", "aggrega_log"},
		},
		{
			name: "union missing one imported name halts",
			desc: descWith(
				[]string{"aggrega_fetch_external_input"},
				nil,
			),
			imported: []string{"aggrega_fetch_external_input", "aggrega_log"},
			wantErr:  true,
			wantSub:  "aggrega_log",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := crossCheckHostFunctions("test-plugin", tc.desc, tc.imported)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected a halt, got nil")
				}
				if !errors.Is(err, ErrWasmManifestRejected) {
					t.Fatalf("expected ErrWasmManifestRejected, got %v", err)
				}
				if tc.wantSub != "" && !strings.Contains(err.Error(), tc.wantSub) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
		})
	}
}

// TestCrossCheckHostFunctionsDeterministicOffender confirms the first reported
// offender is stable across runs: the union is compared in sorted order, so a
// descriptor declaring two functions the binary lacks always names the same one.
func TestCrossCheckHostFunctionsDeterministicOffender(t *testing.T) {
	t.Parallel()

	desc := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeProvider),
		Exports: []PluginExport{
			{Name: providerRenderExportName, HostFunctions: []string{"aggrega_log", "aggrega_emit_warning"}},
		},
	}
	var last string
	for i := 0; i < 8; i++ {
		err := crossCheckHostFunctions("test-plugin", desc, nil)
		if err == nil {
			t.Fatalf("expected a halt")
		}
		if i > 0 && err.Error() != last {
			t.Fatalf("non-deterministic offender: %q vs %q", last, err.Error())
		}
		last = err.Error()
	}
	// Sorted order puts aggrega_emit_warning before aggrega_log.
	if !strings.Contains(last, "aggrega_emit_warning") {
		t.Fatalf("expected first sorted offender aggrega_emit_warning, got %q", last)
	}
}

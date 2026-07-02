// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import "testing"

// TestProgramName asserts the WASI argv label the loader supplies is never
// empty and composes the type and name when both are present, so a dependency
// reading os.Args[0] during package init never observes an empty argv.
func TestProgramName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		spec PluginSpec
		want string
	}{
		{"type and name", PluginSpec{Name: "helm", PluginType: PluginTypeProvider}, "provider-helm"},
		{"name only", PluginSpec{Name: "helm"}, "helm"},
		{"type only, no name falls back", PluginSpec{PluginType: PluginTypeLinter}, "aggrega-plugin"},
		{"neither", PluginSpec{}, "aggrega-plugin"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.spec.programName(); got != tc.want {
				t.Errorf("programName() = %q, want %q", got, tc.want)
			}
			if tc.spec.programName() == "" {
				t.Error("programName() must never return an empty string")
			}
		})
	}
}

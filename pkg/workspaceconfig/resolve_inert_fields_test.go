// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"errors"
	"strings"
	"testing"
)

// TestCheckInertProviderFieldsRefusesBothFields drives the provider-socket
// inert-field check directly, on a composed list rather than through a config
// file.
//
// It is in-package and direct for a reason worth stating, because a reader
// would otherwise reasonably move it out to the resolution tests beside its
// siblings. The `with` arm is reachable from a config file and is covered there.
// The `function` arm is not: the schema declares no `function` key on a provider
// binding, and every config file is validated before its typed decode, so a
// file-borne value is refused before this check runs. That leaves the arm real
// but unreachable through the package's exported surface — it is the layer
// behind the schema, and it is what a build resolving an older schema pin runs
// on. Driving it here is the only way to hold its behaviour, and its message,
// to what the check declares.
//
// The both-fields row pins the walk order the check documents: `function` is
// read before `with` on each binding, so a binding carrying both names
// `function`, and the reported field is stable for a given composed list rather
// than depending on iteration.
func TestCheckInertProviderFieldsRefusesBothFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		providers []PluginRef
		wantField string
	}{
		{
			name:      "function alone",
			providers: []PluginRef{{Capability: "helm", URI: "provider/helm:1", Function: "custom_render"}},
			wantField: "function",
		},
		{
			name:      "with alone",
			providers: []PluginRef{{Capability: "helm", URI: "provider/helm:1", With: &PluginRefWith{Settings: map[string]any{"k": "v"}}}},
			wantField: "with",
		},
		{
			name: "both fields report function first",
			providers: []PluginRef{{
				Capability: "helm",
				URI:        "provider/helm:1",
				Function:   "custom_render",
				With:       &PluginRefWith{Settings: map[string]any{"k": "v"}},
			}},
			wantField: "function",
		},
		{
			name:      "later binding is reached",
			providers: []PluginRef{{Capability: "helm", URI: "provider/helm:1"}, {Capability: "compose", URI: "provider/compose:1", Function: "custom_render"}},
			wantField: "function",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := checkInertProviderFields(tc.providers)
			if err == nil {
				t.Fatalf("a provider binding carrying %s returned nil; expected a refusal", tc.wantField)
			}
			if !errors.Is(err, ErrInertFieldOnProviderSocket()) {
				t.Fatalf("refusal must be ErrInertFieldOnProviderSocket; got: %v", err)
			}
			if !strings.Contains(err.Error(), "`"+tc.wantField+":`") {
				t.Errorf("refusal must name %q as the offending field; got: %v", tc.wantField, err)
			}
			if !strings.Contains(err.Error(), "provider") {
				t.Errorf("refusal must name the provider socket; got: %v", err)
			}
		})
	}
}

// TestCheckInertProviderFieldsAcceptsACleanBinding is the positive control: the
// rows above fail only because of the field each one plants, so a binding
// carrying neither must pass. Without it a check that refused everything would
// satisfy every row.
func TestCheckInertProviderFieldsAcceptsACleanBinding(t *testing.T) {
	t.Parallel()
	providers := []PluginRef{
		{Capability: "helm", URI: "provider/helm:1"},
		{Capability: "compose", URI: "provider/compose:1"},
	}
	if err := checkInertProviderFields(providers); err != nil {
		t.Fatalf("provider bindings carrying neither inert field must pass: %v", err)
	}
}

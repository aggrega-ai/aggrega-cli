// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"errors"
	"strings"
	"testing"
)

// TestADescriptorRefusesAColonInAServedProviderCapability drives a guest's
// describe() payload through the load path's own parse-and-validate — the bytes
// a guest returns, decoded and held to the descriptor contract — and asserts that
// a colon in a name a Recipe's capability reference would have to spell is
// refused, wrapping the manifest-rejected condition.
//
// A Recipe's capability reference is split at its last colon into the name and a
// version pin, so a provider serving a colon-bearing name could never be
// selected, and a pipeline step compatible with one could never match the
// provider rendering a scope. The controls are the colon-bearing names that stay
// legitimate — a parser's parse:<format> and an importer's import:<format>,
// resolved by format — and the colon-free provider and step names every shipped
// guest reports.
func TestADescriptorRefusesAColonInAServedProviderCapability(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		payload string
		refused string
	}{
		{
			name:    "a provider serving a colon-bearing capability is refused",
			payload: `{"abi_version":"v1alpha1","plugin_type":"provider","capabilities":["helm:1"],"exports":[{"name":"render_node"}]}`,
			refused: "helm:1",
		},
		{
			name:    "a colon in any one of a provider's served capabilities is refused",
			payload: `{"abi_version":"v1alpha1","plugin_type":"provider","capabilities":["helm","compose:v2"],"exports":[{"name":"render_node"}]}`,
			refused: "compose:v2",
		},
		{
			name:    "a pipeline step compatible with a colon-bearing provider capability is refused",
			payload: `{"abi_version":"v1alpha1","plugin_type":"pipeline-step","capabilities":["helm-chart:1.0.0"],"exports":[{"name":"step"}]}`,
			refused: "helm-chart:1.0.0",
		},
		{
			name:    "control: a provider serving colon-free capabilities loads",
			payload: `{"abi_version":"v1alpha1","plugin_type":"provider","capabilities":["helm","helm-chart"],"exports":[{"name":"render_node"}]}`,
		},
		{
			name:    "control: a pipeline step compatible with colon-free capabilities loads",
			payload: `{"abi_version":"v1alpha1","plugin_type":"pipeline-step","capabilities":["helm","compose"],"exports":[{"name":"step"}]}`,
		},
		{
			name:    "control: a parser's parse:<format> capabilities load",
			payload: `{"abi_version":"v1alpha1","plugin_type":"parser","capabilities":["parse:yaml","parse:hcl"],"exports":[{"name":"parse"}]}`,
		},
		{
			name:    "control: an importer's import:<format> capabilities load",
			payload: `{"abi_version":"v1alpha1","plugin_type":"importer","capabilities":["import:langflow","import:flowise"],"exports":[{"name":"import"}]}`,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseAndValidateDescriptor([]byte(tc.payload), "acme/guest")
			if tc.refused == "" {
				if err != nil {
					t.Fatalf("a legitimate descriptor was refused: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrWasmManifestRejected()) {
				t.Fatalf("want ErrWasmManifestRejected for the colon-bearing capability %q, got %v", tc.refused, err)
			}
			if !strings.Contains(err.Error(), tc.refused) || !strings.Contains(err.Error(), "acme/guest") {
				t.Errorf("the refusal must name the plugin and the offending name %q; got: %v", tc.refused, err)
			}
		})
	}
}

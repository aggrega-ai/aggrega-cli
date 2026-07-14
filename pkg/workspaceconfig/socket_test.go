// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestSocketPrefersCapabilityDefault pins the per-socket capability-default
// policy: provider and linter opt in (an unconfigured socket prefers a capable
// embedded plugin), while mutator, policy, and importer opt out (an
// unconfigured socket runs no plugin). The table is the single source of the
// policy, so a change to any socket's stance is a deliberate edit here.
func TestSocketPrefersCapabilityDefault(t *testing.T) {
	t.Parallel()
	cases := []struct {
		socket workspaceconfig.Socket
		want   bool
	}{
		{workspaceconfig.SocketProvider, true},
		{workspaceconfig.SocketLinter, true},
		{workspaceconfig.SocketMutator, false},
		{workspaceconfig.SocketPolicy, false},
		{workspaceconfig.SocketImporter, false},
	}
	for _, tc := range cases {
		if got := workspaceconfig.SocketPrefersCapabilityDefault(tc.socket); got != tc.want {
			t.Errorf("SocketPrefersCapabilityDefault(%q) = %v; want %v", tc.socket, got, tc.want)
		}
	}
}

// TestUnifiedPluginRefRoundTrips proves the one unified attachment shape carries
// all three selection forms — uri-only (direct attach), capability-only
// (resolve-by-capability), and capability+uri (disambiguate/override) — for
// every socket list on PluginsConfig, since they now share the PluginRef type.
func TestUnifiedPluginRefRoundTrips(t *testing.T) {
	t.Parallel()
	pc := workspaceconfig.PluginsConfig{
		Policy:    []workspaceconfig.PluginRef{{URI: "policy/opa:1.0.0"}},
		Providers: []workspaceconfig.PluginRef{{Capability: "helm"}, {Capability: "compose", URI: "provider/compose-alt"}},
	}
	if pc.Policy[0].URI != "policy/opa:1.0.0" || pc.Policy[0].Capability != "" {
		t.Errorf("uri-only policy ref not preserved: %+v", pc.Policy[0])
	}
	uris := pc.ProviderTargetURIs()
	if uris["helm"] != "" {
		t.Errorf("capability-only provider ref should project an empty URI; got %q", uris["helm"])
	}
	if uris["compose"] != "provider/compose-alt" {
		t.Errorf("capability+uri provider ref should project the bare identity; got %q", uris["compose"])
	}
}

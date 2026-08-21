// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// verdictSetConfig is the one fixture both tests below read: a config binding
// every socket, so the verdict-tier selection has something to reject at each
// one. The governance socket carries a transform role (mutating tier), both
// verdict roles, a URI-less verdict role, and a role that resolves to nothing;
// the linter socket carries a URI-bearing and a URI-less attachment.
func verdictSetConfig() workspaceconfig.Config {
	return workspaceconfig.Config{
		Plugins: workspaceconfig.PluginsConfig{
			Providers: []workspaceconfig.PluginRef{
				{URI: "plugin/render/helm:1.0.0", Capability: "helm"},
			},
			Governance: []workspaceconfig.PluginRef{
				{URI: "plugin/governance/labels:1.0.0", Role: plugintype.RoleLabelTransform},
				{URI: "plugin/governance/ir-gate:1.0.0", Role: plugintype.RoleIRVerdict},
				{URI: "plugin/governance/render-gate:1.0.0", Role: plugintype.RoleRenderVerdict},
				{Capability: "policy", Role: plugintype.RoleIRVerdict},
				{URI: "plugin/governance/unbound:1.0.0", Role: plugintype.Role("not-a-role")},
			},
			Linter: []workspaceconfig.PluginRef{
				{URI: "plugin/linter/publishing:1.0.0"},
				{Capability: "lint"},
			},
			Importer: []workspaceconfig.PluginRef{
				{URI: "plugin/importer/compose:1.0.0"},
			},
		},
	}
}

// TestVerdictAttachments_SpansEverySocketAtTheVerdictTier pins the accessor's
// contract: it selects the verdict tier across every socket — the two governance
// verdict roles and the linter socket alike — in socket order then authored
// order, tags each attachment with the socket it is bound to, and omits an
// attachment carrying no plugin URI. It is the direct test of the predicate the
// pull's plugin caching and its recorded verdict provenance both read.
func TestVerdictAttachments_SpansEverySocketAtTheVerdictTier(t *testing.T) {
	t.Parallel()
	got := verdictSetConfig().VerdictAttachments()
	want := []workspaceconfig.VerdictAttachment{
		{Socket: workspaceconfig.SocketGovernance, Ref: workspaceconfig.PluginRef{
			URI: "plugin/governance/ir-gate:1.0.0", Role: plugintype.RoleIRVerdict,
		}},
		{Socket: workspaceconfig.SocketGovernance, Ref: workspaceconfig.PluginRef{
			URI: "plugin/governance/render-gate:1.0.0", Role: plugintype.RoleRenderVerdict,
		}},
		{Socket: workspaceconfig.SocketLinter, Ref: workspaceconfig.PluginRef{
			URI: "plugin/linter/publishing:1.0.0",
		}},
	}
	if len(got) != len(want) {
		t.Fatalf("VerdictAttachments() = %+v (len %d); want len %d", got, len(got), len(want))
	}
	for i := range want {
		if got[i].Socket != want[i].Socket || got[i].Ref.URI != want[i].Ref.URI || got[i].Ref.Role != want[i].Ref.Role {
			t.Errorf("VerdictAttachments()[%d] = {socket %q, uri %q, role %q}; want {socket %q, uri %q, role %q}",
				i, got[i].Socket, got[i].Ref.URI, got[i].Ref.Role,
				want[i].Socket, want[i].Ref.URI, want[i].Ref.Role)
		}
	}
}

// TestVerdictAttachments_GovernanceProjectionMatchesTheGovernanceRefs proves the
// accessor is a widening and not a change: its governance-socket members are
// exactly the URI-bearing governance attachments bound to a verdict role, which
// is what the governance-socket accessor returns. The two are computed by
// different paths — one walks every socket by per-attachment tier, the other
// walks the governance list by role — so the comparison is a real cross-check
// rather than a restatement, and it is what pins the recorded verdict provenance
// to the same governance set across the widening.
func TestVerdictAttachments_GovernanceProjectionMatchesTheGovernanceRefs(t *testing.T) {
	t.Parallel()
	cfg := verdictSetConfig()
	var viaSockets []string
	for _, att := range cfg.VerdictAttachments() {
		if att.Socket == workspaceconfig.SocketGovernance {
			viaSockets = append(viaSockets, att.Ref.URI+"|"+string(att.Ref.Role))
		}
	}
	var viaGovernance []string
	for _, ref := range cfg.Plugins.VerdictGovernanceRefs() {
		if ref.URI == "" {
			continue
		}
		viaGovernance = append(viaGovernance, ref.URI+"|"+string(ref.Role))
	}
	if len(viaSockets) == 0 {
		t.Fatal("the fixture yielded no governance verdict attachment; the comparison would be vacuous")
	}
	if len(viaSockets) != len(viaGovernance) {
		t.Fatalf("governance projection = %v; governance refs = %v", viaSockets, viaGovernance)
	}
	for i := range viaSockets {
		if viaSockets[i] != viaGovernance[i] {
			t.Errorf("governance projection[%d] = %q; want %q", i, viaSockets[i], viaGovernance[i])
		}
	}
}

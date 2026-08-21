// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestResolveVerdictInputs_RecordsOnlyAnAttachmentItCanIdentify pins the
// narrowing the lockfile's verdict-input record forces. The record identifies an
// attachment by (plugin, role) and its published shape admits only the two
// verdict roles, so an attachment with no role — a linter attachment, which is a
// verdict attachment read from the same set — contributes no entry however many
// rule inputs it declares. Without the narrowing such an attachment would be
// recorded with an empty role, which the record's shape forbids and no consumer
// would reject, so this test is the enforcement of a constraint that is
// otherwise only declared.
func TestResolveVerdictInputs_RecordsOnlyAnAttachmentItCanIdentify(t *testing.T) {
	dir := t.TempDir()
	rule := filepath.Join(dir, "baseline.rego")
	if err := os.WriteFile(rule, []byte("package baseline\n"), 0o600); err != nil {
		t.Fatalf("write rule input: %v", err)
	}
	lintRule := filepath.Join(dir, "naming.rego")
	if err := os.WriteFile(lintRule, []byte("package naming\n"), 0o600); err != nil {
		t.Fatalf("write lint rule input: %v", err)
	}
	atts := []workspaceconfig.VerdictAttachment{
		{Socket: workspaceconfig.SocketGovernance, Ref: workspaceconfig.PluginRef{
			URI:  "plugin/governance/ir-gate:1.0.0",
			Role: plugintype.RoleIRVerdict,
			With: &workspaceconfig.PluginRefWith{Sources: []string{rule}},
		}},
		{Socket: workspaceconfig.SocketLinter, Ref: workspaceconfig.PluginRef{
			URI:  "plugin/linter/publishing:1.0.0",
			With: &workspaceconfig.PluginRefWith{Sources: []string{lintRule}},
		}},
	}
	got, err := resolveVerdictInputs(atts)
	if err != nil {
		t.Fatalf("resolveVerdictInputs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("verdict inputs = %+v; want exactly the governance-role record", got)
	}
	if got[0].Plugin != "plugin/governance/ir-gate:1.0.0" || got[0].Role != string(plugintype.RoleIRVerdict) {
		t.Errorf("verdict input = {plugin %q, role %q}; want {plugin %q, role %q}",
			got[0].Plugin, got[0].Role, "plugin/governance/ir-gate:1.0.0", plugintype.RoleIRVerdict)
	}
	for i, vi := range got {
		if vi.Role == "" {
			t.Errorf("verdict input %d carries an empty role: %+v", i, vi)
		}
	}
	if len(got[0].Sources) != 1 || got[0].Sources[0].Path != "baseline.rego" || got[0].Sources[0].Digest == "" {
		t.Errorf("verdict input sources = %+v; want one digested entry for baseline.rego", got[0].Sources)
	}
}

// TestResolveVerdictInputs_ASourcelessAttachmentContributesNoEntry pins the
// pre-existing contract across the widening: an attachment that declares no rule
// inputs is provenance-free and contributes no entry, so a workspace with no
// declared sources records nothing at all.
func TestResolveVerdictInputs_ASourcelessAttachmentContributesNoEntry(t *testing.T) {
	atts := []workspaceconfig.VerdictAttachment{
		{Socket: workspaceconfig.SocketGovernance, Ref: workspaceconfig.PluginRef{
			URI:  "plugin/governance/ir-gate:1.0.0",
			Role: plugintype.RoleIRVerdict,
		}},
		{Socket: workspaceconfig.SocketGovernance, Ref: workspaceconfig.PluginRef{
			URI:  "plugin/governance/render-gate:1.0.0",
			Role: plugintype.RoleRenderVerdict,
			With: &workspaceconfig.PluginRefWith{},
		}},
	}
	got, err := resolveVerdictInputs(atts)
	if err != nil {
		t.Fatalf("resolveVerdictInputs: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("verdict inputs = %+v; want none", got)
	}
}

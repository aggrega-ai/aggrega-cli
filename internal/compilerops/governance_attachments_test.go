// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// liveGovernanceConfig is a workspace configuration attaching one plugin at the
// label-transform role and one at the ir-verdict role, each naming a source file by
// path, as an author writes them.
func liveGovernanceConfig() *workspaceconfig.Config {
	cfg := &workspaceconfig.Config{}
	cfg.Plugins.Governance = []workspaceconfig.PluginRef{
		{URI: "governance/rego:1.0.0", Role: plugintype.RoleLabelTransform,
			With: &workspaceconfig.PluginRefWith{Sources: []string{"live-transform.rego"}}},
		{URI: "governance/rego:1.0.0", Role: plugintype.RoleIRVerdict,
			With: &workspaceconfig.PluginRefWith{Sources: []string{"verdict.rego"}}},
	}
	return cfg
}

// TestComposeGovernanceAttachments holds the attachment list every surface that
// dispatches a transform role runs: under a lockfile, each transform attachment is
// the one the pull recorded — its settings and its source files' recorded content —
// and the live verdict attachments follow; with no lockfile the configuration is
// returned as it stands. The caller's configuration is never written through.
func TestComposeGovernanceAttachments(t *testing.T) {
	recorded := &lockfile.Lockfile{Mutators: []lockfile.MutatorAttachment{
		{
			Plugin:   "governance/rego",
			Role:     string(plugintype.RoleLabelTransform),
			Settings: map[string]any{"tier": "gold"},
			Sources:  []lockfile.MutatorSourcePin{{Path: "pinned.rego", Content: "package pinned", Digest: "sha256:0"}},
		},
		{
			Plugin: "governance/rego",
			Role:   string(plugintype.RoleAnnotationTransform),
			Sources: []lockfile.MutatorSourcePin{
				{Path: "annotations.rego", Content: "package annotations", Digest: "sha256:1"},
			},
		},
	}}
	liveVerdict := liveGovernanceConfig().Plugins.Governance[1]

	rows := []struct {
		name     string
		lockfile *lockfile.Lockfile
		want     []workspaceconfig.PluginRef
		same     bool
	}{
		{
			name:     "no lockfile: the live list stands as authored",
			lockfile: nil,
			want:     liveGovernanceConfig().Plugins.Governance,
			same:     true,
		},
		{
			name:     "a lockfile: the recorded transforms with their recorded sources, then the live verdicts",
			lockfile: recorded,
			want: []workspaceconfig.PluginRef{
				{URI: "governance/rego", Role: plugintype.RoleLabelTransform, With: &workspaceconfig.PluginRefWith{
					Settings:      map[string]any{"tier": "gold"},
					PinnedSources: []workspaceconfig.PinnedSource{{Path: "pinned.rego", Content: "package pinned"}},
				}},
				{URI: "governance/rego", Role: plugintype.RoleAnnotationTransform, With: &workspaceconfig.PluginRefWith{
					PinnedSources: []workspaceconfig.PinnedSource{{Path: "annotations.rego", Content: "package annotations"}},
				}},
				liveVerdict,
			},
		},
		{
			name:     "a lockfile recording no transform: no transform runs, the live verdicts stay",
			lockfile: &lockfile.Lockfile{},
			want:     []workspaceconfig.PluginRef{liveVerdict},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			cfg := liveGovernanceConfig()
			got := ComposeGovernanceAttachments(cfg, row.lockfile)
			if got == nil {
				t.Fatal("the composition returned no configuration")
			}
			if (got == cfg) != row.same {
				t.Errorf("the composition returned the caller's own configuration: %v, want %v", got == cfg, row.same)
			}
			if !reflect.DeepEqual(got.Plugins.Governance, row.want) {
				t.Errorf("the composed attachment list is\n%#v\nwant\n%#v", got.Plugins.Governance, row.want)
			}
			if !reflect.DeepEqual(cfg.Plugins.Governance, liveGovernanceConfig().Plugins.Governance) {
				t.Errorf("the caller's configuration was written through: %#v", cfg.Plugins.Governance)
			}
		})
	}

	if got := ComposeGovernanceAttachments(nil, recorded); got != nil {
		t.Errorf("a missing configuration must stay missing, so the graph entry refuses it; got %#v", got)
	}
}

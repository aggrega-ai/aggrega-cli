// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestResolveRefusesAColonInAProviderBindingCapability drives the real
// configuration load over a provider binding whose capability carries a colon.
// A Recipe's capability reference is read as the capability up to its last colon
// and a version pin after it, so the capability a binding is looked up under
// never carries one, and a binding that does is one no Recipe can reach. The
// refusal is asserted against its condition's accessor, not its code.
//
// The controls are the colon-bearing spellings that stay legitimate on this
// surface: a binding's uri pinning a version, and an importer attachment's uri.
// The importer list shares the reference shape but is not judged, because an
// importer names its formats import:<format> with the colon part of the name.
func TestResolveRefusesAColonInAProviderBindingCapability(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		base       string
		child      string
		refused    bool
		capability string
	}{
		{
			name:       "a colon in a providers capability is refused",
			base:       "workspace: {}\nplugins:\n  providers:\n    - capability: \"helm:1.0.0\"\n",
			refused:    true,
			capability: "helm:1.0.0",
		},
		{
			name:       "a colon in a providers capability beside a uri is refused",
			base:       "workspace: {}\nplugins:\n  providers:\n    - capability: \"helm:^1.0.0\"\n      uri: provider/helm\n",
			refused:    true,
			capability: "helm:^1.0.0",
		},
		{
			name:       "a colon in a capability contributed by providers_append is refused on the composed list",
			base:       "workspace: {}\nplugins:\n  providers:\n    - capability: helm\n      uri: provider/helm\n",
			child:      "plugins:\n  providers_append:\n    - capability: \"compose:1\"\n",
			refused:    true,
			capability: "compose:1",
		},
		{
			name: "control: a bare capability whose uri pins a version loads",
			base: "workspace: {}\nplugins:\n  providers:\n    - capability: helm\n      uri: provider/helm:1.0.0\n",
		},
		{
			name: "control: a capability-only binding loads",
			base: "workspace: {}\nplugins:\n  providers:\n    - capability: helm-chart\n",
		},
		{
			name: "control: an importer attachment whose uri carries a colon loads",
			base: "workspace: {}\nplugins:\n  importer:\n    - uri: oci://h/imp:1\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			write(t, filepath.Join(root, ".aggrega-config.yaml"), tc.base)
			start := root
			if tc.child != "" {
				start = filepath.Join(root, "svc")
				write(t, filepath.Join(start, ".aggrega-config.yaml"), tc.child)
			}

			_, err := workspaceconfig.Resolve(start, "")
			if !tc.refused {
				if err != nil {
					t.Fatalf("Resolve refused a legitimate configuration: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Resolve accepted a provider binding whose capability %q carries a colon; expected a loud halt", tc.capability)
			}
			if !errors.Is(err, workspaceconfig.ErrColonInProviderBindingCapability()) {
				t.Fatalf("halt must be ErrColonInProviderBindingCapability; got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.capability) {
				t.Errorf("the refusal must name the offending capability %q; got: %v", tc.capability, err)
			}
		})
	}
}

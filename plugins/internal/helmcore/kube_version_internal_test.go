// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"testing"

	"helm.sh/helm/v3/pkg/chartutil"
)

// TestKubeVersionOfClientLibrary_FollowsHelmsBuildRule holds the mapping from a
// k8s.io/client-go module version to the Kubernetes version helm's build stamps
// for it: the major version plus one, the minor version as it is, a zero patch —
// whatever the module version's patch or pre-release — and a refusal for a module
// version carrying no numeric major and minor, rather than a guess.
func TestKubeVersionOfClientLibrary_FollowsHelmsBuildRule(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		module string
		want   chartutil.KubeVersion
		fails  bool
	}{
		{module: "v0.35.1", want: chartutil.KubeVersion{Version: "v1.35.0", Major: "1", Minor: "35"}},
		{module: "v0.20.0", want: chartutil.KubeVersion{Version: "v1.20.0", Major: "1", Minor: "20"}},
		{module: "v0.36.0-alpha.2", want: chartutil.KubeVersion{Version: "v1.36.0", Major: "1", Minor: "36"}},
		{module: "v1.2.3", want: chartutil.KubeVersion{Version: "v2.2.0", Major: "2", Minor: "2"}},
		{module: "v0", fails: true},
		{module: "", fails: true},
		{module: "vx.35.1", fails: true},
		{module: "v0.x.1", fails: true},
		{module: "v-1.35.1", fails: true},
	} {
		t.Run(tc.module, func(t *testing.T) {
			t.Parallel()
			got, err := kubeVersionOfClientLibrary(tc.module)
			if tc.fails {
				if err == nil {
					t.Fatalf("module version %q derived %+v; want a refusal", tc.module, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("module version %q: %v", tc.module, err)
			}
			if got != tc.want {
				t.Errorf("module version %q derived %+v, want %+v", tc.module, got, tc.want)
			}
		})
	}
}

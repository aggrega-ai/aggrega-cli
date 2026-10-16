// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"strconv"
	"testing"

	"helm.sh/helm/v4/pkg/chart/common"
)

// TestKubeVersionOfClientLibrary_FollowsHelmsRule holds the mapping from a
// k8s.io/client-go module version to the Kubernetes version helm derives for it:
// the major version plus one, the minor version as it is, a zero patch — whatever
// the module version's patch or pre-release — and a refusal for a module version
// carrying no numeric major and minor, rather than a guess.
func TestKubeVersionOfClientLibrary_FollowsHelmsRule(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		module                string
		version, major, minor string
		fails                 bool
	}{
		{module: "v0.37.1", version: "v1.37.0", major: "1", minor: "37"},
		{module: "v0.35.1", version: "v1.35.0", major: "1", minor: "35"},
		{module: "v0.20.0", version: "v1.20.0", major: "1", minor: "20"},
		{module: "v0.36.0-alpha.2", version: "v1.36.0", major: "1", minor: "36"},
		{module: "v1.2.3", version: "v2.2.0", major: "2", minor: "2"},
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
			if got.Version != tc.version || got.Major != tc.major || got.Minor != tc.minor {
				t.Errorf("module version %q derived %q (major %q, minor %q), want %q (major %q, minor %q)",
					tc.module, got.Version, got.Major, got.Minor, tc.version, tc.major, tc.minor)
			}
			// A chart printing the version whole reads its normalized form, which for a
			// derived version is the version itself.
			if s := got.String(); s != tc.version {
				t.Errorf("module version %q derived a version printing %q, want %q", tc.module, s, tc.version)
			}
		})
	}
}

// TestKubeVersionOfClientLibrary_BuildsTheValueTheSDKBuilds holds that a derived
// version is the very value the SDK builds for the same Kubernetes version, every
// field included — those a chart reads by name and the one it reads only by printing
// the version whole — so a chart rendered for the default reads what `helm template`
// gives it. The SDK's own built-in version is the oracle: whatever version it names,
// the client library version that maps to it derives a value equal to it.
func TestKubeVersionOfClientLibrary_BuildsTheValueTheSDKBuilds(t *testing.T) {
	t.Parallel()

	builtIn := common.DefaultCapabilities.KubeVersion
	major, majorErr := strconv.Atoi(builtIn.Major)
	if majorErr != nil || major < 1 || builtIn.Minor == "" {
		t.Fatalf("the SDK's built-in Kubernetes version %+v carries no major and minor to derive a client library version from", builtIn)
	}
	module := "v" + strconv.Itoa(major-1) + "." + builtIn.Minor + ".0"

	got, err := kubeVersionOfClientLibrary(module)
	if err != nil {
		t.Fatalf("module version %q: %v", module, err)
	}
	if got != builtIn {
		t.Errorf("module version %q derived %+v (printing %q), want the SDK's own %+v (printing %q)",
			module, got, got.String(), builtIn, builtIn.String())
	}
}

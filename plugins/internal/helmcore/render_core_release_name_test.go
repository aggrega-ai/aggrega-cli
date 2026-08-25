// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
)

// TestHelmEngine_Sourced_ReleaseNameSeparatesFoldedIdentities renders the two
// component identities that a substituting derivation gives one name to, and
// asserts the rendered bytes carry two distinct release names.
//
// The fixture chart names its Deployment after .Release.Name, so the assertion
// reads the release name the render actually produced rather than the
// derivation in isolation. The two identities differ only in where a dot and a
// hyphen sit, which is exactly what a derivation substituting one for the other
// discards — and the rendered output is the surface where that loss becomes two
// deployments claiming one in-cluster identity.
func TestHelmEngine_Sourced_ReleaseNameSeparatesFoldedIdentities(t *testing.T) {
	t.Parallel()
	names := map[string]string{}
	for _, id := range []string{"data-layer.main-db", "data.layer-main-db"} {
		files, err := helmcore.RenderLoadedChart(context.Background(),
			loadChart(t, "testdata/minimal-chart"),
			map[string]any{"image": "api:2.0.0", "replicas": float64(1)},
			nodeCtx(id, "testdata/minimal-chart"),
			helmcore.NewHermeticValuesValidator(forbidFetch(t)))
		if err != nil {
			t.Fatalf("render %q: %v", id, err)
		}
		var rendered string
		for _, v := range files {
			rendered = string(v)
		}
		name := metadataName(t, rendered)
		if prior, clash := names[name]; clash {
			t.Fatalf("component identities %q and %q both render the release name %q", prior, id, name)
		}
		names[name] = id
	}
}

// TestHelmEngine_Sourced_ReleaseNameEncodesTheIdentity pins the rendered release
// name for identities that carry characters Helm does not admit, so a change to
// the derivation shows as a changed expectation here rather than only as moved
// output elsewhere.
func TestHelmEngine_Sourced_ReleaseNameEncodesTheIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ id, want string }{
		{"api", "api"},
		{"backend-api", "backend-api"},
		{"data.main-db", "data-zdmain-db"},
		{"data-layer.main-db", "data-layer-zdmain-db"},
		{"data.layer-main-db", "data-zdlayer-main-db"},
		{"db_host", "db-zuhost"},
		{"api-zone", "api-zhzone"},
		{"API", "-zx000041-zx000050-zx000049"},
	} {
		files, err := helmcore.RenderLoadedChart(context.Background(),
			loadChart(t, "testdata/minimal-chart"),
			map[string]any{"image": "api:2.0.0", "replicas": float64(1)},
			nodeCtx(tc.id, "testdata/minimal-chart"),
			helmcore.NewHermeticValuesValidator(forbidFetch(t)))
		if err != nil {
			t.Fatalf("render %q: %v", tc.id, err)
		}
		var rendered string
		for _, v := range files {
			rendered = string(v)
		}
		if got := metadataName(t, rendered); got != tc.want {
			t.Errorf("component identity %q renders release name %q, want %q", tc.id, got, tc.want)
		}
	}
}

// metadataName returns the top-level metadata.name of a rendered single-document
// manifest, which the fixture chart sets from .Release.Name.
func metadataName(t *testing.T, doc string) string {
	t.Helper()
	for _, line := range strings.Split(doc, "\n") {
		if rest, ok := strings.CutPrefix(line, "  name: "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatalf("rendered document carries no metadata.name:\n%s", doc)
	return ""
}

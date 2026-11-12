// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// TestOptionalDiscriminator_E2E exercises the optional template helper through
// the full Step 1-5 pipeline against three Release variants:
//
//   - absent: the Release omits infra.replicas entirely; the rendered output
//     carries the fallback value (1).
//   - zero: the Release authors infra.replicas: 0; the rendered output carries
//     0 — the case that `| default 1` would have silently overridden to 1.
//   - three: the Release authors infra.replicas: 3; the rendered output carries
//     3 (positive control for the present-and-non-zero path).
//
// The Recipe template uses `optional .infra "replicas" 1` rather than
// `.infra.replicas | default 1`. The discriminator behavior is what the helper
// exists for; this test pins it end-to-end through the build-faithful harness
// (pipelinetest.Build) — the real pipeline with the CLI build's Options,
// including resolution-completeness.
func TestOptionalDiscriminator_E2E(t *testing.T) {
	t.Parallel()

	catalogPaths := []string{
		"testdata/optional-discriminator/catalog/components",
		"testdata/optional-discriminator/catalog/recipes",
		"testdata/optional-discriminator/catalog/providers",
	}

	cases := []struct {
		name         string
		releaseFile  string
		wantReplicas string
	}{
		{
			name:         "absent_falls_back",
			releaseFile:  "testdata/optional-discriminator/release-absent.yaml",
			wantReplicas: "REPLICA_COUNT: \"1\"",
		},
		{
			name:         "authored_zero_preserved",
			releaseFile:  "testdata/optional-discriminator/release-zero.yaml",
			wantReplicas: "REPLICA_COUNT: \"0\"",
		},
		{
			name:         "authored_three_preserved",
			releaseFile:  "testdata/optional-discriminator/release-three.yaml",
			wantReplicas: "REPLICA_COUNT: \"3\"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := pipelinetest.Build(t, pipelinetest.Input{
				ReleasePath:  tc.releaseFile,
				CatalogPaths: catalogPaths,
			})
			merged := joinFiles(result.Artifacts)
			if !strings.Contains(merged, tc.wantReplicas) {
				t.Errorf("expected %q in rendered output; got:\n%s", tc.wantReplicas, merged)
			}
		})
	}
}

// joinFiles concatenates all rendered file bodies into a single string for
// substring grepping in assertions.
func joinFiles(files map[string][]byte) string {
	var b strings.Builder
	for _, body := range files {
		b.Write(body)
		b.WriteString("\n")
	}
	return b.String()
}

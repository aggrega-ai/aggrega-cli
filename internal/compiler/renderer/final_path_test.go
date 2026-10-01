// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestComputeFinalPathIsSlashSeparated pins that the directory a node's output
// is grouped under is a slash-separated name on every platform.
//
// The value prefixes every file the group emits and reaches an assembling
// provider as its boundary path, so a host-spelled join would make the rendered
// output tree, and what an assembler derives from that path, depend on which
// platform ran the build. On a platform whose separator is a slash the two
// spellings coincide, so this passes there either way; it fails on one whose
// separator is a backslash the moment the join stops being a slash join.
func TestComputeFinalPathIsSlashSeparated(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		bp   string
		bi   *resolver.BoundaryInfra
		want string
	}{
		{
			name: "default directory and sub-directory",
			bp:   "prod",
			want: "boundaries/prod/helm",
		},
		{
			name: "nested boundary path under the default directory",
			bp:   "aws-prod/eks-main",
			want: "boundaries/aws-prod/eks-main/helm",
		},
		{
			name: "authored directory and sub-directory each spanning segments",
			bp:   "prod",
			bi: &resolver.BoundaryInfra{
				Directory:    "clusters/{{ .boundary_name }}",
				SubDirectory: "apps/{{ .capability }}",
			},
			want: "clusters/prod/apps/helm",
		},
		{
			name: "synthesized default boundary emits without a directory segment",
			bp:   "default",
			bi:   &resolver.BoundaryInfra{Synthesized: true},
			want: "helm",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := computeFinalPath("web", tc.bp, "helm", "web-recipe", tc.bi, nil, nil, nil)
			if err != nil {
				t.Fatalf("computeFinalPath: %v", err)
			}
			if got != tc.want {
				t.Errorf("computeFinalPath = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, `\`) {
				t.Errorf("computeFinalPath = %q carries a backslash; an output group's directory is a slash-separated name", got)
			}
		})
	}
}

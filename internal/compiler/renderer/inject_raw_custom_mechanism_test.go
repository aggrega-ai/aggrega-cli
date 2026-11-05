// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// selfInjectEntry is one authored delivery entry of a component's own inject
// block, in the shape the block's decode gives it.
func selfInjectEntry(in, value string) map[string]any {
	return map[string]any{"in": in, "value": value}
}

// TestInjectRaw_CarriesAuthoredCustomMechanisms pins that .inject_raw — the inject
// block a component's author wrote, unresolved — carries every custom mechanism
// under the name the author gave it, in the shape the core mechanisms take there:
// the list of delivery entries, in the order they were written. It is proved
// against emitted output, through the render a build runs.
//
// The regression it pins: the authored block routes custom mechanisms into a field
// no struct tag exposes, and the template view is a reflective tag walk, so
// .inject_raw silently omitted them — {} when a custom mechanism was the only one —
// while .context.inject and its all view carried them. The resolved pipe reads are
// asserted alongside, byte for byte, because exposing the authored block must not
// move them; and the core-only row pins that a block authoring no custom mechanism
// renders exactly what it did.
func TestInjectRaw_CarriesAuthoredCustomMechanisms(t *testing.T) {
	t.Parallel()

	// The custom mechanism's two entries are written reverse-alphabetically, so a
	// projection that re-sorted them would show.
	ssmEntries := []any{selfInjectEntry("SELF_SSM_Z", "s1"), selfInjectEntry("SELF_SSM_A", "s2")}
	const ssmRaw = `[{"in":"SELF_SSM_Z","value":"s1"},{"in":"SELF_SSM_A","value":"s2"}]`

	rows := []struct {
		name  string
		block func() *types.InjectBlock
		reads string
		want  []string
	}{
		{
			name: "a custom mechanism beside env",
			block: func() *types.InjectBlock {
				b := &types.InjectBlock{Env: []any{selfInjectEntry("SELF_ENV", "e1")}}
				b.Additional.Set("ssm_param", ssmEntries)
				return b
			},
			reads: `      RAW: '{{ toJson .inject_raw }}'
      RAW_SSM: '{{ toJson .inject_raw.ssm_param }}'
      PIPE_SSM: '{{ toJson .context.inject.ssm_param }}'
      PIPE_ENV: '{{ toJson .context.inject.env }}'
      PIPE_ALL: '{{ toJson .context.inject.all }}'
`,
			want: []string{
				`name: RAW value: '{"env":[{"in":"SELF_ENV","value":"e1"}],"ssm_param":` + ssmRaw + `}'`,
				`name: RAW_SSM value: '` + ssmRaw + `'`,
				`name: PIPE_SSM value: '{"SELF_SSM_A":"s2","SELF_SSM_Z":"s1"}'`,
				`name: PIPE_ENV value: '{"SELF_ENV":"e1"}'`,
				`name: PIPE_ALL value: '{"SELF_ENV":"e1","SELF_SSM_A":"s2","SELF_SSM_Z":"s1"}'`,
			},
		},
		{
			name: "a custom mechanism alone",
			block: func() *types.InjectBlock {
				b := &types.InjectBlock{}
				b.Additional.Set("ssm_param", ssmEntries)
				return b
			},
			reads: `      RAW: '{{ toJson .inject_raw }}'
      PIPE_SSM: '{{ toJson .context.inject.ssm_param }}'
      PIPE_ALL: '{{ toJson .context.inject.all }}'
`,
			want: []string{
				`name: RAW value: '{"ssm_param":` + ssmRaw + `}'`,
				`name: PIPE_SSM value: '{"SELF_SSM_A":"s2","SELF_SSM_Z":"s1"}'`,
				`name: PIPE_ALL value: '{"SELF_SSM_A":"s2","SELF_SSM_Z":"s1"}'`,
			},
		},
		{
			name: "core mechanisms only",
			block: func() *types.InjectBlock {
				return &types.InjectBlock{Env: []any{selfInjectEntry("SELF_ENV", "e1")}}
			},
			reads: `      RAW: '{{ toJson .inject_raw }}'
      PIPE_ENV: '{{ toJson .context.inject.env }}'
      PIPE_ALL: '{{ toJson .context.inject.all }}'
`,
			want: []string{
				`name: RAW value: '{"env":[{"in":"SELF_ENV","value":"e1"}]}'`,
				`name: PIPE_ENV value: '{"SELF_ENV":"e1"}'`,
				`name: PIPE_ALL value: '{"SELF_ENV":"e1"}'`,
			},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			// Every row reads the merged union, so it marks the artifact the
			// workload's variables landed in.
			raw := apiServerFixture{
				aggregateName: "inject-raw-app",
				selfInject:    row.block(),
				reads:         row.reads,
			}.render(t, "PIPE_ALL")
			got := flattenWhitespace(raw)
			for _, want := range row.want {
				if !strings.Contains(got, want) {
					t.Errorf("emitted artifact missing %q; got:\n%s", want, raw)
				}
			}
		})
	}
}

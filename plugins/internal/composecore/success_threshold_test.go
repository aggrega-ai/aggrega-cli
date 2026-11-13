// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composecore

import (
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestUnsupportedField_ProbeSuccessThreshold proves an authored successThreshold
// on the probe the compose healthcheck is translated from is reported with the
// unsupported-field warning, named by its path in the health block, whatever its
// value — a compose healthcheck has no success threshold, and an authored `1`
// is a field the author wrote and can remove, as an authored `replicas: 1` is.
// Each authored row is paired with the same health block without the field: the
// rendered healthcheck is identical, so the warning is the only trace the field
// leaves. A slot the collapse drops is reported whole by its own warning and its
// fields are not reported again; the field on a sidecar names that container.
func TestUnsupportedField_ProbeSuccessThreshold(t *testing.T) {
	t.Parallel()

	probe := func(set func(p *types.ProbeSpec)) *types.ProbeSpec {
		p := &types.ProbeSpec{HTTP: "/healthz", PeriodSeconds: ptrInt(10), FailureThreshold: ptrInt(4)}
		if set != nil {
			set(p)
		}
		return p
	}
	withThreshold := func(n int) func(p *types.ProbeSpec) {
		return func(p *types.ProbeSpec) { p.SuccessThreshold = ptrInt(n) }
	}

	for _, tc := range []struct {
		name      string
		container string
		health    func(set func(p *types.ProbeSpec)) *types.HealthSpec
		threshold int
		want      string // the warned field; "" when no successThreshold warning is expected
	}{
		{
			name: "the live probe at 1", container: "main", threshold: 1,
			health: func(set func(p *types.ProbeSpec)) *types.HealthSpec { return &types.HealthSpec{Live: probe(set)} },
			want:   "containers.main.health.live.successThreshold",
		},
		{
			name: "the ready probe alone at 3", container: "main", threshold: 3,
			health: func(set func(p *types.ProbeSpec)) *types.HealthSpec {
				p := probe(set)
				p.HTTP, p.TCP = "", 8080
				return &types.HealthSpec{Ready: p}
			},
			want: "containers.main.health.ready.successThreshold",
		},
		{
			name: "a sidecar's startup probe", container: "side", threshold: 1,
			health: func(set func(p *types.ProbeSpec)) *types.HealthSpec {
				p := probe(set)
				p.HTTP, p.Exec = "", []string{"true"}
				return &types.HealthSpec{Startup: p}
			},
			want: "containers.side.health.startup.successThreshold",
		},
		{
			name: "on a slot the collapse drops", container: "main", threshold: 2,
			health: func(set func(p *types.ProbeSpec)) *types.HealthSpec {
				ready := probe(set)
				ready.HTTP, ready.TCP = "", 8080
				return &types.HealthSpec{Live: probe(nil), Ready: ready}
			},
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			synth := func(set func(p *types.ProbeSpec)) (string, []string) {
				t.Helper()
				w := baseDegradeWorkload()
				if tc.container == "main" {
					withMainContainer(w, func(c *types.ContainerSpec) { c.Health = tc.health(set) })
				} else {
					w.Containers.Set(tc.container, types.ContainerSpec{Image: "side:1.0", Health: tc.health(set)})
				}
				files, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
				if err != nil {
					t.Fatalf("synth: %v", err)
				}
				var warned []string
				for _, d := range diags {
					if d.Code != rendercore.UnsupportedFieldCode || d.Severity != abi.SeverityWarn {
						continue
					}
					want := rendercore.UnsupportedFieldDiagnostic(composeTargetName, d.SourceRef)
					if d.Message == want.Message {
						warned = append(warned, d.SourceRef)
					}
				}
				var rendered strings.Builder
				for _, f := range files {
					rendered.WriteString(f.Name)
					rendered.WriteString("\n")
					rendered.Write(f.Body)
				}
				return rendered.String(), warned
			}

			withHC, withWarned := synth(withThreshold(tc.threshold))
			withoutHC, withoutWarned := synth(nil)

			if len(withoutWarned) != 0 {
				t.Errorf("the health block without successThreshold drew unsupported-field warnings: %v", withoutWarned)
			}
			var got []string
			for _, f := range withWarned {
				if f == tc.want || (tc.want == "" && len(f) > len("successThreshold") &&
					f[len(f)-len("successThreshold"):] == "successThreshold") {
					got = append(got, f)
				}
			}
			switch {
			case tc.want == "" && len(got) != 0:
				t.Errorf("a field inside a dropped slot was reported again: %v", got)
			case tc.want != "" && (len(got) != 1 || got[0] != tc.want):
				t.Errorf("warned %v, want exactly %q (all unsupported-field warnings: %v)", got, tc.want, withWarned)
			}
			if !strings.Contains(withHC, "healthcheck:") {
				t.Fatalf("no healthcheck was rendered, so the comparison below is vacuous:\n%s", withHC)
			}
			if withHC != withoutHC {
				t.Errorf("successThreshold changed the rendered output:\nwith:\n%s\nwithout:\n%s", withHC, withoutHC)
			}
		})
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"bytes"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// healthWorkload returns the baseline single-container Workload with the given
// health block on its main container.
func healthWorkload(h types.HealthSpec) *types.Workload {
	w := baseDegradeWorkload()
	withMainContainer(w, func(c *types.ContainerSpec) {
		c.Health = &h
	})
	return w
}

// shippedShape is the probe pairing the compose recipes in the catalog author: a
// tcp readiness probe alongside an exec liveness probe. It is spelled out here so
// the unit cases exercise the same shape the shipped content does.
func shippedShape() types.HealthSpec {
	return types.HealthSpec{
		Ready: &types.ProbeSpec{TCP: 3306, InitialDelaySeconds: ptrInt(10), PeriodSeconds: ptrInt(5)},
		Live:  &types.ProbeSpec{Exec: []string{"mysqladmin", "ping"}, TimeoutSeconds: ptrInt(5), PeriodSeconds: ptrInt(10)},
	}
}

const healthCoordinate = "containers.main.health"

// healthDiagnosticsAt returns the diagnostics a synthesis raised at the given
// authored coordinate.
func healthDiagnosticsAt(t *testing.T, w *types.Workload, coordinate string) []string {
	t.Helper()
	_, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	var msgs []string
	for _, d := range diags {
		if d.SourceRef != coordinate {
			continue
		}
		if d.Severity != abi.SeverityWarn {
			t.Errorf("severity = %q, want warn", d.Severity)
		}
		if d.Code != rendercore.UnsupportedFieldCode {
			t.Errorf("code = %q, want %q", d.Code, rendercore.UnsupportedFieldCode)
		}
		msgs = append(msgs, d.Message)
	}
	return msgs
}

// TestHealthDegrade_DroppedSlotsAreNamed proves a health block whose probe slots
// collapse announces the collapse, naming both the slot that survived and every
// slot whose authored intent reaches no part of the output.
//
// A compose service carries a single healthcheck, so an author who declares more
// than one probe slot keeps only the preferred one. Without this warning the rest
// are validated and then discarded in silence, which is the shape the shipped
// compose recipes have.
func TestHealthDegrade_DroppedSlotsAreNamed(t *testing.T) {
	t.Parallel()
	ready := &types.ProbeSpec{HTTP: "/readyz"}
	live := &types.ProbeSpec{HTTP: "/livez"}
	startup := &types.ProbeSpec{HTTP: "/startupz"}

	tests := []struct {
		scenario    string
		health      types.HealthSpec
		wantKept    string
		wantDropped []string
	}{
		{
			"ready and live, the shape the shipped recipes author",
			shippedShape(),
			"live", []string{"ready"},
		},
		{
			"ready and startup: startup yields to ready",
			types.HealthSpec{Ready: ready, Startup: startup},
			"ready", []string{"startup"},
		},
		{
			"live and startup: startup yields to live",
			types.HealthSpec{Live: live, Startup: startup},
			"live", []string{"startup"},
		},
		{
			"all three slots: two are dropped and both are named",
			types.HealthSpec{Ready: ready, Live: live, Startup: startup},
			"live", []string{"ready", "startup"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.scenario, func(t *testing.T) {
			t.Parallel()
			msgs := healthDiagnosticsAt(t, healthWorkload(tc.health), healthCoordinate)
			if len(msgs) != 1 {
				t.Fatalf("got %d diagnostics at %s, want exactly 1: %q", len(msgs), healthCoordinate, msgs)
			}
			msg := msgs[0]

			if !strings.Contains(msg, healthCoordinate) {
				t.Errorf("message %q does not name the authored coordinate", msg)
			}
			if !strings.Contains(msg, "does not fully support") {
				t.Errorf("message %q missing the partial-support wording; the block is reduced, not dropped", msg)
			}
			if !strings.Contains(msg, "only the "+tc.wantKept+" probe is translated") {
				t.Errorf("message %q does not name %q as the surviving slot", msg, tc.wantKept)
			}
			for _, slot := range tc.wantDropped {
				if !strings.Contains(msg, slot) {
					t.Errorf("message %q does not name the dropped %q slot", msg, slot)
				}
			}
			// The surviving slot must not be reported as dropped. The phrasing puts
			// the kept slot and the dropped ones in one sentence, so a drop-set that
			// wrongly included the winner would still contain every expected
			// substring above — this is what separates the two.
			if strings.Contains(msg, tc.wantKept+" probe is dropped") ||
				strings.Contains(msg, tc.wantKept+" probes are dropped") {
				t.Errorf("message %q reports the surviving %q slot as dropped", msg, tc.wantKept)
			}
		})
	}
}

// TestHealthDegrade_CleanTranslationIsSilent is the load-bearing negative: a
// health block that loses nothing must say nothing. A single declared probe slot
// translates in full, so a warning there would be noise on correct authoring —
// and the single-readiness-probe shape is what the documented examples teach.
func TestHealthDegrade_CleanTranslationIsSilent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		scenario string
		health   *types.HealthSpec
	}{
		{"readiness probe only, as the documented examples author it",
			&types.HealthSpec{Ready: &types.ProbeSpec{HTTP: "/healthz"}}},
		{"liveness probe only",
			&types.HealthSpec{Live: &types.ProbeSpec{HTTP: "/livez"}}},
		{"startup probe only",
			&types.HealthSpec{Startup: &types.ProbeSpec{HTTP: "/startupz"}}},
		{"a health block declaring no probe slot at all", &types.HealthSpec{}},
		{"no health block", nil},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.scenario, func(t *testing.T) {
			t.Parallel()
			w := baseDegradeWorkload()
			if tc.health != nil {
				w = healthWorkload(*tc.health)
			}
			_, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
			if err != nil {
				t.Fatalf("synth: %v", err)
			}
			for _, d := range diags {
				if strings.Contains(d.SourceRef, "health") || strings.Contains(d.Message, "probe") {
					t.Errorf("a clean translation raised a diagnostic: %+v", d)
				}
			}
		})
	}
}

// TestHealthDegrade_Deterministic proves the warning's text is byte-identical
// across repeated synthesis of identical input, which the deterministic-output
// invariant requires of every surface a build produces.
//
// The shape under test names two dropped slots, because a one-name message cannot
// exhibit an ordering difference and so cannot fail this. Repeated rather than
// paired: an ordering that is stable in most runs and not all is the failure mode
// a single comparison misses.
func TestHealthDegrade_Deterministic(t *testing.T) {
	t.Parallel()
	build := func() *types.Workload {
		return healthWorkload(types.HealthSpec{
			Ready:   &types.ProbeSpec{HTTP: "/readyz"},
			Live:    &types.ProbeSpec{HTTP: "/livez"},
			Startup: &types.ProbeSpec{HTTP: "/startupz"},
		})
	}

	first := healthDiagnosticsAt(t, build(), healthCoordinate)
	if len(first) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %q", len(first), first)
	}

	const runs = 32
	for i := 0; i < runs; i++ {
		got := healthDiagnosticsAt(t, build(), healthCoordinate)
		if len(got) != 1 {
			t.Fatalf("run %d: got %d diagnostics, want 1: %q", i, len(got), got)
		}
		if got[0] != first[0] {
			t.Fatalf("run %d produced a different message:\n first: %q\n   got: %q", i, first[0], got[0])
		}
	}
}

// TestHealthDegrade_OutOfBand proves the warning is advisory and the collapse it
// reports is unchanged by it: a block declaring readiness alongside liveness
// renders byte-identically to one declaring liveness alone, so the rendered
// healthcheck a reader gets is the same either way and only the diagnostic
// differs.
func TestHealthDegrade_OutOfBand(t *testing.T) {
	t.Parallel()
	live := &types.ProbeSpec{Exec: []string{"mysqladmin", "ping"}, TimeoutSeconds: ptrInt(5), PeriodSeconds: ptrInt(10)}

	liveOnly, quietDiags, err := SynthesizeWorkloadCore(
		healthWorkload(types.HealthSpec{Live: live}), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth with liveness alone: %v", err)
	}
	for _, d := range quietDiags {
		if d.SourceRef == healthCoordinate {
			t.Fatalf("liveness alone raised a health diagnostic: %+v", d)
		}
	}

	degraded, degradedDiags, err := SynthesizeWorkloadCore(
		healthWorkload(shippedShape()), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth with the degrading shape: %v", err)
	}
	var found bool
	for _, d := range degradedDiags {
		if d.SourceRef == healthCoordinate {
			found = true
		}
	}
	if !found {
		t.Fatalf("the degrading shape raised no health diagnostic (all: %+v)", degradedDiags)
	}

	if !bytes.Equal(liveOnly[0].Body, degraded[0].Body) {
		t.Errorf("the dropped readiness probe changed rendered bytes (the warning must be out-of-band, and the selection unchanged)\n--- liveness alone ---\n%s\n--- with the dropped readiness probe ---\n%s",
			liveOnly[0].Body, degraded[0].Body)
	}
}

// TestHealthDegrade_PerContainerCoordinate proves each degrading container is
// reported at its own authored coordinate, in a deterministic order.
//
// The fixture is deliberately hostile: three containers degrade, one translates
// cleanly, and an unnamed init container degrades too. A warn-once-per-workload
// bug, a wrong-coordinate bug, a warn-on-every-container bug and an
// ordering regression each fail here; a single-container fixture would detect
// none of them.
func TestHealthDegrade_PerContainerCoordinate(t *testing.T) {
	t.Parallel()
	w := healthWorkload(shippedShape())

	// A second degrading container.
	agent := types.ContainerSpec{Image: "agent:1.0"}
	agent.Ports.Set("metrics", 9090)
	agent.Health = &types.HealthSpec{
		Ready:   &types.ProbeSpec{HTTP: "/readyz"},
		Startup: &types.ProbeSpec{HTTP: "/startupz"},
	}
	w.Containers.Set("agent", agent)

	// A cleanly-translating container beside them: it must contribute nothing.
	quiet := types.ContainerSpec{Image: "quiet:1.0"}
	quiet.Ports.Set("http", 8081)
	quiet.Health = &types.HealthSpec{Ready: &types.ProbeSpec{HTTP: "/healthz"}}
	w.Containers.Set("quiet", quiet)

	// Deliberately unnamed: a positional init entry need not carry a name, so a
	// coordinate read off the container's name would be hollow here.
	migrate := types.ContainerSpec{Image: "migrate:1.0"}
	migrate.Ports.Set("admin", 7000)
	migrate.Health = &types.HealthSpec{
		Ready: &types.ProbeSpec{HTTP: "/readyz"},
		Live:  &types.ProbeSpec{HTTP: "/livez"},
	}
	w.InitContainers = append(w.InitContainers, migrate)

	_, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth: %v", err)
	}

	// Exact sequence, not set membership: containers in author order, then the
	// init containers, so an ordering regression fails too.
	want := []string{
		"containers.main.health",
		"containers.agent.health",
		"initContainers[0].health",
	}
	got := make([]string, 0, len(diags))
	for _, d := range diags {
		got = append(got, d.SourceRef)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d diagnostics, want %d (all: %+v)", len(got), len(want), diags)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("diagnostic %d source ref = %q, want %q (all: %+v)", i, got[i], want[i], diags)
		}
	}
}

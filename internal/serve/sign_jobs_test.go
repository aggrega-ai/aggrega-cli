// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"io"
	"strings"
	"testing"
)

// TestTheDaemonRefusesASigningBoundBelowOne drives aggrega/signArtifacts over the
// wire with jobs below 1 and requires an invalid-params refusal naming the field,
// in this surface's vocabulary rather than a flag's. The refusal comes from the
// request's projection, before the signing operation is reached, so no reference
// is read.
func TestTheDaemonRefusesASigningBoundBelowOne(t *testing.T) {
	t.Parallel()
	srv, _ := publishFixture(t, defaultRegistryWorkspace)
	refs := []string{publishTestRegistry + "/component/core/api-server:1.0.0"}

	for _, row := range []struct {
		name string
		jobs any
		want string
	}{
		{"a zero bound", 0, "signArtifacts: jobs must be at least 1, got 0"},
		{"a negative bound", -2, "signArtifacts: jobs must be at least 1, got -2"},
	} {
		t.Run(row.name, func(t *testing.T) {
			resp := callDaemonMethod(t, srv, "aggrega/signArtifacts", map[string]any{"references": refs, "jobs": row.jobs})
			if resp.Error == nil {
				t.Fatalf("a bound below 1 must be refused; got result %s", resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("the refusal carries code %d; want invalid params (%d)", resp.Error.Code, jsonRPCInvalidParams)
			}
			if !strings.Contains(resp.Error.Message, row.want) {
				t.Errorf("the refusal reads %q; want it to carry %q", resp.Error.Message, row.want)
			}
			if strings.Contains(resp.Error.Message, "--") {
				t.Errorf("a refusal on this surface must not name a command line flag: %q", resp.Error.Message)
			}
		})
	}
}

// TestTheDaemonCarriesTheSigningBound holds the request's projection onto the
// signing operation: an absent bound is 1, a bound sent is carried as sent, and a
// bound below 1 projects nothing.
func TestTheDaemonCarriesTheSigningBound(t *testing.T) {
	t.Parallel()
	srv, _ := publishFixture(t, defaultRegistryWorkspace)
	refs := []string{publishTestRegistry + "/component/core/api-server:1.0.0"}
	four, zero := 4, 0

	for _, row := range []struct {
		name    string
		jobs    *int
		want    int
		refused bool
	}{
		{"absent signs one after another", nil, 1, false},
		{"a bound sent is carried", &four, 4, false},
		{"a zero bound is refused rather than read as absent", &zero, 0, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			cfg, paramErr := srv.signArtifactsConfig(signArtifactsParams{References: refs, Jobs: row.jobs}, io.Discard)
			if row.refused {
				if paramErr == nil {
					t.Fatalf("the projection carried a bound of %d instead of refusing it", cfg.Jobs)
				}
				return
			}
			if paramErr != nil {
				t.Fatalf("signArtifactsConfig: %s", paramErr.Message)
			}
			if cfg.Jobs != row.want {
				t.Errorf("the projection carries a bound of %d; want %d", cfg.Jobs, row.want)
			}
		})
	}
}

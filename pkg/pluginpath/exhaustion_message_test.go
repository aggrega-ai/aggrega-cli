// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestAnExhaustedCascadeNamesOnlyTheSourcesItReads holds the exhaustion's wording
// to what the cascade does. Every tier reads bytes already on the machine, so the
// message lists exactly those tiers, names no registry among them, and reports no
// remote source — and the halt is not the remote-fetch condition, because no fetch
// was attempted.
func TestAnExhaustedCascadeNamesOnlyTheSourcesItReads(t *testing.T) {
	cases := []struct {
		name    string
		uri     string
		want    []string
		mustNot []string
	}{
		{
			name:    "a bare plugin coordinate",
			uri:     "pipeline-step/absent-everywhere",
			want:    []string{`plugin "pipeline-step/absent-everywhere" not resolvable through any tier (embed, colocated, local, cache)`},
			mustNot: []string{"registry", "remote source"},
		},
		{
			name:    "a reference shaped like a registry location",
			uri:     "oci://hub.aggrega.ai/plugin/linter/absent:1.0.0",
			want:    []string{"not resolvable", "does not fetch from a registry"},
			mustNot: []string{"remote source"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolver{workspaceRoot: t.TempDir(), cacheDir: ".aggrega/cache"}.Resolve(tc.uri)
			if err == nil {
				t.Fatal("expected the resolution to halt; got nil")
			}
			msg := err.Error()
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("message does not carry %q: %s", want, msg)
				}
			}
			for _, not := range tc.mustNot {
				if strings.Contains(msg, not) {
					t.Errorf("message names %q, which the cascade neither consults nor contacts: %s", not, msg)
				}
			}
			if errors.Is(err, oci.ErrFetchNotFound()) {
				t.Errorf("the exhaustion reports the remote-fetch condition although nothing was fetched: %v", err)
			}
		})
	}
}

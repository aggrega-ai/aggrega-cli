// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestTheSigningToolRefusesABoundBelowOne drives sign-artifacts through the tool
// dispatch with jobs below 1 and requires a tool-level refusal naming the
// argument. The call names a workspace root that does not exist, so a refusal that
// came after the workspace was resolved — let alone after a reference was read —
// would be a different refusal; the control row, a legal bound, is that one.
func TestTheSigningToolRefusesABoundBelowOne(t *testing.T) {
	t.Parallel()
	s := newGuidanceServer(t)
	missingRoot := filepath.Join(t.TempDir(), "no-such-workspace")

	for _, row := range []struct {
		name string
		jobs string
		want string
	}{
		{"a zero bound", "0", "sign-artifacts was sent 'jobs' of 0"},
		{"a negative bound", "-3", "sign-artifacts was sent 'jobs' of -3"},
		{"control: a legal bound passes the check", "2", ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			payload := `{"references":["registry.example.test/component/core/api-server:1.0.0"],"jobs":` + row.jobs +
				`,"workspace_root":` + jsonQuote(missingRoot) + `}`
			f := callToolRaw(t, s, signArtifactsToolName, payload)
			if !f.failed {
				t.Fatalf("the call over a missing workspace reported no failure: %q", f.text)
			}
			if row.want == "" {
				if strings.Contains(f.text, "'jobs'") {
					t.Errorf("a legal bound was refused: %q", f.text)
				}
				return
			}
			if !strings.Contains(f.text, row.want) {
				t.Errorf("the refusal reads %q; want it to carry %q", f.text, row.want)
			}
			assertActionableGuidance(t, signArtifactsToolName, row.name, f)
		})
	}
}

// TestTheSigningToolCarriesItsBound holds the arguments' projection onto the
// signing operation: an absent bound is 1 and a bound sent is carried as sent.
//
// It is not parallel: the publication fixture redirects the global configuration
// layer through the process environment.
func TestTheSigningToolCarriesItsBound(t *testing.T) {
	s, _ := publishFixture(t, defaultRegistryWorkspace)
	refs := []string{publishTestRegistry + "/component/core/api-server:1.0.0"}
	four := 4
	for _, row := range []struct {
		name string
		jobs *int
		want int
	}{
		{"absent signs one after another", nil, 1},
		{"a bound sent is carried", &four, 4},
	} {
		t.Run(row.name, func(t *testing.T) {
			cfg, refusal := s.signArtifactsConfig(signArtifactsArgs{References: refs, Jobs: row.jobs})
			if refusal != "" {
				t.Fatalf("signArtifactsConfig refused legal arguments: %s", refusal)
			}
			if cfg.Jobs != row.want {
				t.Errorf("the projection carries a bound of %d; want %d", cfg.Jobs, row.want)
			}
		})
	}
}

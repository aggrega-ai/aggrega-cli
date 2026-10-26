// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestAnExhaustedCascadeNamesOnlyTheSourcesItReads holds the exhaustion's wording
// to what the cascade does. Every source it reads holds bytes already on the
// machine, so the message names those places in the words the reference teaches,
// names no registry among them and reports no remote source — and the halt is not
// the remote-fetch condition, because no fetch was attempted. Where the plugin's
// entity sits in a catalog folder, the message names the version that entity is
// at and the file its module is read from beside it. The cascade's own labels for
// its sources are never printed.
func TestAnExhaustedCascadeNamesOnlyTheSourcesItReads(t *testing.T) {
	entityDir := filepath.Join(t.TempDir(), "plugin", "importer", "absent", "0.8.6")
	noTierLabels := []string{"registry", "remote source", "tier", "embed, colocated, local, cache"}
	withCache := func(t *testing.T, colocated map[string]ColocatedDir) Resolver {
		return Resolver{workspaceRoot: t.TempDir(), cacheDir: ".aggrega/cache", colocatedDirs: colocated}
	}
	cases := []struct {
		name     string
		uri      string
		resolver func(t *testing.T) Resolver
		want     []string
		mustNot  []string
	}{
		{
			name:     "a bare plugin coordinate, with a content cache to look in",
			uri:      "pipeline-step/absent-everywhere",
			resolver: func(t *testing.T) Resolver { return withCache(t, nil) },
			want: []string{`plugin "pipeline-step/absent-everywhere" is not on this machine: the binary does not carry ` +
				`its module and the content cache a pull fetches into does not hold it: plugin artifact not found on this machine`},
			mustNot: noTierLabels,
		},
		{
			name: "a bare plugin coordinate, with catalog folders and a content cache to look in",
			uri:  "pipeline-step/absent-everywhere",
			resolver: func(t *testing.T) Resolver {
				return withCache(t, map[string]ColocatedDir{"pipeline-step/other": {Dir: entityDir}})
			},
			want: []string{`plugin "pipeline-step/absent-everywhere" is not on this machine: the binary does not carry ` +
				`its module, no catalog folder holds its Plugin entity and the content cache a pull fetches into does ` +
				`not hold it: plugin artifact not found on this machine`},
			mustNot: noTierLabels,
		},
		{
			// A resolution given nothing but the binary — the shape a lint of a Plugin
			// entity resolves its module with — says it looked there and nowhere else.
			name:     "a bare plugin coordinate, with only the binary to look in",
			uri:      "pipeline-step/absent-everywhere",
			resolver: func(t *testing.T) Resolver { return Resolver{} },
			want: []string{`plugin "pipeline-step/absent-everywhere" is not on this machine: the binary does not carry ` +
				`its module: plugin artifact not found on this machine`},
			mustNot: append([]string{"catalog folder", "content cache"}, noTierLabels...),
		},
		{
			name: "a plugin whose entity sits in a catalog folder",
			uri:  "importer/absent",
			resolver: func(t *testing.T) Resolver {
				return withCache(t, map[string]ColocatedDir{"importer/absent": {Dir: entityDir, Version: "0.8.6"}})
			},
			want: []string{`plugin "importer/absent" at version 0.8.6 is not on this machine; place its module, named ` +
				pluginLeaf("importer/absent") + `, beside its Plugin entity in ` + entityDir +
				`: plugin artifact not found on this machine`},
			mustNot: noTierLabels,
		},
		{
			name:     "a reference shaped like a registry location",
			uri:      "oci://hub.aggrega.ai/plugin/linter/absent:1.0.0",
			resolver: func(t *testing.T) Resolver { return withCache(t, nil) },
			want:     []string{"not resolvable", "does not fetch from a registry"},
			mustNot:  []string{"remote source"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.resolver(t).Resolve(tc.uri)
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

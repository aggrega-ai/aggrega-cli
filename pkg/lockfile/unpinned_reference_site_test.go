// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestAReferenceTheLockfileDoesNotPinIsReportedWithWhereItIsWritten holds the
// wording of a drift entry for a reference the lockfile carries no row for: it is
// the lockfile-drift condition, and the entry names the reference and the
// authoring sites that reach it, in the one preview the closure walk's own refusal
// uses — sorted, at most three, then "...". A Manifest entry binding by instance
// name is such a site, so the entry tells the reader which instance's binding the
// missing row is for. A member recording no site is reported by its reference
// alone, as before.
func TestAReferenceTheLockfileDoesNotPinIsReportedWithWhereItIsWritten(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		expected closure.Closure
		want     string
	}{
		{
			name: "an entity bound by instance name",
			expected: closure.Closure{Entities: []closure.ResolvedRef{{
				URI: "compose/greeter-green:1.0.0", ResolvedVersion: "1.0.0", Digest: "sha256:g",
				ReferencedFrom: []string{"manifest.spec.mappings.components.name.greeter-special"},
			}}},
			want: `entity "compose/greeter-green:1.0.0" not in lockfile (referenced from manifest.spec.mappings.components.name.greeter-special)`,
		},
		{
			name: "an entity reached from more sites than the preview shows",
			expected: closure.Closure{Entities: []closure.ResolvedRef{{
				URI: "core/x", ResolvedVersion: "1.0.0", Digest: "sha256:x",
				ReferencedFrom: []string{"d.type", "b.type", "a.type", "c.type"},
			}}},
			want: `entity "core/x" not in lockfile (referenced from a.type, b.type, c.type, ...)`,
		},
		{
			name: "an entity recording no site",
			expected: closure.Closure{Entities: []closure.ResolvedRef{{
				URI: "core/y", ResolvedVersion: "1.0.0", Digest: "sha256:y",
			}}},
			want: `entity "core/y" not in lockfile`,
		},
		{
			name: "a plugin reports its site the same way",
			expected: closure.Closure{Plugins: []closure.ResolvedRef{{
				URI: "governance/x:1.0.0", ResolvedVersion: "1.0.0", Digest: "sha256:p",
				ReferencedFrom: []string{".aggrega-config.yaml.plugins.governance"},
			}}},
			want: `wasm-plugin "governance/x:1.0.0" not in lockfile (referenced from .aggrega-config.yaml.plugins.governance)`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := lockfile.Verify(&lockfile.Lockfile{Version: 1, Release: "release.yaml"}, tc.expected)
			if !errors.Is(err, lockfile.ErrLockfileDrift()) {
				t.Fatalf("a reference with no row must be refused as drift; got %v", err)
			}
			var entry string
			for _, line := range strings.Split(err.Error(), "\n") {
				if strings.Contains(line, " not in lockfile") {
					entry = line
				}
			}
			if !strings.HasSuffix(entry, tc.want) {
				t.Errorf("drift entry = %q; want it to read %q", entry, tc.want)
			}
		})
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryhost_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
)

// TestOf pins the host-extraction contract every consumer now shares: the host
// authority with scheme, path, and userinfo removed, but port, IPv6 brackets,
// and case preserved. The conformant rows are the host segments callers derived
// before unification (so cache-slot paths stay byte-identical); the userinfo
// rows are the fix (credentials never reach the host, and thus never the cache
// path).
func TestOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Conformant — byte-identical to the pre-unification host segment.
		{"https no port", "https://h.io/charts/x", "h.io"},
		{"https with port", "https://h.io:8443/charts/x", "h.io:8443"},
		{"oci no port", "oci://h.io/c", "h.io"},
		{"oci with port", "oci://h.io:5000/c", "h.io:5000"},
		{"ssh scheme", "ssh://h.io/x", "h.io"},
		{"ipv6 with port", "https://[::1]:5000/c", "[::1]:5000"},
		{"case preserved", "https://H.io/Charts/X", "H.io"},
		{"bare ref with path", "h.io/path/name", "h.io"},
		{"bare ref with port", "h.io:5000/x", "h.io:5000"},
		{"bare registry no path", "localhost:5000", "localhost:5000"},
		{"bare host", "ghcr.io", "ghcr.io"},
		{"empty", "", ""},
		// Userinfo — credentials stripped (the fix).
		{"userinfo no port", "https://user:pw@h.io/charts/x", "h.io"},
		{"userinfo with port", "https://user:pw@h.io:8443/p", "h.io:8443"},
		{"git ssh userinfo", "git+ssh://git@h.io/repo.git", "h.io"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := registryhost.Of(tc.in); got != tc.want {
				t.Errorf("Of(%q): got %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

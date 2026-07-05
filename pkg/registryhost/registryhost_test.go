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

// TestIsLoopback pins the shared plain-HTTP-vs-HTTPS gate: the locked loopback
// set (127.0.0.0/8, ::1, "localhost", any ".localhost" name, and 0.0.0.0) with
// and without ports and IPv6 brackets, against non-loopback hosts. The
// negatives lock the boundary that keeps the gate from opening plaintext to a
// remote host: "notlocalhost" and "localhost.evil.com" must NOT match (the
// suffix is anchored on a label, not a substring), and a routable IP or DNS
// name stays non-loopback even when it could resolve to loopback.
func TestIsLoopback(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want bool
	}{
		// localhost name, with and without port.
		{"localhost bare", "localhost", true},
		{"localhost with port", "localhost:5000", true},
		// .localhost subdomains (reserved for loopback), anchored on a label.
		{"localhost subdomain", "app.localhost", true},
		{"localhost subdomain with port", "app.localhost:8080", true},
		{"nested localhost subdomain", "a.b.localhost", true},
		// IPv4 loopback range 127.0.0.0/8.
		{"127.0.0.1", "127.0.0.1", true},
		{"127.0.0.1 with port", "127.0.0.1:5000", true},
		{"127.0.0.0/8 upper", "127.9.8.7:5000", true},
		// IPv6 loopback, bracketed and with port.
		{"ipv6 loopback bracketed", "[::1]", true},
		{"ipv6 loopback bracketed with port", "[::1]:5000", true},
		// 0.0.0.0 unspecified — only ever reachable on the local machine.
		{"unspecified v4", "0.0.0.0", true},
		{"unspecified v4 with port", "0.0.0.0:5000", true},
		// Negatives — the security boundary.
		{"notlocalhost", "notlocalhost", false},
		{"localhost.evil.com", "localhost.evil.com", false},
		{"localhost.evil.com with port", "localhost.evil.com:443", false},
		{"trailing localhost substring", "mylocalhost", false},
		{"public dns", "hub.aggrega.ai", false},
		{"public dns with port", "registry.example.com:5000", false},
		{"routable ipv4", "10.0.0.7:5000", false},
		{"routable ipv4 bare", "8.8.8.8", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := registryhost.IsLoopback(tc.in); got != tc.want {
				t.Errorf("IsLoopback(%q): got %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

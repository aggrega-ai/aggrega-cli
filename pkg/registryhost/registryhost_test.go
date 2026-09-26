// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryhost_test

import (
	"strings"
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

// TestIsHost pins the one predicate every registry-host reader consults. The
// accepting rows are each shape a registry host takes; the refusing rows are the
// spellings that loaded silently before it and reached a trust lookup, a
// verification filter or a reference parser that each read them differently —
// capitals, a scheme, a path, credentials, whitespace, a single label with no
// port — plus the malformed literals that sit next to a valid shape.
func TestIsHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"dotted name", "hub.aggrega.ai", true},
		{"dotted name with port", "registry.example.com:8443", true},
		{"explicit default port is still a host", "hub.aggrega.ai:443", true},
		{"localhost", "localhost", true},
		{"localhost with port", "localhost:5000", true},
		{"single name with port", "registry:5000", true},
		{"ipv4", "127.0.0.1", true},
		{"ipv4 with port", "127.0.0.1:5000", true},
		{"bracketed ipv6", "[::1]", true},
		{"bracketed ipv6 with port", "[::1]:5000", true},
		{"uncompressed ipv6", "[0:0:0:0:0:0:0:1]", true},
		{"label of 63", strings.Repeat("a", 63) + ".com", true},
		{"highest port", "a.b:65535", true},
		{"empty", "", false},
		{"single name", "myregistry", false},
		{"capitals", "HUB.AGGREGA.AI", false},
		{"capital localhost", "LOCALHOST", false},
		{"scheme", "https://hub.aggrega.ai", false},
		{"path", "registry.example.com/charts", false},
		{"userinfo", "user@hub.aggrega.ai", false},
		{"whitespace", "hub aggrega.ai", false},
		{"trailing newline", "hub.aggrega.ai\n", false},
		{"braces and newlines", "{\n  not a host\n}", false},
		{"trailing dot", "hub.aggrega.ai.", false},
		{"empty label", "hub..ai", false},
		{"label starting with a hyphen", "-hub.ai", false},
		{"label ending with a hyphen", "hub-.ai", false},
		{"label of 64", strings.Repeat("a", 64) + ".com", false},
		{"numeric last label", "999.1.1.1", false},
		{"octet above 255", "256.1.1.1", false},
		{"octet with a leading zero", "01.2.3.4", false},
		{"three octets", "1.2.3", false},
		{"port zero", "a.b:0", false},
		{"port above 65535", "a.b:65536", false},
		{"port with a leading zero", "a.b:0443", false},
		{"trailing colon", "a.b:", false},
		{"empty host before a port", ":5000", false},
		{"version suffix is not a port", "web-api:1.0.0", false},
		{"digit-led single name with port", "123:5000", false},
		{"unbracketed ipv6", "::1", false},
		{"unclosed bracket", "[::1", false},
		{"text after a bracket", "[::1]x", false},
		{"capital hex in ipv6", "[::FFFF]", false},
		{"embedded ipv4 in ipv6", "[::ffff:1.2.3.4]", false},
		{"zone in ipv6", "[fe80::1%eth0]", false},
		{"double compression in ipv6", "[1::2::3]", false},
		{"nine groups in ipv6", "[1:2:3:4:5:6:7:8:9]", false},
		{"underscore", "my_registry.example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := registryhost.IsHost(tc.in); got != tc.want {
				t.Errorf("IsHost(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestNormalize pins the spelling every per-registry lookup is keyed on:
// lowercased, authority only, and an explicit default port dropped — where the
// port is the default, which depends on how the host is reached. A loopback host
// is reached over plain HTTP, so 443 is an ordinary port there and dropping it
// would name a different address.
func TestNormalize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"canonical is unchanged", "hub.aggrega.ai", "hub.aggrega.ai"},
		{"capitals are lowered", "HUB.AGGREGA.AI", "hub.aggrega.ai"},
		{"https default port is dropped", "hub.aggrega.ai:443", "hub.aggrega.ai"},
		{"capitals and default port together", "Registry.Example.com:443", "registry.example.com"},
		{"another port is kept", "registry.example.com:8443", "registry.example.com:8443"},
		{"loopback keeps 443", "localhost:443", "localhost:443"},
		{"loopback ipv4 keeps 443", "127.0.0.1:443", "127.0.0.1:443"},
		{"loopback ipv6 keeps 443", "[::1]:443", "[::1]:443"},
		{"routable ipv4 drops 443", "10.0.0.7:443", "10.0.0.7"},
		{"scheme and path are dropped", "https://Hub.Aggrega.AI:443/component/web-api", "hub.aggrega.ai"},
		{"credentials are dropped", "https://user:pw@hub.aggrega.ai/x", "hub.aggrega.ai"},
		{"bound location reads its host", "localhost/component/web-api", "localhost"},
		{"host-less identity reads its first segment", "core/web-api", "core"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := registryhost.Normalize(tc.in); got != tc.want {
				t.Errorf("Normalize(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSame pins that two spellings of one host compare equal and two hosts do
// not — the comparison the trust lookups make.
func TestSame(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want bool
	}{
		{"hub.aggrega.ai", "HUB.AGGREGA.AI", true},
		{"hub.aggrega.ai", "hub.aggrega.ai:443", true},
		{"hub.aggrega.ai", "hub.aggrega.ai/component/web-api", true},
		{"localhost", "localhost:443", false},
		{"registry.example.com", "registry.example.com:8443", false},
		{"registry.example.com", "other.example.com", false},
	}
	for _, tc := range cases {
		if got := registryhost.Same(tc.a, tc.b); got != tc.want {
			t.Errorf("Same(%q, %q) = %v; want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestCheck pins the refusal every configuration layer words through Check: a
// canonical host passes; a value naming a host in another spelling is refused
// with the spelling to write; a value naming no host is refused as one; and a
// value carrying credentials is never echoed back.
func TestCheck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		in         string
		wantErr    bool
		wantSubstr []string
		notSubstr  []string
	}{
		{name: "canonical host", in: "hub.aggrega.ai"},
		{name: "single name with port", in: "registry:5000"},
		{name: "loopback 443 is kept", in: "localhost:443"},
		{name: "capitals", in: "HUB.AGGREGA.AI", wantErr: true,
			wantSubstr: []string{`default_registry "HUB.AGGREGA.AI"`, `write "hub.aggrega.ai"`}},
		{name: "default port", in: "hub.aggrega.ai:443", wantErr: true,
			wantSubstr: []string{`write "hub.aggrega.ai"`}},
		{name: "scheme", in: "https://registry.example.com", wantErr: true,
			wantSubstr: []string{`write "registry.example.com"`}},
		{name: "path", in: "registry.example.com/charts", wantErr: true,
			wantSubstr: []string{`write "registry.example.com"`}},
		{name: "single name", in: "myregistry", wantErr: true,
			wantSubstr: []string{`"myregistry" is not a registry host`}},
		{name: "empty", in: "", wantErr: true, wantSubstr: []string{"is not a registry host"}},
		{name: "credentials are not repeated", in: "user:secret@hub.aggrega.ai", wantErr: true,
			wantSubstr: []string{`write "hub.aggrega.ai"`}, notSubstr: []string{"secret"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := registryhost.Check("default_registry", tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Check(%q) error = %v; want error %v", tc.in, err, tc.wantErr)
			}
			if err == nil {
				return
			}
			for _, s := range tc.wantSubstr {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("Check(%q) = %q; want it to contain %q", tc.in, err, s)
				}
			}
			for _, s := range tc.notSubstr {
				if strings.Contains(err.Error(), s) {
					t.Errorf("Check(%q) = %q; it repeats %q", tc.in, err, s)
				}
			}
		})
	}
}

// TestCheckOr pins the refusal for a value that may name something besides a
// host: it is Check's refusal wherever the value names a host at all, and only a
// value naming no host is refused as neither, offering the alternative first.
func TestCheckOr(t *testing.T) {
	t.Parallel()
	const alternative = "the name of an entry under registries"
	cases := []struct {
		name       string
		in         string
		wantErr    bool
		wantSubstr []string
		notSubstr  []string
	}{
		{name: "canonical host", in: "registry.example.com"},
		{name: "a host in another spelling is Check's refusal", in: "HUB.AGGREGA.AI", wantErr: true,
			wantSubstr: []string{`write "hub.aggrega.ai"`}, notSubstr: []string{alternative}},
		{name: "a repository path is Check's refusal", in: "ghcr.io/acme/catalog", wantErr: true,
			wantSubstr: []string{`default_registry "ghcr.io/acme/catalog"`, `write "ghcr.io"`}, notSubstr: []string{alternative}},
		{name: "a single name is refused as neither", in: "nowhere", wantErr: true,
			wantSubstr: []string{`default_registry "nowhere" is not a registry host, and it is not ` + alternative,
				"write " + alternative + ", or a lowercase host name containing a dot"}},
		{name: "credentials are not repeated", in: "user:secret@nowhere", wantErr: true,
			wantSubstr: []string{alternative}, notSubstr: []string{"secret"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := registryhost.CheckOr("default_registry", tc.in, alternative)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CheckOr(%q) error = %v; want error %v", tc.in, err, tc.wantErr)
			}
			if err == nil {
				return
			}
			for _, s := range tc.wantSubstr {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("CheckOr(%q) = %q; want it to contain %q", tc.in, err, s)
				}
			}
			for _, s := range tc.notSubstr {
				if strings.Contains(err.Error(), s) {
					t.Errorf("CheckOr(%q) = %q; it carries %q", tc.in, err, s)
				}
			}
			if check := registryhost.Check("default_registry", tc.in); registryhost.IsHost(registryhost.Normalize(tc.in)) && check.Error() != err.Error() {
				t.Errorf("CheckOr(%q) = %q; a value naming a host must be refused exactly as Check refuses it, %q", tc.in, err, check)
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

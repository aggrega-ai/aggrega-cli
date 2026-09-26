// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryhost_test

import (
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry"

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

// TestCheckLocation pins the reading of a registry location — a registry host,
// optionally followed by a repository path — and the refusal for a value that is
// not one. A host alone is accepted and refused exactly as Check accepts and
// refuses it; a host with a path is accepted where the host is canonical and the
// path is a repository path, and otherwise refused naming the location to write
// where there is one; and a value naming no host is refused as neither a location
// nor the alternative, offering both. Credentials are never repeated.
func TestCheckLocation(t *testing.T) {
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
		{name: "a host with an owner path", in: "ghcr.io/acme"},
		{name: "a host with a project path", in: "harbor.example.com/platform"},
		{name: "a loopback host with a port and a path", in: "127.0.0.1:5000/team"},
		{name: "a nested path", in: "ghcr.io/acme/catalog/stable"},
		{name: "localhost with a path", in: "localhost/acme"},
		{name: "an ipv6 host with a path", in: "[::1]:5000/acme"},
		{name: "path separators the grammar admits", in: "registry.example.com/my-team_1/sub.name__x"},
		{name: "a host in another spelling is Check's refusal", in: "HUB.AGGREGA.AI", wantErr: true,
			wantSubstr: []string{`write "hub.aggrega.ai"`}, notSubstr: []string{alternative}},
		{name: "a scheme and no path is Check's refusal", in: "https://registry.example.com", wantErr: true,
			wantSubstr: []string{`write "registry.example.com"`}, notSubstr: []string{alternative}},
		{name: "a host in capitals before a path names the location to write", in: "GHCR.io/acme", wantErr: true,
			wantSubstr: []string{`default_registry "GHCR.io/acme" is not written as the registry location it names`, `write "ghcr.io/acme"`}},
		{name: "a scheme before a path names the location to write", in: "https://ghcr.io/acme", wantErr: true,
			wantSubstr: []string{`write "ghcr.io/acme"`}},
		{name: "a default port before a path names the location to write", in: "ghcr.io:443/acme", wantErr: true,
			wantSubstr: []string{`write "ghcr.io/acme"`}},
		{name: "credentials before a path are not repeated", in: "user:secret@ghcr.io/acme", wantErr: true,
			wantSubstr: []string{`write "ghcr.io/acme"`}, notSubstr: []string{"secret"}},
		{name: "a path in capitals is not a repository path", in: "ghcr.io/Acme", wantErr: true,
			wantSubstr: []string{`names the registry host "ghcr.io" followed by something that is not a repository path`}},
		{name: "a tag is not part of a repository path", in: "ghcr.io/acme:1.0.0", wantErr: true,
			wantSubstr: []string{`not a repository path`, "no tag, digest"}},
		{name: "a digest is not part of a repository path", in: "ghcr.io/acme@sha256:0123", wantErr: true,
			wantSubstr: []string{`not a repository path`}},
		{name: "a trailing slash is not part of a repository path", in: "ghcr.io/acme/", wantErr: true,
			wantSubstr: []string{`not a repository path`}},
		{name: "an empty segment is not part of a repository path", in: "ghcr.io//acme", wantErr: true,
			wantSubstr: []string{`not a repository path`}},
		{name: "a slash and nothing after it", in: "ghcr.io/", wantErr: true,
			wantSubstr: []string{`not a repository path`, "write the host alone"}},
		{name: "a single name with a path is refused as neither", in: "myregistry/acme", wantErr: true,
			wantSubstr: []string{`default_registry "myregistry/acme" is not a registry host, alone or followed by a repository path, and it is not ` + alternative,
				"write " + alternative + `, or a registry host optionally followed by "/" and a repository path, where a registry host is a lowercase host name containing a dot`,
				"and a repository path is one or more segments"}},
		{name: "a catalog identity is refused as neither", in: "core/web-api", wantErr: true,
			wantSubstr: []string{"is not a registry host, alone or followed by a repository path"}},
		{name: "a single name is refused as neither", in: "nowhere", wantErr: true,
			wantSubstr: []string{`default_registry "nowhere" is not a registry host, alone or followed by a repository path, and it is not ` + alternative}},
		{name: "credentials with no host are not repeated", in: "user:secret@nowhere", wantErr: true,
			wantSubstr: []string{alternative}, notSubstr: []string{"secret"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := registryhost.CheckLocation("default_registry", tc.in, alternative)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CheckLocation(%q) error = %v; want error %v", tc.in, err, tc.wantErr)
			}
			if err == nil {
				return
			}
			for _, s := range tc.wantSubstr {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("CheckLocation(%q) = %q; want it to contain %q", tc.in, err, s)
				}
			}
			for _, s := range tc.notSubstr {
				if strings.Contains(err.Error(), s) {
					t.Errorf("CheckLocation(%q) = %q; it carries %q", tc.in, err, s)
				}
			}
			if !strings.Contains(tc.in, "/") || strings.HasPrefix(tc.in, "https://") && strings.Count(tc.in, "/") == 2 {
				if check := registryhost.Check("default_registry", tc.in); registryhost.IsHost(registryhost.Normalize(tc.in)) && check.Error() != err.Error() {
					t.Errorf("CheckLocation(%q) = %q; a host alone must be refused exactly as Check refuses it, %q", tc.in, err, check)
				}
			}
		})
	}

	// With no alternative, a value naming no host offers the location shapes alone.
	err := registryhost.CheckLocation("the destination", "myregistry/acme", "")
	if err == nil || !strings.Contains(err.Error(), `the destination "myregistry/acme" is not a registry host, alone or followed by a repository path; write a registry host`) {
		t.Errorf("CheckLocation with no alternative = %v; want the location shapes offered alone", err)
	}
}

// TestIsRepositoryPath pins the OCI distribution grammar a registry location's
// path is held to.
func TestIsRepositoryPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"acme", true},
		{"acme/catalog", true},
		{"a/b/c/d", true},
		{"my-team", true},
		{"my--team", true},
		{"my_team", true},
		{"my__team", true},
		{"my.team", true},
		{"a0.b1-c2_d3__e4", true},
		{"0", true},
		{"", false},
		{"Acme", false},
		{"acme/", false},
		{"/acme", false},
		{"acme//catalog", false},
		{"acme:1.0.0", false},
		{"acme@sha256:0123", false},
		{"-acme", false},
		{"acme-", false},
		{"acme.", false},
		{".acme", false},
		{"my___team", false},
		{"my..team", false},
		{"my._team", false},
		{"acme team", false},
		{"https://acme", false},
	}
	for _, tc := range cases {
		if got := registryhost.IsRepositoryPath(tc.in); got != tc.want {
			t.Errorf("IsRepositoryPath(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

// TestIsRepositoryPathAgreesWithTheRegistryClient holds the reading of a
// repository path to the registry client's own statement of the same grammar,
// which the client applies to every reference it parses. The two are written
// independently, so this drives one corpus through both: every string of up to
// five characters over an alphabet carrying each character class the grammar
// treats differently — a letter, a digit, each separator, the component
// separator, and the capital, colon and at-sign it refuses — plus the longer
// shapes the table above names. A path this package accepted and the client
// refused would load and then fail at the first fetch; one the client accepts
// and this package refused would be a location nothing lets an author write.
func TestIsRepositoryPathAgreesWithTheRegistryClient(t *testing.T) {
	t.Parallel()
	alphabet := []string{"a", "0", ".", "_", "-", "/", "A", ":", "@"}
	corpus := []string{"acme/catalog/stable", "a0.b1-c2_d3__e4/x", "my---team/sub", "harbor/platform/team-a_b.c"}
	var grow func(prefix string, depth int)
	grow = func(prefix string, depth int) {
		corpus = append(corpus, prefix)
		if depth == 0 {
			return
		}
		for _, c := range alphabet {
			grow(prefix+c, depth-1)
		}
	}
	grow("", 5)
	disagreements := 0
	for _, p := range corpus {
		client := registry.Reference{Registry: "registry.example.com", Repository: p}.ValidateRepository() == nil
		if ours := registryhost.IsRepositoryPath(p); ours != client {
			disagreements++
			if disagreements <= 20 {
				t.Errorf("repository path %q: IsRepositoryPath=%v but the registry client accepts=%v", p, ours, client)
			}
		}
	}
	if disagreements > 20 {
		t.Errorf("%d disagreements in all over %d paths", disagreements, len(corpus))
	}
}

// TestLocationOf pins how an accepted registry location is read afterwards: the
// host is everything before the first "/", the path everything after, Prefix
// puts them back together, and Repository names a repository beneath the path
// relative to the host.
func TestLocationOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in           string
		host, path   string
		prefix, repo string
	}{
		{"", "", "", "", "component/core/x"},
		{"hub.aggrega.ai", "hub.aggrega.ai", "", "hub.aggrega.ai", "component/core/x"},
		{"ghcr.io/acme", "ghcr.io", "acme", "ghcr.io/acme", "acme/component/core/x"},
		{"127.0.0.1:5000/team/sub", "127.0.0.1:5000", "team/sub", "127.0.0.1:5000/team/sub", "team/sub/component/core/x"},
		{"[::1]:5000/acme", "[::1]:5000", "acme", "[::1]:5000/acme", "acme/component/core/x"},
	}
	for _, tc := range cases {
		got := registryhost.LocationOf(tc.in)
		if got.Host != tc.host || got.Path != tc.path {
			t.Errorf("LocationOf(%q) = %+v; want host %q path %q", tc.in, got, tc.host, tc.path)
		}
		if p := got.Prefix(); p != tc.prefix {
			t.Errorf("LocationOf(%q).Prefix() = %q; want %q", tc.in, p, tc.prefix)
		}
		if r := got.Repository("component/core/x"); r != tc.repo {
			t.Errorf("LocationOf(%q).Repository = %q; want %q", tc.in, r, tc.repo)
		}
		if tc.in != "" && got.Prefix()+"/component/core/x" != got.Host+"/"+got.Repository("component/core/x") {
			t.Errorf("LocationOf(%q): Prefix and Repository name two different references", tc.in)
		}
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

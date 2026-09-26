// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package registryhost is the single source for reading a registry host: for
// extracting the host authority from a registry reference or a source-artifact
// URL, for deciding whether a string is a registry host at all, and for the one
// spelling every trust lookup is keyed on. Centralising it keeps the cache-slot
// path, the loopback/plain-HTTP decision inputs, the remediation strings, the
// signature-verification filter and the per-registry trust lookups agreeing on
// one host per artifact — in particular, never letting URL userinfo
// (credentials) leak into a host segment, and never letting a host written in
// capitals or with its default port miss the trust policy kept under it. It is
// a stdlib-only leaf so every
// consumer can share it without importing a heavier package's dependency tree.
//
// Each reading answers its own question:
//
//   - Of extracts the authority a reference names, preserving its spelling.
//   - IsHost decides whether a string is a registry host, spelled the way a
//     trust lookup is keyed.
//   - Normalize produces that keyed spelling from a reference or an authority,
//     and Same compares two of them through it.
//   - Check words the refusal for a configured value the two do not accept, and
//     CheckOr words it for a value that may name something besides a host.
//   - IsLoopback decides whether an authority addresses this machine, which is
//     what selects plain HTTP over HTTPS.
//
// A value an author configures as a registry host is accepted only when IsHost
// holds for it and Normalize returns it unchanged, so a configured host and the
// key its trust policy is looked up under are the same string.
package registryhost

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// Of returns the host authority of a registry reference or URL: the scheme is
// stripped, the path (everything from the first "/") is dropped, and any
// userinfo ("user:pw@") is removed. The port and IPv6 brackets are preserved
// and the case is left untouched, so the result is byte-identical to the
// host segment callers derived before unification — except that credentials are
// no longer carried through. A bare reference with no scheme or path (a registry
// host, "ghcr.io", or "localhost:5000") is returned unchanged.
//
// Examples:
//
//	"https://h.io:8443/charts/x"   → "h.io:8443"
//	"oci://h.io/c"                 → "h.io"
//	"https://user:pw@h.io/charts"  → "h.io"        (credentials stripped)
//	"git+ssh://git@h.io/repo.git"  → "h.io"
//	"https://[::1]:5000/c"         → "[::1]:5000"
//	"ghcr.io"                      → "ghcr.io"
func Of(ref string) string {
	if i := strings.Index(ref, "://"); i >= 0 {
		ref = ref[i+3:]
	}
	if i := strings.Index(ref, "/"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		ref = ref[i+1:]
	}
	return ref
}

// IsLoopback reports whether a host authority addresses the local machine, and
// is the single source for the plain-HTTP-vs-HTTPS decision every registry and
// artifact client shares. The input is a host authority (as produced by Of) —
// a bare host, optionally carrying a port and, for an IPv6 literal, brackets;
// the port and brackets are stripped internally before classification.
//
// A host is loopback when it is any of:
//
//   - the "localhost" name, or any name under the ".localhost" suffix
//     (reserved for loopback, so it never legitimately names a remote host);
//   - a loopback IP literal — 127.0.0.0/8 or ::1;
//   - "0.0.0.0", the unspecified address, which a client can only ever reach on
//     the local machine.
//
// The ".localhost" suffix match is anchored on a full label boundary, so
// "localhost.evil.com" is NOT loopback. Names are matched literally and never
// resolved: a DNS name other than the above that happens to point at a loopback
// IP stays non-loopback, keeping the plaintext decision independent of resolver
// state.
func IsLoopback(authority string) bool {
	host := authority
	if h, _, err := net.SplitHostPort(authority); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if host == "0.0.0.0" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// IsHost reports whether s is a registry host, spelled the way a trust lookup
// is keyed: a lowercase DNS name containing at least one dot, the name
// "localhost", an IPv4 literal, or a bracketed IPv6 literal — each optionally
// followed by ":" and a port from 1 to 65535 written without a leading zero —
// or a single-label lowercase name followed by such a port. Nothing else is a
// registry host: no scheme, path, userinfo, whitespace or uppercase letter, no
// single-label name other than localhost unless a port follows it, and no IPv6
// literal outside brackets.
//
// The single-label rule is what tells a registry host from the first segment of
// a host-less catalog identity: "core/web-api" names a Component in no
// particular registry, and its first segment, "core", is not a host. A dot, the
// name localhost, or a port is what a first segment of that kind never carries.
//
// A DNS name's labels are 1 to 63 characters of lowercase letters, digits and
// hyphens, neither starting nor ending with a hyphen, and its last label begins
// with a letter, which keeps a malformed IPv4 literal such as "256.1.1.1" from
// reading as a name. An IPv4 literal is four decimal octets from 0 to 255 with
// no leading zeros. An IPv6 literal is the colon-separated hexadecimal form in
// lowercase, with no embedded dotted IPv4 part and no zone.
//
// It judges the spelling of a host and nothing else: it never resolves the name
// and cannot say whether a registry answers there. The configuration schema
// states the same shape as a pattern on each registries[].host, and a test in the
// configuration package drives one corpus of spellings through both and fails
// where they disagree. default_registry carries no such pattern, because it may
// also name a registries entry, whose name may be any string.
func IsHost(s string) bool {
	host, port, ok := splitAuthority(s)
	if !ok {
		return false
	}
	if port != "" && !isPort(port) {
		return false
	}
	if isIPv4Literal(host) || isIPv6Literal(host) {
		return true
	}
	return isHostName(host, port != "")
}

// Normalize returns the spelling a registry host is looked up under: the
// authority ref names, as Of extracts it, lowercased, with an explicitly
// written default port removed. The default port is the one the client connects
// to when none is written, and that depends on how the host is reached: 443 for
// a host reached over HTTPS, which is every host but a loopback one. A loopback
// host is contacted over plain HTTP (see IsLoopback), so 443 is not its default
// and is kept — "localhost:443" and "localhost" are different addresses.
//
// Every per-registry lookup keys on this spelling, and a value an author
// configures as a registry host is accepted only when Normalize returns it
// unchanged, so the configured host and the lookup key are one string.
//
// It reads spelling only and never resolves a name. What it returns is a lookup
// key; whether that key is a registry host is IsHost's question, and a reference
// that names no host — the first segment of a host-less catalog identity, say —
// yields a key IsHost refuses.
func Normalize(ref string) string {
	authority := strings.ToLower(Of(ref))
	host, port, ok := splitAuthority(authority)
	if ok && port == "443" && !IsLoopback(host) {
		return host
	}
	return authority
}

// Check reports why value cannot be configured as a registry host, or nil when
// it can — when IsHost holds for it and Normalize leaves it unchanged. what names
// the value the way its author supplied it: a configuration key, an environment
// variable, a command's argument. The error names what, repeats the value unless
// it carries credentials, and gives the spelling to write where the value names
// a host at all.
//
// It is the one wording of the refusal, so a value is refused in the same words
// whichever layer carried it. The error wraps nothing: a caller adds whatever
// condition its own surface raises.
func Check(what, value string) error {
	return check(what, value, "")
}

// CheckOr is Check for a value that may name something besides a registry host.
// alternative says, in the words a refusal uses, what else the value may name —
// "the name of an entry under registries", say — and the caller has already
// established that the value names no such thing. A value that names a host in
// another spelling is refused exactly as Check refuses it, naming the spelling
// to write; a value that names no host at all is refused as neither, offering
// the alternative ahead of the shapes a host takes.
func CheckOr(what, value, alternative string) error {
	return check(what, value, alternative)
}

// hostForms lists, in the words a refusal uses, the shapes IsHost accepts. It is
// the one wording of that list, so every refusal names the same forms.
const hostForms = "a lowercase host name containing a dot (registry.example.com), localhost, a single " +
	"name followed by a port (registry:5000), or an IP address, an IPv6 one in brackets — each with an " +
	"optional :port and nothing else"

// check is Check and CheckOr: alternative is empty for a value that can only be
// a registry host.
func check(what, value, alternative string) error {
	normalised := Normalize(value)
	if IsHost(value) && normalised == value {
		return nil
	}
	shown := strconv.Quote(value)
	if strings.Contains(value, "@") {
		shown = "(a value carrying credentials, not repeated here)"
	}
	if IsHost(normalised) {
		return fmt.Errorf("%s %s is not written as the registry host it names; write %q — "+
			"lowercase, with no scheme, path, credentials or default port — which is the spelling "+
			"that host's trust policy is kept under", what, shown, normalised)
	}
	if alternative != "" {
		return fmt.Errorf("%s %s is not a registry host, and it is not %s; write %s, or %s",
			what, shown, alternative, alternative, hostForms)
	}
	return fmt.Errorf("%s %s is not a registry host; write %s", what, shown, hostForms)
}

// Same reports whether a and b name the same registry host under the one
// reading: their Normalize spellings are equal. Every lookup that matches a
// configured host against the host an operation is addressing compares through
// it, so a host written in capitals, or with its default port spelled out, finds
// the policy configured for it rather than missing it.
func Same(a, b string) bool {
	return Normalize(a) == Normalize(b)
}

// splitAuthority separates an authority into its host and its port, reporting
// ok=false for a shape no registry host takes: an unclosed bracket, a trailing
// colon with no port, anything after a closing bracket but a port, or more than
// one colon outside brackets. A missing port is reported as the empty string.
func splitAuthority(s string) (host, port string, ok bool) {
	if strings.HasPrefix(s, "[") {
		end := strings.IndexByte(s, ']')
		if end < 0 {
			return "", "", false
		}
		host, rest := s[:end+1], s[end+1:]
		if rest == "" {
			return host, "", true
		}
		if rest[0] != ':' || len(rest) == 1 {
			return "", "", false
		}
		return host, rest[1:], true
	}
	switch strings.Count(s, ":") {
	case 0:
		return s, "", true
	case 1:
		i := strings.IndexByte(s, ':')
		if i == len(s)-1 {
			return "", "", false
		}
		return s[:i], s[i+1:], true
	default:
		return "", "", false
	}
}

// isPort reports whether p is a port from 1 to 65535 written in decimal with no
// leading zero, which is also what refuses "0".
func isPort(p string) bool {
	if p == "" || len(p) > 5 || p[0] == '0' {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] < '0' || p[i] > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(p)
	return err == nil && n <= 65535
}

// isHostName reports whether h is "localhost" or a lowercase DNS name whose last
// label begins with a letter, of at least two labels — or of one, where withPort
// says a port follows it.
func isHostName(h string, withPort bool) bool {
	if h == "localhost" {
		return true
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 && !withPort {
		return false
	}
	for _, l := range labels {
		if !isLabel(l) {
			return false
		}
	}
	last := labels[len(labels)-1]
	return last[0] >= 'a' && last[0] <= 'z'
}

// isLabel reports whether l is one DNS label: 1 to 63 lowercase letters, digits
// and hyphens, neither starting nor ending with a hyphen.
func isLabel(l string) bool {
	if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}
	for i := 0; i < len(l); i++ {
		c := l[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// isIPv4Literal reports whether h is a dotted-decimal IPv4 address. The
// standard parser refuses a leading zero in an octet, so each address has one
// accepted spelling.
func isIPv4Literal(h string) bool {
	a, err := netip.ParseAddr(h)
	return err == nil && a.Is4()
}

// isIPv6Literal reports whether h is a bracketed IPv6 address written in
// lowercase hexadecimal and colons only — which is what leaves out an embedded
// dotted IPv4 part and a zone.
func isIPv6Literal(h string) bool {
	if len(h) < 3 || h[0] != '[' || h[len(h)-1] != ']' {
		return false
	}
	inner := h[1 : len(h)-1]
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && c != ':' {
			return false
		}
	}
	a, err := netip.ParseAddr(inner)
	return err == nil && a.Is6()
}

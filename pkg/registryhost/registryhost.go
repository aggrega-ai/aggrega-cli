// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package registryhost is the single source for extracting the host authority
// from a registry reference or a source-artifact URL. Centralising it keeps the
// cache-slot path, the loopback/plain-HTTP decision inputs, and the remediation
// strings agreeing on one host per artifact — in particular, never letting URL
// userinfo (credentials) leak into a host segment. It is a stdlib-only leaf so
// every consumer can share it without importing a heavier package's dependency
// tree.
package registryhost

import (
	"net"
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

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

import "strings"

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

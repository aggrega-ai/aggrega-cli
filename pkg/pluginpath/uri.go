// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"path"
	"strings"
)

// isLocalPathRef reports whether uri is an explicit filesystem-path reference —
// a POSIX relative prefix (./ or ../) or an absolute path. This is the Tier-1
// discriminator: an explicit local path is loaded from disk verbatim, distinct
// from a bare catalog/registry URI that resolves through the cache/registry
// tiers. It mirrors the resolver's local-path-intent convention: the ./ or ../
// prefix disambiguates local-file intent from a type-name lookup, so no
// string-content sniffing of the body is required.
func isLocalPathRef(uri string) bool {
	return strings.HasPrefix(uri, "./") ||
		strings.HasPrefix(uri, "../") ||
		strings.HasPrefix(uri, "/")
}

// SidecarLeaf maps a plugin URI to its flat on-disk sidecar filename:
// <uri-leaf>.wasm. The leaf is the URI's final path segment with any version
// pin (":tag") and ".wasm" suffix stripped, so both a bare catalog URI
// (core/helm-renderer:0.4.1) and a local path (./plugins/foo.wasm) map to a
// stable "<name>.wasm" the operator drops into the sidecar directory. It is the
// single source of the sidecar leaf mapping: the resolver computes a sidecar
// path with it, and a caller staging an artifact into a sidecar directory keys
// the file by it.
func SidecarLeaf(uri string) string {
	return sidecarLeaf(uri)
}

func sidecarLeaf(uri string) string {
	leaf := path.Base(strings.TrimPrefix(uri, "oci://"))
	// Strip a version pin: a trailing ":tag" on the leaf (a digest pin
	// "@sha256:..." already lost its tail to path.Base's segment split is not
	// the case here — guard the simple ":tag" form the catalog URIs use).
	if i := strings.LastIndex(leaf, ":"); i >= 0 {
		leaf = leaf[:i]
	}
	leaf = strings.TrimSuffix(leaf, ".wasm")
	return leaf + ".wasm"
}

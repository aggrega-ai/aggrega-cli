// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package ociref owns the single OCI-safe rule for splitting a version tag off a
// plugin reference. It is a stdlib-only leaf so both the catalog resolver and the
// workspace-config writer can share the one rule without an import cycle.
package ociref

import "strings"

// SplitVersionTag splits a plugin reference such as
// "oci://host/path/plugin:1.0.0" into its version-free base and its version
// segment. It is OCI-safe: the version separator is a colon AFTER the last path
// slash, so neither the "oci://" scheme colon nor a "host:port" colon is mistaken
// for a version. When no post-slash colon is present it returns the whole ref as
// base, "" as version, and false as hasVersion.
func SplitVersionTag(ref string) (base, version string, hasVersion bool) {
	off := 0
	if slash := strings.LastIndex(ref, "/"); slash >= 0 {
		off = slash + 1
	}
	tail := ref[off:]
	idx := strings.LastIndex(tail, ":")
	if idx < 0 {
		return ref, "", false
	}
	return ref[:off+idx], ref[off+idx+1:], true
}

// VersionlessBase returns just the version-free base of a plugin reference,
// discarding any trailing version tag. It is the OCI-safe base half of
// SplitVersionTag: a "host:port" colon or the "oci://" scheme colon is never
// stripped, only a colon after the last path slash. A reference with no such
// colon is returned unchanged.
func VersionlessBase(ref string) string {
	base, _, _ := SplitVersionTag(ref)
	return base
}

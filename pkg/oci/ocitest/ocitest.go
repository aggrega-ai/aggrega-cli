// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package ocitest provides fixtures for tests that lay out or read the artifact
// cache by hand. It is a test helper, not a way to address the cache: every slot it
// names comes from oci.CachePath, so a fixture lays a slot where every reader of the
// cache looks.
//
// It depends on the oci package only, so a test anywhere above it can use it, the
// tests of the packages that read and write the cache included.
package ocitest

import "github.com/aggrega-ai/aggrega-cli/pkg/oci"

// MustCachePath returns the cache slot oci.CachePath builds for an artifact of kind,
// uri and version. A fixture's values are well formed by construction, so a refusal
// is a fault in the fixture and panics.
func MustCachePath(workspaceRoot, cacheDir, kind, uri, version string) string {
	slot, err := oci.CachePath(workspaceRoot, cacheDir, kind, uri, version)
	if err != nil {
		panic("fixture cache slot: " + err.Error())
	}
	return slot
}

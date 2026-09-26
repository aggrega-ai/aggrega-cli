// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import "github.com/aggrega-ai/aggrega-cli/pkg/oci"

// mustCachePath is oci.CachePath for a fixture whose reference and version are
// well formed by construction, for the tests that exercise this package from
// outside it. A refusal means the fixture itself is wrong, so it stops the test
// at the line that built it rather than handing back an empty path a later
// assertion would misread.
func mustCachePath(workspaceRoot, cacheDir, uri, version string) string {
	slot, err := oci.CachePath(workspaceRoot, cacheDir, uri, version)
	if err != nil {
		panic("fixture cache slot: " + err.Error())
	}
	return slot
}

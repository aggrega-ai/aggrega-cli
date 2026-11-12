// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginstrust

import "path/filepath"

// FileName is the trust record's fixed file name.
//
// Fixed rather than derived from the Release stem, which is the one structural
// difference from the lockfile beside it: what the record covers comes from
// `.aggrega-config.yaml`'s plugin attachments, which are resolved per directory
// rather than per Release, so a per-Release name would create several records of
// one answer.
const FileName = "plugins-trust.yaml"

// PathFor returns the trust-record path for a Release file path: FileName in the
// Release's own directory, which is where that Release's lockfile sits too.
//
// Shared by every caller so both ends agree on where the record sits relative to
// its Release, the same reason lockfile.PathFor exists.
func PathFor(releasePath string) string {
	return filepath.Join(filepath.Dir(releasePath), FileName)
}

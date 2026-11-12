// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package livetrust

// CachedTrustedRootPathFor is where the client keeps the trust root it fetched
// from the repository at a mirror of the test's choosing, for the case that runs
// the real client against a repository it serves itself.
var CachedTrustedRootPathFor = cachedTrustedRootPath

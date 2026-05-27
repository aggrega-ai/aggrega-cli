// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"path/filepath"
	"strings"
)

// daemonLockfilePath returns the conventional lockfile path for a
// given Release file path. Mirrors cmd/aggrega/pull.go's
// lockfilePathFor: strip a trailing ".yaml"/".yml" and append
// ".lock"; fall back to appending ".lock" when the stem is non-yaml.
//
// Kept package-private here rather than imported from cmd/aggrega/.
// A future polish may promote both copies to pkg/lockfile/ as a
// single exported helper.
func daemonLockfilePath(releasePath string) string {
	ext := filepath.Ext(releasePath)
	if ext == ".yaml" || ext == ".yml" {
		return strings.TrimSuffix(releasePath, ext) + ".lock"
	}
	return releasePath + ".lock"
}

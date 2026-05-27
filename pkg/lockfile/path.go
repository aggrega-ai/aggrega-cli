// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import (
	"path/filepath"
	"strings"
)

// PathFor returns the conventional lockfile path for a given Release
// file path. Strips a trailing ".yaml"/".yml" and appends ".lock";
// falls back to appending ".lock" when the stem is non-yaml.
//
// Shared by the CLI's `aggrega pull` / `aggrega build` and the daemon
// so both ends agree on where the lockfile sits relative to its
// Release.
func PathFor(releasePath string) string {
	ext := filepath.Ext(releasePath)
	if ext == ".yaml" || ext == ".yml" {
		return strings.TrimSuffix(releasePath, ext) + ".lock"
	}
	return releasePath + ".lock"
}

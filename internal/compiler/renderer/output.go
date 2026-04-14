// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// WriteOutput writes rendered files to disk under the given base directory.
// Files are written in sorted key order for deterministic filesystem operations.
func WriteOutput(baseDir string, files map[string][]byte) error {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, relPath := range keys {
		absPath := filepath.Join(baseDir, relPath)
		dir := filepath.Dir(absPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
		if err := os.WriteFile(absPath, files[relPath], 0o644); err != nil {
			return fmt.Errorf("write %q: %w", absPath, err)
		}
	}
	return nil
}

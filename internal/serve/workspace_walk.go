// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	gitignore "github.com/monochromegane/go-gitignore"
)

// walkWorkspaceYAML invokes visit once per YAML file under the
// workspace root, honoring the standard exclusion list and (when
// present at the workspace root) the .gitignore patterns.
//
// The standard exclusion list skips:
//   - any path component whose name begins with '.' (covers .git,
//     .aggrega, .trace, hidden tooling directories)
//   - node_modules, vendor, dist
//
// The .gitignore is loaded from <workspaceRoot>/.gitignore on entry.
// Nested .gitignore files at lower directories are not parsed.
//
// Errors returned by visit halt the walk and propagate.
// filepath.WalkDir does not follow symlinks by default, matching the
// IDE's expectation that the workspace tree is a finite acyclic
// search domain.
func (s *Server) walkWorkspaceYAML(visit func(path string) error) error {
	matcher := loadWorkspaceGitignore(s.workspaceRoot)
	return filepath.WalkDir(s.workspaceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == s.workspaceRoot {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if excludedDirName(name) {
				return filepath.SkipDir
			}
			if matcher != nil && matcher.Match(path, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isYAMLFile(name) {
			return nil
		}
		if matcher != nil && matcher.Match(path, false) {
			return nil
		}
		return visit(path)
	})
}

// excludedDirName matches directory names the workspace walker
// unconditionally skips.
func excludedDirName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "dist":
		return true
	}
	return false
}

// isYAMLFile reports whether name has a .yaml or .yml extension.
func isYAMLFile(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

// loadWorkspaceGitignore returns a matcher for the workspace-root
// .gitignore, or nil when the file is absent or unparseable. A nil
// matcher means "no .gitignore filtering applies" — the walker
// short-circuits to the exclusion-list-only path.
func loadWorkspaceGitignore(root string) gitignore.IgnoreMatcher {
	path := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	matcher, err := gitignore.NewGitIgnore(path)
	if err != nil {
		return nil
	}
	return matcher
}

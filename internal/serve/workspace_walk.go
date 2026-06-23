// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// walkWorkspaceYAML invokes visit once per YAML file under the
// workspace root, honoring the standard exclusion list and any
// `.gitignore` files found along the ancestor chain (root + nested).
//
// The standard exclusion list skips:
//   - any path component whose name begins with '.' (covers .git,
//     .aggrega, .trace, hidden tooling directories)
//   - node_modules, vendor, dist
//
// `.gitignore` semantics: every `.gitignore` from the file's
// containing directory up to the workspace root is consulted, in
// deepest-first order (the closest file wins per gitignore precedence
// rules — including negation patterns within each file). Per-directory
// matchers are cached across the walk to avoid re-parsing.
//
// Errors returned by visit halt the walk and propagate.
// filepath.WalkDir does not follow symlinks by default, matching the
// IDE's expectation that the workspace tree is a finite acyclic
// search domain.
//
// The walk checks ctx.Err() at each directory entry so a shutdown
// cancellation interrupts a long workspace traversal between files
// rather than running it to completion.
func (s *Server) walkWorkspaceYAML(ctx context.Context, visit func(path string) error) error {
	w := newGitignoreWalker(s.workspaceRoot)
	return filepath.WalkDir(s.workspaceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if path == s.workspaceRoot {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if excludedDirName(name) {
				return filepath.SkipDir
			}
			if w.matchesAny(path, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isYAMLFile(name) {
			return nil
		}
		if w.matchesAny(path, false) {
			return nil
		}
		return visit(path)
	})
}

// gitignoreWalker carries the per-directory matcher cache used during
// a single workspace walk. The walker constructs matchers lazily —
// each directory's `.gitignore` is parsed at most once and reused for
// every descendant file or sub-directory.
//
// Cache shape: dir absolute path → matcher (or nil sentinel for "no
// .gitignore here, but we checked"). The nil-sentinel keeps subsequent
// ancestor walks from re-stat'ing the same path.
type gitignoreWalker struct {
	root  string
	cache map[string]gitignore.Matcher
}

// newGitignoreWalker constructs a walker rooted at root.
func newGitignoreWalker(root string) *gitignoreWalker {
	return &gitignoreWalker{
		root:  root,
		cache: map[string]gitignore.Matcher{},
	}
}

// matchesAny tests path against every `.gitignore` matcher from the
// path's containing directory up to the walker's root, deepest-first.
// Returns true if any matcher's Match reports the path as ignored.
//
// Per gitignore precedence rules, the deepest matcher takes priority
// when patterns conflict. The first ignore-match across the ancestor
// chain short-circuits and returns true. Negation patterns
// (`!pattern`) are resolved inside each per-directory matcher; this
// walker doesn't synthesize cross-file negation.
//
// isDir is forwarded to each matcher so directory-only patterns
// (`dirname/`) take effect: directory entries are matched with
// isDir=true. The candidate path is split into root-relative
// components because the matcher operates on path-component slices
// rather than on a single string.
func (w *gitignoreWalker) matchesAny(path string, isDir bool) bool {
	parts := w.relComponents(path)
	if parts == nil {
		return false
	}
	for _, dir := range w.ancestorsToRoot(path) {
		matcher := w.matcherFor(dir)
		if matcher == nil {
			continue
		}
		if matcher.Match(parts, isDir) {
			return true
		}
	}
	return false
}

// relComponents splits path into its components relative to the
// walker's root. Returns nil for a path equal to or outside the root.
// Each per-directory matcher is scoped to a domain expressed in these
// same root-relative components, so candidate paths and pattern
// domains share one coordinate system.
func (w *gitignoreWalker) relComponents(path string) []string {
	rel, err := filepath.Rel(w.root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return nil
	}
	return strings.Split(filepath.ToSlash(rel), "/")
}

// ancestorsToRoot returns the directory chain from the file's
// containing directory up to (and including) the walker's root, in
// deepest-first order. For a path equal to or outside the root, the
// returned slice is empty.
func (w *gitignoreWalker) ancestorsToRoot(path string) []string {
	dir := filepath.Dir(path)
	// Guard: only traverse paths under root. Paths outside the root
	// return an empty ancestor list (no .gitignore consultation).
	rel, err := filepath.Rel(w.root, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil
	}
	var out []string
	for {
		out = append(out, dir)
		if dir == w.root {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return out
}

// matcherFor returns the matcher for dir's `.gitignore`, parsing it
// lazily and caching the result. Returns nil when no `.gitignore`
// exists or cannot be read.
//
// Each pattern is scoped to a domain of dir's components relative to
// the walker's root, so a directory's patterns only match paths within
// that directory's subtree. The root `.gitignore` carries an empty
// domain and therefore applies tree-wide. Comment lines (`#`) and blank
// lines are skipped, matching gitignore file conventions.
func (w *gitignoreWalker) matcherFor(dir string) gitignore.Matcher {
	if cached, ok := w.cache[dir]; ok {
		return cached
	}
	path := filepath.Join(dir, ".gitignore")
	f, err := os.Open(path)
	if err != nil {
		w.cache[dir] = nil
		return nil
	}
	defer f.Close()

	domain := w.relComponents(dir)
	var patterns []gitignore.Pattern
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}
	if err := scanner.Err(); err != nil {
		w.cache[dir] = nil
		return nil
	}
	matcher := gitignore.NewMatcher(patterns)
	w.cache[dir] = matcher
	return matcher
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

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// markerSource names the marker tier that anchored the workspace
// root. Order encodes priority: lower values win when multiple
// markers exist in the same directory.
//
// Two name forms: String() returns the marker's pathy form (the
// literal filename or glob pattern) for use in operator-facing
// diagnostics where the marker NAME is the load-bearing information.
// WireName() returns a symbolic kebab-case enum value for protocol
// consumption (the daemon's aggrega/initialize response).
type markerSource int

const (
	markerNone markerSource = iota
	markerConfigFile
	markerAggregaDir
	markerAggregateGlob
	markerReleaseGlob
	markerManifestGlob
)

// String returns the human-readable name of the marker source.
// Used in error envelopes and diagnostic messages.
func (m markerSource) String() string {
	switch m {
	case markerConfigFile:
		return ".aggrega-config.yaml"
	case markerAggregaDir:
		return ".aggrega/"
	case markerAggregateGlob:
		return "aggregate*.yaml"
	case markerReleaseGlob:
		return "release*.yaml"
	case markerManifestGlob:
		return "manifest*.yaml"
	default:
		return "(none)"
	}
}

// WireName returns the symbolic kebab-case enum value for protocol
// consumption. markerNone returns the empty string so the daemon's
// initialize response can drop the field via the omitempty JSON tag
// (preserving the zero-value invisibility convention). The values
// are stable: extensions consume them programmatically for tooltips
// and disambiguation.
func (m markerSource) WireName() string {
	switch m {
	case markerConfigFile:
		return "config"
	case markerAggregaDir:
		return "directory"
	case markerAggregateGlob:
		return "aggregate-glob"
	case markerReleaseGlob:
		return "release-glob"
	case markerManifestGlob:
		return "manifest-glob"
	default:
		return ""
	}
}

// discoveryResult carries the outcome of a successful workspace-root
// discovery: the root directory's absolute path, the absolute path of
// the config file (empty when no .aggrega-config.yaml was the
// anchor), and the marker tier that anchored.
type discoveryResult struct {
	root       string
	configPath string
	source     markerSource
}

// discoverWorkspaceRoot walks upward from start, checking each
// directory in turn for any of the five workspace markers. The first
// directory containing a marker anchors the workspace.
//
// Marker priority WITHIN A SINGLE DIRECTORY is fixed:
//
//  1. .aggrega-config.yaml         (markerConfigFile)
//  2. .aggrega/                    (markerAggregaDir)
//  3. aggregate*.yaml              (markerAggregateGlob)
//  4. release*.yaml                (markerReleaseGlob)
//  5. manifest*.yaml               (markerManifestGlob)
//
// Cross-directory disambiguation falls out of the parent walk
// naturally: a deeper directory's marker (any tier) anchors before
// reaching a shallower directory's higher-priority marker. A child
// with aggregate.yaml is the workspace root even if the parent has
// .aggrega-config.yaml — the child's directory is closer to the cwd
// and represents the operator's working surface.
//
// Lockfile names ending in .lock.yaml are excluded from the glob
// markers — a lockfile alone does not anchor a workspace. Globs
// match only top-level entries of each walked directory (no
// recursion).
//
// Symlink handling preserves the prior single-marker behavior: the
// walk uses lexical filepath.Dir traversal and does not dereference
// symlinks. Workspaces mounted under symlinks resolve against the
// lexical parent chain.
//
// Returns os.ErrNotExist when the walk reaches the filesystem root
// without a match.
func discoverWorkspaceRoot(start string) (discoveryResult, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return discoveryResult{}, err
	}
	dir := abs
	for {
		res, found, err := checkMarkersInDir(dir)
		if err != nil {
			return discoveryResult{}, err
		}
		if found {
			return res, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return discoveryResult{}, os.ErrNotExist
		}
		dir = parent
	}
}

// checkMarkersInDir inspects dir for any of the five marker types in
// priority order. Returns the populated result on the first match
// found (found=true); empty result with found=false when no marker
// exists in this directory.
func checkMarkersInDir(dir string) (discoveryResult, bool, error) {
	// Tier 1: .aggrega-config.yaml (exact filename).
	configPath := filepath.Join(dir, ".aggrega-config.yaml")
	if st, err := os.Stat(configPath); err == nil && !st.IsDir() {
		return discoveryResult{root: dir, configPath: configPath, source: markerConfigFile}, true, nil
	}

	// Tier 2: .aggrega/ (directory).
	aggregaDir := filepath.Join(dir, ".aggrega")
	if st, err := os.Stat(aggregaDir); err == nil && st.IsDir() {
		return discoveryResult{root: dir, source: markerAggregaDir}, true, nil
	}

	// Tier 3-5: glob markers. Order is significant — aggregate before
	// release before manifest.
	for _, tier := range []struct {
		pattern string
		source  markerSource
	}{
		{"aggregate*.yaml", markerAggregateGlob},
		{"release*.yaml", markerReleaseGlob},
		{"manifest*.yaml", markerManifestGlob},
	} {
		matches, err := filepath.Glob(filepath.Join(dir, tier.pattern))
		if err != nil {
			return discoveryResult{}, false, fmt.Errorf("glob %s: %w", tier.pattern, err)
		}
		if hasNonLockfileMatch(matches) {
			return discoveryResult{root: dir, source: tier.source}, true, nil
		}
	}

	return discoveryResult{}, false, nil
}

// hasNonLockfileMatch reports whether matches contains at least one
// path whose basename does not end in .lock.yaml. A lockfile alone
// does not anchor a workspace — it's a generated companion to a
// Release file, not an authoring surface itself.
func hasNonLockfileMatch(matches []string) bool {
	for _, m := range matches {
		if !strings.HasSuffix(filepath.Base(m), ".lock.yaml") {
			return true
		}
	}
	return false
}

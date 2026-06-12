// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// markerFilePath is the per-operator marker the multi-Manifest
// cascade reads at Step 3. The file lives under the workspace's
// `.aggrega/` scratch directory and is conventionally gitignored —
// operators choose their own active Release without committing the
// choice to the workspace-wide config.
const markerFilePath = ".aggrega/active-release"

// cascadeAttempt records what the disambiguation cascade tried and
// which step (if any) selected the active Release. Consumed by the
// cascadeDiagnosticMessage helper to construct human-readable
// error-envelope text for operations needing Release context when
// the cascade exhausted.
type cascadeAttempt struct {
	// ResolvedStep is 1-3 when a step succeeded; 0 when the cascade
	// exhausted into Step 4 fallback (activeRelease stays nil).
	ResolvedStep int

	// AuthoringReleaseConfigured records whether the workspace
	// config's Build.AuthoringRelease was non-empty (Step 1 was
	// attempted).
	AuthoringReleaseConfigured bool

	// AuthoringReleaseLoadError is non-nil when Step 1 was attempted
	// but the named file failed to load.
	AuthoringReleaseLoadError error

	// AuthoringReleasePath is the resolved path Step 1 attempted, kept
	// so a found-but-invalid authoring_release can be squiggled on the
	// file (the load error alone does not carry its own path).
	AuthoringReleasePath string

	// ReleaseCandidatesFound is the number of Release YAML files
	// discovered by the Step 2 workspace walk.
	ReleaseCandidatesFound int

	// SingleReleaseLoadError is non-nil when Step 2's single candidate
	// was found but failed to load. Symmetric with the Step 1/3 load-
	// error fields; before this capture Step 2's failure was dropped to
	// stderr only, leaving the common one-Release workspace's
	// found-but-invalid case with no structured record.
	SingleReleaseLoadError error

	// SingleReleaseCandidatePath is the path of Step 2's single
	// candidate, kept so its load failure can be squiggled on the file.
	SingleReleaseCandidatePath string

	// MarkerFilePresent records whether the .aggrega/active-release
	// marker existed (Step 3 was attempted).
	MarkerFilePresent bool

	// MarkerFileTarget is the path the marker pointed at (when
	// present), for use in diagnostic messages.
	MarkerFileTarget string

	// MarkerFileLoadError is non-nil when Step 3 was attempted but
	// the marker's target file failed to load.
	MarkerFileLoadError error

	// MarkerResolvedPath is the resolved path the marker target pointed
	// at, kept so a found-but-invalid marker target can be squiggled on
	// the file (MarkerFileTarget is the raw, possibly relative, target).
	MarkerResolvedPath string
}

// failedReleaseCandidate is a Release file the cascade found but could
// not load — a found-but-invalid candidate, distinct from a
// genuinely-absent Release.
type failedReleaseCandidate struct {
	Path string
	Err  error
}

// failedReleaseCandidates returns every found-but-invalid Release the
// cascade encountered, across all three load steps, so each can be
// surfaced as an editor diagnostic on its own file. Empty when no step
// found a file that failed to load (the genuinely-absent case).
func (a cascadeAttempt) failedReleaseCandidates() []failedReleaseCandidate {
	var out []failedReleaseCandidate
	if a.AuthoringReleaseLoadError != nil && a.AuthoringReleasePath != "" {
		out = append(out, failedReleaseCandidate{Path: a.AuthoringReleasePath, Err: a.AuthoringReleaseLoadError})
	}
	if a.SingleReleaseLoadError != nil && a.SingleReleaseCandidatePath != "" {
		out = append(out, failedReleaseCandidate{Path: a.SingleReleaseCandidatePath, Err: a.SingleReleaseLoadError})
	}
	if a.MarkerFileLoadError != nil && a.MarkerResolvedPath != "" {
		out = append(out, failedReleaseCandidate{Path: a.MarkerResolvedPath, Err: a.MarkerFileLoadError})
	}
	return out
}

// runManifestCascade executes the four-step disambiguation cascade
// against the workspace. Returns the resolved Release (nil when the
// cascade exhausts into Step 4) and the cascadeAttempt log
// describing which steps were tried.
//
// Cascade-internal load errors (Step 1 / Step 3 file misses) surface
// as stderr warnings AND record into the cascadeAttempt log; the
// cascade itself does not return an error. A misconfigured
// authoring_release: or stale marker file is non-fatal — the daemon
// proceeds with the next step rather than refusing to start.
//
// Step ordering: Step 1 (config) → Step 2 (single Release in
// workspace) → Step 3 (marker file) → Step 4 (fallback nil). A
// workspace with exactly one Release file and a marker pointing at
// a different file wins Step 2 — the marker is consulted only when
// Step 2's "exactly one" condition does not hold.
func runManifestCascade(workspaceRoot string, wsCfg workspaceconfig.Config, stderr io.Writer) (*types.Release, string, cascadeAttempt) {
	var attempt cascadeAttempt

	// Step 1: config field
	if wsCfg.Build.AuthoringRelease != "" {
		attempt.AuthoringReleaseConfigured = true
		path := resolveCascadePath(workspaceRoot, wsCfg.Build.AuthoringRelease)
		rel, err := resolver.LoadRelease(path)
		if err == nil {
			attempt.ResolvedStep = 1
			return rel, path, attempt
		}
		attempt.AuthoringReleaseLoadError = err
		attempt.AuthoringReleasePath = path
		fmt.Fprintf(stderr, "  cascade: step 1 authoring_release=%q failed to load: %v\n", path, err)
	}

	// Step 2: workspace-recursive Release walk; exactly-one wins
	candidates := walkForReleases(workspaceRoot)
	attempt.ReleaseCandidatesFound = len(candidates)
	if len(candidates) == 1 {
		rel, err := resolver.LoadRelease(candidates[0])
		if err == nil {
			attempt.ResolvedStep = 2
			return rel, candidates[0], attempt
		}
		attempt.SingleReleaseLoadError = err
		attempt.SingleReleaseCandidatePath = candidates[0]
		fmt.Fprintf(stderr, "  cascade: step 2 single Release %q failed to load: %v\n", candidates[0], err)
	}

	// Step 3: marker file
	markerAbsPath := filepath.Join(workspaceRoot, markerFilePath)
	if data, err := os.ReadFile(markerAbsPath); err == nil {
		attempt.MarkerFilePresent = true
		target := strings.TrimSpace(string(data))
		attempt.MarkerFileTarget = target
		if target != "" {
			resolved := resolveCascadePath(workspaceRoot, target)
			rel, lerr := resolver.LoadRelease(resolved)
			if lerr == nil {
				attempt.ResolvedStep = 3
				return rel, resolved, attempt
			}
			attempt.MarkerFileLoadError = lerr
			attempt.MarkerResolvedPath = resolved
			fmt.Fprintf(stderr, "  cascade: step 3 marker target %q failed to load: %v\n", resolved, lerr)
		}
	}

	// Step 4: fallback — Release path stays empty alongside the nil
	// Release object.
	attempt.ResolvedStep = 0
	return nil, "", attempt
}

// resolveCascadePath joins a workspace-relative path with the root,
// or returns the path as-is when it is absolute. Mirrors the path-
// resolution convention CLI verbs use for --release.
func resolveCascadePath(workspaceRoot, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workspaceRoot, p)
}

// walkForReleases returns the absolute paths of every Release-kind
// YAML file under workspaceRoot. Header-only YAML parse; malformed
// files and non-Release kinds are silently skipped. The canonical
// ignore-set (.git, .aggrega, node_modules, vendor, dist, .trace,
// hidden dirs) is excluded — Step 2 walks the entire workspace
// root, not a configured subdirectory, so filtering vendored copies
// and build outputs is load-bearing for correctness.
func walkForReleases(workspaceRoot string) []string {
	var out []string
	_ = filepath.WalkDir(workspaceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if shouldExcludeDir(d.Name(), path, workspaceRoot) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var hdr struct {
			Kind string `yaml:"kind"`
		}
		if uerr := yaml.Unmarshal(data, &hdr); uerr != nil {
			return nil
		}
		if hdr.Kind == "Release" {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// shouldExcludeDir filters out the canonical ignore-set plus any
// hidden directories. The workspace root itself is never excluded
// (its own name might start with `.` legitimately, e.g., a `.local`
// workspace dir).
func shouldExcludeDir(name, path, workspaceRoot string) bool {
	if path == workspaceRoot {
		return false
	}
	switch name {
	case ".git", ".aggrega", "node_modules", "vendor", "dist", ".trace":
		return true
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	return false
}

// cascadeDiagnosticMessage returns a one-line description of what
// the disambiguation cascade attempted. Consumed by error envelopes
// (the 1002 release-not-resolved code) when operations needing
// Release context find activeRelease == nil.
//
// The returned string names every step that was attempted and what
// was missing, so the operator's IDE can render a coherent
// "this is why Release context could not be resolved" diagnostic.
//
// Returns "release resolved" when ResolvedStep is non-zero — the
// helper is intended for the cascade-exhausted case but stays
// honest when called against a populated activeRelease.
func (s *Server) cascadeDiagnosticMessage() string {
	a := s.cascadeAttempt
	if a.ResolvedStep != 0 {
		return fmt.Sprintf("release resolved at cascade step %d", a.ResolvedStep)
	}

	parts := []string{"release not resolved"}

	// Step 1 description
	switch {
	case !a.AuthoringReleaseConfigured:
		parts = append(parts, "no authoring_release configured")
	case a.AuthoringReleaseLoadError != nil:
		parts = append(parts, fmt.Sprintf("authoring_release configured but failed to load (%v)", a.AuthoringReleaseLoadError))
	}

	// Step 2 description
	parts = append(parts, fmt.Sprintf("workspace contains %d Release file(s)", a.ReleaseCandidatesFound))

	// Step 3 description
	switch {
	case !a.MarkerFilePresent:
		parts = append(parts, "no .aggrega/active-release marker")
	case a.MarkerFileTarget == "":
		parts = append(parts, ".aggrega/active-release marker is empty")
	case a.MarkerFileLoadError != nil:
		parts = append(parts, fmt.Sprintf(".aggrega/active-release marker target %q failed to load (%v)", a.MarkerFileTarget, a.MarkerFileLoadError))
	}

	return strings.Join(parts, "; ")
}

// cascadeAttemptFullTrace returns the multi-line trace of what each
// cascade step attempted and why it failed (or was skipped). Consumed
// by the 1002 (release-not-resolved) emission path so the extension
// can auto-log the full trace to its OutputChannel; the toast
// continues to display the one-line summary from
// cascadeDiagnosticMessage.
//
// Each step appears on its own line so the OutputChannel rendering
// remains scannable. Returns the empty string when the cascade
// resolved a Release (the trace is informative only on the
// exhausted-cascade path).
func (s *Server) cascadeAttemptFullTrace() string {
	a := s.cascadeAttempt
	if a.ResolvedStep != 0 {
		return ""
	}

	lines := []string{"Aggrega: release-not-resolved cascade trace"}

	// Step 1 — config-based authoring_release
	switch {
	case !a.AuthoringReleaseConfigured:
		lines = append(lines, "  step 1 (build.authoring_release in .aggrega-config.yaml): not configured")
	case a.AuthoringReleaseLoadError != nil:
		lines = append(lines, fmt.Sprintf("  step 1 (build.authoring_release): configured but failed to load — %v", a.AuthoringReleaseLoadError))
	default:
		lines = append(lines, "  step 1 (build.authoring_release): configured and loaded")
	}

	// Step 2 — single-Release heuristic
	switch a.ReleaseCandidatesFound {
	case 0:
		lines = append(lines, "  step 2 (single-Release heuristic): zero Release files found in workspace")
	case 1:
		if a.SingleReleaseLoadError != nil {
			lines = append(lines, fmt.Sprintf("  step 2 (single-Release heuristic): one Release file found at %s but it failed to load — %v", a.SingleReleaseCandidatePath, a.SingleReleaseLoadError))
		} else {
			lines = append(lines, "  step 2 (single-Release heuristic): one Release file found — would have resolved here if step 1 did not")
		}
	default:
		lines = append(lines, fmt.Sprintf("  step 2 (single-Release heuristic): ambiguous — %d Release files found, no marker selected one", a.ReleaseCandidatesFound))
	}

	// Step 3 — .aggrega/active-release marker
	switch {
	case !a.MarkerFilePresent:
		lines = append(lines, "  step 3 (.aggrega/active-release marker): not present")
	case a.MarkerFileTarget == "":
		lines = append(lines, "  step 3 (.aggrega/active-release marker): present but empty")
	case a.MarkerFileLoadError != nil:
		lines = append(lines, fmt.Sprintf("  step 3 (.aggrega/active-release marker): target %q failed to load — %v", a.MarkerFileTarget, a.MarkerFileLoadError))
	default:
		lines = append(lines, fmt.Sprintf("  step 3 (.aggrega/active-release marker): target %q resolved", a.MarkerFileTarget))
	}

	// Step 4 — Aggregate-only fallback (always exhausted at this point;
	// reaching here means none of the prior steps populated activeRelease)
	lines = append(lines, "  step 4 (Aggregate-only fallback): operation requires Release context; fallback not applicable")

	return strings.Join(lines, "\n")
}

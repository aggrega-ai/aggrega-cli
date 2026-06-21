// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigFilename is the single canonical basename of the per-directory
// workspace configuration file. Every discovery, cascade, and remediation
// site reads this one constant rather than re-spelling the literal, so the
// filename has exactly one definition across the product.
const ConfigFilename = ".aggrega-config.yaml"

// Marker identifies what anchored a workspace root, for the daemon's
// aggrega/initialize "workspaceMarker" wire token. The zero value
// MarkerNone reports an explicit --workspace-root (no marker consulted).
type Marker int

const (
	// MarkerNone reports that an explicit root was supplied (the
	// --workspace-root flag) and no marker was consulted.
	MarkerNone Marker = iota
	// MarkerConfig reports a `.aggrega-config.yaml` carrying a
	// `workspace:` block anchored the root.
	MarkerConfig
	// MarkerAggregaDir reports a `.aggrega/` directory anchored the root.
	MarkerAggregaDir
	// MarkerAggregateGlob reports an `aggregate*.yaml` file anchored the root.
	MarkerAggregateGlob
	// MarkerReleaseGlob reports a `release*.yaml` file anchored the root.
	MarkerReleaseGlob
	// MarkerManifestGlob reports a `manifest*.yaml` file anchored the root.
	MarkerManifestGlob
	// MarkerCWD reports that no marker was found and the start directory
	// was used as the root.
	MarkerCWD
)

// String returns the stable wire token for m, consumed by
// aggrega/initialize's "workspaceMarker" field. MarkerNone and MarkerCWD
// return "" — there is no marker to report.
func (m Marker) String() string {
	switch m {
	case MarkerConfig:
		return "config"
	case MarkerAggregaDir:
		return "directory"
	case MarkerAggregateGlob:
		return "aggregate-glob"
	case MarkerReleaseGlob:
		return "release-glob"
	case MarkerManifestGlob:
		return "manifest-glob"
	default:
		return ""
	}
}

// ResolvedWorkspace is the opaque result of Resolve: the absolute workspace
// root, the cascade-merged Config, the highest-precedence Source that
// contributed, the marker that anchored the root, and the ordered list of
// config files that contributed (lowest precedence first), for the
// "loaded config from" breadcrumb. It is constructed only by Resolve — it has
// no exported fields and no exported constructor — so a caller cannot
// fabricate or mutate a resolved workspace; the only way to obtain one is to
// run the single resolution path.
type ResolvedWorkspace struct {
	root    string
	config  Config
	source  Source
	marker  Marker
	files   []string
}

// Root returns the absolute workspace root directory.
func (r *ResolvedWorkspace) Root() string { return r.root }

// Config returns the cascade-merged effective configuration.
func (r *ResolvedWorkspace) Config() Config { return r.config }

// Source returns the highest-precedence layer that contributed.
func (r *ResolvedWorkspace) Source() Source { return r.source }

// Marker returns what anchored the root (for the daemon wire token).
func (r *ResolvedWorkspace) Marker() Marker { return r.marker }

// ContributingFiles returns the absolute paths of the config files that
// contributed to the merge, lowest precedence first (global, then tree files
// farthest-from-root last). Empty when only Default() applied. Used to emit
// the "loaded config from: <files>" breadcrumb so an operator can always see
// which settings applied.
func (r *ResolvedWorkspace) ContributingFiles() []string {
	out := make([]string, len(r.files))
	copy(out, r.files)
	return out
}

// Resolve performs the whole workspace-resolution job from a starting
// directory: discover the root, cascade-collect the `.aggrega-config.yaml`
// files from the root down to start, merge them (with the global
// $AGGREGA_CONFIG layer beneath and Default() at the base), and return the
// opaque result.
//
// Root precedence: flagRoot (the --workspace-root override) > the nearest
// ancestor whose `.aggrega-config.yaml` carries a `workspace:` block > the
// nearest ancestor carrying a heuristic marker (`.aggrega/`, `aggregate*.yaml`,
// `release*.yaml`, `manifest*.yaml`) > start itself. A bare
// `.aggrega-config.yaml` (no `workspace:` block) does NOT anchor a root — it
// still contributes to the cascade, but cannot hijack the root (the failure
// mode where a home/global settings file marks every directory as a root).
//
// Cascade: every `.aggrega-config.yaml` from the root down to start
// contributes; nearer-to-start overrides farther via the existing
// whole-replace + `_append` semantics, generalized from three fixed layers to
// N. The `workspace:`-bearing root config caps the cascade — files above the
// root are not collected.
func Resolve(start, flagRoot string) (*ResolvedWorkspace, error) {
	if start == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("workspaceconfig: getwd: %w", err)
		}
		start = cwd
	}
	absStart, err := filepath.Abs(start)
	if err != nil {
		return nil, fmt.Errorf("workspaceconfig: resolve start %s: %w", start, err)
	}

	root, marker, err := discoverRoot(absStart, flagRoot)
	if err != nil {
		return nil, err
	}

	// An explicit --workspace-root pins the root; the cascade then collects
	// only that root's config (not a walk from a working directory that may
	// sit outside the pinned root). Discovery mode cascades from root→start.
	if flagRoot != "" {
		absStart = root
	}

	// Tree config files from root down to start, lowest precedence
	// (root, farthest from start) first.
	treeFiles := cascadeFiles(root, absStart)

	merged := Default()
	source := SourceDefaults
	var contributing []string
	var appendLayers []appendSlices

	// Global layer ($AGGREGA_CONFIG), beneath all tree files.
	globalCfg, globalLoaded, globalPath, err := loadGlobalLayer()
	if err != nil {
		return nil, err
	}
	if globalLoaded {
		appendLayers = append(appendLayers, captureAppends(globalCfg))
		stripAppends(&globalCfg)
		merged = globalCfg
		source = SourceGlobal
		contributing = append(contributing, globalPath)
	}

	// Tree layers, farthest-from-start first (lowest precedence) to
	// nearest-to-start last (highest precedence).
	for _, path := range treeFiles {
		raw, lerr := load(path)
		if lerr != nil {
			return nil, lerr
		}
		appendLayers = append(appendLayers, captureAppends(raw))

		overlaid, oerr := loadOverlayInto(path, merged)
		if oerr != nil {
			return nil, oerr
		}
		stripAppends(&overlaid)
		merged = overlaid
		source = SourceWorkspace
		contributing = append(contributing, path)
	}

	applyAppends(&merged, appendLayers...)

	return &ResolvedWorkspace{
		root:   root,
		config: merged,
		source: source,
		marker: marker,
		files:  contributing,
	}, nil
}

// discoverRoot determines the workspace root and the marker that anchored it,
// honoring the precedence documented on Resolve. absStart must be absolute.
func discoverRoot(absStart, flagRoot string) (root string, marker Marker, err error) {
	if flagRoot != "" {
		abs, aerr := filepath.Abs(flagRoot)
		if aerr != nil {
			return "", MarkerNone, fmt.Errorf("workspaceconfig: --workspace-root %s: %w", flagRoot, aerr)
		}
		return abs, MarkerNone, nil
	}

	// Walk up from start. The nearest `workspace:`-bearing config wins
	// outright. Otherwise remember the nearest heuristic marker as a
	// fallback and keep climbing in case a `workspace:` config sits above.
	fallbackRoot := ""
	fallbackMarker := MarkerNone
	dir := absStart
	for {
		cfgPath := filepath.Join(dir, ConfigFilename)
		if data, rerr := os.ReadFile(cfgPath); rerr == nil {
			if hasWorkspaceKey(data) {
				return dir, MarkerConfig, nil
			}
		}
		if fallbackRoot == "" {
			if hm := heuristicMarker(dir); hm != MarkerNone {
				fallbackRoot = dir
				fallbackMarker = hm
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	if fallbackRoot != "" {
		return fallbackRoot, fallbackMarker, nil
	}
	return absStart, MarkerCWD, nil
}

// heuristicMarker reports the highest-priority heuristic marker present in
// dir, or MarkerNone. Lockfile-suffixed glob matches (*.lock.yaml) are
// excluded so a generated lockfile never anchors a root.
func heuristicMarker(dir string) Marker {
	if fi, err := os.Stat(filepath.Join(dir, ".aggrega")); err == nil && fi.IsDir() {
		return MarkerAggregaDir
	}
	for _, m := range []struct {
		prefix string
		marker Marker
	}{
		{"aggregate", MarkerAggregateGlob},
		{"release", MarkerReleaseGlob},
		{"manifest", MarkerManifestGlob},
	} {
		if globPrefixPresent(dir, m.prefix) {
			return m.marker
		}
	}
	return MarkerNone
}

// globPrefixPresent reports whether dir contains a file matching
// `<prefix>*.yaml` or `<prefix>*.yml`, excluding `*.lock.yaml` lockfiles.
func globPrefixPresent(dir, prefix string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if strings.HasSuffix(name, ".lock.yaml") || strings.HasSuffix(name, ".lock.yml") {
			continue
		}
		return true
	}
	return false
}

// cascadeFiles returns the `.aggrega-config.yaml` paths that exist from root
// down to start (inclusive), ordered farthest-from-start (root) first so the
// caller applies them lowest-precedence first. start and root must both be
// absolute, with root an ancestor of (or equal to) start.
func cascadeFiles(root, start string) []string {
	var dirs []string
	dir := start
	for {
		dirs = append(dirs, dir)
		if dir == root {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// dirs is start..root (nearest first); reverse to root..start.
	sort.Sort(sort.Reverse(byPathDepth(dirs)))
	var files []string
	for _, d := range dirs {
		p := filepath.Join(d, ConfigFilename)
		if _, err := os.Stat(p); err == nil {
			files = append(files, p)
		}
	}
	return files
}

// byPathDepth orders paths by descending depth (more separators first), so a
// reverse sort yields shallowest (root) first. Paths share a common ancestor
// chain, so separator count is a faithful depth proxy.
type byPathDepth []string

func (b byPathDepth) Len() int      { return len(b) }
func (b byPathDepth) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b byPathDepth) Less(i, j int) bool {
	return strings.Count(b[i], string(os.PathSeparator)) > strings.Count(b[j], string(os.PathSeparator))
}

// loadGlobalLayer reads the explicit global config layer. Only the
// $AGGREGA_CONFIG override is honored — an unset variable means no global
// layer (the cascade's top tree file and Default() suffice). When set, the
// path is explicit, so a missing file is an error rather than a silent
// fallback. There is no XDG (`~/.config/aggrega/config.yaml`) path: the
// home-level `~/.aggrega-config.yaml` participates as the top cascade rung
// like any other tree file.
func loadGlobalLayer() (cfg Config, loaded bool, path string, err error) {
	v := os.Getenv(envVarConfigOverride)
	if v == "" {
		return Default(), false, "", nil
	}
	if _, statErr := os.Stat(v); statErr != nil {
		return Config{}, false, "", fmt.Errorf("workspaceconfig: $%s config not found at %s: %w", envVarConfigOverride, v, statErr)
	}
	cfg, err = load(v)
	if err != nil {
		return Config{}, false, "", fmt.Errorf("workspaceconfig: load global %s: %w", v, err)
	}
	return cfg, true, v, nil
}

// hasWorkspaceKey reports whether a config document carries a top-level
// `workspace:` key. Presence — not content — is the root marker: the typed
// Config cannot distinguish `workspace: {}` from an omitted block (yaml.v3
// treats them identically), so the decision is made on the raw document.
func hasWorkspaceKey(data []byte) bool {
	var probe map[string]any
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return false
	}
	_, present := probe["workspace"]
	return present
}

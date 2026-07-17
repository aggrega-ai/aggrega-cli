// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ConfigFilename is the single canonical basename of the per-directory
// workspace configuration file. Every discovery, cascade, and remediation
// site reads this one constant rather than re-spelling the literal, so the
// filename has exactly one definition across the product.
const ConfigFilename = ".aggrega-config.yaml"

// AppendCIConfigFilename is the basename of the CI-owned additive overlay that
// sits beside the hand-authored ConfigFilename at any cascade directory. It is
// a separate file so an automated pipeline owns and may overwrite it wholesale
// without ever touching the developer's hand-authored base config.
//
// The overlay is additive-only and validates against the same
// aggrega-config-v1alpha1 schema as the base file. For each of the seven
// list-valued fields (catalog_dirs, narrowing_exemptions, the four plugin
// sockets mutator/policy/linter/importer, and the provider bindings) its
// entries — both the base list and any `_append` companion — are concatenated
// onto the resolved list in cascade order. It cannot remove or scalar-override anything the base declares: a
// scalar or map field carried in the overlay has no additive meaning, so it is
// ignored and named in a resolution warning rather than applied. The overlay
// never anchors a workspace root — only ConfigFilename with a top-level
// `workspace:` block does that — so dropping this file into a directory adds
// attachments without changing where the workspace begins.
const AppendCIConfigFilename = ".aggrega-config-append-ci.yaml"

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
// config files that contributed (lowest precedence first), which a command may
// present to show the resolution provenance. It is constructed only by Resolve — it has
// no exported fields and no exported constructor — so a caller cannot
// fabricate or mutate a resolved workspace; the only way to obtain one is to
// run the single resolution path.
type ResolvedWorkspace struct {
	root     string
	config   Config
	source   Source
	marker   Marker
	files    []string
	warnings []string

	// start, flagRoot, and lookup capture the exact inputs of the resolution
	// that produced this value, so a second pass can re-run the single merge
	// path with only the global file layer suppressed (the reproducibility
	// provenance in GlobalOnlyBuildCritical). They are not part of the resolved
	// result an ordinary consumer reads.
	start    string
	flagRoot string
	lookup   func(string) string
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
// farthest-from-root last). Empty when only Default() applied. This ordered
// list is the resolution-provenance data surface a command may present so an
// operator can see which files' settings applied.
func (r *ResolvedWorkspace) ContributingFiles() []string {
	out := make([]string, len(r.files))
	copy(out, r.files)
	return out
}

// Warnings returns non-fatal advisories accumulated during resolution, in a
// stable order. The CI-owned additive overlay contributes one entry per
// non-additive key it declared — such a key is ignored (the overlay is
// additive-only) and named here so the CI author learns the file cannot
// scalar-override the base. Empty when nothing warned.
func (r *ResolvedWorkspace) Warnings() []string {
	out := make([]string, len(r.warnings))
	copy(out, r.warnings)
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
// root are not collected. At each cascade directory a CI-owned overlay
// (AppendCIConfigFilename), when present, folds additively into the same
// `_append` pool in cascade order — it never whole-replaces a base scalar and
// never anchors the root.
//
// Environment overlay: a top layer above the merged config and below any CLI
// flag the command applies, so the effective ladder is flag > environment >
// config > default. The optional look reads the environment; when omitted (or
// nil) the production os.Getenv is used. A test or the daemon passes an
// explicit, frozen lookup so resolution stays deterministic and parallel-safe.
func Resolve(start, flagRoot string, look ...func(string) string) (*ResolvedWorkspace, error) {
	lookup := os.Getenv
	if len(look) > 0 && look[0] != nil {
		lookup = look[0]
	}
	rw, err := resolve(start, flagRoot, lookup, false)
	if err != nil {
		return nil, err
	}
	rw.start = start
	rw.flagRoot = flagRoot
	rw.lookup = lookup
	return rw, nil
}

// ResolveWithoutGlobal resolves the workspace root and tree cascade without
// reading the global config layer, for a command that is about to write the
// global config and therefore must not fail when that file does not yet exist.
// It otherwise mirrors Resolve — the same root discovery, tree cascade,
// defaults, and environment overlay — and shares its variadic-lookup default of
// the process environment. Only the global-layer read is skipped; a command
// that needs the merged global config must use Resolve instead.
func ResolveWithoutGlobal(start, flagRoot string, look ...func(string) string) (*ResolvedWorkspace, error) {
	lookup := os.Getenv
	if len(look) > 0 && look[0] != nil {
		lookup = look[0]
	}
	rw, err := resolve(start, flagRoot, lookup, true)
	if err != nil {
		return nil, err
	}
	rw.start = start
	rw.flagRoot = flagRoot
	rw.lookup = lookup
	return rw, nil
}

// resolve is the single workspace-resolution merge path Resolve wraps. It runs
// the whole job — root discovery, the tree cascade, the global layer, the
// `_append` composition, and the environment overlay — against an already-
// resolved lookup. When skipGlobal is true it omits the global file layer
// (leaving the tree cascade, defaults, and environment overlay untouched), so a
// second pass can attribute which build-critical bindings originated only in the
// global file. There is no re-implemented merge: both the full and the tree-only
// resolution run this one function.
func resolve(start, flagRoot string, lookup func(string) string, skipGlobal bool) (*ResolvedWorkspace, error) {
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

	merged := Default()
	source := SourceDefaults
	var contributing []string
	var appendLayers []appendSlices
	var warnings []string
	var signingPolicy signingPolicyAccumulator

	// Global layer (explicit $AGGREGA_CONFIG or the XDG default), beneath all
	// tree files. Suppressed for the tree-only provenance pass so the same merge
	// path yields the workspace-tree-only view against which the full resolution
	// is diffed.
	if !skipGlobal {
		globalCfg, globalLoaded, globalPath, err := loadGlobalLayer(lookup)
		if err != nil {
			return nil, err
		}
		if globalLoaded {
			gdir := filepath.Dir(globalPath)
			ga := captureAppends(globalCfg)
			resolveAppendPaths(&ga, gdir)
			appendLayers = append(appendLayers, ga)
			signingPolicy.add(globalCfg.Signing)
			stripAppends(&globalCfg)
			resolveConfigPaths(&globalCfg, gdir)
			merged = globalCfg
			source = SourceGlobal
			contributing = append(contributing, globalPath)
		}
	}

	// Tree layers, farthest-from-start first (lowest precedence) to
	// nearest-to-start last (highest precedence). Relative path fields resolve
	// against the directory of the file that declared them (per layer), so the
	// merged Config carries absolute paths. Each directory contributes its base
	// `.aggrega-config.yaml` (whole-replace + `_append`) first, then its CI-owned
	// overlay (additive-only), so a CI attachment always lands after the base
	// entries it sits beside.
	for _, dir := range cascadeDirs(root, absStart) {
		basePath := filepath.Join(dir, ConfigFilename)
		if _, serr := os.Stat(basePath); serr == nil {
			raw, lerr := load(basePath)
			if lerr != nil {
				return nil, lerr
			}
			la := captureAppends(raw)
			resolveAppendPaths(&la, dir)
			appendLayers = append(appendLayers, la)
			signingPolicy.add(raw.Signing)

			overlaid, oerr := loadOverlayInto(basePath, merged)
			if oerr != nil {
				return nil, oerr
			}
			stripAppends(&overlaid)
			// Absolute values carried from prior (farther) layers skip; only the
			// relative fields this layer wrote resolve against this layer's dir.
			resolveConfigPaths(&overlaid, dir)
			merged = overlaid
			source = SourceWorkspace
			contributing = append(contributing, basePath)
		}

		ciPath := filepath.Join(dir, AppendCIConfigFilename)
		if _, serr := os.Stat(ciPath); serr == nil {
			ciCfg, ignored, cerr := loadCIOverlay(ciPath)
			if cerr != nil {
				return nil, cerr
			}
			ci := foldCIAdditive(ciCfg)
			resolveAppendPaths(&ci, dir)
			appendLayers = append(appendLayers, ci)
			signingPolicy.add(ciCfg.Signing)
			contributing = append(contributing, ciPath)
			for _, key := range ignored {
				warnings = append(warnings, fmt.Sprintf(
					"%s: ignoring non-additive key %q (the CI-owned overlay is additive-only and cannot override the base config)", ciPath, key))
			}
		}
	}

	applyAppends(&merged, appendLayers...)

	// A capability may bind exactly one provider plugin. Duplicates can arise
	// only after compose — within one layer's base list, or across the cascade
	// and its `_append` companions — so the check runs on the composed list.
	if err := checkUniqueProviderTargets(merged.Plugins.Providers); err != nil {
		return nil, err
	}

	// Environment overlay: the top config layer, above the merged file cascade
	// and below the CLI flags the command applies. A SET variable wins over the
	// config value; an unset one falls through.
	envContributing, err := applyEnvOverlay(&merged, lookup)
	if err != nil {
		return nil, err
	}
	for _, name := range envContributing {
		contributing = append(contributing, "$"+name)
	}

	// Compose the org signing policy across every layer plus the environment
	// overlay. This is a scoped exception to the whole-replace-per-layer rule:
	// `required` OR-folds and `kinds` unions, so a lower-precedence layer cannot
	// shadow a higher one and the policy cannot be removed by an ordinary config
	// edit. Kept separate from applyAppends, which owns the seven list fields.
	signingPolicy.composeInto(&merged)

	return &ResolvedWorkspace{
		root:     root,
		config:   merged,
		source:   source,
		marker:   marker,
		files:    contributing,
		warnings: warnings,
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

// cascadeDirs returns the directories from root down to start (inclusive),
// ordered farthest-from-start (root) first so the caller applies them
// lowest-precedence first. start and root must both be absolute, with root an
// ancestor of (or equal to) start. Every such directory is a cascade rung
// whether or not it holds a config file — the caller stats each candidate file
// per directory, so a directory carrying only a CI-owned overlay still
// contributes.
func cascadeDirs(root, start string) []string {
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
	return dirs
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

// resolveFilesystemPath resolves a relative catalog directory against dir,
// returning an absolute path. Empty and already-absolute values pass through.
// This is the cascade's counterpart to the entity-source convention catalog
// loading uses: "relative" always means relative to the config file that wrote
// the value.
func resolveFilesystemPath(dir, v string) string {
	if v == "" || filepath.IsAbs(v) {
		return v
	}
	if abs, err := filepath.Abs(filepath.Join(dir, v)); err == nil {
		return abs
	}
	return v
}

// LooksLikeOCIURI reports whether uri is a registry reference rather than a
// filesystem path: an `oci://` scheme, an aggrega-hub host, or the
// `<host>/<path>:<tag>` shape (a slash and a colon, not absolute). Such URIs
// are NOT declaring-file-relative paths and are left untouched by resolution;
// every consumer that must distinguish a registry plugin from a local one
// shares this one predicate.
func LooksLikeOCIURI(uri string) bool {
	if strings.HasPrefix(uri, "oci://") {
		return true
	}
	if strings.Contains(uri, ".aggrega.ai") || strings.Contains(uri, "hub.aggrega.ai") {
		return true
	}
	if !filepath.IsAbs(uri) && strings.Contains(uri, "/") && strings.Contains(uri, ":") {
		return true
	}
	return false
}

// resolveConfigPaths resolves a layer's relative BASE path fields — workspace
// catalog_dirs and the four plugin-socket URIs — against dir, the directory of
// the config file that declared them. Absolute values carried from a
// farther-out layer pass through, so calling this after each layer's overlay is
// idempotent: only the relative fields this layer wrote are anchored.
func resolveConfigPaths(cfg *Config, dir string) {
	for i, d := range cfg.Workspace.CatalogDirs {
		cfg.Workspace.CatalogDirs[i] = resolveFilesystemPath(dir, d)
	}
	cfg.PluginSidecarDir = resolveFilesystemPath(dir, cfg.PluginSidecarDir)
	resolvePluginURIs(cfg.Plugins.Mutator, dir)
	resolvePluginURIs(cfg.Plugins.Policy, dir)
	resolvePluginURIs(cfg.Plugins.Linter, dir)
	resolvePluginURIs(cfg.Plugins.Importer, dir)
}

// resolveAppendPaths resolves a captured layer's relative `_append` path fields
// against dir before the cascade concatenates them, so each layer's appended
// entries anchor to their own declaring file rather than the merge's.
func resolveAppendPaths(a *appendSlices, dir string) {
	for i, d := range a.CatalogDirs {
		a.CatalogDirs[i] = resolveFilesystemPath(dir, d)
	}
	resolvePluginURIs(a.Mutator, dir)
	resolvePluginURIs(a.Policy, dir)
	resolvePluginURIs(a.Linter, dir)
	resolvePluginURIs(a.Importer, dir)
}

// resolvePluginURIs resolves each ref's relative filesystem URI against dir,
// and likewise each of the ref's declared With.Sources entries. Registry
// references (LooksLikeOCIURI) and already-absolute paths are left untouched —
// only a local, declaring-file-relative path is anchored. Source entries are
// always local files (never a registry reference), so they are anchored on the
// absolute/relative test alone. Anchoring against dir here means an append
// layer's sources anchor to their own declaring file, exactly as its URI does.
func resolvePluginURIs(refs []PluginRef, dir string) {
	for i := range refs {
		u := refs[i].URI
		if u != "" && !filepath.IsAbs(u) && !LooksLikeOCIURI(u) {
			if abs, err := filepath.Abs(filepath.Join(dir, u)); err == nil {
				refs[i].URI = abs
			}
		}
		if refs[i].With != nil {
			for j, src := range refs[i].With.Sources {
				refs[i].With.Sources[j] = resolveFilesystemPath(dir, src)
			}
		}
	}
}

// loadGlobalLayer reads the global config layer that sits beneath every
// tree file. When $AGGREGA_CONFIG is set it names an explicit path: the
// path overrides the default location, so a missing file is an error
// rather than a silent fallback. When $AGGREGA_CONFIG is unset the XDG
// default path is auto-loaded — $XDG_CONFIG_HOME/aggrega/config.yaml,
// falling back to ~/.config/aggrega/config.yaml when $XDG_CONFIG_HOME is
// empty — if that file exists; if it does not, there is no global layer
// (the cascade's tree files and Default() suffice). Every environment
// read goes through lookup so resolution is deterministic and
// parallel-safe.
func loadGlobalLayer(lookup func(string) string) (cfg Config, loaded bool, path string, err error) {
	if v := lookup(EnvConfigOverride); v != "" {
		if _, statErr := os.Stat(v); statErr != nil {
			return Config{}, false, "", fmt.Errorf("workspaceconfig: $%s config not found at %s: %w", EnvConfigOverride, v, statErr)
		}
		cfg, err = load(v)
		if err != nil {
			return Config{}, false, "", fmt.Errorf("workspaceconfig: load global %s: %w", v, err)
		}
		return cfg, true, v, nil
	}

	xdgPath, ok := xdgConfigPath(lookup)
	if !ok {
		return Default(), false, "", nil
	}
	if _, statErr := os.Stat(xdgPath); statErr != nil {
		return Default(), false, "", nil
	}
	cfg, err = load(xdgPath)
	if err != nil {
		return Config{}, false, "", fmt.Errorf("workspaceconfig: load global %s: %w", xdgPath, err)
	}
	return cfg, true, xdgPath, nil
}

// xdgConfigPath resolves the XDG default global config path from the
// environment via lookup: $XDG_CONFIG_HOME/aggrega/config.yaml, or
// $HOME/.config/aggrega/config.yaml when $XDG_CONFIG_HOME is empty. It
// reports false when neither variable yields a base directory, so the
// caller treats an unresolvable home as no global layer rather than
// stat'ing a bogus relative path.
func xdgConfigPath(lookup func(string) string) (string, bool) {
	base := lookup("XDG_CONFIG_HOME")
	if base == "" {
		home := lookup("HOME")
		if home == "" {
			return "", false
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "aggrega", "config.yaml"), true
}

// checkUniqueProviderTargets returns a wrapped ErrDuplicateProviderTarget when
// the composed provider list binds one capability more than once. The walk
// is in list order so the error names the first target seen twice, keeping the
// diagnostic deterministic for a given composed list.
func checkUniqueProviderTargets(providers []PluginRef) error {
	seen := make(map[string]struct{}, len(providers))
	for _, ref := range providers {
		if _, dup := seen[ref.Capability]; dup {
			return fmt.Errorf("workspaceconfig: capability %q is bound to more than one provider plugin; keep exactly one plugins.providers entry per capability: %w", ref.Capability, ErrDuplicateProviderTarget)
		}
		seen[ref.Capability] = struct{}{}
	}
	return nil
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

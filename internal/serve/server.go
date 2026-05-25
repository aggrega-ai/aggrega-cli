// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package serve implements the long-running compiler-as-service daemon
// invoked by `aggrega serve`. The daemon speaks JSON-RPC 2.0 over
// stdin/stdout under the `aggrega/` method namespace, holds catalog +
// parse-cache + diagnostic state across requests, and serves an
// IDE-aligned operation set for editor integrations.
//
// The package's contract is the wire protocol, not the Go API — no
// public types outside this package consume serve.Run or the handler
// functions. The only intended consumer is the IDE extension that
// spawns the daemon as a subprocess.
package serve

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// RunConfig carries the I/O streams the daemon reads from and writes
// to. The cobra-bound entry point passes cmd.InOrStdin / OutOrStdout /
// ErrOrStderr; tests inject bytes.Buffer values to drive the dispatch
// loop in-memory without spawning a subprocess.
type RunConfig struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Diagnostic is the wire shape returned by `aggrega/validate` and
// `aggrega/diagnostics`. Line + Column are 1-based per LSP-aligned
// convention; Severity is one of "error" / "warning" / "info".
type Diagnostic struct {
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// Server holds the daemon's per-session state. Constructed once in Run
// after workspace-root discovery + workspace-config load; reused for
// every JSON-RPC request the dispatch loop reads.
//
// No mutex: JSON-RPC over stdin/stdout is sequential. The dispatch loop
// reads one request, dispatches to a handler, writes one response, then
// reads the next. Concurrent access to Server's mutable state never
// occurs by construction.
type Server struct {
	workspaceRoot string
	wsConfig      workspaceconfig.Config

	// lazyCatalog is the per-URI Tier-1/2/3 cascade for Component +
	// Feature URIs. Constructed once at startup via
	// catalog.NewLazyLookup; no per-request reconstruction. The
	// struct carries its own session-scoped hot cache + negative
	// cache; downstream consumers obtain the resolver.CatalogLookup
	// surface via AsCatalogLookup().
	//
	// The hot cache is primed from recipeCatalog the first time
	// resolveRecipeCatalog runs (via lazyCatalog.Prime(recipeCat)),
	// so the first aggrega/irGraph call pays one walk over
	// catalog_dirs (eager catalog.LoadAll inside resolveRecipeCatalog),
	// not two — the lazy lookup's Tier-1 index walk is short-circuited
	// because Prime sets the per-kind built flags.
	lazyCatalog *catalog.LazyLookup

	// recipeCatalog is the eager-on-first-use catalog handle that
	// serves Recipe + FeatureRecipe + SubAggregateLoader. Implements-
	// keyed lookup with three-form version precedence is a separate
	// design problem that the lazy primitive does not address; the
	// eager catalog continues to carry those surfaces. Loaded from
	// wsConfig.Workspace.CatalogDirs (or workspace root fallback)
	// the first time aggrega/irGraph (or another Recipe-needing
	// operation) runs.
	recipeCatalog *catalog.Catalog

	// activeRelease is nil in this scaffold; populated when the
	// multi-Manifest disambiguation cascade ships.
	activeRelease *types.Release

	// lockfile is nil in this scaffold; populated per active Release
	// when the lockfile machinery ships.
	lockfile *lockfile.Lockfile

	parseCache  *cache
	diagnostics map[string][]Diagnostic

	in     *bufio.Reader
	out    io.Writer
	stderr io.Writer
}

// Run discovers the workspace root from the daemon's current working
// directory, loads the workspace config, constructs a Server, and
// enters the JSON-RPC dispatch loop. Returns when stdin reaches EOF or
// an unrecoverable I/O error occurs.
//
// On missing workspace config (no `.aggrega-config.yaml` discoverable
// via parent-directory walk from the CWD), Run returns an error before
// entering the dispatch loop. The cobra entry point surfaces that
// error to the process exit code; the IDE client interprets non-zero
// exit as a "language server failed to start" condition.
func Run(cfg RunConfig) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	root, configPath, err := discoverWorkspaceRoot(cwd)
	if err != nil {
		return fmt.Errorf("workspace root not discoverable from %s: %w", cwd, err)
	}
	wsCfg, err := workspaceconfig.Load(configPath)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	puller := catalog.NewDefaultOCIPuller(wsCfg)
	lazyCatalog := catalog.NewLazyLookup(
		root,
		wsCfg.Workspace.CacheDir,
		wsCfg.Workspace.CatalogDirs,
		puller,
	)
	srv := &Server{
		workspaceRoot: root,
		wsConfig:      wsCfg,
		lazyCatalog:   lazyCatalog,
		parseCache:    newCache(),
		diagnostics:   map[string][]Diagnostic{},
		in:            bufio.NewReader(cfg.Stdin),
		out:           cfg.Stdout,
		stderr:        stderr,
	}
	return srv.dispatchLoop()
}

// discoverWorkspaceRoot walks upward from start looking for a directory
// containing `.aggrega-config.yaml`. Returns the directory + the
// config file's path. Returns os.ErrNotExist when the walk reaches the
// filesystem root without a match.
//
// The walk uses lexical filepath.Dir traversal — same behavior as
// git rev-parse. Symbolic-link edge cases are not specially handled;
// a workspace mounted under a symlink resolves against the symlink's
// lexical parent chain, not the real-path chain.
func discoverWorkspaceRoot(start string) (root, configPath string, err error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", "", err
	}
	dir := abs
	for {
		candidate := filepath.Join(dir, ".aggrega-config.yaml")
		if _, statErr := os.Stat(candidate); statErr == nil {
			return dir, candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", os.ErrNotExist
		}
		dir = parent
	}
}

// resolveRecipeCatalog loads the catalog lazily on first use and
// caches the result in s.recipeCatalog. Subsequent calls reuse the
// cached handle. Used for Recipe + FeatureRecipe + SubAggregateLoader
// surfaces, which the lazy per-URI lookup does not currently support
// (implements-keyed lookup with three-form version precedence is a
// separate design problem).
//
// Loading targets wsConfig.Workspace.CatalogDirs when populated;
// otherwise falls back to a single-directory load against the
// workspace root. Extends resolution runs immediately after load so
// downstream pipeline calls see fully-merged entities.
//
// On load or extends-resolution error, the error is returned and
// s.recipeCatalog stays nil — subsequent calls retry. Deprecation
// and lint warnings produced during load are emitted to s.stderr
// inline.
func (s *Server) resolveRecipeCatalog() (*catalog.Catalog, error) {
	if s.recipeCatalog != nil {
		return s.recipeCatalog, nil
	}
	dirs := s.wsConfig.Workspace.CatalogDirs
	var cat *catalog.Catalog
	var err error
	if len(dirs) == 0 {
		cat, err = catalog.Load(s.workspaceRoot)
	} else {
		cat, err = catalog.LoadAll(dirs...)
	}
	if err != nil {
		return nil, fmt.Errorf("catalog load: %w", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		for _, e := range extErrs {
			fmt.Fprintf(s.stderr, "  extends: %s\n", e)
		}
		return nil, fmt.Errorf("catalog extends: %d resolution error(s)", len(extErrs))
	}
	for _, w := range cat.DeprecationWarnings() {
		fmt.Fprintf(s.stderr, "  deprecation: %s\n", w)
	}
	for _, w := range cat.LintWarnings() {
		fmt.Fprintf(s.stderr, "  %s\n", w)
	}
	s.recipeCatalog = cat
	// Prime the lazy lookup's hot caches from the already-merged
	// entities in the eager catalog. Eliminates the redundant
	// Tier-1 header-walk the lazy lookup would otherwise perform on
	// first Component/Feature access in this session.
	s.lazyCatalog.Prime(cat)
	return cat, nil
}
